package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"coinsphere/backend/internal/db"
	exchangebinance "coinsphere/backend/internal/exchange/binance"
	"coinsphere/backend/internal/marketdata"
	"coinsphere/backend/internal/security"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

func TestTestnetAccountReconcilerMatchesCleanSnapshotAndGatesResume(t *testing.T) {
	fixture := newTestnetReconcilerFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-MBX-APIKEY") != fixture.apiKey {
			t.Error("reconciliation request used an unexpected API key")
		}
		switch request.URL.Path {
		case "/api/v3/account":
			_, _ = response.Write([]byte(`{"canTrade":true,"balances":[{"asset":"USDT","free":"1000","locked":"0"},{"asset":"BTC","free":"0","locked":"0"}]}`))
		case "/api/v3/openOrders":
			_, _ = response.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected reconciliation path %q", request.URL.Path)
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	reconciler := fixture.reconciler(t, server.URL)

	resumeToken := fixture.app.issueReauthToken(fixture.principal, time.Now())
	if _, err := fixture.app.ResumeTradingAccount(
		context.Background(), fixture.principal, fixture.account.ID.String(), "reconcile-resume-before", resumeToken,
	); !errors.Is(err, ErrTradingReconciliationRequired) {
		t.Fatalf("resume before reconciliation returned %v", err)
	}
	processed, retryAfter, err := reconciler.ProcessNext(context.Background())
	if err != nil || !processed || retryAfter != 0 {
		t.Fatalf("process clean reconciliation: processed=%t retry=%v err=%v", processed, retryAfter, err)
	}

	var reconciliation db.TestnetReconciliation
	if err := fixture.database.Where("account_id = ?", fixture.account.ID).Take(&reconciliation).Error; err != nil {
		t.Fatalf("load matched reconciliation: %v", err)
	}
	if reconciliation.Status != "matched" || reconciliation.ErrorCode != "" ||
		reconciliation.BalanceCount != 1 || reconciliation.PositionCount != 0 || reconciliation.OpenOrderCount != 0 ||
		reconciliation.LastObservedAt == nil {
		t.Fatalf("matched reconciliation = %#v", reconciliation)
	}
	var risk db.TestnetRiskState
	if err := fixture.database.Where("account_id = ?", fixture.account.ID).Take(&risk).Error; err != nil {
		t.Fatalf("load Testnet risk baseline: %v", err)
	}
	var credential db.TradingAccountCredential
	if err := fixture.database.Where("id = ?", fixture.credential.ID).Take(&credential).Error; err != nil {
		t.Fatalf("reload reconciled Testnet credential: %v", err)
	}
	if !risk.BaselineEquity.Equal(decimal.NewFromInt(1000)) || !risk.Equity.Equal(risk.BaselineEquity) ||
		!risk.PeakEquity.Equal(risk.BaselineEquity) || !risk.DayStartEquity.Equal(risk.BaselineEquity) ||
		!risk.CredentialUpdatedAt.Equal(credential.UpdatedAt) {
		t.Fatalf("Testnet risk baseline = %#v", risk)
	}
	var storedAccount db.TradingAccount
	if err := fixture.database.Where("id = ?", fixture.account.ID).Take(&storedAccount).Error; err != nil {
		t.Fatalf("reload reconciled account: %v", err)
	}
	if storedAccount.Status != "paused" || storedAccount.PauseReason != "testnet_reconciled_manual_release_required" || storedAccount.AutomationEnabled {
		t.Fatalf("reconciliation released account automatically: %#v", storedAccount)
	}
	overview, err := fixture.app.GetTradingOverview(context.Background(), fixture.owner.ID)
	if err != nil {
		t.Fatalf("load reconciled trading overview: %v", err)
	}
	if len(overview.Accounts) != 1 || overview.Accounts[0].Reconciliation.Status != "matched" ||
		len(overview.TestnetBalances) != 1 || overview.TestnetBalances[0].Asset != "USDT" ||
		overview.TestnetBalances[0].TotalBalance != "1000" || len(overview.TestnetPositions) != 0 ||
		len(overview.TestnetOpenOrders) != 0 {
		t.Fatalf("reconciled overview = %#v", overview)
	}

	resumeToken = fixture.app.issueReauthToken(fixture.principal, time.Now())
	resumed, err := fixture.app.ResumeTradingAccount(
		context.Background(), fixture.principal, fixture.account.ID.String(), "reconcile-resume-after", resumeToken,
	)
	if err != nil || resumed.Status != "active" {
		t.Fatalf("resume matched Testnet account = %#v, err=%v", resumed, err)
	}
}

func TestTestnetAccountReconcilerPersistsClosePositionOrderShape(t *testing.T) {
	fixture := newTestnetReconcilerFixture(t)
	reconciler := fixture.reconciler(t, "http://127.0.0.1")
	observedAt := time.Date(2026, time.August, 9, 10, 11, 12, 0, time.UTC)
	snapshot := exchangebinance.AccountSnapshot{
		CanTrade: true,
		OpenOrders: []exchangebinance.OpenOrder{{
			Symbol: "BTCUSDT", ExchangeOrderID: 42, ClientOrderID: "external-close-position",
			Side: "sell", OrderType: "stop_market", Status: "new",
			Price: decimal.Zero, OriginalQuantity: decimal.Zero, ExecutedQuantity: decimal.Zero,
			StopPrice: decimal.NewFromInt(52_000), ClosePosition: true, WorkingType: "mark_price",
		}},
		ObservedAt: observedAt,
	}

	persisted, err := reconciler.persistSnapshot(
		context.Background(), fixture.credential, fixture.account, snapshot, "mismatch", "open_orders_present",
	)
	if err != nil || !persisted {
		t.Fatalf("persist close-position snapshot: persisted=%t err=%v", persisted, err)
	}
	var stored db.TestnetOpenOrder
	if err := fixture.database.Where("account_id = ?", fixture.account.ID).Take(&stored).Error; err != nil {
		t.Fatalf("load close-position order: %v", err)
	}
	if !stored.OriginalQuantity.IsZero() || !stored.ClosePosition || stored.ReduceOnly || stored.WorkingType != "mark_price" {
		t.Fatalf("stored close-position order = %#v", stored)
	}
	overview, err := fixture.app.GetTradingOverview(context.Background(), fixture.owner.ID)
	if err != nil || len(overview.TestnetOpenOrders) != 1 {
		t.Fatalf("load close-position overview: orders=%#v err=%v", overview.TestnetOpenOrders, err)
	}
	order := overview.TestnetOpenOrders[0]
	if order.OriginalQuantity != "0" || !order.ClosePosition || order.ReduceOnly || order.WorkingType != "mark_price" {
		t.Fatalf("close-position overview order = %#v", order)
	}
}

func TestTestnetAccountReconcilerPersistsMismatchAndIgnoresStaleResult(t *testing.T) {
	fixture := newTestnetReconcilerFixture(t)
	var mode atomic.Int32
	requestStarted := make(chan struct{}, 1)
	releaseRequest := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v3/account":
			if mode.Load() == 1 {
				requestStarted <- struct{}{}
				<-releaseRequest
			}
			_, _ = response.Write([]byte(`{"canTrade":true,"balances":[{"asset":"USDT","free":"1000","locked":"0"}]}`))
		case "/api/v3/openOrders":
			if mode.Load() == 0 {
				_, _ = response.Write([]byte(`[{"symbol":"BTCUSDT","orderId":42,"clientOrderId":"external-order","side":"SELL","type":"LIMIT","status":"NEW","price":"50000","origQty":"0.01","executedQty":"0","stopPrice":"0"}]`))
				return
			}
			_, _ = response.Write([]byte(`[]`))
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	reconciler := fixture.reconciler(t, server.URL)

	processed, retryAfter, err := reconciler.ProcessNext(context.Background())
	if err != nil || !processed || retryAfter != 10*time.Millisecond {
		t.Fatalf("process mismatched reconciliation: processed=%t retry=%v err=%v", processed, retryAfter, err)
	}
	var mismatch db.TestnetReconciliation
	if err := fixture.database.Where("account_id = ?", fixture.account.ID).Take(&mismatch).Error; err != nil {
		t.Fatalf("load mismatched reconciliation: %v", err)
	}
	if mismatch.Status != "mismatch" || mismatch.ErrorCode != "open_orders_present" || mismatch.OpenOrderCount != 1 {
		t.Fatalf("mismatched reconciliation = %#v", mismatch)
	}
	var openOrders int64
	if err := fixture.database.Model(&db.TestnetOpenOrder{}).Where("account_id = ?", fixture.account.ID).Count(&openOrders).Error; err != nil || openOrders != 1 {
		t.Fatalf("persisted open orders = %d, err=%v", openOrders, err)
	}
	if err := testnetAccountReadinessError(fixture.database, fixture.account.ID); !errors.Is(err, ErrTradingReconciliationRequired) {
		t.Fatalf("mismatched account readiness returned %v", err)
	}

	if err := fixture.database.Transaction(func(tx *gorm.DB) error {
		return clearTestnetReconciliation(tx, fixture.account.ID)
	}); err != nil {
		t.Fatalf("clear mismatch before stale request: %v", err)
	}
	mode.Store(1)
	result := make(chan error, 1)
	go func() {
		_, _, processErr := reconciler.ProcessNext(context.Background())
		result <- processErr
	}()
	select {
	case <-requestStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("stale reconciliation request did not start")
	}
	newUpdatedAt := fixture.credential.UpdatedAt.Add(time.Second)
	if err := fixture.database.Model(&db.TradingAccountCredential{}).Where("id = ?", fixture.credential.ID).Updates(map[string]any{
		"api_key_ciphertext":    fixture.cipher.Encrypt(strings.Repeat("n", 32)),
		"api_secret_ciphertext": fixture.cipher.Encrypt(strings.Repeat("m", 32)),
		"verification_status":   "unverified", "verification_error_code": "", "last_verified_at": nil,
		"updated_at": newUpdatedAt,
	}).Error; err != nil {
		t.Fatalf("replace credential during reconciliation: %v", err)
	}
	close(releaseRequest)
	if err := <-result; err != nil {
		t.Fatalf("finish stale reconciliation: %v", err)
	}
	var reconciliationCount int64
	if err := fixture.database.Model(&db.TestnetReconciliation{}).Where("account_id = ?", fixture.account.ID).Count(&reconciliationCount).Error; err != nil {
		t.Fatalf("count stale reconciliation results: %v", err)
	}
	if reconciliationCount != 0 {
		t.Fatal("stale reconciliation recreated a projection for replaced credentials")
	}
}

func TestTestnetAccountReconcilerIgnoresSnapshotAfterAccountConfigurationChanges(t *testing.T) {
	fixture := newTestnetReconcilerFixture(t)
	requestStarted := make(chan struct{}, 1)
	releaseRequest := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v3/account":
			requestStarted <- struct{}{}
			<-releaseRequest
			_, _ = response.Write([]byte(`{"canTrade":true,"balances":[{"asset":"USDT","free":"1000","locked":"0"}]}`))
		case "/api/v3/openOrders":
			_, _ = response.Write([]byte(`[]`))
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	reconciler := fixture.reconciler(t, server.URL)

	result := make(chan error, 1)
	go func() {
		_, _, processErr := reconciler.ProcessNext(context.Background())
		result <- processErr
	}()
	select {
	case <-requestStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("reconciliation request did not start")
	}
	if err := fixture.database.Model(&db.TradingAccount{}).Where("id = ?", fixture.account.ID).Updates(map[string]any{
		"max_order_notional": decimal.NewFromInt(900),
		"pause_reason":       "risk_configuration_changed",
		"updated_at":         time.Now().UTC().Add(time.Minute),
	}).Error; err != nil {
		t.Fatalf("change account configuration during reconciliation: %v", err)
	}
	close(releaseRequest)
	if err := <-result; err != nil {
		t.Fatalf("finish stale account reconciliation: %v", err)
	}
	var reconciliationCount int64
	if err := fixture.database.Model(&db.TestnetReconciliation{}).Where("account_id = ?", fixture.account.ID).Count(&reconciliationCount).Error; err != nil {
		t.Fatalf("count stale account reconciliation results: %v", err)
	}
	if reconciliationCount != 0 {
		t.Fatal("stale reconciliation recreated a projection after account configuration changed")
	}
}

func TestTestnetAccountReconcilerRejectsNonWhitelistedAvailableBalance(t *testing.T) {
	fixture := newTestnetReconcilerFixture(t)
	reconciler := &TestnetAccountReconciler{database: fixture.database}
	status, errorCode, err := reconciler.classifySnapshot(context.Background(), fixture.account, exchangebinance.AccountSnapshot{
		CanTrade: true,
		Balances: []exchangebinance.AccountBalance{{Asset: "BUSD", Available: decimal.NewFromInt(-1)}},
	})
	if err != nil || status != "mismatch" || errorCode != "spot_inventory_present" {
		t.Fatalf("non-whitelisted available balance classification = %q, %q, %v", status, errorCode, err)
	}
}

func TestTestnetReconciliationFailureClassification(t *testing.T) {
	code, retryAfter, invalid := testnetReconciliationFailure(&exchangebinance.PrivateError{
		Kind: exchangebinance.PrivateErrorRateLimited, RetryAfter: 2 * time.Second,
	})
	if code != "rate_limited" || retryAfter != 2*time.Second || invalid {
		t.Fatalf("rate limit classification = %q, %v, %t", code, retryAfter, invalid)
	}
	code, retryAfter, invalid = testnetReconciliationFailure(&exchangebinance.PrivateError{Kind: exchangebinance.PrivateErrorAuthentication})
	if code != "authentication_failed" || retryAfter != 0 || !invalid {
		t.Fatalf("authentication classification = %q, %v, %t", code, retryAfter, invalid)
	}
}

func TestContinuousSnapshotDifferenceAcceptsManagedUSDMPosition(t *testing.T) {
	instrumentID := mustVerifierUUIDv7(t)
	instrument := db.MarketInstrument{
		ID: instrumentID, NativeSymbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT",
	}
	position := &testnetManagedPosition{InstrumentID: instrumentID, Quantity: decimal.RequireFromString("0.02")}
	code := continuousSnapshotDifference(
		db.TradingAccount{Market: string(marketdata.MarketTypeUSDM)},
		map[uuid.UUID]db.MarketInstrument{instrumentID: instrument},
		map[string]db.MarketInstrument{instrument.NativeSymbol: instrument},
		map[uuid.UUID]*testnetManagedPosition{instrumentID: position},
		exchangebinance.AccountSnapshot{
			CanTrade: true,
			Positions: []exchangebinance.AccountPosition{{
				Symbol: "BTCUSDT", PositionSide: "both", Quantity: decimal.RequireFromString("0.02"),
			}},
			Balances: []exchangebinance.AccountBalance{{Asset: "USDT", Total: decimal.NewFromInt(1000)}},
		},
	)
	if code != "" {
		t.Fatalf("managed USD-M position difference = %q", code)
	}
}

func TestContinuousSnapshotDifferenceRejectsUnownedOrderState(t *testing.T) {
	fixture := newTestnetReconcilerFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v3/account":
			_, _ = response.Write([]byte(`{"canTrade":true,"balances":[{"asset":"USDT","free":"1000","locked":"0"}]}`))
		case "/api/v3/openOrders":
			_, _ = response.Write([]byte(`[{"symbol":"BTCUSDT","orderId":99,"clientOrderId":"external-order","side":"SELL","type":"LIMIT","status":"NEW","price":"50000","origQty":"0.01","executedQty":"0","stopPrice":"0"}]`))
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	reconciler := fixture.reconciler(t, server.URL)
	observedAt := time.Now().UTC().Add(-time.Minute)
	if _, err := reconciler.persistSnapshot(context.Background(), fixture.credential, fixture.account, exchangebinance.AccountSnapshot{
		CanTrade: true, Balances: []exchangebinance.AccountBalance{{Asset: "USDT", Total: decimal.NewFromInt(1000)}}, ObservedAt: observedAt,
	}, "matched", ""); err != nil {
		t.Fatalf("seed continuous reconciliation: %v", err)
	}
	var account db.TradingAccount
	if err := fixture.database.Where("id = ?", fixture.account.ID).Take(&account).Error; err != nil {
		t.Fatalf("load continuous account: %v", err)
	}
	if err := fixture.database.Model(&account).Updates(map[string]any{
		"status": "active", "pause_reason": "", "updated_at": time.Now().UTC(),
	}).Error; err != nil {
		t.Fatalf("activate continuous account: %v", err)
	}
	if err := fixture.database.Model(&db.TestnetReconciliation{}).Where("account_id = ?", account.ID).
		Updates(map[string]any{"last_attempted_at": time.Now().UTC().Add(-time.Hour)}).Error; err != nil {
		t.Fatalf("age reconciliation: %v", err)
	}
	processed, _, err := reconciler.ProcessNext(context.Background())
	if err != nil || !processed {
		t.Fatalf("process unknown external order: processed=%t err=%v", processed, err)
	}
	var reconciliation db.TestnetReconciliation
	if err := fixture.database.Where("account_id = ?", account.ID).Take(&reconciliation).Error; err != nil {
		t.Fatalf("load external-order reconciliation: %v", err)
	}
	if reconciliation.Status != "mismatch" || reconciliation.ErrorCode != "unknown_external_order" {
		t.Fatalf("external-order reconciliation = %#v", reconciliation)
	}
	if err := fixture.database.Where("id = ?", account.ID).Take(&account).Error; err != nil {
		t.Fatalf("reload paused account: %v", err)
	}
	if account.Status != "paused" || account.PauseReason != "testnet_reconciliation_mismatch" || account.AutomationEnabled {
		t.Fatalf("unknown external order did not pause account: %#v", account)
	}
}

func TestTestnetReconcilerIgnoresOlderSnapshotProjection(t *testing.T) {
	fixture := newTestnetReconcilerFixture(t)
	reconciler := fixture.reconciler(t, "http://127.0.0.1")
	newer := time.Now().UTC().Add(-time.Minute)
	older := newer.Add(-time.Minute)
	first := exchangebinance.AccountSnapshot{
		CanTrade: true, Balances: []exchangebinance.AccountBalance{{Asset: "USDT", Total: decimal.NewFromInt(1000)}}, ObservedAt: newer,
	}
	if _, err := reconciler.persistSnapshot(context.Background(), fixture.credential, fixture.account, first, "matched", ""); err != nil {
		t.Fatalf("persist newer snapshot: %v", err)
	}
	var account db.TradingAccount
	if err := fixture.database.Where("id = ?", fixture.account.ID).Take(&account).Error; err != nil {
		t.Fatalf("load account after newer snapshot: %v", err)
	}
	persisted, err := reconciler.persistSnapshot(context.Background(), fixture.credential, account, exchangebinance.AccountSnapshot{
		CanTrade: true, Balances: []exchangebinance.AccountBalance{{Asset: "USDT", Total: decimal.NewFromInt(1)}}, ObservedAt: older,
	}, "matched", "")
	if err != nil || persisted {
		t.Fatalf("older snapshot persisted=%t err=%v", persisted, err)
	}
	var balance db.TestnetBalance
	if err := fixture.database.Where("account_id = ? AND asset = ?", account.ID, "USDT").Take(&balance).Error; err != nil {
		t.Fatalf("load fenced balance: %v", err)
	}
	if !balance.TotalBalance.Equal(decimal.NewFromInt(1000)) {
		t.Fatalf("older snapshot changed balance = %s", balance.TotalBalance)
	}
}

type testnetReconcilerFixture struct {
	database   *gorm.DB
	app        *App
	principal  *Principal
	owner      db.SystemUser
	account    db.TradingAccount
	credential db.TradingAccountCredential
	cipher     *security.SecretCipher
	apiKey     string
	apiSecret  string
}

func newTestnetReconcilerFixture(t *testing.T) testnetReconcilerFixture {
	t.Helper()
	database := openPostgresWorkflowContractDatabase(t).primary
	now := time.Now().UTC().Truncate(time.Microsecond)
	owner := db.SystemUser{Username: "testnet-reconciler-owner", IsActive: true, CreatedAt: now, UpdatedAt: now}
	if err := database.Create(&owner).Error; err != nil {
		t.Fatalf("create reconciler owner: %v", err)
	}
	instrument := db.MarketInstrument{
		ID: mustVerifierUUIDv7(t), Venue: string(marketdata.VenueBinance), Market: string(marketdata.MarketTypeSpot),
		NativeSymbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT", Status: "trading",
		PriceTick: decimal.RequireFromString("0.1"), QuantityStep: decimal.RequireFromString("0.001"),
		MinQuantity: decimal.RequireFromString("0.001"), MinNotional: decimal.RequireFromString("5"), UpdatedAt: now,
	}
	if err := database.Create(&instrument).Error; err != nil {
		t.Fatalf("create reconciler instrument: %v", err)
	}
	idempotency := db.IdempotencyRecord{
		UserID: owner.ID, Scope: "testnet-reconciler-account", KeyHash: strings.Repeat("a", 64),
		RequestHash: strings.Repeat("b", 64), ExpiresAt: now.Add(time.Hour), CreatedAt: now,
	}
	if err := database.Create(&idempotency).Error; err != nil {
		t.Fatalf("create reconciler idempotency record: %v", err)
	}
	initialBalance := decimal.NewFromInt(10_000)
	feeRate := decimal.RequireFromString("0.001")
	maxTotal := decimal.NewFromInt(5_000)
	maxSymbol := decimal.NewFromInt(2_500)
	maxOrder := decimal.NewFromInt(1_000)
	maxDailyLoss := decimal.NewFromInt(500)
	maxDrawdown := decimal.NewFromInt(1_000)
	maxQuoteAge := 30
	account := db.TradingAccount{
		ID: mustVerifierUUIDv7(t), OwnerUserID: owner.ID, Name: "Reconciler Spot",
		Market: "spot", Environment: "testnet", Status: "paused", PauseReason: "testnet_reconciliation_required",
		InitialBalance: &initialBalance, PaperFeeRate: &feeRate,
		MaxTotalNotional: &maxTotal, MaxSymbolNotional: &maxSymbol, MaxOrderNotional: &maxOrder,
		MaxDailyLoss: &maxDailyLoss, MaxDrawdown: &maxDrawdown, MaxQuoteAgeSeconds: &maxQuoteAge,
		CreationIdempotencyRecordID: &idempotency.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.Create(&account).Error; err != nil {
		t.Fatalf("create reconciler account: %v", err)
	}
	if err := database.Create(&db.TradingAccountInstrument{AccountID: account.ID, InstrumentID: instrument.ID, CreatedAt: now}).Error; err != nil {
		t.Fatalf("create reconciler whitelist: %v", err)
	}
	cipher, err := security.NewSecretCipher(strings.Repeat("c", 32))
	if err != nil {
		t.Fatalf("create reconciler cipher: %v", err)
	}
	apiKey := strings.Repeat("k", 32)
	apiSecret := strings.Repeat("s", 32)
	verifiedAt := now
	credential := db.TradingAccountCredential{
		ID: mustVerifierUUIDv7(t), AccountID: account.ID, OwnerUserID: owner.ID,
		APIKeyCiphertext: cipher.Encrypt(apiKey), APISecretCiphertext: cipher.Encrypt(apiSecret),
		WithdrawalDisabled: true, IPWhitelistConfigured: true, Status: "configured", VerificationStatus: "verified",
		LastVerifiedAt: &verifiedAt, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.Create(&credential).Error; err != nil {
		t.Fatalf("create reconciler credential: %v", err)
	}
	releasedAt := now.Add(time.Second)
	if err := database.Model(&db.TradingControl{}).Where("id = 1").Updates(map[string]any{
		"emergency_stopped": false, "stop_reason": "", "released_at": releasedAt,
		"released_by_user_id": owner.ID, "updated_at": releasedAt,
	}).Error; err != nil {
		t.Fatalf("release reconciler emergency stop: %v", err)
	}
	app := &App{
		DB: database, database: database, Cipher: cipher,
		reauthTokens: map[string]reauthTokenRecord{}, revokedAccessTokens: map[string]time.Time{},
	}
	principal := &Principal{User: &owner, AccessTokenID: "testnet-reconciler-session"}
	return testnetReconcilerFixture{
		database: database, app: app, principal: principal, owner: owner, account: account,
		credential: credential, cipher: cipher, apiKey: apiKey, apiSecret: apiSecret,
	}
}

func (fixture testnetReconcilerFixture) reconciler(t *testing.T, serverURL string) *TestnetAccountReconciler {
	t.Helper()
	client, err := exchangebinance.NewPrivateClient(exchangebinance.PrivateClientConfig{
		SpotBaseURL: serverURL, USDMBaseURL: serverURL,
	})
	if err != nil {
		t.Fatalf("create reconciler private client: %v", err)
	}
	reconciler, err := NewTestnetAccountReconciler(fixture.database, fixture.cipher, client, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("create Testnet reconciler: %v", err)
	}
	return reconciler
}
