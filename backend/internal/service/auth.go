package service

import (
	"time"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/perm"
	"coinsphere/backend/internal/security"
)

// Principal 当前请求主体(登录用户或游客)。
//
// Principal 把"当前是谁 + 他能干什么"打包在一起,几乎每个接口都会先构造它再做鉴权。
// PermissionCodes 用 map[string]bool 当"集合"来使:判断有没有某个权限码时直接查 key,速度是 O(1)。
// []int64 / []string 是切片(可变长数组)。见 GO入门笔记『复合类型』。
type Principal struct {
	User            *db.SystemUser
	RoleIDs         []int64
	RoleCodes       []string
	PermissionCodes map[string]bool
	AccessMode      string // authenticated | guest
}

// HasPermission 判断是否拥有权限码。
// (p *Principal) 叫"接收者":它把这个函数挂成 Principal 的方法,函数内用 p 指代当前对象
// (相当于别的语言的 this / self),调用时写 principal.HasPermission("x")。见 GO入门笔记『方法与接收者』。
// 查 map 里不存在的 key 会返回该类型的零值(bool 的零值是 false),所以这里天然表示"没有该权限"。
func (p *Principal) HasPermission(code string) bool { return p.PermissionCodes[code] }

// HasRole 判断是否拥有角色编码。
func (p *Principal) HasRole(code string) bool {
	// for _, item := range 遍历切片:_ 丢弃下标,只取值 item;找到相等的就提前返回 true。
	for _, item := range p.RoleCodes {
		if item == code {
			return true
		}
	}
	return false
}

const guestRoleCode = "R_GUEST"

// Login 用户名密码登录,返回 access/refresh token。
//
// 登录主流程:① 按用户名查用户 → ② 校验账号已启用且密码正确 → ③ 签发 access / refresh 两个令牌
// → ④ 把 refresh 令牌落库(便于以后校验/吊销)→ ⑤ 记录最后登录时间 → ⑥ 返回两个令牌。
// (a *App) 表示这是 App 的方法,通过 a 拿到共享的 DB、Hasher、Tokens 等依赖。
func (a *App) Login(username, password string) (M, error) {
	// var user ... 先声明一个"零值"结构体,用来接收查询结果。
	// GORM:.Where("username = ?", username) 里的 ? 是占位符(值单独传、自动转义,能防 SQL 注入);
	// .First(&user) 相当于 SELECT ... WHERE username=? LIMIT 1,并把结果写回 user;查不到时 .Error 非空。
	// 见 GO入门笔记『框架:GORM』。
	var user db.SystemUser
	if err := a.DB.Where("username = ?", username).First(&user).Error; err != nil {
		return nil, bizErr("用户名或密码错误")
	}
	// ! 是逻辑取反;VerifyPassword 用数据库里存的哈希来校验用户输入的明文密码。
	// 注意:"用户不存在"和"密码错误"故意返回同一句提示,不让攻击者判断到底哪一步错了。
	if !user.IsActive || !a.Hasher.VerifyPassword(password, user.PasswordHash) {
		return nil, bizErr("用户名或密码错误")
	}
	accessToken := a.Tokens.CreateAccessToken(user.ID)
	refreshToken := a.Tokens.CreateRefreshToken(user.ID)

	// 把 refresh token 存进数据库,但只存它的哈希(HashToken)而非明文——即使数据库泄露也拿不到原始令牌。
	// .Save(&record) 会写入这条记录(INSERT/更新)。
	record := db.RefreshTokenRecord{
		ID: refreshToken.TokenID, UserID: user.ID,
		TokenHash: security.HashToken(refreshToken.Value),
		ExpiresAt: refreshToken.ExpiresAt, CreatedAt: time.Now(),
	}
	if err := a.DB.Save(&record).Error; err != nil {
		return nil, err
	}
	now := time.Now()
	// 更新最后登录时间:.Model(...).Where("id = ?").Updates(map) 等价 UPDATE users SET ... WHERE id=?,
	// 只更新 map 里列出的这几列。最后返回 M{...} 拼成 JSON 响应体,nil 表示没有错误。
	a.DB.Model(&db.SystemUser{}).Where("id = ?", user.ID).
		Updates(map[string]any{"last_login_at": now, "updated_at": now})
	return M{"token": accessToken.Value, "refreshToken": refreshToken.Value}, nil
}

// RefreshAccessToken 用 refresh token 换取新的 access token。
//
// 流程:先验 refresh 令牌的签名 → 查库确认这条记录未被吊销 → 再校验它没过期、且哈希对得上,
// 全部通过才签发新的 access 令牌。这样 access 令牌可以很短命,过期后不必让用户重新输密码。
func (a *App) RefreshAccessToken(refreshToken string) (M, error) {
	payload, err := a.Tokens.VerifyToken(refreshToken, "refresh")
	if err != nil {
		return nil, err
	}
	var record db.RefreshTokenRecord
	if err := a.DB.Where("id = ? AND is_revoked = ?", payload.TokenID, false).First(&record).Error; err != nil {
		return nil, security.ErrInvalidToken
	}
	// ExpiresAt.After(now) 判断令牌是否还没过期;|| 两侧任一条件不满足(已过期 或 哈希对不上)都视为无效。
	if !record.ExpiresAt.After(time.Now()) || record.TokenHash != security.HashToken(refreshToken) {
		return nil, security.ErrInvalidToken
	}
	accessToken := a.Tokens.CreateAccessToken(payload.UserID)
	return M{"token": accessToken.Value}, nil
}

// AuthenticateAccessToken 校验 access token 并组装权限上下文。
// 每个需要登录的接口都会先走这里:验 access 令牌拿到 userID,再交给 buildPrincipal 查出
// 用户 + 角色 + 权限,组装成 *Principal 供后续鉴权。
func (a *App) AuthenticateAccessToken(rawToken string) (*Principal, error) {
	payload, err := a.Tokens.VerifyToken(rawToken, "access")
	if err != nil {
		return nil, err
	}
	return a.buildPrincipal(payload.UserID)
}

// buildPrincipal 小写开头 = 包内私有(只能在本包调用)。按 userID 查出用户,再查他的角色、
// 汇总权限码,组装成一个 *Principal 返回。
func (a *App) buildPrincipal(userID int64) (*Principal, error) {
	var user db.SystemUser
	// .First(&user, userID) = 按主键查一条(SELECT ... WHERE id=userID LIMIT 1);
	// 查不到(err != nil)或账号已停用,都视为无效令牌。
	if err := a.DB.First(&user, userID).Error; err != nil || !user.IsActive {
		return nil, security.ErrInvalidToken
	}
	roles, err := a.listRolesForUser(userID)
	if err != nil {
		return nil, err
	}
	// make([]T, 0, n):建一个长度 0、预留容量 n 的切片;下面用 append 往里追加元素,
	// 预留容量能避免追加时反复扩容、复制。这里同时收集角色 ID 和角色编码两个列表。
	roleIDs := make([]int64, 0, len(roles))
	roleCodes := make([]string, 0, len(roles))
	for _, role := range roles {
		roleIDs = append(roleIDs, role.ID)
		roleCodes = append(roleCodes, role.Code)
	}
	// 用结构体字面量组装并返回指针 &Principal{...};User 存的是 &user(用户的地址)。
	// PermissionCodes 交给下面的方法按这些角色查出"权限码集合"。
	return &Principal{
		User: &user, RoleIDs: roleIDs, RoleCodes: roleCodes,
		PermissionCodes: a.listPermissionCodesForRoleIDs(roleIDs),
		AccessMode:      "authenticated",
	}, nil
}

// BuildGuestPrincipal 游客访问上下文。
// 造一个"游客"身份:先从库里查出启用的游客角色,再手工拼一个只存在内存里的匿名用户
// (不写库,ID=0),让未登录的人也能以受限权限浏览首页。
func (a *App) BuildGuestPrincipal() (*Principal, error) {
	var guestRole db.SystemRole
	if err := a.DB.Where("code = ? AND is_enabled = ?", guestRoleCode, true).First(&guestRole).Error; err != nil {
		return nil, security.ErrInvalidToken
	}
	now := time.Now()
	guestUser := &db.SystemUser{
		ID: 0, Username: "guest", Nickname: "游客", FullName: "游客",
		Gender: "unknown", IsActive: true, Company: "coinsphere",
		Bio: "游客默认以匿名方式访问首页。", TagsJSON: "[]",
		CreatedBy: "system", UpdatedBy: "system", CreatedAt: now, UpdatedAt: now,
	}
	return &Principal{
		User: guestUser, RoleIDs: []int64{guestRole.ID}, RoleCodes: []string{guestRole.Code},
		PermissionCodes: map[string]bool{}, AccessMode: "guest",
	}, nil
}

// BuildUserInfo 序列化当前用户信息。
func (a *App) BuildUserInfo(principal *Principal) M {
	permissions := make([]string, 0, len(principal.PermissionCodes))
	// for code := range map 只遍历 map 的"键"(这里我们只要权限码本身,不要值)。
	// map 的遍历顺序是随机的,所以下面用 sortStrings 排一下,保证每次返回的顺序稳定。
	for code := range principal.PermissionCodes {
		permissions = append(permissions, code)
	}
	sortStrings(permissions)
	username := principal.User.Username
	if principal.AccessMode == "guest" {
		username = "guest"
	}
	return M{
		"permissions": permissions,
		"roleCodes":   principal.RoleCodes,
		"userId":      principal.User.ID,
		"username":    username,
		"email":       principal.User.Email,
		"avatar":      principal.User.Avatar,
		"accessMode":  principal.AccessMode,
	}
}

// listRolesForUser 查某用户的所有"启用中"角色。GORM 的链式调用等价于这段 SQL:
//   SELECT roles.* FROM roles
//   JOIN user_roles ON user_roles.role_id = roles.id
//   WHERE user_roles.user_id = ? AND roles.is_enabled = ? ORDER BY roles.id ASC
// .Find(&roles) 把查到的多行结果写回切片。见 GO入门笔记『框架:GORM』。
func (a *App) listRolesForUser(userID int64) ([]db.SystemRole, error) {
	var roles []db.SystemRole
	err := a.DB.
		Joins("JOIN user_roles ON user_roles.role_id = roles.id").
		Where("user_roles.user_id = ? AND roles.is_enabled = ?", userID, true).
		Order("roles.id ASC").
		Find(&roles).Error
	return roles, err
}

// listPermissionCodesForRoleIDs 把这些角色能碰到的"菜单权限码 + 按钮权限码"汇总成一个集合。
// GORM 要点:Where("... IN ?", roleIDs) 传一个切片,会展开成 SQL 的 IN (...);Distinct 去重;
// Pluck("列名", &切片) 只取某一列的值填进切片。最后用 map[string]bool 去重合并成权限集合返回。
func (a *App) listPermissionCodesForRoleIDs(roleIDs []int64) map[string]bool {
	result := map[string]bool{}
	if len(roleIDs) == 0 {
		return result
	}
	var menuCodes []string
	a.DB.Model(&db.SystemMenu{}).Distinct("menus.permission_code").
		Joins("JOIN role_menus ON role_menus.menu_id = menus.id").
		Where("role_menus.role_id IN ? AND menus.permission_code IS NOT NULL AND menus.permission_code <> ''", roleIDs).
		Pluck("menus.permission_code", &menuCodes)
	var buttonCodes []string
	a.DB.Model(&db.SystemMenuButton{}).Distinct("menu_buttons.permission_code").
		Joins("JOIN role_menu_buttons ON role_menu_buttons.button_id = menu_buttons.id").
		Where("role_menu_buttons.role_id IN ?", roleIDs).
		Pluck("menu_buttons.permission_code", &buttonCodes)
	for _, code := range menuCodes {
		if code != "" {
			result[code] = true
		}
	}
	for _, code := range buttonCodes {
		if code != "" {
			result[code] = true
		}
	}
	return result
}

// agentAccessAllowed 判断主体是否可使用指定智能体。
func agentAccessAllowed(principal *Principal, agentCode string) bool {
	required := perm.AssistantAgentRequiredPermission[agentCode]
	if required == "" {
		return true
	}
	return principal.HasPermission(required)
}

// sortStrings 是就地插入排序(标准库有 sort.Strings,这里手写一份以免多引一个包)。
// a, b = b, a 是 Go 的多值赋值:一行交换两个元素,不需要临时变量。
// 切片传进来的是"底层数组的视图",所以这里的重排会直接影响调用方那个切片。
func sortStrings(items []string) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j] < items[j-1]; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}
