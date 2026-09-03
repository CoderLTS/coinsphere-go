package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"coinsphere/backend/internal/perm"
	"coinsphere/backend/internal/service"
	"coinsphere/backend/plugin/sdk"
	"github.com/gin-gonic/gin"
)

// registerRoutes 只暴露 V2 基线仍保留的认证、监控与系统管理边界。
func (s *Server) registerRoutes(router *gin.Engine) {
	get(router, "/health", s.handleReady)
	get(router, "/health/live", func(c *gin.Context) {
		writeJSON(c, http.StatusOK, M{"status": "alive"})
	})
	get(router, "/health/ready", s.handleReady)
	get(router, "/metrics", s.requireAuth(), s.handleMetrics)

	_ = os.MkdirAll(s.StaticDir, 0o755)
	_ = os.MkdirAll(filepath.Join(s.UploadsDir, "avatars"), 0o755)
	router.StaticFS("/static", http.Dir(s.StaticDir))
	router.StaticFS("/uploads", http.Dir(s.UploadsDir))

	api := router.Group("/api/v1")
	authenticated := api.Group("", s.requireAuth())
	super := authenticated.Group("", s.requireRole("R_SUPER"))
	auth := api.Group("/auth")
	auth.POST("/login", s.rateLimit(), s.handleLogin)
	auth.POST("/logout", s.requireAuth(), s.handleLogout)
	auth.POST("/reauth", s.requireAuth(), s.handleReauth)
	get(authenticated, "/me", s.handleMe)

	get(authenticated, "/home/meta", func(c *gin.Context) {
		ok(c, s.App.GetHomeMeta())
	})
	get(authenticated, "/home/overview", func(c *gin.Context) {
		data, err := s.App.GetHomeOverview(c.Request.Context())
		if err == nil {
			for key, value := range s.metrics.snapshot() {
				data[key] = value
			}
		}
		respond(c, data, err, "")
	})

	get(super, "/config/ai-models", s.handleListAIModels)
	super.POST("/config/ai-models", s.handleCreateAIModel)
	super.PUT("/config/ai-models/:modelId", s.handleUpdateAIModel)
	super.PATCH("/config/ai-models/:modelId", s.handlePatchAIModel)
	super.DELETE("/config/ai-models/:modelId", s.handleDeleteAIModel)
	super.POST("/config/ai-models/:modelId/validations", s.handleValidateAIModel)
	get(super, "/assistant/models", s.handleListAssistantModels)
	get(super, "/assistant/sessions", s.handleListAssistantSessions)
	super.POST("/assistant/sessions", s.handleCreateAssistantSession)
	get(super, "/assistant/sessions/:sessionId", s.handleGetAssistantSession)
	super.DELETE("/assistant/sessions/:sessionId", s.handleDeleteAssistantSession)
	get(super, "/assistant/sessions/:sessionId/messages", s.handleListAssistantMessages)
	super.POST("/assistant/sessions/:sessionId/stream", s.handleStreamAssistantSession)

	api.POST("/webhooks/:workflowId", s.handlePublishWorkflowWebhook)
	get(super, "/workflows/templates", s.handleListWorkflowTemplates)
	super.POST("/events", s.handlePublishWorkflowEvent)
	get(super, "/human-tasks", s.handleListWorkflowHumanTasks)
	super.POST("/human-tasks/:taskId", s.handleDecideWorkflowHumanTask)
	get(super, "/workflows/node-definitions", s.handleListWorkflowNodeDefinitions)
	super.POST("/workflows/validate", s.handleValidateWorkflowGraph)
	get(super, "/workflow-groups", s.handleListWorkflowGroups)
	super.POST("/workflow-groups", s.handleCreateWorkflowGroup)
	super.PUT("/workflow-groups/order", s.handleUpdateWorkflowGroupOrder)
	super.PATCH("/workflow-groups/:groupId", s.handleUpdateWorkflowGroup)
	super.DELETE("/workflow-groups/:groupId", s.handleDeleteWorkflowGroup)
	get(super, "/workflows", s.handleListWorkflows)
	super.POST("/workflows", s.handleCreateWorkflow)
	super.PATCH("/workflows/group-assignment", s.handleAssignWorkflowGroup)
	get(super, "/workflows/:workflowId", s.handleGetWorkflow)
	super.PATCH("/workflows/:workflowId", s.handleUpdateWorkflow)
	super.DELETE("/workflows/:workflowId", s.requirePermission(perm.SchedulerWorkflowDefinitionsDelete), s.handleDeleteWorkflow)
	get(super, "/workflows/:workflowId/revisions", s.handleListWorkflowRevisions)
	super.POST("/workflows/:workflowId/revisions", s.handleSaveWorkflowRevision)
	get(super, "/workflows/:workflowId/revisions/:revisionId", s.handleGetWorkflowRevision)
	super.DELETE("/workflows/:workflowId/revisions/:revisionId", s.requirePermission(perm.SchedulerWorkflowDefinitionsDelete), s.handleDeleteWorkflowRevision)
	super.POST("/workflows/:workflowId/lifecycle", s.handleWorkflowLifecycle)
	get(super, "/workflows/:workflowId/runs", s.handleListWorkflowRuns)
	super.POST("/workflows/:workflowId/runs", s.handleCreateWorkflowRun)
	get(api, "/ws/workflows/:workflowId/runs", s.handleWorkflowRunsWebSocket)
	get(authenticated, "/notification-deliveries", s.handleListInAppNotifications)
	authenticated.POST("/notification-deliveries/:deliveryId/read", s.handleReadInAppNotification)
	authenticated.POST("/notification-deliveries/read-all", s.handleReadAllInAppNotifications)
	get(api, "/ws/notifications", s.handleNotificationWebSocket)
	get(super, "/workflow-runs/:runId", s.handleGetWorkflowRun)
	super.POST("/workflow-runs/:runId", s.handleWorkflowRunAction)
	get(super, "/artifacts/:sha256/manifest", s.handleGetWorkflowArtifactManifest)
	get(super, "/artifacts/:sha256/download", s.handleDownloadWorkflowArtifact)
	get(authenticated, "/result-views", s.requirePermission(perm.ResultViewsAccess), s.handleListResultViews)
	super.POST("/result-views", s.handleCreateResultView)
	get(authenticated, "/result-views/:viewId", s.requirePermission(perm.ResultViewsAccess), s.handleGetResultView)
	super.PUT("/result-views/:viewId/grants", s.handleReplaceResultViewGrants)
	super.POST("/result-views/:viewId/revoke", s.handleRevokeResultView)
	get(authenticated, "/result-views/:viewId/runs", s.handleListResultViewRuns)
	authenticated.POST("/result-views/:viewId/runs/:runId/:action", s.handleResultViewRunAction)
	authenticated.POST("/result-views/:viewId/workflow/pause", s.handleResultViewWorkflowPause)
	s.registerSystemPluginRoutes(authenticated, api)
	s.registerResultPluginRoutes(authenticated)

	get(authenticated, "/admin/users", s.requirePermission(perm.SystemUsersView), s.handleListUsers)
	authenticated.POST("/admin/users", s.requirePermission(perm.SystemUsersCreate), s.handleCreateUser)
	authenticated.PUT("/admin/users/:userId", s.requirePermission(perm.SystemUsersUpdate), s.handleUpdateUser)
	authenticated.DELETE("/admin/users/:userId", s.requirePermission(perm.SystemUsersDelete), s.handleDeleteUser)
	authenticated.POST("/system/uploads/avatars", s.handleUploadAvatar)
	get(authenticated, "/system/roles", s.requirePermission(perm.SystemRolesView), s.handleListRoles)
	authenticated.POST("/system/roles", s.requirePermission(perm.SystemRolesCreate), s.handleCreateRole)
	authenticated.PUT("/system/roles/:roleId", s.requirePermission(perm.SystemRolesUpdate), s.handleUpdateRole)
	authenticated.DELETE("/system/roles/:roleId", s.requirePermission(perm.SystemRolesDelete), s.handleDeleteRole)
	authenticated.PUT("/system/roles/:roleId/permissions", s.requirePermission(perm.SystemRolesAssignPermissions), s.handleSaveRolePermissions)
	get(authenticated, "/system/menus", s.handleGetMenus)
	get(authenticated, "/system/menus/manage-tree", s.requirePermission(perm.SystemRolesAssignPermissions), s.handleGetManageMenus)
	get(authenticated, "/system/i18n-dictionaries", s.handleGetI18nDict)
	authenticated.POST("/system/menus", s.requirePermission(perm.SystemMenusCreate), s.handleCreateMenu)
	authenticated.PUT("/system/menus/:menuId", s.requirePermission(perm.SystemMenusUpdate), s.handleUpdateMenu)
	authenticated.DELETE("/system/menus/:menuId", s.requirePermission(perm.SystemMenusDelete), s.handleDeleteMenu)
	authenticated.POST("/system/menu-buttons", s.requirePermission(perm.SystemMenusCreate), s.handleCreateMenuButton)
	authenticated.PUT("/system/menu-buttons/:buttonId", s.requirePermission(perm.SystemMenusUpdate), s.handleUpdateMenuButton)
	authenticated.DELETE("/system/menu-buttons/:buttonId", s.requirePermission(perm.SystemMenusDelete), s.handleDeleteMenuButton)
	get(authenticated, "/system/plugins", s.requirePermission(perm.SystemPluginsView), func(c *gin.Context) {
		ok(c, s.App.ListInstalledPlugins())
	})
	get(authenticated, "/system/proxies", s.requirePermission(perm.SystemProxiesView), s.handleListOutboundProxies)
	authenticated.POST("/system/proxies", s.requirePermission(perm.SystemProxiesCreate), s.handleCreateOutboundProxy)
	authenticated.PUT("/system/proxies/:proxyId", s.requirePermission(perm.SystemProxiesUpdate), s.handleUpdateOutboundProxy)
	authenticated.PATCH("/system/proxies/:proxyId", s.requirePermission(perm.SystemProxiesUpdate), s.handlePatchOutboundProxy)
	authenticated.DELETE("/system/proxies/:proxyId", s.requirePermission(perm.SystemProxiesDelete), s.handleDeleteOutboundProxy)
	authenticated.POST("/system/proxies/:proxyId/validations", s.requirePermission(perm.SystemProxiesValidate), s.handleValidateOutboundProxy)
	get(authenticated, "/system/logs", s.requirePermission(perm.SystemLogsView), s.handleListSystemLogs)
	get(authenticated, "/system/logs/runtime", s.requirePermission(perm.SystemLogsView), s.handleGetSystemLogRuntime)
	authenticated.PUT("/system/logs/runtime", s.requirePermission(perm.SystemLogsConfigure), s.handleUpdateSystemLogRuntime)
}

func get(routes gin.IRoutes, path string, handlers ...gin.HandlerFunc) {
	routes.GET(path, handlers...)
	routes.HEAD(path, handlers...)
}

func registerPluginRoute(routes gin.IRoutes, method, path string, handlers ...gin.HandlerFunc) {
	routes.Handle(method, path, handlers...)
	if method == http.MethodGet {
		routes.Handle(http.MethodHead, path, handlers...)
	}
}

func (s *Server) registerResultPluginRoutes(routes gin.IRoutes) {
	if s.App == nil || s.App.Plugins == nil {
		return
	}
	for _, route := range s.App.Plugins.Routes() {
		if route.Descriptor.Scope != sdk.ScopeResult {
			continue
		}
		registered := route
		pattern := "/result-views/:viewId/plugins/" + registered.PluginID + registered.Descriptor.Pattern
		registerPluginRoute(routes, registered.Descriptor.Method, pattern, func(c *gin.Context) {
			principal := currentPrincipal(c)
			viewID, err := pathInt64(c, "viewId")
			if err != nil {
				writeProblem(c, http.StatusNotFound, service.ErrNotFound.Error())
				return
			}
			scope, err := s.App.ResolveResultScope(c.Request.Context(), viewID, registered.Descriptor.Action, principal)
			if err != nil || scope.PluginID != registered.PluginID {
				respond(c, nil, fmt.Errorf("%w: result view", service.ErrNotFound), "")
				return
			}
			if !authorizeResultAction(c, principal, registered.Descriptor.Action) {
				return
			}
			registered.Handler(c, scope)
		})
	}
}

var resultActionPermissions = map[string]string{
	"approve": perm.ResultViewsApprove, "reject": perm.ResultViewsReject,
	"retry": perm.ResultViewsRetry, "cancel": perm.ResultViewsCancel,
	"pause": perm.ResultViewsPause, "export": perm.ResultViewsExport,
}

func authorizeResultAction(c *gin.Context, principal *service.Principal, action string) bool {
	if action == "" {
		return true
	}
	permission, known := resultActionPermissions[action]
	if known && (principal.HasRole("R_SUPER") || principal.HasPermission(permission)) {
		return true
	}
	writeProblem(c, http.StatusForbidden, service.ErrPermission.Error())
	return false
}

func (s *Server) registerSystemPluginRoutes(routes, publicRoutes gin.IRoutes) {
	if s.App == nil || s.App.Plugins == nil {
		return
	}
	for _, route := range s.App.Plugins.Routes() {
		if route.Descriptor.Scope != sdk.ScopeSystem {
			continue
		}
		registered := route
		pattern := "/plugins/" + registered.PluginID + registered.Descriptor.Pattern
		if registered.Descriptor.WebSocket {
			publicRoutes.GET(pattern, func(c *gin.Context) {
				protocol := "coinsphere.plugin." + registered.PluginID + ".v1"
				token, ok := pluginWebSocketToken(c.Request, protocol)
				principal, err := s.App.AuthenticateAccessToken(token)
				if !ok || err != nil || principal == nil || !principal.HasRole("R_SUPER") {
					writeProblem(c, http.StatusUnauthorized, "invalid websocket authentication")
					return
				}
				registered.Handler(c, sdk.SystemScope{
					PluginID: registered.PluginID, UserID: principal.User.ID,
					RoleCodes: slices.Clone(principal.RoleCodes),
				})
			})
			continue
		}
		registerPluginRoute(routes, registered.Descriptor.Method, pattern, s.requireRole("R_SUPER"), func(c *gin.Context) {
			principal := currentPrincipal(c)
			registered.Handler(c, sdk.SystemScope{
				PluginID: registered.PluginID, UserID: principal.User.ID,
				RoleCodes: slices.Clone(principal.RoleCodes),
			})
		})
	}
}

func pluginWebSocketToken(request *http.Request, expectedProtocol string) (string, bool) {
	values := request.Header.Values("Sec-WebSocket-Protocol")
	if len(values) != 1 {
		return "", false
	}
	protocols := strings.Split(values[0], ",")
	if len(protocols) != 2 || strings.TrimSpace(protocols[0]) != expectedProtocol {
		return "", false
	}
	token := strings.TrimSpace(protocols[1])
	return token, token != ""
}

func (s *Server) handleReady(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Second)
	defer cancel()
	if err := s.App.DatabaseReady(ctx); err != nil {
		writeJSON(c, http.StatusServiceUnavailable, M{"status": "unavailable"})
		return
	}
	writeJSON(c, http.StatusOK, M{"status": "ready"})
}
