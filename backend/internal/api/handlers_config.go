package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gorilla/websocket"

	"coinsphere/backend/internal/perm"
	"coinsphere/backend/internal/service"
)

// ---------- 配置中心 ----------

// 本文件同样都是处理函数,签名 (w, r, principal) 见 handlers_scheduler.go 顶部说明。
// handleConfigOverview 处理 GET /api/v1/config/overview:把模型、智能体、通知汇总拼成一个 JSON 对象返回。
func (s *Server) handleConfigOverview(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	models, err := s.App.ListAiModelConfigs(principal)
	if err != nil {
		fail(w, err.Error())
		return
	}
	// M{...} 现场拼一个 map:键是前端要的字段名,值来自不同的业务方法。
	ok(w, M{
		"models":        models,
		"agents":        s.App.ListAssistantAgents(true),
		"notifySummary": s.App.GetNotifyOverviewSummary(principal),
	})
}

func (s *Server) handleListAiModels(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	data, err := s.App.ListAiModelConfigs(principal)
	respond(w, data, err, "")
}

// handleCreateAiModel 处理 POST /api/v1/config/ai-models:新建一个 AI 模型配置。
func (s *Server) handleCreateAiModel(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	// decodeBody 把请求体 JSON 解析成 AiModelUpsertPayload;下面的 *payload 是解引用,取出指针指向的结构体值传给业务层。
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
	data, err := s.App.ValidateAiModelConfig(r.Context(), configID, principal)
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
	data, err := s.App.TestNotifyChannel(r.Context(), channelID, principal)
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
	page, ok := cursorPage(w, r)
	if !ok {
		return
	}
	data, err := s.App.ListSessions(principal, queryStr(r, "agentCode"), page)
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

// handleAssistantStream 处理 POST .../sessions/{sessionId}/stream:用 SSE(Server-Sent Events)把 AI 回复一段段实时推给前端。
// SSE 就是不关闭响应、持续往里写 "event:.../data:..." 文本;和一次性返回 JSON 的普通接口不同。
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
	// w.(http.Flusher) 是"类型断言":试探 w 底层是否支持 Flusher(能把缓冲立刻刷给客户端)。okFlusher 为 false 说明不支持,无法流式。
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

	// emit 是一个存进变量的匿名函数(闭包):它"记住"了 w 和 flusher,每调用一次就推送一个 SSE 事件。
	// json.Marshal 把数据转成 JSON 字节;w.Write 写入响应;flusher.Flush() 立刻发出去,不等缓冲攒满。
	emit := func(event service.StreamEvent) error {
		raw, _ := json.Marshal(event.Data)
		if _, err := w.Write([]byte("event: " + event.Name + "\ndata: " + string(raw) + "\n\n")); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}
	if err := s.App.StreamSession(r.Context(), principal, sessionID, *payload, emit); err != nil {
		// 流已经开始输出,只能以 error 事件收尾。
		_ = emit(service.StreamEvent{Name: "error", Data: M{"code": 400, "msg": err.Error()}})
		_ = emit(service.StreamEvent{Name: "done", Data: M{}})
	}
}

// respondAssistant 是助手接口专用的统一返回:权限错误回 403,其它错误回 400(状态码本身非 200)。
// respondAssistant 助手接口错误按 FastAPI HTTPException 风格返回 400/403。
func respondAssistant(w http.ResponseWriter, data any, err error) {
	if err != nil {
		// 直接用 == 比较是否是 ErrPermission 这个"哨兵错误"(预先定义好的固定错误值)。
		if err == service.ErrPermission {
			failStatus(w, http.StatusForbidden, err.Error())
			return
		}
		failStatus(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, data)
}

// principalCanUseAgent 判断用户能否使用某个智能体:查出该智能体需要的权限码,为空表示不限,否则要求用户持有它。
func principalCanUseAgent(principal *service.Principal, agentCode string) bool {
	required := perm.AssistantAgentRequiredPermission[agentCode]
	return required == "" || principal.HasPermission(required)
}

// ---------- 站内通知 ----------

func (s *Server) handleListInApp(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	page, ok := cursorPage(w, r)
	if !ok {
		return
	}
	data, err := s.App.ListInAppNotifications(principal.User.ID, page)
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

const notificationsWebSocketProtocol = "coinsphere.notifications.v1"

// wsUpgrader 负责把普通 HTTP 连接升级成 WebSocket，并在握手阶段拒绝跨站来源。
var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     checkWebSocketOrigin,
	Subprotocols:    []string{notificationsWebSocketProtocol},
}

// checkWebSocketOrigin 按浏览器同源元组比较 scheme、主机和有效端口；缺失或含路径的 Origin 均拒绝。
func checkWebSocketOrigin(r *http.Request) bool {
	origins := r.Header.Values("Origin")
	if len(origins) != 1 {
		return false
	}
	origin, err := url.Parse(origins[0])
	if err != nil || origin.Scheme == "" || origin.Host == "" || origin.User != nil ||
		origin.Opaque != "" || origin.Path != "" || origin.RawPath != "" || origin.ForceQuery ||
		origin.RawQuery != "" || origin.Fragment != "" {
		return false
	}
	if !strings.EqualFold(origin.Scheme, "http") && !strings.EqualFold(origin.Scheme, "https") {
		return false
	}

	scheme, ok := effectiveRequestScheme(r)
	if !ok {
		return false
	}
	requestOrigin, err := url.Parse(scheme + "://" + r.Host)
	if err != nil || requestOrigin.Host == "" || requestOrigin.User != nil || requestOrigin.Opaque != "" ||
		requestOrigin.Path != "" || requestOrigin.RawPath != "" || requestOrigin.ForceQuery ||
		requestOrigin.RawQuery != "" || requestOrigin.Fragment != "" {
		return false
	}
	originPort, originPortOK := effectiveOriginPort(origin)
	requestPort, requestPortOK := effectiveOriginPort(requestOrigin)
	return originPortOK && requestPortOK &&
		strings.EqualFold(origin.Scheme, requestOrigin.Scheme) &&
		strings.EqualFold(origin.Hostname(), requestOrigin.Hostname()) &&
		originPort == requestPort
}

func effectiveRequestScheme(r *http.Request) (string, bool) {
	if r.TLS != nil {
		return "https", true
	}
	forwarded := r.Header.Values("X-Forwarded-Proto")
	if len(forwarded) == 0 {
		return "http", true
	}
	if len(forwarded) != 1 {
		return "", false
	}
	scheme := strings.ToLower(strings.TrimSpace(forwarded[0]))
	if scheme != "http" && scheme != "https" {
		return "", false
	}
	return scheme, true
}

func effectiveOriginPort(origin *url.URL) (string, bool) {
	if strings.HasSuffix(origin.Host, ":") {
		return "", false
	}
	if port := origin.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return "", false
		}
		return strconv.Itoa(value), true
	}
	if strings.EqualFold(origin.Scheme, "https") {
		return "443", true
	}
	return "80", true
}

// notificationWebSocketToken 要求固定协议在前、Access Token 在后；查询串一律拒绝，避免令牌进入访问日志。
func notificationWebSocketToken(r *http.Request) (string, bool) {
	if r.URL.RawQuery != "" || len(r.Header.Values("Sec-WebSocket-Protocol")) != 1 {
		return "", false
	}
	protocols := websocket.Subprotocols(r)
	if len(protocols) != 2 || protocols[0] != notificationsWebSocketProtocol {
		return "", false
	}
	token := strings.TrimSpace(protocols[1])
	return token, token != ""
}

// handleNotificationsWS 建立只回显固定协议的通知连接。
func (s *Server) handleNotificationsWS(w http.ResponseWriter, r *http.Request) {
	if !checkWebSocketOrigin(r) {
		writeProblem(w, r, http.StatusForbidden, "websocket origin forbidden")
		return
	}
	token, ok := notificationWebSocketToken(r)
	if !ok {
		writeProblem(w, r, http.StatusUnauthorized, "invalid websocket authentication")
		return
	}
	principal, err := s.App.AuthenticateAccessToken(token)
	if err != nil {
		writeProblem(w, r, http.StatusUnauthorized, "invalid access token")
		return
	}
	// Upgrade 把这次 HTTP 请求升级成 WebSocket 连接 conn;之后就用 conn 收发消息,不再用 w。
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	userID := principal.User.ID
	if !s.App.Hub.Connect(userID, conn, func() service.RealtimeEvent {
		return service.RealtimeEvent{
			Type: "notice.unread",
			Data: M{"unreadCount": s.App.CountUnreadInApp(userID)},
		}
	}) {
		_ = conn.Close()
		return
	}
	defer s.App.Hub.Disconnect(userID, conn)

	// 业务通道是单向通知；读循环只负责处理控制帧并触发 Hub 安装的 PongHandler。
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}
