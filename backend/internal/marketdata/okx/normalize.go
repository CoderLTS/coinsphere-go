// Package okx 只负责把 OKX 公开载荷规范化为 marketdata 领域类型。
package okx

import (
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"time"

	"coinsphere/backend/internal/marketdata"
)

var millisecondTextPattern = regexp.MustCompile(`^(?:0|[1-9][0-9]*)$`)

type instrumentsResponseDTO struct {
	Code string          `json:"code"`
	Data []instrumentDTO `json:"data"`
}

type instrumentDTO struct {
	InstrumentType string `json:"instType"`
	InstrumentID   string `json:"instId"`
	BaseCurrency   string `json:"baseCcy"`
	QuoteCurrency  string `json:"quoteCcy"`
	ContractType   string `json:"ctType"`
	ContractValue  string `json:"ctValCcy"`
	SettleCurrency string `json:"settleCcy"`
	State          string `json:"state"`
	TickSize       string `json:"tickSz"`
	LotSize        string `json:"lotSz"`
}

type candlesResponseDTO struct {
	Code string            `json:"code"`
	Data []json.RawMessage `json:"data"`
}

type subscriptionArgDTO struct {
	Channel string `json:"channel"`
}

type candleEventDTO struct {
	Arg  *subscriptionArgDTO `json:"arg"`
	Data []json.RawMessage   `json:"data"`
}

type tickerEventDTO struct {
	Arg  *subscriptionArgDTO `json:"arg"`
	Data []tickerDTO         `json:"data"`
}

type tickerDTO struct {
	Timestamp string `json:"ts"`
	Last      string `json:"last"`
	Bid       string `json:"bidPx"`
	Ask       string `json:"askPx"`
}

// NormalizeInstrumentSnapshot 将 public instruments 响应完整转换为同一市场类型的有序元数据快照。
func NormalizeInstrumentSnapshot(payload []byte, marketType marketdata.MarketType) ([]marketdata.InstrumentMetadata, error) {
	if marketType != marketdata.MarketTypeSpot && marketType != marketdata.MarketTypeUSDTPerpetual {
		return nil, invalidRequestError("invalid market type")
	}
	var response instrumentsResponseDTO
	if err := json.Unmarshal(payload, &response); err != nil || response.Code != "0" || response.Data == nil {
		return nil, protocolError("invalid instrument payload")
	}

	metadata := make([]marketdata.InstrumentMetadata, 0, len(response.Data))
	seen := make(map[string]struct{}, len(response.Data))
	for _, source := range response.Data {
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

// NormalizeCandlePage 将 history-candles 数组规范化为带确定性游标的升序页面。
func NormalizeCandlePage(payload []byte, request marketdata.CandlePageRequest) (marketdata.CandlePage, error) {
	if err := validateCandleRequest(request); err != nil {
		return marketdata.CandlePage{}, err
	}
	var response candlesResponseDTO
	if err := json.Unmarshal(payload, &response); err != nil || response.Code != "0" || response.Data == nil {
		return marketdata.CandlePage{}, protocolError("invalid candle payload")
	}

	candles := make([]marketdata.Candle, 0, len(response.Data))
	for _, row := range response.Data {
		candle, err := normalizeCandleRow(row, request.Instrument, request.Interval, true)
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

// NormalizeCandleEvent 将 candle 通道的一项数据规范化，保留 confirm 对未闭合 K 线的映射。
func NormalizeCandleEvent(payload []byte, instrument marketdata.Instrument, interval marketdata.CandleInterval) (marketdata.Candle, error) {
	if err := validateInstrumentAndInterval(instrument, interval); err != nil {
		return marketdata.Candle{}, err
	}
	channel, _ := candleChannel(interval)
	var event candleEventDTO
	if err := json.Unmarshal(payload, &event); err != nil || event.Arg == nil || event.Arg.Channel != channel || len(event.Data) != 1 {
		return marketdata.Candle{}, protocolError("invalid candle event")
	}
	return normalizeCandleRow(event.Data[0], instrument, interval, false)
}

// NormalizeTickerEvent 将 tickers 通道的一项数据规范化为单个 UTC 快照。
func NormalizeTickerEvent(payload []byte, instrument marketdata.Instrument) (marketdata.Ticker, error) {
	if err := validateInstrument(instrument); err != nil {
		return marketdata.Ticker{}, err
	}
	var event tickerEventDTO
	if err := json.Unmarshal(payload, &event); err != nil || event.Arg == nil || event.Arg.Channel != "tickers" || len(event.Data) != 1 {
		return marketdata.Ticker{}, protocolError("invalid ticker event")
	}
	source := event.Data[0]
	occurredAt, ok := parseStringMillis(source.Timestamp)
	if !ok {
		return marketdata.Ticker{}, protocolError("invalid ticker time")
	}
	last, err := marketdata.ParseDecimal(source.Last)
	if err != nil {
		return marketdata.Ticker{}, protocolError("invalid last price")
	}
	bid, err := marketdata.ParseDecimal(source.Bid)
	if err != nil {
		return marketdata.Ticker{}, protocolError("invalid bid price")
	}
	ask, err := marketdata.ParseDecimal(source.Ask)
	if err != nil {
		return marketdata.Ticker{}, protocolError("invalid ask price")
	}
	ticker := marketdata.Ticker{
		Venue:        marketdata.VenueOKX,
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
	baseAsset := source.BaseCurrency
	quoteAsset := source.QuoteCurrency
	if marketType == marketdata.MarketTypeSpot {
		if source.InstrumentType != "SPOT" {
			return marketdata.InstrumentMetadata{}, protocolError("invalid spot instrument")
		}
	} else {
		if source.InstrumentType != "SWAP" || source.ContractType != "linear" || source.SettleCurrency != "USDT" {
			return marketdata.InstrumentMetadata{}, protocolError("invalid perpetual contract")
		}
		baseAsset = source.ContractValue
		quoteAsset = source.SettleCurrency
	}
	status, err := mapStatus(source.State)
	if err != nil {
		return marketdata.InstrumentMetadata{}, err
	}
	priceTick, err := marketdata.ParseDecimal(source.TickSize)
	if err != nil {
		return marketdata.InstrumentMetadata{}, protocolError("invalid price tick")
	}
	quantityStep, err := marketdata.ParseDecimal(source.LotSize)
	if err != nil {
		return marketdata.InstrumentMetadata{}, protocolError("invalid quantity step")
	}
	item := marketdata.InstrumentMetadata{
		Venue:        marketdata.VenueOKX,
		MarketType:   marketType,
		NativeSymbol: source.InstrumentID,
		BaseAsset:    baseAsset,
		QuoteAsset:   quoteAsset,
		Status:       status,
		PriceTick:    priceTick,
		QuantityStep: quantityStep,
	}
	if err := marketdata.ValidateInstrumentMetadata(item); err != nil {
		return marketdata.InstrumentMetadata{}, protocolError("invalid instrument metadata")
	}
	return item, nil
}

func mapStatus(state string) (marketdata.InstrumentStatus, error) {
	switch state {
	case "live":
		return marketdata.InstrumentStatusTrading, nil
	case "suspend", "preopen", "test":
		return marketdata.InstrumentStatusSuspended, nil
	default:
		return "", protocolError("unknown instrument status")
	}
}

func normalizeCandleRow(row json.RawMessage, instrument marketdata.Instrument, interval marketdata.CandleInterval, requireClosed bool) (marketdata.Candle, error) {
	var values []json.RawMessage
	if err := json.Unmarshal(row, &values); err != nil || len(values) < 9 {
		return marketdata.Candle{}, protocolError("invalid candle row")
	}
	openMillisText, ok := decodeString(values[0])
	if !ok {
		return marketdata.Candle{}, protocolError("invalid candle open time")
	}
	openText, ok := decodeString(values[1])
	if !ok {
		return marketdata.Candle{}, protocolError("invalid candle open price")
	}
	highText, ok := decodeString(values[2])
	if !ok {
		return marketdata.Candle{}, protocolError("invalid candle high price")
	}
	lowText, ok := decodeString(values[3])
	if !ok {
		return marketdata.Candle{}, protocolError("invalid candle low price")
	}
	closeText, ok := decodeString(values[4])
	if !ok {
		return marketdata.Candle{}, protocolError("invalid candle close price")
	}
	volumeIndex := 5
	if instrument.MarketType == marketdata.MarketTypeUSDTPerpetual {
		// OKX 永续的 vol 是合约张数，volCcy 才是基础资产成交量。
		volumeIndex = 6
	}
	volumeText, ok := decodeString(values[volumeIndex])
	if !ok {
		return marketdata.Candle{}, protocolError("invalid candle volume")
	}
	confirm, ok := decodeString(values[8])
	if !ok || confirm != "0" && confirm != "1" {
		return marketdata.Candle{}, protocolError("invalid candle confirmation")
	}
	if requireClosed && confirm != "1" {
		return marketdata.Candle{}, protocolError("historical candle is not closed")
	}
	openMillis, ok := parseStringMillis(openMillisText)
	if !ok {
		return marketdata.Candle{}, protocolError("invalid candle open time")
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
	duration, _ := marketdata.CandleIntervalDuration(interval)
	candle := marketdata.Candle{
		// OKX 只提供开盘毫秒，领域收盘边界始终由冻结 interval 在 UTC 下推导。
		Venue:        marketdata.VenueOKX,
		InstrumentID: instrument.ID,
		Interval:     interval,
		OpenTime:     time.UnixMilli(openMillis).UTC(),
		CloseTime:    time.UnixMilli(openMillis).UTC().Add(duration),
		Open:         open,
		High:         high,
		Low:          low,
		Close:        close,
		BaseVolume:   volume,
		IsClosed:     confirm == "1",
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

func parseStringMillis(text string) (int64, bool) {
	if !millisecondTextPattern.MatchString(text) {
		return 0, false
	}
	value, err := strconv.ParseInt(text, 10, 64)
	return value, err == nil
}

func candleChannel(interval marketdata.CandleInterval) (string, bool) {
	switch interval {
	case marketdata.CandleInterval1m:
		return "candle1m", true
	case marketdata.CandleInterval5m:
		return "candle5m", true
	case marketdata.CandleInterval15m:
		return "candle15m", true
	case marketdata.CandleInterval1h:
		return "candle1H", true
	case marketdata.CandleInterval4h:
		return "candle4H", true
	case marketdata.CandleInterval1d:
		return "candle1Dutc", true
	default:
		return "", false
	}
}

func validateCandleRequest(request marketdata.CandlePageRequest) error {
	if err := marketdata.ValidateCandlePageRequest(request); err != nil {
		return invalidRequestError("invalid candle request")
	}
	if request.Instrument.Venue != marketdata.VenueOKX {
		return invalidRequestError("instrument venue does not match OKX")
	}
	return nil
}

func validateInstrumentAndInterval(instrument marketdata.Instrument, interval marketdata.CandleInterval) error {
	if err := validateInstrument(instrument); err != nil {
		return err
	}
	if _, ok := candleChannel(interval); !ok {
		return invalidRequestError("invalid candle interval")
	}
	return nil
}

func validateInstrument(instrument marketdata.Instrument) error {
	if err := marketdata.ValidateInstrument(instrument); err != nil {
		return invalidRequestError("invalid instrument")
	}
	if instrument.Venue != marketdata.VenueOKX {
		return invalidRequestError("instrument venue does not match OKX")
	}
	return nil
}

func protocolError(message string) error {
	return &marketdata.SourceError{Kind: marketdata.SourceErrorProtocol, Err: errors.New(message)}
}

func invalidRequestError(message string) error {
	return &marketdata.SourceError{Kind: marketdata.SourceErrorInvalidRequest, Err: errors.New(message)}
}
