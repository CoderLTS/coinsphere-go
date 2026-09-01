package binance

import (
	"time"

	"github.com/shopspring/decimal"
)

type tradingOrder struct {
	ID                 int64
	WorkflowID         int64
	NodeInstanceID     string
	Account            string
	Market             string
	Instrument         string
	ProviderOrderID    string
	ClientOrderID      string
	Side               string
	RequestQuantity    decimal.Decimal
	RequestQuoteAmount decimal.Decimal
	PositionEffect     string
	Quantity           decimal.Decimal
	Executed           decimal.Decimal
	AveragePrice       decimal.Decimal
	Notional           decimal.Decimal
	Status             string
	Mode               string
	OperationKey       string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

func (tradingOrder) TableName() string { return "plugin_binance.orders" }

type tradingFill struct {
	ID              int64
	OrderID         int64
	ProviderTradeID string
	Quantity        decimal.Decimal
	Price           decimal.Decimal
	Fee             decimal.Decimal
	FeeAsset        string
	FilledAt        time.Time
}

func (tradingFill) TableName() string { return "plugin_binance.fills" }

type tradingFee struct {
	ID        int64
	FillID    int64
	Amount    decimal.Decimal
	Asset     string
	CreatedAt time.Time
}

func (tradingFee) TableName() string { return "plugin_binance.fees" }

type paperLedgerEntry struct {
	ID           int64
	Account      string
	OperationKey string
	EntryType    string
	Amount       decimal.Decimal
	OccurredAt   time.Time
}

func (paperLedgerEntry) TableName() string { return "plugin_binance.paper_ledger_entries" }

type tradingPosition struct {
	Account      string
	Mode         string
	Market       string
	Instrument   string
	Quantity     decimal.Decimal
	AveragePrice decimal.Decimal
	UpdatedAt    time.Time
}

func (tradingPosition) TableName() string { return "plugin_binance.positions" }

type accountSnapshot struct {
	ID         int64
	Account    string
	Market     string
	Asset      string
	Equity     decimal.Decimal
	Available  decimal.Decimal
	CapturedAt time.Time
}

func (accountSnapshot) TableName() string { return "plugin_binance.account_snapshots" }

type liveAccountRelease struct {
	Account     string
	Market      string
	Enabled     bool
	ConfirmedBy int64
	ConfirmedAt time.Time
	UpdatedAt   time.Time
}

func (liveAccountRelease) TableName() string { return "plugin_binance.live_account_releases" }
