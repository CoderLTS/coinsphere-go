package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"sort"
	"strings"
	"time"

	"cel.dev/cel-go/common/types/ref"
	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/security"
	"coinsphere/backend/plugin/sdk"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	BatchStatusQueued    = "queued"
	BatchStatusRunning   = "running"
	BatchStatusRetrying  = "retrying"
	BatchStatusSucceeded = "succeeded"
	BatchStatusFailed    = "failed"
	BatchStatusCancelled = "cancelled"
	batchLeaseDuration   = 30 * time.Second
	batchPollInterval    = 250 * time.Millisecond
	batchMaxAttempts     = 3
)

type WorkflowBatchView struct {
	ID                    int64  `json:"id"`
	WorkflowID            int64  `json:"workflowId"`
	RevisionID            int64  `json:"revisionId"`
	TriggerType           string `json:"triggerType"`
	Status                string `json:"status"`
	CurrentNodeInstanceID string `json:"currentNodeInstanceId,omitempty"`
	TriggeredAt           string `json:"triggeredAt"`
	StartedAt             string `json:"startedAt,omitempty"`
	CompletedAt           string `json:"completedAt,omitempty"`
	CancelRequestedAt     string `json:"cancelRequestedAt,omitempty"`
	ErrorCategory         string `json:"errorCategory,omitempty"`
}

type WorkflowBatchActionPayload struct {
	Action string `json:"action"`
}

type workflowBatchGraph struct {
	graph       workflowGraph
	nodes       map[string]workflowGraphNode
	descriptors map[string]sdk.NodeDescriptor
	order       []string
	incoming    map[string][]workflowGraphEdge
}

type bufferedNodeState struct {
	app        *App
	workflowID int64
	revisionID int64
	node       workflowGraphNode
	stateMode  sdk.StateMode
	pending    json.RawMessage
}

func (a *App) CreateWorkflowBatch(ctx context.Context, workflowID int64, principal *Principal) (WorkflowBatchView, error) {
	if principal == nil || principal.User == nil || principal.User.ID <= 0 {
		return WorkflowBatchView{}, ErrPermission
	}
	now := time.Now().UTC()
	batch := db.ExecutionBatch{}
	err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var workflow db.Workflow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&workflow, workflowID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: workflow", ErrNotFound)
			}
			return errors.New("lock workflow failed")
		}
		if workflow.Status != WorkflowStatusRunning || workflow.ActiveRevisionID == nil {
			return fmt.Errorf("%w: workflow is not accepting batches", ErrConflict)
		}
		var revision db.WorkflowRevision
		if err := tx.First(&revision, *workflow.ActiveRevisionID).Error; err != nil {
			return errors.New("load active workflow revision failed")
		}
		graph, err := a.buildWorkflowBatchGraph(revision.GraphJSON)
		if err != nil || graph.nodes[revision.MainTriggerNodeID].NodeType != "core.manual" {
			return fmt.Errorf("%w: workflow does not use a manual trigger", ErrConflict)
		}
		if err := enforceWorkflowBacklog(tx, workflowID); err != nil {
			return err
		}
		ownerID := principal.User.ID
		batch = db.ExecutionBatch{
			WorkflowID: workflowID, RevisionID: revision.ID, TriggerType: "manual",
			TriggerKey: security.RandomToken(), Status: BatchStatusQueued, NotBefore: now,
			TriggeredAt: now, CreatedBy: &ownerID, CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Create(&batch).Error; err != nil {
			return errors.New("create workflow batch failed")
		}
		return nil
	})
	if err != nil {
		return WorkflowBatchView{}, err
	}
	return workflowBatchView(batch), nil
}

func (a *App) ListWorkflowBatches(ctx context.Context, workflowID int64) ([]WorkflowBatchView, error) {
	var exists int64
	if err := a.DB.WithContext(ctx).Model(&db.Workflow{}).Where("id = ?", workflowID).Count(&exists).Error; err != nil {
		return nil, errors.New("load workflow failed")
	}
	if exists == 0 {
		return nil, fmt.Errorf("%w: workflow", ErrNotFound)
	}
	var batches []db.ExecutionBatch
	if err := a.DB.WithContext(ctx).Where("workflow_id = ?", workflowID).Order("created_at DESC, id DESC").Limit(100).Find(&batches).Error; err != nil {
		return nil, errors.New("list workflow batches failed")
	}
	items := make([]WorkflowBatchView, len(batches))
	for index := range batches {
		items[index] = workflowBatchView(batches[index])
	}
	return items, nil
}

func (a *App) GetWorkflowBatch(ctx context.Context, batchID int64) (WorkflowBatchView, error) {
	var batch db.ExecutionBatch
	if err := a.DB.WithContext(ctx).First(&batch, batchID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return WorkflowBatchView{}, fmt.Errorf("%w: workflow batch", ErrNotFound)
		}
		return WorkflowBatchView{}, errors.New("load workflow batch failed")
	}
	return workflowBatchView(batch), nil
}

func (a *App) ApplyWorkflowBatchAction(ctx context.Context, batchID int64, payload WorkflowBatchActionPayload) (WorkflowBatchView, error) {
	action := strings.ToLower(strings.TrimSpace(payload.Action))
	now := time.Now().UTC()
	err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var batch db.ExecutionBatch
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&batch, batchID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: workflow batch", ErrNotFound)
			}
			return errors.New("lock workflow batch failed")
		}
		switch action {
		case "cancel":
			if batch.Status == BatchStatusQueued || batch.Status == BatchStatusRetrying {
				return tx.Model(&batch).Updates(map[string]any{
					"status": BatchStatusCancelled, "cancel_requested_at": now, "completed_at": now,
					"lease_token": nil, "lease_expires_at": nil, "updated_at": now,
				}).Error
			}
			if batch.Status != BatchStatusRunning {
				return fmt.Errorf("%w: batch is already terminal", ErrConflict)
			}
			if err := tx.Model(&batch).Updates(map[string]any{"cancel_requested_at": now, "updated_at": now}).Error; err != nil {
				return errors.New("request workflow batch cancellation failed")
			}
		case "retry":
			if batch.Status != BatchStatusFailed {
				return fmt.Errorf("%w: only a failed batch can be retried", ErrConflict)
			}
			if err := tx.Model(&batch).Updates(map[string]any{
				"status": BatchStatusRetrying, "not_before": now, "completed_at": nil,
				"error_category": nil, "cancel_requested_at": nil, "updated_at": now,
			}).Error; err != nil {
				return errors.New("retry workflow batch failed")
			}
		default:
			return errors.New("batch action must be cancel or retry")
		}
		return nil
	})
	if err != nil {
		return WorkflowBatchView{}, err
	}
	if action == "cancel" {
		a.batchCancelMu.Lock()
		cancel := a.batchCancels[batchID]
		a.batchCancelMu.Unlock()
		if cancel != nil {
			cancel()
		}
	}
	return a.GetWorkflowBatch(ctx, batchID)
}

func (a *App) RunBatchEngine(ctx context.Context) error {
	if err := a.recoverExpiredBatches(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(batchPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-ticker.C:
			if err := a.enqueueScheduledBatches(ctx, now.UTC()); err != nil {
				slog.Error("workflow schedule scan failed", "error_category", "batch_schedule")
			}
			for {
				batch, ok, err := a.claimWorkflowBatch(ctx, now.UTC())
				if err != nil {
					slog.Error("workflow batch claim failed", "error_category", "batch_queue")
					break
				}
				if !ok {
					break
				}
				a.batchWG.Add(1)
				go func() {
					defer a.batchWG.Done()
					a.executeWorkflowBatch(ctx, batch)
				}()
			}
		}
	}
}

func (a *App) WaitForWorkflowBatches(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		a.batchWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *App) recoverExpiredBatches(ctx context.Context) error {
	now := time.Now().UTC()
	var recovered int64
	err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`
UPDATE workflow_node_runs nr
SET status = 'failed', error_category = 'lease_expired', completed_at = ?,
    duration_ms = GREATEST(EXTRACT(EPOCH FROM (? - nr.started_at)) * 1000, 0)::BIGINT
FROM execution_batches eb
WHERE nr.batch_id = eb.id AND nr.status = 'running'
  AND eb.status = 'running' AND eb.lease_expires_at < ?`, now, now, now).Error; err != nil {
			return err
		}
		result := tx.Model(&db.ExecutionBatch{}).
			Where("status = ? AND lease_expires_at < ?", BatchStatusRunning, now).
			Updates(map[string]any{
				"status": BatchStatusQueued, "lease_token": nil, "lease_expires_at": nil,
				"not_before": now, "updated_at": now,
			})
		recovered = result.RowsAffected
		return result.Error
	})
	if err != nil {
		return errors.New("recover expired workflow batches failed")
	}
	if recovered > 0 {
		slog.Info("workflow batches recovered", "count", recovered)
	}
	return nil
}

func (a *App) claimWorkflowBatch(ctx context.Context, now time.Time) (db.ExecutionBatch, bool, error) {
	a.batchClaimMu.Lock()
	defer a.batchClaimMu.Unlock()
	token := security.RandomToken()
	leaseExpiry := now.Add(batchLeaseDuration)
	var batch db.ExecutionBatch
	query := `
WITH candidate AS (
    SELECT eb.id
    FROM execution_batches eb
    JOIN workflows w ON w.id = eb.workflow_id
    JOIN workflow_runtimes wr ON wr.workflow_id = eb.workflow_id
    WHERE eb.status IN ('queued', 'retrying')
      AND eb.not_before <= ?
      AND w.status = 'running'
      AND (SELECT COUNT(*) FROM execution_batches active
           WHERE active.workflow_id = eb.workflow_id AND active.status = 'running') < wr.max_concurrent_batches
    ORDER BY eb.not_before, eb.created_at, eb.id
    FOR UPDATE OF eb SKIP LOCKED
    LIMIT 1
)
UPDATE execution_batches eb
SET status = 'running', lease_token = ?, lease_expires_at = ?,
    started_at = COALESCE(eb.started_at, ?), updated_at = ?
FROM candidate
WHERE eb.id = candidate.id
RETURNING eb.*`
	err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return tx.Raw(query, now, token, leaseExpiry, now, now).Scan(&batch).Error
	})
	if err != nil {
		return db.ExecutionBatch{}, false, err
	}
	return batch, batch.ID > 0, nil
}

func (a *App) executeWorkflowBatch(parent context.Context, batch db.ExecutionBatch) {
	ctx, cancel := context.WithCancel(parent)
	a.batchCancelMu.Lock()
	a.batchCancels[batch.ID] = cancel
	a.batchCancelMu.Unlock()
	defer func() {
		cancel()
		a.batchCancelMu.Lock()
		delete(a.batchCancels, batch.ID)
		a.batchCancelMu.Unlock()
	}()

	leaseDone := make(chan struct{})
	go a.renewBatchLease(ctx, batch.ID, *batch.LeaseToken, leaseDone)
	defer close(leaseDone)

	var revision db.WorkflowRevision
	if err := a.DB.WithContext(ctx).First(&revision, batch.RevisionID).Error; err != nil {
		a.failWorkflowBatch(batch.ID, "revision")
		return
	}
	graph, err := a.buildWorkflowBatchGraph(revision.GraphJSON)
	if err != nil {
		a.failWorkflowBatch(batch.ID, "graph")
		return
	}
	outputs, err := a.loadWorkflowCheckpoints(ctx, batch.ID)
	if err != nil {
		a.failWorkflowBatch(batch.ID, "checkpoint")
		return
	}
	event := map[string]string{"type": batch.TriggerType, "triggeredAt": formatWorkflowTime(batch.TriggeredAt)}
	for _, nodeID := range graph.order {
		if _, completed := outputs[nodeID]; completed {
			continue
		}
		if cancelled, paused := a.batchShouldStop(ctx, batch.ID, batch.WorkflowID); cancelled || paused {
			if cancelled {
				a.cancelWorkflowBatch(batch.ID)
			} else {
				a.requeueWorkflowBatch(batch.ID)
			}
			return
		}
		node := graph.nodes[nodeID]
		if nodeID != revision.MainTriggerNodeID {
			reachable, err := workflowNodeReachable(graph.incoming[nodeID], outputs, event)
			if err != nil {
				a.failWorkflowBatch(batch.ID, "condition")
				return
			}
			if !reachable {
				continue
			}
		}
		input, err := resolveWorkflowNodeInput(node, outputs, event)
		if err != nil {
			a.failWorkflowBatch(batch.ID, "input")
			return
		}
		outcome := a.executeWorkflowNode(ctx, batch, revision, graph, node, input)
		if outcome.err != nil {
			if errors.Is(outcome.err, context.Canceled) || ctx.Err() != nil {
				cancelled, _ := a.batchShouldStop(ctx, batch.ID, batch.WorkflowID)
				if cancelled {
					a.cancelWorkflowBatch(batch.ID)
				} else {
					a.requeueWorkflowBatch(batch.ID)
				}
				return
			}
			if outcome.attempt < batchMaxAttempts {
				a.retryWorkflowBatch(batch.ID, outcome.attempt)
			} else {
				a.failWorkflowBatch(batch.ID, outcome.category)
			}
			return
		}
		outputs[nodeID] = outcome.output
	}
	a.completeWorkflowBatch(batch.ID)
}

type workflowNodeOutcome struct {
	output   map[string]any
	attempt  int
	category string
	err      error
}

func (a *App) executeWorkflowNode(ctx context.Context, batch db.ExecutionBatch, revision db.WorkflowRevision, graph workflowBatchGraph, node workflowGraphNode, input map[string]any) workflowNodeOutcome {
	desc := graph.descriptors[node.NodeInstanceID]
	attempt, err := a.nextWorkflowNodeAttempt(ctx, batch.ID, node.NodeInstanceID)
	if err != nil {
		return workflowNodeOutcome{category: "node_run", err: err}
	}
	operationKey := workflowOperationKey(batch.ID, node.NodeInstanceID, 0)
	startedAt := time.Now().UTC()
	run := db.WorkflowNodeRun{
		BatchID: batch.ID, NodeInstanceID: node.NodeInstanceID, NodeType: node.NodeType,
		NodeVersion: node.NodeVersion, ExecutionPool: string(desc.Pool), Attempt: attempt,
		OperationKey: operationKey, Status: BatchStatusRunning, StartedAt: startedAt,
	}
	if err := a.DB.WithContext(ctx).Create(&run).Error; err != nil {
		return workflowNodeOutcome{attempt: attempt, category: "node_run", err: err}
	}
	if validateWorkflowSchemaValue(desc.InputSchema, input) != nil {
		a.finishWorkflowNodeRun(run.ID, BatchStatusFailed, "input", startedAt)
		return workflowNodeOutcome{attempt: attempt, category: "input", err: errors.New("node input does not match its JSON Schema")}
	}
	_ = a.DB.WithContext(ctx).Model(&db.ExecutionBatch{}).Where("id = ?", batch.ID).
		Updates(map[string]any{"current_node_instance_id": node.NodeInstanceID, "updated_at": startedAt}).Error

	slot := a.streamSlots
	if desc.Pool == sdk.PoolCompute {
		slot = a.computeSlots
	}
	select {
	case slot <- struct{}{}:
		defer func() { <-slot }()
	case <-ctx.Done():
		a.finishWorkflowNodeRun(run.ID, BatchStatusCancelled, "cancelled", startedAt)
		return workflowNodeOutcome{attempt: attempt, category: "cancelled", err: ctx.Err()}
	}

	state := &bufferedNodeState{app: a, workflowID: batch.WorkflowID, revisionID: revision.ID, node: node, stateMode: desc.State}
	request := sdk.ActionRequest{
		Revision:       sdk.RevisionRef{WorkflowID: fmt.Sprint(batch.WorkflowID), RevisionID: fmt.Sprint(revision.ID)},
		NodeInstanceID: node.NodeInstanceID, OperationKey: operationKey,
		Input: mustJSON(input), Config: append(json.RawMessage(nil), node.Config...),
		Secrets: workflowSecretReader{app: a, revisionID: revision.ID, nodeInstanceID: node.NodeInstanceID},
		State:   state, Artifacts: unavailableArtifactStore{},
		Logger: slog.Default().With("event_category", "workflow_node", "node_type", node.NodeType),
	}
	result, category, executeErr := a.callWorkflowNode(ctx, batch, node, request)
	if executeErr == nil && len(result.Artifacts) > 0 {
		category, executeErr = "artifact_unavailable", errors.New("workflow artifacts are unavailable before P1-D")
	}
	var output map[string]any
	if executeErr == nil {
		if len(result.Output) == 0 {
			result.Output = json.RawMessage(`{}`)
		}
		if json.Unmarshal(result.Output, &output) != nil || output == nil || validateWorkflowSchemaValue(desc.OutputSchema, output) != nil {
			category, executeErr = "output", errors.New("node output does not match its JSON Schema")
		}
	}
	if executeErr != nil {
		status := BatchStatusFailed
		if errors.Is(executeErr, context.Canceled) || ctx.Err() != nil {
			status, category = BatchStatusCancelled, "cancelled"
		}
		a.finishWorkflowNodeRun(run.ID, status, category, startedAt)
		return workflowNodeOutcome{attempt: attempt, category: category, err: executeErr}
	}
	if err := a.commitWorkflowNodeSuccess(ctx, run, batch, revision, node, operationKey, result.Output, state.pending, startedAt); err != nil {
		a.finishWorkflowNodeRun(run.ID, BatchStatusFailed, "checkpoint", startedAt)
		return workflowNodeOutcome{attempt: attempt, category: "checkpoint", err: err}
	}
	return workflowNodeOutcome{attempt: attempt, output: output}
}

func (a *App) callWorkflowNode(ctx context.Context, batch db.ExecutionBatch, node workflowGraphNode, request sdk.ActionRequest) (sdk.ActionResult, string, error) {
	switch node.NodeType {
	case "core.manual", "core.schedule":
		return sdk.ActionResult{Output: mustJSON(map[string]any{"triggeredAt": formatWorkflowTime(batch.TriggeredAt)})}, "", nil
	case "core.constant":
		var config struct {
			Value string `json:"value"`
		}
		if json.Unmarshal(node.Config, &config) != nil {
			return sdk.ActionResult{}, "config", errors.New("constant config is invalid")
		}
		return sdk.ActionResult{Output: mustJSON(map[string]any{"value": config.Value})}, "", nil
	case "core.end":
		return sdk.ActionResult{Output: json.RawMessage(`{}`)}, "", nil
	default:
		_, handler, ok := a.Plugins.Action(node.NodeType)
		if !ok {
			return sdk.ActionResult{}, "handler", errors.New("node action handler is unavailable")
		}
		result, err := handler.Execute(ctx, request)
		if err != nil {
			return sdk.ActionResult{}, "handler", err
		}
		return result, "", nil
	}
}

func (a *App) commitWorkflowNodeSuccess(ctx context.Context, run db.WorkflowNodeRun, batch db.ExecutionBatch, revision db.WorkflowRevision, node workflowGraphNode, operationKey string, output, state json.RawMessage, startedAt time.Time) error {
	return a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		duration := max(now.Sub(startedAt).Milliseconds(), 0)
		if err := tx.Model(&db.WorkflowNodeRun{}).Where("id = ? AND status = ?", run.ID, BatchStatusRunning).
			Updates(map[string]any{"status": BatchStatusSucceeded, "completed_at": now, "duration_ms": duration}).Error; err != nil {
			return errors.New("finish workflow node run failed")
		}
		checkpoint := db.WorkflowCheckpoint{
			BatchID: batch.ID, WorkflowID: batch.WorkflowID, RevisionID: revision.ID,
			NodeInstanceID: node.NodeInstanceID, OperationKey: operationKey,
			OutputJSON: string(output), ArtifactsJSON: "[]", CreatedAt: now,
		}
		if err := tx.Create(&checkpoint).Error; err != nil {
			return errors.New("create workflow checkpoint failed")
		}
		if len(state) > 0 {
			nodeState := db.WorkflowNodeState{
				WorkflowID: batch.WorkflowID, NodeInstanceID: node.NodeInstanceID, NodeType: node.NodeType,
				RevisionID: revision.ID, StateJSON: string(state), UpdatedAt: now,
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "workflow_id"}, {Name: "node_instance_id"}},
				DoUpdates: clause.Assignments(map[string]any{
					"node_type": node.NodeType, "revision_id": revision.ID, "state_json": string(state), "updated_at": now,
				}),
			}).Create(&nodeState).Error; err != nil {
				return errors.New("save workflow node state failed")
			}
		}
		return nil
	})
}

func (a *App) buildWorkflowBatchGraph(raw string) (workflowBatchGraph, error) {
	validated, err := a.validateWorkflowGraph(json.RawMessage(raw))
	if err != nil {
		return workflowBatchGraph{}, err
	}
	var graph workflowGraph
	if json.Unmarshal([]byte(validated.graphJSON), &graph) != nil {
		return workflowBatchGraph{}, errors.New("decode workflow graph failed")
	}
	incoming := make(map[string][]workflowGraphEdge, len(graph.Nodes))
	adjacency := make(map[string][]string, len(graph.Nodes))
	degrees := make(map[string]int, len(graph.Nodes))
	for _, node := range graph.Nodes {
		incoming[node.NodeInstanceID] = nil
		adjacency[node.NodeInstanceID] = nil
	}
	for _, edge := range graph.Edges {
		incoming[edge.TargetNodeInstanceID] = append(incoming[edge.TargetNodeInstanceID], edge)
		adjacency[edge.SourceNodeInstanceID] = append(adjacency[edge.SourceNodeInstanceID], edge.TargetNodeInstanceID)
		degrees[edge.TargetNodeInstanceID]++
	}
	queue := []string{validated.mainTriggerID}
	order := make([]string, 0, len(graph.Nodes))
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		order = append(order, id)
		for _, target := range adjacency[id] {
			degrees[target]--
			if degrees[target] == 0 {
				queue = append(queue, target)
			}
		}
	}
	return workflowBatchGraph{graph: graph, nodes: validated.nodes, descriptors: validated.descriptors, order: order, incoming: incoming}, nil
}

func workflowNodeReachable(edges []workflowGraphEdge, outputs map[string]map[string]any, event map[string]string) (bool, error) {
	for _, edge := range edges {
		output, completed := outputs[edge.SourceNodeInstanceID]
		if !completed {
			continue
		}
		if strings.TrimSpace(edge.Condition) == "" {
			return true, nil
		}
		value, err := evaluateWorkflowCEL(edge.Condition, event, output)
		if err != nil {
			return false, err
		}
		if condition, ok := value.(bool); ok && condition {
			return true, nil
		}
	}
	return false, nil
}

func resolveWorkflowNodeInput(node workflowGraphNode, outputs map[string]map[string]any, event map[string]string) (map[string]any, error) {
	input := make(map[string]any, len(node.InputBindings))
	celInput := make(map[string]any)
	nodeIDs := make([]string, 0, len(outputs))
	for nodeID := range outputs {
		nodeIDs = append(nodeIDs, nodeID)
	}
	sort.Strings(nodeIDs)
	for _, nodeID := range nodeIDs {
		output := outputs[nodeID]
		for key, value := range output {
			celInput[key] = value
		}
	}
	for field, binding := range node.InputBindings {
		switch binding.Kind {
		case "field":
			value, ok := workflowFieldValue(outputs[binding.NodeInstanceID], binding.FieldPath)
			if !ok {
				return nil, fmt.Errorf("input field %q is unavailable", field)
			}
			input[field] = value
		case "literal":
			var value any
			if json.Unmarshal(binding.Value, &value) != nil {
				return nil, fmt.Errorf("input literal %q is invalid", field)
			}
			input[field] = value
		case "cel":
			value, err := evaluateWorkflowCEL(binding.Expression, event, celInput)
			if err != nil {
				return nil, fmt.Errorf("input CEL %q failed", field)
			}
			input[field] = value
		}
	}
	return input, nil
}

func workflowFieldValue(root map[string]any, path []string) (any, bool) {
	var current any = root
	for _, segment := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[segment]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func evaluateWorkflowCEL(expression string, event map[string]string, input map[string]any) (any, error) {
	ast, err := compileWorkflowCEL(expression)
	if err != nil {
		return nil, err
	}
	env, err := workflowCELEnvironment()
	if err != nil {
		return nil, err
	}
	program, err := env.Program(ast)
	if err != nil {
		return nil, err
	}
	value, _, err := program.Eval(map[string]any{"event": event, "input": input})
	if err != nil {
		return nil, err
	}
	return workflowCELNative(value)
}

func workflowCELNative(value ref.Val) (any, error) {
	native, err := value.ConvertToNative(reflect.TypeOf((*any)(nil)).Elem())
	if err != nil {
		return nil, err
	}
	return native, nil
}

func (a *App) loadWorkflowCheckpoints(ctx context.Context, batchID int64) (map[string]map[string]any, error) {
	var checkpoints []db.WorkflowCheckpoint
	if err := a.DB.WithContext(ctx).Where("batch_id = ?", batchID).Order("id").Find(&checkpoints).Error; err != nil {
		return nil, err
	}
	outputs := make(map[string]map[string]any, len(checkpoints))
	for _, checkpoint := range checkpoints {
		var output map[string]any
		if json.Unmarshal([]byte(checkpoint.OutputJSON), &output) != nil {
			return nil, errors.New("workflow checkpoint output is invalid")
		}
		outputs[checkpoint.NodeInstanceID] = output
	}
	return outputs, nil
}

func (a *App) nextWorkflowNodeAttempt(ctx context.Context, batchID int64, nodeID string) (int, error) {
	var latest int
	err := a.DB.WithContext(ctx).Model(&db.WorkflowNodeRun{}).
		Where("batch_id = ? AND node_instance_id = ?", batchID, nodeID).
		Select("COALESCE(MAX(attempt), 0)").Scan(&latest).Error
	return latest + 1, err
}

func (a *App) finishWorkflowNodeRun(runID int64, status, category string, startedAt time.Time) {
	now := time.Now().UTC()
	duration := max(now.Sub(startedAt).Milliseconds(), 0)
	_ = a.DB.Model(&db.WorkflowNodeRun{}).Where("id = ? AND status = ?", runID, BatchStatusRunning).
		Updates(map[string]any{"status": status, "error_category": category, "completed_at": now, "duration_ms": duration}).Error
}

func (a *App) retryWorkflowBatch(batchID int64, attempt int) {
	now := time.Now().UTC()
	_ = a.DB.Model(&db.ExecutionBatch{}).Where("id = ?", batchID).Updates(map[string]any{
		"status": BatchStatusRetrying, "not_before": now.Add(time.Duration(attempt) * time.Second),
		"lease_token": nil, "lease_expires_at": nil, "updated_at": now,
	}).Error
}

func (a *App) failWorkflowBatch(batchID int64, category string) {
	a.finishWorkflowBatch(batchID, BatchStatusFailed, category)
}

func (a *App) cancelWorkflowBatch(batchID int64) {
	a.finishWorkflowBatch(batchID, BatchStatusCancelled, "cancelled")
}

func (a *App) completeWorkflowBatch(batchID int64) {
	a.finishWorkflowBatch(batchID, BatchStatusSucceeded, "")
}

func (a *App) finishWorkflowBatch(batchID int64, status, category string) {
	now := time.Now().UTC()
	updates := map[string]any{
		"status": status, "completed_at": now, "lease_token": nil, "lease_expires_at": nil,
		"current_node_instance_id": "", "updated_at": now,
	}
	if category == "" {
		updates["error_category"] = nil
	} else {
		updates["error_category"] = category
	}
	_ = a.DB.Model(&db.ExecutionBatch{}).Where("id = ?", batchID).Updates(updates).Error
}

func (a *App) requeueWorkflowBatch(batchID int64) {
	now := time.Now().UTC()
	_ = a.DB.Model(&db.ExecutionBatch{}).Where("id = ?", batchID).Updates(map[string]any{
		"status": BatchStatusQueued, "not_before": now, "lease_token": nil,
		"lease_expires_at": nil, "updated_at": now,
	}).Error
}

func (a *App) batchShouldStop(ctx context.Context, batchID, workflowID int64) (cancelled, paused bool) {
	var row struct {
		Status            string
		CancelRequestedAt *time.Time
	}
	if err := a.DB.Raw(`SELECT w.status, eb.cancel_requested_at FROM workflows w JOIN execution_batches eb ON eb.workflow_id = w.id WHERE eb.id = ? AND w.id = ?`, batchID, workflowID).Scan(&row).Error; err != nil {
		return false, true
	}
	cancelled = row.CancelRequestedAt != nil
	return cancelled, !cancelled && (ctx.Err() != nil || row.Status != WorkflowStatusRunning)
}

func (a *App) renewBatchLease(ctx context.Context, batchID int64, token string, done <-chan struct{}) {
	ticker := time.NewTicker(batchLeaseDuration / 3)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case now := <-ticker.C:
			_ = a.DB.WithContext(ctx).Model(&db.ExecutionBatch{}).
				Where("id = ? AND status = ? AND lease_token = ?", batchID, BatchStatusRunning, token).
				Updates(map[string]any{"lease_expires_at": now.UTC().Add(batchLeaseDuration), "updated_at": now.UTC()}).Error
		}
	}
}

func (a *App) enqueueScheduledBatches(ctx context.Context, now time.Time) error {
	var due []struct {
		WorkflowID int64
		RevisionID int64
		GraphJSON  string
		DueAt      *time.Time
	}
	if err := a.DB.WithContext(ctx).Raw(`
SELECT w.id AS workflow_id, wr.id AS revision_id, wr.graph_json, rt.next_scheduled_at AS due_at
FROM workflows w
JOIN workflow_revisions wr ON wr.id = w.active_revision_id
JOIN workflow_runtimes rt ON rt.workflow_id = w.id
WHERE w.status = 'running' AND rt.next_scheduled_at IS NOT NULL AND rt.next_scheduled_at <= ?
ORDER BY w.id`, now).Scan(&due).Error; err != nil {
		return err
	}
	for _, item := range due {
		graph, err := a.buildWorkflowBatchGraph(item.GraphJSON)
		if err != nil {
			continue
		}
		trigger := graph.nodes[graph.order[0]]
		if trigger.NodeType != "core.schedule" {
			continue
		}
		interval, err := scheduleInterval(trigger.Config)
		if err != nil {
			continue
		}
		dueAt := now
		if item.DueAt != nil {
			dueAt = item.DueAt.UTC()
		}
		if err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var runtime db.WorkflowRuntime
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&runtime, item.WorkflowID).Error; err != nil {
				return err
			}
			if runtime.NextScheduledAt != nil && runtime.NextScheduledAt.After(now) {
				return nil
			}
			if err := enforceWorkflowBacklog(tx, item.WorkflowID); err != nil {
				return nil
			}
			batch := db.ExecutionBatch{
				WorkflowID: item.WorkflowID, RevisionID: item.RevisionID, TriggerType: "schedule",
				TriggerKey: dueAt.Format(time.RFC3339Nano), Status: BatchStatusQueued,
				NotBefore: now, TriggeredAt: dueAt, CreatedAt: now, UpdatedAt: now,
			}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&batch).Error; err != nil {
				return err
			}
			next := dueAt.Add(interval)
			for !next.After(now) {
				next = next.Add(interval)
			}
			return tx.Model(&runtime).Updates(map[string]any{
				"last_scheduled_at": dueAt, "next_scheduled_at": next, "updated_at": now,
			}).Error
		}); err != nil {
			return errors.New("enqueue scheduled workflow batch failed")
		}
	}
	return nil
}

func enforceWorkflowBacklog(tx *gorm.DB, workflowID int64) error {
	var limits struct {
		BacklogLimit int
		Queued       int64
	}
	if err := tx.Raw(`SELECT wr.backlog_limit, (SELECT COUNT(*) FROM execution_batches eb WHERE eb.workflow_id = wr.workflow_id AND eb.status IN ('queued','running','retrying')) AS queued FROM workflow_runtimes wr WHERE wr.workflow_id = ?`, workflowID).Scan(&limits).Error; err != nil {
		return errors.New("read workflow backlog failed")
	}
	if limits.Queued >= int64(limits.BacklogLimit) {
		return fmt.Errorf("%w: workflow backlog limit reached", ErrConflict)
	}
	return nil
}

func scheduleInterval(raw json.RawMessage) (time.Duration, error) {
	var config struct {
		EverySeconds int `json:"everySeconds"`
	}
	if json.Unmarshal(raw, &config) != nil || config.EverySeconds < 60 || config.EverySeconds > 86400 {
		return 0, errors.New("schedule interval is invalid")
	}
	return time.Duration(config.EverySeconds) * time.Second, nil
}

func workflowOperationKey(batchID int64, nodeID string, iteration int) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d:%s:%d", batchID, nodeID, iteration)))
	return hex.EncodeToString(digest[:])
}

func workflowBatchView(batch db.ExecutionBatch) WorkflowBatchView {
	view := WorkflowBatchView{
		ID: batch.ID, WorkflowID: batch.WorkflowID, RevisionID: batch.RevisionID,
		TriggerType: batch.TriggerType, Status: batch.Status,
		CurrentNodeInstanceID: batch.CurrentNodeInstanceID, TriggeredAt: formatWorkflowTime(batch.TriggeredAt),
	}
	if batch.StartedAt != nil {
		view.StartedAt = formatWorkflowTime(*batch.StartedAt)
	}
	if batch.CompletedAt != nil {
		view.CompletedAt = formatWorkflowTime(*batch.CompletedAt)
	}
	if batch.CancelRequestedAt != nil {
		view.CancelRequestedAt = formatWorkflowTime(*batch.CancelRequestedAt)
	}
	if batch.ErrorCategory != nil {
		view.ErrorCategory = *batch.ErrorCategory
	}
	return view
}

func mustJSON(value any) json.RawMessage {
	raw, _ := json.Marshal(value)
	return raw
}

func (s *bufferedNodeState) Load(ctx context.Context) (json.RawMessage, error) {
	var state db.WorkflowNodeState
	if err := s.app.DB.WithContext(ctx).Where("workflow_id = ? AND node_instance_id = ?", s.workflowID, s.node.NodeInstanceID).First(&state).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return json.RawMessage(`{}`), nil
		}
		return nil, errors.New("load workflow node state failed")
	}
	return json.RawMessage(state.StateJSON), nil
}

func (s *bufferedNodeState) Save(_ context.Context, state json.RawMessage) error {
	if s.stateMode != sdk.StatePersistent {
		return errors.New("stateless workflow node cannot save state")
	}
	if len(state) == 0 || len(state) > maxWorkflowGraphBytes || !json.Valid(state) {
		return errors.New("workflow node state must be valid JSON")
	}
	s.pending = append(json.RawMessage(nil), state...)
	return nil
}

type workflowSecretReader struct {
	app            *App
	revisionID     int64
	nodeInstanceID string
}

func (r workflowSecretReader) Read(ctx context.Context, field string) ([]byte, error) {
	if strings.TrimSpace(field) == "" {
		return nil, errors.New("secret field is required")
	}
	var binding db.WorkflowSecretBinding
	if err := r.app.DB.WithContext(ctx).Where("revision_id = ? AND node_instance_id = ? AND field_name = ?", r.revisionID, r.nodeInstanceID, field).First(&binding).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: workflow secret", ErrNotFound)
		}
		return nil, errors.New("load workflow secret failed")
	}
	plain, err := r.app.Cipher.Decrypt(binding.EncryptedValue)
	if err != nil {
		return nil, errors.New("decrypt workflow secret failed")
	}
	return []byte(plain), nil
}

type unavailableArtifactStore struct{}

func (unavailableArtifactStore) Put(context.Context, string, io.Reader) (sdk.Artifact, error) {
	return sdk.Artifact{}, errors.New("workflow artifact storage is unavailable before P1-D")
}

func (unavailableArtifactStore) Open(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("workflow artifact storage is unavailable before P1-D")
}
