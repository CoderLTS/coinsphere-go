package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"coinsphere/backend/internal/config"
	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/marketdata"
	"coinsphere/backend/internal/security"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestCredentialValueValidation(t *testing.T) {
	for _, value := range []string{"", " padded", "padded ", "internal space", "line\nbreak", "密钥", strings.Repeat("x", 513)} {
		if _, err := normalizeCredentialValue(value, "apiKey"); !errors.Is(err, ErrTradingCredentialInvalid) {
			t.Fatalf("credential value %q returned %v", value, err)
		}
	}
	if value, err := normalizeCredentialValue("test-key-value", "apiKey"); err != nil || value != "test-key-value" {
		t.Fatalf("valid credential value = %q, err=%v", value, err)
	}
}

func TestSpotLiveManualAccountRequiresFeatureAndRecordsRelease(t *testing.T) {
	database := openPostgresWorkflowContractDatabase(t).primary
	owner := db.SystemUser{Username: "spot-live-manual-owner", IsActive: true}
	if err := database.Create(&owner).Error; err != nil {
		t.Fatalf("create Live owner: %v", err)
	}
	admin := db.SystemUser{Username: "spot-live-auto-admin", IsActive: true}
	if err := database.Create(&admin).Error; err != nil {
		t.Fatalf("create Live automation administrator: %v", err)
	}
	instrumentID := uuid.MustParse("019de100-0000-7000-8000-000000000001")
	instrument := db.MarketInstrument{
		ID: instrumentID, Venue: string(marketdata.VenueBinance), Market: string(marketdata.MarketTypeSpot),
		NativeSymbol: "ETHUSDT", BaseAsset: "ETH", QuoteAsset: "USDT", Status: "trading",
		PriceTick: decimal.RequireFromString("0.01"), QuantityStep: decimal.RequireFromString("0.001"),
		MinQuantity: decimal.RequireFromString("0.001"), MinNotional: decimal.RequireFromString("5"),
		UpdatedAt: time.Now().UTC(),
	}
	if err := database.Create(&instrument).Error; err != nil {
		t.Fatalf("create Live instrument: %v", err)
	}
	cipher, err := security.NewSecretCipher(strings.Repeat("l", 32))
	if err != nil {
		t.Fatalf("create Live credential cipher: %v", err)
	}
	app := &App{
		DB: database, database: database, Cipher: cipher, Cfg: &config.AppConfig{},
		reauthTokens: map[string]reauthTokenRecord{}, revokedAccessTokens: map[string]time.Time{},
	}
	payload := TradingAccountCreatePayload{
		Name: "Spot Live manual", Market: "spot", Environment: "live",
		InitialBalance: "1000", PaperFeeRate: "0.001",
		Risk: TradingRiskPayload{
			InstrumentIDs: []string{instrumentID.String()}, MaxTotalNotional: "500",
			MaxSymbolNotional: "250", MaxOrderNotional: "100", MaxDailyLoss: "50",
			MaxDrawdown: "100", MaxQuoteAgeSeconds: 30,
		},
	}
	if _, err := app.CreateTradingAccount(context.Background(), owner.ID, payload, "live-account-disabled"); !errors.Is(err, ErrInvalidTradingRequest) {
		t.Fatalf("disabled Live account creation returned %v", err)
	}
	app.Cfg.Trading.SpotLiveManualEnabled = true
	account, err := app.CreateTradingAccount(context.Background(), owner.ID, payload, "live-account-enabled")
	if err != nil {
		t.Fatalf("create enabled Live account: %v", err)
	}
	if account.Environment != "live" || account.ManualAuthorized || account.Status != "paused" {
		t.Fatalf("new Live account = %#v", account)
	}
	principal := &Principal{User: &owner, AccessTokenID: "spot-live-session"}
	credentialPayload := TradingCredentialPayload{
		APIKey: strings.Repeat("k", 32), APISecret: strings.Repeat("s", 32),
		WithdrawalDisabled: true, IPWhitelistConfigured: true,
	}
	credentialToken := app.issueReauthToken(principal, time.Now())
	if _, err := app.SaveTradingCredentials(
		context.Background(), principal, account.ID, credentialPayload, "live-credential-save", credentialToken,
	); err != nil {
		t.Fatalf("save Live credential: %v", err)
	}

	verifiedAt := time.Now().UTC()
	if err := database.Model(&db.TradingAccountCredential{}).Where("account_id = ?", account.ID).Updates(map[string]any{
		"verification_status": "verified", "verification_error_code": "",
		"last_verified_at": verifiedAt, "updated_at": verifiedAt,
	}).Error; err != nil {
		t.Fatalf("mark Live credential verified: %v", err)
	}
	accountID := uuid.MustParse(account.ID)
	if err := database.Create(&db.TestnetReconciliation{
		AccountID: accountID, CredentialUpdatedAt: verifiedAt, Status: "matched",
		LastAttemptedAt: verifiedAt, LastObservedAt: &verifiedAt, UpdatedAt: verifiedAt,
	}).Error; err != nil {
		t.Fatalf("create Live reconciliation: %v", err)
	}
	if err := database.Create(&db.TestnetRiskState{
		AccountID: accountID, CredentialUpdatedAt: verifiedAt,
		BaselineEquity: decimal.NewFromInt(1000), Equity: decimal.NewFromInt(1000),
		PeakEquity: decimal.NewFromInt(1000), DayStartDate: utcDay(verifiedAt),
		DayStartEquity: decimal.NewFromInt(1000), UpdatedAt: verifiedAt,
	}).Error; err != nil {
		t.Fatalf("create Live risk state: %v", err)
	}
	if err := database.Model(&db.TradingControl{}).Where("id = 1").Updates(map[string]any{
		"emergency_stopped": false, "stop_reason": "", "released_at": verifiedAt,
		"released_by_user_id": owner.ID, "updated_at": verifiedAt,
	}).Error; err != nil {
		t.Fatalf("release trading emergency stop: %v", err)
	}
	resumeToken := app.issueReauthToken(principal, time.Now())
	resumed, err := app.ResumeTradingAccount(
		context.Background(), principal, account.ID, "live-account-resume", resumeToken,
	)
	if err != nil {
		t.Fatalf("resume Live account: %v", err)
	}
	if !resumed.ManualAuthorized || resumed.ManualAuthorizedAt == nil || resumed.AutomationEnabled {
		t.Fatalf("resumed Live account = %#v", resumed)
	}
	if _, err := app.SetTradingAutomation(
		context.Background(), principal, account.ID, true, "live-auto-enable", "",
	); !errors.Is(err, ErrTradingExecutionUnavailable) {
		t.Fatalf("Live automation enable returned %v", err)
	}
	app.Cfg.Trading.SpotLiveAutoEnabled = true
	adminPrincipal := &Principal{
		User: &admin, RoleCodes: []string{"R_SUPER"}, AccessTokenID: "spot-live-admin-session",
	}
	adminToken := app.issueReauthToken(adminPrincipal, time.Now())
	authorized, err := app.SetTradingAuthorization(
		context.Background(), adminPrincipal, account.ID, true, "live-auto-authorize", adminToken,
	)
	if err != nil || !authorized.AutomationAuthorized || authorized.AutoAuthorized {
		t.Fatalf("authorize Live automation = %#v, err=%v", authorized, err)
	}
	autoToken := app.issueReauthToken(principal, time.Now())
	automated, err := app.SetTradingAutomation(
		context.Background(), principal, account.ID, true, "live-auto-enable-ready", autoToken,
	)
	if err != nil || !automated.AutomationEnabled || !automated.AutoAuthorized ||
		automated.AutoAuthorizedAt == nil || !automated.AutomationAuthorized || !automated.ManualAuthorized {
		t.Fatalf("enable released Live automation = %#v, err=%v", automated, err)
	}
	app.Cfg.Trading.SpotLiveAutoEnabled = false
	disabled, err := app.SetTradingAutomation(
		context.Background(), principal, account.ID, false, "live-auto-disable", "",
	)
	if err != nil || disabled.AutomationEnabled || disabled.AutoAuthorized || !disabled.AutomationAuthorized {
		t.Fatalf("disable Live automation after feature shutdown = %#v, err=%v", disabled, err)
	}
	revokeToken := app.issueReauthToken(adminPrincipal, time.Now())
	revoked, err := app.SetTradingAuthorization(
		context.Background(), adminPrincipal, account.ID, false, "live-auto-revoke", revokeToken,
	)
	if err != nil || revoked.AutomationAuthorized || revoked.AutomationEnabled || revoked.AutoAuthorized {
		t.Fatalf("revoke Live automation after feature shutdown = %#v, err=%v", revoked, err)
	}
	app.Cfg.Trading.SpotLiveAutoEnabled = true
	adminToken = app.issueReauthToken(adminPrincipal, time.Now())
	if _, err := app.SetTradingAuthorization(
		context.Background(), adminPrincipal, account.ID, true, "live-auto-reauthorize", adminToken,
	); err != nil {
		t.Fatalf("reauthorize Live automation: %v", err)
	}
	autoToken = app.issueReauthToken(principal, time.Now())
	if _, err := app.SetTradingAutomation(
		context.Background(), principal, account.ID, true, "live-auto-reenable", autoToken,
	); err != nil {
		t.Fatalf("reenable Live automation: %v", err)
	}
	if err := pauseTestnetAccount(database, accountID, "offline_release_reset", time.Now().UTC()); err != nil {
		t.Fatalf("pause Live account: %v", err)
	}
	var paused db.TradingAccount
	if err := database.Where("id = ?", accountID).Take(&paused).Error; err != nil {
		t.Fatalf("load paused Live account: %v", err)
	}
	if paused.Status != "paused" || paused.AutomationEnabled || paused.ManualAuthorizedAt != nil ||
		paused.AutoAuthorizedAt != nil || paused.AutomationAuthorizedAt == nil {
		t.Fatalf("paused Live release state = %#v", paused)
	}

	usdmInstrumentID := uuid.MustParse("019de100-0000-7000-8000-000000000010")
	usdmInstrument := instrument
	usdmInstrument.ID, usdmInstrument.Market = usdmInstrumentID, string(marketdata.MarketTypeUSDM)
	if err := database.Create(&usdmInstrument).Error; err != nil {
		t.Fatalf("create USD-M Live instrument: %v", err)
	}
	usdmLeverage := 2
	usdmPayload := payload
	usdmPayload.Name, usdmPayload.Market = "USD-M Live manual", "usd_m"
	usdmPayload.Risk.InstrumentIDs = []string{usdmInstrumentID.String()}
	usdmPayload.Risk.Leverage = &usdmLeverage
	if _, err := app.CreateTradingAccount(
		context.Background(), owner.ID, usdmPayload, "usdm-live-disabled",
	); !errors.Is(err, ErrInvalidTradingRequest) {
		t.Fatalf("disabled USD-M Live account creation returned %v", err)
	}
	app.Cfg.Trading.USDMLiveManualEnabled = true
	usdmAccount, err := app.CreateTradingAccount(
		context.Background(), owner.ID, usdmPayload, "usdm-live-enabled",
	)
	if err != nil || usdmAccount.Market != "usd_m" || usdmAccount.Status != "paused" {
		t.Fatalf("create USD-M Live account = %#v, err=%v", usdmAccount, err)
	}
	credentialToken = app.issueReauthToken(principal, time.Now())
	if _, err := app.SaveTradingCredentials(
		context.Background(), principal, usdmAccount.ID, credentialPayload, "usdm-live-credential", credentialToken,
	); err != nil {
		t.Fatalf("save USD-M Live credential: %v", err)
	}
	if _, err := app.SetTradingAutomation(
		context.Background(), principal, usdmAccount.ID, true, "usdm-live-auto", "",
	); !errors.Is(err, ErrTradingExecutionUnavailable) {
		t.Fatalf("USD-M Live automation enable returned %v", err)
	}
	overview, err := app.GetTradingOverview(context.Background(), owner.ID)
	if err != nil || !overview.Capabilities.USDMLiveManualEnabled {
		t.Fatalf("USD-M Live capability = %#v, err=%v", overview.Capabilities, err)
	}
}

func TestTestnetCredentialEncryptionIdempotencyAndRevocation(t *testing.T) {
	database := openPostgresWorkflowContractDatabase(t).primary
	owner := db.SystemUser{Username: "testnet-credential-owner", IsActive: true}
	other := db.SystemUser{Username: "testnet-credential-other", IsActive: true}
	if err := database.Create(&owner).Error; err != nil {
		t.Fatalf("create credential owner: %v", err)
	}
	if err := database.Create(&other).Error; err != nil {
		t.Fatalf("create other credential owner: %v", err)
	}
	instrumentID := uuid.MustParse("019da000-0000-7000-8000-000000000001")
	instrument := db.MarketInstrument{
		ID: instrumentID, Venue: string(marketdata.VenueBinance), Market: string(marketdata.MarketTypeSpot),
		NativeSymbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT", Status: "trading",
		PriceTick: decimal.RequireFromString("0.1"), QuantityStep: decimal.RequireFromString("0.001"),
		MinQuantity: decimal.RequireFromString("0.001"), MinNotional: decimal.RequireFromString("5"),
		UpdatedAt: time.Now().UTC(),
	}
	if err := database.Create(&instrument).Error; err != nil {
		t.Fatalf("create credential instrument: %v", err)
	}
	cipher, err := security.NewSecretCipher(strings.Repeat("c", 32))
	if err != nil {
		t.Fatalf("create credential cipher: %v", err)
	}
	app := &App{
		DB: database, database: database, Cipher: cipher,
		reauthTokens: map[string]reauthTokenRecord{}, revokedAccessTokens: map[string]time.Time{},
	}
	principal := &Principal{User: &owner, AccessTokenID: "testnet-credential-session"}
	account, err := app.CreateTradingAccount(context.Background(), owner.ID, TradingAccountCreatePayload{
		Name: "Testnet Spot", Market: "spot", Environment: "testnet",
		InitialBalance: "10000", PaperFeeRate: "0.001",
		Risk: TradingRiskPayload{
			InstrumentIDs: []string{instrumentID.String()}, MaxTotalNotional: "5000",
			MaxSymbolNotional: "2500", MaxOrderNotional: "1000", MaxDailyLoss: "500",
			MaxDrawdown: "1000", MaxQuoteAgeSeconds: 30,
		},
	}, "testnet-account-create-0001")
	if err != nil {
		t.Fatalf("create testnet account: %v", err)
	}
	if account.Environment != "testnet" || account.CredentialsConfigured {
		t.Fatalf("new testnet account = %#v", account)
	}
	controlReleaseAt := time.Now().UTC()
	if err := database.Model(&db.TradingControl{}).Where("id = 1").Updates(map[string]any{
		"emergency_stopped": false, "stop_reason": "", "released_at": controlReleaseAt,
		"released_by_user_id": owner.ID, "updated_at": controlReleaseAt,
	}).Error; err != nil {
		t.Fatalf("release default trading emergency stop: %v", err)
	}

	payload := TradingCredentialPayload{
		APIKey: strings.Repeat("k", 32), APISecret: strings.Repeat("s", 32),
		WithdrawalDisabled: true, IPWhitelistConfigured: true,
	}
	if _, err := app.SaveTradingCredentials(
		context.Background(), principal, account.ID, payload, "testnet-credential-save-0001", "",
	); !errors.Is(err, ErrTradingReauthentication) {
		t.Fatalf("credential save without reauthentication returned %v", err)
	}
	reauthToken := app.issueReauthToken(principal, time.Now())
	view, err := app.SaveTradingCredentials(
		context.Background(), principal, account.ID, payload, "testnet-credential-save-0001", reauthToken,
	)
	if err != nil {
		t.Fatalf("save testnet credential: %v", err)
	}
	if !view.Configured || view.Status != "configured" || view.VerificationStatus != "unverified" {
		t.Fatalf("saved credential view = %#v", view)
	}
	serialized, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("serialize credential view: %v", err)
	}
	if strings.Contains(string(serialized), payload.APIKey) || strings.Contains(string(serialized), payload.APISecret) {
		t.Fatal("credential response exposed a secret")
	}
	resumeToken := app.issueReauthToken(principal, time.Now())
	if _, err := app.ResumeTradingAccount(
		context.Background(), principal, account.ID, "testnet-account-resume-0001", resumeToken,
	); !errors.Is(err, ErrTradingCredentialsUnverified) {
		t.Fatalf("resume with unverified credentials returned %v", err)
	}

	var stored db.TradingAccountCredential
	if err := database.Where("account_id = ?", account.ID).Take(&stored).Error; err != nil {
		t.Fatalf("load stored credential: %v", err)
	}
	if stored.APIKeyCiphertext == payload.APIKey || stored.APISecretCiphertext == payload.APISecret {
		t.Fatal("credential was stored as plaintext")
	}
	decryptedKey, decryptedSecret, err := decryptTestnetCredential(cipher, stored)
	if err != nil || decryptedKey != payload.APIKey || decryptedSecret != payload.APISecret {
		t.Fatalf("decrypt stored credential: keyMatch=%t secretMatch=%t err=%v", decryptedKey == payload.APIKey, decryptedSecret == payload.APISecret, err)
	}

	replayed, err := app.SaveTradingCredentials(
		context.Background(), principal, account.ID, payload, "testnet-credential-save-0001", "",
	)
	if err != nil || !replayed.Configured {
		t.Fatalf("replay credential save = %#v, err=%v", replayed, err)
	}
	changed := payload
	changed.APISecret = strings.Repeat("d", 32)
	if _, err := app.SaveTradingCredentials(
		context.Background(), principal, account.ID, changed, "testnet-credential-save-0001", "",
	); !IsIdempotencyConflict(err) {
		t.Fatalf("credential idempotency conflict returned %v", err)
	}

	otherPrincipal := &Principal{User: &other, AccessTokenID: "other-testnet-session"}
	if _, err := app.SaveTradingCredentials(
		context.Background(), otherPrincipal, account.ID, payload, "testnet-credential-save-0002", "",
	); !errors.Is(err, ErrTradingAccountMissing) {
		t.Fatalf("cross-owner credential save returned %v", err)
	}
	unsafePayload := payload
	unsafePayload.IPWhitelistConfigured = false
	if _, err := app.SaveTradingCredentials(
		context.Background(), principal, account.ID, unsafePayload, "testnet-credential-save-0003", "",
	); !errors.Is(err, ErrTradingCredentialInvalid) {
		t.Fatalf("unsafe credential confirmation returned %v", err)
	}

	revokeToken := app.issueReauthToken(principal, time.Now())
	revoked, err := app.RevokeTradingCredentials(
		context.Background(), principal, account.ID, "testnet-credential-revoke-0001", revokeToken,
	)
	if err != nil || revoked.Configured || revoked.Status != "revoked" {
		t.Fatalf("revoke credential = %#v, err=%v", revoked, err)
	}
	if err := database.Where("account_id = ?", account.ID).Take(&stored).Error; err != nil {
		t.Fatalf("reload revoked credential: %v", err)
	}
	if stored.APIKeyCiphertext != "" || stored.APISecretCiphertext != "" {
		t.Fatal("revoked credential retained encrypted secret material")
	}
	if _, err := app.RevokeTradingCredentials(
		context.Background(), principal, account.ID, "testnet-credential-revoke-0001", "",
	); err != nil {
		t.Fatalf("replay credential revocation: %v", err)
	}
}
