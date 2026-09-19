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

type roleItem struct{ Code, Title, Description string }

var roleItems = []roleItem{
	{"R_SUPER", "超级管理员", "拥有系统全部管理权限"},
	{"R_USER", "普通用户", "默认登录用户"},
}

type menuItem struct {
	Name, Title, Path, Component, Icon, Parent string
	KeepAlive, FixedTab, IsHidden              bool
}

var coreMenuItems = []menuItem{
	{"Home", "首页", "/home", "/home/index", "ri:home-5-line", "", true, true, false},
	{"SchedulerCenter", "工作流", "/scheduler", "/index/index", "ri:time-line", "", false, false, false},
	{"WorkflowDefinitions", "工作流定义", "definition", "/scheduler/workflow", "ri:node-tree", "SchedulerCenter", true, false, false},
	{"ConfigCenter", "配置管理", "/config", "/index/index", "ri:tools-line", "", false, false, false},
	{"OutboundProxies", "代理配置", "proxies", "/system/proxy", "ri:route-line", "ConfigCenter", true, false, false},
	{"AiModelConfig", "模型配置", "ai-models", "/config/ai-model", "ri:brain-line", "ConfigCenter", true, false, false},
	{"Plugins", "插件管理", "plugins", "/system/plugins", "ri:puzzle-2-line", "ConfigCenter", true, false, false},
	{"System", "系统管理", "/system", "/index/index", "ri:settings-3-line", "", false, false, false},
	{"User", "用户管理", "user", "/system/user", "ri:user-3-line", "System", true, false, false},
	{"Role", "角色管理", "role", "/system/role", "ri:team-line", "System", true, false, false},
	{"Menus", "菜单管理", "menu", "/system/menu", "ri:menu-line", "System", true, false, false},
	{"UserCenter", "个人中心", "/profile", "/system/user-center", "", "", true, false, true},
}

var menuI18n = map[string][2]string{
	"Home":                {"首页", "Home"},
	"SchedulerCenter":     {"工作流", "Workflow"},
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

func Seed(ctx context.Context, gdb *gorm.DB, hasher *security.PasswordHasher, adminPassword string, pluginPages []sdk.RegisteredPage) error {
	menuItems := make([]menuItem, 0, len(coreMenuItems)+len(pluginPages))
	pluginMenus := map[string]bool{}
	ownMenus := make([]menuItem, 0, len(pluginPages))
	for _, page := range pluginPages {
		if page.Menu.Mode != "" && page.Menu.Mode != sdk.PluginMenuOwn || pluginMenus[page.PluginID] {
			continue
		}
		pluginMenus[page.PluginID] = true
		ownMenus = append(ownMenus, menuItem{
			Name: "PluginMenu:" + page.PluginID, Title: page.Menu.Title, Path: "/plugins/" + page.PluginID,
			Icon: page.Menu.Icon,
		})
	}
	for _, item := range coreMenuItems {
		if item.Name == "ConfigCenter" {
			menuItems = append(menuItems, ownMenus...)
		}
		menuItems = append(menuItems, item)
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
		default:
			menuName = "PluginMenu:" + page.PluginID
			pagePath = page.PageKey
		}
		menuItems = append(menuItems, menuItem{
			Name: "PluginPage:" + key, Title: page.Title, Path: pagePath, Parent: menuName,
			Component: "plugin:" + key, Icon: page.Icon, KeepAlive: page.KeepAlive,
		})
	}
	return gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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

func seedRoles(tx *gorm.DB) (map[string]*SystemRole, error) {
	now := time.Now().UTC()
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

func seedSuperUser(tx *gorm.DB, hasher *security.PasswordHasher, adminPassword string) (*SystemUser, error) {
	now := time.Now().UTC()
	tags, _ := json.Marshal([]string{"coinsphere", "system", "super-admin"})
	var user SystemUser
	err := tx.Where("username = ?", "coinsphere").First(&user).Error
	if err == gorm.ErrRecordNotFound {
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

func seedMenusAndButtons(tx *gorm.DB, menuItems []menuItem) (map[string]*SystemMenu, map[string]*SystemMenuButton, error) {
	now := time.Now().UTC()
	menuMap := map[string]*SystemMenu{}
	if err := tx.Model(&SystemMenu{}).Where("name LIKE ? OR name LIKE ?", "PluginPage:%", "PluginMenu:%").
		Updates(map[string]any{"is_active": false, "is_hidden": true, "updated_at": now}).Error; err != nil {
		return nil, nil, err
	}

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
			"menu_type": "menu", "active_menu_path": "",
			"sort": sort, "keep_alive": item.KeepAlive, "is_hidden": item.IsHidden,
			"is_hide_tab": false, "is_full_screen": false, "is_active": true,
			"fixed_tab": item.FixedTab, "badge_label": "",
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
	menuItems []menuItem,
) error {
	var existing SystemUserRole
	err := tx.Where("user_id = ? AND role_id = ?", user.ID, roles["R_SUPER"].ID).First(&existing).Error
	if err == gorm.ErrRecordNotFound {
		if err := tx.Create(&SystemUserRole{UserID: user.ID, RoleID: roles["R_SUPER"].ID, CreatedAt: time.Now().UTC()}).Error; err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	allMenuNames := make([]string, 0, len(menuItems))
	for _, item := range menuItems {
		allMenuNames = append(allMenuNames, item.Name)
	}

	roleMenus := map[string][]string{
		"R_SUPER": allMenuNames,
		"R_USER":  {"Home", "UserCenter"},
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
			if err := tx.Create(&SystemRoleMenu{RoleID: role.ID, MenuID: menu.ID, CreatedAt: time.Now().UTC()}).Error; err != nil {
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
			if err := tx.Create(&SystemRoleButton{RoleID: role.ID, ButtonID: button.ID, CreatedAt: time.Now().UTC()}).Error; err != nil {
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
			row = SystemI18nText{BizType: bizType, BizID: bizID, I18nKey: key, Locale: locale, Text: text, UpdatedAt: time.Now().UTC()}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else {
			updates := map[string]any{"i18n_key": key, "text": text, "updated_at": time.Now().UTC()}
			if err := tx.Model(&row).Updates(updates).Error; err != nil {
				return err
			}
		}
	}
	return nil
}
