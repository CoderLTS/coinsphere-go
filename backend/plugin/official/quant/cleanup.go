package quant

import (
	"coinsphere/backend/plugin/sdk"
	"context"
	"gorm.io/gorm"
)

func cleanupWorkflow(ctx context.Context, tx *gorm.DB, request sdk.CleanupRequest) error {
	query := "workflow_id=?"
	args := []any{request.WorkflowID}
	if request.RevisionID != nil {
		query += " AND revision_id=?"
		args = append(args, *request.RevisionID)
	}
	for _, table := range []string{"plugin_quant.backtests", "plugin_quant.market_signals"} {
		if err := tx.WithContext(ctx).Exec("DELETE FROM "+table+" WHERE "+query, args...).Error; err != nil {
			return err
		}
	}
	if err := tx.WithContext(ctx).Exec("UPDATE plugin_quant.signals SET superseded_by=NULL WHERE superseded_by IN (SELECT id FROM plugin_quant.signals WHERE "+query+")", args...).Error; err != nil {
		return err
	}
	return tx.WithContext(ctx).Exec("DELETE FROM plugin_quant.signals WHERE "+query, args...).Error
}
