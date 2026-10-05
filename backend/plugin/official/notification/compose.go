package notification

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"coinsphere/backend/plugin/sdk"
)

type composeSource struct {
	NodeInstanceID string `json:"nodeInstanceId"`
	Branch         string `json:"branch"`
}
type composeConfig struct {
	SubjectSources []composeSource `json:"subjectSources"`
	MessageSources []composeSource `json:"messageSources"`
}
type composeAction struct{}

func registerCompose(registrar sdk.Registrar) error {
	return registrar.Action(sdk.NodeDescriptor{
		Type: "official.notification.compose", Version: "1.0.0", Kind: sdk.NodeKindAction,
		Title: "通知内容组合", Description: "按配置顺序组合实际触发的条件来源", Category: "notification", Color: "#7c3aed", Icon: "combine", Width: 220, Height: 72,
		ExecutionPermissions: []string{"plugins.official.notification.execute"}, Capabilities: sdk.NodeCapabilities{Deterministic: true, Stateless: true},
		Pool: sdk.PoolCompute, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless,
		ConfigSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"subjectSources":{"type":"array","maxItems":256,"items":{"$ref":"#/$defs/source"}},"messageSources":{"type":"array","maxItems":256,"items":{"$ref":"#/$defs/source"}}},"required":["subjectSources","messageSources"],"additionalProperties":false,"$defs":{"source":{"type":"object","properties":{"nodeInstanceId":{"type":"string","minLength":1,"maxLength":128},"branch":{"type":"string","minLength":1,"maxLength":32}},"required":["nodeInstanceId","branch"],"additionalProperties":false}}}`),
		InputSchema:  json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false}`),
		OutputSchema: notificationInputSchema(),
	}, composeAction{})
}
func (composeAction) Execute(_ context.Context, request sdk.ActionRequest) (sdk.ActionResult, error) {
	var config composeConfig
	if json.Unmarshal(request.Config, &config) != nil {
		return sdk.ActionResult{}, errors.New("invalid message composition config")
	}
	collect := func(sources []composeSource, field string) []string {
		values := []string{}
		for _, source := range sources {
			for _, edge := range request.Incoming {
				if edge.NodeInstanceID != source.NodeInstanceID || edge.SourcePort != source.Branch {
					continue
				}
				var output map[string]any
				if json.Unmarshal(edge.Output, &output) != nil || output["triggered"] != true {
					continue
				}
				if value, ok := output[field].(string); ok && value != "" {
					values = append(values, value)
				}
				break
			}
		}
		return values
	}
	digest := sha256.Sum256([]byte(strings.Join(collect(config.SubjectSources, "businessKey"), "\x00")))
	text := []rune(strings.Join(collect(config.MessageSources, "summary"), "\n"))
	if len(text) > 2000 {
		text = text[:2000]
	}
	// Empty composed text preserves the old definition's validation failure.
	return sdk.ActionResult{Output: mustMarshal(map[string]any{"subjectKey": "condition-subject:" + hex.EncodeToString(digest[:16]), "message": string(text)})}, nil
}
