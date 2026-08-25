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
	ConfigSchema json.RawMessage           `json:"configSchema"`
	UISchema     json.RawMessage           `json:"uiSchema"`
	InputSchema  json.RawMessage           `json:"inputSchema"`
	OutputSchema json.RawMessage           `json:"outputSchema"`
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
		available := desc.Kind == sdk.NodeKindAction || desc.Type == "core.manual" || desc.Type == "core.schedule"
		items = append(items, WorkflowNodeDefinitionView{
			Type: desc.Type, Version: desc.Version, Title: workflowNodeTitle(desc.Type),
			Description: workflowNodeDescription(desc.Type), Kind: desc.Kind,
			ConfigSchema: desc.ConfigSchema, UISchema: desc.UISchema,
			InputSchema: desc.InputSchema, OutputSchema: desc.OutputSchema,
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
	return []sdk.NodeDescriptor{
		{
			Type: "core.manual", Version: "1.0.0", Kind: sdk.NodeKindTrigger,
			ConfigSchema: empty, UISchema: json.RawMessage(`{"ui:order":[]}`), InputSchema: empty,
			OutputSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"triggeredAt":{"type":"string","format":"date-time"}},"required":["triggeredAt"],"additionalProperties":false}`),
			Pool:         sdk.PoolStream, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless,
		},
		{
			Type: "core.schedule", Version: "1.0.0", Kind: sdk.NodeKindTrigger,
			ConfigSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"everySeconds":{"type":"integer","title":"Interval (seconds)","minimum":60,"maximum":86400,"default":3600}},"required":["everySeconds"],"additionalProperties":false}`),
			UISchema:     json.RawMessage(`{"ui:order":["everySeconds"]}`), InputSchema: empty,
			OutputSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"triggeredAt":{"type":"string","format":"date-time"}},"required":["triggeredAt"],"additionalProperties":false}`),
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
	}
}

func workflowPorts(desc sdk.NodeDescriptor) ([]string, []string) {
	if desc.Kind == sdk.NodeKindTrigger {
		return []string{}, []string{"out"}
	}
	if desc.Type == "core.end" {
		return []string{"in"}, []string{}
	}
	return []string{"in"}, []string{"out"}
}

func workflowNodeTitle(nodeType string) string {
	switch nodeType {
	case "core.manual":
		return "Manual trigger"
	case "core.schedule":
		return "Schedule trigger"
	case "core.constant":
		return "Constant"
	case "core.end":
		return "End"
	default:
		return nodeType
	}
}

func workflowNodeDescription(nodeType string) string {
	switch nodeType {
	case "core.manual":
		return "Starts one batch on demand."
	case "core.schedule":
		return "Starts one batch at a fixed UTC interval."
	case "core.constant":
		return "Emits a configured text value."
	case "core.end":
		return "Marks the end of a branch."
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
