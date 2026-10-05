package binance

import (
	"coinsphere/backend/plugin/sdk"
	"context"
	"errors"
	"gorm.io/gorm"
)

func cleanupWorkflow(ctx context.Context, tx *gorm.DB, request sdk.CleanupRequest) error {
	var statements []string
	args := []any{request.WorkflowID}
	if request.RevisionID != nil {
		args = append(args, *request.RevisionID)
		statements = []string{}
	} else {
		var facts int64
		// Financial facts are retained by the plugin; deleting a workflow cannot erase an unsettled account.
		if err := tx.WithContext(ctx).Table("plugin_binance.orders").Where("workflow_id=?", request.WorkflowID).Count(&facts).Error; err != nil {
			return err
		}
		if facts > 0 {
			return errors.New("Binance workflow has financial facts; reconcile and archive its account before deletion")
		}
		statements = []string{`DELETE FROM plugin_binance.fees WHERE fill_id IN (SELECT fill.id FROM plugin_binance.fills fill JOIN plugin_binance.orders order_row ON order_row.id = fill.order_id WHERE order_row.workflow_id = ?)`, `DELETE FROM plugin_binance.fills WHERE order_id IN (SELECT id FROM plugin_binance.orders WHERE workflow_id = ?)`, `DELETE FROM plugin_binance.orders WHERE workflow_id = ?`}
	}
	for _, statement := range statements {
		if err := tx.WithContext(ctx).Exec(statement, args...).Error; err != nil {
			return err
		}
	}
	return nil
}
