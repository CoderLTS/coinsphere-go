package official

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"coinsphere/backend/plugin/sdk"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/shopspring/decimal"
)

const smaStrategyID = "official.quant.sma-crossover"

var smaParameterSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"fastPeriod":{"type":"integer","minimum":1,"maximum":100},"slowPeriod":{"type":"integer","minimum":2,"maximum":200}},"required":["fastPeriod","slowPeriod"],"additionalProperties":false}`)
var quantOne = decimal.NewFromInt(1)

var quantIntervals = map[string]time.Duration{
	"1m": time.Minute, "3m": 3 * time.Minute, "5m": 5 * time.Minute,
	"15m": 15 * time.Minute, "30m": 30 * time.Minute, "1h": time.Hour,
	"2h": 2 * time.Hour, "4h": 4 * time.Hour, "6h": 6 * time.Hour,
	"8h": 8 * time.Hour, "12h": 12 * time.Hour, "1d": 24 * time.Hour,
	"3d": 72 * time.Hour, "1w": 7 * 24 * time.Hour,
}

type smaCrossoverStrategy struct{}

func (smaCrossoverStrategy) Descriptor() sdk.StrategyDescriptor {
	return sdk.StrategyDescriptor{
		ID: smaStrategyID, Version: "1.0.0", Name: "SMA crossover",
		ParameterSchema: smaParameterSchema, MinimumLookback: 2,
	}
}

func (smaCrossoverStrategy) Evaluate(ctx context.Context, request sdk.EvaluateRequest) (decimal.Decimal, error) {
	if err := ctx.Err(); err != nil {
		return decimal.Zero, err
	}
	if err := validateStrategyCandles(request); err != nil {
		return decimal.Zero, err
	}
	var parameters struct {
		FastPeriod int `json:"fastPeriod"`
		SlowPeriod int `json:"slowPeriod"`
	}
	decoder := json.NewDecoder(bytes.NewReader(request.Parameters))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&parameters) != nil || parameters.FastPeriod < 1 || parameters.SlowPeriod <= parameters.FastPeriod ||
		parameters.FastPeriod > 100 || parameters.SlowPeriod > 200 || len(request.Candles) < parameters.SlowPeriod {
		return decimal.Zero, errors.New("sma crossover parameters or lookback are invalid")
	}
	fast := candleCloseAverage(request.Candles[len(request.Candles)-parameters.FastPeriod:])
	slow := candleCloseAverage(request.Candles[len(request.Candles)-parameters.SlowPeriod:])
	return decimal.NewFromInt(int64(fast.Cmp(slow))), nil
}

func candleCloseAverage(candles []sdk.Candle) decimal.Decimal {
	total := decimal.Zero
	for _, candle := range candles {
		total = total.Add(candle.Close)
	}
	return total.Div(decimal.NewFromInt(int64(len(candles))))
}

func validateStrategyParameters(desc sdk.StrategyDescriptor, parameters json.RawMessage) error {
	var value any
	if json.Unmarshal(parameters, &value) != nil {
		return errors.New("strategy parameters must be valid JSON")
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	if err := compiler.AddResource("parameters.json", desc.ParameterSchema); err != nil {
		return errors.New("strategy parameter schema is invalid")
	}
	schema, err := compiler.Compile("parameters.json")
	if err != nil || schema.Validate(value) != nil {
		return errors.New("strategy parameters do not match the strategy schema")
	}
	return nil
}

func validateStrategyCandles(request sdk.EvaluateRequest) error {
	duration, ok := quantIntervals[request.Interval]
	if !ok || request.Market != "spot" && request.Market != "usdm" || request.Instrument == "" || request.EvaluatedAt.IsZero() {
		return errors.New("strategy context is invalid")
	}
	_, evaluatedOffset := request.EvaluatedAt.Zone()
	if evaluatedOffset != 0 || len(request.Candles) == 0 {
		return errors.New("strategy evaluation requires UTC closed candles")
	}
	for index, candle := range request.Candles {
		_, openOffset := candle.OpenTime.Zone()
		_, closeOffset := candle.CloseTime.Zone()
		if openOffset != 0 || closeOffset != 0 || candle.OpenTime.IsZero() || !candle.CloseTime.After(candle.OpenTime) ||
			candle.CloseTime.After(request.EvaluatedAt) || candle.Open.Sign() <= 0 || candle.High.Sign() <= 0 ||
			candle.Low.Sign() <= 0 || candle.Close.Sign() <= 0 || candle.Volume.Sign() < 0 ||
			candle.High.LessThan(decimal.Max(candle.Open, decimal.Max(candle.Close, candle.Low))) ||
			candle.Low.GreaterThan(decimal.Min(candle.Open, decimal.Min(candle.Close, candle.High))) {
			return errors.New("strategy candle is invalid or not closed")
		}
		if index > 0 && !candle.OpenTime.Equal(request.Candles[index-1].OpenTime.Add(duration)) {
			return fmt.Errorf("strategy candle gap at %s", candle.OpenTime.UTC().Format(time.RFC3339))
		}
	}
	return nil
}

var _ sdk.Strategy = smaCrossoverStrategy{}
