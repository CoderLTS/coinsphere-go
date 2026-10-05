package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/plugin/sdk"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type WorkflowPublishPayload struct {
	RevisionID                  int64 `json:"revisionId"`
	ExpectedPublishedRevisionID int64 `json:"expectedPublishedRevisionId"`
}

func revisionPointerValue(id *int64) int64 {
	if id == nil {
		return 0
	}
	return *id
}
func graphHasPersistentState(g validatedWorkflowGraph) bool {
	for _, desc := range g.descriptors {
		if desc.State == sdk.StatePersistent {
			return true
		}
	}
	return false
}
func (a *App) PublishWorkflowRevision(ctx context.Context, workflowID int64, payload WorkflowPublishPayload, principal *Principal) (WorkflowDetail, error) {
	if err := a.AuthorizeWorkflow(ctx, workflowID, "workflows.publish"); err != nil {
		return WorkflowDetail{}, err
	}
	if payload.RevisionID <= 0 || payload.ExpectedPublishedRevisionID < 0 {
		return WorkflowDetail{}, errors.New("invalid publish precondition")
	}
	err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var workflow db.Workflow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&workflow, workflowID).Error; err != nil {
			return err
		}
		if revisionPointerValue(workflow.PublishedRevisionID) != payload.ExpectedPublishedRevisionID {
			return fmt.Errorf("%w: published revision changed", ErrConflict)
		}
		var revision db.WorkflowRevision
		if err := tx.Where("workflow_id=? AND id=?", workflowID, payload.RevisionID).First(&revision).Error; err != nil {
			return fmt.Errorf("%w: revision", ErrNotFound)
		}
		g, err := a.validateWorkflowGraph(json.RawMessage(revision.GraphJSON))
		if err != nil {
			return err
		}
		if err := ensureWorkflowRevisionSecrets(tx, workflowID, revision.ID, g); err != nil {
			return err
		}
		if err := a.authorizeExecution(workflow.OwnerUserID, g); err != nil {
			return err
		}
		if err := a.syncRevisionPluginReferences(tx, revision, g); err != nil {
			return err
		}
		now := time.Now().UTC()
		if err := tx.Model(&workflow).Updates(map[string]any{"published_revision_id": revision.ID, "main_trigger_node_id": g.mainTriggerID, "mode": a.workflowModeForTrigger(g.nodes[g.mainTriggerID].NodeType), "updated_at": now}).Error; err != nil {
			return err
		}
		updates := map[string]any{"next_scheduled_at": nil, "trigger_lease_token": nil, "trigger_lease_expires_at": nil, "updated_at": now}
		if graphHasPersistentState(g) {
			updates["max_concurrent_runs"] = 1
		}
		if workflow.Status == WorkflowStatusActive && g.nodes[g.mainTriggerID].NodeType == "core.schedule" {
			next, err := nextWorkflowScheduledAt(g.nodes[g.mainTriggerID].Config, now)
			if err != nil {
				return err
			}
			updates["next_scheduled_at"] = next
		}
		return tx.Model(&db.WorkflowRuntime{}).Where("workflow_id=?", workflowID).Updates(updates).Error
	})
	if err != nil {
		return WorkflowDetail{}, err
	}
	a.stopWorkflowTrigger(workflowID)
	return a.GetWorkflow(ctx, workflowID)
}
func (a *App) syncRevisionPluginReferences(tx *gorm.DB, revision db.WorkflowRevision, g validatedWorkflowGraph) error {
	plugins := map[string]bool{}
	for _, nodeType := range g.nodeTypes {
		if id := a.Plugins.NodePlugin(nodeType); id != "" {
			plugins[id] = true
		}
	}
	ids := make([]string, 0, len(plugins))
	for id := range plugins {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := addPluginReference(tx, id, "revision", fmt.Sprint(revision.ID)); err != nil {
			return err
		}
	}
	return nil
}
func addPluginReference(tx *gorm.DB, pluginID, kind, id string) error {
	var status string
	if err := tx.Raw("SELECT status FROM plugin_installations WHERE plugin_id=? FOR UPDATE", pluginID).Scan(&status).Error; err != nil {
		return err
	}
	if status != "installed" {
		return fmt.Errorf("%w: plugin %s is unavailable", ErrConflict, pluginID)
	}
	return tx.Exec(`INSERT INTO plugin_references(plugin_id,reference_type,reference_id,active) VALUES (?,?,?,TRUE) ON CONFLICT(plugin_id,reference_type,reference_id) DO UPDATE SET active=TRUE`, pluginID, kind, id).Error
}
