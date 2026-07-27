package service

import (
	"fmt"
	"log"
	"sort"
	"time"

	"coinsphere/backend/internal/db"
)

// runResult 一次跑图结束后的上下文。
type runResult struct {
	Execution    *db.WorkflowExecution
	Definition   *db.WorkflowDefinition
	RuntimeEntry *db.WorkflowRuntimeEntry
	TriggerCtx   M
	SharedState  M
	StartedAt    time.Time
	FinishedAt   time.Time
}

// runExecutionGraph 只负责跑图,不推进 execution 终态状态机。
func (a *App) runExecutionGraph(executionID int64) (*runResult, error) {
	execution, err := a.getExecutionByID(executionID)
	if err != nil {
		return nil, err
	}
	definition := execution.WorkflowDefinition
	if definition == nil {
		return nil, bizErr("Workflow definition does not exist")
	}
	graph := loadJSONObject(definition.GraphJSON)
	startedAt := firstTime(execution.StartedAt, execution.ClaimedAt, &execution.QueuedAt)
	triggerCtx := extractTriggerContext(execution.ContextSnapshotJSON)
	triggerType := execution.TriggerType
	if triggerType == "" {
		triggerType = asString(triggerCtx["triggerType"])
	}
	inputs := loadJSONObject(execution.InputSnapshotJSON)

	runtimeEntry := a.getRuntimeEntryByDefinitionAndKey(definition.ID, execution.StartEntryKey)
	sharedState := M{
		"inputs": inputs,
		"trigger": M{
			"type":            triggerType,
			"payload":         orEmptyMap(triggerCtx["payload"]),
			"triggerKey":      derefString(execution.TriggerKey),
			"triggerOutboxId": nilOrValue(execution.TriggerOutboxID),
		},
		"definition": M{
			"id": definition.ID, "code": definition.Code,
			"version": definition.Version, "displayName": definition.DisplayName,
		},
		"runtimeEntry": serializeRuntimeEntryLite(runtimeEntry),
		"taskResult":   M{},
		"nodeOutputs":  M{},
	}

	result := &runResult{
		Execution: execution, Definition: definition, RuntimeEntry: runtimeEntry,
		TriggerCtx: triggerCtx, SharedState: sharedState, StartedAt: startedAt,
	}

	if err := validateWorkflowGraph(graph); err != nil {
		result.FinishedAt = time.Now()
		return result, err
	}
	startNode := findStartNode(graph, execution.StartNodeID, execution.StartEntryKey)
	if startNode == nil {
		result.FinishedAt = time.Now()
		return result, bizErr("Start entry does not exist in workflow definition")
	}

	log.Printf(
		"[engine] execution started: execution_id=%d workflow_code=%s entry=%s trigger=%s",
		execution.ID, definition.Code, execution.StartEntryKey, triggerType,
	)
	if err := a.runGraph(result, graph, asString(startNode["id"])); err != nil {
		result.FinishedAt = time.Now()
		log.Printf("[engine] execution failed: execution_id=%d workflow_code=%s err=%v", execution.ID, definition.Code, err)
		return result, err
	}
	result.FinishedAt = time.Now()
	log.Printf("[engine] execution finished: execution_id=%d workflow_code=%s status=success", execution.ID, definition.Code)
	return result, nil
}

// runGraph 深度遍历执行节点,并落节点/边日志。
func (a *App) runGraph(result *runResult, graph M, startNodeID string) error {
	nodesAny, _ := graph["nodes"].([]any)
	nodeMap := map[string]M{}
	for _, nodeAny := range nodesAny {
		if node, ok := nodeAny.(map[string]any); ok {
			nodeMap[asString(node["id"])] = node
		}
	}
	edgesAny, _ := graph["edges"].([]any)
	adjacency := map[string][]M{}
	for _, edgeAny := range edgesAny {
		if edge, ok := edgeAny.(map[string]any); ok {
			source := asString(edge["source"])
			adjacency[source] = append(adjacency[source], edge)
		}
	}
	for _, edgeList := range adjacency {
		sort.Slice(edgeList, func(i, j int) bool {
			return asString(edgeList[i]["id"]) < asString(edgeList[j]["id"])
		})
	}

	traversalIndex := 0
	execution := result.Execution

	logTransition := func(edge M, payload M, iterationIndex *int, branchKey string) {
		traversalIndex++
		a.DB.Create(&db.WorkflowExecutionTransition{
			WorkflowExecutionID: execution.ID,
			EdgeID:              asString(edge["id"]),
			SourceNodeID:        asString(edge["source"]),
			TargetNodeID:        asString(edge["target"]),
			TraversalIndex:      traversalIndex,
			IterationIndex:      iterationIndex,
			BranchKey:           truncateRunes(branchKey, 32),
			PayloadSnapshotJSON: serializeSnapshot(payload, a.Cfg.Workflow.MaxOutputSnapshotBytes),
			CreatedAt:           time.Now(),
		})
	}

	var executeFrom func(nodeID string) error
	executeFrom = func(nodeID string) error {
		node := nodeMap[nodeID]
		if node == nil {
			return bizErr("Workflow node does not exist: %s", nodeID)
		}
		nodeType := asString(node["type"])
		nodeLog := db.WorkflowExecutionNode{
			WorkflowExecutionID: execution.ID, NodeID: nodeID, NodeType: nodeType,
			Status: "running", StartedAt: time.Now(),
			InputSnapshotJSON: serializeSnapshot(result.SharedState, a.Cfg.Workflow.MaxOutputSnapshotBytes),
		}
		if err := a.DB.Create(&nodeLog).Error; err != nil {
			return err
		}

		publishEvent := func(eventType, aggregateType string, payload, metadata M) (int64, error) {
			mergedPayload := a.buildEventBasePayload(result.Definition, result.RuntimeEntry, execution.ID, result.TriggerCtx)
			for key, value := range payload {
				mergedPayload[key] = value
			}
			mergedMetadata := M{
				"triggerType":  orString(asString(result.TriggerCtx["triggerType"]), execution.TriggerType),
				"workflowCode": result.Definition.Code,
			}
			for key, value := range metadata {
				mergedMetadata[key] = value
			}
			return a.publishDomainEvent(
				eventType, aggregateType, fmt.Sprint(execution.ID),
				mergedPayload, mergedMetadata, &execution.ID, &nodeLog.ID,
			)
		}

		definitionImpl, err := getNodeDefinition(nodeType)
		var nodeResult *nodeExecResult
		if err == nil {
			nodeResult, err = definitionImpl.Execute(&nodeExecContext{
				App: a, Definition: result.Definition, RuntimeEntry: result.RuntimeEntry,
				Execution: execution, NodeLog: &nodeLog, Node: node, Graph: graph,
				SharedState: result.SharedState, TriggerCtx: result.TriggerCtx,
				PublishEvent: publishEvent,
			})
		}
		finishedAt := time.Now()
		durationMs := finishedAt.Sub(nodeLog.StartedAt).Milliseconds()
		if err != nil {
			a.DB.Model(&nodeLog).Updates(map[string]any{
				"status": "failed", "finished_at": finishedAt, "duration_ms": durationMs,
				"error_message": err.Error(),
			})
			log.Printf("[engine] node failed: execution_id=%d node_id=%s err=%v", execution.ID, nodeID, err)
			return err
		}
		a.DB.Model(&nodeLog).Updates(map[string]any{
			"status": "success", "finished_at": finishedAt, "duration_ms": durationMs,
			"output_snapshot_json": serializeSnapshot(nodeResult.Output, a.Cfg.Workflow.MaxOutputSnapshotBytes),
		})

		if nodeResult.Terminate {
			return nil
		}

		nextEdges := adjacency[nodeID]
		if nodeResult.SelectedBranch != nil {
			filtered := make([]M, 0, len(nextEdges))
			for _, edge := range nextEdges {
				if edgeBranchKey(edge) == *nodeResult.SelectedBranch {
					filtered = append(filtered, edge)
				}
			}
			nextEdges = filtered
		}

		if nodeResult.ForeachItems != nil {
			if len(nextEdges) == 0 {
				return nil
			}
			nextEdge := nextEdges[0]
			nextNodeID := asString(nextEdge["target"])
			loopConfigAll, _ := result.SharedState["loopConfig"].(map[string]any)
			loopConfig, _ := loopConfigAll[nodeID].(map[string]any)
			itemKey := orString(asString(loopConfig["itemKey"]), "currentItem")
			indexKey := orString(asString(loopConfig["indexKey"]), "currentIndex")
			previousItem, hasPreviousItem := result.SharedState[itemKey]
			previousIndex, hasPreviousIndex := result.SharedState[indexKey]
			for index, item := range nodeResult.ForeachItems {
				result.SharedState[itemKey] = item
				result.SharedState[indexKey] = index
				iteration := index
				logTransition(nextEdge, M{
					"output": nodeResult.Output, "loopItem": item, "loopIndex": index,
					"itemKey": itemKey, "indexKey": indexKey,
				}, &iteration, edgeBranchKey(nextEdge))
				if err := executeFrom(nextNodeID); err != nil {
					return err
				}
			}
			if hasPreviousItem {
				result.SharedState[itemKey] = previousItem
			} else {
				delete(result.SharedState, itemKey)
			}
			if hasPreviousIndex {
				result.SharedState[indexKey] = previousIndex
			} else {
				delete(result.SharedState, indexKey)
			}
			return nil
		}

		for _, edge := range nextEdges {
			payload := M{"output": nodeResult.Output}
			branchKey := edgeBranchKey(edge)
			if nodeResult.SelectedBranch != nil {
				payload["selectedBranch"] = *nodeResult.SelectedBranch
				branchKey = *nodeResult.SelectedBranch
			}
			logTransition(edge, payload, nil, branchKey)
			if err := executeFrom(asString(edge["target"])); err != nil {
				return err
			}
		}
		return nil
	}

	return executeFrom(startNodeID)
}

// ---------- 标准事件发布 ----------

func (a *App) buildEventBasePayload(definition *db.WorkflowDefinition, runtimeEntry *db.WorkflowRuntimeEntry, executionID int64, triggerCtx M) M {
	startEntryKey, startNodeType := "", ""
	if runtimeEntry != nil {
		startEntryKey = runtimeEntry.EntryKey
		startNodeType = runtimeEntry.StartType
	}
	return M{
		"workflowExecutionId":       executionID,
		"workflowDefinitionId":      definition.ID,
		"workflowDefinitionCode":    definition.Code,
		"workflowDefinitionVersion": definition.Version,
		"workflowDefinitionName":    definition.DisplayName,
		"startEntryKey":             startEntryKey,
		"startNodeType":             startNodeType,
		"triggerType":               asString(triggerCtx["triggerType"]),
	}
}

func (a *App) publishStandardEvent(
	definition *db.WorkflowDefinition, runtimeEntry *db.WorkflowRuntimeEntry,
	executionID int64, nodeID *int64, eventType string, triggerCtx, payload M,
) {
	mergedPayload := a.buildEventBasePayload(definition, runtimeEntry, executionID, triggerCtx)
	for key, value := range payload {
		mergedPayload[key] = value
	}
	metadata := M{"workflowCode": definition.Code, "nodeId": nilOrValue(nodeID)}
	if _, err := a.publishDomainEvent(
		eventType, "workflow_execution", fmt.Sprint(executionID),
		mergedPayload, metadata, &executionID, nodeID,
	); err != nil {
		log.Printf("[engine] publish standard event failed: execution_id=%d event=%s err=%v", executionID, eventType, err)
	}
}

// publishExecutionSucceeded success 终态后的标准事件。
func (a *App) publishExecutionSucceeded(result *runResult) {
	taskResult := orEmptyMap(result.SharedState["taskResult"])
	payload := M{"status": "success"}
	for key, value := range taskResult {
		payload[key] = value
	}
	a.publishStandardEvent(result.Definition, result.RuntimeEntry, result.Execution.ID, nil, "workflow.execution.succeeded", result.TriggerCtx, payload)
	a.publishStandardEvent(result.Definition, result.RuntimeEntry, result.Execution.ID, nil, "workflow.execution.finished", result.TriggerCtx, payload)
	if result.RuntimeEntry != nil {
		a.DB.Model(&db.WorkflowRuntimeEntry{}).Where("id = ?", result.RuntimeEntry.ID).Updates(map[string]any{
			"last_triggered_at": result.FinishedAt, "last_error_message": "", "updated_at": time.Now(),
		})
	}
}

// publishExecutionFailed failed 终态后的标准事件。
func (a *App) publishExecutionFailed(result *runResult, errorMessage string) {
	a.publishFailureEvents(result.Definition, result.RuntimeEntry, result.Execution.ID, result.TriggerCtx, errorMessage, result.StartedAt, result.FinishedAt)
	if result.RuntimeEntry != nil {
		a.DB.Model(&db.WorkflowRuntimeEntry{}).Where("id = ?", result.RuntimeEntry.ID).Updates(map[string]any{
			"last_triggered_at": result.FinishedAt, "last_error_message": errorMessage, "updated_at": time.Now(),
		})
	}
}

// publishRecoveredFailureEvents stale 恢复后的失败事件。
func (a *App) publishRecoveredFailureEvents(executionID int64, errorMessage string) {
	execution, err := a.getExecutionByID(executionID)
	if err != nil || execution.WorkflowDefinition == nil {
		return
	}
	runtimeEntry := a.getRuntimeEntryByDefinitionAndKey(execution.WorkflowDefinition.ID, execution.StartEntryKey)
	triggerCtx := extractTriggerContext(execution.ContextSnapshotJSON)
	startedAt := firstTime(execution.StartedAt, execution.ClaimedAt, &execution.QueuedAt)
	finishedAt := time.Now()
	if execution.FinishedAt != nil {
		finishedAt = *execution.FinishedAt
	}
	a.publishFailureEvents(execution.WorkflowDefinition, runtimeEntry, executionID, triggerCtx, errorMessage, startedAt, finishedAt)
}

func (a *App) publishFailureEvents(
	definition *db.WorkflowDefinition, runtimeEntry *db.WorkflowRuntimeEntry,
	executionID int64, triggerCtx M, errorMessage string, startedAt, finishedAt time.Time,
) {
	a.publishStandardEvent(definition, runtimeEntry, executionID, nil, "workflow.execution.failed", triggerCtx, M{
		"status": "failed", "errorMessage": errorMessage,
		"startedAt": fmtTimeV(startedAt), "finishedAt": fmtTimeV(finishedAt),
	})
	a.publishStandardEvent(definition, runtimeEntry, executionID, nil, "workflow.execution.finished", triggerCtx, M{
		"status": "failed", "errorMessage": errorMessage,
	})
}

// ---------- 小工具 ----------

func extractTriggerContext(payloadJSON string) M {
	value := loadJSONObject(payloadJSON)
	if _, hasType := value["triggerType"]; hasType {
		return value
	}
	if _, hasPayload := value["payload"]; hasPayload {
		return value
	}
	trigger, _ := value["trigger"].(map[string]any)
	if trigger == nil {
		return M{}
	}
	return M{
		"triggerType":     trigger["type"],
		"payload":         orEmptyMap(trigger["payload"]),
		"triggerKey":      trigger["triggerKey"],
		"triggerOutboxId": trigger["triggerOutboxId"],
	}
}

func findStartNode(graph M, startNodeID, startEntryKey string) M {
	nodes, _ := graph["nodes"].([]any)
	for _, nodeAny := range nodes {
		if node, ok := nodeAny.(map[string]any); ok && asString(node["id"]) == startNodeID && startNodeID != "" {
			return node
		}
	}
	return findStartNodeByEntryKey(graph, startEntryKey, "")
}

func buildStartInputs(startNode M, triggerCtx M) M {
	config, _ := startNode["config"].(map[string]any)
	result := M{}
	if base, ok := config["inputBindings"].(map[string]any); ok {
		for key, value := range base {
			result[key] = value
		}
	}
	if extra, ok := triggerCtx["inputs"].(map[string]any); ok {
		for key, value := range extra {
			result[key] = value
		}
	}
	return result
}

func serializeRuntimeEntryLite(runtimeEntry *db.WorkflowRuntimeEntry) M {
	if runtimeEntry == nil {
		return M{}
	}
	return M{
		"id": runtimeEntry.ID, "entryKey": runtimeEntry.EntryKey,
		"startType": runtimeEntry.StartType, "isEnabled": runtimeEntry.IsEnabled,
	}
}

func orEmptyMap(value any) M {
	if m, ok := value.(map[string]any); ok {
		return m
	}
	return M{}
}

func orString(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func firstTime(values ...*time.Time) time.Time {
	for _, value := range values {
		if value != nil && !value.IsZero() {
			return *value
		}
	}
	return time.Now()
}

func int64Text(value int64) string { return fmt.Sprint(value) }

func timeNow() time.Time { return time.Now() }
