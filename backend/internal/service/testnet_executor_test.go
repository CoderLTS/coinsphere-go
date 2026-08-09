package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"coinsphere/backend/internal/db"
	exchangebinance "coinsphere/backend/internal/exchange/binance"
	"coinsphere/backend/internal/marketdata"
	"coinsphere/backend/internal/security"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

func TestTestnetExecutorExecutesDeterministicSpotAndUSDMOrders(t *testing.T) {
	for _, market := range []marketdata.MarketType{marketdata.MarketTypeSpot, marketdata.MarketTypeUSDM} {
		t.Run(string(market), func(t *testing.T) {
			client := &scriptedTestnetOrderClient{}
			fixture := newTestnetExecutorFixture(t, market, client)
			intent := fixture.enqueue(t, "0.5")
			client.place = func(call testnetOrderCall) (exchangebinance.OrderResult, error) {
				return filledTestnetResult(call, 41), nil
			}

			processTestnetSteps(t, fixture.executor, 2, "execute "+string(market)+" Testnet order")
			assertTradingIntentState(t, fixture.database, intent.ID, "executed", "")
			calls := client.snapshotCalls()
			if len(calls) != 2 || calls[0].operation != "place" || calls[0].market != market ||
				calls[0].clientOrderID != intent.ClientOrderID || calls[0].side != "buy" ||
				!calls[0].quantity.Equal(decimal.NewFromInt(5)) || calls[0].reduceOnly {
				t.Fatalf("%s Testnet calls = %#v", market, calls)
			}
			protection := calls[1]
			if protection.operation != "protect" || protection.side != "sell" ||
				!protection.stopPrice.Equal(decimal.NewFromInt(95)) {
				t.Fatalf("%s Testnet protection call = %#v", market, protection)
			}
			if market == marketdata.MarketTypeSpot &&
				(protection.orderType != "stop_loss" || !protection.quantity.Equal(decimal.NewFromInt(5)) ||
					protection.closePosition || protection.workingType != "") {
				t.Fatalf("Spot protection shape = %#v", protection)
			}
			if market == marketdata.MarketTypeUSDM &&
				(protection.orderType != "stop_market" || !protection.quantity.IsZero() ||
					!protection.closePosition || protection.workingType != "mark_price") {
				t.Fatalf("USD-M protection shape = %#v", protection)
			}
			var order db.TestnetOrder
			if err := fixture.database.Where("intent_id = ? AND purpose = 'rebalance'", intent.ID).Take(&order).Error; err != nil {
				t.Fatalf("load %s Testnet order: %v", market, err)
			}
			if order.Status != "filled" || order.ExchangeOrderID == nil || *order.ExchangeOrderID != 41 ||
				order.ClientOrderID != intent.ClientOrderID || !order.FilledQuantity.Equal(decimal.NewFromInt(5)) ||
				!order.AveragePrice.Equal(decimal.NewFromInt(100)) || order.SubmitAttemptCount != 1 ||
				order.QueryAttemptCount != 0 || order.Purpose != "rebalance" || order.OrderType != "market" {
				t.Fatalf("stored %s Testnet order = %#v", market, order)
			}
			var storedProtection db.TestnetOrder
			if err := fixture.database.Where("intent_id = ? AND purpose = 'protection'", intent.ID).
				Take(&storedProtection).Error; err != nil {
				t.Fatalf("load %s Testnet protection: %v", market, err)
			}
			if storedProtection.Status != "new" || !storedProtection.StopPrice.Equal(decimal.NewFromInt(95)) {
				t.Fatalf("stored %s Testnet protection = %#v", market, storedProtection)
			}
			var risk db.TestnetRiskState
			if err := fixture.database.Where("account_id = ?", fixture.account.ID).Take(&risk).Error; err != nil {
				t.Fatalf("load %s Testnet risk state: %v", market, err)
			}
			if !risk.Equity.Equal(decimal.RequireFromString("9999.5")) {
				t.Fatalf("%s Testnet equity = %s, want 9999.5", market, risk.Equity)
			}
		})
	}
}

func TestTestnetExecutorMarksUSDMReductionsReduceOnly(t *testing.T) {
	client := &scriptedTestnetOrderClient{}
	fixture := newTestnetExecutorFixture(t, marketdata.MarketTypeUSDM, client)
	var orderID int64 = 50
	client.place = func(call testnetOrderCall) (exchangebinance.OrderResult, error) {
		orderID++
		return filledTestnetResult(call, orderID), nil
	}

	fixture.enqueue(t, "0.5")
	processTestnetSteps(t, fixture.executor, 2, "open USD-M Testnet position")
	fixture.enqueue(t, "0.2")
	processTestnetSteps(t, fixture.executor, 3, "reduce USD-M Testnet position")
	calls := client.snapshotCalls()
	if len(calls) != 5 || calls[2].operation != "place" || calls[2].side != "sell" ||
		!calls[2].quantity.Equal(decimal.NewFromInt(3)) || !calls[2].reduceOnly ||
		calls[3].operation != "cancel" || calls[4].operation != "protect" {
		t.Fatalf("USD-M reduction calls = %#v", calls)
	}
}

func TestTestnetExecutorReplacesSpotProtectionForChangedPosition(t *testing.T) {
	client := &scriptedTestnetOrderClient{}
	fixture := newTestnetExecutorFixture(t, marketdata.MarketTypeSpot, client)
	var orderID int64 = 100
	client.place = func(call testnetOrderCall) (exchangebinance.OrderResult, error) {
		orderID++
		return filledTestnetResult(call, orderID), nil
	}

	fixture.enqueue(t, "0.5")
	processTestnetSteps(t, fixture.executor, 2, "open Spot Testnet position")
	secondIntent := fixture.enqueue(t, "0.2")
	processTestnetSteps(t, fixture.executor, 3, "resize Spot Testnet position")
	assertTradingIntentState(t, fixture.database, secondIntent.ID, "executed", "")

	calls := client.snapshotCalls()
	if len(calls) != 5 || calls[1].operation != "protect" ||
		!calls[1].quantity.Equal(decimal.NewFromInt(5)) || calls[3].operation != "cancel" ||
		calls[3].clientOrderID != calls[1].clientOrderID || calls[4].operation != "protect" ||
		!calls[4].quantity.Equal(decimal.NewFromInt(2)) {
		t.Fatalf("Spot protection replacement calls = %#v", calls)
	}
	var protections []db.TestnetOrder
	if err := fixture.database.Where("account_id = ? AND purpose = 'protection'", fixture.account.ID).
		Order("created_at, id").Find(&protections).Error; err != nil {
		t.Fatalf("load Spot protection replacements: %v", err)
	}
	if len(protections) != 2 || protections[0].Status != "canceled" || protections[1].Status != "new" ||
		protections[1].ReplacesOrderID == nil || *protections[1].ReplacesOrderID != protections[0].ID ||
		!protections[1].Quantity.Equal(decimal.NewFromInt(2)) {
		t.Fatalf("stored Spot protection replacements = %#v", protections)
	}
}

func TestTestnetExecutorFlattensAndPausesWhenProtectionCannotBeConfirmed(t *testing.T) {
	client := &scriptedTestnetOrderClient{}
	fixture := newTestnetExecutorFixture(t, marketdata.MarketTypeSpot, client)
	intent := fixture.enqueue(t, "0.5")
	client.place = func(call testnetOrderCall) (exchangebinance.OrderResult, error) {
		return filledTestnetResult(call, 201), nil
	}
	client.protect = func(testnetOrderCall) (exchangebinance.OrderResult, error) {
		return exchangebinance.OrderResult{}, &exchangebinance.PrivateError{Kind: exchangebinance.PrivateErrorUnavailable}
	}
	client.query = func(testnetOrderCall) (exchangebinance.OrderResult, error) {
		return exchangebinance.OrderResult{}, &exchangebinance.PrivateError{Kind: exchangebinance.PrivateErrorNotFound}
	}

	processTestnetSteps(t, fixture.executor, 2, "submit uncertain Spot protection")
	assertTradingIntentState(t, fixture.database, intent.ID, "reconciling", "exchange_unavailable")
	processTestnetSteps(t, fixture.executor, 1, "flatten unprotected Spot position")
	assertTradingIntentState(t, fixture.database, intent.ID, "failed", "protection_failed_flattened")

	calls := client.snapshotCalls()
	if len(calls) != 4 || calls[0].operation != "place" || calls[1].operation != "protect" ||
		calls[2].operation != "query" || calls[3].operation != "place" || calls[3].side != "sell" ||
		!calls[3].quantity.Equal(decimal.NewFromInt(5)) || calls[3].reduceOnly {
		t.Fatalf("protection failure calls = %#v", calls)
	}
	var account db.TradingAccount
	if err := fixture.database.Where("id = ?", fixture.account.ID).Take(&account).Error; err != nil {
		t.Fatalf("load protection-failed account: %v", err)
	}
	var instance db.StrategyInstance
	if err := fixture.database.Where("id = ?", fixture.base.instanceID).Take(&instance).Error; err != nil {
		t.Fatalf("load protection-failed instance: %v", err)
	}
	if account.Status != "paused" || account.PauseReason != "protection_not_found" ||
		account.AutomationEnabled || instance.IsEnabled {
		t.Fatalf("protection failure safety state: account=%#v instance=%#v", account, instance)
	}
	var notification db.SystemNotifyDelivery
	if err := fixture.database.Where("recipient_user_id = ? AND target_type = 'testnet_safety'", account.OwnerUserID).
		Take(&notification).Error; err != nil {
		t.Fatalf("load protection failure notification: %v", err)
	}
	if notification.Status != "success" || notification.ChannelType != "in_app" ||
		!strings.Contains(notification.Content, "protection_not_found") {
		t.Fatalf("protection failure notification = %#v", notification)
	}
	var flatten db.TestnetOrder
	if err := fixture.database.Where("intent_id = ? AND purpose = 'flatten'", intent.ID).Take(&flatten).Error; err != nil {
		t.Fatalf("load emergency flatten order: %v", err)
	}
	if flatten.Status != "filled" || !flatten.FilledQuantity.Equal(decimal.NewFromInt(5)) {
		t.Fatalf("emergency flatten order = %#v", flatten)
	}
}

func TestTestnetExecutorFlattensPartialTerminalRebalance(t *testing.T) {
	client := &scriptedTestnetOrderClient{}
	fixture := newTestnetExecutorFixture(t, marketdata.MarketTypeSpot, client)
	intent := fixture.enqueue(t, "0.5")
	placements := 0
	client.place = func(call testnetOrderCall) (exchangebinance.OrderResult, error) {
		placements++
		if placements == 1 {
			result := filledTestnetResult(call, 211)
			result.Status = "canceled"
			result.ExecutedQuantity = decimal.NewFromInt(2)
			result.CumulativeQuoteQuantity = decimal.NewFromInt(200)
			return result, nil
		}
		return filledTestnetResult(call, 212), nil
	}

	processTestnetSteps(t, fixture.executor, 1, "flatten partially filled terminal rebalance")
	assertTradingIntentState(t, fixture.database, intent.ID, "failed", "protection_failed_flattened")
	calls := client.snapshotCalls()
	if len(calls) != 2 || calls[0].operation != "place" || calls[1].operation != "place" ||
		calls[1].side != "sell" || !calls[1].quantity.Equal(decimal.NewFromInt(2)) {
		t.Fatalf("partial terminal rebalance calls = %#v", calls)
	}
}

func TestTestnetExecutorFlattensWhenProtectionResultIsInvalid(t *testing.T) {
	client := &scriptedTestnetOrderClient{}
	fixture := newTestnetExecutorFixture(t, marketdata.MarketTypeSpot, client)
	intent := fixture.enqueue(t, "0.5")
	placements := 0
	client.place = func(call testnetOrderCall) (exchangebinance.OrderResult, error) {
		placements++
		return filledTestnetResult(call, 221+int64(placements)), nil
	}
	client.protect = func(call testnetOrderCall) (exchangebinance.OrderResult, error) {
		result := testnetProtectiveResult(call, 224, "new")
		result.StopPrice = decimal.NewFromInt(96)
		return result, nil
	}

	processTestnetSteps(t, fixture.executor, 2, "flatten invalid protection result")
	assertTradingIntentState(t, fixture.database, intent.ID, "failed", "protection_failed_flattened")
	calls := client.snapshotCalls()
	if len(calls) != 3 || calls[0].operation != "place" || calls[1].operation != "protect" ||
		calls[2].operation != "place" || calls[2].side != "sell" ||
		!calls[2].quantity.Equal(decimal.NewFromInt(5)) {
		t.Fatalf("invalid protection result calls = %#v", calls)
	}
	var protection db.TestnetOrder
	if err := fixture.database.Where("intent_id = ? AND purpose = 'protection'", intent.ID).
		Take(&protection).Error; err != nil {
		t.Fatalf("load invalid protection order: %v", err)
	}
	if protection.Status != "unknown" || protection.LastErrorCode != "protection_order_protocol_error" {
		t.Fatalf("invalid protection order state = %#v", protection)
	}
}

func TestTestnetExecutorQueriesUnknownFlattenBeforeRecovery(t *testing.T) {
	client := &scriptedTestnetOrderClient{}
	fixture := newTestnetExecutorFixture(t, marketdata.MarketTypeUSDM, client)
	intent := fixture.enqueue(t, "0.5")
	marketPlacements := 0
	client.place = func(call testnetOrderCall) (exchangebinance.OrderResult, error) {
		marketPlacements++
		if marketPlacements == 2 {
			return exchangebinance.OrderResult{}, &exchangebinance.PrivateError{Kind: exchangebinance.PrivateErrorUnavailable}
		}
		return filledTestnetResult(call, 300+int64(marketPlacements)), nil
	}
	client.protect = func(testnetOrderCall) (exchangebinance.OrderResult, error) {
		return exchangebinance.OrderResult{}, &exchangebinance.PrivateError{Kind: exchangebinance.PrivateErrorUnavailable}
	}
	client.query = func(call testnetOrderCall) (exchangebinance.OrderResult, error) {
		if strings.HasPrefix(call.clientOrderID, "csf") {
			for _, submitted := range client.snapshotCalls() {
				if submitted.operation == "place" && submitted.clientOrderID == call.clientOrderID {
					return filledTestnetResult(submitted, 302), nil
				}
			}
			t.Fatalf("flatten query had no preceding placement: %#v", call)
		}
		return exchangebinance.OrderResult{}, &exchangebinance.PrivateError{Kind: exchangebinance.PrivateErrorNotFound}
	}

	processTestnetSteps(t, fixture.executor, 3, "submit uncertain USD-M emergency flatten")
	assertTradingIntentState(t, fixture.database, intent.ID, "reconciling", "exchange_unavailable")
	processTestnetSteps(t, fixture.executor, 1, "query uncertain USD-M emergency flatten")
	assertTradingIntentState(t, fixture.database, intent.ID, "failed", "protection_failed_flattened")

	calls := client.snapshotCalls()
	if len(calls) != 5 || calls[3].operation != "place" || calls[3].side != "sell" ||
		!calls[3].quantity.Equal(decimal.NewFromInt(5)) || !calls[3].reduceOnly ||
		calls[4].operation != "query" || calls[4].clientOrderID != calls[3].clientOrderID ||
		marketPlacements != 2 {
		t.Fatalf("unknown flatten recovery calls = %#v, placements=%d", calls, marketPlacements)
	}
	var flatten db.TestnetOrder
	if err := fixture.database.Where("intent_id = ? AND purpose = 'flatten'", intent.ID).Take(&flatten).Error; err != nil {
		t.Fatalf("load recovered USD-M flatten: %v", err)
	}
	if flatten.Status != "filled" || flatten.QueryAttemptCount != 1 || flatten.SubmitAttemptCount != 1 {
		t.Fatalf("recovered USD-M flatten = %#v", flatten)
	}
}

func TestTestnetExecutorQueriesBeforeRetryingUnknownSubmission(t *testing.T) {
	client := &scriptedTestnetOrderClient{}
	fixture := newTestnetExecutorFixture(t, marketdata.MarketTypeSpot, client)
	intent := fixture.enqueue(t, "0.5")
	placeCount := 0
	client.query = func(testnetOrderCall) (exchangebinance.OrderResult, error) {
		return exchangebinance.OrderResult{}, &exchangebinance.PrivateError{Kind: exchangebinance.PrivateErrorNotFound}
	}
	client.place = func(call testnetOrderCall) (exchangebinance.OrderResult, error) {
		placeCount++
		if placeCount == 1 {
			return exchangebinance.OrderResult{}, &exchangebinance.PrivateError{Kind: exchangebinance.PrivateErrorUnavailable}
		}
		return filledTestnetResult(call, 42), nil
	}

	if processed, err := fixture.executor.ProcessNext(context.Background()); err != nil || !processed {
		t.Fatalf("process uncertain Testnet submission: processed=%t err=%v", processed, err)
	}
	assertTradingIntentState(t, fixture.database, intent.ID, "reconciling", "exchange_unavailable")
	var account db.TradingAccount
	if err := fixture.database.Where("id = ?", fixture.account.ID).Take(&account).Error; err != nil {
		t.Fatalf("load account after uncertain submission: %v", err)
	}
	if account.Status != "active" {
		t.Fatalf("transient uncertainty paused serialized account: %#v", account)
	}

	if processed, err := fixture.executor.ProcessNext(context.Background()); err != nil || !processed {
		t.Fatalf("recover uncertain Testnet submission: processed=%t err=%v", processed, err)
	}
	assertTradingIntentState(t, fixture.database, intent.ID, "reconciling", "protection_required")
	if processed, err := fixture.executor.ProcessNext(context.Background()); err != nil || !processed {
		t.Fatalf("protect recovered Testnet submission: processed=%t err=%v", processed, err)
	}
	assertTradingIntentState(t, fixture.database, intent.ID, "executed", "")
	calls := client.snapshotCalls()
	if len(calls) != 4 || calls[0].operation != "place" || calls[1].operation != "query" ||
		calls[2].operation != "place" || calls[0].clientOrderID != calls[1].clientOrderID ||
		calls[1].clientOrderID != calls[2].clientOrderID || calls[3].operation != "protect" {
		t.Fatalf("unknown submission recovery calls = %#v", calls)
	}
	var order db.TestnetOrder
	if err := fixture.database.Where("intent_id = ? AND purpose = 'rebalance'", intent.ID).Take(&order).Error; err != nil {
		t.Fatalf("load recovered Testnet order: %v", err)
	}
	if order.Status != "filled" || order.SubmitAttemptCount != 2 || order.QueryAttemptCount != 1 {
		t.Fatalf("recovered Testnet order = %#v", order)
	}
	if processed, err := fixture.executor.ProcessNext(context.Background()); err != nil || processed {
		t.Fatalf("completed Testnet intent was replayed: processed=%t err=%v", processed, err)
	}
}

func TestTestnetExecutorQueriesAfterRejectedSubmission(t *testing.T) {
	client := &scriptedTestnetOrderClient{}
	fixture := newTestnetExecutorFixture(t, marketdata.MarketTypeSpot, client)
	intent := fixture.enqueue(t, "0.5")
	var submitted testnetOrderCall
	client.place = func(call testnetOrderCall) (exchangebinance.OrderResult, error) {
		submitted = call
		return exchangebinance.OrderResult{}, &exchangebinance.PrivateError{Kind: exchangebinance.PrivateErrorRejected}
	}
	client.query = func(testnetOrderCall) (exchangebinance.OrderResult, error) {
		return filledTestnetResult(submitted, 45), nil
	}

	if processed, err := fixture.executor.ProcessNext(context.Background()); err != nil || !processed {
		t.Fatalf("persist rejected Testnet submission as unknown: processed=%t err=%v", processed, err)
	}
	assertTradingIntentState(t, fixture.database, intent.ID, "reconciling", "exchange_rejected")
	if processed, err := fixture.executor.ProcessNext(context.Background()); err != nil || !processed {
		t.Fatalf("query rejected Testnet submission: processed=%t err=%v", processed, err)
	}
	if processed, err := fixture.executor.ProcessNext(context.Background()); err != nil || !processed {
		t.Fatalf("protect recovered rejected Testnet submission: processed=%t err=%v", processed, err)
	}
	assertTradingIntentState(t, fixture.database, intent.ID, "executed", "")
	calls := client.snapshotCalls()
	if len(calls) != 3 || calls[0].operation != "place" || calls[1].operation != "query" ||
		calls[2].operation != "protect" {
		t.Fatalf("rejected Testnet recovery calls = %#v", calls)
	}
	var account db.TradingAccount
	if err := fixture.database.Where("id = ?", fixture.account.ID).Take(&account).Error; err != nil {
		t.Fatalf("load rejected-submission account: %v", err)
	}
	if account.Status != "paused" || account.PauseReason != "exchange_rejected" {
		t.Fatalf("rejected-submission account = %#v", account)
	}
}

func TestTestnetExecutorRecoversPreparedOrderByQueryOnly(t *testing.T) {
	client := &scriptedTestnetOrderClient{}
	fixture := newTestnetExecutorFixture(t, marketdata.MarketTypeSpot, client)
	intent := fixture.enqueue(t, "0.5")
	now := time.Now().UTC().Truncate(time.Microsecond)
	stoppedWorker := "stopped-testnet-worker"
	if err := fixture.database.Model(&db.TradingIntent{}).Where("id = ?", intent.ID).Updates(map[string]any{
		"status": "processing", "attempt_count": 1, "claimed_at": now,
		"worker_id": stoppedWorker, "updated_at": now,
	}).Error; err != nil {
		t.Fatalf("mark interrupted Testnet intent: %v", err)
	}
	order := db.TestnetOrder{
		ID: intent.ID, AccountID: intent.AccountID, IntentID: intent.ID,
		StrategyInstanceID: intent.StrategyInstanceID, InstrumentID: intent.InstrumentID,
		CredentialUpdatedAt:       fixture.credential.UpdatedAt,
		SubmittedAccountUpdatedAt: fixture.account.UpdatedAt,
		ClientOrderID:             intent.ClientOrderID, Side: "buy", Quantity: decimal.NewFromInt(5),
		Purpose: "rebalance", OrderType: "market", Status: "prepared", SubmitAttemptCount: 1,
		SubmittedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.database.Create(&order).Error; err != nil {
		t.Fatalf("create interrupted prepared Testnet order: %v", err)
	}
	client.query = func(call testnetOrderCall) (exchangebinance.OrderResult, error) {
		call.side = "buy"
		call.quantity = decimal.NewFromInt(5)
		return filledTestnetResult(call, 43), nil
	}
	client.place = func(testnetOrderCall) (exchangebinance.OrderResult, error) {
		return exchangebinance.OrderResult{}, errors.New("place must not run during prepared-order recovery")
	}

	if err := fixture.executor.Recover(context.Background()); err != nil {
		t.Fatalf("recover interrupted Testnet executor: %v", err)
	}
	assertTradingIntentState(t, fixture.database, intent.ID, "reconciling", "")
	if processed, err := fixture.executor.ProcessNext(context.Background()); err != nil || !processed {
		t.Fatalf("query interrupted Testnet order: processed=%t err=%v", processed, err)
	}
	if processed, err := fixture.executor.ProcessNext(context.Background()); err != nil || !processed {
		t.Fatalf("protect recovered interrupted Testnet order: processed=%t err=%v", processed, err)
	}
	assertTradingIntentState(t, fixture.database, intent.ID, "executed", "")
	calls := client.snapshotCalls()
	if len(calls) != 2 || calls[0].operation != "query" || calls[1].operation != "protect" {
		t.Fatalf("prepared-order recovery calls = %#v", calls)
	}
}

func TestTestnetExecutorDiscardsResultAfterAccountVersionChanges(t *testing.T) {
	client := &scriptedTestnetOrderClient{}
	fixture := newTestnetExecutorFixture(t, marketdata.MarketTypeSpot, client)
	intent := fixture.enqueue(t, "0.5")
	client.place = func(call testnetOrderCall) (exchangebinance.OrderResult, error) {
		if err := fixture.database.Model(&db.TradingAccount{}).Where("id = ?", fixture.account.ID).Updates(map[string]any{
			"max_order_notional": decimal.NewFromInt(900),
			"updated_at":         time.Now().UTC().Add(time.Minute),
		}).Error; err != nil {
			t.Fatalf("change account during Testnet submission: %v", err)
		}
		return filledTestnetResult(call, 44), nil
	}

	if processed, err := fixture.executor.ProcessNext(context.Background()); err != nil || !processed {
		t.Fatalf("process stale Testnet result: processed=%t err=%v", processed, err)
	}
	assertTradingIntentState(t, fixture.database, intent.ID, "reconciling", "execution_state_changed")
	var order db.TestnetOrder
	if err := fixture.database.Where("intent_id = ? AND purpose = 'rebalance'", intent.ID).Take(&order).Error; err != nil {
		t.Fatalf("load stale Testnet order: %v", err)
	}
	if order.Status != "unknown" || order.ExchangeOrderID != nil || order.LastErrorCode != "execution_state_changed" {
		t.Fatalf("stale Testnet result was projected: %#v", order)
	}
	var account db.TradingAccount
	if err := fixture.database.Where("id = ?", fixture.account.ID).Take(&account).Error; err != nil {
		t.Fatalf("load stale-result account: %v", err)
	}
	if account.Status != "paused" || account.PauseReason != "execution_state_changed" {
		t.Fatalf("stale-result account = %#v", account)
	}
}

func TestTestnetExecutorDoesNotQueryAfterCredentialVersionChanges(t *testing.T) {
	client := &scriptedTestnetOrderClient{}
	fixture := newTestnetExecutorFixture(t, marketdata.MarketTypeSpot, client)
	intent := fixture.enqueue(t, "0.5")
	client.place = func(testnetOrderCall) (exchangebinance.OrderResult, error) {
		return exchangebinance.OrderResult{}, &exchangebinance.PrivateError{Kind: exchangebinance.PrivateErrorUnavailable}
	}

	if processed, err := fixture.executor.ProcessNext(context.Background()); err != nil || !processed {
		t.Fatalf("persist uncertain Testnet submission: processed=%t err=%v", processed, err)
	}
	if err := fixture.database.Where("account_id = ?", fixture.account.ID).
		Delete(&db.TestnetRiskState{}).Error; err != nil {
		t.Fatalf("delete Testnet risk state before credential rotation: %v", err)
	}
	if err := fixture.database.Where("account_id = ?", fixture.account.ID).
		Delete(&db.TestnetReconciliation{}).Error; err != nil {
		t.Fatalf("delete Testnet reconciliation before credential rotation: %v", err)
	}
	changedAt := fixture.credential.UpdatedAt.Add(time.Minute)
	if err := fixture.database.Model(&db.TradingAccountCredential{}).Where("id = ?", fixture.credential.ID).
		Update("updated_at", changedAt).Error; err != nil {
		t.Fatalf("change Testnet credential version: %v", err)
	}

	if processed, err := fixture.executor.ProcessNext(context.Background()); err != nil || !processed {
		t.Fatalf("process stale-credential recovery: processed=%t err=%v", processed, err)
	}
	assertTradingIntentState(t, fixture.database, intent.ID, "reconciling", "execution_state_changed")
	if calls := client.snapshotCalls(); len(calls) != 1 || calls[0].operation != "place" {
		t.Fatalf("stale-credential recovery called exchange: %#v", calls)
	}
	var order db.TestnetOrder
	if err := fixture.database.Where("intent_id = ? AND purpose = 'rebalance'", intent.ID).Take(&order).Error; err != nil {
		t.Fatalf("load stale-credential Testnet order: %v", err)
	}
	if order.Status != "unknown" || order.QueryAttemptCount != 0 || order.LastQueriedAt != nil {
		t.Fatalf("stale-credential Testnet order = %#v", order)
	}
}

func TestTestnetExecutorBlocksRiskBeforeCreatingExternalOrder(t *testing.T) {
	client := &scriptedTestnetOrderClient{}
	fixture := newTestnetExecutorFixture(t, marketdata.MarketTypeSpot, client)
	if err := fixture.database.Model(&db.TradingAccount{}).Where("id = ?", fixture.account.ID).Updates(map[string]any{
		"max_order_notional": decimal.NewFromInt(100), "updated_at": time.Now().UTC(),
	}).Error; err != nil {
		t.Fatalf("set Testnet risk breach: %v", err)
	}
	intent := fixture.enqueue(t, "0.5")
	if processed, err := fixture.executor.ProcessNext(context.Background()); err != nil || !processed {
		t.Fatalf("process blocked Testnet intent: processed=%t err=%v", processed, err)
	}
	assertTradingIntentState(t, fixture.database, intent.ID, "blocked", "order_notional_limit")
	if calls := client.snapshotCalls(); len(calls) != 0 {
		t.Fatalf("risk-blocked Testnet intent called exchange: %#v", calls)
	}
	assertRowCountGORM(t, fixture.database, &db.TestnetOrder{}, "account_id = ?", fixture.account.ID, 0)
}

func TestTestnetExecutorBlocksMissingProtectionBeforeCreatingExternalOrder(t *testing.T) {
	client := &scriptedTestnetOrderClient{}
	fixture := newTestnetExecutorFixture(t, marketdata.MarketTypeSpot, client)
	if err := fixture.database.Model(&db.StrategyInstance{}).Where("id = ?", fixture.base.instanceID).
		Update("stop_loss_ratio", nil).Error; err != nil {
		t.Fatalf("remove Testnet stop loss configuration: %v", err)
	}
	intent := fixture.enqueue(t, "0.5")
	if processed, err := fixture.executor.ProcessNext(context.Background()); err != nil || !processed {
		t.Fatalf("process unprotected Testnet intent: processed=%t err=%v", processed, err)
	}
	assertTradingIntentState(t, fixture.database, intent.ID, "blocked", "protection_configuration_invalid")
	if calls := client.snapshotCalls(); len(calls) != 0 {
		t.Fatalf("unprotected Testnet intent called exchange: %#v", calls)
	}
	var account db.TradingAccount
	if err := fixture.database.Where("id = ?", fixture.account.ID).Take(&account).Error; err != nil {
		t.Fatalf("load unprotected Testnet account: %v", err)
	}
	if account.Status != "paused" || account.PauseReason != "protection_configuration_invalid" {
		t.Fatalf("unprotected Testnet account = %#v", account)
	}
}

func TestValidTestnetOrderResultShapes(t *testing.T) {
	base := exchangebinance.OrderResult{
		OriginalQuantity: decimal.NewFromInt(5),
	}
	for _, test := range []struct {
		name   string
		status string
		filled string
		quote  string
		price  string
		want   bool
	}{
		{name: "new", status: "new", filled: "0", quote: "0", price: "0", want: true},
		{name: "partial", status: "partially_filled", filled: "2", quote: "200", price: "100", want: true},
		{name: "zero partial", status: "partially_filled", filled: "0", quote: "0", price: "0"},
		{name: "filled", status: "filled", filled: "5", quote: "500", price: "100", want: true},
		{name: "short fill", status: "filled", filled: "4", quote: "400", price: "100"},
		{name: "rejected fill", status: "rejected", filled: "1", quote: "100", price: "100"},
		{name: "canceled empty", status: "canceled", filled: "0", quote: "0", price: "0", want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := base
			result.ExecutedQuantity = decimal.RequireFromString(test.filled)
			result.CumulativeQuoteQuantity = decimal.RequireFromString(test.quote)
			result.AveragePrice = decimal.RequireFromString(test.price)
			if got := validTestnetOrderResult(test.status, result); got != test.want {
				t.Fatalf("validTestnetOrderResult(%s) = %t, want %t", test.status, got, test.want)
			}
		})
	}
}

type testnetExecutorFixture struct {
	database   *gorm.DB
	base       *paperExecutorFixture
	executor   *TestnetExecutor
	account    db.TradingAccount
	credential db.TradingAccountCredential
}

func newTestnetExecutorFixture(
	t *testing.T,
	market marketdata.MarketType,
	client testnetOrderClient,
) testnetExecutorFixture {
	t.Helper()
	base := newPaperExecutorFixtureForMarket(t, "manual", true, true, true, market)
	now := time.Now().UTC().Truncate(time.Microsecond)
	accountUpdates := map[string]any{
		"environment": "testnet", "updated_at": now,
	}
	if err := base.database.Model(&db.TradingAccount{}).Where("id = ?", base.accountID).
		Updates(accountUpdates).Error; err != nil {
		t.Fatalf("convert account to %s Testnet: %v", market, err)
	}
	if err := base.database.Model(&db.StrategyInstance{}).Where("id = ?", base.instanceID).
		Updates(map[string]any{
			"environment": "testnet", "stop_loss_ratio": decimal.RequireFromString("0.05"), "updated_at": now,
		}).Error; err != nil {
		t.Fatalf("convert strategy instance to Testnet: %v", err)
	}
	var account db.TradingAccount
	if err := base.database.Where("id = ?", base.accountID).Take(&account).Error; err != nil {
		t.Fatalf("load Testnet account: %v", err)
	}
	cipher, err := security.NewSecretCipher("testnet-executor-test-key")
	if err != nil {
		t.Fatalf("create Testnet executor cipher: %v", err)
	}
	credential := db.TradingAccountCredential{
		ID: mustUUIDv7(t), AccountID: account.ID, OwnerUserID: account.OwnerUserID,
		APIKeyCiphertext:    cipher.Encrypt("testnet-api-key"),
		APISecretCiphertext: cipher.Encrypt("testnet-api-secret"),
		WithdrawalDisabled:  true, IPWhitelistConfigured: true,
		Status: "configured", VerificationStatus: "verified", LastVerifiedAt: &now,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := base.database.Create(&credential).Error; err != nil {
		t.Fatalf("create Testnet executor credential: %v", err)
	}
	reconciliation := db.TestnetReconciliation{
		AccountID: account.ID, CredentialUpdatedAt: credential.UpdatedAt, Status: "matched",
		LastAttemptedAt: now, LastObservedAt: &now, UpdatedAt: now,
	}
	if err := base.database.Create(&reconciliation).Error; err != nil {
		t.Fatalf("create Testnet executor reconciliation: %v", err)
	}
	baseline := decimal.NewFromInt(10_000)
	if err := base.database.Create(&db.TestnetRiskState{
		AccountID: account.ID, CredentialUpdatedAt: credential.UpdatedAt,
		BaselineEquity: baseline, Equity: baseline, PeakEquity: baseline,
		DayStartDate: utcDay(now), DayStartEquity: baseline, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create Testnet executor risk state: %v", err)
	}
	executor, err := NewTestnetExecutor(base.database, cipher, client, "testnet-test-worker", time.Millisecond)
	if err != nil {
		t.Fatalf("create Testnet executor: %v", err)
	}
	return testnetExecutorFixture{
		database: base.database, base: base, executor: executor, account: account, credential: credential,
	}
}

func (fixture testnetExecutorFixture) enqueue(t *testing.T, target string) db.TradingIntent {
	t.Helper()
	signal := fixture.base.insertSignal(t, target, "testnet")
	return fixture.base.enqueueSignal(t, signal)
}

func processTestnetSteps(t *testing.T, executor *TestnetExecutor, count int, operation string) {
	t.Helper()
	for step := 1; step <= count; step++ {
		if processed, err := executor.ProcessNext(context.Background()); err != nil || !processed {
			t.Fatalf("%s step %d: processed=%t err=%v", operation, step, processed, err)
		}
	}
}

type testnetOrderCall struct {
	operation       string
	market          marketdata.MarketType
	symbol          string
	clientOrderID   string
	side            string
	quantity        decimal.Decimal
	reduceOnly      bool
	orderType       string
	stopPrice       decimal.Decimal
	closePosition   bool
	workingType     string
	exchangeOrderID int64
}

type scriptedTestnetOrderClient struct {
	mu      sync.Mutex
	calls   []testnetOrderCall
	query   func(testnetOrderCall) (exchangebinance.OrderResult, error)
	place   func(testnetOrderCall) (exchangebinance.OrderResult, error)
	protect func(testnetOrderCall) (exchangebinance.OrderResult, error)
	cancel  func(testnetOrderCall) (exchangebinance.OrderResult, error)
}

func (client *scriptedTestnetOrderClient) QueryOrder(
	_ context.Context,
	market marketdata.MarketType,
	_, _, symbol, clientOrderID string,
) (exchangebinance.OrderResult, error) {
	call := testnetOrderCall{
		operation: "query", market: market, symbol: symbol, clientOrderID: clientOrderID,
	}
	client.mu.Lock()
	client.calls = append(client.calls, call)
	query := client.query
	client.mu.Unlock()
	if query == nil {
		return exchangebinance.OrderResult{}, &exchangebinance.PrivateError{Kind: exchangebinance.PrivateErrorNotFound}
	}
	result, err := query(call)
	client.rememberExchangeOrderID(clientOrderID, result.ExchangeOrderID, err)
	return result, err
}

func (client *scriptedTestnetOrderClient) PlaceMarketOrder(
	_ context.Context,
	market marketdata.MarketType,
	_, _, symbol, clientOrderID, side string,
	quantity decimal.Decimal,
	reduceOnly bool,
) (exchangebinance.OrderResult, error) {
	call := testnetOrderCall{
		operation: "place", market: market, symbol: symbol, clientOrderID: clientOrderID,
		side: side, quantity: quantity, reduceOnly: reduceOnly, orderType: "market",
	}
	client.mu.Lock()
	client.calls = append(client.calls, call)
	place := client.place
	client.mu.Unlock()
	if place == nil {
		return exchangebinance.OrderResult{}, errors.New("unexpected Testnet order placement")
	}
	result, err := place(call)
	client.rememberExchangeOrderID(clientOrderID, result.ExchangeOrderID, err)
	return result, err
}

func (client *scriptedTestnetOrderClient) PlaceProtectiveOrder(
	_ context.Context,
	market marketdata.MarketType,
	_, _, symbol, clientOrderID, side string,
	quantity, stopPrice decimal.Decimal,
) (exchangebinance.OrderResult, error) {
	call := testnetOrderCall{
		operation: "protect", market: market, symbol: symbol, clientOrderID: clientOrderID,
		side: side, quantity: quantity, stopPrice: stopPrice, orderType: "stop_loss",
	}
	if market == marketdata.MarketTypeUSDM {
		call.orderType = "stop_market"
		call.closePosition = true
		call.workingType = "mark_price"
	}
	client.mu.Lock()
	client.calls = append(client.calls, call)
	protect := client.protect
	orderID := int64(10_000 + len(client.calls))
	client.mu.Unlock()
	if protect != nil {
		result, err := protect(call)
		client.rememberExchangeOrderID(clientOrderID, result.ExchangeOrderID, err)
		return result, err
	}
	result := testnetProtectiveResult(call, orderID, "new")
	client.rememberExchangeOrderID(clientOrderID, result.ExchangeOrderID, nil)
	return result, nil
}

func (client *scriptedTestnetOrderClient) CancelOrder(
	_ context.Context,
	market marketdata.MarketType,
	_, _, symbol, clientOrderID string,
) (exchangebinance.OrderResult, error) {
	client.mu.Lock()
	call := testnetOrderCall{operation: "cancel", market: market, symbol: symbol, clientOrderID: clientOrderID}
	for index := len(client.calls) - 1; index >= 0; index-- {
		if client.calls[index].clientOrderID == clientOrderID {
			call = client.calls[index]
			call.operation = "cancel"
			break
		}
	}
	client.calls = append(client.calls, call)
	cancel := client.cancel
	orderID := call.exchangeOrderID
	client.mu.Unlock()
	if orderID <= 0 {
		return exchangebinance.OrderResult{}, errors.New("unexpected Testnet cancellation without placed order")
	}
	if cancel != nil {
		result, err := cancel(call)
		client.rememberExchangeOrderID(clientOrderID, result.ExchangeOrderID, err)
		return result, err
	}
	result := testnetProtectiveResult(call, orderID, "canceled")
	client.rememberExchangeOrderID(clientOrderID, result.ExchangeOrderID, nil)
	return result, nil
}

func (client *scriptedTestnetOrderClient) rememberExchangeOrderID(clientOrderID string, orderID int64, err error) {
	if err != nil || orderID <= 0 {
		return
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	for index := len(client.calls) - 1; index >= 0; index-- {
		if client.calls[index].clientOrderID == clientOrderID {
			client.calls[index].exchangeOrderID = orderID
		}
	}
}

func (client *scriptedTestnetOrderClient) snapshotCalls() []testnetOrderCall {
	client.mu.Lock()
	defer client.mu.Unlock()
	return append([]testnetOrderCall(nil), client.calls...)
}

func filledTestnetResult(call testnetOrderCall, orderID int64) exchangebinance.OrderResult {
	orderType := call.orderType
	if orderType == "" {
		orderType = "market"
	}
	return exchangebinance.OrderResult{
		Symbol: call.symbol, ExchangeOrderID: orderID, ClientOrderID: call.clientOrderID,
		Side: call.side, OrderType: orderType, Status: "filled", ReduceOnly: call.reduceOnly,
		OriginalQuantity: call.quantity, ExecutedQuantity: call.quantity,
		CumulativeQuoteQuantity: call.quantity.Mul(decimal.NewFromInt(100)),
		AveragePrice:            decimal.NewFromInt(100), ObservedAt: time.Now().UTC(),
	}
}

func testnetProtectiveResult(call testnetOrderCall, orderID int64, status string) exchangebinance.OrderResult {
	return exchangebinance.OrderResult{
		Symbol: call.symbol, ExchangeOrderID: orderID, ClientOrderID: call.clientOrderID,
		Side: call.side, OrderType: call.orderType, Status: status,
		OriginalQuantity: call.quantity, StopPrice: call.stopPrice,
		ClosePosition: call.closePosition, ReduceOnly: call.reduceOnly, WorkingType: call.workingType,
		ObservedAt: time.Now().UTC(),
	}
}
