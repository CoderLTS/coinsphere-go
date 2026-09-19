package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/plugin/sdk"
)

var assistantEmptyInputSchema = json.RawMessage(`{"type":"object","additionalProperties":false}`)

func (a *App) assistantToolCatalog(principal *Principal) ([]assistantToolDefinition, map[string]assistantToolExecution) {
	definitions := make([]assistantToolDefinition, 0, 16)
	executions := map[string]assistantToolExecution{}
	add := func(name, description string, schema json.RawMessage, execute assistantToolExecution) {
		definitions = append(definitions, assistantToolDefinition{
			Type: "function", Function: assistantToolDefinitionFunction{
				Name: name, Description: description, Parameters: schema,
			},
		})
		executions[name] = execute
	}

	add("platform_overview", "获取平台、数据库和主要业务对象数量的系统概览。", assistantEmptyInputSchema,
		func(ctx context.Context, _ json.RawMessage) (json.RawMessage, *assistantWorkflowCreateSummary, error) {
			data, err := a.assistantPlatformOverview(ctx)
			return assistantToolJSON(data, err)
		})
	add("list_plugins", "列出当前已编译加载的平台插件及贡献类型。", assistantEmptyInputSchema,
		func(context.Context, json.RawMessage) (json.RawMessage, *assistantWorkflowCreateSummary, error) {
			return assistantToolJSON(map[string]any{"items": a.ListInstalledPlugins()}, nil)
		})
	add("list_workflows", "按状态查询最近的平台工作流。", json.RawMessage(`{"type":"object","properties":{"status":{"type":"string","enum":["inactive","active","error"]},"limit":{"type":"integer","minimum":1,"maximum":100,"default":20}},"additionalProperties":false}`),
		func(ctx context.Context, raw json.RawMessage) (json.RawMessage, *assistantWorkflowCreateSummary, error) {
			var input struct {
				Status string `json:"status"`
				Limit  int    `json:"limit"`
			}
			if err := decodeAssistantToolInput(raw, &input); err != nil {
				return nil, nil, err
			}
			if input.Limit == 0 {
				input.Limit = 20
			}
			if input.Limit < 1 || input.Limit > 100 {
				return nil, nil, errors.New("limit must be between 1 and 100")
			}
			items, err := a.ListWorkflows(ctx, input.Status)
			if len(items) > input.Limit {
				items = items[:input.Limit]
			}
			return assistantToolJSON(map[string]any{"items": items}, err)
		})
	add("list_workflow_revisions", "查询指定工作流最近的修订摘要。", json.RawMessage(`{"type":"object","properties":{"workflowId":{"type":"integer","minimum":1},"limit":{"type":"integer","minimum":1,"maximum":50,"default":20}},"required":["workflowId"],"additionalProperties":false}`),
		func(ctx context.Context, raw json.RawMessage) (json.RawMessage, *assistantWorkflowCreateSummary, error) {
			var input struct {
				WorkflowID int64 `json:"workflowId"`
				Limit      int   `json:"limit"`
			}
			if err := decodeAssistantToolInput(raw, &input); err != nil {
				return nil, nil, err
			}
			if input.WorkflowID <= 0 {
				return nil, nil, errors.New("workflowId must be positive")
			}
			if input.Limit == 0 {
				input.Limit = 20
			}
			if input.Limit < 1 || input.Limit > 50 {
				return nil, nil, errors.New("limit must be between 1 and 50")
			}
			var rows []struct {
				ID                int64     `json:"id"`
				RevisionNumber    int64     `json:"revisionNumber"`
				MainTriggerNodeID string    `json:"mainTriggerNodeId"`
				CreatedAt         time.Time `json:"createdAt"`
			}
			err := a.DB.WithContext(ctx).Model(&db.WorkflowRevision{}).Where("workflow_id = ?", input.WorkflowID).
				Order("revision_number DESC").Limit(input.Limit).Find(&rows).Error
			return assistantToolJSON(map[string]any{"items": rows}, err)
		})
	add("list_workflow_runs", "查询指定工作流最近的运行状态摘要。", json.RawMessage(`{"type":"object","properties":{"workflowId":{"type":"integer","minimum":1},"status":{"type":"string","maxLength":32},"limit":{"type":"integer","minimum":1,"maximum":50,"default":20}},"required":["workflowId"],"additionalProperties":false}`),
		func(ctx context.Context, raw json.RawMessage) (json.RawMessage, *assistantWorkflowCreateSummary, error) {
			var input struct {
				WorkflowID int64  `json:"workflowId"`
				Status     string `json:"status"`
				Limit      int    `json:"limit"`
			}
			if err := decodeAssistantToolInput(raw, &input); err != nil {
				return nil, nil, err
			}
			if input.WorkflowID <= 0 {
				return nil, nil, errors.New("workflowId must be positive")
			}
			if input.Limit == 0 {
				input.Limit = 20
			}
			if input.Limit < 1 || input.Limit > 50 {
				return nil, nil, errors.New("limit must be between 1 and 50")
			}
			query := a.DB.WithContext(ctx).Model(&db.WorkflowRun{}).Where("workflow_id = ?", input.WorkflowID)
			if input.Status != "" {
				query = query.Where("status = ?", input.Status)
			}
			var rows []struct {
				ID            int64     `json:"id"`
				RevisionID    int64     `json:"revisionId"`
				TriggerType   string    `json:"triggerType"`
				Status        string    `json:"status"`
				ErrorCategory *string   `json:"errorCategory"`
				TriggeredAt   time.Time `json:"triggeredAt"`
			}
			err := query.Order("id DESC").Limit(input.Limit).Find(&rows).Error
			return assistantToolJSON(map[string]any{"items": rows}, err)
		})
	add("list_human_tasks", "查询最近的工作流人工任务。", json.RawMessage(`{"type":"object","properties":{"status":{"type":"string","enum":["pending","approved","rejected","expired","superseded"]},"limit":{"type":"integer","minimum":1,"maximum":100,"default":20}},"additionalProperties":false}`),
		func(ctx context.Context, raw json.RawMessage) (json.RawMessage, *assistantWorkflowCreateSummary, error) {
			var input struct {
				Status string `json:"status"`
				Limit  int    `json:"limit"`
			}
			if err := decodeAssistantToolInput(raw, &input); err != nil {
				return nil, nil, err
			}
			if input.Limit == 0 {
				input.Limit = 20
			}
			if input.Limit < 1 || input.Limit > 100 {
				return nil, nil, errors.New("limit must be between 1 and 100")
			}
			items, err := a.ListWorkflowHumanTasks(ctx, input.Status)
			if len(items) > input.Limit {
				items = items[:input.Limit]
			}
			return assistantToolJSON(map[string]any{"items": items}, err)
		})
	add("search_system_logs", "按级别、组件或关键词查询最近的结构化系统日志摘要。", json.RawMessage(`{"type":"object","properties":{"level":{"type":"string","enum":["debug","info","warn","error"]},"component":{"type":"string","maxLength":64},"keyword":{"type":"string","maxLength":100},"limit":{"type":"integer","minimum":1,"maximum":50,"default":20}},"additionalProperties":false}`),
		func(ctx context.Context, raw json.RawMessage) (json.RawMessage, *assistantWorkflowCreateSummary, error) {
			var input struct {
				Level     string `json:"level"`
				Component string `json:"component"`
				Keyword   string `json:"keyword"`
				Limit     int    `json:"limit"`
			}
			if err := decodeAssistantToolInput(raw, &input); err != nil {
				return nil, nil, err
			}
			if input.Limit == 0 {
				input.Limit = 20
			}
			if input.Limit < 1 || input.Limit > 50 {
				return nil, nil, errors.New("limit must be between 1 and 50")
			}
			query := a.DB.WithContext(ctx).Model(&db.SystemLog{})
			if input.Level != "" {
				query = query.Where("level = ?", input.Level)
			}
			if input.Component != "" {
				query = query.Where("component = ?", strings.TrimSpace(input.Component))
			}
			if input.Keyword != "" {
				query = query.Where("message ILIKE ?", "%"+strings.TrimSpace(input.Keyword)+"%")
			}
			var rows []struct {
				ID         int64     `json:"id"`
				LoggedAt   time.Time `json:"loggedAt"`
				Level      string    `json:"level"`
				Component  string    `json:"component"`
				Message    string    `json:"message"`
				Route      string    `json:"route"`
				StatusCode *int      `json:"statusCode"`
				DurationMS *int64    `json:"durationMs"`
			}
			err := query.Order("id DESC").Limit(input.Limit).Find(&rows).Error
			return assistantToolJSON(map[string]any{"items": rows}, err)
		})
	add("notification_summary", "按渠道和状态汇总通知投递数量。", assistantEmptyInputSchema,
		func(ctx context.Context, _ json.RawMessage) (json.RawMessage, *assistantWorkflowCreateSummary, error) {
			var rows []struct {
				Channel string `json:"channel"`
				Status  string `json:"status"`
				Count   int64  `json:"count"`
			}
			err := a.DB.WithContext(ctx).Model(&db.NotificationDelivery{}).
				Select("channel, status, COUNT(*) AS count").Group("channel, status").Order("channel, status").Scan(&rows).Error
			return assistantToolJSON(map[string]any{"items": rows}, err)
		})
	add("list_node_capabilities", "查询当前实时工作流节点目录；指定 type 时返回完整 Schema。", json.RawMessage(`{"type":"object","properties":{"type":{"type":"string","maxLength":160}},"additionalProperties":false}`),
		func(_ context.Context, raw json.RawMessage) (json.RawMessage, *assistantWorkflowCreateSummary, error) {
			var input struct {
				Type string `json:"type"`
			}
			if err := decodeAssistantToolInput(raw, &input); err != nil {
				return nil, nil, err
			}
			definitions := a.ListWorkflowNodeDefinitions()
			if input.Type != "" {
				for _, definition := range definitions {
					if definition.Type == input.Type {
						return assistantToolJSON(definition, nil)
					}
				}
				return nil, nil, errors.New("workflow node type was not found")
			}
			items := make([]map[string]any, 0, len(definitions))
			for _, definition := range definitions {
				items = append(items, map[string]any{
					"type": definition.Type, "version": definition.Version, "title": definition.Title,
					"kind": definition.Kind, "available": definition.Available, "secretFields": definition.SecretFields,
				})
			}
			return assistantToolJSON(map[string]any{"items": items}, nil)
		})
	var createdWorkflow *assistantWorkflowCreateSummary
	add("create_workflow", "校验并直接创建一个完整的 inactive 工作流；不会激活或运行工作流。", json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","minLength":1,"maxLength":120},"description":{"type":"string","maxLength":500},"graph":{"type":"object"}},"required":["name","description","graph"],"additionalProperties":false}`),
		func(ctx context.Context, raw json.RawMessage) (json.RawMessage, *assistantWorkflowCreateSummary, error) {
			if createdWorkflow != nil {
				result, _, err := assistantToolJSON(map[string]any{
					"ok": true, "workflowId": createdWorkflow.WorkflowID, "status": createdWorkflow.Status,
					"editUrl": createdWorkflow.EditURL, "instruction": "工作流已创建，不要重复创建。",
				}, nil)
				return result, createdWorkflow, err
			}
			var input struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				Graph       json.RawMessage `json:"graph"`
			}
			if err := decodeAssistantToolInput(raw, &input); err != nil {
				return nil, nil, err
			}
			workflow, err := a.createAssistantWorkflow(ctx, input.Name, input.Description, input.Graph, principal)
			if err != nil {
				return nil, nil, err
			}
			createdWorkflow = workflow
			result, _, err := assistantToolJSON(map[string]any{
				"ok": true, "workflowId": workflow.WorkflowID, "status": workflow.Status,
				"editUrl": workflow.EditURL, "name": workflow.Name, "description": workflow.Description,
				"nodeCount": workflow.NodeCount, "edgeCount": workflow.EdgeCount,
				"nodeTypes": workflow.NodeTypes, "missingSecrets": workflow.MissingSecrets,
				"instruction": "工作流已通过平台校验并创建成功；请向用户说明其已创建、状态为 inactive，并提供编辑地址。",
			}, nil)
			return result, workflow, err
		})

	if a.Plugins != nil {
		for _, registered := range a.Plugins.AssistantQueries() {
			query := registered
			add(query.ToolName, query.Descriptor.Description, query.Descriptor.InputSchema,
				func(ctx context.Context, input json.RawMessage) (json.RawMessage, *assistantWorkflowCreateSummary, error) {
					result, err := a.Plugins.RunAssistantQuery(ctx, query.ToolName, input, sdk.SystemScope{
						UserID: principal.User.ID, RoleCodes: slices.Clone(principal.RoleCodes),
					})
					return result, nil, err
				})
		}
	}
	return definitions, executions
}

func (a *App) assistantSystemPrompt(tools []assistantToolDefinition) string {
	type promptNode struct {
		Type         string                    `json:"type"`
		Version      string                    `json:"version"`
		Kind         sdk.NodeKind              `json:"kind"`
		ConfigSchema json.RawMessage           `json:"configSchema"`
		InputSchema  json.RawMessage           `json:"inputSchema"`
		OutputSchema json.RawMessage           `json:"outputSchema"`
		InputPorts   []string                  `json:"inputPorts"`
		OutputPorts  []string                  `json:"outputPorts"`
		SecretFields []WorkflowSecretFieldView `json:"secretFields"`
		Available    bool                      `json:"available"`
	}
	nodes := make([]promptNode, 0)
	for _, item := range a.ListWorkflowNodeDefinitions() {
		nodes = append(nodes, promptNode{
			Type: item.Type, Version: item.Version, Kind: item.Kind, ConfigSchema: item.ConfigSchema,
			InputSchema: item.InputSchema, OutputSchema: item.OutputSchema, InputPorts: item.InputPorts,
			OutputPorts: item.OutputPorts, SecretFields: item.SecretFields, Available: item.Available,
		})
	}
	var menus []struct {
		Title string `json:"title"`
		Path  string `json:"path"`
	}
	_ = a.DB.Model(&db.SystemMenu{}).Select("title, path").Where("is_active = ? AND is_hidden = ?", true, false).Order("sort, id").Find(&menus).Error
	toolDirectory := make([]map[string]string, 0, len(tools))
	for _, tool := range tools {
		toolDirectory = append(toolDirectory, map[string]string{"name": tool.Function.Name, "description": tool.Function.Description})
	}
	contextData, _ := json.Marshal(map[string]any{
		"menus": menus, "plugins": a.ListInstalledPlugins(), "nodes": nodes, "tools": toolDirectory,
	})
	return `你是 CoinSphere 平台内置智能助手，只服务超级管理员。你的职责是解释平台、通过工具查询平台数据，并根据用户描述生成工作流。

平台知识：CoinSphere 是基于 Go、PostgreSQL 16 和 Vue 的模块化单体工作流平台。工作流负责粗粒度编排，状态默认为 inactive；只有用户明确操作才可激活或运行。时间统一使用 UTC，金融数值使用十进制字符串。插件在编译期通过 SDK 注册节点、页面、路由和只读助手查询。AI、工作流和通用 HTTP 节点不得调用交易所私有接口或绕过风控。

回答规则：平台事实优先使用下面的实时目录和只读工具；不知道时明确说明，不编造数据。工具参数和结果不得复述为原始载荷，也不要展示个人数据。不要输出思维链。

工作流规则：仅当用户明确要求创建工作流时才创建。必须使用实时节点版本、端口和 JSON Schema。图必须是 schemaVersion=1，包含且仅包含一个主触发器；每个节点必须有 nodeInstanceId、nodeType、nodeVersion、config、position；每条边必须有 edgeId、sourceNodeInstanceId、sourcePort、targetNodeInstanceId、targetPort。密钥字段绝不能写入 graph.config。完成图后必须调用 create_workflow；该工具会先由平台校验，校验失败时根据错误修正后重试。工具成功即表示工作流已经创建，必须向用户说明 workflowId、inactive 状态、编辑地址和待补密钥，不要要求确认、不要生成方案卡、不要重复创建，也绝不自动激活或运行。

实时平台目录：` + string(contextData)
}

func (a *App) assistantPlatformOverview(ctx context.Context) (map[string]any, error) {
	overview, err := a.GetHomeOverview(ctx)
	if err != nil {
		return nil, err
	}
	counts := map[string]int64{}
	for name, model := range map[string]any{
		"users": &db.SystemUser{}, "workflows": &db.Workflow{}, "workflowRuns": &db.WorkflowRun{},
		"humanTasks": &db.WorkflowHumanTask{}, "notifications": &db.NotificationDelivery{},
	} {
		var count int64
		if err := a.DB.WithContext(ctx).Model(model).Count(&count).Error; err != nil {
			return nil, err
		}
		counts[name] = count
	}
	return map[string]any{"meta": a.GetHomeMeta(), "database": overview["database"], "counts": counts}, nil
}

func decodeAssistantToolInput(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("tool input is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("tool input must contain one JSON object")
	}
	return nil
}

func assistantToolJSON(value any, err error) (json.RawMessage, *assistantWorkflowCreateSummary, error) {
	if err != nil {
		return nil, nil, errors.New("tool query failed")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, nil, errors.New("encode tool result failed")
	}
	if len(raw) > 64<<10 {
		return nil, nil, fmt.Errorf("tool result exceeds 64 KiB")
	}
	return raw, nil, nil
}
