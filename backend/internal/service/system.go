package service

import (
	"strings"
	"time"

	"gorm.io/gorm"

	"coinsphere/backend/internal/db"
)

const layoutComponent = "/index/index"

const protectedSuperUsername = "coinsphere"

// protectedRoleCodes 用 map 当"集合":把内置角色编码放进来(值填 true 只是占位),
// 后面用来禁止修改/删除这些系统角色。
var protectedRoleCodes = map[string]bool{"R_SUPER": true, "R_GUEST": true}

// ---------- 查询 ----------

// UserListQuery 用户分页过滤。
// 注意 ID *int64、IsActive *bool 用的是指针:nil 表示"这个条件没填、不参与过滤",
// 以此区分于 0 / false 这种"填了、但值恰好是零"的情况。见 GO入门笔记『复合类型』。
type UserListQuery struct {
	Current  int
	Size     int
	ID       *int64
	Username string
	Gender   string
	Phone    string
	Email    string
	IsActive *bool
}

// ListUsers 分页查询用户,附带角色编码。
//
// 这个方法演示了本文件通用的"分页查询"套路:先用 Model 起一个查询,按传入条件逐个 .Where 拼过滤,
// 先 Count 出总数,再用 Offset/Limit 取当前页,最后把结果序列化成前端要的形状。
func (a *App) ListUsers(query UserListQuery) (M, error) {
	// q 是一个可复用的"查询构造器":GORM 链式调用每步都返回它,可以边判断边往上叠加条件。
	// 只对非空/非 nil 的过滤项才 .Where,避免把空条件也拼进 SQL。见 GO入门笔记『框架:GORM』。
	q := a.DB.Model(&db.SystemUser{})
	// 指针字段要先判 != nil,再用 *query.ID 解引用取出它指向的值(* 顺着指针拿到真正的数字)。
	if query.ID != nil {
		q = q.Where("id = ?", *query.ID)
	}
	if query.Username != "" {
		q = q.Where("username LIKE ?", "%"+query.Username+"%")
	}
	if query.Gender != "" {
		q = q.Where("gender = ?", query.Gender)
	}
	if query.Phone != "" {
		q = q.Where("phone LIKE ?", "%"+query.Phone+"%")
	}
	if query.Email != "" {
		q = q.Where("email LIKE ?", "%"+query.Email+"%")
	}
	if query.IsActive != nil {
		q = q.Where("is_active = ?", *query.IsActive)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, err
	}
	var users []db.SystemUser
	// Offset/Limit 就是分页:跳过前 (页码-1)*每页 条,再取 每页 条;Order 指定排序。.Find(&users) 取多行。
	if err := q.Order("created_at DESC, id DESC").
		Offset((query.Current - 1) * query.Size).Limit(query.Size).Find(&users).Error; err != nil {
		return nil, err
	}
	userIDs := make([]int64, 0, len(users))
	for _, user := range users {
		userIDs = append(userIDs, user.ID)
	}
	roleMap := a.listRoleCodesForUsers(userIDs)
	records := make([]M, 0, len(users))
	// 用 &users[i] 取切片里第 i 个元素的地址传给 serializeUser:传指针既避免复制整个结构体,
	// 也保证拿到的就是切片里那一份。注意不要用 range 的循环变量取地址(所有轮次可能是同一个副本地址)。
	for i := range users {
		records = append(records, serializeUser(&users[i], roleMap[users[i].ID]))
	}
	return pagedResult(records, query.Current, query.Size, total), nil
}

// RoleListQuery 角色分页过滤。
type RoleListQuery struct {
	Current     int
	Size        int
	ID          *int64
	DisplayName string
	Code        string
	Description string
	IsEnabled   *bool
}

// ListRoles 分页查询角色。
// 套路同 ListUsers:拼 Where 过滤 → Count 总数 → Offset/Limit 取页 → Find 结果。
func (a *App) ListRoles(query RoleListQuery) (M, error) {
	q := a.DB.Model(&db.SystemRole{})
	if query.ID != nil {
		q = q.Where("id = ?", *query.ID)
	}
	if query.DisplayName != "" {
		q = q.Where("display_name LIKE ?", "%"+query.DisplayName+"%")
	}
	if query.Code != "" {
		q = q.Where("code LIKE ?", "%"+query.Code+"%")
	}
	if query.Description != "" {
		q = q.Where("description LIKE ?", "%"+query.Description+"%")
	}
	if query.IsEnabled != nil {
		q = q.Where("is_enabled = ?", *query.IsEnabled)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, err
	}
	var roles []db.SystemRole
	if err := q.Order("id ASC").Offset((query.Current - 1) * query.Size).Limit(query.Size).Find(&roles).Error; err != nil {
		return nil, err
	}
	records := make([]M, 0, len(roles))
	for i := range roles {
		records = append(records, serializeRole(&roles[i]))
	}
	return pagedResult(records, query.Current, query.Size, total), nil
}

// GetMenuTree 按角色返回可访问菜单树。
// 分两步查库:先查出这些角色能看到的菜单,再查这些菜单下能看到的按钮,
// 最后交给 buildMenuTreePayload 拼成层级树。Distinct("menus.*") 是"整行去重"
// (同一菜单可能因多个角色被 JOIN 出多次);role_id IN ? 传切片会展开成 SQL 的 IN (...)。
func (a *App) GetMenuTree(principal *Principal) ([]M, error) {
	if len(principal.RoleIDs) == 0 {
		return []M{}, nil
	}
	var menus []db.SystemMenu
	if err := a.DB.Distinct("menus.*").
		Joins("JOIN role_menus ON role_menus.menu_id = menus.id").
		Where("role_menus.role_id IN ? AND menus.is_active = ?", principal.RoleIDs, true).
		Order("menus.sort ASC, menus.id ASC").
		Find(&menus).Error; err != nil {
		return nil, err
	}
	if len(menus) == 0 {
		return []M{}, nil
	}
	menuIDs := collectIDs(menus, func(m db.SystemMenu) int64 { return m.ID })
	var buttons []db.SystemMenuButton
	if err := a.DB.Distinct("menu_buttons.*").
		Joins("JOIN role_menu_buttons ON role_menu_buttons.button_id = menu_buttons.id").
		Where("role_menu_buttons.role_id IN ? AND menu_buttons.menu_id IN ?", principal.RoleIDs, menuIDs).
		Order("menu_buttons.sort ASC, menu_buttons.id ASC").
		Find(&buttons).Error; err != nil {
		return nil, err
	}
	return a.buildMenuTreePayload(menus, buttons), nil
}

// GetMenuManagementTree 返回管理端完整菜单树。
// 与 GetMenuTree 不同:管理端不按角色过滤,直接取全部菜单和按钮,拼成完整树给后台配置用。
func (a *App) GetMenuManagementTree() ([]M, error) {
	var menus []db.SystemMenu
	if err := a.DB.Order("sort ASC, id ASC").Find(&menus).Error; err != nil {
		return nil, err
	}
	if len(menus) == 0 {
		return []M{}, nil
	}
	menuIDs := collectIDs(menus, func(m db.SystemMenu) int64 { return m.ID })
	var buttons []db.SystemMenuButton
	if err := a.DB.Where("menu_id IN ?", menuIDs).Order("sort ASC, id ASC").Find(&buttons).Error; err != nil {
		return nil, err
	}
	return a.buildMenuTreePayload(menus, buttons), nil
}

// GetI18nDict 返回运行时国际化字典。
func (a *App) GetI18nDict(scope string) M {
	result := M{"zh": M{}, "en": M{}}
	if scope != "menu" {
		return result
	}
	var rows []db.SystemI18nText
	a.DB.Where("locale IN ?", []string{"zh", "en"}).Find(&rows)
	for _, row := range rows {
		// result[row.Locale].(M) 是"类型断言":result 里的值类型是 any,这里断言它其实是个 M(子对象)。
		// 逗号后面的 ok 为 true 才说明断言成功(这种写法不会 panic),然后往这个子对象里塞键值。
		if localeMap, ok := result[row.Locale].(M); ok {
			localeMap[row.I18nKey] = row.Text
		}
	}
	return result
}

// ---------- 命令 ----------

// UserUpsertPayload 用户创建/更新载荷。
// 反引号里的 `json:"username"` 是 struct tag(标签):告诉 JSON 库前端传来的字段名怎么对应到这里。
// IsActive *bool 用指针,能区分"没传"(nil)和"传了 false"。[]string 是字符串切片。见 GO入门笔记『复合类型』。
type UserUpsertPayload struct {
	Username  string   `json:"username"`
	Nickname  string   `json:"nickname"`
	FullName  string   `json:"fullName"`
	Gender    string   `json:"gender"`
	Phone     string   `json:"phone"`
	Email     string   `json:"email"`
	Avatar    string   `json:"avatar"`
	IsActive  *bool    `json:"isActive"`
	RoleCodes []string `json:"roleCodes"`
	Password  string   `json:"password"`
}

// isActive 处理"可选布尔":没传(IsActive 为 nil)时默认 true,传了就用它的值(*p.IsActive 解引用)。
func (p *UserUpsertPayload) isActive() bool { return p.IsActive == nil || *p.IsActive }

// CreateUser 创建用户。
// 顺序:先做各种校验(非空、密码长度、用户名唯一)→ 解析要分配的角色 → 哈希密码并组装
// SystemUser 结构体入库(Create = INSERT)→ 最后写入"用户-角色"关联表。
func (a *App) CreateUser(payload UserUpsertPayload, principal *Principal) (M, error) {
	if strings.TrimSpace(payload.Username) == "" || strings.TrimSpace(payload.Nickname) == "" {
		return nil, bizErr("用户名和昵称不能为空")
	}
	if payload.Password == "" {
		return nil, bizErr("创建用户时必须设置密码")
	}
	if len(payload.Password) < 6 {
		return nil, bizErr("密码长度至少 6 位")
	}
	var count int64
	a.DB.Model(&db.SystemUser{}).Where("username = ?", payload.Username).Count(&count)
	if count > 0 {
		return nil, bizErr("用户名已存在")
	}
	roles, err := a.resolveAssignableRoles(payload.RoleCodes)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	fullName := payload.FullName
	if fullName == "" {
		fullName = payload.Nickname
	}
	// 结构体字面量:逐字段填好一个 SystemUser,再用 Create(&user) 入库(INSERT)。
	// 密码永远存哈希值(HashPassword),绝不存明文。
	user := db.SystemUser{
		Username: payload.Username, PasswordHash: a.Hasher.HashPassword(payload.Password),
		Nickname: payload.Nickname, FullName: fullName, Gender: normalizeGender(payload.Gender),
		Phone: strings.TrimSpace(payload.Phone), Email: strings.TrimSpace(payload.Email),
		Avatar: strings.TrimSpace(payload.Avatar), IsActive: payload.isActive(),
		TagsJSON:  "[]",
		CreatedBy: principal.User.Username, UpdatedBy: principal.User.Username,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := a.DB.Create(&user).Error; err != nil {
		return nil, err
	}
	if err := a.replaceUserRoles(user.ID, roles); err != nil {
		return nil, err
	}
	return serializeUser(&user, roleCodesOf(roles)), nil
}

// UpdateUser 更新用户。
// 除了字段校验,还带业务规则:内置超管不能在这里改、不能停用当前登录的账号。
// Updates(fields) 只更新 map 里给出的那几列;密码留空则跳过、不改密码。
func (a *App) UpdateUser(userID int64, payload UserUpsertPayload, principal *Principal) (M, error) {
	var user db.SystemUser
	if err := a.DB.First(&user, userID).Error; err != nil {
		return nil, bizErr("用户不存在")
	}
	if user.Username == protectedSuperUsername {
		return nil, bizErr("内置超级管理员不能在这里修改")
	}
	if payload.Username != user.Username {
		var count int64
		a.DB.Model(&db.SystemUser{}).Where("username = ? AND id <> ?", payload.Username, userID).Count(&count)
		if count > 0 {
			return nil, bizErr("用户名已存在")
		}
	}
	if principal.User.ID == userID && !payload.isActive() {
		return nil, bizErr("不能停用当前登录账号")
	}
	roles, err := a.resolveAssignableRoles(payload.RoleCodes)
	if err != nil {
		return nil, err
	}
	fullName := payload.FullName
	if fullName == "" {
		fullName = payload.Nickname
	}
	fields := map[string]any{
		"username": payload.Username, "nickname": payload.Nickname, "full_name": fullName,
		"gender": normalizeGender(payload.Gender), "phone": strings.TrimSpace(payload.Phone),
		"email": strings.TrimSpace(payload.Email), "avatar": strings.TrimSpace(payload.Avatar),
		"is_active": payload.isActive(), "updated_by": principal.User.Username, "updated_at": time.Now(),
	}
	if payload.Password != "" {
		if len(payload.Password) < 6 {
			return nil, bizErr("密码长度至少 6 位")
		}
		fields["password_hash"] = a.Hasher.HashPassword(payload.Password)
	}
	if err := a.DB.Model(&db.SystemUser{}).Where("id = ?", userID).Updates(fields).Error; err != nil {
		return nil, err
	}
	if err := a.replaceUserRoles(userID, roles); err != nil {
		return nil, err
	}
	if err := a.DB.First(&user, userID).Error; err != nil {
		return nil, err
	}
	return serializeUser(&user, roleCodesOf(roles)), nil
}

// DeleteUser 删除用户。
// 业务规则:内置超管、当前登录账号都不允许删除。user_roles 关联由数据库外键级联删除,不必手动清。
func (a *App) DeleteUser(userID int64, principal *Principal) error {
	var user db.SystemUser
	if err := a.DB.First(&user, userID).Error; err != nil {
		return bizErr("用户不存在")
	}
	if user.Username == protectedSuperUsername {
		return bizErr("内置超级管理员不能删除")
	}
	if principal.User.ID == userID {
		return bizErr("不能删除当前登录账号")
	}
	// user_roles 由外键级联删除。
	return a.DB.Delete(&user).Error
}

// RoleUpsertPayload 角色载荷。
type RoleUpsertPayload struct {
	DisplayName string `json:"displayName"`
	Code        string `json:"code"`
	Description string `json:"description"`
	IsEnabled   *bool  `json:"isEnabled"`
}

// isEnabled 同 isActive:可选布尔,没传默认 true。
func (p *RoleUpsertPayload) isEnabled() bool { return p.IsEnabled == nil || *p.IsEnabled }

// CreateRole 创建角色。
// 名称、编码先去空格(编码还转大写),校验名称/编码都不重复后 Create 入库。
func (a *App) CreateRole(payload RoleUpsertPayload) (M, error) {
	roleName := strings.TrimSpace(payload.DisplayName)
	roleCode := strings.ToUpper(strings.TrimSpace(payload.Code))
	if roleName == "" || roleCode == "" {
		return nil, bizErr("角色名称与编码不能为空")
	}
	var count int64
	a.DB.Model(&db.SystemRole{}).Where("display_name = ?", roleName).Count(&count)
	if count > 0 {
		return nil, bizErr("角色名称已存在")
	}
	a.DB.Model(&db.SystemRole{}).Where("code = ?", roleCode).Count(&count)
	if count > 0 {
		return nil, bizErr("角色编码已存在")
	}
	now := time.Now()
	role := db.SystemRole{
		DisplayName: roleName, Code: roleCode, Description: strings.TrimSpace(payload.Description),
		IsEnabled: payload.isEnabled(), CreatedAt: now, UpdatedAt: now,
	}
	if err := a.DB.Create(&role).Error; err != nil {
		return nil, err
	}
	return serializeRole(&role), nil
}

// UpdateRole 更新角色。
// requireMutableRole 先挡住系统内置角色(不可改),再校验名称/编码唯一后 Updates。
func (a *App) UpdateRole(roleID int64, payload RoleUpsertPayload) (M, error) {
	role, err := a.requireMutableRole(roleID)
	if err != nil {
		return nil, err
	}
	roleName := strings.TrimSpace(payload.DisplayName)
	roleCode := strings.ToUpper(strings.TrimSpace(payload.Code))
	if roleName == "" || roleCode == "" {
		return nil, bizErr("角色名称与编码不能为空")
	}
	var count int64
	a.DB.Model(&db.SystemRole{}).Where("display_name = ? AND id <> ?", roleName, roleID).Count(&count)
	if count > 0 {
		return nil, bizErr("角色名称已存在")
	}
	a.DB.Model(&db.SystemRole{}).Where("code = ? AND id <> ?", roleCode, roleID).Count(&count)
	if count > 0 {
		return nil, bizErr("角色编码已存在")
	}
	updates := map[string]any{
		"display_name": roleName, "code": roleCode,
		"description": strings.TrimSpace(payload.Description),
		"is_enabled":  payload.isEnabled(), "updated_at": time.Now(),
	}
	if err := a.DB.Model(role).Updates(updates).Error; err != nil {
		return nil, err
	}
	if err := a.DB.First(role, roleID).Error; err != nil {
		return nil, err
	}
	return serializeRole(role), nil
}

// DeleteRole 删除角色。
// 系统内置角色不可删;已经分配给用户的角色要先解除关联(Count 一下 user_roles)再删。
func (a *App) DeleteRole(roleID int64) error {
	role, err := a.requireMutableRole(roleID)
	if err != nil {
		return err
	}
	var count int64
	a.DB.Model(&db.SystemUserRole{}).Where("role_id = ?", roleID).Count(&count)
	if count > 0 {
		return bizErr("该角色已分配给用户,请先解除关联后再删除")
	}
	return a.DB.Delete(role).Error
}

// RolePermissionPayload 角色权限载荷。
type RolePermissionPayload struct {
	MenuIDs   []int64 `json:"menuIds"`
	ButtonIDs []int64 `json:"buttonIds"`
}

// SaveRolePermissions 保存角色的菜单与按钮权限。
// 先校验菜单/按钮 ID 都合法、且按钮必须挂在被选中的菜单下,再在一个事务里"先删后插"整套关联。
func (a *App) SaveRolePermissions(roleID int64, payload RolePermissionPayload) error {
	if _, err := a.requireMutableRole(roleID); err != nil {
		return err
	}
	menuIDs := dedupeInt64(payload.MenuIDs)
	buttonIDs := dedupeInt64(payload.ButtonIDs)

	var validMenus []db.SystemMenu
	if len(menuIDs) > 0 {
		a.DB.Select("id").Where("id IN ?", menuIDs).Find(&validMenus)
	}
	if len(validMenus) != len(menuIDs) {
		return bizErr("存在无效的菜单")
	}
	validMenuSet := map[int64]bool{}
	for _, menu := range validMenus {
		validMenuSet[menu.ID] = true
	}

	var validButtons []db.SystemMenuButton
	if len(buttonIDs) > 0 {
		a.DB.Select("id, menu_id").Where("id IN ?", buttonIDs).Find(&validButtons)
	}
	if len(validButtons) != len(buttonIDs) {
		return bizErr("存在无效的按钮权限")
	}
	for _, button := range validButtons {
		if !validMenuSet[button.MenuID] {
			return bizErr("按钮权限必须挂载在已选中的菜单下")
		}
	}

	// Transaction 开一个数据库事务:传进去的这个函数里,所有操作要么全部成功一起提交,
	// 要么一旦返回非 nil 的 error 就整体回滚(不会只删一半)。事务内必须用参数 tx(而不是 a.DB)
	// 来执行,才算在同一个事务里。这里"先按 role_id 删干净、再逐条 Create"是典型的"整表重置关联"写法。
	return a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("role_id = ?", roleID).Delete(&db.SystemRoleMenu{}).Error; err != nil {
			return err
		}
		for _, menuID := range menuIDs {
			if err := tx.Create(&db.SystemRoleMenu{RoleID: roleID, MenuID: menuID, CreatedAt: time.Now()}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("role_id = ?", roleID).Delete(&db.SystemRoleButton{}).Error; err != nil {
			return err
		}
		for _, buttonID := range buttonIDs {
			if err := tx.Create(&db.SystemRoleButton{RoleID: roleID, ButtonID: buttonID, CreatedAt: time.Now()}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// I18nTextsPayload 双语文案。
type I18nTextsPayload struct {
	Zh string `json:"zh"`
	En string `json:"en"`
}

// MenuUpsertPayload 菜单载荷。
type MenuUpsertPayload struct {
	ParentID       *int64           `json:"parentId"`
	Title          string           `json:"title"`
	Name           string           `json:"name"`
	PermissionCode string           `json:"permissionCode"`
	Path           string           `json:"path"`
	Component      string           `json:"component"`
	Icon           string           `json:"icon"`
	Sort           int              `json:"sort"`
	IsActive       *bool            `json:"isActive"`
	KeepAlive      bool             `json:"keepAlive"`
	IsHidden       bool             `json:"isHidden"`
	HideTab        bool             `json:"hideTab"`
	ExternalURL    string           `json:"externalUrl"`
	UseIframe      bool             `json:"useIframe"`
	BadgeLabel     string           `json:"badgeLabel"`
	FixedTab       bool             `json:"fixedTab"`
	ActiveMenuPath string           `json:"activeMenuPath"`
	RoleCodes      []string         `json:"roleCodes"`
	IsFullScreen   bool             `json:"isFullScreen"`
	I18nKey        string           `json:"i18nKey"`
	I18nTexts      I18nTextsPayload `json:"i18nTexts"`
}

// CreateMenu 创建菜单。
// CreateMenu / UpdateMenu 都转调 upsertMenu:第一个参数为 nil 走"新增",非 nil 走"更新",
// 这样新增和更新共用同一套校验逻辑(upsert = update + insert)。
func (a *App) CreateMenu(payload MenuUpsertPayload) (M, error) {
	return a.upsertMenu(nil, payload)
}

// UpdateMenu 更新菜单。
func (a *App) UpdateMenu(menuID int64, payload MenuUpsertPayload) (M, error) {
	var menu db.SystemMenu
	if err := a.DB.First(&menu, menuID).Error; err != nil {
		return nil, bizErr("菜单不存在")
	}
	return a.upsertMenu(&menu, payload)
}

func (a *App) upsertMenu(existing *db.SystemMenu, payload MenuUpsertPayload) (M, error) {
	if strings.TrimSpace(payload.Name) == "" || strings.TrimSpace(payload.Title) == "" || strings.TrimSpace(payload.Path) == "" {
		return nil, bizErr("菜单名称、标题与路径不能为空")
	}
	var excludeID int64
	if existing != nil {
		excludeID = existing.ID
	}
	var count int64
	a.DB.Model(&db.SystemMenu{}).Where("name = ? AND id <> ?", payload.Name, excludeID).Count(&count)
	if count > 0 {
		return nil, bizErr("菜单路由名称已存在")
	}
	permissionCode := strings.TrimSpace(payload.PermissionCode)
	if permissionCode != "" {
		a.DB.Model(&db.SystemMenu{}).Where("permission_code = ? AND id <> ?", permissionCode, excludeID).Count(&count)
		if count > 0 {
			return nil, bizErr("菜单权限编码已存在")
		}
	}
	if err := a.validateI18nKey(payload.I18nKey, "menu", excludeID); err != nil {
		return nil, err
	}
	parentID, err := a.resolveParentMenu(payload.ParentID, excludeID)
	if err != nil {
		return nil, err
	}
	roles, err := a.resolveRoleCodes(payload.RoleCodes, true)
	if err != nil {
		return nil, err
	}
	// permCodePtr 用 *string(字符串指针):权限码非空时才让它指向该字符串,为空时保持 nil,
	// 存进数据库就是 NULL(而不是空串"")——这样"未设置"和"设为空"在库里能区分开。
	var permCodePtr *string
	if permissionCode != "" {
		permCodePtr = &permissionCode
	}
	isActive := payload.IsActive == nil || *payload.IsActive
	fields := map[string]any{
		"parent_id": parentID, "path": strings.TrimSpace(payload.Path), "name": strings.TrimSpace(payload.Name),
		"permission_code": permCodePtr, "component": strings.TrimSpace(payload.Component),
		"title": strings.TrimSpace(payload.Title), "icon": strings.TrimSpace(payload.Icon),
		"external_url": strings.TrimSpace(payload.ExternalURL), "active_menu_path": strings.TrimSpace(payload.ActiveMenuPath),
		"sort": payload.Sort, "keep_alive": payload.KeepAlive, "is_hidden": payload.IsHidden,
		"is_hide_tab": payload.HideTab, "is_full_screen": payload.IsFullScreen, "is_active": isActive,
		"use_iframe": payload.UseIframe, "fixed_tab": payload.FixedTab,
		"badge_label": strings.TrimSpace(payload.BadgeLabel), "updated_at": time.Now(),
	}
	var menuID int64
	if existing == nil {
		menu := db.SystemMenu{Name: strings.TrimSpace(payload.Name), CreatedAt: time.Now()}
		if err := a.DB.Create(&menu).Error; err != nil {
			return nil, err
		}
		menuID = menu.ID
	} else {
		menuID = existing.ID
	}
	if err := a.DB.Model(&db.SystemMenu{}).Where("id = ?", menuID).Updates(fields).Error; err != nil {
		return nil, err
	}
	if err := a.replaceMenuRoles(menuID, roles); err != nil {
		return nil, err
	}
	if err := a.upsertI18nPair("menu", menuID, strings.TrimSpace(payload.I18nKey), payload.I18nTexts); err != nil {
		return nil, err
	}
	return M{"id": menuID}, nil
}

// DeleteMenu 删除菜单及其子树与相关 i18n。
// 删一个菜单要连它下面整棵子树一起删。这里用一个 pending 队列做"广度遍历":不断取出一个菜单、
// 查出它的直接子菜单 id 加进来,直到再没有后代,收集齐所有需要删除的菜单 id;最后在事务里一并删除。
func (a *App) DeleteMenu(menuID int64) error {
	var menu db.SystemMenu
	if err := a.DB.First(&menu, menuID).Error; err != nil {
		return bizErr("菜单不存在")
	}
	allMenuIDs := []int64{menuID}
	pending := []int64{menuID}
	// pending[0] 取队首;pending = pending[1:] 把队首切掉(切片重新切一刀,不复制底层数组);
	// append(pending, childIDs...) 把子节点追加到队尾。len(pending) 为 0 表示队列空、遍历结束。
	for len(pending) > 0 {
		current := pending[0]
		pending = pending[1:]
		var childIDs []int64
		a.DB.Model(&db.SystemMenu{}).Where("parent_id = ?", current).Pluck("id", &childIDs)
		allMenuIDs = append(allMenuIDs, childIDs...)
		pending = append(pending, childIDs...)
	}
	var buttonIDs []int64
	a.DB.Model(&db.SystemMenuButton{}).Where("menu_id IN ?", allMenuIDs).Pluck("id", &buttonIDs)

	return a.DB.Transaction(func(tx *gorm.DB) error {
		if len(buttonIDs) > 0 {
			if err := tx.Where("biz_type = ? AND biz_id IN ?", "button", buttonIDs).Delete(&db.SystemI18nText{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("biz_type = ? AND biz_id IN ?", "menu", allMenuIDs).Delete(&db.SystemI18nText{}).Error; err != nil {
			return err
		}
		// 深度优先删除子菜单,级联清掉按钮/绑定。
		for i := len(allMenuIDs) - 1; i >= 0; i-- {
			if err := tx.Delete(&db.SystemMenu{}, allMenuIDs[i]).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// MenuButtonUpsertPayload 菜单按钮载荷。
type MenuButtonUpsertPayload struct {
	MenuID         int64            `json:"menuId"`
	Title          string           `json:"title"`
	PermissionCode string           `json:"permissionCode"`
	Sort           int              `json:"sort"`
	RoleCodes      []string         `json:"roleCodes"`
	I18nKey        string           `json:"i18nKey"`
	I18nTexts      I18nTextsPayload `json:"i18nTexts"`
}

// CreateButton 创建菜单按钮。
func (a *App) CreateButton(payload MenuButtonUpsertPayload) (M, error) {
	return a.upsertButton(nil, payload)
}

// UpdateButton 更新菜单按钮。
func (a *App) UpdateButton(buttonID int64, payload MenuButtonUpsertPayload) (M, error) {
	var button db.SystemMenuButton
	if err := a.DB.First(&button, buttonID).Error; err != nil {
		return nil, bizErr("按钮不存在")
	}
	return a.upsertButton(&button, payload)
}

// upsertButton 与 upsertMenu 同理:existing 为 nil 走新增、非 nil 走更新;按钮必须挂在某个已存在的菜单下。
func (a *App) upsertButton(existing *db.SystemMenuButton, payload MenuButtonUpsertPayload) (M, error) {
	var menu db.SystemMenu
	if err := a.DB.First(&menu, payload.MenuID).Error; err != nil {
		return nil, bizErr("菜单不存在")
	}
	permissionCode := strings.TrimSpace(payload.PermissionCode)
	if permissionCode == "" {
		return nil, bizErr("权限编码不能为空")
	}
	var excludeID int64
	if existing != nil {
		excludeID = existing.ID
	}
	var count int64
	a.DB.Model(&db.SystemMenuButton{}).Where("permission_code = ? AND id <> ?", permissionCode, excludeID).Count(&count)
	if count > 0 {
		return nil, bizErr("按钮权限编码已存在")
	}
	if err := a.validateI18nKey(payload.I18nKey, "button", excludeID); err != nil {
		return nil, err
	}
	roleCodes := payload.RoleCodes
	if len(roleCodes) == 0 {
		menuRoleMap := a.listRoleCodesForMenuIDs([]int64{payload.MenuID})
		roleCodes = menuRoleMap[payload.MenuID]
	}
	roles, err := a.resolveRoleCodes(roleCodes, true)
	if err != nil {
		return nil, err
	}
	var buttonID int64
	if existing == nil {
		button := db.SystemMenuButton{
			MenuID: payload.MenuID, Title: strings.TrimSpace(payload.Title),
			PermissionCode: permissionCode, Sort: payload.Sort, CreatedAt: time.Now(),
		}
		if err := a.DB.Create(&button).Error; err != nil {
			return nil, err
		}
		buttonID = button.ID
	} else {
		buttonID = existing.ID
		updates := map[string]any{
			"menu_id": payload.MenuID, "title": strings.TrimSpace(payload.Title),
			"permission_code": permissionCode, "sort": payload.Sort,
		}
		if err := a.DB.Model(existing).Updates(updates).Error; err != nil {
			return nil, err
		}
	}
	if err := a.replaceButtonRoles(buttonID, roles); err != nil {
		return nil, err
	}
	if err := a.upsertI18nPair("button", buttonID, strings.TrimSpace(payload.I18nKey), payload.I18nTexts); err != nil {
		return nil, err
	}
	return M{"id": buttonID}, nil
}

// DeleteButton 删除菜单按钮。
func (a *App) DeleteButton(buttonID int64) error {
	var button db.SystemMenuButton
	if err := a.DB.First(&button, buttonID).Error; err != nil {
		return bizErr("按钮不存在")
	}
	if err := a.DB.Where("biz_type = ? AND biz_id = ?", "button", buttonID).Delete(&db.SystemI18nText{}).Error; err != nil {
		return err
	}
	return a.DB.Delete(&button).Error
}

// ---------- 内部辅助 ----------

// requireMutableRole 取出角色并挡住"系统内置角色":很多改/删操作都先调它做前置校验,
// 返回 (*角色, error) 两个值——拿到角色指针就能继续改,拿到 error 就说明不允许操作。
func (a *App) requireMutableRole(roleID int64) (*db.SystemRole, error) {
	var role db.SystemRole
	if err := a.DB.First(&role, roleID).Error; err != nil {
		return nil, bizErr("角色不存在")
	}
	if role.IsSystem || protectedRoleCodes[role.Code] {
		return nil, bizErr("系统内置角色不允许修改或删除")
	}
	return &role, nil
}

func (a *App) resolveAssignableRoles(roleCodes []string) ([]db.SystemRole, error) {
	roles, err := a.resolveRoleCodes(roleCodes, true)
	if err != nil {
		return nil, err
	}
	for _, role := range roles {
		if role.Code == guestRoleCode {
			return nil, bizErr("游客角色只能用于匿名访问")
		}
	}
	return roles, nil
}

// resolveRoleCodes 把传入的角色编码去空格、去重(用 seen 这个 map 当"集合"过滤重复),
// 再查库确认它们都真实存在且处于启用状态;数量对不上就报错。
func (a *App) resolveRoleCodes(roleCodes []string, required bool) ([]db.SystemRole, error) {
	unique := make([]string, 0, len(roleCodes))
	seen := map[string]bool{}
	for _, code := range roleCodes {
		normalized := strings.TrimSpace(code)
		if normalized == "" || seen[normalized] {
			continue
		}
		seen[normalized] = true
		unique = append(unique, normalized)
	}
	if required && len(unique) == 0 {
		return nil, bizErr("至少选择一个角色")
	}
	if len(unique) == 0 {
		return nil, nil
	}
	var roles []db.SystemRole
	if err := a.DB.Where("code IN ? AND is_enabled = ?", unique, true).Order("id ASC").Find(&roles).Error; err != nil {
		return nil, err
	}
	if len(roles) != len(unique) {
		return nil, bizErr("存在无效的角色")
	}
	return roles, nil
}

// resolveParentMenu 校验父菜单:既不能把自己选成父级,也不能选自己的后代当父级(否则菜单树会成环)。
func (a *App) resolveParentMenu(parentID *int64, editingMenuID int64) (*int64, error) {
	if parentID == nil {
		return nil, nil
	}
	var parent db.SystemMenu
	if err := a.DB.First(&parent, *parentID).Error; err != nil {
		return nil, bizErr("菜单不存在")
	}
	if editingMenuID != 0 && parent.ID == editingMenuID {
		return nil, bizErr("父级菜单不能选择自己")
	}
	// 从候选父节点顺着 ParentID 一路往上"爬"到根:每爬一层就检查是否撞见正在编辑的菜单,
	// 撞见就说明选的是自己的后代,拒绝。current.ParentID != nil 表示还有上一层。
	current := parent
	for current.ParentID != nil {
		if editingMenuID != 0 && *current.ParentID == editingMenuID {
			return nil, bizErr("父级菜单不能选择自己的子菜单")
		}
		var next db.SystemMenu
		if err := a.DB.First(&next, *current.ParentID).Error; err != nil {
			break
		}
		current = next
	}
	return &parent.ID, nil
}

func (a *App) validateI18nKey(i18nKey, bizType string, bizID int64) error {
	normalized := strings.TrimSpace(i18nKey)
	if normalized == "" {
		return bizErr("国际化 Key 不能为空")
	}
	if strings.Contains(normalized, " ") {
		return bizErr("国际化 Key 不能包含空格")
	}
	query := a.DB.Model(&db.SystemI18nText{}).Where("i18n_key = ? AND locale = ?", normalized, "zh")
	if bizID != 0 {
		query = query.Where("NOT (biz_type = ? AND biz_id = ?)", bizType, bizID)
	}
	var count int64
	query.Count(&count)
	if count > 0 {
		return bizErr("国际化 Key 已被其他菜单或按钮占用")
	}
	return nil
}

// upsertI18nPair 对 zh / en 两条国际化文案做 upsert(有则更新、无则插入)。
// range 遍历一个临时 map,一次处理两种语言(遍历顺序不定,但两条互不影响)。
func (a *App) upsertI18nPair(bizType string, bizID int64, i18nKey string, texts I18nTextsPayload) error {
	for locale, text := range map[string]string{"zh": strings.TrimSpace(texts.Zh), "en": strings.TrimSpace(texts.En)} {
		var row db.SystemI18nText
		err := a.DB.Where("biz_type = ? AND biz_id = ? AND locale = ?", bizType, bizID, locale).First(&row).Error
		// 先查这条文案。若返回哨兵错误 gorm.ErrRecordNotFound(表示"没查到")就走 Create 新增;
		// 是别的错误就直接返回;否则说明查到了,走 Updates 更新。用 == 直接比对这个特定错误值。
		if err == gorm.ErrRecordNotFound {
			row = db.SystemI18nText{BizType: bizType, BizID: bizID, I18nKey: i18nKey, Locale: locale, Text: text, UpdatedAt: time.Now()}
			if err := a.DB.Create(&row).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else {
			updates := map[string]any{"i18n_key": i18nKey, "text": text, "updated_at": time.Now()}
			if err := a.DB.Model(&row).Updates(updates).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

// replaceUserRoles 重置用户的角色关联:先按 user_id 删掉旧关联,再逐条插入新的(整体替换)。
// 下面的 replaceMenuRoles / replaceButtonRoles 是同一套路,只是换成了菜单/按钮的关联表。
func (a *App) replaceUserRoles(userID int64, roles []db.SystemRole) error {
	if err := a.DB.Where("user_id = ?", userID).Delete(&db.SystemUserRole{}).Error; err != nil {
		return err
	}
	for _, role := range roles {
		if err := a.DB.Create(&db.SystemUserRole{UserID: userID, RoleID: role.ID, CreatedAt: time.Now()}).Error; err != nil {
			return err
		}
	}
	return nil
}

func (a *App) replaceMenuRoles(menuID int64, roles []db.SystemRole) error {
	if err := a.DB.Where("menu_id = ?", menuID).Delete(&db.SystemRoleMenu{}).Error; err != nil {
		return err
	}
	for _, role := range roles {
		if err := a.DB.Create(&db.SystemRoleMenu{RoleID: role.ID, MenuID: menuID, CreatedAt: time.Now()}).Error; err != nil {
			return err
		}
	}
	return nil
}

func (a *App) replaceButtonRoles(buttonID int64, roles []db.SystemRole) error {
	if err := a.DB.Where("button_id = ?", buttonID).Delete(&db.SystemRoleButton{}).Error; err != nil {
		return err
	}
	for _, role := range roles {
		if err := a.DB.Create(&db.SystemRoleButton{RoleID: role.ID, ButtonID: buttonID, CreatedAt: time.Now()}).Error; err != nil {
			return err
		}
	}
	return nil
}

// listRoleCodesForUsers 一次查出"多个用户各自的角色编码",返回 map[用户ID] -> [角色编码列表]。
// var rows []struct{...} 是"匿名结构体切片":临时定义一个只有 UserID、Code 两个字段的类型来接结果。
// Table/Select/Joins/Scan 是 GORM 更贴近原生 SQL 的写法,Scan 把多行结果扫进 rows;
// 之后按 UserID 归拢进 map(用 append 追加到该用户对应的切片上)。这样避免在循环里逐个查库(N+1)。
func (a *App) listRoleCodesForUsers(userIDs []int64) map[int64][]string {
	result := map[int64][]string{}
	if len(userIDs) == 0 {
		return result
	}
	var rows []struct {
		UserID int64
		Code   string
	}
	a.DB.Table("user_roles").
		Select("user_roles.user_id AS user_id, roles.code AS code").
		Joins("JOIN roles ON user_roles.role_id = roles.id").
		Where("user_roles.user_id IN ?", userIDs).
		Order("roles.id ASC").Scan(&rows)
	for _, row := range rows {
		result[row.UserID] = append(result[row.UserID], row.Code)
	}
	return result
}

func (a *App) listRoleCodesForMenuIDs(menuIDs []int64) map[int64][]string {
	result := map[int64][]string{}
	if len(menuIDs) == 0 {
		return result
	}
	var rows []struct {
		MenuID int64
		Code   string
	}
	a.DB.Table("role_menus").
		Select("role_menus.menu_id AS menu_id, roles.code AS code").
		Joins("JOIN roles ON role_menus.role_id = roles.id").
		Where("role_menus.menu_id IN ?", menuIDs).
		Order("roles.id ASC").Scan(&rows)
	for _, row := range rows {
		result[row.MenuID] = append(result[row.MenuID], row.Code)
	}
	return result
}

func (a *App) listRoleCodesForButtonIDs(buttonIDs []int64) map[int64][]string {
	result := map[int64][]string{}
	if len(buttonIDs) == 0 {
		return result
	}
	var rows []struct {
		ButtonID int64
		Code     string
	}
	a.DB.Table("role_menu_buttons").
		Select("role_menu_buttons.button_id AS button_id, roles.code AS code").
		Joins("JOIN roles ON role_menu_buttons.role_id = roles.id").
		Where("role_menu_buttons.button_id IN ?", buttonIDs).
		Order("roles.id ASC").Scan(&rows)
	for _, row := range rows {
		result[row.ButtonID] = append(result[row.ButtonID], row.Code)
	}
	return result
}

// listI18nByBizIDs 批量取菜单/按钮的国际化文案。返回的 key 用 [2]any{bizType, bizID}:
// 数组(定长、可比较)能直接当 map 的键,于是用"类型 + ID"两段组合精确定位一条文案。
// 下面的 switch { ... }(不带条件)等价于 if/else 链:按传了哪些 id 拼不同的 Where 条件。
func (a *App) listI18nByBizIDs(menuIDs, buttonIDs []int64) map[[2]any]M {
	result := map[[2]any]M{}
	var rows []db.SystemI18nText
	query := a.DB.Model(&db.SystemI18nText{})
	switch {
	case len(menuIDs) > 0 && len(buttonIDs) > 0:
		query = query.Where(
			"(biz_type = ? AND biz_id IN ?) OR (biz_type = ? AND biz_id IN ?)",
			"menu", menuIDs, "button", buttonIDs,
		)
	case len(menuIDs) > 0:
		query = query.Where("biz_type = ? AND biz_id IN ?", "menu", menuIDs)
	case len(buttonIDs) > 0:
		query = query.Where("biz_type = ? AND biz_id IN ?", "button", buttonIDs)
	default:
		return result
	}
	query.Find(&rows)
	for _, row := range rows {
		key := [2]any{row.BizType, row.BizID}
		entry, ok := result[key]
		if !ok {
			entry = M{"i18nKey": row.I18nKey, "i18nTexts": M{"zh": "", "en": ""}}
			result[key] = entry
		}
		if texts, ok := entry["i18nTexts"].(M); ok && (row.Locale == "zh" || row.Locale == "en") {
			texts[row.Locale] = row.Text
		}
	}
	return result
}

// buildMenuTreePayload 构建前端菜单树结构。
// 把"菜单行 + 按钮行"两张平表拼成前端要的层级菜单树,步骤:
//   1) 先批量查出每个菜单/按钮的角色、国际化文案(集中查,避免在循环里逐条查库);
//   2) actionMap:按 menuID 把按钮归到各自的菜单下;
//   3) childrenMap:记录每个菜单有哪些直接子菜单;
//   4) nodeMap:把每个菜单转成一个前端节点(带 meta 元信息);
//   5) 最后按 ParentID 把子节点挂到父节点的 children 上,没有父的就是根节点。
func (a *App) buildMenuTreePayload(menus []db.SystemMenu, buttons []db.SystemMenuButton) []M {
	menuIDs := collectIDs(menus, func(m db.SystemMenu) int64 { return m.ID })
	buttonIDs := collectIDs(buttons, func(b db.SystemMenuButton) int64 { return b.ID })
	menuRoleMap := a.listRoleCodesForMenuIDs(menuIDs)
	buttonRoleMap := a.listRoleCodesForButtonIDs(buttonIDs)
	i18nMap := a.listI18nByBizIDs(menuIDs, buttonIDs)

	actionMap := map[int64][]M{}
	for _, button := range buttons {
		i18nPair := i18nMap[[2]any{"button", button.ID}]
		i18nKey, i18nTexts := extractI18n(i18nPair, button.Title)
		roles := buttonRoleMap[button.ID]
		if roles == nil {
			roles = []string{}
		}
		actionMap[button.MenuID] = append(actionMap[button.MenuID], M{
			"id": button.ID, "title": button.Title, "permissionCode": button.PermissionCode,
			"i18nKey": i18nKey, "i18nTexts": i18nTexts,
			"sort": button.Sort, "roles": roles,
			"updatedAt": fmtTimeV(button.CreatedAt),
		})
	}

	childrenMap := map[int64][]int64{}
	for _, menu := range menus {
		if menu.ParentID != nil {
			childrenMap[*menu.ParentID] = append(childrenMap[*menu.ParentID], menu.ID)
		}
	}

	nodeMap := map[int64]M{}
	for _, menu := range menus {
		i18nPair := i18nMap[[2]any{"menu", menu.ID}]
		i18nKey, i18nTexts := extractI18n(i18nPair, menu.Title)
		roles := menuRoleMap[menu.ID]
		if roles == nil {
			roles = []string{}
		}
		permissionCode := ""
		if menu.PermissionCode != nil {
			permissionCode = *menu.PermissionCode
		}
		meta := M{
			"title": menu.Title, "permissionCode": permissionCode,
			"i18nKey": i18nKey, "i18nTexts": i18nTexts,
			"keepAlive": menu.KeepAlive, "isHide": menu.IsHidden, "isHideTab": menu.IsHideTab,
			"isFullPage": menu.IsFullScreen, "isIframe": menu.UseIframe, "fixedTab": menu.FixedTab,
			"isEnable": menu.IsActive, "sort": menu.Sort, "roles": roles,
		}
		if menu.Icon != "" {
			meta["icon"] = menu.Icon
		}
		if menu.ActiveMenuPath != "" {
			meta["activePath"] = menu.ActiveMenuPath
		}
		if menu.ExternalURL != "" {
			meta["link"] = menu.ExternalURL
		}
		if menu.BadgeLabel != "" {
			meta["showBadge"] = true
			meta["showTextBadge"] = menu.BadgeLabel
		}
		if actions := actionMap[menu.ID]; len(actions) > 0 {
			meta["actionList"] = actions
		}

		component := menu.Component
		if component == "" && menu.ParentID == nil && len(childrenMap[menu.ID]) > 0 && menu.ExternalURL == "" && !menu.UseIframe {
			component = layoutComponent
		}
		var parentID any
		if menu.ParentID != nil {
			parentID = *menu.ParentID
		}
		nodeMap[menu.ID] = M{
			"id": menu.ID, "parentId": parentID, "path": menu.Path, "name": menu.Name,
			"component": component, "updatedAt": fmtTimeV(menu.UpdatedAt),
			"meta": meta, "children": []M{},
		}
	}

	// 组树:遍历所有菜单,若它有 ParentID 且父节点也在本次结果里,就把自己挂到父节点的 children;
	// 否则当作根节点。nodeMap 里存的是 M(map,引用类型),所以往父节点的 children 里 append
	// 改的就是共享的那一份,树的层级关系自然连了起来。
	roots := []M{}
	for _, menu := range menus {
		node := nodeMap[menu.ID]
		if menu.ParentID != nil {
			if parent, ok := nodeMap[*menu.ParentID]; ok {
				parent["children"] = append(parent["children"].([]M), node)
				continue
			}
		}
		roots = append(roots, node)
	}
	for i := range roots {
		roots[i] = pruneEmptyChildren(roots[i])
	}
	return roots
}

// pruneEmptyChildren 递归清理:节点没有子节点时就删掉 children 字段(叶子不带空数组),
// 有子节点则对每个孩子再递归处理一遍。"函数调用自己"就是递归,天然适合处理树这种自相似结构。
func pruneEmptyChildren(node M) M {
	children, ok := node["children"].([]M)
	if !ok {
		return node
	}
	if len(children) == 0 {
		delete(node, "children")
		return node
	}
	for i := range children {
		children[i] = pruneEmptyChildren(children[i])
	}
	node["children"] = children
	return node
}

func extractI18n(pair M, fallbackTitle string) (string, M) {
	if pair == nil {
		return "", M{"zh": fallbackTitle, "en": fallbackTitle}
	}
	key, _ := pair["i18nKey"].(string)
	texts, _ := pair["i18nTexts"].(M)
	if texts == nil {
		texts = M{"zh": fallbackTitle, "en": fallbackTitle}
	}
	return key, texts
}

// serializeUser 把数据库实体转成前端要的 JSON 对象(M);键名用小驼峰以对齐前端字段。
func serializeUser(user *db.SystemUser, roleCodes []string) M {
	if roleCodes == nil {
		roleCodes = []string{}
	}
	return M{
		"id": user.ID, "avatar": user.Avatar, "isActive": user.IsActive,
		"username": user.Username, "gender": user.Gender, "nickname": user.Nickname,
		"fullName": user.FullName, "phone": user.Phone, "email": user.Email,
		"roleCodes": roleCodes,
		"createdBy": user.CreatedBy, "createdAt": fmtTimeV(user.CreatedAt),
		"updatedBy": user.UpdatedBy, "updatedAt": fmtTimeV(user.UpdatedAt),
	}
}

func serializeRole(role *db.SystemRole) M {
	return M{
		"id": role.ID, "displayName": role.DisplayName, "code": role.Code,
		"description": role.Description, "isEnabled": role.IsEnabled, "isSystem": role.IsSystem,
		"createdAt": fmtTimeV(role.CreatedAt), "updatedAt": fmtTimeV(role.UpdatedAt),
	}
}

// normalizeGender 把各种性别写法归一成 male/female/unknown。
// switch 按 value 匹配:Go 的 case 默认不会"穿透"到下一个(不用写 break),命中一个就结束。
func normalizeGender(gender string) string {
	value := strings.ToLower(strings.TrimSpace(gender))
	switch value {
	case "male", "female", "unknown":
		return value
	case "男", "1":
		return "male"
	case "女", "2":
		return "female"
	default:
		return "unknown"
	}
}

func roleCodesOf(roles []db.SystemRole) []string {
	codes := make([]string, 0, len(roles))
	for _, role := range roles {
		codes = append(codes, role.Code)
	}
	return codes
}

func dedupeInt64(items []int64) []int64 {
	seen := map[int64]bool{}
	result := make([]int64, 0, len(items))
	for _, item := range items {
		if seen[item] {
			continue
		}
		seen[item] = true
		result = append(result, item)
	}
	return result
}

// collectIDs 是泛型工具:[T any] 表示对任意元素类型 T 都能用;第二个参数 id 是"函数作为参数"——
// 调用方传一个"怎么从元素取出 int64 ID"的小函数进来。返回所有元素的 ID 组成的切片。见 GO入门笔记『泛型』。
func collectIDs[T any](items []T, id func(T) int64) []int64 {
	result := make([]int64, 0, len(items))
	for _, item := range items {
		result = append(result, id(item))
	}
	return result
}
