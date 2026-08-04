// Package binance 只负责把 Binance 公开载荷规范化为 marketdata 领域类型。
package binance

import (
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"time"

	"coinsphere/backend/internal/marketdata"
	"github.com/shopspring/decimal"
)

var millisecondNumberPattern = regexp.MustCompile(`^(?:0|[1-9][0-9]*)$`)

type exchangeInfoDTO struct {
	Symbols []instrumentDTO `json:"symbols"`
}

type instrumentDTO struct {
	Symbol       string      `json:"symbol"`
	Status       string      `json:"status"`
	BaseAsset    string      `json:"baseAsset"`
	QuoteAsset   string      `json:"quoteAsset"`
	ContractType string      `json:"contractType"`
	Filters      []filterDTO `json:"filters"`
}

type filterDTO struct {
	FilterType string `json:"filterType"`
	TickSize   string `json:"tickSize"`
	StepSize   string `json:"stepSize"`
}

type klineEventDTO struct {
	Kline *klineDTO `json:"k"`
}

type klineDTO struct {
	OpenTime  json.RawMessage `json:"t"`
	CloseTime json.RawMessage `json:"T"`
	Open      string          `json:"o"`
	High      string          `json:"h"`
	Low       string          `json:"l"`
	Close     string          `json:"c"`
	Volume    string          `json:"v"`
	Closed    *bool           `json:"x"`
}

type tickerEventDTO struct {
	EventTime json.RawMessage `json:"E"`
	Last      string          `json:"c"`
	Bid       string          `json:"b"`
	Ask       string          `json:"a"`
}

// NormalizeInstrumentSnapshot 将 exchangeInfo 响应完整转换为同一市场类型的有序元数据快照。
func NormalizeInstrumentSnapshot(payload []byte, marketType marketdata.MarketType) ([]marketdata.InstrumentMetadata, error) {
	if marketType != marketdata.MarketTypeSpot && marketType != marketdata.MarketTypeUSDTPerpetual {
		return nil, invalidRequestError("invalid market type")
	}

	var response exchangeInfoDTO
	if err := json.Unmarshal(payload, &response); err != nil || response.Symbols == nil {
		return nil, protocolError("invalid instrument payload")
	}

	metadata := make([]marketdata.InstrumentMetadata, 0, len(response.Symbols))
	seen := make(map[string]struct{}, len(response.Symbols))
	for _, source := range response.Symbols {
		item, err := normalizeInstrument(source, marketType)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[item.NativeSymbol]; exists {
			return nil, protocolError("duplicate native symbol")
		}
		seen[item.NativeSymbol] = struct{}{}
		metadata = append(metadata, item)
	}
	sort.Slice(metadata, func(left, right int) bool {
		return metadata[left].NativeSymbol < metadata[right].NativeSymbol
	})
	return metadata, nil
}

// NormalizeCandlePage 将历史 Kline 数组规范化为带确定性游标的升序页面。
func NormalizeCandlePage(payload []byte, request marketdata.CandlePageRequest) (marketdata.CandlePage, error) {
	if err := validateCandleRequest(request); err != nil {
		return marketdata.CandlePage{}, err
	}

	var rows []json.RawMessage
	if err := json.Unmarshal(payload, &rows); err != nil || rows == nil {
		return marketdata.CandlePage{}, protocolError("invalid candle payload")
	}
	candles := make([]marketdata.Candle, 0, len(rows))
	for _, row := range rows {
		candle, err := normalizeHistoricalCandle(row, request.Instrument, request.Interval)
		if err != nil {
			return marketdata.CandlePage{}, err
		}
		candles = append(candles, candle)
	}
	sort.Slice(candles, func(left, right int) bool {
		return candles[left].OpenTime.Before(candles[right].OpenTime)
	})

	page := marketdata.CandlePage{Candles: candles}
	if len(candles) == request.Limit {
		duration, _ := marketdata.CandleIntervalDuration(request.Interval)
		next := candles[len(candles)-1].OpenTime.Add(duration)
		if next.Before(request.EndTime) {
			page.NextCursor = marketdata.CandleCursor(next.Format(time.RFC3339Nano))
		}
	}
	if err := marketdata.ValidateCandlePage(request, page); err != nil {
		return marketdata.CandlePage{}, protocolError("invalid candle page")
	}
	return page, nil
}

// NormalizeCandleEvent 将单条 Kline 事件规范化；未闭合 K 线保留同一逻辑键的更新语义。
func NormalizeCandleEvent(payload []byte, instrument marketdata.Instrument, interval marketdata.CandleInterval) (marketdata.Candle, error) {
	if err := validateInstrumentAndInterval(instrument, interval); err != nil {
		return marketdata.Candle{}, err
	}
	var event klineEventDTO
	if err := json.Unmarshal(payload, &event); err != nil || event.Kline == nil || event.Kline.Closed == nil {
		return marketdata.Candle{}, protocolError("invalid candle event")
	}
	return normalizeKline(event.Kline.OpenTime, event.Kline.CloseTime, event.Kline.Open, event.Kline.High, event.Kline.Low, event.Kline.Close, event.Kline.Volume, *event.Kline.Closed, instrument, interval)
}

// NormalizeTickerEvent 将 24hrTicker 的公开价格字段规范化为单个 UTC 快照。
func NormalizeTickerEvent(payload []byte, instrument marketdata.Instrument) (marketdata.Ticker, error) {
	if err := validateInstrument(instrument); err != nil {
		return marketdata.Ticker{}, err
	}
	var event tickerEventDTO
	if err := json.Unmarshal(payload, &event); err != nil {
		return marketdata.Ticker{}, protocolError("invalid ticker event")
	}
	occurredAt, ok := parseNumberMillis(event.EventTime)
	if !ok {
		return marketdata.Ticker{}, protocolError("invalid ticker time")
	}
	last, err := marketdata.ParseDecimal(event.Last)
	if err != nil {
		return marketdata.Ticker{}, protocolError("invalid last price")
	}
	bid, err := marketdata.ParseDecimal(event.Bid)
	if err != nil {
		return marketdata.Ticker{}, protocolError("invalid bid price")
	}
	ask, err := marketdata.ParseDecimal(event.Ask)
	if err != nil {
		return marketdata.Ticker{}, protocolError("invalid ask price")
	}
	ticker := marketdata.Ticker{
		Venue:        marketdata.VenueBinance,
		InstrumentID: instrument.ID,
		OccurredAt:   time.UnixMilli(occurredAt).UTC(),
		LastPrice:    last,
		BestBidPrice: bid,
		BestAskPrice: ask,
	}
	if err := marketdata.ValidateTicker(ticker); err != nil {
		return marketdata.Ticker{}, protocolError("invalid ticker")
	}
	return ticker, nil
}

func normalizeInstrument(source instrumentDTO, marketType marketdata.MarketType) (marketdata.InstrumentMetadata, error) {
	if marketType == marketdata.MarketTypeSpot && source.ContractType != "" {
		return marketdata.InstrumentMetadata{}, protocolError("unexpected spot contract type")
	}
	if marketType == marketdata.MarketTypeUSDTPerpetual && (source.ContractType != "PERPETUAL" || source.QuoteAsset != "USDT") {
		return marketdata.InstrumentMetadata{}, protocolError("invalid perpetual contract")
	}
	status, err := mapStatus(marketType, source.Status)
	if err != nil {
		return marketdata.InstrumentMetadata{}, err
	}
	priceTick, quantityStep, err := parseFilters(source.Filters)
	if err != nil {
		return marketdata.InstrumentMetadata{}, err
	}
	item := marketdata.InstrumentMetadata{
		Venue:        marketdata.VenueBinance,
		MarketType:   marketType,
		NativeSymbol: source.Symbol,
		BaseAsset:    source.BaseAsset,
		QuoteAsset:   source.QuoteAsset,
		Status:       status,
		PriceTick:    priceTick,
		QuantityStep: quantityStep,
	}
	if err := marketdata.ValidateInstrumentMetadata(item); err != nil {
		return marketdata.InstrumentMetadata{}, protocolError("invalid instrument metadata")
	}
	return item, nil
}

func mapStatus(marketType marketdata.MarketType, status string) (marketdata.InstrumentStatus, error) {
	if status == "TRADING" {
		return marketdata.InstrumentStatusTrading, nil
	}
	if marketType == marketdata.MarketTypeSpot {
		switch status {
		case "PRE_TRADING", "POST_TRADING", "END_OF_DAY", "HALT", "AUCTION_MATCH", "BREAK":
			return marketdata.InstrumentStatusSuspended, nil
		}
	} else {
		switch status {
		case "PENDING_TRADING", "PRE_DELIVERING", "DELIVERING", "DELIVERED", "PRE_SETTLE", "SETTLING", "CLOSE":
			return marketdata.InstrumentStatusSuspended, nil
		}
	}
	return "", protocolError("unknown instrument status")
}

func parseFilters(filters []filterDTO) (decimal.Decimal, decimal.Decimal, error) {
	var priceTick decimal.Decimal
	var quantityStep decimal.Decimal
	var hasPriceTick bool
	var hasQuantityStep bool
	for _, filter := range filters {
		switch filter.FilterType {
		case "PRICE_FILTER":
			if hasPriceTick {
				return decimal.Zero, decimal.Zero, protocolError("duplicate price filter")
			}
			value, err := marketdata.ParseDecimal(filter.TickSize)
			if err != nil {
				return decimal.Zero, decimal.Zero, protocolError("invalid price filter")
			}
			priceTick = value
			hasPriceTick = true
		case "LOT_SIZE":
			if hasQuantityStep {
				return decimal.Zero, decimal.Zero, protocolError("duplicate lot size filter")
			}
			value, err := marketdata.ParseDecimal(filter.StepSize)
			if err != nil {
				return decimal.Zero, decimal.Zero, protocolError("invalid lot size filter")
			}
			quantityStep = value
			hasQuantityStep = true
		}
	}
	if !hasPriceTick || !hasQuantityStep {
		return decimal.Zero, decimal.Zero, protocolError("missing required filter")
	}
	return priceTick, quantityStep, nil
}

func normalizeHistoricalCandle(row json.RawMessage, instrument marketdata.Instrument, interval marketdata.CandleInterval) (marketdata.Candle, error) {
	var values []json.RawMessage
	if err := json.Unmarshal(row, &values); err != nil || len(values) < 7 {
		return marketdata.Candle{}, protocolError("invalid historical candle")
	}
	open, ok := decodeString(values[1])
	if !ok {
		return marketdata.Candle{}, protocolError("invalid candle open price")
	}
	high, ok := decodeString(values[2])
	if !ok {
		return marketdata.Candle{}, protocolError("invalid candle high price")
	}
	low, ok := decodeString(values[3])
	if !ok {
		return marketdata.Candle{}, protocolError("invalid candle low price")
	}
	close, ok := decodeString(values[4])
	if !ok {
		return marketdata.Candle{}, protocolError("invalid candle close price")
	}
	volume, ok := decodeString(values[5])
	if !ok {
		return marketdata.Candle{}, protocolError("invalid candle volume")
	}
	return normalizeKline(values[0], values[6], open, high, low, close, volume, true, instrument, interval)
}

func normalizeKline(openRaw, closeRaw json.RawMessage, openText, highText, lowText, closeText, volumeText string, closed bool, instrument marketdata.Instrument, interval marketdata.CandleInterval) (marketdata.Candle, error) {
	openMillis, ok := parseNumberMillis(openRaw)
	if !ok {
		return marketdata.Candle{}, protocolError("invalid candle open time")
	}
	closeMillis, ok := parseNumberMillis(closeRaw)
	if !ok || closeMillis == 1<<63-1 {
		return marketdata.Candle{}, protocolError("invalid candle close time")
	}
	open, err := marketdata.ParseDecimal(openText)
	if err != nil {
		return marketdata.Candle{}, protocolError("invalid candle open price")
	}
	high, err := marketdata.ParseDecimal(highText)
	if err != nil {
		return marketdata.Candle{}, protocolError("invalid candle high price")
	}
	low, err := marketdata.ParseDecimal(lowText)
	if err != nil {
		return marketdata.Candle{}, protocolError("invalid candle low price")
	}
	close, err := marketdata.ParseDecimal(closeText)
	if err != nil {
		return marketdata.Candle{}, protocolError("invalid candle close price")
	}
	volume, err := marketdata.ParseDecimal(volumeText)
	if err != nil {
		return marketdata.Candle{}, protocolError("invalid candle volume")
	}
	candle := marketdata.Candle{
		// Binance 的 T 是闭区间终点，必须加 1ms 才能落到领域模型的排他 CloseTime。
		Venue:        marketdata.VenueBinance,
		InstrumentID: instrument.ID,
		Interval:     interval,
		OpenTime:     time.UnixMilli(openMillis).UTC(),
		CloseTime:    time.UnixMilli(closeMillis + 1).UTC(),
		Open:         open,
		High:         high,
		Low:          low,
		Close:        close,
		BaseVolume:   volume,
		IsClosed:     closed,
	}
	if err := marketdata.ValidateCandle(candle); err != nil {
		return marketdata.Candle{}, protocolError("invalid candle")
	}
	return candle, nil
}

func decodeString(raw json.RawMessage) (string, bool) {
	var value string
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}

func parseNumberMillis(raw json.RawMessage) (int64, bool) {
	if !millisecondNumberPattern.Match(raw) {
		return 0, false
	}
	value, err := strconv.ParseInt(string(raw), 10, 64)
	return value, err == nil
}

func validateCandleRequest(request marketdata.CandlePageRequest) error {
	if err := marketdata.ValidateCandlePageRequest(request); err != nil {
		return invalidRequestError("invalid candle request")
	}
	if request.Instrument.Venue != marketdata.VenueBinance {
		return invalidRequestError("instrument venue does not match Binance")
	}
	return nil
}

func validateInstrumentAndInterval(instrument marketdata.Instrument, interval marketdata.CandleInterval) error {
	if err := validateInstrument(instrument); err != nil {
		return err
	}
	if _, ok := marketdata.CandleIntervalDuration(interval); !ok {
		return invalidRequestError("invalid candle interval")
	}
	return nil
}

func validateInstrument(instrument marketdata.Instrument) error {
	if err := marketdata.ValidateInstrument(instrument); err != nil {
		return invalidRequestError("invalid instrument")
	}
	if instrument.Venue != marketdata.VenueBinance {
		return invalidRequestError("instrument venue does not match Binance")
	}
	return nil
}

func protocolError(message string) error {
	return &marketdata.SourceError{Kind: marketdata.SourceErrorProtocol, Err: errors.New(message)}
}

func invalidRequestError(message string) error {
	return &marketdata.SourceError{Kind: marketdata.SourceErrorInvalidRequest, Err: errors.New(message)}
}
