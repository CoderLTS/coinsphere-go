package service

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"coinsphere/backend/internal/config"
	"coinsphere/backend/internal/db"
)

// TestDrainPendingEventsClaimsAndCompletes 从服务入口验证成功事件必须经过 claim/complete，
// 而不是退回旧的先查后改路径；认领次数和租约清理同时固定该状态机契约。
func TestDrainPendingEventsClaimsAndCompletes(t *testing.T) {
	database := openMigratedServiceDatabase(t)
	app := newOutboxServiceTestApp(database, 5)
	outboxID := createServiceTestOutbox(t, app, "service.delivery.success", M{"value": "ok"}, M{"source": "test"})

	app.drainPendingEvents(context.Background(), 10)

	record := loadServiceTestOutbox(t, database, outboxID)
	if record.Status != "processed" || record.AttemptCount != 1 || record.ProcessedAt == nil {
		t.Fatalf("unexpected completed Outbox state: status=%s attempt=%d processed_at=%v", record.Status, record.AttemptCount, record.ProcessedAt)
	}
	if record.LeaseID != nil || record.WorkerID != nil || record.LeaseExpiresAt != nil || record.ClaimedAt != nil {
		t.Fatalf("completed Outbox retained lease: lease=%v worker=%v expires=%v claimed=%v", record.LeaseID, record.WorkerID, record.LeaseExpiresAt, record.ClaimedAt)
	}
}

// TestSubscriberQueryFailureIsFencedAndRedacted 让订阅查询真实失败，再用旧 claim 触发一次写入。
// 新租约必须保持不变，且应用日志只能出现固定操作、ID 和分类，不能泄露事件正文或数据库异常正文。
func TestSubscriberQueryFailureIsFencedAndRedacted(t *testing.T) {
	database := openMigratedServiceDatabase(t)
	app := newOutboxServiceTestApp(database, 5)
	payloadMarker := "payload-secret-service-marker"
	metadataMarker := "metadata-secret-service-marker"
	outboxID := createServiceTestOutbox(t, app, "service.delivery.query-failure", M{"secret": payloadMarker}, M{"secret": metadataMarker})

	claims, err := db.ClaimOutboxEvents(context.Background(), database, app.WorkerID, 1, 5*time.Second)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claim query-failure Outbox: claims=%d err=%v", len(claims), err)
	}
	oldClaim := claims[0]
	if err := database.Exec("DROP TABLE workflow_runtime_entries").Error; err != nil {
		t.Fatalf("drop subscriber table: %v", err)
	}

	logs := captureServiceTestLogs(t)
	app.deliverClaimedOutboxEvent(context.Background(), oldClaim)
	requeued := loadServiceTestOutbox(t, database, outboxID)
	if requeued.Status != "pending" || requeued.AttemptCount != 1 || requeued.LastErrorCategory == nil || *requeued.LastErrorCategory != outboxFailureQuery {
		t.Fatalf("unexpected query-failure requeue: status=%s attempt=%d category=%v", requeued.Status, requeued.AttemptCount, requeued.LastErrorCategory)
	}
	if requeued.LastErrorMessage != "" || requeued.LeaseID != nil || requeued.WorkerID != nil {
		t.Fatalf("query-failure requeue retained sensitive error or lease: error=%q lease=%v worker=%v", requeued.LastErrorMessage, requeued.LeaseID, requeued.WorkerID)
	}

	newClaims, err := db.ClaimOutboxEvents(context.Background(), database, "new-owner", 1, 5*time.Second)
	if err != nil || len(newClaims) != 1 {
		t.Fatalf("reclaim query-failure Outbox: claims=%d err=%v", len(newClaims), err)
	}
	app.deliverClaimedOutboxEvent(context.Background(), oldClaim)
	stillClaimed := loadServiceTestOutbox(t, database, outboxID)
	if stillClaimed.Status != "claimed" || stillClaimed.AttemptCount != newClaims[0].AttemptCount || stillClaimed.LeaseID == nil || *stillClaimed.LeaseID != *newClaims[0].LeaseID {
		t.Fatalf("old fencing token changed new claim: status=%s attempt=%d lease=%v", stillClaimed.Status, stillClaimed.AttemptCount, stillClaimed.LeaseID)
	}

	for _, marker := range []string{payloadMarker, metadataMarker, "no such table", "workflow_runtime_entries"} {
		if strings.Contains(logs.String(), marker) {
			t.Fatalf("application log leaked marker %q: %s", marker, logs.String())
		}
	}
}

// TestBacklogExhaustionDeadLettersAndAlertsOnce 用真实事件订阅入口制造持续积压。
// 三次失败必须耗尽默认尝试次数并在同轮领取告警；后续 drain 不得再次标记或输出同一死信。
func TestBacklogExhaustionDeadLettersAndAlertsOnce(t *testing.T) {
	database := openMigratedServiceDatabase(t)
	app := newOutboxServiceTestApp(database, 5)
	app.Cfg.Workflow.BacklogLimitPerKey = 1
	definition, entry := createServiceTestSubscriber(t, database, "service.delivery.backlog")
	if err := database.Create(&db.WorkflowExecution{
		OwnerUserID:          1,
		WorkflowDefinitionID: definition.ID,
		StartEntryKey:        entry.EntryKey,
		StartNodeID:          entry.StartNodeID,
		StartNodeType:        entry.StartType,
		TriggerType:          "manual",
		ConcurrencyKey:       definition.Code + ":" + entry.EntryKey,
		Status:               "queued",
		QueuedAt:             time.Now(),
		MaxAttempts:          1,
	}).Error; err != nil {
		t.Fatalf("create backlog execution: %v", err)
	}
	outboxID := createServiceTestOutbox(t, app, "service.delivery.backlog", M{"value": "blocked"}, M{})
	logs := captureServiceTestLogs(t)

	for range 4 {
		app.drainPendingEvents(context.Background(), 10)
	}

	record := loadServiceTestOutbox(t, database, outboxID)
	if record.Status != "dead_letter" || record.AttemptCount != 3 || record.DeadLetteredAt == nil || record.AlertedAt == nil {
		t.Fatalf("unexpected exhausted Outbox state: status=%s attempt=%d dead=%v alerted=%v", record.Status, record.AttemptCount, record.DeadLetteredAt, record.AlertedAt)
	}
	if record.LastErrorCategory == nil || *record.LastErrorCategory != outboxFailureBacklog {
		t.Fatalf("dead-letter category = %v, want %s", record.LastErrorCategory, outboxFailureBacklog)
	}
	alertLine := `"msg":"outbox dead letter"`
	if count := strings.Count(logs.String(), alertLine); count != 1 {
		t.Fatalf("dead letter alert count = %d, want 1; logs=%s", count, logs.String())
	}
}

// TestSlowSubscriberKeepsOneSecondLease 验证实际订阅入队超过初始一秒租约时，heartbeat 会持续续租。
// 延迟放在第二次 runtime-entry 查询，即真正进入订阅处理后，避免把普通 Outbox 查询伪装成慢订阅。
func TestSlowSubscriberKeepsOneSecondLease(t *testing.T) {
	database := openMigratedServiceDatabase(t)
	app := newOutboxServiceTestApp(database, 1)
	createServiceTestSubscriber(t, database, "service.delivery.slow")
	outboxID := createServiceTestOutbox(t, app, "service.delivery.slow", M{"value": "slow"}, M{})

	entryQueries := 0
	callbackName := "service-test:slow-subscriber"
	if err := database.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table != "workflow_runtime_entries" {
			return
		}
		entryQueries++
		if entryQueries == 2 {
			time.Sleep(1500 * time.Millisecond)
		}
	}); err != nil {
		t.Fatalf("register slow subscriber callback: %v", err)
	}
	t.Cleanup(func() { _ = database.Callback().Query().Remove(callbackName) })

	app.drainPendingEvents(context.Background(), 1)

	record := loadServiceTestOutbox(t, database, outboxID)
	if record.Status != "processed" || record.AttemptCount != 1 {
		t.Fatalf("slow subscriber lost lease: status=%s attempt=%d", record.Status, record.AttemptCount)
	}
	if entryQueries < 2 {
		t.Fatalf("slow subscriber callback observed %d runtime-entry queries, want at least 2", entryQueries)
	}
	var executions int64
	if err := database.Model(&db.WorkflowExecution{}).Where("trigger_outbox_id = ?", outboxID).Count(&executions).Error; err != nil {
		t.Fatalf("count slow-subscriber executions: %v", err)
	}
	if executions != 1 {
		t.Fatalf("slow subscriber executions = %d, want 1", executions)
	}
}

// TestDrainClaimsEachEventWhenReadyToDeliver 固定 dispatcher 的即时认领边界：慢首项处理期间，
// 第二项仍应保持 pending；即使另一 worker 执行过期恢复，也不能提前消耗第二项的 attempt。
func TestDrainClaimsEachEventWhenReadyToDeliver(t *testing.T) {
	database := openMigratedServiceDatabase(t)
	app := newOutboxServiceTestApp(database, 1)
	firstID := createServiceTestOutbox(t, app, "service.delivery.batch-first", M{}, M{})
	secondID := createServiceTestOutbox(t, app, "service.delivery.batch-second", M{}, M{})

	blocked := make(chan struct{})
	release := make(chan struct{})
	var blockOnce sync.Once
	callbackName := "service-test:block-first-delivery"
	if err := database.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == "workflow_runtime_entries" {
			blockOnce.Do(func() {
				close(blocked)
				<-release
			})
		}
	}); err != nil {
		t.Fatalf("register first-delivery blocker: %v", err)
	}
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		_ = database.Callback().Query().Remove(callbackName)
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		app.drainPendingEvents(context.Background(), 2)
	}()
	select {
	case <-blocked:
	case <-time.After(time.Second):
		t.Fatal("first Outbox delivery did not start")
	}

	claimedAt := time.Now().UTC().Add(-2 * time.Minute)
	expiresAt := claimedAt.Add(time.Minute)
	if err := database.Model(&db.DomainEventOutbox{}).
		Where("id = ? AND status = ?", secondID, "claimed").
		Updates(map[string]any{"claimed_at": claimedAt, "lease_expires_at": expiresAt}).Error; err != nil {
		t.Fatalf("expire preclaimed batch tail: %v", err)
	}
	recovered, err := db.RecoverExpiredOutboxEvents(context.Background(), database, 2)
	if err != nil {
		t.Fatalf("recover while first delivery is blocked: %v", err)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Outbox batch did not finish")
	}

	if len(recovered) != 0 {
		t.Fatalf("batch tail was claimed before delivery capacity was available: recovered=%v", recovered)
	}
	for _, id := range []int64{firstID, secondID} {
		record := loadServiceTestOutbox(t, database, id)
		if record.Status != "processed" || record.AttemptCount != 1 {
			t.Fatalf("Outbox %d final state: status=%s attempt=%d", id, record.Status, record.AttemptCount)
		}
	}
}

// TestFinalizeSuccessTransaction 同时固定成功终态的提交面和失败面：execution、attempt、
// 两条标准事件及 runtime entry 必须一起提交；第二条事件失败时第一条事件和全部业务写入必须回滚。
func TestFinalizeSuccessTransaction(t *testing.T) {
	t.Run("commit", func(t *testing.T) {
		database := openMigratedServiceDatabase(t)
		app := newOutboxServiceTestApp(database, 5)
		definition, entry, execution := createTerminalServiceFixture(t, database, app, "finalize-success")
		result := terminalSuccessResult(definition, entry, execution)

		app.finalizeSuccess(context.Background(), execution, 1, result)

		assertTerminalServiceState(t, database, execution.ID, "success", "success")
		assertServiceOutboxTypes(t, database, execution.ID, "workflow.execution.succeeded", "workflow.execution.finished")
		var updatedEntry db.WorkflowRuntimeEntry
		if err := database.First(&updatedEntry, entry.ID).Error; err != nil {
			t.Fatalf("reload successful runtime entry: %v", err)
		}
		if updatedEntry.LastTriggeredAt == nil || updatedEntry.LastErrorMessage != "" {
			t.Fatalf("runtime entry not committed with success: triggered=%v error=%q", updatedEntry.LastTriggeredAt, updatedEntry.LastErrorMessage)
		}
	})

	t.Run("rollback_second_outbox_insert", func(t *testing.T) {
		database := openMigratedServiceDatabase(t)
		app := newOutboxServiceTestApp(database, 5)
		definition, entry, execution := createTerminalServiceFixture(t, database, app, "finalize-success-rollback")
		statements := []string{
			`CREATE FUNCTION service_test_reject_finished_outbox() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'forced finished Outbox insert failure';
END
$$`,
			`CREATE TRIGGER service_test_reject_finished_outbox
BEFORE INSERT ON domain_event_outbox
FOR EACH ROW
WHEN (NEW.event_type = 'workflow.execution.finished')
EXECUTE FUNCTION service_test_reject_finished_outbox()`,
		}
		for _, statement := range statements {
			if err := database.Exec(statement).Error; err != nil {
				t.Fatalf("install finished-Outbox failure trigger: %v", err)
			}
		}
		captureServiceTestLogs(t)

		app.finalizeSuccess(context.Background(), execution, 1, terminalSuccessResult(definition, entry, execution))

		assertTerminalServiceState(t, database, execution.ID, "running", "running")
		assertServiceOutboxTypes(t, database, execution.ID)
		var unchangedEntry db.WorkflowRuntimeEntry
		if err := database.First(&unchangedEntry, entry.ID).Error; err != nil {
			t.Fatalf("reload rolled-back runtime entry: %v", err)
		}
		if unchangedEntry.LastTriggeredAt != nil || unchangedEntry.LastErrorMessage != "before-finalize" {
			t.Fatalf("runtime entry escaped rollback: triggered=%v error=%q", unchangedEntry.LastTriggeredAt, unchangedEntry.LastErrorMessage)
		}
	})

	t.Run("recover_missing_attempt", func(t *testing.T) {
		database := openMigratedServiceDatabase(t)
		app := newOutboxServiceTestApp(database, 5)
		definition, entry, execution := createTerminalServiceFixture(t, database, app, "finalize-success-missing-attempt")
		if err := database.Where("workflow_execution_id = ?", execution.ID).Delete(&db.WorkflowExecutionAttempt{}).Error; err != nil {
			t.Fatalf("remove terminal attempt fixture: %v", err)
		}
		captureServiceTestLogs(t)

		app.finalizeSuccess(context.Background(), execution, 1, terminalSuccessResult(definition, entry, execution))

		var finalized db.WorkflowExecution
		if err := database.First(&finalized, execution.ID).Error; err != nil {
			t.Fatalf("reload missing-attempt execution: %v", err)
		}
		if finalized.Status != "success" || finalized.FinishedAt == nil {
			t.Fatalf("missing attempt prevented terminal recovery: status=%s finished=%v", finalized.Status, finalized.FinishedAt)
		}
		assertTerminalServiceState(t, database, execution.ID, "success", "success")
		assertServiceOutboxTypes(t, database, execution.ID, "workflow.execution.succeeded", "workflow.execution.finished")
	})
}

// TestTerminalFailuresPublishStandardEvents 覆盖没有 runResult 的跑图前失败及 stale 恢复。
// 两条路径都必须在终态事务内补齐 failed/finished 事件，而不是因缺少内存上下文静默漏发。
func TestTerminalFailuresPublishStandardEvents(t *testing.T) {
	t.Run("finalize_failure_without_result", func(t *testing.T) {
		database := openMigratedServiceDatabase(t)
		app := newOutboxServiceTestApp(database, 5)
		_, _, execution := createTerminalServiceFixture(t, database, app, "finalize-failure")

		app.finalizeFailure(context.Background(), execution, 1, nil, errors.New("terminal business failure"))

		assertTerminalServiceState(t, database, execution.ID, "failed", "failed")
		assertServiceOutboxTypes(t, database, execution.ID, "workflow.execution.failed", "workflow.execution.finished")
	})

	t.Run("recover_stale_final_failure", func(t *testing.T) {
		database := openMigratedServiceDatabase(t)
		app := newOutboxServiceTestApp(database, 5)
		_, _, execution := createTerminalServiceFixture(t, database, app, "recover-stale")
		execution.ConcurrencyKey = "recover-stale-key"
		if err := database.Model(&db.WorkflowExecution{}).Where("id = ?", execution.ID).Update("concurrency_key", execution.ConcurrencyKey).Error; err != nil {
			t.Fatalf("set stale concurrency key: %v", err)
		}
		app.runningKeys[execution.ConcurrencyKey] = 1

		app.recoverStaleExecution(context.Background(), execution)

		assertTerminalServiceState(t, database, execution.ID, "failed", "failed")
		assertServiceOutboxTypes(t, database, execution.ID, "workflow.execution.failed", "workflow.execution.finished")
		var recovered db.WorkflowExecution
		if err := database.First(&recovered, execution.ID).Error; err != nil {
			t.Fatalf("reload recovered execution: %v", err)
		}
		if recovered.FailureCategory != "worker_lost" {
			t.Fatalf("recovered failure category = %q, want worker_lost", recovered.FailureCategory)
		}
		if _, held := app.runningKeys[execution.ConcurrencyKey]; held {
			t.Fatal("stale recovery did not release the concurrency key")
		}
	})

	t.Run("heartbeat_after_scan_fences_stale_snapshot", func(t *testing.T) {
		database := openMigratedServiceDatabase(t)
		app := newOutboxServiceTestApp(database, 5)
		_, _, execution := createTerminalServiceFixture(t, database, app, "recover-heartbeat-race")
		execution.ConcurrencyKey = "recover-heartbeat-race-key"
		app.runningKeys[execution.ConcurrencyKey] = 1
		healthyHeartbeat := time.Now().UTC()
		if err := database.Model(&db.WorkflowExecution{}).Where("id = ?", execution.ID).
			Updates(map[string]any{
				"concurrency_key":   execution.ConcurrencyKey,
				"last_heartbeat_at": healthyHeartbeat,
			}).Error; err != nil {
			t.Fatalf("renew heartbeat after stale scan: %v", err)
		}

		app.recoverStaleExecution(context.Background(), execution)

		assertTerminalServiceState(t, database, execution.ID, "running", "running")
		assertServiceOutboxTypes(t, database, execution.ID)
		if _, held := app.runningKeys[execution.ConcurrencyKey]; !held {
			t.Fatal("stale snapshot released the healthy execution concurrency key")
		}
	})
}

func openMigratedServiceDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	return openPostgresWorkflowContractDatabase(t).primary
}

func newOutboxServiceTestApp(database *gorm.DB, leaseSeconds int) *App {
	return &App{
		DB:       database,
		database: database,
		Cfg: &config.AppConfig{Workflow: config.WorkflowConfig{
			OutboxLeaseSeconds:     leaseSeconds,
			BacklogLimitPerKey:     10,
			SemaphoreLimitPerKey:   1,
			MaxAttempts:            1,
			RetryBackoffSeconds:    []int{0},
			MaxInputSnapshotBytes:  4096,
			MaxOutputSnapshotBytes: 4096,
		}},
		WorkerID:       "service-outbox-worker",
		runningKeys:    map[string]int{},
		dispatcherWake: make(chan struct{}, 1),
	}
}

func createServiceTestOutbox(t *testing.T, app *App, eventType string, payload, metadata M) int64 {
	t.Helper()
	outboxID, err := app.publishDomainEvent(eventType, "service_test", eventType, payload, metadata, nil, nil)
	if err != nil {
		t.Fatalf("create service Outbox %s: %v", eventType, err)
	}
	return outboxID
}

func loadServiceTestOutbox(t *testing.T, database *gorm.DB, outboxID int64) db.DomainEventOutbox {
	t.Helper()
	var record db.DomainEventOutbox
	if err := database.First(&record, outboxID).Error; err != nil {
		t.Fatalf("load service Outbox %d: %v", outboxID, err)
	}
	return record
}

func createServiceTestSubscriber(t *testing.T, database *gorm.DB, eventType string) (*db.WorkflowDefinition, *db.WorkflowRuntimeEntry) {
	t.Helper()
	entryKey := "event-entry"
	ownerID := int64(1)
	definition := &db.WorkflowDefinition{
		OwnerUserID: &ownerID,
		Code:        eventType,
		Version:     1,
		DisplayName: eventType,
		GraphJSON: dumpJSON(M{"schemaVersion": 2, "nodes": []any{
			M{"id": "event-start", "type": "start.event", "config": M{"entryKey": entryKey, "eventType": eventType}},
		}, "edges": []any{}}),
	}
	if err := database.Create(definition).Error; err != nil {
		t.Fatalf("create event subscriber definition: %v", err)
	}
	state := &db.WorkflowRuntimeState{OwnerUserID: ownerID, WorkflowCode: definition.Code, ActiveWorkflowDefinitionID: &definition.ID}
	if err := database.Create(state).Error; err != nil {
		t.Fatalf("create event subscriber state: %v", err)
	}
	entry := &db.WorkflowRuntimeEntry{
		WorkflowRuntimeStateID: state.ID,
		WorkflowDefinitionID:   definition.ID,
		StartNodeID:            "event-start",
		EntryKey:               entryKey,
		StartType:              "event",
		IsEnabled:              true,
		RegistrationStatus:     "ready",
	}
	if err := database.Create(entry).Error; err != nil {
		t.Fatalf("create event subscriber entry: %v", err)
	}
	return definition, entry
}

func createTerminalServiceFixture(t *testing.T, database *gorm.DB, app *App, code string) (*db.WorkflowDefinition, *db.WorkflowRuntimeEntry, *db.WorkflowExecution) {
	t.Helper()
	definition, entry := createServiceTestSubscriber(t, database, code)
	entry.LastErrorMessage = "before-finalize"
	if err := database.Model(&db.WorkflowRuntimeEntry{}).Where("id = ?", entry.ID).Update("last_error_message", entry.LastErrorMessage).Error; err != nil {
		t.Fatalf("seed runtime entry error: %v", err)
	}
	now := time.Now().Add(-time.Second)
	workerID := app.WorkerID
	execution := &db.WorkflowExecution{
		OwnerUserID:          1,
		WorkflowDefinitionID: definition.ID,
		StartEntryKey:        entry.EntryKey,
		StartNodeID:          entry.StartNodeID,
		StartNodeType:        entry.StartType,
		TriggerType:          "event",
		Status:               "running",
		QueuedAt:             now,
		ClaimedAt:            &now,
		StartedAt:            &now,
		LastHeartbeatAt:      &now,
		WorkerID:             &workerID,
		AttemptCount:         1,
		MaxAttempts:          1,
		ContextSnapshotJSON:  dumpJSON(M{"triggerType": "event", "payload": M{"fixture": code}}),
		ResultSnapshotJSON:   "{}",
	}
	if err := database.Create(execution).Error; err != nil {
		t.Fatalf("create terminal execution: %v", err)
	}
	if err := database.Create(&db.WorkflowExecutionAttempt{
		WorkflowExecutionID: execution.ID,
		Attempt:             1,
		WorkerID:            workerID,
		StartedAt:           now,
		Status:              "running",
	}).Error; err != nil {
		t.Fatalf("create terminal execution attempt: %v", err)
	}
	return definition, entry, execution
}

func terminalSuccessResult(definition *db.WorkflowDefinition, entry *db.WorkflowRuntimeEntry, execution *db.WorkflowExecution) *runResult {
	return &runResult{
		Execution:    execution,
		Definition:   definition,
		RuntimeEntry: entry,
		TriggerCtx:   M{"triggerType": "event"},
		SharedState: M{
			"taskResult":  M{"value": "ok"},
			"nodeOutputs": M{"end": M{"value": "ok"}},
		},
		StartedAt:  *execution.StartedAt,
		FinishedAt: execution.StartedAt.Add(time.Second),
	}
}

func assertTerminalServiceState(t *testing.T, database *gorm.DB, executionID int64, executionStatus, attemptStatus string) {
	t.Helper()
	var execution db.WorkflowExecution
	if err := database.First(&execution, executionID).Error; err != nil {
		t.Fatalf("reload terminal execution: %v", err)
	}
	if execution.Status != executionStatus {
		t.Fatalf("execution status = %q, want %q", execution.Status, executionStatus)
	}
	var attempt db.WorkflowExecutionAttempt
	if err := database.Where("workflow_execution_id = ? AND attempt = ?", executionID, 1).First(&attempt).Error; err != nil {
		t.Fatalf("reload terminal attempt: %v", err)
	}
	if attempt.Status != attemptStatus {
		t.Fatalf("attempt status = %q, want %q", attempt.Status, attemptStatus)
	}
	if executionStatus == "running" {
		if execution.FinishedAt != nil || attempt.FinishedAt != nil {
			t.Fatalf("rolled-back terminal timestamps remained: execution=%v attempt=%v", execution.FinishedAt, attempt.FinishedAt)
		}
		return
	}
	if execution.FinishedAt == nil || attempt.FinishedAt == nil {
		t.Fatalf("terminal timestamps missing: execution=%v attempt=%v", execution.FinishedAt, attempt.FinishedAt)
	}
}

func assertServiceOutboxTypes(t *testing.T, database *gorm.DB, executionID int64, want ...string) {
	t.Helper()
	var records []db.DomainEventOutbox
	if err := database.Where("workflow_execution_id = ?", executionID).Order("id ASC").Find(&records).Error; err != nil {
		t.Fatalf("load execution Outbox events: %v", err)
	}
	if len(records) != len(want) {
		t.Fatalf("execution Outbox count = %d, want %d", len(records), len(want))
	}
	for index := range want {
		if records[index].EventType != want[index] || records[index].Status != "pending" {
			t.Fatalf("Outbox[%d] = type %q status %q, want type %q status pending", index, records[index].EventType, records[index].Status, want[index])
		}
	}
}

func captureServiceTestLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	buffer := &bytes.Buffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buffer, nil)))
	t.Cleanup(func() {
		slog.SetDefault(previous)
	})
	return buffer
}
