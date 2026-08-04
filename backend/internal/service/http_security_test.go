package service

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"sync/atomic"
	"testing"

	"coinsphere/backend/internal/config"
)

var safeHTTPTestPublicIP = netip.MustParseAddr("93.184.216.34")

func TestSafeHTTPURLPolicySSRF(t *testing.T) {
	lookup := func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{safeHTTPTestPublicIP}, nil
	}
	tests := []struct {
		name    string
		allowed []string
		target  string
		blocked bool
	}{
		{name: "exact host", allowed: []string{"Allowed.Example."}, target: "https://allowed.example/path"},
		{name: "empty allowlist", target: "https://allowed.example/path", blocked: true},
		{name: "subdomain is not exact", allowed: []string{"allowed.example"}, target: "https://sub.allowed.example/path", blocked: true},
		{name: "unsupported scheme", allowed: []string{"allowed.example"}, target: "ftp://allowed.example/path", blocked: true},
		{name: "relative URL", allowed: []string{"allowed.example"}, target: "/path", blocked: true},
		{name: "userinfo", allowed: []string{"allowed.example"}, target: "http://user:pass@allowed.example/path", blocked: true},
		{name: "IP target", allowed: []string{"allowed.example"}, target: "http://127.0.0.1/path", blocked: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, err := newSafeHTTPClientWithDeps(test.allowed, lookup, nil)
			if err != nil {
				t.Fatalf("create client: %v", err)
			}
			target, err := url.Parse(test.target)
			if err != nil {
				t.Fatalf("parse target: %v", err)
			}
			err = client.validateURL(context.Background(), target)
			if errors.Is(err, errUnsafeHTTPRequest) != test.blocked {
				t.Fatalf("blocked=%v, error=%v", test.blocked, err)
			}
		})
	}
}

func TestSafeHTTPRejectsInvalidAllowlistSSRF(t *testing.T) {
	for _, host := range []string{"*.example.com", "example.com:443", "127.0.0.1", "https://example.com"} {
		t.Run(host, func(t *testing.T) {
			_, err := newSafeHTTPClientWithDeps([]string{host}, nil, nil)
			if !errors.Is(err, errUnsafeHTTPRequest) {
				t.Fatalf("expected blocked allowlist entry, got %v", err)
			}
		})
	}
}

func TestSafeHTTPRejectsNonPublicSSRF(t *testing.T) {
	for _, raw := range []string{
		"127.0.0.1",
		"10.0.0.1",
		"100.64.0.1",
		"169.254.169.254",
		"192.0.2.1",
		"198.18.0.1",
		"224.0.0.1",
		"240.0.0.1",
		"::1",
		"::ffff:127.0.0.1",
		"fc00::1",
		"fe80::1",
		"2001:db8::1",
	} {
		t.Run(raw, func(t *testing.T) {
			if isPublicHTTPAddress(netip.MustParseAddr(raw)) {
				t.Fatalf("address %s must be blocked", raw)
			}
		})
	}
	if !isPublicHTTPAddress(safeHTTPTestPublicIP) {
		t.Fatalf("public address %s was blocked", safeHTTPTestPublicIP)
	}
}

func TestSafeHTTPRejectsMixedDNSAnswersSSRF(t *testing.T) {
	lookup := func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{safeHTTPTestPublicIP, netip.MustParseAddr("10.0.0.1")}, nil
	}
	client, err := newSafeHTTPClientWithDeps([]string{"allowed.example"}, lookup, nil)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	if _, err := client.resolvePublic(context.Background(), "allowed.example"); !errors.Is(err, errUnsafeHTTPRequest) {
		t.Fatalf("mixed DNS answer must be blocked, got %v", err)
	}
}

func TestSafeHTTPDoesNotUseEnvironmentProxy(t *testing.T) {
	client, err := newSafeHTTPClientWithDeps(nil, nil, nil)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	transport := client.client.Transport.(*http.Transport)
	if transport.Proxy != nil {
		t.Fatalf("environment proxy must be disabled")
	}
}

func TestSafeHTTPRedirectSSRF(t *testing.T) {
	var blockedHits atomic.Int32
	var blockedTarget string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/redirect" {
			http.Redirect(response, request, blockedTarget, http.StatusFound)
			return
		}
		blockedHits.Add(1)
		response.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	port := mustURLPort(t, server.URL)
	blockedTarget = "http://" + net.JoinHostPort("blocked.example", port) + "/blocked"
	client := localSafeHTTPClient(t, server, []string{"allowed.example", "blocked.example"}, func(_ context.Context, _, host string) ([]netip.Addr, error) {
		if host == "blocked.example" {
			return []netip.Addr{netip.MustParseAddr("10.0.0.1")}, nil
		}
		return []netip.Addr{safeHTTPTestPublicIP}, nil
	}, nil)
	request, err := http.NewRequest(http.MethodGet, "http://"+net.JoinHostPort("allowed.example", port)+"/redirect", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	response, err := client.Do(request)
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
	if !errors.Is(err, errUnsafeHTTPRequest) {
		t.Fatalf("redirect must be blocked, got %v", err)
	}
	if blockedHits.Load() != 0 {
		t.Fatalf("blocked redirect reached the server")
	}
}

func TestHTTPNodeSSRFIsPermanent(t *testing.T) {
	ctx := &nodeExecContext{
		Ctx:   context.Background(),
		App:   &App{Cfg: &config.AppConfig{}},
		Node:  M{"config": M{"url": "http://blocked.example/path"}},
		State: newRunState(M{}),
	}
	_, err := httpRequestExecute(ctx)
	if err == nil {
		t.Fatalf("expected SSRF policy error")
	}
	category, retryable := classifyFailure(err, err.Error())
	if category != failureBusiness || retryable {
		t.Fatalf("category=%s, retryable=%v", category, retryable)
	}
}

func TestSafeHTTPRechecksDNSBeforeDialSSRF(t *testing.T) {
	var lookupCalls atomic.Int32
	var dialCalls atomic.Int32
	lookup := func(context.Context, string, string) ([]netip.Addr, error) {
		if lookupCalls.Add(1) == 1 {
			return []netip.Addr{safeHTTPTestPublicIP}, nil
		}
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}
	dial := func(context.Context, string, string) (net.Conn, error) {
		dialCalls.Add(1)
		return nil, errors.New("unexpected dial")
	}
	client, err := newSafeHTTPClientWithDeps([]string{"allowed.example"}, lookup, dial)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	request, err := http.NewRequest(http.MethodGet, "http://allowed.example/path", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	response, err := client.Do(request)
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
	if !errors.Is(err, errUnsafeHTTPRequest) {
		t.Fatalf("changed DNS answer must be blocked, got %v", err)
	}
	if lookupCalls.Load() < 2 || dialCalls.Load() != 0 {
		t.Fatalf("lookup calls=%d, dial calls=%d", lookupCalls.Load(), dialCalls.Load())
	}
}

func TestSafeHTTPStripsSensitiveHeaders(t *testing.T) {
	received := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		received <- request.Header.Clone()
		response.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	dialed := make(chan string, 1)
	client := localSafeHTTPClient(t, server, []string{"allowed.example"}, func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{safeHTTPTestPublicIP}, nil
	}, dialed)
	request, err := http.NewRequest(http.MethodGet, "http://"+net.JoinHostPort("allowed.example", mustURLPort(t, server.URL))+"/headers", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	for _, name := range []string{
		"Authorization",
		"Cookie",
		"Proxy-Authorization",
		"X-Api-Key",
		"XApiKey",
		"X-Access-Token",
		"X-Client-Secret",
		"X-Credential-ID",
	} {
		request.Header.Set(name, "must-not-leak")
	}
	request.Header.Set("X-Trace-ID", "keep")

	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("execute request: %v", err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()

	headers := <-received
	for _, name := range []string{"Authorization", "Cookie", "Proxy-Authorization", "X-Api-Key", "XApiKey", "X-Access-Token", "X-Client-Secret", "X-Credential-ID"} {
		if headers.Get(name) != "" {
			t.Fatalf("sensitive header %s leaked", name)
		}
	}
	if headers.Get("X-Trace-ID") != "keep" {
		t.Fatalf("safe header was removed")
	}
	dialHost, _, err := net.SplitHostPort(<-dialed)
	if err != nil || dialHost != safeHTTPTestPublicIP.String() {
		t.Fatalf("dial target=%q, error=%v", dialHost, err)
	}
}

func localSafeHTTPClient(
	t *testing.T,
	server *httptest.Server,
	allowed []string,
	lookup lookupNetIPFunc,
	dialed chan<- string,
) *safeHTTPClient {
	t.Helper()
	dialer := &net.Dialer{}
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		if dialed != nil {
			dialed <- address
		}
		return dialer.DialContext(ctx, network, server.Listener.Addr().String())
	}
	client, err := newSafeHTTPClientWithDeps(allowed, lookup, dial)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	return client
}

func mustURLPort(t *testing.T, rawURL string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	return parsed.Port()
}
