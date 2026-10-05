package service

import (
	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/perm"
	"coinsphere/backend/plugin/sdk"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"sort"
	"strings"
)

func (a *App) SyncCapabilities(ctx context.Context) error {
	return a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		catalog := a.CapabilityCatalog()
		for _, p := range catalog {
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "code"}}, DoUpdates: clause.AssignmentColumns([]string{"title", "plugin_id", "protected"})}).Create(&p).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
func (a *App) CapabilityCatalog() map[string]db.Permission {
	catalog := map[string]db.Permission{}
	add := func(code, title string, protected bool) {
		if code != "" {
			catalog[code] = db.Permission{Code: code, Title: title, Protected: protected}
		}
	}
	for _, code := range perm.MenuPermissionCodes {
		add(code, code, false)
	}
	for _, buttons := range perm.ButtonSpecs {
		for _, b := range buttons {
			add(b.Code, b.Title, false)
		}
	}
	for code, title := range map[string]string{"workflows.read": "查看工作流", "workflows.create": "创建工作流", "workflows.update": "编辑工作流", "workflows.publish": "发布工作流", "workflows.activate": "启停工作流", "workflows.run": "手工运行与诊断", "workflows.cancel": "取消运行", "workflows.retry": "重试运行", "workflows.delete": "删除工作流及修订", "workflows.share": "授权工作流", "workflows.secrets.manage": "管理工作流凭据", "human_tasks.read": "查看待办", "human_tasks.decide": "处理待办", "workflow_groups.manage": "管理分组", "result_views.read": "查看结果", "result_views.manage": "管理结果授权", "result_views.export": "导出结果", "notifications.read": "查看个人通知", "assistant.use": "使用助手", "system.observe": "系统观测", "config.ai.manage": "管理模型", "system.users.assign_roles": "授予用户角色"} {
		add(code, title, false)
	}
	for code := range sdk.CoreScopePermissions {
		if catalog[code].Code == "" {
			add(code, code, false)
		}
	}
	for _, plugin := range a.Plugins.Plugins() {
		for _, p := range plugin.Permissions {
			catalog[p.Code] = db.Permission{Code: p.Code, Title: p.Title, PluginID: plugin.ID, Protected: p.Protected}
		}
	}
	return catalog
}

func (a *App) ListCapabilities(ctx context.Context) ([]M, error) {
	var rows []db.Permission
	if err := a.DB.WithContext(ctx).Order("plugin_id, code").Find(&rows).Error; err != nil {
		return nil, err
	}
	items := []M{}
	p := ContextPrincipal(ctx)
	for _, row := range rows {
		items = append(items, M{"code": row.Code, "title": row.Title, "pluginId": row.PluginID, "protected": row.Protected, "grantable": p != nil && p.HasPermission(row.Code) && (!row.Protected || p.HasRole("R_SUPER"))})
	}
	return items, nil
}
func (a *App) PluginCatalog(ctx context.Context) []M {
	items := []M{}
	for _, plugin := range a.Plugins.Plugins() {
		pages := []M{}
		for _, page := range a.Plugins.PluginResultPages(plugin.ID) {
			pages = append(pages, M{"pageKey": page.PageKey, "title": page.Title, "componentEntry": page.ComponentEntry, "configComponentEntry": page.ConfigComponentEntry, "scopeSchema": page.ScopeSchema, "filterSchema": page.FilterSchema, "actions": page.Actions, "actionPermissions": page.ActionPermissions, "permissionCode": page.PermissionCode})
		}
		panels := []M{}
		for _, panel := range a.Plugins.RunPanels(plugin.ID) {
			panels = append(panels, M{"panelKey": panel.PanelKey, "title": panel.Title, "nodeTypes": panel.NodeTypes, "componentEntry": panel.ComponentEntry})
		}
		items = append(items, M{"id": plugin.ID, "version": plugin.Version, "resultPages": pages, "runPanels": panels})
	}
	return items
}
func (a *App) GetWorkflowGrants(ctx context.Context, id int64) ([]WorkflowGrant, error) {
	if err := a.AuthorizeWorkflow(ctx, id, "workflows.share"); err != nil {
		return nil, err
	}
	var rows []struct {
		UserID      int64
		RoleID      int64
		Permissions string
	}
	if err := a.DB.WithContext(ctx).Raw(`SELECT user_id,0 AS role_id,permissions FROM workflow_user_grants WHERE workflow_id=? UNION ALL SELECT 0 AS user_id,role_id,permissions FROM workflow_role_grants WHERE workflow_id=?`, id, id).Scan(&rows).Error; err != nil {
		return nil, err
	}
	grants := []WorkflowGrant{}
	for _, r := range rows {
		g := WorkflowGrant{UserID: r.UserID, RoleID: r.RoleID}
		if json.Unmarshal([]byte(r.Permissions), &g.Permissions) != nil {
			return nil, errors.New("invalid workflow grant")
		}
		grants = append(grants, g)
	}
	return grants, nil
}
func (a *App) PageWorkflows(ctx context.Context, page CursorPage, status, keyword string, groupID *int64) (M, error) {
	if err := requireCapability(ctx, "workflows.read"); err != nil {
		return nil, err
	}
	query := workflowScopeQuery(a.DB.WithContext(ctx).Model(&db.Workflow{}), ContextPrincipal(ctx), "workflows.read", "workflows.id")
	if status != "" {
		if !validWorkflowStatus(status) {
			return nil, errors.New("invalid workflow status")
		}
		query = query.Where("workflows.status=?", status)
	}
	if keyword = strings.TrimSpace(keyword); keyword != "" {
		query = query.Where("workflows.name ILIKE ?", "%"+keyword+"%")
	}
	if groupID != nil {
		if *groupID == 0 {
			query = query.Where("workflows.group_id IS NULL")
		} else {
			query = query.Where("workflows.group_id=?", *groupID)
		}
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}
	after, err := page.AfterID()
	if err != nil {
		return nil, err
	}
	if after > 0 {
		query = query.Where("workflows.id<?", after)
	}
	var rows []struct {
		db.Workflow
		MaxConcurrentRuns int
		BacklogLimit      int
		LatestRunID       *int64
		LatestRunStatus   *string
	}
	err = query.Select("workflows.*, rt.max_concurrent_runs, rt.backlog_limit, recent.id AS latest_run_id, recent.status AS latest_run_status").Joins("JOIN workflow_runtimes rt ON rt.workflow_id=workflows.id").Joins("LEFT JOIN LATERAL (SELECT id,status FROM workflow_runs r WHERE r.workflow_id=workflows.id ORDER BY id DESC LIMIT 1) recent ON TRUE").Order("workflows.id DESC").Limit(page.Limit + 1).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	more := len(rows) > page.Limit
	if more {
		rows = rows[:page.Limit]
	}
	items := []M{}
	for _, r := range rows {
		raw, _ := json.Marshal(workflowView(r.Workflow))
		item := M{}
		_ = json.Unmarshal(raw, &item)
		item["maxConcurrentRuns"], item["backlogLimit"] = r.MaxConcurrentRuns, r.BacklogLimit
		item["latestRunId"], item["latestRunStatus"] = r.LatestRunID, r.LatestRunStatus
		items = append(items, item)
	}
	key := ""
	if len(rows) > 0 {
		key = fmt.Sprint(rows[len(rows)-1].ID)
	}
	return cursorResult(items, page, key, more, total), nil
}
func (a *App) Workbench(ctx context.Context) (M, error) {
	p := ContextPrincipal(ctx)
	if p == nil {
		return nil, ErrPermission
	}
	result := M{}
	if p.HasPermission("workflows.read") {
		page, _ := ParseCursorPage("", 10, "workbench")
		rows, err := a.PageWorkflows(ctx, page, "", "", nil)
		if err != nil {
			return nil, err
		}
		result["workflows"] = rows
	}
	if p.HasPermission("human_tasks.read") {
		tasks, err := a.ListWorkflowHumanTasks(ctx, "pending")
		if err != nil {
			return nil, err
		}
		result["tasks"] = tasks
	}
	if p.HasPermission("result_views.read") {
		views, err := a.ListResultViews(ctx, p)
		if err != nil {
			return nil, err
		}
		result["resultViews"] = views
	}
	return result, nil
}
func (a *App) ResolveSystemScope(ctx context.Context, pluginID, permission string) (sdk.SystemScope, error) {
	if err := requireCapability(ctx, permission); err != nil {
		return sdk.SystemScope{}, err
	}
	p := ContextPrincipal(ctx)
	scope := sdk.SystemScope{PluginID: pluginID, UserID: p.User.ID, RoleCodes: append([]string(nil), p.RoleCodes...), AllWorkflows: p.HasRole("R_SUPER")}
	if !scope.AllWorkflows {
		if err := workflowScopeQuery(a.DB.WithContext(ctx).Model(&db.Workflow{}), p, "workflows.read", "workflows.id").Order("id").Pluck("id", &scope.WorkflowIDs).Error; err != nil {
			return sdk.SystemScope{}, err
		}
	}
	scope.SessionValid = func(context.Context) error { _, err := a.RevalidateSession(p, permission); return err }
	return scope, nil
}
func (a *App) ResolveWorkflowScope(ctx context.Context, pluginID string, id int64, nodeID, permission string) (sdk.WorkflowScope, error) {
	if err := requireCapability(ctx, permission); err != nil {
		return sdk.WorkflowScope{}, err
	}
	if err := a.AuthorizeWorkflow(ctx, id, "workflows.read"); err != nil {
		return sdk.WorkflowScope{}, err
	}
	var w db.Workflow
	if err := a.DB.WithContext(ctx).First(&w, id).Error; err != nil {
		return sdk.WorkflowScope{}, err
	}
	revisionID := revisionPointerValue(w.DraftRevisionID)
	var r db.WorkflowRevision
	if err := a.DB.WithContext(ctx).First(&r, revisionID).Error; err != nil {
		return sdk.WorkflowScope{}, err
	}
	g, err := a.validateWorkflowGraph(json.RawMessage(r.GraphJSON))
	if err != nil || a.Plugins.NodePlugin(g.nodes[nodeID].NodeType) != pluginID {
		return sdk.WorkflowScope{}, ErrNotFound
	}
	return sdk.WorkflowScope{PluginID: pluginID, WorkflowID: fmt.Sprint(id), RevisionID: fmt.Sprint(revisionID), NodeInstanceID: nodeID, UserID: ContextPrincipal(ctx).User.ID}, nil
}
func (a *App) authorizeArtifact(ctx context.Context, digest string) error {
	query := a.DB.WithContext(ctx).Table("workflow_artifact_refs ref").Joins("JOIN workflow_run_nodes n ON n.id=ref.run_node_id").Joins("JOIN workflow_runs r ON r.id=n.run_id").Where("ref.artifact_sha256=?", digest)
	query = workflowScopeQuery(query, ContextPrincipal(ctx), "workflows.read", "r.workflow_id")
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}
func (a *App) syncRunPluginReferences(tx *gorm.DB, run db.WorkflowRun, g validatedWorkflowGraph) error {
	ids := map[string]bool{}
	for _, typ := range g.nodeTypes {
		if id := a.Plugins.NodePlugin(typ); id != "" {
			ids[id] = true
		}
	}
	sorted := []string{}
	for id := range ids {
		sorted = append(sorted, id)
	}
	sort.Strings(sorted)
	for _, id := range sorted {
		if err := addPluginReference(tx, id, "run", fmt.Sprint(run.ID)); err != nil {
			return err
		}
	}
	return nil
}
