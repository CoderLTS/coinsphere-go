package api

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"coinsphere/backend/internal/service"
)

// ---------- 认证 ----------

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	payload, err := decodeBody[struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}](r)
	if err != nil {
		fail(w, err.Error())
		return
	}
	if payload.Username == "" || payload.Password == "" {
		fail(w, "用户名或密码错误")
		return
	}
	data, err := s.App.Login(payload.Username, payload.Password)
	respond(w, data, err, "")
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	payload, err := decodeBody[struct {
		RefreshToken string `json:"refreshToken"`
	}](r)
	if err != nil {
		writeJSON(w, http.StatusOK, M{"code": 401, "msg": err.Error(), "data": nil})
		return
	}
	data, err := s.App.RefreshAccessToken(payload.RefreshToken)
	if err != nil {
		writeJSON(w, http.StatusOK, M{"code": 401, "msg": err.Error(), "data": nil})
		return
	}
	ok(w, data)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	ok(w, s.App.BuildUserInfo(principal))
}

// ---------- 新闻数据 ----------

func (s *Server) handleListNews(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	current := queryInt(r, "current", 1)
	size := clampSize(queryInt(r, "size", 10), 100)
	data, err := s.App.ListNews(current, size, queryStr(r, "keyword"))
	respond(w, data, err, "")
}

func (s *Server) handleCreateNews(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	payload, err := decodeBody[service.NewsUpsertPayload](r)
	if err != nil {
		fail(w, err.Error())
		return
	}
	data, err := s.App.CreateNews(*payload)
	respond(w, data, err, "新闻创建成功")
}

func (s *Server) handleUpdateNews(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	newsID, err := pathInt64(r, "newsId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	payload, err := decodeBody[service.NewsUpsertPayload](r)
	if err != nil {
		fail(w, err.Error())
		return
	}
	data, err := s.App.UpdateNews(newsID, *payload)
	respond(w, data, err, "新闻更新成功")
}

func (s *Server) handleDeleteNews(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	newsID, err := pathInt64(r, "newsId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	respond(w, nil, s.App.DeleteNews(newsID), "新闻删除成功")
}

func (s *Server) handleListPushDeliveries(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	query := service.DeliveryHistoryQuery{
		Current:              queryInt(r, "current", 1),
		Size:                 clampSize(queryInt(r, "size", 10), 100),
		Keyword:              queryStr(r, "keyword"),
		WorkflowDefinitionID: queryInt64Ptr(r, "workflowDefinitionId"),
		ChannelType:          queryStr(r, "channelType"),
		DeliveryStatus:       queryStr(r, "deliveryStatus"),
	}
	data, err := s.App.ListDeliveryHistory(principal, query)
	respond(w, data, err, "")
}

// ---------- 系统管理 ----------

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	query := service.UserListQuery{
		Current:  queryInt(r, "current", 1),
		Size:     clampSize(queryInt(r, "size", 20), 100),
		ID:       queryInt64Ptr(r, "id"),
		Username: queryStr(r, "username"),
		Gender:   queryStr(r, "gender"),
		Phone:    queryStr(r, "phone"),
		Email:    queryStr(r, "email"),
		IsActive: queryBoolPtr(r, "isActive"),
	}
	data, err := s.App.ListUsers(query)
	respond(w, data, err, "")
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	payload, err := decodeBody[service.UserUpsertPayload](r)
	if err != nil {
		fail(w, err.Error())
		return
	}
	data, err := s.App.CreateUser(*payload, principal)
	respond(w, data, err, "用户创建成功")
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	userID, err := pathInt64(r, "userId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	payload, err := decodeBody[service.UserUpsertPayload](r)
	if err != nil {
		fail(w, err.Error())
		return
	}
	data, err := s.App.UpdateUser(userID, *payload, principal)
	respond(w, data, err, "用户更新成功")
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	userID, err := pathInt64(r, "userId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	respond(w, nil, s.App.DeleteUser(userID, principal), "用户删除成功")
}

var allowedAvatarTypes = map[string]string{
	"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp", "image/gif": ".gif",
}

func (s *Server) handleUploadAvatar(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		fail(w, "上传内容解析失败")
		return
	}
	file, header, err := r.FormFile("avatar")
	if err != nil {
		fail(w, "缺少 avatar 文件")
		return
	}
	defer file.Close()

	suffix := strings.ToLower(filepath.Ext(header.Filename))
	if normalized, okType := allowedAvatarTypes[header.Header.Get("Content-Type")]; okType {
		suffix = normalized
	}
	if suffix == ".jpeg" {
		suffix = ".jpg"
	}
	validSuffix := map[string]bool{".jpg": true, ".png": true, ".webp": true, ".gif": true}
	if !validSuffix[suffix] {
		fail(w, "Only jpg/png/webp/gif avatars are supported")
		return
	}
	fileBytes, err := io.ReadAll(io.LimitReader(file, 2*1024*1024+1))
	if err != nil || len(fileBytes) == 0 {
		fail(w, "Uploaded file cannot be empty")
		return
	}
	if len(fileBytes) > 2*1024*1024 {
		fail(w, "Avatar image must be 2MB or smaller")
		return
	}
	randomBytes := make([]byte, 16)
	_, _ = rand.Read(randomBytes)
	fileName := hex.EncodeToString(randomBytes) + suffix
	avatarDir := filepath.Join(s.UploadsDir, "avatars")
	_ = os.MkdirAll(avatarDir, 0o755)
	if err := os.WriteFile(filepath.Join(avatarDir, fileName), fileBytes, 0o644); err != nil {
		fail(w, "保存头像失败: "+err.Error())
		return
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	baseURL := scheme + "://" + r.Host
	okMsg(w, M{"url": baseURL + "/uploads/avatars/" + fileName}, "头像上传成功")
}

func (s *Server) handleListRoles(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	query := service.RoleListQuery{
		Current:     queryInt(r, "current", 1),
		Size:        clampSize(queryInt(r, "size", 20), 100),
		ID:          queryInt64Ptr(r, "id"),
		DisplayName: queryStr(r, "displayName"),
		Code:        queryStr(r, "code"),
		Description: queryStr(r, "description"),
		IsEnabled:   queryBoolPtr(r, "isEnabled"),
	}
	data, err := s.App.ListRoles(query)
	respond(w, data, err, "")
}

func (s *Server) handleCreateRole(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	payload, err := decodeBody[service.RoleUpsertPayload](r)
	if err != nil {
		fail(w, err.Error())
		return
	}
	data, err := s.App.CreateRole(*payload)
	respond(w, data, err, "角色创建成功")
}

func (s *Server) handleUpdateRole(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	roleID, err := pathInt64(r, "roleId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	payload, err := decodeBody[service.RoleUpsertPayload](r)
	if err != nil {
		fail(w, err.Error())
		return
	}
	data, err := s.App.UpdateRole(roleID, *payload)
	respond(w, data, err, "角色更新成功")
}

func (s *Server) handleDeleteRole(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	roleID, err := pathInt64(r, "roleId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	respond(w, nil, s.App.DeleteRole(roleID), "角色删除成功")
}

func (s *Server) handleSaveRolePermissions(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	roleID, err := pathInt64(r, "roleId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	payload, err := decodeBody[service.RolePermissionPayload](r)
	if err != nil {
		fail(w, err.Error())
		return
	}
	respond(w, nil, s.App.SaveRolePermissions(roleID, *payload), "角色权限保存成功")
}

func (s *Server) handleGetMenus(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	data, err := s.App.GetMenuTree(principal)
	respond(w, data, err, "")
}

func (s *Server) handleGetManageMenus(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	data, err := s.App.GetMenuManagementTree()
	respond(w, data, err, "")
}

func (s *Server) handleGetI18nDict(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	scope := queryStr(r, "scope")
	if scope == "" {
		scope = "menu"
	}
	ok(w, s.App.GetI18nDict(scope))
}

func (s *Server) handleCreateMenu(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	payload, err := decodeBody[service.MenuUpsertPayload](r)
	if err != nil {
		fail(w, err.Error())
		return
	}
	data, err := s.App.CreateMenu(*payload)
	respond(w, data, err, "菜单创建成功")
}

func (s *Server) handleUpdateMenu(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	menuID, err := pathInt64(r, "menuId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	payload, err := decodeBody[service.MenuUpsertPayload](r)
	if err != nil {
		fail(w, err.Error())
		return
	}
	data, err := s.App.UpdateMenu(menuID, *payload)
	respond(w, data, err, "菜单更新成功")
}

func (s *Server) handleDeleteMenu(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	menuID, err := pathInt64(r, "menuId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	respond(w, nil, s.App.DeleteMenu(menuID), "菜单删除成功")
}

func (s *Server) handleCreateMenuButton(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	payload, err := decodeBody[service.MenuButtonUpsertPayload](r)
	if err != nil {
		fail(w, err.Error())
		return
	}
	data, err := s.App.CreateButton(*payload)
	respond(w, data, err, "菜单按钮创建成功")
}

func (s *Server) handleUpdateMenuButton(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	buttonID, err := pathInt64(r, "buttonId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	payload, err := decodeBody[service.MenuButtonUpsertPayload](r)
	if err != nil {
		fail(w, err.Error())
		return
	}
	data, err := s.App.UpdateButton(buttonID, *payload)
	respond(w, data, err, "菜单按钮更新成功")
}

func (s *Server) handleDeleteMenuButton(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	buttonID, err := pathInt64(r, "buttonId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	respond(w, nil, s.App.DeleteButton(buttonID), "菜单按钮删除成功")
}
