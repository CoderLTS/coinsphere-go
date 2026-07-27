package db

import (
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"

	"coinsphere/backend/internal/perm"
	"coinsphere/backend/internal/security"
)

type roleItem struct{ Code, Title, Description string }

var roleItems = []roleItem{
	{"R_SUPER", "超级管理员", "拥有系统全部管理权限"},
	{"R_USER", "普通用户", "默认登录用户,可维护自己的模型与通知配置"},
	{"R_GUEST", "游客", "仅允许匿名访问首页"},
}

type menuItem struct {
	Name, Title, Path, Component, Icon, Parent string
	KeepAlive, FixedTab, IsHidden              bool
}

var menuItems = []menuItem{
	{"Home", "首页", "/home", "/home/index", "ri:home-5-line", "", true, true, false},
	{"SchedulerCenter", "工作流调度", "/scheduler", "/index/index", "ri:time-line", "", false, false, false},
	{"WorkflowDefinitions", "工作流定义", "definition", "/scheduler/workflow", "ri:node-tree", "SchedulerCenter", true, false, false},
	{"WorkflowExecutions", "执行记录", "execution", "/scheduler/execution", "ri:history-line", "SchedulerCenter", true, false, false},
	{"DataCenter", "数据管理", "/data", "/index/index", "ri:database-2-line", "", false, false, false},
	{"NewsData", "新闻数据", "news", "/data/news", "ri:newspaper-line", "DataCenter", true, false, false},
	{"PushData", "推送数据", "push", "/data/push", "ri:send-plane-line", "DataCenter", true, false, false},
	{"ConfigCenter", "配置管理", "/config", "/index/index", "ri:function-line", "", false, false, false},
	{"ConfigOverview", "配置概览", "overview", "/config/overview", "ri:apps-2-line", "ConfigCenter", true, false, false},
	{"AiModelConfig", "模型配置", "ai-model", "/config/ai-model", "ri:cpu-line", "ConfigCenter", true, false, false},
	{"AssistantAgentConfig", "智能体配置", "assistant-agent", "/config/assistant-agent", "ri:robot-2-line", "ConfigCenter", true, false, false},
	{"NotifyChannels", "通知渠道", "notify-channel", "/config/notify-channel", "ri:notification-3-line", "ConfigCenter", true, false, false},
	{"System", "系统管理", "/system", "/index/index", "ri:settings-3-line", "", false, false, false},
	{"User", "用户管理", "user", "/system/user", "ri:user-3-line", "System", true, false, false},
	{"Role", "角色管理", "role", "/system/role", "ri:team-line", "System", true, false, false},
	{"Menus", "菜单管理", "menu", "/system/menu", "ri:menu-line", "System", true, false, false},
	{"UserCenter", "个人中心", "/profile", "/system/user-center", "", "", true, false, true},
}

var menuI18n = map[string][2]string{
	"Home":                 {"首页", "Home"},
	"SchedulerCenter":      {"工作流调度", "Workflow Scheduler"},
	"WorkflowDefinitions":  {"工作流定义", "Workflow Definitions"},
	"WorkflowExecutions":   {"执行记录", "Execution Records"},
	"TaskDefinitions":      {"任务定义", "Task Definitions"},
	"DataCenter":           {"数据管理", "Data Management"},
	"NewsData":             {"新闻数据", "News Data"},
	"PushData":             {"推送数据", "Push Deliveries"},
	"ConfigCenter":         {"配置管理", "Configuration"},
	"ConfigOverview":       {"配置概览", "Config Overview"},
	"AiModelConfig":        {"模型配置", "Model Config"},
	"AssistantAgentConfig": {"智能体配置", "Assistant Agents"},
	"NotifyChannels":       {"通知渠道", "Notification Channels"},
	"System":               {"系统管理", "System Management"},
	"User":                 {"用户管理", "User Management"},
	"Role":                 {"角色管理", "Role Management"},
	"Menus":                {"菜单管理", "Menu Management"},
	"UserCenter":           {"个人中心", "Profile"},
}

type agentItem struct {
	Code, Name, Avatar, Description, Prompt, Welcome string
	Starters                                         []string
	DataSourceType                                   string
	Sort                                             int
}

var agentItems = []agentItem{
	{
		"system_general", "系统通用智能体", "ri:message-3-line",
		"用于回答平台使用、配置说明与系统操作问题。",
		"你是 coinsphere 的系统通用智能体,请围绕平台真实能力回答问题,内容要专业、简洁、可执行,不要虚构系统能力。",
		"我可以协助你处理平台使用、配置说明和系统问题。",
		[]string{"帮我解释一下当前工作流体系", "告诉我如何配置通知渠道", "帮我梳理首页上的关键指标"},
		"system_context", 10,
	},
	{
		"news_analysis", "新闻分析智能体", "ri:article-line",
		"面向加密新闻快讯的结构化分析与追问。",
		"你是 coinsphere 的新闻分析智能体,请结合给定新闻上下文给出结构化、清晰、可执行的分析,不要脱离新闻内容虚构事实。",
		"我会结合当前新闻内容给出结构化分析,并支持后续追问。",
		[]string{"先总结这条新闻的核心影响", "从交易角度给出利多利空判断", "提炼后续需要关注的风险点"},
		"news_context", 20,
	},
}

// Seed 写入内置角色、用户、菜单、i18n、智能体、渠道与内置工作流。幂等。
func Seed(gdb *gorm.DB, hasher *security.PasswordHasher) error {
	return gdb.Transaction(func(tx *gorm.DB) error {
		roles, err := seedRoles(tx)
		if err != nil {
			return err
		}
		user, err := seedSuperUser(tx, hasher)
		if err != nil {
			return err
		}
		if err := seedAgents(tx); err != nil {
			return err
		}
		menus, buttons, err := seedMenusAndButtons(tx)
		if err != nil {
			return err
		}
		if err := seedRoleBindings(tx, roles, menus, buttons, user); err != nil {
			return err
		}
		if err := seedI18n(tx, menus, buttons); err != nil {
			return err
		}
		if err := seedBuiltinChannel(tx); err != nil {
			return err
		}
		if err := seedWorkflows(tx, roles["R_SUPER"].ID); err != nil {
			return err
		}
		return tx.Where("expires_at < ?", time.Now()).Delete(&RefreshTokenRecord{}).Error
	})
}

func seedRoles(tx *gorm.DB) (map[string]*SystemRole, error) {
	now := time.Now()
	result := map[string]*SystemRole{}
	for _, item := range roleItems {
		var role SystemRole
		err := tx.Where("code = ?", item.Code).First(&role).Error
		if err == gorm.ErrRecordNotFound {
			role = SystemRole{
				Code: item.Code, DisplayName: item.Title, Description: item.Description,
				IsEnabled: true, IsSystem: true, CreatedAt: now, UpdatedAt: now,
			}
			if err := tx.Create(&role).Error; err != nil {
				return nil, err
			}
		} else if err != nil {
			return nil, err
		} else {
			updates := map[string]any{
				"display_name": item.Title, "description": item.Description,
				"is_enabled": true, "is_system": true, "updated_at": now,
			}
			if err := tx.Model(&role).Updates(updates).Error; err != nil {
				return nil, err
			}
		}
		result[item.Code] = &role
	}
	return result, nil
}

func seedSuperUser(tx *gorm.DB, hasher *security.PasswordHasher) (*SystemUser, error) {
	now := time.Now()
	tags, _ := json.Marshal([]string{"coinsphere", "system", "super-admin"})
	var user SystemUser
	err := tx.Where("username = ?", "coinsphere").First(&user).Error
	if err == gorm.ErrRecordNotFound {
		user = SystemUser{
			Username: "coinsphere", PasswordHash: hasher.HashPassword("coinsphere"),
			Nickname: "超级管理员", FullName: "coinsphere", Gender: "male",
			Phone: "13800000000", Email: "admin@coinsphere.local",
			IsActive: true, JobTitle: "System Owner", Location: "Shanghai", Company: "coinsphere",
			Bio: "默认系统超级管理员,拥有全部后台权限。", TagsJSON: string(tags),
			CreatedBy: "system", UpdatedBy: "system", CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Create(&user).Error; err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	return &user, nil
}

func seedAgents(tx *gorm.DB) error {
	now := time.Now()
	for _, item := range agentItems {
		starters, _ := json.Marshal(item.Starters)
		var agent AssistantAgent
		err := tx.Where("code = ?", item.Code).First(&agent).Error
		if err == gorm.ErrRecordNotFound {
			agent = AssistantAgent{
				Code: item.Code, DisplayName: item.Name, Avatar: item.Avatar,
				Description: item.Description, SystemPrompt: item.Prompt, WelcomeMessage: item.Welcome,
				StarterPromptsJSON: string(starters), DataSourceType: item.DataSourceType,
				IsEnabled: true, Sort: item.Sort, CreatedAt: now, UpdatedAt: now,
			}
			if err := tx.Create(&agent).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	return nil
}

func seedMenusAndButtons(tx *gorm.DB) (map[string]*SystemMenu, map[string]*SystemMenuButton, error) {
	now := time.Now()
	menuMap := map[string]*SystemMenu{}

	upsertMenu := func(item menuItem, sort int) error {
		var parentID *int64
		if item.Parent != "" {
			parent, ok := menuMap[item.Parent]
			if !ok {
				return fmt.Errorf("seed menu %s: parent %s missing", item.Name, item.Parent)
			}
			parentID = &parent.ID
		}
		var permCode *string
		if code := perm.MenuPermissionCodes[item.Name]; code != "" {
			permCode = &code
		}
		fields := map[string]any{
			"parent_id": parentID, "path": item.Path, "permission_code": permCode,
			"component": item.Component, "title": item.Title, "icon": item.Icon,
			"menu_type": "menu", "external_url": "", "active_menu_path": "",
			"sort": sort, "keep_alive": item.KeepAlive, "is_hidden": item.IsHidden,
			"is_hide_tab": false, "is_full_screen": false, "is_active": true,
			"use_iframe": false, "fixed_tab": item.FixedTab, "badge_label": "",
			"updated_at": now,
		}
		var menu SystemMenu
		err := tx.Where("name = ?", item.Name).First(&menu).Error
		if err == gorm.ErrRecordNotFound {
			menu = SystemMenu{Name: item.Name, CreatedAt: now}
			if err := tx.Create(&menu).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if err := tx.Model(&SystemMenu{}).Where("id = ?", menu.ID).Updates(fields).Error; err != nil {
			return err
		}
		if err := tx.First(&menu, menu.ID).Error; err != nil {
			return err
		}
		menuMap[item.Name] = &menu
		return nil
	}

	for index, item := range menuItems {
		if err := upsertMenu(item, (index+1)*10); err != nil {
			return nil, nil, err
		}
	}
	// TaskDefinitions 挂在调度中心下,sort 固定 35(位于定义与执行记录之间)。
	if err := upsertMenu(menuItem{
		Name: "TaskDefinitions", Title: "任务定义", Path: "task-definition",
		Component: "/scheduler/task-definition", Icon: "ri:stack-line",
		Parent: "SchedulerCenter", KeepAlive: true,
	}, 35); err != nil {
		return nil, nil, err
	}

	buttonMap := map[string]*SystemMenuButton{}
	for menuName, specs := range perm.ButtonSpecs {
		menu, ok := menuMap[menuName]
		if !ok {
			continue
		}
		for index, spec := range specs {
			var button SystemMenuButton
			err := tx.Where("permission_code = ?", spec.Code).First(&button).Error
			if err == gorm.ErrRecordNotFound {
				button = SystemMenuButton{
					MenuID: menu.ID, Title: spec.Title, PermissionCode: spec.Code,
					Sort: (index + 1) * 10, CreatedAt: now,
				}
				if err := tx.Create(&button).Error; err != nil {
					return nil, nil, err
				}
			} else if err != nil {
				return nil, nil, err
			} else {
				updates := map[string]any{"menu_id": menu.ID, "title": spec.Title, "sort": (index + 1) * 10}
				if err := tx.Model(&button).Updates(updates).Error; err != nil {
					return nil, nil, err
				}
			}
			buttonMap[spec.Code] = &button
		}
	}
	return menuMap, buttonMap, nil
}

func seedRoleBindings(
	tx *gorm.DB,
	roles map[string]*SystemRole,
	menus map[string]*SystemMenu,
	buttons map[string]*SystemMenuButton,
	user *SystemUser,
) error {
	var existing SystemUserRole
	err := tx.Where("user_id = ? AND role_id = ?", user.ID, roles["R_SUPER"].ID).First(&existing).Error
	if err == gorm.ErrRecordNotFound {
		if err := tx.Create(&SystemUserRole{UserID: user.ID, RoleID: roles["R_SUPER"].ID, CreatedAt: time.Now()}).Error; err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	allMenuNames := make([]string, 0, len(menuItems)+1)
	for _, item := range menuItems {
		allMenuNames = append(allMenuNames, item.Name)
	}
	allMenuNames = append(allMenuNames, "TaskDefinitions")

	roleMenus := map[string][]string{
		"R_SUPER": allMenuNames,
		"R_USER":  {"Home", "ConfigCenter", "AiModelConfig", "NotifyChannels", "UserCenter"},
		"R_GUEST": {"Home"},
	}
	superButtons := make([]string, 0)
	for _, specs := range perm.ButtonSpecs {
		for _, spec := range specs {
			superButtons = append(superButtons, spec.Code)
		}
	}
	roleButtons := map[string][]string{
		"R_SUPER": superButtons,
		"R_USER": {
			perm.ConfigAiModelsCreate, perm.ConfigAiModelsUpdate, perm.ConfigAiModelsDelete,
			perm.ConfigAiModelsValidate, perm.ConfigAiModelsBindAgents,
			perm.ConfigNotificationChannelsCreate, perm.ConfigNotificationChannelsUpdate,
			perm.ConfigNotificationChannelsDelete, perm.ConfigNotificationChannelsTest,
		},
		"R_GUEST": {},
	}

	for roleCode, menuNames := range roleMenus {
		role := roles[roleCode]
		if err := tx.Where("role_id = ?", role.ID).Delete(&SystemRoleMenu{}).Error; err != nil {
			return err
		}
		for _, menuName := range menuNames {
			menu, ok := menus[menuName]
			if !ok {
				continue
			}
			if err := tx.Create(&SystemRoleMenu{RoleID: role.ID, MenuID: menu.ID, CreatedAt: time.Now()}).Error; err != nil {
				return err
			}
		}
	}
	for roleCode, codes := range roleButtons {
		role := roles[roleCode]
		if err := tx.Where("role_id = ?", role.ID).Delete(&SystemRoleButton{}).Error; err != nil {
			return err
		}
		for _, code := range codes {
			button, ok := buttons[code]
			if !ok {
				continue
			}
			if err := tx.Create(&SystemRoleButton{RoleID: role.ID, ButtonID: button.ID, CreatedAt: time.Now()}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func seedI18n(tx *gorm.DB, menus map[string]*SystemMenu, buttons map[string]*SystemMenuButton) error {
	for menuName, texts := range menuI18n {
		menu, ok := menus[menuName]
		if !ok {
			continue
		}
		key := fmt.Sprintf("menus.custom.menu_%d", menu.ID)
		if err := upsertI18nPair(tx, "menu", menu.ID, key, texts[0], texts[1]); err != nil {
			return err
		}
	}
	for _, specs := range perm.ButtonSpecs {
		for _, spec := range specs {
			button, ok := buttons[spec.Code]
			if !ok {
				continue
			}
			key := fmt.Sprintf("permissions.custom.button_%d", button.ID)
			zh, en := spec.Title, spec.Title
			if spec.Code == perm.SchedulerTaskDefinitionsUpdate {
				en = "Edit Task Definition"
			}
			if err := upsertI18nPair(tx, "button", button.ID, key, zh, en); err != nil {
				return err
			}
		}
	}
	return nil
}

func upsertI18nPair(tx *gorm.DB, bizType string, bizID int64, key, zh, en string) error {
	for locale, text := range map[string]string{"zh": zh, "en": en} {
		var row SystemI18nText
		err := tx.Where("biz_type = ? AND biz_id = ? AND locale = ?", bizType, bizID, locale).First(&row).Error
		if err == gorm.ErrRecordNotFound {
			row = SystemI18nText{BizType: bizType, BizID: bizID, I18nKey: key, Locale: locale, Text: text, UpdatedAt: time.Now()}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else {
			updates := map[string]any{"i18n_key": key, "text": text, "updated_at": time.Now()}
			if err := tx.Model(&row).Updates(updates).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func seedBuiltinChannel(tx *gorm.DB) error {
	now := time.Now()
	var channel SystemNotifyChannel
	err := tx.Where("channel_type = ? AND is_builtin = ?", "in_app", true).First(&channel).Error
	if err == gorm.ErrRecordNotFound {
		channel = SystemNotifyChannel{
			ChannelType: "in_app", DisplayName: "站内通知", IsEnabled: true,
			IsBuiltin: true, IsSystem: true, SettingsJSON: "{}",
			Remark: "系统内置站内通知渠道", CreatedAt: now, UpdatedAt: now,
		}
		return tx.Create(&channel).Error
	}
	return err
}

func seedWorkflows(tx *gorm.DB, superRoleID int64) error {
	now := time.Now()
	definitions := []struct {
		Code, DisplayName, Description string
		Graph                          map[string]any
	}{
		{
			"blockbeats_news_sync", "Blockbeats 新闻同步",
			"定时抓取 Blockbeats 最新新闻,在同一工作流内完成事件发布与站内通知。",
			buildBlockbeatsNewsSyncGraph(superRoleID),
		},
		{
			"alert_workflow_failed", "工作流失败告警",
			"响应 workflow.execution.failed 事件并发送失败告警通知。",
			buildAlertWorkflowFailedGraph(superRoleID),
		},
	}
	creator := int64(1)
	for _, item := range definitions {
		graphJSON, _ := json.Marshal(item.Graph)
		var definition WorkflowDefinition
		err := tx.Where("code = ? AND version = ?", item.Code, 1).First(&definition).Error
		if err == gorm.ErrRecordNotFound {
			definition = WorkflowDefinition{
				Code: item.Code, Version: 1, DisplayName: item.DisplayName,
				Description: item.Description, GraphJSON: string(graphJSON),
				IsBuiltin: true, CreatedBy: &creator, CreatedAt: now,
			}
			if err := tx.Create(&definition).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}

		var state WorkflowRuntimeState
		err = tx.Where("workflow_code = ?", item.Code).First(&state).Error
		if err == gorm.ErrRecordNotFound {
			state = WorkflowRuntimeState{
				WorkflowCode: item.Code, ActiveWorkflowDefinitionID: &definition.ID,
				ActivatedAt: &now, ActivatedBy: &creator, UpdatedAt: now,
			}
			if err := tx.Create(&state).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	return nil
}

func buildBlockbeatsNewsSyncGraph(superRoleID int64) map[string]any {
	return map[string]any{
		"nodes": []map[string]any{
			{
				"id": "start_schedule", "type": "start.schedule", "label": "定时开始",
				"config": map[string]any{
					"entryKey": "blockbeats.schedule.default", "displayName": "Blockbeats 定时入口",
					"inputBindings": map[string]any{"pageSize": 20, "page": 1},
					"scheduleType":  "cron", "cronExpression": "0 * * * * *",
				},
				"position": map[string]any{"x": 140, "y": 280},
			},
			{
				"id": "run_news_fetch", "type": "task.run", "label": "抓取新闻",
				"config":   map[string]any{"taskDefinitionCode": "blockbeats_news_fetch"},
				"position": map[string]any{"x": 480, "y": 280},
			},
			{
				"id": "publish_news_synced", "type": "event.publish", "label": "发布同步事件",
				"config": map[string]any{
					"eventType": "news.items.synced", "aggregateType": "workflow_execution",
					"payloadPath": "taskResult",
				},
				"position": map[string]any{"x": 820, "y": 280},
			},
			{
				"id": "branch_has_inserted_items", "type": "condition.branch", "label": "存在新增新闻",
				"config":   map[string]any{"path": "taskResult.insertedCount", "operator": "gt", "value": "0"},
				"position": map[string]any{"x": 1160, "y": 280},
			},
			{
				"id": "foreach_inserted_items", "type": "foreach", "label": "遍历新增新闻",
				"config": map[string]any{
					"itemsPath": "taskResult.insertedItems", "itemKey": "newsItem", "indexKey": "newsIndex",
				},
				"position": map[string]any{"x": 1500, "y": 200},
			},
			{
				"id": "notify_news_inserted", "type": "notify", "label": "发送通知",
				"config": map[string]any{
					"targets":         []map[string]any{{"targetType": "role", "targetId": superRoleID}},
					"channelTypes":    []string{"in_app"},
					"titleTemplate":   "📰 Blockbeats 新资讯:{{ newsItem.title }}",
					"contentTemplate": "### 📰 Blockbeats 新闻播报\n\n**标题:** {{ newsItem.title }}\n**来源:** {{ newsItem.sourceName }}\n**发布时间:** {{ newsItem.publishedAt }}\n\n**内容:**\n{{ newsItem.content }}\n\n🔗 [原文链接]({{ newsItem.originalUrl }})\n🌐 [来源页面]({{ newsItem.sourceUrl }})\n\n✨ 请及时查看并跟进。",
					"messageFormat":   "markdown",
				},
				"position": map[string]any{"x": 1840, "y": 200},
			},
			{"id": "end_notify", "type": "end", "label": "结束", "config": map[string]any{}, "position": map[string]any{"x": 2180, "y": 200}},
			{"id": "end_without_notify", "type": "end", "label": "结束", "config": map[string]any{}, "position": map[string]any{"x": 1500, "y": 392}},
		},
		"edges": []map[string]any{
			{"id": "edge_start_run", "source": "start_schedule", "target": "run_news_fetch"},
			{"id": "edge_run_publish", "source": "run_news_fetch", "target": "publish_news_synced"},
			{"id": "edge_publish_branch", "source": "publish_news_synced", "target": "branch_has_inserted_items"},
			{"id": "edge_branch_true_foreach", "source": "branch_has_inserted_items", "target": "foreach_inserted_items", "branch": "true"},
			{"id": "edge_branch_false_end", "source": "branch_has_inserted_items", "target": "end_without_notify", "branch": "false"},
			{"id": "edge_foreach_notify", "source": "foreach_inserted_items", "target": "notify_news_inserted"},
			{"id": "edge_notify_end", "source": "notify_news_inserted", "target": "end_notify"},
		},
	}
}

func buildAlertWorkflowFailedGraph(superRoleID int64) map[string]any {
	return map[string]any{
		"nodes": []map[string]any{
			{
				"id": "start_failed_event", "type": "start.event", "label": "失败事件开始",
				"config": map[string]any{
					"entryKey": "workflow.failed.default", "displayName": "工作流失败事件入口",
					"inputBindings": map[string]any{}, "eventType": "workflow.execution.failed",
					"filters": []any{},
				},
				"position": map[string]any{"x": 180, "y": 240},
			},
			{
				"id": "notify_workflow_failed", "type": "notify", "label": "发送告警",
				"config": map[string]any{
					"targets":         []map[string]any{{"targetType": "role", "targetId": superRoleID}},
					"channelTypes":    []string{"in_app"},
					"titleTemplate":   "Workflow failed: {{ trigger.payload.workflowDefinitionCode }}",
					"contentTemplate": "Workflow {{ trigger.payload.workflowDefinitionCode }} failed: {{ trigger.payload.errorMessage }}",
					"messageFormat":   "markdown",
				},
				"position": map[string]any{"x": 560, "y": 240},
			},
			{"id": "end_alert", "type": "end", "label": "结束", "config": map[string]any{}, "position": map[string]any{"x": 940, "y": 240}},
		},
		"edges": []map[string]any{
			{"id": "edge_start_notify", "source": "start_failed_event", "target": "notify_workflow_failed"},
			{"id": "edge_notify_end", "source": "notify_workflow_failed", "target": "end_alert"},
		},
	}
}
