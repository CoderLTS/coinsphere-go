package db

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
)

// SQLite 以单条写语句完成候选选择、状态变更和结果返回。julianday 将 GORM 写入时可能保留的
// 时区偏移归一化为绝对时间；strftime 的毫秒精度避免短租约在跨秒 heartbeat 时被提前判定过期。
// 外层条件再次校验候选状态，文件写锁保证多个 WAL 句柄不会重复认领。
const claimOutboxSQLite = `
UPDATE domain_event_outbox
SET status = 'claimed',
    attempt_count = attempt_count + 1,
    lease_id = lower(hex(randomblob(16))),
    worker_id = ?,
    claimed_at = strftime('%Y-%m-%d %H:%M:%f', 'now'),
    lease_expires_at = strftime('%Y-%m-%d %H:%M:%f', 'now', '+' || CAST(? AS TEXT) || ' seconds'),
    processed_at = NULL,
    last_error_category = NULL,
    last_error_message = '',
    dead_lettered_at = NULL,
    alerted_at = NULL,
    updated_at = strftime('%Y-%m-%d %H:%M:%f', 'now')
WHERE id IN (
    SELECT id
    FROM domain_event_outbox
    WHERE status = 'pending'
      AND julianday(available_at) <= julianday('now')
      AND attempt_count < max_attempts
    ORDER BY julianday(available_at), id
    LIMIT ?
)
  AND status = 'pending'
  AND julianday(available_at) <= julianday('now')
  AND attempt_count < max_attempts
RETURNING *
`

// PostgreSQL 在同一条语句中锁定并跳过其他认领者已锁住的候选行，再批量更新并返回租约。
// statement_timestamp() 避免调用方外层事务较长时使用事务开始时间计算出过早的租约。
const claimOutboxPostgres = `
WITH claim_clock AS MATERIALIZED (
    SELECT statement_timestamp() AS now
), candidates AS (
    SELECT outbox.id
    FROM domain_event_outbox AS outbox
    CROSS JOIN claim_clock AS clock
    WHERE outbox.status = 'pending'
      AND outbox.available_at <= clock.now
      AND outbox.attempt_count < outbox.max_attempts
    ORDER BY outbox.available_at, outbox.id
    FOR UPDATE OF outbox SKIP LOCKED
    LIMIT ?
), claimed AS (
    UPDATE domain_event_outbox AS outbox
    SET status = 'claimed',
        attempt_count = outbox.attempt_count + 1,
        lease_id = gen_random_uuid()::text,
        worker_id = ?,
        claimed_at = clock.now,
        lease_expires_at = clock.now + (CAST(? AS BIGINT) * INTERVAL '1 second'),
        processed_at = NULL,
        last_error_category = NULL,
        last_error_message = '',
        dead_lettered_at = NULL,
        alerted_at = NULL,
        updated_at = clock.now
    FROM candidates
    CROSS JOIN claim_clock AS clock
    WHERE outbox.id = candidates.id
      AND outbox.status = 'pending'
      AND outbox.available_at <= clock.now
      AND outbox.attempt_count < outbox.max_attempts
    RETURNING outbox.*
)
SELECT * FROM claimed ORDER BY available_at, id
`

// 过期恢复保留已消耗的 attempt_count：仍有次数时重新排队，耗尽时原子进入死信，
// 避免最后一次租约崩溃后永久停留在 claimed。两种结果都会在同一写入中清除旧租约。
const recoverOutboxSQLite = `
UPDATE domain_event_outbox
SET status = CASE WHEN attempt_count < max_attempts THEN 'pending' ELSE 'dead_letter' END,
    available_at = strftime('%Y-%m-%d %H:%M:%f', 'now'),
    processed_at = CASE WHEN attempt_count < max_attempts THEN NULL ELSE strftime('%Y-%m-%d %H:%M:%f', 'now') END,
    lease_id = NULL,
    worker_id = NULL,
    lease_expires_at = NULL,
    claimed_at = NULL,
    last_error_category = CASE
        WHEN attempt_count < max_attempts THEN 'lease_expired'
        ELSE 'attempts_exhausted'
    END,
    last_error_message = '',
    dead_lettered_at = CASE WHEN attempt_count < max_attempts THEN NULL ELSE strftime('%Y-%m-%d %H:%M:%f', 'now') END,
    alerted_at = NULL,
    updated_at = strftime('%Y-%m-%d %H:%M:%f', 'now')
WHERE id IN (
    SELECT id
    FROM domain_event_outbox
    WHERE status = 'claimed'
      AND julianday(lease_expires_at) <= julianday('now')
    ORDER BY lease_expires_at, id
    LIMIT ?
)
  AND status = 'claimed'
  AND julianday(lease_expires_at) <= julianday('now')
RETURNING *
`

const recoverOutboxPostgres = `
WITH recovery_clock AS MATERIALIZED (
    SELECT statement_timestamp() AS now
), candidates AS (
    SELECT outbox.id
    FROM domain_event_outbox AS outbox
    CROSS JOIN recovery_clock AS clock
    WHERE outbox.status = 'claimed'
      AND outbox.lease_expires_at <= clock.now
    ORDER BY outbox.lease_expires_at, outbox.id
    FOR UPDATE OF outbox SKIP LOCKED
    LIMIT ?
), recovered AS (
    UPDATE domain_event_outbox AS outbox
    SET status = CASE WHEN outbox.attempt_count < outbox.max_attempts THEN 'pending' ELSE 'dead_letter' END,
        available_at = clock.now,
        processed_at = CASE WHEN outbox.attempt_count < outbox.max_attempts THEN NULL ELSE clock.now END,
        lease_id = NULL,
        worker_id = NULL,
        lease_expires_at = NULL,
        claimed_at = NULL,
        last_error_category = CASE
            WHEN outbox.attempt_count < outbox.max_attempts THEN 'lease_expired'
            ELSE 'attempts_exhausted'
        END,
        last_error_message = '',
        dead_lettered_at = CASE WHEN outbox.attempt_count < outbox.max_attempts THEN NULL ELSE clock.now END,
        alerted_at = NULL,
        updated_at = clock.now
    FROM candidates
    CROSS JOIN recovery_clock AS clock
    WHERE outbox.id = candidates.id
      AND outbox.status = 'claimed'
      AND outbox.lease_expires_at <= clock.now
    RETURNING outbox.*
)
SELECT * FROM recovered ORDER BY available_at, id
`

// SQLite 用数据库时间完成失败重排或最终死信。WHERE 中的完整租约条件既阻止过期 Owner 写入，
// 也防止旧 token 在事件被重新认领后覆盖新一代租约；错误正文固定留空，避免持久化订阅异常中的敏感内容。
const failOutboxSQLite = `
UPDATE domain_event_outbox
SET status = CASE WHEN attempt_count < max_attempts THEN 'pending' ELSE 'dead_letter' END,
    available_at = CASE
        WHEN attempt_count < max_attempts
            THEN strftime('%Y-%m-%d %H:%M:%f', 'now', '+' || CAST(? AS TEXT) || ' seconds')
        ELSE strftime('%Y-%m-%d %H:%M:%f', 'now')
    END,
    processed_at = CASE WHEN attempt_count < max_attempts THEN NULL ELSE strftime('%Y-%m-%d %H:%M:%f', 'now') END,
    lease_id = NULL,
    worker_id = NULL,
    lease_expires_at = NULL,
    claimed_at = NULL,
    last_error_category = ?,
    last_error_message = '',
    dead_lettered_at = CASE WHEN attempt_count < max_attempts THEN NULL ELSE strftime('%Y-%m-%d %H:%M:%f', 'now') END,
    alerted_at = NULL,
    updated_at = strftime('%Y-%m-%d %H:%M:%f', 'now')
WHERE id = ?
  AND status = 'claimed'
  AND lease_id = ?
  AND worker_id = ?
  AND attempt_count = ?
  AND julianday(lease_expires_at) > julianday('now')
`

const failOutboxPostgres = `
WITH failure_clock AS MATERIALIZED (
    SELECT statement_timestamp() AS now
)
UPDATE domain_event_outbox AS outbox
SET status = CASE WHEN outbox.attempt_count < outbox.max_attempts THEN 'pending' ELSE 'dead_letter' END,
    available_at = CASE
        WHEN outbox.attempt_count < outbox.max_attempts
            THEN clock.now + (CAST(? AS BIGINT) * INTERVAL '1 second')
        ELSE clock.now
    END,
    processed_at = CASE WHEN outbox.attempt_count < outbox.max_attempts THEN NULL ELSE clock.now END,
    lease_id = NULL,
    worker_id = NULL,
    lease_expires_at = NULL,
    claimed_at = NULL,
    last_error_category = ?,
    last_error_message = '',
    dead_lettered_at = CASE WHEN outbox.attempt_count < outbox.max_attempts THEN NULL ELSE clock.now END,
    alerted_at = NULL,
    updated_at = clock.now
FROM failure_clock AS clock
WHERE outbox.id = ?
  AND outbox.status = 'claimed'
  AND outbox.lease_id = ?
  AND outbox.worker_id = ?
  AND outbox.attempt_count = ?
  AND outbox.lease_expires_at > clock.now
`

// 告警领取先原子写 alerted_at 再只返回固定 ID。它提供并发下的 at-most-once 告警语义：
// 多实例不会重复输出同一死信，但若进程在提交标记后、写日志前崩溃，仍可能漏报；可靠外部告警需独立 Outbox。
const alertOutboxDeadLettersSQLite = `
UPDATE domain_event_outbox
SET alerted_at = CASE
        WHEN julianday(dead_lettered_at) > julianday('now') THEN dead_lettered_at
        ELSE strftime('%Y-%m-%d %H:%M:%f', 'now')
    END,
    updated_at = strftime('%Y-%m-%d %H:%M:%f', 'now')
WHERE id IN (
    SELECT id
    FROM domain_event_outbox
    WHERE status = 'dead_letter'
      AND alerted_at IS NULL
    ORDER BY julianday(dead_lettered_at), id
    LIMIT ?
)
  AND status = 'dead_letter'
  AND alerted_at IS NULL
RETURNING id
`

const alertOutboxDeadLettersPostgres = `
WITH alert_clock AS MATERIALIZED (
    SELECT statement_timestamp() AS now
), candidates AS (
    SELECT outbox.id
    FROM domain_event_outbox AS outbox
    WHERE outbox.status = 'dead_letter'
      AND outbox.alerted_at IS NULL
    ORDER BY outbox.dead_lettered_at, outbox.id
    FOR UPDATE OF outbox SKIP LOCKED
    LIMIT ?
), alerted AS (
    UPDATE domain_event_outbox AS outbox
    SET alerted_at = GREATEST(clock.now, outbox.dead_lettered_at),
        updated_at = clock.now
    FROM candidates
    CROSS JOIN alert_clock AS clock
    WHERE outbox.id = candidates.id
      AND outbox.status = 'dead_letter'
      AND outbox.alerted_at IS NULL
    RETURNING outbox.id
)
SELECT id FROM alerted ORDER BY id
`

// ClaimOutboxEvents 原子认领至多 limit 条到期事件，并为每行生成独立 fencing token。
// database 可以是普通连接或调用方事务；事务回滚时认领和返回的租约一并失效。
func ClaimOutboxEvents(
	ctx context.Context,
	database *gorm.DB,
	workerID string,
	limit int,
	leaseDuration time.Duration,
) ([]DomainEventOutbox, error) {
	if database == nil {
		return nil, errors.New("outbox database is required")
	}
	if strings.TrimSpace(workerID) == "" || utf8.RuneCountInString(workerID) > 120 {
		return nil, errors.New("outbox worker ID must contain 1 to 120 characters")
	}
	if limit < 1 {
		return nil, errors.New("outbox claim limit must be greater than zero")
	}
	if leaseDuration <= 0 {
		return nil, errors.New("outbox lease duration must be greater than zero")
	}
	leaseSeconds := ceilDurationSeconds(leaseDuration)

	var query string
	var args []any
	switch database.Dialector.Name() {
	case "sqlite":
		query = claimOutboxSQLite
		args = []any{workerID, leaseSeconds, limit}
	case "postgres":
		query = claimOutboxPostgres
		args = []any{limit, workerID, leaseSeconds}
	default:
		return nil, fmt.Errorf("outbox claim does not support database driver %q", database.Dialector.Name())
	}
	rows, err := updateOutboxRows(ctx, database, query, args...)
	if err != nil {
		return nil, fmt.Errorf("claim outbox events: %w", err)
	}
	return rows, nil
}

// RecoverExpiredOutboxEvents 原子回收至多 limit 条过期租约。返回行只用于记录固定 ID、
// token 和状态等脱敏运行信息，调用方不得把 PayloadJSON 或 MetadataJSON 写入日志。
func RecoverExpiredOutboxEvents(ctx context.Context, database *gorm.DB, limit int) ([]DomainEventOutbox, error) {
	if database == nil {
		return nil, errors.New("outbox database is required")
	}
	if limit < 1 {
		return nil, errors.New("outbox recovery limit must be greater than zero")
	}

	var query string
	switch database.Dialector.Name() {
	case "sqlite":
		query = recoverOutboxSQLite
	case "postgres":
		query = recoverOutboxPostgres
	default:
		return nil, fmt.Errorf("outbox recovery does not support database driver %q", database.Dialector.Name())
	}
	rows, err := updateOutboxRows(ctx, database, query, limit)
	if err != nil {
		return nil, fmt.Errorf("recover expired outbox events: %w", err)
	}
	return rows, nil
}

// CompleteOutboxEvent 只允许仍有效的当前租约提交 processed 终态。attempt_count 和 worker_id
// 与 lease_id 共同形成 fencing 条件；即使旧 token 被意外复用，也不能越过新的认领代次。
func CompleteOutboxEvent(ctx context.Context, database *gorm.DB, claim DomainEventOutbox) (bool, error) {
	if database == nil {
		return false, errors.New("outbox database is required")
	}
	if err := validateOutboxClaim(claim); err != nil {
		return false, fmt.Errorf("complete outbox event: %w", err)
	}

	nowExpression, ok := outboxNowExpression(database)
	if !ok {
		return false, fmt.Errorf("outbox completion does not support database driver %q", database.Dialector.Name())
	}
	validLeaseExpression := fmt.Sprintf("lease_expires_at > %s", nowExpression)
	if database.Dialector.Name() == "sqlite" {
		validLeaseExpression = fmt.Sprintf("julianday(lease_expires_at) > julianday(%s)", nowExpression)
	}
	query := fmt.Sprintf(`
UPDATE domain_event_outbox
SET status = 'processed',
    processed_at = %[1]s,
    lease_id = NULL,
    worker_id = NULL,
    lease_expires_at = NULL,
    claimed_at = NULL,
    last_error_category = NULL,
    last_error_message = '',
    dead_lettered_at = NULL,
    alerted_at = NULL,
    updated_at = %[1]s
WHERE id = ?
  AND status = 'claimed'
  AND lease_id = ?
  AND worker_id = ?
  AND attempt_count = ?
  AND %[2]s
`, nowExpression, validLeaseExpression)
	result := database.WithContext(ctx).Exec(
		query,
		claim.ID,
		*claim.LeaseID,
		*claim.WorkerID,
		claim.AttemptCount,
	)
	if result.Error != nil {
		return false, fmt.Errorf("complete outbox event: %w", result.Error)
	}
	return result.RowsAffected == 1, nil
}

// RenewOutboxEventLease 只为仍有效的当前认领代次续租。处理批次尾部事件前以及慢订阅处理期间都调用它，
// 因而等待过久的旧 claim 会直接失效，而不会在租约已被回收后继续产生副作用。
func RenewOutboxEventLease(
	ctx context.Context,
	database *gorm.DB,
	claim DomainEventOutbox,
	leaseDuration time.Duration,
) (bool, error) {
	if database == nil {
		return false, errors.New("outbox database is required")
	}
	if err := validateOutboxClaim(claim); err != nil {
		return false, fmt.Errorf("renew outbox event lease: %w", err)
	}
	if leaseDuration <= 0 {
		return false, errors.New("outbox lease duration must be greater than zero")
	}
	nowExpression, ok := outboxNowExpression(database)
	if !ok {
		return false, fmt.Errorf("outbox renewal does not support database driver %q", database.Dialector.Name())
	}
	leaseExpression := "strftime('%Y-%m-%d %H:%M:%f', 'now', '+' || CAST(? AS TEXT) || ' seconds')"
	validLeaseExpression := fmt.Sprintf("julianday(lease_expires_at) > julianday(%s)", nowExpression)
	if database.Dialector.Name() == "postgres" {
		leaseExpression = fmt.Sprintf("%s + (CAST(? AS BIGINT) * INTERVAL '1 second')", nowExpression)
		validLeaseExpression = fmt.Sprintf("lease_expires_at > %s", nowExpression)
	}
	query := fmt.Sprintf(`
UPDATE domain_event_outbox
SET lease_expires_at = %[1]s,
    updated_at = %[2]s
WHERE id = ?
  AND status = 'claimed'
  AND lease_id = ?
  AND worker_id = ?
  AND attempt_count = ?
  AND %[3]s
`, leaseExpression, nowExpression, validLeaseExpression)
	result := database.WithContext(ctx).Exec(
		query,
		ceilDurationSeconds(leaseDuration),
		claim.ID,
		*claim.LeaseID,
		*claim.WorkerID,
		claim.AttemptCount,
	)
	if result.Error != nil {
		return false, fmt.Errorf("renew outbox event lease: %w", result.Error)
	}
	return result.RowsAffected == 1, nil
}

// FailOutboxEvent 在一个 fenced UPDATE 中释放失败租约：仍有尝试次数时按数据库时间重排，
// 最后一次失败则进入 dead_letter。category 必须是固定分类，原始异常正文不会写入数据库。
func FailOutboxEvent(
	ctx context.Context,
	database *gorm.DB,
	claim DomainEventOutbox,
	retryDelay time.Duration,
	category string,
) (bool, error) {
	if database == nil {
		return false, errors.New("outbox database is required")
	}
	if err := validateOutboxClaim(claim); err != nil {
		return false, fmt.Errorf("fail outbox event: %w", err)
	}
	if retryDelay < 0 {
		return false, errors.New("outbox retry delay must not be negative")
	}
	category = strings.TrimSpace(category)
	if category == "" || utf8.RuneCountInString(category) > 64 {
		return false, errors.New("outbox failure category must contain 1 to 64 characters")
	}

	var query string
	switch database.Dialector.Name() {
	case "sqlite":
		query = failOutboxSQLite
	case "postgres":
		query = failOutboxPostgres
	default:
		return false, fmt.Errorf("outbox failure does not support database driver %q", database.Dialector.Name())
	}
	result := database.WithContext(ctx).Exec(
		query,
		ceilDurationSeconds(retryDelay),
		category,
		claim.ID,
		*claim.LeaseID,
		*claim.WorkerID,
		claim.AttemptCount,
	)
	if result.Error != nil {
		return false, fmt.Errorf("fail outbox event: %w", result.Error)
	}
	return result.RowsAffected == 1, nil
}

// MarkOutboxDeadLettersAlerted 原子领取并标记至多 limit 条未告警死信，只返回固定 ID，
// 调用方因此不需要接触 payload、metadata、Owner、token 或错误正文即可输出告警。
func MarkOutboxDeadLettersAlerted(ctx context.Context, database *gorm.DB, limit int) ([]int64, error) {
	if database == nil {
		return nil, errors.New("outbox database is required")
	}
	if limit < 1 {
		return nil, errors.New("outbox alert limit must be greater than zero")
	}

	var query string
	switch database.Dialector.Name() {
	case "sqlite":
		query = alertOutboxDeadLettersSQLite
	case "postgres":
		query = alertOutboxDeadLettersPostgres
	default:
		return nil, fmt.Errorf("outbox alert does not support database driver %q", database.Dialector.Name())
	}
	var ids []int64
	err := database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return tx.Raw(query, limit).Scan(&ids).Error
	})
	if err != nil {
		return nil, fmt.Errorf("mark Outbox dead letters alerted: %w", err)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

func validateOutboxClaim(claim DomainEventOutbox) error {
	if claim.ID < 1 || claim.AttemptCount < 1 || claim.LeaseID == nil || claim.WorkerID == nil ||
		strings.TrimSpace(*claim.LeaseID) == "" || strings.TrimSpace(*claim.WorkerID) == "" {
		return errors.New("valid outbox lease is required")
	}
	return nil
}

func outboxNowExpression(database *gorm.DB) (string, bool) {
	switch database.Dialector.Name() {
	case "sqlite":
		return "strftime('%Y-%m-%d %H:%M:%f', 'now')", true
	case "postgres":
		return "statement_timestamp()", true
	default:
		return "", false
	}
}

func ceilDurationSeconds(duration time.Duration) int64 {
	seconds := int64(duration / time.Second)
	if duration%time.Second != 0 {
		seconds++
	}
	return seconds
}

func sortOutboxRows(rows []DomainEventOutbox) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].AvailableAt.Equal(rows[j].AvailableAt) {
			return rows[i].ID < rows[j].ID
		}
		return rows[i].AvailableAt.Before(rows[j].AvailableAt)
	})
}

// updateOutboxRows 在 RETURNING 结果完整扫描后才提交短事务；扫描或解码失败时，
// 状态更新和生成的 token 会一起回滚，调用方不会丢失一个已经生效却不可见的租约。
func updateOutboxRows(
	ctx context.Context,
	database *gorm.DB,
	query string,
	args ...any,
) ([]DomainEventOutbox, error) {
	var rows []DomainEventOutbox
	err := database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return tx.Raw(query, args...).Scan(&rows).Error
	})
	if err != nil {
		return nil, err
	}
	sortOutboxRows(rows)
	return rows, nil
}
