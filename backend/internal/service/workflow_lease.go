package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/plugin/sdk"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func runLeaseQuery(tx *gorm.DB, run db.WorkflowRun, allowCancelled bool) *gorm.DB {
	q := tx.Model(&db.WorkflowRun{}).Where("id=? AND status=? AND lease_token=? AND lease_expires_at > clock_timestamp()", run.ID, RunStatusRunning, run.LeaseToken)
	if !allowCancelled {
		q = q.Where("cancel_requested_at IS NULL")
	}
	return q
}

// 在同一事务锁定有效租约，避免检查之后被恢复器或新执行者覆盖。
func lockRunLease(tx *gorm.DB, run db.WorkflowRun, allowCancelled bool, result *db.WorkflowRun) error {
	if result == nil {
		result = &db.WorkflowRun{}
	}
	if err := runLeaseQuery(tx, run, allowCancelled).Clauses(clause.Locking{Strength: "UPDATE"}).First(result).Error; err != nil {
		return fmt.Errorf("%w: execution lease is no longer valid", ErrConflict)
	}
	return nil
}

func mustPrincipal(a *App, id int64) *Principal {
	p, err := a.buildPrincipal(id)
	if err != nil {
		return nil
	}
	return p
}
func (a *App) recordSkippedNode(ctx context.Context, run db.WorkflowRun, id string, node workflowGraphNode, iteration int) error {
	return a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockRunLease(tx, run, false, nil); err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&db.WorkflowRunNode{}).Where("run_id=? AND node_instance_id=? AND loop_iteration=? AND status='skipped'", run.ID, id, iteration).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return nil
		}
		now := time.Now().UTC()
		return tx.Create(&db.WorkflowRunNode{RunID: run.ID, NodeInstanceID: id, NodeType: node.NodeType, NodeVersion: node.NodeVersion, Status: "skipped", ExecutionPool: string(a.workflowNodeDescriptors()[node.NodeType].Pool), DurationMS: new(int64), Attempt: 1, LoopIteration: iteration, OperationKey: workflowOperationKey(run.ID, id, iteration), InputSummary: "{}", OutputSummary: "{}", StartedAt: now, CompletedAt: &now}).Error
	})
}

type executionLeaseKey struct{}

func (a *App) lockExecutionLease(tx *gorm.DB, expected db.WorkflowRun, result *db.WorkflowRun) error {
	if err := lockRunLease(tx, expected, false, result); err != nil {
		return err
	}
	p, err := principalForTx(tx, &Principal{User: &db.SystemUser{ID: expected.ExecutionUserID}})
	if err != nil {
		return err
	}
	if err := authorizeWorkflowTx(tx, p, expected.WorkflowID, "workflows.run"); err != nil {
		return err
	}
	var revision db.WorkflowRevision
	if err := tx.First(&revision, expected.RevisionID).Error; err != nil {
		return err
	}
	graph, err := a.validateWorkflowGraph(json.RawMessage(revision.GraphJSON))
	if err != nil {
		return err
	}
	return authorizePluginExecution(p, graph)
}

func (a *App) workflowNodeRetrySafe(desc sdk.NodeDescriptor, config json.RawMessage) bool {
	if desc.SideEffect == sdk.SideEffectNone || desc.RetrySafe {
		return true
	}
	_, handler, ok := a.Plugins.Action(desc.Type)
	if !ok {
		return false
	}
	policy, ok := handler.(sdk.SafeRetry)
	return ok && policy.RetrySafe(config)
}
func authorizePluginExecution(p *Principal, g validatedWorkflowGraph) error {
	for _, desc := range g.descriptors {
		for _, code := range desc.ExecutionPermissions {
			if !p.HasPermission(code) {
				return ErrPermission
			}
		}
	}
	return nil
}
