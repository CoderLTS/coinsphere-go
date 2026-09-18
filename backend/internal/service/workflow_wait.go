package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/plugin/sdk"
	"gorm.io/gorm"
)

func (a *App) workflowResume(ctx context.Context, run db.WorkflowRun, nodeID string) (*sdk.ResumeContext, error) {
	var wait db.WorkflowWait
	err := a.DB.WithContext(ctx).Where("run_id=? AND node_instance_id=? AND status='active'", run.ID, nodeID).First(&wait).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sdk.ResumeContext{Data: json.RawMessage(wait.DataJSON), TimedOut: !time.Now().UTC().Before(wait.Until)}, nil
}

// 保存等待与释放租约在同一事务中完成，避免崩溃后既没有等待记录也无法恢复。
func (a *App) persistWorkflowWait(ctx context.Context, run db.WorkflowRun, runNode db.WorkflowRunNode, wait sdk.WaitRequest) (bool, error) {
	now := time.Now().UTC()
	if wait.Key == "" || len(wait.Key) > 256 || wait.Until.IsZero() || len(wait.Data) > maxWorkflowGraphBytes || !json.Valid(wait.Data) {
		return false, errors.New("invalid plugin wait request")
	}
	if wait.WakeAt.IsZero() || wait.WakeAt.After(wait.Until) {
		wait.WakeAt = wait.Until
	}
	if !wait.WakeAt.After(now) {
		wait.WakeAt = now.Add(time.Second)
	}
	owner := true
	err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockWorkflowRunLease(tx, run, now); err != nil {
			return err
		}
		// 多个入口可以触发同一观察窗口；只保留第一个运行的截止时间。
		key := mustJSONString([]any{run.WorkflowID, runNode.NodeInstanceID, wait.Key})
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended(?,0))`, key).Error; err != nil {
			return err
		}
		var existing db.WorkflowWait
		err := tx.Where("workflow_id=? AND node_instance_id=? AND wait_key=? AND status='active'", run.WorkflowID, runNode.NodeInstanceID, wait.Key).First(&existing).Error
		if err == nil && existing.RunID != run.ID {
			owner = false
			return nil
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if existing.ID > 0 {
			if !existing.Until.Equal(wait.Until) {
				return errors.New("a resumed wait cannot extend its deadline")
			}
			if err := tx.Model(&existing).Updates(map[string]any{"wake_at": wait.WakeAt, "data_json": string(wait.Data), "updated_at": now}).Error; err != nil {
				return err
			}
		} else {
			row := db.WorkflowWait{BlockFollowingRuns: wait.BlockFollowingRuns, WorkflowID: run.WorkflowID, RevisionID: run.RevisionID, RunID: run.ID, NodeInstanceID: runNode.NodeInstanceID, WaitKey: wait.Key, Until: wait.Until, WakeAt: wait.WakeAt, SubscriptionJSON: mustJSONString(wait.Subscription), DataJSON: string(wait.Data), Status: "active", CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&runNode).Updates(map[string]any{"status": RunStatusWaiting, "completed_at": now, "duration_ms": now.Sub(runNode.StartedAt).Milliseconds()}).Error; err != nil {
			return err
		}
		result := tx.Model(&db.WorkflowRun{}).Where("id=? AND status='running' AND lease_token=? AND cancel_requested_at IS NULL", run.ID, run.LeaseToken).Updates(map[string]any{"status": RunStatusWaiting, "lease_token": nil, "lease_expires_at": nil, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("run no longer owns wait lease")
		}
		return nil
	})
	return owner, err
}

func (a *App) resumeWorkflowWaits(ctx context.Context, now time.Time) error {
	return a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`UPDATE workflow_waits t SET status='cancelled',updated_at=? FROM workflow_runs r WHERE r.id=t.run_id AND t.status='active' AND (r.cancel_requested_at IS NOT NULL OR r.status IN ('cancelled','failed'))`, now).Error; err != nil {
			return err
		}
		var runs []db.WorkflowRun
		if err := tx.Raw(`SELECT r.* FROM workflow_runs r JOIN workflow_waits t ON t.run_id=r.id AND t.node_instance_id=r.current_node_instance_id JOIN workflows w ON w.id=r.workflow_id WHERE r.status='waiting' AND r.cancel_requested_at IS NULL AND t.status='active' AND t.wake_at<=? AND w.status='active' ORDER BY t.wake_at LIMIT 100 FOR UPDATE OF r SKIP LOCKED`, now).Scan(&runs).Error; err != nil {
			return err
		}
		for _, run := range runs {
			if err := tx.Model(&run).Updates(map[string]any{"status": RunStatusQueued, "not_before": now, "updated_at": now}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func wakeWorkflowWaits(tx *gorm.DB, record db.WorkflowEventRecord, now time.Time) error {
	return tx.Exec(`UPDATE workflow_waits SET wake_at=LEAST(wake_at,?),updated_at=? WHERE status='active' AND subscription_json->'types' @> jsonb_build_array(CAST(? AS text)) AND (COALESCE(subscription_json->>'source','')='' OR subscription_json->>'source'=?) AND (COALESCE(subscription_json->>'subject','')='' OR subscription_json->>'subject'=?)`, now, now, record.EventType, record.Source, record.Subject).Error
}

func finishWorkflowWait(tx *gorm.DB, runID int64, nodeID string, now time.Time) error {
	return tx.Model(&db.WorkflowWait{}).Where("run_id=? AND node_instance_id=? AND status='active'", runID, nodeID).Updates(map[string]any{"status": "completed", "updated_at": now}).Error
}

func lockWorkflowRunLease(tx *gorm.DB, run db.WorkflowRun, now time.Time) error {
	var id int64
	if err := tx.Raw(`SELECT id FROM workflow_runs WHERE id=? AND status='running' AND lease_token=? AND lease_expires_at>? AND cancel_requested_at IS NULL FOR UPDATE`, run.ID, run.LeaseToken, now).Scan(&id).Error; err != nil {
		return err
	}
	if id == 0 {
		return context.Canceled
	}
	return nil
}
