package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"coinsphere/backend/plugin/official/internal/safehttp"
	"coinsphere/backend/plugin/sdk"
)

const (
	pluginID           = "official.ai"
	maxAIResponseBytes = 1 << 20
)

var emptyObjectSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false}`)

func Register(registry *sdk.Registry, client *safehttp.Client) error {
	return registry.RegisterPlugin(sdk.PluginDescriptor{
		ID: pluginID, Name: "人工智能", Version: "1.0.0",
		Contributes: []string{"nodes", "resultPages"},
	}, func(registrar sdk.Registrar) error { return register(registrar, client) })
}

func register(registrar sdk.Registrar, client *safehttp.Client) error {
	if err := registrar.Action(sdk.NodeDescriptor{
		Type: "official.ai.model_call", Version: "1.0.0", Kind: sdk.NodeKindAction,
		ConfigSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"endpoint":{"type":"string","title":"OpenAI-compatible endpoint","format":"uri","maxLength":2048},"model":{"type":"string","title":"Model","minLength":1,"maxLength":200},"timeoutSeconds":{"type":"integer","title":"Timeout (seconds)","minimum":1,"maximum":120,"default":30},"apiKey":{"type":"string","title":"API key","x-coinsphere-secret":true}},"required":["endpoint","model","timeoutSeconds","apiKey"],"additionalProperties":false}`),
		UISchema:     json.RawMessage(`{"ui:order":["endpoint","model","timeoutSeconds","apiKey"]}`),
		InputSchema:  json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"prompt":{"type":"string","title":"Prompt","minLength":1,"maxLength":32768,"x-coinsphere-field-source":true},"data":{"type":"object","title":"Structured data","x-coinsphere-field-source":true}},"required":["prompt","data"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"model":{"type":"string"},"data":{"type":"object"},"usage":{"type":"object"}},"required":["model","data","usage"],"additionalProperties":false}`),
		Pool:         sdk.PoolStream, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless,
	}, aiModelCallAction{client: client}); err != nil {
		return err
	}
	return registrar.ResultPage(sdk.ResultPageDescriptor{
		PageKey: "calls", Title: "AI 调用",
		ComponentEntry: "./official/ai/ResultPage.vue", ScopeSchema: emptyObjectSchema, Mobile: true,
	})
}

type aiModelCallAction struct{ client *safehttp.Client }

func (a aiModelCallAction) Execute(ctx context.Context, request sdk.ActionRequest) (sdk.ActionResult, error) {
	var config struct {
		Endpoint       string `json:"endpoint"`
		Model          string `json:"model"`
		TimeoutSeconds int    `json:"timeoutSeconds"`
	}
	var input struct {
		Prompt string         `json:"prompt"`
		Data   map[string]any `json:"data"`
	}
	if json.Unmarshal(request.Config, &config) != nil || json.Unmarshal(request.Input, &input) != nil ||
		strings.TrimSpace(input.Prompt) == "" || input.Data == nil {
		return sdk.ActionResult{}, errors.New("AI model call configuration or input is invalid")
	}
	target, err := url.ParseRequestURI(config.Endpoint)
	if err != nil || !target.IsAbs() || safehttp.IsBinanceDomain(target.Hostname()) {
		return sdk.ActionResult{}, safehttp.Blocked("AI endpoint is invalid or prohibited")
	}
	content, err := json.Marshal(map[string]any{"prompt": input.Prompt, "data": input.Data})
	if err != nil || len(content) > maxAIResponseBytes {
		return sdk.ActionResult{}, errors.New("AI model input exceeds the 1 MiB limit")
	}
	payload, err := json.Marshal(map[string]any{
		"model":           config.Model,
		"messages":        []map[string]string{{"role": "user", "content": string(content)}},
		"response_format": map[string]string{"type": "json_object"},
	})
	if err != nil {
		return sdk.ActionResult{}, errors.New("encode AI model request failed")
	}
	apiKey, err := request.Secrets.Read(ctx, "apiKey")
	if err != nil || len(apiKey) == 0 || len(apiKey) > 16<<10 {
		return sdk.ActionResult{}, errors.New("AI API key is unavailable")
	}
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(config.TimeoutSeconds)*time.Second)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(callCtx, http.MethodPost, target.String(), bytes.NewReader(payload))
	if err != nil {
		return sdk.ActionResult{}, errors.New("create AI model request failed")
	}
	httpRequest.Header.Set("Authorization", "Bearer "+string(apiKey))
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("User-Agent", "CoinSphere-AI/1.0")
	httpRequest.Header.Set("Idempotency-Key", request.OperationKey)
	response, err := a.client.Do(httpRequest)
	if err != nil {
		return sdk.ActionResult{}, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxAIResponseBytes+1))
	if err != nil || len(raw) > maxAIResponseBytes {
		return sdk.ActionResult{}, errors.New("AI model response exceeds the 1 MiB limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return sdk.ActionResult{}, fmt.Errorf("AI model response status %d", response.StatusCode)
	}
	var modelResponse struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage map[string]any `json:"usage"`
	}
	if json.Unmarshal(raw, &modelResponse) != nil || len(modelResponse.Choices) != 1 || modelResponse.Usage == nil {
		return sdk.ActionResult{}, errors.New("AI model response is invalid")
	}
	var data map[string]any
	if json.Unmarshal([]byte(modelResponse.Choices[0].Message.Content), &data) != nil || data == nil {
		return sdk.ActionResult{}, errors.New("AI model response content must be a JSON object")
	}
	if modelResponse.Model == "" {
		modelResponse.Model = config.Model
	}
	return sdk.ActionResult{Output: mustMarshal(map[string]any{
		"model": modelResponse.Model, "data": data, "usage": modelResponse.Usage,
	})}, nil
}

var _ sdk.ActionHandler = aiModelCallAction{}

func mustMarshal(value any) json.RawMessage {
	raw, _ := json.Marshal(value)
	return raw
}
