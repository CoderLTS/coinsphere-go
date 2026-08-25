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
const latestMigrationVersion = 9

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
	if len(results) != latestMigrationVersion || results[len(results)-1].Version != latestMigrationVersion || results[len(results)-1].Direction != "up" {
		t.Fatalf("migration results = %#v", results)
	}
	if err := runner.ValidateCurrent(context.Background()); err != nil {
		t.Fatalf("validate current schema: %v", err)
	}
	assertCurrentTables(t, database)
	assertTimescaleExtension(t, database)
	assertQuantSchema(t, database)
	assertNotificationSchema(t, database)

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
	if _, err := runner.Up(context.Background(), 1); err != nil {
		t.Fatalf("apply initial migration: %v", err)
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
	if _, err := runner.Up(context.Background(), 2); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO plugin_installations (plugin_id, version, schema_name, source_path) VALUES ('official.test', '1.0.0', 'plugin_official_test', 'installed/official_test')`); err != nil {
		t.Fatalf("insert plugin installation: %v", err)
	}
	if _, err := runner.Down(context.Background(), 1); err == nil {
		t.Fatal("rollback removed plugin lifecycle data")
	}
	current, _, err := runner.Versions(context.Background())
	if err != nil || current != 2 {
		t.Fatalf("failed rollback changed migration version: current=%d err=%v", current, err)
	}
}

func TestWorkflowLifecycleMigrationDownRejectsData(t *testing.T) {
	database := openPostgresSchema(t)
	runner, _ := New(database)
	if _, err := runner.Up(context.Background(), 3); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var userID, workflowID, revisionID int64
	if err = tx.QueryRow(`INSERT INTO users (username) VALUES ('workflow-owner') RETURNING id`).Scan(&userID); err == nil {
		err = tx.QueryRow(`INSERT INTO workflows (name, main_trigger_node_id, created_by) VALUES ('test', 'manual-trigger', $1) RETURNING id`, userID).Scan(&workflowID)
	}
	if err == nil {
		err = tx.QueryRow(`INSERT INTO workflow_revisions (workflow_id, revision_number, graph_json, node_versions, main_trigger_node_id, created_by) VALUES ($1, 1, '{"nodes":[],"edges":[]}', '{}', 'manual-trigger', $2) RETURNING id`, workflowID, userID).Scan(&revisionID)
	}
	if err == nil {
		_, err = tx.Exec(`UPDATE workflows SET active_revision_id = $1 WHERE id = $2`, revisionID, workflowID)
	}
	if err == nil {
		_, err = tx.Exec(`INSERT INTO workflow_runtimes (workflow_id) VALUES ($1)`, workflowID)
	}
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Down(context.Background(), 1); err == nil {
		t.Fatal("rollback removed workflow lifecycle data")
	}
	current, _, err := runner.Versions(context.Background())
	if err != nil || current != 3 {
		t.Fatalf("failed rollback changed migration version: current=%d err=%v", current, err)
	}
}

func TestWorkflowSchemaWorkbenchMigrationDownRejectsRevisions(t *testing.T) {
	database := openPostgresSchema(t)
	runner, _ := New(database)
	if _, err := runner.Up(context.Background(), 4); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var userID, workflowID, revisionID int64
	if err = tx.QueryRow(`INSERT INTO users (username) VALUES ('schema-owner') RETURNING id`).Scan(&userID); err == nil {
		err = tx.QueryRow(`INSERT INTO workflows (name, main_trigger_node_id, created_by) VALUES ('test', 'manual-trigger', $1) RETURNING id`, userID).Scan(&workflowID)
	}
	if err == nil {
		err = tx.QueryRow(`INSERT INTO workflow_revisions (workflow_id, revision_number, graph_json, node_versions, main_trigger_node_id, created_by) VALUES ($1, 1, '{"nodes":[],"edges":[]}', '{}', 'manual-trigger', $2) RETURNING id`, workflowID, userID).Scan(&revisionID)
	}
	if err == nil {
		_, err = tx.Exec(`UPDATE workflows SET active_revision_id = $1 WHERE id = $2`, revisionID, workflowID)
	}
	if err == nil {
		_, err = tx.Exec(`INSERT INTO workflow_runtimes (workflow_id) VALUES ($1)`, workflowID)
	}
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Down(context.Background(), 1); err == nil {
		t.Fatal("rollback accepted workflow revisions")
	}
	current, _, err := runner.Versions(context.Background())
	if err != nil || current != 4 {
		t.Fatalf("failed rollback changed migration version: current=%d err=%v", current, err)
	}
}

func TestWorkflowBatchMigrationDownRejectsBatches(t *testing.T) {
	database := openPostgresSchema(t)
	runner, _ := New(database)
	if _, err := runner.Up(context.Background(), 5); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var userID, workflowID, revisionID int64
	if err = tx.QueryRow(`INSERT INTO users (username) VALUES ('batch-owner') RETURNING id`).Scan(&userID); err == nil {
		err = tx.QueryRow(`INSERT INTO workflows (name, status, main_trigger_node_id, created_by) VALUES ('batch', 'running', 'manual-trigger', $1) RETURNING id`, userID).Scan(&workflowID)
	}
	if err == nil {
		err = tx.QueryRow(`INSERT INTO workflow_revisions (workflow_id, revision_number, graph_json, node_versions, main_trigger_node_id, created_by) VALUES ($1, 1, '{"schemaVersion":1,"nodes":[],"edges":[]}', '{}', 'manual-trigger', $2) RETURNING id`, workflowID, userID).Scan(&revisionID)
	}
	if err == nil {
		_, err = tx.Exec(`UPDATE workflows SET active_revision_id = $1 WHERE id = $2`, revisionID, workflowID)
	}
	if err == nil {
		_, err = tx.Exec(`INSERT INTO workflow_runtimes (workflow_id) VALUES ($1)`, workflowID)
	}
	if err == nil {
		_, err = tx.Exec(`INSERT INTO execution_batches (workflow_id, revision_id, trigger_type, trigger_key, status, triggered_at, created_by) VALUES ($1, $2, 'manual', 'rollback-guard', 'queued', CURRENT_TIMESTAMP, $3)`, workflowID, revisionID, userID)
	}
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Down(context.Background(), 1); err == nil {
		t.Fatal("rollback removed workflow batch data")
	}
	current, _, err := runner.Versions(context.Background())
	if err != nil || current != 5 {
		t.Fatalf("failed rollback changed migration version: current=%d err=%v", current, err)
	}
}

func TestWorkflowHistoryMigrationDownRejectsArtifacts(t *testing.T) {
	database := openPostgresSchema(t)
	runner, _ := New(database)
	if _, err := runner.Up(context.Background(), 6); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	digest := strings.Repeat("a", 64)
	if _, err := database.Exec(`INSERT INTO workflow_artifacts (sha256, media_type, encoding, size_bytes, stored_size_bytes, storage_key) VALUES ($1, 'application/json', 'gzip', 2, 22, $2)`, digest, "aa/"+digest+".gz"); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Down(context.Background(), 1); err == nil {
		t.Fatal("rollback removed workflow artifacts")
	}
	current, _, err := runner.Versions(context.Background())
	if err != nil || current != 6 {
		t.Fatalf("failed rollback changed migration version: current=%d err=%v", current, err)
	}
}

func TestWorkflowEventMigrationDownRejectsEvents(t *testing.T) {
	database := openPostgresSchema(t)
	runner, _ := New(database)
	if _, err := runner.Up(context.Background(), 7); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO workflow_event_records
        (source, event_id, spec_version, event_type, event_time, partition_key, event_json)
        VALUES ('urn:test', 'event-1', '1.0', 'test.event', CURRENT_TIMESTAMP, 'partition-1',
                '{"specversion":"1.0","source":"urn:test","id":"event-1","type":"test.event","time":"2026-01-01T00:00:00Z","partitionkey":"partition-1","data":{}}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Down(context.Background(), 1); err == nil {
		t.Fatal("rollback removed workflow event data")
	}
	current, _, err := runner.Versions(context.Background())
	if err != nil || current != 7 {
		t.Fatalf("failed rollback changed migration version: current=%d err=%v", current, err)
	}
}

func TestQuantMigrationDownRejectsMarketData(t *testing.T) {
	database := openPostgresSchema(t)
	runner, _ := New(database)
	if _, err := runner.Up(context.Background(), 8); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO plugin_quant.instruments
        (market, symbol, base_asset, quote_asset, status, price_tick, quantity_step, min_quantity)
        VALUES ('spot', 'BTCUSDT', 'BTC', 'USDT', 'TRADING', 0.01, 0.00001, 0.00001)`); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Down(context.Background(), 1); err == nil {
		t.Fatal("rollback removed Quant data")
	}
	current, _, err := runner.Versions(context.Background())
	if err != nil || current != 8 {
		t.Fatalf("failed rollback changed migration version: current=%d err=%v", current, err)
	}
}

func TestPaperMigrationDownRejectsData(t *testing.T) {
	database := openPostgresSchema(t)
	runner, _ := New(database)
	if _, err := runner.Up(context.Background(), latestMigrationVersion); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO plugin_notification.deliveries
        (operation_key, workflow_id, revision_id, node_instance_id, channel, subject_key, title, message, status, delivered_at)
        VALUES ('rollback-guard', 1, 1, 'notify', 'in_app', 'guard', 'Guard', 'Keep this fact', 'delivered', CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Down(context.Background(), 1); err == nil {
		t.Fatal("rollback removed Paper-era data")
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
		`INSERT INTO plugin_quant.instruments (market, symbol, base_asset, quote_asset, status, price_tick, quantity_step, min_quantity) VALUES ('live', 'BTCUSDT', 'BTC', 'USDT', 'TRADING', 0.01, 0.001, 0.001)`,
		`INSERT INTO plugin_quant.candles (market, instrument, interval, open_time, close_time, open, high, low, close, volume, source_event_id) VALUES ('spot', 'BTCUSDT', '1h', '2026-01-01T00:00:00Z', '2026-01-01T00:59:59Z', 100, 90, 80, 95, 1, 'invalid-price')`,
	} {
		if _, err := database.Exec(statement); err == nil {
			t.Fatalf("constraint accepted invalid statement: %s", statement)
		}
	}
	assertIndexes(t, database, []string{
		"idx_roles_code", "idx_users_username", "ux_user_role", "idx_menus_name",
		"idx_menu_buttons_permission_code", "ux_role_menu", "ux_role_button", "ux_i18n_biz",
		"ix_audit_records_created_at", "ux_plugin_references_identity", "ix_plugin_references_active",
		"ix_workflows_status_updated", "ux_workflow_revision_number", "ix_workflow_revisions_created",
		"ix_workflow_secret_bindings_workflow",
		"ix_execution_batches_queue", "ix_execution_batches_workflow", "ix_execution_batches_lease",
		"ix_workflow_node_runs_batch", "ix_workflow_node_runs_operation", "ix_workflow_checkpoints_batch",
		"ix_workflow_activities_cursor", "ix_workflow_activities_batch", "ix_workflow_artifact_refs_sha",
		"ix_workflow_event_records_received", "ix_workflow_event_records_partition",
		"ix_execution_batches_partition", "ix_workflow_event_deliveries_workflow",
		"ix_workflow_event_outbox_pending", "ux_workflow_human_task_pending_business",
		"ix_workflow_human_tasks_status",
		"ix_result_views_status", "ix_result_view_user_grants_user", "ix_result_view_role_grants_role",
	})
}

func TestValidateCurrentRejectsDatabaseAhead(t *testing.T) {
	database := openPostgresSchema(t)
	runner, _ := New(database)
	if _, err := runner.Up(context.Background(), 0); err != nil {
		t.Fatalf("apply initial migration: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO schema_migrations (version_id, is_applied) VALUES ($1, TRUE)`, latestMigrationVersion+1); err != nil {
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
		"workflow_revisions", "workflow_runtimes", "workflow_secret_bindings", "workflows",
		"execution_batches", "workflow_node_runs", "workflow_checkpoints", "workflow_node_states",
		"workflow_activities", "workflow_artifacts", "workflow_artifact_refs",
		"workflow_event_records", "workflow_event_deliveries", "workflow_event_outbox", "workflow_human_tasks",
		"result_views", "result_view_user_grants", "result_view_role_grants",
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

func assertQuantSchema(t *testing.T, database *sql.DB) {
	t.Helper()
	for _, table := range []string{
		"instruments", "candles", "backtests", "signals", "paper_accounts", "paper_orders",
		"paper_fills", "paper_fees", "paper_ledger_entries", "paper_positions",
	} {
		var exists bool
		if err := database.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, "plugin_quant."+table).Scan(&exists); err != nil || !exists {
			t.Fatalf("Quant table plugin_quant.%s exists=%t err=%v", table, exists, err)
		}
	}
	var hypertable bool
	if err := database.QueryRow(`SELECT EXISTS (
        SELECT 1 FROM timescaledb_information.hypertables
        WHERE hypertable_schema = 'plugin_quant' AND hypertable_name = 'candles'
    )`).Scan(&hypertable); err != nil || !hypertable {
		t.Fatalf("Quant candles hypertable=%t err=%v", hypertable, err)
	}
	for _, index := range []string{
		"ix_quant_candles_lookup", "ix_quant_backtests_created", "ix_quant_backtests_scope",
		"ux_quant_signal_pending_business", "ix_quant_signals_scope", "ux_quant_paper_account_node",
		"ix_quant_paper_orders_account", "ix_quant_paper_ledger_account",
	} {
		var count int
		if err := database.QueryRow(`SELECT COUNT(*) FROM pg_indexes WHERE schemaname = 'plugin_quant' AND indexname = $1`, index).Scan(&count); err != nil || count != 1 {
			t.Fatalf("Quant index %s count=%d err=%v", index, count, err)
		}
	}
}

func assertNotificationSchema(t *testing.T, database *sql.DB) {
	t.Helper()
	var table, index bool
	if err := database.QueryRow(`SELECT
        to_regclass('plugin_notification.deliveries') IS NOT NULL,
        EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = 'plugin_notification' AND indexname = 'ix_notification_deliveries_status')
    `).Scan(&table, &index); err != nil || !table || !index {
		t.Fatalf("Notification schema table=%t index=%t err=%v", table, index, err)
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
	lock, err := admin.Conn(context.Background())
	if err != nil {
		_ = admin.Close()
		t.Fatalf("reserve PostgreSQL test connection: %v", err)
	}
	if _, err := lock.ExecContext(context.Background(), "SELECT pg_advisory_lock(671908427)"); err != nil {
		_ = lock.Close()
		_ = admin.Close()
		t.Fatalf("lock shared plugin test schema: %v", err)
	}
	schema := fmt.Sprintf("migration_test_%d_%d_%d", os.Getpid(), time.Now().UnixNano(), postgresSchemaSequence.Add(1))
	if _, err := admin.Exec("CREATE SCHEMA " + pgx.Identifier{schema}.Sanitize()); err != nil {
		_, _ = lock.ExecContext(context.Background(), "SELECT pg_advisory_unlock(671908427)")
		_ = lock.Close()
		_ = admin.Close()
		t.Fatalf("create PostgreSQL test schema: %v", err)
	}

	testConfig := config.Copy()
	testConfig.RuntimeParams["search_path"] = schema
	database := stdlib.OpenDB(*testConfig)
	if err := database.Ping(); err != nil {
		_ = database.Close()
		_, _ = admin.Exec("DROP SCHEMA " + pgx.Identifier{schema}.Sanitize() + " CASCADE")
		_, _ = lock.ExecContext(context.Background(), "SELECT pg_advisory_unlock(671908427)")
		_ = lock.Close()
		_ = admin.Close()
		t.Fatalf("ping PostgreSQL test schema: %v", err)
	}
	t.Cleanup(func() {
		_ = database.Close()
		_, _ = admin.Exec("DROP SCHEMA IF EXISTS plugin_quant CASCADE")
		_, _ = admin.Exec("DROP SCHEMA IF EXISTS plugin_notification CASCADE")
		_, _ = admin.Exec("DROP SCHEMA " + pgx.Identifier{schema}.Sanitize() + " CASCADE")
		_, _ = lock.ExecContext(context.Background(), "SELECT pg_advisory_unlock(671908427)")
		_ = lock.Close()
		_ = admin.Close()
	})
	return database
}
