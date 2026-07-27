package service

import (
	"encoding/json"
	"strings"
	"time"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/security"
)

// AI 协议类型与智能体数据源常量。
const (
	aiProviderOpenAICompatible = "openai_compatible"
	aiProviderAnthropic        = "anthropic"
	aiProviderGemini           = "gemini"

	agentDataSourceNone          = "none"
	agentDataSourceSystemContext = "system_context"
	agentDataSourceNewsContext   = "news_context"
)

var aiProviderTypes = map[string]bool{
	aiProviderOpenAICompatible: true, aiProviderAnthropic: true, aiProviderGemini: true,
}

var agentDataSourceTypes = map[string]bool{
	agentDataSourceNone: true, agentDataSourceSystemContext: true, agentDataSourceNewsContext: true,
}

var builtinAgentCodes = map[string]bool{"system_general": true, "news_analysis": true}

// AiModelUpsertPayload 模型配置载荷。
type AiModelUpsertPayload struct {
	Provider           string `json:"provider"`
	ProviderName       string `json:"providerName"`
	DisplayName        string `json:"displayName"`
	ModelIdentifier    string `json:"modelIdentifier"`
	BaseURL            string `json:"baseUrl"`
	APIKey             string `json:"apiKey"`
	IsEnabled          *bool  `json:"isEnabled"`
	Priority           int    `json:"priority"`
	RequestHeadersJSON string `json:"requestHeadersJson"`
	RequestBodyJSON    string `json:"requestBodyJson"`
	TimeoutMs          int    `json:"timeoutMs"`
	Remark             string `json:"remark"`
}

// ListAiModelConfigs 当前用户的模型配置列表。
func (a *App) ListAiModelConfigs(principal *Principal) ([]M, error) {
	configs, err := a.listModelConfigs(principal.User.ID, false)
	if err != nil {
		return nil, err
	}
	var agents []db.AssistantAgent
	a.DB.Order("sort ASC, id ASC").Find(&agents)
	agentMap := map[int64]*db.AssistantAgent{}
	for i := range agents {
		agentMap[agents[i].ID] = &agents[i]
	}
	bindingMap := a.listBoundAgentIDsByModelIDs(collectIDs(configs, func(c db.SystemAiModelConfig) int64 { return c.ID }))

	result := make([]M, 0, len(configs))
	for i := range configs {
		result = append(result, a.serializeModelConfig(&configs[i], bindingMap, agentMap))
	}
	return result, nil
}

// CreateAiModelConfig 创建模型配置。
func (a *App) CreateAiModelConfig(payload AiModelUpsertPayload, principal *Principal) (M, error) {
	fields, err := a.buildModelUpsert(payload, nil)
	if err != nil {
		return nil, err
	}
	config := db.SystemAiModelConfig{OwnerID: principal.User.ID, CreatedAt: time.Now()}
	if err := a.DB.Create(&config).Error; err != nil {
		return nil, err
	}
	if err := a.DB.Model(&config).Updates(fields).Error; err != nil {
		return nil, err
	}
	return M{"id": config.ID}, nil
}

// UpdateAiModelConfig 更新模型配置。
func (a *App) UpdateAiModelConfig(configID int64, payload AiModelUpsertPayload, principal *Principal) (M, error) {
	config, err := a.requireModelConfig(configID, principal.User.ID)
	if err != nil {
		return nil, err
	}
	fields, err := a.buildModelUpsert(payload, config)
	if err != nil {
		return nil, err
	}
	if err := a.DB.Model(config).Updates(fields).Error; err != nil {
		return nil, err
	}
	return M{"id": configID}, nil
}

// DeleteAiModelConfig 删除模型配置。
func (a *App) DeleteAiModelConfig(configID int64, principal *Principal) error {
	config, err := a.requireModelConfig(configID, principal.User.ID)
	if err != nil {
		return err
	}
	return a.DB.Delete(config).Error
}

// SetAiModelEnabled 启停模型配置。
func (a *App) SetAiModelEnabled(configID int64, enabled bool, principal *Principal) error {
	config, err := a.requireModelConfig(configID, principal.User.ID)
	if err != nil {
		return err
	}
	return a.DB.Model(config).Updates(map[string]any{"is_enabled": enabled, "updated_at": time.Now()}).Error
}

// ValidateAiModelConfig 连通性校验。
func (a *App) ValidateAiModelConfig(configID int64, principal *Principal) (M, error) {
	if _, err := a.requireModelConfig(configID, principal.User.ID); err != nil {
		return nil, err
	}
	runtime, err := a.getAiRuntimeConfig(configID, principal.User.ID, false)
	if err != nil {
		return nil, err
	}
	ok, message := validateAiConfig(runtime)
	status := "failed"
	if ok {
		status = "success"
	}
	now := time.Now()
	a.DB.Model(&db.SystemAiModelConfig{}).Where("id = ? AND owner_id = ?", configID, principal.User.ID).
		Updates(map[string]any{
			"last_validation_status": status, "last_validation_message": message,
			"last_validated_at": now, "updated_at": now,
		})
	return M{"success": ok, "status": status, "message": message, "validatedAt": fmtTimeV(now)}, nil
}

// BindAiModelAgents 更新模型绑定的智能体列表。
func (a *App) BindAiModelAgents(configID int64, agentIDs []int64, principal *Principal) error {
	if _, err := a.requireModelConfig(configID, principal.User.ID); err != nil {
		return err
	}
	unique := dedupeInt64(agentIDs)
	if len(unique) > 0 {
		var count int64
		a.DB.Model(&db.AssistantAgent{}).Where("id IN ?", unique).Count(&count)
		if int(count) != len(unique) {
			return bizErr("部分智能体不存在,无法完成绑定")
		}
	}
	if err := a.DB.Where("model_config_id = ?", configID).Delete(&db.AiModelAgentBinding{}).Error; err != nil {
		return err
	}
	for _, agentID := range unique {
		if err := a.DB.Create(&db.AiModelAgentBinding{ModelConfigID: configID, AgentID: agentID, CreatedAt: time.Now()}).Error; err != nil {
			return err
		}
	}
	return nil
}

// GetAiProviderMeta 模型配置页元数据。
func (a *App) GetAiProviderMeta() M {
	fullFields := []string{
		"providerName", "displayName", "modelIdentifier", "baseUrl", "apiKey",
		"requestHeadersJson", "requestBodyJson", "timeoutMs", "priority", "remark",
	}
	basicFields := []string{"providerName", "displayName", "modelIdentifier", "baseUrl", "apiKey", "timeoutMs", "priority", "remark"}
	providerOptions := []M{
		{
			"value": aiProviderOpenAICompatible, "label": "OpenAI 兼容",
			"description": "适用于 OpenAI、DeepSeek、阿里百炼、智谱、Moonshot、OpenRouter、SiliconFlow 等兼容接口。",
			"fields":      fullFields,
		},
		{
			"value": aiProviderAnthropic, "label": "Anthropic Claude",
			"description": "适用于 Claude 原生 Messages API。", "fields": basicFields,
		},
		{
			"value": aiProviderGemini, "label": "Google Gemini",
			"description": "适用于 Gemini 原生 GenerateContent / streamGenerateContent API。", "fields": basicFields,
		},
	}
	preset := func(provider, providerName, displayName, model, baseURL, extraBody string) M {
		return M{
			"provider": provider, "providerName": providerName, "displayName": displayName,
			"modelIdentifier": model, "baseUrl": baseURL,
			"requestHeadersJson": "{}", "requestBodyJson": extraBody,
		}
	}
	presets := []M{
		preset(aiProviderOpenAICompatible, "OpenAI", "OpenAI GPT-4o-mini", "gpt-4o-mini", "https://api.openai.com/v1", "{}"),
		preset(aiProviderOpenAICompatible, "DeepSeek", "DeepSeek Chat", "deepseek-chat", "https://api.deepseek.com/v1", "{}"),
		preset(aiProviderOpenAICompatible, "阿里百炼", "通义千问 / DeepSeek", "deepseek-v3.2-exp", "https://dashscope.aliyuncs.com/compatible-mode/v1", "{\"enable_thinking\": true}"),
		preset(aiProviderOpenAICompatible, "智谱 AI", "GLM-4-Flash", "glm-4-flash", "https://open.bigmodel.cn/api/paas/v4", "{}"),
		preset(aiProviderOpenAICompatible, "Moonshot", "Kimi K2", "kimi-k2-0711-preview", "https://api.moonshot.cn/v1", "{}"),
		preset(aiProviderOpenAICompatible, "OpenRouter", "OpenRouter 自定义模型", "openai/gpt-4o-mini", "https://openrouter.ai/api/v1", "{}"),
		preset(aiProviderOpenAICompatible, "SiliconFlow", "SiliconFlow 自定义模型", "Qwen/Qwen2.5-72B-Instruct", "https://api.siliconflow.cn/v1", "{}"),
		preset(aiProviderOpenAICompatible, "自定义 OpenAI 兼容", "自定义兼容模型", "", "", "{}"),
		preset(aiProviderAnthropic, "Anthropic", "Claude 3.7 Sonnet", "claude-3-7-sonnet-latest", "https://api.anthropic.com", "{}"),
		preset(aiProviderGemini, "Google", "Gemini 2.0 Flash", "gemini-2.0-flash", "https://generativelanguage.googleapis.com", "{}"),
	}
	return M{"providerOptions": providerOptions, "presets": presets}
}

// ---------- 智能体管理 ----------

// AssistantAgentUpsertPayload 智能体载荷。
type AssistantAgentUpsertPayload struct {
	Code           string   `json:"code"`
	DisplayName    string   `json:"displayName"`
	Avatar         string   `json:"avatar"`
	Description    string   `json:"description"`
	SystemPrompt   string   `json:"systemPrompt"`
	WelcomeMessage string   `json:"welcomeMessage"`
	StarterPrompts []string `json:"starterPrompts"`
	DataSourceType string   `json:"dataSourceType"`
	IsEnabled      *bool    `json:"isEnabled"`
	Sort           int      `json:"sort"`
}

// ListAssistantAgents 智能体列表(含统计)。
func (a *App) ListAssistantAgents(includeDisabled bool) []M {
	query := a.DB.Order("sort ASC, id ASC")
	if !includeDisabled {
		query = query.Where("is_enabled = ?", true)
	}
	var agents []db.AssistantAgent
	query.Find(&agents)
	result := make([]M, 0, len(agents))
	for i := range agents {
		result = append(result, a.serializeAgent(&agents[i]))
	}
	return result
}

// ListRuntimeAgents 助手页可用智能体(带默认模型)。
func (a *App) ListRuntimeAgents(principal *Principal) ([]M, error) {
	var agents []db.AssistantAgent
	a.DB.Where("is_enabled = ?", true).Order("sort ASC, id ASC").Find(&agents)
	models, err := a.listModelConfigs(principal.User.ID, true)
	if err != nil {
		return nil, err
	}
	modelIDs := collectIDs(models, func(c db.SystemAiModelConfig) int64 { return c.ID })
	bindingMap := a.listBoundAgentIDsByModelIDs(modelIDs)
	agentBindingMap := map[int64][]int64{}
	for modelID, agentIDs := range bindingMap {
		for _, agentID := range agentIDs {
			agentBindingMap[agentID] = append(agentBindingMap[agentID], modelID)
		}
	}
	var fallbackModelID *int64
	if len(models) > 0 {
		fallbackModelID = &models[0].ID
	}
	records := make([]M, 0, len(agents))
	for i := range agents {
		item := a.serializeAgent(&agents[i])
		var defaultModelID *int64
		if bound := agentBindingMap[agents[i].ID]; len(bound) > 0 {
			defaultModelID = &bound[0]
		} else {
			defaultModelID = fallbackModelID
		}
		if defaultModelID != nil {
			item["defaultModelId"] = *defaultModelID
			item["hasUsableModel"] = true
		} else {
			item["defaultModelId"] = nil
			item["hasUsableModel"] = false
		}
		records = append(records, item)
	}
	return records, nil
}

// CreateAssistantAgent 创建智能体。
func (a *App) CreateAssistantAgent(payload AssistantAgentUpsertPayload) (M, error) {
	fields, err := buildAgentFields(payload)
	if err != nil {
		return nil, err
	}
	agent := db.AssistantAgent{Code: strings.TrimSpace(payload.Code), CreatedAt: time.Now()}
	if err := a.DB.Create(&agent).Error; err != nil {
		return nil, err
	}
	if err := a.DB.Model(&agent).Updates(fields).Error; err != nil {
		return nil, err
	}
	return M{"id": agent.ID}, nil
}

// UpdateAssistantAgent 更新智能体。
func (a *App) UpdateAssistantAgent(agentID int64, payload AssistantAgentUpsertPayload) (M, error) {
	var agent db.AssistantAgent
	if err := a.DB.First(&agent, agentID).Error; err != nil {
		return nil, bizErr("智能体不存在")
	}
	fields, err := buildAgentFields(payload)
	if err != nil {
		return nil, err
	}
	if err := a.DB.Model(&agent).Updates(fields).Error; err != nil {
		return nil, err
	}
	return M{"id": agentID}, nil
}

// DeleteAssistantAgent 删除智能体。
func (a *App) DeleteAssistantAgent(agentID int64) error {
	var agent db.AssistantAgent
	if err := a.DB.First(&agent, agentID).Error; err != nil {
		return bizErr("智能体不存在")
	}
	if builtinAgentCodes[agent.Code] {
		return bizErr("内置智能体不允许删除")
	}
	var sessionCount int64
	a.DB.Model(&db.AssistantSession{}).Where("agent_id = ?", agentID).Count(&sessionCount)
	if sessionCount > 0 {
		return bizErr("该智能体已经产生历史会话,暂不允许删除")
	}
	if err := a.DB.Where("agent_id = ?", agentID).Delete(&db.AiModelAgentBinding{}).Error; err != nil {
		return err
	}
	return a.DB.Delete(&agent).Error
}

// SetAssistantAgentEnabled 启停智能体。
func (a *App) SetAssistantAgentEnabled(agentID int64, enabled bool) error {
	var agent db.AssistantAgent
	if err := a.DB.First(&agent, agentID).Error; err != nil {
		return bizErr("智能体不存在")
	}
	return a.DB.Model(&agent).Updates(map[string]any{"is_enabled": enabled, "updated_at": time.Now()}).Error
}

// GetAssistantAgentMeta 智能体配置页元数据。
func (a *App) GetAssistantAgentMeta() M {
	var agents []db.AssistantAgent
	a.DB.Order("sort ASC, id ASC").Find(&agents)
	options := make([]M, 0, len(agents))
	for _, agent := range agents {
		options = append(options, M{
			"id": agent.ID, "code": agent.Code, "displayName": agent.DisplayName, "isEnabled": agent.IsEnabled,
		})
	}
	return M{
		"dataSourceOptions": []M{
			{"value": agentDataSourceNone, "label": "无额外数据源"},
			{"value": agentDataSourceSystemContext, "label": "系统上下文"},
			{"value": agentDataSourceNewsContext, "label": "新闻上下文"},
		},
		"agentOptions":      options,
		"builtinAgentCodes": []string{"news_analysis", "system_general"},
	}
}

// ListAssistantModelOptions 某智能体可用模型选项。
func (a *App) ListAssistantModelOptions(principal *Principal, agentCode string) (M, error) {
	agent, err := a.requireEnabledAgent(agentCode)
	if err != nil {
		return nil, err
	}
	configs, err := a.listModelConfigs(principal.User.ID, true)
	if err != nil {
		return nil, err
	}
	defaultModel := a.pickDefaultModel(principal.User.ID, agent.ID, configs)
	models := make([]M, 0, len(configs))
	for _, config := range configs {
		models = append(models, M{
			"id": config.ID, "displayName": config.DisplayName, "providerName": config.ProviderName,
			"provider": config.Provider, "modelIdentifier": config.ModelIdentifier, "priority": config.Priority,
		})
	}
	var defaultModelID any
	if defaultModel != nil {
		defaultModelID = defaultModel.ID
	}
	return M{"agentCode": agent.Code, "defaultModelId": defaultModelID, "models": models}, nil
}

// ---------- 内部 ----------

func (a *App) listModelConfigs(ownerUserID int64, enabledOnly bool) ([]db.SystemAiModelConfig, error) {
	query := a.DB.Where("owner_id = ?", ownerUserID).Order("priority ASC, updated_at DESC, id DESC")
	if enabledOnly {
		query = query.Where("is_enabled = ?", true)
	}
	var configs []db.SystemAiModelConfig
	err := query.Find(&configs).Error
	return configs, err
}

func (a *App) requireModelConfig(configID, ownerUserID int64) (*db.SystemAiModelConfig, error) {
	var config db.SystemAiModelConfig
	if err := a.DB.Where("id = ? AND owner_id = ?", configID, ownerUserID).First(&config).Error; err != nil {
		return nil, bizErr("模型配置不存在")
	}
	return &config, nil
}

func (a *App) requireEnabledAgent(agentCode string) (*db.AssistantAgent, error) {
	var agent db.AssistantAgent
	if err := a.DB.Where("code = ? AND is_enabled = ?", agentCode, true).First(&agent).Error; err != nil {
		return nil, bizErr("智能体不存在或已停用")
	}
	return &agent, nil
}

func (a *App) listBoundAgentIDsByModelIDs(modelIDs []int64) map[int64][]int64 {
	result := map[int64][]int64{}
	if len(modelIDs) == 0 {
		return result
	}
	var bindings []db.AiModelAgentBinding
	a.DB.Where("model_config_id IN ?", modelIDs).Order("id ASC").Find(&bindings)
	for _, binding := range bindings {
		result[binding.ModelConfigID] = append(result[binding.ModelConfigID], binding.AgentID)
	}
	return result
}

func (a *App) pickDefaultModel(ownerUserID, agentID int64, enabledConfigs []db.SystemAiModelConfig) *db.SystemAiModelConfig {
	var bound []db.SystemAiModelConfig
	a.DB.
		Joins("JOIN ai_model_agent_bindings ON ai_model_agent_bindings.model_config_id = ai_model_configs.id").
		Where("ai_model_configs.owner_id = ? AND ai_model_agent_bindings.agent_id = ? AND ai_model_configs.is_enabled = ?", ownerUserID, agentID, true).
		Order("ai_model_configs.priority ASC, ai_model_configs.updated_at DESC, ai_model_configs.id ASC").
		Find(&bound)
	if len(bound) > 0 {
		return &bound[0]
	}
	if len(enabledConfigs) > 0 {
		return &enabledConfigs[0]
	}
	return nil
}

// resolveModelForAgent 按请求或默认策略挑选可用模型。
func (a *App) resolveModelForAgent(principal *Principal, agentCode string, modelConfigID *int64) (*db.SystemAiModelConfig, error) {
	agent, err := a.requireEnabledAgent(agentCode)
	if err != nil {
		return nil, err
	}
	if modelConfigID != nil {
		config, err := a.requireModelConfig(*modelConfigID, principal.User.ID)
		if err != nil {
			return nil, err
		}
		if !config.IsEnabled {
			return nil, bizErr("所选模型已停用,请重新选择可用模型")
		}
		return config, nil
	}
	configs, err := a.listModelConfigs(principal.User.ID, true)
	if err != nil {
		return nil, err
	}
	config := a.pickDefaultModel(principal.User.ID, agent.ID, configs)
	if config == nil {
		return nil, bizErr("请先在模型配置中配置并启用模型")
	}
	return config, nil
}

// getAiRuntimeConfig 解密并解析出可执行的模型运行配置。
func (a *App) getAiRuntimeConfig(modelConfigID, ownerUserID int64, requireEnabled bool) (*aiRuntimeConfig, error) {
	config, err := a.requireModelConfig(modelConfigID, ownerUserID)
	if err != nil {
		return nil, err
	}
	if requireEnabled && !config.IsEnabled {
		return nil, bizErr("当前模型已停用,请重新选择可用模型")
	}
	apiKey, err := a.Cipher.Decrypt(config.EncryptedAPIKey)
	if err != nil {
		return nil, err
	}
	headers, err := parseJSONStringMap(config.RequestHeadersJSON, "请求头 JSON")
	if err != nil {
		return nil, err
	}
	extraBody := loadJSONObject(config.RequestBodyJSON)
	timeoutMs := config.TimeoutMs
	if timeoutMs < 1000 {
		timeoutMs = 60000
	}
	return &aiRuntimeConfig{
		ConfigID: config.ID, ProviderType: config.Provider, ProviderLabel: config.ProviderName,
		DisplayName: config.DisplayName, ModelName: config.ModelIdentifier,
		BaseURL: strings.TrimSpace(config.BaseURL), APIKey: apiKey,
		Headers: headers, ExtraBody: extraBody, TimeoutMs: timeoutMs,
	}, nil
}

func (a *App) buildModelUpsert(payload AiModelUpsertPayload, existing *db.SystemAiModelConfig) (map[string]any, error) {
	provider := strings.TrimSpace(payload.Provider)
	if !aiProviderTypes[provider] {
		return nil, bizErr("不支持的模型协议类型")
	}
	providerName := strings.TrimSpace(payload.ProviderName)
	displayName := strings.TrimSpace(payload.DisplayName)
	modelIdentifier := strings.TrimSpace(payload.ModelIdentifier)
	baseURL := strings.TrimSpace(payload.BaseURL)
	apiKey := strings.TrimSpace(payload.APIKey)
	if providerName == "" {
		return nil, bizErr("厂商名称不能为空")
	}
	if displayName == "" {
		return nil, bizErr("展示名称不能为空")
	}
	if modelIdentifier == "" {
		return nil, bizErr("模型名称不能为空")
	}
	if baseURL == "" {
		return nil, bizErr("接口地址不能为空")
	}
	headersJSON, err := normalizeJSONText(payload.RequestHeadersJSON, "请求头 JSON")
	if err != nil {
		return nil, err
	}
	bodyJSON, err := normalizeJSONText(payload.RequestBodyJSON, "额外请求体 JSON")
	if err != nil {
		return nil, err
	}
	apiKeyCiphertext := ""
	if existing != nil {
		apiKeyCiphertext = existing.EncryptedAPIKey
	}
	if apiKey != "" {
		apiKeyCiphertext = a.Cipher.Encrypt(apiKey)
	} else if existing == nil {
		return nil, bizErr("API Key 不能为空")
	}
	priority := payload.Priority
	if priority < 1 {
		priority = 100
	}
	timeoutMs := payload.TimeoutMs
	if timeoutMs < 1000 {
		timeoutMs = 60000
	}
	isEnabled := payload.IsEnabled == nil || *payload.IsEnabled
	return map[string]any{
		"provider": provider, "provider_name": providerName, "display_name": displayName,
		"model_identifier": modelIdentifier, "base_url": baseURL,
		"encrypted_api_key": apiKeyCiphertext, "is_enabled": isEnabled, "priority": priority,
		"request_headers_json": headersJSON, "request_body_json": bodyJSON,
		"timeout_ms": timeoutMs, "remark": strings.TrimSpace(payload.Remark), "updated_at": time.Now(),
	}, nil
}

func buildAgentFields(payload AssistantAgentUpsertPayload) (map[string]any, error) {
	code := strings.TrimSpace(payload.Code)
	displayName := strings.TrimSpace(payload.DisplayName)
	systemPrompt := strings.TrimSpace(payload.SystemPrompt)
	dataSourceType := strings.TrimSpace(payload.DataSourceType)
	if code == "" {
		return nil, bizErr("智能体编码不能为空")
	}
	if displayName == "" {
		return nil, bizErr("智能体名称不能为空")
	}
	if systemPrompt == "" {
		return nil, bizErr("系统提示词不能为空")
	}
	if !agentDataSourceTypes[dataSourceType] {
		return nil, bizErr("不支持的智能体数据来源类型")
	}
	starters := make([]string, 0, len(payload.StarterPrompts))
	for _, item := range payload.StarterPrompts {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			starters = append(starters, trimmed)
		}
		if len(starters) >= 6 {
			break
		}
	}
	startersJSON, _ := json.Marshal(starters)
	sort := payload.Sort
	if sort < 0 {
		sort = 0
	}
	isEnabled := payload.IsEnabled == nil || *payload.IsEnabled
	return map[string]any{
		"code": code, "display_name": displayName, "avatar": strings.TrimSpace(payload.Avatar),
		"description": strings.TrimSpace(payload.Description), "system_prompt": systemPrompt,
		"welcome_message":      strings.TrimSpace(payload.WelcomeMessage),
		"starter_prompts_json": string(startersJSON), "data_source_type": dataSourceType,
		"is_enabled": isEnabled, "sort": sort, "updated_at": time.Now(),
	}, nil
}

func (a *App) serializeModelConfig(config *db.SystemAiModelConfig, bindingMap map[int64][]int64, agents map[int64]*db.AssistantAgent) M {
	apiKey := ""
	if config.EncryptedAPIKey != "" {
		if decrypted, err := a.Cipher.Decrypt(config.EncryptedAPIKey); err == nil {
			apiKey = decrypted
		}
	}
	boundAgents := []M{}
	for _, agentID := range bindingMap[config.ID] {
		if agent, ok := agents[agentID]; ok {
			boundAgents = append(boundAgents, M{"id": agentID, "code": agent.Code, "displayName": agent.DisplayName})
		}
	}
	var sessionCount int64
	a.DB.Model(&db.AssistantSession{}).Where("model_config_id = ?", config.ID).Count(&sessionCount)
	return M{
		"id": config.ID, "ownerUserId": config.OwnerID, "provider": config.Provider,
		"providerName": config.ProviderName, "displayName": config.DisplayName,
		"modelIdentifier": config.ModelIdentifier, "baseUrl": config.BaseURL,
		"apiKeyMasked": security.Mask(apiKey), "isEnabled": config.IsEnabled, "priority": config.Priority,
		"requestHeadersJson": prettyJSONText(config.RequestHeadersJSON),
		"requestBodyJson":    prettyJSONText(config.RequestBodyJSON),
		"timeoutMs":          config.TimeoutMs, "remark": config.Remark,
		"boundAgents":           boundAgents,
		"lastValidationStatus":  orDefault(config.LastValidationStatus, "unknown"),
		"lastValidationMessage": config.LastValidationMessage,
		"lastValidatedAt":       fmtTime(config.LastValidatedAt),
		"updatedAt":             fmtTimeV(config.UpdatedAt), "createdAt": fmtTimeV(config.CreatedAt),
		"sessionCount": sessionCount,
	}
}

func (a *App) serializeAgent(agent *db.AssistantAgent) M {
	var bindingCount, sessionCount int64
	a.DB.Model(&db.AiModelAgentBinding{}).Where("agent_id = ?", agent.ID).Count(&bindingCount)
	a.DB.Model(&db.AssistantSession{}).Where("agent_id = ?", agent.ID).Count(&sessionCount)
	return M{
		"id": agent.ID, "code": agent.Code, "displayName": agent.DisplayName,
		"avatar": agent.Avatar, "description": agent.Description,
		"systemPrompt": agent.SystemPrompt, "welcomeMessage": agent.WelcomeMessage,
		"starterPrompts": parseStringList(agent.StarterPromptsJSON),
		"dataSourceType": agent.DataSourceType, "isEnabled": agent.IsEnabled, "sort": agent.Sort,
		"bindingCount": bindingCount, "sessionCount": sessionCount,
		"createdAt": fmtTimeV(agent.CreatedAt), "updatedAt": fmtTimeV(agent.UpdatedAt),
	}
}

func parseJSONStringMap(text, fieldName string) (map[string]string, error) {
	if strings.TrimSpace(text) == "" {
		return map[string]string{}, nil
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return nil, bizErr("%s 不是合法的 JSON", fieldName)
	}
	result := map[string]string{}
	for key, value := range raw {
		if text, ok := value.(string); ok {
			result[key] = text
		} else {
			result[key] = dumpJSON(value)
		}
	}
	return result, nil
}

func normalizeJSONText(value, fieldName string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "{}", nil
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(value), &parsed); err != nil {
		return "", bizErr("%s 不是合法的 JSON", fieldName)
	}
	return dumpJSON(parsed), nil
}

func prettyJSONText(value string) string {
	if strings.TrimSpace(value) == "" {
		return "{}"
	}
	var parsed any
	if err := json.Unmarshal([]byte(value), &parsed); err != nil {
		return value
	}
	pretty, err := json.MarshalIndent(parsed, "", "  ")
	if err != nil {
		return value
	}
	return string(pretty)
}

func parseStringList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return []string{}
	}
	var raw []any
	if err := json.Unmarshal([]byte(value), &raw); err != nil {
		return []string{}
	}
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
			result = append(result, text)
		}
	}
	return result
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
