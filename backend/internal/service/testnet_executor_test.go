package service

import (
	"context"
	"errors"
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

			processed, err := fixture.executor.ProcessNext(context.Background())
			if err != nil || !processed {
				t.Fatalf("process %s Testnet order: processed=%t err=%v", market, processed, err)
			}
			assertTradingIntentState(t, fixture.database, intent.ID, "executed", "")
			calls := client.snapshotCalls()
			if len(calls) != 1 || calls[0].operation != "place" || calls[0].market != market ||
				calls[0].clientOrderID != intent.ClientOrderID || calls[0].side != "buy" ||
				!calls[0].quantity.Equal(decimal.NewFromInt(5)) || calls[0].reduceOnly {
				t.Fatalf("%s Testnet calls = %#v", market, calls)
			}
			var order db.TestnetOrder
			if err := fixture.database.Where("intent_id = ?", intent.ID).Take(&order).Error; err != nil {
				t.Fatalf("load %s Testnet order: %v", market, err)
			}
			if order.Status != "filled" || order.ExchangeOrderID == nil || *order.ExchangeOrderID != 41 ||
				order.ClientOrderID != intent.ClientOrderID || !order.FilledQuantity.Equal(decimal.NewFromInt(5)) ||
				!order.AveragePrice.Equal(decimal.NewFromInt(100)) || order.SubmitAttemptCount != 1 ||
				order.QueryAttemptCount != 0 {
				t.Fatalf("stored %s Testnet order = %#v", market, order)
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
	if processed, err := fixture.executor.ProcessNext(context.Background()); err != nil || !processed {
		t.Fatalf("open USD-M Testnet position: processed=%t err=%v", processed, err)
	}
	fixture.enqueue(t, "0.2")
	if processed, err := fixture.executor.ProcessNext(context.Background()); err != nil || !processed {
		t.Fatalf("reduce USD-M Testnet position: processed=%t err=%v", processed, err)
	}
	calls := client.snapshotCalls()
	if len(calls) != 2 || calls[1].operation != "place" || calls[1].side != "sell" ||
		!calls[1].quantity.Equal(decimal.NewFromInt(3)) || !calls[1].reduceOnly {
		t.Fatalf("USD-M reduction calls = %#v", calls)
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
	assertTradingIntentState(t, fixture.database, intent.ID, "executed", "")
	calls := client.snapshotCalls()
	if len(calls) != 3 || calls[0].operation != "place" || calls[1].operation != "query" ||
		calls[2].operation != "place" || calls[0].clientOrderID != calls[1].clientOrderID ||
		calls[1].clientOrderID != calls[2].clientOrderID {
		t.Fatalf("unknown submission recovery calls = %#v", calls)
	}
	var order db.TestnetOrder
	if err := fixture.database.Where("intent_id = ?", intent.ID).Take(&order).Error; err != nil {
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
	assertTradingIntentState(t, fixture.database, intent.ID, "executed", "")
	calls := client.snapshotCalls()
	if len(calls) != 2 || calls[0].operation != "place" || calls[1].operation != "query" {
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
		Status: "prepared", SubmitAttemptCount: 1, SubmittedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.database.Create(&order).Error; err != nil {
		t.Fatalf("create interrupted prepared Testnet order: %v", err)
	}
	client.query = func(call testnetOrderCall) (exchangebinance.OrderResult, error) {
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
	assertTradingIntentState(t, fixture.database, intent.ID, "executed", "")
	calls := client.snapshotCalls()
	if len(calls) != 1 || calls[0].operation != "query" {
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
	if err := fixture.database.Where("intent_id = ?", intent.ID).Take(&order).Error; err != nil {
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
	if err := fixture.database.Where("intent_id = ?", intent.ID).Take(&order).Error; err != nil {
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
	base := newPaperExecutorFixture(t, "manual", true, true, true)
	now := time.Now().UTC().Truncate(time.Microsecond)
	accountUpdates := map[string]any{
		"environment": "testnet", "market_type": string(market), "updated_at": now,
	}
	if market == marketdata.MarketTypeUSDM {
		accountUpdates["leverage"] = 2
	}
	if err := base.database.Model(&db.TradingAccount{}).Where("id = ?", base.accountID).
		Updates(accountUpdates).Error; err != nil {
		t.Fatalf("convert account to %s Testnet: %v", market, err)
	}
	if err := base.database.Model(&db.StrategyInstance{}).Where("id = ?", base.instanceID).
		Updates(map[string]any{"environment": "testnet", "updated_at": now}).Error; err != nil {
		t.Fatalf("convert strategy instance to Testnet: %v", err)
	}
	if market == marketdata.MarketTypeUSDM {
		if err := base.database.Model(&db.MarketInstrument{}).Where("id = ?", base.instrumentID).
			Update("market_type", string(market)).Error; err != nil {
			t.Fatalf("convert instrument to USD-M: %v", err)
		}
		if err := base.database.Model(&db.StrategyVersion{}).Where("id = ?", base.versionID).
			Update("market_type", string(market)).Error; err != nil {
			t.Fatalf("convert strategy version to USD-M: %v", err)
		}
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

type testnetOrderCall struct {
	operation     string
	market        marketdata.MarketType
	symbol        string
	clientOrderID string
	side          string
	quantity      decimal.Decimal
	reduceOnly    bool
}

type scriptedTestnetOrderClient struct {
	mu    sync.Mutex
	calls []testnetOrderCall
	query func(testnetOrderCall) (exchangebinance.OrderResult, error)
	place func(testnetOrderCall) (exchangebinance.OrderResult, error)
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
	return query(call)
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
		side: side, quantity: quantity, reduceOnly: reduceOnly,
	}
	client.mu.Lock()
	client.calls = append(client.calls, call)
	place := client.place
	client.mu.Unlock()
	if place == nil {
		return exchangebinance.OrderResult{}, errors.New("unexpected Testnet order placement")
	}
	return place(call)
}

func (client *scriptedTestnetOrderClient) snapshotCalls() []testnetOrderCall {
	client.mu.Lock()
	defer client.mu.Unlock()
	return append([]testnetOrderCall(nil), client.calls...)
}

func filledTestnetResult(call testnetOrderCall, orderID int64) exchangebinance.OrderResult {
	return exchangebinance.OrderResult{
		Symbol: call.symbol, ExchangeOrderID: orderID, ClientOrderID: call.clientOrderID,
		Side: call.side, OrderType: "market", Status: "filled",
		OriginalQuantity: call.quantity, ExecutedQuantity: call.quantity,
		CumulativeQuoteQuantity: call.quantity.Mul(decimal.NewFromInt(100)),
		AveragePrice:            decimal.NewFromInt(100), ObservedAt: time.Now().UTC(),
	}
}
