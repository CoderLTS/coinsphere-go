// 本文件:智能助手的"会话(session)"与"消息(message)"逻辑 ——
// 新建/查找会话、拉取历史消息、把用户输入和 AI 的流式回复逐条落库。
// 真正调用模型的底层在 aigateway.go,这里负责会话编排与消息拼装。

package service

import (
	"strings"
	"time"

	"coinsphere/backend/internal/db"
)

// ---------- 助手会话 ----------

// AssistantStreamPayload 流式对话载荷。
// struct + JSON tag:前端 POST 的 JSON 会按反引号里的 `json:"..."` 名字填进各字段
//(见 GO入门笔记『复合类型』)。NewsID/EnableReasoning 用指针 *int64/*bool,
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
//(小写开头的 enableReasoning 只在本包内可见,见 GO入门笔记『module / package / import』。)
func (p *AssistantStreamPayload) enableReasoning() bool {
	return p.EnableReasoning == nil || *p.EnableReasoning
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
	news, err := a.resolveAgentNews(agent, newsID)
	if err != nil {
		return nil, err
	}
	// &news.ID 取字段地址得到一个 *int64。用指针是因为会话的 NewsID 这一列允许为空(NULL)。
	var newsIDValue *int64
	if news != nil {
		newsIDValue = &news.ID
	}
	userID := principal.User.ID

	if !forceNew && modelConfigID == nil {
		latest := a.findSession(userID, agent.ID, newsIDValue, nil, true)
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
		session = a.findSession(userID, agent.ID, newsIDValue, &modelConfig.ID, false)
	}
	title := agent.DisplayName
	if news != nil && news.Title != "" {
		title = news.Title
	}
	// 下面是"有则更新、无则新建"的经典分支:session 为 nil(没查到)就 Create 插入一条,
	// 否则 Updates 更新既有会话。&db.AssistantSession{...} 是 struct 字面量并取地址交给 GORM
	//(见 GO入门笔记『复合类型』『框架:GORM』)。
	now := time.Now()
	if session == nil {
		session = &db.AssistantSession{
			UserID: userID, AgentID: agent.ID, NewsID: newsIDValue,
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
	session, agent, err := a.requireSessionWithAgent(principal, sessionID)
	if err != nil {
		return nil, err
	}
	// _ = agent:这里用不到 agent,但函数签名要求接住它;赋给 _ 表示"故意忽略",
	// 否则 Go 会因为"变量声明了却没用"而编译不过(见 GO入门笔记『其它小语法』的 _)。
	_ = agent
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
	records := make([]M, 0, len(sessions))
	for i := range sessions {
		session := &sessions[i]
		item := a.serializeSession(session, &agent)
		var latest db.AssistantMessage
		preview := ""
		if err := a.DB.Where("session_id = ?", session.ID).Order("created_at DESC, id DESC").First(&latest).Error; err == nil {
			preview = strings.TrimSpace(latest.Content)
			if preview == "" {
				preview = strings.TrimSpace(latest.Reasoning)
			}
		}
		var messageCount int64
		a.DB.Model(&db.AssistantMessage{}).Where("session_id = ?", session.ID).Count(&messageCount)
		item["messageCount"] = messageCount
		item["latestPreview"] = truncateRunes(preview, 160)
		records = append(records, item)
	}
	result := pagedResult(records, current, size, total)
	result["hasMore"] = int64(current*size) < total
	return result, nil
}

// DeleteSession 删除会话。
func (a *App) DeleteSession(principal *Principal, sessionID int64) (M, error) {
	session, _, err := a.requireSessionWithAgent(principal, sessionID)
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
// emit func(StreamEvent) error 是一个"回调函数"参数:每产生一个事件(用户消息/推理/正文/结束),
// 就调用 emit 推给前端。把函数当参数传,是 Go 里解耦"产生数据"和"如何发送"的常用手法。
func (a *App) StreamSession(principal *Principal, sessionID int64, payload AssistantStreamPayload, emit func(StreamEvent) error) error {
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

	var history []db.AssistantMessage
	a.DB.Where("session_id = ?", session.ID).Order("created_at ASC, id ASC").Find(&history)
	// range 遍历历史消息切片(_ 丢弃下标,只要值 item);continue 跳过不需要的记录。
	// 把每条整理成 {role, content} 塞进 historyMessages,作为发给模型的上下文。
	historyMessages := make([]M, 0, len(history))
	for _, item := range history {
		if item.Role != "user" && item.Role != "assistant" {
			continue
		}
		content := strings.TrimSpace(item.Content)
		if content == "" {
			continue
		}
		historyMessages = append(historyMessages, M{"role": item.Role, "content": content})
	}

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
		historyMessages = nil
	}

	newsID := payload.NewsID
	if newsID == nil {
		newsID = session.NewsID
	}
	news, err := a.resolveAgentNews(agent, newsID)
	if err != nil {
		return emitError(err.Error())
	}

	messages := buildAssistantMessages(agent, historyMessages, payload.Mode, text, news, payload.enableReasoning())
	runtimeConfig, err := a.getAiRuntimeConfig(*session.ModelConfigID, principal.User.ID, true)
	if err != nil {
		return emitError(err.Error())
	}

	// streamAiChat 每收到模型吐出的一小段就回调这个匿名函数:一边把片段 append 累加到切片里,
	// 一边通过 emit 实时推给前端。等整条流结束后再把所有片段拼成完整消息落库。
	var reasoningParts, contentParts []string
	streamErr := streamAiChat(runtimeConfig, messages, func(chunk aiChunk) error {
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
	if streamErr != nil {
		return emitError(resolveStreamErrorMessage(streamErr))
	}

	contentType := "text"
	if agent.DataSourceType == agentDataSourceNewsContext && (payload.Mode == "analyze" || payload.Mode == "retry") {
		contentType = "analysis_result"
	}
	// strings.Join 把片段切片拼接成一整条字符串(第二个参数是分隔符,这里用空串)。
	assistantMessage := db.AssistantMessage{
		SessionID: session.ID, Role: "assistant", ContentType: contentType,
		Content:   strings.TrimSpace(strings.Join(contentParts, "")),
		Reasoning: strings.TrimSpace(strings.Join(reasoningParts, "")),
		CreatedAt: time.Now(),
	}
	if err := a.DB.Create(&assistantMessage).Error; err != nil {
		return emitError(err.Error())
	}
	a.touchSession(session.ID)
	var latestSession db.AssistantSession
	if err := a.DB.First(&latestSession, session.ID).Error; err == nil {
		session = &latestSession
	}
	return emit(StreamEvent{Name: "done", Data: M{
		"message": serializeAssistantMessage(&assistantMessage),
		"session": a.serializeSession(session, agent),
	}})
}

// ---------- 内部 ----------

func (a *App) requireSessionWithAgent(principal *Principal, sessionID int64) (*db.AssistantSession, *db.AssistantAgent, error) {
	var session db.AssistantSession
	if err := a.DB.Where("id = ? AND user_id = ?", sessionID, principal.User.ID).First(&session).Error; err != nil {
		return nil, nil, bizErr("会话不存在或无权访问")
	}
	var agent db.AssistantAgent
	if err := a.DB.First(&agent, session.AgentID).Error; err != nil {
		return nil, nil, bizErr("智能体不存在")
	}
	if !agentAccessAllowed(principal, agent.Code) {
		return nil, nil, ErrPermission
	}
	return &session, &agent, nil
}

func (a *App) resolveAgentNews(agent *db.AssistantAgent, newsID *int64) (*db.BlockbeatsNews, error) {
	if agent.DataSourceType != agentDataSourceNewsContext {
		return nil, nil
	}
	if newsID == nil || *newsID <= 0 {
		return nil, bizErr("智能体 %s 需要 newsId 参数", agent.Code)
	}
	var news db.BlockbeatsNews
	if err := a.DB.First(&news, *newsID).Error; err != nil {
		return nil, bizErr("新闻不存在或已删除")
	}
	return &news, nil
}

// findSession 按条件查一条会话,查不到返回 nil。返回 *db.AssistantSession 指针,
// 正是为了能用 nil 表示"没找到"(见 GO入门笔记『复合类型』的指针)。
func (a *App) findSession(userID, agentID int64, newsID, modelConfigID *int64, byLatestMessage bool) *db.AssistantSession {
	// GORM 查询可以"攒条件":先把 a.DB.Where(...) 存进 query,再按需 query = query.Where(...) 追加。
	query := a.DB.Where("user_id = ? AND agent_id = ?", userID, agentID)
	if newsID == nil {
		query = query.Where("news_id IS NULL")
	} else {
		query = query.Where("news_id = ?", *newsID)
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

func buildAssistantMessages(agent *db.AssistantAgent, history []M, mode, userText string, news *db.BlockbeatsNews, enableReasoning bool) []M {
	systemParts := []string{strings.TrimSpace(agent.SystemPrompt)}
	if enableReasoning {
		systemParts = append(systemParts, "如果模型支持推理,请输出适量思考过程,但最终结论必须明确。")
	} else {
		systemParts = append(systemParts, "如果用户没有开启深度思考,请不要展示推理过程,直接给出结论和建议。")
	}
	if context := buildDataSourceContext(agent, news); context != "" {
		systemParts = append(systemParts, context)
	}
	systemContent := strings.Join(nonEmpty(systemParts), "\n\n")

	if news != nil && (mode == "analyze" || mode == "retry") {
		return []M{
			{"role": "system", "content": systemContent},
			{"role": "user", "content": "请对这条新闻做结构化分析,并遵守以下要求:\n" +
				"1. 第一行必须以【利多】或【利空】开头。\n" +
				"2. 使用 3 点说明判断理由。\n" +
				"3. 给出 3 条具体可执行的后续建议。\n" +
				"4. 总字数控制在 500 字以内。"},
		}
	}
	messages := []M{{"role": "system", "content": systemContent}}
	messages = append(messages, history...)
	if strings.TrimSpace(userText) != "" {
		messages = append(messages, M{"role": "user", "content": strings.TrimSpace(userText)})
	}
	return messages
}

func buildDataSourceContext(agent *db.AssistantAgent, news *db.BlockbeatsNews) string {
	switch agent.DataSourceType {
	case agentDataSourceSystemContext:
		return "当前系统是 coinsphere,主要包含首页总览、定时任务、数据管理、配置管理、系统管理、" +
			"站内通知、新闻同步和智能助手等能力。回答时请优先围绕这些真实功能展开。"
	case agentDataSourceNewsContext:
		if news == nil {
			return ""
		}
		return "当前新闻上下文如下:\n" +
			"标题:" + news.Title + "\n" +
			"发布时间:" + fmtTime(news.PublishedAt) + "\n" +
			"正文:" + strings.TrimSpace(news.Content) + "\n" +
			"原文链接:" + news.OriginalURL
	default:
		return ""
	}
}

func resolveStreamErrorMessage(err error) string {
	message := err.Error()
	lowered := strings.ToLower(message)
	if strings.Contains(lowered, "allocationquota.freetieronly") || strings.Contains(lowered, "free tier") {
		return "当前模型的免费额度已用尽,请切换其他模型后重试"
	}
	if strings.Contains(lowered, "permission denied") {
		return "当前模型服务拒绝访问,请检查模型权限、密钥和配额配置"
	}
	if message == "" {
		return "智能体暂时不可用,请稍后重试"
	}
	return message
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
		"createdAt": fmtTimeV(message.CreatedAt),
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
