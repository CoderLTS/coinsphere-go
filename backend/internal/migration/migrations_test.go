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
	if len(results) != 3 || results[0].Version != 1 || results[1].Version != 2 || results[2].Version != 3 ||
		results[0].Direction != "up" || results[1].Direction != "up" || results[2].Direction != "up" {
		t.Fatalf("migration results = %#v", results)
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

	results, err = runner.Down(context.Background(), 3)
	if err != nil {
		t.Fatalf("roll back empty migrations: %v", err)
	}
	if len(results) != 3 || results[0].Version != 3 || results[1].Version != 2 || results[2].Version != 1 ||
		results[0].Direction != "down" || results[1].Direction != "down" || results[2].Direction != "down" {
		t.Fatalf("migration rollback results = %#v", results)
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
	if _, err := runner.Down(context.Background(), 1); err != nil {
		t.Fatalf("roll back empty A2 migration: %v", err)
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
	if versionErr != nil || current != 1 || latest != 3 {
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
		{"audit invalid request id", `INSERT INTO audit_records (request_id, action, resource_path, outcome, status_code) VALUES ('bad request id', 'POST /api/test', '/api/test', 'failure', 400)`},
		{"audit invalid outcome", `INSERT INTO audit_records (request_id, action, resource_path, outcome, status_code) VALUES ('audit-invalid-outcome', 'POST /api/test', '/api/test', 'unknown', 400)`},
		{"audit invalid status", `INSERT INTO audit_records (request_id, action, resource_path, outcome, status_code) VALUES ('audit-invalid-status', 'POST /api/test', '/api/test', 'failure', 99)`},
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
	if _, err := runner.Down(context.Background(), 1); err != nil {
		t.Fatalf("roll back empty A2 migration: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO audit_records (request_id, action, resource_path, outcome, status_code) VALUES ('rollback-guard', 'POST /api/test', '/api/test', 'success', 200)`); err != nil {
		t.Fatalf("insert audit rollback guard: %v", err)
	}

	if _, err := runner.Down(context.Background(), 1); err == nil {
		t.Fatal("observability rollback removed persistent audit data")
	}
	current, latest, versionErr := runner.Versions(context.Background())
	if versionErr != nil || current != 2 || latest != 3 {
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
	if _, err := runner.Down(context.Background(), 2); err != nil {
		t.Fatalf("roll back empty A2 and observability migrations: %v", err)
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
	if versionErr != nil || current != 1 || latest != 3 {
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
	if _, err := database.Exec(`INSERT INTO schema_migrations (version_id, is_applied) VALUES (4, TRUE)`); err != nil {
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
		{"candle interval enum", `INSERT INTO market_candles (venue, instrument_id, interval_code, open_time, close_time, open_price, high_price, low_price, close_price, base_volume, is_closed) VALUES ('binance', '019c2f6d-7c00-7000-8000-000000000001', '2m', TIMESTAMPTZ '2026-08-01 00:00:00+00', TIMESTAMPTZ '2026-08-01 00:01:00+00', 100, 101, 99, 100, 1, true)`},
		{"candle UTC interval alignment", `INSERT INTO market_candles (venue, instrument_id, interval_code, open_time, close_time, open_price, high_price, low_price, close_price, base_volume, is_closed) VALUES ('binance', '019c2f6d-7c00-7000-8000-000000000001', '1m', TIMESTAMPTZ '2026-08-01 00:00:30+00', TIMESTAMPTZ '2026-08-01 00:01:30+00', 100, 101, 99, 100, 1, true)`},
		{"candle exclusive close time", `INSERT INTO market_candles (venue, instrument_id, interval_code, open_time, close_time, open_price, high_price, low_price, close_price, base_volume, is_closed) VALUES ('binance', '019c2f6d-7c00-7000-8000-000000000001', '1m', TIMESTAMPTZ '2026-08-01 00:06:00+00', TIMESTAMPTZ '2026-08-01 00:06:59+00', 100, 101, 99, 100, 1, true)`},
		{"candle decimal price", `INSERT INTO market_candles (venue, instrument_id, interval_code, open_time, close_time, open_price, high_price, low_price, close_price, base_volume, is_closed) VALUES ('binance', '019c2f6d-7c00-7000-8000-000000000001', '1m', TIMESTAMPTZ '2026-08-01 00:04:00+00', TIMESTAMPTZ '2026-08-01 00:05:00+00', 0, 101, 0, 100, 1, true)`},
		{"candle negative base volume", `INSERT INTO market_candles (venue, instrument_id, interval_code, open_time, close_time, open_price, high_price, low_price, close_price, base_volume, is_closed) VALUES ('binance', '019c2f6d-7c00-7000-8000-000000000001', '1m', TIMESTAMPTZ '2026-08-01 00:07:00+00', TIMESTAMPTZ '2026-08-01 00:08:00+00', 100, 101, 99, 100, -1, true)`},
		{"candle OHLC", `INSERT INTO market_candles (venue, instrument_id, interval_code, open_time, close_time, open_price, high_price, low_price, close_price, base_volume, is_closed) VALUES ('binance', '019c2f6d-7c00-7000-8000-000000000001', '1m', TIMESTAMPTZ '2026-08-01 00:01:00+00', TIMESTAMPTZ '2026-08-01 00:02:00+00', 100, 99, 98, 100, 1, true)`},
		{"candle foreign key", `INSERT INTO market_candles (venue, instrument_id, interval_code, open_time, close_time, open_price, high_price, low_price, close_price, base_volume, is_closed) VALUES ('okx', '019c2f6d-7c00-7000-8000-000000000001', '1m', TIMESTAMPTZ '2026-08-01 00:02:00+00', TIMESTAMPTZ '2026-08-01 00:03:00+00', 100, 101, 99, 100, 1, true)`},
		{"ticker foreign key", `INSERT INTO market_ticker_snapshots (venue, instrument_id, occurred_at, last_price, best_bid_price, best_ask_price) VALUES ('okx', '019c2f6d-7c00-7000-8000-000000000001', TIMESTAMPTZ '2026-08-01 00:03:30+00', 100, 99, 101)`},
		{"ticker non-finite occurred at", `INSERT INTO market_ticker_snapshots (venue, instrument_id, occurred_at, last_price, best_bid_price, best_ask_price) VALUES ('binance', '019c2f6d-7c00-7000-8000-000000000001', TIMESTAMPTZ 'infinity', 100, 99, 101)`},
		{"ticker non-positive price", `INSERT INTO market_ticker_snapshots (venue, instrument_id, occurred_at, last_price, best_bid_price, best_ask_price) VALUES ('binance', '019c2f6d-7c00-7000-8000-000000000001', TIMESTAMPTZ '2026-08-01 00:03:30+00', 0, 99, 101)`},
		{"ticker spread", `INSERT INTO market_ticker_snapshots (venue, instrument_id, occurred_at, last_price, best_bid_price, best_ask_price) VALUES ('binance', '019c2f6d-7c00-7000-8000-000000000001', TIMESTAMPTZ '2026-08-01 00:03:30+00', 100, 101, 99)`},
	}
	for _, test := range invalidRows {
		if _, err := database.Exec(test.sql); err == nil {
			t.Fatalf("A2 schema accepted %s", test.name)
		}
	}

	const replacementID = "019c2f6d-7c00-7000-8000-000000000007"
	if _, err := database.Exec(`
INSERT INTO market_instruments (
    id, venue, market_type, native_symbol, base_asset, quote_asset, status, price_tick, quantity_step
) VALUES ($1, 'binance', 'spot', 'BTCUSDT', 'BTC', 'USDT', 'suspended', 0.2, 0.01)
ON CONFLICT (venue, market_type, native_symbol) DO UPDATE SET
    base_asset = EXCLUDED.base_asset,
    quote_asset = EXCLUDED.quote_asset,
    status = EXCLUDED.status,
    price_tick = EXCLUDED.price_tick,
    quantity_step = EXCLUDED.quantity_step
`, replacementID); err != nil {
		t.Fatalf("upsert instrument metadata: %v", err)
	}
	var storedID string
	var instrumentUpdated bool
	if err := database.QueryRow(`
SELECT id::text, price_tick = 0.2 AND quantity_step = 0.01 AND status = 'suspended'
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

// A2 Down 在单事务中先锁定三表再计数，失败时必须保持 schema 与版本。
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

			if _, err := runner.Down(context.Background(), 1); err == nil {
				t.Fatalf("A2 rollback removed non-empty %s", test.table)
			}
			current, latest, versionErr := runner.Versions(context.Background())
			if versionErr != nil || current != 3 || latest != 3 {
				t.Fatalf("A2 rollback versions = current:%d latest:%d err:%v", current, latest, versionErr)
			}
			assertA2Tables(t, database)
			assertRowCount(t, database, fmt.Sprintf("SELECT COUNT(*) FROM %s", test.table), 1)
		})
	}
}

func assertCurrentTables(t *testing.T, database *sql.DB) {
	t.Helper()
	want := []string{
		"ai_model_agent_bindings", "ai_model_configs", "assistant_agents", "assistant_messages", "audit_records",
		"assistant_sessions", "domain_event_outbox", "i18n_texts", "menu_buttons", "menus",
		"market_candles", "market_instruments", "market_ticker_snapshots",
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

func insertA2Instrument(t *testing.T, database *sql.DB, id string) {
	t.Helper()
	if _, err := database.Exec(`
INSERT INTO market_instruments (
    id, venue, market_type, native_symbol, base_asset, quote_asset, status, price_tick, quantity_step
) VALUES ($1, 'binance', 'spot', 'BTCUSDT', 'BTC', 'USDT', 'trading', 0.1, 0.001)
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
