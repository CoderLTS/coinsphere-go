// 本文件:作为"客户端"去调用外部 AI 服务(OpenAI 兼容 / Anthropic / Gemini)。
// 与 internal/api 包正好相反 —— api 包当"服务器"接收别人的请求;这里是我们主动用标准库
// net/http 向对方接口发请求,再解析返回的流式结果(见 GO入门笔记『框架:net/http』)。

package service

import (
	// bufio:按行读取流;bytes:把内存里的字节当成可读流;io:通用读写接口;
	// net/http:发起 HTTP 请求(客户端用法);encoding/json:JSON 编解码。
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
	// Client.Timeout 限制单次请求的总耗时,是本项目控制超时的方式;
	// 另一种常见做法是用 context 设 deadline(见 GO入门笔记『并发』的 context)。
	// &http.Client{...} 用 struct 字面量创建对象并取地址返回。
	return &http.Client{Timeout: timeout}
}

// aiChunk 一段流式输出。
type aiChunk struct {
	Reasoning string
	Content   string
}

// 函数在 Go 里也是一种"值",可以起个类型名。chunkHandler 就是"接收一个 aiChunk、返回 error
// 的函数"这种类型 —— 后面把"每收到一段输出该怎么处理"当作回调传进来。
type chunkHandler func(chunk aiChunk) error

// streamAiChat 按协议类型流式调用模型。
// switch 按 cfg.ProviderType 的值分支;Go 的 case 默认不"穿透",不用写 break
//(见 GO入门笔记『其它小语法』)。messages []M 是切片,onChunk 是上面定义的回调函数类型。
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
	// json.Marshal 把请求体(map)编码成 JSON 字节切片 raw;末尾 _ 丢弃错误返回值。
	raw, _ := json.Marshal(body)
	// http.NewRequest 构造一个"客户端请求":POST 方法、目标 URL、请求体。
	// bytes.NewReader(raw) 把字节切片包装成 io.Reader(可被逐步读取的数据源)。
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(cfg.BaseURL, "/")+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	// 设置请求头:声明请求体是 JSON,并用 Bearer Token 携带 API Key 做鉴权。
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	// 再把用户自定义的额外请求头逐个塞进去(range 遍历 map 得到 键, 值)。
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
	// client.Do(req) 真正把请求发出去,返回响应 resp。
	resp, err := cfg.httpClient().Do(req)
	if err != nil {
		return err
	}
	// defer:登记"函数返回前一定执行"的收尾(见 GO入门笔记『defer』)。
	// 响应体是一条网络流,必须 Close 才能释放连接;defer 保证无论从哪个分支返回都会关。
	defer resp.Body.Close()
	if err := ensureSuccessResponse(resp); err != nil {
		return err
	}
	// 把响应体交给 iterateSSE 逐段解析;第二个参数是一个匿名函数(回调),
	// 每解析出一段就调用它。匿名函数内部可以直接引用外层的 onChunk 等变量。
	return iterateSSE(resp.Body, func(event string, payload M) error {
		if event == "error" {
			return bizErr("%s", extractProviderError(payload))
		}
		// 返回的 JSON 已被解析成 map[string]any,取字段要用类型断言把 any 还原成具体类型:
		// payload["choices"].([]any) 试着把它当数组取出,取不到时 _ 处为 false、值为零值。
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
	// fmt.Sprintf 用占位符拼字符串(%s = 填入一个字符串)。注意 Gemini 的鉴权方式不同:
	// API Key 直接拼进 URL 的 key= 查询参数,而不是放在请求头里。
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
// 参数 reader io.Reader 是"任何能被读取的数据源"(接口,见 GO入门笔记『interface』);
// handle 是处理每个事件的回调函数。
func iterateSSE(reader io.Reader, handle func(event string, payload M) error) error {
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

// ensureSuccessResponse 检查 HTTP 状态码:2xx 视为成功,否则读出错误信息返回。
func ensureSuccessResponse(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	// io.LimitReader 限制最多读 64KB,防止错误响应过大;io.ReadAll 把剩余内容一次性读完。
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
