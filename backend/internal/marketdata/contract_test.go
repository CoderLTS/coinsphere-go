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
	"github.com/google/uuid"
)

const (
	spotID = "019c2f6d-7c00-7000-8000-000000000001"
	usdmID = "019c2f6d-7c00-7000-8000-000000000003"
)

func TestBinanceFixtureNormalization(t *testing.T) {
	start := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(3 * time.Minute)
	spotMetadata := normalizeOneMetadata(t, "instruments_spot.json", marketdata.MarketTypeSpot)
	assertMetadata(t, spotMetadata, marketdata.MarketTypeSpot, "BTCUSDT", "0.001")
	usdmMetadata := normalizeOneMetadata(t, "instruments_usd_m.json", marketdata.MarketTypeUSDM)
	assertMetadata(t, usdmMetadata, marketdata.MarketTypeUSDM, "BTCUSDT", "0.01")

	spot := instrumentFromMetadata(t, spotMetadata, spotID)
	if _, err := instrumentFromMetadataChecked(usdmMetadata, usdmID); err != nil {
		t.Fatalf("validate USD-M instrument: %v", err)
	}
	request := marketdata.CandlePageRequest{
		Instrument: spot,
		Interval:   marketdata.CandleInterval1m,
		StartTime:  start,
		EndTime:    end,
		Limit:      2,
	}
	firstPage, err := binance.NormalizeCandlePage(readFixture(t, "candles_1m_page_1.json"), request)
	if err != nil {
		t.Fatalf("normalize first candle page: %v", err)
	}
	if firstPage.NextCursor != "2026-08-01T00:02:00Z" {
		t.Fatalf("first page cursor = %q", firstPage.NextCursor)
	}
	request.Cursor = firstPage.NextCursor
	secondPage, err := binance.NormalizeCandlePage(readFixture(t, "candles_1m_page_2.json"), request)
	if err != nil {
		t.Fatalf("normalize second candle page: %v", err)
	}
	if secondPage.NextCursor != "" {
		t.Fatalf("final page cursor = %q", secondPage.NextCursor)
	}
	candles := append(append([]marketdata.Candle{}, firstPage.Candles...), secondPage.Candles...)
	assertHistoricalCandles(t, candles, start)
	assertDecimalJSON(t, firstPage.Candles[0])

	liveCandle, err := binance.NormalizeCandleEvent(readFixture(t, "candle_1m_event.json"), spot, marketdata.CandleInterval1m)
	if err != nil {
		t.Fatalf("normalize candle event: %v", err)
	}
	if liveCandle.IsClosed || !liveCandle.OpenTime.Equal(end) || !liveCandle.CloseTime.Equal(end.Add(time.Minute)) {
		t.Fatalf("live candle = %#v", liveCandle)
	}
	ticker, err := binance.NormalizeTickerEvent(readFixture(t, "ticker_event.json"), spot)
	if err != nil {
		t.Fatalf("normalize ticker event: %v", err)
	}
	if ticker.OccurredAt.Location() != time.UTC || !ticker.OccurredAt.Equal(start.Add(3*time.Minute+30*time.Second)) {
		t.Fatalf("ticker time = %s", ticker.OccurredAt)
	}
}

func TestBinanceMetadataOrderingAndDuplicates(t *testing.T) {
	payload := []byte(`{"symbols":[{"symbol":"ETHUSDT","status":"TRADING","baseAsset":"ETH","quoteAsset":"USDT","filters":[{"filterType":"PRICE_FILTER","tickSize":"0.1"},{"filterType":"LOT_SIZE","minQty":"0.001","stepSize":"0.001"},{"filterType":"MIN_NOTIONAL","minNotional":"5"}]},{"symbol":"BTCUSDT","status":"TRADING","baseAsset":"BTC","quoteAsset":"USDT","filters":[{"filterType":"PRICE_FILTER","tickSize":"0.1"},{"filterType":"LOT_SIZE","minQty":"0.001","stepSize":"0.001"},{"filterType":"MIN_NOTIONAL","minNotional":"5"}]}]}`)
	items, err := binance.NormalizeInstrumentSnapshot(payload, marketdata.MarketTypeSpot)
	if err != nil || len(items) != 2 || items[0].NativeSymbol != "BTCUSDT" || items[1].NativeSymbol != "ETHUSDT" {
		t.Fatalf("ordered metadata = %#v, err=%v", items, err)
	}
	duplicate := bytes.Replace(payload, []byte("ETHUSDT"), []byte("BTCUSDT"), 1)
	items, err = binance.NormalizeInstrumentSnapshot(duplicate, marketdata.MarketTypeSpot)
	assertProtocolError(t, err)
	if len(items) != 0 {
		t.Fatalf("duplicate metadata returned partial result %#v", items)
	}
}

func TestBinanceRejectsProtocolBoundaries(t *testing.T) {
	start := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	instrument := instrumentFromMetadata(t, normalizeOneMetadata(t, "instruments_spot.json", marketdata.MarketTypeSpot), spotID)
	request := marketdata.CandlePageRequest{Instrument: instrument, Interval: marketdata.CandleInterval1m, StartTime: start, EndTime: start.Add(3 * time.Minute), Limit: 2}
	fixture := readFixture(t, "candles_1m_page_1.json")
	for _, replacement := range [][]byte{[]byte("100.1"), []byte(`"1e2"`), []byte(`"0.1234567890123456789"`), []byte(`"123456789012345678901"`)} {
		page, err := binance.NormalizeCandlePage(bytes.Replace(fixture, []byte(`"100.1"`), replacement, 1), request)
		assertProtocolError(t, err)
		if len(page.Candles) != 0 || page.NextCursor != "" {
			t.Fatalf("invalid decimal returned partial page %#v", page)
		}
	}
	page, err := binance.NormalizeCandlePage(bytes.Replace(fixture, []byte(`"99.9"`), []byte(`"101"`), 1), request)
	assertProtocolError(t, err)
	if len(page.Candles) != 0 {
		t.Fatalf("invalid OHLC returned partial page %#v", page)
	}
	badTime := bytes.Replace(fixture, []byte("1785542459999"), []byte("1785542459998"), 1)
	_, err = binance.NormalizeCandlePage(badTime, request)
	assertProtocolError(t, err)
	badMetadata := bytes.Replace(readFixture(t, "instruments_spot.json"), []byte("TRADING"), []byte("UNKNOWN"), 1)
	_, err = binance.NormalizeInstrumentSnapshot(badMetadata, marketdata.MarketTypeSpot)
	assertProtocolError(t, err)
	_, err = binance.NormalizeInstrumentSnapshot([]byte(`{"symbols":[{"symbol":"BTCUSDT","status":"TRADING","baseAsset":"BTC","quoteAsset":"USDT","filters":[]}]}`), marketdata.MarketTypeSpot)
	assertProtocolError(t, err)
	for _, payload := range [][]byte{
		bytes.Replace(readFixture(t, "ticker_event.json"), []byte(`"102"`), []byte(`"0"`), 1),
		bytes.Replace(readFixture(t, "ticker_event.json"), []byte(`"101.9"`), []byte(`"102.2"`), 1),
		bytes.Replace(readFixture(t, "ticker_event.json"), []byte(`"a"`), []byte(`"ignored"`), 1),
	} {
		_, err = binance.NormalizeTickerEvent(payload, instrument)
		assertProtocolError(t, err)
	}
	_, err = binance.NormalizeCandleEvent([]byte(`{}`), instrument, marketdata.CandleInterval1m)
	assertProtocolError(t, err)
}

func TestSharedBoundaries(t *testing.T) {
	id, err := uuid.Parse(spotID)
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
}

func TestFixtureSubscriptionSemantics(t *testing.T) {
	metadata := normalizeOneMetadata(t, "instruments_spot.json", marketdata.MarketTypeSpot)
	instrument := instrumentFromMetadata(t, metadata, spotID)
	candle, err := binance.NormalizeCandleEvent(readFixture(t, "candle_1m_event.json"), instrument, marketdata.CandleInterval1m)
	if err != nil {
		t.Fatalf("normalize fixture candle: %v", err)
	}
	ticker, err := binance.NormalizeTickerEvent(readFixture(t, "ticker_event.json"), instrument)
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
}

func normalizeOneMetadata(t *testing.T, file string, marketType marketdata.MarketType) marketdata.InstrumentMetadata {
	t.Helper()
	items, err := binance.NormalizeInstrumentSnapshot(readFixture(t, file), marketType)
	if err != nil || len(items) != 1 {
		t.Fatalf("normalize %s: items=%#v err=%v", file, items, err)
	}
	return items[0]
}

func instrumentFromMetadata(t *testing.T, metadata marketdata.InstrumentMetadata, rawID string) marketdata.Instrument {
	t.Helper()
	instrument, err := instrumentFromMetadataChecked(metadata, rawID)
	if err != nil {
		t.Fatalf("validate fixture instrument: %v", err)
	}
	return instrument
}

func instrumentFromMetadataChecked(metadata marketdata.InstrumentMetadata, rawID string) (marketdata.Instrument, error) {
	id, err := uuid.Parse(rawID)
	if err != nil {
		return marketdata.Instrument{}, err
	}
	instrument := marketdata.Instrument{
		ID: id, Venue: metadata.Venue, MarketType: metadata.MarketType, NativeSymbol: metadata.NativeSymbol,
		BaseAsset: metadata.BaseAsset, QuoteAsset: metadata.QuoteAsset, Status: metadata.Status,
		PriceTick: metadata.PriceTick, QuantityStep: metadata.QuantityStep, MinQuantity: metadata.MinQuantity,
		MinNotional: metadata.MinNotional, UpdatedAt: metadata.UpdatedAt,
	}
	return instrument, marketdata.ValidateInstrument(instrument)
}

func assertMetadata(t *testing.T, metadata marketdata.InstrumentMetadata, marketType marketdata.MarketType, symbol, quantityStep string) {
	t.Helper()
	if metadata.Venue != marketdata.VenueBinance || metadata.MarketType != marketType || metadata.NativeSymbol != symbol || metadata.BaseAsset != "BTC" || metadata.QuoteAsset != "USDT" || metadata.Status != marketdata.InstrumentStatusTrading || metadata.PriceTick.String() != "0.1" || metadata.QuantityStep.String() != quantityStep || metadata.MinQuantity.Sign() <= 0 || metadata.MinNotional.Sign() <= 0 || metadata.UpdatedAt.Location() != time.UTC {
		t.Fatalf("metadata = %#v", metadata)
	}
}

func assertHistoricalCandles(t *testing.T, candles []marketdata.Candle, start time.Time) {
	t.Helper()
	expected := []struct{ open, high, low, close, volume string }{{"100.1", "101.2", "99.9", "100.8", "1.25"}, {"100.8", "102", "100.5", "101.5", "2.5"}, {"101.5", "101.8", "100.7", "101", "0.75"}}
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

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join("binance", "testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return payload
}

type fixtureSource struct {
	candle marketdata.Candle
	ticker marketdata.Ticker
}

var _ marketdata.MarketSource = fixtureSource{}

func (fixtureSource) ConfigurePublicAccess(map[marketdata.MarketType]string, string) error {
	return nil
}
func (fixtureSource) CheckConnectivity(context.Context, marketdata.MarketType) error { return nil }

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
