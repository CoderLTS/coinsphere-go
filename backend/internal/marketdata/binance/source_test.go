package binance

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"coinsphere/backend/internal/marketdata"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

func TestSourceRESTRoutingAndCursor(t *testing.T) {
	spotMetadata := testInstrument(t, marketdata.MarketTypeSpot, "019c2f6d-7c00-7000-8000-000000000001")
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		paths = append(paths, request.URL.Path)
		switch request.URL.Path {
		case "/api/v3/exchangeInfo":
			writeFixture(response, "instruments_spot.json")
		case "/fapi/v1/exchangeInfo":
			writeFixture(response, "instruments_usd_m.json")
		case "/api/v3/klines":
			query := request.URL.Query()
			if query.Get("symbol") != "BTCUSDT" || query.Get("interval") != "1m" || query.Get("limit") != "2" {
				t.Errorf("unexpected kline query: %s", request.URL.RawQuery)
			}
			switch query.Get("startTime") {
			case "1785542400000":
				if query.Get("endTime") != "1785542579999" {
					t.Errorf("first endTime = %q", query.Get("endTime"))
				}
				writeFixture(response, "candles_1m_page_1.json")
			case "1785542520000":
				writeFixture(response, "candles_1m_page_2.json")
			default:
				response.WriteHeader(http.StatusBadRequest)
			}
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	source, err := NewSource(SourceConfig{RESTBaseURL: server.URL, WebSocketBaseURL: "ws://127.0.0.1:1"})
	if err != nil {
		t.Fatalf("new source: %v", err)
	}
	if _, err := source.SnapshotInstruments(context.Background(), marketdata.MarketTypeSpot); err != nil {
		t.Fatalf("spot snapshot: %v", err)
	}
	if _, err := source.SnapshotInstruments(context.Background(), marketdata.MarketTypeUSDM); err != nil {
		t.Fatalf("USD-M snapshot: %v", err)
	}

	start := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	request := marketdata.CandlePageRequest{
		Instrument: spotMetadata,
		Interval:   marketdata.CandleInterval1m,
		StartTime:  start,
		EndTime:    start.Add(3 * time.Minute),
		Limit:      2,
	}
	page, err := source.FetchCandlePage(context.Background(), request)
	if err != nil {
		t.Fatalf("first candle page: %v", err)
	}
	if len(page.Candles) != 2 || page.NextCursor != "2026-08-01T00:02:00Z" {
		t.Fatalf("first page = %#v", page)
	}
	request.Cursor = page.NextCursor
	page, err = source.FetchCandlePage(context.Background(), request)
	if err != nil {
		t.Fatalf("second candle page: %v", err)
	}
	if len(page.Candles) != 1 || page.NextCursor != "" {
		t.Fatalf("second page = %#v", page)
	}

	if len(paths) != 4 || paths[0] != "/api/v3/exchangeInfo" || paths[1] != "/fapi/v1/exchangeInfo" || paths[2] != "/api/v3/klines" || paths[3] != "/api/v3/klines" {
		t.Fatalf("REST paths = %#v", paths)
	}
}

func TestSourceHTTPErrorClassification(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Retry-After", "2")
		response.WriteHeader(http.StatusTooManyRequests)
		_, _ = response.Write([]byte("private payload marker"))
	}))
	defer server.Close()
	source, err := NewSource(SourceConfig{RESTBaseURL: server.URL, WebSocketBaseURL: "ws://127.0.0.1:1"})
	if err != nil {
		t.Fatalf("new source: %v", err)
	}
	_, err = source.SnapshotInstruments(context.Background(), marketdata.MarketTypeSpot)
	var sourceErr *marketdata.SourceError
	if !errors.As(err, &sourceErr) || sourceErr.Kind != marketdata.SourceErrorRateLimited || sourceErr.RetryAfter != 2*time.Second {
		t.Fatalf("429 error = %#v", err)
	}
	if strings.Contains(err.Error(), "private payload marker") {
		t.Fatal("HTTP response body leaked through source error")
	}
}

func TestSourceDefaultResponseLimitCoversLargeInstrumentSnapshot(t *testing.T) {
	payload := bytes.Repeat([]byte{' '}, (16<<20)+1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write(payload)
	}))
	defer server.Close()

	source, err := NewSource(SourceConfig{RESTBaseURL: server.URL, WebSocketBaseURL: "ws://127.0.0.1:1"})
	if err != nil {
		t.Fatalf("new source: %v", err)
	}
	body, err := source.get(context.Background(), marketdata.MarketTypeSpot, "/exchangeInfo")
	if err != nil {
		t.Fatalf("large instrument snapshot: %v", err)
	}
	if len(body) != len(payload) {
		t.Fatalf("large instrument snapshot length = %d, want %d", len(body), len(payload))
	}
}

func TestSourceWebSocketReconnectCancelAndClosedDeduplication(t *testing.T) {
	instrument := testInstrument(t, marketdata.MarketTypeSpot, "019c2f6d-7c00-7000-8000-000000000001")
	openPayload := readFixture(t, "candle_1m_event.json")
	closedPayload := bytes.Replace(openPayload, []byte(`"x": false`), []byte(`"x": true`), 1)
	var connections atomic.Int32
	paths := make(chan string, 4)
	server := newWebSocketFixtureServer(t, func(connection *websocket.Conn, request *http.Request) {
		paths <- request.URL.Path
		connectionNumber := connections.Add(1)
		if connectionNumber == 1 {
			_ = connection.WriteMessage(websocket.TextMessage, openPayload)
			_ = connection.Close()
			return
		}
		_ = connection.WriteMessage(websocket.TextMessage, closedPayload)
		_ = connection.WriteMessage(websocket.TextMessage, closedPayload)
		_ = connection.SetReadDeadline(time.Now().Add(2 * time.Second))
		for {
			if _, _, err := connection.ReadMessage(); err != nil {
				return
			}
		}
	})
	defer server.Close()

	source, err := NewSource(SourceConfig{
		RESTBaseURL:         "http://127.0.0.1:1",
		WebSocketBaseURL:    websocketURL(server.URL),
		ReconnectBackoff:    time.Millisecond,
		MaxReconnectBackoff: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new source: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	received := make(chan marketdata.Candle, 4)
	done := make(chan error, 1)
	go func() {
		done <- source.SubscribeCandles(ctx, instrument, marketdata.CandleInterval1m, func(candle marketdata.Candle) error {
			received <- candle
			return nil
		})
	}()

	select {
	case candle := <-received:
		if candle.IsClosed {
			t.Fatal("first reconnect fixture unexpectedly closed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first kline")
	}
	select {
	case candle := <-received:
		if !candle.IsClosed {
			t.Fatal("reconnected fixture was not closed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for closed kline")
	}
	select {
	case duplicate := <-received:
		t.Fatalf("duplicate closed kline was dispatched: %#v", duplicate)
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("subscription cancellation error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subscription did not stop after cancellation")
	}
	if connections.Load() < 2 {
		t.Fatalf("WebSocket did not reconnect, connections=%d", connections.Load())
	}
	for expected := 0; expected < 2; expected++ {
		select {
		case got := <-paths:
			if got != "/ws/btcusdt@kline_1m" {
				t.Fatalf("WebSocket path = %q", got)
			}
		case <-time.After(time.Second):
			t.Fatal("missing WebSocket path")
		}
	}
}

func TestSourceWebSocketTickerCancellation(t *testing.T) {
	instrument := testInstrument(t, marketdata.MarketTypeSpot, "019c2f6d-7c00-7000-8000-000000000001")
	pathSeen := make(chan string, 1)
	server := newWebSocketFixtureServer(t, func(connection *websocket.Conn, request *http.Request) {
		pathSeen <- request.URL.Path
		_ = connection.WriteMessage(websocket.TextMessage, readFixture(t, "ticker_event.json"))
		_ = connection.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, _, _ = connection.ReadMessage()
	})
	defer server.Close()
	source, err := NewSource(SourceConfig{
		RESTBaseURL:      "http://127.0.0.1:1",
		WebSocketBaseURL: websocketURL(server.URL),
	})
	if err != nil {
		t.Fatalf("new source: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- source.SubscribeTickers(ctx, instrument, func(ticker marketdata.Ticker) error {
			if ticker.LastPrice.String() != "102" {
				t.Errorf("ticker = %#v", ticker)
			}
			cancel()
			return nil
		})
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ticker cancellation error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ticker subscription did not stop")
	}
	select {
	case path := <-pathSeen:
		if path != "/ws/btcusdt@ticker" {
			t.Fatalf("ticker WebSocket path = %q", path)
		}
	case <-time.After(time.Second):
		t.Fatal("ticker path was not observed")
	}
}

func testInstrument(t *testing.T, marketType marketdata.MarketType, rawID string) marketdata.Instrument {
	t.Helper()
	file := "instruments_spot.json"
	if marketType == marketdata.MarketTypeUSDM {
		file = "instruments_usd_m.json"
	}
	metadata, err := NormalizeInstrumentSnapshot(readFixture(t, file), marketType)
	if err != nil || len(metadata) != 1 {
		t.Fatalf("normalize instrument: metadata=%#v err=%v", metadata, err)
	}
	id, err := uuid.Parse(rawID)
	if err != nil {
		t.Fatalf("parse instrument ID: %v", err)
	}
	item := metadata[0]
	instrument := marketdata.Instrument{
		ID: id, Venue: item.Venue, MarketType: item.MarketType, NativeSymbol: item.NativeSymbol,
		BaseAsset: item.BaseAsset, QuoteAsset: item.QuoteAsset, Status: item.Status,
		PriceTick: item.PriceTick, QuantityStep: item.QuantityStep, MinQuantity: item.MinQuantity,
		MinNotional: item.MinNotional, UpdatedAt: item.UpdatedAt,
	}
	if err := marketdata.ValidateInstrument(instrument); err != nil {
		t.Fatalf("validate instrument: %v", err)
	}
	return instrument
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return body
}

func writeFixture(response http.ResponseWriter, name string) {
	response.Header().Set("Content-Type", "application/json")
	_, _ = response.Write(readFixtureForTest(name))
}

func readFixtureForTest(name string) []byte {
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		panic(err)
	}
	return body
}

func websocketURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	parsed.Scheme = "ws"
	return parsed.String()
}

func newWebSocketFixtureServer(t *testing.T, handler func(*websocket.Conn, *http.Request)) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	return httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		connection, err := upgrader.Upgrade(response, request, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer connection.Close()
		handler(connection, request)
	}))
}
