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

func TestPrivateClientLiveModeOnlyRoutesSpot(t *testing.T) {
	if _, err := NewPrivateClient(PrivateClientConfig{Environment: "live"}); err == nil {
		t.Fatal("Live private client accepted an implicit market scope")
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v3/account" {
			t.Errorf("unexpected Live path %q", request.URL.Path)
		}
		_, _ = response.Write([]byte(`{}`))
	}))
	defer server.Close()

	client, err := NewPrivateClient(PrivateClientConfig{
		Environment: "live", Market: marketdata.MarketTypeSpot, SpotBaseURL: server.URL,
	})
	if err != nil {
		t.Fatalf("create Live private client: %v", err)
	}
	if err := client.VerifyAccount(context.Background(), marketdata.MarketTypeSpot, "key", "secret"); err != nil {
		t.Fatalf("verify Live Spot account: %v", err)
	}
	var privateErr *PrivateError
	if err := client.VerifyAccount(context.Background(), marketdata.MarketTypeUSDM, "key", "secret"); !errors.As(err, &privateErr) || privateErr.Kind != PrivateErrorAuthentication {
		t.Fatalf("USD-M Live error = %v", err)
	}
}

func TestPrivateClientLiveUSDMScopeAndPositionRisk(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/fapi/v3/account":
			_, _ = response.Write([]byte(`{"canTrade":true,"assets":[{"asset":"USDT","walletBalance":"1000","availableBalance":"900"}],"positions":[]}`))
		case "/fapi/v1/openOrders":
			_, _ = response.Write([]byte(`[]`))
		case "/fapi/v3/positionRisk":
			_, _ = response.Write([]byte(`[{"symbol":"BTCUSDT","positionSide":"BOTH","positionAmt":"0.01","entryPrice":"49000","markPrice":"50000","liquidationPrice":"40000","unRealizedProfit":"10","leverage":"2","marginType":"isolated"}]`))
		default:
			t.Errorf("unexpected USD-M Live path %q", request.URL.Path)
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := NewPrivateClient(PrivateClientConfig{
		Environment: "live", Market: marketdata.MarketTypeUSDM, USDMBaseURL: server.URL,
	})
	if err != nil {
		t.Fatalf("create USD-M Live private client: %v", err)
	}
	snapshot, err := client.SnapshotAccount(
		context.Background(), marketdata.MarketTypeUSDM, "key", "secret", "BTCUSDT",
	)
	if err != nil {
		t.Fatalf("snapshot USD-M Live account: %v", err)
	}
	if len(snapshot.Positions) != 1 || snapshot.Positions[0].Leverage != 2 ||
		!snapshot.Positions[0].Isolated || snapshot.Positions[0].MarkPrice.String() != "50000" ||
		snapshot.Positions[0].LiquidationPrice.String() != "40000" ||
		snapshot.Positions[0].LiquidationDistanceRatio.String() != "0.2" {
		t.Fatalf("USD-M Live position risk = %#v", snapshot.Positions)
	}
	var privateErr *PrivateError
	if err := client.VerifyAccount(context.Background(), marketdata.MarketTypeSpot, "key", "secret"); !errors.As(err, &privateErr) || privateErr.Kind != PrivateErrorAuthentication {
		t.Fatalf("Spot request through USD-M Live client returned %v", err)
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
			_, _ = response.Write([]byte(`[{"symbol":"BTCUSDT","orderId":42,"clientOrderId":"external-usdm","side":"SELL","type":"STOP_MARKET","status":"NEW","price":"0","origQty":"0","executedQty":"0","stopPrice":"52000","closePosition":true,"reduceOnly":false,"workingType":"MARK_PRICE"}]`))
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
		usdm.OpenOrders[0].StopPrice.String() != "52000" || !usdm.OpenOrders[0].OriginalQuantity.IsZero() ||
		!usdm.OpenOrders[0].ClosePosition || usdm.OpenOrders[0].ReduceOnly || usdm.OpenOrders[0].WorkingType != "mark_price" {
		t.Fatalf("USD-M snapshot = %#v", usdm)
	}
}

func TestPrivateSnapshotParsesOpenOrderFillValues(t *testing.T) {
	snapshot, err := parseAccountSnapshot(
		marketdata.MarketTypeSpot,
		[]byte(`{"canTrade":true,"balances":[{"asset":"USDT","free":"1000","locked":"0"}]}`),
		[]byte(`[{"symbol":"BTCUSDT","orderId":41,"clientOrderId":"cs019d-order","side":"BUY","type":"LIMIT","status":"PARTIALLY_FILLED","price":"50000","origQty":"0.01","executedQty":"0.005","cummulativeQuoteQty":"250","avgPrice":"50000","stopPrice":"0"}]`),
	)
	if err != nil {
		t.Fatalf("parse open order fill values: %v", err)
	}
	if len(snapshot.OpenOrders) != 1 ||
		!snapshot.OpenOrders[0].CumulativeQuoteQuantity.Equal(decimal.NewFromInt(250)) ||
		!snapshot.OpenOrders[0].AveragePrice.Equal(decimal.NewFromInt(50_000)) {
		t.Fatalf("open order fill values = %#v", snapshot.OpenOrders)
	}
}

func TestPrivateTradeParsersNormalizeSpotAndUSDM(t *testing.T) {
	spot, err := parseAccountTrades(marketdata.MarketTypeSpot, []byte(`[
        {"symbol":"BTCUSDT","id":12,"orderId":7,"price":"101.25","qty":"0.2","quoteQty":"20.25","commission":"0.0002","commissionAsset":"BTC","time":1786255629000,"isBuyer":false,"isMaker":true},
        {"symbol":"BTCUSDT","id":11,"orderId":7,"price":"100","qty":"0.1","quoteQty":"10","commission":"0.01","commissionAsset":"USDT","time":1786255628000,"isBuyer":true,"isMaker":false}
    ]`))
	if err != nil || len(spot) != 2 {
		t.Fatalf("parse Spot trades: %v %#v", err, spot)
	}
	if spot[0].ExchangeTradeID != 11 || spot[0].Side != "buy" || spot[0].PositionSide != "both" ||
		!spot[0].Quantity.Equal(decimal.RequireFromString("0.1")) || !spot[0].OccurredAt.Equal(time.UnixMilli(1786255628000).UTC()) ||
		spot[1].Side != "sell" || !spot[1].Maker {
		t.Fatalf("normalized Spot trades = %#v", spot)
	}

	usdm, err := parseAccountTrades(marketdata.MarketTypeUSDM, []byte(`[
        {"symbol":"BTCUSDT","id":21,"orderId":8,"side":"SELL","positionSide":"SHORT","price":"99.5","qty":"2","quoteQty":"199","commission":"0.05","commissionAsset":"USDT","realizedPnl":"-1.25","buyer":false,"maker":true,"time":1786255629000}
    ]`))
	if err != nil || len(usdm) != 1 {
		t.Fatalf("parse USD-M trades: %v %#v", err, usdm)
	}
	if usdm[0].Side != "sell" || usdm[0].PositionSide != "short" || !usdm[0].RealizedPnL.Equal(decimal.RequireFromString("-1.25")) ||
		!usdm[0].Commission.Equal(decimal.RequireFromString("0.05")) {
		t.Fatalf("normalized USD-M trade = %#v", usdm[0])
	}
}

func TestPrivateTradeParsersRejectMalformedRows(t *testing.T) {
	for _, test := range []struct {
		name   string
		market marketdata.MarketType
		body   string
	}{
		{name: "null Spot response", market: marketdata.MarketTypeSpot, body: "null"},
		{name: "duplicate Spot trade", market: marketdata.MarketTypeSpot, body: `[
            {"symbol":"BTCUSDT","id":1,"orderId":2,"price":"1","qty":"1","quoteQty":"1","commission":"0","commissionAsset":"BTC","time":1,"isBuyer":true,"isMaker":false},
            {"symbol":"BTCUSDT","id":1,"orderId":2,"price":"1","qty":"1","quoteQty":"1","commission":"0","commissionAsset":"BTC","time":2,"isBuyer":true,"isMaker":false}
        ]`},
		{name: "boolean external ID", market: marketdata.MarketTypeUSDM, body: `[{"symbol":"BTCUSDT","id":true,"orderId":2,"side":"BUY","positionSide":"BOTH","price":"1","qty":"1","quoteQty":"1","commission":"0","commissionAsset":"USDT","realizedPnl":"0","buyer":true,"maker":false,"time":1}]`},
		{name: "negative quantity", market: marketdata.MarketTypeUSDM, body: `[{"symbol":"BTCUSDT","id":1,"orderId":2,"side":"BUY","positionSide":"BOTH","price":"1","qty":"-1","quoteQty":"1","commission":"0","commissionAsset":"USDT","realizedPnl":"0","buyer":true,"maker":false,"time":1}]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseAccountTrades(test.market, []byte(test.body)); err == nil {
				t.Fatal("malformed trade response was accepted")
			}
		})
	}
	if _, err := parseFundingIncome([]byte(`[{"symbol":"BTCUSDT","incomeType":"COMMISSION","asset":"USDT","income":"1","tranId":1,"time":1}]`)); err == nil {
		t.Fatal("non-funding income was accepted")
	}
}

func TestPrivateClientQueriesTradesAndFundingIncome(t *testing.T) {
	fixedNow := time.UnixMilli(1786255628000).UTC()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/v3/myTrades":
			if request.URL.Query().Get("symbol") != "BTCUSDT" || request.URL.Query().Get("orderId") != "7" || request.URL.Query().Get("limit") != "1000" {
				t.Errorf("Spot trade query = %v", request.URL.Query())
			}
			_, _ = response.Write([]byte(`[{"symbol":"BTCUSDT","id":1,"orderId":7,"price":"100","qty":"1","quoteQty":"100","commission":"0.1","commissionAsset":"USDT","time":1786255627000,"isBuyer":true,"isMaker":false}]`))
		case "/fapi/v1/userTrades":
			if request.URL.Query().Get("symbol") != "BTCUSDT" || request.URL.Query().Get("orderId") != "8" {
				t.Errorf("USD-M trade query = %v", request.URL.Query())
			}
			_, _ = response.Write([]byte(`[{"symbol":"BTCUSDT","id":2,"orderId":8,"side":"SELL","positionSide":"BOTH","price":"100","qty":"1","quoteQty":"100","commission":"0.1","commissionAsset":"USDT","realizedPnl":"-2","buyer":false,"maker":true,"time":1786255627000}]`))
		case "/fapi/v1/income":
			query := request.URL.Query()
			if query.Get("incomeType") != "FUNDING_FEE" || query.Get("symbol") != "BTCUSDT" || query.Get("startTime") != "1786252028000" || query.Get("endTime") != "1786255628000" {
				t.Errorf("funding query = %v", query)
			}
			_, _ = response.Write([]byte(`[{"symbol":"BTCUSDT","incomeType":"FUNDING_FEE","asset":"USDT","income":"-0.25","tranId":33,"time":1786255627000}]`))
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := NewPrivateClient(PrivateClientConfig{SpotBaseURL: server.URL, USDMBaseURL: server.URL, Now: func() time.Time { return fixedNow }})
	if err != nil {
		t.Fatalf("create private client: %v", err)
	}
	if trades, err := client.QueryOrderTrades(context.Background(), marketdata.MarketTypeSpot, "key", "secret", "BTCUSDT", 7); err != nil || len(trades) != 1 {
		t.Fatalf("query Spot trades: %v %#v", err, trades)
	}
	if trades, err := client.QueryOrderTrades(context.Background(), marketdata.MarketTypeUSDM, "key", "secret", "BTCUSDT", 8); err != nil || len(trades) != 1 {
		t.Fatalf("query USD-M trades: %v %#v", err, trades)
	}
	if funding, err := client.QueryFundingIncome(context.Background(), "key", "secret", "BTCUSDT", fixedNow.Add(-time.Hour), fixedNow); err != nil || len(funding) != 1 || !funding[0].Amount.Equal(decimal.RequireFromString("-0.25")) {
		t.Fatalf("query funding income: %v %#v", err, funding)
	}
	if _, err := client.QueryOrderTrades(context.Background(), marketdata.MarketType("unknown"), "key", "secret", "BTCUSDT", 7); err == nil {
		t.Fatal("unsupported trade market was accepted")
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
