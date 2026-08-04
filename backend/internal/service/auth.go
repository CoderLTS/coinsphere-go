package service

import (
	"errors"
	"time"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/perm"
	"coinsphere/backend/internal/security"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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

// AuthSession 只在 API 边界内短暂持有 Refresh Token；响应体不得直接序列化该结构。
type AuthSession struct {
	UserID           int64
	AccessToken      string
	RefreshToken     string
	RefreshExpiresAt time.Time
}

// Login 校验凭据并创建可轮换会话。
func (a *App) Login(username, password string) (*AuthSession, error) {
	var user db.SystemUser
	lookupErr := a.DB.Where("username = ?", username).First(&user).Error
	hashToCheck := a.dummyHash
	if lookupErr == nil {
		hashToCheck = user.PasswordHash
	}
	passwordOK := a.Hasher.VerifyPassword(password, hashToCheck)
	if lookupErr != nil || !user.IsActive || !passwordOK {
		return nil, bizErr("用户名或密码错误")
	}
	accessToken := a.Tokens.CreateAccessToken(user.ID)
	refreshToken := a.Tokens.CreateRefreshToken(user.ID)
	now := time.Now()
	record := db.RefreshTokenRecord{
		ID: refreshToken.TokenID, UserID: user.ID,
		TokenHash: security.HashToken(refreshToken.Value),
		ExpiresAt: refreshToken.ExpiresAt, CreatedAt: now,
	}
	if err := a.DB.Create(&record).Error; err != nil {
		return nil, err
	}
	a.DB.Model(&db.SystemUser{}).Where("id = ?", user.ID).
		Updates(map[string]any{"last_login_at": now, "updated_at": now})
	return &AuthSession{
		UserID:      user.ID,
		AccessToken: accessToken.Value, RefreshToken: refreshToken.Value,
		RefreshExpiresAt: refreshToken.ExpiresAt,
	}, nil
}

// RefreshAccessToken 在同一事务中锁定、吊销旧令牌并写入新令牌，阻止并发复用。
func (a *App) RefreshAccessToken(refreshToken string) (*AuthSession, error) {
	payload, err := a.Tokens.VerifyToken(refreshToken, "refresh")
	if err != nil {
		return nil, err
	}
	newRefresh := a.Tokens.CreateRefreshToken(payload.UserID)
	reused := false
	err = a.DB.Transaction(func(tx *gorm.DB) error {
		var record db.RefreshTokenRecord
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", payload.TokenID).First(&record).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return security.ErrInvalidToken
		}
		if err != nil {
			return err
		}
		if record.UserID != payload.UserID {
			return security.ErrInvalidToken
		}
		if record.IsRevoked {
			reused = true
			return tx.Model(&db.RefreshTokenRecord{}).
				Where("user_id = ?", record.UserID).Update("is_revoked", true).Error
		}
		if !record.ExpiresAt.After(time.Now()) ||
			!security.SecureCompare(record.TokenHash, security.HashToken(refreshToken)) {
			return security.ErrInvalidToken
		}
		now := time.Now()
		newRecord := db.RefreshTokenRecord{
			ID: newRefresh.TokenID, UserID: payload.UserID,
			TokenHash: security.HashToken(newRefresh.Value),
			ExpiresAt: newRefresh.ExpiresAt, CreatedAt: now,
		}
		if err := tx.Create(&newRecord).Error; err != nil {
			return err
		}
		result := tx.Model(&db.RefreshTokenRecord{}).
			Where("id = ? AND is_revoked = ?", record.ID, false).
			Update("is_revoked", true)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return security.ErrInvalidToken
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if reused {
		return nil, security.ErrInvalidToken
	}
	accessToken := a.Tokens.CreateAccessToken(payload.UserID)
	return &AuthSession{
		UserID:      payload.UserID,
		AccessToken: accessToken.Value, RefreshToken: newRefresh.Value,
		RefreshExpiresAt: newRefresh.ExpiresAt,
	}, nil
}

// Logout 主动吊销 Refresh Token；无效令牌仍返回成功，避免泄露会话状态。
func (a *App) Logout(refreshToken string) (int64, error) {
	payload, err := a.Tokens.VerifyToken(refreshToken, "refresh")
	if err != nil {
		return 0, nil
	}
	err = a.DB.Model(&db.RefreshTokenRecord{}).
		Where("id = ?", payload.TokenID).Update("is_revoked", true).Error
	return payload.UserID, err
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
//
//	SELECT roles.* FROM roles
//	JOIN user_roles ON user_roles.role_id = roles.id
//	WHERE user_roles.user_id = ? AND roles.is_enabled = ? ORDER BY roles.id ASC
//
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
