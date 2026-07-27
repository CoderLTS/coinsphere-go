// Package perm 全局权限码常量与内置菜单/按钮权限映射。
package perm

// 权限码常量。
// 一组常量用 const ( ... ) 打包声明。这里是显式的字符串常量(不是用 iota 自增的整数枚举),
// 值本身就是权限码字符串;前后端都靠比对这个字符串来判断"能不能进某菜单 / 点某按钮"。
const (
	HomeView = "home.view"

	SchedulerWorkflowDefinitionsView   = "scheduler.workflow_definitions.view"
	SchedulerWorkflowDefinitionsCreate = "scheduler.workflow_definitions.create"
	SchedulerWorkflowDefinitionsUpdate = "scheduler.workflow_definitions.update"
	SchedulerWorkflowDefinitionsDelete = "scheduler.workflow_definitions.delete"
	SchedulerWorkflowDefinitionsRun    = "scheduler.workflow_definitions.run"

	SchedulerWorkflowRuntimeView     = "scheduler.workflow_runtime.view"
	SchedulerWorkflowRuntimeActivate = "scheduler.workflow_runtime.activate"
	SchedulerWorkflowRuntimeUpdate   = "scheduler.workflow_runtime.update"

	SchedulerWorkflowExecutionsView = "scheduler.workflow_executions.view"
	SchedulerTaskDefinitionsView    = "scheduler.task_definitions.view"
	SchedulerTaskDefinitionsUpdate  = "scheduler.task_definitions.update"

	DataNewsView           = "data.news.view"
	DataNewsCreate         = "data.news.create"
	DataNewsUpdate         = "data.news.update"
	DataNewsDelete         = "data.news.delete"
	DataNewsAnalyze        = "data.news.analyze"
	DataPushDeliveriesView = "data.push_deliveries.view"

	ConfigOverviewView               = "config.overview.view"
	ConfigAiModelsView               = "config.ai_models.view"
	ConfigAiModelsCreate             = "config.ai_models.create"
	ConfigAiModelsUpdate             = "config.ai_models.update"
	ConfigAiModelsDelete             = "config.ai_models.delete"
	ConfigAiModelsValidate           = "config.ai_models.validate"
	ConfigAiModelsBindAgents         = "config.ai_models.bind_agents"
	ConfigAssistantAgentsView        = "config.assistant_agents.view"
	ConfigAssistantAgentsCreate      = "config.assistant_agents.create"
	ConfigAssistantAgentsUpdate      = "config.assistant_agents.update"
	ConfigAssistantAgentsDelete      = "config.assistant_agents.delete"
	ConfigNotificationChannelsView   = "config.notification_channels.view"
	ConfigNotificationChannelsCreate = "config.notification_channels.create"
	ConfigNotificationChannelsUpdate = "config.notification_channels.update"
	ConfigNotificationChannelsDelete = "config.notification_channels.delete"
	ConfigNotificationChannelsTest   = "config.notification_channels.test"

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
)

// MenuPermissionCodes 菜单名 -> 权限码(空串表示无权限码)。
// map[string]string 是"字符串→字符串"的字典(见 GO入门笔记『map』):键是前端菜单名,值是进入它所需的权限码。
var MenuPermissionCodes = map[string]string{
	"Home":                 HomeView,
	"SchedulerCenter":      "",
	"WorkflowDefinitions":  SchedulerWorkflowDefinitionsView,
	"WorkflowExecutions":   SchedulerWorkflowExecutionsView,
	"TaskDefinitions":      SchedulerTaskDefinitionsView,
	"DataCenter":           "",
	"NewsData":             DataNewsView,
	"PushData":             DataPushDeliveriesView,
	"ConfigCenter":         "",
	"ConfigOverview":       ConfigOverviewView,
	"AiModelConfig":        ConfigAiModelsView,
	"AssistantAgentConfig": ConfigAssistantAgentsView,
	"NotifyChannels":       ConfigNotificationChannelsView,
	"System":               "",
	"User":                 SystemUsersView,
	"Role":                 SystemRolesView,
	"Menus":                SystemMenusView,
	"UserCenter":           "",
}

// ButtonSpec 按钮定义:action -> 权限码。
type ButtonSpec struct {
	Action string
	Code   string
	Title  string
}

// ButtonSpecs 菜单名 -> 有序按钮列表(顺序决定 sort)。
// 值类型是 []ButtonSpec(ButtonSpec 切片):一个菜单下有多个按钮;切片有序,声明顺序就是显示/排序顺序。
var ButtonSpecs = map[string][]ButtonSpec{
	"WorkflowDefinitions": {
		// 每个 {..., ..., ...} 是一个 ButtonSpec 字面量,按字段声明顺序对应 Action / Code / Title 三个字段。
		{"create", SchedulerWorkflowDefinitionsCreate, "新建工作流定义"},
		{"update", SchedulerWorkflowDefinitionsUpdate, "编辑工作流定义"},
		{"delete", SchedulerWorkflowDefinitionsDelete, "删除版本"},
		{"run", SchedulerWorkflowDefinitionsRun, "手动运行"},
		{"runtime", SchedulerWorkflowRuntimeView, "查看运行态"},
		{"activate", SchedulerWorkflowRuntimeActivate, "激活版本"},
		{"update_runtime", SchedulerWorkflowRuntimeUpdate, "更新入口状态"},
	},
	"WorkflowExecutions": {
		{"view", SchedulerWorkflowExecutionsView, "查看执行记录"},
	},
	"TaskDefinitions": {
		{"update", SchedulerTaskDefinitionsUpdate, "编辑任务定义"},
	},
	"NewsData": {
		{"create", DataNewsCreate, "新增"},
		{"update", DataNewsUpdate, "编辑"},
		{"delete", DataNewsDelete, "删除"},
		{"analyze", DataNewsAnalyze, "AI 分析"},
	},
	"AiModelConfig": {
		{"create", ConfigAiModelsCreate, "新增"},
		{"update", ConfigAiModelsUpdate, "编辑"},
		{"delete", ConfigAiModelsDelete, "删除"},
		{"validate", ConfigAiModelsValidate, "校验"},
		{"bind_agents", ConfigAiModelsBindAgents, "绑定智能体"},
	},
	"AssistantAgentConfig": {
		{"create", ConfigAssistantAgentsCreate, "新增"},
		{"update", ConfigAssistantAgentsUpdate, "编辑"},
		{"delete", ConfigAssistantAgentsDelete, "删除"},
	},
	"NotifyChannels": {
		{"create", ConfigNotificationChannelsCreate, "新增"},
		{"update", ConfigNotificationChannelsUpdate, "编辑"},
		{"delete", ConfigNotificationChannelsDelete, "删除"},
		{"test", ConfigNotificationChannelsTest, "测试"},
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
}

// AssistantAgentRequiredPermission 智能体编码 -> 使用所需权限。
var AssistantAgentRequiredPermission = map[string]string{
	"news_analysis": DataNewsAnalyze,
}
