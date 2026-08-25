package official

import (
	"time"

	"github.com/shopspring/decimal"
)

type quantInstrument struct {
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

func (quantInstrument) TableName() string { return "plugin_quant.instruments" }

type quantCandle struct {
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

func (quantCandle) TableName() string { return "plugin_quant.candles" }

type quantBacktest struct {
	ID              int64
	OperationKey    string
	WorkflowID      int64
	RevisionID      int64
	NodeInstanceID  string
	StrategyID      string
	StrategyVersion string
	Market          string
	Instrument      string
	Interval        string
	StartTime       time.Time
	EndTime         time.Time
	InitialCapital  decimal.Decimal
	FinalEquity     decimal.Decimal
	TotalReturn     decimal.Decimal
	MaxDrawdown     decimal.Decimal
	TotalFees       decimal.Decimal
	TradeCount      int
	CandleCount     int
	Parameters      string
	DataManifest    string
	DetailSHA256    string
	DetailSizeBytes int64
	CreatedAt       time.Time
}

func (quantBacktest) TableName() string { return "plugin_quant.backtests" }
