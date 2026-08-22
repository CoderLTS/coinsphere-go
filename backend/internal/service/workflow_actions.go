package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"coinsphere/backend/internal/db"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrWorkflowActionConflict         = errors.New("workflow action state does not allow this operation")
	ErrWorkflowActionReauthentication = errors.New("valid reauthentication token is required")
)

type workflowActionDispatch func(
	ctx context.Context,
	app *App,
	principal *Principal,
	targetID string,
	formData M,
	idempotencyKey string,
	reauthToken string,
) (any, error)

type workflowActionSpec struct {
	Title                  string
	RequiredPermission     string
	RequiresReauth         bool
	DispatchConsumesReauth bool
	FormSchema             M
	Dispatch               workflowActionDispatch
}

var workflowActionRegistry = map[string]workflowActionSpec{}

func registerWorkflowAction(actionType string, spec workflowActionSpec) {
	if strings.TrimSpace(actionType) == "" || spec.Dispatch == nil {
		panic("invalid workflow action registration")
	}
	if _, exists := workflowActionRegistry[actionType]; exists {
		panic("duplicate workflow action registration: " + actionType)
	}
	workflowActionRegistry[actionType] = spec
}

func requireWorkflowActionSpec(actionType string) (workflowActionSpec, error) {
	spec, ok := workflowActionRegistry[actionType]
	if !ok {
		return workflowActionSpec{}, bizErr("Unknown workflow action type: %s", actionType)
	}
	return spec, nil
}

func (a *App) finalizeWaiting(
	ctx context.Context,
	execution *db.WorkflowExecution,
	attempt int,
	result *runResult,
) error {
	if result == nil || result.PendingWait == nil {
		return errors.New("workflow wait request is missing")
	}
	pending := result.PendingWait
	if pending.Request.Kind != "worker_job" && pending.Request.Kind != "human_action" {
		return bizErr("Unsupported workflow wait kind: %s", pending.Request.Kind)
	}
	if pending.Request.Kind == "human_action" {
		if _, err := requireWorkflowActionSpec(pending.Request.ActionType); err != nil {
			return err
		}
	}
	waitID, err := uuid.NewV7()
	if err != nil {
		return err
	}
	status := "waiting_job"
	if pending.Request.Kind == "human_action" {
		status = "waiting_action"
	}
	now := time.Now().UTC()
	wait := db.WorkflowExecutionWait{
		ID: waitID, OwnerUserID: execution.OwnerUserID, WorkflowExecutionID: execution.ID,
		WorkflowExecutionNodeID: &pending.WorkflowExecutionNodeID,
		Kind:                    pending.Request.Kind, ActionType: pending.Request.ActionType,
		TargetType: pending.Request.TargetType, TargetID: pending.Request.TargetID,
		Status: "pending", RequestJSON: serializeSnapshot(pending.Request.Request, a.Cfg.Workflow.MaxOutputSnapshotBytes),
		ResultJSON: "{}", ResumeNodeID: pending.ResumeNodeID, ResumeBranch: pending.ResumeBranch,
		ExpiresAt: pending.Request.ExpiresAt, CreatedAt: now, UpdatedAt: now,
	}
	return a.dbWithContext(ctx).Transaction(func(tx *gorm.DB) error {
		updated := tx.Model(&db.WorkflowExecution{}).
			Where("id = ? AND status = ? AND cancel_requested_at IS NULL AND attempt_count = ? AND worker_id = ?", execution.ID, "running", attempt, a.WorkerID).
			Updates(map[string]any{
				"status": status, "finished_at": nil, "next_retry_at": nil,
				"context_snapshot_json": serializeSnapshot(result.SharedState, a.Cfg.Workflow.MaxOutputSnapshotBytes),
				"result_snapshot_json":  serializeSnapshot(orEmptyMap(result.SharedState["nodeOutputs"]), a.Cfg.Workflow.MaxOutputSnapshotBytes),
				"failure_category":      "", "error_message": "",
			})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected == 0 {
			return ErrWorkflowCanceled
		}
		if err := tx.Create(&wait).Error; err != nil {
			return err
		}
		return a.closeAttemptWithDB(tx, execution.ID, attempt, a.WorkerID, status, result.StartedAt, result.FinishedAt, "", "")
	})
}

type WorkflowActionDecision struct {
	Decision string `json:"decision"`
	FormData M      `json:"formData"`
}

func (a *App) ListWorkflowActions(ownerUserID int64, status string) ([]M, error) {
	if ownerUserID <= 0 {
		return nil, notFoundErr("workflow action")
	}
	query := a.DB.Preload("WorkflowExecution").
		Where("owner_user_id = ? AND kind = ?", ownerUserID, "human_action")
	if strings.TrimSpace(status) == "" {
		query = query.Where("status IN ?", []string{"pending", "processing"})
	} else {
		query = query.Where("status = ?", strings.TrimSpace(status))
	}
	var waits []db.WorkflowExecutionWait
	if err := query.Order("created_at DESC, id DESC").Limit(200).Find(&waits).Error; err != nil {
		return nil, err
	}
	items := make([]M, 0, len(waits))
	for i := range waits {
		items = append(items, a.serializeWorkflowAction(&waits[i]))
	}
	return items, nil
}

func (a *App) GetWorkflowAction(rawID string, ownerUserID int64) (M, error) {
	id, err := uuid.Parse(strings.TrimSpace(rawID))
	if err != nil || ownerUserID <= 0 {
		return nil, notFoundErr("workflow action")
	}
	var wait db.WorkflowExecutionWait
	err = a.DB.Preload("WorkflowExecution").
		Where("id = ? AND owner_user_id = ? AND kind = ?", id, ownerUserID, "human_action").
		Take(&wait).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, notFoundErr("workflow action")
	}
	if err != nil {
		return nil, err
	}
	return a.serializeWorkflowAction(&wait), nil
}

func (a *App) DecideWorkflowAction(
	ctx context.Context,
	rawID string,
	principal *Principal,
	decision WorkflowActionDecision,
	idempotencyKey string,
	reauthToken string,
) (M, error) {
	if principal == nil || principal.User == nil {
		return nil, ErrPermission
	}
	id, err := uuid.Parse(strings.TrimSpace(rawID))
	if err != nil {
		return nil, notFoundErr("workflow action")
	}
	decision.Decision = strings.ToLower(strings.TrimSpace(decision.Decision))
	if decision.Decision != "approved" && decision.Decision != "rejected" {
		return nil, bizErr("decision must be approved or rejected")
	}
	if decision.FormData == nil {
		decision.FormData = M{}
	}
	requestHash, err := canonicalRequestHash(decision)
	if err != nil {
		return nil, err
	}
	record, reused, err := a.reserveIdempotencyRecord(
		a.DB, principal.User.ID, "workflow-action:"+id.String(), idempotencyKey, requestHash,
	)
	if err != nil {
		return nil, err
	}
	_ = record

	var wait db.WorkflowExecutionWait
	err = a.DB.Transaction(func(tx *gorm.DB) error {
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Preload("WorkflowExecution").
			Where("id = ? AND owner_user_id = ? AND kind = ?", id, principal.User.ID, "human_action").
			Take(&wait).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return notFoundErr("workflow action")
		}
		if err != nil {
			return err
		}
		if reused && wait.Status != "pending" && wait.Status != "processing" {
			return nil
		}
		if wait.Status != "pending" {
			return ErrWorkflowActionConflict
		}
		if wait.ExpiresAt != nil && !wait.ExpiresAt.After(time.Now().UTC()) {
			return ErrWorkflowActionConflict
		}
		spec, err := requireWorkflowActionSpec(wait.ActionType)
		if err != nil {
			return err
		}
		if spec.RequiredPermission != "" && !principal.HasPermission(spec.RequiredPermission) {
			return ErrPermission
		}
		return tx.Model(&wait).Updates(map[string]any{"status": "processing", "updated_at": time.Now().UTC()}).Error
	})
	if err != nil {
		return nil, err
	}
	if reused && wait.Status != "pending" && wait.Status != "processing" {
		return a.GetWorkflowAction(id.String(), principal.User.ID)
	}
	if decision.Decision == "rejected" {
		if err := a.failWorkflowWait(ctx, &wait, "rejected", principal.User.ID, "Workflow action was rejected"); err != nil {
			return nil, err
		}
		return a.GetWorkflowAction(id.String(), principal.User.ID)
	}

	spec, _ := requireWorkflowActionSpec(wait.ActionType)
	if spec.RequiresReauth && !spec.DispatchConsumesReauth && !a.ConsumeReauthToken(reauthToken, principal) {
		a.DB.Model(&db.WorkflowExecutionWait{}).
			Where("id = ? AND owner_user_id = ? AND status = ?", wait.ID, principal.User.ID, "processing").
			Updates(map[string]any{"status": "pending", "updated_at": time.Now().UTC()})
		return nil, ErrWorkflowActionReauthentication
	}
	result, dispatchErr := spec.Dispatch(ctx, a, principal, wait.TargetID, decision.FormData, idempotencyKey, reauthToken)
	if dispatchErr != nil {
		a.DB.Model(&db.WorkflowExecutionWait{}).
			Where("id = ? AND owner_user_id = ? AND status = ?", wait.ID, principal.User.ID, "processing").
			Updates(map[string]any{"status": "pending", "updated_at": time.Now().UTC()})
		return nil, dispatchErr
	}
	if err := a.completeWorkflowWait(ctx, &wait, principal.User.ID, M{"decision": "approved", "result": workflowJSONValue(result)}); err != nil {
		return nil, err
	}
	return a.GetWorkflowAction(id.String(), principal.User.ID)
}

func (a *App) completeWorkflowWait(ctx context.Context, wait *db.WorkflowExecutionWait, resolvedBy int64, result M) error {
	now := time.Now().UTC()
	err := a.dbWithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var locked db.WorkflowExecutionWait
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", wait.ID).Take(&locked).Error; err != nil {
			return err
		}
		if locked.Status != "pending" && locked.Status != "processing" {
			return ErrWorkflowActionConflict
		}
		var execution db.WorkflowExecution
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND owner_user_id = ?", locked.WorkflowExecutionID, locked.OwnerUserID).Take(&execution).Error; err != nil {
			return err
		}
		if execution.Status == "canceled" || execution.Status == "cancel_requested" {
			return tx.Model(&locked).Updates(map[string]any{"status": "canceled", "resolved_at": now, "updated_at": now}).Error
		}
		if execution.Status != "waiting_job" && execution.Status != "waiting_action" {
			return ErrWorkflowActionConflict
		}
		state := loadJSONObject(execution.ContextSnapshotJSON)
		outputs, _ := state["nodeOutputs"].(map[string]any)
		if outputs == nil {
			outputs = M{}
			state["nodeOutputs"] = outputs
		}
		var nodeLog db.WorkflowExecutionNode
		if locked.WorkflowExecutionNodeID != nil {
			if err := tx.Where("id = ? AND workflow_execution_id = ?", *locked.WorkflowExecutionNodeID, execution.ID).Take(&nodeLog).Error; err != nil {
				return err
			}
			outputs[nodeLog.NodeID] = result
			if err := tx.Model(&nodeLog).Updates(map[string]any{
				"status": "success", "output_snapshot_json": serializeSnapshot(result, a.Cfg.Workflow.MaxOutputSnapshotBytes),
				"finished_at": now, "error_message": "",
			}).Error; err != nil {
				return err
			}
		}
		waitUpdates := map[string]any{
			"status": "completed", "result_json": serializeSnapshot(result, a.Cfg.Workflow.MaxOutputSnapshotBytes),
			"resolved_at": now, "updated_at": now,
		}
		if resolvedBy > 0 {
			waitUpdates["resolved_by"] = resolvedBy
		}
		if err := tx.Model(&locked).Updates(waitUpdates).Error; err != nil {
			return err
		}
		return tx.Model(&execution).Updates(map[string]any{
			"status": "queued", "queued_at": now, "claimed_at": nil, "finished_at": nil,
			"last_heartbeat_at": nil, "worker_id": nil, "next_retry_at": nil,
			"context_snapshot_json": serializeSnapshot(state, a.Cfg.Workflow.MaxOutputSnapshotBytes),
			"result_snapshot_json":  serializeSnapshot(outputs, a.Cfg.Workflow.MaxOutputSnapshotBytes),
			"failure_category":      "", "error_message": "",
		}).Error
	})
	if err == nil {
		a.wakeDispatcher()
	}
	return err
}

func (a *App) failWorkflowWait(ctx context.Context, wait *db.WorkflowExecutionWait, waitStatus string, resolvedBy int64, message string) error {
	now := time.Now().UTC()
	return a.dbWithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var locked db.WorkflowExecutionWait
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", wait.ID).Take(&locked).Error; err != nil {
			return err
		}
		if locked.Status != "pending" && locked.Status != "processing" {
			return ErrWorkflowActionConflict
		}
		updates := map[string]any{
			"status": waitStatus, "result_json": serializeSnapshot(M{"error": message}, a.Cfg.Workflow.MaxOutputSnapshotBytes),
			"resolved_at": now, "updated_at": now,
		}
		if resolvedBy > 0 {
			updates["resolved_by"] = resolvedBy
		}
		if err := tx.Model(&locked).Updates(updates).Error; err != nil {
			return err
		}
		var execution db.WorkflowExecution
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Preload("WorkflowDefinition").Where("id = ?", locked.WorkflowExecutionID).Take(&execution).Error; err != nil {
			return err
		}
		if execution.Status != "waiting_job" && execution.Status != "waiting_action" {
			return nil
		}
		startedAt := firstTime(execution.StartedAt, execution.ClaimedAt, &execution.QueuedAt)
		if err := tx.Model(&execution).Updates(map[string]any{
			"status": "failed", "finished_at": now, "duration_ms": now.Sub(startedAt).Milliseconds(),
			"failure_category": failureBusiness, "error_message": truncateRunes(message, 4000),
		}).Error; err != nil {
			return err
		}
		if locked.WorkflowExecutionNodeID != nil {
			if err := tx.Model(&db.WorkflowExecutionNode{}).Where("id = ?", *locked.WorkflowExecutionNodeID).
				Updates(map[string]any{"status": "failed", "finished_at": now, "error_message": truncateRunes(message, 4000)}).Error; err != nil {
				return err
			}
		}
		result, err := a.loadTerminalRunResult(tx, &execution, now)
		if err != nil {
			return err
		}
		return a.publishExecutionFailedWithDB(tx, result, message)
	})
}

func (a *App) serializeWorkflowAction(wait *db.WorkflowExecutionWait) M {
	title := wait.ActionType
	requiredPermission := ""
	requiresReauth := false
	formSchema := M{"type": "object", "properties": M{}}
	if spec, ok := workflowActionRegistry[wait.ActionType]; ok {
		title = spec.Title
		requiredPermission = spec.RequiredPermission
		requiresReauth = spec.RequiresReauth
		if spec.FormSchema != nil {
			formSchema = spec.FormSchema
		}
	}
	return M{
		"id": wait.ID.String(), "executionId": wait.WorkflowExecutionID,
		"actionType": wait.ActionType, "title": title,
		"targetType": wait.TargetType, "targetId": wait.TargetID,
		"status": wait.Status, "request": loadJSONObject(wait.RequestJSON),
		"result": loadJSONObject(wait.ResultJSON), "requiresReauth": requiresReauth,
		"requiredPermission": requiredPermission, "expiresAt": fmtTime(wait.ExpiresAt),
		"formSchema": formSchema,
		"resolvedAt": fmtTime(wait.ResolvedAt), "createdAt": fmtTimeV(wait.CreatedAt),
	}
}

func workflowJSONValue(value any) any {
	if value == nil {
		return M{}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return M{"status": "completed"}
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return M{"status": "completed"}
	}
	return snapshotSafeValue(decoded)
}

func (a *App) workflowWaitLoop(ctx context.Context) {
	interval := time.Duration(a.Cfg.Workflow.PollIntervalMs) * time.Millisecond
	if interval < 500*time.Millisecond {
		interval = 500 * time.Millisecond
	}
	for a.sleeping(ctx, interval) {
		a.reconcileWorkflowWaits(ctx)
	}
}

func (a *App) reconcileWorkflowWaits(ctx context.Context) {
	now := time.Now().UTC()
	var expired []db.WorkflowExecutionWait
	a.DB.WithContext(ctx).Where("status = ? AND expires_at IS NOT NULL AND expires_at <= ?", "pending", now).
		Order("expires_at ASC").Limit(100).Find(&expired)
	for i := range expired {
		_ = a.failWorkflowWait(ctx, &expired[i], "expired", 0, "Workflow action or job expired")
	}
	a.DB.WithContext(ctx).Model(&db.WorkflowExecutionWait{}).
		Where("kind = ? AND status = ? AND updated_at < ?", "human_action", "processing", now.Add(-5*time.Minute)).
		Updates(map[string]any{"status": "pending", "updated_at": now})

	var jobs []db.WorkflowExecutionWait
	a.DB.WithContext(ctx).Where("kind = ? AND status = ?", "worker_job", "pending").
		Order("created_at ASC").Limit(100).Find(&jobs)
	for i := range jobs {
		a.reconcileWorkerWait(ctx, &jobs[i])
	}
}

func (a *App) reconcileWorkerWait(ctx context.Context, wait *db.WorkflowExecutionWait) {
	request := loadJSONObject(wait.RequestJSON)
	workerTaskID := strings.TrimSpace(asString(request["workerTaskId"]))
	if workerTaskID == "" {
		_ = a.failWorkflowWait(ctx, wait, "rejected", 0, "Worker task reference is missing")
		return
	}
	var task struct {
		Status          string
		FailureCategory string
		ErrorMessage    string
	}
	if err := a.DB.WithContext(ctx).Raw(
		"SELECT status, COALESCE(failure_category, '') AS failure_category, COALESCE(error_message, '') AS error_message FROM worker_tasks WHERE id = ?",
		workerTaskID,
	).Scan(&task).Error; err != nil || task.Status == "" {
		return
	}
	switch task.Status {
	case "succeeded":
		_ = a.completeWorkflowWait(ctx, wait, 0, M{
			"status": "succeeded", "resourceType": wait.TargetType, "resourceId": wait.TargetID,
		})
	case "failed", "canceled":
		message := strings.TrimSpace(task.ErrorMessage)
		if message == "" {
			message = "Worker task " + task.Status
		}
		_ = a.failWorkflowWait(ctx, wait, "rejected", 0, message)
	}
}
