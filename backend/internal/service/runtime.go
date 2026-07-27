package service

import (
	"errors"
	"strings"
	"time"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/security"
)

// errBacklogExceeded 同一并发键等待队列超限(HTTP 429)。
var errBacklogExceeded = errors.New("Current start entry backlog exceeded the configured limit")

// isBacklogExceeded 判断错误是否为积压超限。
func isBacklogExceeded(err error) bool { return errors.Is(err, errBacklogExceeded) }

// IsBacklogExceeded 导出给 API 层使用。
func IsBacklogExceeded(err error) bool { return isBacklogExceeded(err) }

// ---------- 概览与查询 ----------

// GetSchedulerOverview 调度首页概览。
func (a *App) GetSchedulerOverview() (M, error) {
	definitions := a.listLatestDefinitions()
	var runtimeStates []db.WorkflowRuntimeState
	a.DB.Find(&runtimeStates)
	executionCounts := a.countExecutionsByDefinitionIDs(collectIDs(definitions, func(d db.WorkflowDefinition) int64 { return d.ID }))
	countsByStatus := a.countExecutionsByStatus([]string{"queued", "running", "retry_waiting"})

	var totalExecutions int64
	for _, count := range executionCounts {
		totalExecutions += count
	}
	activeCount := 0
	for _, state := range runtimeStates {
		if state.ActiveWorkflowDefinitionID != nil {
			activeCount++
		}
	}

	var latestExecution db.WorkflowExecution
	latestExecutedAt := ""
	if err := a.DB.Order("COALESCE(started_at, queued_at) DESC, id DESC").First(&latestExecution).Error; err == nil {
		at := firstTime(latestExecution.FinishedAt, latestExecution.StartedAt, &latestExecution.QueuedAt)
		latestExecutedAt = fmtTimeV(at)
	}

	var oldestQueued *time.Time
	var oldestRow db.WorkflowExecution
	if err := a.DB.Where("status = ?", "queued").Order("queued_at ASC, id ASC").First(&oldestRow).Error; err == nil {
		oldestQueued = &oldestRow.QueuedAt
	}
	staleBefore := time.Now().Add(-time.Duration(a.Cfg.Workflow.ExecutionStaleTimeoutSeconds) * time.Second)
	var staleRunningCount int64
	a.DB.Model(&db.WorkflowExecution{}).
		Where("status = ? AND last_heartbeat_at IS NOT NULL AND last_heartbeat_at < ?", "running", staleBefore).
		Count(&staleRunningCount)

	definitionItems := make([]M, 0)
	for i, definition := range definitions {
		if i >= 8 {
			break
		}
		isActive := false
		for _, state := range runtimeStates {
			if state.WorkflowCode == definition.Code && state.ActiveWorkflowDefinitionID != nil &&
				*state.ActiveWorkflowDefinitionID == definition.ID {
				isActive = true
				break
			}
		}
		definitionItems = append(definitionItems, M{
			"workflowDefinitionId":      definition.ID,
			"workflowDefinitionCode":    definition.Code,
			"workflowDefinitionVersion": definition.Version,
			"workflowDefinitionName":    definition.DisplayName,
			"isActive":                  isActive,
			"executionCount":            executionCounts[definition.ID],
		})
	}
	oldestAgeMs := int64(0)
	if oldestQueued != nil {
		oldestAgeMs = time.Since(*oldestQueued).Milliseconds()
		if oldestAgeMs < 0 {
			oldestAgeMs = 0
		}
	}
	return M{
		"stats": M{
			"definitionCount":       len(definitions),
			"activeDefinitionCount": activeCount,
			"executionCount":        totalExecutions,
			"latestExecutedAt":      latestExecutedAt,
			"pendingCount":          countsByStatus["queued"],
			"queuedCount":           countsByStatus["queued"],
			"runningCount":          countsByStatus["running"],
			"retryWaitingCount":     countsByStatus["retry_waiting"],
			"oldestPendingAgeMs":    oldestAgeMs,
			"staleRunningCount":     staleRunningCount,
		},
		"definitions": definitionItems,
	}, nil
}

// GetRuntimeByDefinition 定义对应 workflow code 的运行态。
func (a *App) GetRuntimeByDefinition(definitionID int64) (M, error) {
	definition, err := a.requireDefinition(definitionID)
	if err != nil {
		return nil, err
	}
	return a.serializeRuntime(definition.Code, a.getRuntimeStateByCode(definition.Code)), nil
}

// ---------- 激活 / 停用 ----------

// ActivateDefinition 激活定义版本并重建运行入口。
func (a *App) ActivateDefinition(definitionID int64, operatorUserID int64) (M, error) {
	definition, err := a.requireDefinition(definitionID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	state := a.getRuntimeStateByCode(definition.Code)
	if state == nil {
		state = &db.WorkflowRuntimeState{
			WorkflowCode: definition.Code, ActiveWorkflowDefinitionID: &definition.ID,
			ActivatedAt: &now, ActivatedBy: &operatorUserID, UpdatedAt: now,
		}
		if err := a.DB.Create(state).Error; err != nil {
			return nil, err
		}
	} else {
		if err := a.DB.Model(state).Updates(map[string]any{
			"active_workflow_definition_id": definition.ID,
			"activated_at":                  now, "activated_by": operatorUserID, "updated_at": now,
		}).Error; err != nil {
			return nil, err
		}
		a.DB.First(state, state.ID)
	}
	if err := a.reconcileRuntimeEntriesForState(state, true); err != nil {
		return nil, err
	}
	a.DB.First(state, state.ID)
	return a.serializeRuntime(definition.Code, state), nil
}

// DeactivateWorkflowCode 停用 workflow code。
func (a *App) DeactivateWorkflowCode(workflowCode string) (M, error) {
	state := a.getRuntimeStateByCode(workflowCode)
	if state == nil {
		return nil, bizErr("Workflow runtime state does not exist")
	}
	if err := a.DB.Where("workflow_runtime_state_id = ?", state.ID).Delete(&db.WorkflowRuntimeEntry{}).Error; err != nil {
		return nil, err
	}
	if err := a.DB.Model(state).Updates(map[string]any{
		"active_workflow_definition_id": nil, "activated_at": nil, "activated_by": nil, "updated_at": time.Now(),
	}).Error; err != nil {
		return nil, err
	}
	a.DB.First(state, state.ID)
	return a.serializeRuntime(workflowCode, state), nil
}

// SetEntryEnabled 启停运行入口。
func (a *App) SetEntryEnabled(definitionID int64, entryKey string, isEnabled bool) (M, error) {
	entry, err := a.requireRuntimeEntry(definitionID, entryKey)
	if err != nil {
		return nil, err
	}
	updates := map[string]any{"is_enabled": isEnabled, "updated_at": time.Now()}
	switch {
	case entry.StartType == "schedule":
		if isEnabled {
			// 重新注册调度:计算 next_run_at。
			if err := a.registerScheduleEntry(entry); err != nil {
				updates["registration_status"] = "error"
				updates["last_error_message"] = err.Error()
				a.DB.Model(entry).Updates(updates)
				return nil, err
			}
			updates["registration_status"] = "registered"
		} else {
			updates["registration_status"] = "disabled"
			updates["schedule_job_id"] = ""
			updates["next_run_at"] = nil
		}
	case !isEnabled:
		updates["registration_status"] = "disabled"
	case entry.StartType == "event" || entry.StartType == "webhook":
		updates["registration_status"] = "registered"
	}
	if err := a.DB.Model(entry).Updates(updates).Error; err != nil {
		return nil, err
	}
	return a.GetRuntimeByDefinition(definitionID)
}

// RotateWebhookSecret 轮换 webhook 密钥。
func (a *App) RotateWebhookSecret(definitionID int64, entryKey string) (M, error) {
	entry, err := a.requireRuntimeEntry(definitionID, entryKey)
	if err != nil {
		return nil, err
	}
	if entry.StartType != "webhook" {
		return nil, bizErr("Only webhook start entries support secret rotation")
	}
	secret := security.RandomURLSafe(24)
	registrationStatus := "disabled"
	if entry.IsEnabled {
		registrationStatus = "registered"
	}
	now := time.Now()
	if err := a.DB.Model(entry).Updates(map[string]any{
		"secret_hash":         security.HashWebhookSecret(a.Cfg.Auth.SecretKey, secret),
		"secret_hint":         buildSecretHint(secret),
		"secret_rotated_at":   now,
		"registration_status": registrationStatus,
		"updated_at":          now,
	}).Error; err != nil {
		return nil, err
	}
	return M{"entryKey": entryKey, "secret": secret, "secretHint": buildSecretHint(secret)}, nil
}

// ---------- 触发入队 ----------

// RunManualStarts 手动触发一个或多个 start.manual 入口。
func (a *App) RunManualStarts(definitionID int64, startEntryKeys []string, triggeredBy int64, inputs M) ([]M, error) {
	if len(startEntryKeys) == 0 {
		return nil, bizErr("At least one start entry must be selected")
	}
	definition, err := a.requireDefinition(definitionID)
	if err != nil {
		return nil, err
	}
	graph := loadJSONObject(definition.GraphJSON)
	manualStartNodes := map[string]M{}
	nodes, _ := graph["nodes"].([]any)
	for _, nodeAny := range nodes {
		node, ok := nodeAny.(map[string]any)
		if !ok || asString(node["type"]) != "start.manual" {
			continue
		}
		config, _ := node["config"].(map[string]any)
		entryKey := strings.TrimSpace(asString(config["entryKey"]))
		if entryKey != "" {
			manualStartNodes[entryKey] = node
		}
	}
	executions := make([]M, 0, len(startEntryKeys))
	for _, entryKey := range startEntryKeys {
		normalized := strings.TrimSpace(entryKey)
		startNode := manualStartNodes[normalized]
		if startNode == nil {
			return nil, bizErr("Manual start entry does not exist: %s", normalized)
		}
		execution, _, err := a.enqueueStartNodeExecution(definition, startNode, M{
			"triggerType": "manual",
			"triggeredBy": triggeredBy,
			"triggerKey":  "manual:" + definition.Code + ":" + normalized + ":" + int64Text(time.Now().UnixNano()),
			"payload":     M{},
			"inputs":      inputs,
		})
		if err != nil {
			return nil, err
		}
		executions = append(executions, a.serializeExecutionSummary(execution))
	}
	return executions, nil
}

// RunRuntimeEntry 按 runtime entry 入队一次执行。
func (a *App) RunRuntimeEntry(runtimeEntryID int64, triggerCtx M) (M, error) {
	var entry db.WorkflowRuntimeEntry
	if err := a.DB.Preload("WorkflowRuntimeState").Preload("WorkflowDefinition").First(&entry, runtimeEntryID).Error; err != nil {
		return nil, bizErr("Workflow runtime entry does not exist")
	}
	state := entry.WorkflowRuntimeState
	if state == nil || state.ActiveWorkflowDefinitionID == nil {
		return nil, bizErr("Workflow code is not active")
	}
	if !entry.IsEnabled {
		return nil, bizErr("Workflow runtime entry is disabled")
	}
	if *state.ActiveWorkflowDefinitionID != entry.WorkflowDefinitionID {
		return nil, bizErr("Workflow runtime entry is not bound to the active definition")
	}
	definition := entry.WorkflowDefinition
	if definition == nil {
		var err error
		definition, err = a.requireDefinition(entry.WorkflowDefinitionID)
		if err != nil {
			return nil, err
		}
	}
	graph := loadJSONObject(definition.GraphJSON)
	startNode := findStartNodeByEntryKey(graph, entry.EntryKey, "")
	if startNode == nil {
		return nil, bizErr("Workflow start entry does not exist in definition")
	}
	execution, duplicate, err := a.enqueueStartNodeExecution(definition, startNode, triggerCtx)
	if err != nil {
		return nil, err
	}
	return M{"execution": a.serializeExecutionSummary(execution), "duplicate": duplicate}, nil
}

// TriggerWebhook 校验 secret 后触发 webhook 入口。
func (a *App) TriggerWebhook(workflowCode, entryKey, secret string, payload M, idempotencyKey string) (M, error) {
	state := a.getRuntimeStateByCode(workflowCode)
	if state == nil || state.ActiveWorkflowDefinitionID == nil {
		return nil, bizErr("Workflow code is not active")
	}
	var entry db.WorkflowRuntimeEntry
	if err := a.DB.Where("workflow_runtime_state_id = ? AND entry_key = ?", state.ID, entryKey).First(&entry).Error; err != nil || entry.StartType != "webhook" {
		return nil, bizErr("Webhook start entry does not exist")
	}
	if !entry.IsEnabled {
		return nil, bizErr("Webhook start entry is disabled")
	}
	if secret == "" || entry.SecretHash == "" ||
		security.HashWebhookSecret(a.Cfg.Auth.SecretKey, secret) != entry.SecretHash {
		return nil, bizErr("Webhook secret is invalid")
	}
	normalizedIdempotency := strings.TrimSpace(idempotencyKey)
	triggerKey := "webhook:" + workflowCode + ":" + entryKey + ":"
	if normalizedIdempotency != "" {
		triggerKey += normalizedIdempotency
	} else {
		triggerKey += int64Text(time.Now().UnixNano())
	}
	triggerCtx := M{
		"triggerType": "webhook",
		"triggerKey":  triggerKey,
		"payload":     payload,
	}
	if normalizedIdempotency != "" {
		triggerCtx["idempotencyKey"] = normalizedIdempotency
	}
	return a.RunRuntimeEntry(entry.ID, triggerCtx)
}

// enqueueStartNodeExecution 创建 queued 状态的执行记录(DB 即队列)。
func (a *App) enqueueStartNodeExecution(definition *db.WorkflowDefinition, startNode M, triggerCtx M) (*db.WorkflowExecution, bool, error) {
	config, _ := startNode["config"].(map[string]any)
	startEntryKey := strings.TrimSpace(asString(config["entryKey"]))
	if startEntryKey == "" {
		return nil, false, bizErr("Start entry key is required")
	}
	triggerType := strings.TrimSpace(asString(triggerCtx["triggerType"]))
	if triggerType == "" {
		nodeType := asString(startNode["type"])
		if idx := strings.Index(nodeType, "."); idx >= 0 {
			triggerType = nodeType[idx+1:]
		}
	}
	triggerKey := strings.TrimSpace(asString(triggerCtx["triggerKey"]))
	idempotencyKey := strings.TrimSpace(asString(triggerCtx["idempotencyKey"]))

	if idempotencyKey != "" {
		if duplicated := a.getExecutionByIdempotencyKey(triggerType, idempotencyKey); duplicated != nil {
			return duplicated, true, nil
		}
	}

	concurrencyKey := definition.Code + ":" + startEntryKey
	var backlogCount int64
	a.DB.Model(&db.WorkflowExecution{}).
		Where("concurrency_key = ? AND status IN ?", concurrencyKey, []string{"queued", "retry_waiting"}).
		Count(&backlogCount)
	if backlogCount >= int64(a.Cfg.Workflow.BacklogLimitPerKey) {
		return nil, false, errBacklogExceeded
	}

	inputs := buildStartInputs(startNode, triggerCtx)
	now := time.Now()
	execution := db.WorkflowExecution{
		WorkflowDefinitionID: definition.ID,
		StartEntryKey:        startEntryKey,
		StartNodeID:          asString(startNode["id"]),
		StartNodeType:        asString(startNode["type"]),
		TriggerType:          triggerType,
		ConcurrencyKey:       concurrencyKey,
		Status:               "queued",
		QueuedAt:             now,
		AttemptCount:         0,
		MaxAttempts:          a.Cfg.Workflow.MaxAttempts,
		InputSnapshotJSON:    serializeSnapshot(inputs, a.Cfg.Workflow.MaxInputSnapshotBytes),
		ContextSnapshotJSON:  serializeSnapshot(triggerCtx, a.Cfg.Workflow.MaxOutputSnapshotBytes),
		ResultSnapshotJSON:   "{}",
	}
	if triggerKey != "" {
		execution.TriggerKey = &triggerKey
	}
	if idempotencyKey != "" {
		execution.IdempotencyKey = &idempotencyKey
	}
	if triggeredBy := asInt64(triggerCtx["triggeredBy"]); triggeredBy > 0 {
		execution.TriggeredBy = &triggeredBy
	}
	if outboxID := asInt64(triggerCtx["triggerOutboxId"]); outboxID > 0 {
		execution.TriggerOutboxID = &outboxID
	}
	if err := a.DB.Create(&execution).Error; err != nil {
		// 唯一约束冲突时按幂等键复用已有执行。
		if idempotencyKey != "" {
			if duplicated := a.getExecutionByIdempotencyKey(triggerType, idempotencyKey); duplicated != nil {
				return duplicated, true, nil
			}
		}
		return nil, false, err
	}
	created, err := a.getExecutionByID(execution.ID)
	if err != nil {
		return nil, false, err
	}
	a.wakeDispatcher()
	return created, false, nil
}

// ---------- 入口对齐与调度注册 ----------

// reconcileRuntimeEntriesForState 依据激活定义重建 runtime entries。
func (a *App) reconcileRuntimeEntriesForState(state *db.WorkflowRuntimeState, preserveExisting bool) error {
	if state.ActiveWorkflowDefinitionID == nil {
		return nil
	}
	definition, err := a.requireDefinition(*state.ActiveWorkflowDefinitionID)
	if err != nil {
		return err
	}
	graph := loadJSONObject(definition.GraphJSON)

	var previousEntries []db.WorkflowRuntimeEntry
	a.DB.Where("workflow_runtime_state_id = ?", state.ID).Find(&previousEntries)
	previousMap := map[string]*db.WorkflowRuntimeEntry{}
	for i := range previousEntries {
		previousMap[previousEntries[i].EntryKey] = &previousEntries[i]
	}
	if err := a.DB.Where("workflow_runtime_state_id = ?", state.ID).Delete(&db.WorkflowRuntimeEntry{}).Error; err != nil {
		return err
	}

	now := time.Now()
	nodes, _ := graph["nodes"].([]any)
	for _, nodeAny := range nodes {
		node, ok := nodeAny.(map[string]any)
		if !ok {
			continue
		}
		nodeType := asString(node["type"])
		if !strings.HasPrefix(nodeType, "start.") {
			continue
		}
		config, _ := node["config"].(map[string]any)
		entryKey := strings.TrimSpace(asString(config["entryKey"]))
		if entryKey == "" {
			return bizErr("Start node entryKey is required")
		}
		startType := strings.SplitN(nodeType, ".", 2)[1]

		previous := previousMap[entryKey]
		isEnabled := true
		if preserveExisting && previous != nil {
			isEnabled = previous.IsEnabled
		}
		entry := db.WorkflowRuntimeEntry{
			WorkflowRuntimeStateID: state.ID, WorkflowDefinitionID: definition.ID,
			StartNodeID: asString(node["id"]), EntryKey: entryKey, StartType: startType,
			IsEnabled: isEnabled, RegistrationStatus: "ready",
			CreatedAt: now, UpdatedAt: now,
		}
		if !isEnabled {
			entry.RegistrationStatus = "disabled"
		}
		// secret 只在 start_type 未变化时继承。
		if preserveExisting && previous != nil && previous.StartType == startType {
			entry.SecretHash = previous.SecretHash
			entry.SecretHint = previous.SecretHint
			entry.SecretRotatedAt = previous.SecretRotatedAt
		}
		if preserveExisting && previous != nil {
			entry.LastTriggeredAt = previous.LastTriggeredAt
		}
		if err := a.DB.Create(&entry).Error; err != nil {
			return err
		}

		switch {
		case startType == "schedule" && isEnabled:
			if err := a.registerScheduleEntry(&entry); err != nil {
				a.DB.Model(&entry).Updates(map[string]any{
					"registration_status": "error", "last_error_message": err.Error(), "updated_at": time.Now(),
				})
			}
		case startType == "event" || startType == "webhook":
			status := "disabled"
			if isEnabled {
				status = "registered"
			}
			a.DB.Model(&entry).Updates(map[string]any{"registration_status": status, "updated_at": time.Now()})
		}
	}
	return nil
}

// registerScheduleEntry 计算并写入 schedule 入口的下次触发时间。
func (a *App) registerScheduleEntry(entry *db.WorkflowRuntimeEntry) error {
	definition, err := a.requireDefinition(entry.WorkflowDefinitionID)
	if err != nil {
		return err
	}
	graph := loadJSONObject(definition.GraphJSON)
	startNode := findStartNodeByEntryKey(graph, entry.EntryKey, "start.schedule")
	if startNode == nil {
		return bizErr("Schedule start node does not exist in definition")
	}
	config, _ := startNode["config"].(map[string]any)
	nextRunAt, err := computeNextScheduleTime(config, time.Now())
	if err != nil {
		return err
	}
	updates := map[string]any{
		"schedule_job_id":     "workflow-runtime-entry:" + int64Text(entry.ID),
		"registration_status": "registered",
		"updated_at":          time.Now(),
	}
	if nextRunAt != nil {
		updates["next_run_at"] = *nextRunAt
	} else {
		updates["next_run_at"] = nil
	}
	return a.DB.Model(&db.WorkflowRuntimeEntry{}).Where("id = ?", entry.ID).Updates(updates).Error
}

// computeNextScheduleTime 依据调度配置计算下一次触发时间。
func computeNextScheduleTime(config map[string]any, after time.Time) (*time.Time, error) {
	scheduleType := strings.TrimSpace(asString(config["scheduleType"]))
	if scheduleType == "" {
		scheduleType = "cron"
	}
	switch scheduleType {
	case "cron":
		schedule, err := parseQuartzCron(asString(config["cronExpression"]))
		if err != nil {
			return nil, err
		}
		next := schedule.Next(after)
		if next.IsZero() {
			return nil, nil
		}
		return &next, nil
	case "interval":
		value := asInt64(config["value"])
		if value <= 0 {
			return nil, bizErr("Interval schedule value must be greater than 0")
		}
		unit := strings.TrimSpace(asString(config["unit"]))
		if unit == "" {
			unit = "minutes"
		}
		var duration time.Duration
		switch unit {
		case "seconds":
			duration = time.Duration(value) * time.Second
		case "minutes":
			duration = time.Duration(value) * time.Minute
		case "hours":
			duration = time.Duration(value) * time.Hour
		case "days":
			duration = time.Duration(value) * 24 * time.Hour
		default:
			return nil, bizErr("Interval schedule unit is not supported")
		}
		next := after.Add(duration)
		return &next, nil
	case "once":
		runAt, err := parseFlexibleTime(asString(config["runAt"]))
		if err != nil {
			return nil, bizErr("Run time format is invalid")
		}
		if !runAt.After(after) {
			return nil, nil // 已过期,不再触发。
		}
		return &runAt, nil
	default:
		return nil, bizErr("Unsupported schedule type")
	}
}

// ---------- 执行查询 ----------

// WorkflowExecutionQuery 执行记录查询。
type WorkflowExecutionQuery struct {
	Current                int
	Size                   int
	WorkflowDefinitionCode string
	Keyword                string
	TriggerType            string
	Status                 string
	DefinitionID           *int64
}

// ListExecutions 分页查询执行记录。
func (a *App) ListExecutions(query WorkflowExecutionQuery) (M, error) {
	if query.DefinitionID != nil {
		if _, err := a.requireDefinition(*query.DefinitionID); err != nil {
			return nil, err
		}
	}
	q := a.DB.Model(&db.WorkflowExecution{}).
		Joins("LEFT JOIN workflow_definitions ON workflow_executions.workflow_definition_id = workflow_definitions.id")
	if query.DefinitionID != nil {
		q = q.Where("workflow_executions.workflow_definition_id = ?", *query.DefinitionID)
	}
	if query.WorkflowDefinitionCode != "" {
		q = q.Where("workflow_definitions.code = ?", query.WorkflowDefinitionCode)
	}
	if query.TriggerType != "" {
		q = q.Where("workflow_executions.trigger_type = ?", query.TriggerType)
	}
	if query.Status != "" {
		q = q.Where("workflow_executions.status = ?", query.Status)
	}
	if text := strings.TrimSpace(query.Keyword); text != "" {
		like := "%" + text + "%"
		q = q.Where(
			"COALESCE(workflow_definitions.display_name,'') LIKE ? OR COALESCE(workflow_definitions.code,'') LIKE ?"+
				" OR COALESCE(workflow_executions.start_entry_key,'') LIKE ? OR COALESCE(workflow_executions.trigger_key,'') LIKE ?"+
				" OR COALESCE(workflow_executions.idempotency_key,'') LIKE ? OR COALESCE(workflow_executions.error_message,'') LIKE ?",
			like, like, like, like, like, like,
		)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, err
	}
	var executions []db.WorkflowExecution
	if err := q.Preload("WorkflowDefinition").
		Order("COALESCE(workflow_executions.started_at, workflow_executions.queued_at) DESC, workflow_executions.id DESC").
		Offset((query.Current - 1) * query.Size).Limit(query.Size).
		Find(&executions).Error; err != nil {
		return nil, err
	}
	records := make([]M, 0, len(executions))
	for i := range executions {
		records = append(records, a.serializeExecutionSummary(&executions[i]))
	}
	return pagedResult(records, query.Current, query.Size, total), nil
}

// GetExecutionDetail 执行详情(图 + 节点日志 + attempt + 边日志)。
func (a *App) GetExecutionDetail(executionID int64) (M, error) {
	execution, err := a.getExecutionByID(executionID)
	if err != nil {
		return nil, err
	}
	data := a.serializeExecutionSummary(execution)
	graph := M{"nodes": []any{}, "edges": []any{}}
	if execution.WorkflowDefinition != nil {
		graph = loadJSONObject(execution.WorkflowDefinition.GraphJSON)
	}
	data["graph"] = graph

	var nodeLogs []db.WorkflowExecutionNode
	a.DB.Where("workflow_execution_id = ?", executionID).Order("started_at ASC, id ASC").Find(&nodeLogs)
	nodeItems := make([]M, 0, len(nodeLogs))
	for i := range nodeLogs {
		item := nodeLogs[i]
		var durationMs int64
		if item.DurationMs != nil {
			durationMs = *item.DurationMs
		}
		nodeItems = append(nodeItems, M{
			"id": item.ID, "nodeId": item.NodeID, "nodeType": item.NodeType, "status": item.Status,
			"startedAt": fmtTimeV(item.StartedAt), "finishedAt": fmtTime(item.FinishedAt),
			"durationMs":         durationMs,
			"inputSnapshotJson":  orDefault(item.InputSnapshotJSON, "{}"),
			"outputSnapshotJson": orDefault(item.OutputSnapshotJSON, "{}"),
			"errorMessage":       item.ErrorMessage,
		})
	}
	data["nodeLogs"] = nodeItems

	var attempts []db.WorkflowExecutionAttempt
	a.DB.Where("workflow_execution_id = ?", executionID).Order("attempt ASC, id ASC").Find(&attempts)
	attemptItems := make([]M, 0, len(attempts))
	for i := range attempts {
		item := attempts[i]
		attemptItems = append(attemptItems, M{
			"id": item.ID, "attempt": item.Attempt, "workerId": item.WorkerID,
			"brokerMessageId": "", "leaseId": "",
			"startedAt": fmtTimeV(item.StartedAt), "finishedAt": fmtTime(item.FinishedAt),
			"failureCategory": item.FailureCategory, "errorSummary": item.ErrorSummary, "status": item.Status,
		})
	}
	data["attempts"] = attemptItems

	var transitions []db.WorkflowExecutionTransition
	a.DB.Where("workflow_execution_id = ?", executionID).Order("traversal_index ASC, id ASC").Find(&transitions)
	transitionItems := make([]M, 0, len(transitions))
	for i := range transitions {
		item := transitions[i]
		var iterationIndex any
		if item.IterationIndex != nil {
			iterationIndex = *item.IterationIndex
		}
		transitionItems = append(transitionItems, M{
			"id": item.ID, "edgeId": item.EdgeID,
			"sourceNodeId": item.SourceNodeID, "targetNodeId": item.TargetNodeID,
			"traversalIndex": item.TraversalIndex, "iterationIndex": iterationIndex,
			"branchKey": item.BranchKey, "payloadSnapshotJson": orDefault(item.PayloadSnapshotJSON, "{}"),
			"createdAt": fmtTimeV(item.CreatedAt),
		})
	}
	data["transitionLogs"] = transitionItems
	return data, nil
}

// ---------- 清理 ----------

// cleanupTerminalHistory 按批删除超出保留期的终态执行。
func (a *App) cleanupTerminalHistory() int {
	cutoff := time.Now().AddDate(0, 0, -a.Cfg.Workflow.ExecutionRetentionDays)
	var ids []int64
	a.DB.Model(&db.WorkflowExecution{}).
		Where("status IN ? AND finished_at IS NOT NULL AND finished_at < ?", []string{"success", "failed"}, cutoff).
		Order("finished_at ASC, id ASC").Limit(a.Cfg.Workflow.RetentionDeleteBatchSize).
		Pluck("id", &ids)
	if len(ids) == 0 {
		return 0
	}
	result := a.DB.Where("id IN ?", ids).Delete(&db.WorkflowExecution{})
	return int(result.RowsAffected)
}

// ---------- 序列化与内部查询 ----------

func (a *App) getExecutionByID(executionID int64) (*db.WorkflowExecution, error) {
	var execution db.WorkflowExecution
	if err := a.DB.Preload("WorkflowDefinition").First(&execution, executionID).Error; err != nil {
		return nil, bizErr("Workflow execution does not exist")
	}
	return &execution, nil
}

func (a *App) getExecutionByIdempotencyKey(triggerType, idempotencyKey string) *db.WorkflowExecution {
	if idempotencyKey == "" {
		return nil
	}
	var execution db.WorkflowExecution
	if err := a.DB.Preload("WorkflowDefinition").
		Where("trigger_type = ? AND idempotency_key = ?", triggerType, idempotencyKey).
		Order("id DESC").First(&execution).Error; err != nil {
		return nil
	}
	return &execution
}

func (a *App) getRuntimeEntryByDefinitionAndKey(definitionID int64, entryKey string) *db.WorkflowRuntimeEntry {
	var entry db.WorkflowRuntimeEntry
	if err := a.DB.Where("workflow_definition_id = ? AND entry_key = ?", definitionID, entryKey).First(&entry).Error; err != nil {
		return nil
	}
	return &entry
}

func (a *App) requireRuntimeEntry(definitionID int64, entryKey string) (*db.WorkflowRuntimeEntry, error) {
	entry := a.getRuntimeEntryByDefinitionAndKey(definitionID, entryKey)
	if entry == nil {
		return nil, bizErr("Workflow runtime entry does not exist")
	}
	return entry, nil
}

func (a *App) countExecutionsByStatus(statuses []string) map[string]int64 {
	result := map[string]int64{}
	if len(statuses) == 0 {
		return result
	}
	var rows []struct {
		Status string
		Count  int64
	}
	a.DB.Model(&db.WorkflowExecution{}).
		Select("status, COUNT(id) AS count").
		Where("status IN ?", statuses).Group("status").Scan(&rows)
	for _, row := range rows {
		result[row.Status] = row.Count
	}
	return result
}

func (a *App) serializeRuntime(workflowCode string, state *db.WorkflowRuntimeState) M {
	entries := []M{}
	var runtimeStateID, activeDefinitionID any
	activatedAt := ""
	if state != nil {
		runtimeStateID = state.ID
		if state.ActiveWorkflowDefinitionID != nil {
			activeDefinitionID = *state.ActiveWorkflowDefinitionID
		}
		activatedAt = fmtTime(state.ActivatedAt)
		var entryRows []db.WorkflowRuntimeEntry
		a.DB.Where("workflow_runtime_state_id = ?", state.ID).Order("entry_key ASC, id ASC").Find(&entryRows)
		for i := range entryRows {
			entries = append(entries, serializeRuntimeEntry(&entryRows[i]))
		}
	}
	return M{
		"workflowCode": workflowCode, "runtimeStateId": runtimeStateID,
		"activeDefinitionId": activeDefinitionID, "activatedAt": activatedAt,
		"entries": entries,
	}
}

func serializeRuntimeEntry(entry *db.WorkflowRuntimeEntry) M {
	return M{
		"id": entry.ID, "definitionId": entry.WorkflowDefinitionID,
		"startNodeId": entry.StartNodeID, "entryKey": entry.EntryKey, "startType": entry.StartType,
		"isEnabled": entry.IsEnabled, "registrationStatus": entry.RegistrationStatus,
		"nextRunAt": fmtTime(entry.NextRunAt), "lastTriggeredAt": fmtTime(entry.LastTriggeredAt),
		"lastErrorMessage": entry.LastErrorMessage, "secretHint": entry.SecretHint,
		"secretRotatedAt": fmtTime(entry.SecretRotatedAt),
	}
}

func (a *App) serializeExecutionSummary(execution *db.WorkflowExecution) M {
	definitionCode, definitionName := "", ""
	definitionVersion := 0
	if execution.WorkflowDefinition != nil {
		definitionCode = execution.WorkflowDefinition.Code
		definitionVersion = execution.WorkflowDefinition.Version
		definitionName = execution.WorkflowDefinition.DisplayName
	}
	var durationMs int64
	if execution.DurationMs != nil {
		durationMs = *execution.DurationMs
	}
	var triggeredBy, triggerKey, idempotencyKey, workerID, triggerOutboxID any
	if execution.TriggeredBy != nil {
		triggeredBy = *execution.TriggeredBy
	}
	if execution.TriggerKey != nil {
		triggerKey = *execution.TriggerKey
	}
	if execution.IdempotencyKey != nil {
		idempotencyKey = *execution.IdempotencyKey
	}
	if execution.WorkerID != nil {
		workerID = *execution.WorkerID
	}
	if execution.TriggerOutboxID != nil {
		triggerOutboxID = *execution.TriggerOutboxID
	}
	return M{
		"id":                        execution.ID,
		"workflowDefinitionId":      execution.WorkflowDefinitionID,
		"workflowDefinitionCode":    definitionCode,
		"workflowDefinitionVersion": definitionVersion,
		"workflowDefinitionName":    definitionName,
		"startEntryKey":             execution.StartEntryKey,
		"startNodeId":               execution.StartNodeID,
		"startNodeType":             execution.StartNodeType,
		"triggerType":               execution.TriggerType,
		"triggeredBy":               triggeredBy,
		"triggerKey":                triggerKey,
		"idempotencyKey":            idempotencyKey,
		"concurrencyKey":            execution.ConcurrencyKey,
		"triggerOutboxId":           triggerOutboxID,
		"status":                    execution.Status,
		"queuedAt":                  fmtTimeV(execution.QueuedAt),
		"claimedAt":                 fmtTime(execution.ClaimedAt),
		"startedAt":                 fmtTime(execution.StartedAt),
		"finishedAt":                fmtTime(execution.FinishedAt),
		"lastHeartbeatAt":           fmtTime(execution.LastHeartbeatAt),
		"workerId":                  workerID,
		"attemptCount":              execution.AttemptCount,
		"maxAttempts":               execution.MaxAttempts,
		"nextRetryAt":               fmtTime(execution.NextRetryAt),
		"failureCategory":           execution.FailureCategory,
		"brokerMessageId":           "",
		"durationMs":                durationMs,
		"errorMessage":              execution.ErrorMessage,
		"inputSnapshotJson":         orDefault(execution.InputSnapshotJSON, "{}"),
		"contextSnapshotJson":       orDefault(execution.ContextSnapshotJSON, "{}"),
		"resultSnapshotJson":        orDefault(execution.ResultSnapshotJSON, "{}"),
	}
}

func buildSecretHint(secret string) string {
	runes := []rune(secret)
	if len(runes) <= 8 {
		return strings.Repeat("*", len(runes))
	}
	return string(runes[:4]) + "****" + string(runes[len(runes)-4:])
}
