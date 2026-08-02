package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunMigratesWithoutAutoMigrate(t *testing.T) {
	tempDir := t.TempDir()
	databasePath := filepath.Join(tempDir, "command.db")
	configPath := filepath.Join(tempDir, "config.yml")
	configYAML := fmt.Sprintf("database:\n  driver: sqlite\n  path: %q\n", filepath.ToSlash(databasePath))
	if err := os.WriteFile(configPath, []byte(configYAML), 0o600); err != nil {
		t.Fatalf("write test config: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"-config", configPath, "-direction", "up"}, &stdout, &stderr); err != nil {
		t.Fatalf("run migration command: %v\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "current=3 latest=3 applied=3") {
		t.Fatalf("unexpected command output: %s", stdout.String())
	}

	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("open migrated database: %v", err)
	}

	var migrationTable string
	if err := db.QueryRow("SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'").Scan(&migrationTable); err != nil {
		t.Fatalf("migration table was not created: %v", err)
	}
	var businessTable string
	if err := db.QueryRow("SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'system_users'").Scan(&businessTable); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("migration command unexpectedly ran AutoMigrate: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close migrated database: %v", err)
	}

	stdout.Reset()
	if err := run(context.Background(), []string{"-config", configPath, "-direction", "status"}, &stdout, &stderr); err != nil {
		t.Fatalf("read migration status: %v", err)
	}
	if !strings.Contains(stdout.String(), "00001\tapplied") {
		t.Fatalf("unexpected status output: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "00002\tapplied") {
		t.Fatalf("unexpected status output: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "00003\tapplied") {
		t.Fatalf("unexpected status output: %s", stdout.String())
	}

	stdout.Reset()
	if err := run(context.Background(), []string{"-config", configPath, "-direction", "version"}, &stdout, &stderr); err != nil {
		t.Fatalf("read migration version: %v", err)
	}
	if !strings.Contains(stdout.String(), "current=3 latest=3") {
		t.Fatalf("unexpected version output: %s", stdout.String())
	}

	stdout.Reset()
	if err := run(context.Background(), []string{"-config", configPath, "-direction", "up"}, &stdout, &stderr); err != nil {
		t.Fatalf("repeat migration command: %v", err)
	}
	if !strings.Contains(stdout.String(), "applied=0") {
		t.Fatalf("expected idempotent command output, got %s", stdout.String())
	}

	stdout.Reset()
	if err := run(context.Background(), []string{"-config", configPath, "-direction", "down", "-steps", "1"}, &stdout, &stderr); err != nil {
		t.Fatalf("run down migration command: %v", err)
	}
	if !strings.Contains(stdout.String(), "current=2 latest=3 rolled_back=1") {
		t.Fatalf("unexpected down output: %s", stdout.String())
	}

	// 00003 Down 只撤销可靠投递增量，保留旧兼容 Outbox 表，避免 schema 回滚误删事件容器。
	db, err = sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("reopen rolled back database: %v", err)
	}
	if err := db.QueryRow("SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'domain_event_outbox'").Scan(&businessTable); err != nil {
		t.Fatalf("outbox base table was removed by migration Down: %v", err)
	}
	if err := db.QueryRow("SELECT max_attempts FROM domain_event_outbox LIMIT 1").Scan(new(int)); err == nil || !strings.Contains(strings.ToLower(err.Error()), "no such column") {
		t.Fatalf("00003 columns still exist after Down: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close rolled back database: %v", err)
	}
}

func TestParseOptions(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "up defaults", args: nil},
		{name: "down steps", args: []string{"-direction", "down", "-steps", "2"}},
		{name: "targeted up", args: []string{"-direction", "up", "-target", "3"}},
		{name: "invalid direction", args: []string{"-direction", "sideways"}, wantErr: "unsupported direction"},
		{name: "invalid steps", args: []string{"-direction", "down", "-steps", "0"}, wantErr: "steps must be at least one"},
		{name: "target with status", args: []string{"-direction", "status", "-target", "1"}, wantErr: "target is only valid"},
		{name: "invalid timeout", args: []string{"-timeout", "0s"}, wantErr: "timeout must be greater than zero"},
		{name: "positional argument", args: []string{"up"}, wantErr: "unexpected positional arguments"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opts, err := parseOptions(test.args, &bytes.Buffer{})
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("parse options: %v", err)
				}
				if opts.timeout != 5*time.Minute {
					t.Fatalf("unexpected default timeout: %s", opts.timeout)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("expected error containing %q, got %v", test.wantErr, err)
			}
		})
	}
}
