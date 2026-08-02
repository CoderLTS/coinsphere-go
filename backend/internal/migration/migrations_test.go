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

func TestOutboxLegacyUpgradeSQLite(t *testing.T) {
	runOutboxLegacyUpgrade(t, "sqlite", func(t *testing.T) *sql.DB { return openSQLite(t) })
}

func TestOutboxLegacyUpgradePostgres(t *testing.T) {
	dsn := postgresTestDSN(t)
	runOutboxLegacyUpgrade(t, "postgres", func(t *testing.T) *sql.DB {
		return openPostgresSchema(t, dsn)
	})
}

// runOutboxLegacyUpgrade 从 00002 版本和精确旧 Outbox 形态出发，验证事件、通知入站引用、
// 旧 failed 终态及既有尝试次数在 00003 Up 前后保持不变。异常旧行必须使整个文件原子失败。
func runOutboxLegacyUpgrade(t *testing.T, driver string, open databaseFactory) {
	t.Helper()
	db := open(t)
	ctx := context.Background()
	runner, err := migration.New(db, driver)
	if err != nil {
		t.Fatalf("create legacy-upgrade migration runner: %v", err)
	}
	if _, err := runner.Up(ctx, 2); err != nil {
		t.Fatalf("upgrade legacy database to version 2: %v", err)
	}
	createLegacyOutboxSchema(t, ctx, db, driver)

	if _, err := db.ExecContext(ctx, `
INSERT INTO workflow_executions (id) VALUES (101)
`); err != nil {
		t.Fatalf("insert workflow fixture: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO workflow_execution_nodes (id) VALUES (201)
`); err != nil {
		t.Fatalf("insert workflow node fixture: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO domain_event_outbox (
    id, event_type, aggregate_type, aggregate_id,
    workflow_execution_id, workflow_execution_node_id,
    payload_json, metadata_json, status, attempt_count,
    available_at, processed_at, last_error_message, created_at, updated_at
) VALUES
    (1, 'contract.pending', 'contract', 'pending', 101, 201, '{}', '{}', 'pending', 0,
        CURRENT_TIMESTAMP, NULL, '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
    (2, 'contract.processed', 'contract', 'processed', 101, 201, '{}', '{}', 'processed', 1,
        CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
    (3, 'contract.failed', 'contract', 'failed', 101, 201, '{}', '{}', 'failed', 5,
        CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'redacted-test-error', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
`); err != nil {
		t.Fatalf("insert legacy outbox fixtures: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO notification_deliveries (id, outbox_event_id) VALUES (301, 2)
`); err != nil {
		t.Fatalf("insert notification reference fixture: %v", err)
	}

	results, err := runner.Up(ctx, 0)
	if err != nil {
		t.Fatalf("upgrade legacy outbox to version 3: %v", err)
	}
	if len(results) != 1 || results[0].Version != 3 {
		t.Fatalf("unexpected legacy outbox upgrade results: %+v", results)
	}
	assertVersions(t, ctx, runner, 3, 3)

	var rowCount, failedAttempts, failedMaxAttempts int
	if err := db.QueryRowContext(ctx, `
SELECT COUNT(*),
       MAX(CASE WHEN status = 'failed' THEN attempt_count ELSE 0 END),
       MAX(CASE WHEN status = 'failed' THEN max_attempts ELSE 0 END)
FROM domain_event_outbox
`).Scan(&rowCount, &failedAttempts, &failedMaxAttempts); err != nil {
		t.Fatalf("inspect upgraded outbox fixtures: %v", err)
	}
	if rowCount != 3 || failedAttempts != 5 || failedMaxAttempts != 5 {
		t.Fatalf(
			"legacy outbox data changed: rows=%d failedAttempts=%d failedMaxAttempts=%d",
			rowCount,
			failedAttempts,
			failedMaxAttempts,
		)
	}
	var referencedEventID int64
	if err := db.QueryRowContext(ctx, `
SELECT outbox_event_id FROM notification_deliveries WHERE id = 301
`).Scan(&referencedEventID); err != nil || referencedEventID != 2 {
		t.Fatalf("notification outbox reference changed: id=%d err=%v", referencedEventID, err)
	}
	if driver == "sqlite" {
		var foreignKeyViolationCount int
		rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
		if err != nil {
			t.Fatalf("check SQLite foreign keys after Outbox upgrade: %v", err)
		}
		for rows.Next() {
			foreignKeyViolationCount++
		}
		if err := rows.Close(); err != nil {
			t.Fatalf("close SQLite foreign-key check rows: %v", err)
		}
		if foreignKeyViolationCount != 0 {
			t.Fatalf("Outbox upgrade left %d SQLite foreign-key violations", foreignKeyViolationCount)
		}
	}

	// 任意旧事件存在时 Down 必须保留表、事件、入站引用与 migration 版本。
	if _, err := runner.Down(ctx, 1); err == nil {
		t.Fatal("expected legacy Outbox data to block version 3 Down")
	}
	assertVersions(t, ctx, runner, 3, 3)
	if err := db.QueryRowContext(ctx, `
SELECT outbox_event_id FROM notification_deliveries WHERE id = 301
`).Scan(&referencedEventID); err != nil || referencedEventID != 2 {
		t.Fatalf("failed Down changed notification reference: id=%d err=%v", referencedEventID, err)
	}
	if driver == "postgres" {
		assertConcurrentInsertBlocksOutboxDown(t, ctx, db, runner)
	}

	// 在独立数据库重复旧基线，并注入无法推断语义的终态缺时间行，验证 Up 失败不留部分列或推进版本。
	invalidDB := open(t)
	invalidRunner, err := migration.New(invalidDB, driver)
	if err != nil {
		t.Fatalf("create invalid legacy migration runner: %v", err)
	}
	if _, err := invalidRunner.Up(ctx, 2); err != nil {
		t.Fatalf("upgrade invalid legacy database to version 2: %v", err)
	}
	createLegacyOutboxSchema(t, ctx, invalidDB, driver)
	if _, err := invalidDB.ExecContext(ctx, `
INSERT INTO domain_event_outbox (
    status, attempt_count, available_at, processed_at, created_at, updated_at
) VALUES ('failed', 1, CURRENT_TIMESTAMP, NULL, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
`); err != nil {
		t.Fatalf("insert invalid legacy outbox fixture: %v", err)
	}
	if _, err := invalidRunner.Up(ctx, 0); err == nil {
		t.Fatal("expected invalid legacy Outbox row to fail version 3 Up")
	}
	assertVersions(t, ctx, invalidRunner, 2, 3)
	if _, err := invalidDB.ExecContext(ctx, "SELECT max_attempts FROM domain_event_outbox"); err == nil {
		t.Fatal("failed Outbox Up left partial version 3 columns")
	}

	// 使用没有事件、但仍有 notification_deliveries 入站外键的旧库验证成功回滚重放。
	// SQLite Down 会临时改名 Outbox；每一步都必须把外键目标保持为最终表名，禁止遗留候选表引用。
	replayDB := open(t)
	replayRunner, err := migration.New(replayDB, driver)
	if err != nil {
		t.Fatalf("create Outbox rollback-replay migration runner: %v", err)
	}
	if _, err := replayRunner.Up(ctx, 2); err != nil {
		t.Fatalf("upgrade Outbox rollback-replay database to version 2: %v", err)
	}
	createLegacyOutboxSchema(t, ctx, replayDB, driver)
	assertNotificationOutboxForeignKey(t, ctx, replayDB, driver)
	if _, err := replayRunner.Up(ctx, 0); err != nil {
		t.Fatalf("upgrade Outbox rollback-replay database to version 3: %v", err)
	}
	assertNotificationOutboxForeignKey(t, ctx, replayDB, driver)
	if _, err := replayRunner.Down(ctx, 1); err != nil {
		t.Fatalf("rollback empty Outbox with inbound foreign key: %v", err)
	}
	assertNotificationOutboxForeignKey(t, ctx, replayDB, driver)
	if _, err := replayRunner.Up(ctx, 0); err != nil {
		t.Fatalf("reapply Outbox with inbound foreign key: %v", err)
	}
	assertNotificationOutboxForeignKey(t, ctx, replayDB, driver)
}

func assertConcurrentInsertBlocksOutboxDown(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	runner *migration.Runner,
) {
	t.Helper()

	if _, err := db.ExecContext(ctx, "DELETE FROM notification_deliveries"); err != nil {
		t.Fatalf("delete notification fixture before concurrent Outbox Down: %v", err)
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM domain_event_outbox"); err != nil {
		t.Fatalf("delete Outbox fixtures before concurrent Down: %v", err)
	}

	writer, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin concurrent Outbox insert: %v", err)
	}
	if _, err := writer.ExecContext(ctx, `
INSERT INTO domain_event_outbox (
    event_type, status, attempt_count, available_at, created_at, updated_at
) VALUES ('contract.concurrent', 'pending', 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
`); err != nil {
		_ = writer.Rollback()
		t.Fatalf("insert uncommitted Outbox event: %v", err)
	}

	downCtx, cancelDown := context.WithTimeout(ctx, 5*time.Second)
	defer cancelDown()
	downDone := make(chan error, 1)
	go func() {
		_, err := runner.Down(downCtx, 1)
		downDone <- err
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		err := db.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM pg_locks AS locks
    JOIN pg_class AS tables ON tables.oid = locks.relation
    JOIN pg_namespace AS schemas ON schemas.oid = tables.relnamespace
    WHERE schemas.nspname = current_schema()
      AND tables.relname = 'domain_event_outbox'
      AND locks.mode = 'AccessExclusiveLock'
      AND NOT locks.granted
)
`).Scan(&waiting)
		if err != nil {
			_ = writer.Rollback()
			t.Fatalf("inspect concurrent Outbox migration lock: %v", err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			_ = writer.Rollback()
			t.Fatal("Outbox Down did not wait for the concurrent writer")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := writer.Commit(); err != nil {
		t.Fatalf("commit concurrent Outbox insert: %v", err)
	}
	select {
	case err := <-downDone:
		if err == nil {
			t.Fatal("concurrent Outbox event was deleted by Down")
		}
	case <-downCtx.Done():
		t.Fatalf("Outbox Down did not finish: %v", downCtx.Err())
	}
	assertVersions(t, ctx, runner, 3, 3)
	var eventCount int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM domain_event_outbox").Scan(&eventCount); err != nil || eventCount != 1 {
		t.Fatalf("concurrent Outbox Down changed events: count=%d err=%v", eventCount, err)
	}
}

func runEmbeddedMigrations(t *testing.T, driver string, db *sql.DB) {
	t.Helper()
	runner, err := migration.New(db, driver)
	if err != nil {
		t.Fatalf("create embedded migration runner: %v", err)
	}

	ctx := context.Background()
	assertVersions(t, ctx, runner, 0, 3)

	results, err := runner.Up(ctx, 0)
	if err != nil {
		t.Fatalf("upgrade embedded migrations: %v", err)
	}
	if len(results) != 3 || results[0].Version != 1 || results[1].Version != 2 || results[2].Version != 3 {
		t.Fatalf("unexpected embedded up results: %+v", results)
	}
	assertVersions(t, ctx, runner, 3, 3)
	assertWorkerTaskSchema(t, ctx, db)
	assertOutboxSchema(t, ctx, db)
	assertOutboxIndexes(t, ctx, db, driver)

	results, err = runner.Up(ctx, 0)
	if err != nil {
		t.Fatalf("repeat embedded upgrade: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected idempotent embedded upgrade, got %+v", results)
	}

	// 专项 Outbox 断言已经插入合法事件；00003 Down 必须在锁内 fail-closed，不能静默删除事件。
	if _, err := runner.Down(ctx, 1); err == nil {
		t.Fatal("expected non-empty outbox table to block rollback")
	}
	assertVersions(t, ctx, runner, 3, 3)
	assertOutboxFixturesPreserved(t, ctx, db)
	if _, err := db.ExecContext(ctx, "DELETE FROM domain_event_outbox"); err != nil {
		t.Fatalf("delete outbox fixtures: %v", err)
	}

	rolledBack, err := runner.Down(ctx, 1)
	if err != nil {
		t.Fatalf("rollback outbox migration: %v", err)
	}
	if len(rolledBack) != 1 || rolledBack[0].Version != 3 {
		t.Fatalf("unexpected outbox down results: %+v", rolledBack)
	}
	assertVersions(t, ctx, runner, 2, 3)
	assertLegacyOutboxSchema(t, ctx, db)
	assertOutboxEventTypeIndex(t, ctx, db, driver)

	results, err = runner.Up(ctx, 0)
	if err != nil {
		t.Fatalf("reapply outbox migration: %v", err)
	}
	if len(results) != 1 || results[0].Version != 3 {
		t.Fatalf("unexpected outbox reapply results: %+v", results)
	}
	assertVersions(t, ctx, runner, 3, 3)
	assertOutboxIndexes(t, ctx, db, driver)

	// 回滚 Worker migration 前必须先安全撤销空 Outbox migration，之后沿用 00002 的非空保护契约。
	_, err = runner.Down(ctx, 1)
	if err != nil {
		t.Fatalf("rollback empty outbox migration: %v", err)
	}
	assertVersions(t, ctx, runner, 2, 3)

	if _, err := runner.Down(ctx, 1); err == nil {
		t.Fatal("expected non-empty worker task table to block rollback")
	}
	assertVersions(t, ctx, runner, 2, 3)
	var taskCount int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM worker_tasks").Scan(&taskCount); err != nil || taskCount != 4 {
		t.Fatalf("failed rollback changed worker tasks: count=%d err=%v", taskCount, err)
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM worker_tasks"); err != nil {
		t.Fatalf("delete worker task fixtures: %v", err)
	}
	if driver == "postgres" {
		assertConcurrentInsertBlocksWorkerTaskDown(t, ctx, db, runner)
	}

	rolledBack, err = runner.Down(ctx, 1)
	if err != nil {
		t.Fatalf("rollback embedded migration: %v", err)
	}
	if len(rolledBack) != 1 || rolledBack[0].Version != 2 {
		t.Fatalf("unexpected embedded down results: %+v", rolledBack)
	}
	assertVersions(t, ctx, runner, 1, 3)
	if _, err := db.ExecContext(ctx, "SELECT 1 FROM worker_tasks"); err == nil {
		t.Fatal("worker_tasks still exists after rolling back migration 2")
	}

	results, err = runner.Up(ctx, 2)
	if err != nil {
		t.Fatalf("reapply worker task migration: %v", err)
	}
	if len(results) != 1 || results[0].Version != 2 {
		t.Fatalf("unexpected embedded reapply results: %+v", results)
	}
	assertVersions(t, ctx, runner, 2, 3)

	rolledBack, err = runner.Down(ctx, 2)
	if err != nil {
		t.Fatalf("rollback all embedded migrations: %v", err)
	}
	if len(rolledBack) != 2 || rolledBack[0].Version != 2 || rolledBack[1].Version != 1 {
		t.Fatalf("unexpected full embedded down results: %+v", rolledBack)
	}
	assertVersions(t, ctx, runner, 0, 3)
}

func assertConcurrentInsertBlocksWorkerTaskDown(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	runner *migration.Runner,
) {
	t.Helper()

	writer, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin concurrent worker task insert: %v", err)
	}
	if _, err := writer.ExecContext(ctx, `
INSERT INTO worker_tasks (id, task_type, payload_json)
VALUES ('018f0000-0000-7000-8000-000000000011', 'contract.concurrent', '{}')
`); err != nil {
		_ = writer.Rollback()
		t.Fatalf("insert uncommitted worker task: %v", err)
	}

	downCtx, cancelDown := context.WithTimeout(ctx, 5*time.Second)
	defer cancelDown()
	downDone := make(chan error, 1)
	go func() {
		_, err := runner.Down(downCtx, 1)
		downDone <- err
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		err := db.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM pg_locks AS locks
    JOIN pg_class AS tables ON tables.oid = locks.relation
    JOIN pg_namespace AS schemas ON schemas.oid = tables.relnamespace
    WHERE schemas.nspname = current_schema()
      AND tables.relname = 'worker_tasks'
      AND locks.mode = 'AccessExclusiveLock'
      AND NOT locks.granted
)
`).Scan(&waiting)
		if err != nil {
			_ = writer.Rollback()
			t.Fatalf("inspect concurrent migration lock: %v", err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			_ = writer.Rollback()
			t.Fatal("worker task Down did not wait for the concurrent writer")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := writer.Commit(); err != nil {
		t.Fatalf("commit concurrent worker task insert: %v", err)
	}
	select {
	case err := <-downDone:
		if err == nil {
			t.Fatal("concurrent worker task was deleted by Down")
		}
	case <-downCtx.Done():
		t.Fatalf("worker task Down did not finish: %v", downCtx.Err())
	}

	assertVersions(t, ctx, runner, 2, 2)
	var taskCount int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM worker_tasks").Scan(&taskCount); err != nil || taskCount != 1 {
		t.Fatalf("concurrent Down changed worker tasks: count=%d err=%v", taskCount, err)
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM worker_tasks"); err != nil {
		t.Fatalf("delete concurrent worker task fixture: %v", err)
	}
}

func assertWorkerTaskSchema(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()

	if _, err := db.ExecContext(ctx, `
INSERT INTO worker_tasks (id, task_type, payload_json)
VALUES ('018f0000-0000-7000-8000-000000000001', 'contract.noop', '{}')
`); err != nil {
		t.Fatalf("insert queued worker task: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO worker_tasks (
    id, task_type, payload_json, status, attempt_count,
    lease_id, worker_id, lease_expires_at, last_heartbeat_at, claimed_at
) VALUES (
    '018f0000-0000-7000-8000-000000000002', 'contract.noop', '{}', 'claimed', 1,
    '018f0000-0000-7000-8000-000000000101', 'worker-a', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
)
`); err != nil {
		t.Fatalf("insert leased worker task: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO worker_tasks (
    id, task_type, payload_json, status, attempt_count,
    lease_id, worker_id, lease_expires_at, last_heartbeat_at, claimed_at
) VALUES (
    '018f0000-0000-7000-8000-000000000006', 'contract.noop', '{}', 'claimed', 1,
    '018f0000-0000-7000-8000-000000000101', 'worker-b', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
)`); err == nil {
		t.Fatal("expected duplicate lease to fail")
	}
	validTransitions := []string{
		`UPDATE worker_tasks SET status = 'running', started_at = CURRENT_TIMESTAMP
WHERE id = '018f0000-0000-7000-8000-000000000002'`,
		`UPDATE worker_tasks SET status = 'cancelRequested', cancel_requested_at = CURRENT_TIMESTAMP
WHERE id = '018f0000-0000-7000-8000-000000000002'`,
		`UPDATE worker_tasks SET status = 'canceled', finished_at = CURRENT_TIMESTAMP,
    lease_id = NULL, worker_id = NULL, lease_expires_at = NULL, last_heartbeat_at = NULL
WHERE id = '018f0000-0000-7000-8000-000000000002'`,
		`INSERT INTO worker_tasks (id, task_type, payload_json, status, finished_at)
VALUES ('018f0000-0000-7000-8000-000000000008', 'contract.noop', '{}', 'succeeded', CURRENT_TIMESTAMP)`,
		`INSERT INTO worker_tasks (id, task_type, payload_json, status, finished_at)
VALUES ('018f0000-0000-7000-8000-000000000009', 'contract.noop', '{}', 'failed', CURRENT_TIMESTAMP)`,
	}
	for _, statement := range validTransitions {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("apply valid worker task state: %v", err)
		}
	}

	invalidRows := []struct {
		name string
		sql  string
	}{
		{
			name: "unknown status",
			sql:  "INSERT INTO worker_tasks (id, task_type, payload_json, status) VALUES ('018f0000-0000-7000-8000-000000000003', 'contract.noop', '{}', 'unknown')",
		},
		{
			name: "attempt beyond maximum",
			sql:  "INSERT INTO worker_tasks (id, task_type, payload_json, attempt_count, max_attempts) VALUES ('018f0000-0000-7000-8000-000000000004', 'contract.noop', '{}', 2, 1)",
		},
		{
			name: "active task without lease",
			sql:  "INSERT INTO worker_tasks (id, task_type, payload_json, status) VALUES ('018f0000-0000-7000-8000-000000000005', 'contract.noop', '{}', 'running')",
		},
		{
			name: "canceled task without timestamps",
			sql:  "INSERT INTO worker_tasks (id, task_type, payload_json, status, finished_at) VALUES ('018f0000-0000-7000-8000-000000000007', 'contract.noop', '{}', 'canceled', CURRENT_TIMESTAMP)",
		},
		{
			name: "terminal task without finished time",
			sql:  "INSERT INTO worker_tasks (id, task_type, payload_json, status) VALUES ('018f0000-0000-7000-8000-000000000010', 'contract.noop', '{}', 'succeeded')",
		},
	}
	for _, test := range invalidRows {
		if _, err := db.ExecContext(ctx, test.sql); err == nil {
			t.Fatalf("expected %s to fail", test.name)
		}
	}
}

// assertOutboxSchema 以真实写入验证 00003 的状态、租约 fencing、尝试次数、终态时间和告警留存约束。
// 测试只使用固定的空 JSON，不读取或输出事件 payload、metadata、错误正文等可能包含敏感信息的列。
func assertOutboxSchema(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()

	validRows := []string{
		`INSERT INTO domain_event_outbox (
    event_type, aggregate_type, aggregate_id, payload_json, metadata_json,
    status, attempt_count, available_at, created_at, updated_at
) VALUES ('contract.pending', 'contract', 'pending', '{}', '{}', 'pending', 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		`INSERT INTO domain_event_outbox (
    event_type, aggregate_type, aggregate_id, payload_json, metadata_json,
    status, attempt_count, max_attempts, available_at,
    lease_id, worker_id, claimed_at, lease_expires_at, created_at, updated_at
) VALUES (
    'contract.claimed', 'contract', 'claimed', '{}', '{}',
    'claimed', 1, 3, CURRENT_TIMESTAMP,
    '018f0000-0000-7000-8000-000000000301', 'dispatcher-a',
    CURRENT_TIMESTAMP, '2099-01-01T00:00:00Z', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
)`,
		`INSERT INTO domain_event_outbox (
    event_type, aggregate_type, aggregate_id, payload_json, metadata_json,
    status, attempt_count, available_at, processed_at, created_at, updated_at
) VALUES (
    'contract.processed', 'contract', 'processed', '{}', '{}',
    'processed', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
)`,
		`INSERT INTO domain_event_outbox (
    event_type, aggregate_type, aggregate_id, payload_json, metadata_json,
    status, attempt_count, available_at, processed_at, last_error_category, created_at, updated_at
) VALUES (
    'contract.failed', 'contract', 'failed', '{}', '{}',
    'failed', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'legacy_terminal', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
)`,
		`INSERT INTO domain_event_outbox (
    event_type, aggregate_type, aggregate_id, payload_json, metadata_json,
    status, attempt_count, max_attempts, available_at, processed_at,
    last_error_category, dead_lettered_at, alerted_at, created_at, updated_at
) VALUES (
    'contract.dead', 'contract', 'dead', '{}', '{}',
    'dead_letter', 3, 3, CURRENT_TIMESTAMP, '2026-08-02T00:00:00Z',
    'attempts_exhausted', '2026-08-02T00:00:00Z', '2026-08-02T00:01:00Z',
    CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
)`,
	}
	for _, statement := range validRows {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("insert valid outbox contract row: %v", err)
		}
	}

	// 当前 dispatcher 在 A1-3 后仍直接把 pending 更新为 processed 或 legacy failed。
	// 这里走真实 UPDATE，既验证 SQLite 的更新触发器，也防止 PostgreSQL CHECK 提前破坏现有运行时。
	runtimeTransitions := []string{
		`INSERT INTO domain_event_outbox (
    event_type, status, attempt_count, available_at, created_at, updated_at
) VALUES ('contract.runtime-success', 'pending', 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		`UPDATE domain_event_outbox
SET status = 'processed', processed_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
WHERE event_type = 'contract.runtime-success'`,
		`INSERT INTO domain_event_outbox (
    event_type, status, attempt_count, available_at, created_at, updated_at
) VALUES ('contract.runtime-failure', 'pending', 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		`UPDATE domain_event_outbox
SET status = 'failed', attempt_count = attempt_count + 1,
    processed_at = CURRENT_TIMESTAMP, last_error_message = 'redacted-test-error', updated_at = CURRENT_TIMESTAMP
WHERE event_type = 'contract.runtime-failure'`,
	}
	for _, statement := range runtimeTransitions {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("apply existing dispatcher transition under Outbox contract: %v", err)
		}
	}

	invalidRows := []struct {
		name string
		sql  string
	}{
		{
			name: "unknown status",
			sql:  `INSERT INTO domain_event_outbox (status, attempt_count, available_at) VALUES ('unknown', 0, CURRENT_TIMESTAMP)`,
		},
		{
			name: "attempt beyond maximum",
			sql:  `INSERT INTO domain_event_outbox (status, attempt_count, max_attempts, available_at) VALUES ('pending', 4, 3, CURRENT_TIMESTAMP)`,
		},
		{
			name: "claimed without complete lease",
			sql:  `INSERT INTO domain_event_outbox (status, attempt_count, available_at) VALUES ('claimed', 1, CURRENT_TIMESTAMP)`,
		},
		{
			name: "pending with active lease",
			sql: `INSERT INTO domain_event_outbox (
    status, attempt_count, available_at, lease_id, worker_id, claimed_at, lease_expires_at
) VALUES (
    'pending', 1, CURRENT_TIMESTAMP, '018f0000-0000-7000-8000-000000000302',
    'dispatcher-b', CURRENT_TIMESTAMP, '2099-01-01T00:00:00Z'
)`,
		},
		{
			name: "terminal without processed time",
			sql:  `INSERT INTO domain_event_outbox (status, attempt_count, available_at) VALUES ('processed', 1, CURRENT_TIMESTAMP)`,
		},
		{
			name: "dead letter without exhausted attempts",
			sql: `INSERT INTO domain_event_outbox (
    status, attempt_count, max_attempts, available_at, processed_at, dead_lettered_at
) VALUES (
    'dead_letter', 2, 3, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
)`,
		},
		{
			name: "alert before dead letter",
			sql: `INSERT INTO domain_event_outbox (
    status, attempt_count, max_attempts, available_at, processed_at, dead_lettered_at, alerted_at
) VALUES (
    'dead_letter', 3, 3, CURRENT_TIMESTAMP, '2026-08-02T00:01:00Z',
    '2026-08-02T00:01:00Z', '2026-08-02T00:00:00Z'
)`,
		},
		{
			name: "duplicate lease fencing token",
			sql: `INSERT INTO domain_event_outbox (
    status, attempt_count, available_at, lease_id, worker_id, claimed_at, lease_expires_at
) VALUES (
    'claimed', 1, CURRENT_TIMESTAMP, '018f0000-0000-7000-8000-000000000301',
    'dispatcher-c', CURRENT_TIMESTAMP, '2099-01-01T00:00:00Z'
)`,
		},
	}
	for _, test := range invalidRows {
		if _, err := db.ExecContext(ctx, test.sql); err == nil {
			t.Fatalf("expected %s to fail", test.name)
		}
	}
	if _, err := db.ExecContext(ctx, `
UPDATE domain_event_outbox
SET status = 'claimed'
WHERE event_type = 'contract.pending'
`); err == nil {
		t.Fatal("expected invalid pending-to-claimed update without a lease to fail")
	}
	if _, err := db.ExecContext(ctx, `
UPDATE domain_event_outbox
SET alerted_at = '2026-08-01T23:59:00Z'
WHERE event_type = 'contract.dead'
`); err == nil {
		t.Fatal("expected alert timestamp before dead-letter time to fail on update")
	}
	if _, err := db.ExecContext(ctx, `
DELETE FROM domain_event_outbox
WHERE event_type IN ('contract.runtime-success', 'contract.runtime-failure')
`); err != nil {
		t.Fatalf("remove current-dispatcher transition fixtures: %v", err)
	}

	assertOutboxFixturesPreserved(t, ctx, db)
}

func assertOutboxFixturesPreserved(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var count int
	if err := db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM domain_event_outbox
WHERE status IN ('pending', 'claimed', 'processed', 'failed', 'dead_letter')
`).Scan(&count); err != nil || count != 5 {
		t.Fatalf("outbox fixtures changed: count=%d err=%v", count, err)
	}
}

// assertOutboxIndexes 固定旧查询基线与 A1-3 新增扫描路径；双方言必须具有完全相同的索引集合。
func assertOutboxIndexes(t *testing.T, ctx context.Context, db *sql.DB, driver string) {
	t.Helper()
	for _, name := range []string{
		"idx_domain_event_outbox_event_type",
		"ix_event_outbox_pending",
		"ix_event_outbox_recovery",
		"ux_event_outbox_lease_id",
		"ix_event_outbox_dead_letter_alert",
		"ix_event_outbox_terminal_retention",
	} {
		assertOutboxIndex(t, ctx, db, driver, name)
	}
}

func assertOutboxEventTypeIndex(t *testing.T, ctx context.Context, db *sql.DB, driver string) {
	t.Helper()
	assertOutboxIndex(t, ctx, db, driver, "idx_domain_event_outbox_event_type")
}

func assertOutboxIndex(t *testing.T, ctx context.Context, db *sql.DB, driver, name string) {
	t.Helper()
	var count int
	query := `SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`
	if driver == "postgres" {
		query = `SELECT COUNT(*) FROM pg_indexes WHERE schemaname = current_schema() AND indexname = $1`
	}
	if err := db.QueryRowContext(ctx, query, name).Scan(&count); err != nil {
		t.Fatalf("inspect Outbox index %s: %v", name, err)
	}
	if count != 1 {
		t.Fatalf("expected Outbox index %s exactly once, got %d", name, count)
	}
}

func assertNotificationOutboxForeignKey(t *testing.T, ctx context.Context, db *sql.DB, driver string) {
	t.Helper()
	var targetTable, targetColumn, onDelete string
	if driver == "sqlite" {
		if err := db.QueryRowContext(ctx, `
SELECT "table", "to", on_delete
FROM pragma_foreign_key_list('notification_deliveries')
WHERE "from" = 'outbox_event_id'
`).Scan(&targetTable, &targetColumn, &onDelete); err != nil {
			t.Fatalf("inspect SQLite notification Outbox foreign key: %v", err)
		}
	} else {
		if err := db.QueryRowContext(ctx, `
SELECT referenced.relname,
       referenced_column.attname,
       CASE constraint_record.confdeltype
           WHEN 'a' THEN 'NO ACTION'
           WHEN 'r' THEN 'RESTRICT'
           WHEN 'c' THEN 'CASCADE'
           WHEN 'n' THEN 'SET NULL'
           WHEN 'd' THEN 'SET DEFAULT'
       END
FROM pg_constraint AS constraint_record
JOIN pg_class AS source ON source.oid = constraint_record.conrelid
JOIN pg_class AS referenced ON referenced.oid = constraint_record.confrelid
JOIN pg_attribute AS source_column
  ON source_column.attrelid = source.oid AND source_column.attnum = constraint_record.conkey[1]
JOIN pg_attribute AS referenced_column
  ON referenced_column.attrelid = referenced.oid AND referenced_column.attnum = constraint_record.confkey[1]
WHERE constraint_record.contype = 'f'
  AND source.relnamespace = (SELECT oid FROM pg_namespace WHERE nspname = current_schema())
  AND source.relname = 'notification_deliveries'
  AND source_column.attname = 'outbox_event_id'
`).Scan(&targetTable, &targetColumn, &onDelete); err != nil {
			t.Fatalf("inspect PostgreSQL notification Outbox foreign key: %v", err)
		}
	}
	if targetTable != "domain_event_outbox" || targetColumn != "id" || onDelete != "SET NULL" {
		t.Fatalf(
			"notification Outbox foreign key changed: table=%q column=%q onDelete=%q",
			targetTable,
			targetColumn,
			onDelete,
		)
	}
}

func assertLegacyOutboxSchema(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `
INSERT INTO domain_event_outbox (status, attempt_count, available_at)
VALUES ('pending', 0, CURRENT_TIMESTAMP)
`); err != nil {
		t.Fatalf("legacy outbox schema is not writable after Down: %v", err)
	}
	if _, err := db.ExecContext(ctx, "SELECT max_attempts FROM domain_event_outbox"); err == nil {
		t.Fatal("00003 column max_attempts still exists after Down")
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM domain_event_outbox"); err != nil {
		t.Fatalf("delete legacy outbox fixture: %v", err)
	}
}

// createLegacyOutboxSchema 只在测试中重建 00003 前的 GORM 表关系，确保双方言都从真实旧库边界升级。
// notification_deliveries 的入站外键专门防止 SQLite migration 通过换表导致 outbox_event_id 被置空。
func createLegacyOutboxSchema(t *testing.T, ctx context.Context, db *sql.DB, driver string) {
	t.Helper()
	statements := []string{
		`CREATE TABLE workflow_executions (id BIGINT PRIMARY KEY)`,
		`CREATE TABLE workflow_execution_nodes (id BIGINT PRIMARY KEY)`,
	}
	if driver == "sqlite" {
		statements = append(statements, `CREATE TABLE domain_event_outbox (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type VARCHAR(120), aggregate_type VARCHAR(120), aggregate_id VARCHAR(120),
    workflow_execution_id INTEGER, workflow_execution_node_id INTEGER,
    payload_json TEXT, metadata_json TEXT,
    status VARCHAR(20) DEFAULT 'pending', attempt_count INTEGER DEFAULT 0,
    available_at DATETIME, processed_at DATETIME, last_error_message TEXT,
    created_at DATETIME, updated_at DATETIME,
    CONSTRAINT fk_domain_event_outbox_workflow_execution
        FOREIGN KEY (workflow_execution_id) REFERENCES workflow_executions(id) ON DELETE SET NULL,
    CONSTRAINT fk_domain_event_outbox_workflow_execution_node
        FOREIGN KEY (workflow_execution_node_id) REFERENCES workflow_execution_nodes(id) ON DELETE SET NULL
)`)
	} else {
		statements = append(statements, `CREATE TABLE domain_event_outbox (
    id BIGSERIAL PRIMARY KEY,
    event_type VARCHAR(120), aggregate_type VARCHAR(120), aggregate_id VARCHAR(120),
    workflow_execution_id BIGINT, workflow_execution_node_id BIGINT,
    payload_json TEXT, metadata_json TEXT,
    status VARCHAR(20) DEFAULT 'pending', attempt_count INTEGER DEFAULT 0,
    available_at TIMESTAMPTZ, processed_at TIMESTAMPTZ, last_error_message TEXT,
    created_at TIMESTAMPTZ, updated_at TIMESTAMPTZ,
    CONSTRAINT fk_domain_event_outbox_workflow_execution
        FOREIGN KEY (workflow_execution_id) REFERENCES workflow_executions(id) ON DELETE SET NULL,
    CONSTRAINT fk_domain_event_outbox_workflow_execution_node
        FOREIGN KEY (workflow_execution_node_id) REFERENCES workflow_execution_nodes(id) ON DELETE SET NULL
)`)
	}
	statements = append(statements,
		`CREATE INDEX ix_event_outbox_pending ON domain_event_outbox (status, available_at)`,
		`CREATE TABLE notification_deliveries (
    id BIGINT PRIMARY KEY,
    outbox_event_id BIGINT,
    CONSTRAINT fk_notification_deliveries_outbox_event
        FOREIGN KEY (outbox_event_id) REFERENCES domain_event_outbox(id) ON DELETE SET NULL
)`,
	)
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("create legacy Outbox schema: %v", err)
		}
	}
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
