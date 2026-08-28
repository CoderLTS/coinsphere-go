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

type quantInstrumentSource struct {
	WorkflowID int64
	Market     string
	Symbol     string
	SyncedAt   time.Time
}

func (quantInstrumentSource) TableName() string { return "plugin_quant.instrument_sources" }

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

type quantSignal struct {
	ID                int64
	OperationKey      string
	PaperOperationKey *string
	WorkflowID        int64
	RevisionID        int64
	NodeInstanceID    string
	StrategyID        string
	StrategyVersion   string
	Market            string
	Instrument        string
	BusinessKey       string
	Target            decimal.Decimal
	EvaluatedAt       time.Time
	Status            string
	DecisionTaskID    *int64
	SupersededBy      *int64
	RejectionReason   *string
	CreatedAt         time.Time
	DecidedAt         *time.Time
	ExecutedAt        *time.Time
}

func (quantSignal) TableName() string { return "plugin_quant.signals" }

type quantPaperAccount struct {
	ID             int64
	WorkflowID     int64
	NodeInstanceID string
	Status         string
	InitialBalance decimal.Decimal
	CashBalance    decimal.Decimal
	Equity         decimal.Decimal
	PeakEquity     decimal.Decimal
	DayStartEquity decimal.Decimal
	DayStartDate   time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (quantPaperAccount) TableName() string { return "plugin_quant.paper_accounts" }

type quantPaperOrder struct {
	ID           int64
	AccountID    int64
	SignalID     int64
	OperationKey string
	Market       string
	Instrument   string
	Side         string
	Quantity     decimal.Decimal
	QuotePrice   decimal.Decimal
	Notional     decimal.Decimal
	Status       string
	QuotedAt     time.Time
	CashAfter    decimal.Decimal
	EquityAfter  decimal.Decimal
	CreatedAt    time.Time
}

func (quantPaperOrder) TableName() string { return "plugin_quant.paper_orders" }

type quantPaperFill struct {
	ID            int64
	OrderID       int64
	OperationKey  string
	QuantityDelta decimal.Decimal
	Price         decimal.Decimal
	Notional      decimal.Decimal
	FilledAt      time.Time
}

func (quantPaperFill) TableName() string { return "plugin_quant.paper_fills" }

type quantPaperFee struct {
	ID           int64
	FillID       int64
	OperationKey string
	Amount       decimal.Decimal
	CreatedAt    time.Time
}

func (quantPaperFee) TableName() string { return "plugin_quant.paper_fees" }

type quantPaperLedgerEntry struct {
	ID           int64
	AccountID    int64
	OperationKey string
	EntryType    string
	Amount       decimal.Decimal
	OccurredAt   time.Time
}

func (quantPaperLedgerEntry) TableName() string { return "plugin_quant.paper_ledger_entries" }

type quantPaperPosition struct {
	AccountID    int64
	Market       string
	Instrument   string
	Quantity     decimal.Decimal
	AveragePrice decimal.Decimal
	LastPrice    decimal.Decimal
	UpdatedAt    time.Time
}

func (quantPaperPosition) TableName() string { return "plugin_quant.paper_positions" }
