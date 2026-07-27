package service

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// aiRuntimeConfig 解密后的模型运行配置。
type aiRuntimeConfig struct {
	ConfigID      int64
	ProviderType  string
	ProviderLabel string
	DisplayName   string
	ModelName     string
	BaseURL       string
	APIKey        string
	Headers       map[string]string
	ExtraBody     M
	TimeoutMs     int
}

func (c *aiRuntimeConfig) httpClient() *http.Client {
	timeout := time.Duration(c.TimeoutMs) * time.Millisecond
	if timeout < 5*time.Second {
		timeout = 5 * time.Second
	}
	return &http.Client{Timeout: timeout}
}

// aiChunk 一段流式输出。
type aiChunk struct {
	Reasoning string
	Content   string
}

type chunkHandler func(chunk aiChunk) error

// streamAiChat 按协议类型流式调用模型。
func streamAiChat(cfg *aiRuntimeConfig, messages []M, onChunk chunkHandler) error {
	switch cfg.ProviderType {
	case aiProviderOpenAICompatible:
		return streamOpenAICompatible(cfg, messages, onChunk)
	case aiProviderAnthropic:
		return streamAnthropic(cfg, messages, onChunk)
	case aiProviderGemini:
		return streamGemini(cfg, messages, onChunk)
	default:
		return bizErr("暂不支持的模型协议类型: %s", cfg.ProviderType)
	}
}

// validateAiConfig 连通性校验。
func validateAiConfig(cfg *aiRuntimeConfig) (bool, string) {
	var err error
	switch cfg.ProviderType {
	case aiProviderOpenAICompatible:
		err = openAIValidate(cfg)
	case aiProviderAnthropic:
		err = anthropicValidate(cfg)
	case aiProviderGemini:
		err = geminiValidate(cfg)
	default:
		err = bizErr("暂不支持的模型协议类型: %s", cfg.ProviderType)
	}
	if err != nil {
		return false, err.Error()
	}
	return true, "连接校验成功"
}

// ---------- OpenAI 兼容 ----------

func openAIBody(cfg *aiRuntimeConfig, messages []M, stream bool) M {
	body := M{"model": cfg.ModelName, "messages": messages}
	if stream {
		body["stream"] = true
	} else {
		body["max_tokens"] = 1
	}
	for key, value := range cfg.ExtraBody {
		body[key] = value
	}
	return body
}

func openAIRequest(cfg *aiRuntimeConfig, body M) (*http.Request, error) {
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(cfg.BaseURL, "/")+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	for key, value := range cfg.Headers {
		req.Header.Set(key, value)
	}
	return req, nil
}

func streamOpenAICompatible(cfg *aiRuntimeConfig, messages []M, onChunk chunkHandler) error {
	req, err := openAIRequest(cfg, openAIBody(cfg, messages, true))
	if err != nil {
		return err
	}
	resp, err := cfg.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := ensureSuccessResponse(resp); err != nil {
		return err
	}
	return iterateSSE(resp.Body, func(event string, payload M) error {
		if event == "error" {
			return bizErr("%s", extractProviderError(payload))
		}
		choices, _ := payload["choices"].([]any)
		if len(choices) == 0 {
			return nil
		}
		choice, _ := choices[0].(map[string]any)
		delta, _ := choice["delta"].(map[string]any)
		reasoning, _ := delta["reasoning_content"].(string)
		content, _ := delta["content"].(string)
		if reasoning != "" || content != "" {
			return onChunk(aiChunk{Reasoning: reasoning, Content: content})
		}
		return nil
	})
}

func openAIValidate(cfg *aiRuntimeConfig) error {
	body := openAIBody(cfg, []M{{"role": "user", "content": "ping"}}, false)
	req, err := openAIRequest(cfg, body)
	if err != nil {
		return err
	}
	resp, err := cfg.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return ensureSuccessResponse(resp)
}

// ---------- Anthropic ----------

const anthropicVersion = "2023-06-01"

func anthropicRequest(cfg *aiRuntimeConfig, body M) (*http.Request, error) {
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(cfg.BaseURL, "/")+"/v1/messages", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", cfg.APIKey)
	req.Header.Set("anthropic-version", anthropicVersion)
	for key, value := range cfg.Headers {
		req.Header.Set(key, value)
	}
	return req, nil
}

func streamAnthropic(cfg *aiRuntimeConfig, messages []M, onChunk chunkHandler) error {
	systemPrompt, requestMessages := splitSystemMessages(messages)
	body := M{"model": cfg.ModelName, "max_tokens": 2048, "stream": true, "messages": requestMessages}
	if systemPrompt != "" {
		body["system"] = systemPrompt
	}
	req, err := anthropicRequest(cfg, body)
	if err != nil {
		return err
	}
	resp, err := cfg.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := ensureSuccessResponse(resp); err != nil {
		return err
	}
	return iterateSSE(resp.Body, func(event string, payload M) error {
		if event == "error" {
			return bizErr("%s", extractProviderError(payload))
		}
		if event != "content_block_delta" {
			return nil
		}
		delta, _ := payload["delta"].(map[string]any)
		switch delta["type"] {
		case "text_delta":
			if text, _ := delta["text"].(string); text != "" {
				return onChunk(aiChunk{Content: text})
			}
		case "thinking_delta":
			if thinking, _ := delta["thinking"].(string); thinking != "" {
				return onChunk(aiChunk{Reasoning: thinking})
			}
		}
		return nil
	})
}

func anthropicValidate(cfg *aiRuntimeConfig) error {
	body := M{"model": cfg.ModelName, "max_tokens": 1, "messages": []M{{"role": "user", "content": "ping"}}}
	req, err := anthropicRequest(cfg, body)
	if err != nil {
		return err
	}
	resp, err := cfg.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return ensureSuccessResponse(resp)
}

// ---------- Gemini ----------

func streamGemini(cfg *aiRuntimeConfig, messages []M, onChunk chunkHandler) error {
	systemPrompt, contents := geminiContents(messages)
	body := M{"contents": contents}
	if systemPrompt != "" {
		body["systemInstruction"] = M{"parts": []M{{"text": systemPrompt}}}
	}
	raw, _ := json.Marshal(body)
	url := fmt.Sprintf(
		"%s/v1beta/models/%s:streamGenerateContent?alt=sse&key=%s",
		strings.TrimRight(cfg.BaseURL, "/"), cfg.ModelName, cfg.APIKey,
	)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range cfg.Headers {
		req.Header.Set(key, value)
	}
	resp, err := cfg.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := ensureSuccessResponse(resp); err != nil {
		return err
	}
	return iterateSSE(resp.Body, func(event string, payload M) error {
		if event == "error" {
			return bizErr("%s", extractProviderError(payload))
		}
		candidates, _ := payload["candidates"].([]any)
		for _, candidateAny := range candidates {
			candidate, _ := candidateAny.(map[string]any)
			content, _ := candidate["content"].(map[string]any)
			parts, _ := content["parts"].([]any)
			for _, partAny := range parts {
				part, _ := partAny.(map[string]any)
				if text, _ := part["text"].(string); text != "" {
					if err := onChunk(aiChunk{Content: text}); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
}

func geminiValidate(cfg *aiRuntimeConfig) error {
	body := M{"contents": []M{{"role": "user", "parts": []M{{"text": "ping"}}}}}
	raw, _ := json.Marshal(body)
	url := fmt.Sprintf(
		"%s/v1beta/models/%s:generateContent?key=%s",
		strings.TrimRight(cfg.BaseURL, "/"), cfg.ModelName, cfg.APIKey,
	)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range cfg.Headers {
		req.Header.Set(key, value)
	}
	resp, err := cfg.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return ensureSuccessResponse(resp)
}

func geminiContents(messages []M) (string, []M) {
	systemPrompt, requestMessages := splitSystemMessages(messages)
	contents := make([]M, 0, len(requestMessages))
	for _, item := range requestMessages {
		role := "user"
		if item["role"] == "assistant" {
			role = "model"
		}
		contents = append(contents, M{"role": role, "parts": []M{{"text": item["content"]}}})
	}
	return systemPrompt, contents
}

// ---------- 共享 ----------

func splitSystemMessages(messages []M) (string, []M) {
	systemParts := make([]string, 0, 2)
	requestMessages := make([]M, 0, len(messages))
	for _, item := range messages {
		role, _ := item["role"].(string)
		content, _ := item["content"].(string)
		content = strings.TrimSpace(content)
		if content == "" {
			continue
		}
		if role == "system" {
			systemParts = append(systemParts, content)
			continue
		}
		requestMessages = append(requestMessages, M{"role": role, "content": content})
	}
	return strings.TrimSpace(strings.Join(systemParts, "\n\n")), requestMessages
}

// iterateSSE 解析 SSE 行流为 (event, JSON payload)。
func iterateSSE(reader io.Reader, handle func(event string, payload M) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	eventName := "message"
	var dataLines []string

	flush := func() error {
		payloadText := strings.TrimSpace(strings.Join(dataLines, "\n"))
		currentEvent := eventName
		eventName = "message"
		dataLines = nil
		if payloadText == "" || payloadText == "[DONE]" {
			return nil
		}
		var payload M
		if err := json.Unmarshal([]byte(payloadText), &payload); err != nil {
			return err
		}
		return handle(currentEvent, payload)
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(line[6:])
			if eventName == "" {
				eventName = "message"
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(line[5:]))
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return flush()
}

func ensureSuccessResponse(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	var payload M
	if err := json.Unmarshal(raw, &payload); err != nil {
		text := strings.TrimSpace(string(raw))
		if text == "" {
			text = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return bizErr("%s", text)
	}
	return bizErr("%s", extractProviderError(payload))
}

func extractProviderError(payload M) string {
	if payload == nil {
		return "unknown provider error"
	}
	switch errValue := payload["error"].(type) {
	case map[string]any:
		if message, ok := errValue["message"].(string); ok && message != "" {
			return message
		}
		if status, ok := errValue["status"].(string); ok && status != "" {
			return status
		}
		return dumpJSON(errValue)
	case string:
		return errValue
	}
	if message, ok := payload["message"].(string); ok {
		return message
	}
	return dumpJSON(payload)
}
