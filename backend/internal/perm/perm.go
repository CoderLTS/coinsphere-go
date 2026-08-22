// Package perm 全局权限码常量与内置菜单/按钮权限映射。
package perm

// 权限码常量。
// 一组常量用 const ( ... ) 打包声明。这里是显式的字符串常量(不是用 iota 自增的整数枚举),
// 值本身就是权限码字符串;前后端都靠比对这个字符串来判断"能不能进某菜单 / 点某按钮"。
const (
	HomeView            = "home.view"
	TradingOverviewView = "trading.overview.view"

	SchedulerWorkflowDefinitionsView   = "scheduler.workflow_definitions.view"
	SchedulerWorkflowDefinitionsCreate = "scheduler.workflow_definitions.create"
	SchedulerWorkflowDefinitionsUpdate = "scheduler.workflow_definitions.update"
	SchedulerWorkflowDefinitionsDelete = "scheduler.workflow_definitions.delete"
	SchedulerWorkflowDefinitionsRun    = "scheduler.workflow_definitions.run"

	SchedulerWorkflowRuntimeView     = "scheduler.workflow_runtime.view"
	SchedulerWorkflowRuntimeActivate = "scheduler.workflow_runtime.activate"
	SchedulerWorkflowRuntimeUpdate   = "scheduler.workflow_runtime.update"

	SchedulerWorkflowExecutionsView = "scheduler.workflow_executions.view"

	DataNewsView           = "data.news.view"
	DataNewsCreate         = "data.news.create"
	DataNewsUpdate         = "data.news.update"
	DataNewsDelete         = "data.news.delete"
	DataNewsAnalyze        = "data.news.analyze"
	DataPushDeliveriesView = "data.push_deliveries.view"
	DataMarketView         = "data.market.view"
	DataMarketManage       = "data.market.manage"

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
	"Home":              HomeView,
	"WorkflowWorkbench": SchedulerWorkflowDefinitionsView,
	"UserCenter":        "",
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
	"WorkflowWorkbench": {
		// 每个 {..., ..., ...} 是一个 ButtonSpec 字面量,按字段声明顺序对应 Action / Code / Title 三个字段。
		{"create", SchedulerWorkflowDefinitionsCreate, "新建工作流定义"},
		{"update", SchedulerWorkflowDefinitionsUpdate, "编辑工作流定义"},
		{"delete", SchedulerWorkflowDefinitionsDelete, "删除版本"},
		{"run", SchedulerWorkflowDefinitionsRun, "手动运行"},
		{"runtime", SchedulerWorkflowRuntimeView, "查看运行态"},
		{"activate", SchedulerWorkflowRuntimeActivate, "激活版本"},
		{"update_runtime", SchedulerWorkflowRuntimeUpdate, "更新入口状态"},
		{"view", SchedulerWorkflowExecutionsView, "查看执行记录"},
		{"create_channel", ConfigNotificationChannelsCreate, "新增通知渠道"},
		{"update_channel", ConfigNotificationChannelsUpdate, "编辑通知渠道"},
		{"delete_channel", ConfigNotificationChannelsDelete, "删除通知渠道"},
		{"test_channel", ConfigNotificationChannelsTest, "测试通知渠道"},
		{"trading_view", TradingOverviewView, "使用交易管理节点"},
		{"news_view", DataNewsView, "使用新闻管理节点"},
		{"news_create", DataNewsCreate, "创建新闻"},
		{"news_update", DataNewsUpdate, "更新新闻"},
		{"news_delete", DataNewsDelete, "删除新闻"},
		{"news_analyze", DataNewsAnalyze, "分析新闻"},
		{"push_view", DataPushDeliveriesView, "查看推送记录"},
		{"market_view", DataMarketView, "查看市场数据"},
		{"market_manage", DataMarketManage, "管理市场同步"},
		{"config_view", ConfigOverviewView, "查看配置"},
		{"ai_model_view", ConfigAiModelsView, "使用 AI 模型节点"},
		{"ai_model_create", ConfigAiModelsCreate, "创建 AI 模型"},
		{"ai_model_update", ConfigAiModelsUpdate, "更新 AI 模型"},
		{"ai_model_delete", ConfigAiModelsDelete, "删除 AI 模型"},
		{"ai_model_validate", ConfigAiModelsValidate, "校验 AI 模型"},
		{"ai_model_bind_agents", ConfigAiModelsBindAgents, "绑定智能体"},
		{"assistant_view", ConfigAssistantAgentsView, "使用智能体节点"},
		{"assistant_create", ConfigAssistantAgentsCreate, "创建智能体"},
		{"assistant_update", ConfigAssistantAgentsUpdate, "更新智能体"},
		{"assistant_delete", ConfigAssistantAgentsDelete, "删除智能体"},
		{"channel_view", ConfigNotificationChannelsView, "使用通知渠道节点"},
		{"user_view", SystemUsersView, "使用用户管理节点"},
		{"user_create", SystemUsersCreate, "创建用户"},
		{"user_update", SystemUsersUpdate, "更新用户"},
		{"user_delete", SystemUsersDelete, "删除用户"},
		{"role_view", SystemRolesView, "使用角色管理节点"},
		{"role_create", SystemRolesCreate, "创建角色"},
		{"role_update", SystemRolesUpdate, "更新角色"},
		{"role_delete", SystemRolesDelete, "删除角色"},
		{"role_permissions", SystemRolesAssignPermissions, "分配角色权限"},
		{"menu_view", SystemMenusView, "查看菜单资源"},
		{"menu_create", SystemMenusCreate, "创建菜单资源"},
		{"menu_update", SystemMenusUpdate, "更新菜单资源"},
		{"menu_delete", SystemMenusDelete, "删除菜单资源"},
	},
}

// AssistantAgentRequiredPermission 智能体编码 -> 使用所需权限。
var AssistantAgentRequiredPermission = map[string]string{
	"news_analysis": DataNewsAnalyze,
}
