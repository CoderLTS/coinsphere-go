// 本文件:智能助手的"会话(session)"与"消息(message)"逻辑 ——
// 新建/查找会话、拉取历史消息、把用户输入和 AI 的流式回复逐条落库。
// 真正调用模型的底层在 aigateway.go,上下文数据源在 agentsource.go,这里负责会话编排与消息拼装。
//
// 本文件里的 buildAgentMessages / runAgentOnce 是"与会话无关"的部分,
// 工作流的 assistant.agent 节点(nodes_agent.go)复用它们,不必再拼一遍提示词。

package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"coinsphere/backend/internal/db"
)

// ---------- 助手会话 ----------

// AssistantStreamPayload 流式对话载荷。
// struct + JSON tag:前端 POST 的 JSON 会按反引号里的 `json:"..."` 名字填进各字段
// (见 GO入门笔记『复合类型』)。NewsID/EnableReasoning 用指针 *int64/*bool,
// 是为了区分"前端没传"(nil)和"传了 0 / false"这两种情况。
type AssistantStreamPayload struct {
	AgentCode       string `json:"agentCode"`
	Mode            string `json:"mode"` // chat | analyze | retry
	Text            string `json:"text"`
	NewsID          *int64 `json:"newsId"`
	EnableReasoning *bool  `json:"enableReasoning"`
}

// (p *AssistantStreamPayload) 是方法接收者(见 GO入门笔记『方法与接收者』)。
// 逻辑:字段没传(nil)时默认开启;否则用 *p.EnableReasoning 顺着指针取出布尔值。
// (小写开头的 enableReasoning 只在本包内可见,见 GO入门笔记『module / package / import』。)
func (p *AssistantStreamPayload) enableReasoning() bool {
	return p.EnableReasoning == nil || *p.EnableReasoning
}

// analyzeMode 是否为"结构化分析"模式(analyze 首次、retry 重来一次)。
func (p *AssistantStreamPayload) analyzeMode() bool {
	return p.Mode == "analyze" || p.Mode == "retry"
}

// ListAgentsForAssistant 助手页可见的智能体列表(按权限过滤)。
func (a *App) ListAgentsForAssistant(principal *Principal) ([]M, error) {
	agents, err := a.ListRuntimeAgents(principal)
	if err != nil {
		return nil, err
	}
	result := make([]M, 0, len(agents))
	for _, item := range agents {
		code, _ := item["code"].(string)
		if agentAccessAllowed(principal, code) {
			result = append(result, item)
		}
	}
	return result, nil
}

// GetOrCreateSession 获取或创建当前用户会话。
// (a *App) 是方法接收者;参数里 newsID, modelConfigID *int64 是"可选的 int64"(nil 表示没给)。
// 返回 (M, error):M 是 map[string]any 的别名,用来拼给前端的 JSON(见 GO入门笔记『复合类型』)。
func (a *App) GetOrCreateSession(principal *Principal, agentCode string, newsID, modelConfigID *int64, forceNew bool) (M, error) {
	// := 短声明并接住 (结果, 错误);if err != nil 出错即返回(见 GO入门笔记『变量、函数、错误』)。
	agent, err := a.requireEnabledAgent(agentCode)
	if err != nil {
		return nil, err
	}
	if !agentAccessAllowed(principal, agent.Code) {
		return nil, ErrPermission
	}
	// 上下文由数据源注册表解析(见 agentsource.go);需要外部 id 却没给会在这里报错。
	agentCtx, err := a.resolveAgentContext(agent, newsID)
	if err != nil {
		return nil, err
	}
	// 只有真正需要外部实体的数据源才把 refID 记进会话,否则留 NULL。
	var refIDValue *int64
	if source := getAgentDataSource(agent.DataSourceType); source != nil && source.RequiresRefID {
		refIDValue = newsID
	}
	userID := principal.User.ID

	if !forceNew && modelConfigID == nil {
		latest := a.findSession(userID, agent.ID, refIDValue, nil, true)
		if latest != nil {
			return a.serializeSession(latest, agent), nil
		}
	}

	modelConfig, err := a.resolveModelForAgent(principal, agent.Code, modelConfigID)
	if err != nil {
		return nil, err
	}
	var session *db.AssistantSession
	if !forceNew {
		session = a.findSession(userID, agent.ID, refIDValue, &modelConfig.ID, false)
	}
	title := agent.DisplayName
	if agentCtx.Title != "" {
		title = agentCtx.Title
	}
	// 下面是"有则更新、无则新建"的经典分支:session 为 nil(没查到)就 Create 插入一条,
	// 否则 Updates 更新既有会话。&db.AssistantSession{...} 是 struct 字面量并取地址交给 GORM
	//(见 GO入门笔记『复合类型』『框架:GORM』)。
	now := time.Now()
	if session == nil {
		session = &db.AssistantSession{
			UserID: userID, AgentID: agent.ID, NewsID: refIDValue,
			ModelConfigID:            &modelConfig.ID,
			ModelDisplayNameSnapshot: modelConfig.DisplayName,
			ProviderLabelSnapshot:    modelConfig.ProviderName,
			Title:                    title,
			CreatedAt:                now, UpdatedAt: now, LastMessageAt: now,
		}
		if err := a.DB.Create(session).Error; err != nil {
			return nil, err
		}
	} else {
		updates := map[string]any{
			"title": title, "model_config_id": modelConfig.ID,
			"model_display_name_snapshot": modelConfig.DisplayName,
			"provider_label_snapshot":     modelConfig.ProviderName,
			"updated_at":                  now,
		}
		if err := a.DB.Model(session).Updates(updates).Error; err != nil {
			return nil, err
		}
		a.DB.First(session, session.ID)
	}
	return a.serializeSession(session, agent), nil
}

// ListSessionMessages 会话消息列表。
func (a *App) ListSessionMessages(principal *Principal, sessionID int64) ([]M, error) {
	session, err := a.requireSession(principal, sessionID)
	if err != nil {
		return nil, err
	}
	var messages []db.AssistantMessage
	a.DB.Where("session_id = ?", session.ID).Order("created_at ASC, id ASC").Find(&messages)
	result := make([]M, 0, len(messages))
	for i := range messages {
		result = append(result, serializeAssistantMessage(&messages[i]))
	}
	return result, nil
}

// ListSessions 会话历史分页。
func (a *App) ListSessions(principal *Principal, agentCode string, current, size int) (M, error) {
	var agent db.AssistantAgent
	if err := a.DB.Where("code = ?", agentCode).First(&agent).Error; err != nil {
		return nil, bizErr("智能体不存在")
	}
	if !agentAccessAllowed(principal, agent.Code) {
		return nil, ErrPermission
	}
	userID := principal.User.ID
	var total int64
	a.DB.Model(&db.AssistantSession{}).Where("user_id = ? AND agent_id = ?", userID, agent.ID).Count(&total)
	// GORM 链式查询可以跨多行:Where 条件、Order 排序、Offset/Limit 分页(跳过前几条、每页取几条),
	// 最后 Find 把结果写回 sessions(见 GO入门笔记『框架:GORM』)。? 是占位符,防 SQL 注入。
	var sessions []db.AssistantSession
	a.DB.Where("user_id = ? AND agent_id = ?", userID, agent.ID).
		Order("last_message_at DESC, updated_at DESC, id DESC").
		Offset((current - 1) * size).Limit(size).Find(&sessions)

	// 预览和条数一次性批量查出来。早先是在循环里对每个会话各查两次,
	// 一页 20 条就是 41 次查询(N+1)。
	sessionIDs := collectIDs(sessions, func(s db.AssistantSession) int64 { return s.ID })
	previewMap := a.latestMessagePreviews(sessionIDs)
	countMap := a.messageCountsBySessionIDs(sessionIDs)

	records := make([]M, 0, len(sessions))
	for i := range sessions {
		session := &sessions[i]
		item := a.serializeSession(session, &agent)
		item["messageCount"] = countMap[session.ID]
		item["latestPreview"] = truncateRunes(previewMap[session.ID], 160)
		records = append(records, item)
	}
	result := pagedResult(records, current, size, total)
	result["hasMore"] = int64(current*size) < total
	return result, nil
}

// DeleteSession 删除会话。
func (a *App) DeleteSession(principal *Principal, sessionID int64) (M, error) {
	session, err := a.requireSession(principal, sessionID)
	if err != nil {
		return nil, err
	}
	if err := a.DB.Delete(session).Error; err != nil {
		return nil, err
	}
	return M{"id": session.ID}, nil
}

// StreamEvent SSE 事件。
type StreamEvent struct {
	Name string
	Data M
}

// StreamSession 流式对话,通过 emit 逐事件输出。
//
// ctx 来自 HTTP 请求(见 handlers_config.go):用户关掉页面时它会被取消,
// 一路传到 streamAiChat 后底层连接立刻断开,不会继续从模型侧拉流。
// emit func(StreamEvent) error 是一个"回调函数"参数:每产生一个事件(用户消息/推理/正文/结束),
// 就调用 emit 推给前端。把函数当参数传,是 Go 里解耦"产生数据"和"如何发送"的常用手法。
func (a *App) StreamSession(ctx context.Context, principal *Principal, sessionID int64, payload AssistantStreamPayload, emit func(StreamEvent) error) error {
	session, agent, err := a.requireSessionWithAgent(principal, sessionID)
	if err != nil {
		return err
	}
	if agent.Code != payload.AgentCode {
		return bizErr("当前会话与请求的智能体不匹配")
	}
	if session.ModelConfigID == nil {
		return bizErr("当前会话绑定的模型已删除,请重新选择模型后创建新会话")
	}

	// emitError 是个匿名函数(闭包),封装"发一条 error 事件 + 一条 done 事件"的固定套路,
	// 后面出错时直接调它。StreamEvent{...}、M{...} 都是 struct/map 字面量。
	emitError := func(message string) error {
		if err := emit(StreamEvent{Name: "error", Data: M{"code": 400, "msg": message}}); err != nil {
			return err
		}
		return emit(StreamEvent{Name: "done", Data: M{}})
	}

	historyMessages := a.loadSessionHistory(session.ID)

	text := strings.TrimSpace(payload.Text)
	if payload.Mode == "chat" {
		if text == "" {
			return emitError("消息内容不能为空")
		}
		userMessage := db.AssistantMessage{
			SessionID: session.ID, Role: "user", ContentType: "text", Content: text, CreatedAt: time.Now(),
		}
		if err := a.DB.Create(&userMessage).Error; err != nil {
			return emitError(err.Error())
		}
		a.touchSession(session.ID)
		if err := emit(StreamEvent{Name: "user", Data: M{"message": serializeAssistantMessage(&userMessage)}}); err != nil {
			return err
		}
	} else {
		// 分析模式是"就这条数据重新分析一遍",不带聊天历史。
		historyMessages = nil
	}

	refID := payload.NewsID
	if refID == nil {
		refID = session.NewsID
	}
	agentCtx, err := a.resolveAgentContext(agent, refID)
	if err != nil {
		return emitError(err.Error())
	}

	source := getAgentDataSource(agent.DataSourceType)
	analyze := payload.analyzeMode() && source.supportsAnalyze()
	messages := buildAgentMessages(agent, historyMessages, analyze, text, agentCtx, payload.enableReasoning())
	runtimeConfig, err := a.getAiRuntimeConfig(*session.ModelConfigID, principal.User.ID, true)
	if err != nil {
		return emitError(err.Error())
	}

	contentType := "text"
	if analyze {
		contentType = "analysis_result"
	}

	// streamAiChat 每收到模型吐出的一小段就回调这个匿名函数:一边把片段 append 累加到切片里,
	// 一边通过 emit 实时推给前端。等整条流结束后再把所有片段拼成完整消息落库。
	var reasoningParts, contentParts []string
	usage := aiUsage{}
	streamErr := streamAiChat(ctx, runtimeConfig, messages, func(chunk aiChunk) error {
		if chunk.Usage != nil {
			mergeUsage(&usage, chunk.Usage)
		}
		if chunk.Reasoning != "" && payload.enableReasoning() {
			reasoningParts = append(reasoningParts, chunk.Reasoning)
			if err := emit(StreamEvent{Name: "reasoning", Data: M{"content": chunk.Reasoning}}); err != nil {
				return err
			}
		}
		if chunk.Content != "" {
			contentParts = append(contentParts, chunk.Content)
			if err := emit(StreamEvent{Name: "content", Data: M{"content": chunk.Content}}); err != nil {
				return err
			}
		}
		return nil
	})

	// 不论成功还是中途失败,只要已经收到过内容就落库。
	// 早先是"全部成功才落库",流断在中途时用户屏幕上已有半条回复,刷新后却凭空消失,
	// 而且下一轮对话的历史里也没有它。
	assistantMessage := a.persistAssistantReply(session.ID, contentType, contentParts, reasoningParts, usage)

	if streamErr != nil {
		return emitError(resolveStreamErrorMessage(streamErr))
	}
	if assistantMessage == nil {
		return emitError("模型没有返回任何内容,请稍后重试")
	}
	var latestSession db.AssistantSession
	if err := a.DB.First(&latestSession, session.ID).Error; err == nil {
		session = &latestSession
	}
	return emit(StreamEvent{Name: "done", Data: M{
		"message": serializeAssistantMessage(assistantMessage),
		"session": a.serializeSession(session, agent),
	}})
}

// ---------- 智能体调用(与会话无关,工作流节点也用) ----------

// buildAgentMessages 拼出发给模型的 messages 数组。
//
// 系统提示词由三段拼成:智能体自己的 SystemPrompt、是否展示推理过程的约束、数据源上下文。
// analyze 为 true 时不带聊天历史,只发一条"按格式分析"的指令(指令来自数据源登记项)。
func buildAgentMessages(agent *db.AssistantAgent, history []M, analyze bool, userText string, agentCtx agentContext, enableReasoning bool) []M {
	systemParts := []string{strings.TrimSpace(agent.SystemPrompt)}
	if enableReasoning {
		systemParts = append(systemParts, "如果模型支持推理,请输出适量思考过程,但最终结论必须明确。")
	} else {
		systemParts = append(systemParts, "如果用户没有开启深度思考,请不要展示推理过程,直接给出结论和建议。")
	}
	if agentCtx.Text != "" {
		systemParts = append(systemParts, agentCtx.Text)
	}
	systemContent := strings.Join(nonEmpty(systemParts), "\n\n")

	if analyze {
		source := getAgentDataSource(agent.DataSourceType)
		return []M{
			{"role": "system", "content": systemContent},
			{"role": "user", "content": source.AnalyzePrompt},
		}
	}
	messages := []M{{"role": "system", "content": systemContent}}
	messages = append(messages, history...)
	if strings.TrimSpace(userText) != "" {
		messages = append(messages, M{"role": "user", "content": strings.TrimSpace(userText)})
	}
	return messages
}

// agentRunResult 一次非流式智能体调用的结果。
type agentRunResult struct {
	Content   string
	Reasoning string
	Usage     aiUsage
}

// runAgentOnce 跑一次智能体并把整段回复收完再返回。
// 工作流节点用它:编排场景不需要逐字推送,拿到完整结果写进节点输出更有用。
func (a *App) runAgentOnce(ctx context.Context, runtimeConfig *aiRuntimeConfig, messages []M) (agentRunResult, error) {
	var contentParts, reasoningParts []string
	result := agentRunResult{}
	err := streamAiChat(ctx, runtimeConfig, messages, func(chunk aiChunk) error {
		if chunk.Usage != nil {
			mergeUsage(&result.Usage, chunk.Usage)
		}
		if chunk.Reasoning != "" {
			reasoningParts = append(reasoningParts, chunk.Reasoning)
		}
		if chunk.Content != "" {
			contentParts = append(contentParts, chunk.Content)
		}
		return nil
	})
	result.Content = strings.TrimSpace(strings.Join(contentParts, ""))
	result.Reasoning = strings.TrimSpace(strings.Join(reasoningParts, ""))
	return result, err
}

// ---------- 内部 ----------

// mergeUsage 累加 usage。三家协议给 usage 的时机不同(Anthropic 分两个事件分别给输入和输出),
// 所以按字段取最大值合并,而不是简单覆盖。
func mergeUsage(target *aiUsage, delta *aiUsage) {
	if delta.PromptTokens > target.PromptTokens {
		target.PromptTokens = delta.PromptTokens
	}
	if delta.CompletionTokens > target.CompletionTokens {
		target.CompletionTokens = delta.CompletionTokens
	}
	if delta.TotalTokens > target.TotalTokens {
		target.TotalTokens = delta.TotalTokens
	}
	// 协议没给显式 total(Anthropic)时用输入+输出补上。
	// 这里必须每次都重算:分两个事件给的话,第一次算出来的 total 只包含输入,是不完整的。
	if sum := target.PromptTokens + target.CompletionTokens; sum > target.TotalTokens {
		target.TotalTokens = sum
	}
}

// persistAssistantReply 把收到的回复落库;一个字都没收到就不建空记录,返回 nil。
func (a *App) persistAssistantReply(sessionID int64, contentType string, contentParts, reasoningParts []string, usage aiUsage) *db.AssistantMessage {
	// strings.Join 把片段切片拼接成一整条字符串(第二个参数是分隔符,这里用空串)。
	content := strings.TrimSpace(strings.Join(contentParts, ""))
	reasoning := strings.TrimSpace(strings.Join(reasoningParts, ""))
	if content == "" && reasoning == "" {
		return nil
	}
	message := db.AssistantMessage{
		SessionID: sessionID, Role: "assistant", ContentType: contentType,
		Content: content, Reasoning: reasoning,
		PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens, TotalTokens: usage.TotalTokens,
		CreatedAt: time.Now(),
	}
	if err := a.DB.Create(&message).Error; err != nil {
		return nil
	}
	a.touchSession(sessionID)
	return &message
}

// loadSessionHistory 读取会话历史并按上限截断。
//
// 上限很重要:模型每次调用都要重发全部历史,不截断的话成本随会话长度线性上涨,
// 最后直接撞上模型的上下文窗口报错。这里保留"最近 N 条",丢掉更早的。
func (a *App) loadSessionHistory(sessionID int64) []M {
	limit := a.Cfg.Assistant.HistoryMaxMessages
	if limit < 2 {
		limit = 40
	}
	// 先按时间倒序取最近 limit 条,再在内存里翻回正序交给模型。
	var recent []db.AssistantMessage
	a.DB.Where("session_id = ? AND role IN ?", sessionID, []string{"user", "assistant"}).
		Order("created_at DESC, id DESC").Limit(limit).Find(&recent)

	messages := make([]M, 0, len(recent))
	for i := len(recent) - 1; i >= 0; i-- {
		content := strings.TrimSpace(recent[i].Content)
		if content == "" {
			continue
		}
		messages = append(messages, M{"role": recent[i].Role, "content": content})
	}
	return messages
}

// latestMessagePreviews 批量取每个会话的最新一条消息预览(一次查询)。
func (a *App) latestMessagePreviews(sessionIDs []int64) map[int64]string {
	result := map[int64]string{}
	if len(sessionIDs) == 0 {
		return result
	}
	var rows []db.AssistantMessage
	a.DB.Where("session_id IN ?", sessionIDs).
		Order("session_id ASC, created_at DESC, id DESC").Find(&rows)
	for i := range rows {
		// 同一个 session 的第一行就是最新的那条(上面按 created_at DESC 排过)。
		if _, exists := result[rows[i].SessionID]; exists {
			continue
		}
		preview := strings.TrimSpace(rows[i].Content)
		if preview == "" {
			preview = strings.TrimSpace(rows[i].Reasoning)
		}
		result[rows[i].SessionID] = preview
	}
	return result
}

// messageCountsBySessionIDs 批量统计每个会话的消息条数(一次查询)。
func (a *App) messageCountsBySessionIDs(sessionIDs []int64) map[int64]int64 {
	result := map[int64]int64{}
	if len(sessionIDs) == 0 {
		return result
	}
	var rows []struct {
		SessionID int64
		Count     int64
	}
	a.DB.Model(&db.AssistantMessage{}).
		Select("session_id, COUNT(id) AS count").
		Where("session_id IN ?", sessionIDs).Group("session_id").Scan(&rows)
	for _, row := range rows {
		result[row.SessionID] = row.Count
	}
	return result
}

// requireSession 查当前用户自己的会话;查不到或不属于他就报错。
func (a *App) requireSession(principal *Principal, sessionID int64) (*db.AssistantSession, error) {
	var session db.AssistantSession
	if err := a.DB.Where("id = ? AND user_id = ?", sessionID, principal.User.ID).First(&session).Error; err != nil {
		return nil, bizErr("会话不存在或无权访问")
	}
	return &session, nil
}

// requireSessionWithAgent 在 requireSession 之上再取出会话绑定的智能体并校验访问权限。
func (a *App) requireSessionWithAgent(principal *Principal, sessionID int64) (*db.AssistantSession, *db.AssistantAgent, error) {
	session, err := a.requireSession(principal, sessionID)
	if err != nil {
		return nil, nil, err
	}
	var agent db.AssistantAgent
	if err := a.DB.First(&agent, session.AgentID).Error; err != nil {
		return nil, nil, bizErr("智能体不存在")
	}
	if !agentAccessAllowed(principal, agent.Code) {
		return nil, nil, ErrPermission
	}
	return session, &agent, nil
}

// findSession 按条件查一条会话,查不到返回 nil。返回 *db.AssistantSession 指针,
// 正是为了能用 nil 表示"没找到"(见 GO入门笔记『复合类型』的指针)。
func (a *App) findSession(userID, agentID int64, refID, modelConfigID *int64, byLatestMessage bool) *db.AssistantSession {
	// GORM 查询可以"攒条件":先把 a.DB.Where(...) 存进 query,再按需 query = query.Where(...) 追加。
	query := a.DB.Where("user_id = ? AND agent_id = ?", userID, agentID)
	if refID == nil {
		query = query.Where("news_id IS NULL")
	} else {
		query = query.Where("news_id = ?", *refID)
	}
	if modelConfigID != nil {
		query = query.Where("model_config_id = ?", *modelConfigID)
	}
	order := "updated_at DESC, id DESC"
	if byLatestMessage {
		order = "last_message_at DESC, updated_at DESC, id DESC"
	}
	var session db.AssistantSession
	if err := query.Order(order).First(&session).Error; err != nil {
		return nil
	}
	return &session
}

func (a *App) touchSession(sessionID int64) {
	now := time.Now()
	a.DB.Model(&db.AssistantSession{}).Where("id = ?", sessionID).
		Updates(map[string]any{"updated_at": now, "last_message_at": now})
}

// resolveStreamErrorMessage 把底层错误翻译成给用户看的话术。
//
// 优先按 HTTP 状态码判断 —— 状态码是明确信号。早先是在错误文本里搜 "free tier"、
// "permission denied" 这类关键词,模型换个措辞就失效,业务数据里出现同样的词还会误判。
func resolveStreamErrorMessage(err error) string {
	if errors.Is(err, context.Canceled) {
		return "对话已取消"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "模型响应超时,请稍后重试"
	}
	if httpErr := asAiHTTPError(err); httpErr != nil {
		switch httpErr.StatusCode {
		case http.StatusUnauthorized:
			return "模型密钥无效或已过期,请在模型配置中更新"
		case http.StatusForbidden:
			return "当前模型服务拒绝访问,请检查模型权限、密钥和配额配置"
		case http.StatusNotFound:
			return "模型不存在,请检查模型名称与接口地址"
		case http.StatusTooManyRequests:
			return "模型请求过于频繁或额度已用尽,请稍后重试或切换其他模型"
		}
		if httpErr.StatusCode >= 500 {
			return "模型服务暂时不可用,请稍后重试"
		}
		if httpErr.Message != "" {
			return httpErr.Message
		}
	}
	if message := err.Error(); message != "" {
		return message
	}
	return "智能体暂时不可用,请稍后重试"
}

func (a *App) serializeSession(session *db.AssistantSession, agent *db.AssistantAgent) M {
	var newsID, modelConfigID any
	if session.NewsID != nil {
		newsID = *session.NewsID
	}
	if session.ModelConfigID != nil {
		modelConfigID = *session.ModelConfigID
	}
	return M{
		"id": session.ID, "agentId": session.AgentID, "agentCode": agent.Code,
		"agentName": agent.DisplayName, "agentAvatar": agent.Avatar, "agentDescription": agent.Description,
		"title": session.Title, "newsId": newsID, "modelConfigId": modelConfigID,
		"modelDisplayName": session.ModelDisplayNameSnapshot, "providerName": session.ProviderLabelSnapshot,
		"createdAt": fmtTimeV(session.CreatedAt), "updatedAt": fmtTimeV(session.UpdatedAt),
		"lastMessageAt": fmtTimeV(session.LastMessageAt),
	}
}

func serializeAssistantMessage(message *db.AssistantMessage) M {
	return M{
		"id": message.ID, "role": message.Role, "contentType": message.ContentType,
		"content": message.Content, "reasoning": message.Reasoning,
		"promptTokens": message.PromptTokens, "completionTokens": message.CompletionTokens,
		"totalTokens": message.TotalTokens,
		"createdAt":   fmtTimeV(message.CreatedAt),
	}
}

func nonEmpty(items []string) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item) != "" {
			result = append(result, item)
		}
	}
	return result
}
