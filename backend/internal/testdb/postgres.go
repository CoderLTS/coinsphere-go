// Package testdb provides isolated synthetic PostgreSQL databases for CI.
package testdb

import (
	"context"
	"crypto/rand"
	"database/sql"
	"net/url"
	"os"
	"strings"
	"testing"

	"coinsphere/backend/internal/migration"
	officialmigrations "coinsphere/backend/plugin/official/migrations"
	"coinsphere/backend/version"
	_ "github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func Open(t *testing.T, baseline bool) (*sql.DB, *gorm.DB) {
	t.Helper()
	dsn := os.Getenv("COINSPHERE_TEST_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL CI service is not configured")
	}
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme != "postgres" {
		t.Fatal("synthetic test DSN must be a postgres URL")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal("cannot open test database administrator")
	}
	name := "coinsphere_ci_" + strings.ToLower(rand.Text())
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
		admin.Close()
		t.Fatal("cannot create isolated test database")
	}
	parsed.Path = "/" + name
	database, err := sql.Open("pgx", parsed.String())
	if err != nil {
		t.Fatal("cannot open isolated database")
	}
	t.Cleanup(func() {
		database.Close()
		_, err := admin.Exec("DROP DATABASE " + name + " WITH (FORCE)")
		admin.Close()
		if err != nil {
			t.Error("cannot clean isolated synthetic database")
		}
	})
	gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: database}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("cannot open isolated GORM database")
	}
	if baseline {
		runner, err := migration.New(database)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := runner.Up(context.Background(), 0); err != nil {
			t.Fatal(err)
		}
		for _, bundle := range officialmigrations.Bundles() {
			if _, err := database.Exec("CREATE SCHEMA IF NOT EXISTS " + bundle.Schema); err != nil {
				t.Fatal(err)
			}
			runner, err := migration.NewPluginBaseline(database, bundle.Files, bundle.Schema)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := runner.Up(context.Background(), 0); err != nil {
				t.Fatal(err)
			}
		}
		for _, p := range version.BuiltinCatalog {
			schema, err := migration.PluginSchemaName(p.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, b := range officialmigrations.Bundles() {
				if b.ID == p.ID {
					schema = b.Schema
				}
			}
			if _, err := database.Exec("INSERT INTO plugin_installations(plugin_id,version,schema_name,source_path,status) VALUES($1,$2,$3,'builtin','installed')", p.ID, p.Version, schema); err != nil {
				t.Fatal(err)
			}
		}
	}
	return database, gdb
}
