package service

import (
	"log"
	"strings"
	"time"

	"coinsphere/backend/internal/db"
)

// StartRuntime 启动全部后台循环:调度、派发、事件、恢复与清理。
// 单进程内 goroutine 协作,替代原 orchestrator/worker 双进程 + Redis。
func (a *App) StartRuntime() {
	a.bootstrapRuntimeEntries()
	a.reconcileScheduleRegistrations()

	a.spawn(a.schedulerLoop)
	a.spawn(a.dispatchLoop)
	a.spawn(a.eventOutboxLoop)
	a.spawn(a.staleRecoveryLoop)
	a.spawn(a.cleanupLoop)
	log.Printf("[runtime] started: worker_id=%s concurrency=%d", a.WorkerID, a.Cfg.Workflow.ExecutorConcurrency)
}

// StopRuntime 通知全部循环退出并等待收尾。
func (a *App) StopRuntime() {
	close(a.stop)
	a.wg.Wait()
	log.Printf("[runtime] stopped")
}

func (a *App) spawn(loop func()) {
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		loop()
	}()
}

func (a *App) sleeping(interval time.Duration) bool {
	select {
	case <-a.stop:
		return false
	case <-time.After(interval):
		return true
	}
}

func (a *App) wakeDispatcher() {
	select {
	case a.dispatcherWake <- struct{}{}:
	default:
	}
}

// bootstrapRuntimeEntries 启动时为已激活但没有入口记录的 workflow 重建入口
// (覆盖种子数据只写激活状态不写入口的情况)。
func (a *App) bootstrapRuntimeEntries() {
	var states []db.WorkflowRuntimeState
	a.DB.Where("active_workflow_definition_id IS NOT NULL").Find(&states)
	for i := range states {
		state := &states[i]
		var entryCount int64
		a.DB.Model(&db.WorkflowRuntimeEntry{}).Where("workflow_runtime_state_id = ?", state.ID).Count(&entryCount)
		if entryCount > 0 {
			continue
		}
		if err := a.reconcileRuntimeEntriesForState(state, false); err != nil {
			log.Printf("[runtime] bootstrap entries failed: workflow_code=%s err=%v", state.WorkflowCode, err)
		}
	}
}

// ---------- 调度循环 ----------

// schedulerLoop 扫描到期的 schedule 入口并入队执行。
func (a *App) schedulerLoop() {
	pollInterval := time.Duration(a.Cfg.Workflow.PollIntervalMs) * time.Millisecond
	reconcileInterval := time.Duration(a.Cfg.Workflow.ScheduleReconcileIntervalSeconds) * time.Second
	lastReconcile := time.Now()
	for a.sleeping(pollInterval) {
		a.fireDueScheduleEntries()
		if time.Since(lastReconcile) >= reconcileInterval {
			a.reconcileScheduleRegistrations()
			lastReconcile = time.Now()
		}
	}
}

func (a *App) fireDueScheduleEntries() {
	now := time.Now()
	var entries []db.WorkflowRuntimeEntry
	a.DB.Joins("JOIN workflow_runtime_states ON workflow_runtime_entries.workflow_runtime_state_id = workflow_runtime_states.id").
		Where(
			"workflow_runtime_entries.start_type = ? AND workflow_runtime_entries.is_enabled = ? "+
				"AND workflow_runtime_entries.registration_status = ? "+
				"AND workflow_runtime_entries.next_run_at IS NOT NULL AND workflow_runtime_entries.next_run_at <= ? "+
				"AND workflow_runtime_states.active_workflow_definition_id = workflow_runtime_entries.workflow_definition_id",
			"schedule", true, "registered", now,
		).
		Limit(a.Cfg.Workflow.OutboxBatchSize).
		Find(&entries)

	for i := range entries {
		entry := &entries[i]
		dueAt := *entry.NextRunAt
		// 先推进 next_run_at 抢占触发权(乐观锁),避免重复触发。
		next := a.computeEntryNextRun(entry, now)
		claim := a.DB.Model(&db.WorkflowRuntimeEntry{}).
			Where("id = ? AND next_run_at = ?", entry.ID, dueAt)
		var updated int64
		if next != nil {
			updated = claim.Update("next_run_at", *next).RowsAffected
		} else {
			updated = claim.Update("next_run_at", nil).RowsAffected
		}
		if updated == 0 {
			continue
		}
		timestamp := int64Text(dueAt.Unix())
		_, err := a.RunRuntimeEntry(entry.ID, M{
			"triggerType":    "schedule",
			"triggerKey":     "schedule:" + int64Text(entry.ID) + ":" + timestamp,
			"idempotencyKey": "schedule:" + int64Text(entry.ID) + ":" + timestamp,
			"payload":        M{},
		})
		if err != nil && !isBacklogExceeded(err) {
			log.Printf("[scheduler] fire entry failed: entry_id=%d err=%v", entry.ID, err)
			a.DB.Model(&db.WorkflowRuntimeEntry{}).Where("id = ?", entry.ID).
				Updates(map[string]any{"last_error_message": err.Error(), "updated_at": time.Now()})
		}
	}
}

func (a *App) computeEntryNextRun(entry *db.WorkflowRuntimeEntry, after time.Time) *time.Time {
	definition, err := a.requireDefinition(entry.WorkflowDefinitionID)
	if err != nil {
		return nil
	}
	graph := loadJSONObject(definition.GraphJSON)
	startNode := findStartNodeByEntryKey(graph, entry.EntryKey, "start.schedule")
	if startNode == nil {
		return nil
	}
	config, _ := startNode["config"].(map[string]any)
	next, err := computeNextScheduleTime(config, after)
	if err != nil {
		return nil
	}
	return next
}

// reconcileScheduleRegistrations 对齐 schedule 入口注册状态。
func (a *App) reconcileScheduleRegistrations() {
	var states []db.WorkflowRuntimeState
	a.DB.Find(&states)
	for i := range states {
		state := &states[i]
		activeDefinitionID := int64(0)
		if state.ActiveWorkflowDefinitionID != nil {
			activeDefinitionID = *state.ActiveWorkflowDefinitionID
		}
		var entries []db.WorkflowRuntimeEntry
		a.DB.Where("workflow_runtime_state_id = ?", state.ID).Find(&entries)
		for j := range entries {
			entry := &entries[j]
			if entry.StartType != "schedule" {
				status := "disabled"
				if entry.IsEnabled {
					status = "registered"
				}
				if entry.RegistrationStatus != status {
					a.DB.Model(entry).Updates(map[string]any{"registration_status": status, "updated_at": time.Now()})
				}
				continue
			}
			if !entry.IsEnabled || entry.WorkflowDefinitionID != activeDefinitionID {
				if entry.RegistrationStatus != "disabled" {
					a.DB.Model(entry).Updates(map[string]any{
						"registration_status": "disabled", "schedule_job_id": "", "next_run_at": nil, "updated_at": time.Now(),
					})
				}
				continue
			}
			// 缺少 next_run_at 的已启用调度入口重新注册。
			if entry.NextRunAt == nil || entry.RegistrationStatus != "registered" {
				if err := a.registerScheduleEntry(entry); err != nil {
					a.DB.Model(entry).Updates(map[string]any{
						"registration_status": "error", "last_error_message": err.Error(), "updated_at": time.Now(),
					})
				}
			}
		}
	}
}

// ---------- 派发与执行 ----------

// dispatchLoop 提升到期重试 + 认领 queued 执行,交给 worker 池。
func (a *App) dispatchLoop() {
	pollInterval := time.Duration(a.Cfg.Workflow.PollIntervalMs) * time.Millisecond
	slots := make(chan struct{}, a.Cfg.Workflow.ExecutorConcurrency)
	for {
		select {
		case <-a.stop:
			return
		case <-a.dispatcherWake:
		case <-time.After(pollInterval):
		}
		a.promoteDueRetries()
		a.claimAndRun(slots)
	}
}

func (a *App) promoteDueRetries() {
	now := time.Now()
	a.DB.Model(&db.WorkflowExecution{}).
		Where("status = ? AND next_retry_at IS NOT NULL AND next_retry_at <= ?", "retry_waiting", now).
		Updates(map[string]any{"status": "queued", "next_retry_at": nil})
}

func (a *App) claimAndRun(slots chan struct{}) {
	for {
		select {
		case slots <- struct{}{}:
		default:
			return // 并发槽已满。
		}
		execution := a.claimNextExecution()
		if execution == nil {
			<-slots
			return
		}
		a.wg.Add(1)
		go func(execution *db.WorkflowExecution) {
			defer a.wg.Done()
			defer func() { <-slots }()
			a.processExecution(execution)
		}(execution)
	}
}

// claimNextExecution 乐观认领一条 queued 执行(跳过并发键被占用的)。
func (a *App) claimNextExecution() *db.WorkflowExecution {
	var candidates []db.WorkflowExecution
	a.DB.Where("status = ?", "queued").Order("queued_at ASC, id ASC").Limit(20).Find(&candidates)
	for i := range candidates {
		candidate := &candidates[i]
		if !a.tryAcquireKey(candidate.ConcurrencyKey) {
			continue
		}
		now := time.Now()
		result := a.DB.Model(&db.WorkflowExecution{}).
			Where("id = ? AND status = ?", candidate.ID, "queued").
			Updates(map[string]any{
				"status": "running", "claimed_at": now, "started_at": now,
				"finished_at": nil, "last_heartbeat_at": now, "worker_id": a.WorkerID,
				"next_retry_at": nil, "failure_category": "", "error_message": "",
				"attempt_count": candidate.AttemptCount + 1,
			})
		if result.Error != nil || result.RowsAffected == 0 {
			a.releaseKey(candidate.ConcurrencyKey)
			continue
		}
		claimed, err := a.getExecutionByID(candidate.ID)
		if err != nil {
			a.releaseKey(candidate.ConcurrencyKey)
			continue
		}
		return claimed
	}
	return nil
}

// tryAcquireKey 进程内并发键信号量(limit 默认 1)。
func (a *App) tryAcquireKey(key string) bool {
	if key == "" {
		return true
	}
	limit := a.Cfg.Workflow.SemaphoreLimitPerKey
	if limit < 1 {
		limit = 1
	}
	a.runningKeysMu.Lock()
	defer a.runningKeysMu.Unlock()
	if a.runningKeys[key] >= limit {
		return false
	}
	a.runningKeys[key]++
	return true
}

func (a *App) releaseKey(key string) {
	if key == "" {
		return
	}
	a.runningKeysMu.Lock()
	defer a.runningKeysMu.Unlock()
	if a.runningKeys[key] <= 1 {
		delete(a.runningKeys, key)
	} else {
		a.runningKeys[key]--
	}
}

// processExecution 执行一条已认领的 execution:attempt 记录、心跳、跑图、终态回写。
func (a *App) processExecution(execution *db.WorkflowExecution) {
	defer a.releaseKey(execution.ConcurrencyKey)
	attempt := execution.AttemptCount
	startedAt := time.Now()
	if execution.StartedAt != nil {
		startedAt = *execution.StartedAt
	}
	a.DB.Create(&db.WorkflowExecutionAttempt{
		WorkflowExecutionID: execution.ID, Attempt: attempt,
		WorkerID: a.WorkerID, StartedAt: startedAt, Status: "running",
	})

	// 心跳 goroutine:证明本执行仍在运行,供 stale 恢复判定。
	heartbeatStop := make(chan struct{})
	go func() {
		interval := time.Duration(a.Cfg.Workflow.HeartbeatIntervalSeconds) * time.Second
		if interval < time.Second {
			interval = time.Second
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatStop:
				return
			case <-ticker.C:
				a.DB.Model(&db.WorkflowExecution{}).
					Where("id = ? AND status = ? AND attempt_count = ? AND worker_id = ?", execution.ID, "running", attempt, a.WorkerID).
					Update("last_heartbeat_at", time.Now())
			}
		}
	}()

	result, runErr := a.runExecutionGraph(execution.ID)
	close(heartbeatStop)

	if runErr != nil {
		a.finalizeFailure(execution, attempt, result, runErr.Error())
		return
	}
	a.finalizeSuccess(execution, attempt, result)
}

func (a *App) finalizeSuccess(execution *db.WorkflowExecution, attempt int, result *runResult) {
	finishedAt := result.FinishedAt
	durationMs := finishedAt.Sub(result.StartedAt).Milliseconds()
	updated := a.DB.Model(&db.WorkflowExecution{}).
		Where("id = ? AND status = ? AND attempt_count = ? AND worker_id = ?", execution.ID, "running", attempt, a.WorkerID).
		Updates(map[string]any{
			"status": "success", "finished_at": finishedAt, "duration_ms": durationMs,
			"context_snapshot_json": serializeSnapshot(result.SharedState, a.Cfg.Workflow.MaxOutputSnapshotBytes),
			"result_snapshot_json":  serializeSnapshot(orEmptyMap(result.SharedState["nodeOutputs"]), a.Cfg.Workflow.MaxOutputSnapshotBytes),
			"error_message":         "",
		})
	if updated.RowsAffected == 0 {
		return
	}
	a.closeAttempt(execution.ID, attempt, "success", finishedAt, "", "")
	a.publishExecutionSucceeded(result)
}

func (a *App) finalizeFailure(execution *db.WorkflowExecution, attempt int, result *runResult, errorMessage string) {
	failureCategory, retriable := classifyFailure(errorMessage)
	finishedAt := time.Now()
	startedAt := finishedAt
	if result != nil {
		finishedAt = result.FinishedAt
		startedAt = result.StartedAt
	}
	durationMs := finishedAt.Sub(startedAt).Milliseconds()
	maxAttempts := execution.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = a.Cfg.Workflow.MaxAttempts
	}
	nextStatus := "failed"
	var nextRetryAt *time.Time
	if retriable && attempt < maxAttempts {
		nextStatus = "retry_waiting"
		retryAt := a.computeNextRetryAt(attempt)
		nextRetryAt = &retryAt
	}
	updates := map[string]any{
		"status": nextStatus, "finished_at": finishedAt, "duration_ms": durationMs,
		"error_message":    truncateRunes(errorMessage, 4000),
		"failure_category": truncateRunes(failureCategory, 64),
	}
	if nextRetryAt != nil {
		updates["next_retry_at"] = *nextRetryAt
	} else {
		updates["next_retry_at"] = nil
	}
	if result != nil {
		updates["context_snapshot_json"] = serializeSnapshot(result.SharedState, a.Cfg.Workflow.MaxOutputSnapshotBytes)
		updates["result_snapshot_json"] = serializeSnapshot(orEmptyMap(result.SharedState["nodeOutputs"]), a.Cfg.Workflow.MaxOutputSnapshotBytes)
	}
	updated := a.DB.Model(&db.WorkflowExecution{}).
		Where("id = ? AND status = ? AND attempt_count = ? AND worker_id = ?", execution.ID, "running", attempt, a.WorkerID).
		Updates(updates)
	if updated.RowsAffected == 0 {
		return
	}
	a.closeAttempt(execution.ID, attempt, nextStatus, finishedAt, failureCategory, errorMessage)
	if nextStatus == "failed" && result != nil {
		a.publishExecutionFailed(result, errorMessage)
	}
}

func (a *App) closeAttempt(executionID int64, attempt int, status string, finishedAt time.Time, failureCategory, errorSummary string) {
	a.DB.Model(&db.WorkflowExecutionAttempt{}).
		Where("workflow_execution_id = ? AND attempt = ?", executionID, attempt).
		Updates(map[string]any{
			"status": status, "finished_at": finishedAt,
			"failure_category": truncateRunes(failureCategory, 64),
			"error_summary":    truncateRunes(errorSummary, 4000),
		})
}

func (a *App) computeNextRetryAt(attempt int) time.Time {
	backoffs := a.Cfg.Workflow.RetryBackoffSeconds
	if len(backoffs) == 0 {
		backoffs = []int{30, 120, 600}
	}
	index := attempt - 1
	if index < 0 {
		index = 0
	}
	if index >= len(backoffs) {
		index = len(backoffs) - 1
	}
	return time.Now().Add(time.Duration(backoffs[index]) * time.Second)
}

func classifyFailure(errorMessage string) (string, bool) {
	text := strings.ToLower(errorMessage)
	for _, marker := range []string{"timeout", "connection", "429", "502", "503", "504"} {
		if strings.Contains(text, marker) {
			return "infra_retryable", true
		}
	}
	return "business_failed", false
}

// ---------- 事件 outbox 循环 ----------

func (a *App) eventOutboxLoop() {
	interval := time.Duration(a.Cfg.Workflow.OutboxPollIntervalMs) * time.Millisecond
	for a.sleeping(interval) {
		a.drainPendingEvents(a.Cfg.Workflow.OutboxBatchSize)
	}
}

// ---------- Stale 恢复循环 ----------

// staleRecoveryLoop 心跳超时的 running 执行判定为 worker_lost。
// 也覆盖进程崩溃重启后的孤儿执行恢复。
func (a *App) staleRecoveryLoop() {
	interval := time.Duration(a.Cfg.Workflow.StaleRecoveryIntervalSeconds) * time.Second
	for a.sleeping(interval) {
		staleBefore := time.Now().Add(-time.Duration(a.Cfg.Workflow.ExecutionStaleTimeoutSeconds) * time.Second)
		var staleRows []db.WorkflowExecution
		a.DB.Preload("WorkflowDefinition").
			Where("status = ? AND last_heartbeat_at IS NOT NULL AND last_heartbeat_at < ?", "running", staleBefore).
			Order("last_heartbeat_at ASC, id ASC").Limit(a.Cfg.Workflow.OutboxBatchSize).
			Find(&staleRows)
		for i := range staleRows {
			a.recoverStaleExecution(&staleRows[i])
		}
	}
}

func (a *App) recoverStaleExecution(execution *db.WorkflowExecution) {
	finishedAt := time.Now()
	startedAt := firstTime(execution.StartedAt, execution.ClaimedAt, &execution.QueuedAt)
	durationMs := finishedAt.Sub(startedAt).Milliseconds()
	attempt := execution.AttemptCount
	maxAttempts := execution.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = a.Cfg.Workflow.MaxAttempts
	}
	nextStatus := "failed"
	var nextRetryAt *time.Time
	if attempt < maxAttempts {
		nextStatus = "retry_waiting"
		retryAt := a.computeNextRetryAt(attempt)
		nextRetryAt = &retryAt
	}
	updates := map[string]any{
		"status": nextStatus, "finished_at": finishedAt, "duration_ms": durationMs,
		"error_message": "Worker heartbeat timed out", "failure_category": "worker_lost",
	}
	if nextRetryAt != nil {
		updates["next_retry_at"] = *nextRetryAt
	} else {
		updates["next_retry_at"] = nil
	}
	updated := a.DB.Model(&db.WorkflowExecution{}).
		Where("id = ? AND status = ? AND attempt_count = ?", execution.ID, "running", attempt).
		Updates(updates)
	if updated.RowsAffected == 0 {
		return
	}
	a.closeAttempt(execution.ID, attempt, nextStatus, finishedAt, "worker_lost", "Worker heartbeat timed out")
	a.releaseKey(execution.ConcurrencyKey)
	if nextStatus == "failed" {
		a.publishRecoveredFailureEvents(execution.ID, "Worker heartbeat timed out")
	}
	log.Printf("[recovery] stale execution recovered: execution_id=%d next_status=%s", execution.ID, nextStatus)
}

// ---------- 清理循环 ----------

// cleanupLoop 每天 03:00 之后清理超保留期的终态执行。
func (a *App) cleanupLoop() {
	lastCleanupDate := ""
	for a.sleeping(time.Minute) {
		now := time.Now()
		if now.Hour() < 3 {
			continue
		}
		cleanupDate := now.Format("2006-01-02")
		if lastCleanupDate == cleanupDate {
			continue
		}
		deletedTotal := 0
		for {
			deleted := a.cleanupTerminalHistory()
			deletedTotal += deleted
			if deleted < a.Cfg.Workflow.RetentionDeleteBatchSize {
				break
			}
		}
		lastCleanupDate = cleanupDate
		if deletedTotal > 0 {
			log.Printf("[cleanup] finished: deleted=%d", deletedTotal)
		}
	}
}
