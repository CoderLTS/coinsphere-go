package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	RunStatusQueued    = "queued"
	RunStatusRunning   = "running"
	RunStatusWaiting   = "waiting"
	RunStatusRetrying  = "retrying"
	RunStatusSucceeded = "succeeded"
	RunStatusFailed    = "failed"
	RunStatusCancelled = "cancelled"
	runLeaseDuration   = 30 * time.Second
	runPollInterval    = 250 * time.Millisecond
	runMaxAttempts     = 3
)

type WorkflowRunView struct {
	ID                    int64           `json:"id"`
	WorkflowID            int64           `json:"workflowId"`
	RevisionID            int64           `json:"revisionId"`
	EntryPoint            string          `json:"entryPoint"`
	Input                 json.RawMessage `json:"input"`
	EventRecordID         int64           `json:"eventRecordId,omitempty"`
	TriggerType           string          `json:"triggerType"`
	Status                string          `json:"status"`
	CurrentNodeInstanceID string          `json:"currentNodeInstanceId,omitempty"`
	TriggeredAt           string          `json:"triggeredAt"`
	StartedAt             string          `json:"startedAt,omitempty"`
	CompletedAt           string          `json:"completedAt,omitempty"`
	CancelRequestedAt     string          `json:"cancelRequestedAt,omitempty"`
	ErrorCategory         string          `json:"errorCategory,omitempty"`
	ErrorMessage          string          `json:"errorMessage,omitempty"`
	ResultSummary         json.RawMessage `json:"resultSummary"`
	PartitionKey          string          `json:"partitionKey,omitempty"`
	Diagnostic            bool            `json:"diagnostic"`
	OriginalRunID         int64           `json:"originalRunId,omitempty"`
}

type WorkflowRunActionPayload struct {
	Action string `json:"action"`
}

type WorkflowRunCreatePayload struct {
	EntryPoint string          `json:"entryPoint"`
	RevisionID int64           `json:"revisionId"`
	Input      json.RawMessage `json:"input"`
}

type workflowRunGraph struct {
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

func (a *App) CreateWorkflowRun(ctx context.Context, workflowID int64, payload WorkflowRunCreatePayload, principal *Principal) (WorkflowRunView, error) {
	if principal == nil || principal.User == nil || principal.User.ID <= 0 {
		return WorkflowRunView{}, ErrPermission
	}
	now := time.Now().UTC()
	run := db.WorkflowRun{}
	err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var workflow db.Workflow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&workflow, workflowID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: workflow", ErrNotFound)
			}
			return errors.New("lock workflow failed")
		}
		entryPoint := strings.TrimSpace(payload.EntryPoint)
		if entryPoint == "" {
			entryPoint = "realtime"
		}
		if entryPoint != "realtime" && entryPoint != "backtest" {
			return errors.New("workflow entryPoint must be realtime or backtest")
		}
		if entryPoint == "realtime" && (workflow.Status != WorkflowStatusActive || workflow.ActiveRevisionID == nil) {
			return fmt.Errorf("%w: workflow is not accepting runs", ErrConflict)
		}
		revisionID := payload.RevisionID
		if entryPoint == "realtime" {
			revisionID = *workflow.ActiveRevisionID
		} else if revisionID <= 0 {
			return errors.New("backtest revisionId is required")
		}
		var revision db.WorkflowRevision
		if err := tx.Where("workflow_id = ? AND id = ?", workflowID, revisionID).First(&revision).Error; err != nil {
			return errors.New("load workflow revision failed")
		}
		graph, err := a.buildWorkflowRunGraphAt(revision.GraphJSON, entryPoint)
		if err != nil {
			return fmt.Errorf("%w: workflow entryPoint is unavailable", ErrConflict)
		}
		if entryPoint == "realtime" && graph.nodes[graph.order[0]].NodeType != "core.manual" {
			return fmt.Errorf("%w: workflow does not use a manual trigger", ErrConflict)
		}
		if err := enforceWorkflowBacklog(tx, workflowID); err != nil {
			return err
		}
		ownerID := principal.User.ID
		input := payload.Input
		if len(input) == 0 {
			input = json.RawMessage(`{}`)
		}
		var inputObject map[string]any
		if json.Unmarshal(input, &inputObject) != nil || inputObject == nil {
			return errors.New("workflow run input must be a JSON object")
		}
		if entryPoint == "backtest" && validateWorkflowSchemaValue(graph.descriptors[graph.order[0]].InputSchema, inputObject) != nil {
			return errors.New("workflow backtest input does not match its JSON Schema")
		}
		run = db.WorkflowRun{
			WorkflowID: workflowID, RevisionID: revision.ID, EntryPoint: entryPoint, InputJSON: string(input), TriggerType: "manual",
			TriggerKey: security.RandomToken(), Status: RunStatusQueued, NotBefore: now,
			TriggeredAt: now, CreatedBy: &ownerID, ResultSummary: `{}`, CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Create(&run).Error; err != nil {
			return errors.New("create workflow run failed")
		}
		return nil
	})
	if err != nil {
		return WorkflowRunView{}, err
	}
	a.PublishWorkflowRunUpdated(run.WorkflowID, run.ID)
	return workflowRunView(run), nil
}

type WorkflowRunListQuery struct {
	Page        CursorPage
	TriggerType string
	Status      string
	From        *time.Time
	To          *time.Time
	Keyword     string
}

func (a *App) PageWorkflowRuns(ctx context.Context, workflowID int64, query WorkflowRunListQuery) (M, error) {
	var exists int64
	if err := a.DB.WithContext(ctx).Model(&db.Workflow{}).Where("id = ?", workflowID).Count(&exists).Error; err != nil {
		return nil, errors.New("load workflow failed")
	}
	if exists == 0 {
		return nil, fmt.Errorf("%w: workflow", ErrNotFound)
	}
	dbQuery := a.DB.WithContext(ctx).Model(&db.WorkflowRun{}).Where("workflow_id = ?", workflowID)
	if query.TriggerType != "" {
		dbQuery = dbQuery.Where("trigger_type = ?", query.TriggerType)
	}
	if query.Status != "" {
		dbQuery = dbQuery.Where("status = ?", query.Status)
	}
	if query.From != nil {
		dbQuery = dbQuery.Where("triggered_at >= ?", query.From.UTC())
	}
	if query.To != nil {
		dbQuery = dbQuery.Where("triggered_at <= ?", query.To.UTC())
	}
	if keyword := strings.TrimSpace(query.Keyword); keyword != "" {
		pattern := "%" + keyword + "%"
		dbQuery = dbQuery.Where(`(
			trigger_key ILIKE ? OR error_category ILIKE ? OR error_message ILIKE ?
			OR EXISTS (SELECT 1 FROM workflow_node_logs log WHERE log.run_id = workflow_runs.id AND log.message ILIKE ?)
		)`, pattern, pattern, pattern, pattern)
	}
	var total int64
	if err := dbQuery.Count(&total).Error; err != nil {
		return nil, errors.New("count workflow runs failed")
	}
	afterID, err := query.Page.AfterID()
	if err != nil {
		return nil, err
	}
	if afterID > 0 {
		dbQuery = dbQuery.Where("id < ?", afterID)
	}
	var runs []db.WorkflowRun
	if err := dbQuery.Order("id DESC").Limit(query.Page.Limit + 1).Find(&runs).Error; err != nil {
		return nil, errors.New("list workflow runs failed")
	}
	hasMore := len(runs) > query.Page.Limit
	if hasMore {
		runs = runs[:query.Page.Limit]
	}
	items := make([]WorkflowRunView, len(runs))
	for index := range runs {
		items[index] = workflowRunView(runs[index])
	}
	lastKey := ""
	if len(runs) > 0 {
		lastKey = int64CursorKey(runs[len(runs)-1].ID)
	}
	return cursorResult(items, query.Page, lastKey, hasMore, total), nil
}

func (a *App) ListRecentWorkflowRuns(ctx context.Context, workflowID int64) ([]WorkflowRunView, error) {
	var runs []db.WorkflowRun
	if err := a.DB.WithContext(ctx).Where("workflow_id = ?", workflowID).Order("id DESC").Limit(100).Find(&runs).Error; err != nil {
		return nil, errors.New("list workflow runs failed")
	}
	items := make([]WorkflowRunView, len(runs))
	for index := range runs {
		items[index] = workflowRunView(runs[index])
	}
	return items, nil
}

func (a *App) GetWorkflowRun(ctx context.Context, runID int64) (WorkflowRunView, error) {
	var run db.WorkflowRun
	if err := a.DB.WithContext(ctx).First(&run, runID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return WorkflowRunView{}, fmt.Errorf("%w: workflow run", ErrNotFound)
		}
		return WorkflowRunView{}, errors.New("load workflow run failed")
	}
	return workflowRunView(run), nil
}

func (a *App) ApplyWorkflowRunAction(ctx context.Context, runID int64, payload WorkflowRunActionPayload) (WorkflowRunView, error) {
	action := strings.ToLower(strings.TrimSpace(payload.Action))
	if action == "replay" {
		return a.createDiagnosticReplay(ctx, runID)
	}
	now := time.Now().UTC()
	var workflowID int64
	err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run db.WorkflowRun
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&run, runID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: workflow run", ErrNotFound)
			}
			return errors.New("lock workflow run failed")
		}
		workflowID = run.WorkflowID
		switch action {
		case "cancel":
			if run.Status == RunStatusQueued || run.Status == RunStatusWaiting || run.Status == RunStatusRetrying {
				return tx.Model(&run).Updates(map[string]any{
					"status": RunStatusCancelled, "cancel_requested_at": now, "completed_at": now,
					"lease_token": nil, "lease_expires_at": nil, "updated_at": now,
				}).Error
			}
			if run.Status != RunStatusRunning {
				return fmt.Errorf("%w: run is already terminal", ErrConflict)
			}
			if err := tx.Model(&run).Updates(map[string]any{"cancel_requested_at": now, "updated_at": now}).Error; err != nil {
				return errors.New("request workflow run cancellation failed")
			}
		case "retry":
			if run.Status != RunStatusFailed {
				return fmt.Errorf("%w: only a failed run can be retried", ErrConflict)
			}
			if err := tx.Model(&run).Updates(map[string]any{
				"status": RunStatusRetrying, "not_before": now, "completed_at": nil,
				"error_category": nil, "error_message": nil, "cancel_requested_at": nil, "updated_at": now,
			}).Error; err != nil {
				return errors.New("retry workflow run failed")
			}
		default:
			return errors.New("run action must be cancel, retry, or replay")
		}
		return nil
	})
	if err != nil {
		return WorkflowRunView{}, err
	}
	a.PublishWorkflowRunUpdated(workflowID, runID)
	if action == "cancel" {
		a.runCancelMu.Lock()
		cancel := a.runCancels[runID]
		a.runCancelMu.Unlock()
		if cancel != nil {
			cancel()
		}
	}
	return a.GetWorkflowRun(ctx, runID)
}

func (a *App) RunWorkflowEngine(ctx context.Context) error {
	defer a.stopWorkflowTriggers()
	if err := a.recoverExpiredRuns(ctx); err != nil {
		return err
	}
	if err := a.syncWorkflowTriggers(ctx); err != nil {
		return err
	}
	if err := a.cleanupWorkflowHistory(ctx, time.Now().UTC()); err != nil {
		slog.Error("workflow history cleanup failed", "component", "workflow.runtime", "error_category", "history_retention")
	}
	nextCleanup := time.Now().UTC().Add(24 * time.Hour)
	ticker := time.NewTicker(runPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-ticker.C:
			if !now.Before(nextCleanup) {
				if err := a.cleanupWorkflowHistory(ctx, now.UTC()); err != nil {
					slog.Error("workflow history cleanup failed", "component", "workflow.runtime", "error_category", "history_retention")
				}
				nextCleanup = now.UTC().Add(24 * time.Hour)
			}
			if err := a.enqueueScheduledRuns(ctx, now.UTC()); err != nil {
				slog.Error("workflow schedule scan failed", "component", "workflow.runtime", "error_category", "run_schedule")
			}
			if err := a.dispatchWorkflowEventOutbox(ctx, now.UTC()); err != nil {
				slog.Error("workflow event outbox dispatch failed", "component", "workflow.runtime", "error_category", "event_outbox")
			}
			if err := a.expireWorkflowHumanTasks(ctx, now.UTC()); err != nil {
				slog.Error("workflow human task expiration failed", "component", "workflow.runtime", "error_category", "human_task")
			}
			if err := a.syncWorkflowTriggers(ctx); err != nil {
				slog.Error("workflow trigger scan failed", "component", "workflow.runtime", "error_category", "trigger_scan")
			}
			for {
				run, ok, err := a.claimWorkflowRun(ctx, now.UTC())
				if err != nil {
					slog.Error("workflow run claim failed", "component", "workflow.runtime", "error_category", "run_queue")
					break
				}
				if !ok {
					break
				}
				a.runWG.Add(1)
				go func() {
					defer a.runWG.Done()
					a.executeWorkflowRun(ctx, run)
				}()
			}
		}
	}
}

func (a *App) WaitForWorkflowRuns(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		a.runWG.Wait()
		a.triggerWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *App) recoverExpiredRuns(ctx context.Context) error {
	now := time.Now().UTC()
	var recovered int64
	err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`
INSERT INTO workflow_node_logs (workflow_id, run_id, run_node_id, logged_at, level, message, fields_json)
SELECT r.workflow_id, r.id, n.id, ?, 'error', '节点租约已过期，运行将恢复排队', '{"error_category":"lease_expired"}'::jsonb
FROM workflow_run_nodes n
JOIN workflow_runs r ON r.id = n.run_id
WHERE n.status = 'running' AND r.status = 'running' AND r.lease_expires_at < ?`, now, now).Error; err != nil {
			return err
		}
		if err := tx.Exec(`
UPDATE workflow_run_nodes nr
SET status = 'failed', error_category = 'lease_expired', error_message = '节点租约已过期', completed_at = ?,
    duration_ms = GREATEST(EXTRACT(EPOCH FROM (? - nr.started_at)) * 1000, 0)::BIGINT
FROM workflow_runs eb
WHERE nr.run_id = eb.id AND nr.status = 'running'
  AND eb.status = 'running' AND eb.lease_expires_at < ?`, now, now, now).Error; err != nil {
			return err
		}
		result := tx.Model(&db.WorkflowRun{}).
			Where("status = ? AND lease_expires_at < ?", RunStatusRunning, now).
			Updates(map[string]any{
				"status": RunStatusQueued, "lease_token": nil, "lease_expires_at": nil,
				"not_before": now, "updated_at": now,
			})
		recovered = result.RowsAffected
		return result.Error
	})
	if err != nil {
		return errors.New("recover expired workflow runs failed")
	}
	if recovered > 0 {
		slog.Info("workflow runs recovered", "component", "workflow.runtime", "count", recovered)
	}
	return nil
}

func (a *App) claimWorkflowRun(ctx context.Context, now time.Time) (db.WorkflowRun, bool, error) {
	a.runClaimMu.Lock()
	defer a.runClaimMu.Unlock()
	token := security.RandomToken()
	leaseExpiry := now.Add(runLeaseDuration)
	var run db.WorkflowRun
	query := `
WITH candidate AS (
    SELECT eb.id
    FROM workflow_runs eb
    JOIN workflows w ON w.id = eb.workflow_id
    JOIN workflow_runtimes wr ON wr.workflow_id = eb.workflow_id
    WHERE eb.status IN ('queued', 'retrying')
      AND eb.not_before <= ?
      AND w.status = 'active'
      AND (SELECT COUNT(*) FROM workflow_runs active
           WHERE active.workflow_id = eb.workflow_id AND active.status = 'running') < wr.max_concurrent_runs
      AND (eb.partition_key = '' OR NOT EXISTS (
          SELECT 1 FROM workflow_runs prior
          WHERE prior.workflow_id = eb.workflow_id
            AND prior.partition_key = eb.partition_key
            AND prior.status IN ('queued', 'running', 'retrying')
            AND (prior.created_at, prior.id) < (eb.created_at, eb.id)
      ))
    ORDER BY eb.not_before, eb.created_at, eb.id
    FOR UPDATE OF eb SKIP LOCKED
    LIMIT 1
)
UPDATE workflow_runs eb
SET status = 'running', lease_token = ?, lease_expires_at = ?,
    started_at = COALESCE(eb.started_at, ?), updated_at = ?
FROM candidate
WHERE eb.id = candidate.id
RETURNING eb.*`
	err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return tx.Raw(query, now, token, leaseExpiry, now, now).Scan(&run).Error
	})
	if err != nil {
		return db.WorkflowRun{}, false, err
	}
	if run.ID > 0 {
		a.PublishWorkflowRunUpdated(run.WorkflowID, run.ID)
	}
	return run, run.ID > 0, nil
}

func (a *App) executeWorkflowRun(parent context.Context, run db.WorkflowRun) {
	ctx, cancel := context.WithCancel(parent)
	a.runCancelMu.Lock()
	a.runCancels[run.ID] = cancel
	a.runCancelMu.Unlock()
	defer func() {
		cancel()
		a.runCancelMu.Lock()
		delete(a.runCancels, run.ID)
		a.runCancelMu.Unlock()
	}()

	leaseDone := make(chan struct{})
	go a.renewRunLease(ctx, run.ID, *run.LeaseToken, leaseDone)
	defer close(leaseDone)

	var revision db.WorkflowRevision
	if err := a.DB.WithContext(ctx).First(&revision, run.RevisionID).Error; err != nil {
		a.failWorkflowRun(run.ID, "revision")
		return
	}
	graph, err := a.buildWorkflowRunGraphAt(revision.GraphJSON, run.EntryPoint)
	if err != nil {
		a.failWorkflowRun(run.ID, "graph")
		return
	}
	outputs, err := a.loadWorkflowRunCheckpoints(ctx, run.ID)
	if err != nil {
		a.failWorkflowRun(run.ID, "checkpoint")
		return
	}
	event := map[string]string{"type": run.TriggerType, "triggeredAt": formatWorkflowTime(run.TriggeredAt)}
	if run.EventRecordID != nil {
		cloudEvent, eventData, err := a.workflowRunEvent(ctx, run)
		if err != nil {
			a.failWorkflowRun(run.ID, "event")
			return
		}
		event = workflowEventContext(cloudEvent)
		if _, completed := outputs[revision.MainTriggerNodeID]; completed {
			outputs[revision.MainTriggerNodeID] = eventData
		}
	}
	for _, nodeID := range graph.order {
		if _, completed := outputs[nodeID]; completed {
			continue
		}
		if cancelled, paused := a.runShouldStop(ctx, run.ID, run.WorkflowID); cancelled || paused {
			if cancelled {
				a.cancelWorkflowRun(run.ID)
			} else {
				a.requeueWorkflowRun(run.ID)
			}
			return
		}
		node := graph.nodes[nodeID]
		if nodeID != graph.order[0] {
			reachable, err := workflowNodeReachable(graph.incoming[nodeID], outputs, event)
			if err != nil {
				a.failWorkflowRun(run.ID, "condition")
				return
			}
			if !reachable {
				continue
			}
		}
		if !workflowQuantPositionInputReady(node, outputs) {
			continue
		}
		input, err := resolveWorkflowNodeInput(node, graph.incoming[nodeID], outputs, event)
		if err != nil {
			a.failWorkflowRun(run.ID, "input")
			return
		}
		if err := injectWorkflowQuantInput(node, graph.incoming[nodeID], outputs, event, input); err != nil {
			a.failWorkflowRun(run.ID, "input")
			return
		}
		if nodeID == graph.order[0] && run.EntryPoint == "backtest" {
			if json.Unmarshal([]byte(run.InputJSON), &input) != nil || input == nil {
				a.failWorkflowRun(run.ID, "input")
				return
			}
		}
		outcome := a.executeWorkflowNode(ctx, run, revision, graph, node, input, event, 0)
		if outcome.waiting {
			return
		}
		if outcome.err != nil {
			if errors.Is(outcome.err, context.Canceled) || ctx.Err() != nil {
				cancelled, _ := a.runShouldStop(ctx, run.ID, run.WorkflowID)
				if cancelled {
					a.cancelWorkflowRun(run.ID)
				} else {
					a.requeueWorkflowRun(run.ID)
				}
				return
			}
			if outcome.attempt < runMaxAttempts {
				a.retryWorkflowRun(run.ID, outcome.attempt)
			} else {
				a.failWorkflowRun(run.ID, outcome.category)
			}
			return
		}
		outputs[nodeID] = outcome.output
	}
	a.completeWorkflowRun(run.ID)
}

type workflowNodeOutcome struct {
	output   map[string]any
	attempt  int
	category string
	waiting  bool
	err      error
}

func (a *App) executeWorkflowNode(ctx context.Context, run db.WorkflowRun, revision db.WorkflowRevision, graph workflowRunGraph, node workflowGraphNode, input map[string]any, event map[string]string, iteration int) workflowNodeOutcome {
	desc := graph.descriptors[node.NodeInstanceID]
	attempt, err := a.nextWorkflowNodeAttempt(ctx, run.ID, node.NodeInstanceID, iteration)
	if err != nil {
		return workflowNodeOutcome{category: "node_run", err: err}
	}
	operationKey := workflowOperationKey(run.ID, node.NodeInstanceID, iteration)
	startedAt := time.Now().UTC()
	runNode := db.WorkflowRunNode{
		RunID: run.ID, NodeInstanceID: node.NodeInstanceID, NodeType: node.NodeType,
		NodeVersion: node.NodeVersion, ExecutionPool: string(desc.Pool), Attempt: attempt, LoopIteration: iteration,
		OperationKey: operationKey, Status: RunStatusRunning, InputSummary: workflowValueSummary(input),
		OutputSummary: `{}`, StartedAt: startedAt,
	}
	if err := a.DB.WithContext(ctx).Create(&runNode).Error; err != nil {
		return workflowNodeOutcome{attempt: attempt, category: "node_run", err: err}
	}
	a.PublishWorkflowRunUpdated(run.WorkflowID, run.ID)
	if node.NodeType != "core.end" {
		a.appendWorkflowNodeLog(ctx, run.WorkflowID, run.ID, runNode.ID, slog.LevelInfo, "节点开始执行", map[string]any{
			"attempt": attempt, "loop_iteration": iteration,
		})
	}
	if validateWorkflowSchemaValue(desc.InputSchema, input) != nil {
		a.finishWorkflowRunNode(run.WorkflowID, runNode, RunStatusFailed, "input", "节点输入不符合 JSON Schema", startedAt)
		return workflowNodeOutcome{attempt: attempt, category: "input", err: errors.New("node input does not match its JSON Schema")}
	}
	if run.Diagnostic && desc.SideEffect != sdk.SideEffectNone {
		output, artifacts, err := a.replayWorkflowSideEffect(ctx, run, node.NodeInstanceID, iteration)
		if err != nil || validateWorkflowSchemaValue(desc.OutputSchema, output) != nil {
			a.finishWorkflowRunNode(run.WorkflowID, runNode, RunStatusFailed, "diagnostic", "诊断重放缺少可用检查点", startedAt)
			return workflowNodeOutcome{attempt: attempt, category: "diagnostic", err: errors.New("diagnostic side effect checkpoint is unavailable")}
		}
		raw := mustJSON(output)
		if err := a.commitWorkflowNodeSuccess(ctx, runNode, run, revision, node, operationKey, iteration, raw, nil, artifacts, startedAt); err != nil {
			a.finishWorkflowRunNode(run.WorkflowID, runNode, RunStatusFailed, "checkpoint", "保存节点检查点失败", startedAt)
			return workflowNodeOutcome{attempt: attempt, category: "checkpoint", err: err}
		}
		return workflowNodeOutcome{attempt: attempt, output: output}
	}
	if err := a.DB.WithContext(ctx).Model(&db.WorkflowRun{}).Where("id = ?", run.ID).
		Updates(map[string]any{"current_node_instance_id": node.NodeInstanceID, "updated_at": startedAt}).Error; err == nil {
		a.PublishWorkflowRunUpdated(run.WorkflowID, run.ID)
	}

	slot := a.streamSlots
	if desc.Pool == sdk.PoolCompute {
		slot = a.computeSlots
	}
	select {
	case slot <- struct{}{}:
		defer func() { <-slot }()
	case <-ctx.Done():
		a.finishWorkflowRunNode(run.WorkflowID, runNode, RunStatusCancelled, "cancelled", "节点执行已取消", startedAt)
		return workflowNodeOutcome{attempt: attempt, category: "cancelled", err: ctx.Err()}
	}

	state := &bufferedNodeState{app: a, workflowID: run.WorkflowID, revisionID: revision.ID, node: node, stateMode: desc.State}
	request := sdk.ActionRequest{
		Revision:       sdk.RevisionRef{WorkflowID: fmt.Sprint(run.WorkflowID), RevisionID: fmt.Sprint(revision.ID)},
		NodeInstanceID: node.NodeInstanceID, OperationKey: operationKey,
		Input: mustJSON(input), Config: append(json.RawMessage(nil), node.Config...),
		Secrets: workflowSecretReader{app: a, revisionID: revision.ID, nodeInstanceID: node.NodeInstanceID},
		State:   state, Artifacts: workflowArtifactStore{app: a}, ExecutionMode: sdk.ExecutionModeWorkflow,
		Logger: a.workflowNodeLogger(run.WorkflowID, run.ID, runNode.ID, node.NodeType),
	}
	if node.NodeType == "official.quant.backtest_start" {
		request.Frames = workflowFrameExecutor{
			app: a, run: run, revision: revision, graph: graph, sourceNodeID: node.NodeInstanceID,
			frameNodeIDs: workflowBacktestFrameNodeIDs(graph, node.NodeInstanceID),
		}
	}
	result, category, executeErr := a.callWorkflowNode(ctx, run, revision, node, request, event)
	if errors.Is(executeErr, errWorkflowWaiting) {
		a.finishWorkflowRunNode(run.WorkflowID, runNode, RunStatusWaiting, "", "节点等待人工决定", startedAt)
		a.waitWorkflowRun(run.ID)
		return workflowNodeOutcome{attempt: attempt, waiting: true}
	}
	var output map[string]any
	if executeErr == nil {
		if len(result.Output) == 0 {
			result.Output = json.RawMessage(`{}`)
		}
		if json.Unmarshal(result.Output, &output) != nil || output == nil || validateWorkflowSchemaValue(desc.OutputSchema, output) != nil {
			category, executeErr = "output", errors.New("node output does not match its JSON Schema")
		} else if branch, _ := output["branch"].(string); len(desc.Branches) > 0 && !containsString(desc.Branches, branch) {
			category, executeErr = "output", errors.New("node output does not match a declared branch")
		}
	}
	if executeErr != nil {
		status := RunStatusFailed
		if errors.Is(executeErr, context.Canceled) || ctx.Err() != nil {
			status, category = RunStatusCancelled, "cancelled"
		}
		a.finishWorkflowRunNode(run.WorkflowID, runNode, status, category, workflowErrorMessage(executeErr), startedAt)
		return workflowNodeOutcome{attempt: attempt, category: category, err: executeErr}
	}
	if err := a.commitWorkflowNodeSuccess(ctx, runNode, run, revision, node, operationKey, iteration, result.Output, state.pending, result.Artifacts, startedAt); err != nil {
		a.finishWorkflowRunNode(run.WorkflowID, runNode, RunStatusFailed, "checkpoint", "保存节点检查点失败", startedAt)
		return workflowNodeOutcome{attempt: attempt, category: "checkpoint", err: err}
	}
	return workflowNodeOutcome{attempt: attempt, output: output}
}

func (a *App) callWorkflowNode(ctx context.Context, run db.WorkflowRun, revision db.WorkflowRevision, node workflowGraphNode, request sdk.ActionRequest, event map[string]string) (sdk.ActionResult, string, error) {
	switch node.NodeType {
	case "core.manual", "core.schedule":
		return sdk.ActionResult{Output: mustJSON(map[string]any{"triggeredAt": formatWorkflowTime(run.TriggeredAt)})}, "", nil
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
	case "core.loop":
		result, err := a.executeWorkflowLoop(ctx, run, revision, node, request.Input, event)
		return result, "loop", err
	case "core.loop_item":
		return sdk.ActionResult{Output: append(json.RawMessage(nil), request.Input...)}, "", nil
	case "core.loop_end":
		var input struct {
			Value map[string]any `json:"value"`
		}
		if json.Unmarshal(request.Input, &input) != nil {
			return sdk.ActionResult{}, "input", errors.New("loop end input is invalid")
		}
		if input.Value == nil {
			input.Value = map[string]any{}
		}
		return sdk.ActionResult{Output: mustJSON(map[string]any{"value": input.Value})}, "", nil
	case "core.human_approval":
		result, err := a.workflowHumanApproval(ctx, run, node, request.Input)
		return result, "human_task", err
	default:
		if run.EventRecordID != nil {
			if _, _, ok := a.Plugins.Trigger(node.NodeType); ok || node.NodeType == "core.event" {
				_, data, err := a.workflowRunEvent(ctx, run)
				if err != nil {
					return sdk.ActionResult{}, "event", err
				}
				return sdk.ActionResult{Output: mustJSON(data)}, "", nil
			}
		}
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

func (a *App) commitWorkflowNodeSuccess(ctx context.Context, runNode db.WorkflowRunNode, run db.WorkflowRun, revision db.WorkflowRevision, node workflowGraphNode, operationKey string, iteration int, output, state json.RawMessage, artifacts []sdk.Artifact, startedAt time.Time) error {
	err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		duration := max(now.Sub(startedAt).Milliseconds(), 0)
		manifests, err := loadWorkflowArtifactManifests(tx, artifacts)
		if err != nil {
			return err
		}
		artifactsJSON, err := marshalWorkflowArtifactManifests(manifests)
		if err != nil {
			return errors.New("encode workflow artifact manifest failed")
		}
		checkpointOutput := output
		outputSummary := workflowJSONSummary(output)
		if run.EventRecordID != nil && a.workflowEventTriggerNode(node.NodeType) {
			checkpointOutput = json.RawMessage(`{}`)
			outputSummary = mustJSONString(map[string]any{"eventRecordId": *run.EventRecordID})
		}
		if err := tx.Model(&db.WorkflowRunNode{}).Where("id = ? AND status = ?", runNode.ID, RunStatusRunning).
			Updates(map[string]any{"status": RunStatusSucceeded, "output_summary": outputSummary, "completed_at": now, "duration_ms": duration}).Error; err != nil {
			return errors.New("finish workflow node run failed")
		}
		checkpoint := db.WorkflowRunCheckpoint{
			RunID: run.ID, RunNodeID: runNode.ID, WorkflowID: run.WorkflowID, RevisionID: revision.ID,
			NodeInstanceID: node.NodeInstanceID, LoopIteration: iteration, OperationKey: operationKey,
			Status: RunStatusSucceeded, OutputJSON: string(checkpointOutput), ArtifactsJSON: artifactsJSON, CreatedAt: now,
		}
		if err := tx.Create(&checkpoint).Error; err != nil {
			return errors.New("create workflow checkpoint failed")
		}
		if err := createWorkflowArtifactRefs(tx, runNode.ID, manifests); err != nil {
			return err
		}
		if node.NodeType != "core.end" {
			if err := appendWorkflowNodeLog(tx, db.WorkflowNodeLog{
				WorkflowID: run.WorkflowID, RunID: run.ID, RunNodeID: runNode.ID, LoggedAt: now,
				Level: "info", Message: "节点执行成功", FieldsJSON: workflowLogFields(map[string]any{"duration_ms": duration}),
			}); err != nil {
				return err
			}
		}
		if len(state) > 0 {
			nodeState := db.WorkflowNodeState{
				WorkflowID: run.WorkflowID, NodeInstanceID: node.NodeInstanceID, NodeType: node.NodeType,
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
	if err == nil {
		a.PublishWorkflowRunUpdated(run.WorkflowID, run.ID)
	}
	return err
}

func (a *App) workflowEventTriggerNode(nodeType string) bool {
	if nodeType == "core.event" {
		return true
	}
	_, _, ok := a.Plugins.Trigger(nodeType)
	return ok
}

func mustJSONString(value any) string { return string(mustJSON(value)) }

func (a *App) buildWorkflowRunGraph(raw string) (workflowRunGraph, error) {
	return a.buildWorkflowRunGraphAt(raw, "realtime")
}

func (a *App) buildWorkflowRunGraphAt(raw, entryPoint string) (workflowRunGraph, error) {
	validated, err := a.validateWorkflowGraph(json.RawMessage(raw))
	if err != nil {
		return workflowRunGraph{}, err
	}
	var graph workflowGraph
	if json.Unmarshal([]byte(validated.graphJSON), &graph) != nil {
		return workflowRunGraph{}, errors.New("decode workflow graph failed")
	}
	startID := validated.entryPoints[entryPoint]
	if startID == "" {
		return workflowRunGraph{}, errors.New("workflow entryPoint is unavailable")
	}
	return buildWorkflowRunGraph(graph, validated.nodes, validated.descriptors, startID), nil
}

func buildWorkflowRunGraph(graph workflowGraph, nodes map[string]workflowGraphNode, descriptors map[string]sdk.NodeDescriptor, startID string) workflowRunGraph {
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
	}
	reachable := map[string]bool{}
	queue := []string{startID}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if reachable[id] {
			continue
		}
		reachable[id] = true
		queue = append(queue, adjacency[id]...)
	}
	for _, edge := range graph.Edges {
		if reachable[edge.SourceNodeInstanceID] && reachable[edge.TargetNodeInstanceID] {
			degrees[edge.TargetNodeInstanceID]++
		}
	}
	queue = []string{startID}
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
	return workflowRunGraph{graph: graph, nodes: nodes, descriptors: descriptors, order: order, incoming: incoming}
}

func (a *App) buildWorkflowLoopGraph(node workflowGraphNode) (workflowRunGraph, workflowLoopConfig, string, string, error) {
	loop, err := validateWorkflowLoop(node, a.workflowNodeDescriptors())
	if err != nil {
		return workflowRunGraph{}, workflowLoopConfig{}, "", "", err
	}
	mapping := make(map[string]string, len(loop.nodes))
	for bodyID := range loop.nodes {
		mapping[bodyID], err = workflowLoopNodeID(node.NodeInstanceID, bodyID)
		if err != nil {
			return workflowRunGraph{}, workflowLoopConfig{}, "", "", err
		}
	}
	graph := workflowGraph{SchemaVersion: 1, Nodes: make([]workflowGraphNode, 0, len(loop.config.Body.Nodes)), Edges: make([]workflowGraphEdge, 0, len(loop.config.Body.Edges))}
	nodes := make(map[string]workflowGraphNode, len(loop.nodes))
	descriptors := make(map[string]sdk.NodeDescriptor, len(loop.descriptors))
	for _, bodyNode := range loop.config.Body.Nodes {
		runtimeNode := bodyNode
		runtimeNode.NodeInstanceID = mapping[bodyNode.NodeInstanceID]
		runtimeNode.InputBindings = make(map[string]workflowInputBinding, len(bodyNode.InputBindings))
		for field, binding := range bodyNode.InputBindings {
			if binding.NodeInstanceID != "" {
				binding.NodeInstanceID = mapping[binding.NodeInstanceID]
			}
			for index := range binding.Sources {
				binding.Sources[index].NodeInstanceID = mapping[binding.Sources[index].NodeInstanceID]
			}
			runtimeNode.InputBindings[field] = binding
		}
		graph.Nodes = append(graph.Nodes, runtimeNode)
		nodes[runtimeNode.NodeInstanceID] = runtimeNode
		descriptors[runtimeNode.NodeInstanceID] = loop.descriptors[bodyNode.NodeInstanceID]
	}
	for _, bodyEdge := range loop.config.Body.Edges {
		runtimeEdge := bodyEdge
		runtimeEdge.SourceNodeInstanceID = mapping[bodyEdge.SourceNodeInstanceID]
		runtimeEdge.TargetNodeInstanceID = mapping[bodyEdge.TargetNodeInstanceID]
		graph.Edges = append(graph.Edges, runtimeEdge)
	}
	itemID, endID := mapping[loop.itemID], mapping[loop.endID]
	return buildWorkflowRunGraph(graph, nodes, descriptors, itemID), loop.config, itemID, endID, nil
}

func (a *App) executeWorkflowLoop(ctx context.Context, run db.WorkflowRun, revision db.WorkflowRevision, node workflowGraphNode, rawInput json.RawMessage, event map[string]string) (sdk.ActionResult, error) {
	graph, config, itemID, endID, err := a.buildWorkflowLoopGraph(node)
	if err != nil {
		return sdk.ActionResult{}, err
	}
	var input struct {
		Value map[string]any `json:"value"`
	}
	if json.Unmarshal(rawInput, &input) != nil {
		return sdk.ActionResult{}, errors.New("loop input is invalid")
	}
	if input.Value == nil {
		input.Value = map[string]any{}
	}
	startedAt, err := a.workflowLoopStartedAt(ctx, run.ID, node.NodeInstanceID)
	if err != nil {
		return sdk.ActionResult{}, err
	}
	loopCtx, cancel := context.WithDeadline(ctx, startedAt.Add(time.Duration(config.TimeoutSeconds)*time.Second))
	defer cancel()

	carried := input.Value
	for iteration := 1; iteration <= config.MaxIterations; iteration++ {
		if err := workflowLoopContextError(ctx, loopCtx); err != nil {
			return sdk.ActionResult{}, err
		}
		outputs, err := a.loadWorkflowIterationCheckpoints(loopCtx, run.ID, iteration, graph.order)
		if err != nil {
			return sdk.ActionResult{}, err
		}
		for _, nodeID := range graph.order {
			if _, completed := outputs[nodeID]; completed {
				continue
			}
			if cancelled, paused := a.runShouldStop(loopCtx, run.ID, run.WorkflowID); cancelled || paused {
				return sdk.ActionResult{}, context.Canceled
			}
			bodyNode := graph.nodes[nodeID]
			if nodeID != itemID {
				reachable, err := workflowNodeReachable(graph.incoming[nodeID], outputs, event)
				if err != nil {
					return sdk.ActionResult{}, err
				}
				if !reachable {
					continue
				}
			}
			bodyInput := map[string]any{"iteration": iteration, "value": carried}
			if nodeID != itemID {
				bodyInput, err = resolveWorkflowNodeInput(bodyNode, graph.incoming[nodeID], outputs, event)
				if err != nil {
					return sdk.ActionResult{}, err
				}
			}
			outcome := a.executeWorkflowNode(loopCtx, run, revision, graph, bodyNode, bodyInput, event, iteration)
			if outcome.waiting {
				return sdk.ActionResult{}, errors.New("loop body cannot enter a durable wait")
			}
			if outcome.err != nil {
				if err := workflowLoopContextError(ctx, loopCtx); err != nil {
					return sdk.ActionResult{}, err
				}
				return sdk.ActionResult{}, outcome.err
			}
			outputs[nodeID] = outcome.output
		}
		end, ok := outputs[endID]
		if !ok {
			return sdk.ActionResult{}, errors.New("loop body did not reach core.loop_end")
		}
		carried, _ = end["value"].(map[string]any)
		if carried == nil {
			carried = map[string]any{}
		}
		conditionInput := flattenWorkflowOutputs(outputs)
		conditionInput["iteration"] = iteration
		conditionInput["value"] = carried
		value, err := evaluateWorkflowCEL(config.ExitCondition, event, conditionInput)
		if err != nil {
			return sdk.ActionResult{}, err
		}
		exited, ok := value.(bool)
		if !ok {
			return sdk.ActionResult{}, errors.New("loop exitCondition did not return Boolean")
		}
		if exited || iteration == config.MaxIterations {
			return sdk.ActionResult{Output: mustJSON(map[string]any{
				"iterations": iteration, "exited": exited, "value": carried,
			})}, nil
		}
	}
	return sdk.ActionResult{}, errors.New("loop did not produce a result")
}

func (a *App) workflowLoopStartedAt(ctx context.Context, runID int64, nodeID string) (time.Time, error) {
	var startedAt time.Time
	err := a.DB.WithContext(ctx).Model(&db.WorkflowRunNode{}).
		Where("run_id = ? AND node_instance_id = ? AND loop_iteration = 0", runID, nodeID).
		Select("MIN(started_at)").Scan(&startedAt).Error
	if err != nil || startedAt.IsZero() {
		return time.Time{}, errors.New("load workflow loop deadline failed")
	}
	return startedAt.UTC(), nil
}

func workflowLoopContextError(parent, loop context.Context) error {
	if parent.Err() != nil {
		return parent.Err()
	}
	if errors.Is(loop.Err(), context.DeadlineExceeded) {
		return errors.New("workflow loop absolute timeout exceeded")
	}
	return loop.Err()
}

func workflowNodeReachable(edges []workflowGraphEdge, outputs map[string]map[string]any, event map[string]string) (bool, error) {
	for _, edge := range edges {
		reached, err := workflowEdgeReached(edge, outputs, event)
		if err != nil {
			return false, err
		}
		if reached {
			return true, nil
		}
	}
	return false, nil
}

func workflowEdgeReached(edge workflowGraphEdge, outputs map[string]map[string]any, event map[string]string) (bool, error) {
	output, completed := outputs[edge.SourceNodeInstanceID]
	if !completed {
		return false, nil
	}
	if ready, declared := output["ready"].(bool); declared && !ready {
		return false, nil
	}
	if edge.SourcePort != "out" {
		branch, _ := output["branch"].(string)
		if branch != edge.SourcePort {
			return false, nil
		}
	}
	if strings.TrimSpace(edge.Condition) == "" {
		return true, nil
	}
	value, err := evaluateWorkflowCEL(edge.Condition, event, output)
	if err != nil {
		return false, err
	}
	condition, _ := value.(bool)
	return condition, nil
}

func resolveWorkflowNodeInput(node workflowGraphNode, incoming []workflowGraphEdge, outputs map[string]map[string]any, event map[string]string) (map[string]any, error) {
	input := make(map[string]any, len(node.InputBindings))
	celInput := flattenWorkflowOutputs(outputs)
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
		case "condition_entry":
			value, err := workflowConditionPathEntered(binding.Sources, incoming, outputs, event)
			if err != nil {
				return nil, fmt.Errorf("input condition path %q failed", field)
			}
			input[field] = value
		case "condition_subject", "condition_message":
			value, err := workflowConditionNotificationValue(binding.Kind, binding.Sources, incoming, outputs, event)
			if err != nil {
				return nil, fmt.Errorf("input condition notification %q failed", field)
			}
			input[field] = value
		}
	}
	return input, nil
}

func workflowConditionPathEntered(sources []workflowInputBindingSource, incoming []workflowGraphEdge, outputs map[string]map[string]any, event map[string]string) (bool, error) {
	for _, source := range sources {
		output := outputs[source.NodeInstanceID]
		entered, _ := output["entered"].(bool)
		if !entered {
			continue
		}
		for _, edge := range incoming {
			if edge.SourceNodeInstanceID != source.NodeInstanceID || edge.SourcePort != source.Branch {
				continue
			}
			reached, err := workflowEdgeReached(edge, outputs, event)
			if err != nil {
				return false, err
			}
			if reached {
				return true, nil
			}
		}
	}
	return false, nil
}

func workflowConditionNotificationValue(kind string, sources []workflowInputBindingSource, incoming []workflowGraphEdge, outputs map[string]map[string]any, event map[string]string) (string, error) {
	values := make([]string, 0, len(sources))
	for _, source := range sources {
		output := outputs[source.NodeInstanceID]
		triggered, _ := output["triggered"].(bool)
		if !triggered {
			continue
		}
		reached := false
		for _, edge := range incoming {
			if edge.SourceNodeInstanceID != source.NodeInstanceID || edge.SourcePort != source.Branch {
				continue
			}
			var err error
			reached, err = workflowEdgeReached(edge, outputs, event)
			if err != nil {
				return "", err
			}
			if reached {
				break
			}
		}
		if !reached {
			continue
		}
		field := "summary"
		if kind == "condition_subject" {
			field = "businessKey"
		}
		if value, _ := output[field].(string); value != "" {
			values = append(values, value)
		}
	}
	if kind == "condition_subject" {
		digest := sha256.Sum256([]byte(strings.Join(values, "\x00")))
		return "quant-condition:" + hex.EncodeToString(digest[:16]), nil
	}
	return truncateWorkflowText(strings.Join(values, "\n"), 2000), nil
}

func flattenWorkflowOutputs(outputs map[string]map[string]any) map[string]any {
	flattened := make(map[string]any)
	nodeIDs := make([]string, 0, len(outputs))
	for nodeID := range outputs {
		nodeIDs = append(nodeIDs, nodeID)
	}
	sort.Strings(nodeIDs)
	for _, nodeID := range nodeIDs {
		output := outputs[nodeID]
		for key, value := range output {
			flattened[key] = value
		}
	}
	return flattened
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

func (a *App) loadWorkflowRunCheckpoints(ctx context.Context, runID int64) (map[string]map[string]any, error) {
	var checkpoints []db.WorkflowRunCheckpoint
	if err := a.DB.WithContext(ctx).Where("run_id = ? AND loop_iteration = 0", runID).Order("id").Find(&checkpoints).Error; err != nil {
		return nil, err
	}
	return decodeWorkflowRunCheckpointOutputs(checkpoints)
}

func (a *App) loadWorkflowIterationCheckpoints(ctx context.Context, runID int64, iteration int, nodeIDs []string) (map[string]map[string]any, error) {
	var checkpoints []db.WorkflowRunCheckpoint
	if err := a.DB.WithContext(ctx).Where(
		"run_id = ? AND loop_iteration = ? AND node_instance_id IN ?", runID, iteration, nodeIDs,
	).Order("id").Find(&checkpoints).Error; err != nil {
		return nil, err
	}
	return decodeWorkflowRunCheckpointOutputs(checkpoints)
}

func decodeWorkflowRunCheckpointOutputs(checkpoints []db.WorkflowRunCheckpoint) (map[string]map[string]any, error) {
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

func (a *App) nextWorkflowNodeAttempt(ctx context.Context, runID int64, nodeID string, iteration int) (int, error) {
	var latest int
	err := a.DB.WithContext(ctx).Model(&db.WorkflowRunNode{}).
		Where("run_id = ? AND node_instance_id = ? AND loop_iteration = ?", runID, nodeID, iteration).
		Select("COALESCE(MAX(attempt), 0)").Scan(&latest).Error
	return latest + 1, err
}

func (a *App) finishWorkflowRunNode(workflowID int64, runNode db.WorkflowRunNode, status, category, message string, startedAt time.Time) {
	now := time.Now().UTC()
	duration := max(now.Sub(startedAt).Milliseconds(), 0)
	updates := map[string]any{"status": status, "completed_at": now, "duration_ms": duration}
	if category == "" {
		updates["error_category"] = nil
	} else {
		updates["error_category"] = category
	}
	if message == "" {
		updates["error_message"] = nil
	} else {
		updates["error_message"] = workflowLogMessage(message)
	}
	if err := a.DB.Model(&db.WorkflowRunNode{}).Where("id = ? AND status = ?", runNode.ID, RunStatusRunning).Updates(updates).Error; err != nil {
		return
	}
	a.PublishWorkflowRunUpdated(workflowID, runNode.RunID)
	level := slog.LevelInfo
	if status == RunStatusFailed {
		level = slog.LevelError
	} else if status == RunStatusCancelled {
		level = slog.LevelWarn
	}
	a.appendWorkflowNodeLog(context.Background(), workflowID, runNode.RunID, runNode.ID, level, message, map[string]any{
		"status": status, "error_category": category, "duration_ms": duration,
	})
}

func (a *App) retryWorkflowRun(runID int64, attempt int) {
	now := time.Now().UTC()
	var run db.WorkflowRun
	if a.DB.Model(&db.WorkflowRun{}).Select("workflow_id").First(&run, runID).Error != nil {
		return
	}
	if a.DB.Model(&db.WorkflowRun{}).Where("id = ?", runID).Updates(map[string]any{
		"status": RunStatusRetrying, "not_before": now.Add(time.Duration(attempt) * time.Second),
		"lease_token": nil, "lease_expires_at": nil, "error_category": nil, "error_message": nil, "updated_at": now,
	}).Error == nil {
		a.PublishWorkflowRunUpdated(run.WorkflowID, runID)
	}
}

func (a *App) failWorkflowRun(runID int64, category string) {
	a.finishWorkflowRun(runID, RunStatusFailed, category)
}

func (a *App) cancelWorkflowRun(runID int64) {
	a.finishWorkflowRun(runID, RunStatusCancelled, "cancelled")
}

func (a *App) completeWorkflowRun(runID int64) {
	a.finishWorkflowRun(runID, RunStatusSucceeded, "")
}

func (a *App) finishWorkflowRun(runID int64, status, category string) {
	now := time.Now().UTC()
	var workflowID int64
	updates := map[string]any{
		"status": status, "completed_at": now, "lease_token": nil, "lease_expires_at": nil,
		"current_node_instance_id": "", "updated_at": now,
	}
	if category == "" {
		updates["error_category"] = nil
	} else {
		updates["error_category"] = category
	}
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		var run db.WorkflowRun
		if err := tx.First(&run, runID).Error; err != nil {
			return err
		}
		workflowID = run.WorkflowID
		updates["result_summary"] = workflowRunResultSummary(tx, runID, run.EntryPoint)
		if category == "" {
			updates["error_message"] = nil
		} else {
			updates["error_message"] = workflowLogMessage("工作流运行失败: " + category)
		}
		if err := tx.Model(&run).Updates(updates).Error; err != nil {
			return err
		}
		if status == RunStatusFailed && run.TriggerType != "failure" {
			return a.enqueueWorkflowEvent(tx, newWorkflowFailureEvent(run, category, now))
		}
		return nil
	})
	if err != nil {
		slog.Error("finish workflow run failed", "component", "workflow.runtime", "run_id", runID, "error_category", "run_finish")
		return
	}
	a.PublishWorkflowRunUpdated(workflowID, runID)
}

func workflowRunResultSummary(tx *gorm.DB, runID int64, entryPoint string) string {
	var attempts int64
	_ = tx.Model(&db.WorkflowRunNode{}).Where("run_id = ?", runID).Count(&attempts).Error
	summary := map[string]any{"nodeAttempts": attempts}
	var last db.WorkflowRunNode
	query := tx.Where("run_id = ?", runID)
	if entryPoint == "backtest" {
		query = query.Where("node_type = ?", "official.quant.backtest_start")
	}
	if err := query.Order("id DESC").First(&last).Error; err == nil {
		summary["lastNodeInstanceId"] = last.NodeInstanceID
		summary["lastNodeStatus"] = last.Status
		var output map[string]any
		if json.Unmarshal([]byte(last.OutputSummary), &output) == nil && len(output) > 0 {
			summary["output"] = output
		}
	}
	return mustJSONString(summary)
}

func (a *App) requeueWorkflowRun(runID int64) {
	now := time.Now().UTC()
	var run db.WorkflowRun
	if a.DB.Model(&db.WorkflowRun{}).Select("workflow_id").First(&run, runID).Error != nil {
		return
	}
	if a.DB.Model(&db.WorkflowRun{}).Where("id = ?", runID).Updates(map[string]any{
		"status": RunStatusQueued, "not_before": now, "lease_token": nil,
		"lease_expires_at": nil, "updated_at": now,
	}).Error == nil {
		a.PublishWorkflowRunUpdated(run.WorkflowID, runID)
	}
}

func (a *App) waitWorkflowRun(runID int64) {
	now := time.Now().UTC()
	var run db.WorkflowRun
	if a.DB.Model(&db.WorkflowRun{}).Select("workflow_id").First(&run, runID).Error != nil {
		return
	}
	if a.DB.Model(&db.WorkflowRun{}).Where("id = ? AND status = ?", runID, RunStatusRunning).Updates(map[string]any{
		"status": RunStatusWaiting, "lease_token": nil, "lease_expires_at": nil, "updated_at": now,
	}).Error == nil {
		a.PublishWorkflowRunUpdated(run.WorkflowID, runID)
	}
}

func (a *App) runShouldStop(ctx context.Context, runID, workflowID int64) (cancelled, paused bool) {
	var row struct {
		Status            string
		CancelRequestedAt *time.Time
	}
	if err := a.DB.Raw(`SELECT w.status, r.cancel_requested_at FROM workflows w JOIN workflow_runs r ON r.workflow_id = w.id WHERE r.id = ? AND w.id = ?`, runID, workflowID).Scan(&row).Error; err != nil {
		return false, true
	}
	cancelled = row.CancelRequestedAt != nil
	return cancelled, !cancelled && (ctx.Err() != nil || row.Status != WorkflowStatusActive)
}

func (a *App) renewRunLease(ctx context.Context, runID int64, token string, done <-chan struct{}) {
	ticker := time.NewTicker(runLeaseDuration / 3)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case now := <-ticker.C:
			_ = a.DB.WithContext(ctx).Model(&db.WorkflowRun{}).
				Where("id = ? AND status = ? AND lease_token = ?", runID, RunStatusRunning, token).
				Updates(map[string]any{"lease_expires_at": now.UTC().Add(runLeaseDuration), "updated_at": now.UTC()}).Error
		}
	}
}

func (a *App) enqueueScheduledRuns(ctx context.Context, now time.Time) error {
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
WHERE w.status = 'active' AND rt.next_scheduled_at IS NOT NULL AND rt.next_scheduled_at <= ?
ORDER BY w.id`, now).Scan(&due).Error; err != nil {
		return err
	}
	for _, item := range due {
		graph, err := a.buildWorkflowRunGraph(item.GraphJSON)
		if err != nil {
			continue
		}
		trigger := graph.nodes[graph.order[0]]
		if trigger.NodeType != "core.schedule" {
			continue
		}
		dueAt := now
		if item.DueAt != nil {
			dueAt = item.DueAt.UTC()
		}
		var createdRun db.WorkflowRun
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
			run := db.WorkflowRun{
				WorkflowID: item.WorkflowID, RevisionID: item.RevisionID, EntryPoint: "realtime", InputJSON: `{}`, TriggerType: "schedule",
				TriggerKey: dueAt.Format(time.RFC3339Nano), Status: RunStatusQueued,
				NotBefore: now, TriggeredAt: dueAt, ResultSummary: `{}`, CreatedAt: now, UpdatedAt: now,
			}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&run).Error; err != nil {
				return err
			}
			createdRun = run
			next, err := nextWorkflowScheduledAt(trigger.Config, dueAt)
			if err != nil {
				return err
			}
			for !next.After(now) {
				next, err = nextWorkflowScheduledAt(trigger.Config, next)
				if err != nil {
					return err
				}
			}
			return tx.Model(&runtime).Updates(map[string]any{
				"last_scheduled_at": dueAt, "next_scheduled_at": next, "updated_at": now,
			}).Error
		}); err != nil {
			return errors.New("enqueue scheduled workflow run failed")
		}
		if createdRun.ID > 0 {
			a.PublishWorkflowRunUpdated(createdRun.WorkflowID, createdRun.ID)
		}
	}
	return nil
}

func enforceWorkflowBacklog(tx *gorm.DB, workflowID int64) error {
	var limits struct {
		BacklogLimit int
		Queued       int64
	}
	if err := tx.Raw(`SELECT wr.backlog_limit, (SELECT COUNT(*) FROM workflow_runs r WHERE r.workflow_id = wr.workflow_id AND r.status IN ('queued','running','waiting','retrying')) AS queued FROM workflow_runtimes wr WHERE wr.workflow_id = ?`, workflowID).Scan(&limits).Error; err != nil {
		return errors.New("read workflow backlog failed")
	}
	if limits.Queued >= int64(limits.BacklogLimit) {
		return fmt.Errorf("%w: %w", ErrConflict, errWorkflowBackpressure)
	}
	return nil
}

func workflowOperationKey(runID int64, nodeID string, iteration int) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d:%s:%d", runID, nodeID, iteration)))
	return hex.EncodeToString(digest[:])
}

func workflowRunView(run db.WorkflowRun) WorkflowRunView {
	view := WorkflowRunView{
		ID: run.ID, WorkflowID: run.WorkflowID, RevisionID: run.RevisionID,
		EntryPoint: run.EntryPoint, Input: json.RawMessage(run.InputJSON), TriggerType: run.TriggerType, Status: run.Status,
		CurrentNodeInstanceID: run.CurrentNodeInstanceID, TriggeredAt: formatWorkflowTime(run.TriggeredAt),
		PartitionKey: run.PartitionKey, Diagnostic: run.Diagnostic, ResultSummary: json.RawMessage(run.ResultSummary),
	}
	if len(view.ResultSummary) == 0 {
		view.ResultSummary = json.RawMessage(`{}`)
	}
	if len(view.Input) == 0 {
		view.Input = json.RawMessage(`{}`)
	}
	if run.EventRecordID != nil {
		view.EventRecordID = *run.EventRecordID
	}
	if run.OriginalRunID != nil {
		view.OriginalRunID = *run.OriginalRunID
	}
	if run.StartedAt != nil {
		view.StartedAt = formatWorkflowTime(*run.StartedAt)
	}
	if run.CompletedAt != nil {
		view.CompletedAt = formatWorkflowTime(*run.CompletedAt)
	}
	if run.CancelRequestedAt != nil {
		view.CancelRequestedAt = formatWorkflowTime(*run.CancelRequestedAt)
	}
	if run.ErrorCategory != nil {
		view.ErrorCategory = *run.ErrorCategory
	}
	if run.ErrorMessage != nil {
		view.ErrorMessage = *run.ErrorMessage
	}
	return view
}

func (a *App) createDiagnosticReplay(ctx context.Context, runID int64) (WorkflowRunView, error) {
	now := time.Now().UTC()
	var replay db.WorkflowRun
	err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var original db.WorkflowRun
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&original, runID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: workflow run", ErrNotFound)
			}
			return errors.New("load workflow run failed")
		}
		if original.Status != RunStatusSucceeded && original.Status != RunStatusFailed && original.Status != RunStatusCancelled {
			return fmt.Errorf("%w: only a terminal run can be replayed", ErrConflict)
		}
		if err := enforceWorkflowBacklog(tx, original.WorkflowID); err != nil {
			return err
		}
		replay = db.WorkflowRun{
			WorkflowID: original.WorkflowID, RevisionID: original.RevisionID, EntryPoint: original.EntryPoint, InputJSON: original.InputJSON, TriggerType: original.TriggerType,
			TriggerKey: "replay:" + security.RandomToken(), EventRecordID: original.EventRecordID,
			PartitionKey: original.PartitionKey, Diagnostic: true, OriginalRunID: &original.ID,
			Status: RunStatusQueued, NotBefore: now, TriggeredAt: original.TriggeredAt, ResultSummary: `{}`,
			CreatedBy: original.CreatedBy, CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Create(&replay).Error; err != nil {
			return errors.New("create diagnostic replay failed")
		}
		return nil
	})
	if err != nil {
		return WorkflowRunView{}, err
	}
	a.PublishWorkflowRunUpdated(replay.WorkflowID, replay.ID)
	return workflowRunView(replay), nil
}

func (a *App) replayWorkflowSideEffect(ctx context.Context, run db.WorkflowRun, nodeID string, iteration int) (map[string]any, []sdk.Artifact, error) {
	if run.OriginalRunID == nil {
		return nil, nil, errors.New("diagnostic replay has no original run")
	}
	var checkpoint db.WorkflowRunCheckpoint
	if err := a.DB.WithContext(ctx).Where(
		"run_id = ? AND node_instance_id = ? AND loop_iteration = ?", *run.OriginalRunID, nodeID, iteration,
	).First(&checkpoint).Error; err != nil {
		return nil, nil, errors.New("original side effect checkpoint is unavailable")
	}
	var output map[string]any
	var manifests []workflowArtifactManifest
	if json.Unmarshal([]byte(checkpoint.OutputJSON), &output) != nil || output == nil ||
		json.Unmarshal([]byte(checkpoint.ArtifactsJSON), &manifests) != nil {
		return nil, nil, errors.New("original side effect checkpoint is invalid")
	}
	artifacts := make([]sdk.Artifact, len(manifests))
	for index, manifest := range manifests {
		artifacts[index] = sdk.Artifact{SHA256: manifest.SHA256, MediaType: manifest.MediaType, Size: manifest.SizeBytes}
	}
	return output, artifacts, nil
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
