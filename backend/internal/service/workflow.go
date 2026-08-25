package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"coinsphere/backend/internal/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	WorkflowModeBatch       = "batch"
	WorkflowStatusPaused    = "paused"
	WorkflowStatusRunning   = "running"
	WorkflowStatusAttention = "needs_attention"
	WorkflowStatusArchived  = "archived"
	WorkflowTemplateBlank   = "blank"
	maxWorkflowGraphBytes   = 1 << 20
)

var workflowNodeIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

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
	ExpectedActiveRevisionID int64           `json:"expectedActiveRevisionId"`
	Graph                    json.RawMessage `json:"graph"`
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
	ID                int64           `json:"id"`
	WorkflowID        int64           `json:"workflowId"`
	RevisionNumber    int64           `json:"revisionNumber"`
	Graph             json.RawMessage `json:"graph"`
	NodeVersions      json.RawMessage `json:"nodeVersions"`
	MainTriggerNodeID string          `json:"mainTriggerNodeId"`
	CreatedBy         int64           `json:"createdBy"`
	CreatedAt         string          `json:"createdAt"`
}

type validatedWorkflowGraph struct {
	graphJSON        string
	nodeVersionsJSON string
	mainTriggerID    string
}

type workflowGraphNode struct {
	NodeInstanceID string          `json:"nodeInstanceId"`
	NodeType       string          `json:"nodeType"`
	NodeVersion    string          `json:"nodeVersion"`
	Config         json.RawMessage `json:"config"`
	Position       *struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
	} `json:"position"`
}

type workflowGraphEdge struct {
	EdgeID               string `json:"edgeId"`
	SourceNodeInstanceID string `json:"sourceNodeInstanceId"`
	SourcePort           string `json:"sourcePort"`
	TargetNodeInstanceID string `json:"targetNodeInstanceId"`
	TargetPort           string `json:"targetPort"`
}

func (a *App) ListWorkflowTemplates() []WorkflowTemplate {
	return []WorkflowTemplate{{
		Key: WorkflowTemplateBlank, Name: "Blank batch workflow", Mode: WorkflowModeBatch,
		Description: "Manual trigger connected to an end node.",
	}}
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
	if templateKey != WorkflowTemplateBlank {
		return WorkflowDetail{}, fmt.Errorf("unknown workflow template %q", templateKey)
	}
	if principal == nil || principal.User == nil || principal.User.ID <= 0 {
		return WorkflowDetail{}, ErrPermission
	}
	graph, err := validateWorkflowGraph(json.RawMessage(blankWorkflowGraph))
	if err != nil {
		return WorkflowDetail{}, errors.New("blank workflow template is invalid")
	}

	now := time.Now().UTC()
	workflow := db.Workflow{
		Name: name, Description: description, Mode: WorkflowModeBatch, Status: WorkflowStatusPaused,
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
			WorkflowID: workflow.ID, ActivityCursor: 0, HealthSummary: "idle", UpdatedAt: now,
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
	graph, err := validateWorkflowGraph(payload.Graph)
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
		if err := tx.Model(&db.Workflow{}).Where("id = ?", workflowID).Updates(map[string]any{
			"active_revision_id": revision.ID, "main_trigger_node_id": graph.mainTriggerID, "updated_at": now,
		}).Error; err != nil {
			return errors.New("activate workflow revision failed")
		}
		return nil
	})
	if err != nil {
		return WorkflowRevisionView{}, err
	}
	return workflowRevisionView(revision), nil
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
	return workflowRevisionView(revision), nil
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
		if action == "start" && (workflow.Mode != WorkflowModeBatch || workflow.ActiveRevisionID == nil) {
			return fmt.Errorf("%w: workflow is not startable", ErrConflict)
		}
		now := time.Now().UTC()
		updates := map[string]any{"status": next, "updated_at": now}
		if next == WorkflowStatusArchived {
			updates["archived_at"] = now
		}
		if err := tx.Model(&db.Workflow{}).Where("id = ?", workflowID).Updates(updates).Error; err != nil {
			return errors.New("update workflow lifecycle failed")
		}
		return nil
	})
	if err != nil {
		return WorkflowDetail{}, err
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
		if current == WorkflowStatusRunning {
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

func validateWorkflowGraph(raw json.RawMessage) (validatedWorkflowGraph, error) {
	if len(raw) == 0 || len(raw) > maxWorkflowGraphBytes {
		return validatedWorkflowGraph{}, fmt.Errorf("workflow graph must contain 1 to %d bytes", maxWorkflowGraphBytes)
	}
	var graph struct {
		SchemaVersion int                 `json:"schemaVersion"`
		Nodes         []workflowGraphNode `json:"nodes"`
		Edges         []workflowGraphEdge `json:"edges"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&graph); err != nil {
		return validatedWorkflowGraph{}, errors.New("workflow graph must be a JSON object")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return validatedWorkflowGraph{}, errors.New("workflow graph must contain exactly one JSON object")
	}
	if graph.SchemaVersion != 1 {
		return validatedWorkflowGraph{}, errors.New("workflow graph schemaVersion must be 1")
	}
	if len(graph.Nodes) != 2 {
		return validatedWorkflowGraph{}, errors.New("P1-A workflow graph requires one manual trigger and one end node")
	}
	if len(graph.Edges) != 1 {
		return validatedWorkflowGraph{}, errors.New("P1-A workflow graph requires one trigger-to-end edge")
	}

	seen := make(map[string]bool, len(graph.Nodes))
	versions := make(map[string]map[string]string, len(graph.Nodes))
	triggerID, endID := "", ""
	for _, node := range graph.Nodes {
		if !workflowNodeIDPattern.MatchString(node.NodeInstanceID) || seen[node.NodeInstanceID] {
			return validatedWorkflowGraph{}, fmt.Errorf("invalid or duplicate nodeInstanceId %q", node.NodeInstanceID)
		}
		seen[node.NodeInstanceID] = true
		if node.NodeVersion != "1.0.0" {
			return validatedWorkflowGraph{}, fmt.Errorf("node %q requires supported nodeVersion 1.0.0", node.NodeInstanceID)
		}
		var config map[string]any
		if len(node.Config) == 0 || json.Unmarshal(node.Config, &config) != nil || config == nil {
			return validatedWorkflowGraph{}, fmt.Errorf("node %q config must be a JSON object", node.NodeInstanceID)
		}
		if len(config) != 0 {
			return validatedWorkflowGraph{}, fmt.Errorf("P1-A core node %q config must be empty", node.NodeInstanceID)
		}
		if node.Position == nil {
			return validatedWorkflowGraph{}, fmt.Errorf("node %q position is required", node.NodeInstanceID)
		}
		switch node.NodeType {
		case "core.manual":
			if triggerID != "" {
				return validatedWorkflowGraph{}, errors.New("workflow graph must contain exactly one main trigger")
			}
			triggerID = node.NodeInstanceID
		case "core.end":
			if endID != "" {
				return validatedWorkflowGraph{}, errors.New("P1-A workflow graph requires exactly one end node")
			}
			endID = node.NodeInstanceID
		default:
			return validatedWorkflowGraph{}, fmt.Errorf("node type %q is not available until P1-B", node.NodeType)
		}
		versions[node.NodeInstanceID] = map[string]string{"nodeType": node.NodeType, "nodeVersion": node.NodeVersion}
	}
	if triggerID == "" || endID == "" {
		return validatedWorkflowGraph{}, errors.New("workflow graph must contain one manual trigger and one end node")
	}
	edge := graph.Edges[0]
	if !workflowNodeIDPattern.MatchString(edge.EdgeID) || edge.SourceNodeInstanceID != triggerID || edge.TargetNodeInstanceID != endID || edge.SourcePort != "out" || edge.TargetPort != "in" {
		return validatedWorkflowGraph{}, errors.New("P1-A workflow edge must connect the manual trigger out port to the end in port")
	}
	canonical, err := json.Marshal(graph)
	if err != nil {
		return validatedWorkflowGraph{}, errors.New("encode workflow graph failed")
	}
	nodeVersions, err := json.Marshal(versions)
	if err != nil {
		return validatedWorkflowGraph{}, errors.New("encode workflow node versions failed")
	}
	return validatedWorkflowGraph{
		graphJSON: string(canonical), nodeVersionsJSON: string(nodeVersions), mainTriggerID: triggerID,
	}, nil
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
		CreatedAt: formatWorkflowTime(revision.CreatedAt),
	}
}

func formatWorkflowTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func validWorkflowStatus(status string) bool {
	return status == WorkflowStatusPaused || status == WorkflowStatusRunning ||
		status == WorkflowStatusAttention || status == WorkflowStatusArchived
}
