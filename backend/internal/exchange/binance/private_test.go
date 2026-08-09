package binance

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"coinsphere/backend/internal/marketdata"
	"github.com/shopspring/decimal"
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

func TestPrivateClientSnapshotsSpotAndUSDMAccounts(t *testing.T) {
	fixedNow := time.Date(2026, time.August, 9, 7, 8, 9, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-MBX-APIKEY") == "" || request.URL.Query().Get("signature") == "" {
			t.Error("snapshot request was not signed")
		}
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/v3/account":
			_, _ = response.Write([]byte(`{"canTrade":true,"balances":[{"asset":"BTC","free":"0.25","locked":"0.05"},{"asset":"USDT","free":"1000","locked":"0"},{"asset":"ZERO","free":"0","locked":"0"}]}`))
		case "/api/v3/openOrders":
			_, _ = response.Write([]byte(`[{"symbol":"BTCUSDT","orderId":41,"clientOrderId":"external-spot","side":"BUY","type":"LIMIT","status":"NEW","price":"50000","origQty":"0.01","executedQty":"0","stopPrice":"0"}]`))
		case "/fapi/v3/account":
			_, _ = response.Write([]byte(`{"canTrade":true,"assets":[{"asset":"USDT","walletBalance":"1200","availableBalance":"1100"},{"asset":"ZERO","walletBalance":"0","availableBalance":"0"}],"positions":[{"symbol":"BTCUSDT","positionSide":"BOTH","positionAmt":"-0.02","entryPrice":"51000","unrealizedProfit":"-4.5"},{"symbol":"ETHUSDT","positionSide":"BOTH","positionAmt":"0","entryPrice":"0","unrealizedProfit":"0"}]}`))
		case "/fapi/v1/openOrders":
			_, _ = response.Write([]byte(`[{"symbol":"BTCUSDT","orderId":42,"clientOrderId":"external-usdm","side":"SELL","type":"STOP_MARKET","status":"NEW","price":"0","origQty":"0.02","executedQty":"0","stopPrice":"52000"}]`))
		default:
			t.Errorf("unexpected snapshot path %q", request.URL.Path)
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := NewPrivateClient(PrivateClientConfig{
		SpotBaseURL: server.URL, USDMBaseURL: server.URL, Now: func() time.Time { return fixedNow },
	})
	if err != nil {
		t.Fatalf("create snapshot client: %v", err)
	}

	spot, err := client.SnapshotAccount(context.Background(), marketdata.MarketTypeSpot, "key", "secret")
	if err != nil {
		t.Fatalf("snapshot Spot account: %v", err)
	}
	if !spot.CanTrade || len(spot.Balances) != 2 || spot.Balances[0].Asset != "BTC" ||
		spot.Balances[0].Total.String() != "0.3" || spot.Balances[0].Available.String() != "0.25" ||
		len(spot.Positions) != 0 || len(spot.OpenOrders) != 1 || spot.OpenOrders[0].ExchangeOrderID != 41 ||
		!spot.ObservedAt.Equal(fixedNow) {
		t.Fatalf("Spot snapshot = %#v", spot)
	}

	usdm, err := client.SnapshotAccount(context.Background(), marketdata.MarketTypeUSDM, "key", "secret")
	if err != nil {
		t.Fatalf("snapshot USD-M account: %v", err)
	}
	if !usdm.CanTrade || len(usdm.Balances) != 1 || usdm.Balances[0].Total.String() != "1200" ||
		len(usdm.Positions) != 1 || usdm.Positions[0].PositionSide != "both" ||
		usdm.Positions[0].Quantity.String() != "-0.02" || usdm.Positions[0].UnrealizedPnL.String() != "-4.5" ||
		len(usdm.OpenOrders) != 1 || usdm.OpenOrders[0].OrderType != "stop_market" ||
		usdm.OpenOrders[0].StopPrice.String() != "52000" {
		t.Fatalf("USD-M snapshot = %#v", usdm)
	}
}

func TestPrivateClientPlacesAndQueriesDeterministicMarketOrders(t *testing.T) {
	fixedNow := time.Date(2026, time.August, 9, 8, 9, 10, 0, time.UTC)
	apiKey := strings.Repeat("k", 32)
	apiSecret := strings.Repeat("s", 32)
	requestCount := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requestCount[request.Method+" "+request.URL.Path]++
		if request.Header.Get("X-MBX-APIKEY") != apiKey {
			t.Error("order request used an unexpected API key")
		}
		query := request.URL.Query()
		signature := query.Get("signature")
		query.Del("signature")
		mac := hmac.New(sha256.New, []byte(apiSecret))
		_, _ = mac.Write([]byte(query.Encode()))
		if !hmac.Equal([]byte(signature), []byte(hex.EncodeToString(mac.Sum(nil)))) {
			t.Error("order request signature is invalid")
		}
		if query.Get("timestamp") != "1786262950000" || query.Get("recvWindow") != "5000" {
			t.Errorf("order signed query = %v", query)
		}
		if request.Method == http.MethodPost {
			wantReduceOnly := ""
			if request.URL.Path == "/fapi/v1/order" {
				wantReduceOnly = "true"
			}
			if query.Get("newClientOrderId") != "cs019d-order" || query.Get("side") != "BUY" ||
				query.Get("type") != "MARKET" || query.Get("quantity") != "0.01" ||
				query.Get("newOrderRespType") != "RESULT" || query.Get("origClientOrderId") != "" ||
				query.Get("reduceOnly") != wantReduceOnly {
				t.Errorf("market order query = %v", query)
			}
		} else if query.Get("origClientOrderId") != "cs019d-order" || query.Get("newClientOrderId") != "" {
			t.Errorf("order lookup query = %v", query)
		}
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/v3/order":
			_, _ = response.Write([]byte(`{"symbol":"BTCUSDT","orderId":41,"clientOrderId":"cs019d-order","side":"BUY","type":"MARKET","status":"FILLED","origQty":"0.01","executedQty":"0.01","cummulativeQuoteQty":"500"}`))
		case "/fapi/v1/order":
			_, _ = response.Write([]byte(`{"symbol":"BTCUSDT","orderId":42,"clientOrderId":"cs019d-order","side":"BUY","type":"MARKET","status":"FILLED","origQty":"0.01","executedQty":"0.01","cumQuote":"501","avgPrice":"50100"}`))
		default:
			t.Errorf("unexpected order path %q", request.URL.Path)
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := NewPrivateClient(PrivateClientConfig{
		SpotBaseURL: server.URL, USDMBaseURL: server.URL, Now: func() time.Time { return fixedNow },
	})
	if err != nil {
		t.Fatalf("create order client: %v", err)
	}

	for _, test := range []struct {
		marketType       marketdata.MarketType
		wantOrderID      int64
		wantAveragePrice string
		reduceOnly       bool
	}{
		{marketType: marketdata.MarketTypeSpot, wantOrderID: 41, wantAveragePrice: "50000"},
		{marketType: marketdata.MarketTypeUSDM, wantOrderID: 42, wantAveragePrice: "50100", reduceOnly: true},
	} {
		placed, err := client.PlaceMarketOrder(
			context.Background(), test.marketType, apiKey, apiSecret,
			"BTCUSDT", "cs019d-order", "buy", decimal.RequireFromString("0.01"), test.reduceOnly,
		)
		if err != nil || placed.ExchangeOrderID != test.wantOrderID || placed.Status != "filled" ||
			placed.AveragePrice.String() != test.wantAveragePrice || !placed.ObservedAt.Equal(fixedNow) {
			t.Fatalf("place %s market order = %#v, %v", test.marketType, placed, err)
		}
		queried, err := client.QueryOrder(
			context.Background(), test.marketType, apiKey, apiSecret, "BTCUSDT", "cs019d-order",
		)
		if err != nil || queried.Symbol != placed.Symbol || queried.ExchangeOrderID != placed.ExchangeOrderID ||
			queried.ClientOrderID != placed.ClientOrderID || queried.Side != placed.Side || queried.OrderType != placed.OrderType ||
			queried.Status != placed.Status || !queried.OriginalQuantity.Equal(placed.OriginalQuantity) ||
			!queried.ExecutedQuantity.Equal(placed.ExecutedQuantity) ||
			!queried.CumulativeQuoteQuantity.Equal(placed.CumulativeQuoteQuantity) ||
			!queried.AveragePrice.Equal(placed.AveragePrice) || !queried.ObservedAt.Equal(placed.ObservedAt) {
			t.Fatalf("query %s market order = %#v, %v; want %#v", test.marketType, queried, err, placed)
		}
	}
	for _, key := range []string{
		http.MethodPost + " /api/v3/order", http.MethodGet + " /api/v3/order",
		http.MethodPost + " /fapi/v1/order", http.MethodGet + " /fapi/v1/order",
	} {
		if requestCount[key] != 1 {
			t.Errorf("request count for %s = %d", key, requestCount[key])
		}
	}
}

func TestPrivateClientPlacesAndCancelsSpotAndUSDMProtectiveOrders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		if query.Get("signature") == "" || request.Header.Get("X-MBX-APIKEY") != "key" {
			t.Error("protective order request was not signed")
		}
		status := "NEW"
		if request.Method == http.MethodDelete {
			status = "CANCELED"
			if query.Get("origClientOrderId") != "csp019d-order" || query.Get("newClientOrderId") != "" {
				t.Errorf("protective cancellation query = %v", query)
			}
		} else if request.URL.Path == "/api/v3/order" {
			if query.Get("type") != "STOP_LOSS" || query.Get("quantity") != "0.01" ||
				query.Get("stopPrice") != "49000" || query.Get("closePosition") != "" ||
				query.Get("workingType") != "" || query.Get("reduceOnly") != "" {
				t.Errorf("Spot protective order query = %v", query)
			}
		} else {
			if query.Get("type") != "STOP_MARKET" || query.Get("quantity") != "" ||
				query.Get("stopPrice") != "49000" || query.Get("closePosition") != "true" ||
				query.Get("workingType") != "MARK_PRICE" || query.Get("reduceOnly") != "" {
				t.Errorf("USD-M protective order query = %v", query)
			}
		}
		response.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/api/v3/order" {
			_, _ = fmt.Fprintf(response, `{"symbol":"BTCUSDT","orderId":51,"clientOrderId":"csp019d-order","side":"SELL","type":"STOP_LOSS","status":%q,"origQty":"0.01","executedQty":"0","cummulativeQuoteQty":"0","stopPrice":"49000"}`, status)
			return
		}
		_, _ = fmt.Fprintf(response, `{"symbol":"BTCUSDT","orderId":52,"clientOrderId":"csp019d-order","side":"SELL","type":"STOP_MARKET","status":%q,"origQty":"0","executedQty":"0","cumQuote":"0","avgPrice":"0","stopPrice":"49000","closePosition":true,"reduceOnly":false,"workingType":"MARK_PRICE"}`, status)
	}))
	defer server.Close()
	client, err := NewPrivateClient(PrivateClientConfig{SpotBaseURL: server.URL, USDMBaseURL: server.URL})
	if err != nil {
		t.Fatalf("create protective order client: %v", err)
	}

	for _, test := range []struct {
		market   marketdata.MarketType
		quantity decimal.Decimal
		orderID  int64
	}{
		{market: marketdata.MarketTypeSpot, quantity: decimal.RequireFromString("0.01"), orderID: 51},
		{market: marketdata.MarketTypeUSDM, quantity: decimal.Zero, orderID: 52},
	} {
		placed, err := client.PlaceProtectiveOrder(
			context.Background(), test.market, "key", "secret", "BTCUSDT", "csp019d-order", "sell",
			test.quantity, decimal.NewFromInt(49_000),
		)
		if err != nil || placed.ExchangeOrderID != test.orderID || placed.Status != "new" ||
			!placed.StopPrice.Equal(decimal.NewFromInt(49_000)) {
			t.Fatalf("place %s protective order = %#v, %v", test.market, placed, err)
		}
		canceled, err := client.CancelOrder(
			context.Background(), test.market, "key", "secret", "BTCUSDT", "csp019d-order",
		)
		if err != nil || canceled.ExchangeOrderID != test.orderID || canceled.Status != "canceled" {
			t.Fatalf("cancel %s protective order = %#v, %v", test.market, canceled, err)
		}
	}
}

func TestPrivateClientClassifiesMissingOrderForQueryFirstRecovery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusBadRequest)
		_, _ = response.Write([]byte(`{"code":-2013,"msg":"sensitive-response-marker"}`))
	}))
	defer server.Close()
	client, err := NewPrivateClient(PrivateClientConfig{SpotBaseURL: server.URL, USDMBaseURL: server.URL})
	if err != nil {
		t.Fatalf("create missing-order client: %v", err)
	}
	_, err = client.QueryOrder(context.Background(), marketdata.MarketTypeSpot, "key", "secret", "BTCUSDT", "cs019d-order")
	var privateErr *PrivateError
	if !errors.As(err, &privateErr) || privateErr.Kind != PrivateErrorNotFound {
		t.Fatalf("missing-order error = %#v", err)
	}
	if strings.Contains(err.Error(), "sensitive-response-marker") {
		t.Fatal("missing-order error exposed response data")
	}
}

func TestPrivateClientRejectsInconsistentOrderFills(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{
			name: "quote without fill",
			body: `{"symbol":"BTCUSDT","orderId":41,"clientOrderId":"cs019d-order","side":"BUY","type":"MARKET","status":"NEW","origQty":"0.01","executedQty":"0","cummulativeQuoteQty":"1"}`,
		},
		{
			name: "fill without quote",
			body: `{"symbol":"BTCUSDT","orderId":41,"clientOrderId":"cs019d-order","side":"BUY","type":"MARKET","status":"FILLED","origQty":"0.01","executedQty":"0.01","cummulativeQuoteQty":"0","avgPrice":"50000"}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				_, _ = response.Write([]byte(test.body))
			}))
			defer server.Close()
			client, err := NewPrivateClient(PrivateClientConfig{SpotBaseURL: server.URL, USDMBaseURL: server.URL})
			if err != nil {
				t.Fatalf("create inconsistent-order client: %v", err)
			}
			_, err = client.QueryOrder(
				context.Background(), marketdata.MarketTypeSpot, "key", "secret", "BTCUSDT", "cs019d-order",
			)
			var privateErr *PrivateError
			if !errors.As(err, &privateErr) || privateErr.Kind != PrivateErrorProtocol {
				t.Fatalf("inconsistent order error = %#v", err)
			}
		})
	}
}

func TestPrivateClientRejectsMalformedSnapshotWithoutLeakage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "/account") {
			_, _ = response.Write([]byte(`{"canTrade":true,"balances":[{"asset":"BTC","free":"sensitive-response-marker","locked":"0"}]}`))
			return
		}
		_, _ = response.Write([]byte(`[]`))
	}))
	defer server.Close()
	client, err := NewPrivateClient(PrivateClientConfig{SpotBaseURL: server.URL, USDMBaseURL: server.URL})
	if err != nil {
		t.Fatalf("create malformed snapshot client: %v", err)
	}
	_, err = client.SnapshotAccount(context.Background(), marketdata.MarketTypeSpot, "key", "secret")
	var privateErr *PrivateError
	if !errors.As(err, &privateErr) || privateErr.Kind != PrivateErrorProtocol {
		t.Fatalf("malformed snapshot error = %#v", err)
	}
	if strings.Contains(err.Error(), "sensitive-response-marker") {
		t.Fatal("snapshot protocol error exposed response data")
	}
}

func TestPrivateDecimalEnforcesStorageBoundary(t *testing.T) {
	tests := []struct {
		name          string
		value         string
		allowNegative bool
		want          string
		wantError     bool
	}{
		{name: "positive boundary", value: "99999999999999999999.999999999999999999", want: "99999999999999999999.999999999999999999"},
		{name: "negative boundary", value: "-99999999999999999999.999999999999999999", allowNegative: true, want: "-99999999999999999999.999999999999999999"},
		{name: "negative rejected", value: "-1", wantError: true},
		{name: "integer overflow", value: "100000000000000000000", wantError: true},
		{name: "fraction overflow", value: "0.0000000000000000001", wantError: true},
		{name: "exponent rejected", value: "1e2", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value, err := privateDecimal(test.value, test.allowNegative)
			if test.wantError {
				if err == nil {
					t.Fatalf("privateDecimal(%q) unexpectedly returned %s", test.value, value)
				}
				return
			}
			if err != nil || value.String() != test.want {
				t.Fatalf("privateDecimal(%q) = %s, %v; want %s", test.value, value, err, test.want)
			}
		})
	}
}

func TestPrivateSnapshotRejectsBalanceTotalOutsideStorageBoundary(t *testing.T) {
	_, err := parseAccountSnapshot(
		marketdata.MarketTypeSpot,
		[]byte(`{"canTrade":true,"balances":[{"asset":"USDT","free":"99999999999999999999","locked":"1"}]}`),
		[]byte(`[]`),
	)
	if err == nil {
		t.Fatal("Spot snapshot accepted a balance total outside numeric(38,18)")
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
