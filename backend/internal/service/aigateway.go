// 本文件:作为"客户端"去调用外部 AI 服务(OpenAI 兼容 / Anthropic / Gemini)。
// 与 internal/api 包正好相反 —— api 包当"服务器"接收别人的请求;这里是我们主动用标准库
// net/http 向对方接口发请求,再解析返回的流式结果(见 GO入门笔记『框架:net/http』)。
//
// 结构上分三层:
//   1. aiProvider 接口 —— 每种协议只负责"请求怎么拼""一个 SSE 事件怎么解析";
//   2. 公共流程 —— 发请求、判状态码、按 SSE 切事件,三种协议共用一份;
//   3. streamAiChat / validateAiConfig —— 对外的两个入口。
//
// 这样加第四种协议 = 实现一个 aiProvider + 在 aiProviders 里登记一行,不用复制流程代码。

package service

import (
	// bufio:按行读取流;bytes:把内存里的字节当成可读流;io:通用读写接口;
	// net/http:发起 HTTP 请求(客户端用法);encoding/json:JSON 编解码。
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// anthropic 协议要求显式给出 max_tokens;没配就用这个默认值(可被模型配置里的"请求体 JSON"覆盖)。
const (
	anthropicVersion          = "2023-06-01"
	anthropicDefaultMaxTokens = 4096
)

// aiRuntimeConfig 解密后的模型运行配置。
// struct 是一组字段的集合(见 GO入门笔记『复合类型』)。这个结构体只在内部用,
// 所以字段没有 JSON tag;APIKey 此时已是解密后的明文,仅在内存里短暂使用。
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

// (c *aiRuntimeConfig) 是方法接收者(见 GO入门笔记『方法与接收者』);返回值 *http.Client
// 是"指向 http.Client 的指针"。http.Client 是标准库里"发请求的一方"(客户端)。
func (c *aiRuntimeConfig) httpClient() *http.Client {
	// time.Duration 是"时间段"类型;毫秒数 × time.Millisecond 得到一个时长。
	timeout := time.Duration(c.TimeoutMs) * time.Millisecond
	if timeout < 5*time.Second {
		timeout = 5 * time.Second
	}
	// Client.Timeout 限制单次请求的总耗时。真正的"中途取消"靠请求上挂的 context:
	// 用户关掉页面时 ctx 被取消,底层连接立刻断开,不会继续从模型侧拉流(也就不再计费)。
	return &http.Client{Timeout: timeout}
}

// endpoint 拼接接口地址,顺手去掉 BaseURL 末尾多余的斜杠。
func (c *aiRuntimeConfig) endpoint(path string) string {
	return strings.TrimRight(c.BaseURL, "/") + path
}

// applyCustomHeaders 把用户在模型配置里填的额外请求头塞进请求(放最后,允许覆盖默认头)。
func (c *aiRuntimeConfig) applyCustomHeaders(req *http.Request) {
	for key, value := range c.Headers {
		req.Header.Set(key, value)
	}
}

// withExtraBody 把用户配置的"请求体 JSON"合并进请求体。
//
// 注意合并顺序:ExtraBody 在最后,所以用户配的键可以覆盖我们的默认值
// (比如把 OpenAI 的 stream_options 关掉、把 Anthropic 的 max_tokens 调大)。
// 三种协议都走这个函数 —— 早先只有 OpenAI 分支合并了 ExtraBody,
// 同一个配置项换个协议就静默失效。
func (c *aiRuntimeConfig) withExtraBody(body M) M {
	for key, value := range c.ExtraBody {
		body[key] = value
	}
	return body
}

// aiUsage 一次调用的 token 消耗。三家协议字段名不同,统一归一到这里。
type aiUsage struct {
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
}

// aiChunk 一段流式输出。Usage 只在流的末尾出现一次(没有就是 nil)。
type aiChunk struct {
	Reasoning string
	Content   string
	Usage     *aiUsage
}

func (c aiChunk) isEmpty() bool { return c.Reasoning == "" && c.Content == "" && c.Usage == nil }

// 函数在 Go 里也是一种"值",可以起个类型名。chunkHandler 就是"接收一个 aiChunk、返回 error
// 的函数"这种类型 —— 后面把"每收到一段输出该怎么处理"当作回调传进来。
type chunkHandler func(chunk aiChunk) error

// aiProvider 一种模型协议的适配器。三个方法各管一件事,公共的发请求/判状态/解析 SSE 不在这里。
type aiProvider interface {
	// buildStreamRequest 组装一次流式对话请求。
	buildStreamRequest(ctx context.Context, cfg *aiRuntimeConfig, messages []M) (*http.Request, error)
	// buildProbeRequest 组装一次"连通性探活"请求,用于配置页的测试连接。
	buildProbeRequest(ctx context.Context, cfg *aiRuntimeConfig) (*http.Request, error)
	// parseEvent 从一个 SSE 事件里取出增量内容;返回空 aiChunk 表示这个事件没有可用内容。
	parseEvent(event string, payload M) (aiChunk, error)
}

// 协议注册表。加一种协议 = 实现 aiProvider + 在这里登记一行。
var aiProviders = map[string]aiProvider{
	aiProviderOpenAICompatible: openAIProvider{},
	aiProviderAnthropic:        anthropicProvider{},
	aiProviderGemini:           geminiProvider{},
}

func resolveAiProvider(providerType string) (aiProvider, error) {
	if provider, ok := aiProviders[providerType]; ok {
		return provider, nil
	}
	return nil, bizErr("暂不支持的模型协议类型: %s", providerType)
}

// streamAiChat 按协议类型流式调用模型。
//
// ctx 一路传到底层 HTTP 请求:调用方(SSE handler)把 r.Context() 传进来,
// 用户一关页面 ctx 就被取消,这里的请求随即中断,不会继续拉流。
func streamAiChat(ctx context.Context, cfg *aiRuntimeConfig, messages []M, onChunk chunkHandler) error {
	provider, err := resolveAiProvider(cfg.ProviderType)
	if err != nil {
		return err
	}
	req, err := provider.buildStreamRequest(ctx, cfg, messages)
	if err != nil {
		return err
	}
	resp, err := cfg.httpClient().Do(req)
	if err != nil {
		// 连接失败 / 超时 / 被取消都属于基础设施问题,标成可重试(见 failure.go)。
		return retryableErr(err)
	}
	// defer:登记"函数返回前一定执行"的收尾(见 GO入门笔记『defer』)。
	// 响应体是一条网络流,必须 Close 才能释放连接;defer 保证无论从哪个分支返回都会关。
	defer resp.Body.Close()
	if err := ensureSuccessResponse(resp); err != nil {
		return err
	}
	return iterateSSE(ctx, resp.Body, func(event string, payload M) error {
		chunk, err := provider.parseEvent(event, payload)
		if err != nil {
			return err
		}
		if chunk.isEmpty() {
			return nil
		}
		return onChunk(chunk)
	})
}

// validateAiConfig 连通性校验。
func validateAiConfig(ctx context.Context, cfg *aiRuntimeConfig) (bool, string) {
	provider, err := resolveAiProvider(cfg.ProviderType)
	if err != nil {
		return false, err.Error()
	}
	req, err := provider.buildProbeRequest(ctx, cfg)
	if err != nil {
		return false, err.Error()
	}
	resp, err := cfg.httpClient().Do(req)
	if err != nil {
		return false, err.Error()
	}
	defer resp.Body.Close()
	if err := ensureSuccessResponse(resp); err != nil {
		return false, err.Error()
	}
	return true, "连接校验成功"
}

// jsonRequest 造一个带 JSON 请求体的 POST 请求,并挂上 ctx。
func jsonRequest(ctx context.Context, method, url string, body M) (*http.Request, error) {
	// json.Marshal 把请求体(map)编码成 JSON 字节切片 raw。
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	// bytes.NewReader(raw) 把字节切片包装成 io.Reader(可被逐步读取的数据源)。
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

// ---------- OpenAI 兼容 ----------

type openAIProvider struct{}

func (openAIProvider) buildStreamRequest(ctx context.Context, cfg *aiRuntimeConfig, messages []M) (*http.Request, error) {
	body := cfg.withExtraBody(M{
		"model":    cfg.ModelName,
		"messages": messages,
		"stream":   true,
		// 让服务端在流末尾补一个带 usage 的分片,用来统计 token。
		// 少数严格的兼容实现可能不认这个字段,可以在模型配置的"请求体 JSON"里写
		// {"stream_options": null} 关掉(ExtraBody 最后合并,能覆盖这里)。
		"stream_options": M{"include_usage": true},
	})
	req, err := jsonRequest(ctx, http.MethodPost, cfg.endpoint("/chat/completions"), body)
	if err != nil {
		return nil, err
	}
	// Bearer Token 鉴权。
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	cfg.applyCustomHeaders(req)
	return req, nil
}

// 探活用 GET /models:不产生推理,不计费。早先是发一条 max_tokens=1 的真实生成请求,
// 每点一次"测试连接"都要花钱。
func (openAIProvider) buildProbeRequest(ctx context.Context, cfg *aiRuntimeConfig) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.endpoint("/models"), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	cfg.applyCustomHeaders(req)
	return req, nil
}

func (openAIProvider) parseEvent(event string, payload M) (aiChunk, error) {
	if event == "error" {
		return aiChunk{}, bizErr("%s", extractProviderError(payload))
	}
	chunk := aiChunk{}
	// 返回的 JSON 已被解析成 map[string]any,取字段要用类型断言把 any 还原成具体类型:
	// payload["choices"].([]any) 试着把它当数组取出,取不到时 _ 处为 false、值为零值。
	if choices, _ := payload["choices"].([]any); len(choices) > 0 {
		choice, _ := choices[0].(map[string]any)
		delta, _ := choice["delta"].(map[string]any)
		chunk.Reasoning, _ = delta["reasoning_content"].(string)
		chunk.Content, _ = delta["content"].(string)
	}
	if usage, ok := payload["usage"].(map[string]any); ok {
		chunk.Usage = &aiUsage{
			PromptTokens:     asInt64(usage["prompt_tokens"]),
			CompletionTokens: asInt64(usage["completion_tokens"]),
			TotalTokens:      asInt64(usage["total_tokens"]),
		}
	}
	return chunk, nil
}

// ---------- Anthropic ----------

type anthropicProvider struct{}

func (anthropicProvider) newRequest(ctx context.Context, cfg *aiRuntimeConfig, body M) (*http.Request, error) {
	req, err := jsonRequest(ctx, http.MethodPost, cfg.endpoint("/v1/messages"), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", cfg.APIKey)
	req.Header.Set("anthropic-version", anthropicVersion)
	cfg.applyCustomHeaders(req)
	return req, nil
}

func (p anthropicProvider) buildStreamRequest(ctx context.Context, cfg *aiRuntimeConfig, messages []M) (*http.Request, error) {
	systemPrompt, requestMessages := splitSystemMessages(messages)
	body := M{
		"model":      cfg.ModelName,
		"max_tokens": anthropicDefaultMaxTokens,
		"stream":     true,
		"messages":   requestMessages,
	}
	if systemPrompt != "" {
		body["system"] = systemPrompt
	}
	return p.newRequest(ctx, cfg, cfg.withExtraBody(body))
}

func (p anthropicProvider) buildProbeRequest(ctx context.Context, cfg *aiRuntimeConfig) (*http.Request, error) {
	// Anthropic 没有免费的探活端点,只能发一条 max_tokens=1 的最小请求。
	return p.newRequest(ctx, cfg, M{
		"model": cfg.ModelName, "max_tokens": 1,
		"messages": []M{{"role": "user", "content": "ping"}},
	})
}

func (anthropicProvider) parseEvent(event string, payload M) (aiChunk, error) {
	if event == "error" {
		return aiChunk{}, bizErr("%s", extractProviderError(payload))
	}
	switch event {
	case "content_block_delta":
		delta, _ := payload["delta"].(map[string]any)
		switch delta["type"] {
		case "text_delta":
			text, _ := delta["text"].(string)
			return aiChunk{Content: text}, nil
		case "thinking_delta":
			thinking, _ := delta["thinking"].(string)
			return aiChunk{Reasoning: thinking}, nil
		}
	case "message_start":
		// 输入 token 在流的开头给出。
		message, _ := payload["message"].(map[string]any)
		if usage, ok := message["usage"].(map[string]any); ok {
			return aiChunk{Usage: &aiUsage{PromptTokens: asInt64(usage["input_tokens"])}}, nil
		}
	case "message_delta":
		// 输出 token 在流的末尾给出。
		if usage, ok := payload["usage"].(map[string]any); ok {
			return aiChunk{Usage: &aiUsage{CompletionTokens: asInt64(usage["output_tokens"])}}, nil
		}
	}
	return aiChunk{}, nil
}

// ---------- Gemini ----------

type geminiProvider struct{}

// Gemini 的鉴权走 x-goog-api-key 请求头。早先是把 key 拼进 URL 的查询参数,
// 那样密钥会出现在网关 access log、反向代理日志和任何 URL 采集里。
func (geminiProvider) newRequest(ctx context.Context, cfg *aiRuntimeConfig, path string, body M) (*http.Request, error) {
	req, err := jsonRequest(ctx, http.MethodPost, cfg.endpoint(path), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-goog-api-key", cfg.APIKey)
	cfg.applyCustomHeaders(req)
	return req, nil
}

func (p geminiProvider) buildStreamRequest(ctx context.Context, cfg *aiRuntimeConfig, messages []M) (*http.Request, error) {
	systemPrompt, contents := geminiContents(messages)
	body := M{"contents": contents}
	if systemPrompt != "" {
		body["systemInstruction"] = M{"parts": []M{{"text": systemPrompt}}}
	}
	// fmt.Sprintf 用占位符拼字符串(%s = 填入一个字符串)。
	path := fmt.Sprintf("/v1beta/models/%s:streamGenerateContent?alt=sse", cfg.ModelName)
	return p.newRequest(ctx, cfg, path, cfg.withExtraBody(body))
}

func (p geminiProvider) buildProbeRequest(ctx context.Context, cfg *aiRuntimeConfig) (*http.Request, error) {
	path := fmt.Sprintf("/v1beta/models/%s:generateContent", cfg.ModelName)
	return p.newRequest(ctx, cfg, path, M{
		"contents": []M{{"role": "user", "parts": []M{{"text": "ping"}}}},
	})
}

func (geminiProvider) parseEvent(event string, payload M) (aiChunk, error) {
	if event == "error" {
		return aiChunk{}, bizErr("%s", extractProviderError(payload))
	}
	chunk := aiChunk{}
	var builder strings.Builder
	candidates, _ := payload["candidates"].([]any)
	for _, candidateAny := range candidates {
		candidate, _ := candidateAny.(map[string]any)
		content, _ := candidate["content"].(map[string]any)
		parts, _ := content["parts"].([]any)
		for _, partAny := range parts {
			part, _ := partAny.(map[string]any)
			if text, _ := part["text"].(string); text != "" {
				builder.WriteString(text)
			}
		}
	}
	chunk.Content = builder.String()
	if usage, ok := payload["usageMetadata"].(map[string]any); ok {
		chunk.Usage = &aiUsage{
			PromptTokens:     asInt64(usage["promptTokenCount"]),
			CompletionTokens: asInt64(usage["candidatesTokenCount"]),
			TotalTokens:      asInt64(usage["totalTokenCount"]),
		}
	}
	return chunk, nil
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
// 参数 reader io.Reader 是"任何能被读取的数据源"(接口,见 GO入门笔记『interface』);
// handle 是处理每个事件的回调函数。每读一行都检查一次 ctx:整条流可能很长,
// 用户中途关掉页面时要能立刻停下来。
func iterateSSE(ctx context.Context, reader io.Reader, handle func(event string, payload M) error) error {
	// bufio.Scanner 把流按行切开、一行行读;Buffer 设定单行最大缓冲,避免超长行报错。
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	eventName := "message"
	var dataLines []string

	// flush 是个匿名函数(闭包):它能读写外层的 eventName、dataLines 变量。
	// SSE 用空行分隔事件,每遇到空行就 flush 一次,把攒下的数据行解析成一个事件。
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

	// scanner.Scan() 每读到下一行返回 true,读完返回 false;scanner.Text() 取当前行内容。
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
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

// aiHTTPError 模型服务返回的非 2xx 响应。
// 带上状态码,调用方就能按 401/403/429 这些明确信号给用户话术,
// 不用再去错误文本里搜 "free tier" 这种关键词。
type aiHTTPError struct {
	StatusCode int
	Message    string
}

func (e *aiHTTPError) Error() string { return e.Message }

// asAiHTTPError 从错误链里取出 aiHTTPError;取不到返回 nil。
func asAiHTTPError(err error) *aiHTTPError {
	var target *aiHTTPError
	if errors.As(err, &target) {
		return target
	}
	return nil
}

// ensureSuccessResponse 检查 HTTP 状态码:2xx 视为成功,否则读出错误信息并标注可否重试。
func ensureSuccessResponse(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	// io.LimitReader 限制最多读 64KB,防止错误响应过大;io.ReadAll 把剩余内容一次性读完。
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	message := strings.TrimSpace(string(raw))
	var payload M
	if err := json.Unmarshal(raw, &payload); err == nil {
		message = extractProviderError(payload)
	}
	if message == "" {
		message = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	httpErr := &aiHTTPError{StatusCode: resp.StatusCode, Message: message}
	// 429(限流)与 5xx(服务端故障)过一会儿可能就好了;其余 4xx 是请求本身的问题。
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return retryableErr(httpErr)
	}
	return permanentErr(httpErr)
}

func extractProviderError(payload M) string {
	if payload == nil {
		return "unknown provider error"
	}
	// 这是"类型 switch":payload["error"] 的实际类型不确定,按它是 map 还是 string 分别处理;
	// errValue 在每个 case 分支里就是对应的那个具体类型(见 GO入门笔记『interface』)。
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
