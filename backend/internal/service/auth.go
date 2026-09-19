package service

import (
	"sort"
	"strings"
	"time"

	"coinsphere/backend/internal/db"
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
	AccessMode      string // authenticated
	AccessTokenID   string
	AccessTokenExp  time.Time
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

// guestRoleCode remains a seed identifier for existing role data; no HTTP route
// constructs a guest principal after the authentication boundary migration.
const guestRoleCode = "R_GUEST"

// AuthSession 是登录后签发的短期 access-token 会话。
type AuthSession struct {
	UserID      int64
	AccessToken string
}

// Login 校验凭据并签发 access token。
func (a *App) Login(username, password string, keepLoggedIn bool) (*AuthSession, error) {
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
	accessToken := a.Tokens.CreateAccessToken(user.ID, keepLoggedIn)
	now := time.Now()
	a.DB.Model(&db.SystemUser{}).Where("id = ?", user.ID).
		Updates(map[string]any{"last_login_at": now, "updated_at": now})
	return &AuthSession{
		UserID: user.ID, AccessToken: accessToken.Value,
	}, nil
}

// AuthenticateAccessToken 校验 access token 并组装权限上下文。
// 每个需要登录的接口都会先走这里:验 access 令牌拿到 userID,再交给 buildPrincipal 查出
// 用户 + 角色 + 权限,组装成 *Principal 供后续鉴权。
func (a *App) AuthenticateAccessToken(rawToken string) (*Principal, error) {
	payload, err := a.Tokens.VerifyAccessToken(rawToken)
	if err != nil {
		return nil, err
	}
	if a.isAccessTokenRevoked(payload.TokenID) {
		return nil, security.ErrInvalidToken
	}
	principal, err := a.buildPrincipal(payload.UserID)
	if err != nil {
		return nil, err
	}
	principal.AccessTokenID = payload.TokenID
	principal.AccessTokenExp = payload.ExpiresAt
	return principal, nil
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

// BuildUserInfo 序列化当前用户信息。
func (a *App) BuildUserInfo(principal *Principal) M {
	permissions := make([]string, 0, len(principal.PermissionCodes))
	// for code := range map 只遍历 map 的"键"(这里我们只要权限码本身,不要值)。
	// map 的遍历顺序是随机的,所以下面用 sortStrings 排一下,保证每次返回的顺序稳定。
	for code := range principal.PermissionCodes {
		permissions = append(permissions, code)
	}
	sort.Strings(permissions)
	username := principal.User.Username
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

type reauthTokenRecord struct {
	userID        int64
	accessTokenID string
	expiresAt     time.Time
}

const reauthTokenTTL = 5 * time.Minute

// Reauthenticate verifies the current password before issuing a one-time token
// bound to the exact access-token session.
func (a *App) Reauthenticate(principal *Principal, password string) (string, error) {
	if principal == nil || principal.User == nil || principal.User.ID <= 0 ||
		principal.AccessTokenID == "" || strings.TrimSpace(password) == "" {
		return "", security.ErrInvalidToken
	}

	var user db.SystemUser
	if err := a.DB.First(&user, principal.User.ID).Error; err != nil || !user.IsActive ||
		!a.Hasher.VerifyPassword(password, user.PasswordHash) {
		return "", security.ErrInvalidToken
	}
	return a.issueReauthToken(principal, time.Now()), nil
}

func (a *App) issueReauthToken(principal *Principal, now time.Time) string {
	raw := security.RandomURLSafe(32)
	hash := security.HashToken(raw)
	a.authStateMu.Lock()
	a.pruneAuthStateLocked(now)
	a.reauthTokens[hash] = reauthTokenRecord{
		userID: principal.User.ID, accessTokenID: principal.AccessTokenID,
		expiresAt: now.Add(reauthTokenTTL),
	}
	a.authStateMu.Unlock()
	return raw
}

// ConsumeReauthToken permits exactly one matching user/session use. A token
// presented by another user or session remains unusable and cannot be consumed.
func (a *App) ConsumeReauthToken(raw string, principal *Principal) bool {
	if principal == nil || principal.User == nil || principal.User.ID <= 0 || principal.AccessTokenID == "" || raw == "" {
		return false
	}
	hash := security.HashToken(raw)
	now := time.Now()
	a.authStateMu.Lock()
	defer a.authStateMu.Unlock()
	a.pruneAuthStateLocked(now)
	record, ok := a.reauthTokens[hash]
	if !ok || record.userID != principal.User.ID || record.accessTokenID != principal.AccessTokenID {
		return false
	}
	delete(a.reauthTokens, hash)
	return true
}

// LogoutAccessToken revokes the current signed token until its natural expiry.
// ponytail: process-local revocation is sufficient for the current single-app topology; use a shared store only with multiple API instances.
func (a *App) LogoutAccessToken(principal *Principal) {
	if principal == nil || principal.AccessTokenID == "" || principal.AccessTokenExp.IsZero() {
		return
	}
	a.authStateMu.Lock()
	a.pruneAuthStateLocked(time.Now())
	a.revokedAccessTokens[principal.AccessTokenID] = principal.AccessTokenExp
	a.authStateMu.Unlock()
}

func (a *App) isAccessTokenRevoked(tokenID string) bool {
	if tokenID == "" {
		return true
	}
	a.authStateMu.Lock()
	defer a.authStateMu.Unlock()
	a.pruneAuthStateLocked(time.Now())
	_, revoked := a.revokedAccessTokens[tokenID]
	return revoked
}

func (a *App) pruneAuthStateLocked(now time.Time) {
	for hash, record := range a.reauthTokens {
		if !record.expiresAt.After(now) {
			delete(a.reauthTokens, hash)
		}
	}
	for tokenID, expiresAt := range a.revokedAccessTokens {
		if !expiresAt.After(now) {
			delete(a.revokedAccessTokens, tokenID)
		}
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
