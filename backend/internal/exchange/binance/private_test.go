package binance

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"coinsphere/backend/internal/marketdata"
)

func TestPrivateClientDefaultsToTestnetHosts(t *testing.T) {
	client, err := NewPrivateClient(PrivateClientConfig{})
	if err != nil {
		t.Fatalf("create default private client: %v", err)
	}
	wantHosts := map[marketdata.MarketType]string{
		marketdata.MarketTypeSpot: "testnet.binance.vision",
		marketdata.MarketTypeUSDM: "testnet.binancefuture.com",
	}
	for marketType, wantHost := range wantHosts {
		endpoint := client.baseURLs[marketType]
		if endpoint.Scheme != "https" || endpoint.Host != wantHost {
			t.Fatalf("default %s endpoint = %s", marketType, endpoint.String())
		}
	}
}

func TestPrivateClientSignsAndRoutesAccountRequests(t *testing.T) {
	fixedNow := time.Date(2026, time.August, 9, 6, 7, 8, 900_000_000, time.UTC)
	apiKey := strings.Repeat("k", 32)
	apiSecret := strings.Repeat("s", 32)
	wantPaths := map[string]bool{"/api/v3/account": false, "/fapi/v3/account": false}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if _, ok := wantPaths[request.URL.Path]; !ok {
			t.Errorf("unexpected account path %q", request.URL.Path)
		} else {
			wantPaths[request.URL.Path] = true
		}
		if got := request.Header.Get("X-MBX-APIKEY"); got != apiKey {
			t.Errorf("API key header = %q", got)
		}
		query := request.URL.Query()
		signature := query.Get("signature")
		query.Del("signature")
		if query.Get("timestamp") != "1786255628900" || query.Get("recvWindow") != "5000" {
			t.Errorf("signed query = %v", query)
		}
		mac := hmac.New(sha256.New, []byte(apiSecret))
		_, _ = mac.Write([]byte(query.Encode()))
		if !hmac.Equal([]byte(signature), []byte(hex.EncodeToString(mac.Sum(nil)))) {
			t.Error("request signature is invalid")
		}
		if strings.Contains(request.URL.RequestURI(), apiKey) || strings.Contains(request.URL.RequestURI(), apiSecret) {
			t.Error("request URL exposed credentials")
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{}`))
	}))
	defer server.Close()

	client, err := NewPrivateClient(PrivateClientConfig{
		SpotBaseURL: server.URL, USDMBaseURL: server.URL, Now: func() time.Time { return fixedNow },
	})
	if err != nil {
		t.Fatalf("create private client: %v", err)
	}
	for _, marketType := range []marketdata.MarketType{marketdata.MarketTypeSpot, marketdata.MarketTypeUSDM} {
		if err := client.VerifyAccount(context.Background(), marketType, apiKey, apiSecret); err != nil {
			t.Fatalf("verify %s account: %v", marketType, err)
		}
	}
	for requestPath, called := range wantPaths {
		if !called {
			t.Errorf("account path %q was not called", requestPath)
		}
	}
}

func TestPrivateClientClassifiesErrorsWithoutResponseLeakage(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		wantKind  PrivateErrorKind
		wantRetry time.Duration
	}{
		{name: "authentication", status: http.StatusUnauthorized, body: `{"code":-1022,"msg":"sensitive-response-marker"}`, wantKind: PrivateErrorAuthentication},
		{name: "permission", status: http.StatusUnauthorized, body: `{"code":-2015,"msg":"sensitive-response-marker"}`, wantKind: PrivateErrorPermission},
		{name: "clock skew", status: http.StatusBadRequest, body: `{"code":-1021,"msg":"sensitive-response-marker"}`, wantKind: PrivateErrorClockSkew},
		{name: "rate limited", status: http.StatusTooManyRequests, body: `{"code":-1003,"msg":"sensitive-response-marker"}`, wantKind: PrivateErrorRateLimited, wantRetry: 2 * time.Second},
		{name: "unavailable", status: http.StatusServiceUnavailable, body: `{"code":-1000,"msg":"sensitive-response-marker"}`, wantKind: PrivateErrorUnavailable},
		{name: "server status wins", status: http.StatusServiceUnavailable, body: `{"code":-2015,"msg":"sensitive-response-marker"}`, wantKind: PrivateErrorUnavailable},
		{name: "waf rejection", status: http.StatusForbidden, body: `{"code":0,"msg":"sensitive-response-marker"}`, wantKind: PrivateErrorRejected},
		{name: "rejected", status: http.StatusBadRequest, body: `{"code":-1100,"msg":"sensitive-response-marker"}`, wantKind: PrivateErrorRejected},
	}
	apiKey := strings.Repeat("k", 32)
	apiSecret := strings.Repeat("s", 32)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.Header().Set("Retry-After", "2")
				response.WriteHeader(test.status)
				_, _ = response.Write([]byte(test.body))
			}))
			defer server.Close()
			client, err := NewPrivateClient(PrivateClientConfig{SpotBaseURL: server.URL, USDMBaseURL: server.URL})
			if err != nil {
				t.Fatalf("create private client: %v", err)
			}
			err = client.VerifyAccount(context.Background(), marketdata.MarketTypeSpot, apiKey, apiSecret)
			var privateErr *PrivateError
			if !errors.As(err, &privateErr) || privateErr.Kind != test.wantKind || privateErr.RetryAfter != test.wantRetry {
				t.Fatalf("private error = %#v, want kind=%q retry=%v", privateErr, test.wantKind, test.wantRetry)
			}
			for _, forbidden := range []string{apiKey, apiSecret, "sensitive-response-marker"} {
				if strings.Contains(err.Error(), forbidden) {
					t.Fatalf("error exposed private value %q", forbidden)
				}
			}
		})
	}
}

func TestPrivateClientDoesNotForwardCredentialsAcrossRedirects(t *testing.T) {
	var redirected atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirected.Store(true)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Location", target.URL)
		response.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	client, err := NewPrivateClient(PrivateClientConfig{SpotBaseURL: source.URL, USDMBaseURL: source.URL})
	if err != nil {
		t.Fatalf("create private client: %v", err)
	}
	err = client.VerifyAccount(context.Background(), marketdata.MarketTypeSpot, strings.Repeat("k", 32), strings.Repeat("s", 32))
	var privateErr *PrivateError
	if !errors.As(err, &privateErr) || privateErr.Kind != PrivateErrorRejected {
		t.Fatalf("redirect error = %#v", err)
	}
	if redirected.Load() {
		t.Fatal("private request followed a redirect")
	}
}

func TestPrivateBaseURLRejectsCredentialBearingURL(t *testing.T) {
	for _, raw := range []string{"", "ftp://example.com", "http://example.com", "https://api.binance.com", "https://user@testnet.binance.vision", "https://testnet.binance.vision?token=x", "https://testnet.binance.vision/#fragment"} {
		if _, err := privateBaseURL(raw, "testnet.binance.vision"); err == nil {
			t.Fatalf("privateBaseURL(%q) succeeded", raw)
		}
	}
	parsed, err := privateBaseURL("https://testnet.binance.vision/base", "testnet.binance.vision")
	if err != nil {
		t.Fatalf("valid private base URL: %v", err)
	}
	if parsed != (url.URL{Scheme: "https", Host: "testnet.binance.vision", Path: "/base"}) {
		t.Fatalf("parsed private base URL = %#v", parsed)
	}
}
