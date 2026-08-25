package migration

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

const postgresDSNEnv = "COINSPHERE_TEST_POSTGRES_DSN"
const latestMigrationVersion = 2

var postgresSchemaSequence atomic.Uint64

func TestInitialMigrationLifecycle(t *testing.T) {
	database := openPostgresSchema(t)
	runner, err := New(database)
	if err != nil {
		t.Fatalf("create migration runner: %v", err)
	}
	if err := runner.ValidateCurrent(context.Background()); err == nil {
		t.Fatal("ValidateCurrent accepted an empty schema")
	}

	results, err := runner.Up(context.Background(), 0)
	if err != nil {
		t.Fatalf("apply initial migration: %v", err)
	}
	if len(results) != 2 || results[len(results)-1].Version != latestMigrationVersion || results[len(results)-1].Direction != "up" {
		t.Fatalf("migration results = %#v", results)
	}
	if err := runner.ValidateCurrent(context.Background()); err != nil {
		t.Fatalf("validate current schema: %v", err)
	}
	assertCurrentTables(t, database)
	assertTimescaleExtension(t, database)

	if results, err = runner.Up(context.Background(), 0); err != nil || len(results) != 0 {
		t.Fatalf("repeat migration results=%#v err=%v", results, err)
	}
	if results, err = runner.Down(context.Background(), 1); err != nil || len(results) != 1 || results[0].Direction != "down" {
		t.Fatalf("rollback results=%#v err=%v", results, err)
	}
	if _, err := runner.Up(context.Background(), 0); err != nil {
		t.Fatalf("reapply initial migration: %v", err)
	}
}

func TestInitialMigrationDownRejectsData(t *testing.T) {
	database := openPostgresSchema(t)
	runner, _ := New(database)
	if _, err := runner.Up(context.Background(), 0); err != nil {
		t.Fatalf("apply initial migration: %v", err)
	}
	if _, err := runner.Down(context.Background(), 1); err != nil {
		t.Fatalf("roll back plugin lifecycle migration: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO roles (code) VALUES ('rollback-guard')`); err != nil {
		t.Fatalf("insert rollback guard: %v", err)
	}
	if _, err := runner.Down(context.Background(), 1); err == nil {
		t.Fatal("rollback removed a non-empty schema")
	}
	current, _, err := runner.Versions(context.Background())
	if err != nil || current != 1 {
		t.Fatalf("failed rollback changed migration version: current=%d err=%v", current, err)
	}
}

func TestPluginLifecycleMigrationDownRejectsData(t *testing.T) {
	database := openPostgresSchema(t)
	runner, _ := New(database)
	if _, err := runner.Up(context.Background(), 0); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO plugin_installations (plugin_id, version, schema_name, source_path) VALUES ('official.test', '1.0.0', 'plugin_official_test', 'installed/official_test')`); err != nil {
		t.Fatalf("insert plugin installation: %v", err)
	}
	if _, err := runner.Down(context.Background(), 1); err == nil {
		t.Fatal("rollback removed plugin lifecycle data")
	}
	current, _, err := runner.Versions(context.Background())
	if err != nil || current != latestMigrationVersion {
		t.Fatalf("failed rollback changed migration version: current=%d err=%v", current, err)
	}
}

func TestPluginMigrationUsesIndependentSchemaAndLedger(t *testing.T) {
	database := openPostgresSchema(t)
	pluginID := fmt.Sprintf("official.test%d", postgresSchemaSequence.Add(1))
	schema, err := PluginSchemaName(pluginID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = RemovePluginSchema(context.Background(), database, pluginID) })
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "00001_create_records.sql"), []byte("-- +goose Up\nCREATE TABLE records (id BIGINT PRIMARY KEY);\n-- +goose Down\nDROP TABLE records;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WithPluginMigrations(context.Background(), database, pluginID, directory, func(runner *Runner) error {
		results, err := runner.Up(context.Background(), 0)
		if err != nil {
			return err
		}
		if len(results) != 1 || results[0].Version != 1 {
			return fmt.Errorf("unexpected plugin migration results: %#v", results)
		}
		return nil
	}); err != nil {
		t.Fatalf("apply plugin migration: %v", err)
	}
	for _, table := range []string{"records", "schema_migrations"} {
		var exists bool
		if err := database.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, schema+"."+table).Scan(&exists); err != nil || !exists {
			t.Fatalf("plugin table %s.%s exists=%t err=%v", schema, table, exists, err)
		}
	}
}

func TestInitialMigrationConstraintsAndIndexes(t *testing.T) {
	database := openPostgresSchema(t)
	runner, _ := New(database)
	if _, err := runner.Up(context.Background(), 0); err != nil {
		t.Fatalf("apply initial migration: %v", err)
	}
	for _, statement := range []string{
		`INSERT INTO roles (code) VALUES ('')`,
		`INSERT INTO users (username) VALUES ('')`,
		`INSERT INTO audit_records (request_id, action, resource_path, outcome, status_code) VALUES ('bad id', 'test', '/', 'success', 200)`,
		`INSERT INTO audit_records (request_id, action, resource_path, outcome, status_code) VALUES ('valid-id', 'test', '/', 'unknown', 200)`,
	} {
		if _, err := database.Exec(statement); err == nil {
			t.Fatalf("constraint accepted invalid statement: %s", statement)
		}
	}
	assertIndexes(t, database, []string{
		"idx_roles_code", "idx_users_username", "ux_user_role", "idx_menus_name",
		"idx_menu_buttons_permission_code", "ux_role_menu", "ux_role_button", "ux_i18n_biz",
		"ix_audit_records_created_at", "ux_plugin_references_identity", "ix_plugin_references_active",
	})
}

func TestValidateCurrentRejectsDatabaseAhead(t *testing.T) {
	database := openPostgresSchema(t)
	runner, _ := New(database)
	if _, err := runner.Up(context.Background(), 0); err != nil {
		t.Fatalf("apply initial migration: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO schema_migrations (version_id, is_applied) VALUES (3, TRUE)`); err != nil {
		t.Fatalf("record newer migration: %v", err)
	}
	if err := runner.ValidateCurrent(context.Background()); err == nil {
		t.Fatal("ValidateCurrent accepted a database ahead of the binary")
	}
}

func assertCurrentTables(t *testing.T, database *sql.DB) {
	t.Helper()
	want := []string{
		"audit_records", "i18n_texts", "menu_buttons", "menus", "role_menu_buttons",
		"plugin_installations", "plugin_references", "role_menus", "roles", "schema_migrations", "user_roles", "users",
	}
	rows, err := database.Query(`
SELECT table_name
FROM information_schema.tables
WHERE table_schema = current_schema() AND table_type = 'BASE TABLE'
ORDER BY table_name
`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatalf("scan table: %v", err)
		}
		got = append(got, table)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate tables: %v", err)
	}
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("tables = %v, want %v", got, want)
	}
}

func assertTimescaleExtension(t *testing.T, database *sql.DB) {
	t.Helper()
	var extension, hypertables bool
	if err := database.QueryRow(`
SELECT
    EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'timescaledb'),
    EXISTS (SELECT 1 FROM timescaledb_information.hypertables WHERE hypertable_schema = current_schema())
`).Scan(&extension, &hypertables); err != nil {
		t.Fatalf("inspect TimescaleDB extension: %v", err)
	}
	if !extension || hypertables {
		t.Fatalf("TimescaleDB baseline = extension:%t hypertables:%t", extension, hypertables)
	}
}

func assertIndexes(t *testing.T, database *sql.DB, names []string) {
	t.Helper()
	for _, name := range names {
		var count int
		if err := database.QueryRow(`SELECT COUNT(*) FROM pg_indexes WHERE schemaname = current_schema() AND indexname = $1`, name).Scan(&count); err != nil {
			t.Fatalf("inspect index %s: %v", name, err)
		}
		if count != 1 {
			t.Fatalf("index %s count = %d, want 1", name, count)
		}
	}
}

func openPostgresSchema(t *testing.T) *sql.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv(postgresDSNEnv))
	if dsn == "" {
		if os.Getenv("CI") != "" {
			t.Fatalf("%s is required in CI", postgresDSNEnv)
		}
		t.Skipf("%s is not configured", postgresDSNEnv)
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse PostgreSQL test DSN: %v", err)
	}
	admin := stdlib.OpenDB(*config)
	if err := admin.Ping(); err != nil {
		_ = admin.Close()
		t.Fatalf("ping PostgreSQL test database: %v", err)
	}
	schema := fmt.Sprintf("migration_test_%d_%d_%d", os.Getpid(), time.Now().UnixNano(), postgresSchemaSequence.Add(1))
	if _, err := admin.Exec("CREATE SCHEMA " + pgx.Identifier{schema}.Sanitize()); err != nil {
		_ = admin.Close()
		t.Fatalf("create PostgreSQL test schema: %v", err)
	}

	testConfig := config.Copy()
	testConfig.RuntimeParams["search_path"] = schema
	database := stdlib.OpenDB(*testConfig)
	if err := database.Ping(); err != nil {
		_ = database.Close()
		_, _ = admin.Exec("DROP SCHEMA " + pgx.Identifier{schema}.Sanitize() + " CASCADE")
		_ = admin.Close()
		t.Fatalf("ping PostgreSQL test schema: %v", err)
	}
	t.Cleanup(func() {
		_ = database.Close()
		_, _ = admin.Exec("DROP SCHEMA " + pgx.Identifier{schema}.Sanitize() + " CASCADE")
		_ = admin.Close()
	})
	return database
}
