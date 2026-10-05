package notification

import (
	"coinsphere/backend/plugin/sdk"
	"context"
	"gorm.io/gorm"
)

func cleanupWorkflow(ctx context.Context, tx *gorm.DB, request sdk.CleanupRequest) error {
	var statements []string
	args := []any{request.WorkflowID}
	if request.RevisionID != nil {
		args = append(args, *request.RevisionID)
		statements = []string{`DELETE FROM plugin_notification.deliveries WHERE workflow_id = ? AND revision_id = ?`}
	} else {
		statements = []string{`DELETE FROM plugin_notification.deliveries WHERE workflow_id = ?`}
	}
	for _, statement := range statements {
		if err := tx.WithContext(ctx).Exec(statement, args...).Error; err != nil {
			return err
		}
	}
	return nil
}
