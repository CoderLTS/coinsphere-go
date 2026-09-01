package binance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"

	"coinsphere/backend/plugin/sdk"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const pluginID = "official.binance"

var instrumentPattern = regexp.MustCompile(`^[A-Z0-9]{2,32}$`)
var clientOrderIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:/-]{1,36}$`)
var accountIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
var binanceInstrumentPattern = regexp.MustCompile(`^\S{1,32}$`)
var binanceSeriesInstrumentPattern = instrumentPattern
var emptyObjectSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false}`)
var binanceIntervals = map[string]time.Duration{
	"1m": time.Minute, "3m": 3 * time.Minute, "5m": 5 * time.Minute, "15m": 15 * time.Minute,
	"30m": 30 * time.Minute, "1h": time.Hour, "2h": 2 * time.Hour, "4h": 4 * time.Hour,
	"6h": 6 * time.Hour, "8h": 8 * time.Hour, "12h": 12 * time.Hour, "1d": 24 * time.Hour,
	"3d": 72 * time.Hour, "1w": 7 * 24 * time.Hour,
}
var binanceIntervalOrder = []string{"1m", "3m", "5m", "15m", "30m", "1h", "2h", "4h", "6h", "8h", "12h", "1d", "3d", "1w"}

type binanceRuntime struct {
	db           *gorm.DB
	client       sdk.NetworkClient
	hub          *binanceCandleHub
	resolveProxy func(context.Context, int64) (string, error)
	liveLocks    sync.Map
}

func (q *binanceRuntime) lockLiveAccount(account, market string) func() {
	value, _ := q.liveLocks.LoadOrStore(account+"\x00"+market, &sync.Mutex{})
	mutex := value.(*sync.Mutex)
	mutex.Lock()
	return mutex.Unlock
}

type binanceSeriesConfig struct {
	Market     string `json:"market"`
	Instrument string `json:"instrument"`
	Interval   string `json:"interval"`
	ProxyID    int64  `json:"proxyId"`
}

type binanceCandleStreamConfig struct {
	Market     string   `json:"market"`
	Instrument string   `json:"instrument"`
	Intervals  []string `json:"intervals"`
	ProxyID    int64    `json:"proxyId"`
}

type binanceCandleBackfillConfig struct {
	binanceCandleStreamConfig
	CandleCount int
	EndTime     time.Time
}

type binanceInstrumentSyncConfig struct {
	Markets            []string `json:"markets"`
	QuoteAssets        []string `json:"quoteAssets"`
	BaseAssetAllowlist []string `json:"baseAssetAllowlist"`
	BaseAssetDenylist  []string `json:"baseAssetDenylist"`
	SymbolAllowlist    []string `json:"symbolAllowlist"`
	SymbolDenylist     []string `json:"symbolDenylist"`
	ProxyID            int64    `json:"proxyId"`
}

type binanceInstrument struct {
	Market       string
	Symbol       string
	BaseAsset    string
	QuoteAsset   string
	Status       string
	PriceTick    decimal.Decimal
	QuantityStep decimal.Decimal
	MinQuantity  decimal.Decimal
	UpdatedAt    time.Time
}

func (binanceInstrument) TableName() string { return "plugin_binance.instruments" }

type binanceInstrumentSource struct {
	WorkflowID int64
	Market     string
	Symbol     string
	SyncedAt   time.Time
}

func (binanceInstrumentSource) TableName() string { return "plugin_binance.instrument_sources" }

type binanceCandle struct {
	Market        string
	Instrument    string
	Interval      string
	OpenTime      time.Time
	CloseTime     time.Time
	Open          decimal.Decimal
	High          decimal.Decimal
	Low           decimal.Decimal
	Close         decimal.Decimal
	Volume        decimal.Decimal
	SourceEventID string
	ReceivedAt    time.Time
}

func (binanceCandle) TableName() string { return "plugin_binance.candles" }

func parseBinanceSeriesConfig(raw json.RawMessage) (binanceSeriesConfig, error) {
	var config binanceSeriesConfig
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil {
		return config, errors.New("Binance series configuration is invalid")
	}
	config.Market = strings.ToLower(strings.TrimSpace(config.Market))
	config.Instrument = strings.ToUpper(strings.TrimSpace(config.Instrument))
	config.Interval = strings.TrimSpace(config.Interval)
	if config.Market != "spot" && config.Market != "usdm" || !instrumentPattern.MatchString(config.Instrument) || config.ProxyID < 0 {
		return config, errors.New("Binance market or instrument is invalid")
	}
	if config.Interval != "" {
		if _, ok := binanceIntervals[config.Interval]; !ok {
			return config, errors.New("Binance interval is unsupported")
		}
	}
	return config, nil
}

func parseBinanceCandleStreamConfig(raw json.RawMessage) (binanceCandleStreamConfig, error) {
	var config binanceCandleStreamConfig
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil {
		return config, errors.New("Binance candle stream configuration is invalid")
	}
	return normalizeBinanceCandleStreamConfig(config)
}

func parseBinanceCandleBackfillConfig(raw json.RawMessage, now time.Time) (binanceCandleBackfillConfig, error) {
	var payload struct {
		Market, Instrument, EndTime string
		Intervals                   []string `json:"intervals"`
		CandleCount                 int      `json:"candleCount"`
		ProxyID                     int64    `json:"proxyId"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || payload.CandleCount < 1 || payload.CandleCount > 10000 {
		return binanceCandleBackfillConfig{}, errors.New("Binance candle backfill configuration is invalid")
	}
	stream, err := normalizeBinanceCandleStreamConfig(binanceCandleStreamConfig{Market: payload.Market, Instrument: payload.Instrument, Intervals: payload.Intervals, ProxyID: payload.ProxyID})
	if err != nil {
		return binanceCandleBackfillConfig{}, err
	}
	end := now.UTC()
	if value := strings.TrimSpace(payload.EndTime); value != "" {
		end, err = time.Parse(time.RFC3339, value)
		_, offset := end.Zone()
		if err != nil || offset != 0 || end.After(now) {
			return binanceCandleBackfillConfig{}, errors.New("Binance candle backfill end time is invalid")
		}
	}
	return binanceCandleBackfillConfig{binanceCandleStreamConfig: stream, CandleCount: payload.CandleCount, EndTime: end.UTC()}, nil
}

func normalizeBinanceCandleStreamConfig(config binanceCandleStreamConfig) (binanceCandleStreamConfig, error) {
	config.Market = strings.ToLower(strings.TrimSpace(config.Market))
	config.Instrument = strings.ToUpper(strings.TrimSpace(config.Instrument))
	if config.Market != "spot" && config.Market != "usdm" || !instrumentPattern.MatchString(config.Instrument) ||
		len(config.Intervals) == 0 || len(config.Intervals) > len(binanceIntervalOrder) || config.ProxyID < 0 {
		return config, errors.New("Binance candle stream configuration is invalid")
	}
	selected := make(map[string]bool, len(config.Intervals))
	for _, interval := range config.Intervals {
		if _, ok := binanceIntervals[interval]; !ok || selected[interval] {
			return config, errors.New("Binance candle interval is unsupported or duplicated")
		}
		selected[interval] = true
	}
	config.Intervals = config.Intervals[:0]
	for _, interval := range binanceIntervalOrder {
		if selected[interval] {
			config.Intervals = append(config.Intervals, interval)
		}
	}
	return config, nil
}

func parseBinanceInstrumentSyncConfig(raw json.RawMessage) (binanceInstrumentSyncConfig, error) {
	var config binanceInstrumentSyncConfig
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil {
		return config, errors.New("Binance instrument sync configuration is invalid")
	}
	config.Markets = normalizeBinanceFilter(config.Markets, true)
	config.QuoteAssets = normalizeBinanceFilter(config.QuoteAssets, false)
	config.BaseAssetAllowlist = normalizeBinanceFilter(config.BaseAssetAllowlist, false)
	config.BaseAssetDenylist = normalizeBinanceFilter(config.BaseAssetDenylist, false)
	config.SymbolAllowlist = normalizeBinanceFilter(config.SymbolAllowlist, false)
	config.SymbolDenylist = normalizeBinanceFilter(config.SymbolDenylist, false)
	if len(config.Markets) == 0 || len(config.QuoteAssets) == 0 || config.ProxyID < 0 {
		return config, errors.New("Binance instrument sync markets and quote assets are required")
	}
	for _, market := range config.Markets {
		if market != "spot" && market != "usdm" {
			return config, errors.New("Binance instrument sync market is invalid")
		}
	}
	return config, nil
}

func normalizeBinanceFilter(values []string, lower bool) []string {
	seen, result := map[string]bool{}, make([]string, 0, len(values))
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

func mustMarshal(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }
