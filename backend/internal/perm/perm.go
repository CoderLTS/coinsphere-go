// Package perm owns the permissions exposed by the V2 application baseline.
package perm

const (
	HomeView = "home.view"

	SchedulerWorkflowDefinitionsView   = "scheduler.workflow_definitions.view"
	SchedulerWorkflowDefinitionsCreate = "scheduler.workflow_definitions.create"
	SchedulerWorkflowDefinitionsUpdate = "scheduler.workflow_definitions.update"
	SchedulerWorkflowDefinitionsDelete = "scheduler.workflow_definitions.delete"
	SchedulerWorkflowDefinitionsRun    = "scheduler.workflow_definitions.run"
	SchedulerWorkflowRuntimeView       = "scheduler.workflow_runtime.view"
	SchedulerWorkflowRuntimeActivate   = "scheduler.workflow_runtime.activate"
	SchedulerWorkflowRuntimeUpdate     = "scheduler.workflow_runtime.update"
	SchedulerWorkflowExecutionsView    = "scheduler.workflow_executions.view"

	DataMarketView = "data.market.view"

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
	ResultViewsAccess            = "result.views.access"
	ResultViewsApprove           = "result.views.approve"
	ResultViewsReject            = "result.views.reject"
	ResultViewsRetry             = "result.views.retry"
	ResultViewsCancel            = "result.views.cancel"
	ResultViewsPause             = "result.views.pause"
	ResultViewsExport            = "result.views.export"
)

var MenuPermissionCodes = map[string]string{
	"Home": HomeView, "SchedulerCenter": "",
	"WorkflowDefinitions": SchedulerWorkflowDefinitionsView,
	"WorkflowExecutions":  SchedulerWorkflowExecutionsView,
	"NodeDefinitions":     SchedulerWorkflowDefinitionsView, "DataCenter": "",
	"MarketMetadata": DataMarketView, "MarketChart": DataMarketView,
	"Results": ResultViewsAccess,
	"System":  "", "User": SystemUsersView, "Role": SystemRolesView,
	"Menus": SystemMenusView, "UserCenter": "",
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
	"WorkflowExecutions": {{"view", SchedulerWorkflowExecutionsView, "查看执行记录"}},
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
	"Results": {
		{"approve", ResultViewsApprove, "批准"},
		{"reject", ResultViewsReject, "拒绝"},
		{"retry", ResultViewsRetry, "重试"},
		{"cancel", ResultViewsCancel, "取消"},
		{"pause", ResultViewsPause, "暂停"},
		{"export", ResultViewsExport, "导出"},
	},
}
