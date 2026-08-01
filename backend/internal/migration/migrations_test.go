package migration_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"coinsphere/backend/internal/migration"

	_ "github.com/glebarez/go-sqlite"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

const postgresDSNEnv = "COINSPHERE_TEST_POSTGRES_DSN"

var postgresSchemaSequence atomic.Uint64

type databaseFactory func(t *testing.T) *sql.DB

func TestEmbeddedMigrationsSQLite(t *testing.T) {
	runEmbeddedMigrations(t, "sqlite", openSQLite(t))
}

func TestEmbeddedMigrationsPostgres(t *testing.T) {
	runEmbeddedMigrations(t, "postgres", openPostgresSchema(t, postgresTestDSN(t)))
}

func runEmbeddedMigrations(t *testing.T, driver string, db *sql.DB) {
	t.Helper()
	runner, err := migration.New(db, driver)
	if err != nil {
		t.Fatalf("create embedded migration runner: %v", err)
	}

	ctx := context.Background()
	assertVersions(t, ctx, runner, 0, 1)

	results, err := runner.Up(ctx, 0)
	if err != nil {
		t.Fatalf("upgrade embedded migrations: %v", err)
	}
	if len(results) != 1 || results[0].Version != 1 {
		t.Fatalf("unexpected embedded up results: %+v", results)
	}
	assertVersions(t, ctx, runner, 1, 1)

	results, err = runner.Up(ctx, 0)
	if err != nil {
		t.Fatalf("repeat embedded upgrade: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected idempotent embedded upgrade, got %+v", results)
	}

	rolledBack, err := runner.Down(ctx, 1)
	if err != nil {
		t.Fatalf("rollback embedded migration: %v", err)
	}
	if len(rolledBack) != 1 || rolledBack[0].Version != 1 {
		t.Fatalf("unexpected embedded down results: %+v", rolledBack)
	}
	assertVersions(t, ctx, runner, 0, 1)
}

func TestMigrationContractSQLite(t *testing.T) {
	runMigrationContract(t, "sqlite", openSQLite)
}

func TestMigrationContractPostgres(t *testing.T) {
	dsn := postgresTestDSN(t)
	runMigrationContract(t, "postgres", func(t *testing.T) *sql.DB {
		return openPostgresSchema(t, dsn)
	})
}

func TestNewRejectsUnsupportedDriver(t *testing.T) {
	db := openSQLite(t)
	for _, driver := range []string{"mysql", "oracle"} {
		if _, err := migration.NewWithFS(db, driver, contractMigrations()); err == nil {
			t.Fatalf("expected unsupported driver error for %s", driver)
		}
	}
}

func runMigrationContract(t *testing.T, driver string, open databaseFactory) {
	t.Helper()

	t.Run("empty database upgrades to latest", func(t *testing.T) {
		db := open(t)
		runner := newContractRunner(t, db, driver, contractMigrations())
		ctx := context.Background()

		assertVersions(t, ctx, runner, 0, 3)
		results, err := runner.Up(ctx, 0)
		if err != nil {
			t.Fatalf("upgrade empty database: %v", err)
		}
		if len(results) != 3 {
			t.Fatalf("expected 3 applied migrations, got %+v", results)
		}
		assertVersions(t, ctx, runner, 3, 3)

		var note string
		if err := db.QueryRow("SELECT note FROM migration_items WHERE id = 1").Scan(&note); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("expected migrated table with no rows, got %v", err)
		}
	})

	t.Run("old version upgrades without losing data", func(t *testing.T) {
		db := open(t)
		runner := newContractRunner(t, db, driver, contractMigrations())
		ctx := context.Background()

		results, err := runner.Up(ctx, 1)
		if err != nil {
			t.Fatalf("upgrade to old version: %v", err)
		}
		if len(results) != 1 || results[0].Version != 1 {
			t.Fatalf("unexpected old-version results: %+v", results)
		}
		if _, err := db.Exec("INSERT INTO migration_items (id, name) VALUES (1, 'preserved')"); err != nil {
			t.Fatalf("insert old-version data: %v", err)
		}

		results, err = runner.Up(ctx, 0)
		if err != nil {
			t.Fatalf("upgrade old version to latest: %v", err)
		}
		if len(results) != 2 {
			t.Fatalf("expected 2 remaining migrations, got %+v", results)
		}
		assertVersions(t, ctx, runner, 3, 3)

		var name, note string
		if err := db.QueryRow("SELECT name, note FROM migration_items WHERE id = 1").Scan(&name, &note); err != nil {
			t.Fatalf("read upgraded row: %v", err)
		}
		if name != "preserved" || note != "" {
			t.Fatalf("unexpected upgraded row: name=%q note=%q", name, note)
		}
	})

	t.Run("rollback and reapply", func(t *testing.T) {
		db := open(t)
		runner := newContractRunner(t, db, driver, contractMigrations())
		ctx := context.Background()

		if _, err := runner.Up(ctx, 0); err != nil {
			t.Fatalf("initial upgrade: %v", err)
		}
		if _, err := db.Exec("INSERT INTO migration_items (id, name) VALUES (1, 'duplicate-check')"); err != nil {
			t.Fatalf("insert indexed row: %v", err)
		}
		if _, err := db.Exec("INSERT INTO migration_items (id, name) VALUES (2, 'duplicate-check')"); err == nil {
			t.Fatal("expected unique index to reject duplicate name")
		}

		results, err := runner.Down(ctx, 1)
		if err != nil {
			t.Fatalf("rollback latest migration: %v", err)
		}
		if len(results) != 1 || results[0].Version != 3 {
			t.Fatalf("unexpected rollback results: %+v", results)
		}
		assertVersions(t, ctx, runner, 2, 3)
		if _, err := db.Exec("INSERT INTO migration_items (id, name) VALUES (2, 'duplicate-check')"); err != nil {
			t.Fatalf("duplicate name should succeed after index rollback: %v", err)
		}
		if _, err := db.Exec("DELETE FROM migration_items WHERE id = 2"); err != nil {
			t.Fatalf("remove duplicate before reapplying index: %v", err)
		}

		results, err = runner.Up(ctx, 0)
		if err != nil {
			t.Fatalf("reapply latest migration: %v", err)
		}
		if len(results) != 1 || results[0].Version != 3 {
			t.Fatalf("unexpected reapply results: %+v", results)
		}
		assertVersions(t, ctx, runner, 3, 3)
	})

	t.Run("oversized rollback is rejected before mutation", func(t *testing.T) {
		db := open(t)
		runner := newContractRunner(t, db, driver, contractMigrations())
		ctx := context.Background()

		if _, err := runner.Up(ctx, 2); err != nil {
			t.Fatalf("upgrade before rollback preflight: %v", err)
		}
		if _, err := runner.Down(ctx, 3); err == nil {
			t.Fatal("expected oversized rollback to fail")
		}
		assertVersions(t, ctx, runner, 2, 3)
		if _, err := db.Exec("INSERT INTO migration_items (id, name, note) VALUES (1, 'still-present', 'yes')"); err != nil {
			t.Fatalf("oversized rollback changed schema: %v", err)
		}
	})

	t.Run("invalid upgrade target is rejected", func(t *testing.T) {
		db := open(t)
		runner := newContractRunner(t, db, driver, contractMigrations())
		ctx := context.Background()

		if _, err := runner.Up(ctx, 2); err != nil {
			t.Fatalf("upgrade to version 2: %v", err)
		}
		if _, err := runner.Up(ctx, 1); err == nil {
			t.Fatal("expected target below current version to fail")
		}
		if _, err := runner.Up(ctx, 4); err == nil {
			t.Fatal("expected target above latest version to fail")
		}
		assertVersions(t, ctx, runner, 2, 3)
	})

	t.Run("older binary rejects newer database", func(t *testing.T) {
		db := open(t)
		latestRunner := newContractRunner(t, db, driver, contractMigrations())
		ctx := context.Background()

		if _, err := latestRunner.Up(ctx, 0); err != nil {
			t.Fatalf("upgrade with latest migrations: %v", err)
		}
		olderRunner := newContractRunner(t, db, driver, oldBinaryMigrations())
		assertVersions(t, ctx, olderRunner, 3, 2)
		if _, err := olderRunner.Up(ctx, 0); err == nil {
			t.Fatal("expected old binary up to reject newer database")
		}
		if _, err := olderRunner.Down(ctx, 1); err == nil {
			t.Fatal("expected old binary down to reject newer database")
		}
		if _, err := olderRunner.Status(ctx); err == nil {
			t.Fatal("expected old binary status to reject incomplete migration sources")
		}
		assertVersions(t, ctx, latestRunner, 3, 3)
	})

	t.Run("repeated upgrade is idempotent", func(t *testing.T) {
		db := open(t)
		runner := newContractRunner(t, db, driver, contractMigrations())
		ctx := context.Background()

		if _, err := runner.Up(ctx, 0); err != nil {
			t.Fatalf("initial upgrade: %v", err)
		}
		if _, err := db.Exec("INSERT INTO migration_items (id, name, note) VALUES (1, 'once', 'keep')"); err != nil {
			t.Fatalf("insert data before repeated upgrade: %v", err)
		}
		results, err := runner.Up(ctx, 0)
		if err != nil {
			t.Fatalf("repeat upgrade: %v", err)
		}
		if len(results) != 0 {
			t.Fatalf("expected no migrations on repeated upgrade, got %+v", results)
		}

		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM migration_items WHERE name = 'once' AND note = 'keep'").Scan(&count); err != nil {
			t.Fatalf("count preserved rows: %v", err)
		}
		if count != 1 {
			t.Fatalf("expected one preserved row, got %d", count)
		}
	})

	t.Run("failed migration is atomic", func(t *testing.T) {
		db := open(t)
		runner := newContractRunner(t, db, driver, failingMigrations())
		ctx := context.Background()

		results, err := runner.Up(ctx, 0)
		if err == nil {
			t.Fatal("expected second migration to fail")
		}
		if len(results) != 1 || results[0].Version != 1 {
			t.Fatalf("expected first successful migration to be reported, got %+v", results)
		}
		assertVersions(t, ctx, runner, 1, 2)

		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM migration_partial").Scan(&count); err == nil {
			t.Fatal("failed migration left a partial table behind")
		}
	})
}

func TestDownReportsCommittedStepsBeforeFailure(t *testing.T) {
	db := openSQLite(t)
	runner := newContractRunner(t, db, "sqlite", failingDownMigrations())
	ctx := context.Background()

	if _, err := runner.Up(ctx, 0); err != nil {
		t.Fatalf("apply failing-down fixture: %v", err)
	}
	results, err := runner.Down(ctx, 2)
	if err == nil {
		t.Fatal("expected second down migration to fail")
	}
	if len(results) != 1 || results[0].Version != 3 {
		t.Fatalf("expected committed version 3 rollback to be reported, got %+v", results)
	}
	assertVersions(t, ctx, runner, 2, 3)
}

func newContractRunner(t *testing.T, db *sql.DB, driver string, migrations fstest.MapFS) *migration.Runner {
	t.Helper()
	runner, err := migration.NewWithFS(db, driver, migrations)
	if err != nil {
		t.Fatalf("create migration runner: %v", err)
	}
	return runner
}

func assertVersions(t *testing.T, ctx context.Context, runner *migration.Runner, wantCurrent, wantLatest int64) {
	t.Helper()
	current, latest, err := runner.Versions(ctx)
	if err != nil {
		t.Fatalf("read versions: %v", err)
	}
	if current != wantCurrent || latest != wantLatest {
		t.Fatalf("unexpected versions: current=%d latest=%d, want current=%d latest=%d", current, latest, wantCurrent, wantLatest)
	}
}

func openSQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "migration-test.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		t.Fatalf("ping sqlite: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close sqlite: %v", err)
		}
	})
	return db
}

func postgresTestDSN(t *testing.T) string {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv(postgresDSNEnv))
	if dsn == "" {
		t.Skipf("%s is not configured", postgresDSNEnv)
	}
	return dsn
}

func openPostgresSchema(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	adminConfig, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse postgres test DSN: %v", err)
	}
	adminDB := stdlib.OpenDB(*adminConfig)
	if err := adminDB.Ping(); err != nil {
		adminDB.Close()
		t.Fatalf("ping postgres: %v", err)
	}

	schema := fmt.Sprintf("migration_test_%d_%d_%d", os.Getpid(), time.Now().UnixNano(), postgresSchemaSequence.Add(1))
	quotedSchema := quoteIdentifier(schema)
	if _, err := adminDB.Exec("CREATE SCHEMA " + quotedSchema); err != nil {
		adminDB.Close()
		t.Fatalf("create postgres test schema: %v", err)
	}

	testConfig := adminConfig.Copy()
	if testConfig.RuntimeParams == nil {
		testConfig.RuntimeParams = make(map[string]string)
	}
	testConfig.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*testConfig)
	db.SetMaxOpenConns(4)
	if err := db.Ping(); err != nil {
		db.Close()
		_, _ = adminDB.Exec("DROP SCHEMA " + quotedSchema + " CASCADE")
		adminDB.Close()
		t.Fatalf("ping postgres test schema: %v", err)
	}

	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close postgres test database: %v", err)
		}
		if _, err := adminDB.Exec("DROP SCHEMA " + quotedSchema + " CASCADE"); err != nil {
			t.Errorf("drop postgres test schema: %v", err)
		}
		if err := adminDB.Close(); err != nil {
			t.Errorf("close postgres admin database: %v", err)
		}
	})
	return db
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func contractMigrations() fstest.MapFS {
	return fstest.MapFS{
		"00001_create_items.sql": &fstest.MapFile{Data: []byte(`-- +goose Up
CREATE TABLE migration_items (
    id BIGINT PRIMARY KEY,
    name VARCHAR(100) NOT NULL
);

-- +goose Down
DROP TABLE migration_items;
`)},
		"00002_add_note.sql": &fstest.MapFile{Data: []byte(`-- +goose Up
ALTER TABLE migration_items ADD COLUMN note TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE migration_items DROP COLUMN note;
`)},
		"00003_unique_name.sql": &fstest.MapFile{Data: []byte(`-- +goose Up
CREATE UNIQUE INDEX ux_migration_items_name ON migration_items (name);

-- +goose Down
DROP INDEX ux_migration_items_name;
`)},
	}
}

func oldBinaryMigrations() fstest.MapFS {
	migrations := contractMigrations()
	delete(migrations, "00003_unique_name.sql")
	return migrations
}

func failingMigrations() fstest.MapFS {
	return fstest.MapFS{
		"00001_create_base.sql": &fstest.MapFile{Data: []byte(`-- +goose Up
CREATE TABLE migration_base (id BIGINT PRIMARY KEY);

-- +goose Down
DROP TABLE migration_base;
`)},
		"00002_fail_atomically.sql": &fstest.MapFile{Data: []byte(`-- +goose Up
CREATE TABLE migration_partial (id BIGINT PRIMARY KEY);
INSERT INTO migration_table_that_does_not_exist (id) VALUES (1);

-- +goose Down
DROP TABLE migration_partial;
`)},
	}
}

func failingDownMigrations() fstest.MapFS {
	return fstest.MapFS{
		"00001_create_base.sql": &fstest.MapFile{Data: []byte(`-- +goose Up
CREATE TABLE migration_base (id BIGINT PRIMARY KEY);

-- +goose Down
DROP TABLE migration_base;
`)},
		"00002_create_middle.sql": &fstest.MapFile{Data: []byte(`-- +goose Up
CREATE TABLE migration_middle (id BIGINT PRIMARY KEY);

-- +goose Down
DROP TABLE migration_middle;
INSERT INTO migration_table_that_does_not_exist (id) VALUES (1);
`)},
		"00003_create_latest.sql": &fstest.MapFile{Data: []byte(`-- +goose Up
CREATE TABLE migration_latest (id BIGINT PRIMARY KEY);

-- +goose Down
DROP TABLE migration_latest;
`)},
	}
}
