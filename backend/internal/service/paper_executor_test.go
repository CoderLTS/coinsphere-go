package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/marketdata"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

func TestPaperExecutorRecoveryEmergencyRiskAndProjectionReplay(t *testing.T) {
	fixture := newPaperExecutorFixture(t, "manual", true, true, true)
	openSignal := fixture.insertSignal(t, "0.5", "paper")
	intent := fixture.enqueueSignal(t, openSignal)

	claimedAt := time.Now().UTC()
	if err := fixture.database.Model(&db.TradingIntent{}).Where("id = ?", intent.ID).Updates(map[string]any{
		"status": "processing", "attempt_count": 1, "claimed_at": claimedAt, "worker_id": "stopped-worker",
	}).Error; err != nil {
		t.Fatalf("mark intent as interrupted: %v", err)
	}
	if err := fixture.executor.Recover(context.Background()); err != nil {
		t.Fatalf("recover interrupted intent: %v", err)
	}
	assertTradingIntentState(t, fixture.database, intent.ID, "pending", "")
	if processed, err := fixture.executor.ProcessNext(context.Background()); err != nil || !processed {
		t.Fatalf("process recovered intent: processed=%t err=%v", processed, err)
	}
	assertTradingIntentState(t, fixture.database, intent.ID, "executed", "")

	if err := fixture.app.createPaperIntentForSignalWithDB(fixture.database, openSignal, true); err != nil {
		t.Fatalf("replay paper intent command: %v", err)
	}
	if processed, err := fixture.executor.ProcessNext(context.Background()); err != nil || processed {
		t.Fatalf("duplicate intent was processed: processed=%t err=%v", processed, err)
	}
	assertRowCountGORM(t, fixture.database, &db.TradingIntent{}, "strategy_signal_id = ?", openSignal.ID, 1)
	assertRowCountGORM(t, fixture.database, &db.PaperOrder{}, "account_id = ?", fixture.accountID, 1)
	assertRowCountGORM(t, fixture.database, &db.TradingEvent{}, "account_id = ?", fixture.accountID, 3)

	fixture.activateEmergencyStop(t)
	reduceSignal := fixture.insertSignal(t, "0.25", "paper")
	reduceIntent := fixture.enqueueSignal(t, reduceSignal)
	if processed, err := fixture.executor.ProcessNext(context.Background()); err != nil || !processed {
		t.Fatalf("process emergency reduction: processed=%t err=%v", processed, err)
	}
	assertTradingIntentState(t, fixture.database, reduceIntent.ID, "executed", "")
	assertPaperPositionQuantity(t, fixture.database, fixture.accountID, fixture.instrumentID, "2.5")

	increaseSignal := fixture.insertSignal(t, "0.75", "paper")
	increaseIntent := fixture.enqueueSignal(t, increaseSignal)
	if processed, err := fixture.executor.ProcessNext(context.Background()); err != nil || !processed {
		t.Fatalf("process emergency increase: processed=%t err=%v", processed, err)
	}
	assertTradingIntentState(t, fixture.database, increaseIntent.ID, "blocked", "global_emergency_stop")
	assertPaperPositionQuantity(t, fixture.database, fixture.accountID, fixture.instrumentID, "2.5")
	assertRowCountGORM(t, fixture.database, &db.PaperOrder{}, "account_id = ?", fixture.accountID, 2)

	fixture.releaseEmergencyStop(t)
	if err := fixture.database.Model(&db.TradingAccount{}).Where("id = ?", fixture.accountID).Updates(map[string]any{
		"status": "active", "pause_reason": "", "max_order_notional": decimal.RequireFromString("10"),
		"updated_at": time.Now().UTC(),
	}).Error; err != nil {
		t.Fatalf("prepare order risk breach: %v", err)
	}
	riskSignal := fixture.insertSignal(t, "1", "paper")
	riskIntent := fixture.enqueueSignal(t, riskSignal)
	if processed, err := fixture.executor.ProcessNext(context.Background()); err != nil || !processed {
		t.Fatalf("process risk breach: processed=%t err=%v", processed, err)
	}
	assertTradingIntentState(t, fixture.database, riskIntent.ID, "blocked", "order_notional_limit")
	var account db.TradingAccount
	if err := fixture.database.Where("id = ?", fixture.accountID).Take(&account).Error; err != nil {
		t.Fatalf("load risk-paused account: %v", err)
	}
	if account.Status != "paused" || account.PauseReason != "order_notional_limit" || account.AutomationEnabled {
		t.Fatalf("risk-paused account = %#v", account)
	}

	beforeOrders, beforePositions, beforeBalances := loadPaperProjections(t, fixture.database, fixture.accountID)
	if err := fixture.executor.RebuildAccountProjections(context.Background(), fixture.accountID); err != nil {
		t.Fatalf("rebuild paper projections: %v", err)
	}
	afterOrders, afterPositions, afterBalances := loadPaperProjections(t, fixture.database, fixture.accountID)
	if !reflect.DeepEqual(beforeOrders, afterOrders) {
		t.Fatalf("rebuilt orders differ:\nbefore=%#v\nafter=%#v", beforeOrders, afterOrders)
	}
	if !reflect.DeepEqual(beforePositions, afterPositions) {
		t.Fatalf("rebuilt positions differ:\nbefore=%#v\nafter=%#v", beforePositions, afterPositions)
	}
	if !reflect.DeepEqual(beforeBalances, afterBalances) {
		t.Fatalf("rebuilt balances differ:\nbefore=%#v\nafter=%#v", beforeBalances, afterBalances)
	}
	assertRowCountGORM(t, fixture.database, &db.TradingEvent{}, "account_id = ?", fixture.accountID, 6)
}

func TestPaperExecutorKeepsIncompleteAutomationDisabled(t *testing.T) {
	tests := []struct {
		name              string
		automationEnabled bool
		authorized        bool
		riskComplete      bool
		wantReason        string
	}{
		{name: "account switch", automationEnabled: false, authorized: true, riskComplete: true, wantReason: "automation_not_authorized"},
		{name: "authorization", automationEnabled: true, authorized: false, riskComplete: true, wantReason: "automation_not_authorized"},
		{name: "risk limit", automationEnabled: true, authorized: true, riskComplete: false, wantReason: "risk_configuration_incomplete"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPaperExecutorFixture(t, "auto", test.automationEnabled, test.authorized, test.riskComplete)
			signal := fixture.insertSignal(t, "0.5", "paper")
			intent := fixture.enqueueSignal(t, signal)
			if processed, err := fixture.executor.ProcessNext(context.Background()); err != nil || !processed {
				t.Fatalf("process incomplete automation: processed=%t err=%v", processed, err)
			}
			assertTradingIntentState(t, fixture.database, intent.ID, "blocked", test.wantReason)
			var account db.TradingAccount
			if err := fixture.database.Where("id = ?", fixture.accountID).Take(&account).Error; err != nil {
				t.Fatalf("load disabled account: %v", err)
			}
			var instance db.StrategyInstance
			if err := fixture.database.Where("id = ?", fixture.instanceID).Take(&instance).Error; err != nil {
				t.Fatalf("load disabled instance: %v", err)
			}
			if account.Status != "paused" || account.AutomationEnabled || instance.IsEnabled {
				t.Fatalf("incomplete automation remained enabled: account=%#v instance=%#v", account, instance)
			}
			assertRowCountGORM(t, fixture.database, &db.PaperOrder{}, "account_id = ?", fixture.accountID, 0)
		})
	}
}

func TestPaperExecutorRejectsNonPaperIntents(t *testing.T) {
	fixture := newPaperExecutorFixture(t, "manual", true, true, true)
	for _, environment := range []string{"testnet", "live"} {
		t.Run(environment, func(t *testing.T) {
			signal := fixture.insertSignal(t, "0.5", environment)
			if err := fixture.app.createPaperIntentForSignalWithDB(fixture.database, signal, true); !errors.Is(err, ErrPaperExecutionUnavailable) {
				t.Fatalf("%s signal enqueue returned %v", environment, err)
			}
			intentID := mustUUIDv7(t)
			intent := db.TradingIntent{
				ID: intentID, AccountID: fixture.accountID, StrategySignalID: signal.ID,
				StrategyInstanceID: fixture.instanceID, OwnerUserID: fixture.owner.ID,
				InstrumentID: fixture.instrumentID, Market: "spot", Mode: "manual",
				Environment: environment, Target: signal.Target, Status: "pending",
				ClientOrderID: paperClientOrderID(intentID), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
			}
			if err := fixture.database.Create(&intent).Error; err == nil {
				t.Fatalf("database accepted a %s trading intent", environment)
			}
		})
	}
	assertRowCountGORM(t, fixture.database, &db.TradingIntent{}, "account_id = ?", fixture.accountID, 0)
	assertRowCountGORM(t, fixture.database, &db.PaperOrder{}, "account_id = ?", fixture.accountID, 0)
}

func TestTradingRiskIdempotentReplayDoesNotRevalidateInstrumentState(t *testing.T) {
	fixture := newPaperExecutorFixture(t, "manual", false, false, true)
	fixture.app.reauthTokens = map[string]reauthTokenRecord{}
	fixture.app.revokedAccessTokens = map[string]time.Time{}
	principal := &Principal{
		User: &fixture.owner, AccessMode: "authenticated", AccessTokenID: "paper-risk-session",
	}
	payload := TradingRiskPayload{
		InstrumentIDs: []string{fixture.instrumentID.String()}, MaxTotalNotional: "5000",
		MaxSymbolNotional: "2500", MaxOrderNotional: "900", MaxDailyLoss: "500",
		MaxDrawdown: "1000", MaxQuoteAgeSeconds: 60,
	}
	idempotencyKey := "paper-risk-replay-command"
	token := fixture.app.issueReauthToken(principal, time.Now())
	if _, err := fixture.app.UpdateTradingRisk(
		context.Background(), principal, fixture.accountID.String(), payload, idempotencyKey, token,
	); err != nil {
		t.Fatalf("update Paper risk: %v", err)
	}
	if err := fixture.database.Model(&db.MarketInstrument{}).Where("id = ?", fixture.instrumentID).
		Update("status", "break").Error; err != nil {
		t.Fatalf("suspend Paper instrument: %v", err)
	}
	if _, err := fixture.app.UpdateTradingRisk(
		context.Background(), principal, fixture.accountID.String(), payload, idempotencyKey, "",
	); err != nil {
		t.Fatalf("replay Paper risk update: %v", err)
	}
}

type paperExecutorFixture struct {
	database     *gorm.DB
	app          *App
	executor     *PaperExecutor
	owner        db.SystemUser
	accountID    uuid.UUID
	instrumentID uuid.UUID
	instanceID   uuid.UUID
	versionID    uuid.UUID
	mode         string
	signalNumber int
}

func newPaperExecutorFixture(
	t *testing.T, mode string, automationEnabled, authorized, riskComplete bool,
) *paperExecutorFixture {
	t.Helper()
	database := openPostgresWorkflowContractDatabase(t).primary
	now := time.Now().UTC().Truncate(time.Microsecond)
	owner := db.SystemUser{Username: "paper-executor-owner", IsActive: true, CreatedAt: now, UpdatedAt: now}
	if err := database.Create(&owner).Error; err != nil {
		t.Fatalf("create paper owner: %v", err)
	}
	instrumentID := uuid.MustParse("019d8000-0000-7000-8000-000000000001")
	strategyID := uuid.MustParse("019d8000-0000-7000-8000-000000000010")
	versionID := uuid.MustParse("019d8000-0000-7000-8000-000000000011")
	instanceID := uuid.MustParse("019d8000-0000-7000-8000-000000000012")
	accountID := uuid.MustParse("019d8000-0000-7000-8000-000000000013")
	publishTaskID := "019d8000-0000-7000-8000-000000000014"
	instrument := db.MarketInstrument{
		ID: instrumentID, Venue: string(marketdata.VenueBinance), Market: "spot", NativeSymbol: "BTCUSDT",
		BaseAsset: "BTC", QuoteAsset: "USDT", Status: "trading",
		PriceTick: decimal.RequireFromString("0.1"), QuantityStep: decimal.RequireFromString("0.001"),
		MinQuantity: decimal.RequireFromString("0.001"), MinNotional: decimal.RequireFromString("5"), UpdatedAt: now,
	}
	if err := database.Create(&instrument).Error; err != nil {
		t.Fatalf("create paper instrument: %v", err)
	}
	publishRecord := createPaperTestIdempotency(t, database, owner.ID, "strategy:publish", 1)
	accountRecord := createPaperTestIdempotency(t, database, owner.ID, "trading-account:create", 2)
	draft := db.StrategyDraft{
		ID: strategyID, Name: "paper target", SourceCode: "def on_bar(candles, params): return Decimal('0.5')",
		Market: "spot", InstrumentID: instrumentID, Interval: "1m", LookbackBars: 2,
		ParameterSchemaJSON: "{}", RuntimeVersion: "python3.12", CreatedByUserID: owner.ID,
		UpdatedByUserID: owner.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.Create(&draft).Error; err != nil {
		t.Fatalf("create paper strategy: %v", err)
	}
	payload := fmt.Sprintf(`{"strategyId":%q,"strategyVersionId":%q}`, strategyID.String(), versionID.String())
	if err := database.Exec(`
INSERT INTO worker_tasks (id, task_type, payload_json, status, attempt_count, lane, finished_at)
VALUES (?, 'strategy.publish', ?, 'succeeded', 1, 'backtest', ?)
`, publishTaskID, payload, now).Error; err != nil {
		t.Fatalf("create paper strategy publish task: %v", err)
	}
	publishedAt := now
	version := db.StrategyVersion{
		ID: versionID, StrategyID: strategyID, VersionNumber: 1, Status: "published",
		WorkerTaskID: publishTaskID, IdempotencyRecordID: publishRecord.ID, Name: draft.Name,
		SourceCode: draft.SourceCode, CodeSHA256: strings.Repeat("a", 64), RuntimeVersion: "python3.12",
		Market: "spot", InstrumentID: instrumentID, Symbol: "BTCUSDT", Interval: "1m", LookbackBars: 2,
		ParameterSchemaJSON: "{}", PublishedByUserID: owner.ID, PublishedAt: &publishedAt, CreatedAt: now,
	}
	if err := database.Create(&version).Error; err != nil {
		t.Fatalf("create paper strategy version: %v", err)
	}
	initial := decimal.RequireFromString("10000")
	feeRate := decimal.RequireFromString("0.001")
	maxTotal := decimal.RequireFromString("5000")
	maxSymbol := decimal.RequireFromString("2500")
	maxOrder := decimal.RequireFromString("1000")
	maxDailyLoss := decimal.RequireFromString("500")
	maxDrawdown := decimal.RequireFromString("1000")
	quoteAge := 60
	authorizedAt := (*time.Time)(nil)
	authorizedBy := (*int64)(nil)
	if authorized {
		authorizedAt = &now
		authorizedBy = &owner.ID
	}
	if !riskComplete {
		maxDrawdown = decimal.Zero
	}
	account := db.TradingAccount{
		ID: accountID, OwnerUserID: owner.ID, Name: "Paper Spot", Market: "spot", Environment: "paper",
		Status: "active", PauseReason: "", AutomationEnabled: automationEnabled,
		AutomationAuthorizedAt: authorizedAt, AutomationAuthorizedByID: authorizedBy,
		InitialBalance: &initial, PaperFeeRate: &feeRate, MaxTotalNotional: &maxTotal,
		MaxSymbolNotional: &maxSymbol, MaxOrderNotional: &maxOrder, MaxDailyLoss: &maxDailyLoss,
		MaxDrawdown: &maxDrawdown, MaxQuoteAgeSeconds: &quoteAge,
		CreationIdempotencyRecordID: &accountRecord.ID, CreatedAt: now, UpdatedAt: now,
	}
	if !riskComplete {
		account.MaxDrawdown = nil
	}
	if err := database.Create(&account).Error; err != nil {
		t.Fatalf("create paper account: %v", err)
	}
	if err := database.Create(&db.TradingAccountInstrument{
		AccountID: accountID, InstrumentID: instrumentID, CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create paper instrument whitelist: %v", err)
	}
	if err := database.Create(&db.PaperBalance{
		AccountID: accountID, CashBalance: initial, Equity: initial, PeakEquity: initial,
		DayStartDate: utcDay(now), DayStartEquity: initial, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create paper balance: %v", err)
	}
	allocation := decimal.RequireFromString("1000")
	instance := db.StrategyInstance{
		ID: instanceID, OwnerUserID: owner.ID, StrategyVersionID: versionID,
		TradingAccountID: &accountID, AllocationUSDT: &allocation, Name: "paper instance",
		Mode: mode, Environment: "paper", ParametersJSON: "{}", IsEnabled: true,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.Create(&instance).Error; err != nil {
		t.Fatalf("create paper strategy instance: %v", err)
	}
	if err := database.Exec(`
INSERT INTO market_ticker_snapshots (
    venue, instrument_id, occurred_at, last_price, best_bid_price, best_ask_price
) VALUES ('binance', ?, ?, 100, 99.9, 100.1)
`, instrumentID, time.Now().UTC()).Error; err != nil {
		t.Fatalf("create paper quote: %v", err)
	}
	if err := database.Model(&db.TradingControl{}).Where("id = 1").Updates(map[string]any{
		"emergency_stopped": false, "stop_reason": "", "released_at": now,
		"released_by_user_id": owner.ID, "updated_at": now,
	}).Error; err != nil {
		t.Fatalf("release initial emergency stop: %v", err)
	}
	app := &App{DB: database, database: database}
	executor, err := NewPaperExecutor(database, "paper-test-worker", time.Millisecond)
	if err != nil {
		t.Fatalf("create paper executor: %v", err)
	}
	return &paperExecutorFixture{
		database: database, app: app, executor: executor, owner: owner, accountID: accountID,
		instrumentID: instrumentID, instanceID: instanceID, versionID: versionID, mode: mode,
	}
}

func (fixture *paperExecutorFixture) insertSignal(t *testing.T, target, environment string) db.StrategySignal {
	t.Helper()
	fixture.signalNumber++
	now := time.Now().UTC().Truncate(time.Microsecond)
	openTime := now.Add(time.Duration(fixture.signalNumber) * time.Minute)
	closeTime := openTime.Add(time.Minute)
	signal := db.StrategySignal{
		ID: mustUUIDv7(t), OwnerUserID: fixture.owner.ID, StrategyInstanceID: fixture.instanceID,
		StrategyVersionID: fixture.versionID, InstrumentID: fixture.instrumentID, Interval: "1m",
		CandleOpenTime: openTime, CandleCloseTime: closeTime,
		Target: decimal.RequireFromString(target), Mode: fixture.mode, Environment: environment,
		Status: "active", CreatedAt: now,
	}
	if fixture.mode == "manual" {
		record := createPaperTestIdempotency(
			t, fixture.database, fixture.owner.ID, "strategy-signal:decision:"+signal.ID.String(), fixture.signalNumber+10,
		)
		expiresAt := closeTime.Add(time.Hour)
		signal.Status = "approved"
		signal.ExpiresAt = &expiresAt
		signal.DecisionIdempotencyRecordID = &record.ID
		signal.DecidedByUserID = &fixture.owner.ID
		signal.DecidedAt = &now
	}
	if err := fixture.database.Create(&signal).Error; err != nil {
		t.Fatalf("create %s paper signal: %v", fixture.mode, err)
	}
	return signal
}

func (fixture *paperExecutorFixture) enqueueSignal(t *testing.T, signal db.StrategySignal) db.TradingIntent {
	t.Helper()
	if err := fixture.app.createPaperIntentForSignalWithDB(fixture.database, signal, true); err != nil {
		t.Fatalf("enqueue paper signal: %v", err)
	}
	var intent db.TradingIntent
	if err := fixture.database.Where("strategy_signal_id = ?", signal.ID).Take(&intent).Error; err != nil {
		t.Fatalf("load paper intent: %v", err)
	}
	return intent
}

func (fixture *paperExecutorFixture) activateEmergencyStop(t *testing.T) {
	t.Helper()
	now := time.Now().UTC()
	if err := fixture.database.Model(&db.TradingControl{}).Where("id = 1").Updates(map[string]any{
		"emergency_stopped": true, "stop_reason": "paper-test-stop", "stopped_at": now,
		"stopped_by_user_id": fixture.owner.ID, "released_at": nil, "released_by_user_id": nil,
		"updated_at": now,
	}).Error; err != nil {
		t.Fatalf("activate emergency stop: %v", err)
	}
	if err := fixture.database.Model(&db.TradingAccount{}).Where("id = ?", fixture.accountID).Updates(map[string]any{
		"status": "paused", "pause_reason": "global_emergency_stop", "automation_enabled": false, "updated_at": now,
	}).Error; err != nil {
		t.Fatalf("pause account for emergency stop: %v", err)
	}
}

func (fixture *paperExecutorFixture) releaseEmergencyStop(t *testing.T) {
	t.Helper()
	now := time.Now().UTC()
	if err := fixture.database.Model(&db.TradingControl{}).Where("id = 1").Updates(map[string]any{
		"emergency_stopped": false, "stop_reason": "", "released_at": now,
		"released_by_user_id": fixture.owner.ID, "updated_at": now,
	}).Error; err != nil {
		t.Fatalf("release emergency stop: %v", err)
	}
}

func createPaperTestIdempotency(t *testing.T, database *gorm.DB, userID int64, scope string, number int) db.IdempotencyRecord {
	t.Helper()
	record := db.IdempotencyRecord{
		UserID: userID, Scope: scope, KeyHash: fmt.Sprintf("%064x", number),
		RequestHash: fmt.Sprintf("%064x", number+1000), ExpiresAt: time.Now().UTC().Add(time.Hour),
		CreatedAt: time.Now().UTC(),
	}
	if err := database.Create(&record).Error; err != nil {
		t.Fatalf("create paper idempotency record: %v", err)
	}
	return record
}

func assertTradingIntentState(t *testing.T, database *gorm.DB, id uuid.UUID, status, reason string) {
	t.Helper()
	var intent db.TradingIntent
	if err := database.Where("id = ?", id).Take(&intent).Error; err != nil {
		t.Fatalf("load paper intent state: %v", err)
	}
	if intent.Status != status || intent.BlockReason != reason {
		t.Fatalf("paper intent state = %s/%s, want %s/%s", intent.Status, intent.BlockReason, status, reason)
	}
}

func assertPaperPositionQuantity(
	t *testing.T, database *gorm.DB, accountID, instrumentID uuid.UUID, want string,
) {
	t.Helper()
	var position db.PaperPosition
	if err := database.Where("account_id = ? AND instrument_id = ?", accountID, instrumentID).Take(&position).Error; err != nil {
		t.Fatalf("load paper position: %v", err)
	}
	if position.Quantity.String() != want {
		t.Fatalf("paper position quantity = %s, want %s", position.Quantity.String(), want)
	}
}

func assertRowCountGORM(t *testing.T, database *gorm.DB, model any, query string, value any, want int64) {
	t.Helper()
	var count int64
	if err := database.Model(model).Where(query, value).Count(&count).Error; err != nil {
		t.Fatalf("count %T rows: %v", model, err)
	}
	if count != want {
		t.Fatalf("%T row count = %d, want %d", model, count, want)
	}
}

func loadPaperProjections(
	t *testing.T, database *gorm.DB, accountID uuid.UUID,
) ([]db.PaperOrder, []db.PaperPosition, []db.PaperBalance) {
	t.Helper()
	var orders []db.PaperOrder
	if err := database.Where("account_id = ?", accountID).Order("id").Find(&orders).Error; err != nil {
		t.Fatalf("load paper orders: %v", err)
	}
	var positions []db.PaperPosition
	if err := database.Where("account_id = ?", accountID).Order("instrument_id").Find(&positions).Error; err != nil {
		t.Fatalf("load paper positions: %v", err)
	}
	var balances []db.PaperBalance
	if err := database.Where("account_id = ?", accountID).Order("account_id").Find(&balances).Error; err != nil {
		t.Fatalf("load paper balances: %v", err)
	}
	return orders, positions, balances
}

func mustUUIDv7(t *testing.T) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("create UUIDv7: %v", err)
	}
	return id
}
