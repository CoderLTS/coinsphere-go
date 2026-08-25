package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"coinsphere/backend/internal/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	WorkflowModeBatch         = "batch"
	WorkflowModeEvent         = "event"
	WorkflowModeStream        = "stream"
	WorkflowStatusPaused      = "paused"
	WorkflowStatusRunning     = "running"
	WorkflowStatusAttention   = "needs_attention"
	WorkflowStatusArchived    = "archived"
	WorkflowTemplateBlank     = "blank"
	WorkflowTemplateSchedule  = "scheduled"
	WorkflowTemplateEvent     = "event"
	WorkflowTemplateFailure   = "failure-handler"
	WorkflowTemplateWebhook   = "connector-webhook"
	WorkflowTemplateWebSocket = "connector-websocket"
	maxWorkflowGraphBytes     = 1 << 20
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

const failureWorkflowGraph = `{
  "schemaVersion": 1,
  "nodes": [
    {"nodeInstanceId":"failure-trigger","nodeType":"core.event","nodeVersion":"1.0.0","config":{"types":["io.coinsphere.workflow.batch.failed"],"source":"urn:coinsphere:workflow-core"},"position":{"x":160,"y":220}},
    {"nodeInstanceId":"end","nodeType":"core.end","nodeVersion":"1.0.0","config":{},"position":{"x":520,"y":220}}
  ],
  "edges": [
    {"edgeId":"failure-to-end","sourceNodeInstanceId":"failure-trigger","sourcePort":"out","targetNodeInstanceId":"end","targetPort":"in"}
  ]
}`

const webhookWorkflowGraph = `{
  "schemaVersion": 1,
  "nodes": [
    {"nodeInstanceId":"webhook-trigger","nodeType":"official.connector.webhook","nodeVersion":"1.0.0","config":{"eventType":"example.webhook"},"position":{"x":160,"y":220}},
    {"nodeInstanceId":"end","nodeType":"core.end","nodeVersion":"1.0.0","config":{},"position":{"x":520,"y":220}}
  ],
  "edges": [
    {"edgeId":"webhook-to-end","sourceNodeInstanceId":"webhook-trigger","sourcePort":"out","targetNodeInstanceId":"end","targetPort":"in"}
  ]
}`

const webSocketWorkflowGraph = `{
  "schemaVersion": 1,
  "nodes": [
    {"nodeInstanceId":"websocket-trigger","nodeType":"official.connector.websocket","nodeVersion":"1.0.0","config":{"url":"wss://stream.example.com/events","eventType":"example.event","idField":"id","partitionField":"partitionKey","useAuthorization":false},"position":{"x":160,"y":220}},
    {"nodeInstanceId":"end","nodeType":"core.end","nodeVersion":"1.0.0","config":{},"position":{"x":520,"y":220}}
  ],
  "edges": [
    {"edgeId":"websocket-to-end","sourceNodeInstanceId":"websocket-trigger","sourcePort":"out","targetNodeInstanceId":"end","targetPort":"in"}
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
}

type WorkflowRevisionSavePayload struct {
	ExpectedActiveRevisionID int64                  `json:"expectedActiveRevisionId"`
	Graph                    json.RawMessage        `json:"graph"`
	SecretChanges            []WorkflowSecretChange `json:"secretChanges,omitempty"`
}

type WorkflowLifecyclePayload struct {
	Action string `json:"action"`
}

type WorkflowView struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	Description       string `json:"description"`
	Mode              string `json:"mode"`
	Status            string `json:"status"`
	ActiveRevisionID  int64  `json:"activeRevisionId"`
	MainTriggerNodeID string `json:"mainTriggerNodeId"`
	RetentionDays     int    `json:"retentionDays"`
	CreatedBy         int64  `json:"createdBy"`
	CreatedAt         string `json:"createdAt"`
	UpdatedAt         string `json:"updatedAt"`
	ArchivedAt        string `json:"archivedAt,omitempty"`
}

type WorkflowRuntimeView struct {
	ActivityCursor int64  `json:"activityCursor"`
	HealthSummary  string `json:"healthSummary"`
	UpdatedAt      string `json:"updatedAt"`
}

type WorkflowDetail struct {
	WorkflowView
	Runtime WorkflowRuntimeView `json:"runtime"`
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
	return []WorkflowTemplate{
		{Key: WorkflowTemplateBlank, Name: "Blank batch workflow", Mode: WorkflowModeBatch, Description: "Manual trigger connected to an end node."},
		{Key: WorkflowTemplateSchedule, Name: "Scheduled batch workflow", Mode: WorkflowModeBatch, Description: "UTC interval trigger connected to an end node."},
		{Key: WorkflowTemplateEvent, Name: "Event workflow", Mode: WorkflowModeEvent, Description: "CloudEvent trigger connected to an end node."},
		{Key: WorkflowTemplateFailure, Name: "Failure handler", Mode: WorkflowModeEvent, Description: "Standard workflow failure trigger connected to an end node."},
		{Key: WorkflowTemplateWebhook, Name: "Connector webhook", Mode: WorkflowModeBatch, Description: "Authenticated webhook trigger connected to an end node."},
		{Key: WorkflowTemplateWebSocket, Name: "Connector WebSocket", Mode: WorkflowModeStream, Description: "Public WebSocket stream trigger connected to an end node."},
	}
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
	if templateKey != WorkflowTemplateBlank && templateKey != WorkflowTemplateSchedule &&
		templateKey != WorkflowTemplateEvent && templateKey != WorkflowTemplateFailure &&
		templateKey != WorkflowTemplateWebhook && templateKey != WorkflowTemplateWebSocket {
		return WorkflowDetail{}, fmt.Errorf("unknown workflow template %q", templateKey)
	}
	if principal == nil || principal.User == nil || principal.User.ID <= 0 {
		return WorkflowDetail{}, ErrPermission
	}
	templateGraph := blankWorkflowGraph
	if templateKey == WorkflowTemplateSchedule {
		templateGraph = scheduledWorkflowGraph
	} else if templateKey == WorkflowTemplateEvent {
		templateGraph = eventWorkflowGraph
	} else if templateKey == WorkflowTemplateFailure {
		templateGraph = failureWorkflowGraph
	} else if templateKey == WorkflowTemplateWebhook {
		templateGraph = webhookWorkflowGraph
	} else if templateKey == WorkflowTemplateWebSocket {
		templateGraph = webSocketWorkflowGraph
	}
	graph, err := a.validateWorkflowGraph(json.RawMessage(templateGraph))
	if err != nil {
		return WorkflowDetail{}, errors.New("workflow template is invalid")
	}

	now := time.Now().UTC()
	workflow := db.Workflow{
		Name: name, Description: description, Mode: workflowModeForTrigger(graph.nodes[graph.mainTriggerID].NodeType), Status: WorkflowStatusPaused,
		MainTriggerNodeID: graph.mainTriggerID, RetentionDays: 30, CreatedBy: principal.User.ID,
		CreatedAt: now, UpdatedAt: now,
	}
	err = a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&workflow).Error; err != nil {
			return errors.New("create workflow failed")
		}
		revision := db.WorkflowRevision{
			WorkflowID: workflow.ID, RevisionNumber: 1, GraphJSON: graph.graphJSON,
			NodeVersions: graph.nodeVersionsJSON, MainTriggerNodeID: graph.mainTriggerID,
			CreatedBy: principal.User.ID, CreatedAt: now,
		}
		if err := tx.Create(&revision).Error; err != nil {
			return errors.New("create initial workflow revision failed")
		}
		workflow.ActiveRevisionID = &revision.ID
		if err := tx.Model(&db.Workflow{}).Where("id = ?", workflow.ID).Update("active_revision_id", revision.ID).Error; err != nil {
			return errors.New("activate initial workflow revision failed")
		}
		if err := tx.Create(&db.WorkflowRuntime{
			WorkflowID: workflow.ID, ActivityCursor: 0, HealthSummary: "idle",
			MaxConcurrentBatches: 2, BacklogLimit: 100, UpdatedAt: now,
		}).Error; err != nil {
			return errors.New("create workflow runtime failed")
		}
		return nil
	})
	if err != nil {
		return WorkflowDetail{}, err
	}
	return a.GetWorkflow(ctx, workflow.ID)
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
	return WorkflowDetail{
		WorkflowView: workflowView(workflow),
		Runtime: WorkflowRuntimeView{
			ActivityCursor: runtime.ActivityCursor, HealthSummary: runtime.HealthSummary,
			UpdatedAt: formatWorkflowTime(runtime.UpdatedAt),
		},
	}, nil
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

	var revision db.WorkflowRevision
	err = a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var workflow db.Workflow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&workflow, workflowID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: workflow", ErrNotFound)
			}
			return errors.New("lock workflow failed")
		}
		if workflow.Status == WorkflowStatusArchived {
			return fmt.Errorf("%w: archived workflow is read-only", ErrConflict)
		}
		if workflow.ActiveRevisionID == nil || *workflow.ActiveRevisionID != payload.ExpectedActiveRevisionID {
			return fmt.Errorf("%w: active workflow revision changed", ErrConflict)
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
		if err := a.persistWorkflowSecrets(tx, workflowID, *workflow.ActiveRevisionID, revision, graph, secretChanges, now); err != nil {
			return err
		}
		if err := tx.Model(&db.Workflow{}).Where("id = ?", workflowID).Updates(map[string]any{
			"active_revision_id": revision.ID, "main_trigger_node_id": graph.mainTriggerID,
			"mode": workflowModeForTrigger(graph.nodes[graph.mainTriggerID].NodeType), "updated_at": now,
		}).Error; err != nil {
			return errors.New("activate workflow revision failed")
		}
		if workflow.Status == WorkflowStatusRunning {
			nextScheduledAt := any(nil)
			trigger := graph.nodes[graph.mainTriggerID]
			if trigger.NodeType == "core.schedule" {
				interval, err := scheduleInterval(trigger.Config)
				if err != nil {
					return errors.New("schedule config is invalid")
				}
				nextScheduledAt = now.Add(interval)
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
		if action == "start" && workflow.ActiveRevisionID == nil {
			return fmt.Errorf("%w: workflow is not startable", ErrConflict)
		}
		now := time.Now().UTC()
		updates := map[string]any{"status": next, "updated_at": now}
		var runtimeUpdates map[string]any
		if action == "start" {
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
			var graph workflowGraph
			if json.Unmarshal([]byte(validated.graphJSON), &graph) == nil {
				for _, node := range graph.Nodes {
					if node.NodeInstanceID == revision.MainTriggerNodeID && node.NodeType == "core.schedule" {
						interval, err := scheduleInterval(node.Config)
						if err != nil {
							return fmt.Errorf("%w: schedule config is invalid", ErrConflict)
						}
						runtimeUpdates["next_scheduled_at"] = now.Add(interval)
					}
				}
			}
		} else if action == "pause" {
			runtimeUpdates = map[string]any{"health_summary": "idle", "updated_at": now, "next_scheduled_at": nil}
		}
		if next == WorkflowStatusArchived {
			updates["archived_at"] = now
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
	if action == "pause" || action == "archive" {
		a.stopWorkflowTrigger(workflowID)
	}
	return a.GetWorkflow(ctx, workflowID)
}

func nextWorkflowStatus(current, action string) (string, error) {
	switch action {
	case "start":
		if current == WorkflowStatusPaused {
			return WorkflowStatusRunning, nil
		}
	case "pause":
		if current == WorkflowStatusRunning || current == WorkflowStatusAttention {
			return WorkflowStatusPaused, nil
		}
	case "archive":
		if current == WorkflowStatusPaused || current == WorkflowStatusAttention {
			return WorkflowStatusArchived, nil
		}
	default:
		return "", errors.New("lifecycle action must be start, pause, or archive")
	}
	return "", fmt.Errorf("%w: cannot %s workflow from %s", ErrConflict, action, current)
}

func workflowView(workflow db.Workflow) WorkflowView {
	activeRevisionID := int64(0)
	if workflow.ActiveRevisionID != nil {
		activeRevisionID = *workflow.ActiveRevisionID
	}
	view := WorkflowView{
		ID: workflow.ID, Name: workflow.Name, Description: workflow.Description, Mode: workflow.Mode,
		Status: workflow.Status, ActiveRevisionID: activeRevisionID,
		MainTriggerNodeID: workflow.MainTriggerNodeID, RetentionDays: workflow.RetentionDays,
		CreatedBy: workflow.CreatedBy, CreatedAt: formatWorkflowTime(workflow.CreatedAt),
		UpdatedAt: formatWorkflowTime(workflow.UpdatedAt),
	}
	if workflow.ArchivedAt != nil {
		view.ArchivedAt = formatWorkflowTime(*workflow.ArchivedAt)
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
	return status == WorkflowStatusPaused || status == WorkflowStatusRunning ||
		status == WorkflowStatusAttention || status == WorkflowStatusArchived
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
