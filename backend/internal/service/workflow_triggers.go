package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

type workflowTriggerKey struct {
	workflowID int64
	nodeID     string
}

const (
	WorkflowTriggerStatusRunning  = "running"
	WorkflowTriggerStatusWaiting  = "waiting"
	WorkflowTriggerStatusError    = "error"
	WorkflowTriggerStatusDisabled = "disabled"
)

type workflowTriggerRun struct {
	connectionVersion int64
	revisionID        int64
	cancel            context.CancelFunc
	token             chan struct{}
	done              chan struct{}
}

type workflowTriggerEmitter struct {
	nodeID     string
	app        *App
	workflowID int64
}

func (e workflowTriggerEmitter) Emit(ctx context.Context, event cloudevents.Event) error {
	ticker := time.NewTicker(runPollInterval)
	defer ticker.Stop()
	for {
		err := e.app.publishWorkflowTriggerEvent(ctx, event, e.workflowID, e.nodeID)
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

func (a *App) publishWorkflowTriggerEvent(ctx context.Context, event cloudevents.Event, workflowID int64, nodeID string) error {
	var record db.WorkflowEventRecord
	err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		record, err = a.persistWorkflowEventTx(tx, event, workflowID, nodeID)
		return err
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
	return (&bufferedNodeState{
		app: s.app, workflowID: s.workflowID, revisionID: s.revisionID, node: s.node,
		scopeKey: workflowTriggerStateScope(s.workflowID, s.revisionID, s.node.NodeInstanceID), stateMode: s.stateMode,
	}).Load(ctx)
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
		ScopeKey:   workflowTriggerStateScope(s.workflowID, s.revisionID, s.node.NodeInstanceID),
		WorkflowID: s.workflowID, NodeInstanceID: s.node.NodeInstanceID, NodeType: s.node.NodeType,
		RevisionID: s.revisionID, StateJSON: string(state), UpdatedAt: now,
	}
	if err := s.app.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "workflow_id"}, {Name: "node_instance_id"}, {Name: "scope_key"}},
		DoUpdates: clause.Assignments(map[string]any{
			"node_type": row.NodeType, "revision_id": row.RevisionID, "state_json": row.StateJSON, "updated_at": now,
		}),
	}).Create(&row).Error; err != nil {
		return errors.New("save workflow trigger state failed")
	}
	return nil
}

func workflowTriggerStateScope(workflowID, revisionID int64, nodeID string) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("trigger:%d:%d:%s", workflowID, revisionID, nodeID)))
	return hex.EncodeToString(digest[:])
}

func syncWorkflowTriggerRuntimes(tx *gorm.DB, workflowID, revisionID int64, graph validatedWorkflowGraph, active bool, now time.Time) error {
	if err := tx.Where("workflow_id = ?", workflowID).Delete(&db.WorkflowTriggerRuntime{}).Error; err != nil {
		return err
	}
	for _, id := range graph.triggerIDs {
		row := db.WorkflowTriggerRuntime{WorkflowID: workflowID, RevisionID: revisionID, NodeInstanceID: id, Status: WorkflowTriggerStatusDisabled, UpdatedAt: now}
		if active {
			row.Status = WorkflowTriggerStatusRunning
			if graph.nodes[id].NodeType == "core.schedule" {
				next, err := nextWorkflowScheduledAt(graph.nodes[id].Config, now)
				if err != nil {
					return err
				}
				row.NextScheduledAt = &next
			}
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

func (a *App) syncWorkflowTriggers(ctx context.Context) error {
	now := time.Now().UTC()
	if err := a.DB.WithContext(ctx).Exec(`UPDATE workflow_trigger_runtimes rt SET status='running',next_retry_at=NULL,updated_at=? FROM workflows w WHERE w.id=rt.workflow_id AND w.status='active' AND rt.revision_id=w.active_revision_id AND rt.status='waiting' AND rt.next_retry_at<=?`, now, now).Error; err != nil {
		return err
	}
	var rows []db.WorkflowTriggerRuntime
	if err := a.DB.WithContext(ctx).Raw(`SELECT rt.* FROM workflow_trigger_runtimes rt JOIN workflows w ON w.id=rt.workflow_id WHERE w.status='active' AND rt.status='running' AND rt.revision_id=w.active_revision_id`).Scan(&rows).Error; err != nil {
		return err
	}
	desired := map[workflowTriggerKey]db.WorkflowTriggerRuntime{}
	versions := map[workflowTriggerKey]int64{}
	for _, row := range rows {
		var revision db.WorkflowRevision
		if err := a.DB.WithContext(ctx).First(&revision, row.RevisionID).Error; err != nil {
			a.workflowTriggerError(row, "revision")
			continue
		}
		graph, err := a.buildWorkflowRunGraph(revision.GraphJSON)
		if err != nil {
			a.workflowTriggerError(row, "graph")
			continue
		}
		node := graph.nodes[row.NodeInstanceID]
		key := workflowTriggerKey{row.WorkflowID, row.NodeInstanceID}
		if desc := graph.descriptors[row.NodeInstanceID]; desc.ConnectionType != "" {
			err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				_, _, version, err := a.currentConnection(tx, node.ConnectionID, desc.ConnectionType)
				versions[key] = version
				return err
			})
			if err != nil {
				a.workflowTriggerError(row, "connection")
				continue
			}
		}
		if a.Plugins != nil {
			if _, _, ok := a.Plugins.Trigger(node.NodeType); ok {
				desired[workflowTriggerKey{row.WorkflowID, row.NodeInstanceID}] = row
			}
		}
	}
	a.triggerMu.Lock()
	for key, running := range a.triggerRuns {
		row, exists := desired[key]
		if !exists || row.RevisionID != running.revisionID || versions[key] != running.connectionVersion {
			running.cancel()
			select {
			case <-running.done:
				delete(a.triggerRuns, key)
			default:
				delete(desired, key)
			}
		}
	}
	a.triggerMu.Unlock()
	for key, row := range desired {
		a.triggerMu.Lock()
		_, running := a.triggerRuns[key]
		a.triggerMu.Unlock()
		if running {
			continue
		}
		if err := a.startWorkflowTrigger(ctx, row, versions[key]); err != nil {
			a.workflowTriggerError(row, "trigger_start")
		}
	}
	return nil
}

func (a *App) workflowTriggerError(row db.WorkflowTriggerRuntime, category string) {
	now := time.Now().UTC()
	status := WorkflowTriggerStatusError
	var nextRetryAt *time.Time
	retryCount := row.RetryCount
	if retryCount < 3 {
		retryCount++
		when := now.Add(time.Duration(1<<retryCount) * time.Second)
		nextRetryAt = &when
		status = WorkflowTriggerStatusWaiting
	}
	_ = a.DB.Model(&db.WorkflowTriggerRuntime{}).Where("workflow_id=? AND node_instance_id=? AND revision_id=? AND status='running'", row.WorkflowID, row.NodeInstanceID, row.RevisionID).Updates(map[string]any{"status": status, "error_category": category, "retry_count": retryCount, "next_retry_at": nextRetryAt, "updated_at": now}).Error
	slog.Error("workflow trigger failed", "component", "workflow.runtime", "workflow_id", row.WorkflowID, "node_instance_id", row.NodeInstanceID, "error_category", category)
}

func (a *App) startWorkflowTrigger(parent context.Context, runtime db.WorkflowTriggerRuntime, connectionVersion int64) error {
	revisionID := runtime.RevisionID
	key := workflowTriggerKey{runtime.WorkflowID, runtime.NodeInstanceID}
	var revision db.WorkflowRevision
	if err := a.DB.WithContext(parent).First(&revision, revisionID).Error; err != nil {
		return errors.New("load workflow trigger revision failed")
	}
	graph, err := a.buildWorkflowRunGraph(revision.GraphJSON)
	if err != nil {
		return err
	}
	node := graph.nodes[runtime.NodeInstanceID]
	desc, handler, ok := a.Plugins.Trigger(node.NodeType)
	if !ok {
		return fmt.Errorf("trigger handler %q is unavailable", node.NodeType)
	}
	ctx, cancel := context.WithCancel(parent)
	token := make(chan struct{})
	a.triggerMu.Lock()
	if _, exists := a.triggerRuns[key]; exists {
		a.triggerMu.Unlock()
		cancel()
		return nil
	}
	done := make(chan struct{})
	a.triggerRuns[key] = workflowTriggerRun{connectionVersion: connectionVersion, revisionID: revisionID, cancel: cancel, token: token, done: done}
	a.triggerMu.Unlock()
	request := sdk.TriggerRequest{
		Revision:       sdk.RevisionRef{WorkflowID: fmt.Sprint(runtime.WorkflowID), RevisionID: fmt.Sprint(revisionID)},
		NodeInstanceID: node.NodeInstanceID, ProfileBindings: node.ProfileBindings, Profiles: a.Profiles, Config: append(json.RawMessage(nil), node.Config...),
		Secrets: workflowSecretReader{app: a, revisionID: revisionID, nodeInstanceID: node.NodeInstanceID},
		State:   workflowTriggerState{app: a, workflowID: runtime.WorkflowID, revisionID: revisionID, node: node, stateMode: desc.State},
		Logger:  slog.Default().With("event_category", "workflow_trigger", "node_type", node.NodeType),
	}
	if desc.ConnectionType != "" {
		config, secrets, err := a.workflowConnection(ctx, 0, node, desc)
		if err != nil {
			cancel()
			a.triggerMu.Lock()
			delete(a.triggerRuns, key)
			a.triggerMu.Unlock()
			close(done)
			return err
		}
		request.Config = config
		request.Secrets = secrets
	}
	a.triggerWG.Add(1)
	go func() {
		defer a.triggerWG.Done()
		defer close(done)
		_ = handler.Run(ctx, request, workflowTriggerEmitter{app: a, workflowID: runtime.WorkflowID, nodeID: runtime.NodeInstanceID})
		stoppedByCancellation := ctx.Err() != nil
		cancel()
		a.triggerMu.Lock()
		current, ownsRun := a.triggerRuns[key]
		ownsRun = ownsRun && current.token == token
		if !ownsRun {
			a.triggerMu.Unlock()
			return
		}
		if !stoppedByCancellation {
			a.workflowTriggerError(runtime, "trigger_run")
		}
		delete(a.triggerRuns, key)
		a.triggerMu.Unlock()
	}()
	return nil
}

func (a *App) stopWorkflowTrigger(workflowID int64) {
	var done []chan struct{}
	a.triggerMu.Lock()
	for key, running := range a.triggerRuns {
		if key.workflowID == workflowID {
			running.cancel()
			done = append(done, running.done)
			delete(a.triggerRuns, key)
		}
	}
	a.triggerMu.Unlock()
	for _, wait := range done {
		<-wait
	}
}

func (a *App) stopWorkflowTriggers() {
	a.triggerMu.Lock()
	for key, running := range a.triggerRuns {
		running.cancel()
		delete(a.triggerRuns, key)
	}
	a.triggerMu.Unlock()
}

var _ sdk.Emitter = workflowTriggerEmitter{}
var _ sdk.StateStore = workflowTriggerState{}
