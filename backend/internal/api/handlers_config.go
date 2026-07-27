package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"

	"coinsphere/backend/internal/perm"
	"coinsphere/backend/internal/service"
)

// ---------- 配置中心 ----------

func (s *Server) handleConfigOverview(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	models, err := s.App.ListAiModelConfigs(principal)
	if err != nil {
		fail(w, err.Error())
		return
	}
	ok(w, M{
		"models":        models,
		"agents":        s.App.ListAssistantAgents(true),
		"notifySummary": s.App.GetNotifyOverviewSummary(),
	})
}

func (s *Server) handleListAiModels(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	data, err := s.App.ListAiModelConfigs(principal)
	respond(w, data, err, "")
}

func (s *Server) handleCreateAiModel(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	payload, err := decodeBody[service.AiModelUpsertPayload](r)
	if err != nil {
		fail(w, err.Error())
		return
	}
	data, err := s.App.CreateAiModelConfig(*payload, principal)
	respond(w, data, err, "模型配置已创建")
}

func (s *Server) handleUpdateAiModel(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	configID, err := pathInt64(r, "configId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	payload, err := decodeBody[service.AiModelUpsertPayload](r)
	if err != nil {
		fail(w, err.Error())
		return
	}
	data, err := s.App.UpdateAiModelConfig(configID, *payload, principal)
	respond(w, data, err, "模型配置已更新")
}

func (s *Server) handleDeleteAiModel(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	configID, err := pathInt64(r, "configId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	respond(w, nil, s.App.DeleteAiModelConfig(configID, principal), "模型配置已删除")
}

func (s *Server) handlePatchAiModel(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	configID, err := pathInt64(r, "configId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	payload, err := decodeBody[struct {
		IsEnabled bool `json:"isEnabled"`
	}](r)
	if err != nil {
		fail(w, err.Error())
		return
	}
	respond(w, nil, s.App.SetAiModelEnabled(configID, payload.IsEnabled, principal), "模型状态已更新")
}

func (s *Server) handleValidateAiModel(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	configID, err := pathInt64(r, "configId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	data, err := s.App.ValidateAiModelConfig(configID, principal)
	if err != nil {
		fail(w, err.Error())
		return
	}
	message, _ := data["message"].(string)
	okMsg(w, data, message)
}

func (s *Server) handleBindAiModelAgents(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	configID, err := pathInt64(r, "configId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	payload, err := decodeBody[struct {
		AgentIDs []int64 `json:"agentIds"`
	}](r)
	if err != nil {
		fail(w, err.Error())
		return
	}
	respond(w, nil, s.App.BindAiModelAgents(configID, payload.AgentIDs, principal), "模型绑定智能体已更新")
}

func (s *Server) handleAiModelMeta(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	ok(w, s.App.GetAiProviderMeta())
}

func (s *Server) handleListConfigAgents(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	ok(w, s.App.ListAssistantAgents(true))
}

func (s *Server) handleCreateConfigAgent(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	payload, err := decodeBody[service.AssistantAgentUpsertPayload](r)
	if err != nil {
		fail(w, err.Error())
		return
	}
	data, err := s.App.CreateAssistantAgent(*payload)
	respond(w, data, err, "智能体已创建")
}

func (s *Server) handleUpdateConfigAgent(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	agentID, err := pathInt64(r, "agentId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	payload, err := decodeBody[service.AssistantAgentUpsertPayload](r)
	if err != nil {
		fail(w, err.Error())
		return
	}
	data, err := s.App.UpdateAssistantAgent(agentID, *payload)
	respond(w, data, err, "智能体已更新")
}

func (s *Server) handleDeleteConfigAgent(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	agentID, err := pathInt64(r, "agentId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	respond(w, nil, s.App.DeleteAssistantAgent(agentID), "智能体已删除")
}

func (s *Server) handlePatchConfigAgent(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	agentID, err := pathInt64(r, "agentId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	payload, err := decodeBody[struct {
		IsEnabled bool `json:"isEnabled"`
	}](r)
	if err != nil {
		fail(w, err.Error())
		return
	}
	respond(w, nil, s.App.SetAssistantAgentEnabled(agentID, payload.IsEnabled), "智能体状态已更新")
}

func (s *Server) handleConfigAgentMeta(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	ok(w, s.App.GetAssistantAgentMeta())
}

func (s *Server) handleListNotifyChannels(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	ok(w, s.App.ListNotifyChannels(principal))
}

func (s *Server) handleCreateNotifyChannel(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	payload, err := decodeBody[service.NotifyChannelUpsertPayload](r)
	if err != nil {
		fail(w, err.Error())
		return
	}
	data, err := s.App.CreateNotifyChannel(*payload, principal)
	respond(w, data, err, "通知渠道已创建")
}

func (s *Server) handleUpdateNotifyChannel(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	channelID, err := pathInt64(r, "channelId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	payload, err := decodeBody[service.NotifyChannelUpsertPayload](r)
	if err != nil {
		fail(w, err.Error())
		return
	}
	data, err := s.App.UpdateNotifyChannel(channelID, *payload, principal)
	respond(w, data, err, "通知渠道已更新")
}

func (s *Server) handleDeleteNotifyChannel(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	channelID, err := pathInt64(r, "channelId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	respond(w, nil, s.App.DeleteNotifyChannel(channelID, principal), "通知渠道已删除")
}

func (s *Server) handlePatchNotifyChannel(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	channelID, err := pathInt64(r, "channelId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	payload, err := decodeBody[struct {
		IsEnabled bool `json:"isEnabled"`
	}](r)
	if err != nil {
		fail(w, err.Error())
		return
	}
	data, err := s.App.SetNotifyChannelEnabled(channelID, payload.IsEnabled, principal)
	respond(w, data, err, "通知渠道状态已更新")
}

func (s *Server) handleTestNotifyChannel(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	channelID, err := pathInt64(r, "channelId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	data, err := s.App.TestNotifyChannel(channelID, principal)
	if err != nil {
		fail(w, err.Error())
		return
	}
	message, _ := data["message"].(string)
	okMsg(w, data, message)
}

func (s *Server) handleNotifyChannelMeta(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	ok(w, s.App.GetNotifyChannelMeta())
}

// ---------- 助手 ----------

func (s *Server) handleAssistantAgents(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	data, err := s.App.ListAgentsForAssistant(principal)
	respond(w, data, err, "")
}

func (s *Server) handleAssistantModelOptions(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	agentCode := r.PathValue("agentCode")
	if !principalCanUseAgent(principal, agentCode) {
		failStatus(w, http.StatusForbidden, "无权使用当前智能体")
		return
	}
	data, err := s.App.ListAssistantModelOptions(principal, agentCode)
	respondAssistant(w, data, err)
}

func (s *Server) handleAssistantSessionCurrent(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	agentCode := queryStr(r, "agentCode")
	forceNew := strings.EqualFold(queryStr(r, "forceNew"), "true")
	data, err := s.App.GetOrCreateSession(principal, agentCode, queryInt64Ptr(r, "newsId"), queryInt64Ptr(r, "modelConfigId"), forceNew)
	respondAssistant(w, data, err)
}

func (s *Server) handleAssistantSessions(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	current := queryInt(r, "current", 1)
	size := clampSize(queryInt(r, "size", 10), 50)
	data, err := s.App.ListSessions(principal, queryStr(r, "agentCode"), current, size)
	respondAssistant(w, data, err)
}

func (s *Server) handleAssistantMessages(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	sessionID, err := pathInt64(r, "sessionId")
	if err != nil {
		failStatus(w, http.StatusBadRequest, err.Error())
		return
	}
	data, err := s.App.ListSessionMessages(principal, sessionID)
	respondAssistant(w, data, err)
}

func (s *Server) handleAssistantDeleteSession(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	sessionID, err := pathInt64(r, "sessionId")
	if err != nil {
		failStatus(w, http.StatusBadRequest, err.Error())
		return
	}
	data, err := s.App.DeleteSession(principal, sessionID)
	respondAssistant(w, data, err)
}

// handleAssistantStream SSE 流式对话。
func (s *Server) handleAssistantStream(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	sessionID, err := pathInt64(r, "sessionId")
	if err != nil {
		failStatus(w, http.StatusBadRequest, err.Error())
		return
	}
	payload, err := decodeBody[service.AssistantStreamPayload](r)
	if err != nil {
		failStatus(w, http.StatusBadRequest, err.Error())
		return
	}
	flusher, okFlusher := w.(http.Flusher)
	if !okFlusher {
		failStatus(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	emit := func(event service.StreamEvent) error {
		raw, _ := json.Marshal(event.Data)
		if _, err := w.Write([]byte("event: " + event.Name + "\ndata: " + string(raw) + "\n\n")); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}
	if err := s.App.StreamSession(principal, sessionID, *payload, emit); err != nil {
		// 流已经开始输出,只能以 error 事件收尾。
		_ = emit(service.StreamEvent{Name: "error", Data: M{"code": 400, "msg": err.Error()}})
		_ = emit(service.StreamEvent{Name: "done", Data: M{}})
	}
}

// respondAssistant 助手接口错误按 FastAPI HTTPException 风格返回 400/403。
func respondAssistant(w http.ResponseWriter, data any, err error) {
	if err != nil {
		if err == service.ErrPermission {
			failStatus(w, http.StatusForbidden, err.Error())
			return
		}
		failStatus(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, data)
}

func principalCanUseAgent(principal *service.Principal, agentCode string) bool {
	required := perm.AssistantAgentRequiredPermission[agentCode]
	return required == "" || principal.HasPermission(required)
}

// ---------- 站内通知 ----------

func (s *Server) handleListInApp(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	current := queryInt(r, "current", 1)
	size := clampSize(queryInt(r, "size", 20), 100)
	data, err := s.App.ListInAppNotifications(principal.User.ID, current, size)
	respond(w, data, err, "")
}

func (s *Server) handleReadInApp(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	deliveryID, err := pathInt64(r, "deliveryId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	if err := s.App.MarkInAppRead(principal.User.ID, deliveryID); err != nil {
		fail(w, err.Error())
		return
	}
	okMsg(w, M{"unreadCount": s.App.CountUnreadInApp(principal.User.ID)}, "通知已标记为已读")
}

func (s *Server) handleReadAllInApp(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	okMsg(w, s.App.MarkAllInAppRead(principal.User.ID), "全部通知已标记为已读")
}

func (s *Server) handleTestInApp(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	okMsg(w, s.App.SendTestInAppNotification(principal.User.ID), "测试通知已发送")
}

// ---------- WebSocket ----------

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

func (s *Server) handleNotificationsWS(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		token = extractBearerToken(r)
	}
	if token == "" {
		http.Error(w, "missing token", http.StatusUnauthorized)
		return
	}
	principal, err := s.App.AuthenticateAccessToken(token)
	if err != nil {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	userID := principal.User.ID
	s.App.Hub.Connect(userID, conn)
	defer func() {
		s.App.Hub.Disconnect(userID, conn)
		conn.Close()
	}()

	unreadCount := s.App.CountUnreadInApp(userID)
	initial, _ := json.Marshal(M{"type": "notice.unread", "unreadCount": unreadCount})
	if err := conn.WriteMessage(websocket.TextMessage, initial); err != nil {
		return
	}
	for {
		messageType, message, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if messageType == websocket.TextMessage && string(message) == "ping" {
			pong, _ := json.Marshal(M{"type": "pong"})
			if err := conn.WriteMessage(websocket.TextMessage, pong); err != nil {
				return
			}
		}
	}
}
