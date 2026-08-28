package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/plugin/sdk"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var errWorkflowWaiting = errors.New("workflow is waiting for a durable signal")

type WorkflowHumanTaskView struct {
	ID             int64  `json:"id"`
	WorkflowID     int64  `json:"workflowId"`
	RunID          int64  `json:"runId"`
	NodeInstanceID string `json:"nodeInstanceId"`
	TaskType       string `json:"taskType"`
	BusinessKey    string `json:"businessKey"`
	Prompt         string `json:"prompt"`
	Status         string `json:"status"`
	ExpiresAt      string `json:"expiresAt"`
	CreatedAt      string `json:"createdAt"`
	DecidedAt      string `json:"decidedAt,omitempty"`
}

type WorkflowHumanTaskDecision struct {
	Action string         `json:"action"`
	Data   map[string]any `json:"data,omitempty"`
}

func (a *App) workflowHumanApproval(ctx context.Context, run db.WorkflowRun, node workflowGraphNode, input json.RawMessage) (sdk.ActionResult, error) {
	var config struct {
		DecisionMode  string `json:"decisionMode"`
		TaskType      string `json:"taskType"`
		Prompt        string `json:"prompt"`
		ExpiresSecond int    `json:"expiresSeconds"`
	}
	var values struct {
		BusinessKey string `json:"businessKey"`
	}
	if json.Unmarshal(node.Config, &config) != nil || json.Unmarshal(input, &values) != nil ||
		strings.TrimSpace(values.BusinessKey) == "" || config.ExpiresSecond < 60 {
		return sdk.ActionResult{}, errors.New("human approval configuration is invalid")
	}
	now := time.Now().UTC()
	if config.DecisionMode == "auto" {
		return sdk.ActionResult{Output: mustJSON(map[string]any{
			"taskId": 0, "status": "approved", "decidedAt": formatWorkflowTime(now),
		})}, nil
	}
	if config.DecisionMode != "" && config.DecisionMode != "human" {
		return sdk.ActionResult{}, errors.New("human approval decision mode is invalid")
	}
	var task db.WorkflowHumanTask
	err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		businessKey := strings.TrimSpace(values.BusinessKey)
		identity := fmt.Sprintf("%d:%d:%s:%d:%s", run.WorkflowID, len(node.NodeInstanceID), node.NodeInstanceID, len(businessKey), businessKey)
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended(?, 0))`, identity).Error; err != nil {
			return errors.New("lock workflow human task identity failed")
		}
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("run_id = ? AND node_instance_id = ?", run.ID, node.NodeInstanceID).First(&task).Error
		if err == nil {
			if task.Status == "pending" && !task.ExpiresAt.After(now) {
				if err := finishWorkflowHumanTask(tx, &task, "expired", nil, nil, now); err != nil {
					return err
				}
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("load workflow human task failed")
		}
		var superseded []db.WorkflowHumanTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
			"workflow_id = ? AND node_instance_id = ? AND business_key = ? AND status = 'pending'",
			run.WorkflowID, node.NodeInstanceID, businessKey,
		).Find(&superseded).Error; err != nil {
			return errors.New("load superseded workflow human tasks failed")
		}
		for index := range superseded {
			if err := finishWorkflowHumanTask(tx, &superseded[index], "superseded", nil, nil, now); err != nil {
				return err
			}
			if err := resumeWorkflowHumanTaskRun(tx, superseded[index].RunID, now); err != nil {
				return err
			}
		}
		task = db.WorkflowHumanTask{
			WorkflowID: run.WorkflowID, RunID: run.ID, NodeInstanceID: node.NodeInstanceID,
			TaskType: strings.TrimSpace(config.TaskType), BusinessKey: businessKey,
			Prompt: strings.TrimSpace(config.Prompt), Status: "pending", ExpiresAt: now.Add(time.Duration(config.ExpiresSecond) * time.Second),
			DecisionJSON: `{}`, CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Create(&task).Error; err != nil {
			return errors.New("create workflow human task failed")
		}
		return nil
	})
	if err != nil {
		return sdk.ActionResult{}, err
	}
	if task.Status == "pending" {
		return sdk.ActionResult{}, errWorkflowWaiting
	}
	decidedAt := now
	if task.DecidedAt != nil {
		decidedAt = task.DecidedAt.UTC()
	}
	return sdk.ActionResult{Output: mustJSON(map[string]any{
		"taskId": task.ID, "status": task.Status, "decidedAt": formatWorkflowTime(decidedAt),
	})}, nil
}

func (a *App) ListWorkflowHumanTasks(ctx context.Context, status string) ([]WorkflowHumanTaskView, error) {
	status = strings.TrimSpace(status)
	if status != "" && status != "pending" && status != "approved" && status != "rejected" && status != "expired" && status != "superseded" {
		return nil, errors.New("invalid human task status")
	}
	query := a.DB.WithContext(ctx).Order("created_at DESC, id DESC").Limit(200)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var tasks []db.WorkflowHumanTask
	if err := query.Find(&tasks).Error; err != nil {
		return nil, errors.New("list workflow human tasks failed")
	}
	views := make([]WorkflowHumanTaskView, len(tasks))
	for index := range tasks {
		views[index] = workflowHumanTaskView(tasks[index])
	}
	return views, nil
}

func (a *App) DecideWorkflowHumanTask(ctx context.Context, taskID int64, payload WorkflowHumanTaskDecision, principal *Principal) (WorkflowHumanTaskView, error) {
	if principal == nil || principal.User == nil || principal.User.ID <= 0 {
		return WorkflowHumanTaskView{}, ErrPermission
	}
	action := strings.ToLower(strings.TrimSpace(payload.Action))
	if action != "approve" && action != "reject" {
		return WorkflowHumanTaskView{}, errors.New("human task action must be approve or reject")
	}
	if payload.Data == nil {
		payload.Data = map[string]any{}
	}
	decision, err := json.Marshal(payload.Data)
	if err != nil || len(decision) > 64<<10 {
		return WorkflowHumanTaskView{}, errors.New("human task decision exceeds the 64 KiB limit")
	}
	now := time.Now().UTC()
	var task db.WorkflowHumanTask
	expired := false
	err = a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&task, taskID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: workflow human task", ErrNotFound)
			}
			return errors.New("load workflow human task failed")
		}
		if task.Status != "pending" {
			return fmt.Errorf("%w: human task is already decided", ErrConflict)
		}
		if !task.ExpiresAt.After(now) {
			if err := finishWorkflowHumanTask(tx, &task, "expired", nil, nil, now); err != nil {
				return err
			}
			if err := resumeWorkflowHumanTaskRun(tx, task.RunID, now); err != nil {
				return err
			}
			expired = true
			return nil
		}
		status := "approved"
		if action == "reject" {
			status = "rejected"
		}
		actorID := principal.User.ID
		if err := finishWorkflowHumanTask(tx, &task, status, decision, &actorID, now); err != nil {
			return err
		}
		return resumeWorkflowHumanTaskRun(tx, task.RunID, now)
	})
	if err != nil {
		return WorkflowHumanTaskView{}, err
	}
	if expired {
		return WorkflowHumanTaskView{}, fmt.Errorf("%w: human task has expired", ErrConflict)
	}
	a.PublishWorkflowRunUpdated(task.WorkflowID, task.RunID)
	return workflowHumanTaskView(task), nil
}

func (a *App) expireWorkflowHumanTasks(ctx context.Context, now time.Time) error {
	return a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var tasks []db.WorkflowHumanTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("status = 'pending' AND expires_at <= ?", now).Order("expires_at, id").Limit(100).Find(&tasks).Error; err != nil {
			return errors.New("load expired workflow human tasks failed")
		}
		for index := range tasks {
			if err := finishWorkflowHumanTask(tx, &tasks[index], "expired", nil, nil, now); err != nil {
				return err
			}
			if err := resumeWorkflowHumanTaskRun(tx, tasks[index].RunID, now); err != nil {
				return err
			}
		}
		return nil
	})
}

func finishWorkflowHumanTask(tx *gorm.DB, task *db.WorkflowHumanTask, status string, decision []byte, actorID *int64, now time.Time) error {
	if len(decision) == 0 {
		decision = []byte(`{}`)
	}
	if err := tx.Model(task).Where("status = 'pending'").Updates(map[string]any{
		"status": status, "decision_json": string(decision), "decided_by": actorID,
		"decided_at": now, "updated_at": now,
	}).Error; err != nil {
		return errors.New("finish workflow human task failed")
	}
	task.Status, task.DecisionJSON, task.DecidedBy, task.DecidedAt, task.UpdatedAt = status, string(decision), actorID, &now, now
	return nil
}

func resumeWorkflowHumanTaskRun(tx *gorm.DB, runID int64, now time.Time) error {
	if err := tx.Model(&db.WorkflowRun{}).Where("id = ? AND status = ?", runID, RunStatusWaiting).Updates(map[string]any{
		"status": RunStatusQueued, "not_before": now, "lease_token": nil, "lease_expires_at": nil, "updated_at": now,
	}).Error; err != nil {
		return errors.New("resume workflow human task run failed")
	}
	return nil
}

func workflowHumanTaskView(task db.WorkflowHumanTask) WorkflowHumanTaskView {
	view := WorkflowHumanTaskView{
		ID: task.ID, WorkflowID: task.WorkflowID, RunID: task.RunID, NodeInstanceID: task.NodeInstanceID,
		TaskType: task.TaskType, BusinessKey: task.BusinessKey, Prompt: task.Prompt, Status: task.Status,
		ExpiresAt: formatWorkflowTime(task.ExpiresAt), CreatedAt: formatWorkflowTime(task.CreatedAt),
	}
	if task.DecidedAt != nil {
		view.DecidedAt = formatWorkflowTime(*task.DecidedAt)
	}
	return view
}
