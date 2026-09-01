package quant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"coinsphere/backend/plugin/sdk"
	"github.com/shopspring/decimal"
	"gorm.io/gorm/clause"
)

type quantWorkflowBacktestAction struct{ runtime *quantRuntime }

type quantWorkflowBacktestPoint struct {
	EvaluatedAt            string                     `json:"evaluatedAt"`
	NodeOutputs            map[string]json.RawMessage `json:"nodeOutputs"`
	PreviousTargetPosition string                     `json:"previousTargetPosition"`
	TargetPosition         string                     `json:"targetPosition"`
	Action                 string                     `json:"action"`
	ExecutionOpenTime      string                     `json:"executionOpenTime"`
	ExecutionPrice         string                     `json:"executionPrice"`
	QuantityDelta          string                     `json:"quantityDelta"`
	Fee                    string                     `json:"fee"`
	Equity                 string                     `json:"equity"`
}

type quantWorkflowBacktestSimulation struct {
	FinalEquity decimal.Decimal
	TotalReturn decimal.Decimal
	MaxDrawdown decimal.Decimal
	TotalFees   decimal.Decimal
	TradeCount  int
	Points      []quantWorkflowBacktestPoint
}

type quantBacktestCandleCacheContextKey struct{}

type quantBacktestCandleCache struct {
	start     time.Time
	end       time.Time
	total     int
	series    map[string][]quantCandle
	lookbacks map[string]int
}

func (a quantWorkflowBacktestAction) Execute(ctx context.Context, request sdk.ActionRequest) (sdk.ActionResult, error) {
	if request.Frames == nil {
		return sdk.ActionResult{}, errors.New("Quant backtest frame executor is unavailable")
	}
	series, err := parseQuantSeriesConfig(request.Config)
	if err != nil {
		return sdk.ActionResult{}, err
	}
	var input struct {
		StartTime, EndTime, InitialCapital, FeeRate, SlippageRate string
	}
	if !decodeQuantStrict(request.Input, &input) {
		return sdk.ActionResult{}, errors.New("Quant workflow backtest input is invalid")
	}
	start, startErr := parseQuantUTCTime(input.StartTime)
	end, endErr := parseQuantUTCTime(input.EndTime)
	capital, capitalErr := decimal.NewFromString(input.InitialCapital)
	feeRate, feeErr := decimal.NewFromString(input.FeeRate)
	slippageRate, slippageErr := decimal.NewFromString(input.SlippageRate)
	if startErr != nil || endErr != nil || !start.Before(end) || capitalErr != nil || capital.Sign() <= 0 ||
		feeErr != nil || feeRate.Sign() < 0 || feeRate.GreaterThan(quantOne) ||
		slippageErr != nil || slippageRate.Sign() < 0 || slippageRate.GreaterThan(quantOne) {
		return sdk.ActionResult{}, errors.New("Quant workflow backtest parameters are invalid")
	}
	if existing, ok, err := a.runtime.loadQuantBacktestByOperation(ctx, request.OperationKey); err != nil {
		return sdk.ActionResult{}, err
	} else if ok {
		return quantWorkflowBacktestResult(existing), nil
	}
	candles, err := a.runtime.loadQuantCandles(ctx, series, start, end, 1_000_001)
	if err != nil {
		return sdk.ActionResult{}, err
	}
	if len(candles) > 1_000_000 {
		return sdk.ActionResult{}, errors.New("Quant workflow backtest exceeds the 1,000,000 candle limit")
	}
	if len(candles) < 2 {
		return sdk.ActionResult{}, errors.New("Quant workflow backtest requires at least two candles")
	}
	cacheKey := quantCandleSeriesKey(series)
	cache := &quantBacktestCandleCache{
		start: candles[0].CloseTime.UTC(), end: candles[len(candles)-1].CloseTime.UTC(), total: len(candles),
		series: map[string][]quantCandle{cacheKey: candles}, lookbacks: map[string]int{},
	}
	ctx = context.WithValue(ctx, quantBacktestCandleCacheContextKey{}, cache)
	simulation, err := executeQuantWorkflowBacktest(ctx, request.Frames, series, candles, capital, feeRate, slippageRate)
	if err != nil {
		return sdk.ActionResult{}, err
	}
	detailCandles := make([]map[string]any, len(candles))
	for index, candle := range candles {
		detailCandles[index] = quantCandleData(candle)
	}
	detail, err := json.Marshal(map[string]any{
		"schemaVersion": 2, "strategyId": "workflow-revision", "strategyVersion": request.Revision.RevisionID,
		"market": series.Market, "instrument": series.Instrument, "interval": series.Interval,
		"parameters": input, "candles": detailCandles, "points": simulation.Points,
	})
	if err != nil {
		return sdk.ActionResult{}, errors.New("encode Quant workflow backtest detail failed")
	}
	workflowID, workflowErr := quantInt64(request.Revision.WorkflowID)
	revisionID, revisionErr := quantInt64(request.Revision.RevisionID)
	if workflowErr != nil || revisionErr != nil {
		return sdk.ActionResult{}, errors.New("Quant workflow backtest identity is invalid")
	}
	parameters, _ := json.Marshal(input)
	manifest, _ := json.Marshal(map[string]any{
		"market": series.Market, "instrument": series.Instrument, "interval": series.Interval,
		"firstOpenTime": candles[0].OpenTime.UTC().Format(time.RFC3339Nano),
		"lastCloseTime": candles[len(candles)-1].CloseTime.UTC().Format(time.RFC3339Nano), "candleCount": len(candles),
	})
	row := quantBacktest{
		OperationKey: request.OperationKey, WorkflowID: workflowID, RevisionID: revisionID, NodeInstanceID: request.NodeInstanceID,
		StrategyID: "workflow-revision", StrategyVersion: request.Revision.RevisionID,
		Market: series.Market, Instrument: series.Instrument, Interval: series.Interval,
		StartTime: candles[0].OpenTime.UTC(), EndTime: candles[len(candles)-1].CloseTime.UTC(),
		InitialCapital: capital, FinalEquity: simulation.FinalEquity, TotalReturn: simulation.TotalReturn,
		MaxDrawdown: simulation.MaxDrawdown, TotalFees: simulation.TotalFees, TradeCount: simulation.TradeCount,
		CandleCount: len(candles), Parameters: string(parameters), DataManifest: string(manifest),
		Detail: string(detail), CreatedAt: time.Now().UTC(),
	}
	if err := a.runtime.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return sdk.ActionResult{}, errors.New("persist Quant workflow backtest summary failed")
	}
	stored, ok, err := a.runtime.loadQuantBacktestByOperation(ctx, request.OperationKey)
	if err != nil || !ok {
		return sdk.ActionResult{}, errors.New("load persisted Quant workflow backtest failed")
	}
	return quantWorkflowBacktestResult(stored), nil
}

func quantCandleSeriesKey(config quantSeriesConfig) string {
	return config.Market + "\x00" + config.Instrument + "\x00" + config.Interval
}

func (c *quantBacktestCandleCache) candlesThroughClose(ctx context.Context, runtime *quantRuntime, config quantSeriesConfig, closeTime time.Time, limit int) ([]quantCandle, error) {
	key := quantCandleSeriesKey(config)
	candles := c.series[key]
	if c.lookbacks[key] < limit {
		available := 1_000_000 - (c.total - len(candles))
		if available < 1 {
			return nil, errors.New("Quant workflow backtest exceeds the 1,000,000 candle limit")
		}
		duration := quantIntervals[config.Interval]
		lowerBound := c.start.Add(-duration * time.Duration(limit+2))
		var loaded []quantCandle
		if err := runtime.db.WithContext(ctx).Where(
			"market = ? AND instrument = ? AND interval = ? AND close_time >= ? AND close_time <= ?",
			config.Market, config.Instrument, config.Interval, lowerBound.UTC(), c.end.UTC(),
		).Order("open_time DESC").Limit(available + 1).Find(&loaded).Error; err != nil {
			return nil, errors.New("preload Quant workflow candles failed")
		}
		if len(loaded) > available {
			return nil, errors.New("Quant workflow backtest exceeds the 1,000,000 candle limit")
		}
		for left, right := 0, len(loaded)-1; left < right; left, right = left+1, right-1 {
			loaded[left], loaded[right] = loaded[right], loaded[left]
		}
		c.total += len(loaded) - len(candles)
		c.series[key] = loaded
		c.lookbacks[key] = limit
		candles = loaded
	}
	end := sort.Search(len(candles), func(index int) bool { return candles[index].CloseTime.After(closeTime) })
	start := end - limit
	if start < 0 {
		start = 0
	}
	return candles[start:end], nil
}

func executeQuantWorkflowBacktest(ctx context.Context, frames sdk.FrameExecutor, series quantSeriesConfig, candles []quantCandle, capital, feeRate, slippageRate decimal.Decimal) (quantWorkflowBacktestSimulation, error) {
	cash, quantity, target := capital, decimal.Zero, decimal.Zero
	peak, maxDrawdown, totalFees := capital, decimal.Zero, decimal.Zero
	points := make([]quantWorkflowBacktestPoint, 0, len(candles)-1)
	trades := 0
	for index := 0; index < len(candles)-1; index++ {
		if err := ctx.Err(); err != nil {
			return quantWorkflowBacktestSimulation{}, err
		}
		current, next := candles[index], candles[index+1]
		frame, err := frames.ExecuteFrame(ctx, sdk.FrameRequest{
			SourceOutput: mustMarshal(map[string]any{
				"branch": "each", "eventTime": current.CloseTime.UTC().Format(time.RFC3339Nano),
				"evaluatedAt": current.CloseTime.UTC().Format(time.RFC3339Nano),
			}),
			PreviousTargetPosition: target.String(),
		})
		if err != nil {
			return quantWorkflowBacktestSimulation{}, err
		}
		previousTarget, nextTarget, action := target, target, "hold"
		for _, raw := range frame.Signals {
			var signal struct {
				TargetPosition, Action string
			}
			if json.Unmarshal(raw, &signal) != nil {
				return quantWorkflowBacktestSimulation{}, errors.New("decode Quant workflow backtest signal failed")
			}
			candidate, err := decimal.NewFromString(signal.TargetPosition)
			if err != nil || !nextTarget.Equal(target) && !nextTarget.Equal(candidate) {
				return quantWorkflowBacktestSimulation{}, fmt.Errorf("Quant workflow signal conflict at %s", current.CloseTime.UTC().Format(time.RFC3339Nano))
			}
			nextTarget, action = candidate, signal.Action
		}
		target = nextTarget
		executionPrice, delta, fee := next.Open, decimal.Zero, decimal.Zero
		if !target.Equal(previousTarget) {
			equityAtOpen := cash.Add(quantity.Mul(next.Open))
			desiredQuantity := equityAtOpen.Mul(target).Div(next.Open)
			delta = desiredQuantity.Sub(quantity)
			if delta.Sign() > 0 {
				executionPrice = next.Open.Mul(quantOne.Add(slippageRate))
			} else {
				executionPrice = next.Open.Mul(quantOne.Sub(slippageRate))
			}
			fee = delta.Abs().Mul(executionPrice).Mul(feeRate)
			cash = cash.Sub(delta.Mul(executionPrice)).Sub(fee)
			quantity = desiredQuantity
			totalFees = totalFees.Add(fee)
			trades++
		}
		equity := cash.Add(quantity.Mul(next.Close))
		if equity.Sign() < 0 {
			return quantWorkflowBacktestSimulation{}, errors.New("Quant workflow backtest equity was depleted")
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
		points = append(points, quantWorkflowBacktestPoint{
			EvaluatedAt: current.CloseTime.UTC().Format(time.RFC3339Nano), NodeOutputs: frame.NodeOutputs,
			PreviousTargetPosition: previousTarget.String(), TargetPosition: target.String(), Action: action,
			ExecutionOpenTime: next.OpenTime.UTC().Format(time.RFC3339Nano), ExecutionPrice: executionPrice.String(),
			QuantityDelta: delta.String(), Fee: fee.String(), Equity: equity.String(),
		})
	}
	final := cash.Add(quantity.Mul(candles[len(candles)-1].Close))
	return quantWorkflowBacktestSimulation{
		FinalEquity: final, TotalReturn: final.Sub(capital).Div(capital), MaxDrawdown: maxDrawdown,
		TotalFees: totalFees, TradeCount: trades, Points: points,
	}, nil
}

func quantWorkflowBacktestResult(backtest quantBacktest) sdk.ActionResult {
	output := quantBacktestOutput(backtest)
	output["branch"] = "completed"
	return sdk.ActionResult{Output: mustMarshal(output)}
}

var _ sdk.ActionHandler = quantWorkflowBacktestAction{}
