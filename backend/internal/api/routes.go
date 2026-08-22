package api

// import:标准库(net/http、os、path/filepath)在上,本项目内部包(perm 权限码、service 业务逻辑)在下。
import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"coinsphere/backend/internal/perm"
	"coinsphere/backend/internal/service"
)

// registerRoutes 把每个 URL 登记到路由表 mux。理解一行就理解全部:
//
//	mux.HandleFunc("方法 /路径", 处理函数) —— 例如 "GET /api/v1/..."。这是 Go 1.22+ net/http 的新路由写法(见 GO入门笔记『框架:net/http』)。
//	路径里的 {definitionId} 之类是"路径参数",处理函数用 r.PathValue("definitionId") 取出。
//	处理函数外面套一层 s.requireAuth / s.requirePermission(...),就是给这个接口加上"登录/权限"检查(中间件,见 api.go)。
//
// registerRoutes 注册全部路由(与原 FastAPI 路由一一对应)。
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

	// Binance 行情元数据和 K 线共享只读，自选始终按当前用户隔离。
	mux.HandleFunc("GET /api/v1/markets/symbols", s.requirePermission(perm.DataMarketView, s.handleListMarketSymbols))
	mux.HandleFunc("GET /api/v1/markets/candles", s.requirePermission(perm.DataMarketView, s.handleListMarketCandles))
	mux.HandleFunc("GET /api/v1/markets/metadata-sync/settings", s.requirePermission(perm.DataMarketView, s.handleGetMarketSyncSettings))
	mux.HandleFunc("PUT /api/v1/markets/metadata-sync/settings", s.requirePermission(perm.DataMarketManage, s.handleUpdateMarketSyncSettings))
	mux.HandleFunc("GET /api/v1/markets/metadata-sync/status", s.requirePermission(perm.DataMarketView, s.handleGetMarketSyncStatus))
	mux.HandleFunc("POST /api/v1/markets/metadata-sync/executions", s.requirePermission(perm.DataMarketManage, s.handleRunMarketSync))
	mux.HandleFunc("POST /api/v1/markets/metadata-sync/proxy-check", s.requirePermission(perm.DataMarketManage, s.handleCheckMarketProxy))
	mux.HandleFunc("GET /api/v1/watchlists", s.requireAuth(s.handleListWatchlists))
	mux.HandleFunc("POST /api/v1/watchlists", s.requireAuth(s.handleCreateWatchlist))
	mux.HandleFunc("DELETE /api/v1/watchlists/{watchlistId}", s.requireAuth(s.handleDeleteWatchlist))

	// 管理员维护可信单文件草稿；已发布版本共享只读，回测始终按当前用户隔离。
	mux.HandleFunc("GET /api/v1/admin/strategies", s.requireAuth(s.handleListStrategyDrafts))
	mux.HandleFunc("POST /api/v1/admin/strategies", s.requireAuth(s.handleCreateStrategyDraft))
	mux.HandleFunc("GET /api/v1/admin/strategies/{strategyId}", s.requireAuth(s.handleGetStrategyDraft))
	mux.HandleFunc("PUT /api/v1/admin/strategies/{strategyId}", s.requireAuth(s.handleUpdateStrategyDraft))
	mux.HandleFunc("DELETE /api/v1/admin/strategies/{strategyId}", s.requireAuth(s.handleArchiveStrategyDraft))
	mux.HandleFunc("POST /api/v1/admin/strategies/{strategyId}/publish", s.requireAuth(s.handlePublishStrategy))
	mux.HandleFunc("GET /api/v1/strategies", s.requireAuth(s.handleListPublishedStrategies))
	mux.HandleFunc("GET /api/v1/strategies/{strategyVersionId}", s.requireAuth(s.handleGetPublishedStrategy))
	mux.HandleFunc("GET /api/v1/backtests", s.requireAuth(s.handleListBacktests))
	mux.HandleFunc("POST /api/v1/backtests", s.requireAuth(s.handleCreateBacktest))
	mux.HandleFunc("GET /api/v1/backtests/{backtestId}", s.requireAuth(s.handleGetBacktest))
	mux.HandleFunc("POST /api/v1/backtests/{backtestId}/cancel", s.requireAuth(s.handleCancelBacktest))
	mux.HandleFunc("GET /api/v1/strategy-instances", s.requireAuth(s.handleListStrategyInstances))
	mux.HandleFunc("GET /api/v1/signals", s.requireAuth(s.handleListStrategySignals))
	mux.HandleFunc("POST /api/v1/signals/{signalId}/approve", s.requireAuth(s.handleApproveStrategySignal))
	mux.HandleFunc("POST /api/v1/signals/{signalId}/reject", s.requireAuth(s.handleRejectStrategySignal))
	mux.HandleFunc("GET /api/v1/trading/overview", s.requireAuth(s.handleTradingOverview))
	mux.HandleFunc("GET /api/v1/trading/accounts", s.requireAuth(s.handleListTradingAccounts))
	mux.HandleFunc("POST /api/v1/trading/accounts", s.requireAuth(s.handleCreateTradingAccount))
	mux.HandleFunc("GET /api/v1/trading/accounts/{accountId}", s.requireAuth(s.handleGetTradingAccount))
	mux.HandleFunc("PUT /api/v1/trading/accounts/{accountId}", s.requireAuth(s.handleUpdateTradingAccount))
	mux.HandleFunc("DELETE /api/v1/trading/accounts/{accountId}", s.requireAuth(s.handleArchiveTradingAccount))
	mux.HandleFunc("PUT /api/v1/trading/accounts/{accountId}/risk", s.requireAuth(s.handleUpdateTradingRisk))
	mux.HandleFunc("POST /api/v1/trading/accounts/{accountId}/automation/enable", s.requireAuth(s.handleEnableTradingAutomation))
	mux.HandleFunc("POST /api/v1/trading/accounts/{accountId}/automation/disable", s.requireAuth(s.handleDisableTradingAutomation))
	mux.HandleFunc("POST /api/v1/trading/accounts/{accountId}/resume", s.requireAuth(s.handleResumeTradingAccount))
	mux.HandleFunc("PUT /api/v1/trading/accounts/{accountId}/credentials", s.requireAuth(s.handleSaveTradingCredentials))
	mux.HandleFunc("POST /api/v1/trading/accounts/{accountId}/credentials/revoke", s.requireAuth(s.handleRevokeTradingCredentials))
	mux.HandleFunc("POST /api/v1/trading/emergency-stop", s.requireAuth(s.handleActivateTradingEmergencyStop))
	mux.HandleFunc("POST /api/v1/admin/trading/accounts/{accountId}/authorize", s.requireAuth(s.handleAuthorizeTradingAutomation))
	mux.HandleFunc("POST /api/v1/admin/trading/accounts/{accountId}/revoke", s.requireAuth(s.handleRevokeTradingAutomation))
	mux.HandleFunc("POST /api/v1/admin/trading/emergency-stop/release", s.requireAuth(s.handleReleaseTradingEmergencyStop))

	// 数据管理。
	// 第二个参数 perm.DataNewsView 是 perm 包里定义的权限码常量;requirePermission 会检查当前用户是否持有它。
	// 像 {newsId} 这样的花括号段是路径参数,对应 handler 里的 r.PathValue("newsId")。
	mux.HandleFunc("GET /api/v1/data/news", s.requirePermission(perm.DataNewsView, s.handleListNews))
	mux.HandleFunc("POST /api/v1/data/news", s.requirePermission(perm.DataNewsCreate, s.handleCreateNews))
	mux.HandleFunc("PUT /api/v1/data/news/{newsId}", s.requirePermission(perm.DataNewsUpdate, s.handleUpdateNews))
	mux.HandleFunc("DELETE /api/v1/data/news/{newsId}", s.requirePermission(perm.DataNewsDelete, s.handleDeleteNews))
	mux.HandleFunc("GET /api/v1/admin/notification-deliveries", s.requirePermission(perm.DataPushDeliveriesView, s.handleListPushDeliveries))

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

	// 调度中心。
	mux.HandleFunc("GET /api/v1/workflows/overview", s.requirePermission(perm.SchedulerWorkflowDefinitionsView, s.handleSchedulerOverview))
	mux.HandleFunc("GET /api/v1/workflows/node-definitions", s.requirePermission(perm.SchedulerWorkflowDefinitionsView, s.handleListNodeDefinitions))
	mux.HandleFunc("GET /api/v1/workflow-node-templates", s.requirePermission(perm.SchedulerWorkflowDefinitionsView, s.handleListWorkflowNodeTemplates))
	mux.HandleFunc("POST /api/v1/workflow-node-templates", s.requirePermission(perm.SchedulerWorkflowDefinitionsCreate, s.handleCreateWorkflowNodeTemplate))
	mux.HandleFunc("PUT /api/v1/workflow-node-templates/{templateId}", s.requirePermission(perm.SchedulerWorkflowDefinitionsUpdate, s.handleUpdateWorkflowNodeTemplate))
	mux.HandleFunc("DELETE /api/v1/workflow-node-templates/{templateId}", s.requirePermission(perm.SchedulerWorkflowDefinitionsDelete, s.handleDeleteWorkflowNodeTemplate))
	mux.HandleFunc("GET /api/v1/workflows/agent-options", s.requirePermission(perm.SchedulerWorkflowDefinitionsView, s.handleListWorkflowAgentOptions))
	mux.HandleFunc("GET /api/v1/workflows", s.requirePermission(perm.SchedulerWorkflowDefinitionsView, s.handleListWorkflowDefinitions))
	mux.HandleFunc("POST /api/v1/workflows/validate", s.requirePermission(perm.SchedulerWorkflowDefinitionsView, s.handleValidateWorkflowDefinition))
	mux.HandleFunc("POST /api/v1/workflows", s.requirePermission(perm.SchedulerWorkflowDefinitionsCreate, s.handleCreateWorkflowDefinition))
	mux.HandleFunc("GET /api/v1/workflows/{definitionId}", s.requirePermission(perm.SchedulerWorkflowDefinitionsView, s.handleGetWorkflowDefinition))
	mux.HandleFunc("PUT /api/v1/workflows/{definitionId}", s.requirePermission(perm.SchedulerWorkflowDefinitionsUpdate, s.handleUpdateWorkflowDefinition))
	mux.HandleFunc("DELETE /api/v1/workflows/{definitionId}", s.requirePermission(perm.SchedulerWorkflowDefinitionsDelete, s.handleDeleteWorkflowDefinition))
	mux.HandleFunc("POST /api/v1/workflows/{definitionId}/activate", s.requirePermission(perm.SchedulerWorkflowRuntimeActivate, s.handleActivateWorkflowDefinition))
	mux.HandleFunc("POST /api/v1/workflows/{definitionId}/deactivate", s.requirePermission(perm.SchedulerWorkflowRuntimeActivate, s.handleDeactivateWorkflowDefinition))
	mux.HandleFunc("GET /api/v1/workflows/{definitionId}/runtime", s.requirePermission(perm.SchedulerWorkflowRuntimeView, s.handleGetWorkflowRuntime))
	mux.HandleFunc("PATCH /api/v1/workflows/{definitionId}/runtime/entries/{entryKey}", s.requirePermission(perm.SchedulerWorkflowRuntimeUpdate, s.handlePatchRuntimeEntry))
	mux.HandleFunc("POST /api/v1/workflows/{definitionId}/runtime/entries/{entryKey}/rotate-secret", s.requirePermission(perm.SchedulerWorkflowRuntimeUpdate, s.handleRotateWebhookSecret))
	mux.HandleFunc("POST /api/v1/workflows/{definitionId}/executions", s.requirePermission(perm.SchedulerWorkflowDefinitionsRun, s.handleRunWorkflowStarts))
	mux.HandleFunc("GET /api/v1/workflows/{definitionId}/executions", s.requirePermission(perm.SchedulerWorkflowExecutionsView, s.handleListDefinitionExecutions))
	mux.HandleFunc("GET /api/v1/workflows/executions", s.requirePermission(perm.SchedulerWorkflowExecutionsView, s.handleListAllExecutions))
	mux.HandleFunc("GET /api/v1/workflows/{workflowPath...}", s.requirePermission(perm.SchedulerWorkflowExecutionsView, s.handleWorkflowCatchAll))
	// webhook 同时要求登录 token 和请求头 secret，避免公开 HTTP 入口绕过登录边界。
	mux.HandleFunc("POST /api/v1/workflows/webhooks/{workflowCode}/{entryKey}", s.requireAuth(s.handleWebhookTrigger))

	// 配置中心。
	mux.HandleFunc("GET /api/v1/config/overview", s.requirePermission(perm.ConfigOverviewView, s.handleConfigOverview))
	mux.HandleFunc("GET /api/v1/config/ai-models", s.requirePermission(perm.ConfigAiModelsView, s.handleListAiModels))
	mux.HandleFunc("POST /api/v1/config/ai-models", s.requirePermission(perm.ConfigAiModelsCreate, s.handleCreateAiModel))
	mux.HandleFunc("GET /api/v1/config/ai-models/meta", s.requirePermission(perm.ConfigAiModelsView, s.handleAiModelMeta))
	mux.HandleFunc("PUT /api/v1/config/ai-models/{configId}", s.requirePermission(perm.ConfigAiModelsUpdate, s.handleUpdateAiModel))
	mux.HandleFunc("DELETE /api/v1/config/ai-models/{configId}", s.requirePermission(perm.ConfigAiModelsDelete, s.handleDeleteAiModel))
	mux.HandleFunc("PATCH /api/v1/config/ai-models/{configId}", s.requirePermission(perm.ConfigAiModelsUpdate, s.handlePatchAiModel))
	mux.HandleFunc("POST /api/v1/config/ai-models/{configId}/validations", s.requirePermission(perm.ConfigAiModelsValidate, s.handleValidateAiModel))
	mux.HandleFunc("PUT /api/v1/config/ai-models/{configId}/agent-bindings", s.requirePermission(perm.ConfigAiModelsBindAgents, s.handleBindAiModelAgents))
	mux.HandleFunc("GET /api/v1/config/assistant-agents", s.requirePermission(perm.ConfigAssistantAgentsView, s.handleListConfigAgents))
	mux.HandleFunc("POST /api/v1/config/assistant-agents", s.requirePermission(perm.ConfigAssistantAgentsCreate, s.handleCreateConfigAgent))
	mux.HandleFunc("GET /api/v1/config/assistant-agents/meta", s.requirePermission(perm.ConfigAiModelsView, s.handleConfigAgentMeta))
	mux.HandleFunc("PUT /api/v1/config/assistant-agents/{agentId}", s.requirePermission(perm.ConfigAssistantAgentsUpdate, s.handleUpdateConfigAgent))
	mux.HandleFunc("DELETE /api/v1/config/assistant-agents/{agentId}", s.requirePermission(perm.ConfigAssistantAgentsDelete, s.handleDeleteConfigAgent))
	mux.HandleFunc("PATCH /api/v1/config/assistant-agents/{agentId}", s.requirePermission(perm.ConfigAssistantAgentsUpdate, s.handlePatchConfigAgent))
	mux.HandleFunc("GET /api/v1/notification-channels", s.requirePermission(perm.ConfigNotificationChannelsView, s.handleListNotifyChannels))
	mux.HandleFunc("POST /api/v1/notification-channels", s.requirePermission(perm.ConfigNotificationChannelsCreate, s.handleCreateNotifyChannel))
	mux.HandleFunc("GET /api/v1/notification-channels/meta", s.requirePermission(perm.ConfigNotificationChannelsView, s.handleNotifyChannelMeta))
	mux.HandleFunc("PUT /api/v1/notification-channels/{channelId}", s.requirePermission(perm.ConfigNotificationChannelsUpdate, s.handleUpdateNotifyChannel))
	mux.HandleFunc("DELETE /api/v1/notification-channels/{channelId}", s.requirePermission(perm.ConfigNotificationChannelsDelete, s.handleDeleteNotifyChannel))
	mux.HandleFunc("PATCH /api/v1/notification-channels/{channelId}", s.requirePermission(perm.ConfigNotificationChannelsUpdate, s.handlePatchNotifyChannel))
	mux.HandleFunc("POST /api/v1/notification-channels/{channelId}/tests", s.requirePermission(perm.ConfigNotificationChannelsTest, s.handleTestNotifyChannel))

	// 助手。
	mux.HandleFunc("GET /api/v1/assistant/agents", s.requireAuth(s.handleAssistantAgents))
	mux.HandleFunc("GET /api/v1/assistant/agents/{agentCode}/model-options", s.requireAuth(s.handleAssistantModelOptions))
	mux.HandleFunc("GET /api/v1/assistant/sessions/current", s.requireAuth(s.handleAssistantSessionCurrent))
	mux.HandleFunc("GET /api/v1/assistant/sessions", s.requireAuth(s.handleAssistantSessions))
	mux.HandleFunc("GET /api/v1/assistant/sessions/{sessionId}/messages", s.requireAuth(s.handleAssistantMessages))
	mux.HandleFunc("DELETE /api/v1/assistant/sessions/{sessionId}", s.requireAuth(s.handleAssistantDeleteSession))
	mux.HandleFunc("POST /api/v1/assistant/sessions/{sessionId}/stream", s.requireAuth(s.handleAssistantStream))

	// 通知。
	mux.HandleFunc("GET /api/v1/notification-deliveries", s.requireAuth(s.handleListInApp))
	mux.HandleFunc("POST /api/v1/notification-deliveries/read-all", s.requireAuth(s.handleReadAllInApp))
	mux.HandleFunc("POST /api/v1/notification-deliveries/tests", s.requireAuth(s.handleTestInApp))
	mux.HandleFunc("POST /api/v1/notification-deliveries/{deliveryId}/read", s.requireAuth(s.handleReadInApp))
	// WebSocket 在 handler 内校验固定子协议携带的 Access Token。
	mux.HandleFunc("GET /api/v1/ws/notifications", s.handleNotificationsWS)
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
