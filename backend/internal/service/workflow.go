package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"coinsphere/backend/internal/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	WorkflowModeBatch        = "batch"
	WorkflowModeEvent        = "event"
	WorkflowModeStream       = "stream"
	WorkflowStatusInactive   = "inactive"
	WorkflowStatusActive     = "active"
	WorkflowStatusError      = "error"
	WorkflowTemplateBlank    = "blank"
	WorkflowTemplateSchedule = "scheduled"
	WorkflowTemplateEvent    = "event"
	maxWorkflowGraphBytes    = 1 << 20
)

const blankWorkflowGraph = `{
  "schemaVersion": 1,
  "nodes": [
    {"nodeInstanceId":"manual-trigger","nodeType":"core.manual","nodeVersion":"1.0.0","config":{},"position":{"x":160,"y":220}},
    {"nodeInstanceId":"end","nodeType":"core.end","nodeVersion":"1.0.0","config":{},"position":{"x":520,"y":220}}
  ],
  "edges": [
    {"edgeId":"manual-to-end","sourceNodeInstanceId":"manual-trigger","sourcePort":"out","targetNodeInstanceId":"end","targetPort":"in"}
  ]
}`

const scheduledWorkflowGraph = `{
  "schemaVersion": 1,
  "nodes": [
    {"nodeInstanceId":"schedule-trigger","nodeType":"core.schedule","nodeVersion":"1.0.0","config":{"everySeconds":3600},"position":{"x":160,"y":220}},
    {"nodeInstanceId":"end","nodeType":"core.end","nodeVersion":"1.0.0","config":{},"position":{"x":520,"y":220}}
  ],
  "edges": [
    {"edgeId":"schedule-to-end","sourceNodeInstanceId":"schedule-trigger","sourcePort":"out","targetNodeInstanceId":"end","targetPort":"in"}
  ]
}`

const eventWorkflowGraph = `{
  "schemaVersion": 1,
  "nodes": [
    {"nodeInstanceId":"event-trigger","nodeType":"core.event","nodeVersion":"1.0.0","config":{"types":["example.event"]},"position":{"x":160,"y":220}},
    {"nodeInstanceId":"end","nodeType":"core.end","nodeVersion":"1.0.0","config":{},"position":{"x":520,"y":220}}
  ],
  "edges": [
    {"edgeId":"event-to-end","sourceNodeInstanceId":"event-trigger","sourcePort":"out","targetNodeInstanceId":"end","targetPort":"in"}
  ]
}`

type WorkflowTemplate struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Mode        string `json:"mode"`
}

type WorkflowCreatePayload struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	TemplateKey string `json:"templateKey"`
	GroupID     *int64 `json:"groupId"`
}

type WorkflowUpdatePayload struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type WorkflowRevisionSavePayload struct {
	ExpectedActiveRevisionID  int64                  `json:"expectedActiveRevisionId"`
	Graph                     json.RawMessage        `json:"graph"`
	SecretChanges             []WorkflowSecretChange `json:"secretChanges,omitempty"`
	ResetStateNodeInstanceIDs []string               `json:"resetStateNodeInstanceIds,omitempty"`
}

type WorkflowLifecyclePayload struct {
	Action string `json:"action"`
}

type WorkflowView struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	Description       string `json:"description"`
	GroupID           *int64 `json:"groupId"`
	Mode              string `json:"mode"`
	Status            string `json:"status"`
	ActiveRevisionID  int64  `json:"activeRevisionId"`
	MainTriggerNodeID string `json:"mainTriggerNodeId"`
	RetentionDays     int    `json:"retentionDays"`
	CreatedBy         int64  `json:"createdBy"`
	CreatedAt         string `json:"createdAt"`
	UpdatedAt         string `json:"updatedAt"`
}

type WorkflowRuntimeView struct {
	MaxConcurrentRuns int    `json:"maxConcurrentRuns"`
	BacklogLimit      int    `json:"backlogLimit"`
	NextScheduledAt   string `json:"nextScheduledAt,omitempty"`
	LastScheduledAt   string `json:"lastScheduledAt,omitempty"`
	UpdatedAt         string `json:"updatedAt"`
}

type WorkflowDetail struct {
	WorkflowView
	Runtime              WorkflowRuntimeView `json:"runtime"`
	StateNodeInstanceIDs []string            `json:"stateNodeInstanceIds"`
}

type WorkflowRevisionView struct {
	ID                int64                      `json:"id"`
	WorkflowID        int64                      `json:"workflowId"`
	RevisionNumber    int64                      `json:"revisionNumber"`
	Graph             json.RawMessage            `json:"graph"`
	NodeVersions      json.RawMessage            `json:"nodeVersions"`
	MainTriggerNodeID string                     `json:"mainTriggerNodeId"`
	CreatedBy         int64                      `json:"createdBy"`
	CreatedAt         string                     `json:"createdAt"`
	SecretFields      map[string]map[string]bool `json:"secretFields"`
}

func (a *App) ListWorkflowTemplates() []WorkflowTemplate {
	items := []WorkflowTemplate{
		{Key: WorkflowTemplateBlank, Name: "空白工作流", Mode: WorkflowModeBatch, Description: "从手动开始节点创建空白流程。"},
		{Key: WorkflowTemplateSchedule, Name: "定时工作流", Mode: WorkflowModeBatch, Description: "按固定间隔或 Cron 调度流程。"},
		{Key: WorkflowTemplateEvent, Name: "事件工作流", Mode: WorkflowModeEvent, Description: "接收匹配的 CloudEvent 后运行。"},
	}
	if a.Plugins != nil {
		for _, template := range a.Plugins.Templates() {
			items = append(items, WorkflowTemplate{Key: template.Key, Name: template.Name, Description: template.Description, Mode: template.Mode})
		}
	}
	return items
}

func (a *App) workflowTemplate(key string) (json.RawMessage, bool) {
	core := map[string]string{WorkflowTemplateBlank: blankWorkflowGraph, WorkflowTemplateSchedule: scheduledWorkflowGraph, WorkflowTemplateEvent: eventWorkflowGraph}
	if graph := core[key]; graph != "" {
		return json.RawMessage(graph), true
	}
	if a.Plugins != nil {
		for _, template := range a.Plugins.Templates() {
			if template.Key == key {
				return append(json.RawMessage(nil), template.Graph...), true
			}
		}
	}
	return nil, false
}

func (a *App) CreateWorkflow(ctx context.Context, payload WorkflowCreatePayload, principal *Principal) (WorkflowDetail, error) {
	name := strings.TrimSpace(payload.Name)
	description := strings.TrimSpace(payload.Description)
	templateKey := strings.TrimSpace(payload.TemplateKey)
	if templateKey == "" {
		templateKey = WorkflowTemplateBlank
	}
	if name == "" || utf8.RuneCountInString(name) > 120 {
		return WorkflowDetail{}, errors.New("workflow name must contain 1 to 120 characters")
	}
	if utf8.RuneCountInString(description) > 500 {
		return WorkflowDetail{}, errors.New("workflow description must not exceed 500 characters")
	}
	templateGraph, ok := a.workflowTemplate(templateKey)
	if !ok {
		return WorkflowDetail{}, fmt.Errorf("unknown workflow template %q", templateKey)
	}
	if principal == nil || principal.User == nil || principal.User.ID <= 0 {
		return WorkflowDetail{}, ErrPermission
	}
	graph, err := a.validateWorkflowGraph(templateGraph)
	if err != nil {
		return WorkflowDetail{}, errors.New("workflow template is invalid")
	}

	now := time.Now().UTC()
	workflow := db.Workflow{
		Name: name, Description: description, Mode: workflowModeForTrigger(graph.nodes[graph.mainTriggerID].NodeType), Status: WorkflowStatusInactive,
		MainTriggerNodeID: graph.mainTriggerID, RetentionDays: 30, CreatedBy: principal.User.ID,
		CreatedAt: now, UpdatedAt: now,
	}
	err = a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := validateWorkflowGroupID(tx, payload.GroupID); err != nil {
			return err
		}
		var createErr error
		workflow, createErr = createWorkflowRecord(tx, name, description, payload.GroupID, graph, principal.User.ID, now)
		return createErr
	})
	if err != nil {
		return WorkflowDetail{}, err
	}
	return a.GetWorkflow(ctx, workflow.ID)
}

func createWorkflowRecord(tx *gorm.DB, name, description string, groupID *int64, graph validatedWorkflowGraph, userID int64, now time.Time) (db.Workflow, error) {
	workflow := db.Workflow{
		Name: name, Description: description, GroupID: groupID, Mode: workflowModeForTrigger(graph.nodes[graph.mainTriggerID].NodeType), Status: WorkflowStatusInactive,
		MainTriggerNodeID: graph.mainTriggerID, RetentionDays: 30, CreatedBy: userID, CreatedAt: now, UpdatedAt: now,
	}
	if err := tx.Create(&workflow).Error; err != nil {
		return db.Workflow{}, errors.New("create workflow failed")
	}
	revision := db.WorkflowRevision{
		WorkflowID: workflow.ID, RevisionNumber: 1, GraphJSON: graph.graphJSON,
		NodeVersions: graph.nodeVersionsJSON, MainTriggerNodeID: graph.mainTriggerID, CreatedBy: userID, CreatedAt: now,
	}
	if err := tx.Create(&revision).Error; err != nil {
		return db.Workflow{}, errors.New("create initial workflow revision failed")
	}
	workflow.ActiveRevisionID = &revision.ID
	if err := tx.Model(&db.Workflow{}).Where("id = ?", workflow.ID).Update("active_revision_id", revision.ID).Error; err != nil {
		return db.Workflow{}, errors.New("activate initial workflow revision failed")
	}
	if err := tx.Create(&db.WorkflowRuntime{
		WorkflowID: workflow.ID, MaxConcurrentRuns: 2, BacklogLimit: 100, UpdatedAt: now,
	}).Error; err != nil {
		return db.Workflow{}, errors.New("create workflow runtime failed")
	}
	return workflow, nil
}

func (a *App) ListWorkflows(ctx context.Context, status string) ([]WorkflowView, error) {
	status = strings.TrimSpace(status)
	if status != "" && !validWorkflowStatus(status) {
		return nil, errors.New("invalid workflow status")
	}
	query := a.DB.WithContext(ctx).Order("updated_at DESC, id DESC")
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var workflows []db.Workflow
	if err := query.Find(&workflows).Error; err != nil {
		return nil, errors.New("list workflows failed")
	}
	items := make([]WorkflowView, 0, len(workflows))
	for _, workflow := range workflows {
		items = append(items, workflowView(workflow))
	}
	return items, nil
}

func (a *App) GetWorkflow(ctx context.Context, workflowID int64) (WorkflowDetail, error) {
	var workflow db.Workflow
	if err := a.DB.WithContext(ctx).First(&workflow, workflowID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return WorkflowDetail{}, fmt.Errorf("%w: workflow", ErrNotFound)
		}
		return WorkflowDetail{}, errors.New("load workflow failed")
	}
	var runtime db.WorkflowRuntime
	if err := a.DB.WithContext(ctx).First(&runtime, "workflow_id = ?", workflowID).Error; err != nil {
		return WorkflowDetail{}, errors.New("load workflow runtime failed")
	}
	stateNodeInstanceIDs := make([]string, 0)
	if err := a.DB.WithContext(ctx).Model(&db.WorkflowNodeState{}).Where("workflow_id = ?", workflowID).
		Order("node_instance_id").Pluck("node_instance_id", &stateNodeInstanceIDs).Error; err != nil {
		return WorkflowDetail{}, errors.New("load workflow node states failed")
	}
	runtimeView := WorkflowRuntimeView{
		MaxConcurrentRuns: runtime.MaxConcurrentRuns, BacklogLimit: runtime.BacklogLimit,
		UpdatedAt: formatWorkflowTime(runtime.UpdatedAt),
	}
	if runtime.NextScheduledAt != nil {
		runtimeView.NextScheduledAt = formatWorkflowTime(*runtime.NextScheduledAt)
	}
	if runtime.LastScheduledAt != nil {
		runtimeView.LastScheduledAt = formatWorkflowTime(*runtime.LastScheduledAt)
	}
	return WorkflowDetail{
		WorkflowView:         workflowView(workflow),
		Runtime:              runtimeView,
		StateNodeInstanceIDs: stateNodeInstanceIDs,
	}, nil
}

func (a *App) UpdateWorkflow(ctx context.Context, workflowID int64, payload WorkflowUpdatePayload) (WorkflowDetail, error) {
	name := strings.TrimSpace(payload.Name)
	description := strings.TrimSpace(payload.Description)
	if name == "" || utf8.RuneCountInString(name) > 120 {
		return WorkflowDetail{}, errors.New("workflow name must contain 1 to 120 characters")
	}
	if utf8.RuneCountInString(description) > 500 {
		return WorkflowDetail{}, errors.New("workflow description must not exceed 500 characters")
	}
	database := a.DB.WithContext(ctx)
	result := database.Model(&db.Workflow{}).Where("id = ?", workflowID).
		Updates(map[string]any{
			"name": name, "description": description, "updated_at": time.Now().UTC(),
		})
	if result.Error != nil {
		return WorkflowDetail{}, errors.New("update workflow failed")
	}
	if result.RowsAffected == 0 {
		var workflow db.Workflow
		if err := database.Select("id").First(&workflow, workflowID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return WorkflowDetail{}, fmt.Errorf("%w: workflow", ErrNotFound)
			}
			return WorkflowDetail{}, errors.New("load workflow failed")
		}
		return WorkflowDetail{}, errors.New("update workflow failed")
	}
	return a.GetWorkflow(ctx, workflowID)
}

func (a *App) SaveWorkflowRevision(ctx context.Context, workflowID int64, payload WorkflowRevisionSavePayload, principal *Principal) (WorkflowRevisionView, error) {
	if payload.ExpectedActiveRevisionID <= 0 {
		return WorkflowRevisionView{}, errors.New("expectedActiveRevisionId must be positive")
	}
	if principal == nil || principal.User == nil || principal.User.ID <= 0 {
		return WorkflowRevisionView{}, ErrPermission
	}
	graph, err := a.validateWorkflowGraph(payload.Graph)
	if err != nil {
		return WorkflowRevisionView{}, err
	}
	secretChanges, err := validateWorkflowSecretChanges(graph, payload.SecretChanges)
	if err != nil {
		return WorkflowRevisionView{}, err
	}
	resetStateNodeIDs := make(map[string]bool, len(payload.ResetStateNodeInstanceIDs))
	for _, rawNodeID := range payload.ResetStateNodeInstanceIDs {
		nodeID := strings.TrimSpace(rawNodeID)
		if !workflowNodeIDPattern.MatchString(nodeID) {
			return WorkflowRevisionView{}, errors.New("resetStateNodeInstanceIds contains an invalid nodeInstanceId")
		}
		if resetStateNodeIDs[nodeID] {
			return WorkflowRevisionView{}, fmt.Errorf("duplicate state reset for node %q", nodeID)
		}
		resetStateNodeIDs[nodeID] = true
	}

	var revision db.WorkflowRevision
	err = a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var workflow db.Workflow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&workflow, workflowID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: workflow", ErrNotFound)
			}
			return errors.New("lock workflow failed")
		}
		if workflow.ActiveRevisionID == nil || *workflow.ActiveRevisionID != payload.ExpectedActiveRevisionID {
			return fmt.Errorf("%w: active workflow revision changed", ErrConflict)
		}
		var activeRevision db.WorkflowRevision
		if err := tx.Where("workflow_id = ? AND id = ?", workflowID, *workflow.ActiveRevisionID).First(&activeRevision).Error; err != nil {
			return errors.New("load active workflow revision failed")
		}
		activeGraph, err := a.validateWorkflowGraph(json.RawMessage(activeRevision.GraphJSON))
		if err != nil {
			return errors.New("active workflow revision graph is invalid")
		}
		var states []db.WorkflowNodeState
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("workflow_id = ?", workflowID).Find(&states).Error; err != nil {
			return errors.New("load workflow node states failed")
		}
		requiredStateResets := make(map[string]bool)
		for _, state := range states {
			previous, existed := activeGraph.nodeVersions[state.NodeInstanceID]
			next, remains := graph.nodeVersions[state.NodeInstanceID]
			if !existed || !remains || previous != next {
				requiredStateResets[state.NodeInstanceID] = true
			}
		}
		if len(resetStateNodeIDs) != len(requiredStateResets) {
			return workflowStateResetConflict(requiredStateResets)
		}
		for nodeID := range resetStateNodeIDs {
			if !requiredStateResets[nodeID] {
				return workflowStateResetConflict(requiredStateResets)
			}
		}
		if len(requiredStateResets) > 0 && workflow.Status != WorkflowStatusInactive {
			return fmt.Errorf("%w: workflow must be inactive before resetting node state", ErrConflict)
		}
		if len(requiredStateResets) > 0 {
			nodeIDs := make([]string, 0, len(requiredStateResets))
			for nodeID := range requiredStateResets {
				nodeIDs = append(nodeIDs, nodeID)
			}
			if err := tx.Where("workflow_id = ? AND node_instance_id IN ?", workflowID, nodeIDs).
				Delete(&db.WorkflowNodeState{}).Error; err != nil {
				return errors.New("reset workflow node states failed")
			}
		}
		var latest int64
		if err := tx.Model(&db.WorkflowRevision{}).Where("workflow_id = ?", workflowID).
			Select("COALESCE(MAX(revision_number), 0)").Scan(&latest).Error; err != nil {
			return errors.New("read latest workflow revision failed")
		}
		now := time.Now().UTC()
		revision = db.WorkflowRevision{
			WorkflowID: workflowID, RevisionNumber: latest + 1, GraphJSON: graph.graphJSON,
			NodeVersions: graph.nodeVersionsJSON, MainTriggerNodeID: graph.mainTriggerID,
			CreatedBy: principal.User.ID, CreatedAt: now,
		}
		if err := tx.Create(&revision).Error; err != nil {
			return errors.New("create workflow revision failed")
		}
		if err := a.persistWorkflowSecrets(tx, workflowID, *workflow.ActiveRevisionID, revision, activeGraph, graph, secretChanges, now); err != nil {
			return err
		}
		if err := tx.Model(&db.Workflow{}).Where("id = ?", workflowID).Updates(map[string]any{
			"active_revision_id": revision.ID, "main_trigger_node_id": graph.mainTriggerID,
			"mode": workflowModeForTrigger(graph.nodes[graph.mainTriggerID].NodeType), "updated_at": now,
		}).Error; err != nil {
			return errors.New("activate workflow revision failed")
		}
		if workflow.Status == WorkflowStatusActive {
			nextScheduledAt := any(nil)
			trigger := graph.nodes[graph.mainTriggerID]
			if trigger.NodeType == "core.schedule" {
				next, err := nextWorkflowScheduledAt(trigger.Config, now)
				if err != nil {
					return errors.New("schedule config is invalid")
				}
				nextScheduledAt = next
			}
			if err := tx.Model(&db.WorkflowRuntime{}).Where("workflow_id = ?", workflowID).Updates(map[string]any{
				"next_scheduled_at": nextScheduledAt, "updated_at": now,
			}).Error; err != nil {
				return errors.New("update workflow runtime schedule failed")
			}
		}
		return nil
	})
	if err != nil {
		return WorkflowRevisionView{}, err
	}
	views := []WorkflowRevisionView{workflowRevisionView(revision)}
	if err := a.attachWorkflowRevisionSecrets(ctx, workflowID, views); err != nil {
		return WorkflowRevisionView{}, err
	}
	return views[0], nil
}

func workflowStateResetConflict(required map[string]bool) error {
	nodeIDs := make([]string, 0, len(required))
	for nodeID := range required {
		nodeIDs = append(nodeIDs, nodeID)
	}
	sort.Strings(nodeIDs)
	if len(nodeIDs) == 0 {
		return errors.New("resetStateNodeInstanceIds does not match a destructive state change")
	}
	return fmt.Errorf("%w: destructive edits require resetStateNodeInstanceIds for %s", ErrConflict, strings.Join(nodeIDs, ", "))
}

func (a *App) ListWorkflowRevisions(ctx context.Context, workflowID int64) ([]WorkflowRevisionView, error) {
	var count int64
	if err := a.DB.WithContext(ctx).Model(&db.Workflow{}).Where("id = ?", workflowID).Count(&count).Error; err != nil {
		return nil, errors.New("load workflow failed")
	}
	if count == 0 {
		return nil, fmt.Errorf("%w: workflow", ErrNotFound)
	}
	var revisions []db.WorkflowRevision
	if err := a.DB.WithContext(ctx).Where("workflow_id = ?", workflowID).
		Order("revision_number DESC").Find(&revisions).Error; err != nil {
		return nil, errors.New("list workflow revisions failed")
	}
	items := make([]WorkflowRevisionView, 0, len(revisions))
	for _, revision := range revisions {
		items = append(items, workflowRevisionView(revision))
	}
	if err := a.attachWorkflowRevisionSecrets(ctx, workflowID, items); err != nil {
		return nil, err
	}
	return items, nil
}

func (a *App) GetWorkflowRevision(ctx context.Context, workflowID, revisionID int64) (WorkflowRevisionView, error) {
	var revision db.WorkflowRevision
	if err := a.DB.WithContext(ctx).Where("workflow_id = ? AND id = ?", workflowID, revisionID).First(&revision).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return WorkflowRevisionView{}, fmt.Errorf("%w: workflow revision", ErrNotFound)
		}
		return WorkflowRevisionView{}, errors.New("load workflow revision failed")
	}
	views := []WorkflowRevisionView{workflowRevisionView(revision)}
	if err := a.attachWorkflowRevisionSecrets(ctx, workflowID, views); err != nil {
		return WorkflowRevisionView{}, err
	}
	return views[0], nil
}

func (a *App) ApplyWorkflowLifecycle(ctx context.Context, workflowID int64, payload WorkflowLifecyclePayload) (WorkflowDetail, error) {
	action := strings.ToLower(strings.TrimSpace(payload.Action))
	err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var workflow db.Workflow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&workflow, workflowID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: workflow", ErrNotFound)
			}
			return errors.New("lock workflow failed")
		}
		next, err := nextWorkflowStatus(workflow.Status, action)
		if err != nil {
			return err
		}
		if action == "activate" && workflow.ActiveRevisionID == nil {
			return fmt.Errorf("%w: workflow is not startable", ErrConflict)
		}
		now := time.Now().UTC()
		updates := map[string]any{"status": next, "updated_at": now}
		var runtimeUpdates map[string]any
		if action == "activate" {
			var revision db.WorkflowRevision
			if err := tx.First(&revision, *workflow.ActiveRevisionID).Error; err != nil {
				return errors.New("load active workflow revision failed")
			}
			validated, err := a.validateWorkflowGraph(json.RawMessage(revision.GraphJSON))
			if err != nil {
				return fmt.Errorf("%w: active workflow revision is invalid", ErrConflict)
			}
			if err := ensureWorkflowRevisionSecrets(tx, workflow.ID, revision.ID, validated); err != nil {
				return err
			}
			runtimeUpdates = map[string]any{"updated_at": now, "next_scheduled_at": nil}
			trigger := validated.nodes[validated.mainTriggerID]
			if trigger.NodeType == "core.schedule" {
				next, err := nextWorkflowScheduledAt(trigger.Config, now)
				if err != nil {
					return fmt.Errorf("%w: schedule config is invalid", ErrConflict)
				}
				runtimeUpdates["next_scheduled_at"] = next
			}
		} else if action == "deactivate" {
			runtimeUpdates = map[string]any{"updated_at": now, "next_scheduled_at": nil}
		}
		if err := tx.Model(&db.Workflow{}).Where("id = ?", workflowID).Updates(updates).Error; err != nil {
			return errors.New("update workflow lifecycle failed")
		}
		if runtimeUpdates != nil {
			if err := tx.Model(&db.WorkflowRuntime{}).Where("workflow_id = ?", workflowID).Updates(runtimeUpdates).Error; err != nil {
				return errors.New("update workflow runtime schedule failed")
			}
		}
		return nil
	})
	if err != nil {
		return WorkflowDetail{}, err
	}
	if action == "deactivate" {
		a.stopWorkflowTrigger(workflowID)
	}
	return a.GetWorkflow(ctx, workflowID)
}

func nextWorkflowStatus(current, action string) (string, error) {
	switch action {
	case "activate":
		if current == WorkflowStatusInactive {
			return WorkflowStatusActive, nil
		}
	case "deactivate":
		if current == WorkflowStatusActive || current == WorkflowStatusError {
			return WorkflowStatusInactive, nil
		}
	default:
		return "", errors.New("lifecycle action must be activate or deactivate")
	}
	return "", fmt.Errorf("%w: cannot %s workflow from %s", ErrConflict, action, current)
}

func workflowView(workflow db.Workflow) WorkflowView {
	activeRevisionID := int64(0)
	if workflow.ActiveRevisionID != nil {
		activeRevisionID = *workflow.ActiveRevisionID
	}
	view := WorkflowView{
		ID: workflow.ID, Name: workflow.Name, Description: workflow.Description, GroupID: workflow.GroupID, Mode: workflow.Mode,
		Status: workflow.Status, ActiveRevisionID: activeRevisionID,
		MainTriggerNodeID: workflow.MainTriggerNodeID, RetentionDays: workflow.RetentionDays,
		CreatedBy: workflow.CreatedBy, CreatedAt: formatWorkflowTime(workflow.CreatedAt),
		UpdatedAt: formatWorkflowTime(workflow.UpdatedAt),
	}
	return view
}

func workflowRevisionView(revision db.WorkflowRevision) WorkflowRevisionView {
	return WorkflowRevisionView{
		ID: revision.ID, WorkflowID: revision.WorkflowID, RevisionNumber: revision.RevisionNumber,
		Graph: json.RawMessage(revision.GraphJSON), NodeVersions: json.RawMessage(revision.NodeVersions),
		MainTriggerNodeID: revision.MainTriggerNodeID, CreatedBy: revision.CreatedBy,
		CreatedAt: formatWorkflowTime(revision.CreatedAt), SecretFields: map[string]map[string]bool{},
	}
}

func formatWorkflowTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func validWorkflowStatus(status string) bool {
	return status == WorkflowStatusInactive || status == WorkflowStatusActive || status == WorkflowStatusError
}

func workflowModeForTrigger(nodeType string) string {
	switch nodeType {
	case "core.manual", "core.schedule", "official.connector.webhook":
		return WorkflowModeBatch
	case "core.event":
		return WorkflowModeEvent
	default:
		return WorkflowModeStream
	}
}
