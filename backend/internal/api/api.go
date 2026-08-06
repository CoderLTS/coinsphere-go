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
	"net/http"
	"strconv"
	"strings"
	"time"

	"coinsphere/backend/internal/service"
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
	StaticDir    string
	UploadsDir   string
	loginLimiter *rateLimiter // 登录限流(见评审 #6)
	metrics      *httpMetrics
}

// NewServer 是本项目约定的"构造函数":新建 Server,把所有 URL 与处理函数登记到路由表,返回给 main 启动 HTTP 服务。
// 返回 http.Handler，使全部路由统一经过可观测性中间件。
// NewServer 创建服务并注册全部路由。
func NewServer(app *service.App, staticDir, uploadsDir string) http.Handler {
	// &Server{...} 新建 Server 并用 & 取地址得到指针;:= 是函数内的短变量声明,自动推断类型(见 GO入门笔记『变量声明』)。
	s := &Server{
		App:          app,
		StaticDir:    staticDir,
		UploadsDir:   uploadsDir,
		loginLimiter: newRateLimiter(app.Cfg.Auth.LoginRateLimitPerMinute),
		metrics:      &httpMetrics{startedAt: time.Now()},
	}
	// http.NewServeMux() 创建标准库的路由表(见 GO入门笔记『框架:net/http』)。
	mux := http.NewServeMux()
	// s.registerRoutes(mux):用 . 调用 s 上的方法;s 是指针,Go 会自动解引用。
	s.registerRoutes(mux)
	return s.observe(mux)
}

// ---------- 统一响应 ----------

// writeJSON 把任意数据编码成 JSON 写回客户端,是所有响应的底层出口。
// 参数 w http.ResponseWriter 是"用来写响应的对象",几乎每个处理函数都靠它输出(见 GO入门笔记『框架:net/http』)。
func writeJSON(w http.ResponseWriter, status int, payload any) {
	if object, ok := payload.(map[string]any); ok {
		if code, ok := object["code"].(int); ok && code >= http.StatusBadRequest {
			detail, _ := object["detail"].(string)
			if detail == "" {
				detail, _ = object["msg"].(string)
			}
			writeProblemResponse(w, statusForProblem(status, code), detail, "")
			return
		}
	}
	// 顺序不能乱:先设响应头,再写状态码,最后写 body。WriteHeader 一旦调用,响应头就发出去了。
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	// _ = ... 表示故意丢弃返回值(这里丢弃 Encode 的 error),见 GO入门笔记『其它会撞见的小语法』。
	_ = encoder.Encode(payload)
}

// ok 返回成功响应。成功响应沿用 {code, msg, data} 信封。
// M{...} 是上面定义的 map 别名,这里现场拼出一个 JSON 对象。
func ok(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, M{"code": 200, "msg": "success", "data": data})
}

func okMsg(w http.ResponseWriter, data any, msg string) {
	writeJSON(w, http.StatusOK, M{"code": 200, "msg": msg, "data": data})
}

// fail 返回 HTTP 400 Problem Details。
func fail(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusBadRequest, M{"code": http.StatusBadRequest, "msg": msg})
}

// failStatus 返回指定状态的 Problem Details。
func failStatus(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, M{"code": status, "detail": detail})
}

func writeProblem(w http.ResponseWriter, r *http.Request, status int, detail string) {
	requestID := ""
	if state, ok := r.Context().Value(requestStateKey{}).(*requestState); ok {
		requestID = state.requestID
	}
	if requestID == "" {
		requestID = incomingRequestID(r.Header.Get(requestIDHeader))
	}
	writeProblemResponse(w, status, detail, requestID)
}

func writeProblemResponse(w http.ResponseWriter, status int, detail, requestID string) {
	if requestID == "" {
		requestID = incomingRequestID(w.Header().Get(requestIDHeader))
	}
	markResponseFailed(w)
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
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
func respond(w http.ResponseWriter, data any, err error, successMsg string) {
	if err != nil {
		// errors.Is 判断 err 是否就是(或包裹了)指定的哨兵错误 ErrPermission;是权限问题就回 403。
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

// decodeBody 是泛型函数:[T any] 表示"对任意类型 T";调用处写 decodeBody[LoginRequest](r),T 就变成 LoginRequest(见 GO入门笔记『泛型』)。
// 作用:把请求体里的 JSON 解析成一个 *T(指向 T 的指针)。返回两个值:结果指针 + error。
func decodeBody[T any](r *http.Request) (*T, error) {
	// var payload T 声明一个 T 类型的零值变量。
	var payload T
	if r.Body == nil {
		// &payload 取它的地址返回;nil 表示"没有错误"(见 GO入门笔记『错误处理』)。
		return &payload, nil
	}
	decoder := json.NewDecoder(r.Body)
	// if err := f(); err != nil {} 是常见写法:调用、接住 err、当场判断,err 只在这个 if 内可见。
	if err := decoder.Decode(&payload); err != nil {
		return nil, errors.New("请求体解析失败: " + err.Error())
	}
	return &payload, nil
}

func queryStr(r *http.Request, name string) string {
	return strings.TrimSpace(r.URL.Query().Get(name))
}

// queryInt64Ptr 返回 *int64(指针):没传该参数时返回 nil,以此区分"没填"和"填了 0"。这是 Go 表示可选值的常用手法。
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

// pathInt64 取 URL 路径参数并转成 int64。r.PathValue("userId") 对应路由里 {userId} 那一段(见 GO入门笔记『框架:net/http』)。
// 返回 (int64, error):调用处先判断 err,不合法就直接回错。
func pathInt64(r *http.Request, name string) (int64, error) {
	value, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || value <= 0 {
		return 0, errors.New("路径参数不合法: " + name)
	}
	return value, nil
}

func queryCursorPage(r *http.Request) (service.CursorPage, error) {
	limit := 50
	if raw := queryStr(r, "limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 200 {
			return service.CursorPage{}, errors.New("limit must be between 1 and 200")
		}
		limit = value
	}
	values := r.URL.Query()
	values.Del("cursor")
	values.Del("limit")
	pattern := r.Pattern
	if pattern == "" {
		pattern = r.Method + " " + r.URL.Path
	}
	scope := fmt.Sprintf("%x", sha256.Sum256([]byte(pattern+"?"+values.Encode())))
	return service.ParseCursorPage(queryStr(r, "cursor"), limit, scope)
}

func cursorPage(w http.ResponseWriter, r *http.Request) (service.CursorPage, bool) {
	page, err := queryCursorPage(r)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return service.CursorPage{}, false
	}
	return page, true
}

// ---------- 认证中间件 ----------

// authedHandler 是一个"函数类型":凡是签名为 (w, r, principal) 的函数都算这种类型。
// 它比标准处理函数多一个 principal(当前登录用户),鉴权中间件校验通过后会把它传进来。
// 函数在 Go 里是"一等公民",可以像值一样作为参数传递、作为返回值返回(见下面的中间件)。
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

// requireAuth 是"中间件":接收一个处理函数 next,返回一个包了鉴权逻辑的新处理函数——"包一层"就是中间件模式。
// 新函数先取 token 并验证,通过才调用 next(w, r, principal),否则直接回 401。于是各业务接口自己不用再写鉴权。
// return func(w, r){...} 返回的是匿名函数(闭包):它"记住"了外层的 s 和 next,每次请求都能用。
// (s *Server) 是方法接收者:表示这是 Server 的方法,方法内用 s 指代当前对象(见 GO入门笔记『方法与接收者』)。
// 返回类型 http.HandlerFunc 是标准库的处理函数类型,可直接登记到路由表(见 GO入门笔记『框架:net/http』)。
// requireAuth 必须登录。
func (s *Server) requireAuth(next authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := extractBearerToken(r)
		if token == "" {
			writeProblem(w, r, http.StatusUnauthorized, "missing authorization header")
			return
		}
		// 一次接住两个返回值:principal 是解析出的用户,err 是错误。这就是 Go 的多返回值(见 GO入门笔记『函数 & 多返回值』)。
		principal, err := s.App.AuthenticateAccessToken(token)
		if err != nil {
			writeProblem(w, r, http.StatusUnauthorized, "invalid access token")
			return
		}
		setAuditActor(r, principal.User.ID)
		next(w, r, principal)
	}
}

// requirePermission 在 requireAuth 之上再包一层:先要求登录,再检查用户是否持有权限码 code,没有就回 403。
// 它把"检查权限再调 next"做成一个函数,当作 next 传给 requireAuth —— 中间件可以这样层层嵌套组合。
// code 形如 "system:users:view",是前后端约定的权限标识;principal.HasPermission(code) 判断当前用户有没有它。
// requirePermission 必须持有权限码。
func (s *Server) requirePermission(code string, next authedHandler) http.HandlerFunc {
	return s.requireAuth(func(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
		if !principal.HasPermission(code) {
			writeProblem(w, r, http.StatusForbidden, "permission denied")
			return
		}
		next(w, r, principal)
	})
}
