package service

import (
	"encoding/json"
	"sort"
	"strings"

	"coinsphere/backend/plugin/sdk"
)

const workflowJSONSchema202012 = "https://json-schema.org/draft/2020-12/schema"

var workflowQuantConditionTypes = map[string]bool{
	"official.quant.volume_spike_condition": true,
	"official.quant.price_change_condition": true,
	"official.quant.macd_condition":         true,
	"official.quant.kdj_condition":          true,
	"official.quant.rsi_condition":          true,
	"official.quant.bollinger_condition":    true,
	"official.quant.code_strategy":          true,
}

func isWorkflowQuantConditionType(nodeType string) bool {
	return workflowQuantConditionTypes[nodeType]
}

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
			Type: desc.Type, Version: desc.Version, Title: workflowNodeTitle(desc.Type),
			Description: workflowNodeDescription(desc.Type), Kind: desc.Kind,
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
	return []sdk.NodeDescriptor{
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

func workflowNodeTitle(nodeType string) string {
	switch nodeType {
	case "core.manual":
		return "Manual trigger"
	case "core.schedule":
		return "Schedule trigger"
	case "core.event":
		return "Event trigger"
	case "core.constant":
		return "Constant"
	case "core.end":
		return "End"
	case "core.human_approval":
		return "Human approval"
	case "core.loop":
		return "Loop"
	case "core.loop_item":
		return "Loop item"
	case "core.loop_end":
		return "Loop end"
	case "official.connector.http":
		return "HTTP 请求"
	case "official.connector.webhook":
		return "Webhook 触发"
	case "official.connector.websocket":
		return "WebSocket 触发"
	case "official.ai.model_call":
		return "AI 模型调用"
	case "official.quant.realtime_candles":
		return "Binance K 线实时采集"
	case "official.quant.backfill_candles":
		return "Binance K 线补数"
	case "official.quant.sync_instruments":
		return "Binance 币种元数据采集"
	case "official.quant.evaluate":
		return "量化策略评估"
	case "official.quant.volume_spike_condition":
		return "放量判断"
	case "official.quant.price_change_condition":
		return "价格波动判断"
	case "official.quant.macd_condition":
		return "MACD 判断"
	case "official.quant.kdj_condition":
		return "KDJ 判断"
	case "official.quant.rsi_condition":
		return "RSI 判断"
	case "official.quant.bollinger_condition":
		return "布林带判断"
	case "official.quant.market_signal":
		return "输出信号"
	case "official.quant.backtest":
		return "量化策略回测"
	case "official.quant.backtest_start":
		return "回测开始"
	case "official.quant.code_strategy":
		return "代码策略"
	case "official.quant.position":
		return "仓位计算"
	case "official.quant.output_signal":
		return "输出策略信号"
	case "official.quant.signal":
		return "量化信号"
	case "official.quant.paper_execute":
		return "Paper 执行"
	case "official.notification.in_app":
		return "站内通知"
	case "official.notification.dingtalk":
		return "钉钉通知"
	case "official.qq.receive":
		return "QQ 消息接收"
	case "official.qq.send":
		return "QQ 消息发送"
	case "official.notification.smtp":
		return "邮件通知"
	default:
		return nodeType
	}
}

func workflowNodeDescription(nodeType string) string {
	switch nodeType {
	case "core.manual":
		return "Starts one run on demand."
	case "core.schedule":
		return "Starts one run on an interval or six-field Cron schedule."
	case "core.event":
		return "Starts one run for each matching CloudEvent."
	case "core.constant":
		return "Emits a configured text value."
	case "core.end":
		return "Marks the end of a branch."
	case "core.human_approval":
		return "Persists an approval task and releases execution capacity while waiting."
	case "core.loop":
		return "Runs an embedded acyclic graph with a fixed iteration and absolute time limit."
	case "core.loop_item":
		return "Provides the current iteration and carried value inside a loop."
	case "core.loop_end":
		return "Returns the carried value from one loop iteration."
	case "official.quant.realtime_candles":
		return "Streams and publishes closed Binance Spot or USD-M candles."
	case "official.quant.backfill_candles":
		return "Backfills closed Binance Spot or USD-M candles without publishing events."
	case "official.quant.sync_instruments":
		return "Synchronizes a filtered Binance instrument metadata snapshot."
	case "official.quant.evaluate":
		return "Evaluates a compiled Go strategy against closed candles."
	case "official.quant.volume_spike_condition", "official.quant.price_change_condition", "official.quant.macd_condition",
		"official.quant.kdj_condition", "official.quant.rsi_condition", "official.quant.bollinger_condition":
		return "Evaluates one indicator rule against closed candles."
	case "official.quant.market_signal":
		return "Persists one idempotent market signal for a matched closed candle."
	case "official.quant.backtest":
		return "Runs a deterministic next-open backtest over stored candles."
	case "official.quant.backtest_start":
		return "Drives the shared Quant subgraph candle by candle with run-time backtest parameters."
	case "official.quant.code_strategy":
		return "Evaluates restricted CEL over closed OHLCV series and Decimal functions."
	case "official.quant.position":
		return "Converts one reached path into a market-bounded target position."
	case "official.quant.output_signal":
		return "Merges position candidates and persists only real-time target changes."
	case "official.quant.signal":
		return "Persists a replaceable strategy signal fact."
	case "official.quant.paper_execute":
		return "Revalidates a public quote and applies the complete Paper risk gate atomically."
	case "official.notification.in_app":
		return "Persists one idempotent in-app notification delivery."
	case "official.notification.dingtalk":
		return "Sends one idempotent DingTalk robot notification."
	case "official.qq.receive":
		return "Starts one run for each QQ group mention or direct message."
	case "official.qq.send":
		return "Sends one idempotent QQ group or direct message."
	case "official.notification.smtp":
		return "Sends one idempotent TLS-protected SMTP notification."
	default:
		return "Compiled plugin node."
	}
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
