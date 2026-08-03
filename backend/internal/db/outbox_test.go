package db

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"coinsphere/backend/internal/config"
	"coinsphere/backend/internal/migration"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var outboxContractSchemaSequence atomic.Uint64

type outboxContractDatabase struct {
	primary  *gorm.DB
	openPeer func(t *testing.T) *gorm.DB
}

func TestOutboxLeaseContractSQLite(t *testing.T) {
	runOutboxLeaseContract(t, openSQLiteOutboxContractDatabase)
}

func TestOutboxLeaseContractPostgres(t *testing.T) {
	runOutboxLeaseContract(t, openPostgresOutboxContractDatabase)
}

// runOutboxLeaseContract 对双方言执行同一套状态转换，防止某一实现只通过语法测试、
// 却在真实并发、事务回滚或租约换代时偏离公共契约。
func runOutboxLeaseContract(t *testing.T, open func(t *testing.T) *outboxContractDatabase) {
	t.Helper()

	t.Run("concurrent batch claim", func(t *testing.T) {
		database := open(t)
		peer := database.openPeer(t)
		wantIDs := make(map[int64]struct{}, 7)
		for index := range 6 {
			wantIDs[insertPendingOutboxEvent(t, database.primary, fmt.Sprintf("contract.batch.%d", index), 3)] = struct{}{}
		}

		// 现有 producer 使用 time.Now，SQLite 会保留时区偏移；用相反偏移同时锁定“已到不漏、未来不抢”。
		eastEight := time.FixedZone("UTC+08:00", 8*60*60)
		offsetDueID := insertPendingOutboxEvent(t, database.primary, "contract.offset-due", 3)
		setOutboxAvailableAt(t, database.primary, offsetDueID, time.Now().In(eastEight).Add(-time.Minute))
		wantIDs[offsetDueID] = struct{}{}

		westEight := time.FixedZone("UTC-08:00", -8*60*60)
		futureID := insertPendingOutboxEvent(t, database.primary, "contract.future", 3)
		setOutboxAvailableAt(t, database.primary, futureID, time.Now().In(westEight).Add(time.Hour))

		// 两个独立数据库句柄同时开始，确保 SQLite 测到跨实例文件锁，PostgreSQL 测到
		// SKIP LOCKED，而不是被同一 GORM 连接池或测试调用顺序人为串行化。
		type claimResult struct {
			rows []DomainEventOutbox
			err  error
		}
		start := make(chan struct{})
		results := make(chan claimResult, 2)
		claim := func(gdb *gorm.DB, workerID string) {
			<-start
			rows, err := ClaimOutboxEvents(context.Background(), gdb, workerID, 4, time.Minute)
			results <- claimResult{rows: rows, err: err}
		}
		go claim(database.primary, "outbox-contract-a")
		go claim(peer, "outbox-contract-b")
		close(start)

		seenIDs := make(map[int64]struct{}, 7)
		seenLeases := make(map[string]struct{}, 7)
		batchSizes := make(map[int]int, 2)
		for range 2 {
			result := <-results
			if result.err != nil {
				t.Fatalf("claim concurrent Outbox batch: %v", result.err)
			}
			batchSizes[len(result.rows)]++
			assertOutboxRowsOrdered(t, result.rows)
			for _, row := range result.rows {
				if _, exists := seenIDs[row.ID]; exists {
					t.Fatalf("event %d was claimed by both workers", row.ID)
				}
				if row.LeaseID == nil || row.WorkerID == nil || row.AttemptCount != 1 || row.Status != "claimed" {
					t.Fatalf("incomplete claimed row: %s", outboxContractRowSummary(row))
				}
				if _, exists := seenLeases[*row.LeaseID]; exists {
					t.Fatalf("lease token was reused for event %d", row.ID)
				}
				seenIDs[row.ID] = struct{}{}
				seenLeases[*row.LeaseID] = struct{}{}
			}
		}
		if len(seenIDs) != len(wantIDs) || batchSizes[4] != 1 || batchSizes[3] != 1 {
			t.Fatalf("unexpected batch split: claimed=%d sizes=%v", len(seenIDs), batchSizes)
		}
		for id := range wantIDs {
			if _, exists := seenIDs[id]; !exists {
				t.Fatalf("event %d was not claimed", id)
			}
		}
		futureRow := readOutboxEvent(t, database.primary, futureID)
		if futureRow.Status != "pending" || futureRow.AttemptCount != 0 || futureRow.LeaseID != nil {
			t.Fatalf("future event was claimed early: %s", outboxContractRowSummary(futureRow))
		}
	})

	t.Run("claim rolls back with caller transaction", func(t *testing.T) {
		database := open(t)
		eventID := insertPendingOutboxEvent(t, database.primary, "contract.rollback", 3)
		rollback := errors.New("contract transaction rollback")

		err := database.primary.Transaction(func(tx *gorm.DB) error {
			rows, err := ClaimOutboxEvents(context.Background(), tx, "outbox-rollback", 1, time.Minute)
			if err != nil {
				return err
			}
			if len(rows) != 1 || rows[0].ID != eventID {
				return fmt.Errorf("unexpected transaction claim: %v", outboxContractRowsSummary(rows))
			}
			return rollback
		})
		if !errors.Is(err, rollback) {
			t.Fatalf("transaction error = %v, want rollback sentinel", err)
		}

		row := readOutboxEvent(t, database.primary, eventID)
		if row.Status != "pending" || row.AttemptCount != 0 || row.LeaseID != nil {
			t.Fatalf("rolled back claim changed event: %s", outboxContractRowSummary(row))
		}
		claimed, err := ClaimOutboxEvents(context.Background(), database.primary, "outbox-after-rollback", 1, time.Minute)
		if err != nil || len(claimed) != 1 || claimed[0].AttemptCount != 1 {
			t.Fatalf("event was not claimable after rollback: rows=%v err=%v", outboxContractRowsSummary(claimed), err)
		}
	})

	t.Run("batch statement failure is atomic", func(t *testing.T) {
		database := open(t)
		firstID := insertPendingOutboxEvent(t, database.primary, "contract.atomic.first", 3)
		failedID := insertPendingOutboxEvent(t, database.primary, "contract.atomic.fail", 3)
		installOutboxClaimFailureTrigger(t, database.primary)

		// 批量中的指定行由真实数据库 trigger 拒绝；无论数据库选择何种行更新顺序，
		// 单条 DML 都必须整体回滚，不能留下状态、尝试次数或 token 的部分变更。
		rows, err := ClaimOutboxEvents(context.Background(), database.primary, "outbox-atomic-failure", 2, time.Minute)
		if err == nil {
			t.Fatalf("claim unexpectedly succeeded with rows %v", outboxContractRowsSummary(rows))
		}
		for _, eventID := range []int64{firstID, failedID} {
			row := readOutboxEvent(t, database.primary, eventID)
			if row.Status != "pending" || row.AttemptCount != 0 || row.LeaseID != nil || row.WorkerID != nil {
				t.Fatalf("failed batch left a partial claim: %s", outboxContractRowSummary(row))
			}
		}
	})

	t.Run("expired recovery fences old lease", func(t *testing.T) {
		database := open(t)
		retryID := insertPendingOutboxEvent(t, database.primary, "contract.recover", 2)
		exhaustedID := insertPendingOutboxEvent(t, database.primary, "contract.exhausted", 1)
		firstClaims, err := ClaimOutboxEvents(context.Background(), database.primary, "outbox-old-owner", 2, time.Minute)
		if err != nil || len(firstClaims) != 2 {
			t.Fatalf("claim recovery fixtures: rows=%v err=%v", outboxContractRowsSummary(firstClaims), err)
		}
		oldClaims := make(map[int64]DomainEventOutbox, 2)
		for _, claim := range firstClaims {
			oldClaims[claim.ID] = claim
		}

		// 直接推进数据库中的租约时间，不用 Sleep 制造慢测试；claimed_at 仍早于过期时间，
		// 因而测试夹具继续满足 00003 的数据库约束。
		claimedAt := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Second)
		expiresAt := claimedAt.Add(time.Minute)
		result := database.primary.Exec(
			"UPDATE domain_event_outbox SET claimed_at = ?, lease_expires_at = ? WHERE id IN (?, ?)",
			claimedAt,
			expiresAt,
			retryID,
			exhaustedID,
		)
		if result.Error != nil || result.RowsAffected != 2 {
			t.Fatalf("expire Outbox leases: affected=%d err=%v", result.RowsAffected, result.Error)
		}

		completed, err := CompleteOutboxEvent(context.Background(), database.primary, oldClaims[retryID])
		if err != nil || completed {
			t.Fatalf("expired lease completed event: completed=%v err=%v", completed, err)
		}
		renewed, err := RenewOutboxEventLease(context.Background(), database.primary, oldClaims[retryID], time.Minute)
		if err != nil || renewed {
			t.Fatalf("expired lease was renewed: renewed=%v err=%v", renewed, err)
		}
		failed, err := FailOutboxEvent(context.Background(), database.primary, oldClaims[retryID], time.Second, "subscriber_failed")
		if err != nil || failed {
			t.Fatalf("expired lease rescheduled event: failed=%v err=%v", failed, err)
		}
		recovered, err := RecoverExpiredOutboxEvents(context.Background(), database.primary, 10)
		if err != nil || len(recovered) != 2 {
			t.Fatalf("recover expired Outbox leases: rows=%v err=%v", outboxContractRowsSummary(recovered), err)
		}

		retryRow := readOutboxEvent(t, database.primary, retryID)
		if retryRow.Status != "pending" || retryRow.AttemptCount != 1 || retryRow.LeaseID != nil ||
			retryRow.LastErrorCategory == nil || *retryRow.LastErrorCategory != "lease_expired" {
			t.Fatalf("retryable expired event was not recovered: %s", outboxContractRowSummary(retryRow))
		}
		exhaustedRow := readOutboxEvent(t, database.primary, exhaustedID)
		if exhaustedRow.Status != "dead_letter" || exhaustedRow.ProcessedAt == nil ||
			exhaustedRow.DeadLetteredAt == nil || !exhaustedRow.ProcessedAt.Equal(*exhaustedRow.DeadLetteredAt) ||
			exhaustedRow.LastErrorCategory == nil || *exhaustedRow.LastErrorCategory != "attempts_exhausted" {
			t.Fatalf("exhausted expired event did not reach dead letter: %s", outboxContractRowSummary(exhaustedRow))
		}

		secondClaims, err := ClaimOutboxEvents(context.Background(), database.primary, "outbox-new-owner", 10, time.Minute)
		if err != nil || len(secondClaims) != 1 || secondClaims[0].ID != retryID || secondClaims[0].AttemptCount != 2 {
			t.Fatalf("reclaim recovered event: rows=%v err=%v", outboxContractRowsSummary(secondClaims), err)
		}
		newClaim := secondClaims[0]
		if *newClaim.LeaseID == *oldClaims[retryID].LeaseID {
			t.Fatal("reclaim reused the old fencing token")
		}

		completed, err = CompleteOutboxEvent(context.Background(), database.primary, oldClaims[retryID])
		if err != nil || completed {
			t.Fatalf("old lease modified reclaimed event: completed=%v err=%v", completed, err)
		}
		failed, err = FailOutboxEvent(context.Background(), database.primary, oldClaims[retryID], time.Second, "subscriber_failed")
		if err != nil || failed {
			t.Fatalf("old lease rescheduled reclaimed event: failed=%v err=%v", failed, err)
		}
		stillClaimed := readOutboxEvent(t, database.primary, retryID)
		if stillClaimed.Status != "claimed" || stillClaimed.LeaseID == nil || *stillClaimed.LeaseID != *newClaim.LeaseID {
			t.Fatalf("old lease changed current claim: %s", outboxContractRowSummary(stillClaimed))
		}

		completed, err = CompleteOutboxEvent(context.Background(), database.primary, newClaim)
		if err != nil || !completed {
			t.Fatalf("current lease could not complete event: completed=%v err=%v", completed, err)
		}
		finished := readOutboxEvent(t, database.primary, retryID)
		if finished.Status != "processed" || finished.ProcessedAt == nil || finished.LeaseID != nil {
			t.Fatalf("completed event has invalid terminal state: %s", outboxContractRowSummary(finished))
		}
	})

	t.Run("failed delivery retries then reaches dead letter", func(t *testing.T) {
		database := open(t)
		eventID := insertPendingOutboxEvent(t, database.primary, "contract.delivery.retry", 2)
		claims, err := ClaimOutboxEvents(context.Background(), database.primary, "outbox-delivery-old", 1, time.Minute)
		if err != nil || len(claims) != 1 || claims[0].ID != eventID {
			t.Fatalf("claim retry fixture: rows=%v err=%v", outboxContractRowsSummary(claims), err)
		}
		oldClaim := claims[0]
		renewed, err := RenewOutboxEventLease(context.Background(), database.primary, oldClaim, 2*time.Minute)
		if err != nil || !renewed {
			t.Fatalf("renew current Outbox lease: renewed=%v err=%v", renewed, err)
		}

		rollback := errors.New("failure transition rollback")
		err = database.primary.Transaction(func(tx *gorm.DB) error {
			updated, err := FailOutboxEvent(context.Background(), tx, oldClaim, 2*time.Second, "subscriber_failed")
			if err != nil {
				return err
			}
			if !updated {
				return errors.New("transaction did not update current Outbox lease")
			}
			return rollback
		})
		if !errors.Is(err, rollback) {
			t.Fatalf("failure transaction error = %v, want rollback sentinel", err)
		}
		rolledBack := readOutboxEvent(t, database.primary, eventID)
		if rolledBack.Status != "claimed" || rolledBack.LeaseID == nil || rolledBack.AttemptCount != 1 {
			t.Fatalf("rolled back failure changed claim: %s", outboxContractRowSummary(rolledBack))
		}

		failedAt := time.Now().UTC()
		updated, err := FailOutboxEvent(context.Background(), database.primary, oldClaim, 2*time.Second, "subscriber_failed")
		if err != nil || !updated {
			t.Fatalf("reschedule failed Outbox delivery: updated=%v err=%v", updated, err)
		}
		retryRow := readOutboxEvent(t, database.primary, eventID)
		if retryRow.Status != "pending" || retryRow.AttemptCount != 1 || retryRow.LeaseID != nil ||
			retryRow.AvailableAt.Before(failedAt.Add(500*time.Millisecond)) || retryRow.LastErrorCategory == nil ||
			*retryRow.LastErrorCategory != "subscriber_failed" || retryRow.LastErrorMessage != "" {
			t.Fatalf("failed delivery was not safely rescheduled: %s", outboxContractRowSummary(retryRow))
		}
		tooEarly, err := ClaimOutboxEvents(context.Background(), database.primary, "outbox-delivery-too-early", 1, time.Minute)
		if err != nil || len(tooEarly) != 0 {
			t.Fatalf("retry was claimed before backoff elapsed: rows=%v err=%v", outboxContractRowsSummary(tooEarly), err)
		}

		setOutboxAvailableAt(t, database.primary, eventID, time.Now().UTC().Add(-time.Minute))
		claims, err = ClaimOutboxEvents(context.Background(), database.primary, "outbox-delivery-new", 1, time.Minute)
		if err != nil || len(claims) != 1 || claims[0].AttemptCount != 2 {
			t.Fatalf("reclaim retry after backoff: rows=%v err=%v", outboxContractRowsSummary(claims), err)
		}
		newClaim := claims[0]
		updated, err = FailOutboxEvent(context.Background(), database.primary, oldClaim, time.Second, "subscriber_failed")
		if err != nil || updated {
			t.Fatalf("old failure token changed new claim: updated=%v err=%v", updated, err)
		}
		updated, err = FailOutboxEvent(context.Background(), database.primary, newClaim, time.Second, "subscriber_failed")
		if err != nil || !updated {
			t.Fatalf("dead-letter exhausted delivery: updated=%v err=%v", updated, err)
		}
		deadLetter := readOutboxEvent(t, database.primary, eventID)
		if deadLetter.Status != "dead_letter" || deadLetter.ProcessedAt == nil || deadLetter.DeadLetteredAt == nil ||
			!deadLetter.ProcessedAt.Equal(*deadLetter.DeadLetteredAt) || deadLetter.LeaseID != nil ||
			deadLetter.LastErrorCategory == nil || *deadLetter.LastErrorCategory != "subscriber_failed" {
			t.Fatalf("exhausted delivery has invalid dead letter state: %s", outboxContractRowSummary(deadLetter))
		}
	})

	t.Run("dead letter alerts are claimed once across workers", func(t *testing.T) {
		database := open(t)
		peer := database.openPeer(t)
		wantIDs := make(map[int64]struct{}, 7)
		for index := range 7 {
			wantIDs[insertDeadLetterOutboxEvent(t, database.primary, fmt.Sprintf("contract.alert.%d", index))] = struct{}{}
		}

		// 两个独立句柄同时领取告警，验证 PostgreSQL 的 SKIP LOCKED 与 SQLite 文件写锁
		// 都只能让一个 ID 出现在一个批次中，且 limit 对每个批次独立生效。
		type alertResult struct {
			ids []int64
			err error
		}
		start := make(chan struct{})
		results := make(chan alertResult, 2)
		claimAlerts := func(gdb *gorm.DB) {
			<-start
			ids, err := MarkOutboxDeadLettersAlerted(context.Background(), gdb, 4)
			results <- alertResult{ids: ids, err: err}
		}
		go claimAlerts(database.primary)
		go claimAlerts(peer)
		close(start)

		seen := make(map[int64]struct{}, len(wantIDs))
		batchSizes := make(map[int]int, 2)
		for range 2 {
			result := <-results
			if result.err != nil {
				t.Fatalf("claim concurrent dead letter alerts: %v", result.err)
			}
			batchSizes[len(result.ids)]++
			for _, id := range result.ids {
				if _, exists := seen[id]; exists {
					t.Fatalf("dead letter %d was alerted by both workers", id)
				}
				seen[id] = struct{}{}
			}
		}
		if len(seen) != len(wantIDs) || batchSizes[4] != 1 || batchSizes[3] != 1 {
			t.Fatalf("unexpected alert batch split: alerted=%d sizes=%v", len(seen), batchSizes)
		}
		for id := range wantIDs {
			if _, exists := seen[id]; !exists {
				t.Fatalf("dead letter %d was not alerted", id)
			}
			if row := readOutboxEvent(t, database.primary, id); row.AlertedAt == nil {
				t.Fatalf("dead letter %d was returned without alerted_at", id)
			}
		}
		none, err := MarkOutboxDeadLettersAlerted(context.Background(), database.primary, 10)
		if err != nil || len(none) != 0 {
			t.Fatalf("already alerted dead letters were reclaimed: ids=%v err=%v", none, err)
		}

		rollbackID := insertDeadLetterOutboxEvent(t, database.primary, "contract.alert.rollback")
		rollback := errors.New("alert transaction rollback")
		err = database.primary.Transaction(func(tx *gorm.DB) error {
			ids, err := MarkOutboxDeadLettersAlerted(context.Background(), tx, 1)
			if err != nil {
				return err
			}
			if len(ids) != 1 || ids[0] != rollbackID {
				return fmt.Errorf("unexpected transaction alert IDs: %v", ids)
			}
			return rollback
		})
		if !errors.Is(err, rollback) {
			t.Fatalf("alert transaction error = %v, want rollback sentinel", err)
		}
		if row := readOutboxEvent(t, database.primary, rollbackID); row.AlertedAt != nil {
			t.Fatalf("rolled back alert left alerted_at: %s", outboxContractRowSummary(row))
		}
		reclaimed, err := MarkOutboxDeadLettersAlerted(context.Background(), database.primary, 1)
		if err != nil || len(reclaimed) != 1 || reclaimed[0] != rollbackID {
			t.Fatalf("rolled back alert was not reclaimable: ids=%v err=%v", reclaimed, err)
		}
	})

	t.Run("dead letter alert batch failure is atomic", func(t *testing.T) {
		database := open(t)
		firstID := insertDeadLetterOutboxEvent(t, database.primary, "contract.alert.atomic.first")
		failedID := insertDeadLetterOutboxEvent(t, database.primary, "contract.alert.atomic.fail")
		installOutboxAlertFailureTrigger(t, database.primary)

		ids, err := MarkOutboxDeadLettersAlerted(context.Background(), database.primary, 2)
		if err == nil {
			t.Fatalf("alert batch unexpectedly succeeded with IDs %v", ids)
		}
		for _, eventID := range []int64{firstID, failedID} {
			if row := readOutboxEvent(t, database.primary, eventID); row.AlertedAt != nil {
				t.Fatalf("failed alert batch partially marked event: %s", outboxContractRowSummary(row))
			}
		}
	})
}

// outboxContractRowSummary 只保留固定状态字段，避免测试失败日志泄露 payload、metadata、Owner 或 token。
func outboxContractRowSummary(row DomainEventOutbox) string {
	return fmt.Sprintf(
		"id=%d status=%s attempts=%d/%d lease=%t worker=%t processed=%t deadLetter=%t errorCategory=%t",
		row.ID,
		row.Status,
		row.AttemptCount,
		row.MaxAttempts,
		row.LeaseID != nil,
		row.WorkerID != nil,
		row.ProcessedAt != nil,
		row.DeadLetteredAt != nil,
		row.LastErrorCategory != nil,
	)
}

func outboxContractRowsSummary(rows []DomainEventOutbox) []string {
	summaries := make([]string, len(rows))
	for index := range rows {
		summaries[index] = outboxContractRowSummary(rows[index])
	}
	return summaries
}

func insertPendingOutboxEvent(t *testing.T, database *gorm.DB, eventType string, maxAttempts int) int64 {
	t.Helper()
	now := time.Now().UTC().Add(-time.Minute)
	var eventID int64
	err := database.Raw(`
INSERT INTO domain_event_outbox (
    event_type, aggregate_type, aggregate_id, payload_json, metadata_json,
    status, attempt_count, max_attempts, available_at, created_at, updated_at
) VALUES (?, 'contract', 'fixture', '{}', '{}', 'pending', 0, ?, ?, ?, ?)
RETURNING id
`, eventType, maxAttempts, now, now, now).Scan(&eventID).Error
	if err != nil {
		t.Fatalf("insert pending Outbox event: %v", err)
	}
	return eventID
}

func insertDeadLetterOutboxEvent(t *testing.T, database *gorm.DB, eventType string) int64 {
	t.Helper()
	now := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	var eventID int64
	err := database.Raw(`
INSERT INTO domain_event_outbox (
    event_type, aggregate_type, aggregate_id, payload_json, metadata_json,
    status, attempt_count, max_attempts, available_at, processed_at,
    last_error_category, last_error_message, dead_lettered_at, created_at, updated_at
) VALUES (?, 'contract', 'fixture', '{}', '{}', 'dead_letter', 1, 1, ?, ?,
          'subscriber_failed', '', ?, ?, ?)
RETURNING id
`, eventType, now, now, now, now, now).Scan(&eventID).Error
	if err != nil {
		t.Fatalf("insert dead-letter Outbox event: %v", err)
	}
	return eventID
}

func readOutboxEvent(t *testing.T, database *gorm.DB, eventID int64) DomainEventOutbox {
	t.Helper()
	var row DomainEventOutbox
	if err := database.Where("id = ?", eventID).Take(&row).Error; err != nil {
		t.Fatalf("read Outbox event %d: %v", eventID, err)
	}
	return row
}

func setOutboxAvailableAt(t *testing.T, database *gorm.DB, eventID int64, availableAt time.Time) {
	t.Helper()
	result := database.Exec("UPDATE domain_event_outbox SET available_at = ? WHERE id = ?", availableAt, eventID)
	if result.Error != nil || result.RowsAffected != 1 {
		t.Fatalf("set Outbox available time: id=%d affected=%d err=%v", eventID, result.RowsAffected, result.Error)
	}
}

func assertOutboxRowsOrdered(t *testing.T, rows []DomainEventOutbox) {
	t.Helper()
	for index := 1; index < len(rows); index++ {
		previous, current := rows[index-1], rows[index]
		if current.AvailableAt.Before(previous.AvailableAt) ||
			(current.AvailableAt.Equal(previous.AvailableAt) && current.ID < previous.ID) {
			t.Fatalf("claimed batch is not ordered: previous=%d current=%d", previous.ID, current.ID)
		}
	}
}

func installOutboxClaimFailureTrigger(t *testing.T, database *gorm.DB) {
	t.Helper()
	var statements []string
	switch database.Dialector.Name() {
	case "sqlite":
		statements = []string{`
CREATE TRIGGER outbox_contract_reject_claim
BEFORE UPDATE ON domain_event_outbox
FOR EACH ROW
WHEN NEW.status = 'claimed' AND NEW.event_type = 'contract.atomic.fail'
BEGIN
    SELECT RAISE(ABORT, 'forced Outbox claim failure');
END
`}
	case "postgres":
		statements = []string{
			`CREATE FUNCTION outbox_contract_reject_claim() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status = 'claimed' AND NEW.event_type = 'contract.atomic.fail' THEN
        RAISE EXCEPTION 'forced Outbox claim failure';
    END IF;
    RETURN NEW;
END
$$`,
			`CREATE TRIGGER outbox_contract_reject_claim
BEFORE UPDATE ON domain_event_outbox
FOR EACH ROW EXECUTE FUNCTION outbox_contract_reject_claim()`,
		}
	default:
		t.Fatalf("unsupported Outbox contract driver %q", database.Dialector.Name())
	}
	for _, statement := range statements {
		if err := database.Exec(statement).Error; err != nil {
			t.Fatalf("install Outbox claim failure trigger: %v", err)
		}
	}
}

func installOutboxAlertFailureTrigger(t *testing.T, database *gorm.DB) {
	t.Helper()
	var statements []string
	switch database.Dialector.Name() {
	case "sqlite":
		statements = []string{`
CREATE TRIGGER outbox_contract_reject_alert
BEFORE UPDATE ON domain_event_outbox
FOR EACH ROW
WHEN NEW.alerted_at IS NOT NULL AND NEW.event_type = 'contract.alert.atomic.fail'
BEGIN
    SELECT RAISE(ABORT, 'forced Outbox alert failure');
END
`}
	case "postgres":
		statements = []string{
			`CREATE FUNCTION outbox_contract_reject_alert() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.alerted_at IS NOT NULL AND NEW.event_type = 'contract.alert.atomic.fail' THEN
        RAISE EXCEPTION 'forced Outbox alert failure';
    END IF;
    RETURN NEW;
END
$$`,
			`CREATE TRIGGER outbox_contract_reject_alert
BEFORE UPDATE ON domain_event_outbox
FOR EACH ROW EXECUTE FUNCTION outbox_contract_reject_alert()`,
		}
	default:
		t.Fatalf("unsupported Outbox contract driver %q", database.Dialector.Name())
	}
	for _, statement := range statements {
		if err := database.Exec(statement).Error; err != nil {
			t.Fatalf("install Outbox alert failure trigger: %v", err)
		}
	}
}

func openSQLiteOutboxContractDatabase(t *testing.T) *outboxContractDatabase {
	t.Helper()
	cfg := config.DatabaseConfig{
		Driver: "sqlite",
		Path:   filepath.Join(t.TempDir(), "outbox-contract.db"),
	}
	primary := openOutboxContractHandle(t, cfg)
	applyOutboxContractMigrations(t, primary, "sqlite")
	prepareOutboxContractRelations(t, primary)
	return &outboxContractDatabase{
		primary: primary,
		openPeer: func(t *testing.T) *gorm.DB {
			return openOutboxContractHandle(t, cfg)
		},
	}
}

func openOutboxContractHandle(t *testing.T, cfg config.DatabaseConfig) *gorm.DB {
	t.Helper()
	database, err := Connect(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open %s Outbox contract database: %v", cfg.Driver, err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatalf("get %s Outbox contract database: %v", cfg.Driver, err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close %s Outbox contract database: %v", cfg.Driver, err)
		}
	})
	return database
}

func openPostgresOutboxContractDatabase(t *testing.T) *outboxContractDatabase {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv(postgresMigrationDSNEnv))
	if dsn == "" {
		if os.Getenv("CI") != "" {
			t.Fatalf("%s is required in CI", postgresMigrationDSNEnv)
		}
		t.Skipf("%s is not configured", postgresMigrationDSNEnv)
	}
	adminConfig, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse PostgreSQL Outbox contract DSN: %v", err)
	}
	adminDB := stdlib.OpenDB(*adminConfig)
	if err := adminDB.Ping(); err != nil {
		_ = adminDB.Close()
		t.Fatalf("ping PostgreSQL Outbox contract database: %v", err)
	}
	schemaName := fmt.Sprintf(
		"outbox_contract_%d_%d_%d",
		os.Getpid(),
		time.Now().UnixNano(),
		outboxContractSchemaSequence.Add(1),
	)
	quotedSchema := `"` + strings.ReplaceAll(schemaName, `"`, `""`) + `"`
	if _, err := adminDB.Exec("CREATE SCHEMA " + quotedSchema); err != nil {
		_ = adminDB.Close()
		t.Fatalf("create PostgreSQL Outbox contract schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := adminDB.Exec("DROP SCHEMA " + quotedSchema + " CASCADE"); err != nil {
			t.Errorf("drop PostgreSQL Outbox contract schema: %v", err)
		}
		if err := adminDB.Close(); err != nil {
			t.Errorf("close PostgreSQL Outbox contract admin database: %v", err)
		}
	})

	testConfig := adminConfig.Copy()
	if testConfig.RuntimeParams == nil {
		testConfig.RuntimeParams = make(map[string]string)
	}
	testConfig.RuntimeParams["search_path"] = schemaName
	primary := openPostgresOutboxHandle(t, testConfig)
	applyOutboxContractMigrations(t, primary, "postgres")
	prepareOutboxContractRelations(t, primary)
	return &outboxContractDatabase{
		primary: primary,
		openPeer: func(t *testing.T) *gorm.DB {
			return openPostgresOutboxHandle(t, testConfig)
		},
	}
}

func openPostgresOutboxHandle(t *testing.T, cfg *pgx.ConnConfig) *gorm.DB {
	t.Helper()
	sqlDB := stdlib.OpenDB(*cfg)
	if err := sqlDB.Ping(); err != nil {
		_ = sqlDB.Close()
		t.Fatalf("ping PostgreSQL Outbox contract schema: %v", err)
	}
	database, err := gorm.Open(gormpostgres.New(gormpostgres.Config{Conn: sqlDB}), &gorm.Config{})
	if err != nil {
		_ = sqlDB.Close()
		t.Fatalf("open PostgreSQL Outbox GORM handle: %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close PostgreSQL Outbox contract handle: %v", err)
		}
	})
	return database
}

func applyOutboxContractMigrations(t *testing.T, database *gorm.DB, driver string) {
	t.Helper()
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatalf("get %s migration database: %v", driver, err)
	}
	runner, err := migration.New(sqlDB, driver)
	if err != nil {
		t.Fatalf("create %s Outbox migration runner: %v", driver, err)
	}
	if _, err := runner.Up(context.Background(), 0); err != nil {
		t.Fatalf("apply %s Outbox migrations: %v", driver, err)
	}
}

// 00003 可先于业务父表应用；随后复现当前服务启动路径，只迁移其余模型并用占位模型
// 保留 Outbox DDL，确保双方言契约测试运行在实际可写的最终关系结构上。
func prepareOutboxContractRelations(t *testing.T, database *gorm.DB) {
	t.Helper()
	if err := database.AutoMigrate(autoMigrateModels(true)...); err != nil {
		t.Fatalf("prepare Outbox contract relations: %v", err)
	}
}
