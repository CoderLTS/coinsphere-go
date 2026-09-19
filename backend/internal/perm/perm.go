// Package perm owns the permissions exposed by the Core4 application baseline.
package perm

const (
	HomeView = "home.view"

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
	ResultViewsAccess            = "result.views.access"
	ResultViewsApprove           = "result.views.approve"
	ResultViewsReject            = "result.views.reject"
	ResultViewsRetry             = "result.views.retry"
	ResultViewsCancel            = "result.views.cancel"
	ResultViewsPause             = "result.views.pause"
	ResultViewsExport            = "result.views.export"
)

var MenuPermissionCodes = map[string]string{
	"Home": HomeView, "Results": ResultViewsAccess, "SchedulerCenter": "",
	"WorkflowDefinitions": "",
	"System":              "", "User": SystemUsersView, "Role": SystemRolesView,
	"Menus": SystemMenusView, "Plugins": SystemPluginsView, "OutboundProxies": SystemProxiesView, "UserCenter": "",
}

type ButtonSpec struct {
	Action string
	Code   string
	Title  string
}

var ButtonSpecs = map[string][]ButtonSpec{
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
