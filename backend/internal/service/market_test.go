package service

import (
	"errors"
	"testing"
	"time"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/marketdata"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestMarketDTOsUseDecimalStringsAndUTC(t *testing.T) {
	id := uuid.MustParse("019c2f6d-7c00-7000-8000-000000000001")
	localTime := time.Date(2026, time.August, 6, 16, 0, 0, 123, time.FixedZone("UTC+8", 8*60*60))
	symbol := serializeMarketSymbol(db.MarketInstrument{
		ID: id, Venue: "binance", Market: "spot", NativeSymbol: "BTCUSDT",
		PriceTick: decimal.RequireFromString("0.0100"), QuantityStep: decimal.RequireFromString("0.0010"),
		MinQuantity: decimal.RequireFromString("0.0010"), MinNotional: decimal.RequireFromString("5.0000"), UpdatedAt: localTime,
	})
	if symbol.ID != id.String() || symbol.PriceTick != "0.01" || symbol.QuantityStep != "0.001" || symbol.UpdatedAt != "2026-08-06T08:00:00.000000123Z" {
		t.Fatalf("symbol DTO = %#v", symbol)
	}
	candle := serializeMarketCandle(db.MarketCandle{
		InstrumentID: id, Interval: "1m", OpenTime: localTime, CloseTime: localTime.Add(time.Minute),
		Open: decimal.RequireFromString("100.10"), High: decimal.RequireFromString("101.20"),
		Low: decimal.RequireFromString("99.90"), Close: decimal.RequireFromString("100.80"),
		BaseVolume: decimal.RequireFromString("1.250"), IsClosed: true,
	})
	if candle.Open != "100.1" || candle.BaseVolume != "1.25" || candle.OpenTime != "2026-08-06T08:00:00.000000123Z" || !candle.IsClosed {
		t.Fatalf("candle DTO = %#v", candle)
	}
}

func TestMarketBoundaryValidationStopsBeforeDatabase(t *testing.T) {
	app := &App{}
	page := CursorPage{Limit: 50}
	for _, query := range []CandleListQuery{
		{Page: page, Interval: "1m"},
		{Page: page, InstrumentID: "019c2f6d-7c00-4000-8000-000000000001", Interval: "1m"},
		{Page: page, InstrumentID: "019c2f6d-7c00-7000-8000-000000000001", Interval: "2m"},
		{Page: page, InstrumentID: "019c2f6d-7c00-7000-8000-000000000001", Interval: "1m", StartTime: "2026-08-06T08:00:00+00:00"},
	} {
		if _, err := app.ListMarketCandles(t.Context(), query); !errors.Is(err, ErrInvalidMarketRequest) {
			t.Fatalf("query %#v error = %v", query, err)
		}
	}
	if err := app.DeleteWatchlistItem(t.Context(), 42, "019c2f6d-7c00-4000-8000-000000000001"); !errors.Is(err, ErrInvalidMarketRequest) {
		t.Fatalf("invalid watchlist ID error = %v", err)
	}
}

func TestTypedMarketCursorReusesOpaqueCursorContract(t *testing.T) {
	page := CursorPage{Limit: 1, scope: "market-symbols"}
	result := typedCursorResult([]string{"first"}, page, "019c2f6d-7c00-7000-8000-000000000001", true, 2)
	next, err := ParseCursorPage(result.NextCursor, 1, "market-symbols")
	if err != nil || next.After != "019c2f6d-7c00-7000-8000-000000000001" || !result.HasMore || result.Total != 2 {
		t.Fatalf("cursor result = %#v, next = %#v, err = %v", result, next, err)
	}
}

func TestMarketSyncSettingsAndManualTriggerBoundaries(t *testing.T) {
	if got, err := normalizeOptions([]string{"spot", "spot"}, map[string]bool{"spot": true, "usd_m": true}, "marketTypes"); err != nil || len(got) != 1 || got[0] != "spot" {
		t.Fatalf("normalize duplicate market types = %#v, err=%v", got, err)
	}
	for _, values := range [][]string{nil, {"future"}} {
		if _, err := normalizeOptions(values, map[string]bool{"spot": true, "usd_m": true}, "marketTypes"); !errors.Is(err, ErrInvalidMarketRequest) {
			t.Fatalf("invalid market types %v returned %v", values, err)
		}
	}
	for _, value := range []string{"", "http://api.binance.com", "https://example.com", "https://api.binance.com/path", "https://127.0.0.1"} {
		if _, err := normalizeBinanceRESTBaseURL(value, "spotRestBaseUrl"); !errors.Is(err, ErrInvalidMarketRequest) {
			t.Fatalf("invalid Binance REST base URL %q returned %v", value, err)
		}
	}
	if got, err := normalizeBinanceRESTBaseURL("https://DATA-API.BINANCE.VISION/", "spotRestBaseUrl"); err != nil || got != "https://data-api.binance.vision" {
		t.Fatalf("normalized Binance REST base URL = %q, err=%v", got, err)
	}
	for _, value := range []string{"https://proxy.internal:7890", "http://proxy.internal", "http://user:pass@proxy.internal:7890", "http://proxy.internal:7890/path"} {
		if _, err := normalizeMarketProxyURL(value, true); !errors.Is(err, ErrInvalidMarketRequest) {
			t.Fatalf("invalid market proxy URL %q returned %v", value, err)
		}
	}
	for value, expected := range map[string]string{
		"HTTP://PROXY.INTERNAL:7890/": "http://proxy.internal:7890",
		"socks5://[::1]:1080":         "socks5://[::1]:1080",
	} {
		if got, err := normalizeMarketProxyURL(value, true); err != nil || got != expected {
			t.Fatalf("normalized market proxy URL = %q, err=%v, want=%q", got, err, expected)
		}
	}
	if got, err := normalizeMarketProxyURL("", false); err != nil || got != "" {
		t.Fatalf("disabled empty market proxy URL = %q, err=%v", got, err)
	}
	if _, err := (&App{}).RunMarketMetadataSync(t.Context(), 1, ""); !errors.Is(err, ErrInvalidMarketRequest) {
		t.Fatalf("missing idempotency key returned %v", err)
	}
}

func TestMarketSourceFailureClassification(t *testing.T) {
	for _, item := range []struct {
		kind      marketdata.SourceErrorKind
		category  string
		retryable bool
	}{
		{marketdata.SourceErrorInvalidRequest, failureBusiness, false},
		{marketdata.SourceErrorProtocol, failureBusiness, false},
		{marketdata.SourceErrorRateLimited, failureInfraRetryable, true},
		{marketdata.SourceErrorUnavailable, failureInfraRetryable, true},
	} {
		err := marketNodeSourceError(&marketdata.SourceError{Kind: item.kind})
		category, retryable := classifyFailure(err, err.Error())
		if category != item.category || retryable != item.retryable {
			t.Fatalf("source error %s classified as %s/%t", item.kind, category, retryable)
		}
	}
}
