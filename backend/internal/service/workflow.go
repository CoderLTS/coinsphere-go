package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/plugin/sdk"
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
	maxWorkflowRevisions     = 10
)

const blankWorkflowGraph = `{
  "schemaVersion": 3,
 "entryPoints":{"main":"manual-trigger"},
  "nodes": [
    {"nodeInstanceId":"manual-trigger","nodeType":"core.manual","nodeVersion":"1.0.0","config":{},"position":{"x":160,"y":220}},
    {"nodeInstanceId":"end","nodeType":"core.end","nodeVersion":"1.0.0","config":{},"position":{"x":520,"y":220}}
  ],
  "edges": [
    {"edgeId":"manual-to-end","sourceNodeInstanceId":"manual-trigger","sourcePort":"out","targetNodeInstanceId":"end","targetPort":"in"}
  ]
}`

const scheduledWorkflowGraph = `{
  "schemaVersion": 3,
 "entryPoints":{"main":"schedule-trigger"},
  "nodes": [
    {"nodeInstanceId":"schedule-trigger","nodeType":"core.schedule","nodeVersion":"1.0.0","config":{"everySeconds":3600},"position":{"x":160,"y":220}},
    {"nodeInstanceId":"end","nodeType":"core.end","nodeVersion":"1.0.0","config":{},"position":{"x":520,"y":220}}
  ],
  "edges": [
    {"edgeId":"schedule-to-end","sourceNodeInstanceId":"schedule-trigger","sourcePort":"out","targetNodeInstanceId":"end","targetPort":"in"}
  ]
}`

const eventWorkflowGraph = `{
  "schemaVersion": 3,
 "entryPoints":{"main":"event-trigger"},
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
	Graph             json.RawMessage        `json:"graph,omitempty"`
	SecretChanges     []WorkflowSecretChange `json:"secretChanges,omitempty"`
	MaxConcurrentRuns int                    `json:"maxConcurrentRuns,omitempty"`
	BacklogLimit      int                    `json:"backlogLimit,omitempty"`
	Name              string                 `json:"name"`
	Description       string                 `json:"description"`
	TemplateKey       string                 `json:"templateKey"`
	GroupID           *int64                 `json:"groupId"`
}

type WorkflowUpdatePayload struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type WorkflowRevisionSavePayload struct {
	Metadata                *WorkflowUpdatePayload `json:"metadata,omitempty"`
	ExpectedDraftRevisionID int64                  `json:"expectedDraftRevisionId"`
	Graph                   json.RawMessage        `json:"graph"`
	SecretChanges           []WorkflowSecretChange `json:"secretChanges,omitempty"`
}

type WorkflowLifecyclePayload struct {
	Action string `json:"action"`
}

type WorkflowView struct {
	OwnerUserID         int64  `json:"ownerUserId"`
	DraftRevisionID     int64  `json:"draftRevisionId"`
	ID                  int64  `json:"id"`
	Name                string `json:"name"`
	Description         string `json:"description"`
	GroupID             *int64 `json:"groupId"`
	Mode                string `json:"mode"`
	Status              string `json:"status"`
	PublishedRevisionID int64  `json:"publishedRevisionId"`
	MainTriggerNodeID   string `json:"mainTriggerNodeId"`
	RetentionDays       int    `json:"retentionDays"`
	CreatedBy           int64  `json:"createdBy"`
	CreatedAt           string `json:"createdAt"`
	UpdatedAt           string `json:"updatedAt"`
}

type WorkflowRuntimeView struct {
	MaxConcurrentRuns int    `json:"maxConcurrentRuns"`
	BacklogLimit      int    `json:"backlogLimit"`
	NextScheduledAt   string `json:"nextScheduledAt,omitempty"`
	LastScheduledAt   string `json:"lastScheduledAt,omitempty"`
	UpdatedAt         string `json:"updatedAt"`
}

type WorkflowDetail struct {
	Permissions []string `json:"permissions"`
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
	if err := requireCapability(ctx, "workflows.create"); err != nil {
		return WorkflowDetail{}, err
	}
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
	if len(payload.Graph) > 0 {
		templateGraph = payload.Graph
		ok = true
	}
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
		Name: name, Description: description, Mode: a.workflowModeForTrigger(graph.nodes[graph.mainTriggerID].NodeType), Status: WorkflowStatusInactive,
		MainTriggerNodeID: graph.mainTriggerID, RetentionDays: 30, CreatedBy: principal.User.ID,
		CreatedAt: now, UpdatedAt: now,
	}
	err = a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := validateWorkflowGroupID(tx, payload.GroupID); err != nil {
			return err
		}
		var createErr error
		workflow, createErr = a.createWorkflowRecord(tx, name, description, payload.GroupID, graph, principal.User.ID, now)
		if createErr != nil {
			return createErr
		}
		changes, err := validateWorkflowSecretChanges(graph, payload.SecretChanges)
		if err != nil {
			return err
		}
		rev := db.WorkflowRevision{ID: *workflow.DraftRevisionID}
		if err := a.syncRevisionPluginReferences(tx, rev, graph); err != nil {
			return err
		}
		if err := a.persistWorkflowSecrets(tx, workflow.ID, 0, rev, validatedWorkflowGraph{}, graph, changes, now); err != nil {
			return err
		}
		concurrency, backlog := payload.MaxConcurrentRuns, payload.BacklogLimit
		if concurrency == 0 {
			concurrency = 2
		}
		if backlog == 0 {
			backlog = 100
		}
		if concurrency < 1 || concurrency > 32 || backlog < 1 || backlog > 10000 {
			return errors.New("invalid workflow capacity")
		}
		if graphHasPersistentState(graph) {
			concurrency = 1
		}
		return tx.Model(&db.WorkflowRuntime{}).Where("workflow_id = ?", workflow.ID).Updates(map[string]any{"max_concurrent_runs": concurrency, "backlog_limit": backlog}).Error
	})
	if err != nil {
		return WorkflowDetail{}, err
	}
	return a.GetWorkflow(ctx, workflow.ID)
}

func (a *App) createWorkflowRecord(tx *gorm.DB, name, description string, groupID *int64, graph validatedWorkflowGraph, userID int64, now time.Time) (db.Workflow, error) {
	workflow := db.Workflow{
		Name: name, Description: description, GroupID: groupID, Mode: a.workflowModeForTrigger(graph.nodes[graph.mainTriggerID].NodeType), Status: WorkflowStatusInactive,
		MainTriggerNodeID: graph.mainTriggerID, RetentionDays: 30, OwnerUserID: userID, CreatedBy: userID, CreatedAt: now, UpdatedAt: now,
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
	workflow.DraftRevisionID = &revision.ID
	if err := tx.Model(&db.Workflow{}).Where("id = ?", workflow.ID).Update("draft_revision_id", revision.ID).Error; err != nil {
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
	if err := requireCapability(ctx, "workflows.read"); err != nil {
		return nil, err
	}
	query := workflowScopeQuery(a.DB.WithContext(ctx).Model(&db.Workflow{}), ContextPrincipal(ctx), "workflows.read", "workflows.id").Order("updated_at DESC, id DESC").Limit(200)
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
	if err := a.AuthorizeWorkflow(ctx, workflowID, "workflows.read"); err != nil {
		return WorkflowDetail{}, err
	}
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
	permissions, err := a.workflowPermissions(ctx, workflow)
	if err != nil {
		return WorkflowDetail{}, err
	}
	return WorkflowDetail{Permissions: permissions,
		WorkflowView:         workflowView(workflow),
		Runtime:              runtimeView,
		StateNodeInstanceIDs: stateNodeInstanceIDs,
	}, nil
}

func (a *App) UpdateWorkflow(ctx context.Context, workflowID int64, payload WorkflowUpdatePayload) (WorkflowDetail, error) {
	if err := a.AuthorizeWorkflow(ctx, workflowID, "workflows.update"); err != nil {
		return WorkflowDetail{}, err
	}
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
	if err := a.AuthorizeWorkflow(ctx, workflowID, "workflows.update"); err != nil {
		return WorkflowRevisionView{}, err
	}
	if payload.ExpectedDraftRevisionID <= 0 {
		return WorkflowRevisionView{}, errors.New("expectedDraftRevisionId must be positive")
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
		if workflow.DraftRevisionID == nil || *workflow.DraftRevisionID != payload.ExpectedDraftRevisionID {
			return fmt.Errorf("%w: active workflow revision changed", ErrConflict)
		}
		var activeRevision db.WorkflowRevision
		if err := tx.Where("workflow_id = ? AND id = ?", workflowID, *workflow.DraftRevisionID).First(&activeRevision).Error; err != nil {
			return errors.New("load active workflow revision failed")
		}
		activeGraph, err := a.validateWorkflowGraph(json.RawMessage(activeRevision.GraphJSON))
		if err != nil {
			return errors.New("active workflow revision graph is invalid")
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
		if err := a.persistWorkflowSecrets(tx, workflowID, *workflow.DraftRevisionID, revision, activeGraph, graph, secretChanges, now); err != nil {
			return err
		}
		if err := tx.Model(&db.Workflow{}).Where("id = ?", workflowID).Updates(map[string]any{
			"draft_revision_id": revision.ID,
			"updated_at":        now,
		}).Error; err != nil {
			return errors.New("activate workflow revision failed")
		}
		if payload.Metadata != nil {
			name, description := strings.TrimSpace(payload.Metadata.Name), strings.TrimSpace(payload.Metadata.Description)
			if name == "" || utf8.RuneCountInString(name) > 120 || utf8.RuneCountInString(description) > 500 {
				return errors.New("invalid workflow metadata")
			}
			if err := tx.Model(&workflow).Updates(map[string]any{"name": name, "description": description}).Error; err != nil {
				return err
			}
		}
		if err := a.syncRevisionPluginReferences(tx, revision, graph); err != nil {
			return err
		}
		if err := a.pruneWorkflowRevisions(tx, workflowID, revision.ID); err != nil {
			return err
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

func (a *App) pruneWorkflowRevisions(tx *gorm.DB, workflowID, retainedRevisionID int64) error {
	var revisionIDs []int64
	if err := tx.Model(&db.WorkflowRevision{}).Where("workflow_id = ?", workflowID).
		Order("revision_number DESC").Pluck("id", &revisionIDs).Error; err != nil {
		return errors.New("list workflow revisions for pruning failed")
	}
	for index := maxWorkflowRevisions; index < len(revisionIDs); index++ {
		id := revisionIDs[index]
		var refs int64
		if err := tx.Raw(`SELECT (SELECT COUNT(*) FROM workflow_runs WHERE revision_id=?) + (SELECT COUNT(*) FROM workflows WHERE draft_revision_id=? OR published_revision_id=?)`, id, id, id).Scan(&refs).Error; err != nil {
			return err
		}
		if refs > 0 {
			continue
		}
		if err := a.deleteWorkflowRevisionRecord(tx, workflowID, id, retainedRevisionID, false, true); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) deleteWorkflowRevisionRecord(tx *gorm.DB, workflowID, revisionID, retainedRevisionID int64, removeRuns, removeOutputs bool) error {
	if removeRuns {
		if _, err := deleteWorkflowRunTree(tx, "workflow_id = ? AND revision_id = ?", workflowID, revisionID); err != nil {
			return err
		}
	} else {
		var runCount int64
		if err := tx.Model(&db.WorkflowRun{}).Where("workflow_id = ? AND revision_id = ?", workflowID, revisionID).Count(&runCount).Error; err != nil {
			return errors.New("check workflow revision references failed")
		}
		if runCount > 0 {
			return fmt.Errorf("%w: workflow revision is referenced by workflow runs", ErrConflict)
		}
	}
	if removeOutputs {
		if err := a.Plugins.Cleanup(tx.Statement.Context, tx, sdk.CleanupRequest{WorkflowID: workflowID, RevisionID: &revisionID}); err != nil {
			return err
		}
	}
	if err := tx.Where("workflow_id = ? AND revision_id = ?", workflowID, revisionID).Delete(&db.WorkflowNodeState{}).Error; err != nil {
		return err
	}
	if err := tx.Exec("DELETE FROM plugin_references WHERE reference_type = 'revision' AND reference_id = ?", fmt.Sprint(revisionID)).Error; err != nil {
		return err
	}

	if err := tx.Where("workflow_id = ? AND revision_id = ?", workflowID, revisionID).
		Delete(&db.WorkflowSecretBinding{}).Error; err != nil {
		return errors.New("delete workflow revision secrets failed")
	}
	if err := tx.Where("workflow_id = ? AND id = ?", workflowID, revisionID).
		Delete(&db.WorkflowRevision{}).Error; err != nil {
		return errors.New("delete workflow revision failed")
	}
	return nil
}

func (a *App) ListWorkflowRevisions(ctx context.Context, workflowID int64) ([]WorkflowRevisionView, error) {
	if err := a.AuthorizeWorkflow(ctx, workflowID, "workflows.read"); err != nil {
		return nil, err
	}
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
	if err := a.AuthorizeWorkflow(ctx, workflowID, "workflows.read"); err != nil {
		return WorkflowRevisionView{}, err
	}
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

func (a *App) DeleteWorkflowRevision(ctx context.Context, workflowID, revisionID int64) error {
	if err := a.AuthorizeWorkflow(ctx, workflowID, "workflows.delete"); err != nil {
		return err
	}
	var workflow db.Workflow
	if err := a.DB.WithContext(ctx).Select("id", "draft_revision_id", "published_revision_id").First(&workflow, workflowID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("%w: workflow", ErrNotFound)
		}
		return errors.New("load workflow failed")
	}
	if workflow.DraftRevisionID != nil && *workflow.DraftRevisionID == revisionID || workflow.PublishedRevisionID != nil && *workflow.PublishedRevisionID == revisionID {
		return fmt.Errorf("%w: active workflow revision cannot be deleted", ErrConflict)
	}
	if err := a.cancelWorkflowRuns(ctx, workflowID, &revisionID); err != nil {
		return err
	}
	if err := a.waitWorkflowRuns(ctx, workflowID, &revisionID); err != nil {
		return err
	}
	var storageKeys []string
	err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var workflow db.Workflow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&workflow, workflowID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: workflow", ErrNotFound)
			}
			return errors.New("lock workflow failed")
		}
		var revision db.WorkflowRevision
		if err := tx.Where("workflow_id = ? AND id = ?", workflowID, revisionID).First(&revision).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: workflow revision", ErrNotFound)
			}
			return errors.New("load workflow revision failed")
		}
		if workflow.DraftRevisionID != nil && *workflow.DraftRevisionID == revisionID || workflow.PublishedRevisionID != nil && *workflow.PublishedRevisionID == revisionID {
			return fmt.Errorf("%w: active workflow revision cannot be deleted", ErrConflict)
		}
		if err := tx.Exec("SET LOCAL coinsphere.workflow_delete = 'on'").Error; err != nil {
			return errors.New("enable workflow deletion mode failed")
		}
		var err error
		storageKeys, err = deleteWorkflowRunTree(tx, "workflow_id = ? AND revision_id = ?", workflowID, revisionID)
		if err != nil {
			return err
		}
		return a.deleteWorkflowRevisionRecord(tx, workflowID, revisionID, *workflow.DraftRevisionID, false, true)
	})
	if err != nil {
		return err
	}
	return a.removeWorkflowArtifacts(storageKeys)
}

// DeleteWorkflow removes the workflow definition and every execution record owned by it.
func (a *App) DeleteWorkflow(ctx context.Context, workflowID int64) error {
	if err := a.AuthorizeWorkflow(ctx, workflowID, "workflows.delete"); err != nil {
		return err
	}
	if workflowID <= 0 {
		return fmt.Errorf("%w: workflow", ErrNotFound)
	}
	var storageKeys []string
	err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var workflow db.Workflow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&workflow, workflowID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: workflow", ErrNotFound)
			}
			return errors.New("lock workflow failed")
		}
		if workflow.Status == WorkflowStatusActive {
			return fmt.Errorf("%w: active workflow cannot be deleted", ErrConflict)
		}
		var activeRuns int64
		if err := tx.Raw(fmt.Sprintf(workflowRunTreeCTE, "workflow_id = ?")+`SELECT COUNT(*) FROM workflow_runs WHERE id IN (SELECT id FROM run_tree) AND status = ?`, workflowID, RunStatusRunning).Scan(&activeRuns).Error; err != nil {
			return errors.New("check workflow runs before deletion failed")
		}
		if activeRuns > 0 {
			return fmt.Errorf("%w: workflow has active runs", ErrConflict)
		}
		if err := tx.Exec("SET LOCAL coinsphere.workflow_delete = 'on'").Error; err != nil {
			return errors.New("enable workflow deletion mode failed")
		}
		var err error
		storageKeys, err = deleteWorkflowRunTree(tx, "workflow_id = ?", workflowID)
		if err != nil {
			return err
		}
		if err := a.Plugins.Cleanup(ctx, tx, sdk.CleanupRequest{WorkflowID: workflowID}); err != nil {
			return err
		}
		if err := tx.Where("workflow_id = ?", workflowID).Delete(&db.WorkflowNodeState{}).Error; err != nil {
			return errors.New("delete workflow node states failed")
		}
		if err := tx.Where("workflow_id = ?", workflowID).Delete(&db.WorkflowSecretBinding{}).Error; err != nil {
			return errors.New("delete workflow secrets failed")
		}
		if err := tx.Model(&workflow).Updates(map[string]any{
			"published_revision_id": nil, "draft_revision_id": nil, "updated_at": time.Now().UTC(),
		}).Error; err != nil {
			return errors.New("clear workflow active revision failed")
		}
		if err := tx.Where("workflow_id = ?", workflowID).Delete(&db.WorkflowRevision{}).Error; err != nil {
			return errors.New("delete workflow revisions failed")
		}
		if err := tx.Where("workflow_id = ?", workflowID).Delete(&db.WorkflowRuntime{}).Error; err != nil {
			return errors.New("delete workflow runtime failed")
		}
		if err := tx.Where("id = ?", workflowID).Delete(&db.Workflow{}).Error; err != nil {
			return errors.New("delete workflow failed")
		}
		return nil
	})
	if err != nil {
		return err
	}
	return a.removeWorkflowArtifacts(storageKeys)
}

func (a *App) removeWorkflowArtifacts(storageKeys []string) error {
	for _, key := range storageKeys {
		if strings.TrimSpace(a.ArtifactRoot) == "" {
			continue
		}
		if err := os.Remove(filepath.Join(a.ArtifactRoot, filepath.FromSlash(key))); err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.New("remove workflow artifact failed")
		}
	}
	return nil
}

const workflowRunTreeCTE = `WITH RECURSIVE run_tree(id) AS (
    SELECT id FROM workflow_runs WHERE %s
    UNION ALL
    SELECT child.id FROM workflow_runs child JOIN run_tree parent ON child.original_run_id = parent.id
) `

func deleteWorkflowRunTree(tx *gorm.DB, rootWhere string, args ...any) ([]string, error) {
	cte := fmt.Sprintf(workflowRunTreeCTE, rootWhere)
	var digests []string
	if err := tx.Raw(cte+`SELECT DISTINCT ref.artifact_sha256
FROM workflow_artifact_refs ref
JOIN workflow_run_nodes node ON node.id = ref.run_node_id
WHERE node.run_id IN (SELECT id FROM run_tree)`, args...).Scan(&digests).Error; err != nil {
		return nil, errors.New("collect workflow artifacts failed")
	}
	statements := []string{
		`DELETE FROM workflow_human_tasks WHERE run_id IN (SELECT id FROM run_tree)`,
		`DELETE FROM workflow_event_deliveries WHERE run_id IN (SELECT id FROM run_tree)`,
		`DELETE FROM workflow_artifact_refs WHERE run_node_id IN (SELECT id FROM workflow_run_nodes WHERE run_id IN (SELECT id FROM run_tree))`,
		`DELETE FROM workflow_run_checkpoints WHERE run_id IN (SELECT id FROM run_tree)`,
		`DELETE FROM workflow_node_logs WHERE run_id IN (SELECT id FROM run_tree)`,
		`DELETE FROM workflow_run_nodes WHERE run_id IN (SELECT id FROM run_tree)`,
	}
	for _, statement := range statements {
		if err := tx.Exec(cte+statement, args...).Error; err != nil {
			return nil, errors.New("delete workflow run details failed")
		}
	}
	for {
		result := tx.Exec(cte+`DELETE FROM workflow_runs
WHERE id = (
    SELECT run.id FROM workflow_runs run
    WHERE run.id IN (SELECT id FROM run_tree)
      AND NOT EXISTS (SELECT 1 FROM workflow_runs child WHERE child.original_run_id = run.id)
    LIMIT 1
)`, args...)
		if result.Error != nil {
			return nil, errors.New("delete workflow runs failed")
		}
		if result.RowsAffected == 0 {
			break
		}
	}
	if len(digests) == 0 {
		return nil, nil
	}
	var artifacts []db.WorkflowArtifact
	if err := tx.Where("sha256 IN ?", digests).Find(&artifacts).Error; err != nil {
		return nil, errors.New("load workflow artifacts failed")
	}
	storageKeys := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		var references int64
		if err := tx.Model(&db.WorkflowArtifactRef{}).Where("artifact_sha256 = ?", artifact.SHA256).Count(&references).Error; err != nil {
			return nil, errors.New("check workflow artifact references failed")
		}
		if references != 0 {
			continue
		}
		if err := tx.Delete(&artifact).Error; err != nil {
			return nil, errors.New("delete workflow artifact failed")
		}
		storageKeys = append(storageKeys, artifact.StorageKey)
	}
	return storageKeys, nil
}

func (a *App) cancelWorkflowRuns(ctx context.Context, workflowID int64, revisionID *int64) error {
	var runIDs []int64
	err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var workflow db.Workflow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&workflow, workflowID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: workflow", ErrNotFound)
			}
			return errors.New("lock workflow failed")
		}
		query := tx.Model(&db.WorkflowRun{}).Where("workflow_id = ? AND status IN ?", workflowID, []string{RunStatusQueued, RunStatusRunning, RunStatusWaiting, RunStatusRetrying})
		if revisionID != nil {
			query = query.Where("revision_id = ?", *revisionID)
		}
		if err := query.Pluck("id", &runIDs).Error; err != nil {
			return errors.New("list workflow runs for cancellation failed")
		}
		now := time.Now().UTC()
		runQuery := tx.Model(&db.WorkflowRun{}).Where("workflow_id = ? AND status IN ?", workflowID, []string{RunStatusQueued, RunStatusWaiting, RunStatusRetrying})
		if revisionID != nil {
			runQuery = runQuery.Where("revision_id = ?", *revisionID)
		}
		if err := runQuery.
			Updates(map[string]any{"status": RunStatusCancelled, "cancel_requested_at": now, "completed_at": now, "lease_token": nil, "lease_expires_at": nil, "updated_at": now}).Error; err != nil {
			return errors.New("cancel queued workflow runs failed")
		}
		runQuery = tx.Model(&db.WorkflowRun{}).Where("workflow_id = ? AND status = ?", workflowID, RunStatusRunning)
		if revisionID != nil {
			runQuery = runQuery.Where("revision_id = ?", *revisionID)
		}
		if err := runQuery.
			Updates(map[string]any{"cancel_requested_at": now, "updated_at": now}).Error; err != nil {
			return errors.New("request running workflow cancellation failed")
		}
		return nil
	})
	if err != nil {
		return err
	}
	a.runCancelMu.Lock()
	for _, runID := range runIDs {
		if cancel := a.runCancels[runID]; cancel != nil {
			cancel()
		}
	}
	a.runCancelMu.Unlock()
	return nil
}

func (a *App) waitWorkflowRuns(ctx context.Context, workflowID int64, revisionID *int64) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		query := a.DB.WithContext(ctx).Model(&db.WorkflowRun{}).Where("workflow_id = ? AND status IN ?", workflowID, []string{RunStatusQueued, RunStatusRunning, RunStatusWaiting, RunStatusRetrying})
		if revisionID != nil {
			query = query.Where("revision_id = ?", *revisionID)
		}
		var count int64
		if err := query.Count(&count).Error; err != nil {
			return errors.New("check workflow run cancellation failed")
		}
		if count == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (a *App) ApplyWorkflowLifecycle(ctx context.Context, workflowID int64, payload WorkflowLifecyclePayload) (WorkflowDetail, error) {
	if err := a.AuthorizeWorkflow(ctx, workflowID, "workflows.activate"); err != nil {
		return WorkflowDetail{}, err
	}
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
		if action == "activate" && workflow.PublishedRevisionID == nil {
			return fmt.Errorf("%w: workflow is not startable", ErrConflict)
		}
		now := time.Now().UTC()
		updates := map[string]any{"status": next, "updated_at": now}
		var runtimeUpdates map[string]any
		if action == "activate" {
			var revision db.WorkflowRevision
			if err := tx.First(&revision, *workflow.PublishedRevisionID).Error; err != nil {
				return errors.New("load active workflow revision failed")
			}
			validated, err := a.validateWorkflowGraph(json.RawMessage(revision.GraphJSON))
			if err != nil {
				return fmt.Errorf("%w: active workflow revision is invalid", ErrConflict)
			}
			if err := a.authorizeExecution(workflow.OwnerUserID, validated); err != nil {
				return err
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
	if workflow.PublishedRevisionID != nil {
		activeRevisionID = *workflow.PublishedRevisionID
	}
	view := WorkflowView{
		ID: workflow.ID, OwnerUserID: workflow.OwnerUserID, DraftRevisionID: revisionPointerValue(workflow.DraftRevisionID), Name: workflow.Name, Description: workflow.Description, GroupID: workflow.GroupID, Mode: workflow.Mode,
		Status: workflow.Status, PublishedRevisionID: activeRevisionID,
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

func (a *App) workflowModeForTrigger(nodeType string) string {
	switch nodeType {
	case "core.manual", "core.schedule":
		return WorkflowModeBatch
	case "core.event":
		return WorkflowModeEvent
	default:
		if _, ok := a.Plugins.Ingress(nodeType); ok {
			return WorkflowModeEvent
		}
		return WorkflowModeStream
	}
}
