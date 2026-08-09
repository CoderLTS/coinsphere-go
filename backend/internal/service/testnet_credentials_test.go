package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

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
