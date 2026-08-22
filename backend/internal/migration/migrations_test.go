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
const latestMigrationVersion = 4

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
	assertTimescaleLifecycle(t, database)

	results, err = runner.Up(context.Background(), 0)
	if err != nil || len(results) != 0 {
		t.Fatalf("repeat migration results=%#v err=%v", results, err)
	}
	results, err = runner.Down(context.Background(), latestMigrationVersion)
	if err != nil {
		t.Fatalf("roll back empty schema: %v", err)
	}
	if len(results) != latestMigrationVersion || results[0].Version != latestMigrationVersion || results[0].Direction != "down" {
		t.Fatalf("rollback results = %#v", results)
	}
	if _, err := runner.Up(context.Background(), 0); err != nil {
		t.Fatalf("reapply initial migration: %v", err)
	}
}

func TestInitialMigrationDownRejectsData(t *testing.T) {
	database := openPostgresSchema(t)
	runner, err := New(database)
	if err != nil {
		t.Fatalf("create migration runner: %v", err)
	}
	if _, err := runner.Up(context.Background(), 0); err != nil {
		t.Fatalf("apply initial migration: %v", err)
	}
	if _, err := runner.Down(context.Background(), 3); err != nil {
		t.Fatalf("roll back post-baseline migrations: %v", err)
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

func TestEndpointMigrationDownRejectsChangedSettings(t *testing.T) {
	database := openPostgresSchema(t)
	runner, err := New(database)
	if err != nil {
		t.Fatalf("create migration runner: %v", err)
	}
	if _, err := runner.Up(context.Background(), 0); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	if _, err := runner.Down(context.Background(), 2); err != nil {
		t.Fatalf("roll back workflow console and node definition migrations: %v", err)
	}
	if _, err := database.Exec(`UPDATE market_sync_settings SET spot_rest_base_url = 'https://api.binance.com' WHERE id = 1`); err != nil {
		t.Fatalf("change sync setting: %v", err)
	}
	if _, err := runner.Down(context.Background(), 1); err == nil {
		t.Fatal("endpoint migration rollback removed changed settings")
	}
	current, _, err := runner.Versions(context.Background())
	if err != nil || current != 2 {
		t.Fatalf("failed endpoint rollback changed migration version: current=%d err=%v", current, err)
	}
}

func TestWorkflowConsoleMigrationAllowsOnlyPristinePaperBalance(t *testing.T) {
	for _, test := range []struct {
		name        string
		fees        string
		wantVersion int64
		wantError   bool
	}{
		{name: "pristine balance", fees: "0", wantVersion: 4},
		{name: "changed balance", fees: "1", wantVersion: 3, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			database := openPostgresSchema(t)
			runner, err := New(database)
			if err != nil {
				t.Fatalf("create migration runner: %v", err)
			}
			if _, err := runner.Up(context.Background(), 3); err != nil {
				t.Fatalf("apply pre-console migrations: %v", err)
			}

			var ownerID, idempotencyID int64
			if err := database.QueryRow(`INSERT INTO users (username) VALUES ('migration-paper-owner') RETURNING id`).Scan(&ownerID); err != nil {
				t.Fatalf("insert Paper account owner: %v", err)
			}
			if err := database.QueryRow(`
INSERT INTO idempotency_records (user_id, scope, key_hash, request_hash, expires_at, created_at)
VALUES ($1, 'trading-account:create', repeat('a', 64), repeat('b', 64),
        CURRENT_TIMESTAMP + INTERVAL '1 day', CURRENT_TIMESTAMP)
RETURNING id
`, ownerID).Scan(&idempotencyID); err != nil {
				t.Fatalf("insert Paper account idempotency record: %v", err)
			}
			const accountID = "019d5000-0000-7000-8000-000000000100"
			if _, err := database.Exec(`
INSERT INTO trading_accounts (
    id, owner_user_id, name, market_type, environment, initial_balance,
    paper_fee_rate, creation_idempotency_record_id
) VALUES ($1, $2, 'migration paper', 'spot', 'paper', 10000, 0.001, $3)
`, accountID, ownerID, idempotencyID); err != nil {
				t.Fatalf("insert Paper account: %v", err)
			}
			if _, err := database.Exec(`
INSERT INTO paper_balances (
    account_id, cash_balance, equity, peak_equity, day_start_date,
    day_start_equity, realized_pnl, unrealized_pnl, fees, funding, updated_at
) VALUES ($1, 10000, 10000, 10000, CURRENT_DATE, 10000, 0, 0, $2, 0, CURRENT_TIMESTAMP)
`, accountID, test.fees); err != nil {
				t.Fatalf("insert Paper balance: %v", err)
			}

			_, err = runner.Up(context.Background(), 4)
			if (err != nil) != test.wantError {
				t.Fatalf("apply workflow console migration: err=%v wantError=%t", err, test.wantError)
			}
			current, _, versionErr := runner.Versions(context.Background())
			if versionErr != nil || current != test.wantVersion {
				t.Fatalf("migration version=%d want=%d err=%v", current, test.wantVersion, versionErr)
			}
			var balances int
			if err := database.QueryRow(`SELECT COUNT(*) FROM paper_balances WHERE account_id = $1`, accountID).Scan(&balances); err != nil || balances != 1 {
				t.Fatalf("Paper balance count=%d want=1 err=%v", balances, err)
			}
		})
	}
}

func TestInitialMigrationCoreConstraints(t *testing.T) {
	database := openPostgresSchema(t)
	runner, err := New(database)
	if err != nil {
		t.Fatalf("create migration runner: %v", err)
	}
	if _, err := runner.Up(context.Background(), 0); err != nil {
		t.Fatalf("apply initial migration: %v", err)
	}

	invalidStatements := []string{
		`UPDATE market_sync_settings SET market_types = '[]'::jsonb WHERE id = 1`,
		`UPDATE market_sync_settings SET quote_assets = '["BTC"]'::jsonb WHERE id = 1`,
		`UPDATE market_sync_settings SET spot_rest_base_url = 'http://127.0.0.1' WHERE id = 1`,
		`UPDATE market_sync_settings SET proxy_enabled = TRUE, proxy_url = 'https://proxy.invalid:7890' WHERE id = 1`,
		`INSERT INTO worker_tasks (id, task_type, payload_json, status) VALUES ('invalid-status', 'noop', '{}', 'unknown')`,
		`INSERT INTO trading_controls (id, global_kill_switch) VALUES (2, FALSE)`,
	}
	for _, statement := range invalidStatements {
		if _, err := database.Exec(statement); err == nil {
			t.Fatalf("constraint accepted invalid statement: %s", statement)
		}
	}
	assertIndexes(t, database, []string{
		"ix_market_workflow_subscriptions_instrument_interval",
		"ix_worker_tasks_lane_claim",
		"uq_strategy_signals_instance_candle",
		"uq_trading_intents_signal",
		"ix_worker_heartbeats_lane_heartbeat",
		"ix_workflow_node_templates_owner_enabled",
	})
}

func TestValidateCurrentRejectsDatabaseAhead(t *testing.T) {
	database := openPostgresSchema(t)
	runner, err := New(database)
	if err != nil {
		t.Fatalf("create migration runner: %v", err)
	}
	if _, err := runner.Up(context.Background(), 0); err != nil {
		t.Fatalf("apply initial migration: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO schema_migrations (version_id, is_applied) VALUES (5, TRUE)`); err != nil {
		t.Fatalf("record newer migration: %v", err)
	}
	if err := runner.ValidateCurrent(context.Background()); err == nil {
		t.Fatal("ValidateCurrent accepted a database ahead of the binary")
	}
}

func assertCurrentTables(t *testing.T, database *sql.DB) {
	t.Helper()
	want := []string{
		"ai_model_agent_bindings", "ai_model_configs", "assistant_agents", "assistant_messages", "assistant_sessions",
		"audit_records", "backtests", "domain_event_outbox", "i18n_texts", "idempotency_records", "market_candles",
		"market_instruments", "market_sync_settings", "market_ticker_snapshots", "market_workflow_subscriptions", "menu_buttons",
		"menus", "news_items", "notification_channels", "notification_deliveries", "paper_balances", "paper_orders",
		"paper_positions", "role_menu_buttons", "role_menus", "roles", "schema_migrations", "strategies", "strategy_instances",
		"strategy_signals", "strategy_versions", "testnet_balances", "testnet_open_orders", "testnet_orders",
		"testnet_positions", "testnet_reconciliations", "testnet_risk_states", "testnet_trade_facts", "trading_account_credentials",
		"trading_account_instruments", "trading_accounts", "trading_controls", "trading_events", "trading_intents", "user_roles",
		"users", "watchlist_items", "worker_heartbeats", "worker_tasks", "workflow_definitions", "workflow_execution_attempts", "workflow_execution_nodes",
		"workflow_execution_transitions", "workflow_executions", "workflow_runtime_entries", "workflow_runtime_states",
		"workflow_node_templates",
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

func assertTimescaleLifecycle(t *testing.T, database *sql.DB) {
	t.Helper()
	var hypertable, compression, retention bool
	if err := database.QueryRow(`
SELECT
    EXISTS (SELECT 1 FROM timescaledb_information.hypertables WHERE hypertable_schema = current_schema() AND hypertable_name = 'market_candles'),
    EXISTS (SELECT 1 FROM timescaledb_information.jobs WHERE hypertable_schema = current_schema() AND hypertable_name = 'market_candles' AND proc_name = 'policy_compression'),
    EXISTS (SELECT 1 FROM timescaledb_information.jobs WHERE hypertable_schema = current_schema() AND hypertable_name = 'market_candles' AND proc_name = 'policy_retention')
`).Scan(&hypertable, &compression, &retention); err != nil {
		t.Fatalf("inspect Timescale lifecycle: %v", err)
	}
	if !hypertable || !compression || !retention {
		t.Fatalf("Timescale lifecycle = hypertable:%t compression:%t retention:%t", hypertable, compression, retention)
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
