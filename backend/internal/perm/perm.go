// Package perm owns the permissions exposed by the V2 application baseline.
package perm

const (
	HomeView = "home.view"

	SchedulerWorkflowDefinitionsView   = "workflows.read"
	SchedulerWorkflowDefinitionsCreate = "workflows.create"
	SchedulerWorkflowDefinitionsUpdate = "workflows.update"
	SchedulerWorkflowDefinitionsDelete = "workflows.delete"
	SchedulerWorkflowDefinitionsRun    = "workflows.run"
	SchedulerWorkflowRuntimeView       = "workflows.read"
	SchedulerWorkflowRuntimeActivate   = "workflows.activate"
	SchedulerWorkflowRuntimeUpdate     = "workflows.update"

	SystemUsersView              = "system.users.view"
	SystemUsersCreate            = "system.users.create"
	SystemUsersUpdate            = "system.users.update"
	SystemUsersDelete            = "system.users.delete"
	SystemRolesView              = "system.roles.view"
	SystemRolesCreate            = "system.roles.create"
	SystemRolesUpdate            = "system.roles.update"
	SystemRolesDelete            = "system.roles.delete"
	SystemRolesAssignPermissions = "system.roles.assign_permissions"
	SystemMenusView              = "system.menus.view"
	SystemMenusCreate            = "system.menus.create"
	SystemMenusUpdate            = "system.menus.update"
	SystemMenusDelete            = "system.menus.delete"
	SystemPluginsView            = "system.plugins.view"
	SystemProxiesView            = "system.proxies.view"
	SystemProxiesCreate          = "system.proxies.create"
	SystemProxiesUpdate          = "system.proxies.update"
	SystemProxiesDelete          = "system.proxies.delete"
	SystemProxiesValidate        = "system.proxies.validate"
	SystemLogsView               = "system.logs.view"
	SystemLogsConfigure          = "system.logs.configure"
	ResultViewsAccess            = "result_views.read"
	ResultViewsApprove           = "result_views.approve"
	ResultViewsReject            = "result_views.reject"
	ResultViewsRetry             = "result_views.retry"
	ResultViewsCancel            = "result_views.cancel"
	ResultViewsPause             = "result_views.pause"
	ResultViewsExport            = "result_views.export"
)

var MenuPermissionCodes = map[string]string{
	"Home": HomeView, "SystemOverview": "system.observe", "AiModelConfig": "config.ai.manage", "SchedulerCenter": "",
	"WorkflowDefinitions": SchedulerWorkflowDefinitionsView,
	"System":              "", "User": SystemUsersView, "Role": SystemRolesView,
	"Menus": SystemMenusView, "Plugins": SystemPluginsView, "OutboundProxies": SystemProxiesView, "UserCenter": "",
}

type ButtonSpec struct {
	Action string
	Code   string
	Title  string
}

var ButtonSpecs = map[string][]ButtonSpec{
	"WorkflowDefinitions": {
		{"create", SchedulerWorkflowDefinitionsCreate, "新建工作流定义"},
		{"update", SchedulerWorkflowDefinitionsUpdate, "编辑工作流定义"},
		{"delete", SchedulerWorkflowDefinitionsDelete, "删除版本"},
		{"run", SchedulerWorkflowDefinitionsRun, "手动运行"},
		{"runtime", SchedulerWorkflowRuntimeView, "查看运行态"},
		{"activate", SchedulerWorkflowRuntimeActivate, "激活版本"},
		{"update_runtime", SchedulerWorkflowRuntimeUpdate, "更新入口状态"},
	},
	"User": {
		{"create", SystemUsersCreate, "新增"},
		{"update", SystemUsersUpdate, "编辑"},
		{"delete", SystemUsersDelete, "删除"},
	},
	"Role": {
		{"create", SystemRolesCreate, "新增"},
		{"update", SystemRolesUpdate, "编辑"},
		{"delete", SystemRolesDelete, "删除"},
		{"assign_permissions", SystemRolesAssignPermissions, "分配权限"},
	},
	"Menus": {
		{"create", SystemMenusCreate, "新增"},
		{"update", SystemMenusUpdate, "编辑"},
		{"delete", SystemMenusDelete, "删除"},
	},
	"OutboundProxies": {
		{"create", SystemProxiesCreate, "新增"},
		{"update", SystemProxiesUpdate, "编辑"},
		{"delete", SystemProxiesDelete, "删除"},
		{"validate", SystemProxiesValidate, "检测"},
	},
	"Home": {
		{"logs", SystemLogsView, "查看系统日志"},
		{"configure", SystemLogsConfigure, "配置"},
	},
}
