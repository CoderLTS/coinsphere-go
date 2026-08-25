package official

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"testing"

	"coinsphere/backend/plugin/sdk"
)

var testPublicIP = netip.MustParseAddr("93.184.216.34")

func TestOfficialPluginRegistration(t *testing.T) {
	registry := sdk.NewRegistry()
	if err := RegisterAll(registry, []string{"allowed.example"}); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := registry.Action("official.connector.http"); !ok {
		t.Fatal("connector HTTP action was not registered")
	}
	if _, _, ok := registry.Trigger("official.connector.webhook"); !ok {
		t.Fatal("connector webhook trigger was not registered")
	}
	if _, _, ok := registry.Trigger("official.connector.websocket"); !ok {
		t.Fatal("connector WebSocket trigger was not registered")
	}
	if _, _, ok := registry.Action("official.ai.model_call"); !ok {
		t.Fatal("AI model call action was not registered")
	}
	if _, ok := registry.ResultPage(connectorPluginID, "connections"); !ok {
		t.Fatal("connector result page was not registered")
	}
	if _, ok := registry.ResultPage(aiPluginID, "calls"); !ok {
		t.Fatal("AI result page was not registered")
	}
	if err := RegisterQuant(registry, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := registry.Trigger("official.quant.binance_candles"); !ok {
		t.Fatal("Quant candle trigger was not registered")
	}
	if _, _, ok := registry.Action("official.quant.backtest"); !ok {
		t.Fatal("Quant backtest action was not registered")
	}
	if _, _, ok := registry.Action("official.quant.signal"); !ok {
		t.Fatal("Quant signal action was not registered")
	}
	if _, _, ok := registry.Action("official.quant.paper_execute"); !ok {
		t.Fatal("Quant Paper action was not registered")
	}
	if _, _, ok := registry.Strategy(smaStrategyID); !ok {
		t.Fatal("Quant strategy was not registered")
	}
	if _, ok := registry.ResultPage(quantPluginID, "quant"); !ok {
		t.Fatal("Quant result page was not registered")
	}
	if _, ok := registry.ResultPage(quantPluginID, "paper"); !ok {
		t.Fatal("Quant Paper result page was not registered")
	}
	if err := RegisterNotification(registry, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := registry.Action("official.notification.in_app"); !ok {
		t.Fatal("Notification action was not registered")
	}
	if _, ok := registry.ResultPage(notificationPluginID, "deliveries"); !ok {
		t.Fatal("Notification result page was not registered")
	}
	quantRoutes, notificationRoutes := 0, 0
	for _, route := range registry.Routes() {
		switch route.PluginID {
		case quantPluginID:
			quantRoutes++
		case notificationPluginID:
			notificationRoutes++
		}
	}
	if quantRoutes != 11 || notificationRoutes != 2 {
		t.Fatalf("route counts: Quant=%d Notification=%d", quantRoutes, notificationRoutes)
	}
}

func TestOfficialNetworkPolicy(t *testing.T) {
	lookupPublic := func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{testPublicIP}, nil
	}
	client, err := newSafeHTTPClientWithDeps([]string{"allowed.example", "api.binance.com", "stream.binance.com"}, lookupPublic, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		method  string
		target  string
		blocked bool
	}{
		{http.MethodGet, "https://allowed.example/data", false},
		{http.MethodGet, "https://sub.allowed.example/data", true},
		{http.MethodGet, "https://api.binance.com/api/v3/klines", false},
		{http.MethodGet, "https://api.binance.com/api/v3/klines/private", true},
		{http.MethodGet, "https://api.binance.com/api/v3/account", true},
		{http.MethodPost, "https://api.binance.com/api/v3/order", true},
	} {
		target, parseErr := url.Parse(test.target)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		err := client.validateHTTPURL(context.Background(), test.method, target)
		if errors.Is(err, errUnsafeEndpoint) != test.blocked {
			t.Fatalf("target %s blocked=%t, err=%v", test.target, test.blocked, err)
		}
	}
	websocketURL, _ := url.Parse("wss://stream.binance.com/ws/btcusdt@kline_1m")
	if err := client.validateWebSocketURL(context.Background(), websocketURL, false); err != nil {
		t.Fatalf("public Binance stream was blocked: %v", err)
	}
	if err := client.validateWebSocketURL(context.Background(), websocketURL, true); !errors.Is(err, errUnsafeEndpoint) {
		t.Fatalf("authorized Binance stream was accepted: %v", err)
	}

	mixedLookup := func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{testPublicIP, netip.MustParseAddr("127.0.0.1")}, nil
	}
	mixed, _ := newSafeHTTPClientWithDeps([]string{"allowed.example"}, mixedLookup, nil)
	if _, err := mixed.resolvePublic(context.Background(), "allowed.example"); !errors.Is(err, errUnsafeEndpoint) {
		t.Fatalf("mixed public/private DNS answer was accepted: %v", err)
	}
}

func TestPermanentWebSocketHandshakeStatus(t *testing.T) {
	for status, permanent := range map[int]bool{
		400: true, 401: true, 403: true, 404: true,
		408: false, 429: false, 500: false,
	} {
		if got := permanentWebSocketStatus(status); got != permanent {
			t.Fatalf("status %d permanent = %t, want %t", status, got, permanent)
		}
	}
}

func TestOfficialHTTPAndAIUseStructuredBoundaries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/connector":
			if request.Header.Get("Authorization") != "Bearer connector-test" {
				t.Error("connector Authorization secret was not applied")
			}
			if request.Header.Get("Idempotency-Key") != "connector-operation" {
				t.Error("connector operation key was not applied")
			}
			_, _ = response.Write([]byte(`{"accepted":true}`))
		case "/ai":
			if request.Header.Get("Authorization") != "Bearer ai-test" {
				t.Error("AI API key was not applied")
			}
			if request.Header.Get("Idempotency-Key") != "ai-operation" {
				t.Error("AI operation key was not applied")
			}
			_, _ = response.Write([]byte(`{"model":"test-model","choices":[{"message":{"content":"{\"score\":1}"}}],"usage":{"total_tokens":2}}`))
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	serverURL, _ := url.Parse(server.URL)
	lookup := func(context.Context, string, string) ([]netip.Addr, error) { return []netip.Addr{testPublicIP}, nil }
	dialer := &net.Dialer{}
	dial := func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, server.Listener.Addr().String())
	}
	client, err := newSafeHTTPClientWithDeps([]string{"allowed.example"}, lookup, dial)
	if err != nil {
		t.Fatal(err)
	}
	base := "http://" + net.JoinHostPort("allowed.example", serverURL.Port())
	connector := connectorHTTPAction{client: client}
	result, err := connector.Execute(context.Background(), sdk.ActionRequest{
		OperationKey: "connector-operation",
		Config:       json.RawMessage(`{"url":"` + base + `/connector","method":"POST","timeoutSeconds":5,"useAuthorization":true}`),
		Input:        json.RawMessage(`{"body":{"value":1}}`),
		Secrets:      staticSecretReader{"authorization": "Bearer connector-test"},
	})
	if err != nil || string(result.Output) != `{"data":{"accepted":true},"status":200}` {
		t.Fatalf("connector output = %s, err = %v", result.Output, err)
	}
	ai := aiModelCallAction{client: client}
	result, err = ai.Execute(context.Background(), sdk.ActionRequest{
		OperationKey: "ai-operation",
		Config:       json.RawMessage(`{"endpoint":"` + base + `/ai","model":"test-model","timeoutSeconds":5}`),
		Input:        json.RawMessage(`{"prompt":"score","data":{"value":1}}`),
		Secrets:      staticSecretReader{"apiKey": "ai-test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		Data map[string]any `json:"data"`
	}
	if json.Unmarshal(result.Output, &output) != nil || output.Data["score"] != float64(1) {
		t.Fatalf("AI output = %s", result.Output)
	}
}

type staticSecretReader map[string]string

func (r staticSecretReader) Read(_ context.Context, field string) ([]byte, error) {
	value := r[field]
	if value == "" {
		return nil, errors.New("secret not found")
	}
	return []byte(value), nil
}
