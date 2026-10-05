// Package trading contains financial contracts owned by the trading plugins.
package trading

import (
	"context"
	"encoding/json"
	"github.com/shopspring/decimal"
	"time"
)

type SecretReader interface {
	Read(context.Context, string) ([]byte, error)
}
type Candle struct {
	OpenTime  time.Time
	CloseTime time.Time
	Open      decimal.Decimal
	High      decimal.Decimal
	Low       decimal.Decimal
	Close     decimal.Decimal
	Volume    decimal.Decimal
}

type Instrument struct {
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

type InstrumentQuery struct {
	Markets     []string
	Instruments []string
	Limit       int
	ProxyID     int64
}

type CandleQuery struct {
	Market     string
	Instrument string
	Interval   string
	StartTime  time.Time
	EndTime    time.Time
	Limit      int
	ProxyID    int64
}

type QuoteQuery struct {
	Market     string
	Instrument string
	ProxyID    int64
}

type Quote struct {
	Price    decimal.Decimal
	QuotedAt time.Time
}

type MarketDataProvider interface {
	ID() string
	Instruments(context.Context, InstrumentQuery) ([]Instrument, error)
	Candles(context.Context, CandleQuery) ([]Candle, error)
	Quote(context.Context, QuoteQuery) (Quote, error)
}

type MarketDataRegistry interface {
	MarketDataProvider(string) (MarketDataProvider, bool)
}

type OrderRequest struct {
	Account        string
	Market         string
	Instrument     string
	Side           string
	Quantity       decimal.Decimal
	QuoteAmount    decimal.Decimal
	PositionEffect string
	ClientOrderID  string
	Secrets        SecretReader
	ProxyID        int64
}

type OrderQuery struct {
	Account       string
	Market        string
	Instrument    string
	OrderID       string
	ClientOrderID string
	Secrets       SecretReader
	ProxyID       int64
}

type CancelOrderRequest = OrderQuery

type OrderResult struct {
	ProviderOrderID string
	ClientOrderID   string
	Status          string
	Market          string
	Instrument      string
	Side            string
	Quantity        decimal.Decimal
	Executed        decimal.Decimal
	AveragePrice    decimal.Decimal
	UpdatedAt       time.Time
}

type ExecutionProvider interface {
	ID() string
	PlaceOrder(context.Context, OrderRequest) (OrderResult, error)
	GetOrder(context.Context, OrderQuery) (OrderResult, error)
	CancelOrder(context.Context, CancelOrderRequest) error
}

type ExecutionRegistry interface {
	ExecutionProvider(string) (ExecutionProvider, bool)
}

type StrategyRegistry interface {
	Strategy(string) (StrategyDescriptor, Strategy, bool)
	Strategies() []StrategyDescriptor
}

type EvaluateRequest struct {
	Market      string
	Instrument  string
	Interval    string
	Candles     []Candle
	Parameters  json.RawMessage
	EvaluatedAt time.Time
}

type StrategyDescriptor struct {
	ID              string
	Version         string
	Name            string
	ParameterSchema json.RawMessage
	MinimumLookback int
}

type Strategy interface {
	Descriptor() StrategyDescriptor
	Evaluate(context.Context, EvaluateRequest) (decimal.Decimal, error)
}
