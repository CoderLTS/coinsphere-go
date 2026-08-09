package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/security"
	"github.com/google/uuid"
)

func TestStrategyInstanceValidationDefaultsAndBoundaries(t *testing.T) {
	schema, err := validateParameterSchema(map[string]json.RawMessage{
		"threshold": json.RawMessage(`{"type":"decimal","minimum":"0","maximum":"1"}`),
	})
	if err != nil {
		t.Fatalf("validate instance schema: %v", err)
	}
	version := db.StrategyVersion{
		ID:                  uuid.MustParse("019d4000-0000-7000-8000-000000000001"),
		ParameterSchemaJSON: string(schema),
	}
	validated, err := validateStrategyInstancePayload(StrategyInstanceCreatePayload{
		Name:       "  paper hold  ",
		Parameters: map[string]json.RawMessage{"threshold": json.RawMessage(`"0.2500"`)},
	}, version)
	if err != nil {
		t.Fatalf("validate default instance: %v", err)
	}
	if validated.Name != "paper hold" || validated.Mode != "signal_only" || validated.Environment != "paper" {
		t.Fatalf("normalized instance = %#v", validated)
	}
	if validated.ParametersJSON != `{"threshold":"0.2500"}` {
		t.Fatalf("normalized parameters = %s", validated.ParametersJSON)
	}
	testnet, err := validateStrategyInstancePayload(StrategyInstanceCreatePayload{
		Name: "testnet protected", Mode: "manual", Environment: "testnet",
		TradingAccountID: "019d4000-0000-7000-8000-000000000002",
		AllocationUSDT:   "1000", StopLossRatio: "0.0500",
		Parameters: map[string]json.RawMessage{"threshold": json.RawMessage(`"0.2500"`)},
	}, version)
	if err != nil {
		t.Fatalf("validate protected Testnet instance: %v", err)
	}
	if testnet.StopLossRatio == nil || testnet.StopLossRatio.String() != "0.05" {
		t.Fatalf("normalized Testnet stop loss = %#v", testnet.StopLossRatio)
	}
	view := serializeStrategyInstance(db.StrategyInstance{StopLossRatio: testnet.StopLossRatio})
	if view.StopLossRatio == nil || *view.StopLossRatio != "0.05" {
		t.Fatalf("serialized Testnet stop loss = %#v", view.StopLossRatio)
	}

	invalid := []StrategyInstanceCreatePayload{
		{Name: "", Mode: "signal_only"},
		{Name: "ok", Mode: "unsupported"},
		{Name: "ok", Environment: "sandbox"},
		{Name: "ok", Parameters: map[string]json.RawMessage{"unknown": json.RawMessage(`1`)}},
		{Name: "ok", Parameters: map[string]json.RawMessage{"threshold": json.RawMessage(`2`)}},
		{Name: "missing stop", Mode: "manual", Environment: "testnet", TradingAccountID: "019d4000-0000-7000-8000-000000000002", AllocationUSDT: "1000"},
		{Name: "zero stop", Mode: "manual", Environment: "testnet", TradingAccountID: "019d4000-0000-7000-8000-000000000002", AllocationUSDT: "1000", StopLossRatio: "0"},
		{Name: "full stop", Mode: "manual", Environment: "testnet", TradingAccountID: "019d4000-0000-7000-8000-000000000002", AllocationUSDT: "1000", StopLossRatio: "1"},
		{Name: "paper stop", Mode: "manual", Environment: "paper", TradingAccountID: "019d4000-0000-7000-8000-000000000002", AllocationUSDT: "1000", StopLossRatio: "0.05"},
	}
	for _, payload := range invalid {
		if _, err := validateStrategyInstancePayload(payload, version); !errors.Is(err, ErrInvalidStrategyRequest) {
			t.Fatalf("invalid instance %#v returned %v", payload, err)
		}
	}
}

func TestStrategySignalDecisionAndNotificationContract(t *testing.T) {
	database := openPostgresWorkflowContractDatabase(t).primary
	owner := db.SystemUser{Username: "m2-signal-owner", IsActive: true}
	if err := database.Create(&owner).Error; err != nil {
		t.Fatalf("create signal owner: %v", err)
	}
	const instrumentID = "019d6000-0000-7000-8000-000000000001"
	const strategyID = "019d6000-0000-7000-8000-000000000010"
	const versionID = "019d6000-0000-7000-8000-000000000011"
	const publishTaskID = "019d6000-0000-7000-8000-000000000012"
	const instanceID = "019d6000-0000-7000-8000-000000000013"
	const signalID = "019d6000-0000-7000-8000-000000000014"
	const accountID = "019d6000-0000-7000-8000-000000000016"
	if err := database.Exec(`
INSERT INTO market_instruments (
    id, venue, market_type, native_symbol, base_asset, quote_asset, status,
    price_tick, quantity_step, min_quantity, min_notional, updated_at
) VALUES (?, 'binance', 'spot', 'BTCUSDT', 'BTC', 'USDT', 'trading',
          0.1, 0.001, 0.001, 5, CURRENT_TIMESTAMP)
`, instrumentID).Error; err != nil {
		t.Fatalf("create signal instrument: %v", err)
	}
	publishRecord := db.IdempotencyRecord{
		UserID: owner.ID, Scope: "strategy:publish:m2-service", KeyHash: strings.Repeat("a", 64),
		RequestHash: strings.Repeat("b", 64), ExpiresAt: time.Now().UTC().Add(time.Hour), CreatedAt: time.Now().UTC(),
	}
	if err := database.Create(&publishRecord).Error; err != nil {
		t.Fatalf("create publish idempotency record: %v", err)
	}
	accountRecord := db.IdempotencyRecord{
		UserID: owner.ID, Scope: "trading-account:create:m2-service", KeyHash: strings.Repeat("d", 64),
		RequestHash: strings.Repeat("e", 64), ExpiresAt: time.Now().UTC().Add(time.Hour), CreatedAt: time.Now().UTC(),
	}
	if err := database.Create(&accountRecord).Error; err != nil {
		t.Fatalf("create paper account idempotency record: %v", err)
	}
	if err := database.Exec(`
INSERT INTO trading_accounts (
    id, owner_user_id, name, market_type, environment, initial_balance,
    paper_fee_rate, creation_idempotency_record_id
) VALUES (?, ?, 'm2 paper', 'spot', 'paper', 10000, 0.001, ?)
`, accountID, owner.ID, accountRecord.ID).Error; err != nil {
		t.Fatalf("create paper account: %v", err)
	}
	if err := database.Exec(`
INSERT INTO strategies (
    id, name, source_code, market_type, instrument_id, interval_code, lookback_bars,
    parameter_schema_json, created_by_user_id, updated_by_user_id
) VALUES (?, 'm2 decision', 'def on_bar(candles, params): return Decimal(''0.5'')',
          'spot', ?, '1m', 2, '{}', ?, ?)
`, strategyID, instrumentID, owner.ID, owner.ID).Error; err != nil {
		t.Fatalf("create signal strategy: %v", err)
	}
	if err := database.Exec(`
INSERT INTO worker_tasks (id, task_type, payload_json, status, attempt_count, lane, finished_at)
VALUES (?, 'strategy.publish', ?, 'succeeded', 1, 'backtest', CURRENT_TIMESTAMP)
`, publishTaskID, `{"strategyId":"`+strategyID+`","strategyVersionId":"`+versionID+`"}`).Error; err != nil {
		t.Fatalf("create signal publish task: %v", err)
	}
	if err := database.Exec(`
INSERT INTO strategy_versions (
    id, strategy_id, version_number, status, worker_task_id, idempotency_record_id,
    name, source_code, code_sha256, runtime_version, market_type, instrument_id, symbol,
    interval_code, lookback_bars, parameter_schema_json, published_by_user_id, published_at
) VALUES (?, ?, 1, 'published', ?, ?, 'm2 decision',
          'def on_bar(candles, params): return Decimal(''0.5'')', ?, 'python3.12',
          'spot', ?, 'BTCUSDT', '1m', 2, '{}', ?, CURRENT_TIMESTAMP)
`, versionID, strategyID, publishTaskID, publishRecord.ID, strings.Repeat("c", 64), instrumentID, owner.ID).Error; err != nil {
		t.Fatalf("create signal strategy version: %v", err)
	}
	if err := database.Exec(`
INSERT INTO strategy_instances (
    id, owner_user_id, strategy_version_id, trading_account_id, allocation_usdt,
    name, mode, environment, is_enabled
) VALUES (?, ?, ?, ?, 1000, 'm2 manual', 'manual', 'paper', TRUE)
`, instanceID, owner.ID, versionID, accountID).Error; err != nil {
		t.Fatalf("create strategy instance: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	expiresAt := now.Add(10 * time.Minute)
	if err := database.Exec(`
INSERT INTO strategy_signals (
    id, owner_user_id, strategy_instance_id, strategy_version_id, instrument_id,
    interval_code, candle_open_time, candle_close_time, target, mode, environment,
    status, expires_at, created_at
) VALUES (?, ?, ?, ?, ?, '1m', ?, ?, 0.5, 'manual', 'paper', 'active', ?, ?)
`, signalID, owner.ID, instanceID, versionID, instrumentID, now.Add(-2*time.Minute), now.Add(-time.Minute), expiresAt, now).Error; err != nil {
		t.Fatalf("create strategy signal: %v", err)
	}

	const dingTalkToken = "test-dingtalk-token"
	const dingTalkSecret = "test-dingtalk-secret"
	var dingTalkCalls atomic.Int32
	dingTalkServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := dingTalkCalls.Add(1)
		query := r.URL.Query()
		if query.Get("access_token") != dingTalkToken {
			t.Errorf("DingTalk access token = %q", query.Get("access_token"))
		}
		timestamp := query.Get("timestamp")
		mac := hmac.New(sha256.New, []byte(dingTalkSecret))
		_, _ = mac.Write([]byte(timestamp + "\n" + dingTalkSecret))
		expectedSign := base64.StdEncoding.EncodeToString(mac.Sum(nil))
		if timestamp == "" || !hmac.Equal([]byte(query.Get("sign")), []byte(expectedSign)) {
			t.Errorf("DingTalk signature is invalid")
		}
		w.Header().Set("Content-Type", "application/json")
		if call == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"errcode":130101,"errmsg":"rate limited"}`))
			return
		}
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer dingTalkServer.Close()
	cipher, err := security.NewSecretCipher("m2-signal-notification-test-key")
	if err != nil {
		t.Fatalf("create signal notification cipher: %v", err)
	}
	ownerID := owner.ID
	dingTalkChannel := db.SystemNotifyChannel{
		ChannelType: "dingtalk_webhook", OwnerID: &ownerID, DisplayName: "M2 retry",
		IsEnabled: true, SettingsJSON: dumpJSON(M{"webhookBaseUrl": dingTalkServer.URL}),
		EncryptedSecretsJSON: cipher.Encrypt(dumpJSON(M{"accessToken": dingTalkToken, "secret": dingTalkSecret})),
		CreatedAt:            time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := database.Create(&dingTalkChannel).Error; err != nil {
		t.Fatalf("create signal DingTalk channel: %v", err)
	}

	app := &App{
		DB: database, database: database, Hub: NewHub(),
		Cipher:       cipher,
		reauthTokens: map[string]reauthTokenRecord{}, revokedAccessTokens: map[string]time.Time{},
	}
	principal := &Principal{User: &owner, AccessTokenID: "m2-signal-session"}
	decisionKey := strings.Repeat("a", idempotencyKeyMinLength)
	if _, err := app.DecideStrategySignal(context.Background(), principal, signalID, "approved", decisionKey, ""); !errors.Is(err, ErrStrategySignalReauthentication) {
		t.Fatalf("approval without reauthentication returned %v", err)
	}
	reauthToken := app.issueReauthToken(principal, time.Now())
	approved, err := app.DecideStrategySignal(context.Background(), principal, signalID, "approved", decisionKey, reauthToken)
	if err != nil {
		t.Fatalf("approve strategy signal: %v", err)
	}
	if approved.Status != "approved" || approved.DecidedAt == nil {
		t.Fatalf("approved signal = %#v", approved)
	}
	replayed, err := app.DecideStrategySignal(context.Background(), principal, signalID, "approved", decisionKey, "")
	if err != nil || replayed.Status != "approved" {
		t.Fatalf("replay approved signal = %#v, err = %v", replayed, err)
	}
	var intentCount int64
	if err := database.Model(&db.TradingIntent{}).Where("strategy_signal_id = ?", signalID).Count(&intentCount).Error; err != nil {
		t.Fatalf("count approved signal intents: %v", err)
	}
	if intentCount != 1 {
		t.Fatalf("approved signal intent count = %d, want 1", intentCount)
	}
	var approvedRow db.StrategySignal
	if err := database.Where("id = ?", signalID).Take(&approvedRow).Error; err != nil || approvedRow.DecisionIdempotencyRecordID == nil {
		t.Fatalf("load approved signal decision record: row=%#v err=%v", approvedRow, err)
	}
	if err := database.Model(&db.IdempotencyRecord{}).
		Where("id = ?", *approvedRow.DecisionIdempotencyRecordID).
		Update("expires_at", now.Add(-time.Hour)).Error; err != nil {
		t.Fatalf("expire signal decision idempotency record: %v", err)
	}
	replayed, err = app.DecideStrategySignal(context.Background(), principal, signalID, "approved", decisionKey, "")
	if err != nil || replayed.Status != "approved" {
		t.Fatalf("replay approved signal after idempotency expiry = %#v, err = %v", replayed, err)
	}
	if _, err := app.DecideStrategySignal(context.Background(), principal, signalID, "approved", "approve-signal-0002", ""); !errors.Is(err, ErrStrategySignalConflict) {
		t.Fatalf("duplicate approval returned %v", err)
	}

	const rejectedSignalID = "019d6000-0000-7000-8000-000000000015"
	if err := database.Exec(`
INSERT INTO strategy_signals (
    id, owner_user_id, strategy_instance_id, strategy_version_id, instrument_id,
    interval_code, candle_open_time, candle_close_time, target, mode, environment,
    status, expires_at, created_at
) VALUES (?, ?, ?, ?, ?, '1m', ?, ?, -0.25, 'manual', 'paper', 'active', ?, ?)
`, rejectedSignalID, owner.ID, instanceID, versionID, instrumentID, now, now.Add(time.Minute), expiresAt.Add(time.Minute), now).Error; err != nil {
		t.Fatalf("create rejectable strategy signal: %v", err)
	}
	rejected, err := app.DecideStrategySignal(
		context.Background(), principal, rejectedSignalID, "rejected", "reject-signal-0001", "",
	)
	if err != nil || rejected.Status != "rejected" || rejected.DecidedAt == nil {
		t.Fatalf("reject strategy signal = %#v, err = %v", rejected, err)
	}

	const expiredSignalID = "019d6000-0000-7000-8000-000000000016"
	if err := database.Exec(`
INSERT INTO strategy_signals (
    id, owner_user_id, strategy_instance_id, strategy_version_id, instrument_id,
    interval_code, candle_open_time, candle_close_time, target, mode, environment,
    status, expires_at, created_at
) VALUES (?, ?, ?, ?, ?, '1m', ?, ?, 0.1, 'manual', 'paper', 'active', ?, ?)
`, expiredSignalID, owner.ID, instanceID, versionID, instrumentID,
		now.Add(-4*time.Minute), now.Add(-3*time.Minute), now.Add(-2*time.Minute), now).Error; err != nil {
		t.Fatalf("create expired strategy signal: %v", err)
	}
	if _, err := app.DecideStrategySignal(
		context.Background(), principal, expiredSignalID, "rejected", "reject-expired-0001", "",
	); !errors.Is(err, ErrStrategySignalConflict) {
		t.Fatalf("expired signal decision returned %v", err)
	}
	var expiredRow db.StrategySignal
	if err := database.Where("id = ?", expiredSignalID).Take(&expiredRow).Error; err != nil || expiredRow.Status != "expired" {
		t.Fatalf("expired signal state = %#v, err = %v", expiredRow, err)
	}
	other := db.SystemUser{Username: "m2-other-owner", IsActive: true}
	if err := database.Create(&other).Error; err != nil {
		t.Fatalf("create other signal owner: %v", err)
	}
	otherPrincipal := &Principal{User: &other, AccessTokenID: "m2-other-session"}
	if _, err := app.DecideStrategySignal(context.Background(), otherPrincipal, signalID, "rejected", "reject-signal-0001", ""); !errors.Is(err, ErrStrategySignalMissing) {
		t.Fatalf("cross-owner decision returned %v", err)
	}

	outboxID, err := app.publishDomainEvent(
		"strategy.signal.created", "strategy_signal", signalID,
		M{"signalId": signalID}, M{}, nil, nil,
	)
	if err != nil {
		t.Fatalf("publish strategy signal event: %v", err)
	}
	event := &domainEvent{
		OutboxID: outboxID, EventType: "strategy.signal.created",
		AggregateType: "strategy_signal", AggregateID: signalID,
	}
	if err := app.handleEventTriggeredEntries(context.Background(), event); !errors.Is(err, errCriticalNotificationDelivery) {
		t.Fatalf("first rate-limited strategy signal delivery = %v", err)
	}
	if err := app.handleEventTriggeredEntries(context.Background(), event); err != nil {
		t.Fatalf("redeliver strategy signal notification: %v", err)
	}
	if err := app.handleEventTriggeredEntries(context.Background(), event); err != nil {
		t.Fatalf("replay completed strategy signal notification: %v", err)
	}
	var deliveries []db.SystemNotifyDelivery
	if err := database.Where("strategy_signal_id = ?", signalID).Find(&deliveries).Error; err != nil {
		t.Fatalf("list strategy signal notifications: %v", err)
	}
	if len(deliveries) != 2 {
		t.Fatalf("strategy signal notifications = %#v", deliveries)
	}
	for i := range deliveries {
		if deliveries[i].Status != "success" || deliveries[i].Title != "策略信号已批准" {
			t.Fatalf("strategy signal notification = %#v", deliveries[i])
		}
		if got := app.serializeDelivery(&deliveries[i])["strategySignalId"]; got != signalID {
			t.Fatalf("serialized strategy signal id = %#v", got)
		}
		if strings.Contains(deliveries[i].ProviderResponseText, dingTalkToken) ||
			strings.Contains(deliveries[i].ProviderResponseText, dingTalkSecret) {
			t.Fatal("strategy signal delivery stored a notification secret")
		}
	}
	if dingTalkCalls.Load() != 2 {
		t.Fatalf("DingTalk retry calls = %d, want 2", dingTalkCalls.Load())
	}
}
