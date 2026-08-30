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

var quantInstrumentPattern = regexp.MustCompile(`^\S{1,32}$`)

type quantSeriesConfig struct {
	Market     string `json:"market"`
	Instrument string `json:"instrument"`
	Interval   string `json:"interval"`
}

type quantCandleStreamConfig struct {
	Market     string   `json:"market"`
	Instrument string   `json:"instrument"`
	Intervals  []string `json:"intervals"`
}

type quantCandleBackfillConfig struct {
	quantCandleStreamConfig
	CandleCount int
	EndTime     time.Time
}

type quantInstrumentSyncConfig struct {
	Markets            []string `json:"markets"`
	QuoteAssets        []string `json:"quoteAssets"`
	BaseAssetAllowlist []string `json:"baseAssetAllowlist"`
	BaseAssetDenylist  []string `json:"baseAssetDenylist"`
	SymbolAllowlist    []string `json:"symbolAllowlist"`
	SymbolDenylist     []string `json:"symbolDenylist"`
}

func parseQuantInstrumentSyncConfig(raw json.RawMessage) (quantInstrumentSyncConfig, error) {
	var config quantInstrumentSyncConfig
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil {
		return config, errors.New("quant instrument sync configuration is invalid")
	}
	config.Markets = normalizeQuantFilter(config.Markets, true)
	config.QuoteAssets = normalizeQuantFilter(config.QuoteAssets, false)
	config.BaseAssetAllowlist = normalizeQuantFilter(config.BaseAssetAllowlist, false)
	config.BaseAssetDenylist = normalizeQuantFilter(config.BaseAssetDenylist, false)
	config.SymbolAllowlist = normalizeQuantFilter(config.SymbolAllowlist, false)
	config.SymbolDenylist = normalizeQuantFilter(config.SymbolDenylist, false)
	if len(config.Markets) == 0 || len(config.QuoteAssets) == 0 {
		return config, errors.New("quant instrument sync markets and quote assets are required")
	}
	for _, market := range config.Markets {
		if market != "spot" && market != "usdm" {
			return config, errors.New("quant instrument sync market is invalid")
		}
	}
	for _, values := range [][]string{
		config.QuoteAssets, config.BaseAssetAllowlist, config.BaseAssetDenylist,
		config.SymbolAllowlist, config.SymbolDenylist,
	} {
		for _, value := range values {
			if !quantInstrumentPattern.MatchString(value) {
				return config, errors.New("quant instrument sync filter is invalid")
			}
		}
	}
	return config, nil
}

func normalizeQuantFilter(values []string, lower bool) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if lower {
			value = strings.ToLower(value)
		} else {
			value = strings.ToUpper(value)
		}
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
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

func parseQuantCandleStreamConfig(raw json.RawMessage) (quantCandleStreamConfig, error) {
	var config quantCandleStreamConfig
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil {
		return config, errors.New("quant candle stream configuration is invalid")
	}
	return normalizeQuantCandleStreamConfig(config)
}

func parseQuantCandleBackfillConfig(raw json.RawMessage, now time.Time) (quantCandleBackfillConfig, error) {
	var payload struct {
		Market, Instrument, EndTime string
		Intervals                   []string `json:"intervals"`
		CandleCount                 int      `json:"candleCount"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || payload.CandleCount < 1 || payload.CandleCount > 10000 {
		return quantCandleBackfillConfig{}, errors.New("quant candle backfill configuration is invalid")
	}
	stream, err := normalizeQuantCandleStreamConfig(quantCandleStreamConfig{
		Market: payload.Market, Instrument: payload.Instrument, Intervals: payload.Intervals,
	})
	if err != nil {
		return quantCandleBackfillConfig{}, err
	}
	endTime := now.UTC()
	if value := strings.TrimSpace(payload.EndTime); value != "" {
		endTime, err = parseQuantUTCTime(value)
		if err != nil || endTime.After(now) {
			return quantCandleBackfillConfig{}, errors.New("quant candle backfill end time is invalid")
		}
	}
	return quantCandleBackfillConfig{quantCandleStreamConfig: stream, CandleCount: payload.CandleCount, EndTime: endTime}, nil
}

func normalizeQuantCandleStreamConfig(config quantCandleStreamConfig) (quantCandleStreamConfig, error) {
	config.Market = strings.ToLower(strings.TrimSpace(config.Market))
	config.Instrument = strings.ToUpper(strings.TrimSpace(config.Instrument))
	if config.Market != "spot" && config.Market != "usdm" || !quantInstrumentPattern.MatchString(config.Instrument) ||
		len(config.Intervals) == 0 || len(config.Intervals) > len(quantIntervalOrder) {
		return config, errors.New("quant candle stream market, instrument, or intervals are invalid")
	}
	selected := make(map[string]bool, len(config.Intervals))
	for _, interval := range config.Intervals {
		interval = strings.TrimSpace(interval)
		if _, ok := quantIntervals[interval]; !ok || selected[interval] {
			return config, errors.New("quant candle stream interval is unsupported or duplicated")
		}
		selected[interval] = true
	}
	config.Intervals = config.Intervals[:0]
	for _, interval := range quantIntervalOrder {
		if selected[interval] {
			config.Intervals = append(config.Intervals, interval)
		}
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
