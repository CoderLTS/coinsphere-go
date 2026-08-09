package service

import (
	"testing"
	"time"

	"coinsphere/backend/internal/db"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestSerializeTestnetOrderIncludesProtectiveOrderShape(t *testing.T) {
	replacesOrderID := uuid.New()
	row := db.TestnetOrder{
		ID: uuid.New(), AccountID: uuid.New(), IntentID: uuid.New(), InstrumentID: uuid.New(),
		ClientOrderID: "cs-p-order", Side: "sell", Quantity: decimal.NewFromInt(2),
		Purpose: "protection", OrderType: "stop_market", StopPrice: decimal.RequireFromString("49000.25"),
		ClosePosition: true, WorkingType: "mark_price", ReplacesOrderID: &replacesOrderID,
		Status: "new", SubmittedAt: time.Unix(1, 0).UTC(), CreatedAt: time.Unix(2, 0).UTC(),
		UpdatedAt: time.Unix(3, 0).UTC(),
	}

	view := serializeTestnetOrder(row, "BTCUSDT")
	if view.Purpose != row.Purpose || view.OrderType != row.OrderType || view.StopPrice != "49000.25" ||
		!view.ClosePosition || view.ReduceOnly || view.WorkingType != row.WorkingType ||
		view.ReplacesOrderID == nil || *view.ReplacesOrderID != replacesOrderID.String() {
		t.Fatalf("protective Testnet order view = %#v", view)
	}
}

func TestSerializeTestnetTradeFactUsesStringAmountsAndIDs(t *testing.T) {
	orderID := uuid.New()
	intentID := uuid.New()
	externalTradeID := int64(9007199254740991)
	row := db.TestnetTradeFact{
		ID: 42, AccountID: uuid.New(), CredentialUpdatedAt: time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC),
		OrderID: &orderID, IntentID: &intentID, EventType: "fee", Symbol: "BTCUSDT",
		ExternalTradeID: &externalTradeID, ExternalTransactionID: "tx-1", Side: "buy", PositionSide: "both",
		Quantity: decimal.RequireFromString("0.010000000000000001"), Price: decimal.NewFromInt(50000),
		QuoteQuantity: decimal.NewFromInt(500), Amount: decimal.RequireFromString("0.20"), Asset: "USDT",
		RealizedPnL: decimal.Zero, Buyer: true, Maker: false,
		OccurredAt: time.Date(2026, 8, 10, 1, 2, 4, 0, time.UTC), CreatedAt: time.Date(2026, 8, 10, 1, 2, 5, 0, time.UTC),
	}

	view := serializeTestnetTradeFact(row)
	if view.ID != "42" || view.AccountID != row.AccountID.String() || view.EventType != "fee" ||
		view.Quantity != "0.010000000000000001" || view.Amount != "0.2" || view.Asset != "USDT" ||
		view.ExternalTradeID == nil || *view.ExternalTradeID != "9007199254740991" ||
		view.OrderID == nil || *view.OrderID != orderID.String() || view.IntentID == nil || *view.IntentID != intentID.String() {
		t.Fatalf("Testnet trade fact view = %#v", view)
	}
}
