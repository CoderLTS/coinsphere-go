package quant

import (
	"time"

	"github.com/shopspring/decimal"
)

type quantCandle struct {
	Venue      string
	Market     string
	Instrument string
	Interval   string
	OpenTime   time.Time
	CloseTime  time.Time
	Open       decimal.Decimal
	High       decimal.Decimal
	Low        decimal.Decimal
	Close      decimal.Decimal
	Volume     decimal.Decimal
}

type quantBacktest struct {
	ID              int64
	OperationKey    string
	WorkflowID      int64
	RevisionID      int64
	NodeInstanceID  string
	StrategyID      string
	StrategyVersion string
	Venue           string
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
	Detail          string
	CreatedAt       time.Time
}

func (quantBacktest) TableName() string { return "plugin_quant.backtests" }

type quantMarketSignal struct {
	ID              int64
	OperationKey    string
	WorkflowID      int64
	RevisionID      int64
	NodeInstanceID  string
	Venue           string
	Market          string
	Instrument      string
	Interval        string
	Name            string
	Indicator       string
	CandleCloseTime time.Time
	Summary         string
	Values          string
	CreatedAt       time.Time
}

func (quantMarketSignal) TableName() string { return "plugin_quant.market_signals" }

type quantSignal struct {
	ID              int64
	OperationKey    string
	WorkflowID      int64
	RevisionID      int64
	NodeInstanceID  string
	StrategyID      string
	StrategyVersion string
	Venue           string
	Market          string
	Instrument      string
	BusinessKey     string
	Target          decimal.Decimal
	EvaluatedAt     time.Time
	Status          string
	SupersededBy    *int64
	CreatedAt       time.Time
	DecidedAt       *time.Time
}

func (quantSignal) TableName() string { return "plugin_quant.signals" }
