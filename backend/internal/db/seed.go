// 本文件负责“首次启动写入种子数据(seed data)”:数据库刚建好、表还是空的时候,往里塞入
// 系统必须的初始记录——内置角色、超级管理员账号、前端菜单、多语言文案、内置智能体、
// 内置通知渠道、内置工作流。所有写入都做成“幂等”:重复运行不会产生重复数据(已存在就
// 跳过或更新),所以每次启动都能安全地调用一次。
// 本文件大量使用 GORM(把 Go 结构体当数据库表来读写),见 GO入门笔记『框架:GORM』。

package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"

	"coinsphere/backend/internal/perm"
	"coinsphere/backend/internal/security"
)

// struct(结构体)= 把若干字段打包成一个类型(类似别的语言只有数据的“类”)。这里三个字段
// 都是 string,可以合并写在同一行、用逗号分隔。见 GO入门笔记『struct、指针、slice、map』。
type roleItem struct{ Code, Title, Description string }

// []roleItem 是一个 slice(切片,可变长数组);这里列出“内置角色”清单。大括号里每一项
// {"R_SUPER", ...} 是一个 struct 字面量,按字段声明顺序依次填值(Code、Title、Description),
// 这叫“位置写法”。内置角色 = 系统自带的三种身份:超级管理员(全权)、普通用户、游客。
var roleItems = []roleItem{
	{"R_SUPER", "超级管理员", "拥有系统全部管理权限"},
	{"R_USER", "普通用户", "默认登录用户,可维护自己的模型与通知配置"},
	{"R_GUEST", "游客", "仅允许匿名访问首页"},
}

// menuItem 描述前端左侧导航里的一个菜单项。多个同类型字段可合并声明:这里 6 个 string、
// 3 个 bool(布尔类型,取值 true/false)。
type menuItem struct {
	Name, Title, Path, Component, Icon, Parent string
	KeepAlive, FixedTab, IsHidden              bool
}

// 内置菜单清单(前端导航默认就有这些)。每项仍用“位置写法”,值的顺序必须和上面 menuItem
// 的字段顺序完全一致。Parent 为空字符串 "" 表示顶级菜单,否则填父菜单的 Name。
var menuItems = []menuItem{
	{"Home", "首页", "/home", "/home/index", "ri:home-5-line", "", true, true, false},
	{"TradingCenter", "交易管理", "/trading", "/index/index", "ri:exchange-funds-line", "", false, false, false},
	{"TradingAccounts", "交易账户", "accounts", "/trading/accounts", "ri:wallet-3-line", "TradingCenter", true, false, false},
	{"StrategyManagement", "策略管理", "strategies", "/strategy/drafts", "ri:code-box-line", "TradingCenter", true, false, false},
	{"SchedulerCenter", "工作流调度", "/scheduler", "/index/index", "ri:time-line", "", false, false, false},
	{"NodeDefinitions", "节点定义", "node-definition", "/scheduler/node-definition", "ri:stack-line", "SchedulerCenter", true, false, false},
	{"WorkflowDefinitions", "工作流定义", "definition", "/scheduler/workflow", "ri:node-tree", "SchedulerCenter", true, false, false},
	{"WorkflowExecutions", "执行记录", "execution", "/scheduler/execution", "ri:history-line", "SchedulerCenter", true, false, false},
	{"DataCenter", "数据管理", "/data", "/index/index", "ri:database-2-line", "", false, false, false},
	{"NewsData", "新闻数据", "news", "/data/news", "ri:newspaper-line", "DataCenter", true, false, false},
	{"MarketMetadata", "币种数据", "market-metadata", "/data/market-metadata", "ri:coins-line", "DataCenter", true, false, false},
	{"MarketChart", "K 线详情", "market-chart", "/data/market-chart", "ri:stock-line", "DataCenter", false, false, true},
	{"ConfigCenter", "配置管理", "/config", "/index/index", "ri:function-line", "", false, false, false},
	{"ConfigOverview", "配置概览", "overview", "/config/overview", "ri:apps-2-line", "ConfigCenter", true, false, false},
	{"AiModelConfig", "模型配置", "ai-model", "/config/ai-model", "ri:cpu-line", "ConfigCenter", true, false, false},
	{"AssistantAgentConfig", "智能体配置", "assistant-agent", "/config/assistant-agent", "ri:robot-2-line", "ConfigCenter", true, false, false},
	{"System", "系统管理", "/system", "/index/index", "ri:settings-3-line", "", false, false, false},
	{"User", "用户管理", "user", "/system/user", "ri:user-3-line", "System", true, false, false},
	{"Role", "角色管理", "role", "/system/role", "ri:team-line", "System", true, false, false},
	{"Menus", "菜单管理", "menu", "/system/menu", "ri:menu-line", "System", true, false, false},
	{"UserCenter", "个人中心", "/profile", "/system/user-center", "", "", true, false, true},
}

// map(字典/映射)= 一堆“键 → 值”的对应,写成 map[键类型]值类型。这里键是菜单 Name,
// 值是 [2]string(定长为 2 的数组):第 0 个存中文、第 1 个存英文,即菜单的多语言文案。
var menuI18n = map[string][2]string{
	"Home":                 {"首页", "Home"},
	"TradingCenter":        {"交易管理", "Trading"},
	"TradingAccounts":      {"交易账户", "Trading Accounts"},
	"StrategyManagement":   {"策略管理", "Strategies"},
	"SchedulerCenter":      {"工作流调度", "Workflow Scheduler"},
	"WorkflowDefinitions":  {"工作流定义", "Workflow Definitions"},
	"WorkflowExecutions":   {"执行记录", "Execution Records"},
	"NodeDefinitions":      {"节点定义", "Node Definitions"},
	"DataCenter":           {"数据管理", "Data Management"},
	"NewsData":             {"新闻数据", "News Data"},
	"MarketMetadata":       {"币种数据", "Instruments"},
	"MarketChart":          {"K 线详情", "Candles"},
	"ConfigCenter":         {"配置管理", "Configuration"},
	"ConfigOverview":       {"配置概览", "Config Overview"},
	"AiModelConfig":        {"模型配置", "Model Config"},
	"AssistantAgentConfig": {"智能体配置", "Assistant Agents"},
	"System":               {"系统管理", "System Management"},
	"User":                 {"用户管理", "User Management"},
	"Role":                 {"角色管理", "Role Management"},
	"Menus":                {"菜单管理", "Menu Management"},
	"UserCenter":           {"个人中心", "Profile"},
}

// agentItem 描述一个“内置智能体”(平台自带的 AI 助手)。注意 Starters 字段类型是 []string,
// 说明 slice 也能直接当结构体字段用,这里存“开场推荐问题”列表。
type agentItem struct {
	Code, Name, Avatar, Description, Prompt, Welcome string
	Starters                                         []string
	DataSourceType                                   string
	Sort                                             int
}

// 内置智能体清单。每个元素是跨多行书写的 struct 字面量(仍是位置写法);其中的
// []string{...} 是嵌套的 slice 字面量,对应“开场问题”那一列。
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
func Seed(ctx context.Context, gdb *gorm.DB, hasher *security.PasswordHasher, adminPassword string) error {
	// 入参里的 *gorm.DB、*security.PasswordHasher 都是“指针”(*类型 表示指向该类型的指针),
	// 传指针可避免复制、并共享同一个数据库连接。函数只返回一个 error:nil 表示成功。
	// Transaction(事务):把括号里的整段操作当成“要么全成功、要么全回滚”的整体;传进去的
	// func(tx *gorm.DB) error {...} 是匿名函数(闭包),内部一律改用 tx 读写。只要它返回非 nil 的
	// error,GORM 就自动回滚,之前写入的种子数据全部撤销。见 GO入门笔记『框架:GORM』。
	return gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// := 是“短变量声明”:一次性接住返回值并自动推断类型。seedRoles 返回 (map, error)。
		// 紧跟的 if err != nil 是 Go 最常见的错误处理:err 非 nil 就说明出错,直接把错误往上
		// 返回(在事务里 return 错误 = 触发回滚)。见 GO入门笔记『变量、函数、错误』。
		roles, err := seedRoles(tx)
		if err != nil {
			return err
		}
		user, err := seedSuperUser(tx, hasher, adminPassword)
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
		return nil
	})
}

// seedRoles 写入/更新内置角色,返回 code → *SystemRole 的 map 供后续绑定使用。
func seedRoles(tx *gorm.DB) (map[string]*SystemRole, error) {
	now := time.Now()
	result := map[string]*SystemRole{}
	// range 遍历 slice:每轮把一个元素“拷贝”给 item,开头的 _ 丢弃用不到的下标。
	// 下面这段是本文件反复出现的“有则更新、无则插入(upsert)”套路——GORM 也有现成的
	// FirstOrCreate 一步完成,这里为分别控制新增/更新字段而手写成 First + Create/Updates。
	for _, item := range roleItems {
		// var 声明一个 SystemRole 变量(各字段先取“零值”);下面 First 会把查到的行回填进它。
		var role SystemRole
		// Where(...).First(&role):等价 SQL 是 SELECT * FROM system_role WHERE code = ? LIMIT 1。
		// ? 是占位符(防注入);&role 传地址,GORM 把结果回填进 role;.Error 取出这步的错误。
		err := tx.Where("code = ?", item.Code).First(&role).Error
		if err == gorm.ErrRecordNotFound {
			// 查不到时 GORM 返回哨兵错误 gorm.ErrRecordNotFound → 表示该角色还不存在,去新建:
			// 这里用“键值写法”的 struct 字面量(字段名: 值)构造一条内置角色,IsSystem=true 标记系统内置。
			role = SystemRole{
				Code: item.Code, DisplayName: item.Title, Description: item.Description,
				IsEnabled: true, IsSystem: true, CreatedAt: now, UpdatedAt: now,
			}
			// Create(&role):等价 SQL 是 INSERT INTO system_role (...) VALUES (...)。
			if err := tx.Create(&role).Error; err != nil {
				return nil, err
			}
		} else if err != nil {
			return nil, err
		} else {
			// 已存在 → 只更新这几列。updates 是一个 map[string]any(列名 → 新值);下面
			// Model(&role).Updates(updates) 等价 UPDATE system_role SET 这些列 WHERE id = role.ID
			// (Updates 只改传入的列;若想“整行覆盖式”更新,GORM 里用 Save)。
			updates := map[string]any{
				"display_name": item.Title, "description": item.Description,
				"is_enabled": true, "is_system": true, "updated_at": now,
			}
			if err := tx.Model(&role).Updates(updates).Error; err != nil {
				return nil, err
			}
		}
		// 把这条角色的指针(&role)存进结果 map,后面绑定菜单/按钮时按 code 取用。
		result[item.Code] = &role
	}
	return result, nil
}

// seedSuperUser 写入内置超级管理员账号(仅当它还不存在时)。
func seedSuperUser(tx *gorm.DB, hasher *security.PasswordHasher, adminPassword string) (*SystemUser, error) {
	now := time.Now()
	// json.Marshal 把一个 Go 值序列化成 JSON 文本(返回 []byte)。数据库没有“数组”类型,
	// 常用一段 JSON 字符串代替;末尾的 _ 丢弃了 error。后面 string(tags) 再把 []byte 转成字符串。
	tags, _ := json.Marshal([]string{"coinsphere", "system", "super-admin"})
	var user SystemUser
	err := tx.Where("username = ?", "coinsphere").First(&user).Error
	if err == gorm.ErrRecordNotFound {
		// 内置“超级管理员”账号:用户名 coinsphere,初始密码取自配置(默认 coinsphere),但绝不明文存库——
		// 先经 hasher.HashPassword 哈希成不可逆摘要再写入。这个账号拥有后台全部权限。
		user = SystemUser{
			Username: "coinsphere", PasswordHash: hasher.HashPassword(adminPassword),
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

// seedAgents 写入内置智能体,套路与 seedRoles 一样:查不到就 Create。
func seedAgents(tx *gorm.DB) error {
	now := time.Now()
	for _, item := range agentItems {
		// 同样用 json.Marshal 把“开场问题”这个 []string 序列化成 JSON 字符串存库。
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

// seedMenusAndButtons 写入内置菜单及其下面的“按钮级权限点”,返回两张便于查找的 map。
func seedMenusAndButtons(tx *gorm.DB) (map[string]*SystemMenu, map[string]*SystemMenuButton, error) {
	now := time.Now()
	menuMap := map[string]*SystemMenu{}

	// 把一个匿名函数赋给变量 upsertMenu,就得到一个“局部函数”(闭包):它能直接读写外层的
	// tx、now、menuMap 等变量。下面在循环里反复调用它来插入/更新每个菜单。
	upsertMenu := func(item menuItem, sort int) error {
		// *int64 是“指向 int64 的指针”,默认零值是 nil。用“指针 + nil”表示可有可无:
		// nil 代表顶级菜单(无父级),非 nil 时用 &parent.ID 取父菜单主键的地址填入。
		var parentID *int64
		if item.Parent != "" {
			// 从 map 取值的“双返回值”写法:parent 是值,ok 表示这个键是否存在(false = 父菜单还没建)。
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
		// 无论新建还是已存在,都统一刷新一次字段:UPDATE system_menu SET (fields...) WHERE id = ?。
		if err := tx.Model(&SystemMenu{}).Where("id = ?", menu.ID).Updates(fields).Error; err != nil {
			return err
		}
		// First 第二个参数直接给主键时,等价 SELECT * FROM system_menu WHERE id = ? —— 按主键回读最新行。
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
	for _, name := range []string{"PaperTrading", "StrategyCenter", "PushData", "NotifyChannels"} {
		if err := tx.Model(&SystemMenu{}).Where("name = ?", name).
			Updates(map[string]any{"is_active": false, "is_hidden": true, "updated_at": now}).Error; err != nil {
			return nil, nil, err
		}
	}

	buttonMap := map[string]*SystemMenuButton{}
	// 按钮 = 页面上的操作按钮(新增/编辑/删除等),每个对应一个权限码。range 一个 map 时,
	// 两个循环变量分别是“键、值”(遍历顺序不保证)。
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

// seedRoleBindings 给三种内置角色绑定“能看哪些菜单、能用哪些按钮”,并把超管账号关联到超管角色。
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

	// make([]string, 0, N) 造一个长度为 0、但预留容量 N 的空 slice;随后用 append 往里追加元素。
	// 预留容量只是性能优化(减少扩容),不影响结果。
	allMenuNames := make([]string, 0, len(menuItems))
	for _, item := range menuItems {
		allMenuNames = append(allMenuNames, item.Name)
	}

	// map 的值也可以是 slice:roleMenus 表示“每种角色能看到的菜单名清单”——超管看全部,
	// 普通用户只看几项,游客只看首页。
	roleMenus := map[string][]string{
		"R_SUPER": allMenuNames,
		"R_USER":  {"Home", "TradingCenter", "TradingAccounts", "DataCenter", "MarketMetadata", "MarketChart", "ConfigCenter", "AiModelConfig", "UserCenter"},
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

	// “先删后插”实现幂等重建:先 DELETE FROM system_role_menu WHERE role_id = ? 清掉旧绑定,
	// 再逐条 Create 新绑定。这样无论跑多少次,结果都和上面的清单完全一致。
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

// seedI18n 为每个菜单/按钮写入中英文两条翻译文案。
func seedI18n(tx *gorm.DB, menus map[string]*SystemMenu, buttons map[string]*SystemMenuButton) error {
	for menuName, texts := range menuI18n {
		menu, ok := menus[menuName]
		if !ok {
			continue
		}
		// fmt.Sprintf 按模板拼出字符串并返回(不打印);%d 是整数占位符,这里拼出翻译用的 key。
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
			if err := upsertI18nPair(tx, "button", button.ID, key, zh, en); err != nil {
				return err
			}
		}
	}
	return nil
}

// upsertI18nPair 对同一条业务记录写入 zh、en 两个语种的文案(有则更新、无则插入)。
func upsertI18nPair(tx *gorm.DB, bizType string, bizID int64, key, zh, en string) error {
	// 直接对一个“临时 map 字面量”做 range:等于分别用 "zh"、"en" 两个 locale 各跑一次循环。
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

// seedBuiltinChannel 写入内置的“站内通知”渠道(系统自带,用于站内消息推送)。
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

// seedWorkflows 写入内置工作流。工作流本身是一张“节点 + 连线”的图,
// 序列化成 JSON 存进 GraphJSON 列。
func seedWorkflows(tx *gorm.DB, superRoleID int64) error {
	now := time.Now()
	// []struct{...}{...} 是“匿名结构体的 slice”:这个结构只用一次,懒得单独 type 命名,
	// 就地定义字段(Code/DisplayName/Description/Graph)再直接列出元素。
	definitions := []struct {
		Code, DisplayName, Description string
		Graph                          map[string]any
	}{
		{
			"alert_workflow_failed", "工作流失败告警",
			"响应 workflow.execution.failed 事件并发送失败告警通知。",
			buildAlertWorkflowFailedGraph(superRoleID),
		},
		{
			"binance_market_metadata_sync", "Binance 币种元数据同步",
			"按全局设置手动或每小时同步 Binance 币种元数据。",
			buildMarketMetadataSyncGraph(),
		},
	}
	// int64(1) 是类型转换:把字面量 1 明确成 int64 类型。creator 代表“创建人 = 1 号用户(超管)”。
	creator := int64(1)
	for _, item := range definitions {
		// 把整张图(map)序列化成 JSON 文本,准备存进下面的 GraphJSON 字段。
		graphJSON, _ := json.Marshal(item.Graph)
		var definition WorkflowDefinition
		err := tx.Where("code = ? AND version = ?", item.Code, 1).First(&definition).Error
		if err == gorm.ErrRecordNotFound {
			// IsBuiltin=true 标记为内置工作流;CreatedBy 字段类型是 *int64,所以用 &creator 取地址填入。
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

		// 每条工作流还需要一行“运行时状态”,记录当前激活的是哪个定义版本。
		var state WorkflowRuntimeState
		err = tx.Where("workflow_code = ?", item.Code).First(&state).Error
		if err == gorm.ErrRecordNotFound {
			// ActiveWorkflowDefinitionID/ActivatedAt/ActivatedBy 都是指针字段,用 & 取地址表示“已设值”。
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

func buildMarketMetadataSyncGraph() map[string]any {
	return map[string]any{
		"nodes": []map[string]any{
			{
				"id": "start_manual", "type": "start.manual", "label": "手动同步",
				"config": map[string]any{
					"entryKey": "market.metadata.manual", "displayName": "手动同步",
					"inputBindings": map[string]any{},
				},
				"position": map[string]any{"x": 120, "y": 180},
			},
			{
				"id": "start_hourly", "type": "start.schedule", "label": "每小时同步",
				"config": map[string]any{
					"entryKey": "market.metadata.hourly", "displayName": "每小时同步",
					"inputBindings": map[string]any{}, "scheduleType": "interval", "value": 1, "unit": "hours",
				},
				"position": map[string]any{"x": 120, "y": 340},
			},
			{
				"id": "sync_metadata", "type": "market.metadata.sync", "label": "同步币种元数据",
				"config": map[string]any{}, "position": map[string]any{"x": 500, "y": 260},
			},
			{
				"id": "end_sync", "type": "end", "label": "完成",
				"config": map[string]any{}, "position": map[string]any{"x": 880, "y": 260},
			},
		},
		"edges": []map[string]any{
			{"id": "edge_manual_sync", "source": "start_manual", "target": "sync_metadata"},
			{"id": "edge_hourly_sync", "source": "start_hourly", "target": "sync_metadata"},
			{"id": "edge_sync_end", "source": "sync_metadata", "target": "end_sync"},
		},
	}
}

// buildAlertWorkflowFailedGraph 手写“工作流失败告警”的图:结构同上,更简单——
// start.event(监听 workflow.execution.failed 事件)→ notify(发告警)→ end。
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
