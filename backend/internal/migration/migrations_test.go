package migration

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

const postgresDSNEnv = "COINSPHERE_TEST_POSTGRES_DSN"

var postgresSchemaSequence atomic.Uint64

func TestPostgresBaselineLifecycle(t *testing.T) {
	database := openPostgresSchema(t)
	runner, err := New(database)
	if err != nil {
		t.Fatalf("create migration runner: %v", err)
	}
	if err := runner.ValidateCurrent(context.Background()); err == nil {
		t.Fatal("ValidateCurrent accepted an unmigrated schema")
	}
	var versionTables int
	if err := database.QueryRow(`
SELECT COUNT(*)
FROM information_schema.tables
WHERE table_schema = current_schema() AND table_name = 'schema_migrations'
`).Scan(&versionTables); err != nil {
		t.Fatalf("inspect migration version table: %v", err)
	}
	if versionTables != 0 {
		t.Fatal("ValidateCurrent created the migration version table")
	}

	results, err := runner.Up(context.Background(), 0)
	if err != nil {
		t.Fatalf("apply baseline: %v", err)
	}
	if len(results) != 1 || results[0].Version != 1 || results[0].Direction != "up" {
		t.Fatalf("baseline results = %#v", results)
	}
	if err := runner.ValidateCurrent(context.Background()); err != nil {
		t.Fatalf("validate current baseline: %v", err)
	}
	assertBaselineTables(t, database)

	results, err = runner.Up(context.Background(), 0)
	if err != nil {
		t.Fatalf("repeat baseline: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("repeat baseline applied %#v", results)
	}

	results, err = runner.Down(context.Background(), 1)
	if err != nil {
		t.Fatalf("roll back empty baseline: %v", err)
	}
	if len(results) != 1 || results[0].Version != 1 || results[0].Direction != "down" {
		t.Fatalf("baseline rollback results = %#v", results)
	}
	if err := runner.ValidateCurrent(context.Background()); err == nil {
		t.Fatal("ValidateCurrent accepted a rolled-back baseline")
	}
	if _, err := runner.Up(context.Background(), 0); err != nil {
		t.Fatalf("replay baseline: %v", err)
	}
	assertBaselineTables(t, database)
}

func TestPostgresBaselineDownRejectsData(t *testing.T) {
	database := openPostgresSchema(t)
	runner, err := New(database)
	if err != nil {
		t.Fatalf("create migration runner: %v", err)
	}
	if _, err := runner.Up(context.Background(), 0); err != nil {
		t.Fatalf("apply baseline: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO roles (code) VALUES ('rollback-guard')`); err != nil {
		t.Fatalf("insert rollback guard row: %v", err)
	}

	if _, err := runner.Down(context.Background(), 1); err == nil {
		t.Fatal("baseline rollback removed a non-empty schema")
	}
	if err := runner.ValidateCurrent(context.Background()); err != nil {
		t.Fatalf("failed rollback changed migration version: %v", err)
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM roles WHERE code = 'rollback-guard'`).Scan(&count); err != nil {
		t.Fatalf("read rollback guard row: %v", err)
	}
	if count != 1 {
		t.Fatalf("rollback guard rows = %d, want 1", count)
	}
}

func TestPostgresBaselineStateConstraints(t *testing.T) {
	database := openPostgresSchema(t)
	runner, err := New(database)
	if err != nil {
		t.Fatalf("create migration runner: %v", err)
	}
	if _, err := runner.Up(context.Background(), 0); err != nil {
		t.Fatalf("apply baseline: %v", err)
	}

	invalidRows := []struct {
		name string
		sql  string
	}{
		{"worker unknown status", `INSERT INTO worker_tasks (id, task_type, payload_json, status) VALUES ('worker-invalid-status', 'contract.noop', '{}', 'unknown')`},
		{"worker attempts beyond maximum", `INSERT INTO worker_tasks (id, task_type, payload_json, attempt_count, max_attempts) VALUES ('worker-invalid-attempts', 'contract.noop', '{}', 2, 1)`},
		{"worker active state without lease", `INSERT INTO worker_tasks (id, task_type, payload_json, status) VALUES ('worker-invalid-lease', 'contract.noop', '{}', 'running')`},
		{"worker canceled without cancel time", `INSERT INTO worker_tasks (id, task_type, payload_json, status, finished_at) VALUES ('worker-invalid-cancel', 'contract.noop', '{}', 'canceled', CURRENT_TIMESTAMP)`},
		{"worker terminal state without finish time", `INSERT INTO worker_tasks (id, task_type, payload_json, status) VALUES ('worker-invalid-finish', 'contract.noop', '{}', 'succeeded')`},
		{"outbox unknown status", `INSERT INTO domain_event_outbox (status, attempt_count, available_at) VALUES ('unknown', 0, CURRENT_TIMESTAMP)`},
		{"outbox attempts beyond maximum", `INSERT INTO domain_event_outbox (status, attempt_count, max_attempts, available_at) VALUES ('pending', 4, 3, CURRENT_TIMESTAMP)`},
		{"outbox claimed without lease", `INSERT INTO domain_event_outbox (status, attempt_count, available_at) VALUES ('claimed', 1, CURRENT_TIMESTAMP)`},
		{"outbox pending with lease", `INSERT INTO domain_event_outbox (status, attempt_count, available_at, lease_id, worker_id, claimed_at, lease_expires_at) VALUES ('pending', 1, CURRENT_TIMESTAMP, 'pending-lease', 'dispatcher', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP + INTERVAL '1 minute')`},
		{"outbox terminal state without processed time", `INSERT INTO domain_event_outbox (status, attempt_count, available_at) VALUES ('processed', 1, CURRENT_TIMESTAMP)`},
		{"outbox dead letter before attempts exhausted", `INSERT INTO domain_event_outbox (status, attempt_count, max_attempts, available_at, processed_at, dead_lettered_at) VALUES ('dead_letter', 2, 3, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`},
		{"outbox alert before dead letter", `INSERT INTO domain_event_outbox (status, attempt_count, max_attempts, available_at, processed_at, dead_lettered_at, alerted_at) VALUES ('dead_letter', 3, 3, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP - INTERVAL '1 minute')`},
		{"notification missing outbox reference", `INSERT INTO notification_deliveries (outbox_event_id) VALUES (9223372036854775807)`},
	}
	for _, test := range invalidRows {
		if _, err := database.Exec(test.sql); err == nil {
			t.Fatalf("baseline accepted %s", test.name)
		}
	}

	if _, err := database.Exec(`
INSERT INTO worker_tasks (
    id, task_type, payload_json, status, attempt_count,
    lease_id, worker_id, lease_expires_at, last_heartbeat_at, claimed_at
) VALUES (
    'worker-valid-lease', 'contract.noop', '{}', 'claimed', 1,
    'shared-worker-lease', 'worker-a', CURRENT_TIMESTAMP + INTERVAL '1 minute', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
)
`); err != nil {
		t.Fatalf("insert valid claimed Worker task: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO worker_tasks (
    id, task_type, payload_json, status, attempt_count,
    lease_id, worker_id, lease_expires_at, last_heartbeat_at, claimed_at
) VALUES (
    'worker-duplicate-lease', 'contract.noop', '{}', 'claimed', 1,
    'shared-worker-lease', 'worker-b', CURRENT_TIMESTAMP + INTERVAL '1 minute', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
)
`); err == nil {
		t.Fatal("baseline accepted a duplicate Worker lease")
	}

	if _, err := database.Exec(`
INSERT INTO domain_event_outbox (
    status, attempt_count, available_at, lease_id, worker_id, claimed_at, lease_expires_at
) VALUES (
    'claimed', 1, CURRENT_TIMESTAMP, 'shared-lease', 'dispatcher-a',
    CURRENT_TIMESTAMP, CURRENT_TIMESTAMP + INTERVAL '1 minute'
)
`); err != nil {
		t.Fatalf("insert valid claimed Outbox row: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO domain_event_outbox (
    status, attempt_count, available_at, lease_id, worker_id, claimed_at, lease_expires_at
) VALUES (
    'claimed', 1, CURRENT_TIMESTAMP, 'shared-lease', 'dispatcher-b',
    CURRENT_TIMESTAMP, CURRENT_TIMESTAMP + INTERVAL '1 minute'
)
`); err == nil {
		t.Fatal("baseline accepted a duplicate Outbox lease")
	}

	assertPostgresIndexes(t, database, []string{
		"ix_event_outbox_pending",
		"ix_event_outbox_recovery",
		"ux_event_outbox_lease_id",
		"ix_event_outbox_dead_letter_alert",
		"ix_event_outbox_terminal_retention",
		"ix_worker_tasks_claim",
		"ix_worker_tasks_recovery",
		"ux_worker_tasks_lease_id",
	})
}

func TestPostgresBaselineDownSeesConcurrentCommit(t *testing.T) {
	database := openPostgresSchema(t)
	runner, err := New(database)
	if err != nil {
		t.Fatalf("create migration runner: %v", err)
	}
	if _, err := runner.Up(context.Background(), 0); err != nil {
		t.Fatalf("apply baseline: %v", err)
	}

	writer, err := database.Begin()
	if err != nil {
		t.Fatalf("begin concurrent writer: %v", err)
	}
	defer func() { _ = writer.Rollback() }()
	if _, err := writer.Exec(`INSERT INTO roles (code) VALUES ('concurrent-rollback-guard')`); err != nil {
		t.Fatalf("insert uncommitted rollback guard: %v", err)
	}

	downDone := make(chan error, 1)
	go func() {
		_, err := runner.Down(context.Background(), 1)
		downDone <- err
	}()
	waitForRollbackTableLock(t, database, "roles")
	if err := writer.Commit(); err != nil {
		t.Fatalf("commit concurrent rollback guard: %v", err)
	}

	select {
	case err := <-downDone:
		if err == nil {
			t.Fatal("baseline rollback deleted data committed while rollback was waiting")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("baseline rollback did not finish after concurrent commit")
	}
	if err := runner.ValidateCurrent(context.Background()); err != nil {
		t.Fatalf("failed concurrent rollback changed migration version: %v", err)
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM roles WHERE code = 'concurrent-rollback-guard'`).Scan(&count); err != nil {
		t.Fatalf("read concurrent rollback guard row: %v", err)
	}
	if count != 1 {
		t.Fatalf("concurrent rollback guard rows = %d, want 1", count)
	}
}

func TestValidateCurrentRejectsDatabaseAhead(t *testing.T) {
	database := openPostgresSchema(t)
	runner, err := New(database)
	if err != nil {
		t.Fatalf("create migration runner: %v", err)
	}
	if _, err := runner.Up(context.Background(), 0); err != nil {
		t.Fatalf("apply baseline: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO schema_migrations (version_id, is_applied) VALUES (2, TRUE)`); err != nil {
		t.Fatalf("record newer migration: %v", err)
	}
	if err := runner.ValidateCurrent(context.Background()); err == nil {
		t.Fatal("ValidateCurrent accepted a database newer than the binary")
	}
}

func assertBaselineTables(t *testing.T, database *sql.DB) {
	t.Helper()
	want := []string{
		"ai_model_agent_bindings", "ai_model_configs", "assistant_agents", "assistant_messages",
		"assistant_sessions", "domain_event_outbox", "i18n_texts", "menu_buttons", "menus",
		"news_items", "notification_channels", "notification_deliveries", "refresh_tokens", "role_menu_buttons",
		"role_menus", "roles", "schema_migrations", "task_definition_configs", "user_roles", "users",
		"worker_tasks", "workflow_definitions", "workflow_execution_attempts", "workflow_execution_nodes",
		"workflow_execution_transitions", "workflow_executions", "workflow_runtime_entries", "workflow_runtime_states",
	}
	rows, err := database.Query(`
SELECT table_name
FROM information_schema.tables
WHERE table_schema = current_schema() AND table_type = 'BASE TABLE'
ORDER BY table_name
`)
	if err != nil {
		t.Fatalf("list baseline tables: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatalf("scan baseline table: %v", err)
		}
		got = append(got, table)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate baseline tables: %v", err)
	}
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("baseline tables = %v, want %v", got, want)
	}
}

func waitForRollbackTableLock(t *testing.T, database *sql.DB, table string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		err := database.QueryRow(`
SELECT EXISTS (
    SELECT 1
    FROM pg_locks AS lock
    JOIN pg_class AS relation ON relation.oid = lock.relation
    JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
    WHERE namespace.nspname = current_schema()
      AND relation.relname = $1
      AND lock.mode = 'AccessExclusiveLock'
      AND NOT lock.granted
)
`, table).Scan(&waiting)
		if err != nil {
			t.Fatalf("inspect rollback table lock: %v", err)
		}
		if waiting {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("rollback did not wait for an exclusive lock on %s", table)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func assertPostgresIndexes(t *testing.T, database *sql.DB, names []string) {
	t.Helper()
	for _, name := range names {
		var count int
		if err := database.QueryRow(`
SELECT COUNT(*)
FROM pg_indexes
WHERE schemaname = current_schema() AND indexname = $1
`, name).Scan(&count); err != nil {
			t.Fatalf("inspect PostgreSQL index %s: %v", name, err)
		}
		if count != 1 {
			t.Fatalf("PostgreSQL index %s count = %d, want 1", name, count)
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
		if err := database.Close(); err != nil {
			t.Errorf("close PostgreSQL test schema: %v", err)
		}
		if _, err := admin.Exec("DROP SCHEMA " + pgx.Identifier{schema}.Sanitize() + " CASCADE"); err != nil {
			t.Errorf("drop PostgreSQL test schema: %v", err)
		}
		if err := admin.Close(); err != nil {
			t.Errorf("close PostgreSQL admin database: %v", err)
		}
	})
	return database
}
