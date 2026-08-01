// nodes.go —— 两张"注册表",把"类型编码"映射到"处理函数"。
//
// 注册表模式(registry pattern)是本文件的核心:与其写一大串 if/switch 判断"是什么类型就干什么",
// 不如把每种类型和它的处理函数成对登记进一张表,用时按编码查表、拿到函数直接调用。
// 新增一种类型只要往表里加一条,别处不用改。
//
// 本文件有两张表:
//   1. taskDefinitions        —— 可执行"任务"(如抓新闻),供 task.run 节点调用;
//   2. workflowNodeDefinitions —— 工作流"节点"类型(开始/任务/条件/循环/HTTP/通知…),
//                                 engine.go 跑图时就靠它按节点 type 找到对应处理函数。
// 所谓"代码即真源":这些能力直接写死在代码里,不依赖数据库配置。

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
//
// 重点看 Execute 字段:类型是 func(a *App, inputs M) (M, error) —— 一个"函数类型"。
// 结构体里可以直接存一个"函数值",这正是注册表的关键:把"这种任务怎么执行"的那段代码
// 当成数据挂在字段上,需要时取出来调用。见 GO入门笔记『变量、函数、错误』。
// (M 是本项目别名 = map[string]any;ParameterSchema 用 JSON Schema 描述该任务接收哪些参数。)
type taskDefinition struct {
	Code            string
	Label           string
	Description     string
	ParameterSchema M
	Execute         func(a *App, inputs M) (M, error)
}

// taskDefinitions 是所有内置任务的清单(注册表本体)。
// []*taskDefinition 表示"taskDefinition 指针的切片";里面每个 {...} 就是一条登记,
// 元素类型是指针,所以本可写 &taskDefinition{...},Go 允许省略前面的 &taskDefinition。
// 目前只登记了一种任务:抓取 Blockbeats 快讯。
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
		// 这里给 Execute 字段赋了一个"匿名函数"(函数字面量),就是这种任务真正执行的逻辑:
		// 读分页参数(缺省给默认值)→ 调 syncLatestNews 抓取 → 返回统计结果。
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

// getTaskDefinition 按 code 在注册表里查任务:for range 逐条扫描,找到就返回,找不到返回业务错误。
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
	// make([]M, 0, n):新建长度 0、预留容量 n 的切片;下面用 append 一条条填。
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
	// 先 copy 一份再排序,避免打乱全局 taskDefinitions 的顺序。
	items := make([]*taskDefinition, len(taskDefinitions))
	copy(items, taskDefinitions)
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].Code) < strings.ToLower(items[j].Code)
	})
	keywordText := strings.ToLower(strings.TrimSpace(keyword))
	if keywordText != "" {
		// items[:0] 是常见惯用法:复用同一底层数组做"原地筛选",把命中的元素依次写回开头。
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
	// 按页码算出切片的起止下标,并夹到合法范围内;items[start:end] 就是本页数据(左闭右开)。
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
	// GORM 查询:Where 里 ? 是占位符(值单独传,防 SQL 注入),First 查一条并写回 &existing。
	// .Error == nil 表示确实查到了记录。见 GO入门笔记『框架:GORM』。
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
	// 三种情况:有覆盖值且记录已存在 → 更新;有覆盖值但没记录 → 新建;没有任何覆盖值 → 删掉旧记录。
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
//
// 合并有优先级:先铺 schema 默认值,再盖全局覆盖,最后盖节点运行时输入 ——
// 后写的覆盖先写的,所以运行时输入优先级最高。
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

// extractSchemaDefaultParams 从 schema 的 properties 里,把每个字段声明的 default 值挑出来。
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

// matchesSchemaType 判断一个值是否符合 schema 声明的类型(type 为数组时"满足其一即可")。
func matchesSchemaType(value, expectedType any) bool {
	if typeList, ok := expectedType.([]any); ok {
		for _, item := range typeList {
			if matchesSchemaType(value, item) {
				return true
			}
		}
		return false
	}
	// switch 按类型名分派;Go 的 case 默认不"穿透"到下一个,不用手写 break。见 GO入门笔记『其它会撞见的小语法』。
	// 每个 case 里再用类型断言 value.(T) 检查这个值实际是不是对应的 Go 类型。
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

// toFloat 尽量把一个 any 值转成 float64;第二个返回值表示能不能转成。
// switch value.(type) 是"类型 switch":按值的真实类型分支,进入 case 后 typed 就已是那个具体类型。
// 见 GO入门笔记『其它会撞见的小语法』。
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
//
// engine.go 跑图时会读这几个字段决定下一步:
//   Output         节点输出数据(存成输出快照,也可被下游引用);
//   SelectedBranch 条件节点选中的分支名(*string 指针:nil 表示"没选分支");
//   ForeachItems   循环节点要逐个遍历的数组(非 nil 就触发 foreach);
//   Terminate      是否就此结束整张图(结束节点置 true)。
type nodeExecResult struct {
	Output         M
	SelectedBranch *string
	ForeachItems   []any
	Terminate      bool
}

// nodeExecContext 节点执行上下文。
//
// engine.go 每执行一个节点,就把这个"上下文包"传给它的 Execute:里面有 App、当前执行/节点日志、
// 整张图、共享状态、触发上下文等。注意 PublishEvent 是一个函数类型字段 —— 引擎注入的回调,
// 节点想发领域事件时调它就行,不必关心底层怎么发。
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

// workflowNodeDefinition 一种工作流节点类型的登记项:把类型编码 TypeCode 和它的处理函数 Execute 绑在一起。
// Execute 的类型 func(ctx *nodeExecContext) (*nodeExecResult, error) 就是所有节点统一的"处理器"签名。
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

// mergeProps 合并两份 schema 属性(base 打底,extra 覆盖同名键),生成新 map,不改动传入的两个。
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

// startNodeExecute 所有 start.* 开始节点共用的处理函数:只是把触发信息原样输出。
// 下面登记表里多处直接把这个函数名当值填给 Execute 字段 —— 函数作为值复用的典型例子。
func startNodeExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	config, _ := ctx.Node["config"].(map[string]any)
	return &nodeExecResult{Output: M{
		"started":     true,
		"triggerType": ctx.TriggerCtx["triggerType"],
		"entryKey":    asString(config["entryKey"]),
	}}, nil
}

// workflowNodeDefinitions 是工作流所有内置节点类型的注册表。engine.go 按节点的 type 到这里查处理器。
// 内置类型一览:
//   start.manual/schedule/event/webhook  四种"开始"节点(手动/定时/事件/Webhook 触发),共用 startNodeExecute;
//   task.run          执行一个任务定义(调 taskDefinitions 里的 Execute);
//   event.publish     发布一条领域事件;
//   notify            发送通知(站内信/钉钉/邮件);
//   http.request      发起一次 HTTP 请求;
//   condition.branch  条件判断,输出 true/false 分支;
//   foreach           遍历数组,对下游子图逐个执行;
//   delay.wait        等待若干毫秒;
//   end               结束节点,终止整张图。
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
			// task.run 节点:按配置里的 taskDefinitionCode 到任务注册表查出任务,合并输入后执行,
			// 结果写进 sharedState["taskResult"] 供下游引用。这就是两张注册表衔接的地方。
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
			// defer 登记"函数返回前一定执行"的收尾动作,这里确保响应体最终被关闭(不管后面从哪条路 return)。见 GO入门笔记『defer』。
			defer response.Body.Close()
			// io.LimitReader 限制最多读 4MB,避免超大响应把内存撑爆。
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
			// SelectedBranch: &branch —— 用 & 取 branch 的地址,填进 *string 字段;
			// engine.go 看到它非 nil,就只沿着 branchKey 等于 "true"/"false" 的边继续走。
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
			// 把要遍历的数组放进 ForeachItems 返回;engine.go 收到后会对每个元素跑一遍下游子图。
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
			// Terminate: true 告诉引擎:到此结束整张图,后面的边都不再走。
			return &nodeExecResult{Output: M{"ended": true}, Terminate: true}, nil
		},
	},
}

// getNodeDefinition 按 typeCode 在节点注册表里查处理器。engine.go 跑图时就靠它把"节点类型"变成"要调用的函数"。
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

// setNodeOutput 把某个节点的输出按"节点 id"存进 sharedState["nodeOutputs"],方便下游按 id 取用。
// (第一次用时若该 map 还不存在,就先建一个 —— 惰性初始化。)
func setNodeOutput(ctx *nodeExecContext, output M) {
	nodeOutputs, _ := ctx.SharedState["nodeOutputs"].(map[string]any)
	if nodeOutputs == nil {
		nodeOutputs = M{}
		ctx.SharedState["nodeOutputs"] = nodeOutputs
	}
	nodeOutputs[asString(ctx.Node["id"])] = output
}

// compareValues 按 operator 比较两个值:truthy 看真假,eq 按文本比,其余(gt/gte/lt/lte)按数字比。
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

// isTruthy 判断一个值是否"为真"(仿脚本语言:nil/空串/0/空数组/空 map 都算假)。
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
