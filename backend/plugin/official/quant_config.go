package official

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

var quantInstrumentPattern = regexp.MustCompile(`^[A-Z0-9]{2,32}$`)

type quantSeriesConfig struct {
	Market     string `json:"market"`
	Instrument string `json:"instrument"`
	Interval   string `json:"interval"`
}

type quantStrategyConfig struct {
	quantSeriesConfig
	StrategyID string          `json:"strategyId"`
	Parameters json.RawMessage `json:"parameters"`
}

type quantBacktestConfig struct {
	quantStrategyConfig
	StartTime      time.Time
	EndTime        time.Time
	InitialCapital decimal.Decimal
	FeeRate        decimal.Decimal
	SlippageRate   decimal.Decimal
}

func parseQuantSeriesConfig(raw json.RawMessage) (quantSeriesConfig, error) {
	var config quantSeriesConfig
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil {
		return config, errors.New("quant series configuration is invalid")
	}
	config.Market = strings.ToLower(strings.TrimSpace(config.Market))
	config.Instrument = strings.ToUpper(strings.TrimSpace(config.Instrument))
	config.Interval = strings.TrimSpace(config.Interval)
	if config.Market != "spot" && config.Market != "usdm" || !quantInstrumentPattern.MatchString(config.Instrument) {
		return config, errors.New("quant market or instrument is invalid")
	}
	if _, ok := quantIntervals[config.Interval]; !ok {
		return config, errors.New("quant interval is unsupported")
	}
	return config, nil
}

func parseQuantStrategyConfig(raw json.RawMessage) (quantStrategyConfig, error) {
	var payload struct {
		Market, Instrument, Interval, StrategyID string
		Parameters                               json.RawMessage `json:"parameters"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || len(payload.Parameters) == 0 {
		return quantStrategyConfig{}, errors.New("quant strategy configuration is invalid")
	}
	series, err := parseQuantSeriesConfig(mustMarshal(map[string]any{
		"market": payload.Market, "instrument": payload.Instrument, "interval": payload.Interval,
	}))
	if err != nil || strings.TrimSpace(payload.StrategyID) == "" {
		return quantStrategyConfig{}, errors.New("quant strategy configuration is invalid")
	}
	return quantStrategyConfig{quantSeriesConfig: series, StrategyID: strings.TrimSpace(payload.StrategyID), Parameters: payload.Parameters}, nil
}

func parseQuantBacktestConfig(raw json.RawMessage) (quantBacktestConfig, error) {
	var payload struct {
		Market, Instrument, Interval, StrategyID string
		StartTime, EndTime                       string
		InitialCapital, FeeRate, SlippageRate    string
		Parameters                               json.RawMessage `json:"parameters"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil {
		return quantBacktestConfig{}, errors.New("quant backtest configuration is invalid")
	}
	strategy, err := parseQuantStrategyConfig(mustMarshal(map[string]any{
		"market": payload.Market, "instrument": payload.Instrument, "interval": payload.Interval,
		"strategyId": payload.StrategyID, "parameters": json.RawMessage(payload.Parameters),
	}))
	if err != nil {
		return quantBacktestConfig{}, err
	}
	start, startErr := time.Parse(time.RFC3339, payload.StartTime)
	end, endErr := time.Parse(time.RFC3339, payload.EndTime)
	initial, initialErr := decimal.NewFromString(payload.InitialCapital)
	fee, feeErr := decimal.NewFromString(payload.FeeRate)
	slippage, slippageErr := decimal.NewFromString(payload.SlippageRate)
	_, startOffset := start.Zone()
	_, endOffset := end.Zone()
	if startErr != nil || endErr != nil || startOffset != 0 || endOffset != 0 || !end.After(start) ||
		initialErr != nil || feeErr != nil || slippageErr != nil || initial.Sign() <= 0 ||
		fee.Sign() < 0 || fee.GreaterThan(quantOne) || slippage.Sign() < 0 || slippage.GreaterThan(quantOne) {
		return quantBacktestConfig{}, errors.New("quant backtest range or Decimal settings are invalid")
	}
	return quantBacktestConfig{
		quantStrategyConfig: strategy, StartTime: start.UTC(), EndTime: end.UTC(),
		InitialCapital: initial, FeeRate: fee, SlippageRate: slippage,
	}, nil
}

func quantInt64(value string) (int64, error) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, errors.New("positive integer is required")
	}
	return parsed, nil
}
