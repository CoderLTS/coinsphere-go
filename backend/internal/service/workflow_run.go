package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/security"
	"coinsphere/backend/plugin/sdk"
	workflowgraph "coinsphere/backend/workflow/graph"
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
	if err := a.AuthorizeWorkflow(ctx, workflowID, "workflows.run"); err != nil {
		return WorkflowRunView{}, err
	}
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
			entryPoint = "main"
		}
		if !workflowNodeIDPattern.MatchString(entryPoint) || len(entryPoint) > 32 {
			return errors.New("invalid workflow entryPoint")
		}
		revisionID := payload.RevisionID
		if revisionID <= 0 {
			revisionID = revisionPointerValue(workflow.PublishedRevisionID)
		}
		if revisionID <= 0 {
			return fmt.Errorf("%w: select a saved revision", ErrConflict)
		}
		var revision db.WorkflowRevision
		if err := tx.Where("workflow_id = ? AND id = ?", workflowID, revisionID).First(&revision).Error; err != nil {
			return errors.New("加载工作流版本失败")
		}
		graph, err := a.buildWorkflowRunGraphAt(revision.GraphJSON, entryPoint)
		if err != nil {
			return fmt.Errorf("%w: 所选工作流版本的配置无效，无法从该入口运行", ErrConflict)
		}
		validated, err := a.validateWorkflowGraph(json.RawMessage(revision.GraphJSON))
		if err != nil {
			return err
		}
		if err := a.authorizeExecution(principal.User.ID, validated); err != nil {
			return err
		}
		if err := ensureWorkflowRevisionSecrets(tx, workflowID, revision.ID, validated); err != nil {
			return err
		}
		if graph.descriptors[graph.order[0]].Kind == sdk.NodeKindTrigger && graph.nodes[graph.order[0]].NodeType != "core.manual" {
			return fmt.Errorf("%w: 工作流未使用手动触发节点", ErrConflict)
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
			return errors.New("工作流运行参数必须是 JSON 对象")
		}
		if validateWorkflowSchemaValue(graph.descriptors[graph.order[0]].InputSchema, inputObject) != nil {
			return errors.New("入口参数不符合所选工作流版本的要求")
		}
		run = db.WorkflowRun{
			WorkflowID: workflowID, RevisionID: revision.ID, EntryPoint: entryPoint, InputJSON: string(input), TriggerType: "manual",
			TriggerKey: security.RandomToken(), Status: RunStatusQueued, NotBefore: now,
			TriggeredAt: now, ExecutionUserID: ownerID, RequiresSerial: graphHasPersistentState(validated), CreatedBy: &ownerID, ResultSummary: `{}`, CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Create(&run).Error; err != nil {
			return errors.New("创建工作流运行记录失败")
		}
		return a.syncRunPluginReferences(tx, run, validated)
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
	if err := a.AuthorizeWorkflow(ctx, workflowID, "workflows.read"); err != nil {
		return nil, err
	}
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
	if err := a.AuthorizeWorkflow(ctx, workflowID, "workflows.read"); err != nil {
		return nil, err
	}
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
	if err := a.authorizeRun(ctx, runID, "workflows.read"); err != nil {
		return WorkflowRunView{}, err
	}
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
	permission := "workflows." + action
	if action == "replay" {
		permission = "workflows.run"
	}
	if err := a.authorizeRun(ctx, runID, permission); err != nil {
		return WorkflowRunView{}, err
	}
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
			if run.ErrorCategory != nil && *run.ErrorCategory == string(sdk.ErrorUnknownResult) {
				return fmt.Errorf("%w: external result requires reconciliation", ErrConflict)
			}
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
	nextRecovery := time.Now().UTC().Add(5 * time.Second)
	nextCleanup := time.Now().UTC().Add(24 * time.Hour)
	ticker := time.NewTicker(runPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-ticker.C:
			if !now.Before(nextRecovery) {
				if err := a.recoverExpiredRuns(ctx); err != nil {
					slog.Error("workflow lease recovery failed", "error_category", "lease_recovery")
				}
				nextRecovery = now.UTC().Add(5 * time.Second)
			}
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
				select {
				case a.runSlots <- struct{}{}:
				default:
					goto claimsDone
				}
				run, ok, err := a.claimWorkflowRun(ctx, now.UTC())
				if err != nil {
					<-a.runSlots
					slog.Error("workflow run claim failed", "component", "workflow.runtime", "error_category", "run_queue")
					break
				}
				if !ok {
					<-a.runSlots
					break
				}
				a.runWG.Add(1)
				go func() {
					defer a.runWG.Done()
					defer func() { <-a.runSlots }()
					a.executeWorkflowRun(ctx, run)
				}()
			}
		claimsDone:
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
	return a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var expired []db.WorkflowRun
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where("status='running' AND lease_expires_at < clock_timestamp()").Order("id").Limit(100).Find(&expired).Error; err != nil {
			return err
		}
		for _, r := range expired {
			var active []db.WorkflowRunNode
			if err := tx.Where("run_id=? AND status='running'", r.ID).Find(&active).Error; err != nil {
				return err
			}
			var revision db.WorkflowRevision
			if err := tx.First(&revision, r.RevisionID).Error; err != nil {
				return err
			}
			g, err := a.validateWorkflowGraph(json.RawMessage(revision.GraphJSON))
			if err != nil {
				return err
			}
			unknown := false
			for _, rn := range active {
				node, ok := g.nodes[rn.NodeInstanceID]
				if !ok { // Expanded Loop children keep the same descriptor and configured policy.
					for _, parent := range g.nodes {
						if parent.NodeType == "core.loop" {
							body, _, _, _, e := a.buildWorkflowLoopGraph(parent)
							if e == nil {
								node, ok = body.nodes[rn.NodeInstanceID]
								if ok {
									break
								}
							}
						}
					}
				}
				desc, found := a.workflowNodeDescriptors()[rn.NodeType]
				category := "lease_expired"
				if rn.InvocationStarted && (!found || !ok || (!r.Diagnostic && !a.workflowNodeRetrySafe(desc, node.Config))) {
					unknown = true
					category = string(sdk.ErrorUnknownResult)
				}
				if err := tx.Model(&rn).Updates(map[string]any{"status": RunStatusFailed, "error_category": category, "error_message": "执行租约已失效，结果须确认", "completed_at": now, "duration_ms": max(now.Sub(rn.StartedAt).Milliseconds(), 0)}).Error; err != nil {
					return err
				}
			}
			updates := map[string]any{"status": RunStatusQueued, "lease_token": nil, "lease_expires_at": nil, "not_before": now, "updated_at": now}
			if unknown {
				updates["status"] = RunStatusFailed
				updates["error_category"] = string(sdk.ErrorUnknownResult)
				updates["completed_at"] = now
			} else if r.CancelRequestedAt != nil {
				updates["status"] = RunStatusCancelled
				updates["completed_at"] = now
			}
			if err := tx.Model(&r).Updates(updates).Error; err != nil {
				return err
			}
			if updates["status"] != RunStatusQueued {
				if err := tx.Exec("UPDATE plugin_references SET active=FALSE WHERE reference_type='run' AND reference_id=?", fmt.Sprint(r.ID)).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
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
      AND (w.status = 'active' OR eb.created_by IS NOT NULL)
      AND (SELECT COUNT(*) FROM workflow_runs active
           WHERE active.workflow_id = eb.workflow_id AND active.status IN ('running','waiting')) < wr.max_concurrent_runs
      AND (NOT eb.requires_serial OR NOT EXISTS (SELECT 1 FROM workflow_runs active WHERE active.workflow_id=eb.workflow_id AND active.status IN ('running','waiting')))
      AND NOT EXISTS (SELECT 1 FROM workflow_runs active WHERE active.workflow_id=eb.workflow_id AND active.requires_serial AND active.status IN ('running','waiting'))
      AND (eb.partition_key = '' OR NOT EXISTS (
          SELECT 1 FROM workflow_runs prior
          WHERE prior.workflow_id = eb.workflow_id
            AND prior.partition_key = eb.partition_key
            AND prior.status IN ('queued', 'running', 'retrying','waiting')
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
		// ponytail: 单个队列领取锁适用于当前单体；只有实测领取吞吐成为瓶颈才按工作流分锁。
		var locked bool
		if err := tx.Raw("SELECT pg_try_advisory_xact_lock(hashtextextended('coinsphere.run-claim',0))").Scan(&locked).Error; err != nil {
			return err
		}
		if !locked {
			return nil
		}
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
	ctx, cancel := context.WithCancel(context.WithValue(parent, executionLeaseKey{}, run))
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
	go a.renewRunLease(ctx, run, leaseDone, cancel)
	defer close(leaseDone)

	var revision db.WorkflowRevision
	if err := a.DB.WithContext(ctx).First(&revision, run.RevisionID).Error; err != nil {
		a.failWorkflowRun(run, "revision")
		return
	}
	graph, err := a.buildWorkflowRunGraphAt(revision.GraphJSON, run.EntryPoint)
	if err != nil {
		a.failWorkflowRun(run, "graph")
		return
	}
	validated, err := a.validateWorkflowGraph(json.RawMessage(revision.GraphJSON))
	if err != nil || a.authorizeExecution(run.ExecutionUserID, validated) != nil {
		a.failWorkflowRun(run, "authorization")
		return
	}
	if err := a.AuthorizeWorkflow(WithPrincipal(ctx, mustPrincipal(a, run.ExecutionUserID)), run.WorkflowID, "workflows.run"); err != nil {
		a.failWorkflowRun(run, "authorization")
		return
	}
	outputs, err := a.loadWorkflowRunCheckpoints(ctx, run.ID)
	if err != nil {
		a.failWorkflowRun(run, "checkpoint")
		return
	}
	event := map[string]string{"type": run.TriggerType, "triggeredAt": formatWorkflowTime(run.TriggeredAt)}
	if run.EventRecordID != nil {
		cloudEvent, eventData, err := a.workflowRunEvent(ctx, run)
		if err != nil {
			a.failWorkflowRun(run, "event")
			return
		}
		event = workflowEventContext(cloudEvent)
		if _, completed := outputs[revision.MainTriggerNodeID]; completed {
			outputs[revision.MainTriggerNodeID] = eventData
		}
	}
	var entryInput map[string]any
	if json.Unmarshal([]byte(run.InputJSON), &entryInput) != nil {
		a.failWorkflowRun(run, "input")
		return
	}
	for _, nodeID := range graph.order {
		if _, completed := outputs[nodeID]; completed {
			continue
		}
		if cancelled, paused := a.runShouldStop(ctx, run.ID, run.WorkflowID); cancelled || paused {
			if cancelled {
				a.cancelWorkflowRun(run)
			} else {
				a.requeueWorkflowRun(run)
			}
			return
		}
		node := graph.nodes[nodeID]
		if nodeID != graph.order[0] {
			reachable, err := workflowNodeReachable(graph.incoming[nodeID], outputs, event, entryInput)
			if err != nil {
				a.failWorkflowRun(run, "condition")
				return
			}
			if !reachable {
				if err := a.recordSkippedNode(ctx, run, nodeID, graph.nodes[nodeID], 0); err != nil {
					a.failWorkflowRun(run, "lease")
					return
				}
				continue
			}
		}
		input, err := resolveWorkflowNodeInput(node, graph.incoming[nodeID], outputs, event, entryInput)
		if err != nil {
			a.failWorkflowRun(run, "input")
			return
		}
		if nodeID == graph.order[0] {
			if json.Unmarshal([]byte(run.InputJSON), &input) != nil || input == nil {
				a.failWorkflowRun(run, "input")
				return
			}
		}
		outcome := a.executeWorkflowNode(ctx, run, revision, graph, node, input, outputs, event, entryInput, 0)
		if outcome.waiting {
			return
		}
		if outcome.err != nil {
			if outcome.category == string(sdk.ErrorUnknownResult) {
				a.failWorkflowRun(run, outcome.category)
				return
			}
			if errors.Is(outcome.err, context.Canceled) || ctx.Err() != nil {
				cancelled, _ := a.runShouldStop(ctx, run.ID, run.WorkflowID)
				if cancelled {
					a.cancelWorkflowRun(run)
				} else {
					a.requeueWorkflowRun(run)
				}
				return
			}
			if outcome.attempt < runMaxAttempts && sdk.ClassifyError(outcome.err) == sdk.ErrorTransient {
				a.retryWorkflowRun(run, outcome.attempt)
			} else {
				a.failWorkflowRun(run, outcome.category)
			}
			return
		}
		outputs[nodeID] = outcome.output
	}
	a.completeWorkflowRun(run)
}

type workflowNodeOutcome struct {
	output   map[string]any
	attempt  int
	category string
	waiting  bool
	err      error
}

func (a *App) executeWorkflowNode(ctx context.Context, run db.WorkflowRun, revision db.WorkflowRevision, graph workflowRunGraph, node workflowGraphNode, input map[string]any, outputs map[string]map[string]any, event map[string]string, entryInput map[string]any, iteration int) workflowNodeOutcome {
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
	if err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := a.lockExecutionLease(tx, run, nil); err != nil {
			return err
		}
		return tx.Create(&runNode).Error
	}); err != nil {
		return workflowNodeOutcome{attempt: attempt, category: "node_run", err: err}
	}
	a.PublishWorkflowRunUpdated(run.WorkflowID, run.ID)
	if node.NodeType != "core.end" {
		a.appendWorkflowNodeLog(ctx, run.WorkflowID, run.ID, runNode.ID, slog.LevelInfo, "节点开始执行", map[string]any{
			"attempt": attempt, "loop_iteration": iteration,
		})
	}
	if validateWorkflowSchemaValue(desc.InputSchema, input) != nil {
		a.finishWorkflowRunNode(run, runNode, RunStatusFailed, "input", "节点输入不符合 JSON Schema", startedAt)
		return workflowNodeOutcome{attempt: attempt, category: "input", err: errors.New("node input does not match its JSON Schema")}
	}
	if run.Diagnostic && desc.SideEffect != sdk.SideEffectNone {
		output, artifacts, err := a.replayWorkflowSideEffect(ctx, run, node.NodeInstanceID, iteration)
		if err != nil || validateWorkflowSchemaValue(desc.OutputSchema, output) != nil {
			a.finishWorkflowRunNode(run, runNode, RunStatusFailed, "diagnostic", "诊断重放缺少可用检查点", startedAt)
			return workflowNodeOutcome{attempt: attempt, category: "diagnostic", err: errors.New("diagnostic side effect checkpoint is unavailable")}
		}
		raw := mustJSON(output)
		if err := a.commitWorkflowNodeSuccess(ctx, runNode, run, revision, node, operationKey, iteration, raw, nil, artifacts, startedAt); err != nil {
			a.finishWorkflowRunNode(run, runNode, RunStatusFailed, "checkpoint", "保存节点检查点失败", startedAt)
			return workflowNodeOutcome{attempt: attempt, category: "checkpoint", err: err}
		}
		return workflowNodeOutcome{attempt: attempt, output: output}
	}
	slot := a.streamSlots
	if desc.Pool == sdk.PoolCompute {
		slot = a.computeSlots
	}
	if node.NodeType != "core.loop" && node.NodeType != "core.human_approval" {
		select {
		case slot <- struct{}{}:
			defer func() { <-slot }()
		case <-ctx.Done():
			a.finishWorkflowRunNode(run, runNode, RunStatusCancelled, "cancelled", "节点执行已取消", startedAt)
			return workflowNodeOutcome{attempt: attempt, category: "cancelled", err: ctx.Err()}
		}

	}

	// A queued node may have waited for capacity while its lease or grants expired.
	if err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := a.lockExecutionLease(tx, run, nil); err != nil {
			return err
		}
		r := runLeaseQuery(tx, run, false).Updates(map[string]any{"current_node_instance_id": node.NodeInstanceID, "updated_at": startedAt})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return ErrConflict
		}
		return tx.Model(&db.WorkflowRunNode{}).Where("id=? AND status=?", runNode.ID, RunStatusRunning).Update("invocation_started", true).Error
	}); err != nil {
		return workflowNodeOutcome{attempt: attempt, category: "authorization", err: err}
	}
	state := &bufferedNodeState{app: a, workflowID: run.WorkflowID, revisionID: revision.ID, node: node, stateMode: desc.State}
	request := sdk.ActionRequest{
		Revision:       sdk.RevisionRef{WorkflowID: fmt.Sprint(run.WorkflowID), RevisionID: fmt.Sprint(revision.ID)},
		NodeInstanceID: node.NodeInstanceID, OperationKey: operationKey,
		Input: mustJSON(input), Config: append(json.RawMessage(nil), node.Config...),
		Secrets: workflowSecretReader{app: a, revisionID: revision.ID, nodeInstanceID: node.NodeInstanceID},
		State:   state, Artifacts: workflowArtifactStore{app: a}, GraphSnapshot: json.RawMessage(revision.GraphJSON),
		Incoming: workflowIncomingOutputs(graph.incoming[node.NodeInstanceID], outputs, event, entryInput),
		Logger:   a.workflowNodeLogger(run, runNode.ID, node.NodeType),
	}

	result, category, executeErr := a.callWorkflowNode(ctx, run, revision, node, request, event)
	if errors.Is(executeErr, errWorkflowWaiting) {
		a.PublishWorkflowRunUpdated(run.WorkflowID, run.ID)
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
		if sdk.ClassifyError(executeErr) == sdk.ErrorTransient && !a.workflowNodeRetrySafe(desc, node.Config) {
			executeErr = &sdk.ExecutionError{Class: sdk.ErrorUnknownResult, Err: executeErr}
		}
		if sdk.ClassifyError(executeErr) == sdk.ErrorUnknownResult {
			category = string(sdk.ErrorUnknownResult)
		}
		status := RunStatusFailed
		if (errors.Is(executeErr, context.Canceled) || ctx.Err() != nil) && desc.SideEffect != sdk.SideEffectNone && !a.workflowNodeRetrySafe(desc, node.Config) {
			category = string(sdk.ErrorUnknownResult)
			executeErr = &sdk.ExecutionError{Class: sdk.ErrorUnknownResult, Err: executeErr}
		} else if errors.Is(executeErr, context.Canceled) || ctx.Err() != nil {
			status, category = RunStatusCancelled, "cancelled"
		}
		a.finishWorkflowRunNode(run, runNode, status, category, workflowErrorMessage(executeErr), startedAt)
		return workflowNodeOutcome{attempt: attempt, category: category, err: executeErr}
	}
	if err := a.commitWorkflowNodeSuccess(ctx, runNode, run, revision, node, operationKey, iteration, result.Output, state.pending, result.Artifacts, startedAt); err != nil {
		a.finishWorkflowRunNode(run, runNode, RunStatusFailed, "checkpoint", "保存节点检查点失败", startedAt)
		category := "checkpoint"
		if desc.SideEffect != sdk.SideEffectNone && !a.workflowNodeRetrySafe(desc, node.Config) {
			category = string(sdk.ErrorUnknownResult)
		}
		return workflowNodeOutcome{attempt: attempt, category: category, err: err}
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
			return sdk.ActionResult{}, string(sdk.ErrorPermanent), errors.New("node action handler is unavailable")
		}
		result, err := handler.Execute(ctx, request)
		if err != nil {
			return sdk.ActionResult{}, string(sdk.ClassifyError(err)), err
		}
		return result, "", nil
	}
}

func (a *App) commitWorkflowNodeSuccess(ctx context.Context, runNode db.WorkflowRunNode, run db.WorkflowRun, revision db.WorkflowRevision, node workflowGraphNode, operationKey string, iteration int, output, state json.RawMessage, artifacts []sdk.Artifact, startedAt time.Time) error {
	err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := a.lockExecutionLease(tx, run, nil); err != nil {
			return err
		}
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
		result := tx.Model(&db.WorkflowRunNode{}).Where("id = ? AND status = ?", runNode.ID, RunStatusRunning).
			Updates(map[string]any{"status": RunStatusSucceeded, "output_summary": outputSummary, "completed_at": now, "duration_ms": duration})
		if result.Error != nil || result.RowsAffected != 1 {
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
				Columns: []clause.Column{{Name: "workflow_id"}, {Name: "revision_id"}, {Name: "node_instance_id"}},
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
	return a.buildWorkflowRunGraphAt(raw, "main")
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
		return workflowRunGraph{}, errors.New("所选工作流版本不支持该运行方式")
	}
	return buildWorkflowRunGraph(graph, validated.nodes, validated.descriptors, startID), nil
}

func buildWorkflowRunGraph(graph workflowGraph, nodes map[string]workflowGraphNode, descriptors map[string]sdk.NodeDescriptor, startID string) workflowRunGraph {
	order, incoming, _ := workflowgraph.Order(graph, startID)
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
	graph := workflowGraph{SchemaVersion: 3, Nodes: make([]workflowGraphNode, 0, len(loop.config.Body.Nodes)), Edges: make([]workflowGraphEdge, 0, len(loop.config.Body.Edges))}
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
			if binding.Kind == "cel" {
				binding.Expression, err = workflowgraph.RewriteNodeReferences(binding.Expression, mapping)
				if err != nil {
					return workflowRunGraph{}, workflowLoopConfig{}, "", "", err
				}
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
		if runtimeEdge.Condition != "" {
			runtimeEdge.Condition, err = workflowgraph.RewriteNodeReferences(runtimeEdge.Condition, mapping)
			if err != nil {
				return workflowRunGraph{}, workflowLoopConfig{}, "", "", err
			}
		}
		graph.Edges = append(graph.Edges, runtimeEdge)
	}
	if loop.config.ExitCondition != "" {
		loop.config.ExitCondition, err = workflowgraph.RewriteNodeReferences(loop.config.ExitCondition, mapping)
		if err != nil {
			return workflowRunGraph{}, workflowLoopConfig{}, "", "", err
		}
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
				reachable, err := workflowNodeReachable(graph.incoming[nodeID], outputs, event, map[string]any{"iteration": iteration, "value": carried})
				if err != nil {
					return sdk.ActionResult{}, err
				}
				if !reachable {
					continue
				}
			}
			bodyInput := map[string]any{"iteration": iteration, "value": carried}
			if nodeID != itemID {
				bodyInput, err = resolveWorkflowNodeInput(bodyNode, graph.incoming[nodeID], outputs, event, map[string]any{"iteration": iteration, "value": carried})
				if err != nil {
					return sdk.ActionResult{}, err
				}
			}
			outcome := a.executeWorkflowNode(loopCtx, run, revision, graph, bodyNode, bodyInput, outputs, event, map[string]any{"iteration": iteration, "value": carried}, iteration)
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
		conditionInput := map[string]any{}
		conditionInput["iteration"] = iteration
		conditionInput["value"] = carried
		value, err := workflowgraph.Evaluate(config.ExitCondition, workflowgraph.Context{Event: event, Input: conditionInput, Nodes: outputs})
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

func workflowContext(event map[string]string, outputs map[string]map[string]any, inputs ...map[string]any) workflowgraph.Context {
	ctx := workflowgraph.Context{Event: event, Nodes: outputs}
	if len(inputs) > 0 {
		ctx.Input = inputs[0]
	}
	return ctx
}
func workflowNodeReachable(edges []workflowGraphEdge, outputs map[string]map[string]any, event map[string]string, inputs ...map[string]any) (bool, error) {
	reached, err := workflowgraph.Reached(edges, workflowContext(event, outputs, inputs...))
	return len(reached) > 0, err
}
func workflowEdgeReached(edge workflowGraphEdge, outputs map[string]map[string]any, event map[string]string, inputs ...map[string]any) (bool, error) {
	return workflowgraph.EdgeReached(edge, workflowContext(event, outputs, inputs...))
}
func resolveWorkflowNodeInput(node workflowGraphNode, incoming []workflowGraphEdge, outputs map[string]map[string]any, event map[string]string, inputs ...map[string]any) (map[string]any, error) {
	return workflowgraph.Resolve(node, incoming, workflowContext(event, outputs, inputs...))
}
func workflowFieldValue(root map[string]any, path []string) (any, bool) {
	return workflowgraph.Field(root, path)
}
func evaluateWorkflowCEL(expression string, event map[string]string, input map[string]any) (any, error) {
	return workflowgraph.Evaluate(expression, workflowgraph.Context{Event: event, Input: input})
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

func (a *App) finishWorkflowRunNode(run db.WorkflowRun, runNode db.WorkflowRunNode, status, category, message string, startedAt time.Time) {
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
	if err := a.DB.Transaction(func(tx *gorm.DB) error {
		if err := lockRunLease(tx, run, status == RunStatusCancelled, nil); err != nil {
			return err
		}
		return tx.Model(&db.WorkflowRunNode{}).Where("id=? AND status=?", runNode.ID, RunStatusRunning).Updates(updates).Error
	}); err != nil {
		return
	}
	a.PublishWorkflowRunUpdated(run.WorkflowID, runNode.RunID)
	level := slog.LevelInfo
	if status == RunStatusFailed {
		level = slog.LevelError
	} else if status == RunStatusCancelled {
		level = slog.LevelWarn
	}
	a.appendWorkflowNodeLog(context.WithValue(context.Background(), executionLeaseKey{}, run), run.WorkflowID, runNode.RunID, runNode.ID, level, message, map[string]any{
		"status": status, "error_category": category, "duration_ms": duration,
	})
}

func (a *App) retryWorkflowRun(run db.WorkflowRun, attempt int) {
	now := time.Now().UTC()
	if runLeaseQuery(a.DB, run, false).Updates(map[string]any{
		"status": RunStatusRetrying, "not_before": now.Add(time.Duration(attempt) * time.Second),
		"lease_token": nil, "lease_expires_at": nil, "error_category": nil, "error_message": nil, "updated_at": now,
	}).Error == nil {
		a.PublishWorkflowRunUpdated(run.WorkflowID, run.ID)
	}
}

func (a *App) failWorkflowRun(run db.WorkflowRun, category string) {
	a.finishWorkflowRun(run, RunStatusFailed, category)
}

func (a *App) cancelWorkflowRun(run db.WorkflowRun) {
	a.finishWorkflowRun(run, RunStatusCancelled, "cancelled")
}

func (a *App) completeWorkflowRun(run db.WorkflowRun) {
	a.finishWorkflowRun(run, RunStatusSucceeded, "")
}

func (a *App) finishWorkflowRun(expected db.WorkflowRun, status, category string) {
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
		if err := lockRunLease(tx, expected, status == RunStatusCancelled, &run); err != nil {
			return err
		}
		workflowID = run.WorkflowID
		summary, err := workflowRunResultSummary(tx, expected.ID)
		if err != nil {
			return err
		}
		updates["result_summary"] = summary
		if category == "" {
			updates["error_message"] = nil
		} else {
			updates["error_message"] = workflowLogMessage("工作流运行失败: " + category)
		}
		if err := tx.Model(&run).Updates(updates).Error; err != nil {
			return err
		}
		if err := tx.Exec("UPDATE plugin_references SET active=FALSE WHERE reference_type='run' AND reference_id=?", fmt.Sprint(run.ID)).Error; err != nil {
			return err
		}
		if status == RunStatusFailed && run.TriggerType != "failure" {
			return a.enqueueWorkflowEvent(tx, newWorkflowFailureEvent(run, category, now))
		}
		return nil
	})
	if err != nil {
		slog.Error("finish workflow run failed", "component", "workflow.runtime", "run_id", expected.ID, "error_category", "run_finish")
		return
	}
	a.PublishWorkflowRunUpdated(workflowID, expected.ID)
}

func workflowRunResultSummary(tx *gorm.DB, runID int64) (string, error) {
	var attempts int64
	if err := tx.Model(&db.WorkflowRunNode{}).Where("run_id = ?", runID).Count(&attempts).Error; err != nil {
		return "", err
	}
	summary := map[string]any{"nodeAttempts": attempts}
	var last db.WorkflowRunNode
	query := tx.Where("run_id = ? AND status = ? AND output_summary <> '{}'", runID, RunStatusSucceeded)
	err := query.Order("id DESC").First(&last).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", err
	}
	if err == nil {
		summary["lastNodeInstanceId"] = last.NodeInstanceID
		summary["lastNodeStatus"] = last.Status
		var output map[string]any
		if json.Unmarshal([]byte(last.OutputSummary), &output) == nil && len(output) > 0 {
			summary["output"] = output
		}
	}
	return mustJSONString(summary), nil
}

func (a *App) requeueWorkflowRun(run db.WorkflowRun) {
	now := time.Now().UTC()
	result := runLeaseQuery(a.DB, run, false).Updates(map[string]any{"status": RunStatusQueued, "not_before": now, "lease_token": nil, "lease_expires_at": nil, "updated_at": now})
	if result.Error == nil && result.RowsAffected == 1 {
		a.PublishWorkflowRunUpdated(run.WorkflowID, run.ID)
	}
}

func (a *App) runShouldStop(ctx context.Context, runID, workflowID int64) (cancelled, paused bool) {
	var row struct {
		Status            string
		CreatedBy         *int64
		CancelRequestedAt *time.Time
	}
	if err := a.DB.Raw(`SELECT w.status, r.created_by, r.cancel_requested_at FROM workflows w JOIN workflow_runs r ON r.workflow_id = w.id WHERE r.id = ? AND w.id = ?`, runID, workflowID).Scan(&row).Error; err != nil {
		return false, true
	}
	cancelled = row.CancelRequestedAt != nil
	return cancelled, !cancelled && (ctx.Err() != nil || row.Status != WorkflowStatusActive && row.CreatedBy == nil)
}

func (a *App) renewRunLease(ctx context.Context, run db.WorkflowRun, done <-chan struct{}, cancel context.CancelFunc) {
	ticker := time.NewTicker(runLeaseDuration / 3)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case now := <-ticker.C:
			err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				if err := a.lockExecutionLease(tx, run, nil); err != nil {
					return err
				}
				r := runLeaseQuery(tx, run, false).Updates(map[string]any{"lease_expires_at": gorm.Expr("clock_timestamp() + interval '30 seconds'"), "updated_at": now.UTC()})
				if r.Error != nil {
					return r.Error
				}
				if r.RowsAffected != 1 {
					return ErrConflict
				}
				return nil
			})
			if err != nil {
				cancel()
				return
			}
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
JOIN workflow_revisions wr ON wr.id = w.published_revision_id
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
			var workflow db.Workflow
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&workflow, item.WorkflowID).Error; err != nil {
				return err
			}
			if workflow.Status != WorkflowStatusActive || revisionPointerValue(workflow.PublishedRevisionID) != item.RevisionID {
				return nil
			}
			validated, err := a.validateWorkflowGraph(json.RawMessage(item.GraphJSON))
			if err != nil {
				return err
			}
			actor, err := principalForTx(tx, &Principal{User: &db.SystemUser{ID: workflow.OwnerUserID}})
			if err != nil {
				return err
			}
			if err := authorizeWorkflowTx(tx, actor, workflow.ID, "workflows.run"); err != nil {
				return err
			}
			if err := authorizePluginExecution(actor, validated); err != nil {
				return err
			}
			var runtime db.WorkflowRuntime
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&runtime, item.WorkflowID).Error; err != nil {
				return err
			}
			if runtime.NextScheduledAt == nil || runtime.NextScheduledAt.After(now) {
				return nil
			}
			if err := enforceWorkflowBacklog(tx, item.WorkflowID); err != nil {
				return nil
			}
			run := db.WorkflowRun{
				ExecutionUserID: workflow.OwnerUserID, RequiresSerial: graphHasPersistentState(validated), WorkflowID: item.WorkflowID, RevisionID: item.RevisionID, EntryPoint: "main", InputJSON: `{}`, TriggerType: "schedule",
				TriggerKey: dueAt.Format(time.RFC3339Nano), Status: RunStatusQueued,
				NotBefore: now, TriggeredAt: dueAt, ResultSummary: `{}`, CreatedAt: now, UpdatedAt: now,
			}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&run).Error; err != nil {
				return err
			}
			if run.ID > 0 {
				if err := a.syncRunPluginReferences(tx, run, validated); err != nil {
					return err
				}
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
			RequiresSerial: original.RequiresSerial, ExecutionUserID: ContextPrincipal(ctx).User.ID, CreatedBy: &ContextPrincipal(ctx).User.ID, CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Create(&replay).Error; err != nil {
			return errors.New("create diagnostic replay failed")
		}
		var revision db.WorkflowRevision
		if err := tx.First(&revision, replay.RevisionID).Error; err != nil {
			return err
		}
		g, err := a.validateWorkflowGraph(json.RawMessage(revision.GraphJSON))
		if err != nil {
			return err
		}
		if err := a.authorizeExecution(replay.ExecutionUserID, g); err != nil {
			return err
		}
		return a.syncRunPluginReferences(tx, replay, g)
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
	if err := s.app.DB.WithContext(ctx).Where("workflow_id = ? AND revision_id = ? AND node_instance_id = ?", s.workflowID, s.revisionID, s.node.NodeInstanceID).First(&state).Error; err != nil {
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
	database       *gorm.DB
	app            *App
	revisionID     int64
	nodeInstanceID string
}

func (r workflowSecretReader) Read(ctx context.Context, field string) ([]byte, error) {
	if strings.TrimSpace(field) == "" {
		return nil, errors.New("secret field is required")
	}
	if r.app.Cipher == nil {
		return nil, errors.New("workflow secret cipher is unavailable")
	}
	var binding db.WorkflowSecretBinding
	read := func(tx *gorm.DB) error {
		return tx.Where("revision_id = ? AND node_instance_id = ? AND field_name = ?", r.revisionID, r.nodeInstanceID, field).First(&binding).Error
	}
	var err error
	if r.database != nil {
		// Ingress owns and has already locked the published revision transaction.
		err = read(r.database.WithContext(ctx))
	} else {
		err = r.app.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if run, ok := ctx.Value(executionLeaseKey{}).(db.WorkflowRun); ok {
				if run.RevisionID != r.revisionID {
					return ErrPermission
				}
				if err := r.app.lockExecutionLease(tx, run, nil); err != nil {
					return err
				}
			} else if lease, ok := ctx.Value(triggerLeaseKey{}).(triggerLease); ok {
				if lease.revisionID != r.revisionID {
					return ErrPermission
				}
				if err := r.app.lockTriggerLease(tx, lease); err != nil {
					return err
				}
			} else {
				return ErrPermission
			}
			return read(tx)
		})
	}
	if err != nil {
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
