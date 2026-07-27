package service

import (
	"time"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/perm"
	"coinsphere/backend/internal/security"
)

// Principal 当前请求主体(登录用户或游客)。
type Principal struct {
	User            *db.SystemUser
	RoleIDs         []int64
	RoleCodes       []string
	PermissionCodes map[string]bool
	AccessMode      string // authenticated | guest
}

// HasPermission 判断是否拥有权限码。
func (p *Principal) HasPermission(code string) bool { return p.PermissionCodes[code] }

// HasRole 判断是否拥有角色编码。
func (p *Principal) HasRole(code string) bool {
	for _, item := range p.RoleCodes {
		if item == code {
			return true
		}
	}
	return false
}

const guestRoleCode = "R_GUEST"

// Login 用户名密码登录,返回 access/refresh token。
func (a *App) Login(username, password string) (M, error) {
	var user db.SystemUser
	if err := a.DB.Where("username = ?", username).First(&user).Error; err != nil {
		return nil, bizErr("用户名或密码错误")
	}
	if !user.IsActive || !a.Hasher.VerifyPassword(password, user.PasswordHash) {
		return nil, bizErr("用户名或密码错误")
	}
	accessToken := a.Tokens.CreateAccessToken(user.ID)
	refreshToken := a.Tokens.CreateRefreshToken(user.ID)

	record := db.RefreshTokenRecord{
		ID: refreshToken.TokenID, UserID: user.ID,
		TokenHash: security.HashToken(refreshToken.Value),
		ExpiresAt: refreshToken.ExpiresAt, CreatedAt: time.Now(),
	}
	if err := a.DB.Save(&record).Error; err != nil {
		return nil, err
	}
	now := time.Now()
	a.DB.Model(&db.SystemUser{}).Where("id = ?", user.ID).
		Updates(map[string]any{"last_login_at": now, "updated_at": now})
	return M{"token": accessToken.Value, "refreshToken": refreshToken.Value}, nil
}

// RefreshAccessToken 用 refresh token 换取新的 access token。
func (a *App) RefreshAccessToken(refreshToken string) (M, error) {
	payload, err := a.Tokens.VerifyToken(refreshToken, "refresh")
	if err != nil {
		return nil, err
	}
	var record db.RefreshTokenRecord
	if err := a.DB.Where("id = ? AND is_revoked = ?", payload.TokenID, false).First(&record).Error; err != nil {
		return nil, security.ErrInvalidToken
	}
	if !record.ExpiresAt.After(time.Now()) || record.TokenHash != security.HashToken(refreshToken) {
		return nil, security.ErrInvalidToken
	}
	accessToken := a.Tokens.CreateAccessToken(payload.UserID)
	return M{"token": accessToken.Value}, nil
}

// AuthenticateAccessToken 校验 access token 并组装权限上下文。
func (a *App) AuthenticateAccessToken(rawToken string) (*Principal, error) {
	payload, err := a.Tokens.VerifyToken(rawToken, "access")
	if err != nil {
		return nil, err
	}
	return a.buildPrincipal(payload.UserID)
}

func (a *App) buildPrincipal(userID int64) (*Principal, error) {
	var user db.SystemUser
	if err := a.DB.First(&user, userID).Error; err != nil || !user.IsActive {
		return nil, security.ErrInvalidToken
	}
	roles, err := a.listRolesForUser(userID)
	if err != nil {
		return nil, err
	}
	roleIDs := make([]int64, 0, len(roles))
	roleCodes := make([]string, 0, len(roles))
	for _, role := range roles {
		roleIDs = append(roleIDs, role.ID)
		roleCodes = append(roleCodes, role.Code)
	}
	return &Principal{
		User: &user, RoleIDs: roleIDs, RoleCodes: roleCodes,
		PermissionCodes: a.listPermissionCodesForRoleIDs(roleIDs),
		AccessMode:      "authenticated",
	}, nil
}

// BuildGuestPrincipal 游客访问上下文。
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

func (a *App) listRolesForUser(userID int64) ([]db.SystemRole, error) {
	var roles []db.SystemRole
	err := a.DB.
		Joins("JOIN user_roles ON user_roles.role_id = roles.id").
		Where("user_roles.user_id = ? AND roles.is_enabled = ?", userID, true).
		Order("roles.id ASC").
		Find(&roles).Error
	return roles, err
}

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

func sortStrings(items []string) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j] < items[j-1]; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}
