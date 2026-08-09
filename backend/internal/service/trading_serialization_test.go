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
