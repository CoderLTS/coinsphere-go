package api

import (
	"net/http"
	"os"
	"path/filepath"

	"coinsphere/backend/internal/perm"
	"coinsphere/backend/internal/service"
)

// registerRoutes 注册全部路由(与原 FastAPI 路由一一对应)。
func (s *Server) registerRoutes(mux *http.ServeMux) {
	// 健康检查。
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		ok(w, M{"status": "ok"})
	})

	// 静态目录。
	_ = os.MkdirAll(s.StaticDir, 0o755)
	_ = os.MkdirAll(filepath.Join(s.UploadsDir, "avatars"), 0o755)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir(s.StaticDir))))
	mux.Handle("GET /uploads/", http.StripPrefix("/uploads/", http.FileServer(http.Dir(s.UploadsDir))))

	// 认证。
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/auth/refresh", s.handleRefresh)
	mux.HandleFunc("GET /api/auth/me", s.requireGuestOrAuth(s.handleMe))

	// 首页。
	mux.HandleFunc("GET /api/home/meta", s.requireGuestOrAuth(func(w http.ResponseWriter, r *http.Request, p *service.Principal) {
		ok(w, s.App.GetHomeMeta())
	}))
	mux.HandleFunc("GET /api/home/overview", s.requireGuestOrAuth(func(w http.ResponseWriter, r *http.Request, p *service.Principal) {
		data, err := s.App.GetHomeOverview()
		respond(w, data, err, "")
	}))

	// 数据管理。
	mux.HandleFunc("GET /api/data/news", s.requirePermission(perm.DataNewsView, s.handleListNews))
	mux.HandleFunc("POST /api/data/news", s.requirePermission(perm.DataNewsCreate, s.handleCreateNews))
	mux.HandleFunc("PUT /api/data/news/{newsId}", s.requirePermission(perm.DataNewsUpdate, s.handleUpdateNews))
	mux.HandleFunc("DELETE /api/data/news/{newsId}", s.requirePermission(perm.DataNewsDelete, s.handleDeleteNews))
	mux.HandleFunc("GET /api/data/push-deliveries", s.requirePermission(perm.DataPushDeliveriesView, s.handleListPushDeliveries))

	// 系统管理。
	mux.HandleFunc("GET /api/system/users", s.requirePermission(perm.SystemUsersView, s.handleListUsers))
	mux.HandleFunc("POST /api/system/users", s.requirePermission(perm.SystemUsersCreate, s.handleCreateUser))
	mux.HandleFunc("PUT /api/system/users/{userId}", s.requirePermission(perm.SystemUsersUpdate, s.handleUpdateUser))
	mux.HandleFunc("DELETE /api/system/users/{userId}", s.requirePermission(perm.SystemUsersDelete, s.handleDeleteUser))
	mux.HandleFunc("POST /api/system/uploads/avatars", s.requireAuth(s.handleUploadAvatar))
	mux.HandleFunc("GET /api/system/roles", s.requirePermission(perm.SystemRolesView, s.handleListRoles))
	mux.HandleFunc("POST /api/system/roles", s.requirePermission(perm.SystemRolesCreate, s.handleCreateRole))
	mux.HandleFunc("PUT /api/system/roles/{roleId}", s.requirePermission(perm.SystemRolesUpdate, s.handleUpdateRole))
	mux.HandleFunc("DELETE /api/system/roles/{roleId}", s.requirePermission(perm.SystemRolesDelete, s.handleDeleteRole))
	mux.HandleFunc("PUT /api/system/roles/{roleId}/permissions", s.requirePermission(perm.SystemRolesAssignPermissions, s.handleSaveRolePermissions))
	mux.HandleFunc("GET /api/system/menus", s.requireGuestOrAuth(s.handleGetMenus))
	mux.HandleFunc("GET /api/system/menus/manage-tree", s.requirePermission(perm.SystemRolesAssignPermissions, s.handleGetManageMenus))
	mux.HandleFunc("GET /api/system/i18n-dictionaries", s.requireGuestOrAuth(s.handleGetI18nDict))
	mux.HandleFunc("POST /api/system/menus", s.requirePermission(perm.SystemMenusCreate, s.handleCreateMenu))
	mux.HandleFunc("PUT /api/system/menus/{menuId}", s.requirePermission(perm.SystemMenusUpdate, s.handleUpdateMenu))
	mux.HandleFunc("DELETE /api/system/menus/{menuId}", s.requirePermission(perm.SystemMenusDelete, s.handleDeleteMenu))
	mux.HandleFunc("POST /api/system/menu-buttons", s.requirePermission(perm.SystemMenusCreate, s.handleCreateMenuButton))
	mux.HandleFunc("PUT /api/system/menu-buttons/{buttonId}", s.requirePermission(perm.SystemMenusUpdate, s.handleUpdateMenuButton))
	mux.HandleFunc("DELETE /api/system/menu-buttons/{buttonId}", s.requirePermission(perm.SystemMenusDelete, s.handleDeleteMenuButton))

	// 调度中心。
	mux.HandleFunc("GET /api/scheduler/overview", s.requirePermission(perm.SchedulerWorkflowDefinitionsView, s.handleSchedulerOverview))
	mux.HandleFunc("GET /api/scheduler/task-definitions", s.requirePermission(perm.SchedulerWorkflowDefinitionsView, s.handleListTaskDefinitions))
	mux.HandleFunc("GET /api/scheduler/task-definitions/page", s.requirePermission(perm.SchedulerTaskDefinitionsView, s.handleListTaskDefinitionPage))
	mux.HandleFunc("PUT /api/scheduler/task-definitions/{taskCode}/default-params", s.requirePermission(perm.SchedulerTaskDefinitionsUpdate, s.handleUpdateTaskDefaultParams))
	mux.HandleFunc("GET /api/scheduler/node-definitions", s.requirePermission(perm.SchedulerWorkflowDefinitionsView, s.handleListNodeDefinitions))
	mux.HandleFunc("GET /api/scheduler/workflow-definitions", s.requirePermission(perm.SchedulerWorkflowDefinitionsView, s.handleListWorkflowDefinitions))
	mux.HandleFunc("POST /api/scheduler/workflow-definitions/validate", s.requirePermission(perm.SchedulerWorkflowDefinitionsView, s.handleValidateWorkflowDefinition))
	mux.HandleFunc("POST /api/scheduler/workflow-definitions", s.requirePermission(perm.SchedulerWorkflowDefinitionsCreate, s.handleCreateWorkflowDefinition))
	mux.HandleFunc("GET /api/scheduler/workflow-definitions/{definitionId}", s.requirePermission(perm.SchedulerWorkflowDefinitionsView, s.handleGetWorkflowDefinition))
	mux.HandleFunc("PUT /api/scheduler/workflow-definitions/{definitionId}", s.requirePermission(perm.SchedulerWorkflowDefinitionsUpdate, s.handleUpdateWorkflowDefinition))
	mux.HandleFunc("DELETE /api/scheduler/workflow-definitions/{definitionId}", s.requirePermission(perm.SchedulerWorkflowDefinitionsDelete, s.handleDeleteWorkflowDefinition))
	mux.HandleFunc("POST /api/scheduler/workflow-definitions/{definitionId}/activate", s.requirePermission(perm.SchedulerWorkflowRuntimeActivate, s.handleActivateWorkflowDefinition))
	mux.HandleFunc("POST /api/scheduler/workflow-definitions/{definitionId}/deactivate", s.requirePermission(perm.SchedulerWorkflowRuntimeActivate, s.handleDeactivateWorkflowDefinition))
	mux.HandleFunc("GET /api/scheduler/workflow-definitions/{definitionId}/runtime", s.requirePermission(perm.SchedulerWorkflowRuntimeView, s.handleGetWorkflowRuntime))
	mux.HandleFunc("PATCH /api/scheduler/workflow-definitions/{definitionId}/runtime/entries/{entryKey}", s.requirePermission(perm.SchedulerWorkflowRuntimeUpdate, s.handlePatchRuntimeEntry))
	mux.HandleFunc("POST /api/scheduler/workflow-definitions/{definitionId}/runtime/entries/{entryKey}/rotate-secret", s.requirePermission(perm.SchedulerWorkflowRuntimeUpdate, s.handleRotateWebhookSecret))
	mux.HandleFunc("POST /api/scheduler/workflow-definitions/{definitionId}/executions", s.requirePermission(perm.SchedulerWorkflowDefinitionsRun, s.handleRunWorkflowStarts))
	mux.HandleFunc("GET /api/scheduler/workflow-definitions/{definitionId}/executions", s.requirePermission(perm.SchedulerWorkflowExecutionsView, s.handleListDefinitionExecutions))
	mux.HandleFunc("GET /api/scheduler/workflow-executions", s.requirePermission(perm.SchedulerWorkflowExecutionsView, s.handleListAllExecutions))
	mux.HandleFunc("GET /api/scheduler/workflow-executions/{executionId}", s.requirePermission(perm.SchedulerWorkflowExecutionsView, s.handleGetExecutionDetail))
	mux.HandleFunc("POST /api/scheduler/webhooks/{workflowCode}/{entryKey}", s.handleWebhookTrigger)

	// 配置中心。
	mux.HandleFunc("GET /api/config/overview", s.requirePermission(perm.ConfigOverviewView, s.handleConfigOverview))
	mux.HandleFunc("GET /api/config/ai-models", s.requirePermission(perm.ConfigAiModelsView, s.handleListAiModels))
	mux.HandleFunc("POST /api/config/ai-models", s.requirePermission(perm.ConfigAiModelsCreate, s.handleCreateAiModel))
	mux.HandleFunc("GET /api/config/ai-models/meta", s.requirePermission(perm.ConfigAiModelsView, s.handleAiModelMeta))
	mux.HandleFunc("PUT /api/config/ai-models/{configId}", s.requirePermission(perm.ConfigAiModelsUpdate, s.handleUpdateAiModel))
	mux.HandleFunc("DELETE /api/config/ai-models/{configId}", s.requirePermission(perm.ConfigAiModelsDelete, s.handleDeleteAiModel))
	mux.HandleFunc("PATCH /api/config/ai-models/{configId}", s.requirePermission(perm.ConfigAiModelsUpdate, s.handlePatchAiModel))
	mux.HandleFunc("POST /api/config/ai-models/{configId}/validations", s.requirePermission(perm.ConfigAiModelsValidate, s.handleValidateAiModel))
	mux.HandleFunc("PUT /api/config/ai-models/{configId}/agent-bindings", s.requirePermission(perm.ConfigAiModelsBindAgents, s.handleBindAiModelAgents))
	mux.HandleFunc("GET /api/config/assistant-agents", s.requirePermission(perm.ConfigAssistantAgentsView, s.handleListConfigAgents))
	mux.HandleFunc("POST /api/config/assistant-agents", s.requirePermission(perm.ConfigAssistantAgentsCreate, s.handleCreateConfigAgent))
	mux.HandleFunc("GET /api/config/assistant-agents/meta", s.requirePermission(perm.ConfigAiModelsView, s.handleConfigAgentMeta))
	mux.HandleFunc("PUT /api/config/assistant-agents/{agentId}", s.requirePermission(perm.ConfigAssistantAgentsUpdate, s.handleUpdateConfigAgent))
	mux.HandleFunc("DELETE /api/config/assistant-agents/{agentId}", s.requirePermission(perm.ConfigAssistantAgentsDelete, s.handleDeleteConfigAgent))
	mux.HandleFunc("PATCH /api/config/assistant-agents/{agentId}", s.requirePermission(perm.ConfigAssistantAgentsUpdate, s.handlePatchConfigAgent))
	mux.HandleFunc("GET /api/config/notification-channels", s.requirePermission(perm.ConfigNotificationChannelsView, s.handleListNotifyChannels))
	mux.HandleFunc("POST /api/config/notification-channels", s.requirePermission(perm.ConfigNotificationChannelsCreate, s.handleCreateNotifyChannel))
	mux.HandleFunc("GET /api/config/notification-channels/meta", s.requirePermission(perm.ConfigNotificationChannelsView, s.handleNotifyChannelMeta))
	mux.HandleFunc("PUT /api/config/notification-channels/{channelId}", s.requirePermission(perm.ConfigNotificationChannelsUpdate, s.handleUpdateNotifyChannel))
	mux.HandleFunc("DELETE /api/config/notification-channels/{channelId}", s.requirePermission(perm.ConfigNotificationChannelsDelete, s.handleDeleteNotifyChannel))
	mux.HandleFunc("PATCH /api/config/notification-channels/{channelId}", s.requirePermission(perm.ConfigNotificationChannelsUpdate, s.handlePatchNotifyChannel))
	mux.HandleFunc("POST /api/config/notification-channels/{channelId}/tests", s.requirePermission(perm.ConfigNotificationChannelsTest, s.handleTestNotifyChannel))

	// 助手。
	mux.HandleFunc("GET /api/assistant/agents", s.requireAuth(s.handleAssistantAgents))
	mux.HandleFunc("GET /api/assistant/agents/{agentCode}/model-options", s.requireAuth(s.handleAssistantModelOptions))
	mux.HandleFunc("GET /api/assistant/sessions/current", s.requireAuth(s.handleAssistantSessionCurrent))
	mux.HandleFunc("GET /api/assistant/sessions", s.requireAuth(s.handleAssistantSessions))
	mux.HandleFunc("GET /api/assistant/sessions/{sessionId}/messages", s.requireAuth(s.handleAssistantMessages))
	mux.HandleFunc("DELETE /api/assistant/sessions/{sessionId}", s.requireAuth(s.handleAssistantDeleteSession))
	mux.HandleFunc("POST /api/assistant/sessions/{sessionId}/stream", s.requireAuth(s.handleAssistantStream))

	// 通知。
	mux.HandleFunc("GET /api/notifications/in-app", s.requireAuth(s.handleListInApp))
	mux.HandleFunc("POST /api/notifications/in-app/read-all", s.requireAuth(s.handleReadAllInApp))
	mux.HandleFunc("POST /api/notifications/in-app/tests", s.requireAuth(s.handleTestInApp))
	mux.HandleFunc("POST /api/notifications/in-app/{deliveryId}/read", s.requireAuth(s.handleReadInApp))
	mux.HandleFunc("GET /ws/notifications", s.handleNotificationsWS)
}
