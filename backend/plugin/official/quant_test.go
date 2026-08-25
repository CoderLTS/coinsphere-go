package official

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"coinsphere/backend/plugin/sdk"
	"github.com/shopspring/decimal"
)

func TestSMAStrategyIsDeterministicAndHonorsCancellation(t *testing.T) {
	strategy := smaCrossoverStrategy{}
	candles := quantSDKCandles(testQuantCandles(6))
	request := sdk.EvaluateRequest{
		Market: "spot", Instrument: "BTCUSDT", Interval: "1h", Candles: candles[1:6],
		Parameters: json.RawMessage(`{"fastPeriod":2,"slowPeriod":5}`), EvaluatedAt: candles[5].CloseTime,
	}
	if err := validateStrategyParameters(strategy.Descriptor(), request.Parameters); err != nil {
		t.Fatalf("integer strategy parameters were rejected: %v", err)
	}
	if err := validateStrategyParameters(strategy.Descriptor(), json.RawMessage(`{"fastPeriod":2.5,"slowPeriod":5}`)); err == nil {
		t.Fatal("fractional strategy period was accepted")
	}
	live, err := strategy.Evaluate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	backtest, err := strategy.Evaluate(context.Background(), request)
	if err != nil || !live.Equal(backtest) || !live.Equal(quantOne) {
		t.Fatalf("live=%s backtest=%s err=%v", live, backtest, err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := strategy.Evaluate(cancelled, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled strategy returned %v", err)
	}
}

func TestQuantBacktestUsesNextOpenAndSkipsFinalSignal(t *testing.T) {
	candles := testQuantCandles(4)
	config := quantBacktestConfig{
		quantStrategyConfig: quantStrategyConfig{
			quantSeriesConfig: quantSeriesConfig{Market: "spot", Instrument: "BTCUSDT", Interval: "1h"},
			Parameters:        json.RawMessage(`{}`),
		},
		InitialCapital: decimal.RequireFromString("100"),
		FeeRate:        decimal.RequireFromString("0.01"),
		SlippageRate:   decimal.RequireFromString("0.10"),
	}
	result, err := simulateQuantBacktest(context.Background(), fixedTargetStrategy{}, fixedTargetStrategy{}.Descriptor(), config, candles, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Points) != len(candles)-1 {
		t.Fatalf("points=%d, want %d", len(result.Points), len(candles)-1)
	}
	first := result.Points[0]
	if first.EvaluatedAt != candles[0].CloseTime.Format(time.RFC3339Nano) ||
		first.ExecutionOpenTime != candles[1].OpenTime.Format(time.RFC3339Nano) || first.ExecutionPrice != "12.1" {
		t.Fatalf("first point = %#v", first)
	}
	if result.Points[len(result.Points)-1].EvaluatedAt != candles[len(candles)-2].CloseTime.Format(time.RFC3339Nano) {
		t.Fatal("the final candle produced a signal without a next open")
	}
	if result.TotalFees.Sign() <= 0 || result.TradeCount == 0 {
		t.Fatalf("fees=%s trades=%d", result.TotalFees, result.TradeCount)
	}
}

func TestQuantBacktestRejectsInsufficientLookbackAndGaps(t *testing.T) {
	config := quantBacktestConfig{
		quantStrategyConfig: quantStrategyConfig{
			quantSeriesConfig: quantSeriesConfig{Market: "spot", Instrument: "BTCUSDT", Interval: "1h"},
			Parameters:        json.RawMessage(`{}`),
		},
		InitialCapital: decimal.NewFromInt(100),
	}
	strategy := fixedTargetStrategy{}
	if _, err := simulateQuantBacktest(context.Background(), strategy, strategy.Descriptor(), config, testQuantCandles(1), 1); err == nil {
		t.Fatal("backtest accepted a final candle without a next open")
	}
	candles := testQuantCandles(3)
	candles[1].OpenTime = candles[1].OpenTime.Add(time.Minute)
	if _, err := simulateQuantBacktest(context.Background(), strategy, strategy.Descriptor(), config, candles, 1); err == nil {
		t.Fatal("backtest accepted a candle gap")
	}
}

func TestParseBinanceKlinePreservesUTCDecimals(t *testing.T) {
	config := quantSeriesConfig{Market: "usdm", Instrument: "BTCUSDT", Interval: "1m"}
	candle, err := parseQuantKline(config, json.RawMessage(`[1767225600000,"100.01","102.03","99.99","101.25","12.345",1767225659999]`))
	if err != nil {
		t.Fatal(err)
	}
	if candle.Open.String() != "100.01" || candle.Close.String() != "101.25" || candle.Volume.String() != "12.345" {
		t.Fatalf("parsed candle = %#v", candle)
	}
	_, openOffset := candle.OpenTime.Zone()
	_, closeOffset := candle.CloseTime.Zone()
	if openOffset != 0 || closeOffset != 0 || candle.SourceEventID != "usdm:BTCUSDT:1m:1767225600000" {
		t.Fatalf("parsed UTC identity = %#v", candle)
	}
	if _, err := parseQuantKline(config, json.RawMessage(`[1767225600000,"100","90","80","95","1",1767225659999]`)); err == nil {
		t.Fatal("invalid Binance OHLC values were accepted")
	}
}

type fixedTargetStrategy struct{}

func (fixedTargetStrategy) Descriptor() sdk.StrategyDescriptor {
	return sdk.StrategyDescriptor{
		ID: "official.quant.fixed-test", Version: "1.0.0", Name: "Fixed test", MinimumLookback: 1,
		ParameterSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false}`),
	}
}

func (fixedTargetStrategy) Evaluate(ctx context.Context, _ sdk.EvaluateRequest) (decimal.Decimal, error) {
	if err := ctx.Err(); err != nil {
		return decimal.Zero, err
	}
	return quantOne, nil
}

func testQuantCandles(count int) []quantCandle {
	start := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	candles := make([]quantCandle, count)
	for index := range candles {
		openTime := start.Add(time.Duration(index) * time.Hour)
		price := decimal.NewFromInt(int64(10 + index))
		candles[index] = quantCandle{
			Market: "spot", Instrument: "BTCUSDT", Interval: "1h", OpenTime: openTime,
			CloseTime: openTime.Add(time.Hour - time.Millisecond), Open: price, High: price.Add(quantOne),
			Low: price.Sub(quantOne), Close: price, Volume: decimal.NewFromInt(1),
		}
	}
	return candles
}
