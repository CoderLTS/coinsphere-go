package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"coinsphere/backend/internal/db"
	exchangebinance "coinsphere/backend/internal/exchange/binance"
	"coinsphere/backend/internal/security"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

func TestTestnetCredentialVerifierPersistsSafeStatesAndIgnoresStaleResults(t *testing.T) {
	database := openPostgresWorkflowContractDatabase(t).primary
	now := time.Now().UTC()
	owner := db.SystemUser{Username: "testnet-verifier-owner", IsActive: true, CreatedAt: now, UpdatedAt: now}
	if err := database.Create(&owner).Error; err != nil {
		t.Fatalf("create verifier owner: %v", err)
	}
	idempotency := db.IdempotencyRecord{
		UserID: owner.ID, Scope: "testnet-verifier-account", KeyHash: strings.Repeat("a", 64),
		RequestHash: strings.Repeat("b", 64), ExpiresAt: now.Add(time.Hour), CreatedAt: now,
	}
	if err := database.Create(&idempotency).Error; err != nil {
		t.Fatalf("create verifier idempotency record: %v", err)
	}
	accountID := mustVerifierUUIDv7(t)
	initialBalance := decimal.NewFromInt(10_000)
	feeRate := decimal.RequireFromString("0.001")
	account := db.TradingAccount{
		ID: accountID, OwnerUserID: owner.ID, Name: "Verifier Spot", Market: "spot", Environment: "testnet",
		Status: "paused", PauseReason: "credentials_required", InitialBalance: &initialBalance, PaperFeeRate: &feeRate,
		CreationIdempotencyRecordID: &idempotency.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.Create(&account).Error; err != nil {
		t.Fatalf("create verifier account: %v", err)
	}
	cipher, err := security.NewSecretCipher(strings.Repeat("c", 32))
	if err != nil {
		t.Fatalf("create verifier cipher: %v", err)
	}
	apiKey := strings.Repeat("k", 32)
	apiSecret := strings.Repeat("s", 32)
	credential := db.TradingAccountCredential{
		ID: mustVerifierUUIDv7(t), AccountID: accountID, OwnerUserID: owner.ID,
		APIKeyCiphertext: cipher.Encrypt(apiKey), APISecretCiphertext: cipher.Encrypt(apiSecret),
		WithdrawalDisabled: true, IPWhitelistConfigured: true, Status: "configured", VerificationStatus: "unverified",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.Create(&credential).Error; err != nil {
		t.Fatalf("create verifier credential: %v", err)
	}

	var responseMode atomic.Int32
	requestStarted := make(chan struct{}, 1)
	releaseRequest := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-MBX-APIKEY") != apiKey || request.URL.Path != "/api/v3/account" {
			t.Errorf("unexpected private account request")
		}
		switch responseMode.Load() {
		case 1:
			response.WriteHeader(http.StatusUnauthorized)
			_, _ = response.Write([]byte(`{"code":-1022,"msg":"sensitive-response-marker"}`))
		case 2:
			response.WriteHeader(http.StatusServiceUnavailable)
			_, _ = response.Write([]byte(`{"code":-1000,"msg":"sensitive-response-marker"}`))
		case 3:
			requestStarted <- struct{}{}
			<-releaseRequest
			_, _ = response.Write([]byte(`{}`))
		default:
			_, _ = response.Write([]byte(`{}`))
		}
	}))
	defer server.Close()
	client, err := exchangebinance.NewPrivateClient(exchangebinance.PrivateClientConfig{
		SpotBaseURL: server.URL, USDMBaseURL: server.URL,
	})
	if err != nil {
		t.Fatalf("create verifier private client: %v", err)
	}
	verifier, err := NewTestnetCredentialVerifier(database, cipher, client, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("create verifier: %v", err)
	}

	processed, retryAfter, err := verifier.ProcessNext(context.Background())
	if err != nil || !processed || retryAfter != 0 {
		t.Fatalf("verify configured credential: processed=%t retry=%v err=%v", processed, retryAfter, err)
	}
	assertVerifierCredentialState(t, database, credential.ID, "verified", "", true)
	var storedAccount db.TradingAccount
	if err := database.Where("id = ?", accountID).Take(&storedAccount).Error; err != nil {
		t.Fatalf("reload verifier account: %v", err)
	}
	if storedAccount.Status != "paused" || storedAccount.AutomationEnabled {
		t.Fatalf("credential verification unlocked account: %#v", storedAccount)
	}

	responseMode.Store(1)
	resetVerifierCredential(t, database, credential.ID, now.Add(time.Second))
	if processed, retryAfter, err = verifier.ProcessNext(context.Background()); err != nil || !processed || retryAfter != 0 {
		t.Fatalf("classify invalid credential: processed=%t retry=%v err=%v", processed, retryAfter, err)
	}
	assertVerifierCredentialState(t, database, credential.ID, "invalid", "authentication_failed", false)

	responseMode.Store(2)
	resetVerifierCredential(t, database, credential.ID, now.Add(2*time.Second))
	if processed, retryAfter, err = verifier.ProcessNext(context.Background()); err != nil || !processed || retryAfter != 10*time.Millisecond {
		t.Fatalf("classify unavailable credential: processed=%t retry=%v err=%v", processed, retryAfter, err)
	}
	assertVerifierCredentialState(t, database, credential.ID, "unknown", "exchange_unavailable", false)

	responseMode.Store(3)
	resetVerifierCredential(t, database, credential.ID, now.Add(3*time.Second))
	result := make(chan error, 1)
	go func() {
		_, _, processErr := verifier.ProcessNext(context.Background())
		result <- processErr
	}()
	select {
	case <-requestStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("private verification request did not start")
	}
	newKeyCiphertext := cipher.Encrypt(strings.Repeat("n", 32))
	newSecretCiphertext := cipher.Encrypt(strings.Repeat("m", 32))
	if err := database.Model(&db.TradingAccountCredential{}).Where("id = ?", credential.ID).Updates(map[string]any{
		"api_key_ciphertext": newKeyCiphertext, "api_secret_ciphertext": newSecretCiphertext,
		"verification_status": "unverified", "verification_error_code": "", "last_verified_at": nil,
		"updated_at": now.Add(4 * time.Second),
	}).Error; err != nil {
		t.Fatalf("replace credential during verification: %v", err)
	}
	close(releaseRequest)
	if err := <-result; err != nil {
		t.Fatalf("finish stale verification: %v", err)
	}
	var stored db.TradingAccountCredential
	if err := database.Where("id = ?", credential.ID).Take(&stored).Error; err != nil {
		t.Fatalf("reload replaced credential: %v", err)
	}
	if stored.VerificationStatus != "unverified" || stored.APIKeyCiphertext != newKeyCiphertext || stored.APISecretCiphertext != newSecretCiphertext {
		t.Fatalf("stale verification overwrote new credential: %#v", stored)
	}
}

func resetVerifierCredential(t *testing.T, database *gorm.DB, credentialID uuid.UUID, updatedAt time.Time) {
	t.Helper()
	if err := database.Model(&db.TradingAccountCredential{}).Where("id = ?", credentialID).Updates(map[string]any{
		"verification_status": "unverified", "verification_error_code": "", "last_verified_at": nil, "updated_at": updatedAt,
	}).Error; err != nil {
		t.Fatalf("reset verifier credential: %v", err)
	}
}

func assertVerifierCredentialState(t *testing.T, database *gorm.DB, credentialID uuid.UUID, status, errorCode string, verified bool) {
	t.Helper()
	var credential db.TradingAccountCredential
	if err := database.Where("id = ?", credentialID).Take(&credential).Error; err != nil {
		t.Fatalf("reload verifier credential: %v", err)
	}
	if credential.VerificationStatus != status || credential.VerificationErrorCode != errorCode || (credential.LastVerifiedAt != nil) != verified {
		t.Fatalf("credential verification state = %#v", credential)
	}
}

func mustVerifierUUIDv7(t *testing.T) uuid.UUID {
	t.Helper()
	value, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("create UUIDv7: %v", err)
	}
	return value
}
