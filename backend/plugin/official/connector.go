package official

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

	"coinsphere/backend/plugin/sdk"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/gorilla/websocket"
)

const maxConnectorPayloadBytes = 1 << 20

var errPermanentWebSocketHandshake = errors.New("permanent WebSocket handshake failure")

func registerConnector(registrar sdk.Registrar, client *safeHTTPClient) error {
	if err := registrar.Action(sdk.NodeDescriptor{
		Type: "official.connector.http", Version: "1.0.0", Kind: sdk.NodeKindAction,
		ConfigSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"url":{"type":"string","title":"URL","format":"uri","maxLength":2048},"method":{"type":"string","title":"Method","enum":["GET","POST","PUT","PATCH"],"default":"GET"},"timeoutSeconds":{"type":"integer","title":"Timeout (seconds)","minimum":1,"maximum":60,"default":15},"useAuthorization":{"type":"boolean","title":"Use Authorization secret","default":false},"authorization":{"type":"string","title":"Authorization","x-coinsphere-secret":true}},"required":["url","method","timeoutSeconds","useAuthorization"],"additionalProperties":false}`),
		UISchema:     json.RawMessage(`{"ui:order":["url","method","timeoutSeconds","useAuthorization","authorization"]}`),
		InputSchema:  json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"body":{"type":"object","title":"JSON body","x-coinsphere-field-source":true}},"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"status":{"type":"integer"},"data":{"type":"object"}},"required":["status","data"],"additionalProperties":false}`),
		Pool:         sdk.PoolStream, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless,
	}, connectorHTTPAction{client: client}); err != nil {
		return err
	}
	if err := registrar.Trigger(sdk.NodeDescriptor{
		Type: "official.connector.webhook", Version: "1.0.0", Kind: sdk.NodeKindTrigger,
		ConfigSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"eventType":{"type":"string","title":"CloudEvent type","minLength":1,"maxLength":255},"secret":{"type":"string","title":"Webhook secret","x-coinsphere-secret":true}},"required":["eventType","secret"],"additionalProperties":false}`),
		UISchema:     json.RawMessage(`{"ui:order":["eventType","secret"]}`), InputSchema: emptyObjectSchema,
		OutputSchema: dynamicObjectSchema, Pool: sdk.PoolStream, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless,
	}, webhookTrigger{}); err != nil {
		return err
	}
	if err := registrar.Trigger(sdk.NodeDescriptor{
		Type: "official.connector.websocket", Version: "1.0.0", Kind: sdk.NodeKindTrigger,
		ConfigSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"url":{"type":"string","title":"WebSocket URL","format":"uri","maxLength":2048},"eventType":{"type":"string","title":"CloudEvent type","minLength":1,"maxLength":255},"idField":{"type":"string","title":"Event ID field","pattern":"^[A-Za-z0-9_.-]{1,128}$"},"partitionField":{"type":"string","title":"Partition field","pattern":"^[A-Za-z0-9_.-]{1,128}$"},"useAuthorization":{"type":"boolean","title":"Use Authorization secret","default":false},"authorization":{"type":"string","title":"Authorization","x-coinsphere-secret":true}},"required":["url","eventType","idField","partitionField","useAuthorization"],"additionalProperties":false}`),
		UISchema:     json.RawMessage(`{"ui:order":["url","eventType","idField","partitionField","useAuthorization","authorization"]}`), InputSchema: emptyObjectSchema,
		OutputSchema: dynamicObjectSchema, Pool: sdk.PoolStream, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless,
	}, websocketTrigger{client: client}); err != nil {
		return err
	}
	return registrar.ResultPage(sdk.ResultPageDescriptor{
		PageKey: "connections", Title: "连接诊断",
		ComponentEntry: "./official/connector/ResultPage.vue", ScopeSchema: emptyObjectSchema, Mobile: true,
	})
}

type connectorHTTPAction struct{ client *safeHTTPClient }

func (a connectorHTTPAction) Execute(ctx context.Context, request sdk.ActionRequest) (sdk.ActionResult, error) {
	var config struct {
		URL              string `json:"url"`
		Method           string `json:"method"`
		TimeoutSeconds   int    `json:"timeoutSeconds"`
		UseAuthorization bool   `json:"useAuthorization"`
	}
	var input struct {
		Body map[string]any `json:"body"`
	}
	if json.Unmarshal(request.Config, &config) != nil || json.Unmarshal(request.Input, &input) != nil {
		return sdk.ActionResult{}, errors.New("connector HTTP configuration is invalid")
	}
	target, err := url.ParseRequestURI(config.URL)
	if err != nil || !target.IsAbs() {
		return sdk.ActionResult{}, errors.New("connector HTTP URL is invalid")
	}
	if config.UseAuthorization && isBinanceDomain(target.Hostname()) {
		return sdk.ActionResult{}, unsafeEndpoint("generic connectors cannot authorize Binance requests")
	}
	var body io.Reader
	if config.Method != http.MethodGet && input.Body != nil {
		raw, err := json.Marshal(input.Body)
		if err != nil || len(raw) > maxConnectorPayloadBytes {
			return sdk.ActionResult{}, errors.New("connector HTTP request body exceeds the 1 MiB limit")
		}
		body = bytes.NewReader(raw)
	}
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(config.TimeoutSeconds)*time.Second)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(callCtx, config.Method, target.String(), body)
	if err != nil {
		return sdk.ActionResult{}, errors.New("create connector HTTP request failed")
	}
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("User-Agent", "CoinSphere-Connector/1.0")
	if config.Method != http.MethodGet {
		httpRequest.Header.Set("Idempotency-Key", request.OperationKey)
	}
	if config.UseAuthorization {
		authorization, err := request.Secrets.Read(callCtx, "authorization")
		if err != nil || len(authorization) == 0 || len(authorization) > 16<<10 {
			return sdk.ActionResult{}, errors.New("connector Authorization secret is unavailable")
		}
		httpRequest.Header.Set("Authorization", string(authorization))
	}
	response, err := a.client.Do(httpRequest)
	if err != nil {
		return sdk.ActionResult{}, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxConnectorPayloadBytes+1))
	if err != nil || len(raw) > maxConnectorPayloadBytes {
		return sdk.ActionResult{}, errors.New("connector HTTP response exceeds the 1 MiB limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return sdk.ActionResult{}, fmt.Errorf("connector HTTP response status %d", response.StatusCode)
	}
	var data map[string]any
	if json.Unmarshal(raw, &data) != nil || data == nil {
		return sdk.ActionResult{}, errors.New("connector HTTP response must be a JSON object")
	}
	return sdk.ActionResult{Output: mustMarshal(map[string]any{"status": response.StatusCode, "data": data})}, nil
}

type webhookTrigger struct{}

func (webhookTrigger) Run(ctx context.Context, _ sdk.TriggerRequest, _ sdk.Emitter) error {
	<-ctx.Done()
	return ctx.Err()
}

type websocketTrigger struct{ client *safeHTTPClient }

func (t websocketTrigger) Run(ctx context.Context, request sdk.TriggerRequest, emitter sdk.Emitter) error {
	var config struct {
		URL, EventType, IDField, PartitionField string
		UseAuthorization                        bool `json:"useAuthorization"`
	}
	if json.Unmarshal(request.Config, &config) != nil {
		return errors.New("connector WebSocket configuration is invalid")
	}
	target, err := url.ParseRequestURI(config.URL)
	if err != nil || !target.IsAbs() {
		return errors.New("connector WebSocket URL is invalid")
	}
	if err := t.client.validateWebSocketURL(ctx, target, config.UseAuthorization); err != nil {
		return err
	}
	headers := http.Header{"User-Agent": []string{"CoinSphere-Connector/1.0"}}
	if config.UseAuthorization {
		authorization, err := request.Secrets.Read(ctx, "authorization")
		if err != nil || len(authorization) == 0 || len(authorization) > 16<<10 {
			return errors.New("connector Authorization secret is unavailable")
		}
		headers.Set("Authorization", string(authorization))
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := t.readWebSocket(ctx, request, emitter, target, headers, config.EventType, config.IDField, config.PartitionField); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, errPermanentWebSocketHandshake) {
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
		}
	}
}

func (t websocketTrigger) readWebSocket(ctx context.Context, request sdk.TriggerRequest, emitter sdk.Emitter, target *url.URL, headers http.Header, eventType, idField, partitionField string) error {
	if err := t.client.validateWebSocketURL(ctx, target, headers.Get("Authorization") != ""); err != nil {
		return err
	}
	dialer := websocket.Dialer{Proxy: nil, NetDialContext: t.client.dialContext, HandshakeTimeout: 10 * time.Second}
	connection, response, err := dialer.DialContext(ctx, target.String(), headers)
	status := 0
	if response != nil {
		status = response.StatusCode
		if response.Body != nil {
			response.Body.Close()
		}
	}
	if err != nil {
		if permanentWebSocketStatus(status) {
			return fmt.Errorf("%w: status %d", errPermanentWebSocketHandshake, status)
		}
		return err
	}
	connection.SetReadLimit(maxConnectorPayloadBytes)
	closed := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			connection.Close()
		case <-closed:
		}
	}()
	defer close(closed)
	defer connection.Close()
	for {
		var data map[string]any
		if err := connection.ReadJSON(&data); err != nil {
			return err
		}
		eventID, ok := connectorStringField(data, idField, 128)
		if !ok {
			return errors.New("connector WebSocket event ID field is missing or invalid")
		}
		partitionKey, ok := connectorStringField(data, partitionField, 256)
		if !ok {
			return errors.New("connector WebSocket partition field is missing or invalid")
		}
		event := cloudevents.NewEvent()
		event.SetID(eventID)
		event.SetSource(fmt.Sprintf("urn:coinsphere:connector:websocket:%s:%s", request.Revision.WorkflowID, request.NodeInstanceID))
		event.SetType(eventType)
		event.SetTime(time.Now().UTC())
		event.SetExtension("partitionkey", partitionKey)
		if err := event.SetData(cloudevents.ApplicationJSON, data); err != nil {
			return errors.New("encode connector WebSocket event failed")
		}
		if err := emitter.Emit(ctx, event); err != nil {
			return err
		}
	}
}

func permanentWebSocketStatus(status int) bool {
	return status >= 400 && status < 500 && status != http.StatusRequestTimeout && status != http.StatusTooManyRequests
}

func connectorStringField(data map[string]any, field string, limit int) (string, bool) {
	var current any = data
	for _, segment := range strings.Split(field, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return "", false
		}
		current, ok = object[segment]
		if !ok {
			return "", false
		}
	}
	value, ok := current.(string)
	value = strings.TrimSpace(value)
	return value, ok && value != "" && len(value) <= limit
}

func mustMarshal(value any) json.RawMessage {
	raw, _ := json.Marshal(value)
	return raw
}

var _ sdk.ActionHandler = connectorHTTPAction{}
var _ sdk.TriggerHandler = webhookTrigger{}
var _ sdk.TriggerHandler = websocketTrigger{}
