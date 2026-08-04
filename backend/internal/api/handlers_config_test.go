package api

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestCheckWebSocketOrigin(t *testing.T) {
	tests := []struct {
		name      string
		host      string
		origins   []string
		forwarded []string
		tls       bool
		want      bool
	}{
		{name: "direct http", host: "app.example:8080", origins: []string{"http://app.example:8080"}, want: true},
		{name: "default port normalized", host: "app.example:80", origins: []string{"http://app.example"}, want: true},
		{name: "case insensitive host", host: "app.example:8080", origins: []string{"HTTP://APP.EXAMPLE:8080"}, want: true},
		{name: "ipv6 authority", host: "[::1]:8080", origins: []string{"http://[::1]:8080"}, want: true},
		{name: "direct tls", host: "app.example", origins: []string{"https://app.example"}, tls: true, want: true},
		{name: "trusted proxy tls", host: "app.example:8443", origins: []string{"https://app.example:8443"}, forwarded: []string{"https"}, want: true},
		{name: "missing origin", host: "app.example"},
		{name: "multiple origins", host: "app.example", origins: []string{"http://app.example", "http://app.example"}},
		{name: "null origin", host: "app.example", origins: []string{"null"}},
		{name: "malformed origin", host: "app.example", origins: []string{"://app.example"}},
		{name: "unsupported scheme", host: "app.example", origins: []string{"ws://app.example"}},
		{name: "cross scheme", host: "app.example", origins: []string{"https://app.example"}},
		{name: "cross host", host: "app.example", origins: []string{"http://evil.example"}},
		{name: "host suffix attack", host: "app.example", origins: []string{"http://app.example.evil"}},
		{name: "cross port", host: "app.example:8080", origins: []string{"http://app.example:8081"}},
		{name: "userinfo", host: "app.example", origins: []string{"http://user@app.example"}},
		{name: "path", host: "app.example", origins: []string{"http://app.example/"}},
		{name: "query", host: "app.example", origins: []string{"http://app.example?x=1"}},
		{name: "fragment", host: "app.example", origins: []string{"http://app.example#x"}},
		{name: "empty port", host: "app.example", origins: []string{"http://app.example:"}},
		{name: "out of range origin port", host: "app.example:8080", origins: []string{"http://app.example:99999"}},
		{name: "out of range request port", host: "app.example:99999", origins: []string{"http://app.example:99999"}},
		{name: "request host query", host: "app.example?x=1", origins: []string{"http://app.example"}},
		{name: "request host fragment", host: "app.example#x", origins: []string{"http://app.example"}},
		{name: "invalid forwarded scheme", host: "app.example", origins: []string{"http://app.example"}, forwarded: []string{"ws"}},
		{name: "multiple forwarded schemes", host: "app.example", origins: []string{"https://app.example"}, forwarded: []string{"https", "http"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://backend/ws/notifications", nil)
			r.Host = test.host
			for _, origin := range test.origins {
				r.Header.Add("Origin", origin)
			}
			for _, scheme := range test.forwarded {
				r.Header.Add("X-Forwarded-Proto", scheme)
			}
			if test.tls {
				r.TLS = &tls.ConnectionState{}
			}
			if got := checkWebSocketOrigin(r); got != test.want {
				t.Fatalf("checkWebSocketOrigin() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestWebSocketUpgraderEnforcesOrigin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err == nil {
			_ = conn.Close()
		}
	}))
	defer server.Close()
	wsURL := strings.Replace(server.URL, "http", "ws", 1)

	sameOriginHeader := http.Header{"Origin": []string{server.URL}}
	dialer := websocket.Dialer{
		Subprotocols: []string{notificationsWebSocketProtocol, "test-access-token"},
	}
	conn, response, err := dialer.Dial(wsURL, sameOriginHeader)
	if err != nil {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		t.Fatalf("same-origin handshake failed: status=%d err=%v", status, err)
	}
	if conn.Subprotocol() != notificationsWebSocketProtocol {
		t.Fatalf("selected protocol = %q, want fixed notification protocol", conn.Subprotocol())
	}
	_ = conn.Close()
	if response != nil {
		_ = response.Body.Close()
	}

	crossOriginHeader := http.Header{"Origin": []string{"http://evil.example"}}
	conn, response, err = websocket.DefaultDialer.Dial(wsURL, crossOriginHeader)
	if conn != nil {
		_ = conn.Close()
	}
	status := 0
	if response != nil {
		status = response.StatusCode
		_ = response.Body.Close()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin handshake: status=%d err=%v, want 403", status, err)
	}
}

func TestNotificationWebSocketTokenContract(t *testing.T) {
	tests := []struct {
		name       string
		target     string
		protocols  []string
		headerSets [][]string
		want       bool
	}{
		{name: "fixed protocol and token", target: "http://app/ws/notifications", protocols: []string{notificationsWebSocketProtocol, "test-access-token"}, want: true},
		{name: "query rejected", target: "http://app/ws/notifications?token=test", protocols: []string{notificationsWebSocketProtocol, "test-access-token"}},
		{name: "missing token", target: "http://app/ws/notifications", protocols: []string{notificationsWebSocketProtocol}},
		{name: "wrong order", target: "http://app/ws/notifications", protocols: []string{"test-access-token", notificationsWebSocketProtocol}},
		{name: "extra protocol", target: "http://app/ws/notifications", protocols: []string{notificationsWebSocketProtocol, "test-access-token", "extra"}},
		{name: "duplicate header", target: "http://app/ws/notifications", headerSets: [][]string{{notificationsWebSocketProtocol, "test-access-token"}, {notificationsWebSocketProtocol, "test-access-token"}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, test.target, nil)
			if len(test.protocols) > 0 {
				r.Header.Set("Sec-WebSocket-Protocol", strings.Join(test.protocols, ", "))
			}
			for _, protocols := range test.headerSets {
				r.Header.Add("Sec-WebSocket-Protocol", strings.Join(protocols, ", "))
			}
			_, ok := notificationWebSocketToken(r)
			if ok != test.want {
				t.Fatalf("notificationWebSocketToken() accepted=%v, want %v", ok, test.want)
			}
		})
	}
}
