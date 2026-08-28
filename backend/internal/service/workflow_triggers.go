package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/plugin/sdk"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type workflowTriggerRun struct {
	revisionID int64
	cancel     context.CancelFunc
	token      chan struct{}
}

type workflowTriggerEmitter struct {
	app        *App
	workflowID int64
}

func (e workflowTriggerEmitter) Emit(ctx context.Context, event cloudevents.Event) error {
	ticker := time.NewTicker(runPollInterval)
	defer ticker.Stop()
	for {
		err := e.app.publishWorkflowTriggerEvent(ctx, event, e.workflowID)
		if err == nil {
			return nil
		}
		if !errors.Is(err, errWorkflowBackpressure) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (a *App) publishWorkflowTriggerEvent(ctx context.Context, event cloudevents.Event, workflowID int64) error {
	var record db.WorkflowEventRecord
	err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		record, err = a.persistWorkflowEventTx(tx, event, workflowID)
		if err != nil {
			return err
		}
		return a.deliverWorkflowEventTx(tx, record, event, 0, time.Now().UTC())
	})
	if err == nil {
		a.publishWorkflowEventRunUpdates(record.ID)
	}
	return err
}

type workflowTriggerState struct {
	app        *App
	workflowID int64
	revisionID int64
	node       workflowGraphNode
	stateMode  sdk.StateMode
}

func (s workflowTriggerState) Load(ctx context.Context) (json.RawMessage, error) {
	return (&bufferedNodeState{app: s.app, workflowID: s.workflowID, node: s.node}).Load(ctx)
}

func (s workflowTriggerState) Save(ctx context.Context, state json.RawMessage) error {
	if s.stateMode != sdk.StatePersistent {
		return errors.New("stateless workflow trigger cannot save state")
	}
	if len(state) == 0 || len(state) > maxWorkflowGraphBytes || !json.Valid(state) {
		return errors.New("workflow trigger state must be valid JSON")
	}
	now := time.Now().UTC()
	row := db.WorkflowNodeState{
		WorkflowID: s.workflowID, NodeInstanceID: s.node.NodeInstanceID, NodeType: s.node.NodeType,
		RevisionID: s.revisionID, StateJSON: string(state), UpdatedAt: now,
	}
	if err := s.app.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "workflow_id"}, {Name: "node_instance_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"node_type": row.NodeType, "revision_id": row.RevisionID, "state_json": row.StateJSON, "updated_at": now,
		}),
	}).Create(&row).Error; err != nil {
		return errors.New("save workflow trigger state failed")
	}
	return nil
}

func (a *App) syncWorkflowTriggers(ctx context.Context) error {
	var workflows []db.Workflow
	if err := a.DB.WithContext(ctx).Where(
		"status = ? AND mode = ? AND active_revision_id IS NOT NULL", WorkflowStatusActive, WorkflowModeStream,
	).Order("id").Find(&workflows).Error; err != nil {
		return errors.New("load stream workflows failed")
	}
	desired := make(map[int64]int64, len(workflows))
	for _, workflow := range workflows {
		desired[workflow.ID] = *workflow.ActiveRevisionID
	}

	a.triggerMu.Lock()
	if a.triggerRuns == nil {
		a.triggerRuns = map[int64]workflowTriggerRun{}
	}
	for workflowID, running := range a.triggerRuns {
		if revisionID, ok := desired[workflowID]; !ok || revisionID != running.revisionID {
			running.cancel()
			delete(a.triggerRuns, workflowID)
		}
	}
	a.triggerMu.Unlock()

	for _, workflow := range workflows {
		a.triggerMu.Lock()
		_, running := a.triggerRuns[workflow.ID]
		a.triggerMu.Unlock()
		if running {
			continue
		}
		if err := a.startWorkflowTrigger(ctx, workflow, *workflow.ActiveRevisionID); err != nil {
			_ = a.DB.WithContext(ctx).Model(&db.Workflow{}).Where("id = ? AND status = ?", workflow.ID, WorkflowStatusActive).
				Updates(map[string]any{"status": WorkflowStatusError, "updated_at": time.Now().UTC()}).Error
			slog.Error("workflow trigger start failed", "workflow_id", workflow.ID, "error_category", "trigger_start")
		}
	}
	return nil
}

func (a *App) startWorkflowTrigger(parent context.Context, workflow db.Workflow, revisionID int64) error {
	var revision db.WorkflowRevision
	if err := a.DB.WithContext(parent).First(&revision, revisionID).Error; err != nil {
		return errors.New("load workflow trigger revision failed")
	}
	graph, err := a.buildWorkflowRunGraph(revision.GraphJSON)
	if err != nil {
		return err
	}
	node := graph.nodes[revision.MainTriggerNodeID]
	desc, handler, ok := a.Plugins.Trigger(node.NodeType)
	if !ok {
		return fmt.Errorf("trigger handler %q is unavailable", node.NodeType)
	}
	ctx, cancel := context.WithCancel(parent)
	token := make(chan struct{})
	a.triggerMu.Lock()
	if _, exists := a.triggerRuns[workflow.ID]; exists {
		a.triggerMu.Unlock()
		cancel()
		return nil
	}
	a.triggerRuns[workflow.ID] = workflowTriggerRun{revisionID: revisionID, cancel: cancel, token: token}
	a.triggerMu.Unlock()
	request := sdk.TriggerRequest{
		Revision:       sdk.RevisionRef{WorkflowID: fmt.Sprint(workflow.ID), RevisionID: fmt.Sprint(revisionID)},
		NodeInstanceID: node.NodeInstanceID, Config: append(json.RawMessage(nil), node.Config...),
		Secrets: workflowSecretReader{app: a, revisionID: revisionID, nodeInstanceID: node.NodeInstanceID},
		State:   workflowTriggerState{app: a, workflowID: workflow.ID, revisionID: revisionID, node: node, stateMode: desc.State},
		Logger:  slog.Default().With("event_category", "workflow_trigger", "node_type", node.NodeType),
	}
	a.triggerWG.Add(1)
	go func() {
		defer a.triggerWG.Done()
		_ = handler.Run(ctx, request, workflowTriggerEmitter{app: a, workflowID: workflow.ID})
		stoppedByCancellation := ctx.Err() != nil
		cancel()
		a.triggerMu.Lock()
		current, ownsRun := a.triggerRuns[workflow.ID]
		ownsRun = ownsRun && current.token == token
		if !ownsRun {
			a.triggerMu.Unlock()
			return
		}
		if !stoppedByCancellation {
			slog.Error("workflow trigger stopped", "workflow_id", workflow.ID, "error_category", "trigger_run")
			_ = a.DB.Model(&db.Workflow{}).Where("id = ? AND status = ?", workflow.ID, WorkflowStatusActive).
				Updates(map[string]any{"status": WorkflowStatusError, "updated_at": time.Now().UTC()}).Error
		}
		delete(a.triggerRuns, workflow.ID)
		a.triggerMu.Unlock()
	}()
	return nil
}

func (a *App) stopWorkflowTrigger(workflowID int64) {
	a.triggerMu.Lock()
	if running, ok := a.triggerRuns[workflowID]; ok {
		running.cancel()
		delete(a.triggerRuns, workflowID)
	}
	a.triggerMu.Unlock()
}

func (a *App) stopWorkflowTriggers() {
	a.triggerMu.Lock()
	for workflowID, running := range a.triggerRuns {
		running.cancel()
		delete(a.triggerRuns, workflowID)
	}
	a.triggerMu.Unlock()
}

var _ sdk.Emitter = workflowTriggerEmitter{}
var _ sdk.StateStore = workflowTriggerState{}
