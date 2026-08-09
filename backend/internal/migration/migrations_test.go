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
	if len(results) != 16 {
		t.Fatalf("migration results = %#v", results)
	}
	for index, result := range results {
		if result.Version != int64(index+1) || result.Direction != "up" {
			t.Fatalf("migration results = %#v", results)
		}
	}
	if err := runner.ValidateCurrent(context.Background()); err != nil {
		t.Fatalf("validate current baseline: %v", err)
	}
	assertCurrentTables(t, database)

	results, err = runner.Up(context.Background(), 0)
	if err != nil {
		t.Fatalf("repeat baseline: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("repeat baseline applied %#v", results)
	}

	results, err = runner.Down(context.Background(), 16)
	if err != nil {
		t.Fatalf("roll back empty migrations: %v", err)
	}
	if len(results) != 16 {
		t.Fatalf("migration rollback results = %#v", results)
	}
	for index, result := range results {
		if result.Version != int64(16-index) || result.Direction != "down" {
			t.Fatalf("migration rollback results = %#v", results)
		}
	}
	if err := runner.ValidateCurrent(context.Background()); err == nil {
		t.Fatal("ValidateCurrent accepted a rolled-back baseline")
	}
	if _, err := runner.Up(context.Background(), 0); err != nil {
		t.Fatalf("replay baseline: %v", err)
	}
	assertCurrentTables(t, database)
}

func TestPostgresBaselineDownRejectsData(t *testing.T) {
	database := openPostgresSchema(t)
	runner, err := New(database)
	if err != nil {
		t.Fatalf("create migration runner: %v", err)
	}
	if _, err := runner.Up(context.Background(), 0); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	if _, err := runner.Down(context.Background(), 14); err != nil {
		t.Fatalf("roll back empty market migrations: %v", err)
	}
	if _, err := runner.Down(context.Background(), 1); err != nil {
		t.Fatalf("roll back empty observability migration: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO roles (code) VALUES ('rollback-guard')`); err != nil {
		t.Fatalf("insert rollback guard row: %v", err)
	}

	if _, err := runner.Down(context.Background(), 1); err == nil {
		t.Fatal("baseline rollback removed a non-empty schema")
	}
	current, latest, versionErr := runner.Versions(context.Background())
	if versionErr != nil || current != 1 || latest != 16 {
		t.Fatalf("failed baseline rollback versions = current:%d latest:%d err:%v", current, latest, versionErr)
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
		{"audit invalid request id", `INSERT INTO audit_records (request_id, action, resource_path, outcome, status_code) VALUES ('bad request id', 'POST /api/v1/test', '/api/v1/test', 'failure', 400)`},
		{"audit invalid outcome", `INSERT INTO audit_records (request_id, action, resource_path, outcome, status_code) VALUES ('audit-invalid-outcome', 'POST /api/v1/test', '/api/v1/test', 'unknown', 400)`},
		{"audit invalid status", `INSERT INTO audit_records (request_id, action, resource_path, outcome, status_code) VALUES ('audit-invalid-status', 'POST /api/v1/test', '/api/v1/test', 'failure', 99)`},
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
		"ix_idempotency_records_expires_at",
		"ux_idempotency_records_user_scope_key",
		"ix_event_outbox_pending",
		"ix_event_outbox_recovery",
		"ux_event_outbox_lease_id",
		"ix_event_outbox_dead_letter_alert",
		"ix_event_outbox_terminal_retention",
		"ix_worker_tasks_claim",
		"ix_worker_tasks_lane_claim",
		"ix_worker_tasks_recovery",
		"ux_worker_tasks_lease_id",
		"ix_audit_records_created_at",
		"ix_audit_records_actor_created_at",
		"ix_audit_records_request_id",
	})
}

func TestObservabilityDownRejectsAuditData(t *testing.T) {
	database := openPostgresSchema(t)
	runner, err := New(database)
	if err != nil {
		t.Fatalf("create migration runner: %v", err)
	}
	if _, err := runner.Up(context.Background(), 0); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	if _, err := runner.Down(context.Background(), 14); err != nil {
		t.Fatalf("roll back empty market migrations: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO audit_records (request_id, action, resource_path, outcome, status_code) VALUES ('rollback-guard', 'POST /api/v1/test', '/api/v1/test', 'success', 200)`); err != nil {
		t.Fatalf("insert audit rollback guard: %v", err)
	}

	if _, err := runner.Down(context.Background(), 1); err == nil {
		t.Fatal("observability rollback removed persistent audit data")
	}
	current, latest, versionErr := runner.Versions(context.Background())
	if versionErr != nil || current != 2 || latest != 16 {
		t.Fatalf("failed observability rollback versions = current:%d latest:%d err:%v", current, latest, versionErr)
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM audit_records WHERE request_id = 'rollback-guard'`).Scan(&count); err != nil {
		t.Fatalf("read audit rollback guard: %v", err)
	}
	if count != 1 {
		t.Fatalf("audit rollback guard rows = %d, want 1", count)
	}
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
	if _, err := runner.Down(context.Background(), 15); err != nil {
		t.Fatalf("roll back empty market and observability migrations: %v", err)
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
	current, latest, versionErr := runner.Versions(context.Background())
	if versionErr != nil || current != 1 || latest != 16 {
		t.Fatalf("failed concurrent rollback versions = current:%d latest:%d err:%v", current, latest, versionErr)
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
	if _, err := database.Exec(`INSERT INTO schema_migrations (version_id, is_applied) VALUES (17, TRUE)`); err != nil {
		t.Fatalf("record newer migration: %v", err)
	}
	if err := runner.ValidateCurrent(context.Background()); err == nil {
		t.Fatal("ValidateCurrent accepted a database newer than the binary")
	}
}

func TestA2MarketContractSchemaAndUpserts(t *testing.T) {
	database := openPostgresSchema(t)
	runner, err := New(database)
	if err != nil {
		t.Fatalf("create migration runner: %v", err)
	}
	if _, err := runner.Up(context.Background(), 0); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	assertA2Columns(t, database)
	assertA2TimescaleLifecycle(t, database)
	const instrumentID = "019c2f6d-7c00-7000-8000-000000000001"
	insertA2Instrument(t, database, instrumentID)

	invalidRows := []struct {
		name string
		sql  string
	}{
		{"instrument without generated UUIDv7", `INSERT INTO market_instruments (venue, market_type, native_symbol, base_asset, quote_asset, status, price_tick, quantity_step) VALUES ('binance', 'spot', 'ETHUSDT', 'ETH', 'USDT', 'trading', 0.1, 0.001)`},
		{"instrument UUIDv4", `INSERT INTO market_instruments (id, venue, market_type, native_symbol, base_asset, quote_asset, status, price_tick, quantity_step) VALUES ('019c2f6d-7c00-4000-8000-000000000002', 'binance', 'spot', 'ETHUSDT', 'ETH', 'USDT', 'trading', 0.1, 0.001)`},
		{"instrument UUID wrong variant", `INSERT INTO market_instruments (id, venue, market_type, native_symbol, base_asset, quote_asset, status, price_tick, quantity_step) VALUES ('019c2f6d-7c00-7000-7000-000000000002', 'binance', 'spot', 'ETHUSDT', 'ETH', 'USDT', 'trading', 0.1, 0.001)`},
		{"instrument venue enum", `INSERT INTO market_instruments (id, venue, market_type, native_symbol, base_asset, quote_asset, status, price_tick, quantity_step) VALUES ('019c2f6d-7c00-7000-8000-000000000003', 'kraken', 'spot', 'ETHUSDT', 'ETH', 'USDT', 'trading', 0.1, 0.001)`},
		{"instrument market type enum", `INSERT INTO market_instruments (id, venue, market_type, native_symbol, base_asset, quote_asset, status, price_tick, quantity_step) VALUES ('019c2f6d-7c00-7000-8000-000000000004', 'binance', 'future', 'ETHUSDT', 'ETH', 'USDT', 'trading', 0.1, 0.001)`},
		{"instrument status enum", `INSERT INTO market_instruments (id, venue, market_type, native_symbol, base_asset, quote_asset, status, price_tick, quantity_step) VALUES ('019c2f6d-7c00-7000-8000-000000000005', 'binance', 'spot', 'ETHUSDT', 'ETH', 'USDT', 'unknown', 0.1, 0.001)`},
		{"instrument decimal step", `INSERT INTO market_instruments (id, venue, market_type, native_symbol, base_asset, quote_asset, status, price_tick, quantity_step) VALUES ('019c2f6d-7c00-7000-8000-000000000006', 'binance', 'spot', 'ETHUSDT', 'ETH', 'USDT', 'trading', 0, 0.001)`},
		{"instrument native symbol charset", `INSERT INTO market_instruments (id, venue, market_type, native_symbol, base_asset, quote_asset, status, price_tick, quantity_step) VALUES ('019c2f6d-7c00-7000-8000-000000000008', 'binance', 'spot', 'BTC/USDT', 'BTC', 'USDT', 'trading', 0.1, 0.001)`},
		{"instrument base asset charset", `INSERT INTO market_instruments (id, venue, market_type, native_symbol, base_asset, quote_asset, status, price_tick, quantity_step) VALUES ('019c2f6d-7c00-7000-8000-000000000009', 'binance', 'spot', 'ETHUSDT', 'eth', 'USDT', 'trading', 0.1, 0.001)`},
		{"instrument quote asset charset", `INSERT INTO market_instruments (id, venue, market_type, native_symbol, base_asset, quote_asset, status, price_tick, quantity_step) VALUES ('019c2f6d-7c00-7000-8000-00000000000a', 'binance', 'spot', 'ETHUSDT', 'ETH', 'USD/T', 'trading', 0.1, 0.001)`},
		{"instrument numeric integer overflow", `INSERT INTO market_instruments (id, venue, market_type, native_symbol, base_asset, quote_asset, status, price_tick, quantity_step) VALUES ('019c2f6d-7c00-7000-8000-00000000000b', 'binance', 'spot', 'ETHUSDT', 'ETH', 'USDT', 'trading', 100000000000000000000, 0.001)`},
		{"instrument minimum quantity", `INSERT INTO market_instruments (id, venue, market_type, native_symbol, base_asset, quote_asset, status, price_tick, quantity_step, min_quantity, min_notional, updated_at) VALUES ('019c2f6d-7c00-7000-8000-00000000000d', 'binance', 'spot', 'ETHUSDT', 'ETH', 'USDT', 'trading', 0.1, 0.001, 0, 5, TIMESTAMPTZ '2026-08-01 00:00:00+00')`},
		{"instrument minimum notional", `INSERT INTO market_instruments (id, venue, market_type, native_symbol, base_asset, quote_asset, status, price_tick, quantity_step, min_quantity, min_notional, updated_at) VALUES ('019c2f6d-7c00-7000-8000-00000000000e', 'binance', 'spot', 'ETHUSDT', 'ETH', 'USDT', 'trading', 0.1, 0.001, 0.001, 0, TIMESTAMPTZ '2026-08-01 00:00:00+00')`},
		{"instrument update time", `INSERT INTO market_instruments (id, venue, market_type, native_symbol, base_asset, quote_asset, status, price_tick, quantity_step, min_quantity, min_notional, updated_at) VALUES ('019c2f6d-7c00-7000-8000-00000000000f', 'binance', 'spot', 'ETHUSDT', 'ETH', 'USDT', 'trading', 0.1, 0.001, 0.001, 5, TIMESTAMPTZ 'infinity')`},
		{"candle interval enum", `INSERT INTO market_candles (venue, instrument_id, interval_code, open_time, close_time, open_price, high_price, low_price, close_price, base_volume, is_closed) VALUES ('binance', '019c2f6d-7c00-7000-8000-000000000001', '2m', TIMESTAMPTZ '2026-08-01 00:00:00+00', TIMESTAMPTZ '2026-08-01 00:01:00+00', 100, 101, 99, 100, 1, true)`},
		{"candle UTC interval alignment", `INSERT INTO market_candles (venue, instrument_id, interval_code, open_time, close_time, open_price, high_price, low_price, close_price, base_volume, is_closed) VALUES ('binance', '019c2f6d-7c00-7000-8000-000000000001', '1m', TIMESTAMPTZ '2026-08-01 00:00:30+00', TIMESTAMPTZ '2026-08-01 00:01:30+00', 100, 101, 99, 100, 1, true)`},
		{"candle exclusive close time", `INSERT INTO market_candles (venue, instrument_id, interval_code, open_time, close_time, open_price, high_price, low_price, close_price, base_volume, is_closed) VALUES ('binance', '019c2f6d-7c00-7000-8000-000000000001', '1m', TIMESTAMPTZ '2026-08-01 00:06:00+00', TIMESTAMPTZ '2026-08-01 00:06:59+00', 100, 101, 99, 100, 1, true)`},
		{"candle decimal price", `INSERT INTO market_candles (venue, instrument_id, interval_code, open_time, close_time, open_price, high_price, low_price, close_price, base_volume, is_closed) VALUES ('binance', '019c2f6d-7c00-7000-8000-000000000001', '1m', TIMESTAMPTZ '2026-08-01 00:04:00+00', TIMESTAMPTZ '2026-08-01 00:05:00+00', 0, 101, 0, 100, 1, true)`},
		{"candle negative base volume", `INSERT INTO market_candles (venue, instrument_id, interval_code, open_time, close_time, open_price, high_price, low_price, close_price, base_volume, is_closed) VALUES ('binance', '019c2f6d-7c00-7000-8000-000000000001', '1m', TIMESTAMPTZ '2026-08-01 00:07:00+00', TIMESTAMPTZ '2026-08-01 00:08:00+00', 100, 101, 99, 100, -1, true)`},
		{"candle OHLC", `INSERT INTO market_candles (venue, instrument_id, interval_code, open_time, close_time, open_price, high_price, low_price, close_price, base_volume, is_closed) VALUES ('binance', '019c2f6d-7c00-7000-8000-000000000001', '1m', TIMESTAMPTZ '2026-08-01 00:01:00+00', TIMESTAMPTZ '2026-08-01 00:02:00+00', 100, 99, 98, 100, 1, true)`},
		{"candle foreign key", `INSERT INTO market_candles (venue, instrument_id, interval_code, open_time, close_time, open_price, high_price, low_price, close_price, base_volume, is_closed) VALUES ('binance', '019c2f6d-7c00-7000-8000-000000000002', '1m', TIMESTAMPTZ '2026-08-01 00:02:00+00', TIMESTAMPTZ '2026-08-01 00:03:00+00', 100, 101, 99, 100, 1, true)`},
		{"ticker foreign key", `INSERT INTO market_ticker_snapshots (venue, instrument_id, occurred_at, last_price, best_bid_price, best_ask_price) VALUES ('binance', '019c2f6d-7c00-7000-8000-000000000002', TIMESTAMPTZ '2026-08-01 00:03:30+00', 100, 99, 101)`},
		{"ticker non-finite occurred at", `INSERT INTO market_ticker_snapshots (venue, instrument_id, occurred_at, last_price, best_bid_price, best_ask_price) VALUES ('binance', '019c2f6d-7c00-7000-8000-000000000001', TIMESTAMPTZ 'infinity', 100, 99, 101)`},
		{"ticker non-positive price", `INSERT INTO market_ticker_snapshots (venue, instrument_id, occurred_at, last_price, best_bid_price, best_ask_price) VALUES ('binance', '019c2f6d-7c00-7000-8000-000000000001', TIMESTAMPTZ '2026-08-01 00:03:30+00', 0, 99, 101)`},
		{"ticker spread", `INSERT INTO market_ticker_snapshots (venue, instrument_id, occurred_at, last_price, best_bid_price, best_ask_price) VALUES ('binance', '019c2f6d-7c00-7000-8000-000000000001', TIMESTAMPTZ '2026-08-01 00:03:30+00', 100, 101, 99)`},
	}
	for _, test := range invalidRows {
		if _, err := database.Exec(test.sql); err == nil {
			t.Fatalf("A2 schema accepted %s", test.name)
		}
	}
	if _, err := database.Exec(`
INSERT INTO market_instruments (
    id, venue, market_type, native_symbol, base_asset, quote_asset, status,
    price_tick, quantity_step, min_quantity, min_notional, updated_at
) VALUES (
    '019c2f6d-7c00-7000-8000-00000000000c', 'binance', 'usd_m', 'BTCUSDT', 'BTC', 'USDT', 'trading',
    0.1, 0.001, 0.001, 5, TIMESTAMPTZ '2026-08-01 00:00:00+00'
)
`); err != nil {
		t.Fatalf("insert valid USD-M instrument: %v", err)
	}

	const replacementID = "019c2f6d-7c00-7000-8000-000000000007"
	if _, err := database.Exec(`
INSERT INTO market_instruments (
    id, venue, market_type, native_symbol, base_asset, quote_asset, status,
    price_tick, quantity_step, min_quantity, min_notional, updated_at
) VALUES ($1, 'binance', 'spot', 'BTCUSDT', 'BTC', 'USDT', 'suspended', 0.2, 0.01, 0.002, 6, TIMESTAMPTZ '2026-08-01 00:01:00+00')
ON CONFLICT (venue, market_type, native_symbol) DO UPDATE SET
    base_asset = EXCLUDED.base_asset,
    quote_asset = EXCLUDED.quote_asset,
    status = EXCLUDED.status,
    price_tick = EXCLUDED.price_tick,
    quantity_step = EXCLUDED.quantity_step,
    min_quantity = EXCLUDED.min_quantity,
    min_notional = EXCLUDED.min_notional,
    updated_at = EXCLUDED.updated_at
`, replacementID); err != nil {
		t.Fatalf("upsert instrument metadata: %v", err)
	}
	var storedID string
	var instrumentUpdated bool
	if err := database.QueryRow(`
SELECT id::text,
       price_tick = 0.2 AND quantity_step = 0.01 AND min_quantity = 0.002
           AND min_notional = 6 AND updated_at = TIMESTAMPTZ '2026-08-01 00:01:00+00'
           AND status = 'suspended'
FROM market_instruments
WHERE venue = 'binance' AND market_type = 'spot' AND native_symbol = 'BTCUSDT'
`).Scan(&storedID, &instrumentUpdated); err != nil {
		t.Fatalf("read upserted instrument: %v", err)
	}
	if storedID != instrumentID || !instrumentUpdated {
		t.Fatalf("instrument upsert stored id=%q updated=%t", storedID, instrumentUpdated)
	}
	assertRowCount(t, database, "SELECT COUNT(*) FROM market_instruments WHERE venue = 'binance' AND market_type = 'spot' AND native_symbol = 'BTCUSDT'", 1)

	for _, isClosed := range []bool{false, true} {
		if _, err := database.Exec(`
INSERT INTO market_candles (
    venue, instrument_id, interval_code, open_time, close_time,
    open_price, high_price, low_price, close_price, base_volume, is_closed
) VALUES (
    'binance', $1, '1m', TIMESTAMPTZ '2026-08-01 00:00:00+00', TIMESTAMPTZ '2026-08-01 00:01:00+00',
    100.1, 101.2, 99.9, 100.8, 1.25, $2
)
ON CONFLICT (venue, instrument_id, interval_code, open_time) DO UPDATE SET
    close_time = EXCLUDED.close_time,
    open_price = EXCLUDED.open_price,
    high_price = EXCLUDED.high_price,
    low_price = EXCLUDED.low_price,
    close_price = EXCLUDED.close_price,
    base_volume = EXCLUDED.base_volume,
    is_closed = EXCLUDED.is_closed
`, instrumentID, isClosed); err != nil {
			t.Fatalf("upsert candle is_closed=%t: %v", isClosed, err)
		}
	}
	var candleClosed bool
	if err := database.QueryRow(`
SELECT is_closed
FROM market_candles
WHERE venue = 'binance' AND instrument_id = $1 AND interval_code = '1m' AND open_time = TIMESTAMPTZ '2026-08-01 00:00:00+00'
`, instrumentID).Scan(&candleClosed); err != nil {
		t.Fatalf("read upserted candle: %v", err)
	}
	if !candleClosed {
		t.Fatal("candle upsert did not retain the latest value")
	}
	assertRowCount(t, database, "SELECT COUNT(*) FROM market_candles WHERE venue = 'binance' AND instrument_id = '019c2f6d-7c00-7000-8000-000000000001' AND interval_code = '1m' AND open_time = TIMESTAMPTZ '2026-08-01 00:00:00+00'", 1)

	for _, ticker := range []struct {
		occurredAt string
		lastPrice  string
	}{
		{"2026-08-01 00:03:30+00", "102"},
		{"2026-08-01 00:03:30+00", "102.5"},
		{"2026-08-01 00:03:00+00", "101"},
	} {
		if _, err := database.Exec(`
INSERT INTO market_ticker_snapshots (
    venue, instrument_id, occurred_at, last_price, best_bid_price, best_ask_price
) VALUES ('binance', $1, $2::timestamptz, $3::numeric, 101.9, 102.1)
ON CONFLICT (venue, instrument_id) DO UPDATE SET
    occurred_at = EXCLUDED.occurred_at,
    last_price = EXCLUDED.last_price,
    best_bid_price = EXCLUDED.best_bid_price,
    best_ask_price = EXCLUDED.best_ask_price
WHERE EXCLUDED.occurred_at >= market_ticker_snapshots.occurred_at
`, instrumentID, ticker.occurredAt, ticker.lastPrice); err != nil {
			t.Fatalf("upsert ticker at %s: %v", ticker.occurredAt, err)
		}
	}
	var tickerCurrent bool
	if err := database.QueryRow(`
SELECT
    occurred_at = TIMESTAMPTZ '2026-08-01 00:03:30+00'
    AND last_price = 102.5
FROM market_ticker_snapshots
WHERE venue = 'binance' AND instrument_id = $1
`, instrumentID).Scan(&tickerCurrent); err != nil {
		t.Fatalf("read ticker snapshot: %v", err)
	}
	if !tickerCurrent {
		t.Fatal("older ticker upsert overwrote the newer snapshot")
	}
	assertRowCount(t, database, "SELECT COUNT(*) FROM market_ticker_snapshots WHERE venue = 'binance' AND instrument_id = '019c2f6d-7c00-7000-8000-000000000001'", 1)
}

// A2 Down 在单事务中锁定三张行情表并保护数据，失败时必须保持 schema 与版本。
func TestA2MarketContractDownRejectsData(t *testing.T) {
	tests := []struct {
		name      string
		table     string
		insertSQL string
	}{
		{"instrument", "market_instruments", ""},
		{"candle", "market_candles", `INSERT INTO market_candles (venue, instrument_id, interval_code, open_time, close_time, open_price, high_price, low_price, close_price, base_volume, is_closed) VALUES ('binance', '019c2f6d-7c00-7000-8000-000000000001', '1m', TIMESTAMPTZ '2026-08-01 00:00:00+00', TIMESTAMPTZ '2026-08-01 00:01:00+00', 100, 101, 99, 100, 1, true)`},
		{"ticker", "market_ticker_snapshots", `INSERT INTO market_ticker_snapshots (venue, instrument_id, occurred_at, last_price, best_bid_price, best_ask_price) VALUES ('binance', '019c2f6d-7c00-7000-8000-000000000001', TIMESTAMPTZ '2026-08-01 00:03:30+00', 100, 99, 101)`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database := openPostgresSchema(t)
			runner, err := New(database)
			if err != nil {
				t.Fatalf("create migration runner: %v", err)
			}
			if _, err := runner.Up(context.Background(), 0); err != nil {
				t.Fatalf("apply migrations: %v", err)
			}
			insertA2Instrument(t, database, "019c2f6d-7c00-7000-8000-000000000001")
			if test.insertSQL != "" {
				if _, err := database.Exec(test.insertSQL); err != nil {
					t.Fatalf("insert %s rollback guard: %v", test.name, err)
				}
			}

			if _, err := runner.Down(context.Background(), 14); err == nil {
				t.Fatalf("A2 rollback removed non-empty %s", test.table)
			}
			current, latest, versionErr := runner.Versions(context.Background())
			if versionErr != nil || current != 3 || latest != 16 {
				t.Fatalf("A2 rollback versions = current:%d latest:%d err:%v", current, latest, versionErr)
			}
			assertA2Tables(t, database)
			assertRowCount(t, database, fmt.Sprintf("SELECT COUNT(*) FROM %s", test.table), 1)
		})
	}
}

func TestM1MarketWatchlistConstraintsAndDownGuard(t *testing.T) {
	database := openPostgresSchema(t)
	runner, err := New(database)
	if err != nil {
		t.Fatalf("create migration runner: %v", err)
	}
	if _, err := runner.Up(context.Background(), 0); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	const instrumentID = "019c2f6d-7c00-7000-8000-000000000001"
	insertA2Instrument(t, database, instrumentID)
	var ownerID int64
	if err := database.QueryRow(`INSERT INTO users (username) VALUES ('watchlist-owner') RETURNING id`).Scan(&ownerID); err != nil {
		t.Fatalf("insert watchlist owner: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO watchlist_items (id, owner_user_id, instrument_id, interval_code)
VALUES ('019c2f6d-7c00-7000-8000-000000000010', $1, $2, '1m')
`, ownerID, instrumentID); err != nil {
		t.Fatalf("insert watchlist item: %v", err)
	}

	invalidRows := []struct {
		name string
		sql  string
	}{
		{"UUIDv4", `INSERT INTO watchlist_items (id, owner_user_id, instrument_id, interval_code) VALUES ('019c2f6d-7c00-4000-8000-000000000011', 1, '019c2f6d-7c00-7000-8000-000000000001', '5m')`},
		{"interval", `INSERT INTO watchlist_items (id, owner_user_id, instrument_id, interval_code) VALUES ('019c2f6d-7c00-7000-8000-000000000012', 1, '019c2f6d-7c00-7000-8000-000000000001', '2m')`},
		{"duplicate", `INSERT INTO watchlist_items (id, owner_user_id, instrument_id, interval_code) VALUES ('019c2f6d-7c00-7000-8000-000000000013', 1, '019c2f6d-7c00-7000-8000-000000000001', '1m')`},
		{"missing owner", `INSERT INTO watchlist_items (id, owner_user_id, instrument_id, interval_code) VALUES ('019c2f6d-7c00-7000-8000-000000000014', 9223372036854775807, '019c2f6d-7c00-7000-8000-000000000001', '5m')`},
		{"missing instrument", `INSERT INTO watchlist_items (id, owner_user_id, instrument_id, interval_code) VALUES ('019c2f6d-7c00-7000-8000-000000000015', 1, '019c2f6d-7c00-7000-8000-000000000099', '5m')`},
	}
	for _, test := range invalidRows {
		if _, err := database.Exec(test.sql); err == nil {
			t.Fatalf("watchlist schema accepted %s", test.name)
		}
	}
	assertPostgresIndexes(t, database, []string{"ix_watchlist_items_instrument_interval"})

	if _, err := runner.Down(context.Background(), 13); err == nil {
		t.Fatal("watchlist rollback removed persistent user data")
	}
	current, latest, versionErr := runner.Versions(context.Background())
	if versionErr != nil || current != 4 || latest != 16 {
		t.Fatalf("watchlist rollback versions = current:%d latest:%d err:%v", current, latest, versionErr)
	}
	assertRowCount(t, database, "SELECT COUNT(*) FROM watchlist_items", 1)
}

func TestM14WorkerLaneConstraintsAndDownGuard(t *testing.T) {
	database := openPostgresSchema(t)
	runner, err := New(database)
	if err != nil {
		t.Fatalf("create migration runner: %v", err)
	}
	if _, err := runner.Up(context.Background(), 0); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	if _, err := database.Exec(`
INSERT INTO worker_tasks (id, task_type, payload_json, lane, priority)
	VALUES ('worker-backtest-lane', 'contract.noop', '{}', 'backtest', 10)
`); err != nil {
		t.Fatalf("insert backtest task: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO worker_tasks (id, task_type, payload_json)
VALUES ('worker-realtime-default-lane', 'contract.noop', '{}')
`); err != nil {
		t.Fatalf("insert default realtime task: %v", err)
	}

	invalidRows := []struct {
		name string
		sql  string
	}{
		{"unknown lane", `INSERT INTO worker_tasks (id, task_type, payload_json, lane) VALUES ('worker-invalid-lane', 'strategy.backtest', '{"backtestId":"019c2f6d-7c00-7000-8000-000000000001"}', 'other')`},
		{"negative priority", `INSERT INTO worker_tasks (id, task_type, payload_json, priority) VALUES ('worker-invalid-priority', 'strategy.backtest', '{"backtestId":"019c2f6d-7c00-7000-8000-000000000001"}', -1)`},
	}
	for _, test := range invalidRows {
		if _, err := database.Exec(test.sql); err == nil {
			t.Fatalf("worker lane schema accepted %s", test.name)
		}
	}

	if _, err := runner.Down(context.Background(), 12); err == nil {
		t.Fatal("worker lane rollback removed persistent tasks")
	}
	current, latest, versionErr := runner.Versions(context.Background())
	if versionErr != nil || current != 5 || latest != 16 {
		t.Fatalf("worker lane rollback versions = current:%d latest:%d err:%v", current, latest, versionErr)
	}
	assertRowCount(t, database, "SELECT COUNT(*) FROM worker_tasks", 2)
}

func TestM14StrategyBacktestRuntimeConstraintsAndDownGuard(t *testing.T) {
	database := openPostgresSchema(t)
	runner, err := New(database)
	if err != nil {
		t.Fatalf("create migration runner: %v", err)
	}
	if _, err := runner.Up(context.Background(), 0); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	invalidTasks := []struct {
		name string
		sql  string
	}{
		{"publish realtime lane", `INSERT INTO worker_tasks (id, task_type, payload_json) VALUES ('invalid-publish-lane', 'strategy.publish', '{"strategyId":"019c2f6d-7c00-7000-8000-000000000001","strategyVersionId":"019c2f6d-7c00-7000-8000-000000000002"}')`},
		{"backtest extra payload", `INSERT INTO worker_tasks (id, task_type, payload_json, lane) VALUES ('invalid-backtest-extra', 'strategy.backtest', '{"backtestId":"019c2f6d-7c00-7000-8000-000000000003","extra":true}', 'backtest')`},
		{"backtest UUIDv4", `INSERT INTO worker_tasks (id, task_type, payload_json, lane) VALUES ('invalid-backtest-uuid', 'strategy.backtest', '{"backtestId":"019c2f6d-7c00-4000-8000-000000000003"}', 'backtest')`},
	}
	for _, test := range invalidTasks {
		if _, err := database.Exec(test.sql); err == nil {
			t.Fatalf("strategy task schema accepted %s", test.name)
		}
	}

	const instrumentID = "019c2f6d-7c00-7000-8000-000000000001"
	const strategyID = "019c2f6d-7c00-7000-8000-000000000010"
	const versionID = "019c2f6d-7c00-7000-8000-000000000011"
	const backtestID = "019c2f6d-7c00-7000-8000-000000000012"
	const publishTaskID = "019c2f6d-7c00-7000-8000-000000000013"
	const backtestTaskID = "019c2f6d-7c00-7000-8000-000000000014"
	insertA2Instrument(t, database, instrumentID)
	var ownerID int64
	if err := database.QueryRow(`INSERT INTO users (username) VALUES ('strategy-owner') RETURNING id`).Scan(&ownerID); err != nil {
		t.Fatalf("insert strategy owner: %v", err)
	}
	var publishRecordID, backtestRecordID int64
	if err := database.QueryRow(`
INSERT INTO idempotency_records (user_id, scope, key_hash, request_hash, expires_at, created_at)
VALUES ($1, 'strategy:publish', repeat('a', 64), repeat('b', 64),
        CURRENT_TIMESTAMP + INTERVAL '1 day', CURRENT_TIMESTAMP)
RETURNING id
`, ownerID).Scan(&publishRecordID); err != nil {
		t.Fatalf("insert publish idempotency record: %v", err)
	}
	if err := database.QueryRow(`
INSERT INTO idempotency_records (user_id, scope, key_hash, request_hash, expires_at, created_at)
VALUES ($1, 'backtest:create', repeat('c', 64), repeat('d', 64),
        CURRENT_TIMESTAMP + INTERVAL '1 day', CURRENT_TIMESTAMP)
RETURNING id
`, ownerID).Scan(&backtestRecordID); err != nil {
		t.Fatalf("insert backtest idempotency record: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO strategies (
    id, name, source_code, market_type, instrument_id, interval_code, lookback_bars,
    parameter_schema_json, created_by_user_id, updated_by_user_id
) VALUES ($1, 'hold', 'def on_bar(candles, params): return Decimal(''0'')', 'spot', $2, '1m', 1, '{}', $3, $3)
`, strategyID, instrumentID, ownerID); err != nil {
		t.Fatalf("insert strategy draft: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO worker_tasks (id, task_type, payload_json, lane)
VALUES
    ($1, 'strategy.publish', $2, 'backtest'),
    ($3, 'strategy.backtest', $4, 'backtest')
`, publishTaskID, `{"strategyId":"`+strategyID+`","strategyVersionId":"`+versionID+`"}`,
		backtestTaskID, `{"backtestId":"`+backtestID+`"}`); err != nil {
		t.Fatalf("insert strategy tasks: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO strategy_versions (
    id, strategy_id, version_number, worker_task_id, idempotency_record_id, name,
    source_code, code_sha256, runtime_version, market_type, instrument_id, symbol, interval_code,
    lookback_bars, parameter_schema_json, published_by_user_id
) VALUES ($1, $2, 1, $3, $4, 'hold', 'def on_bar(candles, params): return Decimal(''0'')',
          repeat('e', 64), 'python3.12', 'spot', $5, 'BTCUSDT', '1m', 1, '{}', $6)
`, versionID, strategyID, publishTaskID, publishRecordID, instrumentID, ownerID); err != nil {
		t.Fatalf("insert pending strategy version: %v", err)
	}
	if _, err := database.Exec(`
UPDATE strategy_versions
SET status = 'published', published_at = CURRENT_TIMESTAMP
WHERE id = $1
`, versionID); err != nil {
		t.Fatalf("publish immutable strategy version: %v", err)
	}
	for _, statement := range []string{
		`UPDATE strategy_versions SET source_code = 'changed' WHERE id = '` + versionID + `'`,
		`UPDATE strategy_versions SET status = 'failed', published_at = NULL WHERE id = '` + versionID + `'`,
		`DELETE FROM strategy_versions WHERE id = '` + versionID + `'`,
	} {
		if _, err := database.Exec(statement); err == nil {
			t.Fatalf("published strategy version accepted mutation: %s", statement)
		}
	}

	if _, err := database.Exec(`
INSERT INTO backtests (
    id, owner_user_id, strategy_version_id, worker_task_id, idempotency_record_id,
    simulator_version, start_time, end_time, allocation_usdt, initial_equity, fee_rate,
    slippage_rate
) VALUES ($1, $2, $3, $4, $5,
          'decimal-bar-v1', TIMESTAMPTZ '2026-08-01 00:00:00+00',
          TIMESTAMPTZ '2026-08-01 00:01:00+00',
          100, 1000, 0.001, 0.002)
`, backtestID, ownerID, versionID, backtestTaskID, backtestRecordID); err != nil {
		t.Fatalf("insert backtest: %v", err)
	}
	if _, err := database.Exec(`UPDATE backtests SET input_sha256 = repeat('f', 64) WHERE id = $1`, backtestID); err == nil {
		t.Fatal("backtest accepted a partial result hash set")
	}
	if _, err := database.Exec(`
UPDATE backtests
SET summary_json = '{"type":"summary"}', input_sha256 = repeat('f', 64),
    result_sha256 = repeat('1', 64), manifest_sha256 = repeat('2', 64)
WHERE id = $1
`, backtestID); err != nil {
		t.Fatalf("freeze complete backtest result: %v", err)
	}
	assertPostgresIndexes(t, database, []string{"ix_backtests_owner_created", "ix_strategy_versions_published"})

	if _, err := runner.Down(context.Background(), 11); err == nil {
		t.Fatal("strategy runtime rollback removed persistent data")
	}
	current, latest, versionErr := runner.Versions(context.Background())
	if versionErr != nil || current != 6 || latest != 16 {
		t.Fatalf("strategy runtime rollback versions = current:%d latest:%d err:%v", current, latest, versionErr)
	}
	assertRowCount(t, database, "SELECT COUNT(*) FROM strategies", 1)
	assertRowCount(t, database, "SELECT COUNT(*) FROM backtests", 1)
}

func TestM2RealtimeSignalConstraintsAndDownGuard(t *testing.T) {
	database := openPostgresSchema(t)
	runner, err := New(database)
	if err != nil {
		t.Fatalf("create migration runner: %v", err)
	}
	if _, err := runner.Up(context.Background(), 0); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	const instrumentID = "019d1000-0000-7000-8000-000000000001"
	const strategyID = "019d1000-0000-7000-8000-000000000010"
	const versionID = "019d1000-0000-7000-8000-000000000011"
	const publishTaskID = "019d1000-0000-7000-8000-000000000012"
	const instanceID = "019d1000-0000-7000-8000-000000000013"
	const realtimeTaskID = "019d1000-0000-7000-8000-000000000014"
	const signalID = "019d1000-0000-7000-8000-000000000030"
	const candleTime = "2026-08-08T00:00:00Z"
	insertA2Instrument(t, database, instrumentID)
	var ownerID, recordID int64
	if err := database.QueryRow(`INSERT INTO users (username) VALUES ('m2-owner') RETURNING id`).Scan(&ownerID); err != nil {
		t.Fatalf("insert M2 owner: %v", err)
	}
	if err := database.QueryRow(`
INSERT INTO idempotency_records (user_id, scope, key_hash, request_hash, expires_at, created_at)
VALUES ($1, 'strategy:publish:m2', repeat('a', 64), repeat('b', 64),
        CURRENT_TIMESTAMP + INTERVAL '1 day', CURRENT_TIMESTAMP)
RETURNING id
`, ownerID).Scan(&recordID); err != nil {
		t.Fatalf("insert M2 idempotency record: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO strategies (
    id, name, source_code, market_type, instrument_id, interval_code, lookback_bars,
    parameter_schema_json, created_by_user_id, updated_by_user_id
) VALUES ($1, 'm2 hold', 'def on_bar(candles, params): return Decimal(''0'')',
          'spot', $2, '1m', 2, '{}', $3, $3)
`, strategyID, instrumentID, ownerID); err != nil {
		t.Fatalf("insert M2 strategy: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO worker_tasks (id, task_type, payload_json, status, attempt_count, lane, finished_at)
VALUES ($1, 'strategy.publish', $2, 'succeeded', 1, 'backtest', CURRENT_TIMESTAMP)
`, publishTaskID, `{"strategyId":"`+strategyID+`","strategyVersionId":"`+versionID+`"}`); err != nil {
		t.Fatalf("insert M2 publish task: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO strategy_versions (
    id, strategy_id, version_number, status, worker_task_id, idempotency_record_id,
    name, source_code, code_sha256, runtime_version, market_type, instrument_id, symbol,
    interval_code, lookback_bars, parameter_schema_json, published_by_user_id, published_at
) VALUES ($1, $2, 1, 'published', $3, $4, 'm2 hold',
          'def on_bar(candles, params): return Decimal(''0'')', repeat('c', 64),
          'python3.12', 'spot', $5, 'BTCUSDT', '1m', 2, '{}', $6, CURRENT_TIMESTAMP)
`, versionID, strategyID, publishTaskID, recordID, instrumentID, ownerID); err != nil {
		t.Fatalf("insert M2 strategy version: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO strategy_instances (
    id, owner_user_id, strategy_version_id, name, mode, environment
) VALUES ($1, $2, $3, 'manual paper', 'manual', 'paper')
`, instanceID, ownerID, versionID); err != nil {
		t.Fatalf("insert M2 strategy instance: %v", err)
	}
	var enabled bool
	if err := database.QueryRow(`SELECT is_enabled FROM strategy_instances WHERE id = $1`, instanceID).Scan(&enabled); err != nil || enabled {
		t.Fatalf("strategy instance default enabled=%v err=%v", enabled, err)
	}
	if _, err := database.Exec(`UPDATE strategy_instances SET is_enabled = TRUE WHERE id = $1`, instanceID); err != nil {
		t.Fatalf("enable M2 strategy instance: %v", err)
	}

	payload := `{"instanceId":"` + instanceID + `","candleOpenTime":"` + candleTime + `"}`
	dedupe := "strategy.realtime:" + instanceID + ":" + candleTime
	if _, err := database.Exec(`
INSERT INTO worker_tasks (id, task_type, payload_json, lane, dedupe_key)
VALUES ($1, 'strategy.realtime', $2, 'realtime', $3)
`, realtimeTaskID, payload, dedupe); err != nil {
		t.Fatalf("insert realtime task: %v", err)
	}
	invalidTasks := []struct {
		name string
		sql  string
	}{
		{"wrong lane", `INSERT INTO worker_tasks (id, task_type, payload_json, lane, dedupe_key) VALUES ('019d1000-0000-7000-8000-000000000020', 'strategy.realtime', '` + payload + `', 'backtest', 'wrong-lane')`},
		{"missing dedupe", `INSERT INTO worker_tasks (id, task_type, payload_json) VALUES ('019d1000-0000-7000-8000-000000000021', 'strategy.realtime', '` + payload + `')`},
		{"extra payload", `INSERT INTO worker_tasks (id, task_type, payload_json, dedupe_key) VALUES ('019d1000-0000-7000-8000-000000000022', 'strategy.realtime', '{"instanceId":"` + instanceID + `","candleOpenTime":"` + candleTime + `","extra":true}', 'extra')`},
		{"duplicate dedupe", `INSERT INTO worker_tasks (id, task_type, payload_json, dedupe_key) VALUES ('019d1000-0000-7000-8000-000000000023', 'strategy.realtime', '` + payload + `', '` + dedupe + `')`},
	}
	for _, test := range invalidTasks {
		if _, err := database.Exec(test.sql); err == nil {
			t.Fatalf("realtime task schema accepted %s", test.name)
		}
	}
	if _, err := database.Exec(`INSERT INTO worker_tasks (id, task_type, payload_json) VALUES ('m2-unrelated-text', 'contract.noop', 'not-json')`); err != nil {
		t.Fatalf("realtime payload constraint affected unrelated task: %v", err)
	}

	if _, err := database.Exec(`
INSERT INTO strategy_signals (
    id, owner_user_id, strategy_instance_id, strategy_version_id, instrument_id,
    interval_code, candle_open_time, candle_close_time, target, mode, environment, expires_at
) VALUES ($1, $2, $3, $4, $5, '1m',
          TIMESTAMPTZ '2026-08-08 00:00:00+00', TIMESTAMPTZ '2026-08-08 00:01:00+00',
          0.5, 'manual', 'paper', TIMESTAMPTZ '2026-08-08 00:02:00+00')
`, signalID, ownerID, instanceID, versionID, instrumentID); err != nil {
		t.Fatalf("insert M2 signal: %v", err)
	}
	invalidSignals := []string{
		`INSERT INTO strategy_signals (id, owner_user_id, strategy_instance_id, strategy_version_id, instrument_id, interval_code, candle_open_time, candle_close_time, target, mode, environment, expires_at) VALUES ('019d1000-0000-7000-8000-000000000031', ` + fmt.Sprint(ownerID) + `, '` + instanceID + `', '` + versionID + `', '` + instrumentID + `', '1m', TIMESTAMPTZ '2026-08-08 00:00:00+00', TIMESTAMPTZ '2026-08-08 00:01:00+00', 0, 'manual', 'paper', TIMESTAMPTZ '2026-08-08 00:02:00+00')`,
		`INSERT INTO strategy_signals (id, owner_user_id, strategy_instance_id, strategy_version_id, instrument_id, interval_code, candle_open_time, candle_close_time, target, mode, environment) VALUES ('019d1000-0000-7000-8000-000000000032', ` + fmt.Sprint(ownerID) + `, '` + instanceID + `', '` + versionID + `', '` + instrumentID + `', '1m', TIMESTAMPTZ '2026-08-08 00:01:00+00', TIMESTAMPTZ '2026-08-08 00:02:00+00', 0, 'manual', 'paper')`,
		`INSERT INTO strategy_signals (id, owner_user_id, strategy_instance_id, strategy_version_id, instrument_id, interval_code, candle_open_time, candle_close_time, target, mode, environment) VALUES ('019d1000-0000-7000-8000-000000000033', ` + fmt.Sprint(ownerID) + `, '` + instanceID + `', '` + versionID + `', '` + instrumentID + `', '1m', TIMESTAMPTZ '2026-08-08 00:02:00+00', TIMESTAMPTZ '2026-08-08 00:03:00+00', 2, 'signal_only', 'paper')`,
		`INSERT INTO strategy_signals (id, owner_user_id, strategy_instance_id, strategy_version_id, instrument_id, interval_code, candle_open_time, candle_close_time, target, mode, environment, expires_at) VALUES ('019d1000-0000-7000-8000-000000000034', ` + fmt.Sprint(ownerID) + `, '` + instanceID + `', '` + versionID + `', '` + instrumentID + `', '1m', TIMESTAMPTZ '2026-08-08 00:03:00+00', TIMESTAMPTZ '2026-08-08 00:04:00+00', 0.25, 'manual', 'paper', TIMESTAMPTZ '2026-08-08 00:05:00+00')`,
	}
	for _, statement := range invalidSignals {
		if _, err := database.Exec(statement); err == nil {
			t.Fatalf("signal schema accepted invalid row: %s", statement)
		}
	}
	var decisionRecordID int64
	if err := database.QueryRow(`
INSERT INTO idempotency_records (user_id, scope, key_hash, request_hash, expires_at, created_at)
VALUES ($1, 'strategy-signal:decision:m2', repeat('d', 64), repeat('e', 64),
        CURRENT_TIMESTAMP + INTERVAL '1 day', CURRENT_TIMESTAMP)
RETURNING id
`, ownerID).Scan(&decisionRecordID); err != nil {
		t.Fatalf("insert M2 decision idempotency record: %v", err)
	}
	if _, err := database.Exec(`
UPDATE strategy_signals
SET status = 'approved', decision_idempotency_record_id = $1,
    decided_by_user_id = $2, decided_at = TIMESTAMPTZ '2026-08-08 00:01:30+00'
WHERE id = $3
`, decisionRecordID, ownerID, signalID); err != nil {
		t.Fatalf("approve M2 signal: %v", err)
	}
	if _, err := database.Exec(`UPDATE strategy_signals SET decided_at = NULL WHERE id = $1`, signalID); err == nil {
		t.Fatal("approved signal accepted an incomplete decision state")
	}
	if _, err := database.Exec(`
INSERT INTO notification_deliveries (
    strategy_signal_id, target_type, recipient_user_id, channel_type, status, title, content, created_at
) VALUES ($1, 'strategy_signal', $2, 'in_app', 'success', 'signal', 'created', CURRENT_TIMESTAMP)
`, signalID, ownerID); err != nil {
		t.Fatalf("insert M2 signal notification: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO notification_deliveries (
    strategy_signal_id, target_type, recipient_user_id, channel_type, status, title, content, created_at
) VALUES ($1, 'strategy_signal', $2, 'in_app', 'success', 'duplicate', 'duplicate', CURRENT_TIMESTAMP)
`, signalID, ownerID); err == nil {
		t.Fatal("signal accepted a duplicate in-app notification")
	}
	var externalChannelID int64
	if err := database.QueryRow(`
INSERT INTO notification_channels (
    channel_type, owner_id, display_name, is_enabled, settings_json, encrypted_secrets_json, created_at, updated_at
) VALUES ('dingtalk_webhook', $1, 'M2 migration channel', TRUE, '{}', '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
RETURNING id
`, ownerID).Scan(&externalChannelID); err != nil {
		t.Fatalf("insert M2 external notification channel: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO notification_deliveries (
    strategy_signal_id, target_type, recipient_user_id, channel_id, channel_type, status, title, content, created_at
) VALUES ($1, 'strategy_signal', $2, $3, 'dingtalk_webhook', 'failed', 'signal', 'retry', CURRENT_TIMESTAMP)
`, signalID, ownerID, externalChannelID); err != nil {
		t.Fatalf("insert M2 external signal notification: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO notification_deliveries (
    strategy_signal_id, target_type, recipient_user_id, channel_id, channel_type, status, title, content, created_at
) VALUES ($1, 'strategy_signal', $2, $3, 'dingtalk_webhook', 'pending', 'duplicate', 'duplicate', CURRENT_TIMESTAMP)
`, signalID, ownerID, externalChannelID); err == nil {
		t.Fatal("signal accepted a duplicate external channel notification")
	}
	assertPostgresIndexes(t, database, []string{
		"ix_strategy_instances_owner_enabled", "ix_strategy_signals_owner_created",
		"ux_worker_tasks_type_dedupe", "ux_notification_deliveries_in_app_signal",
		"ux_strategy_signals_manual_active_instance", "ux_notification_deliveries_signal_channel",
	})

	if _, err := runner.Down(context.Background(), 8); err != nil {
		t.Fatalf("roll back signal channel delivery migration: %v", err)
	}
	current, latest, versionErr := runner.Versions(context.Background())
	if versionErr != nil || current != 8 || latest != 16 {
		t.Fatalf("signal channel delivery rollback versions = current:%d latest:%d err:%v", current, latest, versionErr)
	}
	if _, err := runner.Down(context.Background(), 1); err == nil {
		t.Fatal("M2 decision rollback removed persistent decision data")
	}
	current, latest, versionErr = runner.Versions(context.Background())
	if versionErr != nil || current != 8 || latest != 16 {
		t.Fatalf("M2 decision rollback versions = current:%d latest:%d err:%v", current, latest, versionErr)
	}
	if _, err := database.Exec(`DELETE FROM notification_deliveries WHERE strategy_signal_id = $1`, signalID); err != nil {
		t.Fatalf("clear isolated signal notification: %v", err)
	}
	if _, err := database.Exec(`
UPDATE strategy_signals
SET status = 'active', decision_idempotency_record_id = NULL,
    decided_by_user_id = NULL, decided_at = NULL
WHERE id = $1
`, signalID); err != nil {
		t.Fatalf("clear isolated signal decision: %v", err)
	}
	if _, err := runner.Down(context.Background(), 1); err != nil {
		t.Fatalf("roll back empty M2 decision migration: %v", err)
	}
	if _, err := runner.Down(context.Background(), 1); err == nil {
		t.Fatal("M2 realtime rollback removed persistent signal data")
	}
	current, latest, versionErr = runner.Versions(context.Background())
	if versionErr != nil || current != 7 || latest != 16 {
		t.Fatalf("M2 realtime rollback versions = current:%d latest:%d err:%v", current, latest, versionErr)
	}
	assertRowCount(t, database, "SELECT COUNT(*) FROM strategy_signals", 1)
}

func TestM2PaperExecutorConstraintsAppendOnlyAndDownGuard(t *testing.T) {
	database := openPostgresSchema(t)
	runner, err := New(database)
	if err != nil {
		t.Fatalf("create migration runner: %v", err)
	}
	if _, err := runner.Up(context.Background(), 0); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	const instrumentID = "019d7000-0000-7000-8000-000000000001"
	const accountID = "019d7000-0000-7000-8000-000000000010"
	const invalidAccountID = "019d7000-0000-7000-8000-000000000011"
	const eventID = "019d7000-0000-7000-8000-000000000020"
	insertA2Instrument(t, database, instrumentID)
	var ownerID, accountRecordID, invalidRecordID int64
	if err := database.QueryRow(`INSERT INTO users (username) VALUES ('paper-owner') RETURNING id`).Scan(&ownerID); err != nil {
		t.Fatalf("insert paper owner: %v", err)
	}
	if err := database.QueryRow(`
INSERT INTO idempotency_records (user_id, scope, key_hash, request_hash, expires_at, created_at)
VALUES ($1, 'trading-account:create', repeat('a', 64), repeat('b', 64),
        CURRENT_TIMESTAMP + INTERVAL '1 day', CURRENT_TIMESTAMP)
RETURNING id
`, ownerID).Scan(&accountRecordID); err != nil {
		t.Fatalf("insert paper account idempotency record: %v", err)
	}
	if err := database.QueryRow(`
INSERT INTO idempotency_records (user_id, scope, key_hash, request_hash, expires_at, created_at)
VALUES ($1, 'trading-account:create', repeat('c', 64), repeat('d', 64),
        CURRENT_TIMESTAMP + INTERVAL '1 day', CURRENT_TIMESTAMP)
RETURNING id
`, ownerID).Scan(&invalidRecordID); err != nil {
		t.Fatalf("insert invalid paper account idempotency record: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO trading_accounts (
    id, owner_user_id, name, market_type, environment, initial_balance, paper_fee_rate,
    max_total_notional, max_symbol_notional, max_order_notional, max_daily_loss,
    max_drawdown, max_quote_age_seconds, creation_idempotency_record_id
) VALUES ($1, $2, 'paper spot', 'spot', 'paper', 10000, 0.001,
          5000, 2500, 1000, 500, 1000, 30, $3)
`, accountID, ownerID, accountRecordID); err != nil {
		t.Fatalf("insert paper account: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO trading_account_instruments (account_id, instrument_id)
VALUES ($1, $2)
`, accountID, instrumentID); err != nil {
		t.Fatalf("insert paper instrument whitelist: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO paper_balances (
    account_id, cash_balance, equity, peak_equity, day_start_date,
    day_start_equity, updated_at
) VALUES ($1, 10000, 10000, 10000, CURRENT_DATE, 10000, CURRENT_TIMESTAMP)
`, accountID); err != nil {
		t.Fatalf("insert paper balance: %v", err)
	}

	if _, err := database.Exec(`
INSERT INTO trading_accounts (
    id, owner_user_id, name, market_type, environment, initial_balance, paper_fee_rate,
    creation_idempotency_record_id
) VALUES ($1, $2, 'live forbidden', 'spot', 'live', 10000, 0.001, $3)
`, invalidAccountID, ownerID, invalidRecordID); err == nil {
		t.Fatal("paper account schema accepted a live environment")
	}
	if _, err := database.Exec(`
UPDATE trading_controls
SET emergency_stopped = FALSE, stop_reason = ''
WHERE id = 1
`); err == nil {
		t.Fatal("trading control accepted release without an actor and timestamp")
	}
	if _, err := database.Exec(`
INSERT INTO trading_events (
    event_id, account_id, instrument_id, event_type, price, amount,
    occurred_at, dedupe_key
) VALUES ($1, $2, $3, 'funding', 100, 1, CURRENT_TIMESTAMP, 'funding-contract')
`, eventID, accountID, instrumentID); err != nil {
		t.Fatalf("insert append-only funding event: %v", err)
	}
	if _, err := database.Exec(`UPDATE trading_events SET amount = 2 WHERE event_id = $1`, eventID); err == nil {
		t.Fatal("trading event accepted an update")
	}
	if _, err := database.Exec(`DELETE FROM trading_events WHERE event_id = $1`, eventID); err == nil {
		t.Fatal("trading event accepted a delete")
	}
	assertPostgresIndexes(t, database, []string{
		"ix_trading_accounts_owner", "ix_trading_intents_pending", "ix_trading_intents_owner",
		"ix_paper_orders_account", "ix_trading_events_account",
	})

	if _, err := runner.Down(context.Background(), 7); err == nil {
		t.Fatal("paper executor rollback removed persistent trading data")
	}
	current, latest, versionErr := runner.Versions(context.Background())
	if versionErr != nil || current != 10 || latest != 16 {
		t.Fatalf("paper executor rollback versions = current:%d latest:%d err:%v", current, latest, versionErr)
	}
	assertRowCount(t, database, "SELECT COUNT(*) FROM trading_accounts", 1)
	assertRowCount(t, database, "SELECT COUNT(*) FROM trading_events", 1)
}

func TestM3TestnetCredentialConstraintsAndDownGuard(t *testing.T) {
	database := openPostgresSchema(t)
	runner, err := New(database)
	if err != nil {
		t.Fatalf("create migration runner: %v", err)
	}
	if _, err := runner.Up(context.Background(), 0); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	const paperAccountID = "019d9000-0000-7000-8000-000000000010"
	const testnetAccountID = "019d9000-0000-7000-8000-000000000011"
	var ownerID, otherOwnerID int64
	if err := database.QueryRow(`INSERT INTO users (username) VALUES ('testnet-owner') RETURNING id`).Scan(&ownerID); err != nil {
		t.Fatalf("insert testnet owner: %v", err)
	}
	if err := database.QueryRow(`INSERT INTO users (username) VALUES ('testnet-other-owner') RETURNING id`).Scan(&otherOwnerID); err != nil {
		t.Fatalf("insert other testnet owner: %v", err)
	}
	createAccount := func(id, environment, keyHash string) {
		t.Helper()
		var recordID int64
		if err := database.QueryRow(`
INSERT INTO idempotency_records (user_id, scope, key_hash, request_hash, expires_at, created_at)
VALUES ($1, 'trading-account:create', $2, repeat('b', 64), CURRENT_TIMESTAMP + INTERVAL '1 day', CURRENT_TIMESTAMP)
RETURNING id
`, ownerID, keyHash).Scan(&recordID); err != nil {
			t.Fatalf("insert %s account idempotency record: %v", environment, err)
		}
		if _, err := database.Exec(`
INSERT INTO trading_accounts (
    id, owner_user_id, name, market_type, environment, initial_balance,
    paper_fee_rate, creation_idempotency_record_id
) VALUES ($1, $2, $3, 'spot', $4, 10000, 0.001, $5)
`, id, ownerID, environment+" spot", environment, recordID); err != nil {
			t.Fatalf("insert %s account: %v", environment, err)
		}
	}
	createAccount(paperAccountID, "paper", strings.Repeat("1", 64))
	createAccount(testnetAccountID, "testnet", strings.Repeat("2", 64))

	if _, err := database.Exec(`
INSERT INTO trading_account_credentials (
    id, account_id, owner_user_id, api_key_ciphertext, api_secret_ciphertext,
    withdrawal_disabled, ip_whitelist_configured
) VALUES ('019d9000-0000-7000-8000-000000000020', $1, $2, 'ciphertext-key', 'ciphertext-secret', TRUE, TRUE)
`, testnetAccountID, ownerID); err != nil {
		t.Fatalf("insert testnet credential: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO trading_account_credentials (
    id, account_id, owner_user_id, api_key_ciphertext, api_secret_ciphertext,
    withdrawal_disabled, ip_whitelist_configured
) VALUES ('019d9000-0000-7000-8000-000000000021', $1, $2, 'ciphertext-key', 'ciphertext-secret', TRUE, TRUE)
`, paperAccountID, ownerID); err == nil {
		t.Fatal("credential schema accepted a Paper account")
	}
	if _, err := database.Exec(`
UPDATE trading_account_credentials SET owner_user_id = $1 WHERE account_id = $2
`, otherOwnerID, testnetAccountID); err == nil {
		t.Fatal("credential schema accepted an owner mismatch")
	}
	if _, err := database.Exec(`
UPDATE trading_account_credentials SET ip_whitelist_configured = FALSE WHERE account_id = $1
`, testnetAccountID); err == nil {
		t.Fatal("credential schema accepted a configured credential without an IP whitelist confirmation")
	}
	if _, err := database.Exec(`
UPDATE trading_accounts SET environment = 'paper' WHERE id = $1
`, testnetAccountID); err == nil {
		t.Fatal("credential schema allowed its account to leave Testnet")
	}
	assertPostgresIndexes(t, database, []string{"ix_trading_account_credentials_owner"})

	if _, err := runner.Down(context.Background(), 5); err != nil {
		t.Fatalf("roll back empty Testnet order and reconciliation migrations: %v", err)
	}
	if _, err := runner.Down(context.Background(), 1); err == nil {
		t.Fatal("Testnet credential rollback removed persistent data")
	}
	current, latest, versionErr := runner.Versions(context.Background())
	if versionErr != nil || current != 11 || latest != 16 {
		t.Fatalf("Testnet credential rollback versions = current:%d latest:%d err:%v", current, latest, versionErr)
	}
	assertRowCount(t, database, "SELECT COUNT(*) FROM trading_account_credentials", 1)
}

func TestM3TestnetReconciliationConstraintsAndDownGuard(t *testing.T) {
	database := openPostgresSchema(t)
	runner, err := New(database)
	if err != nil {
		t.Fatalf("create migration runner: %v", err)
	}
	if _, err := runner.Up(context.Background(), 0); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	const accountID = "019da000-0000-7000-8000-000000000011"
	const credentialID = "019da000-0000-7000-8000-000000000020"
	var ownerID, recordID int64
	if err := database.QueryRow(`INSERT INTO users (username) VALUES ('testnet-reconciliation-owner') RETURNING id`).Scan(&ownerID); err != nil {
		t.Fatalf("insert reconciliation owner: %v", err)
	}
	if err := database.QueryRow(`
INSERT INTO idempotency_records (user_id, scope, key_hash, request_hash, expires_at, created_at)
VALUES ($1, 'trading-account:create', repeat('a', 64), repeat('b', 64),
        CURRENT_TIMESTAMP + INTERVAL '1 day', CURRENT_TIMESTAMP)
RETURNING id
`, ownerID).Scan(&recordID); err != nil {
		t.Fatalf("insert reconciliation idempotency record: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO trading_accounts (
    id, owner_user_id, name, market_type, environment, initial_balance,
    paper_fee_rate, leverage, creation_idempotency_record_id
) VALUES ($1, $2, 'Reconciliation USD-M', 'usd_m', 'testnet', 10000, 0.001, 1, $3)
`, accountID, ownerID, recordID); err != nil {
		t.Fatalf("insert reconciliation account: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO trading_account_credentials (
    id, account_id, owner_user_id, api_key_ciphertext, api_secret_ciphertext,
    withdrawal_disabled, ip_whitelist_configured, verification_status,
    last_verified_at, created_at, updated_at
) VALUES ($1, $2, $3, 'ciphertext-key', 'ciphertext-secret', TRUE, TRUE, 'verified',
          TIMESTAMPTZ '2026-08-09 00:00:00+00', TIMESTAMPTZ '2026-08-09 00:00:00+00',
          TIMESTAMPTZ '2026-08-09 00:00:00+00')
`, credentialID, accountID, ownerID); err != nil {
		t.Fatalf("insert reconciled credential: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO testnet_reconciliations (
    account_id, credential_updated_at, status, error_code,
    balance_count, position_count, open_order_count,
    last_attempted_at, last_observed_at, updated_at
) VALUES ($1, TIMESTAMPTZ '2026-08-09 00:00:00+00', 'mismatch', 'positions_present',
          1, 1, 1, TIMESTAMPTZ '2026-08-09 00:01:00+00',
          TIMESTAMPTZ '2026-08-09 00:01:00+00', TIMESTAMPTZ '2026-08-09 00:01:00+00')
`, accountID); err != nil {
		t.Fatalf("insert Testnet reconciliation: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO testnet_balances (
    account_id, credential_updated_at, asset, total_balance, available_balance, observed_at
) VALUES ($1, TIMESTAMPTZ '2026-08-09 00:00:00+00', 'USDT', 1000, 900,
          TIMESTAMPTZ '2026-08-09 00:01:00+00')
`, accountID); err != nil {
		t.Fatalf("insert Testnet balance: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO testnet_positions (
    account_id, credential_updated_at, native_symbol, position_side,
    quantity, entry_price, unrealized_pnl, observed_at
) VALUES ($1, TIMESTAMPTZ '2026-08-09 00:00:00+00', 'BTCUSDT', 'both',
          0.01, 50000, -2, TIMESTAMPTZ '2026-08-09 00:01:00+00')
`, accountID); err != nil {
		t.Fatalf("insert Testnet position: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO testnet_open_orders (
    account_id, credential_updated_at, native_symbol, exchange_order_id, client_order_id,
    side, order_type, status, price, original_quantity, executed_quantity, stop_price, observed_at
) VALUES ($1, TIMESTAMPTZ '2026-08-09 00:00:00+00', 'BTCUSDT', 42, 'external-order',
          'sell', 'stop_market', 'new', 0, 0.01, 0, 49000,
          TIMESTAMPTZ '2026-08-09 00:01:00+00')
`, accountID); err != nil {
		t.Fatalf("insert Testnet open order: %v", err)
	}
	if _, err := database.Exec(`
UPDATE testnet_open_orders SET original_quantity = 0 WHERE account_id = $1
`, accountID); err == nil {
		t.Fatal("Testnet open order accepted zero quantity without closePosition")
	}
	if _, err := database.Exec(`
UPDATE testnet_open_orders
SET original_quantity = 0, close_position = TRUE, working_type = 'mark_price'
WHERE account_id = $1
`, accountID); err != nil {
		t.Fatalf("set valid close-position open order: %v", err)
	}
	if _, err := runner.Down(context.Background(), 2); err == nil {
		t.Fatal("open-order shape rollback discarded persistent flags")
	}
	current, latest, versionErr := runner.Versions(context.Background())
	if versionErr != nil || current != 15 || latest != 16 {
		t.Fatalf("open-order shape rollback versions = current:%d latest:%d err:%v", current, latest, versionErr)
	}
	if _, err := database.Exec(`
UPDATE testnet_open_orders
SET original_quantity = 0.01, close_position = FALSE, working_type = ''
WHERE account_id = $1
`, accountID); err != nil {
		t.Fatalf("restore legacy open-order shape: %v", err)
	}

	if _, err := database.Exec(`
INSERT INTO testnet_balances (
    account_id, credential_updated_at, asset, total_balance, available_balance, observed_at
) VALUES ($1, TIMESTAMPTZ '2026-08-09 00:00:00+00', 'BTC', -1, 0, CURRENT_TIMESTAMP)
`, accountID); err == nil {
		t.Fatal("Testnet balance accepted a negative total")
	}
	if _, err := database.Exec(`
UPDATE trading_account_credentials
SET updated_at = TIMESTAMPTZ '2026-08-09 00:02:00+00'
WHERE account_id = $1
`, accountID); err == nil {
		t.Fatal("credential version changed while a reconciliation projection referenced it")
	}
	assertPostgresIndexes(t, database, []string{
		"ix_testnet_reconciliations_status", "ix_testnet_balances_account",
		"ix_testnet_positions_account", "ix_testnet_open_orders_account",
	})

	if _, err := runner.Down(context.Background(), 3); err != nil {
		t.Fatalf("roll back empty Testnet order migration: %v", err)
	}
	if _, err := runner.Down(context.Background(), 1); err == nil {
		t.Fatal("Testnet reconciliation rollback removed persistent projection data")
	}
	current, latest, versionErr = runner.Versions(context.Background())
	if versionErr != nil || current != 12 || latest != 16 {
		t.Fatalf("Testnet reconciliation rollback versions = current:%d latest:%d err:%v", current, latest, versionErr)
	}
	assertRowCount(t, database, "SELECT COUNT(*) FROM testnet_reconciliations", 1)
	assertRowCount(t, database, "SELECT COUNT(*) FROM testnet_open_orders", 1)
}

func TestM3TestnetOrderRiskConstraintsAndDownGuard(t *testing.T) {
	database := openPostgresSchema(t)
	runner, err := New(database)
	if err != nil {
		t.Fatalf("create migration runner: %v", err)
	}
	if _, err := runner.Up(context.Background(), 0); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	const accountID = "019db000-0000-7000-8000-000000000011"
	const credentialID = "019db000-0000-7000-8000-000000000020"
	var ownerID, recordID int64
	if err := database.QueryRow(`INSERT INTO users (username) VALUES ('testnet-order-owner') RETURNING id`).Scan(&ownerID); err != nil {
		t.Fatalf("insert Testnet order owner: %v", err)
	}
	if err := database.QueryRow(`
INSERT INTO idempotency_records (user_id, scope, key_hash, request_hash, expires_at, created_at)
VALUES ($1, 'trading-account:create', repeat('c', 64), repeat('d', 64),
        CURRENT_TIMESTAMP + INTERVAL '1 day', CURRENT_TIMESTAMP)
RETURNING id
`, ownerID).Scan(&recordID); err != nil {
		t.Fatalf("insert Testnet order idempotency record: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO trading_accounts (
    id, owner_user_id, name, market_type, environment, initial_balance,
    paper_fee_rate, creation_idempotency_record_id
) VALUES ($1, $2, 'Deterministic Spot', 'spot', 'testnet', 10000, 0.001, $3)
`, accountID, ownerID, recordID); err != nil {
		t.Fatalf("insert Testnet order account: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO trading_account_credentials (
    id, account_id, owner_user_id, api_key_ciphertext, api_secret_ciphertext,
    withdrawal_disabled, ip_whitelist_configured, verification_status,
    last_verified_at, created_at, updated_at
) VALUES ($1, $2, $3, 'ciphertext-key', 'ciphertext-secret', TRUE, TRUE, 'verified',
          TIMESTAMPTZ '2026-08-09 01:00:00+00', TIMESTAMPTZ '2026-08-09 01:00:00+00',
          TIMESTAMPTZ '2026-08-09 01:00:00+00')
`, credentialID, accountID, ownerID); err != nil {
		t.Fatalf("insert Testnet order credential: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO testnet_reconciliations (
    account_id, credential_updated_at, status, last_attempted_at, last_observed_at, updated_at
) VALUES ($1, TIMESTAMPTZ '2026-08-09 01:00:00+00', 'matched',
          TIMESTAMPTZ '2026-08-09 01:01:00+00', TIMESTAMPTZ '2026-08-09 01:01:00+00',
          TIMESTAMPTZ '2026-08-09 01:01:00+00')
`, accountID); err != nil {
		t.Fatalf("insert Testnet order reconciliation: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO testnet_risk_states (
    account_id, credential_updated_at, baseline_equity, equity, peak_equity,
    day_start_date, day_start_equity, updated_at
) VALUES ($1, TIMESTAMPTZ '2026-08-09 01:00:00+00', 1000, 900, 1000,
          DATE '2026-08-09', 1000, TIMESTAMPTZ '2026-08-09 01:01:00+00')
`, accountID); err != nil {
		t.Fatalf("insert Testnet risk state: %v", err)
	}
	for _, test := range []struct {
		name string
		sql  string
	}{
		{"negative baseline", `UPDATE testnet_risk_states SET baseline_equity = -1 WHERE account_id = '` + accountID + `'`},
		{"peak below equity", `UPDATE testnet_risk_states SET peak_equity = 899 WHERE account_id = '` + accountID + `'`},
		{"infinite UTC date", `UPDATE testnet_risk_states SET day_start_date = 'infinity' WHERE account_id = '` + accountID + `'`},
	} {
		if _, err := database.Exec(test.sql); err == nil {
			t.Fatalf("Testnet risk state accepted %s", test.name)
		}
	}
	assertPostgresIndexes(t, database, []string{
		"uq_trading_intents_testnet_account_active", "ix_trading_intents_testnet_runnable",
		"ix_testnet_orders_account", "ix_testnet_orders_recovery",
	})

	if _, err := runner.Down(context.Background(), 3); err != nil {
		t.Fatalf("roll back empty Testnet protective order migration: %v", err)
	}
	if _, err := runner.Down(context.Background(), 1); err == nil {
		t.Fatal("Testnet order rollback removed persistent risk state")
	}
	current, latest, versionErr := runner.Versions(context.Background())
	if versionErr != nil || current != 13 || latest != 16 {
		t.Fatalf("Testnet order rollback versions = current:%d latest:%d err:%v", current, latest, versionErr)
	}
	assertRowCount(t, database, "SELECT COUNT(*) FROM testnet_risk_states", 1)
	if _, err := database.Exec(`DELETE FROM testnet_risk_states WHERE account_id = $1`, accountID); err != nil {
		t.Fatalf("clear Testnet risk state: %v", err)
	}
	if _, err := runner.Down(context.Background(), 1); err != nil {
		t.Fatalf("roll back empty Testnet order migration: %v", err)
	}
	current, latest, versionErr = runner.Versions(context.Background())
	if versionErr != nil || current != 12 || latest != 16 {
		t.Fatalf("empty Testnet order rollback versions = current:%d latest:%d err:%v", current, latest, versionErr)
	}
}

func TestM3TestnetProtectiveOrderConstraintsAndDownGuard(t *testing.T) {
	database := openPostgresSchema(t)
	runner, err := New(database)
	if err != nil {
		t.Fatalf("create migration runner: %v", err)
	}
	if _, err := runner.Up(context.Background(), 0); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	const instrumentID = "019dc000-0000-7000-8000-000000000001"
	const strategyID = "019dc000-0000-7000-8000-000000000010"
	const versionID = "019dc000-0000-7000-8000-000000000011"
	const instanceID = "019dc000-0000-7000-8000-000000000012"
	insertA2Instrument(t, database, instrumentID)
	var ownerID, recordID int64
	if err := database.QueryRow(`INSERT INTO users (username) VALUES ('protective-order-owner') RETURNING id`).Scan(&ownerID); err != nil {
		t.Fatalf("insert protective order owner: %v", err)
	}
	if err := database.QueryRow(`
INSERT INTO idempotency_records (user_id, scope, key_hash, request_hash, expires_at, created_at)
VALUES ($1, 'strategy:publish:protective-order', repeat('e', 64), repeat('f', 64),
        CURRENT_TIMESTAMP + INTERVAL '1 day', CURRENT_TIMESTAMP)
RETURNING id
`, ownerID).Scan(&recordID); err != nil {
		t.Fatalf("insert protective order idempotency record: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO worker_tasks (id, task_type, payload_json)
VALUES ('protective-order-publish-task', 'contract.noop', '{}')
`); err != nil {
		t.Fatalf("insert protective order worker task: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO strategies (
    id, name, source_code, market_type, instrument_id, interval_code, lookback_bars,
    parameter_schema_json, created_by_user_id, updated_by_user_id
) VALUES ($1, 'protective order', 'def on_bar(candles, params): return Decimal(''0'')',
          'spot', $2, '1m', 2, '{}', $3, $3)
`, strategyID, instrumentID, ownerID); err != nil {
		t.Fatalf("insert protective order strategy: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO strategy_versions (
    id, strategy_id, version_number, status, worker_task_id, idempotency_record_id,
    name, source_code, code_sha256, runtime_version, market_type, instrument_id,
    symbol, interval_code, lookback_bars, parameter_schema_json, published_by_user_id
) VALUES ($1, $2, 1, 'pending', 'protective-order-publish-task', $3,
          'protective order', 'def on_bar(candles, params): return Decimal(''0'')',
          repeat('a', 64), 'python3.12', 'spot', $4, 'BTCUSDT', '1m', 2, '{}', $5)
`, versionID, strategyID, recordID, instrumentID, ownerID); err != nil {
		t.Fatalf("insert protective order strategy version: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO strategy_instances (
    id, owner_user_id, strategy_version_id, name, mode, environment
) VALUES ($1, $2, $3, 'protective order instance', 'signal_only', 'paper')
`, instanceID, ownerID, versionID); err != nil {
		t.Fatalf("insert protective order strategy instance: %v", err)
	}

	for _, value := range []string{"0", "1", "-0.01"} {
		if _, err := database.Exec(`UPDATE strategy_instances SET stop_loss_ratio = $1 WHERE id = $2`, value, instanceID); err == nil {
			t.Fatalf("strategy instance accepted invalid stop loss ratio %s", value)
		}
	}
	if _, err := database.Exec(`UPDATE strategy_instances SET stop_loss_ratio = 0.05 WHERE id = $1`, instanceID); err != nil {
		t.Fatalf("set valid stop loss ratio: %v", err)
	}
	assertPostgresIndexes(t, database, []string{"uq_testnet_orders_active_protection"})

	if _, err := runner.Down(context.Background(), 3); err == nil {
		t.Fatal("protective order rollback removed configured stop loss data")
	}
	current, latest, versionErr := runner.Versions(context.Background())
	if versionErr != nil || current != 14 || latest != 16 {
		t.Fatalf("protective order rollback versions = current:%d latest:%d err:%v", current, latest, versionErr)
	}
	if _, err := database.Exec(`UPDATE strategy_instances SET stop_loss_ratio = NULL WHERE id = $1`, instanceID); err != nil {
		t.Fatalf("clear stop loss ratio: %v", err)
	}
	if _, err := runner.Down(context.Background(), 1); err != nil {
		t.Fatalf("roll back empty protective order migration: %v", err)
	}
	current, latest, versionErr = runner.Versions(context.Background())
	if versionErr != nil || current != 13 || latest != 16 {
		t.Fatalf("empty protective order rollback versions = current:%d latest:%d err:%v", current, latest, versionErr)
	}
	var stopLossColumns int
	if err := database.QueryRow(`
SELECT COUNT(*) FROM information_schema.columns
WHERE table_schema = current_schema()
  AND table_name = 'strategy_instances'
  AND column_name = 'stop_loss_ratio'
`).Scan(&stopLossColumns); err != nil {
		t.Fatalf("inspect rolled-back stop loss column: %v", err)
	}
	if stopLossColumns != 0 {
		t.Fatal("protective order rollback retained stop_loss_ratio")
	}
}

func TestM3TestnetTradeFactConstraintsAppendOnlyAndDownGuard(t *testing.T) {
	database := openPostgresSchema(t)
	runner, err := New(database)
	if err != nil {
		t.Fatalf("create migration runner: %v", err)
	}
	if _, err := runner.Up(context.Background(), 0); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	const accountID = "019dd000-0000-7000-8000-000000000010"
	const credentialID = "019dd000-0000-7000-8000-000000000020"
	const instrumentID = "019dd000-0000-7000-8000-000000000030"
	const unlistedInstrumentID = "019dd000-0000-7000-8000-000000000031"
	var ownerID, recordID int64
	if err := database.QueryRow(`INSERT INTO users (username) VALUES ('testnet-ledger-owner') RETURNING id`).Scan(&ownerID); err != nil {
		t.Fatalf("insert Testnet ledger owner: %v", err)
	}
	if err := database.QueryRow(`
INSERT INTO idempotency_records (user_id, scope, key_hash, request_hash, expires_at, created_at)
VALUES ($1, 'trading-account:create', repeat('1', 64), repeat('2', 64),
        CURRENT_TIMESTAMP + INTERVAL '1 day', CURRENT_TIMESTAMP)
RETURNING id
`, ownerID).Scan(&recordID); err != nil {
		t.Fatalf("insert Testnet ledger idempotency record: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO market_instruments (
    id, venue, market_type, native_symbol, base_asset, quote_asset, status,
    price_tick, quantity_step, min_quantity, min_notional, updated_at
) VALUES
    ($1, 'binance', 'usd_m', 'BTCUSDT', 'BTC', 'USDT', 'trading', 0.1, 0.001, 0.001, 5,
     TIMESTAMPTZ '2026-08-10 00:00:00+00'),
    ($2, 'binance', 'usd_m', 'ETHUSDT', 'ETH', 'USDT', 'trading', 0.01, 0.001, 0.001, 5,
     TIMESTAMPTZ '2026-08-10 00:00:00+00')
`, instrumentID, unlistedInstrumentID); err != nil {
		t.Fatalf("insert Testnet ledger instruments: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO trading_accounts (
    id, owner_user_id, name, market_type, environment, initial_balance,
    paper_fee_rate, leverage, creation_idempotency_record_id
) VALUES ($1, $2, 'Ledger USD-M', 'usd_m', 'testnet', 10000, 0.001, 1, $3)
`, accountID, ownerID, recordID); err != nil {
		t.Fatalf("insert Testnet ledger account: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO trading_account_instruments (account_id, instrument_id)
VALUES ($1, $2)
`, accountID, instrumentID); err != nil {
		t.Fatalf("insert Testnet ledger whitelist: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO trading_account_credentials (
    id, account_id, owner_user_id, api_key_ciphertext, api_secret_ciphertext,
    withdrawal_disabled, ip_whitelist_configured, verification_status,
    last_verified_at, created_at, updated_at
) VALUES ($1, $2, $3, 'ciphertext-key', 'ciphertext-secret', TRUE, TRUE, 'verified',
          TIMESTAMPTZ '2026-08-10 00:00:00+00', TIMESTAMPTZ '2026-08-10 00:00:00+00',
          TIMESTAMPTZ '2026-08-10 00:00:00+00')
`, credentialID, accountID, ownerID); err != nil {
		t.Fatalf("insert Testnet ledger credential: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO testnet_trade_facts (
    account_id, credential_updated_at, instrument_id, event_type, symbol,
    external_transaction_id, amount, asset, occurred_at, dedupe_key
) VALUES ($1, TIMESTAMPTZ '2026-08-10 00:00:00+00', $2, 'funding', 'BTCUSDT',
          '9001', -0.25, 'USDT', TIMESTAMPTZ '2026-08-10 00:05:00+00', 'funding:9001')
`, accountID, instrumentID); err != nil {
		t.Fatalf("insert Testnet funding fact: %v", err)
	}

	invalidRows := []struct {
		name string
		sql  string
		args []any
	}{
		{
			name: "duplicate dedupe key",
			sql: `INSERT INTO testnet_trade_facts (
                account_id, credential_updated_at, instrument_id, event_type, symbol,
                external_transaction_id, amount, asset, occurred_at, dedupe_key
            ) VALUES ($1, TIMESTAMPTZ '2026-08-10 00:00:00+00', $2, 'funding', 'BTCUSDT',
                      '9002', -0.20, 'USDT', TIMESTAMPTZ '2026-08-10 00:06:00+00', 'funding:9001')`,
			args: []any{accountID, instrumentID},
		},
		{
			name: "unbound funding symbol",
			sql: `INSERT INTO testnet_trade_facts (
                account_id, credential_updated_at, event_type, symbol,
                external_transaction_id, amount, asset, occurred_at, dedupe_key
            ) VALUES ($1, TIMESTAMPTZ '2026-08-10 00:00:00+00', 'funding', 'BTCUSDT',
                      '9003', -0.20, 'USDT', TIMESTAMPTZ '2026-08-10 00:07:00+00', 'funding:9003')`,
			args: []any{accountID},
		},
		{
			name: "instrument symbol mismatch",
			sql: `INSERT INTO testnet_trade_facts (
                account_id, credential_updated_at, instrument_id, event_type, symbol,
                external_transaction_id, amount, asset, occurred_at, dedupe_key
            ) VALUES ($1, TIMESTAMPTZ '2026-08-10 00:00:00+00', $2, 'funding', 'ETHUSDT',
                      '9004', -0.20, 'USDT', TIMESTAMPTZ '2026-08-10 00:08:00+00', 'funding:9004')`,
			args: []any{accountID, instrumentID},
		},
		{
			name: "instrument outside account whitelist",
			sql: `INSERT INTO testnet_trade_facts (
                account_id, credential_updated_at, instrument_id, event_type, symbol,
                external_transaction_id, amount, asset, occurred_at, dedupe_key
            ) VALUES ($1, TIMESTAMPTZ '2026-08-10 00:00:00+00', $2, 'funding', 'ETHUSDT',
                      '9005', -0.20, 'USDT', TIMESTAMPTZ '2026-08-10 00:09:00+00', 'funding:9005')`,
			args: []any{accountID, unlistedInstrumentID},
		},
		{
			name: "fill without managed order",
			sql: `INSERT INTO testnet_trade_facts (
                account_id, credential_updated_at, event_type, symbol, external_trade_id,
                side, position_side, quantity, price, quote_quantity, asset, occurred_at, dedupe_key
            ) VALUES ($1, TIMESTAMPTZ '2026-08-10 00:00:00+00', 'fill', 'BTCUSDT', 42,
                      'buy', 'both', 0.01, 50000, 500, 'BTC',
                      TIMESTAMPTZ '2026-08-10 00:10:00+00', 'trade:BTCUSDT:42:fill')`,
			args: []any{accountID},
		},
	}
	for _, test := range invalidRows {
		if _, err := database.Exec(test.sql, test.args...); err == nil {
			t.Fatalf("Testnet trade fact schema accepted %s", test.name)
		}
	}
	if _, err := database.Exec(`UPDATE testnet_trade_facts SET amount = -0.30 WHERE dedupe_key = 'funding:9001'`); err == nil {
		t.Fatal("Testnet trade fact accepted an update")
	}
	if _, err := database.Exec(`DELETE FROM testnet_trade_facts WHERE dedupe_key = 'funding:9001'`); err == nil {
		t.Fatal("Testnet trade fact accepted a delete")
	}
	assertPostgresIndexes(t, database, []string{
		"ix_testnet_trade_facts_account", "ix_testnet_trade_facts_order", "uq_testnet_trade_facts_dedupe",
	})

	if _, err := runner.Down(context.Background(), 1); err == nil {
		t.Fatal("Testnet trade fact rollback removed persistent ledger data")
	}
	current, latest, versionErr := runner.Versions(context.Background())
	if versionErr != nil || current != 16 || latest != 16 {
		t.Fatalf("Testnet trade fact rollback versions = current:%d latest:%d err:%v", current, latest, versionErr)
	}
	assertRowCount(t, database, "SELECT COUNT(*) FROM testnet_trade_facts", 1)
}

func assertCurrentTables(t *testing.T, database *sql.DB) {
	t.Helper()
	want := []string{
		"ai_model_agent_bindings", "ai_model_configs", "assistant_agents", "assistant_messages", "audit_records",
		"assistant_sessions", "domain_event_outbox", "i18n_texts", "idempotency_records", "menu_buttons", "menus",
		"market_candles", "market_instruments", "market_ticker_snapshots", "watchlist_items",
		"news_items", "notification_channels", "notification_deliveries", "backtests", "role_menu_buttons",
		"paper_balances", "paper_orders", "paper_positions", "role_menus", "roles", "schema_migrations",
		"task_definition_configs", "testnet_balances", "testnet_open_orders", "testnet_orders", "testnet_positions",
		"testnet_reconciliations", "testnet_risk_states", "testnet_trade_facts",
		"trading_account_instruments", "trading_accounts", "trading_controls",
		"trading_account_credentials", "trading_events", "trading_intents", "user_roles", "users",
		"strategies", "strategy_instances", "strategy_signals", "strategy_versions",
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

func assertA2Columns(t *testing.T, database *sql.DB) {
	t.Helper()
	type columnSpec struct {
		dataType  string
		length    int64
		precision int64
		scale     int64
	}
	expected := map[string]columnSpec{
		"market_instruments.id":                  {dataType: "uuid"},
		"market_instruments.venue":               {dataType: "character varying", length: 16},
		"market_instruments.market_type":         {dataType: "character varying", length: 32},
		"market_instruments.native_symbol":       {dataType: "character varying", length: 64},
		"market_instruments.base_asset":          {dataType: "character varying", length: 32},
		"market_instruments.quote_asset":         {dataType: "character varying", length: 32},
		"market_instruments.status":              {dataType: "character varying", length: 16},
		"market_instruments.price_tick":          {dataType: "numeric", precision: 38, scale: 18},
		"market_instruments.quantity_step":       {dataType: "numeric", precision: 38, scale: 18},
		"market_instruments.min_quantity":        {dataType: "numeric", precision: 38, scale: 18},
		"market_instruments.min_notional":        {dataType: "numeric", precision: 38, scale: 18},
		"market_instruments.updated_at":          {dataType: "timestamp with time zone"},
		"market_candles.venue":                   {dataType: "character varying", length: 16},
		"market_candles.instrument_id":           {dataType: "uuid"},
		"market_candles.interval_code":           {dataType: "character varying", length: 4},
		"market_candles.open_time":               {dataType: "timestamp with time zone"},
		"market_candles.close_time":              {dataType: "timestamp with time zone"},
		"market_candles.open_price":              {dataType: "numeric", precision: 38, scale: 18},
		"market_candles.high_price":              {dataType: "numeric", precision: 38, scale: 18},
		"market_candles.low_price":               {dataType: "numeric", precision: 38, scale: 18},
		"market_candles.close_price":             {dataType: "numeric", precision: 38, scale: 18},
		"market_candles.base_volume":             {dataType: "numeric", precision: 38, scale: 18},
		"market_candles.is_closed":               {dataType: "boolean"},
		"market_ticker_snapshots.venue":          {dataType: "character varying", length: 16},
		"market_ticker_snapshots.instrument_id":  {dataType: "uuid"},
		"market_ticker_snapshots.occurred_at":    {dataType: "timestamp with time zone"},
		"market_ticker_snapshots.last_price":     {dataType: "numeric", precision: 38, scale: 18},
		"market_ticker_snapshots.best_bid_price": {dataType: "numeric", precision: 38, scale: 18},
		"market_ticker_snapshots.best_ask_price": {dataType: "numeric", precision: 38, scale: 18},
	}
	rows, err := database.Query(`
SELECT
    table_name,
    column_name,
    data_type,
    COALESCE(character_maximum_length, 0),
    COALESCE(numeric_precision, 0),
    COALESCE(numeric_scale, 0),
    is_nullable,
    column_default
FROM information_schema.columns
WHERE table_schema = current_schema()
  AND table_name IN ('market_instruments', 'market_candles', 'market_ticker_snapshots')
`)
	if err != nil {
		t.Fatalf("list A2 columns: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var table, column, dataType, nullable string
		var length, precision, scale int64
		var defaultValue sql.NullString
		if err := rows.Scan(&table, &column, &dataType, &length, &precision, &scale, &nullable, &defaultValue); err != nil {
			t.Fatalf("scan A2 column: %v", err)
		}
		key := table + "." + column
		want, ok := expected[key]
		if !ok {
			t.Fatalf("unexpected A2 column %s", key)
		}
		if dataType != want.dataType || length != want.length || precision != want.precision || scale != want.scale || nullable != "NO" || defaultValue.Valid {
			t.Fatalf("A2 column %s = type:%s length:%d precision:%d scale:%d nullable:%s default:%q", key, dataType, length, precision, scale, nullable, defaultValue.String)
		}
		delete(expected, key)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate A2 columns: %v", err)
	}
	if len(expected) != 0 {
		t.Fatalf("missing A2 columns: %v", expected)
	}
}

// assertA2TimescaleLifecycle 固定 hypertable 与两个独立后台策略，避免物理生命周期静默漂移。
func assertA2TimescaleLifecycle(t *testing.T, database *sql.DB) {
	t.Helper()
	var extensionReady, hypertableReady, dimensionReady bool
	if err := database.QueryRow(`
SELECT
    EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'timescaledb'),
    EXISTS (
        SELECT 1
        FROM timescaledb_information.hypertables
        WHERE hypertable_schema = current_schema()
          AND hypertable_name = 'market_candles'
          AND num_dimensions = 1
          AND compression_enabled
          AND primary_dimension = 'open_time'
    ),
    EXISTS (
        SELECT 1
        FROM timescaledb_information.dimensions
        WHERE hypertable_schema = current_schema()
          AND hypertable_name = 'market_candles'
          AND dimension_number = 1
          AND column_name = 'open_time'
          AND time_interval = INTERVAL '7 days'
    )
`).Scan(&extensionReady, &hypertableReady, &dimensionReady); err != nil {
		t.Fatalf("inspect A2 Timescale lifecycle: %v", err)
	}
	if !extensionReady || !hypertableReady || !dimensionReady {
		t.Fatalf("A2 Timescale lifecycle = extension:%t hypertable:%t dimension:%t", extensionReady, hypertableReady, dimensionReady)
	}

	var columnstorePolicy, retentionPolicy, policyCountReady bool
	if err := database.QueryRow(`
SELECT
    COUNT(*) FILTER (
        WHERE proc_name = 'policy_compression'
          AND scheduled
          AND (config ->> 'compress_after')::interval = INTERVAL '30 days'
    ) = 1,
    COUNT(*) FILTER (
        WHERE proc_name = 'policy_retention'
          AND scheduled
          AND (config ->> 'drop_after')::interval = INTERVAL '2 years'
    ) = 1,
    COUNT(*) = 2
FROM timescaledb_information.jobs
WHERE hypertable_schema = current_schema()
  AND hypertable_name = 'market_candles'
`).Scan(&columnstorePolicy, &retentionPolicy, &policyCountReady); err != nil {
		t.Fatalf("inspect A2 Timescale policies: %v", err)
	}
	if !columnstorePolicy || !retentionPolicy || !policyCountReady {
		t.Fatalf("A2 Timescale policies = columnstore:%t retention:%t count:%t", columnstorePolicy, retentionPolicy, policyCountReady)
	}

	var indexCount int
	var primaryIndexOnly bool
	if err := database.QueryRow(`
SELECT COUNT(*), BOOL_AND(indexname = 'market_candles_pkey')
FROM pg_indexes
WHERE schemaname = current_schema() AND tablename = 'market_candles'
`).Scan(&indexCount, &primaryIndexOnly); err != nil {
		t.Fatalf("inspect A2 candle indexes: %v", err)
	}
	if indexCount != 1 || !primaryIndexOnly {
		t.Fatalf("A2 candle indexes = count:%d primary-only:%t", indexCount, primaryIndexOnly)
	}
}

func insertA2Instrument(t *testing.T, database *sql.DB, id string) {
	t.Helper()
	if _, err := database.Exec(`
INSERT INTO market_instruments (
    id, venue, market_type, native_symbol, base_asset, quote_asset, status,
    price_tick, quantity_step, min_quantity, min_notional, updated_at
) VALUES ($1, 'binance', 'spot', 'BTCUSDT', 'BTC', 'USDT', 'trading', 0.1, 0.001, 0.001, 5, TIMESTAMPTZ '2026-08-01 00:00:00+00')
`, id); err != nil {
		t.Fatalf("insert A2 instrument: %v", err)
	}
}

func assertA2Tables(t *testing.T, database *sql.DB) {
	t.Helper()
	rows, err := database.Query(`
SELECT table_name
FROM information_schema.tables
WHERE table_schema = current_schema()
  AND table_type = 'BASE TABLE'
  AND table_name IN ('market_instruments', 'market_candles', 'market_ticker_snapshots')
ORDER BY table_name
`)
	if err != nil {
		t.Fatalf("list A2 tables: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatalf("scan A2 table: %v", err)
		}
		got = append(got, table)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate A2 tables: %v", err)
	}
	want := []string{"market_candles", "market_instruments", "market_ticker_snapshots"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("A2 tables = %v, want %v", got, want)
	}
}

func assertRowCount(t *testing.T, database *sql.DB, query string, want int) {
	t.Helper()
	var got int
	if err := database.QueryRow(query).Scan(&got); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if got != want {
		t.Fatalf("row count = %d, want %d", got, want)
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
