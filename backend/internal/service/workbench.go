package service

import (
	"context"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/perm"
)

// GetWorkbench returns the small set of records needed to render the primary
// workflow surface. Operational host details stay restricted to superusers.
func (a *App) GetWorkbench(ctx context.Context, principal *Principal) (M, error) {
	if principal == nil || principal.User == nil {
		return nil, ErrPermission
	}
	ownerID := principal.User.ID
	definitions := []M{}
	if principal.HasPermission(perm.SchedulerWorkflowDefinitionsView) {
		items, err := a.ListWorkflowDefinitions(ownerID)
		if err != nil {
			return nil, err
		}
		definitions = items
	}

	executions := []M{}
	if principal.HasPermission(perm.SchedulerWorkflowExecutionsView) {
		var rows []db.WorkflowExecution
		if err := a.dbWithContext(ctx).Preload("WorkflowDefinition").
			Where("owner_user_id = ? AND status NOT IN ?", ownerID, terminalWorkflowExecutionStatuses).
			Order("id DESC").Limit(100).Find(&rows).Error; err != nil {
			return nil, err
		}
		for i := range rows {
			executions = append(executions, a.serializeExecutionSummary(&rows[i]))
		}
	}

	actions, err := a.ListWorkflowActions(ownerID, "")
	if err != nil {
		return nil, err
	}
	health, err := a.GetSchedulerOverview(ownerID)
	if err != nil {
		return nil, err
	}
	if principal.HasRole("R_SUPER") {
		health["system"], _ = a.GetHomeOverview(ctx)
	}
	return M{
		"workflows": definitions, "executions": executions, "actions": actions,
		"health": health, "nodeDefinitions": a.ListNodeDefinitions(principal),
	}, nil
}
