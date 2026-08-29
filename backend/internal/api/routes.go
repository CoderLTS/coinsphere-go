package api

// import:标准库(net/http、os、path/filepath)在上,本项目内部包(perm 权限码、service 业务逻辑)在下。
import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"

	"coinsphere/backend/internal/perm"
	"coinsphere/backend/internal/service"
	"coinsphere/backend/plugin/sdk"
)

// registerRoutes 把每个 URL 登记到路由表 mux。理解一行就理解全部:
//
//	mux.HandleFunc("方法 /路径", 处理函数) —— 例如 "GET /api/v1/..."。这是 Go 1.22+ net/http 的新路由写法(见 GO入门笔记『框架:net/http』)。
//	路径里的 {definitionId} 之类是"路径参数",处理函数用 r.PathValue("definitionId") 取出。
//	处理函数外面套一层 s.requireAuth / s.requirePermission(...),就是给这个接口加上"登录/权限"检查(中间件,见 api.go)。
//
// registerRoutes 只暴露 V2 基线仍保留的认证、监控与系统管理边界。
func (s *Server) registerRoutes(mux *http.ServeMux) {
	// 处理函数的固定签名是 func(w http.ResponseWriter, r *http.Request);这里直接写了一个匿名函数当处理器。
	// /health 保留为数据库就绪别名，供既有 Compose 与发布脚本平滑切换。
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		s.handleReady(w, r)
	})
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, M{"status": "alive"})
	})
	mux.HandleFunc("GET /health/ready", s.handleReady)
	mux.HandleFunc("GET /metrics", s.requireAuth(func(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
		s.handleMetrics(w, r)
	}))

	// 静态目录。
	// os.MkdirAll 建目录(已存在也不报错);0o755 是八进制的目录权限;开头的 _ = 忽略返回的 error。
	_ = os.MkdirAll(s.StaticDir, 0o755)
	_ = os.MkdirAll(filepath.Join(s.UploadsDir, "avatars"), 0o755)
	// mux.Handle 与 HandleFunc 类似,但收的是 http.Handler 对象;FileServer 把磁盘目录当静态资源伺服,StripPrefix 先去掉 URL 前缀再找文件。
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir(s.StaticDir))))
	mux.Handle("GET /uploads/", http.StripPrefix("/uploads/", http.FileServer(http.Dir(s.UploadsDir))))

	// 认证。
	// 登录套一层 s.rateLimit(...) 限流，登出撤销当前 access-token 会话。
	mux.HandleFunc("POST /api/v1/auth/login", s.rateLimit(s.handleLogin))
	mux.HandleFunc("POST /api/v1/auth/logout", s.requireAuth(s.handleLogout))
	mux.HandleFunc("POST /api/v1/auth/reauth", s.requireAuth(s.handleReauth))
	mux.HandleFunc("GET /api/v1/me", s.requireAuth(s.handleMe))

	// 首页。
	mux.HandleFunc("GET /api/v1/home/meta", s.requireAuth(func(w http.ResponseWriter, r *http.Request, p *service.Principal) {
		ok(w, s.App.GetHomeMeta())
	}))
	mux.HandleFunc("GET /api/v1/home/overview", s.requireAuth(func(w http.ResponseWriter, r *http.Request, p *service.Principal) {
		data, err := s.App.GetHomeOverview(r.Context())
		if err == nil {
			for key, value := range s.metrics.snapshot() {
				data[key] = value
			}
		}
		respond(w, data, err, "")
	}))

	// P2 workflow execution, events, and Schema-driven workbench.
	mux.HandleFunc("POST /api/v1/webhooks/{workflowId}", s.handlePublishWorkflowWebhook)
	mux.HandleFunc("GET /api/v1/workflows/templates", s.requireRole("R_SUPER", s.handleListWorkflowTemplates))
	mux.HandleFunc("POST /api/v1/events", s.requireRole("R_SUPER", s.handlePublishWorkflowEvent))
	mux.HandleFunc("GET /api/v1/human-tasks", s.requireRole("R_SUPER", s.handleListWorkflowHumanTasks))
	mux.HandleFunc("POST /api/v1/human-tasks/{taskId}", s.requireRole("R_SUPER", s.handleDecideWorkflowHumanTask))
	mux.HandleFunc("GET /api/v1/workflows/node-definitions", s.requireRole("R_SUPER", s.handleListWorkflowNodeDefinitions))
	mux.HandleFunc("POST /api/v1/workflows/validate", s.requireRole("R_SUPER", s.handleValidateWorkflowGraph))
	mux.HandleFunc("GET /api/v1/workflows", s.requireRole("R_SUPER", s.handleListWorkflows))
	mux.HandleFunc("POST /api/v1/workflows", s.requireRole("R_SUPER", s.handleCreateWorkflow))
	mux.HandleFunc("GET /api/v1/workflows/{workflowId}", s.requireRole("R_SUPER", s.handleGetWorkflow))
	mux.HandleFunc("PATCH /api/v1/workflows/{workflowId}", s.requireRole("R_SUPER", s.handleUpdateWorkflow))
	mux.HandleFunc("GET /api/v1/workflows/{workflowId}/revisions", s.requireRole("R_SUPER", s.handleListWorkflowRevisions))
	mux.HandleFunc("POST /api/v1/workflows/{workflowId}/revisions", s.requireRole("R_SUPER", s.handleSaveWorkflowRevision))
	mux.HandleFunc("GET /api/v1/workflows/{workflowId}/revisions/{revisionId}", s.requireRole("R_SUPER", s.handleGetWorkflowRevision))
	mux.HandleFunc("POST /api/v1/workflows/{workflowId}/lifecycle", s.requireRole("R_SUPER", s.handleWorkflowLifecycle))
	mux.HandleFunc("GET /api/v1/workflows/{workflowId}/runs", s.requireRole("R_SUPER", s.handleListWorkflowRuns))
	mux.HandleFunc("POST /api/v1/workflows/{workflowId}/runs", s.requireRole("R_SUPER", s.handleCreateWorkflowRun))
	mux.HandleFunc("GET /api/v1/ws/workflows/{workflowId}/runs", s.handleWorkflowRunsWebSocket)
	mux.HandleFunc("GET /api/v1/notification-deliveries", s.requireAuth(s.handleListInAppNotifications))
	mux.HandleFunc("POST /api/v1/notification-deliveries/{deliveryId}/read", s.requireAuth(s.handleReadInAppNotification))
	mux.HandleFunc("POST /api/v1/notification-deliveries/read-all", s.requireAuth(s.handleReadAllInAppNotifications))
	mux.HandleFunc("GET /api/v1/ws/notifications", s.handleNotificationWebSocket)
	mux.HandleFunc("GET /api/v1/workflow-runs/{runId}", s.requireRole("R_SUPER", s.handleGetWorkflowRun))
	mux.HandleFunc("POST /api/v1/workflow-runs/{runId}", s.requireRole("R_SUPER", s.handleWorkflowRunAction))
	mux.HandleFunc("GET /api/v1/artifacts/{sha256}/manifest", s.requireRole("R_SUPER", s.handleGetWorkflowArtifactManifest))
	mux.HandleFunc("GET /api/v1/artifacts/{sha256}/download", s.requireRole("R_SUPER", s.handleDownloadWorkflowArtifact))
	mux.HandleFunc("GET /api/v1/result-views", s.requirePermission(perm.ResultViewsAccess, s.handleListResultViews))
	mux.HandleFunc("POST /api/v1/result-views", s.requireRole("R_SUPER", s.handleCreateResultView))
	mux.HandleFunc("GET /api/v1/result-views/{viewId}", s.requirePermission(perm.ResultViewsAccess, s.handleGetResultView))
	mux.HandleFunc("PUT /api/v1/result-views/{viewId}/grants", s.requireRole("R_SUPER", s.handleReplaceResultViewGrants))
	mux.HandleFunc("POST /api/v1/result-views/{viewId}/revoke", s.requireRole("R_SUPER", s.handleRevokeResultView))
	mux.HandleFunc("GET /api/v1/result-views/{viewId}/runs", s.requireAuth(s.handleListResultViewRuns))
	mux.HandleFunc("POST /api/v1/result-views/{viewId}/runs/{runId}/{action}", s.requireAuth(s.handleResultViewRunAction))
	mux.HandleFunc("POST /api/v1/result-views/{viewId}/workflow/pause", s.requireAuth(s.handleResultViewWorkflowPause))
	s.registerSystemPluginRoutes(mux)
	s.registerResultPluginRoutes(mux)

	// 系统管理。
	mux.HandleFunc("GET /api/v1/admin/users", s.requirePermission(perm.SystemUsersView, s.handleListUsers))
	mux.HandleFunc("POST /api/v1/admin/users", s.requirePermission(perm.SystemUsersCreate, s.handleCreateUser))
	mux.HandleFunc("PUT /api/v1/admin/users/{userId}", s.requirePermission(perm.SystemUsersUpdate, s.handleUpdateUser))
	mux.HandleFunc("DELETE /api/v1/admin/users/{userId}", s.requirePermission(perm.SystemUsersDelete, s.handleDeleteUser))
	mux.HandleFunc("POST /api/v1/system/uploads/avatars", s.requireAuth(s.handleUploadAvatar))
	mux.HandleFunc("GET /api/v1/system/roles", s.requirePermission(perm.SystemRolesView, s.handleListRoles))
	mux.HandleFunc("POST /api/v1/system/roles", s.requirePermission(perm.SystemRolesCreate, s.handleCreateRole))
	mux.HandleFunc("PUT /api/v1/system/roles/{roleId}", s.requirePermission(perm.SystemRolesUpdate, s.handleUpdateRole))
	mux.HandleFunc("DELETE /api/v1/system/roles/{roleId}", s.requirePermission(perm.SystemRolesDelete, s.handleDeleteRole))
	mux.HandleFunc("PUT /api/v1/system/roles/{roleId}/permissions", s.requirePermission(perm.SystemRolesAssignPermissions, s.handleSaveRolePermissions))
	mux.HandleFunc("GET /api/v1/system/menus", s.requireAuth(s.handleGetMenus))
	mux.HandleFunc("GET /api/v1/system/menus/manage-tree", s.requirePermission(perm.SystemRolesAssignPermissions, s.handleGetManageMenus))
	mux.HandleFunc("GET /api/v1/system/i18n-dictionaries", s.requireAuth(s.handleGetI18nDict))
	mux.HandleFunc("POST /api/v1/system/menus", s.requirePermission(perm.SystemMenusCreate, s.handleCreateMenu))
	mux.HandleFunc("PUT /api/v1/system/menus/{menuId}", s.requirePermission(perm.SystemMenusUpdate, s.handleUpdateMenu))
	mux.HandleFunc("DELETE /api/v1/system/menus/{menuId}", s.requirePermission(perm.SystemMenusDelete, s.handleDeleteMenu))
	mux.HandleFunc("POST /api/v1/system/menu-buttons", s.requirePermission(perm.SystemMenusCreate, s.handleCreateMenuButton))
	mux.HandleFunc("PUT /api/v1/system/menu-buttons/{buttonId}", s.requirePermission(perm.SystemMenusUpdate, s.handleUpdateMenuButton))
	mux.HandleFunc("DELETE /api/v1/system/menu-buttons/{buttonId}", s.requirePermission(perm.SystemMenusDelete, s.handleDeleteMenuButton))
	mux.HandleFunc("GET /api/v1/system/plugins", s.requirePermission(perm.SystemPluginsView, func(w http.ResponseWriter, _ *http.Request, _ *service.Principal) {
		ok(w, s.App.ListInstalledPlugins())
	}))
	mux.HandleFunc("GET /api/v1/system/logs", s.requirePermission(perm.SystemLogsView, s.handleListSystemLogs))
	mux.HandleFunc("GET /api/v1/system/logs/runtime", s.requirePermission(perm.SystemLogsView, s.handleGetSystemLogRuntime))
	mux.HandleFunc("PUT /api/v1/system/logs/runtime", s.requirePermission(perm.SystemLogsConfigure, s.handleUpdateSystemLogRuntime))
}

func (s *Server) registerResultPluginRoutes(mux *http.ServeMux) {
	if s.App == nil || s.App.Plugins == nil {
		return
	}
	for _, route := range s.App.Plugins.Routes() {
		if route.Descriptor.Scope != sdk.ScopeResult {
			continue
		}
		registered := route
		pattern := registered.Descriptor.Method + " /api/v1/result-views/{viewId}/plugins/" + registered.PluginID + registered.Descriptor.Pattern
		mux.HandleFunc(pattern, s.requireAuth(func(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
			viewID, err := pathInt64(r, "viewId")
			if err != nil {
				writeProblem(w, r, http.StatusNotFound, service.ErrNotFound.Error())
				return
			}
			scope, err := s.App.ResolveResultScope(r.Context(), viewID, registered.Descriptor.Action, principal)
			if err != nil || scope.PluginID != registered.PluginID {
				respond(w, nil, fmt.Errorf("%w: result view", service.ErrNotFound), "")
				return
			}
			if !authorizeResultAction(w, r, principal, registered.Descriptor.Action) {
				return
			}
			registered.Handler(w, r, scope)
		}))
	}
}

var resultActionPermissions = map[string]string{
	"approve": perm.ResultViewsApprove, "reject": perm.ResultViewsReject,
	"retry": perm.ResultViewsRetry, "cancel": perm.ResultViewsCancel,
	"pause": perm.ResultViewsPause, "export": perm.ResultViewsExport,
}

func authorizeResultAction(w http.ResponseWriter, r *http.Request, principal *service.Principal, action string) bool {
	if action == "" {
		return true
	}
	permission, known := resultActionPermissions[action]
	if known && (principal.HasRole("R_SUPER") || principal.HasPermission(permission)) {
		return true
	}
	writeProblem(w, r, http.StatusForbidden, service.ErrPermission.Error())
	return false
}

func (s *Server) registerSystemPluginRoutes(mux *http.ServeMux) {
	if s.App == nil || s.App.Plugins == nil {
		return
	}
	for _, route := range s.App.Plugins.Routes() {
		if route.Descriptor.Scope != sdk.ScopeSystem {
			continue
		}
		registered := route
		pattern := registered.Descriptor.Method + " /api/v1/plugins/" + registered.PluginID + registered.Descriptor.Pattern
		mux.HandleFunc(pattern, s.requireRole("R_SUPER", func(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
			registered.Handler(w, r, sdk.SystemScope{
				PluginID: registered.PluginID, UserID: principal.User.ID,
				RoleCodes: slices.Clone(principal.RoleCodes),
			})
		}))
	}
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	if err := s.App.DatabaseReady(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, M{"status": "unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, M{"status": "ready"})
}
