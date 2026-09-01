package service

import (
	"encoding/json"
	"sort"
	"strings"

	"coinsphere/backend/plugin/sdk"
)

const workflowJSONSchema202012 = "https://json-schema.org/draft/2020-12/schema"

type WorkflowSecretFieldView struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

type WorkflowNodeDefinitionView struct {
	Type         string                    `json:"type"`
	Version      string                    `json:"version"`
	Title        string                    `json:"title"`
	Description  string                    `json:"description"`
	Kind         sdk.NodeKind              `json:"kind"`
	Category     string                    `json:"category"`
	Color        string                    `json:"color"`
	Icon         string                    `json:"icon"`
	Width        int                       `json:"width"`
	Height       int                       `json:"height"`
	Capabilities sdk.NodeCapabilities      `json:"capabilities"`
	ConfigSchema json.RawMessage           `json:"configSchema"`
	UISchema     json.RawMessage           `json:"uiSchema"`
	InputSchema  json.RawMessage           `json:"inputSchema"`
	OutputSchema json.RawMessage           `json:"outputSchema"`
	Branches     []string                  `json:"branches,omitempty"`
	InputPorts   []string                  `json:"inputPorts"`
	OutputPorts  []string                  `json:"outputPorts"`
	SecretFields []WorkflowSecretFieldView `json:"secretFields"`
	Available    bool                      `json:"available"`
}

func (a *App) ListWorkflowNodeDefinitions() []WorkflowNodeDefinitionView {
	descriptors := a.workflowNodeDescriptors()
	items := make([]WorkflowNodeDefinitionView, 0, len(descriptors))
	for _, desc := range descriptors {
		inputPorts, outputPorts := workflowPorts(desc)
		available := desc.Type != "core.loop_item" && desc.Type != "core.loop_end"
		items = append(items, WorkflowNodeDefinitionView{
			Type: desc.Type, Version: desc.Version, Title: desc.Title,
			Description: desc.Description, Kind: desc.Kind, Category: desc.Category,
			Color: desc.Color, Icon: desc.Icon, Width: desc.Width, Height: desc.Height,
			Capabilities: desc.Capabilities,
			ConfigSchema: desc.ConfigSchema, UISchema: desc.UISchema,
			InputSchema: desc.InputSchema, OutputSchema: desc.OutputSchema,
			Branches:   append([]string(nil), desc.Branches...),
			InputPorts: inputPorts, OutputPorts: outputPorts,
			SecretFields: workflowSecretFieldViews(desc.ConfigSchema), Available: available,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Type < items[j].Type })
	return items
}

func (a *App) workflowNodeDescriptors() map[string]sdk.NodeDescriptor {
	items := coreWorkflowNodeDescriptors()
	if a.Plugins != nil {
		items = append(items, a.Plugins.Nodes()...)
	}
	result := make(map[string]sdk.NodeDescriptor, len(items))
	for _, item := range items {
		result[item.Type] = item
	}
	return result
}

func coreWorkflowNodeDescriptors() []sdk.NodeDescriptor {
	empty := json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false}`)
	valueInput := json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"value":{"type":"object","title":"Value","x-coinsphere-field-source":true}},"additionalProperties":false}`)
	items := []sdk.NodeDescriptor{
		{
			Type: "core.manual", Version: "1.0.0", Kind: sdk.NodeKindTrigger,
			ConfigSchema: empty, UISchema: json.RawMessage(`{"ui:order":[]}`), InputSchema: empty,
			OutputSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"triggeredAt":{"type":"string","format":"date-time"}},"required":["triggeredAt"],"additionalProperties":false}`),
			Pool:         sdk.PoolStream, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless,
		},
		{
			Type: "core.schedule", Version: "1.0.0", Kind: sdk.NodeKindTrigger,
			ConfigSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"everySeconds":{"type":"integer","title":"Interval (seconds)","minimum":60,"maximum":86400,"default":3600},"cronExpression":{"type":"string","title":"Cron expression","minLength":1,"maxLength":255},"timeZone":{"type":"string","title":"Time zone","minLength":1,"maxLength":255,"default":"Asia/Shanghai"}},"oneOf":[{"required":["everySeconds"]},{"required":["cronExpression","timeZone"]}],"additionalProperties":false}`),
			UISchema:     json.RawMessage(`{"ui:order":["everySeconds","cronExpression","timeZone"]}`), InputSchema: empty,
			OutputSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"triggeredAt":{"type":"string","format":"date-time"}},"required":["triggeredAt"],"additionalProperties":false}`),
			Pool:         sdk.PoolStream, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless,
		},
		{
			Type: "core.event", Version: "1.0.0", Kind: sdk.NodeKindTrigger,
			ConfigSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"types":{"type":"array","title":"Event types","items":{"type":"string","minLength":1,"maxLength":255},"minItems":1,"uniqueItems":true},"source":{"type":"string","title":"Source","maxLength":500},"subject":{"type":"string","title":"Subject","maxLength":500}},"required":["types"],"additionalProperties":false}`),
			UISchema:     json.RawMessage(`{"ui:order":["types","source","subject"]}`), InputSchema: empty,
			OutputSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`),
			Pool:         sdk.PoolStream, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless,
		},
		{
			Type: "core.constant", Version: "1.0.0", Kind: sdk.NodeKindAction,
			ConfigSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"value":{"type":"string","title":"Value","description":"Value emitted by this node."}},"required":["value"],"additionalProperties":false}`),
			UISchema:     json.RawMessage(`{"ui:order":["value"],"value":{"ui:widget":"textarea"}}`), InputSchema: empty,
			OutputSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`),
			Pool:         sdk.PoolCompute, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless,
		},
		{
			Type: "core.end", Version: "1.0.0", Kind: sdk.NodeKindAction,
			ConfigSchema: empty, UISchema: json.RawMessage(`{"ui:order":[]}`),
			InputSchema:  json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"result":{"type":"string","title":"Result","x-coinsphere-field-source":true}},"additionalProperties":false}`),
			OutputSchema: empty, Pool: sdk.PoolCompute, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless,
		},
		{
			Type: "core.human_approval", Version: "1.0.0", Kind: sdk.NodeKindAction,
			ConfigSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"decisionMode":{"type":"string","title":"Decision mode","enum":["human","auto"],"default":"human"},"taskType":{"type":"string","title":"Task type","minLength":1,"maxLength":64,"default":"approval"},"prompt":{"type":"string","title":"Prompt","maxLength":500,"default":"Review this workflow action."},"expiresSeconds":{"type":"integer","title":"Expires after (seconds)","minimum":60,"maximum":604800,"default":86400}},"required":["taskType","prompt","expiresSeconds"],"additionalProperties":false}`),
			UISchema:     json.RawMessage(`{"ui:order":["decisionMode","taskType","prompt","expiresSeconds"]}`),
			InputSchema:  json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"businessKey":{"type":"string","title":"Business key","minLength":1,"maxLength":256,"x-coinsphere-field-source":true}},"required":["businessKey"],"additionalProperties":false}`),
			OutputSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"taskId":{"type":"integer"},"status":{"type":"string","enum":["approved","rejected","expired","superseded"]},"decidedAt":{"type":"string","format":"date-time"}},"required":["taskId","status","decidedAt"],"additionalProperties":false}`),
			Pool:         sdk.PoolStream, SideEffect: sdk.SideEffectHumanAction, State: sdk.StateStateless,
		},
		{
			Type: "core.loop", Version: "1.0.0", Kind: sdk.NodeKindAction,
			ConfigSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"maxIterations":{"type":"integer","title":"Maximum iterations","minimum":1,"maximum":100,"default":10},"timeoutSeconds":{"type":"integer","title":"Absolute timeout (seconds)","minimum":1,"maximum":86400,"default":60},"exitCondition":{"type":"string","title":"Boolean exit condition","minLength":1,"maxLength":4096,"default":"input.iteration >= 1"},"body":{"type":"object","title":"Embedded DAG","default":{"schemaVersion":1,"nodes":[{"nodeInstanceId":"item","nodeType":"core.loop_item","nodeVersion":"1.0.0","config":{},"position":{"x":80,"y":80}},{"nodeInstanceId":"done","nodeType":"core.loop_end","nodeVersion":"1.0.0","config":{},"position":{"x":360,"y":80}}],"edges":[{"edgeId":"item-done","sourceNodeInstanceId":"item","sourcePort":"out","targetNodeInstanceId":"done","targetPort":"in"}]}}},"required":["maxIterations","timeoutSeconds","exitCondition","body"],"additionalProperties":false}`),
			UISchema:     json.RawMessage(`{"ui:order":["maxIterations","timeoutSeconds","exitCondition","body"],"exitCondition":{"ui:widget":"textarea"}}`),
			InputSchema:  valueInput,
			OutputSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"iterations":{"type":"integer"},"exited":{"type":"boolean"},"value":{"type":"object"}},"required":["iterations","exited","value"],"additionalProperties":false}`),
			Pool:         sdk.PoolStream, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless,
		},
		{
			Type: "core.loop_item", Version: "1.0.0", Kind: sdk.NodeKindAction,
			ConfigSchema: empty, UISchema: json.RawMessage(`{"ui:order":[]}`),
			InputSchema:  json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"iteration":{"type":"integer"},"value":{"type":"object"}},"additionalProperties":false}`),
			OutputSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"iteration":{"type":"integer"},"value":{"type":"object"}},"required":["iteration","value"],"additionalProperties":false}`),
			Pool:         sdk.PoolStream, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless,
		},
		{
			Type: "core.loop_end", Version: "1.0.0", Kind: sdk.NodeKindAction,
			ConfigSchema: empty, UISchema: json.RawMessage(`{"ui:order":[]}`), InputSchema: valueInput,
			OutputSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"value":{"type":"object"}},"required":["value"],"additionalProperties":false}`),
			Pool:         sdk.PoolStream, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless,
		},
	}
	meta := map[string][5]string{
		"core.manual":         {"手动开始", "声明手动触发入口节点", "开始", "#2563eb", "play"},
		"core.schedule":       {"定时开始", "声明定时触发入口节点", "开始", "#1d4ed8", "clock"},
		"core.event":          {"事件开始", "声明事件触发入口节点", "开始", "#0f766e", "radio"},
		"core.constant":       {"常量", "输出配置的常量文本", "数据", "#0891b2", "braces"},
		"core.end":            {"结束", "声明当前执行链路结束", "结束", "#dc2626", "circle-stop"},
		"core.human_approval": {"人工审批", "创建人工审批任务并等待处理", "控制", "#d97706", "user-check"},
		"core.loop":           {"循环", "在限制次数和时间内执行内嵌流程", "控制", "#ca8a04", "repeat"},
		"core.loop_item":      {"循环项", "提供当前循环值", "控制", "#ca8a04", "repeat-1"},
		"core.loop_end":       {"循环结束", "返回单次循环结果", "控制", "#ca8a04", "circle-stop"},
	}
	for index := range items {
		value := meta[items[index].Type]
		items[index].Title, items[index].Description, items[index].Category = value[0], value[1], value[2]
		items[index].Color, items[index].Icon = value[3], value[4]
		items[index].Width, items[index].Height = 220, 72
		items[index].Capabilities.Stateless = items[index].State == sdk.StateStateless
		items[index].Capabilities.Deterministic = items[index].SideEffect == sdk.SideEffectNone
	}
	return items
}

func workflowPorts(desc sdk.NodeDescriptor) ([]string, []string) {
	if desc.Kind == sdk.NodeKindTrigger {
		if len(desc.Branches) > 0 {
			return []string{}, append([]string(nil), desc.Branches...)
		}
		return []string{}, []string{"out"}
	}
	if desc.Type == "core.loop_item" {
		return []string{}, []string{"out"}
	}
	if desc.Type == "core.end" || desc.Type == "core.loop_end" {
		return []string{"in"}, []string{}
	}
	if len(desc.Branches) > 0 {
		return []string{"in"}, append([]string(nil), desc.Branches...)
	}
	return []string{"in"}, []string{"out"}
}

func workflowSecretFieldViews(raw json.RawMessage) []WorkflowSecretFieldView {
	fields, required := workflowSecretFields(raw)
	items := make([]WorkflowSecretFieldView, 0, len(fields))
	for name, property := range fields {
		items = append(items, WorkflowSecretFieldView{
			Name: name, Title: schemaString(property, "title", name),
			Description: schemaString(property, "description", ""), Required: required[name],
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}

func workflowSecretFields(raw json.RawMessage) (map[string]map[string]any, map[string]bool) {
	var schema map[string]any
	_ = json.Unmarshal(raw, &schema)
	properties, _ := schema["properties"].(map[string]any)
	required := stringSetFromAny(schema["required"])
	fields := make(map[string]map[string]any)
	for name, value := range properties {
		property, _ := value.(map[string]any)
		if secret, _ := property["x-coinsphere-secret"].(bool); secret {
			fields[name] = property
		}
	}
	return fields, required
}

func schemaString(schema map[string]any, key, fallback string) string {
	if value, ok := schema[key].(string); ok && strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func stringSetFromAny(value any) map[string]bool {
	result := map[string]bool{}
	items, _ := value.([]any)
	for _, item := range items {
		if text, ok := item.(string); ok {
			result[text] = true
		}
	}
	return result
}
