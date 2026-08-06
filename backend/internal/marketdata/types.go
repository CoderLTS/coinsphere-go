// Package marketdata 定义行情领域的冻结公共契约。
package marketdata

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type Venue string

const (
	VenueBinance Venue = "binance"
)

type MarketType string

const (
	MarketTypeSpot MarketType = "spot"
	MarketTypeUSDM MarketType = "usd_m"
)

type InstrumentStatus string

const (
	InstrumentStatusTrading   InstrumentStatus = "trading"
	InstrumentStatusSuspended InstrumentStatus = "suspended"
)

type CandleInterval string

const (
	CandleInterval1m  CandleInterval = "1m"
	CandleInterval5m  CandleInterval = "5m"
	CandleInterval15m CandleInterval = "15m"
	CandleInterval1h  CandleInterval = "1h"
	CandleInterval4h  CandleInterval = "4h"
	CandleInterval1d  CandleInterval = "1d"
)

// InstrumentMetadata 是交易所元数据快照项；内部 ID 只能由持久化边界首次创建。
type InstrumentMetadata struct {
	Venue        Venue            `json:"venue"`
	MarketType   MarketType       `json:"marketType"`
	NativeSymbol string           `json:"nativeSymbol"`
	BaseAsset    string           `json:"baseAsset"`
	QuoteAsset   string           `json:"quoteAsset"`
	Status       InstrumentStatus `json:"status"`
	PriceTick    decimal.Decimal  `json:"priceTick"`
	QuantityStep decimal.Decimal  `json:"quantityStep"`
	MinQuantity  decimal.Decimal  `json:"minQuantity"`
	MinNotional  decimal.Decimal  `json:"minNotional"`
	UpdatedAt    time.Time        `json:"updatedAt"`
}

type Instrument struct {
	ID           uuid.UUID        `json:"id"`
	Venue        Venue            `json:"venue"`
	MarketType   MarketType       `json:"marketType"`
	NativeSymbol string           `json:"nativeSymbol"`
	BaseAsset    string           `json:"baseAsset"`
	QuoteAsset   string           `json:"quoteAsset"`
	Status       InstrumentStatus `json:"status"`
	PriceTick    decimal.Decimal  `json:"priceTick"`
	QuantityStep decimal.Decimal  `json:"quantityStep"`
	MinQuantity  decimal.Decimal  `json:"minQuantity"`
	MinNotional  decimal.Decimal  `json:"minNotional"`
	UpdatedAt    time.Time        `json:"updatedAt"`
}

type Candle struct {
	Venue        Venue           `json:"venue"`
	InstrumentID uuid.UUID       `json:"instrumentId"`
	Interval     CandleInterval  `json:"interval"`
	OpenTime     time.Time       `json:"openTime"`
	CloseTime    time.Time       `json:"closeTime"`
	Open         decimal.Decimal `json:"open"`
	High         decimal.Decimal `json:"high"`
	Low          decimal.Decimal `json:"low"`
	Close        decimal.Decimal `json:"close"`
	BaseVolume   decimal.Decimal `json:"baseVolume"`
	IsClosed     bool            `json:"isClosed"`
}

// CandleWriteResult 说明本次 Upsert 是否真正写入，以及是否首次把该 K 线闭合。
type CandleWriteResult struct {
	Changed     bool
	FirstClosed bool
}

type Ticker struct {
	Venue        Venue           `json:"venue"`
	InstrumentID uuid.UUID       `json:"instrumentId"`
	OccurredAt   time.Time       `json:"occurredAt"`
	LastPrice    decimal.Decimal `json:"lastPrice"`
	BestBidPrice decimal.Decimal `json:"bestBidPrice"`
	BestAskPrice decimal.Decimal `json:"bestAskPrice"`
}

type CandleCursor string

type CandlePageRequest struct {
	Instrument Instrument     `json:"instrument"`
	Interval   CandleInterval `json:"interval"`
	StartTime  time.Time      `json:"startTime"`
	EndTime    time.Time      `json:"endTime"`
	Limit      int            `json:"limit"`
	Cursor     CandleCursor   `json:"cursor"`
}

type CandlePage struct {
	Candles    []Candle     `json:"candles"`
	NextCursor CandleCursor `json:"nextCursor"`
}

type CandleHandler func(Candle) error
type TickerHandler func(Ticker) error

// MarketSource 抽取 Binance public 行情生命周期，不是动态插件扩展点。
type MarketSource interface {
	SnapshotInstruments(ctx context.Context, marketType MarketType) ([]InstrumentMetadata, error)
	FetchCandlePage(ctx context.Context, request CandlePageRequest) (CandlePage, error)
	SubscribeCandles(ctx context.Context, instrument Instrument, interval CandleInterval, handle CandleHandler) error
	SubscribeTickers(ctx context.Context, instrument Instrument, handle TickerHandler) error
}

type SourceErrorKind string

const (
	SourceErrorInvalidRequest SourceErrorKind = "invalid_request"
	SourceErrorRateLimited    SourceErrorKind = "rate_limited"
	SourceErrorUnavailable    SourceErrorKind = "unavailable"
	SourceErrorProtocol       SourceErrorKind = "protocol"
)

type SourceError struct {
	Kind       SourceErrorKind
	RetryAfter time.Duration
	Err        error
}

// Error 不透出交易所原始内容，细节只供受控的 errors.Unwrap 调用链使用。
func (errorValue SourceError) Error() string {
	switch errorValue.Kind {
	case SourceErrorInvalidRequest, SourceErrorRateLimited, SourceErrorUnavailable, SourceErrorProtocol:
		return "market source " + string(errorValue.Kind)
	default:
		return "market source error"
	}
}

func (errorValue SourceError) Unwrap() error {
	return errorValue.Err
}

func (errorValue SourceError) Retryable() bool {
	if errorValue.RetryAfter < 0 || errorValue.Kind != SourceErrorRateLimited && errorValue.RetryAfter != 0 {
		return false
	}
	return errorValue.Kind == SourceErrorRateLimited || errorValue.Kind == SourceErrorUnavailable
}
