package marketdata_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"coinsphere/backend/internal/marketdata"
	"coinsphere/backend/internal/marketdata/binance"
	"coinsphere/backend/internal/marketdata/okx"
	"github.com/google/uuid"
)

type adapterFixture struct {
	name                 string
	venue                marketdata.Venue
	spotID               string
	perpetualID          string
	spotSymbol           string
	perpetualSymbol      string
	normalizeInstruments func([]byte, marketdata.MarketType) ([]marketdata.InstrumentMetadata, error)
	normalizePage        func([]byte, marketdata.CandlePageRequest) (marketdata.CandlePage, error)
	normalizeCandle      func([]byte, marketdata.Instrument, marketdata.CandleInterval) (marketdata.Candle, error)
	normalizeTicker      func([]byte, marketdata.Instrument) (marketdata.Ticker, error)
}

var adapterFixtures = []adapterFixture{
	{
		name:                 "binance",
		venue:                marketdata.VenueBinance,
		spotID:               "019c2f6d-7c00-7000-8000-000000000001",
		perpetualID:          "019c2f6d-7c00-7000-8000-000000000003",
		spotSymbol:           "BTCUSDT",
		perpetualSymbol:      "BTCUSDT",
		normalizeInstruments: binance.NormalizeInstrumentSnapshot,
		normalizePage:        binance.NormalizeCandlePage,
		normalizeCandle:      binance.NormalizeCandleEvent,
		normalizeTicker:      binance.NormalizeTickerEvent,
	},
	{
		name:                 "okx",
		venue:                marketdata.VenueOKX,
		spotID:               "019c2f6d-7c00-7000-8000-000000000002",
		perpetualID:          "019c2f6d-7c00-7000-8000-000000000004",
		spotSymbol:           "BTC-USDT",
		perpetualSymbol:      "BTC-USDT-SWAP",
		normalizeInstruments: okx.NormalizeInstrumentSnapshot,
		normalizePage:        okx.NormalizeCandlePage,
		normalizeCandle:      okx.NormalizeCandleEvent,
		normalizeTicker:      okx.NormalizeTickerEvent,
	},
}

func TestFixtureNormalization(t *testing.T) {
	start := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(3 * time.Minute)
	for _, fixture := range adapterFixtures {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			spotMetadata := normalizeOneMetadata(t, fixture, "instruments_spot.json", marketdata.MarketTypeSpot)
			assertMetadata(t, spotMetadata, fixture.venue, marketdata.MarketTypeSpot, fixture.spotSymbol, "0.001")
			perpetualMetadata := normalizeOneMetadata(t, fixture, "instruments_usdt_perpetual.json", marketdata.MarketTypeUSDTPerpetual)
			assertMetadata(t, perpetualMetadata, fixture.venue, marketdata.MarketTypeUSDTPerpetual, fixture.perpetualSymbol, "0.01")

			spot := instrumentFromMetadata(t, spotMetadata, fixture.spotID)
			_ = instrumentFromMetadata(t, perpetualMetadata, fixture.perpetualID)
			request := marketdata.CandlePageRequest{
				Instrument: spot,
				Interval:   marketdata.CandleInterval1m,
				StartTime:  start,
				EndTime:    end,
				Limit:      2,
			}
			firstPage, err := fixture.normalizePage(readFixture(t, fixture.name, "candles_1m_page_1.json"), request)
			if err != nil {
				t.Fatalf("normalize first candle page: %v", err)
			}
			if firstPage.NextCursor != "2026-08-01T00:02:00Z" {
				t.Fatalf("first page cursor = %q", firstPage.NextCursor)
			}
			request.Cursor = firstPage.NextCursor
			secondPage, err := fixture.normalizePage(readFixture(t, fixture.name, "candles_1m_page_2.json"), request)
			if err != nil {
				t.Fatalf("normalize second candle page: %v", err)
			}
			if secondPage.NextCursor != "" {
				t.Fatalf("final page cursor = %q", secondPage.NextCursor)
			}
			candles := append(append([]marketdata.Candle{}, firstPage.Candles...), secondPage.Candles...)
			assertHistoricalCandles(t, candles, start)
			assertDecimalJSON(t, firstPage.Candles[0])

			liveCandle, err := fixture.normalizeCandle(readFixture(t, fixture.name, "candle_1m_event.json"), spot, marketdata.CandleInterval1m)
			if err != nil {
				t.Fatalf("normalize candle event: %v", err)
			}
			if liveCandle.IsClosed || !liveCandle.OpenTime.Equal(end) || !liveCandle.CloseTime.Equal(end.Add(time.Minute)) {
				t.Fatalf("live candle = %#v", liveCandle)
			}
			if liveCandle.Open.String() != "101" || liveCandle.High.String() != "102.2" || liveCandle.Low.String() != "100.9" || liveCandle.Close.String() != "102" || liveCandle.BaseVolume.String() != "0.5" {
				t.Fatalf("live candle prices = %#v", liveCandle)
			}

			ticker, err := fixture.normalizeTicker(readFixture(t, fixture.name, "ticker_event.json"), spot)
			if err != nil {
				t.Fatalf("normalize ticker event: %v", err)
			}
			if ticker.OccurredAt.Location() != time.UTC || !ticker.OccurredAt.Equal(start.Add(3*time.Minute+30*time.Second)) {
				t.Fatalf("ticker time = %s", ticker.OccurredAt)
			}
			if ticker.LastPrice.String() != "102" || ticker.BestBidPrice.String() != "101.9" || ticker.BestAskPrice.String() != "102.1" {
				t.Fatalf("ticker prices = %#v", ticker)
			}
		})
	}
}

func TestMetadataSnapshotOrderingAndDuplicates(t *testing.T) {
	for _, fixture := range adapterFixtures {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			var orderedPayload []byte
			var want []string
			switch fixture.name {
			case "binance":
				orderedPayload = []byte(`{"symbols":[{"symbol":"ETHUSDT","status":"TRADING","baseAsset":"ETH","quoteAsset":"USDT","filters":[{"filterType":"PRICE_FILTER","tickSize":"0.1"},{"filterType":"LOT_SIZE","stepSize":"0.001"}]},{"symbol":"BTCUSDT","status":"TRADING","baseAsset":"BTC","quoteAsset":"USDT","filters":[{"filterType":"PRICE_FILTER","tickSize":"0.1"},{"filterType":"LOT_SIZE","stepSize":"0.001"}]}]}`)
				want = []string{"BTCUSDT", "ETHUSDT"}
			case "okx":
				orderedPayload = []byte(`{"code":"0","data":[{"instType":"SPOT","instId":"ETH-USDT","baseCcy":"ETH","quoteCcy":"USDT","state":"live","tickSz":"0.1","lotSz":"0.001"},{"instType":"SPOT","instId":"BTC-USDT","baseCcy":"BTC","quoteCcy":"USDT","state":"live","tickSz":"0.1","lotSz":"0.001"}]}`)
				want = []string{"BTC-USDT", "ETH-USDT"}
			default:
				t.Fatalf("unexpected fixture %q", fixture.name)
			}

			items, err := fixture.normalizeInstruments(orderedPayload, marketdata.MarketTypeSpot)
			if err != nil || len(items) != len(want) {
				t.Fatalf("normalize ordered metadata: items=%#v err=%v", items, err)
			}
			for index, symbol := range want {
				if items[index].NativeSymbol != symbol {
					t.Fatalf("metadata[%d] symbol = %q, want %q", index, items[index].NativeSymbol, symbol)
				}
			}

			duplicatePayload := bytes.Replace(orderedPayload, []byte(want[1]), []byte(want[0]), 1)
			items, err = fixture.normalizeInstruments(duplicatePayload, marketdata.MarketTypeSpot)
			assertProtocolError(t, err)
			if len(items) != 0 {
				t.Fatalf("duplicate metadata returned partial result %#v", items)
			}
		})
	}
}

func TestOKXPerpetualBaseVolume(t *testing.T) {
	fixture := adapterFixtures[1]
	metadata := normalizeOneMetadata(t, fixture, "instruments_usdt_perpetual.json", marketdata.MarketTypeUSDTPerpetual)
	instrument := instrumentFromMetadata(t, metadata, fixture.perpetualID)
	start := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	page, err := okx.NormalizeCandlePage([]byte(`{"code":"0","data":[["1785542400000","100.1","101.2","99.9","100.8","25","1.25","0","1"]]}`), marketdata.CandlePageRequest{
		Instrument: instrument,
		Interval:   marketdata.CandleInterval1m,
		StartTime:  start,
		EndTime:    start.Add(time.Minute),
		Limit:      1,
	})
	if err != nil || len(page.Candles) != 1 || page.Candles[0].BaseVolume.String() != "1.25" {
		t.Fatalf("normalize perpetual base volume: page=%#v err=%v", page, err)
	}
}

func TestAdaptersRejectProtocolBoundaries(t *testing.T) {
	start := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(3 * time.Minute)
	for _, fixture := range adapterFixtures {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			metadata := normalizeOneMetadata(t, fixture, "instruments_spot.json", marketdata.MarketTypeSpot)
			instrument := instrumentFromMetadata(t, metadata, fixture.spotID)
			request := marketdata.CandlePageRequest{Instrument: instrument, Interval: marketdata.CandleInterval1m, StartTime: start, EndTime: end, Limit: 2}
			pageFixture := readFixture(t, fixture.name, "candles_1m_page_1.json")

			for _, replacement := range [][]byte{
				[]byte("100.1"),
				[]byte(`"1e2"`),
				[]byte(`"0.1234567890123456789"`),
				[]byte(`"123456789012345678901"`),
			} {
				payload := bytes.Replace(pageFixture, []byte(`"100.1"`), replacement, 1)
				page, err := fixture.normalizePage(payload, request)
				assertProtocolError(t, err)
				if len(page.Candles) != 0 || page.NextCursor != "" {
					t.Fatalf("invalid decimal returned partial page %#v", page)
				}
			}

			badOHLC := bytes.Replace(pageFixture, []byte(`"99.9"`), []byte(`"101"`), 1)
			page, err := fixture.normalizePage(badOHLC, request)
			assertProtocolError(t, err)
			if len(page.Candles) != 0 || page.NextCursor != "" {
				t.Fatalf("invalid OHLC returned partial page %#v", page)
			}

			if fixture.name == "binance" {
				badTime := bytes.Replace(pageFixture, []byte("1785542459999"), []byte("1785542459998"), 1)
				page, err = fixture.normalizePage(badTime, request)
				assertProtocolError(t, err)
				if len(page.Candles) != 0 || page.NextCursor != "" {
					t.Fatalf("invalid Binance time returned partial page %#v", page)
				}
				badMetadata := bytes.Replace(readFixture(t, fixture.name, "instruments_spot.json"), []byte("TRADING"), []byte("UNKNOWN"), 1)
				_, err = fixture.normalizeInstruments(badMetadata, marketdata.MarketTypeSpot)
				assertProtocolError(t, err)
				_, err = fixture.normalizeInstruments([]byte(`{"symbols":[{"symbol":"BTCUSDT","status":"TRADING","baseAsset":"BTC","quoteAsset":"USDT","filters":[]}]}`), marketdata.MarketTypeSpot)
				assertProtocolError(t, err)
			} else {
				badTime := bytes.Replace(pageFixture, []byte("1785542400000"), []byte("1785542400001"), 1)
				page, err = fixture.normalizePage(badTime, request)
				assertProtocolError(t, err)
				if len(page.Candles) != 0 || page.NextCursor != "" {
					t.Fatalf("invalid OKX time returned partial page %#v", page)
				}
				badMetadata := bytes.Replace(readFixture(t, fixture.name, "instruments_spot.json"), []byte("live"), []byte("unknown"), 1)
				_, err = fixture.normalizeInstruments(badMetadata, marketdata.MarketTypeSpot)
				assertProtocolError(t, err)
				badPerpetual := bytes.Replace(readFixture(t, fixture.name, "instruments_usdt_perpetual.json"), []byte("linear"), []byte("inverse"), 1)
				_, err = fixture.normalizeInstruments(badPerpetual, marketdata.MarketTypeUSDTPerpetual)
				assertProtocolError(t, err)
				_, err = fixture.normalizeInstruments([]byte(`{"code":"0"}`), marketdata.MarketTypeSpot)
				assertProtocolError(t, err)
			}

			missingTickerField := []byte(`"a"`)
			if fixture.name == "okx" {
				missingTickerField = []byte(`"askPx"`)
			}
			for _, payload := range [][]byte{
				bytes.Replace(readFixture(t, fixture.name, "ticker_event.json"), []byte(`"102"`), []byte(`"0"`), 1),
				bytes.Replace(readFixture(t, fixture.name, "ticker_event.json"), []byte(`"101.9"`), []byte(`"102.2"`), 1),
				bytes.Replace(readFixture(t, fixture.name, "ticker_event.json"), missingTickerField, []byte(`"ignored"`), 1),
			} {
				_, err = fixture.normalizeTicker(payload, instrument)
				assertProtocolError(t, err)
			}
			_, err = fixture.normalizeCandle([]byte(`{}`), instrument, marketdata.CandleInterval1m)
			assertProtocolError(t, err)
		})
	}
}

func TestSharedBoundaries(t *testing.T) {
	id, err := uuid.Parse("019c2f6d-7c00-7000-8000-000000000001")
	if err != nil || marketdata.ValidateUUIDv7(id) != nil {
		t.Fatal("fixed UUIDv7 was rejected")
	}
	for _, text := range []string{"1e2", "-1", "1.", "0.1234567890123456789", "123456789012345678901"} {
		if _, err := marketdata.ParseDecimal(text); err == nil {
			t.Fatalf("invalid decimal %q was accepted", text)
		}
	}
	if _, err := marketdata.ParseDecimal("99999999999999999999.999999999999999999"); err != nil {
		t.Fatalf("numeric(38,18) boundary was rejected: %v", err)
	}

	rateLimited := marketdata.SourceError{Kind: marketdata.SourceErrorRateLimited, RetryAfter: time.Second, Err: errors.New("payload must stay hidden")}
	if err := marketdata.ValidateSourceError(rateLimited); err != nil || !rateLimited.Retryable() {
		t.Fatalf("rate limited error semantics failed: %v", err)
	}
	if strings.Contains(rateLimited.Error(), "payload") {
		t.Fatalf("source error exposed its wrapped value: %q", rateLimited.Error())
	}
	if err := marketdata.ValidateSourceError(marketdata.SourceError{Kind: marketdata.SourceErrorUnavailable, RetryAfter: time.Second}); err == nil {
		t.Fatal("unavailable retry-after was accepted")
	}
	if err := marketdata.ValidateSourceError(marketdata.SourceError{Kind: marketdata.SourceErrorRateLimited, RetryAfter: -time.Second}); err == nil {
		t.Fatal("negative retry-after was accepted")
	}
}

func TestFixtureSubscriptionSemantics(t *testing.T) {
	for _, fixture := range adapterFixtures {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			metadata := normalizeOneMetadata(t, fixture, "instruments_spot.json", marketdata.MarketTypeSpot)
			instrument := instrumentFromMetadata(t, metadata, fixture.spotID)
			candle, err := fixture.normalizeCandle(readFixture(t, fixture.name, "candle_1m_event.json"), instrument, marketdata.CandleInterval1m)
			if err != nil {
				t.Fatalf("normalize fixture candle: %v", err)
			}
			ticker, err := fixture.normalizeTicker(readFixture(t, fixture.name, "ticker_event.json"), instrument)
			if err != nil {
				t.Fatalf("normalize fixture ticker: %v", err)
			}
			source := fixtureSource{candle: candle, ticker: ticker}

			ctx, cancel := context.WithCancel(context.Background())
			candleCalls := 0
			err = source.SubscribeCandles(ctx, instrument, marketdata.CandleInterval1m, func(got marketdata.Candle) error {
				candleCalls++
				if got != candle {
					t.Fatal("fixture candle changed before handler")
				}
				cancel()
				return nil
			})
			if !errors.Is(err, context.Canceled) || candleCalls != 1 {
				t.Fatalf("canceled subscription = err:%v calls:%d", err, candleCalls)
			}

			handlerErr := errors.New("handler failed")
			tickerCalls := 0
			err = source.SubscribeTickers(context.Background(), instrument, func(marketdata.Ticker) error {
				tickerCalls++
				return handlerErr
			})
			if err != handlerErr || tickerCalls != 1 {
				t.Fatalf("handler subscription = err:%v calls:%d", err, tickerCalls)
			}
		})
	}
}

func normalizeOneMetadata(t *testing.T, fixture adapterFixture, file string, marketType marketdata.MarketType) marketdata.InstrumentMetadata {
	t.Helper()
	items, err := fixture.normalizeInstruments(readFixture(t, fixture.name, file), marketType)
	if err != nil || len(items) != 1 {
		t.Fatalf("normalize %s: items=%#v err=%v", file, items, err)
	}
	return items[0]
}

func instrumentFromMetadata(t *testing.T, metadata marketdata.InstrumentMetadata, rawID string) marketdata.Instrument {
	t.Helper()
	id, err := uuid.Parse(rawID)
	if err != nil {
		t.Fatalf("parse fixed UUID: %v", err)
	}
	instrument := marketdata.Instrument{
		ID:           id,
		Venue:        metadata.Venue,
		MarketType:   metadata.MarketType,
		NativeSymbol: metadata.NativeSymbol,
		BaseAsset:    metadata.BaseAsset,
		QuoteAsset:   metadata.QuoteAsset,
		Status:       metadata.Status,
		PriceTick:    metadata.PriceTick,
		QuantityStep: metadata.QuantityStep,
	}
	if err := marketdata.ValidateInstrument(instrument); err != nil {
		t.Fatalf("validate fixture instrument: %v", err)
	}
	return instrument
}

func assertMetadata(t *testing.T, metadata marketdata.InstrumentMetadata, venue marketdata.Venue, marketType marketdata.MarketType, nativeSymbol, quantityStep string) {
	t.Helper()
	if metadata.Venue != venue || metadata.MarketType != marketType || metadata.NativeSymbol != nativeSymbol || metadata.BaseAsset != "BTC" || metadata.QuoteAsset != "USDT" || metadata.Status != marketdata.InstrumentStatusTrading || metadata.PriceTick.String() != "0.1" || metadata.QuantityStep.String() != quantityStep {
		t.Fatalf("metadata = %#v", metadata)
	}
}

func assertHistoricalCandles(t *testing.T, candles []marketdata.Candle, start time.Time) {
	t.Helper()
	expected := []struct {
		open, high, low, close, volume string
	}{
		{"100.1", "101.2", "99.9", "100.8", "1.25"},
		{"100.8", "102", "100.5", "101.5", "2.5"},
		{"101.5", "101.8", "100.7", "101", "0.75"},
	}
	if len(candles) != len(expected) {
		t.Fatalf("candle count = %d", len(candles))
	}
	for index, want := range expected {
		candle := candles[index]
		openTime := start.Add(time.Duration(index) * time.Minute)
		if candle.OpenTime.Location() != time.UTC || candle.CloseTime.Location() != time.UTC || !candle.OpenTime.Equal(openTime) || !candle.CloseTime.Equal(openTime.Add(time.Minute)) || !candle.IsClosed {
			t.Fatalf("candle[%d] time = %#v", index, candle)
		}
		if candle.Open.String() != want.open || candle.High.String() != want.high || candle.Low.String() != want.low || candle.Close.String() != want.close || candle.BaseVolume.String() != want.volume {
			t.Fatalf("candle[%d] prices = %#v", index, candle)
		}
	}
}

func assertDecimalJSON(t *testing.T, candle marketdata.Candle) {
	t.Helper()
	payload, err := json.Marshal(candle)
	if err != nil {
		t.Fatalf("marshal candle: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatalf("decode candle JSON: %v", err)
	}
	for _, field := range []string{"open", "high", "low", "close", "baseVolume"} {
		if len(fields[field]) == 0 || fields[field][0] != '"' {
			t.Fatalf("decimal field %q is not a JSON string: %s", field, fields[field])
		}
	}
}

func assertProtocolError(t *testing.T, err error) {
	t.Helper()
	var sourceError *marketdata.SourceError
	if !errors.As(err, &sourceError) || sourceError.Kind != marketdata.SourceErrorProtocol || sourceError.Retryable() {
		t.Fatalf("error = %v, want non-retryable protocol error", err)
	}
}

func readFixture(t *testing.T, venue, name string) []byte {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join(venue, "testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s/%s: %v", venue, name, err)
	}
	return payload
}

// fixtureSource 只在测试中证明冻结订阅回调语义，不承担网络或回放能力。
type fixtureSource struct {
	candle marketdata.Candle
	ticker marketdata.Ticker
}

var _ marketdata.MarketSource = fixtureSource{}

func (source fixtureSource) SnapshotInstruments(ctx context.Context, marketType marketdata.MarketType) ([]marketdata.InstrumentMetadata, error) {
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	return nil, nil
}

func (source fixtureSource) FetchCandlePage(ctx context.Context, request marketdata.CandlePageRequest) (marketdata.CandlePage, error) {
	if err := context.Cause(ctx); err != nil {
		return marketdata.CandlePage{}, err
	}
	return marketdata.CandlePage{}, nil
}

func (source fixtureSource) SubscribeCandles(ctx context.Context, instrument marketdata.Instrument, interval marketdata.CandleInterval, handle marketdata.CandleHandler) error {
	return dispatchFixture(ctx, source.candle, handle)
}

func (source fixtureSource) SubscribeTickers(ctx context.Context, instrument marketdata.Instrument, handle marketdata.TickerHandler) error {
	return dispatchFixture(ctx, source.ticker, handle)
}

func dispatchFixture[T any](ctx context.Context, value T, handle func(T) error) error {
	if handle == nil {
		return &marketdata.SourceError{Kind: marketdata.SourceErrorInvalidRequest, Err: errors.New("nil handler")}
	}
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	default:
	}
	if err := handle(value); err != nil {
		return err
	}
	<-ctx.Done()
	return context.Cause(ctx)
}
