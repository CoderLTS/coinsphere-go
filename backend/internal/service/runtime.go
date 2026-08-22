package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/security"
)

// ErrBacklogExceeded 表示同一并发键的等待队列已满。
var ErrBacklogExceeded = errors.New("current start entry backlog exceeded the configured limit")

// ErrWorkflowCanceled separates a user cancellation from retryable process cancellation.
var ErrWorkflowCanceled = errors.New("workflow execution canceled")

// ---------- 概览与查询 ----------

// 从这里开始是供 API 层调用的业务方法。(a *App) 是接收者(见 loops.go 已说明的"方法与接收者")。
// 返回类型 (M, error):M 是本项目定义的类型别名(type M = map[string]any),专门用来拼 JSON 响应;error 表示错误。见 GO入门笔记『复合类型』『变量、函数、错误』。
// GetSchedulerOverview 调度首页概览。
func (a *App) GetSchedulerOverview(ownerUserID int64) (M, error) {
	definitions := a.listLatestDefinitions(ownerUserID)
	owned := definitions[:0]
	for _, definition := range definitions {
		if !definition.IsBuiltin {
			owned = append(owned, definition)
		}
	}
	definitions = owned
	var runtimeStates []db.WorkflowRuntimeState
	a.DB.Where("owner_user_id = ?", ownerUserID).Find(&runtimeStates)
	executionCounts := a.countExecutionsByDefinitionIDs(collectIDs(definitions, func(d db.WorkflowDefinition) int64 { return d.ID }))
	countsByStatus := a.countExecutionsByStatus(ownerUserID, []string{"queued", "running", "retry_waiting", "waiting_job", "waiting_action", "cancel_requested"})

	var totalExecutions int64
	// range 一个 map:_ 丢弃键,只累加每个值。遍历 map 的顺序是随机的,这里只做求和不受影响。见 GO入门笔记『复合类型』。
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
	// First 取排序后的第一条(SELECT ... ORDER BY ... LIMIT 1)。查不到会返回错误,所以这里用 err == nil 表示"确实查到了才处理"。见 GO入门笔记『框架:GORM』。
	if err := a.DB.Where("owner_user_id = ?", ownerUserID).Order("COALESCE(started_at, queued_at) DESC, id DESC").First(&latestExecution).Error; err == nil {
		at := firstTime(latestExecution.FinishedAt, latestExecution.StartedAt, &latestExecution.QueuedAt)
		latestExecutedAt = fmtTimeV(at)
	}

	var oldestQueued *time.Time
	var oldestRow db.WorkflowExecution
	if err := a.DB.Where("owner_user_id = ? AND status = ?", ownerUserID, "queued").Order("queued_at ASC, id ASC").First(&oldestRow).Error; err == nil {
		oldestQueued = &oldestRow.QueuedAt
	}
	staleBefore := time.Now().Add(-time.Duration(a.Cfg.Workflow.ExecutionStaleTimeoutSeconds) * time.Second)
	var staleRunningCount int64
	a.DB.Model(&db.WorkflowExecution{}).
		Where("owner_user_id = ? AND status = ? AND last_heartbeat_at IS NOT NULL AND last_heartbeat_at < ?", ownerUserID, "running", staleBefore).
		Count(&staleRunningCount)

	// make([]M, 0) 造一个长度为 0 的空切片,后面用 append 往里追加元素。见 GO入门笔记『复合类型』。
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
			"waitingJobCount":       countsByStatus["waiting_job"],
			"waitingActionCount":    countsByStatus["waiting_action"],
			"cancelRequestedCount":  countsByStatus["cancel_requested"],
			"oldestPendingAgeMs":    oldestAgeMs,
			"staleRunningCount":     staleRunningCount,
		},
		"definitions": definitionItems,
	}, nil
}

// GetRuntimeByDefinition 定义对应 workflow code 的运行态。
func (a *App) GetRuntimeByDefinition(definitionID, ownerUserID int64) (M, error) {
	var result M
	// state 与 entries 必须来自同一数据库快照；否则激活在两次 SELECT 之间提交时，
	// 响应可能把旧 activeDefinitionId 与新入口拼成不存在的中间状态。
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		definition, err := requireOwnedDefinitionWithDB(tx, definitionID, ownerUserID)
		if err != nil {
			return err
		}
		state, err := findRuntimeStateByCodeWithDB(tx, ownerUserID, definition.Code)
		if err != nil {
			return err
		}
		result, err = serializeRuntimeWithDB(tx, definition.Code, state)
		return err
	}, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ---------- 激活 / 停用 ----------

// ActivateDefinition 激活定义版本并重建运行入口。
func (a *App) ActivateDefinition(definitionID int64, operatorUserID int64) (M, error) {
	var workflowCode string
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		if _, err := requireOwnedDefinitionWithDB(tx, definitionID, operatorUserID); err != nil {
			return err
		}
		definition, err := lockWorkflowDefinitionFamily(tx, definitionID)
		if err != nil {
			return err
		}
		workflowCode = definition.Code
		state, err := findRuntimeStateByCodeWithDB(tx, operatorUserID, workflowCode)
		if err != nil {
			return err
		}
		if state == nil {
			state = &db.WorkflowRuntimeState{OwnerUserID: operatorUserID, WorkflowCode: workflowCode}
			if err := tx.Create(state).Error; err != nil {
				return err
			}
		}
		if state.ActiveWorkflowDefinitionID != nil {
			if err := deactivateWorkflowStrategyResourcesWithDB(tx, *state.ActiveWorkflowDefinitionID); err != nil {
				return err
			}
		}
		// 先重建完整入口，最后再切 active 指针；二者仍在同一事务内，任一步失败整体回滚。
		if err := a.reconcileRuntimeEntriesForDefinitionWithDB(tx, state, definition, true); err != nil {
			return err
		}
		if err := a.reconcileWorkflowStrategiesWithDB(tx, definition); err != nil {
			return err
		}
		now := time.Now()
		updated := tx.Model(&db.WorkflowRuntimeState{}).Where("id = ?", state.ID).Updates(map[string]any{
			"active_workflow_definition_id": definition.ID,
			"activated_at":                  now, "activated_by": operatorUserID, "updated_at": now,
		})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return fmt.Errorf("activate workflow runtime state: updated %d rows", updated.RowsAffected)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return a.GetRuntimeByDefinition(definitionID, operatorUserID)
}

func (a *App) ActivateDefinitionForPrincipal(definitionID int64, principal *Principal) (M, error) {
	if principal == nil || principal.User == nil {
		return nil, ErrPermission
	}
	definition, err := requireOwnedDefinitionWithDB(a.DB, definitionID, principal.User.ID)
	if err != nil {
		return nil, err
	}
	if err := assertWorkflowNodePermissions(loadJSONObject(definition.GraphJSON), principal); err != nil {
		return nil, err
	}
	if err := a.assertWorkflowResourcesOwned(loadJSONObject(definition.GraphJSON), principal.User.ID); err != nil {
		return nil, err
	}
	return a.ActivateDefinition(definitionID, principal.User.ID)
}

// DeactivateDefinition 停用 definition 所属 workflow code。
func (a *App) DeactivateDefinition(definitionID, ownerUserID int64) (M, error) {
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		if _, err := requireOwnedDefinitionWithDB(tx, definitionID, ownerUserID); err != nil {
			return err
		}
		definition, err := lockWorkflowDefinitionFamily(tx, definitionID)
		if err != nil {
			return err
		}
		state, err := findRuntimeStateByCodeWithDB(tx, ownerUserID, definition.Code)
		if err != nil {
			return err
		}
		if state == nil || state.ActiveWorkflowDefinitionID == nil {
			return bizErr("Workflow runtime state does not exist")
		}
		if err := deactivateWorkflowStrategyResourcesWithDB(tx, *state.ActiveWorkflowDefinitionID); err != nil {
			return err
		}
		if err := tx.Where("workflow_runtime_state_id = ?", state.ID).Delete(&db.WorkflowRuntimeEntry{}).Error; err != nil {
			return err
		}
		updated := tx.Model(&db.WorkflowRuntimeState{}).Where("id = ?", state.ID).Updates(map[string]any{
			"active_workflow_definition_id": nil, "activated_at": nil, "activated_by": nil, "updated_at": time.Now(),
		})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return fmt.Errorf("deactivate workflow runtime state: updated %d rows", updated.RowsAffected)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return a.GetRuntimeByDefinition(definitionID, ownerUserID)
}

// SetEntryEnabled 启停运行入口。
func (a *App) SetEntryEnabled(definitionID, ownerUserID int64, entryKey string, isEnabled bool) (M, error) {
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		if _, err := requireOwnedDefinitionWithDB(tx, definitionID, ownerUserID); err != nil {
			return err
		}
		if _, err := lockWorkflowDefinitionFamily(tx, definitionID); err != nil {
			return err
		}
		entry, err := requireRuntimeEntryWithDB(tx, definitionID, entryKey)
		if err != nil {
			return err
		}
		updates := map[string]any{"is_enabled": isEnabled, "updated_at": time.Now()}
		switch {
		case entry.StartType == "schedule" && isEnabled:
			scheduleUpdates, err := a.scheduleRegistrationUpdatesWithDB(tx, entry)
			if err != nil {
				return err
			}
			for key, value := range scheduleUpdates {
				updates[key] = value
			}
		case entry.StartType == "schedule":
			updates["registration_status"] = "disabled"
			updates["schedule_job_id"] = ""
			updates["next_run_at"] = nil
		case !isEnabled:
			updates["registration_status"] = "disabled"
		case entry.StartType == "event" || entry.StartType == "webhook":
			updates["registration_status"] = "registered"
		}
		updated := tx.Model(&db.WorkflowRuntimeEntry{}).Where("id = ?", entry.ID).Updates(updates)
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return fmt.Errorf("update workflow runtime entry: updated %d rows", updated.RowsAffected)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return a.GetRuntimeByDefinition(definitionID, ownerUserID)
}

// RotateWebhookSecret 轮换 webhook 密钥。
// RotateWebhookSecret 轮换 webhook 密钥:生成新明文 secret,数据库里只存它的哈希(secret_hash)与提示片段,明文只在本次响应里返回一次。
func (a *App) RotateWebhookSecret(definitionID, ownerUserID int64, entryKey string) (M, error) {
	secret := security.RandomURLSafe(24)
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		if _, err := requireOwnedDefinitionWithDB(tx, definitionID, ownerUserID); err != nil {
			return err
		}
		if _, err := lockWorkflowDefinitionFamily(tx, definitionID); err != nil {
			return err
		}
		entry, err := requireRuntimeEntryWithDB(tx, definitionID, entryKey)
		if err != nil {
			return err
		}
		if entry.StartType != "webhook" {
			return bizErr("Only webhook start entries support secret rotation")
		}
		registrationStatus := "disabled"
		if entry.IsEnabled {
			registrationStatus = "registered"
		}
		now := time.Now()
		updated := tx.Model(&db.WorkflowRuntimeEntry{}).Where("id = ?", entry.ID).Updates(map[string]any{
			"secret_hash":         security.HashWebhookSecret(a.Cfg.Auth.WebhookPepper, secret),
			"secret_hint":         buildSecretHint(secret),
			"secret_rotated_at":   now,
			"registration_status": registrationStatus,
			"updated_at":          now,
		})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return fmt.Errorf("rotate workflow webhook secret: updated %d rows", updated.RowsAffected)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return M{"entryKey": entryKey, "secret": secret, "secretHint": buildSecretHint(secret)}, nil
}

// ---------- 触发入队 ----------

// RunManualStarts 手动触发一个或多个 start.manual 入口。
func (a *App) RunManualStarts(definitionID int64, startEntryKeys []string, triggeredBy int64, inputs M, idempotencyKey string) ([]M, error) {
	if len(startEntryKeys) == 0 {
		return nil, bizErr("At least one start entry must be selected")
	}
	requestHash, err := canonicalRequestHash(struct {
		StartEntryKeys []string `json:"startEntryKeys"`
		Inputs         M        `json:"inputs"`
	}{StartEntryKeys: startEntryKeys, Inputs: inputs})
	if err != nil {
		return nil, err
	}
	definition, err := requireOwnedDefinitionWithDB(a.DB, definitionID, triggeredBy)
	if err != nil {
		return nil, err
	}
	graph := loadJSONObject(definition.GraphJSON)
	if workflowContainsStrategy(graph) {
		state := a.getRuntimeStateByCode(triggeredBy, definition.Code)
		if state == nil || state.ActiveWorkflowDefinitionID == nil || *state.ActiveWorkflowDefinitionID != definition.ID {
			return nil, bizErr("包含策略节点的工作流必须先激活")
		}
	}
	// map[string]M{} 造一个空 map(键是 string、值是 M)。见 GO入门笔记『复合类型』。
	manualStartNodes := map[string]M{}
	nodes, _ := graph["nodes"].([]any)
	for _, nodeAny := range nodes {
		// node, ok := x.(T) 是"带检查的类型断言":ok 表示断言是否成功,失败就跳过这条,避免 panic(程序崩溃)。见 GO入门笔记『复合类型』。
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
	startNodes := make([]M, 0, len(startEntryKeys))
	entryKeys := make([]string, 0, len(startEntryKeys))
	for _, entryKey := range startEntryKeys {
		normalized := strings.TrimSpace(entryKey)
		startNode := manualStartNodes[normalized]
		if startNode == nil {
			return nil, bizErr("Manual start entry does not exist: %s", normalized)
		}
		startNodes = append(startNodes, startNode)
		entryKeys = append(entryKeys, normalized)
	}

	executions := make([]M, 0, len(startNodes))
	created := false
	err = a.DB.Transaction(func(tx *gorm.DB) error {
		record, reused, err := a.reserveIdempotencyRecord(tx, triggeredBy, "workflow:manual:"+int64Text(definitionID), idempotencyKey, requestHash)
		if err != nil {
			return err
		}
		for index, startNode := range startNodes {
			entryKey := entryKeys[index]
			internalKey := workflowExecutionIdempotencyKey(record.ID, "manual:"+int64Text(definitionID)+":"+entryKey)
			if reused {
				execution, err := a.getExecutionByIdempotencyKeyWithDB(tx, "manual", internalKey)
				if err != nil {
					return err
				}
				if execution == nil {
					return errors.New("idempotency record has no workflow execution")
				}
				executions = append(executions, a.serializeExecutionSummary(execution))
				continue
			}
			execution, duplicate, err := a.enqueueStartNodeExecutionWithDB(tx, definition, startNode, M{
				"triggerType":    "manual",
				"triggeredBy":    triggeredBy,
				"triggerKey":     "manual:" + definition.Code + ":" + entryKey + ":record:" + int64Text(record.ID),
				"payload":        M{},
				"inputs":         inputs,
				"idempotencyKey": internalKey,
			})
			if err != nil {
				return err
			}
			created = created || !duplicate
			executions = append(executions, a.serializeExecutionSummary(execution))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if created {
		a.wakeDispatcher()
	}
	return executions, nil
}

func deactivateWorkflowStrategyResourcesWithDB(database *gorm.DB, definitionID int64) error {
	now := time.Now().UTC()
	if err := database.Model(&db.StrategyInstance{}).
		Where("workflow_definition_id = ? AND is_enabled", definitionID).
		Updates(map[string]any{"is_enabled": false, "updated_at": now}).Error; err != nil {
		return err
	}
	return database.Where("workflow_definition_id = ?", definitionID).
		Delete(&db.MarketWorkflowSubscription{}).Error
}

// RunRuntimeEntry 按 runtime entry 入队一次执行。
func (a *App) RunRuntimeEntry(runtimeEntryID int64, triggerCtx M) (M, error) {
	var entry db.WorkflowRuntimeEntry
	// 链式 Preload 顺带把关联的运行态、定义一起查出来;First(&entry, id) 按主键查一条(SELECT ... WHERE id=? LIMIT 1)。见 GO入门笔记『框架:GORM』。
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

// TriggerWebhook 校验已登录用户和 secret 后触发 webhook 入口。
func (a *App) TriggerWebhook(triggeredBy int64, workflowCode, entryKey, secret string, payload M, idempotencyKey string) (M, error) {
	if triggeredBy <= 0 {
		return nil, bizErr("Webhook trigger user is required")
	}
	requestHash, err := canonicalRequestHash(payload)
	if err != nil {
		return nil, err
	}
	state := a.getRuntimeStateByCode(triggeredBy, workflowCode)
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
	if *state.ActiveWorkflowDefinitionID != entry.WorkflowDefinitionID {
		return nil, bizErr("Webhook start entry is not bound to the active definition")
	}
	// 校验密钥:把传入明文按同样算法哈希后,与库里存的 secret_hash 做恒定时间比对,不一致就拒绝(库里从不存明文)。见评审 #3/#9。
	if secret == "" || entry.SecretHash == "" ||
		!security.SecureCompare(security.HashWebhookSecret(a.Cfg.Auth.WebhookPepper, secret), entry.SecretHash) {
		return nil, bizErr("Webhook secret is invalid")
	}
	definition, err := requireOwnedDefinitionWithDB(a.DB, entry.WorkflowDefinitionID, triggeredBy)
	if err != nil {
		return nil, err
	}
	startNode := findStartNodeByEntryKey(loadJSONObject(definition.GraphJSON), entry.EntryKey, "")
	if startNode == nil {
		return nil, bizErr("Workflow start entry does not exist in definition")
	}

	var result M
	created := false
	err = a.DB.Transaction(func(tx *gorm.DB) error {
		record, reused, err := a.reserveIdempotencyRecord(tx, triggeredBy, "workflow:webhook:"+workflowCode+":"+entryKey, idempotencyKey, requestHash)
		if err != nil {
			return err
		}
		internalKey := workflowExecutionIdempotencyKey(record.ID, "webhook:"+workflowCode+":"+entryKey)
		if reused {
			execution, err := a.getExecutionByIdempotencyKeyWithDB(tx, "webhook", internalKey)
			if err != nil {
				return err
			}
			if execution == nil {
				return errors.New("idempotency record has no workflow execution")
			}
			result = M{"execution": a.serializeExecutionSummary(execution), "duplicate": true}
			return nil
		}

		execution, duplicate, err := a.enqueueStartNodeExecutionWithDB(tx, definition, startNode, M{
			"triggerType":    "webhook",
			"triggeredBy":    triggeredBy,
			"triggerKey":     "webhook:" + workflowCode + ":" + entryKey + ":record:" + int64Text(record.ID),
			"payload":        payload,
			"idempotencyKey": internalKey,
		})
		if err != nil {
			return err
		}
		created = !duplicate
		result = M{"execution": a.serializeExecutionSummary(execution), "duplicate": duplicate}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if created {
		a.wakeDispatcher()
	}
	return result, nil
}

// 这是"数据库即队列"的入队口:把一次触发变成 workflow_executions 表里一条 status='queued' 的记录。
// 之后由 loops.go 的 dispatchLoop / claimNextExecution 用带条件的 UPDATE 把它认领走——整条队列就是这张表,不需要 Redis / 消息中间件。
// enqueueStartNodeExecution 创建 queued 状态的执行记录(DB 即队列)。
func (a *App) enqueueStartNodeExecution(definition *db.WorkflowDefinition, startNode M, triggerCtx M) (*db.WorkflowExecution, bool, error) {
	execution, duplicate, err := a.enqueueStartNodeExecutionWithDB(a.DB, definition, startNode, triggerCtx)
	if err == nil && !duplicate {
		a.wakeDispatcher()
	}
	return execution, duplicate, err
}

func (a *App) enqueueStartNodeExecutionWithDB(database *gorm.DB, definition *db.WorkflowDefinition, startNode M, triggerCtx M) (*db.WorkflowExecution, bool, error) {
	if definition.OwnerUserID == nil || *definition.OwnerUserID <= 0 || definition.IsBuiltin {
		return nil, false, bizErr("Builtin workflow templates cannot be executed")
	}
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

	// 幂等键去重:同一个 idempotencyKey 若已入过队,直接返回那条已存在的执行(第二个返回值 true 表示"这是重复")。
	// 这让"同一次触发被重试 / 重复投递"只产生一条执行,是把分布式里"至少投递一次"变成"效果只一次"的关键。
	if idempotencyKey != "" {
		duplicated, err := a.getExecutionByIdempotencyKeyWithDB(database, triggerType, idempotencyKey)
		if err != nil {
			return nil, false, err
		}
		if duplicated != nil {
			return duplicated, true, nil
		}
	}

	// concurrencyKey = 定义code:入口key,同一入口的执行共用它,用于限并发与限积压。
	concurrencyKey := int64Text(*definition.OwnerUserID) + ":" + definition.Code + ":" + startEntryKey
	var backlogCount int64
	// 数一下该键还有多少条在排队 / 待重试(status IN ('queued','retry_waiting'));IN ? 里传一个切片当列表。见 GO入门笔记『框架:GORM』。
	database.Model(&db.WorkflowExecution{}).
		Where("concurrency_key = ? AND status IN ?", concurrencyKey, []string{"queued", "retry_waiting"}).
		Count(&backlogCount)
	// 积压超过上限就拒绝入队(返回前面定义的哨兵错误),防止某个入口把队列撑爆。int64(...) 是类型转换。
	if backlogCount >= int64(a.Cfg.Workflow.BacklogLimitPerKey) {
		return nil, false, ErrBacklogExceeded
	}

	inputs := buildStartInputs(triggerCtx)
	now := time.Now()
	execution := db.WorkflowExecution{
		OwnerUserID:          *definition.OwnerUserID,
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
	// Create = INSERT 这条执行记录。见 GO入门笔记『框架:GORM』。
	result := database.Clauses(clause.OnConflict{DoNothing: true}).Create(&execution)
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected == 0 {
		if idempotencyKey != "" {
			duplicated, err := a.getExecutionByIdempotencyKeyWithDB(database, triggerType, idempotencyKey)
			if err != nil {
				return nil, false, err
			}
			if duplicated != nil {
				return duplicated, true, nil
			}
		}
		return nil, false, errors.New("workflow execution insert conflicted")
	}
	created, err := a.getExecutionByIDWithDB(database, execution.ID)
	if err != nil {
		return nil, false, err
	}
	// 非事务调用由包装方法唤醒，事务调用方在提交后唤醒 dispatcher。
	// 返回 (新建的执行, false=不是重复, nil=无错误)。
	return created, false, nil
}

// buildStartInputs 只接受本次触发的输入；定义中的固定值由节点配置承担。
func buildStartInputs(triggerCtx M) M {
	result := M{}
	if extra, ok := triggerCtx["inputs"].(map[string]any); ok {
		for key, value := range extra {
			result[key] = value
		}
	}
	return result
}

// ---------- 入口对齐与调度注册 ----------

// reconcileRuntimeEntriesForState 依据激活定义重建 runtime entries。
func (a *App) reconcileRuntimeEntriesForStateWithDB(database *gorm.DB, state *db.WorkflowRuntimeState, preserveExisting bool) error {
	if state.ActiveWorkflowDefinitionID == nil {
		return nil
	}
	definition, err := requireDefinitionWithDB(database, *state.ActiveWorkflowDefinitionID)
	if err != nil {
		return err
	}
	return a.reconcileRuntimeEntriesForDefinitionWithDB(database, state, definition, preserveExisting)
}

func (a *App) reconcileRuntimeEntriesForDefinitionWithDB(database *gorm.DB, state *db.WorkflowRuntimeState, definition *db.WorkflowDefinition, preserveExisting bool) error {
	graph := loadJSONObject(definition.GraphJSON)

	// 先把旧入口读出来存进 previousMap(键=entryKey,值=指向旧入口的指针),等下重建时好继承旧的启停状态 / 密钥。
	var previousEntries []db.WorkflowRuntimeEntry
	if err := database.Where("workflow_runtime_state_id = ?", state.ID).Find(&previousEntries).Error; err != nil {
		return err
	}
	previousMap := map[string]*db.WorkflowRuntimeEntry{}
	for i := range previousEntries {
		previousMap[previousEntries[i].EntryKey] = &previousEntries[i]
	}
	// 再全删旧入口,随后按当前 graph 重新逐个建出来(先清后建,保证与最新定义完全一致)。
	if err := database.Where("workflow_runtime_state_id = ?", state.ID).Delete(&db.WorkflowRuntimeEntry{}).Error; err != nil {
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
		// strings.HasPrefix:只处理以 "start." 开头的起始节点,其余跳过。
		if !strings.HasPrefix(nodeType, "start.") {
			continue
		}
		config, _ := node["config"].(map[string]any)
		entryKey := strings.TrimSpace(asString(config["entryKey"]))
		if entryKey == "" {
			return bizErr("Start node entryKey is required")
		}
		// SplitN(s, ".", 2) 按 "." 最多切成 2 段,[1] 取第二段:如 "start.schedule" → "schedule"。见 GO入门笔记『复合类型』。
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
		if err := database.Create(&entry).Error; err != nil {
			return err
		}

		switch {
		case startType == "schedule" && isEnabled:
			if err := a.registerScheduleEntryWithDB(database, &entry); err != nil {
				if updateErr := database.Model(&entry).Updates(map[string]any{
					"registration_status": "error", "last_error_message": err.Error(), "updated_at": time.Now(),
				}).Error; updateErr != nil {
					return updateErr
				}
			}
		case startType == "event" || startType == "webhook":
			status := "disabled"
			if isEnabled {
				status = "registered"
			}
			if err := database.Model(&entry).Updates(map[string]any{"registration_status": status, "updated_at": time.Now()}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

// registerScheduleEntry 计算并写入 schedule 入口的下次触发时间。
func (a *App) registerScheduleEntry(entry *db.WorkflowRuntimeEntry) error {
	return a.registerScheduleEntryWithDB(a.DB, entry)
}

func (a *App) registerScheduleEntryWithDB(database *gorm.DB, entry *db.WorkflowRuntimeEntry) error {
	updates, err := a.scheduleRegistrationUpdatesWithDB(database, entry)
	if err != nil {
		return err
	}
	return database.Model(&db.WorkflowRuntimeEntry{}).Where("id = ?", entry.ID).Updates(updates).Error
}

func (a *App) scheduleRegistrationUpdatesWithDB(database *gorm.DB, entry *db.WorkflowRuntimeEntry) (map[string]any, error) {
	definition, err := requireDefinitionWithDB(database, entry.WorkflowDefinitionID)
	if err != nil {
		return nil, err
	}
	graph := loadJSONObject(definition.GraphJSON)
	startNode := findStartNodeByEntryKey(graph, entry.EntryKey, "start.schedule")
	if startNode == nil {
		return nil, bizErr("Schedule start node does not exist in definition")
	}
	config, _ := startNode["config"].(map[string]any)
	// 算出"从现在起的下一次触发时间"。schedulerLoop 正是靠比较 next_run_at 是否 <= now 来决定该不该触发。
	nextRunAt, err := computeNextScheduleTime(config, time.Now())
	if err != nil {
		return nil, err
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
	return updates, nil
}

// computeNextScheduleTime 依据调度配置计算下一次触发时间。
// 返回 *time.Time:算出下次时间就返回它的地址;若永不再触发(如 once 已过期)就返回 nil。
func computeNextScheduleTime(config map[string]any, after time.Time) (*time.Time, error) {
	scheduleType := strings.TrimSpace(asString(config["scheduleType"]))
	if scheduleType == "" {
		scheduleType = "cron"
	}
	// 带表达式的 switch:按 scheduleType 的值选分支(cron 表达式 / 固定间隔 / 只跑一次)。见 GO入门笔记『其它会撞见的小语法』。
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
		// time.Duration 是时长类型;下面把数量 × 时间单位(time.Second 等)得到真正的时长。见 GO入门笔记『复合类型』。
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
// struct 是"把一组字段打包"的类型;字段首字母大写表示导出(能被别的包访问)。见 GO入门笔记『复合类型』『项目怎么组织』。
// DefinitionID 是 *int64(指针):用 nil 表示"未指定这个过滤条件",以区别于"指定为 0"。
type WorkflowExecutionQuery struct {
	OwnerUserID            int64
	Page                   CursorPage
	WorkflowDefinitionCode string
	Keyword                string
	TriggerType            string
	Status                 string
	DefinitionID           *int64
}

var terminalWorkflowExecutionStatuses = []string{"success", "failed", "canceled"}

// CancelExecution requests cancellation for a running execution and immediately
// closes executions that are not currently owned by a worker.
func (a *App) CancelExecution(executionID, ownerUserID int64) (M, error) {
	if ownerUserID <= 0 {
		return nil, notFoundErr("workflow execution")
	}
	now := time.Now().UTC()
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		var wait db.WorkflowExecutionWait
		waitErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("workflow_execution_id = ? AND owner_user_id = ? AND status IN ?", executionID, ownerUserID, []string{"pending", "processing"}).
			Take(&wait).Error
		if waitErr != nil && !errors.Is(waitErr, gorm.ErrRecordNotFound) {
			return waitErr
		}
		hasActiveWait := waitErr == nil
		if wait.Status == "processing" {
			return ErrWorkflowActionConflict
		}

		var execution db.WorkflowExecution
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND owner_user_id = ?", executionID, ownerUserID).
			Take(&execution).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return notFoundErr("workflow execution")
		}
		if err != nil {
			return err
		}
		switch execution.Status {
		case "success", "failed", "canceled":
			return nil
		case "running", "cancel_requested":
			if err := tx.Model(&db.WorkflowExecution{}).
				Where("id = ? AND owner_user_id = ? AND status IN ?", executionID, ownerUserID, []string{"running", "cancel_requested"}).
				Updates(map[string]any{"status": "cancel_requested", "cancel_requested_at": now}).Error; err != nil {
				return err
			}
		default:
			if err := tx.Model(&db.WorkflowExecution{}).
				Where("id = ? AND owner_user_id = ? AND status NOT IN ?", executionID, ownerUserID, terminalWorkflowExecutionStatuses).
				Updates(map[string]any{
					"status": "canceled", "cancel_requested_at": now, "finished_at": now,
					"next_retry_at": nil, "failure_category": "", "error_message": "",
				}).Error; err != nil {
				return err
			}
		}
		if !hasActiveWait {
			return nil
		}
		return tx.Model(&wait).Where("status = ?", "pending").
			Updates(map[string]any{"status": "canceled", "resolved_at": now, "updated_at": now}).Error
	})
	if err != nil {
		return nil, err
	}
	execution, err := a.getOwnedExecutionByID(executionID, ownerUserID)
	if err != nil {
		return nil, err
	}
	a.wakeDispatcher()
	return a.serializeExecutionSummary(execution), nil
}

// RerunExecution creates a new queue item pinned to the original definition
// version and persisted (already display-safe) input snapshot.
func (a *App) RerunExecution(executionID, ownerUserID int64, idempotencyKey string) (M, error) {
	if ownerUserID <= 0 {
		return nil, notFoundErr("workflow execution")
	}
	requestHash, err := canonicalRequestHash(M{"executionId": executionID})
	if err != nil {
		return nil, err
	}
	var rerun *db.WorkflowExecution
	created := false
	err = a.DB.Transaction(func(tx *gorm.DB) error {
		var original db.WorkflowExecution
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Preload("WorkflowDefinition").
			Where("id = ? AND owner_user_id = ?", executionID, ownerUserID).
			Take(&original).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return notFoundErr("workflow execution")
		}
		if err != nil {
			return err
		}
		if !containsString(terminalWorkflowExecutionStatuses, original.Status) {
			return bizErr("Only completed workflow executions can be rerun")
		}
		if original.WorkflowDefinition == nil || original.WorkflowDefinition.OwnerUserID == nil ||
			*original.WorkflowDefinition.OwnerUserID != ownerUserID || original.WorkflowDefinition.IsBuiltin {
			return notFoundErr("workflow definition")
		}

		record, reused, err := a.reserveIdempotencyRecord(
			tx, ownerUserID, "workflow:rerun:"+int64Text(executionID), idempotencyKey, requestHash,
		)
		if err != nil {
			return err
		}
		internalKey := workflowExecutionIdempotencyKey(record.ID, "rerun:"+int64Text(executionID))
		if reused {
			rerun, err = a.getExecutionByIdempotencyKeyWithDB(tx, "rerun", internalKey)
			if err != nil {
				return err
			}
			if rerun == nil {
				return errors.New("idempotency record has no workflow execution")
			}
			return nil
		}

		triggerCtx := extractTriggerContext(original.ContextSnapshotJSON)
		triggerCtx["triggerType"] = "rerun"
		triggerCtx["triggeredBy"] = ownerUserID
		triggerCtx["rerunOfExecutionId"] = executionID
		triggerKey := "rerun:" + int64Text(executionID)
		now := time.Now().UTC()
		row := db.WorkflowExecution{
			OwnerUserID: ownerUserID, WorkflowDefinitionID: original.WorkflowDefinitionID,
			StartEntryKey: original.StartEntryKey, StartNodeID: original.StartNodeID, StartNodeType: original.StartNodeType,
			TriggerType: "rerun", TriggeredBy: &ownerUserID, TriggerKey: &triggerKey, IdempotencyKey: &internalKey,
			ConcurrencyKey: original.ConcurrencyKey, Status: "queued", QueuedAt: now,
			MaxAttempts: original.MaxAttempts, InputSnapshotJSON: original.InputSnapshotJSON,
			ContextSnapshotJSON: serializeSnapshot(triggerCtx, a.Cfg.Workflow.MaxOutputSnapshotBytes),
			ResultSnapshotJSON:  "{}", RerunOfExecutionID: &original.ID,
		}
		if row.MaxAttempts <= 0 {
			row.MaxAttempts = a.Cfg.Workflow.MaxAttempts
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		rerun, err = a.getExecutionByIDWithDB(tx, row.ID)
		created = err == nil
		return err
	})
	if err != nil {
		return nil, err
	}
	if created {
		a.wakeDispatcher()
	}
	return M{"execution": a.serializeExecutionSummary(rerun), "duplicate": !created}, nil
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func (a *App) executionCancellationRequested(ctx context.Context, executionID int64) (bool, error) {
	var row struct {
		Status            string
		CancelRequestedAt *time.Time
	}
	err := a.dbWithContext(ctx).Model(&db.WorkflowExecution{}).
		Select("status", "cancel_requested_at").Where("id = ?", executionID).Take(&row).Error
	if err != nil {
		return false, err
	}
	return row.Status == "cancel_requested" || row.Status == "canceled" || row.CancelRequestedAt != nil, nil
}

// ListExecutions 分页查询执行记录。
func (a *App) ListExecutions(query WorkflowExecutionQuery) (M, error) {
	if query.OwnerUserID <= 0 {
		return nil, notFoundErr("workflow execution")
	}
	if query.DefinitionID != nil {
		if _, err := requireOwnedDefinitionWithDB(a.DB, *query.DefinitionID, query.OwnerUserID); err != nil {
			return nil, err
		}
	}
	// GORM 的查询是"链式构建":先拿到基础查询 q,再按条件一段段 q = q.Where(...) 累加,最后才真正执行(Count / Find)。
	q := a.DB.Model(&db.WorkflowExecution{}).
		Joins("LEFT JOIN workflow_definitions ON workflow_executions.workflow_definition_id = workflow_definitions.id").
		Where("workflow_executions.owner_user_id = ?", query.OwnerUserID)
	// query.DefinitionID 是指针,!= nil 表示调用方指定了这个过滤;*query.DefinitionID 取出它指向的值。见 GO入门笔记『复合类型』。
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
	} else {
		q = q.Where("workflow_executions.status IN ?", terminalWorkflowExecutionStatuses)
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
	afterID, err := query.Page.AfterID()
	if err != nil {
		return nil, err
	}
	if afterID > 0 {
		q = q.Where("workflow_executions.id < ?", afterID)
	}
	if err := q.Preload("WorkflowDefinition").
		Order("workflow_executions.id DESC").Limit(query.Page.Limit + 1).
		Find(&executions).Error; err != nil {
		return nil, err
	}
	hasMore := len(executions) > query.Page.Limit
	if hasMore {
		executions = executions[:query.Page.Limit]
	}
	records := make([]M, 0, len(executions))
	for i := range executions {
		records = append(records, a.serializeExecutionSummary(&executions[i]))
	}
	lastKey := ""
	if len(executions) > 0 {
		lastKey = int64CursorKey(executions[len(executions)-1].ID)
	}
	return cursorResult(records, query.Page, lastKey, hasMore, total), nil
}

// GetExecutionDetail 执行详情(图 + 节点日志 + attempt + 边日志)。
func (a *App) GetExecutionDetail(executionID, ownerUserID int64) (M, error) {
	execution, err := a.getOwnedExecutionByID(executionID, ownerUserID)
	if err != nil {
		return nil, err
	}
	data := a.serializeExecutionSummary(execution)
	graph := M{"schemaVersion": 2, "nodes": []any{}, "edges": []any{}}
	if execution.WorkflowDefinition != nil {
		graph = loadJSONObject(execution.WorkflowDefinition.GraphJSON)
	}
	data["graph"] = graph
	data["startNodeId"] = execution.StartNodeID
	data["input"] = loadJSONObject(execution.InputSnapshotJSON)
	data["output"] = loadJSONObject(execution.ResultSnapshotJSON)
	nodeNames := workflowNodeDisplayNames(graph)

	// 下面反复用同一套路:查一批行 → make 一个 []M → for range 逐行转成 map(M)塞进去 → 交给前端。
	var nodeLogs []db.WorkflowExecutionNode
	a.DB.Where("workflow_execution_id = ?", executionID).Order("started_at ASC, id ASC").Find(&nodeLogs)
	// make([]M, 0, len(...)):长度 0、预留容量 len,减少 append 时反复扩容。见 GO入门笔记『复合类型』。
	nodeItems := make([]M, 0, len(nodeLogs))
	for i := range nodeLogs {
		item := nodeLogs[i]
		var durationMs int64
		// DurationMs 是指针(可空):非 nil 才解引用取值,避免对 nil 解引用而崩溃。见 GO入门笔记『复合类型』。
		if item.DurationMs != nil {
			durationMs = *item.DurationMs
		}
		nodeItems = append(nodeItems, M{
			"id": item.ID, "nodeId": item.NodeID, "nodeName": nodeNames[item.NodeID],
			"status": item.Status, "statusLabel": workflowExecutionStatusLabel(item.Status),
			"startedAt": fmtTimeV(item.StartedAt), "finishedAt": fmtTime(item.FinishedAt),
			"durationMs": durationMs, "error": workflowExecutionError(item.ErrorMessage, ""),
			"input": loadJSONObject(item.InputSnapshotJSON), "output": loadJSONObject(item.OutputSnapshotJSON),
		})
	}
	data["nodeLogs"] = nodeItems

	var attempts []db.WorkflowExecutionAttempt
	a.DB.Where("workflow_execution_id = ?", executionID).Order("attempt ASC, id ASC").Find(&attempts)
	attemptItems := make([]M, 0, len(attempts))
	for i := range attempts {
		item := attempts[i]
		attemptItems = append(attemptItems, M{
			"id": item.ID, "attempt": item.Attempt,
			"startedAt": fmtTimeV(item.StartedAt), "finishedAt": fmtTime(item.FinishedAt),
			"status": item.Status, "statusLabel": workflowExecutionStatusLabel(item.Status),
			"error": workflowExecutionError(item.ErrorSummary, item.FailureCategory),
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
			"sourceNodeName": nodeNames[item.SourceNodeID], "targetNodeName": nodeNames[item.TargetNodeID],
			"traversalIndex": item.TraversalIndex, "iterationIndex": iterationIndex,
			"branchLabel": workflowBranchLabel(item.BranchKey),
			"createdAt":   fmtTimeV(item.CreatedAt),
		})
	}
	data["transitionLogs"] = transitionItems
	return data, nil
}

// ---------- 清理 ----------

// cleanupTerminalHistory 按批删除超出保留期的终态执行。
func (a *App) cleanupTerminalHistory() int {
	// AddDate(0, 0, -N) 把当前时间往前推 N 天,得到保留期的截止时间。
	cutoff := time.Now().AddDate(0, 0, -a.Cfg.Workflow.ExecutionRetentionDays)
	var ids []int64
	// Pluck("id", &ids):只取某一列的值到切片(等价 SELECT id FROM ...),这里先捞出一批要删的主键。见 GO入门笔记『框架:GORM』。
	a.DB.Model(&db.WorkflowExecution{}).
		Where("status IN ? AND finished_at IS NOT NULL AND finished_at < ?", terminalWorkflowExecutionStatuses, cutoff).
		Order("finished_at ASC, id ASC").Limit(a.Cfg.Workflow.RetentionDeleteBatchSize).
		Pluck("id", &ids)
	if len(ids) == 0 {
		return 0
	}
	// 再按这批 id 批量删除(DELETE ... WHERE id IN (...));result.RowsAffected 是实际删除的行数。
	result := a.DB.Where("id IN ?", ids).Delete(&db.WorkflowExecution{})
	return int(result.RowsAffected)
}

// ---------- 序列化与内部查询 ----------

func (a *App) getExecutionByID(executionID int64) (*db.WorkflowExecution, error) {
	return a.getExecutionByIDWithDB(a.DB, executionID)
}

func (a *App) getExecutionByIDWithDB(database *gorm.DB, executionID int64) (*db.WorkflowExecution, error) {
	var execution db.WorkflowExecution
	err := database.Preload("WorkflowDefinition").First(&execution, executionID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, notFoundErr("workflow execution")
	}
	if err != nil {
		return nil, err
	}
	return &execution, nil
}

func (a *App) getOwnedExecutionByID(executionID, ownerUserID int64) (*db.WorkflowExecution, error) {
	var execution db.WorkflowExecution
	err := a.DB.Preload("WorkflowDefinition").Where("id = ? AND owner_user_id = ?", executionID, ownerUserID).Take(&execution).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, notFoundErr("workflow execution")
	}
	return &execution, err
}

func (a *App) getExecutionByIdempotencyKeyWithDB(database *gorm.DB, triggerType, idempotencyKey string) (*db.WorkflowExecution, error) {
	if idempotencyKey == "" {
		return nil, nil
	}
	var execution db.WorkflowExecution
	if err := database.Preload("WorkflowDefinition").
		Where("trigger_type = ? AND idempotency_key = ?", triggerType, idempotencyKey).
		Order("id DESC").First(&execution).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &execution, nil
}

func (a *App) getRuntimeEntryByDefinitionAndKey(definitionID int64, entryKey string) *db.WorkflowRuntimeEntry {
	var entry db.WorkflowRuntimeEntry
	if err := a.DB.Where("workflow_definition_id = ? AND entry_key = ?", definitionID, entryKey).First(&entry).Error; err != nil {
		return nil
	}
	return &entry
}

func requireRuntimeEntryWithDB(database *gorm.DB, definitionID int64, entryKey string) (*db.WorkflowRuntimeEntry, error) {
	var entry db.WorkflowRuntimeEntry
	err := database.Where("workflow_definition_id = ? AND entry_key = ?", definitionID, entryKey).First(&entry).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, bizErr("Workflow runtime entry does not exist")
	}
	if err != nil {
		return nil, err
	}
	return &entry, nil
}

func (a *App) countExecutionsByStatus(ownerUserID int64, statuses []string) map[string]int64 {
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
		Where("owner_user_id = ? AND status IN ?", ownerUserID, statuses).Group("status").Scan(&rows)
	for _, row := range rows {
		result[row.Status] = row.Count
	}
	return result
}

func serializeRuntimeWithDB(database *gorm.DB, workflowCode string, state *db.WorkflowRuntimeState) (M, error) {
	entries := []M{}
	activeGraph := M{}
	var runtimeStateID, activeDefinitionID any
	activatedAt := ""
	if state != nil {
		runtimeStateID = state.ID
		if state.ActiveWorkflowDefinitionID != nil {
			activeDefinitionID = *state.ActiveWorkflowDefinitionID
			var definition db.WorkflowDefinition
			if err := database.Select("graph_json").First(&definition, *state.ActiveWorkflowDefinitionID).Error; err != nil {
				return nil, err
			}
			activeGraph = loadJSONObject(definition.GraphJSON)
		}
		activatedAt = fmtTime(state.ActivatedAt)
		var entryRows []db.WorkflowRuntimeEntry
		if err := database.Where("workflow_runtime_state_id = ?", state.ID).Order("entry_key ASC, id ASC").Find(&entryRows).Error; err != nil {
			return nil, err
		}
		for i := range entryRows {
			entries = append(entries, serializeRuntimeEntry(&entryRows[i], workflowStartDisplayName(activeGraph, entryRows[i].StartNodeID)))
		}
	}
	return M{
		"workflowCode": workflowCode, "runtimeStateId": runtimeStateID,
		"activeDefinitionId": activeDefinitionID, "activatedAt": activatedAt,
		"entries": entries,
	}, nil
}

func serializeRuntimeEntry(entry *db.WorkflowRuntimeEntry, entryName string) M {
	return M{
		"id": entry.ID, "definitionId": entry.WorkflowDefinitionID,
		"startNodeId": entry.StartNodeID, "entryKey": entry.EntryKey, "entryName": entryName, "startType": entry.StartType,
		"isEnabled": entry.IsEnabled, "registrationStatus": entry.RegistrationStatus,
		"nextRunAt": fmtTime(entry.NextRunAt), "lastTriggeredAt": fmtTime(entry.LastTriggeredAt),
		"lastErrorMessage": entry.LastErrorMessage, "secretHint": entry.SecretHint,
		"secretRotatedAt": fmtTime(entry.SecretRotatedAt),
	}
}

func (a *App) serializeExecutionSummary(execution *db.WorkflowExecution) M {
	definitionName := ""
	definitionVersion := 0
	entryName := ""
	if execution.WorkflowDefinition != nil {
		definitionVersion = execution.WorkflowDefinition.Version
		definitionName = execution.WorkflowDefinition.DisplayName
		entryName = workflowStartDisplayName(loadJSONObject(execution.WorkflowDefinition.GraphJSON), execution.StartNodeID)
	}
	var durationMs int64
	if execution.DurationMs != nil {
		durationMs = *execution.DurationMs
	}
	terminal := containsString(terminalWorkflowExecutionStatuses, execution.Status)
	return M{
		"id":                        execution.ID,
		"workflowDefinitionId":      execution.WorkflowDefinitionID,
		"workflowDefinitionVersion": definitionVersion,
		"workflowDefinitionName":    definitionName,
		"entryName":                 entryName,
		"triggerType":               execution.TriggerType,
		"triggerLabel":              workflowTriggerLabel(execution.TriggerType),
		"status":                    execution.Status,
		"statusLabel":               workflowExecutionStatusLabel(execution.Status),
		"queuedAt":                  fmtTimeV(execution.QueuedAt),
		"startedAt":                 fmtTime(execution.StartedAt),
		"finishedAt":                fmtTime(execution.FinishedAt),
		"cancelRequestedAt":         fmtTime(execution.CancelRequestedAt),
		"rerunOfExecutionId":        nilOrValue(execution.RerunOfExecutionID),
		"attemptCount":              execution.AttemptCount,
		"maxAttempts":               execution.MaxAttempts,
		"durationMs":                durationMs,
		"error":                     workflowExecutionError(execution.ErrorMessage, execution.FailureCategory),
		"canCancel":                 !terminal && execution.Status != "cancel_requested",
		"canRerun":                  terminal,
	}
}

func workflowNodeDisplayNames(graph M) map[string]string {
	names := map[string]string{}
	nodes, _ := graph["nodes"].([]any)
	for _, raw := range nodes {
		node, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id := asString(node["id"])
		name := strings.TrimSpace(asString(node["label"]))
		if name == "" {
			name = "未命名节点"
		}
		names[id] = name
	}
	return names
}

func workflowStartDisplayName(graph M, startNodeID string) string {
	nodes, _ := graph["nodes"].([]any)
	for _, raw := range nodes {
		node, ok := raw.(map[string]any)
		if !ok || asString(node["id"]) != startNodeID {
			continue
		}
		config, _ := node["config"].(map[string]any)
		if name := strings.TrimSpace(asString(config["displayName"])); name != "" {
			return name
		}
		if name := strings.TrimSpace(asString(node["label"])); name != "" {
			return name
		}
	}
	return "未命名入口"
}

func workflowExecutionStatusLabel(status string) string {
	return map[string]string{
		"queued": "排队中", "running": "运行中", "retry_waiting": "等待重试",
		"waiting_job": "等待任务", "waiting_action": "等待处理", "cancel_requested": "取消中",
		"success": "成功", "failed": "失败", "canceled": "已取消",
	}[status]
}

func workflowTriggerLabel(triggerType string) string {
	return map[string]string{
		"manual": "手动触发", "rerun": "重新运行", "schedule": "定时触发", "event": "事件触发", "webhook": "Webhook 触发",
	}[triggerType]
}

func workflowBranchLabel(branch string) string {
	if label := map[string]string{"true": "是", "false": "否", "default": "默认"}[branch]; label != "" {
		return label
	}
	return branch
}

func workflowExecutionError(summary, category string) any {
	if strings.TrimSpace(summary) == "" && strings.TrimSpace(category) == "" {
		return nil
	}
	categoryLabel := map[string]string{
		failureInfraRetryable: "临时服务故障",
		failureBusiness:       "配置或业务校验失败",
	}[category]
	return M{
		"summary": strings.TrimSpace(summary), "category": categoryLabel,
		"retryable": category == failureInfraRetryable,
	}
}

// buildSecretHint 生成密钥提示(前 4 + **** + 后 4),用于展示而不泄露完整明文。
// []rune(s) 把字符串按"字符"(而非字节)切开,runes[:4] / runes[len-4:] 是切片区间取子串,对中文等多字节字符也安全。见 GO入门笔记『复合类型』。
func buildSecretHint(secret string) string {
	runes := []rune(secret)
	if len(runes) <= 8 {
		return strings.Repeat("*", len(runes))
	}
	return string(runes[:4]) + "****" + string(runes[len(runes)-4:])
}
