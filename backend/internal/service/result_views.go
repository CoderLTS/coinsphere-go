package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/plugin/sdk"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ResultViewCreatePayload struct {
	Name           string          `json:"name"`
	PluginID       string          `json:"pluginId"`
	PageKey        string          `json:"pageKey"`
	Scope          json.RawMessage `json:"scope"`
	Filters        json.RawMessage `json:"filters"`
	AllowedActions []string        `json:"allowedActions"`
	UserIDs        []int64         `json:"userIds"`
	RoleCodes      []string        `json:"roleCodes"`
}

type ResultViewGrantPayload struct {
	UserIDs   []int64  `json:"userIds"`
	RoleCodes []string `json:"roleCodes"`
}

type ResultViewView struct {
	ID             int64           `json:"id"`
	Name           string          `json:"name"`
	PluginID       string          `json:"pluginId"`
	PageKey        string          `json:"pageKey"`
	Scope          json.RawMessage `json:"scope,omitempty"`
	Filters        json.RawMessage `json:"filters,omitempty"`
	AllowedActions []string        `json:"allowedActions"`
	Status         string          `json:"status"`
	UserIDs        []int64         `json:"userIds,omitempty"`
	RoleCodes      []string        `json:"roleCodes,omitempty"`
	CreatedAt      string          `json:"createdAt"`
	RevokedAt      string          `json:"revokedAt,omitempty"`
}

type ResultViewRun struct {
	ID                    int64  `json:"id"`
	Status                string `json:"status"`
	TriggerType           string `json:"triggerType"`
	CurrentNodeInstanceID string `json:"currentNodeInstanceId,omitempty"`
	TriggeredAt           string `json:"triggeredAt"`
	StartedAt             string `json:"startedAt,omitempty"`
	CompletedAt           string `json:"completedAt,omitempty"`
	ErrorCategory         string `json:"errorCategory,omitempty"`
	ErrorMessage          string `json:"errorMessage,omitempty"`
}

func (a *App) CreateResultView(ctx context.Context, payload ResultViewCreatePayload, principal *Principal) (ResultViewView, error) {
	if principal == nil || principal.User == nil || !principal.HasRole("R_SUPER") {
		return ResultViewView{}, ErrPermission
	}
	payload.Name = strings.TrimSpace(payload.Name)
	payload.PluginID = strings.TrimSpace(payload.PluginID)
	payload.PageKey = strings.TrimSpace(payload.PageKey)
	if payload.Name == "" || len(payload.Name) > 120 {
		return ResultViewView{}, errors.New("result view name is required")
	}
	page, ok := a.Plugins.ResultPage(payload.PluginID, payload.PageKey)
	if !ok {
		return ResultViewView{}, errors.New("result page is unavailable")
	}
	scope, err := resultViewObject(payload.Scope)
	if err != nil || validateWorkflowSchemaValue(page.ScopeSchema, scope) != nil {
		return ResultViewView{}, errors.New("result view scope does not match the plugin schema")
	}
	filters, err := resultViewObject(payload.Filters)
	if err != nil || validateWorkflowSchemaValue(page.FilterSchema, filters) != nil {
		return ResultViewView{}, errors.New("result view filters do not match the plugin schema")
	}
	actions, err := resultViewActions(payload.AllowedActions, page.Actions)
	if err != nil {
		return ResultViewView{}, err
	}
	scopeJSON, _ := json.Marshal(scope)
	filtersJSON, _ := json.Marshal(filters)
	actionsJSON, _ := json.Marshal(actions)
	now := time.Now().UTC()
	view := db.ResultView{
		Name: payload.Name, PluginID: payload.PluginID, PageKey: payload.PageKey,
		ScopeJSON: string(scopeJSON), FiltersJSON: string(filtersJSON), AllowedActions: string(actionsJSON),
		Status: "active", CreatedBy: principal.User.ID, CreatedAt: now,
	}
	err = a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&view).Error; err != nil {
			return errors.New("create result view failed")
		}
		return replaceResultViewGrants(tx, view.ID, payload.UserIDs, payload.RoleCodes, now)
	})
	if err != nil {
		return ResultViewView{}, err
	}
	return a.GetResultView(ctx, view.ID, principal)
}

func (a *App) ListResultViews(ctx context.Context, principal *Principal) ([]ResultViewView, error) {
	if principal == nil || principal.User == nil {
		return nil, ErrPermission
	}
	query := a.DB.WithContext(ctx).Model(&db.ResultView{}).Order("created_at DESC, id DESC").Limit(200)
	admin := principal.HasRole("R_SUPER")
	if !admin {
		query = query.Where(`status = 'active' AND (EXISTS (
			SELECT 1 FROM result_view_user_grants g WHERE g.view_id = result_views.id AND g.user_id = ?
		) OR EXISTS (
			SELECT 1 FROM result_view_role_grants g WHERE g.view_id = result_views.id AND g.role_id IN ?
		))`, principal.User.ID, principal.RoleIDs)
	}
	var views []db.ResultView
	if err := query.Find(&views).Error; err != nil {
		return nil, errors.New("list result views failed")
	}
	items := make([]ResultViewView, len(views))
	for index := range views {
		items[index] = a.resultViewView(ctx, views[index], admin)
	}
	return items, nil
}

func (a *App) GetResultView(ctx context.Context, viewID int64, principal *Principal) (ResultViewView, error) {
	view, admin, err := a.authorizedResultView(ctx, viewID, principal, false)
	if err != nil {
		return ResultViewView{}, err
	}
	return a.resultViewView(ctx, view, admin), nil
}

func (a *App) ReplaceResultViewGrants(ctx context.Context, viewID int64, payload ResultViewGrantPayload, principal *Principal) (ResultViewView, error) {
	if principal == nil || principal.User == nil || !principal.HasRole("R_SUPER") {
		return ResultViewView{}, ErrPermission
	}
	now := time.Now().UTC()
	err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var view db.ResultView
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&view, viewID).Error; err != nil || view.Status != "active" {
			return fmt.Errorf("%w: result view", ErrNotFound)
		}
		return replaceResultViewGrants(tx, viewID, payload.UserIDs, payload.RoleCodes, now)
	})
	if err != nil {
		return ResultViewView{}, err
	}
	return a.GetResultView(ctx, viewID, principal)
}

func (a *App) RevokeResultView(ctx context.Context, viewID int64, principal *Principal) (ResultViewView, error) {
	if principal == nil || principal.User == nil || !principal.HasRole("R_SUPER") {
		return ResultViewView{}, ErrPermission
	}
	now := time.Now().UTC()
	result := a.DB.WithContext(ctx).Model(&db.ResultView{}).Where("id = ? AND status = 'active'", viewID).
		Updates(map[string]any{"status": "revoked", "revoked_at": now})
	if result.Error != nil {
		return ResultViewView{}, errors.New("revoke result view failed")
	}
	if result.RowsAffected != 1 {
		return ResultViewView{}, fmt.Errorf("%w: result view", ErrNotFound)
	}
	return a.GetResultView(ctx, viewID, principal)
}

func (a *App) ResolveResultScope(ctx context.Context, viewID int64, action string, principal *Principal) (sdk.ResultScope, error) {
	view, _, err := a.authorizedResultView(ctx, viewID, principal, true)
	if err != nil {
		return sdk.ResultScope{}, err
	}
	actions := []string{}
	if json.Unmarshal([]byte(view.AllowedActions), &actions) != nil {
		return sdk.ResultScope{}, errors.New("result view actions are invalid")
	}
	action = strings.TrimSpace(action)
	if action != "" && !containsString(actions, action) {
		return sdk.ResultScope{}, ErrPermission
	}
	return sdk.ResultScope{
		ViewID: fmt.Sprint(view.ID), PluginID: view.PluginID, PageKey: view.PageKey,
		Scope: json.RawMessage(view.ScopeJSON), Filters: json.RawMessage(view.FiltersJSON), AllowedActions: actions,
		UserID: principal.User.ID, RoleCodes: append([]string(nil), principal.RoleCodes...),
		HumanTasks: resultHumanTasks{app: a},
	}, nil
}

func (a *App) ApplyResultScopeRunAction(ctx context.Context, scope sdk.ResultScope, runID int64, action string) (WorkflowRunView, error) {
	workflowID, err := resultScopeWorkflowID(scope)
	if err != nil || runID <= 0 || action != "retry" && action != "cancel" {
		return WorkflowRunView{}, fmt.Errorf("%w: result run", ErrNotFound)
	}
	var run db.WorkflowRun
	if err := a.DB.WithContext(ctx).Select("id", "workflow_id").First(&run, runID).Error; err != nil || run.WorkflowID != workflowID {
		return WorkflowRunView{}, fmt.Errorf("%w: result run", ErrNotFound)
	}
	return a.ApplyWorkflowRunAction(ctx, runID, WorkflowRunActionPayload{Action: action})
}

func (a *App) ListResultScopeRuns(ctx context.Context, scope sdk.ResultScope) ([]ResultViewRun, error) {
	workflowID, err := resultScopeWorkflowID(scope)
	if err != nil {
		return nil, fmt.Errorf("%w: result workflow", ErrNotFound)
	}
	runs, err := a.ListRecentWorkflowRuns(ctx, workflowID)
	if err != nil {
		return nil, err
	}
	items := make([]ResultViewRun, len(runs))
	for index, run := range runs {
		items[index] = ResultViewRun{
			ID: run.ID, Status: run.Status, TriggerType: run.TriggerType,
			CurrentNodeInstanceID: run.CurrentNodeInstanceID, TriggeredAt: run.TriggeredAt,
			StartedAt: run.StartedAt, CompletedAt: run.CompletedAt,
			ErrorCategory: run.ErrorCategory, ErrorMessage: run.ErrorMessage,
		}
	}
	return items, nil
}

func (a *App) PauseResultScopeWorkflow(ctx context.Context, scope sdk.ResultScope) (WorkflowDetail, error) {
	workflowID, err := resultScopeWorkflowID(scope)
	if err != nil {
		return WorkflowDetail{}, fmt.Errorf("%w: result workflow", ErrNotFound)
	}
	return a.ApplyWorkflowLifecycle(ctx, workflowID, WorkflowLifecyclePayload{Action: "deactivate"})
}

func resultScopeWorkflowID(scope sdk.ResultScope) (int64, error) {
	var fixed struct {
		WorkflowID int64 `json:"workflowId"`
	}
	if json.Unmarshal(scope.Scope, &fixed) != nil || fixed.WorkflowID <= 0 {
		return 0, errors.New("result scope has no workflow")
	}
	return fixed.WorkflowID, nil
}

type resultHumanTasks struct{ app *App }

func (s resultHumanTasks) Decide(ctx context.Context, taskID int64, action string, userID int64) error {
	principal, err := s.app.buildPrincipal(userID)
	if err != nil {
		return ErrPermission
	}
	_, err = s.app.DecideWorkflowHumanTask(ctx, taskID, WorkflowHumanTaskDecision{Action: action}, principal)
	return err
}

func (a *App) authorizedResultView(ctx context.Context, viewID int64, principal *Principal, activeOnly bool) (db.ResultView, bool, error) {
	if principal == nil || principal.User == nil || viewID <= 0 {
		return db.ResultView{}, false, fmt.Errorf("%w: result view", ErrNotFound)
	}
	var view db.ResultView
	if err := a.DB.WithContext(ctx).First(&view, viewID).Error; err != nil {
		return db.ResultView{}, false, fmt.Errorf("%w: result view", ErrNotFound)
	}
	admin := principal.HasRole("R_SUPER")
	if activeOnly && view.Status != "active" {
		return db.ResultView{}, false, fmt.Errorf("%w: result view", ErrNotFound)
	}
	if admin {
		return view, true, nil
	}
	if view.Status != "active" {
		return db.ResultView{}, false, fmt.Errorf("%w: result view", ErrNotFound)
	}
	var grants int64
	if err := a.DB.WithContext(ctx).Raw(`SELECT COUNT(*) FROM (
		SELECT 1 FROM result_view_user_grants WHERE view_id = ? AND user_id = ?
		UNION ALL
		SELECT 1 FROM result_view_role_grants WHERE view_id = ? AND role_id IN ?
	) authorized`, viewID, principal.User.ID, viewID, principal.RoleIDs).Scan(&grants).Error; err != nil || grants == 0 {
		return db.ResultView{}, false, fmt.Errorf("%w: result view", ErrNotFound)
	}
	return view, false, nil
}

func (a *App) resultViewView(ctx context.Context, view db.ResultView, admin bool) ResultViewView {
	actions := []string{}
	_ = json.Unmarshal([]byte(view.AllowedActions), &actions)
	result := ResultViewView{
		ID: view.ID, Name: view.Name, PluginID: view.PluginID, PageKey: view.PageKey,
		AllowedActions: actions, Status: view.Status, CreatedAt: formatWorkflowTime(view.CreatedAt),
	}
	if view.RevokedAt != nil {
		result.RevokedAt = formatWorkflowTime(*view.RevokedAt)
	}
	if admin {
		result.Scope, result.Filters = json.RawMessage(view.ScopeJSON), json.RawMessage(view.FiltersJSON)
		_ = a.DB.WithContext(ctx).Model(&db.ResultViewUserGrant{}).Where("view_id = ?", view.ID).Order("user_id").Pluck("user_id", &result.UserIDs).Error
		_ = a.DB.WithContext(ctx).Table("result_view_role_grants grants").Select("roles.code").
			Joins("JOIN roles ON roles.id = grants.role_id").Where("grants.view_id = ?", view.ID).Order("roles.code").Scan(&result.RoleCodes).Error
	}
	return result
}

func replaceResultViewGrants(tx *gorm.DB, viewID int64, userIDs []int64, roleCodes []string, now time.Time) error {
	userIDs = uniquePositiveInt64s(userIDs)
	roleCodes = uniqueStrings(roleCodes)
	if len(userIDs) > 0 {
		var count int64
		if err := tx.Model(&db.SystemUser{}).Where("id IN ? AND is_active = TRUE", userIDs).Count(&count).Error; err != nil || count != int64(len(userIDs)) {
			return errors.New("result view contains an unknown user grant")
		}
	}
	var roles []db.SystemRole
	if len(roleCodes) > 0 {
		if err := tx.Where("code IN ? AND is_enabled = TRUE", roleCodes).Find(&roles).Error; err != nil || len(roles) != len(roleCodes) {
			return errors.New("result view contains an unknown role grant")
		}
	}
	if err := tx.Where("view_id = ?", viewID).Delete(&db.ResultViewUserGrant{}).Error; err != nil {
		return errors.New("replace result view user grants failed")
	}
	if err := tx.Where("view_id = ?", viewID).Delete(&db.ResultViewRoleGrant{}).Error; err != nil {
		return errors.New("replace result view role grants failed")
	}
	for _, userID := range userIDs {
		if err := tx.Create(&db.ResultViewUserGrant{ViewID: viewID, UserID: userID, CreatedAt: now}).Error; err != nil {
			return errors.New("create result view user grant failed")
		}
	}
	for _, role := range roles {
		if err := tx.Create(&db.ResultViewRoleGrant{ViewID: viewID, RoleID: role.ID, CreatedAt: now}).Error; err != nil {
			return errors.New("create result view role grant failed")
		}
	}
	return nil
}

func resultViewObject(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return nil, errors.New("result view value must be a JSON object")
	}
	return value, nil
}

func resultViewActions(requested, declared []string) ([]string, error) {
	available := map[string]bool{}
	for _, action := range declared {
		available[action] = true
	}
	result := uniqueStrings(requested)
	for _, action := range result {
		if !available[action] {
			return nil, fmt.Errorf("result action %q is unavailable", action)
		}
	}
	return result, nil
}

func uniqueStrings(values []string) []string {
	set := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			set[value] = true
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func uniquePositiveInt64s(values []int64) []int64 {
	set := map[int64]bool{}
	for _, value := range values {
		if value > 0 {
			set[value] = true
		}
	}
	result := make([]int64, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}
