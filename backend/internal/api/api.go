// 本文件是 HTTP 接口层的地基:统一响应格式、请求参数解析、登录/权限中间件。新手可对照 GO入门笔记.md 阅读。
// package:同一文件夹下所有 .go 文件都写 package api,包内函数/变量互相直接可见(见 GO入门笔记『项目怎么组织』)。
// internal/ 是 Go 的特殊约定:里面的包只能被本项目引用,用来放内部实现,外部项目无法 import。
// Package api HTTP 路由与中间件。
package api

// import:声明本文件用到的外部包。第一组是 Go 自带的标准库,空行后一组是本项目内部包(见 GO入门笔记『项目怎么组织』)。
import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"coinsphere/backend/internal/service"
	"github.com/gin-gonic/gin"
)

// map[键类型]值类型 是 Go 的字典;any 表示"任意类型"(见 GO入门笔记『复合类型』)。
// type X = Y 是"类型别名",下面的 M 就完全等价于 map[string]any,只是写起来更短、语义更清楚。

// M JSON 对象别名。
type M = map[string]any

// struct(结构体)把一组字段打包成一个类型,类似别的语言的对象/类(见 GO入门笔记『复合类型』)。
// 字段 App 的类型是 *service.App —— 开头的 * 表示"指针",即指向 service.App 的地址而非整份拷贝(见 GO入门笔记『指针』)。
// 字段名首字母大写(App/StaticDir)= 导出,能被别的包访问;小写则只在本包可见。

// Server HTTP 服务。
type Server struct {
	App          *service.App
	WebDir       string
	StaticDir    string
	UploadsDir   string
	loginLimiter *rateLimiter // 登录限流(见评审 #6)
	metrics      *httpMetrics
}

// NewServer 是本项目约定的"构造函数":新建 Server,把所有 URL 与处理函数登记到路由表,返回给 main 启动 HTTP 服务。
// 返回 http.Handler，使全部路由统一经过可观测性中间件。
// NewServer 创建服务并注册全部路由。
func NewServer(app *service.App, webDir, staticDir, uploadsDir string) http.Handler {
	// &Server{...} 新建 Server 并用 & 取地址得到指针;:= 是函数内的短变量声明,自动推断类型(见 GO入门笔记『变量声明』)。
	s := &Server{
		App:          app,
		WebDir:       webDir,
		StaticDir:    staticDir,
		UploadsDir:   uploadsDir,
		loginLimiter: newRateLimiter(app.Cfg.Auth.LoginRateLimitPerMinute),
		metrics:      &httpMetrics{startedAt: time.Now()},
	}
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.RedirectTrailingSlash = false
	router.RedirectFixedPath = false
	router.HandleMethodNotAllowed = true
	_ = router.SetTrustedProxies(nil)
	router.Use(s.observe(), recoverHTTP())
	router.NoRoute(func(c *gin.Context) {
		if (c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead) &&
			(c.Request.URL.Path == "/static" || c.Request.URL.Path == "/uploads") {
			location := c.Request.URL.Path + "/"
			if c.Request.URL.RawQuery != "" {
				location += "?" + c.Request.URL.RawQuery
			}
			c.Redirect(http.StatusMovedPermanently, location)
			return
		}
		path := c.Request.URL.Path
		if (c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead) &&
			path != "/api" && !strings.HasPrefix(path, "/api/") &&
			path != "/health" && !strings.HasPrefix(path, "/health/") &&
			path != "/metrics" && !strings.HasPrefix(path, "/metrics/") &&
			s.serveWeb(c) {
			return
		}
		writeProblem(c, http.StatusNotFound, http.StatusText(http.StatusNotFound))
	})
	router.NoMethod(func(c *gin.Context) {
		writeProblem(c, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
	})
	s.registerRoutes(router)
	return router
}

const webContentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self' ws://%s wss://%s https://api.iconify.design https://api.unisvg.com https://api.simplesvg.com; media-src 'self' blob:; worker-src 'self' blob:; object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'; frame-src 'self'"

func (s *Server) serveWeb(c *gin.Context) bool {
	name := strings.TrimPrefix(c.Request.URL.Path, "/")
	if name != "" && s.serveWebFile(c, name, name == "index.html") {
		return true
	}
	return s.serveWebFile(c, "index.html", true)
}

func (s *Server) serveWebFile(c *gin.Context, name string, index bool) bool {
	if s.WebDir == "" {
		return false
	}
	file, err := http.Dir(s.WebDir).Open(name)
	if err != nil {
		return false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		return false
	}
	c.Header("Content-Security-Policy", fmt.Sprintf(webContentSecurityPolicy, c.Request.Host, c.Request.Host))
	if index {
		c.Header("Cache-Control", "public, no-transform, max-age=0, must-revalidate")
	}
	http.ServeContent(c.Writer, c.Request, info.Name(), info.ModTime(), file)
	return true
}

// ---------- 统一响应 ----------

// writeJSON 把任意数据编码成 JSON 写回客户端,是所有响应的底层出口。
func writeJSON(c *gin.Context, status int, payload any) {
	if object, ok := payload.(map[string]any); ok {
		if code, ok := object["code"].(int); ok && code >= http.StatusBadRequest {
			detail, _ := object["detail"].(string)
			if detail == "" {
				detail, _ = object["msg"].(string)
			}
			writeProblemResponse(c, statusForProblem(status, code), detail)
			return
		}
	}
	// 顺序不能乱:先设响应头,再写状态码,最后写 body。WriteHeader 一旦调用,响应头就发出去了。
	c.Header("Content-Type", "application/json; charset=utf-8")
	c.Status(status)
	encoder := json.NewEncoder(c.Writer)
	encoder.SetEscapeHTML(false)
	// _ = ... 表示故意丢弃返回值(这里丢弃 Encode 的 error),见 GO入门笔记『其它会撞见的小语法』。
	_ = encoder.Encode(payload)
}

// ok 返回成功响应。成功响应沿用 {code, msg, data} 信封。
// M{...} 是上面定义的 map 别名,这里现场拼出一个 JSON 对象。
func ok(c *gin.Context, data any) {
	writeJSON(c, http.StatusOK, M{"code": 200, "msg": "success", "data": data})
}

func okMsg(c *gin.Context, data any, msg string) {
	writeJSON(c, http.StatusOK, M{"code": 200, "msg": msg, "data": data})
}

// fail 返回 HTTP 400 Problem Details。
func fail(c *gin.Context, msg string) {
	writeJSON(c, http.StatusBadRequest, M{"code": http.StatusBadRequest, "msg": msg})
}

// failStatus 返回指定状态的 Problem Details。
func failStatus(c *gin.Context, status int, detail string) {
	writeJSON(c, status, M{"code": status, "detail": detail})
}

func writeProblem(c *gin.Context, status int, detail string) {
	writeProblemResponse(c, status, detail)
}

func writeProblemResponse(c *gin.Context, status int, detail string) {
	requestID := requestIDFrom(c)
	if requestID == "" {
		requestID = incomingRequestID(c.GetHeader(requestIDHeader))
	}
	c.Set(responseFailedContextKey, true)
	c.Header("Content-Type", "application/problem+json")
	c.Status(status)
	encoder := json.NewEncoder(c.Writer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(M{
		"type":      "about:blank",
		"title":     http.StatusText(status),
		"status":    status,
		"detail":    detail,
		"requestId": requestID,
	})
}

func statusForProblem(status, code int) int {
	if status >= http.StatusBadRequest {
		return status
	}
	if code >= http.StatusBadRequest && code <= 599 {
		return code
	}
	return http.StatusBadRequest
}

// respond 业务结果统一出口:多数处理函数拿到 (data, err) 后直接交给它,由它决定成功/失败怎么回。
// Go 不用异常,而是把"出错了吗"作为最后一个返回值 err 传出来;err != nil 即代表出错(见 GO入门笔记『错误处理』)。
// respond 业务结果统一出口:权限错误 403,其余业务错误 400。
func respond(c *gin.Context, data any, err error, successMsg string) {
	if err != nil {
		// errors.Is 判断 err 是否就是(或包裹了)指定的哨兵错误 ErrPermission;是权限问题就回 403。
		if errors.Is(err, service.ErrPermission) {
			failStatus(c, http.StatusForbidden, err.Error())
			return
		}
		if errors.Is(err, service.ErrNotFound) {
			failStatus(c, http.StatusNotFound, err.Error())
			return
		}
		if errors.Is(err, service.ErrConflict) {
			failStatus(c, http.StatusConflict, strings.TrimPrefix(err.Error(), service.ErrConflict.Error()+": "))
			return
		}
		fail(c, err.Error())
		return
	}
	if successMsg == "" {
		ok(c, data)
		return
	}
	okMsg(c, data, successMsg)
}

// ---------- 请求解析 ----------

// decodeBody 是泛型函数:[T any] 表示"对任意类型 T"。
// 作用:把请求体里的 JSON 解析成一个 *T(指向 T 的指针)。返回两个值:结果指针 + error。
func decodeBody[T any](c *gin.Context) (*T, error) {
	// var payload T 声明一个 T 类型的零值变量。
	var payload T
	if c.Request.Body == nil {
		// &payload 取它的地址返回;nil 表示"没有错误"(见 GO入门笔记『错误处理』)。
		return &payload, nil
	}
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	// if err := f(); err != nil {} 是常见写法:调用、接住 err、当场判断,err 只在这个 if 内可见。
	if err := decoder.Decode(&payload); err != nil {
		return nil, errors.New("请求体解析失败: " + err.Error())
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("请求体只能包含一个 JSON 对象")
	}
	return &payload, nil
}

func queryStr(c *gin.Context, name string) string {
	return strings.TrimSpace(c.Query(name))
}

// queryInt64Ptr 返回 *int64(指针):没传该参数时返回 nil,以此区分"没填"和"填了 0"。这是 Go 表示可选值的常用手法。
func queryInt64Ptr(c *gin.Context, name string) *int64 {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return nil
	}
	return &value
}

func queryBoolPtr(c *gin.Context, name string) *bool {
	raw := strings.ToLower(strings.TrimSpace(c.Query(name)))
	if raw == "" {
		return nil
	}
	value := raw == "true" || raw == "1"
	return &value
}

// pathInt64 取 Gin URL 路径参数并转成 int64。
// 返回 (int64, error):调用处先判断 err,不合法就直接回错。
func pathInt64(c *gin.Context, name string) (int64, error) {
	value, err := strconv.ParseInt(c.Param(name), 10, 64)
	if err != nil || value <= 0 {
		return 0, errors.New("路径参数不合法: " + name)
	}
	return value, nil
}

func queryCursorPage(c *gin.Context) (service.CursorPage, error) {
	limit := 50
	if raw := queryStr(c, "limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 200 {
			return service.CursorPage{}, errors.New("limit must be between 1 and 200")
		}
		limit = value
	}
	values := c.Request.URL.Query()
	values.Del("cursor")
	values.Del("limit")
	pattern := routePattern(c)
	if pattern == "" {
		pattern = c.Request.Method + " " + c.Request.URL.Path
	}
	scope := fmt.Sprintf("%x", sha256.Sum256([]byte(pattern+"?"+values.Encode())))
	return service.ParseCursorPage(queryStr(c, "cursor"), limit, scope)
}

func cursorPage(c *gin.Context) (service.CursorPage, bool) {
	page, err := queryCursorPage(c)
	if err != nil {
		writeProblem(c, http.StatusBadRequest, err.Error())
		return service.CursorPage{}, false
	}
	return page, true
}

// ---------- 认证中间件 ----------

const principalContextKey = "principal"

func extractBearerToken(c *gin.Context) string {
	raw := strings.TrimSpace(c.GetHeader("Authorization"))
	if raw == "" {
		return ""
	}
	if !strings.HasPrefix(strings.ToLower(raw), "bearer ") {
		return ""
	}
	return strings.TrimSpace(raw[7:])
}

// requireAuth 必须登录。
func (s *Server) requireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := extractBearerToken(c)
		if token == "" {
			writeProblem(c, http.StatusUnauthorized, "missing authorization header")
			c.Abort()
			return
		}
		// 一次接住两个返回值:principal 是解析出的用户,err 是错误。这就是 Go 的多返回值(见 GO入门笔记『函数 & 多返回值』)。
		principal, err := s.App.AuthenticateAccessToken(token)
		if err != nil {
			writeProblem(c, http.StatusUnauthorized, "invalid access token")
			c.Abort()
			return
		}
		c.Set(principalContextKey, principal)
		setAuditActor(c, principal.User.ID)
		c.Next()
	}
}

func currentPrincipal(c *gin.Context) *service.Principal {
	principal, _ := c.Get(principalContextKey)
	value, _ := principal.(*service.Principal)
	return value
}

// code 形如 "system:users:view",是前后端约定的权限标识;principal.HasPermission(code) 判断当前用户有没有它。
// requirePermission 必须持有权限码。
func (s *Server) requirePermission(code string) gin.HandlerFunc {
	return func(c *gin.Context) {
		principal := currentPrincipal(c)
		if principal == nil || !principal.HasPermission(code) {
			writeProblem(c, http.StatusForbidden, "permission denied")
			c.Abort()
			return
		}
		c.Next()
	}
}

func (s *Server) requireRole(code string) gin.HandlerFunc {
	return func(c *gin.Context) {
		principal := currentPrincipal(c)
		if principal == nil || !principal.HasRole(code) {
			writeProblem(c, http.StatusForbidden, "permission denied")
			c.Abort()
			return
		}
		c.Next()
	}
}

func recoverHTTP() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if recover() == nil {
				return
			}
			c.Set(responseFailedContextKey, true)
			slog.ErrorContext(c.Request.Context(), "http panic recovered", "component", "http", "error_category", "panic")
			if !c.Writer.Written() {
				writeProblem(c, http.StatusInternalServerError, "internal server error")
			}
			c.Abort()
		}()
		c.Next()
	}
}
