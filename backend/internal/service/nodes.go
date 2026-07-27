package service

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"coinsphere/backend/internal/db"
)

// ---------- 任务定义注册表(代码即真源) ----------

// taskDefinition 一个可执行任务能力。
type taskDefinition struct {
	Code            string
	Label           string
	Description     string
	ParameterSchema M
	Execute         func(a *App, inputs M) (M, error)
}

var taskDefinitions = []*taskDefinition{
	{
		Code:        "blockbeats_news_fetch",
		Label:       "Blockbeats 新闻抓取",
		Description: "拉取 Blockbeats 快讯,去重后写入新闻数据表。",
		ParameterSchema: M{
			"type": "object",
			"properties": M{
				"pageSize": M{"type": "integer", "title": "抓取数量", "default": 10, "minimum": 1, "maximum": 50},
				"page":     M{"type": "integer", "title": "页码", "default": 1, "minimum": 1},
			},
		},
		Execute: func(a *App, inputs M) (M, error) {
			pageSize := int(asInt64(inputs["pageSize"]))
			if pageSize <= 0 {
				pageSize = 10
			}
			page := int(asInt64(inputs["page"]))
			if page <= 0 {
				page = 1
			}
			result, err := a.syncLatestNews(pageSize, page)
			if err != nil {
				return nil, err
			}
			return M{
				"taskDefinitionCode": "blockbeats_news_fetch",
				"fetchedCount":       result.FetchedCount,
				"insertedCount":      result.InsertedCount,
				"insertedItems":      result.InsertedItems,
			}, nil
		},
	},
}

func getTaskDefinition(code string) (*taskDefinition, error) {
	for _, definition := range taskDefinitions {
		if definition.Code == code {
			return definition, nil
		}
	}
	return nil, bizErr("任务定义不存在: %s", code)
}

// ListTaskDefinitions 任务定义列表(节点面板用)。
func (a *App) ListTaskDefinitions() []M {
	result := make([]M, 0, len(taskDefinitions))
	for _, definition := range taskDefinitions {
		result = append(result, M{
			"code": definition.Code, "label": definition.Label,
			"description": definition.Description, "parameterSchema": definition.ParameterSchema,
		})
	}
	return result
}

// ListTaskDefinitionPage 任务定义管理页分页。
func (a *App) ListTaskDefinitionPage(current, size int, keyword string) M {
	items := make([]*taskDefinition, len(taskDefinitions))
	copy(items, taskDefinitions)
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].Code) < strings.ToLower(items[j].Code)
	})
	keywordText := strings.ToLower(strings.TrimSpace(keyword))
	if keywordText != "" {
		filtered := items[:0]
		for _, item := range items {
			if strings.Contains(strings.ToLower(item.Code), keywordText) ||
				strings.Contains(strings.ToLower(item.Label), keywordText) ||
				strings.Contains(strings.ToLower(item.Description), keywordText) {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	total := len(items)
	start := (current - 1) * size
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}
	end := start + size
	if end > total {
		end = total
	}
	pageItems := items[start:end]
	records := make([]M, 0, len(pageItems))
	for _, item := range pageItems {
		records = append(records, a.serializeTaskDefinitionItem(item))
	}
	return pagedResult(records, current, size, int64(total))
}

// UpdateTaskDefinitionDefaultParams 保存全局默认参数覆盖。
func (a *App) UpdateTaskDefinitionDefaultParams(code string, params M, operatorUserID int64) (M, error) {
	definition, err := getTaskDefinition(code)
	if err != nil {
		return nil, err
	}
	if err := validatePartialParams(params, definition.ParameterSchema); err != nil {
		return nil, err
	}
	var existing db.TaskDefinitionConfig
	existingFound := a.DB.Where("task_definition_code = ?", code).First(&existing).Error == nil
	existingOverrides := M{}
	if existingFound {
		existingOverrides = loadJSONObject(existing.ParameterOverridesJSON)
	}
	mergedEffective := buildEffectiveDefaultParams(definition.ParameterSchema, existingOverrides)
	for key, value := range params {
		mergedEffective[key] = value
	}
	nextOverrides := buildConfiguredOverrides(definition.ParameterSchema, mergedEffective)
	now := time.Now()
	if len(nextOverrides) > 0 {
		if existingFound {
			a.DB.Model(&existing).Updates(map[string]any{
				"parameter_overrides_json": dumpJSON(nextOverrides),
				"updated_by":               operatorUserID, "updated_at": now,
			})
		} else {
			a.DB.Create(&db.TaskDefinitionConfig{
				TaskDefinitionCode:     code,
				ParameterOverridesJSON: dumpJSON(nextOverrides),
				UpdatedBy:              &operatorUserID,
				CreatedAt:              now, UpdatedAt: now,
			})
		}
	} else if existingFound {
		a.DB.Where("task_definition_code = ?", code).Delete(&db.TaskDefinitionConfig{})
	}
	return a.serializeTaskDefinitionItem(definition), nil
}

// buildExecutionInputs 合并 schema 默认值、全局覆盖与节点输入。
func (a *App) buildExecutionInputs(code string, taskParams, runtimeInputs M) (M, error) {
	definition, err := getTaskDefinition(code)
	if err != nil {
		return nil, err
	}
	var config db.TaskDefinitionConfig
	overrides := M{}
	if err := a.DB.Where("task_definition_code = ?", code).First(&config).Error; err == nil {
		overrides = loadJSONObject(config.ParameterOverridesJSON)
	}
	result := buildEffectiveDefaultParams(definition.ParameterSchema, overrides)
	for key, value := range taskParams {
		result[key] = value
	}
	for key, value := range runtimeInputs {
		result[key] = value
	}
	return result, nil
}

func (a *App) serializeTaskDefinitionItem(definition *taskDefinition) M {
	var config db.TaskDefinitionConfig
	configFound := a.DB.Where("task_definition_code = ?", definition.Code).First(&config).Error == nil
	overrides := M{}
	updatedAt := ""
	var updatedBy any
	if configFound {
		overrides = loadJSONObject(config.ParameterOverridesJSON)
		updatedAt = fmtTimeV(config.UpdatedAt)
		if config.UpdatedBy != nil {
			updatedBy = *config.UpdatedBy
		}
	}
	return M{
		"code": definition.Code, "label": definition.Label, "description": definition.Description,
		"parameterSchema":        definition.ParameterSchema,
		"schemaDefaultParams":    extractSchemaDefaultParams(definition.ParameterSchema),
		"configuredOverrides":    overrides,
		"effectiveDefaultParams": buildEffectiveDefaultParams(definition.ParameterSchema, overrides),
		"updatedAt":              updatedAt, "updatedBy": updatedBy,
	}
}

func extractSchemaDefaultParams(schema M) M {
	defaults := M{}
	properties, _ := schema["properties"].(M)
	if properties == nil {
		if raw, ok := schema["properties"].(map[string]any); ok {
			properties = raw
		}
	}
	for key, propertyAny := range properties {
		property, ok := propertyAny.(M)
		if !ok {
			if raw, isMap := propertyAny.(map[string]any); isMap {
				property = raw
			} else {
				continue
			}
		}
		if defaultValue, exists := property["default"]; exists {
			defaults[key] = defaultValue
		}
	}
	return defaults
}

func buildEffectiveDefaultParams(schema, overrides M) M {
	result := extractSchemaDefaultParams(schema)
	for key, value := range overrides {
		result[key] = value
	}
	return result
}

func buildConfiguredOverrides(schema, effective M) M {
	schemaDefaults := extractSchemaDefaultParams(schema)
	overrides := M{}
	for key, value := range effective {
		if schemaDefault, exists := schemaDefaults[key]; exists && jsonEqual(schemaDefault, value) {
			continue
		}
		overrides[key] = value
	}
	return overrides
}

func jsonEqual(left, right any) bool { return dumpJSON(left) == dumpJSON(right) }

func validatePartialParams(params, schema M) error {
	properties, _ := schema["properties"].(M)
	if properties == nil {
		if raw, ok := schema["properties"].(map[string]any); ok {
			properties = raw
		}
	}
	if properties == nil {
		if len(params) > 0 {
			return bizErr("Task definition does not declare configurable params")
		}
		return nil
	}
	for key, value := range params {
		fieldSchemaAny, exists := properties[key]
		if !exists {
			return bizErr("Task definition param is not allowed: %s", key)
		}
		fieldSchema, ok := fieldSchemaAny.(map[string]any)
		if !ok {
			return bizErr("Task definition param is not allowed: %s", key)
		}
		if err := validateFieldValue(key, value, fieldSchema); err != nil {
			return err
		}
	}
	return nil
}

func validateFieldValue(key string, value any, fieldSchema map[string]any) error {
	if expectedType, exists := fieldSchema["type"]; exists && !matchesSchemaType(value, expectedType) {
		return bizErr("Task definition param type is invalid: %s", key)
	}
	if enumValues, ok := fieldSchema["enum"].([]any); ok {
		found := false
		for _, candidate := range enumValues {
			if jsonEqual(candidate, value) {
				found = true
				break
			}
		}
		if !found {
			return bizErr("Task definition param value is invalid: %s", key)
		}
	}
	if _, isBool := value.(bool); isBool {
		return nil
	}
	if number, isNumber := toFloat(value); isNumber {
		if minimum, ok := toFloatAny(fieldSchema["minimum"]); ok && number < minimum {
			return bizErr("Task definition param is below minimum: %s", key)
		}
		if maximum, ok := toFloatAny(fieldSchema["maximum"]); ok && number > maximum {
			return bizErr("Task definition param is above maximum: %s", key)
		}
	}
	return nil
}

func matchesSchemaType(value, expectedType any) bool {
	if typeList, ok := expectedType.([]any); ok {
		for _, item := range typeList {
			if matchesSchemaType(value, item) {
				return true
			}
		}
		return false
	}
	switch asString(expectedType) {
	case "string":
		_, ok := value.(string)
		return ok
	case "integer":
		if _, isBool := value.(bool); isBool {
			return false
		}
		number, ok := toFloat(value)
		return ok && number == float64(int64(number))
	case "number":
		if _, isBool := value.(bool); isBool {
			return false
		}
		_, ok := toFloat(value)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "null":
		return value == nil
	default:
		return true
	}
}

func toFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func toFloatAny(value any) (float64, bool) {
	if value == nil {
		return 0, false
	}
	return toFloat(value)
}

// ---------- 工作流节点注册表 ----------

// nodeExecResult 节点执行结果。
type nodeExecResult struct {
	Output         M
	SelectedBranch *string
	ForeachItems   []any
	Terminate      bool
}

// nodeExecContext 节点执行上下文。
type nodeExecContext struct {
	App          *App
	Definition   *db.WorkflowDefinition
	RuntimeEntry *db.WorkflowRuntimeEntry
	Execution    *db.WorkflowExecution
	NodeLog      *db.WorkflowExecutionNode
	Node         M
	Graph        M
	SharedState  M
	TriggerCtx   M
	PublishEvent func(eventType, aggregateType string, payload, metadata M) (int64, error)
}

type workflowNodeDefinition struct {
	TypeCode     string
	Label        string
	ConfigSchema M
	Execute      func(ctx *nodeExecContext) (*nodeExecResult, error)
}

var baseStartProperties = M{
	"entryKey":      M{"type": "string", "title": "入口标识"},
	"displayName":   M{"type": "string", "title": "入口名称"},
	"inputBindings": M{"type": "object", "title": "默认输入绑定"},
}

func mergeProps(base M, extra M) M {
	merged := M{}
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range extra {
		merged[key] = value
	}
	return merged
}

func startNodeExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	config, _ := ctx.Node["config"].(map[string]any)
	return &nodeExecResult{Output: M{
		"started":     true,
		"triggerType": ctx.TriggerCtx["triggerType"],
		"entryKey":    asString(config["entryKey"]),
	}}, nil
}

var workflowNodeDefinitions = []*workflowNodeDefinition{
	{
		TypeCode: "start.manual", Label: "手动开始",
		ConfigSchema: M{"type": "object", "properties": baseStartProperties, "required": []string{"entryKey"}},
		Execute:      startNodeExecute,
	},
	{
		TypeCode: "start.schedule", Label: "定时开始",
		ConfigSchema: M{
			"type": "object",
			"properties": mergeProps(baseStartProperties, M{
				"scheduleType":   M{"type": "string", "enum": []string{"cron", "interval", "once"}, "title": "计划类型"},
				"cronExpression": M{"type": "string", "title": "Cron 表达式"},
				"value":          M{"type": "integer", "title": "间隔数值"},
				"unit":           M{"type": "string", "enum": []string{"seconds", "minutes", "hours", "days"}, "title": "间隔单位"},
				"runAt":          M{"type": "string", "title": "执行时间"},
			}),
			"required": []string{"entryKey", "scheduleType"},
		},
		Execute: startNodeExecute,
	},
	{
		TypeCode: "start.event", Label: "事件开始",
		ConfigSchema: M{
			"type": "object",
			"properties": mergeProps(baseStartProperties, M{
				"eventType": M{"type": "string", "title": "事件类型"},
				"filters": M{
					"type": "array", "title": "过滤条件",
					"items": M{"type": "object", "properties": M{
						"path":   M{"type": "string", "title": "字段路径"},
						"equals": M{"type": "string", "title": "等于"},
					}},
				},
			}),
			"required": []string{"entryKey", "eventType"},
		},
		Execute: startNodeExecute,
	},
	{
		TypeCode: "start.webhook", Label: "Webhook 开始",
		ConfigSchema: M{"type": "object", "properties": baseStartProperties, "required": []string{"entryKey"}},
		Execute:      startNodeExecute,
	},
	{
		TypeCode: "task.run", Label: "执行任务",
		ConfigSchema: M{
			"type": "object",
			"properties": M{
				"taskDefinitionCode": M{"type": "string", "title": "任务定义编码"},
				"inputsPath":         M{"type": "string", "title": "输入路径", "default": "inputs"},
			},
			"required": []string{"taskDefinitionCode"},
		},
		Execute: func(ctx *nodeExecContext) (*nodeExecResult, error) {
			config, _ := ctx.Node["config"].(map[string]any)
			definitionCode := strings.TrimSpace(asString(config["taskDefinitionCode"]))
			if definitionCode == "" {
				return nil, bizErr("Task node is missing taskDefinitionCode")
			}
			definition, err := getTaskDefinition(definitionCode)
			if err != nil {
				return nil, err
			}
			inputsPath := strings.TrimSpace(asString(config["inputsPath"]))
			if inputsPath == "" {
				inputsPath = "inputs"
			}
			inputValues, _ := readPath(ctx.SharedState, inputsPath).(map[string]any)
			if inputValues == nil {
				inputValues, _ = ctx.SharedState["inputs"].(map[string]any)
			}
			taskParams, _ := config["taskParams"].(map[string]any)
			mergedInputs, err := ctx.App.buildExecutionInputs(definitionCode, taskParams, inputValues)
			if err != nil {
				return nil, err
			}
			payload, err := definition.Execute(ctx.App, mergedInputs)
			if err != nil {
				return nil, err
			}
			ctx.SharedState["taskResult"] = payload
			setNodeOutput(ctx, payload)
			return &nodeExecResult{Output: payload}, nil
		},
	},
	{
		TypeCode: "event.publish", Label: "发布事件",
		ConfigSchema: M{
			"type": "object",
			"properties": M{
				"eventType":     M{"type": "string", "title": "事件类型"},
				"aggregateType": M{"type": "string", "title": "聚合类型", "default": "workflow_execution"},
				"payloadPath":   M{"type": "string", "title": "事件载荷路径", "default": "taskResult"},
				"metadataPath":  M{"type": "string", "title": "元数据路径", "default": ""},
			},
			"required": []string{"eventType"},
		},
		Execute: func(ctx *nodeExecContext) (*nodeExecResult, error) {
			config, _ := ctx.Node["config"].(map[string]any)
			payloadPath := strings.TrimSpace(asString(config["payloadPath"]))
			if payloadPath == "" {
				payloadPath = "taskResult"
			}
			payload, _ := readPath(ctx.SharedState, payloadPath).(map[string]any)
			if payload == nil {
				payload = M{"value": readPath(ctx.SharedState, payloadPath)}
			}
			metadata := M{}
			if metadataPath := strings.TrimSpace(asString(config["metadataPath"])); metadataPath != "" {
				if candidate, ok := readPath(ctx.SharedState, metadataPath).(map[string]any); ok {
					metadata = candidate
				}
			}
			aggregateType := strings.TrimSpace(asString(config["aggregateType"]))
			if aggregateType == "" {
				aggregateType = "workflow_execution"
			}
			outboxID, err := ctx.PublishEvent(strings.TrimSpace(asString(config["eventType"])), aggregateType, payload, metadata)
			if err != nil {
				return nil, err
			}
			return &nodeExecResult{Output: M{"outboxId": outboxID, "eventType": asString(config["eventType"])}}, nil
		},
	},
	{
		TypeCode: "notify", Label: "发送通知",
		ConfigSchema: M{
			"type": "object",
			"properties": M{
				"targets": M{"type": "array", "title": "通知目标", "items": M{"type": "object", "properties": M{
					"targetType": M{"type": "string", "enum": []string{"user", "role"}},
					"targetId":   M{"type": "integer"},
					"targetCode": M{"type": "string"},
				}}},
				"channelTypes":    M{"type": "array", "title": "通知渠道", "items": M{"type": "string", "enum": []string{"in_app", "dingtalk_webhook", "smtp_email"}}},
				"titleTemplate":   M{"type": "string", "title": "标题模板"},
				"contentTemplate": M{"type": "string", "title": "内容模板"},
				"messageFormat":   M{"type": "string", "title": "消息格式", "default": "markdown"},
			},
			"required": []string{"targets", "channelTypes", "titleTemplate", "contentTemplate"},
		},
		Execute: func(ctx *nodeExecContext) (*nodeExecResult, error) {
			config, _ := ctx.Node["config"].(map[string]any)
			var outboxEventID *int64
			if raw := ctx.TriggerCtx["triggerOutboxId"]; raw != nil {
				id := asInt64(raw)
				if id > 0 {
					outboxEventID = &id
				}
			}
			result, err := ctx.App.dispatchNotifyNode(ctx.Execution, ctx.NodeLog, outboxEventID, config, ctx.SharedState)
			if err != nil {
				return nil, err
			}
			setNodeOutput(ctx, result)
			return &nodeExecResult{Output: result}, nil
		},
	},
	{
		TypeCode: "http.request", Label: "HTTP 请求",
		ConfigSchema: M{
			"type": "object",
			"properties": M{
				"url":         M{"type": "string", "title": "请求地址"},
				"method":      M{"type": "string", "title": "请求方法", "default": "POST", "enum": []string{"GET", "POST", "PUT"}},
				"payloadPath": M{"type": "string", "title": "请求体路径", "default": "taskResult"},
				"headersJson": M{"type": "string", "title": "请求头 JSON", "default": "{}"},
				"timeoutMs":   M{"type": "integer", "title": "超时毫秒", "default": 10000},
			},
			"required": []string{"url"},
		},
		Execute: func(ctx *nodeExecContext) (*nodeExecResult, error) {
			config, _ := ctx.Node["config"].(map[string]any)
			payloadPath := strings.TrimSpace(asString(config["payloadPath"]))
			if payloadPath == "" {
				payloadPath = "taskResult"
			}
			payload := readPath(ctx.SharedState, payloadPath)
			headersText := asString(config["headersJson"])
			headers := M{}
			if strings.TrimSpace(headersText) != "" {
				var parsed M
				if err := json.Unmarshal([]byte(headersText), &parsed); err != nil {
					return nil, bizErr("Node JSON config is invalid")
				}
				headers = parsed
			}
			timeoutMs := asInt64(config["timeoutMs"])
			if timeoutMs <= 0 {
				timeoutMs = 10000
			}
			method := strings.ToUpper(strings.TrimSpace(asString(config["method"])))
			if method == "" {
				method = "POST"
			}
			body, _ := json.Marshal(payload)
			request, err := http.NewRequest(method, strings.TrimSpace(asString(config["url"])), bytes.NewReader(body))
			if err != nil {
				return nil, err
			}
			request.Header.Set("Content-Type", "application/json")
			for key, value := range headers {
				request.Header.Set(key, asString(value))
			}
			timeout := time.Duration(timeoutMs) * time.Millisecond
			if timeout < time.Second {
				timeout = time.Second
			}
			client := &http.Client{Timeout: timeout}
			response, err := client.Do(request)
			if err != nil {
				return nil, err
			}
			defer response.Body.Close()
			raw, _ := io.ReadAll(io.LimitReader(response.Body, 4*1024*1024))
			if response.StatusCode < 200 || response.StatusCode >= 300 {
				return nil, bizErr("HTTP request failed with status %d: %s", response.StatusCode, truncateRunes(string(raw), 500))
			}
			var responsePayload any
			if err := json.Unmarshal(raw, &responsePayload); err != nil {
				responsePayload = string(raw)
			}
			result := M{"statusCode": response.StatusCode, "payload": responsePayload}
			setNodeOutput(ctx, result)
			return &nodeExecResult{Output: result}, nil
		},
	},
	{
		TypeCode: "condition.branch", Label: "条件判断",
		ConfigSchema: M{
			"type": "object",
			"properties": M{
				"path":     M{"type": "string", "title": "字段路径"},
				"operator": M{"type": "string", "title": "比较运算", "default": "eq", "enum": []string{"eq", "gt", "gte", "lt", "lte", "truthy"}},
				"value":    M{"type": "string", "title": "比较值", "default": ""},
			},
			"required": []string{"path", "operator"},
		},
		Execute: func(ctx *nodeExecContext) (*nodeExecResult, error) {
			config, _ := ctx.Node["config"].(map[string]any)
			actual := readPath(ctx.SharedState, asString(config["path"]))
			operator := strings.TrimSpace(asString(config["operator"]))
			if operator == "" {
				operator = "eq"
			}
			matched := compareValues(actual, config["value"], operator)
			branch := "false"
			if matched {
				branch = "true"
			}
			return &nodeExecResult{Output: M{"matched": matched}, SelectedBranch: &branch}, nil
		},
	},
	{
		TypeCode: "foreach", Label: "遍历",
		ConfigSchema: M{
			"type": "object",
			"properties": M{
				"itemsPath": M{"type": "string", "title": "数组路径"},
				"itemKey":   M{"type": "string", "title": "元素变量名", "default": "currentItem"},
				"indexKey":  M{"type": "string", "title": "索引变量名", "default": "currentIndex"},
			},
			"required": []string{"itemsPath"},
		},
		Execute: func(ctx *nodeExecContext) (*nodeExecResult, error) {
			config, _ := ctx.Node["config"].(map[string]any)
			items, _ := readPath(ctx.SharedState, asString(config["itemsPath"])).([]any)
			if items == nil {
				items = []any{}
			}
			itemKey := strings.TrimSpace(asString(config["itemKey"]))
			if itemKey == "" {
				itemKey = "currentItem"
			}
			indexKey := strings.TrimSpace(asString(config["indexKey"]))
			if indexKey == "" {
				indexKey = "currentIndex"
			}
			loopConfig, _ := ctx.SharedState["loopConfig"].(map[string]any)
			if loopConfig == nil {
				loopConfig = M{}
				ctx.SharedState["loopConfig"] = loopConfig
			}
			loopConfig[asString(ctx.Node["id"])] = M{"itemKey": itemKey, "indexKey": indexKey}
			return &nodeExecResult{Output: M{"count": len(items)}, ForeachItems: items}, nil
		},
	},
	{
		TypeCode: "delay.wait", Label: "等待",
		ConfigSchema: M{
			"type": "object",
			"properties": M{
				"durationMs": M{"type": "integer", "title": "等待毫秒", "default": 1000, "minimum": 0, "maximum": 600000},
			},
		},
		Execute: func(ctx *nodeExecContext) (*nodeExecResult, error) {
			config, _ := ctx.Node["config"].(map[string]any)
			durationMs := asInt64(config["durationMs"])
			if durationMs > 0 {
				time.Sleep(time.Duration(durationMs) * time.Millisecond)
			}
			return &nodeExecResult{Output: M{"durationMs": durationMs}}, nil
		},
	},
	{
		TypeCode: "end", Label: "结束",
		ConfigSchema: M{"type": "object", "properties": M{}},
		Execute: func(ctx *nodeExecContext) (*nodeExecResult, error) {
			return &nodeExecResult{Output: M{"ended": true}, Terminate: true}, nil
		},
	},
}

func getNodeDefinition(typeCode string) (*workflowNodeDefinition, error) {
	for _, definition := range workflowNodeDefinitions {
		if definition.TypeCode == typeCode {
			return definition, nil
		}
	}
	return nil, bizErr("Unknown workflow node type: %s", typeCode)
}

// ListNodeDefinitions 节点定义列表(编辑器面板用)。
func (a *App) ListNodeDefinitions() []M {
	result := make([]M, 0, len(workflowNodeDefinitions))
	for _, definition := range workflowNodeDefinitions {
		result = append(result, M{
			"typeCode": definition.TypeCode, "label": definition.Label, "configSchema": definition.ConfigSchema,
		})
	}
	return result
}

func setNodeOutput(ctx *nodeExecContext, output M) {
	nodeOutputs, _ := ctx.SharedState["nodeOutputs"].(map[string]any)
	if nodeOutputs == nil {
		nodeOutputs = M{}
		ctx.SharedState["nodeOutputs"] = nodeOutputs
	}
	nodeOutputs[asString(ctx.Node["id"])] = output
}

func compareValues(actual, expected any, operator string) bool {
	if operator == "truthy" {
		return isTruthy(actual)
	}
	if operator == "eq" {
		return pyStr(actual) == pyStr(expected)
	}
	actualNumber, okActual := toFloatFlexible(actual)
	expectedNumber, okExpected := toFloatFlexible(expected)
	if !okActual || !okExpected {
		return false
	}
	switch operator {
	case "gt":
		return actualNumber > expectedNumber
	case "gte":
		return actualNumber >= expectedNumber
	case "lt":
		return actualNumber < expectedNumber
	case "lte":
		return actualNumber <= expectedNumber
	default:
		return false
	}
}

func isTruthy(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case string:
		return typed != ""
	case float64:
		return typed != 0
	case int64:
		return typed != 0
	case []any:
		return len(typed) > 0
	case map[string]any:
		return len(typed) > 0
	default:
		return true
	}
}

// pyStr 与 Python str() 对齐的比较文本(数值不带多余小数)。
func pyStr(value any) string {
	switch typed := value.(type) {
	case nil:
		return "None"
	case bool:
		if typed {
			return "True"
		}
		return "False"
	case float64:
		if typed == float64(int64(typed)) {
			return strconv.FormatInt(int64(typed), 10)
		}
		return strconv.FormatFloat(typed, 'g', -1, 64)
	default:
		return asString(value)
	}
}

func toFloatFlexible(value any) (float64, bool) {
	if number, ok := toFloat(value); ok {
		return number, true
	}
	if text, ok := value.(string); ok {
		parsed, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
		if err == nil {
			return parsed, true
		}
	}
	return 0, false
}
