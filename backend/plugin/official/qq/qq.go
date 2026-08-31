package qq

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/plugin/official/internal/safehttp"
	"coinsphere/backend/plugin/sdk"
	"gorm.io/gorm"
)

const (
	qqPluginID       = "official.qq"
	qqAPIBase        = "https://api.sgroup.qq.com"
	qqAccessTokenURL = "https://bots.qq.com/app/getAppAccessToken"
	qqRequestTimeout = 8 * time.Second
	qqResponseLimit  = 64 << 10
)

var emptyObjectSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false}`)

type qqAccessToken struct {
	Value     string
	ExpiresAt time.Time
}

type qqRuntime struct {
	db *gorm.DB

	http    *safehttp.Client
	tokenMu sync.Mutex
	tokens  map[string]qqAccessToken

	receiverMu sync.Mutex
	receivers  map[string]struct{}
}

type qqCredentials struct {
	AppID        string
	ClientSecret string
}

type qqSendInput struct {
	SubjectKey         string `json:"subjectKey"`
	TargetType         string `json:"targetType"`
	TargetID           string `json:"targetId"`
	MessageType        string `json:"messageType"`
	Content            string `json:"content"`
	MediaType          string `json:"mediaType"`
	MediaURL           string `json:"mediaUrl"`
	MediaFilename      string `json:"mediaFilename"`
	KeyboardTemplateID string `json:"keyboardTemplateId"`
	ReplyToMessageID   string `json:"replyToMessageId"`
	ReplySequence      int    `json:"replySequence"`
}

type qqSendAction struct{ runtime *qqRuntime }

func Register(registry *sdk.Registry, database *gorm.DB) error {
	client, err := safehttp.New([]string{"bots.qq.com", "api.sgroup.qq.com", "api.bot.qq.com"})
	if err != nil {
		return err
	}
	client.SetTimeout(qqRequestTimeout)
	client.DisableRedirects()
	runtime := &qqRuntime{
		db: database, http: client, tokens: map[string]qqAccessToken{}, receivers: map[string]struct{}{},
	}
	return registry.RegisterPlugin(sdk.PluginDescriptor{
		ID: qqPluginID, Name: "QQ机器人", Version: "1.0.0", Contributes: []string{"nodes", "triggers"},
	}, runtime.register)
}

func (q *qqRuntime) register(registrar sdk.Registrar) error {
	if err := registrar.Trigger(sdk.NodeDescriptor{
		Type: "official.qq.receive", Version: "1.0.0", Kind: sdk.NodeKindTrigger,
		ConfigSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"appId":{"type":"string","title":"AppID","pattern":"^[A-Za-z0-9_-]{1,128}$"},"clientSecret":{"type":"string","title":"Client Secret","x-coinsphere-secret":true}},"required":["appId","clientSecret"],"additionalProperties":false}`),
		UISchema:     json.RawMessage(`{"ui:order":["appId","clientSecret"]}`),
		InputSchema:  emptyObjectSchema,
		OutputSchema: qqReceiveOutputSchema(),
		Pool:         sdk.PoolStream, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless,
	}, qqReceiveTrigger{runtime: q}); err != nil {
		return err
	}
	return registrar.Action(sdk.NodeDescriptor{
		Type: "official.qq.send", Version: "1.0.0", Kind: sdk.NodeKindAction,
		ConfigSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"appId":{"type":"string","title":"AppID","pattern":"^[A-Za-z0-9_-]{1,128}$"},"clientSecret":{"type":"string","title":"Client Secret","x-coinsphere-secret":true}},"required":["appId","clientSecret"],"additionalProperties":false}`),
		UISchema:     json.RawMessage(`{"ui:order":["appId","clientSecret"]}`),
		InputSchema:  qqSendInputSchema(),
		OutputSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"deliveryId":{"type":"integer"},"channel":{"type":"string","const":"qq"},"status":{"type":"string","const":"delivered"},"deliveredAt":{"type":"string","format":"date-time"},"providerMessageId":{"type":"string"}},"required":["deliveryId","channel","status","deliveredAt","providerMessageId"],"additionalProperties":false}`),
		Pool:         sdk.PoolStream, SideEffect: sdk.SideEffectNotification, State: sdk.StateStateless,
	}, qqSendAction{runtime: q})
}

func qqSendInputSchema() json.RawMessage {
	return json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"subjectKey":{"type":"string","title":"业务键","minLength":1,"maxLength":256,"x-coinsphere-field-source":true},"targetType":{"type":"string","title":"目标类型","enum":["group","user"],"enumLabels":["群聊","单聊"],"x-coinsphere-field-source":true},"targetId":{"type":"string","title":"目标 OpenID","minLength":1,"maxLength":128,"x-coinsphere-field-source":true},"messageType":{"type":"string","title":"消息类型","enum":["text","markdown","media"],"enumLabels":["纯文本","Markdown","富媒体"],"x-coinsphere-field-source":true},"content":{"type":"string","title":"消息内容","minLength":1,"maxLength":2000,"x-coinsphere-field-source":true},"mediaType":{"type":"string","title":"媒体类型","enum":["image","video","audio","file"],"enumLabels":["图片","视频","语音","文件"],"x-coinsphere-field-source":true},"mediaUrl":{"type":"string","title":"媒体 URL","format":"uri","maxLength":2000,"x-coinsphere-field-source":true},"mediaFilename":{"type":"string","title":"媒体文件名","maxLength":255,"x-coinsphere-field-source":true},"keyboardTemplateId":{"type":"string","title":"键盘模板 ID","maxLength":128,"x-coinsphere-field-source":true},"replyToMessageId":{"type":"string","title":"回复消息 ID","maxLength":128,"x-coinsphere-field-source":true},"replySequence":{"type":"integer","title":"回复序号","minimum":1,"maximum":5,"default":1,"x-coinsphere-field-source":true}},"required":["subjectKey","targetType","targetId","messageType"],"allOf":[{"if":{"properties":{"messageType":{"const":"text"}}},"then":{"required":["content"]}},{"if":{"properties":{"messageType":{"const":"markdown"}}},"then":{"required":["content"]}},{"if":{"properties":{"messageType":{"const":"media"}}},"then":{"required":["mediaType","mediaUrl"]}}],"additionalProperties":false}`)
}

func qqReceiveOutputSchema() json.RawMessage {
	return json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"eventType":{"type":"string","enum":["GROUP_AT_MESSAGE_CREATE","C2C_MESSAGE_CREATE"]},"targetType":{"type":"string","enum":["group","user"]},"targetId":{"type":"string","minLength":1,"maxLength":128},"senderOpenId":{"type":"string","minLength":1,"maxLength":128},"messageId":{"type":"string","minLength":1,"maxLength":128},"messageType":{"type":"integer"},"content":{"type":"string","maxLength":32768},"timestamp":{"type":"string","format":"date-time"},"messageIndex":{"type":"string","maxLength":256},"referencedMessageIndex":{"type":"string","maxLength":256},"attachments":{"type":"array","maxItems":100,"items":{"type":"object","properties":{"url":{"type":"string","maxLength":4096},"filename":{"type":"string","maxLength":255},"contentType":{"type":"string","maxLength":128},"size":{"type":"integer","minimum":0},"width":{"type":"integer","minimum":0},"height":{"type":"integer","minimum":0},"voiceWavUrl":{"type":"string","maxLength":4096},"asrText":{"type":"string","maxLength":32768}},"required":["url","filename","contentType","size"],"additionalProperties":false}}},"required":["eventType","targetType","targetId","senderOpenId","messageId","messageType","content","timestamp","attachments"],"additionalProperties":false}`)
}

func (a qqSendAction) Execute(ctx context.Context, request sdk.ActionRequest) (sdk.ActionResult, error) {
	var input qqSendInput
	if json.Unmarshal(request.Input, &input) != nil {
		return sdk.ActionResult{}, errors.New("QQ send input is invalid")
	}
	if err := validateQQSendInput(&input); err != nil {
		return sdk.ActionResult{}, err
	}
	credentials, err := a.runtime.readCredentials(ctx, request.Config, request.Secrets)
	if err != nil {
		return sdk.ActionResult{}, err
	}
	workflowID, workflowErr := strconv.ParseInt(request.Revision.WorkflowID, 10, 64)
	revisionID, revisionErr := strconv.ParseInt(request.Revision.RevisionID, 10, 64)
	if workflowErr != nil || revisionErr != nil {
		return sdk.ActionResult{}, errors.New("QQ workflow identity is invalid")
	}
	title, auditMessage := qqAuditText(input)
	delivery, err := beginExternalDelivery(ctx, a.runtime.db, request, workflowID, revisionID, "qq", title, notificationInput{
		SubjectKey: input.SubjectKey, Message: auditMessage,
	})
	if err != nil {
		return sdk.ActionResult{}, err
	}
	if delivery.Status == "delivered" {
		return qqSendResult(delivery, ""), nil
	}
	providerMessageID, category, sendErr := a.runtime.send(ctx, credentials, input)
	if sendErr != nil {
		if updateErr := finishExternalDelivery(ctx, a.runtime.db, delivery.ID, "failed", category); updateErr != nil {
			return sdk.ActionResult{}, updateErr
		}
		return sdk.ActionResult{}, errors.New("QQ provider delivery failed: " + category)
	}
	if err := finishExternalDelivery(ctx, a.runtime.db, delivery.ID, "delivered", ""); err != nil {
		return sdk.ActionResult{}, err
	}
	if err := a.runtime.db.WithContext(ctx).First(&delivery, delivery.ID).Error; err != nil {
		return sdk.ActionResult{}, errors.New("load QQ delivery failed")
	}
	return qqSendResult(delivery, providerMessageID), nil
}

func validateQQSendInput(input *qqSendInput) error {
	input.SubjectKey = strings.TrimSpace(input.SubjectKey)
	input.TargetType = strings.TrimSpace(input.TargetType)
	input.TargetID = strings.TrimSpace(input.TargetID)
	input.MessageType = strings.TrimSpace(input.MessageType)
	input.MediaType = strings.TrimSpace(input.MediaType)
	input.MediaURL = strings.TrimSpace(input.MediaURL)
	input.MediaFilename = strings.TrimSpace(input.MediaFilename)
	input.KeyboardTemplateID = strings.TrimSpace(input.KeyboardTemplateID)
	input.ReplyToMessageID = strings.TrimSpace(input.ReplyToMessageID)
	if input.ReplySequence == 0 {
		input.ReplySequence = 1
	}
	maxReplySequence := 5
	if input.TargetType == "user" {
		maxReplySequence = 4
	}
	if input.SubjectKey == "" || utf8.RuneCountInString(input.SubjectKey) > 256 ||
		input.TargetID == "" || utf8.RuneCountInString(input.TargetID) > 128 ||
		input.TargetType != "group" && input.TargetType != "user" ||
		utf8.RuneCountInString(input.ReplyToMessageID) > 128 || input.ReplySequence < 1 || input.ReplySequence > maxReplySequence ||
		utf8.RuneCountInString(input.KeyboardTemplateID) > 128 || utf8.RuneCountInString(input.MediaFilename) > 255 {
		return errors.New("QQ send target or reply configuration is invalid")
	}
	switch input.MessageType {
	case "text", "markdown":
		if strings.TrimSpace(input.Content) == "" || utf8.RuneCountInString(input.Content) > 2000 ||
			input.MediaType != "" || input.MediaURL != "" || input.MediaFilename != "" {
			return errors.New("QQ text or Markdown content is invalid")
		}
	case "media":
		if input.Content != "" || !validQQMediaType(input.MediaType) || !validQQMediaURL(input.MediaURL) {
			return errors.New("QQ media content is invalid")
		}
	default:
		return errors.New("QQ message type is invalid")
	}
	return nil
}

func validQQMediaType(value string) bool {
	return value == "image" || value == "video" || value == "audio" || value == "file"
}

func validQQMediaURL(raw string) bool {
	if raw == "" || len(raw) > 2000 {
		return false
	}
	target, err := url.ParseRequestURI(raw)
	return err == nil && target.IsAbs() && target.Host != "" && target.User == nil &&
		(target.Scheme == "http" || target.Scheme == "https")
}

func qqAuditText(input qqSendInput) (string, string) {
	switch input.MessageType {
	case "markdown":
		return "QQ Markdown 消息", input.Content
	case "media":
		titles := map[string]string{"image": "QQ 图片消息", "video": "QQ 视频消息", "audio": "QQ 语音消息", "file": "QQ 文件消息"}
		return titles[input.MediaType], input.MediaURL
	default:
		return "QQ 文本消息", input.Content
	}
}

func qqSendResult(delivery db.NotificationDelivery, providerMessageID string) sdk.ActionResult {
	deliveredAt := delivery.CreatedAt
	if delivery.DeliveredAt != nil {
		deliveredAt = *delivery.DeliveredAt
	}
	return sdk.ActionResult{Output: mustMarshal(map[string]any{
		"deliveryId": delivery.ID, "channel": "qq", "status": "delivered",
		"deliveredAt": deliveredAt.UTC().Format(time.RFC3339Nano), "providerMessageId": providerMessageID,
	})}
}

func (q *qqRuntime) readCredentials(ctx context.Context, raw json.RawMessage, secrets sdk.SecretReader) (qqCredentials, error) {
	var config struct {
		AppID string `json:"appId"`
	}
	if json.Unmarshal(raw, &config) != nil {
		return qqCredentials{}, errors.New("QQ configuration is invalid")
	}
	secret, err := secrets.Read(ctx, "clientSecret")
	credentials := qqCredentials{AppID: strings.TrimSpace(config.AppID), ClientSecret: strings.TrimSpace(string(secret))}
	if err != nil || !validQQAppID(credentials.AppID) ||
		credentials.ClientSecret == "" || len(credentials.ClientSecret) > 4096 {
		return qqCredentials{}, errors.New("QQ credentials are unavailable")
	}
	return credentials, nil
}

func validQQAppID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char >= '0' && char <= '9', char == '_', char == '-':
		default:
			return false
		}
	}
	return true
}

func (q *qqRuntime) send(ctx context.Context, credentials qqCredentials, input qqSendInput) (string, string, error) {
	token, category, err := q.accessToken(ctx, credentials)
	if err != nil {
		return "", category, err
	}
	payload := map[string]any{}
	switch input.MessageType {
	case "text":
		payload["msg_type"], payload["content"] = 0, input.Content
	case "markdown":
		payload["msg_type"], payload["markdown"] = 2, map[string]string{"content": input.Content}
	case "media":
		fileInfo, uploadCategory, uploadErr := q.uploadMedia(ctx, credentials, token, input)
		if uploadErr != nil {
			return "", uploadCategory, uploadErr
		}
		payload["msg_type"], payload["media"] = 7, map[string]string{"file_info": fileInfo}
	}
	if input.KeyboardTemplateID != "" {
		payload["keyboard"] = map[string]string{"id": input.KeyboardTemplateID}
	}
	if input.ReplyToMessageID != "" {
		payload["msg_id"], payload["msg_seq"] = input.ReplyToMessageID, input.ReplySequence
	}
	endpoint := "/v2/groups/" + url.PathEscape(input.TargetID) + "/messages"
	if input.TargetType == "user" {
		endpoint = "/v2/users/" + url.PathEscape(input.TargetID) + "/messages"
	}
	status, raw, category, err := q.doJSON(ctx, http.MethodPost, qqAPIBase+endpoint, credentials.AppID, token, payload)
	if err != nil {
		return "", category, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return "", qqStatusCategory(status), errors.New("QQ rejected message")
	}
	var result struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &result) != nil || strings.TrimSpace(result.ID) == "" || len(result.ID) > 128 {
		return "", "invalid_response", errors.New("decode QQ message response")
	}
	return strings.TrimSpace(result.ID), "", nil
}

func (q *qqRuntime) uploadMedia(ctx context.Context, credentials qqCredentials, token string, input qqSendInput) (string, string, error) {
	fileTypes := map[string]int{"image": 1, "video": 2, "audio": 3, "file": 4}
	payload := map[string]any{"file_type": fileTypes[input.MediaType], "url": input.MediaURL, "srv_send_msg": false}
	if input.MediaFilename != "" {
		payload["file_name"] = input.MediaFilename
	}
	endpoint := "/v2/groups/" + url.PathEscape(input.TargetID) + "/files"
	if input.TargetType == "user" {
		endpoint = "/v2/users/" + url.PathEscape(input.TargetID) + "/files"
	}
	status, raw, category, err := q.doJSON(ctx, http.MethodPost, qqAPIBase+endpoint, credentials.AppID, token, payload)
	if err != nil {
		return "", category, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return "", qqStatusCategory(status), errors.New("QQ rejected media upload")
	}
	var result struct {
		FileInfo string `json:"file_info"`
	}
	if json.Unmarshal(raw, &result) != nil || strings.TrimSpace(result.FileInfo) == "" || len(result.FileInfo) > 16<<10 {
		return "", "invalid_response", errors.New("decode QQ media response")
	}
	return strings.TrimSpace(result.FileInfo), "", nil
}

func (q *qqRuntime) accessToken(ctx context.Context, credentials qqCredentials) (string, string, error) {
	fingerprintRaw := sha256.Sum256([]byte(credentials.AppID + "\x00" + credentials.ClientSecret))
	fingerprint := hex.EncodeToString(fingerprintRaw[:])
	q.tokenMu.Lock()
	defer q.tokenMu.Unlock()
	if cached, ok := q.tokens[fingerprint]; ok && time.Now().UTC().Add(30*time.Second).Before(cached.ExpiresAt) {
		return cached.Value, "", nil
	}
	status, raw, category, err := q.doJSON(ctx, http.MethodPost, qqAccessTokenURL, "", "", map[string]string{
		"appId": credentials.AppID, "clientSecret": credentials.ClientSecret,
	})
	if err != nil {
		return "", category, err
	}
	var result struct {
		Code        int             `json:"code"`
		AccessToken string          `json:"access_token"`
		ExpiresIn   json.RawMessage `json:"expires_in"`
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return "", qqStatusCategory(status), errors.New("QQ access token request rejected")
	}
	if json.Unmarshal(raw, &result) != nil {
		return "", "invalid_response", errors.New("decode QQ access token response")
	}
	token := strings.TrimSpace(result.AccessToken)
	if result.Code != 0 || token == "" || len(token) > 16<<10 {
		return "", "authentication", errors.New("QQ access token request rejected")
	}
	ttl, err := qqTokenTTL(result.ExpiresIn)
	if err != nil || ttl <= 0 || ttl > int64((24*time.Hour)/time.Second) {
		return "", "invalid_response", errors.New("QQ access token expiry is invalid")
	}
	q.tokens[fingerprint] = qqAccessToken{Value: token, ExpiresAt: time.Now().UTC().Add(time.Duration(ttl) * time.Second)}
	return token, "", nil
}

func (q *qqRuntime) doJSON(ctx context.Context, method, target, appID, token string, payload any) (int, []byte, string, error) {
	var raw []byte
	var err error
	if payload != nil {
		raw, err = json.Marshal(payload)
		if err != nil {
			return 0, nil, "configuration", err
		}
	}
	callCtx, cancel := context.WithTimeout(ctx, qqRequestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(callCtx, method, target, bytes.NewReader(raw))
	if err != nil {
		return 0, nil, "configuration", err
	}
	request.Header.Set("Accept", "application/json")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "QQBot "+token)
	}
	if appID != "" {
		request.Header.Set("X-Union-Appid", appID)
	}
	response, err := q.http.Do(request)
	if err != nil {
		return 0, nil, qqRequestCategory(callCtx, err), err
	}
	defer response.Body.Close()
	body, err := readQQResponse(response.Body)
	if err != nil {
		return response.StatusCode, nil, "invalid_response", err
	}
	return response.StatusCode, body, "", nil
}

func qqStatusCategory(status int) string {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "authentication"
	case status == http.StatusTooManyRequests:
		return "rate_limited"
	case status >= http.StatusInternalServerError:
		return "provider_unavailable"
	default:
		return "provider_rejected"
	}
}

func qqTokenTTL(raw json.RawMessage) (int64, error) {
	if len(raw) == 0 {
		return 0, errors.New("missing QQ token expiry")
	}
	text := string(raw)
	if raw[0] == '"' {
		if err := json.Unmarshal(raw, &text); err != nil {
			return 0, err
		}
	}
	return strconv.ParseInt(strings.TrimSpace(text), 10, 64)
}

func readQQResponse(reader io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, qqResponseLimit+1))
	if err != nil {
		return nil, err
	}
	if len(body) > qqResponseLimit {
		return nil, errors.New("QQ response is too large")
	}
	return body, nil
}

func qqRequestCategory(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, safehttp.ErrUnsafeEndpoint) {
		return "network_policy"
	}
	return "network"
}

func mustMarshal(value any) json.RawMessage {
	raw, _ := json.Marshal(value)
	return raw
}

var _ sdk.ActionHandler = qqSendAction{}
