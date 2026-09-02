// 本文件负责“首次启动写入种子数据(seed data)”:数据库刚建好、表还是空的时候,往里塞入
// 系统必须的初始记录——内置角色、超级管理员账号、前端菜单和多语言文案。所有写入都做成“幂等”:重复运行不会产生重复数据(已存在就
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
	"coinsphere/backend/plugin/sdk"
)

// struct(结构体)= 把若干字段打包成一个类型(类似别的语言只有数据的“类”)。这里三个字段
// 都是 string,可以合并写在同一行、用逗号分隔。见 GO入门笔记『struct、指针、slice、map』。
type roleItem struct{ Code, Title, Description string }

// []roleItem 是一个 slice(切片,可变长数组);这里列出“内置角色”清单。大括号里每一项
// {"R_SUPER", ...} 是一个 struct 字面量,按字段声明顺序依次填值(Code、Title、Description),
// 这叫“位置写法”。内置角色 = 系统自带的三种身份:超级管理员(全权)、普通用户、游客。
var roleItems = []roleItem{
	{"R_SUPER", "超级管理员", "拥有系统全部管理权限"},
	{"R_USER", "普通用户", "默认登录用户"},
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
var coreMenuItems = []menuItem{
	{"Home", "首页", "/home", "/home/index", "ri:home-5-line", "", true, true, false},
	{"SchedulerCenter", "工作流调度", "/scheduler", "/index/index", "ri:time-line", "", false, false, false},
	{"WorkflowDefinitions", "工作流定义", "definition", "/scheduler/workflow", "ri:node-tree", "SchedulerCenter", true, false, false},
	{"System", "系统管理", "/system", "/index/index", "ri:settings-3-line", "", false, false, false},
	{"User", "用户管理", "user", "/system/user", "ri:user-3-line", "System", true, false, false},
	{"Role", "角色管理", "role", "/system/role", "ri:team-line", "System", true, false, false},
	{"Menus", "菜单管理", "menu", "/system/menu", "ri:menu-line", "System", true, false, false},
	{"ConfigCenter", "配置管理", "/config", "/index/index", "ri:settings-4-line", "", false, false, false},
	{"OutboundProxies", "代理配置", "proxies", "/system/proxy", "ri:route-line", "ConfigCenter", true, false, false},
	{"AiModelConfig", "模型配置", "ai-models", "/config/ai-model", "ri:brain-line", "ConfigCenter", true, false, false},
	{"Plugins", "插件管理", "plugins", "/system/plugins", "ri:puzzle-2-line", "ConfigCenter", true, false, false},
	{"UserCenter", "个人中心", "/profile", "/system/user-center", "", "", true, false, true},
}

// map(字典/映射)= 一堆“键 → 值”的对应,写成 map[键类型]值类型。这里键是菜单 Name,
// 值是 [2]string(定长为 2 的数组):第 0 个存中文、第 1 个存英文,即菜单的多语言文案。
var menuI18n = map[string][2]string{
	"Home":                {"首页", "Home"},
	"SchedulerCenter":     {"工作流调度", "Workflow Scheduler"},
	"WorkflowDefinitions": {"工作流定义", "Workflow Definitions"},
	"System":              {"系统管理", "System Management"},
	"ConfigCenter":        {"配置管理", "Configuration"},
	"User":                {"用户管理", "User Management"},
	"Role":                {"角色管理", "Role Management"},
	"Menus":               {"菜单管理", "Menu Management"},
	"Plugins":             {"插件管理", "Plugins"},
	"OutboundProxies":     {"代理配置", "Proxy Configuration"},
	"AiModelConfig":       {"模型配置", "AI Models"},
	"UserCenter":          {"个人中心", "Profile"},
}

// Seed 写入内置角色、用户、菜单与 i18n。幂等。
func Seed(ctx context.Context, gdb *gorm.DB, hasher *security.PasswordHasher, adminPassword string, pluginPages []sdk.RegisteredPage) error {
	menuItems := append([]menuItem(nil), coreMenuItems...)
	pluginMenus := map[string]bool{}
	for _, page := range pluginPages {
		if page.Menu.Mode != "" && page.Menu.Mode != sdk.PluginMenuOwn || pluginMenus[page.PluginID] {
			continue
		}
		pluginMenus[page.PluginID] = true
		menuItems = append(menuItems, menuItem{
			Name: "PluginMenu:" + page.PluginID, Title: page.Menu.Title, Path: "/plugins/" + page.PluginID,
			Icon: page.Menu.Icon,
		})
	}
	for _, page := range pluginPages {
		key := page.PluginID + "/" + page.PageKey
		menuName := ""
		pagePath := "/plugins/" + key
		switch page.Menu.Mode {
		case sdk.PluginMenuExisting:
			menuName = page.Menu.Parent
			pagePath = page.PluginID + "/" + page.PageKey
		case sdk.PluginMenuDirect:
			// 页面直接作为顶级菜单。
		default:
			menuName = "PluginMenu:" + page.PluginID
			pagePath = page.PageKey
		}
		menuItems = append(menuItems, menuItem{
			Name: "PluginPage:" + key, Title: page.Title, Path: pagePath, Parent: menuName,
			Component: "plugin:" + key, Icon: page.Icon, KeepAlive: page.KeepAlive,
		})
	}
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
		menus, buttons, err := seedMenusAndButtons(tx, menuItems)
		if err != nil {
			return err
		}
		if err := seedRoleBindings(tx, roles, menus, buttons, user, menuItems); err != nil {
			return err
		}
		if err := seedI18n(tx, menus, buttons); err != nil {
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

// seedMenusAndButtons 写入内置菜单及其下面的“按钮级权限点”,返回两张便于查找的 map。
func seedMenusAndButtons(tx *gorm.DB, menuItems []menuItem) (map[string]*SystemMenu, map[string]*SystemMenuButton, error) {
	now := time.Now()
	menuMap := map[string]*SystemMenu{}
	if err := tx.Model(&SystemMenu{}).Where("name LIKE ? OR name LIKE ?", "PluginPage:%", "PluginMenu:%").
		Updates(map[string]any{"is_active": false, "is_hidden": true, "updated_at": now}).Error; err != nil {
		return nil, nil, err
	}

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
	for _, name := range []string{
		"Results", "DataCenter", "MarketMetadata", "MarketChart",
		"Workflows", "TradingCenter", "TradingAccounts", "StrategyManagement",
		"NewsData", "ConfigOverview", "AssistantAgentConfig",
		"NodeDefinitions", "WorkflowExecutions", "SystemLogs",
	} {
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
	menuItems []menuItem,
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
		"R_USER":  {"Home", "UserCenter"},
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
		"R_USER":  {},
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
