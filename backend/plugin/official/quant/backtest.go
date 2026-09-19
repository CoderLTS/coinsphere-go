package quant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"coinsphere/backend/plugin/sdk"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type quantEvaluateAction struct{ runtime *quantRuntime }

func (a quantEvaluateAction) Execute(ctx context.Context, request sdk.ActionRequest) (sdk.ActionResult, error) {
	config, err := parseQuantStrategyConfig(request.Config)
	if err != nil {
		return sdk.ActionResult{}, err
	}
	desc, strategy, ok := a.runtime.registry.Strategy(config.StrategyID)
	if !ok {
		return sdk.ActionResult{}, errors.New("quant strategy is unavailable")
	}
	if err := validateStrategyParameters(desc, config.Parameters); err != nil {
		return sdk.ActionResult{}, err
	}
	lookback, err := quantStrategyLookback(desc, config.Parameters)
	if err != nil {
		return sdk.ActionResult{}, err
	}
	var input struct {
		EventTime string `json:"eventTime"`
	}
	if json.Unmarshal(request.Input, &input) != nil {
		return sdk.ActionResult{}, errors.New("quant evaluation input is invalid")
	}
	eventTime, err := parseQuantUTCTime(input.EventTime)
	if err != nil {
		return sdk.ActionResult{}, err
	}
	candles, err := a.runtime.loadQuantCandlesThroughClose(ctx, config.quantSeriesConfig, eventTime, lookback)
	if err != nil {
		return sdk.ActionResult{}, err
	}
	if len(candles) != lookback || !candles[len(candles)-1].CloseTime.Equal(eventTime) {
		return sdk.ActionResult{}, errors.New("quant strategy has insufficient closed lookback")
	}
	target, err := strategy.Evaluate(ctx, sdk.EvaluateRequest{
		Market: config.Market, Instrument: config.Instrument, Interval: config.Interval,
		Candles: quantSDKCandles(candles), Parameters: config.Parameters,
		EvaluatedAt: candles[len(candles)-1].CloseTime.UTC(),
	})
	if err != nil {
		return sdk.ActionResult{}, err
	}
	if target.LessThan(decimal.NewFromInt(-1)) || target.GreaterThan(quantOne) {
		return sdk.ActionResult{}, errors.New("quant strategy target must be between -1 and 1")
	}
	return sdk.ActionResult{Output: mustMarshal(map[string]any{
		"venue": config.Venue, "strategyId": desc.ID, "strategyVersion": desc.Version, "target": target.String(),
		"evaluatedAt": candles[len(candles)-1].CloseTime.UTC().Format(time.RFC3339Nano),
	})}, nil
}

type quantBacktestAction struct{ runtime *quantRuntime }

func (a quantBacktestAction) Execute(ctx context.Context, request sdk.ActionRequest) (sdk.ActionResult, error) {
	config, err := parseQuantBacktestConfig(request.Config)
	if err != nil {
		return sdk.ActionResult{}, err
	}
	if existing, ok, err := a.runtime.loadQuantBacktestByOperation(ctx, request.OperationKey); err != nil {
		return sdk.ActionResult{}, err
	} else if ok {
		return quantBacktestActionResult(existing), nil
	}
	desc, strategy, ok := a.runtime.registry.Strategy(config.StrategyID)
	if !ok {
		return sdk.ActionResult{}, errors.New("quant strategy is unavailable")
	}
	if err := validateStrategyParameters(desc, config.Parameters); err != nil {
		return sdk.ActionResult{}, err
	}
	lookback, err := quantStrategyLookback(desc, config.Parameters)
	if err != nil {
		return sdk.ActionResult{}, err
	}
	candles, err := a.runtime.loadQuantCandles(ctx, config.quantSeriesConfig, config.StartTime, config.EndTime, 1_000_001)
	if err != nil {
		return sdk.ActionResult{}, err
	}
	if len(candles) > 1_000_000 {
		return sdk.ActionResult{}, errors.New("quant backtest exceeds the 1,000,000 candle limit")
	}
	simulation, err := simulateQuantBacktest(ctx, strategy, desc, config, candles, lookback)
	if err != nil {
		return sdk.ActionResult{}, err
	}
	detail, manifest, err := quantBacktestDetail(config, desc, candles, simulation)
	if err != nil {
		return sdk.ActionResult{}, err
	}
	workflowID, err := quantInt64(request.Revision.WorkflowID)
	if err != nil {
		return sdk.ActionResult{}, errors.New("quant workflow identity is invalid")
	}
	revisionID, err := quantInt64(request.Revision.RevisionID)
	if err != nil {
		return sdk.ActionResult{}, errors.New("quant revision identity is invalid")
	}
	parameters, _ := json.Marshal(json.RawMessage(config.Parameters))
	manifestJSON, _ := json.Marshal(manifest)
	row := quantBacktest{
		OperationKey: request.OperationKey, WorkflowID: workflowID, RevisionID: revisionID,
		NodeInstanceID: request.NodeInstanceID, StrategyID: desc.ID, StrategyVersion: desc.Version, Venue: config.Venue,
		Market: config.Market, Instrument: config.Instrument, Interval: config.Interval,
		StartTime: candles[0].OpenTime.UTC(), EndTime: candles[len(candles)-1].CloseTime.UTC(),
		InitialCapital: config.InitialCapital, FinalEquity: simulation.FinalEquity,
		TotalReturn: simulation.TotalReturn, MaxDrawdown: simulation.MaxDrawdown,
		TotalFees: simulation.TotalFees, TradeCount: simulation.TradeCount, CandleCount: len(candles),
		Parameters: string(parameters), DataManifest: string(manifestJSON),
		Detail: string(detail), CreatedAt: time.Now().UTC(),
	}
	if err := a.runtime.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return sdk.ActionResult{}, errors.New("persist Quant backtest summary failed")
	}
	stored, ok, err := a.runtime.loadQuantBacktestByOperation(ctx, request.OperationKey)
	if err != nil || !ok {
		return sdk.ActionResult{}, errors.New("load persisted Quant backtest failed")
	}
	return quantBacktestActionResult(stored), nil
}

type quantBacktestSimulation struct {
	FinalEquity decimal.Decimal
	TotalReturn decimal.Decimal
	MaxDrawdown decimal.Decimal
	TotalFees   decimal.Decimal
	TradeCount  int
	Points      []quantBacktestPoint
}

type quantBacktestPoint struct {
	EvaluatedAt       string `json:"evaluatedAt"`
	Target            string `json:"target"`
	ExecutionOpenTime string `json:"executionOpenTime"`
	ExecutionPrice    string `json:"executionPrice"`
	QuantityDelta     string `json:"quantityDelta"`
	Fee               string `json:"fee"`
	Equity            string `json:"equity"`
}

func simulateQuantBacktest(ctx context.Context, strategy sdk.Strategy, desc sdk.StrategyDescriptor, config quantBacktestConfig, candles []quantCandle, lookback int) (quantBacktestSimulation, error) {
	if len(candles) < lookback+1 {
		return quantBacktestSimulation{}, errors.New("quant backtest has insufficient lookback or no next candle open")
	}
	sdkCandles := quantSDKCandles(candles)
	if err := validateStrategyCandles(sdk.EvaluateRequest{
		Market: config.Market, Instrument: config.Instrument, Interval: config.Interval,
		Candles: sdkCandles, EvaluatedAt: candles[len(candles)-1].CloseTime.UTC(),
	}); err != nil {
		return quantBacktestSimulation{}, err
	}
	cash, quantity := config.InitialCapital, decimal.Zero
	peak, maxDrawdown, fees := config.InitialCapital, decimal.Zero, decimal.Zero
	points := make([]quantBacktestPoint, 0, len(candles)-lookback)
	trades := 0
	for index := lookback - 1; index < len(candles)-1; index++ {
		if err := ctx.Err(); err != nil {
			return quantBacktestSimulation{}, err
		}
		window := sdkCandles[index-lookback+1 : index+1]
		target, err := strategy.Evaluate(ctx, sdk.EvaluateRequest{
			Market: config.Market, Instrument: config.Instrument, Interval: config.Interval,
			Candles: window, Parameters: config.Parameters, EvaluatedAt: candles[index].CloseTime.UTC(),
		})
		if err != nil {
			return quantBacktestSimulation{}, err
		}
		if target.LessThan(decimal.NewFromInt(-1)) || target.GreaterThan(quantOne) {
			return quantBacktestSimulation{}, errors.New("quant strategy target must be between -1 and 1")
		}
		next := candles[index+1]
		equityAtOpen := cash.Add(quantity.Mul(next.Open))
		desiredQuantity := equityAtOpen.Mul(target).Div(next.Open)
		delta := desiredQuantity.Sub(quantity)
		executionPrice, fee := next.Open, decimal.Zero
		if !delta.IsZero() {
			if delta.Sign() > 0 {
				executionPrice = next.Open.Mul(quantOne.Add(config.SlippageRate))
			} else {
				executionPrice = next.Open.Mul(quantOne.Sub(config.SlippageRate))
			}
			fee = delta.Abs().Mul(executionPrice).Mul(config.FeeRate)
			cash = cash.Sub(delta.Mul(executionPrice)).Sub(fee)
			quantity = desiredQuantity
			fees = fees.Add(fee)
			trades++
		}
		equity := cash.Add(quantity.Mul(next.Close))
		if equity.Sign() < 0 {
			return quantBacktestSimulation{}, errors.New("quant backtest equity was depleted")
		}
		if equity.GreaterThan(peak) {
			peak = equity
		}
		if peak.Sign() > 0 {
			drawdown := peak.Sub(equity).Div(peak)
			if drawdown.GreaterThan(maxDrawdown) {
				maxDrawdown = drawdown
			}
		}
		points = append(points, quantBacktestPoint{
			EvaluatedAt: candles[index].CloseTime.UTC().Format(time.RFC3339Nano), Target: target.String(),
			ExecutionOpenTime: next.OpenTime.UTC().Format(time.RFC3339Nano), ExecutionPrice: executionPrice.String(),
			QuantityDelta: delta.String(), Fee: fee.String(), Equity: equity.String(),
		})
	}
	final := cash.Add(quantity.Mul(candles[len(candles)-1].Close))
	return quantBacktestSimulation{
		FinalEquity: final, TotalReturn: final.Sub(config.InitialCapital).Div(config.InitialCapital),
		MaxDrawdown: maxDrawdown, TotalFees: fees, TradeCount: trades, Points: points,
	}, nil
}

func (q *quantRuntime) loadQuantCandles(ctx context.Context, config quantSeriesConfig, start, end time.Time, limit int) ([]quantCandle, error) {
	if q.marketData == nil {
		return nil, errors.New("quant market data registry is unavailable")
	}
	provider, ok := q.marketData.MarketDataProvider(config.Venue)
	if !ok {
		return nil, fmt.Errorf("quant market data provider %q is unavailable", config.Venue)
	}
	items, err := provider.Candles(ctx, sdk.CandleQuery{
		Market: config.Market, Instrument: config.Instrument, Interval: config.Interval,
		StartTime: start.UTC(), EndTime: end.UTC(), Limit: limit,
	})
	if err != nil {
		return nil, err
	}
	candles := make([]quantCandle, len(items))
	for index, candle := range items {
		candles[index] = quantCandle{Venue: config.Venue, Market: config.Market, Instrument: config.Instrument, Interval: config.Interval,
			OpenTime: candle.OpenTime.UTC(), CloseTime: candle.CloseTime.UTC(), Open: candle.Open, High: candle.High,
			Low: candle.Low, Close: candle.Close, Volume: candle.Volume}
	}
	return candles, nil
}

func (q *quantRuntime) loadQuantCandlesThroughClose(ctx context.Context, config quantSeriesConfig, closeTime time.Time, limit int) ([]quantCandle, error) {
	if cache, ok := ctx.Value(quantBacktestCandleCacheContextKey{}).(*quantBacktestCandleCache); ok {
		return cache.candlesThroughClose(ctx, q, config, closeTime.UTC(), limit)
	}
	return q.loadQuantCandles(ctx, config, time.Time{}, closeTime.UTC().Add(time.Nanosecond), limit)
}

func quantStrategyLookback(desc sdk.StrategyDescriptor, parameters json.RawMessage) (int, error) {
	if desc.ID == smaStrategyID {
		var value struct {
			SlowPeriod int `json:"slowPeriod"`
		}
		if json.Unmarshal(parameters, &value) != nil || value.SlowPeriod < desc.MinimumLookback || value.SlowPeriod > 200 {
			return 0, errors.New("sma crossover lookback is invalid")
		}
		return value.SlowPeriod, nil
	}
	return desc.MinimumLookback, nil
}

func parseQuantUTCTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, errors.New("quant time must use RFC3339 UTC")
	}
	_, offset := parsed.Zone()
	if offset != 0 {
		return time.Time{}, errors.New("quant time must use UTC")
	}
	return parsed.UTC(), nil
}

func quantSDKCandles(candles []quantCandle) []sdk.Candle {
	converted := make([]sdk.Candle, len(candles))
	for index, candle := range candles {
		converted[index] = quantSDKCandle(candle)
	}
	return converted
}

func quantBacktestDetail(config quantBacktestConfig, desc sdk.StrategyDescriptor, candles []quantCandle, simulation quantBacktestSimulation) ([]byte, map[string]any, error) {
	data := make([]map[string]any, len(candles))
	for index, candle := range candles {
		data[index] = quantCandleData(candle)
	}
	rawData, err := json.Marshal(data)
	if err != nil {
		return nil, nil, err
	}
	digest := sha256.Sum256(rawData)
	manifest := map[string]any{
		"sha256": hex.EncodeToString(digest[:]), "candleCount": len(candles),
		"firstOpenTime": candles[0].OpenTime.UTC().Format(time.RFC3339Nano),
		"lastCloseTime": candles[len(candles)-1].CloseTime.UTC().Format(time.RFC3339Nano),
	}
	detail, err := json.Marshal(map[string]any{
		"schemaVersion": 2, "venue": config.Venue, "strategyId": desc.ID, "strategyVersion": desc.Version,
		"market": config.Market, "instrument": config.Instrument, "interval": config.Interval,
		"parameters": json.RawMessage(config.Parameters), "dataManifest": manifest,
		"summary": map[string]any{
			"initialCapital": config.InitialCapital.String(), "finalEquity": simulation.FinalEquity.String(),
			"totalReturn": simulation.TotalReturn.String(), "maxDrawdown": simulation.MaxDrawdown.String(),
			"totalFees": simulation.TotalFees.String(), "tradeCount": simulation.TradeCount,
		},
		"candles": data, "points": simulation.Points,
	})
	return detail, manifest, err
}

func (q *quantRuntime) loadQuantBacktestByOperation(ctx context.Context, operationKey string) (quantBacktest, bool, error) {
	var backtest quantBacktest
	if err := q.db.WithContext(ctx).Where("operation_key = ?", operationKey).First(&backtest).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return quantBacktest{}, false, nil
		}
		return quantBacktest{}, false, errors.New("load Quant backtest failed")
	}
	return backtest, true, nil
}

func quantBacktestActionResult(backtest quantBacktest) sdk.ActionResult {
	return sdk.ActionResult{Output: mustMarshal(quantBacktestOutput(backtest))}
}

func quantBacktestOutput(backtest quantBacktest) map[string]any {
	return map[string]any{
		"backtestId": backtest.ID, "strategyId": backtest.StrategyID, "strategyVersion": backtest.StrategyVersion, "venue": backtest.Venue,
		"finalEquity": backtest.FinalEquity.String(), "totalReturn": backtest.TotalReturn.String(),
		"maxDrawdown": backtest.MaxDrawdown.String(), "totalFees": backtest.TotalFees.String(),
		"tradeCount": backtest.TradeCount, "candleCount": backtest.CandleCount,
	}
}

func quantBacktestView(backtest quantBacktest) map[string]any {
	view := quantBacktestOutput(backtest)
	view["id"] = backtest.ID
	delete(view, "backtestId")
	view["venue"], view["market"], view["instrument"], view["interval"] = backtest.Venue, backtest.Market, backtest.Instrument, backtest.Interval
	view["startTime"] = backtest.StartTime.UTC().Format(time.RFC3339Nano)
	view["endTime"] = backtest.EndTime.UTC().Format(time.RFC3339Nano)
	view["initialCapital"] = backtest.InitialCapital.String()
	view["createdAt"] = backtest.CreatedAt.UTC().Format(time.RFC3339Nano)
	return view
}

var _ sdk.ActionHandler = quantEvaluateAction{}
var _ sdk.ActionHandler = quantBacktestAction{}
