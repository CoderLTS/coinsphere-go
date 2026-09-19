package binance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"coinsphere/backend/plugin/sdk"
	"github.com/shopspring/decimal"
)

type marketDataProvider struct{ runtime *binanceRuntime }

func (marketDataProvider) ID() string { return "binance" }

func (p marketDataProvider) Instruments(ctx context.Context, query sdk.InstrumentQuery) ([]sdk.Instrument, error) {
	db := p.runtime.db.WithContext(ctx).Order("market, symbol")
	if len(query.Markets) > 0 {
		db = db.Where("market IN ?", query.Markets)
	}
	if len(query.Instruments) > 0 {
		db = db.Where("symbol IN ?", query.Instruments)
	}
	limit := query.Limit
	if limit <= 0 || limit > 10000 {
		limit = 10000
	}
	var rows []binanceInstrument
	if err := db.Limit(limit).Find(&rows).Error; err != nil {
		return nil, errors.New("list Binance instruments failed")
	}
	result := make([]sdk.Instrument, len(rows))
	for i, row := range rows {
		result[i] = sdk.Instrument{Market: row.Market, Symbol: row.Symbol, BaseAsset: row.BaseAsset, QuoteAsset: row.QuoteAsset,
			Status: row.Status, PriceTick: row.PriceTick, QuantityStep: row.QuantityStep, MinQuantity: row.MinQuantity, UpdatedAt: row.UpdatedAt.UTC()}
	}
	return result, nil
}

func (p marketDataProvider) Candles(ctx context.Context, query sdk.CandleQuery) ([]sdk.Candle, error) {
	config, err := parseBinanceSeriesConfig(mustMarshal(binanceSeriesConfig{Market: query.Market, Instrument: query.Instrument, Interval: query.Interval, ProxyID: query.ProxyID}))
	if err != nil {
		return nil, err
	}
	limit := query.Limit
	if limit <= 0 || limit > 1_000_001 {
		return nil, errors.New("Binance candle limit is invalid")
	}
	db := p.runtime.db.WithContext(ctx).Where("market = ? AND instrument = ? AND interval = ?", config.Market, config.Instrument, config.Interval)
	if !query.StartTime.IsZero() {
		db = db.Where("open_time >= ?", query.StartTime.UTC())
	}
	if !query.EndTime.IsZero() {
		db = db.Where("open_time < ?", query.EndTime.UTC())
	}
	var rows []binanceCandle
	if err := db.Order("open_time DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, errors.New("load Binance candles failed")
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].OpenTime.Before(rows[j].OpenTime) })
	result := make([]sdk.Candle, len(rows))
	for i, row := range rows {
		result[i] = binanceSDKCandle(row)
	}
	return result, nil
}

func (p marketDataProvider) Quote(ctx context.Context, query sdk.QuoteQuery) (sdk.Quote, error) {
	config, err := parseBinanceSeriesConfig(mustMarshal(binanceSeriesConfig{Market: query.Market, Instrument: query.Instrument, ProxyID: query.ProxyID}))
	if err != nil {
		return sdk.Quote{}, err
	}
	base, path := "https://data-api.binance.vision", "/api/v3/ticker/price"
	if config.Market == "usdm" {
		base, path = "https://fapi.binance.com", "/fapi/v1/ticker/price"
	}
	var payload struct {
		Symbol string          `json:"symbol"`
		Price  json.RawMessage `json:"price"`
		Time   int64           `json:"time"`
	}
	if err := p.runtime.getBinanceJSON(ctx, base+path+"?"+url.Values{"symbol": {config.Instrument}}.Encode(), config.ProxyID, &payload); err != nil {
		return sdk.Quote{}, err
	}
	if strings.ToUpper(payload.Symbol) != config.Instrument {
		return sdk.Quote{}, errors.New("Binance quote instrument mismatch")
	}
	var text string
	if json.Unmarshal(payload.Price, &text) != nil {
		text = strings.Trim(string(payload.Price), `"`)
	}
	price, err := decimal.NewFromString(text)
	if err != nil || price.Sign() <= 0 {
		return sdk.Quote{}, fmt.Errorf("Binance quote price is invalid")
	}
	quotedAt := time.Now().UTC()
	if payload.Time > 0 {
		quotedAt = time.UnixMilli(payload.Time).UTC()
	}
	return sdk.Quote{Price: price, QuotedAt: quotedAt}, nil
}

var _ sdk.MarketDataProvider = marketDataProvider{}
