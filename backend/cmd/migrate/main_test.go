package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestRunAppliesPostgresBaselineWithoutAutoMigrate(t *testing.T) {
	dsn, database := migrationCommandDatabase(t)
	configPath := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf("database:\n  dsn: %q\n", dsn)), 0o600); err != nil {
		t.Fatalf("write migration config: %v", err)
	}

	var output bytes.Buffer
	if err := run(context.Background(), []string{"-config", configPath, "-direction", "up"}, &output, &output); err != nil {
		t.Fatalf("run migration up: %v", err)
	}
	if !strings.Contains(output.String(), "current=2 latest=2 applied=2") {
		t.Fatalf("migration output = %q", output.String())
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = 'users'`).Scan(&count); err != nil {
		t.Fatalf("inspect baseline table: %v", err)
	}
	if count != 1 {
		t.Fatalf("users table count = %d, want 1", count)
	}
}

func TestParseOptionsRejectsInvalidArguments(t *testing.T) {
	for _, args := range [][]string{
		{"-direction", "sideways"},
		{"-direction", "down", "-steps", "0"},
		{"-direction", "version", "-target", "1"},
		{"-timeout", "0s"},
		{"unexpected"},
	} {
		if _, err := parseOptions(args, &bytes.Buffer{}); err == nil {
			t.Fatalf("parseOptions(%v) succeeded", args)
		}
	}
}

func migrationCommandDatabase(t *testing.T) (string, *sql.DB) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("COINSPHERE_TEST_POSTGRES_DSN"))
	if dsn == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("COINSPHERE_TEST_POSTGRES_DSN is required in CI")
		}
		t.Skip("COINSPHERE_TEST_POSTGRES_DSN is not configured")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse PostgreSQL test DSN: %v", err)
	}
	admin := stdlib.OpenDB(*config)
	schema := fmt.Sprintf("migrate_command_test_%d_%d", os.Getpid(), time.Now().UnixNano())
	if _, err := admin.Exec("CREATE SCHEMA " + pgx.Identifier{schema}.Sanitize()); err != nil {
		_ = admin.Close()
		t.Fatalf("create migration command schema: %v", err)
	}
	testConfig := config.Copy()
	if testConfig.RuntimeParams == nil {
		testConfig.RuntimeParams = make(map[string]string)
	}
	testConfig.RuntimeParams["search_path"] = schema
	database := stdlib.OpenDB(*testConfig)
	if err := database.Ping(); err != nil {
		_ = database.Close()
		_, _ = admin.Exec("DROP SCHEMA " + pgx.Identifier{schema}.Sanitize() + " CASCADE")
		_ = admin.Close()
		t.Fatalf("ping migration command schema: %v", err)
	}
	t.Cleanup(func() {
		_ = database.Close()
		_, _ = admin.Exec("DROP SCHEMA " + pgx.Identifier{schema}.Sanitize() + " CASCADE")
		_ = admin.Close()
	})
	return postgresURLWithSearchPath(t, dsn, schema), database
}

func postgresURLWithSearchPath(t *testing.T, dsn, schema string) string {
	t.Helper()
	parsed, err := url.Parse(dsn)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		t.Fatalf("PostgreSQL test DSN must be a postgres:// or postgresql:// URL")
	}
	query := parsed.Query()
	query.Set("options", "-csearch_path="+schema)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
