package service

import (
	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/security"
	"coinsphere/backend/plugin/sdk"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"log/slog"
	"time"
)

type workflowTriggerRun struct {
	revisionID int64
	cancel     context.CancelFunc
	token      string
	done       chan struct{}
}
type triggerLease struct {
	workflowID, revisionID int64
	token                  string
}
type triggerLeaseKey struct{}
type workflowTriggerEmitter struct {
	app   *App
	lease triggerLease
}

func (e workflowTriggerEmitter) Emit(ctx context.Context, event cloudevents.Event) error {
	ticker := time.NewTicker(runPollInterval)
	defer ticker.Stop()
	for {
		err := e.app.publishWorkflowTriggerEvent(ctx, event, e.lease)
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

// Lock workflow before runtime; publication, claim and emission share this order.
func (a *App) lockTriggerLease(tx *gorm.DB, lease triggerLease) error {
	var w db.Workflow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND status='active' AND published_revision_id=?", lease.workflowID, lease.revisionID).First(&w).Error; err != nil {
		return ErrConflict
	}
	var rt db.WorkflowRuntime
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("workflow_id=? AND trigger_lease_token=? AND trigger_lease_expires_at>clock_timestamp()", lease.workflowID, lease.token).First(&rt).Error; err != nil {
		return ErrConflict
	}
	p, err := principalForTx(tx, &Principal{User: &db.SystemUser{ID: w.OwnerUserID}})
	if err != nil {
		return err
	}
	if err := authorizeWorkflowTx(tx, p, w.ID, "workflows.run"); err != nil {
		return err
	}
	var revision db.WorkflowRevision
	if err := tx.First(&revision, lease.revisionID).Error; err != nil {
		return err
	}
	g, err := a.validateWorkflowGraph(json.RawMessage(revision.GraphJSON))
	if err != nil {
		return err
	}
	return authorizePluginExecution(p, g)
}
func (a *App) publishWorkflowTriggerEvent(ctx context.Context, event cloudevents.Event, lease triggerLease) error {
	var record db.WorkflowEventRecord
	err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := a.lockTriggerLease(tx, lease); err != nil {
			return err
		}
		var err error
		record, err = a.persistWorkflowEventTx(tx, event, lease.workflowID)
		return err
	})
	if err == nil {
		a.publishWorkflowEventRunUpdates(record.ID)
	}
	return err
}

type workflowTriggerState struct {
	app       *App
	lease     triggerLease
	node      workflowGraphNode
	stateMode sdk.StateMode
}

func (s workflowTriggerState) Load(ctx context.Context) (json.RawMessage, error) {
	var raw json.RawMessage
	err := s.app.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.app.lockTriggerLease(tx, s.lease); err != nil {
			return err
		}
		var state db.WorkflowNodeState
		err := tx.Where("workflow_id=? AND revision_id=? AND node_instance_id=?", s.lease.workflowID, s.lease.revisionID, s.node.NodeInstanceID).First(&state).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			raw = json.RawMessage(`{}`)
			return nil
		}
		if err != nil {
			return err
		}
		raw = json.RawMessage(state.StateJSON)
		return nil
	})
	return raw, err
}
func (s workflowTriggerState) Save(ctx context.Context, state json.RawMessage) error {
	if s.stateMode != sdk.StatePersistent || len(state) == 0 || len(state) > maxWorkflowGraphBytes || !json.Valid(state) {
		return errors.New("invalid persistent trigger state")
	}
	return s.app.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.app.lockTriggerLease(tx, s.lease); err != nil {
			return err
		}
		row := db.WorkflowNodeState{WorkflowID: s.lease.workflowID, RevisionID: s.lease.revisionID, NodeInstanceID: s.node.NodeInstanceID, NodeType: s.node.NodeType, StateJSON: string(state), UpdatedAt: time.Now().UTC()}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "workflow_id"}, {Name: "revision_id"}, {Name: "node_instance_id"}}, DoUpdates: clause.Assignments(map[string]any{"state_json": row.StateJSON, "node_type": row.NodeType, "updated_at": row.UpdatedAt})}).Create(&row).Error
	})
}
func (a *App) syncWorkflowTriggers(ctx context.Context) error {
	var workflows []db.Workflow
	if err := a.DB.WithContext(ctx).Where("status=? AND mode=? AND published_revision_id IS NOT NULL", WorkflowStatusActive, WorkflowModeStream).Order("id").Find(&workflows).Error; err != nil {
		return err
	}
	desired := map[int64]int64{}
	for _, w := range workflows {
		desired[w.ID] = *w.PublishedRevisionID
	}
	a.triggerMu.Lock()
	if a.triggerRuns == nil {
		a.triggerRuns = map[int64]workflowTriggerRun{}
	}
	for id, r := range a.triggerRuns {
		if rev, ok := desired[id]; !ok || rev != r.revisionID {
			r.cancel()
		}
	}
	a.triggerMu.Unlock()
	for _, w := range workflows {
		a.triggerMu.Lock()
		_, running := a.triggerRuns[w.ID]
		a.triggerMu.Unlock()
		if running {
			continue
		}
		if err := a.startWorkflowTrigger(ctx, w, *w.PublishedRevisionID); err != nil {
			slog.Error("workflow trigger start failed", "workflow_id", w.ID, "error_category", "trigger_start")
		}
	}
	return nil
}
func (a *App) startWorkflowTrigger(parent context.Context, w db.Workflow, revisionID int64) error {
	var revision db.WorkflowRevision
	if err := a.DB.WithContext(parent).First(&revision, revisionID).Error; err != nil {
		return err
	}
	graph, err := a.buildWorkflowRunGraph(revision.GraphJSON)
	if err != nil {
		return err
	}
	node := graph.nodes[revision.MainTriggerNodeID]
	desc, handler, ok := a.Plugins.Trigger(node.NodeType)
	if !ok {
		return fmt.Errorf("trigger handler %q unavailable", node.NodeType)
	}
	lease := triggerLease{w.ID, revisionID, security.RandomToken()}
	claimed := false
	err = a.DB.WithContext(parent).Transaction(func(tx *gorm.DB) error {
		var current db.Workflow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND status='active' AND published_revision_id=?", w.ID, revisionID).First(&current).Error; err != nil {
			return err
		}
		r := tx.Model(&db.WorkflowRuntime{}).Where("workflow_id=? AND (trigger_lease_token IS NULL OR trigger_lease_expires_at<=clock_timestamp())", w.ID).Updates(map[string]any{"trigger_lease_token": lease.token, "trigger_lease_expires_at": gorm.Expr("clock_timestamp()+interval '30 seconds'")})
		if r.Error != nil {
			return r.Error
		}
		claimed = r.RowsAffected == 1
		if !claimed {
			return nil
		}
		return a.lockTriggerLease(tx, lease)
	})
	if err != nil || !claimed {
		return err
	}
	ctx, cancel := context.WithCancel(context.WithValue(parent, triggerLeaseKey{}, lease))
	done := make(chan struct{})
	a.triggerMu.Lock()
	a.triggerRuns[w.ID] = workflowTriggerRun{revisionID: revisionID, cancel: cancel, token: lease.token, done: done}
	a.triggerMu.Unlock()
	request := sdk.TriggerRequest{Revision: sdk.RevisionRef{WorkflowID: fmt.Sprint(w.ID), RevisionID: fmt.Sprint(revisionID)}, NodeInstanceID: node.NodeInstanceID, Config: node.Config, Secrets: workflowSecretReader{app: a, revisionID: revisionID, nodeInstanceID: node.NodeInstanceID}, State: workflowTriggerState{app: a, lease: lease, node: node, stateMode: desc.State}, Logger: slog.Default().With("event_category", "workflow_trigger", "node_type", node.NodeType)}
	a.triggerWG.Add(1)
	go func() {
		defer a.triggerWG.Done()
		defer close(done)
		renewalDone := make(chan struct{})
		go a.renewTriggerLease(ctx, lease, renewalDone, cancel)
		runErr := handler.Run(ctx, request, workflowTriggerEmitter{app: a, lease: lease})
		wasCancelled := ctx.Err() != nil
		cancel()
		close(renewalDone)
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = a.DB.WithContext(cleanupCtx).Transaction(func(tx *gorm.DB) error {
			if err := a.lockTriggerLease(tx, lease); err != nil {
				return err
			}
			if !wasCancelled {
				if err := tx.Model(&db.Workflow{}).Where("id=? AND published_revision_id=?", w.ID, revisionID).Updates(map[string]any{"status": WorkflowStatusError, "updated_at": time.Now().UTC()}).Error; err != nil {
					return err
				}
			}
			return tx.Model(&db.WorkflowRuntime{}).Where("workflow_id=? AND trigger_lease_token=?", w.ID, lease.token).Updates(map[string]any{"trigger_lease_token": nil, "trigger_lease_expires_at": nil}).Error
		})
		if !wasCancelled {
			slog.Error("workflow trigger stopped", "workflow_id", w.ID, "error_category", "trigger_run", "failed", runErr != nil)
		}
		a.triggerMu.Lock()
		if current, ok := a.triggerRuns[w.ID]; ok && current.token == lease.token {
			delete(a.triggerRuns, w.ID)
		}
		a.triggerMu.Unlock()
	}()
	return nil
}
func (a *App) renewTriggerLease(ctx context.Context, lease triggerLease, done <-chan struct{}, cancel context.CancelFunc) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				if err := a.lockTriggerLease(tx, lease); err != nil {
					return err
				}
				r := tx.Model(&db.WorkflowRuntime{}).Where("workflow_id=? AND trigger_lease_token=?", lease.workflowID, lease.token).Update("trigger_lease_expires_at", gorm.Expr("clock_timestamp()+interval '30 seconds'"))
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
func (a *App) stopWorkflowTrigger(id int64) {
	a.triggerMu.Lock()
	r, ok := a.triggerRuns[id]
	if ok {
		r.cancel()
	}
	a.triggerMu.Unlock()
	if ok {
		select {
		case <-r.done:
		case <-time.After(runLeaseDuration):
		}
	}
}
func (a *App) stopWorkflowTriggers() {
	a.triggerMu.Lock()
	defer a.triggerMu.Unlock()
	for _, r := range a.triggerRuns {
		r.cancel()
	}
}

var _ sdk.Emitter = workflowTriggerEmitter{}
var _ sdk.StateStore = workflowTriggerState{}
