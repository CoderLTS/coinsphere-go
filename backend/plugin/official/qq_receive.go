package official

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"coinsphere/backend/plugin/sdk"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/gorilla/websocket"
)

const (
	qqGroupAndC2CIntent = 1 << 25
	qqGatewayMaxPayload = 1 << 20
)

var (
	errQQReconnect        = errors.New("QQ gateway requested reconnect")
	errQQInvalidSession   = errors.New("QQ gateway session is invalid")
	errQQPermanentGateway = errors.New("permanent QQ gateway failure")
)

type qqReceiveTrigger struct{ runtime *qqRuntime }

type qqGatewaySession struct {
	ID  string
	Seq atomic.Int64
}

type qqGatewayPayload struct {
	Op int             `json:"op"`
	S  *int64          `json:"s,omitempty"`
	T  string          `json:"t,omitempty"`
	D  json.RawMessage `json:"d,omitempty"`
}

type qqIncomingMessage struct {
	ID          string `json:"id"`
	Content     string `json:"content"`
	GroupOpenID string `json:"group_openid"`
	Timestamp   string `json:"timestamp"`
	MessageType int    `json:"message_type"`
	Author      struct {
		UserOpenID   string `json:"user_openid"`
		MemberOpenID string `json:"member_openid"`
	} `json:"author"`
	MessageScene struct {
		Ext []string `json:"ext"`
	} `json:"message_scene"`
	Attachments []struct {
		URL          string `json:"url"`
		Filename     string `json:"filename"`
		ContentType  string `json:"content_type"`
		Size         int64  `json:"size"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		VoiceWavURL  string `json:"voice_wav_url"`
		ASRReferText string `json:"asr_refer_text"`
	} `json:"attachments"`
}

func (t qqReceiveTrigger) Run(ctx context.Context, request sdk.TriggerRequest, emitter sdk.Emitter) error {
	credentials, err := t.runtime.readCredentials(ctx, request.Config, request.Secrets)
	if err != nil {
		return err
	}
	if !t.runtime.acquireReceiver(credentials.AppID) {
		return errors.New("QQ receiver for this AppID is already active")
	}
	defer t.runtime.releaseReceiver(credentials.AppID)

	session := qqGatewaySession{}
	backoff := time.Second
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		ready, runErr := t.runtime.runGateway(ctx, emitter, credentials, &session)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(runErr, errQQPermanentGateway) {
			return runErr
		}
		if ready {
			backoff = time.Second
		}
		if errors.Is(runErr, errQQInvalidSession) {
			session.ID = ""
			session.Seq.Store(0)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
	}
}

func (q *qqRuntime) acquireReceiver(appID string) bool {
	q.receiverMu.Lock()
	defer q.receiverMu.Unlock()
	if _, exists := q.receivers[appID]; exists {
		return false
	}
	q.receivers[appID] = struct{}{}
	return true
}

func (q *qqRuntime) releaseReceiver(appID string) {
	q.receiverMu.Lock()
	delete(q.receivers, appID)
	q.receiverMu.Unlock()
}

func (q *qqRuntime) runGateway(ctx context.Context, emitter sdk.Emitter, credentials qqCredentials, session *qqGatewaySession) (bool, error) {
	token, category, err := q.accessToken(ctx, credentials)
	if err != nil {
		if category == "authentication" {
			return false, fmt.Errorf("%w: authentication", errQQPermanentGateway)
		}
		return false, err
	}
	gatewayURL, err := q.gatewayURL(ctx, credentials.AppID, token)
	if err != nil {
		return false, err
	}
	dialer := websocket.Dialer{Proxy: nil, NetDialContext: q.http.dialContext, HandshakeTimeout: 10 * time.Second}
	connection, response, err := dialer.DialContext(ctx, gatewayURL.String(), http.Header{"User-Agent": []string{"CoinSphere-QQ/1.0"}})
	status := 0
	if response != nil {
		status = response.StatusCode
		if response.Body != nil {
			response.Body.Close()
		}
	}
	if err != nil {
		if permanentWebSocketStatus(status) {
			return false, fmt.Errorf("%w: status %d", errQQPermanentGateway, status)
		}
		return false, err
	}
	connection.SetReadLimit(qqGatewayMaxPayload)
	closed := make(chan struct{})
	go closeQQGatewayOnCancel(ctx, connection, closed)
	defer close(closed)
	defer connection.Close()

	var hello qqGatewayPayload
	if err := connection.ReadJSON(&hello); err != nil || hello.Op != 10 {
		return false, errors.New("QQ gateway hello is invalid")
	}
	var helloData struct {
		HeartbeatInterval int `json:"heartbeat_interval"`
	}
	if json.Unmarshal(hello.D, &helloData) != nil || helloData.HeartbeatInterval < 1000 || helloData.HeartbeatInterval > 5*60*1000 {
		return false, errors.New("QQ gateway heartbeat interval is invalid")
	}
	err = connection.WriteJSON(qqGatewayAuthentication(token, session))
	if err != nil {
		return false, err
	}

	var heartbeatACK atomic.Bool
	heartbeatACK.Store(true)
	heartbeatNow := make(chan struct{}, 1)
	heartbeatDone := make(chan struct{})
	go q.runGatewayHeartbeat(connection, session, time.Duration(helloData.HeartbeatInterval)*time.Millisecond, &heartbeatACK, heartbeatNow, heartbeatDone)
	defer close(heartbeatDone)

	ready := false
	for {
		var payload qqGatewayPayload
		if err := connection.ReadJSON(&payload); err != nil {
			return ready, qqGatewayReadError(err)
		}
		if payload.S != nil {
			session.Seq.Store(*payload.S)
		}
		switch payload.Op {
		case 0:
			switch payload.T {
			case "READY":
				var data struct {
					SessionID string `json:"session_id"`
				}
				if json.Unmarshal(payload.D, &data) != nil || strings.TrimSpace(data.SessionID) == "" || len(data.SessionID) > 256 {
					return ready, errors.New("QQ gateway ready event is invalid")
				}
				session.ID = strings.TrimSpace(data.SessionID)
				ready = true
			case "RESUMED":
				ready = true
			case "GROUP_AT_MESSAGE_CREATE", "C2C_MESSAGE_CREATE":
				if err := emitQQMessage(ctx, emitter, credentials.AppID, payload.T, payload.D); err != nil {
					return ready, err
				}
			}
		case 7:
			return ready, errQQReconnect
		case 1:
			select {
			case heartbeatNow <- struct{}{}:
			default:
			}
		case 9:
			return ready, qqInvalidSessionError(payload.D)
		case 11:
			heartbeatACK.Store(true)
		}
	}
}

func qqInvalidSessionError(raw json.RawMessage) error {
	var resumable bool
	if json.Unmarshal(raw, &resumable) != nil || !resumable {
		return errQQInvalidSession
	}
	return errQQReconnect
}

func closeQQGatewayOnCancel(ctx context.Context, connection *websocket.Conn, done <-chan struct{}) {
	select {
	case <-ctx.Done():
		connection.Close()
	case <-done:
	}
}

func qqGatewayAuthentication(token string, session *qqGatewaySession) map[string]any {
	if session.ID != "" {
		return map[string]any{"op": 6, "d": map[string]any{
			"token": "QQBot " + token, "session_id": session.ID, "seq": session.Seq.Load(),
		}}
	}
	return map[string]any{"op": 2, "d": map[string]any{
		"token": "QQBot " + token, "intents": qqGroupAndC2CIntent, "shard": []int{0, 1},
		"properties": map[string]string{"$os": runtime.GOOS, "$browser": "coinsphere", "$device": "coinsphere"},
	}}
}

func (q *qqRuntime) runGatewayHeartbeat(connection *websocket.Conn, session *qqGatewaySession, interval time.Duration, ack *atomic.Bool, now, done <-chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
		case <-now:
		}
		if !ack.Swap(false) {
			connection.Close()
			return
		}
		var sequence any
		if latest := session.Seq.Load(); latest != 0 {
			sequence = latest
		}
		if connection.WriteJSON(map[string]any{"op": 1, "d": sequence}) != nil {
			connection.Close()
			return
		}
	}
}

func (q *qqRuntime) gatewayURL(ctx context.Context, appID, token string) (*url.URL, error) {
	status, raw, _, err := q.doJSON(ctx, http.MethodGet, qqAPIBase+"/gateway", appID, token, nil)
	if err != nil {
		return nil, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			return nil, fmt.Errorf("%w: authentication", errQQPermanentGateway)
		}
		return nil, errors.New("QQ gateway endpoint request rejected")
	}
	var result struct {
		URL string `json:"url"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return nil, errors.New("decode QQ gateway endpoint")
	}
	target, err := url.ParseRequestURI(strings.TrimSpace(result.URL))
	if err != nil || !target.IsAbs() || target.Scheme != "wss" || !isQQGatewayHost(target.Hostname()) {
		return nil, errors.New("QQ gateway endpoint is invalid")
	}
	if err := q.http.validateWebSocketURL(ctx, target, false); err != nil {
		return nil, err
	}
	return target, nil
}

func isQQGatewayHost(host string) bool {
	return strings.EqualFold(host, "api.sgroup.qq.com") || strings.EqualFold(host, "api.bot.qq.com")
}

func qqGatewayReadError(err error) error {
	var closeError *websocket.CloseError
	if !errors.As(err, &closeError) {
		return err
	}
	switch closeError.Code {
	case 4006, 4007, 4009:
		return errQQInvalidSession
	case 4900, 4901, 4902, 4903, 4904, 4905, 4906, 4907, 4908, 4909, 4910, 4911, 4912, 4913:
		return errQQInvalidSession
	case 4001, 4002, 4003, 4004, 4005, 4010, 4011, 4012, 4013, 4014, 4914, 4915:
		return fmt.Errorf("%w: close code %d", errQQPermanentGateway, closeError.Code)
	default:
		return err
	}
}

func emitQQMessage(ctx context.Context, emitter sdk.Emitter, appID, eventType string, raw json.RawMessage) error {
	var message qqIncomingMessage
	if json.Unmarshal(raw, &message) != nil {
		return errors.New("decode QQ message event")
	}
	data, eventTime, partitionKey, err := normalizeQQMessage(eventType, message)
	if err != nil {
		return err
	}
	event := cloudevents.NewEvent()
	event.SetID(message.ID)
	event.SetSource("urn:coinsphere:qq:" + appID)
	event.SetType(eventType)
	event.SetTime(eventTime)
	event.SetExtension("partitionkey", partitionKey)
	if err := event.SetData(cloudevents.ApplicationJSON, data); err != nil {
		return errors.New("encode QQ message event")
	}
	return emitter.Emit(ctx, event)
}

func normalizeQQMessage(eventType string, message qqIncomingMessage) (map[string]any, time.Time, string, error) {
	message.ID = strings.TrimSpace(message.ID)
	message.GroupOpenID = strings.TrimSpace(message.GroupOpenID)
	message.Author.UserOpenID = strings.TrimSpace(message.Author.UserOpenID)
	message.Author.MemberOpenID = strings.TrimSpace(message.Author.MemberOpenID)
	if message.ID == "" || len(message.ID) > 128 || utf8.RuneCountInString(message.Content) > 32768 {
		return nil, time.Time{}, "", errors.New("QQ message identity or content is invalid")
	}
	eventTime, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(message.Timestamp))
	if err != nil {
		return nil, time.Time{}, "", errors.New("QQ message timestamp is invalid")
	}
	targetType, targetID, senderID := "user", message.Author.UserOpenID, message.Author.UserOpenID
	if eventType == "GROUP_AT_MESSAGE_CREATE" {
		targetType, targetID, senderID = "group", message.GroupOpenID, message.Author.MemberOpenID
	} else if eventType != "C2C_MESSAGE_CREATE" {
		return nil, time.Time{}, "", errors.New("QQ message event type is unsupported")
	}
	if targetID == "" || senderID == "" || len(targetID) > 128 || len(senderID) > 128 {
		return nil, time.Time{}, "", errors.New("QQ message target is invalid")
	}
	messageIndex, referencedIndex := qqMessageIndices(message.MessageScene.Ext)
	attachments := make([]map[string]any, 0, len(message.Attachments))
	if len(message.Attachments) > 100 {
		return nil, time.Time{}, "", errors.New("QQ message has too many attachments")
	}
	for _, attachment := range message.Attachments {
		item, err := normalizeQQAttachment(attachment)
		if err != nil {
			return nil, time.Time{}, "", err
		}
		attachments = append(attachments, item)
	}
	data := map[string]any{
		"eventType": eventType, "targetType": targetType, "targetId": targetID,
		"senderOpenId": senderID, "messageId": message.ID, "messageType": message.MessageType,
		"content": message.Content, "timestamp": eventTime.UTC().Format(time.RFC3339Nano), "attachments": attachments,
	}
	if messageIndex != "" {
		data["messageIndex"] = messageIndex
	}
	if referencedIndex != "" {
		data["referencedMessageIndex"] = referencedIndex
	}
	return data, eventTime.UTC(), targetID, nil
}

func qqMessageIndices(values []string) (string, string) {
	var messageIndex, referencedIndex string
	for _, value := range values {
		key, item, ok := strings.Cut(value, "=")
		item = strings.TrimSpace(item)
		if !ok || item == "" || len(item) > 256 {
			continue
		}
		switch strings.TrimSpace(key) {
		case "msg_idx":
			messageIndex = item
		case "ref_msg_idx":
			referencedIndex = item
		}
	}
	return messageIndex, referencedIndex
}

func normalizeQQAttachment(attachment struct {
	URL          string `json:"url"`
	Filename     string `json:"filename"`
	ContentType  string `json:"content_type"`
	Size         int64  `json:"size"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	VoiceWavURL  string `json:"voice_wav_url"`
	ASRReferText string `json:"asr_refer_text"`
}) (map[string]any, error) {
	attachment.URL = strings.TrimSpace(attachment.URL)
	attachment.Filename = strings.TrimSpace(attachment.Filename)
	attachment.ContentType = strings.TrimSpace(attachment.ContentType)
	attachment.VoiceWavURL = strings.TrimSpace(attachment.VoiceWavURL)
	attachment.ASRReferText = strings.TrimSpace(attachment.ASRReferText)
	if len(attachment.URL) > 4096 || utf8.RuneCountInString(attachment.Filename) > 255 ||
		len(attachment.ContentType) > 128 || len(attachment.VoiceWavURL) > 4096 ||
		utf8.RuneCountInString(attachment.ASRReferText) > 32768 || attachment.Size < 0 || attachment.Width < 0 || attachment.Height < 0 {
		return nil, errors.New("QQ message attachment is invalid")
	}
	item := map[string]any{
		"url": attachment.URL, "filename": attachment.Filename, "contentType": attachment.ContentType, "size": attachment.Size,
	}
	if attachment.Width > 0 {
		item["width"] = attachment.Width
	}
	if attachment.Height > 0 {
		item["height"] = attachment.Height
	}
	if attachment.VoiceWavURL != "" {
		item["voiceWavUrl"] = attachment.VoiceWavURL
	}
	if attachment.ASRReferText != "" {
		item["asrText"] = attachment.ASRReferText
	}
	return item, nil
}

var _ sdk.TriggerHandler = qqReceiveTrigger{}
