package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestWorkflowWebSocketOriginAndPort(t *testing.T) {
	request := httptest.NewRequest("GET", "http://example.test/api/v1/workflows/1/activity/ws", nil)
	request.Header.Set("Origin", "http://example.test")
	if !checkWorkflowWebSocketOrigin(request) {
		t.Fatal("same origin websocket request was rejected")
	}
	request.Header.Set("Origin", "https://example.test")
	if checkWorkflowWebSocketOrigin(request) {
		t.Fatal("cross-scheme websocket request was accepted")
	}
	for _, raw := range []string{"http://example.test:0", "http://example.test:65536", "http://example.test:"} {
		origin, err := url.Parse(raw)
		if err == nil {
			if port, ok := workflowOriginPort(origin); ok || port != "" {
				t.Fatalf("invalid origin port %q returned %q, %t", raw, port, ok)
			}
		}
	}
}

func TestWorkflowWebSocketAuthenticationAndCursor(t *testing.T) {
	request := httptest.NewRequest("GET", "http://example.test/api/v1/workflows/1/activity/ws?after=42", nil)
	request.Header.Set("Sec-WebSocket-Protocol", "coinsphere.workflow-activity.v1, access-token")
	if token, ok := workflowActivityWebSocketToken(request); !ok || token != "access-token" {
		t.Fatalf("websocket token = %q, %t", token, ok)
	}
	if cursor, ok := workflowActivityWebSocketCursor(request); !ok || cursor != 42 {
		t.Fatalf("websocket cursor = %d, %t", cursor, ok)
	}
	for name, mutate := range map[string]func(*http.Request){
		"missing token": func(r *http.Request) { r.Header.Set("Sec-WebSocket-Protocol", workflowActivityWebSocketProtocol) },
		"wrong order": func(r *http.Request) {
			r.Header.Set("Sec-WebSocket-Protocol", "access-token, "+workflowActivityWebSocketProtocol)
		},
		"unknown query":   func(r *http.Request) { r.URL.RawQuery = "after=1&extra=1" },
		"negative cursor": func(r *http.Request) { r.URL.RawQuery = "after=-1" },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := request.Clone(request.Context())
			invalid.Header = request.Header.Clone()
			mutate(invalid)
			if name == "missing token" || name == "wrong order" {
				if _, ok := workflowActivityWebSocketToken(invalid); ok {
					t.Fatal("invalid websocket authentication was accepted")
				}
			} else if _, ok := workflowActivityWebSocketCursor(invalid); ok {
				t.Fatal("invalid websocket cursor was accepted")
			}
		})
	}
}
