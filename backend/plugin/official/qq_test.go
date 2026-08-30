package official

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"coinsphere/backend/plugin/sdk"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/gorilla/websocket"
)

type qqRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn qqRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func newQQTestRuntime(handler qqRoundTripFunc) *qqRuntime {
	client := &safeHTTPClient{
		client: &http.Client{Transport: handler},
		allowedHosts: map[string]struct{}{
			"bots.qq.com": {}, "api.sgroup.qq.com": {}, "api.bot.qq.com": {},
		},
		lookup: func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
		},
	}
	return &qqRuntime{http: client, tokens: map[string]qqAccessToken{}, receivers: map[string]struct{}{}}
}

func qqTestResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func decodeQQTestBody(t *testing.T, request *http.Request) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	return body
}

func TestRegisterQQ(t *testing.T) {
	if err := RegisterQQ(sdk.NewRegistry(), nil); err != nil {
		t.Fatalf("register QQ plugin: %v", err)
	}
}

func TestQQSendTextAndMarkdown(t *testing.T) {
	tests := []struct {
		name        string
		input       qqSendInput
		wantPath    string
		wantType    float64
		wantContent string
	}{
		{
			name: "group text",
			input: qqSendInput{
				SubjectKey: "subject", TargetType: "group", TargetID: "group-1",
				MessageType: "text", Content: "  hello  ", KeyboardTemplateID: "keyboard-1",
				ReplyToMessageID: "source-1", ReplySequence: 5,
			},
			wantPath: "/v2/groups/group-1/messages", wantType: 0, wantContent: "  hello  ",
		},
		{
			name: "user markdown",
			input: qqSendInput{
				SubjectKey: "subject", TargetType: "user", TargetID: "user-1",
				MessageType: "markdown", Content: "# hello", ReplyToMessageID: "source-2", ReplySequence: 4,
			},
			wantPath: "/v2/users/user-1/messages", wantType: 2, wantContent: "# hello",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var tokenRequests int
			runtime := newQQTestRuntime(func(request *http.Request) (*http.Response, error) {
				switch request.URL.Host {
				case "bots.qq.com":
					tokenRequests++
					body := decodeQQTestBody(t, request)
					if body["appId"] != "app-1" || body["clientSecret"] != "secret-1" {
						t.Fatalf("unexpected token payload: %#v", body)
					}
					return qqTestResponse(http.StatusOK, `{"access_token":"token-1","expires_in":"7200"}`), nil
				case "api.sgroup.qq.com":
					if request.URL.Path != test.wantPath {
						t.Fatalf("message path = %q, want %q", request.URL.Path, test.wantPath)
					}
					if request.Header.Get("Authorization") != "QQBot token-1" ||
						request.Header.Get("X-Union-Appid") != "app-1" {
						t.Fatalf("unexpected QQ headers: %#v", request.Header)
					}
					body := decodeQQTestBody(t, request)
					if body["msg_type"] != test.wantType {
						t.Fatalf("msg_type = %#v, want %v", body["msg_type"], test.wantType)
					}
					if test.input.MessageType == "text" {
						if body["content"] != test.wantContent {
							t.Fatalf("content = %#v, want %q", body["content"], test.wantContent)
						}
						if body["keyboard"].(map[string]any)["id"] != "keyboard-1" {
							t.Fatalf("unexpected keyboard payload: %#v", body["keyboard"])
						}
					} else if body["markdown"].(map[string]any)["content"] != test.wantContent {
						t.Fatalf("unexpected Markdown payload: %#v", body["markdown"])
					}
					if body["msg_id"] != test.input.ReplyToMessageID ||
						body["msg_seq"] != float64(test.input.ReplySequence) {
						t.Fatalf("unexpected reply payload: %#v", body)
					}
					return qqTestResponse(http.StatusOK, `{"id":"provider-1"}`), nil
				default:
					t.Fatalf("unexpected host %q", request.URL.Host)
					return nil, nil
				}
			})
			if err := validateQQSendInput(&test.input); err != nil {
				t.Fatalf("validate input: %v", err)
			}
			messageID, category, err := runtime.send(context.Background(), qqCredentials{
				AppID: "app-1", ClientSecret: "secret-1",
			}, test.input)
			if err != nil || category != "" || messageID != "provider-1" {
				t.Fatalf("send result = (%q, %q, %v)", messageID, category, err)
			}
			if tokenRequests != 1 {
				t.Fatalf("token requests = %d, want 1", tokenRequests)
			}
		})
	}
}

func TestQQSendMediaAndTokenCache(t *testing.T) {
	var tokenRequests int
	var apiRequests int
	runtime := newQQTestRuntime(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "bots.qq.com" {
			tokenRequests++
			return qqTestResponse(http.StatusOK, `{"access_token":"token-1","expires_in":7200}`), nil
		}
		apiRequests++
		body := decodeQQTestBody(t, request)
		switch request.URL.Path {
		case "/v2/groups/group-1/files", "/v2/users/user-1/files":
			if body["file_type"] != float64(1) || body["url"] != "https://cdn.example/image.png" ||
				body["file_name"] != "image.png" || body["srv_send_msg"] != false {
				t.Fatalf("unexpected media upload payload: %#v", body)
			}
			return qqTestResponse(http.StatusOK, `{"file_info":"file-info-1"}`), nil
		case "/v2/groups/group-1/messages", "/v2/users/user-1/messages":
			media, _ := body["media"].(map[string]any)
			keyboard, _ := body["keyboard"].(map[string]any)
			if body["msg_type"] != float64(7) || media["file_info"] != "file-info-1" ||
				keyboard["id"] != "keyboard-1" {
				t.Fatalf("unexpected media message payload: %#v", body)
			}
			return qqTestResponse(http.StatusOK, `{"id":"provider-media"}`), nil
		default:
			t.Fatalf("unexpected QQ path %q", request.URL.Path)
			return nil, nil
		}
	})
	credentials := qqCredentials{AppID: "app-1", ClientSecret: "secret-1"}
	input := qqSendInput{
		SubjectKey: "subject", TargetType: "group", TargetID: "group-1", MessageType: "media",
		MediaType: "image", MediaURL: "https://cdn.example/image.png", MediaFilename: "image.png",
		KeyboardTemplateID: "keyboard-1",
	}
	if err := validateQQSendInput(&input); err != nil {
		t.Fatalf("validate media input: %v", err)
	}
	for range 2 {
		messageID, category, err := runtime.send(context.Background(), credentials, input)
		if err != nil || category != "" || messageID != "provider-media" {
			t.Fatalf("send media result = (%q, %q, %v)", messageID, category, err)
		}
		input.TargetType, input.TargetID = "user", "user-1"
	}
	if tokenRequests != 1 || apiRequests != 4 {
		t.Fatalf("request counts = token %d, API %d; want 1 and 4", tokenRequests, apiRequests)
	}
}

func TestQQValidationAndErrorCategories(t *testing.T) {
	inputs := []qqSendInput{
		{SubjectKey: "s", TargetType: "group", TargetID: "g", MessageType: "text", Content: "x", ReplySequence: 6},
		{SubjectKey: "s", TargetType: "user", TargetID: "u", MessageType: "text", Content: "x", ReplySequence: 5},
		{SubjectKey: "s", TargetType: "group", TargetID: "g", MessageType: "text", Content: "x", MediaFilename: "ignored.txt"},
		{SubjectKey: "s", TargetType: "group", TargetID: "g", MessageType: "media", MediaType: "image", MediaURL: "file:///tmp/a"},
	}
	for index := range inputs {
		if err := validateQQSendInput(&inputs[index]); err == nil {
			t.Fatalf("input %d unexpectedly passed validation", index)
		}
	}

	runtime := newQQTestRuntime(func(*http.Request) (*http.Response, error) {
		return qqTestResponse(http.StatusServiceUnavailable, `{"code":500}`), nil
	})
	_, category, err := runtime.accessToken(context.Background(), qqCredentials{AppID: "app", ClientSecret: "secret"})
	if err == nil || category != "provider_unavailable" {
		t.Fatalf("token error category = %q, %v", category, err)
	}
}

func TestQQGatewayProtocol(t *testing.T) {
	session := &qqGatewaySession{}
	identify := qqGatewayAuthentication("token-1", session)
	if identify["op"] != 2 {
		t.Fatalf("identify opcode = %#v", identify["op"])
	}
	identifyData := identify["d"].(map[string]any)
	if identifyData["token"] != "QQBot token-1" || identifyData["intents"] != qqGroupAndC2CIntent {
		t.Fatalf("unexpected identify payload: %#v", identifyData)
	}

	session.ID = "session-1"
	session.Seq.Store(42)
	resume := qqGatewayAuthentication("token-2", session)
	resumeData := resume["d"].(map[string]any)
	if resume["op"] != 6 || resumeData["session_id"] != "session-1" || resumeData["seq"] != int64(42) {
		t.Fatalf("unexpected resume payload: %#v", resume)
	}
	if !errors.Is(qqGatewayReadError(&websocket.CloseError{Code: 4009}), errQQInvalidSession) {
		t.Fatal("session timeout must invalidate the Gateway session")
	}
	if !errors.Is(qqGatewayReadError(&websocket.CloseError{Code: 4013}), errQQPermanentGateway) {
		t.Fatal("invalid intents must stop the Gateway session")
	}
	if !errors.Is(qqInvalidSessionError(json.RawMessage(`false`)), errQQInvalidSession) ||
		!errors.Is(qqInvalidSessionError(json.RawMessage(`true`)), errQQReconnect) {
		t.Fatal("invalid session resumability was not respected")
	}
	if !isQQGatewayHost("api.sgroup.qq.com") || !isQQGatewayHost("API.BOT.QQ.COM") ||
		isQQGatewayHost("example.com") {
		t.Fatal("QQ Gateway host allowlist is invalid")
	}
}

func qqWebSocketPair(t *testing.T) (*websocket.Conn, *websocket.Conn, func()) {
	t.Helper()
	serverConnection := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := (&websocket.Upgrader{}).Upgrade(writer, request, nil)
		if err == nil {
			serverConnection <- connection
		}
	}))
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		server.Close()
		t.Fatalf("dial test WebSocket: %v", err)
	}
	serverSide := <-serverConnection
	return client, serverSide, func() {
		client.Close()
		serverSide.Close()
		server.Close()
	}
}

func TestQQGatewayHeartbeatAndCancel(t *testing.T) {
	client, server, cleanup := qqWebSocketPair(t)
	defer cleanup()
	session := &qqGatewaySession{}
	session.Seq.Store(9)
	var ack atomic.Bool
	ack.Store(true)
	done := make(chan struct{})
	now := make(chan struct{}, 1)
	exited := make(chan struct{})
	go func() {
		(&qqRuntime{}).runGatewayHeartbeat(client, session, time.Hour, &ack, now, done)
		close(exited)
	}()
	now <- struct{}{}
	var heartbeat struct {
		Op int   `json:"op"`
		D  int64 `json:"d"`
	}
	if err := server.ReadJSON(&heartbeat); err != nil {
		t.Fatalf("read heartbeat: %v", err)
	}
	if heartbeat.Op != 1 || heartbeat.D != 9 {
		t.Fatalf("unexpected heartbeat: %#v", heartbeat)
	}
	close(done)
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("heartbeat did not stop")
	}

	cancelClient, cancelServer, cancelCleanup := qqWebSocketPair(t)
	defer cancelCleanup()
	ctx, cancel := context.WithCancel(context.Background())
	cancelDone := make(chan struct{})
	go closeQQGatewayOnCancel(ctx, cancelClient, cancelDone)
	cancel()
	if err := cancelServer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	if _, _, err := cancelServer.ReadMessage(); err == nil {
		t.Fatal("cancel did not close the Gateway connection")
	}
}

type qqCaptureEmitter struct {
	event cloudevents.Event
}

func (emitter *qqCaptureEmitter) Emit(_ context.Context, event cloudevents.Event) error {
	emitter.event = event
	return nil
}

func TestQQMessageNormalization(t *testing.T) {
	tests := []struct {
		eventType  string
		raw        string
		targetType string
		targetID   string
		senderID   string
		messageID  string
	}{
		{
			eventType:  "GROUP_AT_MESSAGE_CREATE",
			raw:        `{"id":"group-message","content":"hello","group_openid":"group-1","timestamp":"2026-08-30T01:02:03Z","message_type":0,"auth_token":"hidden","author":{"member_openid":"member-1","user_openid":"ignored"},"message_scene":{"ext":["msg_idx=7","ref_msg_idx=4"]},"attachments":[{"url":"https://cdn.example/a.png","filename":"a.png","content_type":"image/png","size":12,"width":3,"height":4,"auth_token":"hidden"}]}`,
			targetType: "group", targetID: "group-1", senderID: "member-1", messageID: "group-message",
		},
		{
			eventType:  "C2C_MESSAGE_CREATE",
			raw:        `{"id":"user-message","content":"hello","timestamp":"2026-08-30T01:02:03Z","message_type":0,"auth_token":"hidden","author":{"user_openid":"user-1"}}`,
			targetType: "user", targetID: "user-1", senderID: "user-1", messageID: "user-message",
		},
	}
	for _, test := range tests {
		t.Run(test.eventType, func(t *testing.T) {
			emitter := &qqCaptureEmitter{}
			if err := emitQQMessage(context.Background(), emitter, "app-1", test.eventType, json.RawMessage(test.raw)); err != nil {
				t.Fatalf("emit QQ message: %v", err)
			}
			if emitter.event.ID() != test.messageID || emitter.event.Source() != "urn:coinsphere:qq:app-1" ||
				emitter.event.Extensions()["partitionkey"] != test.targetID {
				t.Fatalf("unexpected CloudEvent context: %#v", emitter.event.Context)
			}
			var data map[string]any
			if err := emitter.event.DataAs(&data); err != nil {
				t.Fatalf("decode CloudEvent data: %v", err)
			}
			if data["targetType"] != test.targetType || data["targetId"] != test.targetID ||
				data["senderOpenId"] != test.senderID {
				t.Fatalf("unexpected normalized message: %#v", data)
			}
			if test.eventType == "GROUP_AT_MESSAGE_CREATE" &&
				(data["messageIndex"] != "7" || data["referencedMessageIndex"] != "4" ||
					len(data["attachments"].([]any)) != 1) {
				t.Fatalf("group references or attachments were not preserved: %#v", data)
			}
			encoded, _ := json.Marshal(data)
			if strings.Contains(string(encoded), "auth_token") || strings.Contains(string(encoded), "hidden") {
				t.Fatalf("sensitive fields leaked into normalized message: %s", encoded)
			}
		})
	}
}

func TestQQSingleReceiverPerAppID(t *testing.T) {
	runtime := &qqRuntime{receivers: map[string]struct{}{}}
	if !runtime.acquireReceiver("app-1") || runtime.acquireReceiver("app-1") {
		t.Fatal("receiver lock allowed two active receivers for one AppID")
	}
	runtime.releaseReceiver("app-1")
	if !runtime.acquireReceiver("app-1") {
		t.Fatal("receiver lock was not released")
	}
}
