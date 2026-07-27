// Package api HTTP 路由与中间件。响应契约与原 FastAPI 后端保持一致。
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"coinsphere/backend/internal/service"
)

// M JSON 对象别名。
type M = map[string]any

// Server HTTP 服务。
type Server struct {
	App        *service.App
	StaticDir  string
	UploadsDir string
}

// NewServer 创建服务并注册全部路由。
func NewServer(app *service.App, staticDir, uploadsDir string) *http.ServeMux {
	s := &Server{App: app, StaticDir: staticDir, UploadsDir: uploadsDir}
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	return mux
}

// ---------- 统一响应 ----------

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(payload)
}

func ok(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, M{"code": 200, "msg": "success", "data": data})
}

func okMsg(w http.ResponseWriter, data any, msg string) {
	writeJSON(w, http.StatusOK, M{"code": 200, "msg": msg, "data": data})
}

func fail(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusOK, M{"code": 400, "msg": msg, "data": nil})
}

// failStatus FastAPI HTTPException 等价物:非 200 状态 + detail。
func failStatus(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, M{"code": status, "msg": detail, "detail": detail, "data": nil})
}

// respond 业务结果统一出口:权限错误 403,其余业务错误 code=400。
func respond(w http.ResponseWriter, data any, err error, successMsg string) {
	if err != nil {
		if errors.Is(err, service.ErrPermission) {
			failStatus(w, http.StatusForbidden, err.Error())
			return
		}
		fail(w, err.Error())
		return
	}
	if successMsg == "" {
		ok(w, data)
		return
	}
	okMsg(w, data, successMsg)
}

// ---------- 请求解析 ----------

func decodeBody[T any](r *http.Request) (*T, error) {
	var payload T
	if r.Body == nil {
		return &payload, nil
	}
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&payload); err != nil {
		return nil, errors.New("请求体解析失败: " + err.Error())
	}
	return &payload, nil
}

func queryInt(r *http.Request, name string, fallback int) int {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func queryStr(r *http.Request, name string) string {
	return strings.TrimSpace(r.URL.Query().Get(name))
}

func queryInt64Ptr(r *http.Request, name string) *int64 {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return nil
	}
	return &value
}

func queryBoolPtr(r *http.Request, name string) *bool {
	raw := strings.ToLower(strings.TrimSpace(r.URL.Query().Get(name)))
	if raw == "" {
		return nil
	}
	value := raw == "true" || raw == "1"
	return &value
}

func pathInt64(r *http.Request, name string) (int64, error) {
	value, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || value <= 0 {
		return 0, errors.New("路径参数不合法: " + name)
	}
	return value, nil
}

func clampSize(size, max int) int {
	if size > max {
		return max
	}
	return size
}

// ---------- 认证中间件 ----------

type authedHandler func(w http.ResponseWriter, r *http.Request, principal *service.Principal)

func extractBearerToken(r *http.Request) string {
	raw := strings.TrimSpace(r.Header.Get("Authorization"))
	if raw == "" {
		return ""
	}
	if !strings.HasPrefix(strings.ToLower(raw), "bearer ") {
		return ""
	}
	return strings.TrimSpace(raw[7:])
}

// requireAuth 必须登录。
func (s *Server) requireAuth(next authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := extractBearerToken(r)
		if token == "" {
			failStatus(w, http.StatusUnauthorized, "Missing authorization header")
			return
		}
		principal, err := s.App.AuthenticateAccessToken(token)
		if err != nil {
			failStatus(w, http.StatusUnauthorized, err.Error())
			return
		}
		next(w, r, principal)
	}
}

// requireGuestOrAuth 允许游客访问。
func (s *Server) requireGuestOrAuth(next authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := extractBearerToken(r)
		if token == "" {
			principal, err := s.App.BuildGuestPrincipal()
			if err != nil {
				failStatus(w, http.StatusUnauthorized, err.Error())
				return
			}
			next(w, r, principal)
			return
		}
		principal, err := s.App.AuthenticateAccessToken(token)
		if err != nil {
			failStatus(w, http.StatusUnauthorized, err.Error())
			return
		}
		next(w, r, principal)
	}
}

// requirePermission 必须持有权限码。
func (s *Server) requirePermission(code string, next authedHandler) http.HandlerFunc {
	return s.requireAuth(func(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
		if !principal.HasPermission(code) {
			failStatus(w, http.StatusForbidden, "无权访问")
			return
		}
		next(w, r, principal)
	})
}
