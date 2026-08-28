package service

import (
	"context"
	"errors"
	"time"

	"coinsphere/backend/internal/db"
	"gorm.io/gorm"
)

const quantInstrumentWorkflowGraph = `{
  "schemaVersion": 1,
  "nodes": [
    {"nodeInstanceId":"schedule","nodeType":"core.schedule","nodeVersion":"1.0.0","config":{"cronExpression":"0 0 */6 * * *","timeZone":"Asia/Shanghai"},"position":{"x":100,"y":220}},
    {"nodeInstanceId":"sync-instruments","nodeType":"official.quant.sync_instruments","nodeVersion":"1.0.0","config":{"markets":["spot","usdm"],"quoteAssets":["USDT","USDC"],"baseAssetAllowlist":[],"baseAssetDenylist":[],"symbolAllowlist":[],"symbolDenylist":[]},"position":{"x":400,"y":220}},
    {"nodeInstanceId":"end","nodeType":"core.end","nodeVersion":"1.0.0","config":{},"position":{"x":700,"y":220}}
  ],
  "edges": [
    {"edgeId":"schedule-to-sync","sourceNodeInstanceId":"schedule","sourcePort":"out","targetNodeInstanceId":"sync-instruments","targetPort":"in"},
    {"edgeId":"sync-to-end","sourceNodeInstanceId":"sync-instruments","sourcePort":"out","targetNodeInstanceId":"end","targetPort":"in"}
  ]
}`

func (a *App) EnsureQuantInstrumentWorkflow(ctx context.Context) (bool, error) {
	graph, err := a.validateWorkflowGraph([]byte(quantInstrumentWorkflowGraph))
	if err != nil {
		return false, errors.New("built-in Quant instrument workflow is invalid")
	}
	created := false
	err = a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", int64(0x43535157464c4f57)).Error; err != nil {
			return errors.New("lock built-in workflow initialization failed")
		}
		var count int64
		if err := tx.Raw(`SELECT COUNT(*)
FROM workflows AS workflow
JOIN workflow_revisions AS revision ON revision.id = workflow.active_revision_id
WHERE jsonb_path_exists(revision.graph_json, '$.nodes[*] ? (@.nodeType == "official.quant.sync_instruments")')`).Scan(&count).Error; err != nil {
			return errors.New("inspect built-in Quant instrument workflow failed")
		}
		if count > 0 {
			return nil
		}
		var user db.SystemUser
		if err := tx.Where("username = ?", "coinsphere").First(&user).Error; err != nil {
			return errors.New("load built-in workflow owner failed")
		}
		now := time.Now().UTC()
		workflow, err := createWorkflowRecord(tx, "Binance 币种元数据采集", "每 6 小时同步 Binance Spot 与 USD-M 币种元数据", graph, user.ID, now)
		if err != nil {
			return err
		}
		if err := tx.Model(&db.Workflow{}).Where("id = ?", workflow.ID).Updates(map[string]any{
			"status": WorkflowStatusActive, "updated_at": now,
		}).Error; err != nil {
			return errors.New("activate built-in Quant instrument workflow failed")
		}
		if err := tx.Model(&db.WorkflowRuntime{}).Where("workflow_id = ?", workflow.ID).Updates(map[string]any{
			"next_scheduled_at": now, "updated_at": now,
		}).Error; err != nil {
			return errors.New("schedule built-in Quant instrument workflow failed")
		}
		created = true
		return nil
	})
	return created, err
}
