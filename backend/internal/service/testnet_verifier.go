package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"coinsphere/backend/internal/db"
	exchangebinance "coinsphere/backend/internal/exchange/binance"
	"coinsphere/backend/internal/marketdata"
	"coinsphere/backend/internal/security"
	"gorm.io/gorm"
)

const defaultTestnetCredentialPollInterval = 30 * time.Second

// TestnetCredentialVerifier is the only runtime path that decrypts trading credentials.
type TestnetCredentialVerifier struct {
	database     *gorm.DB
	cipher       *security.SecretCipher
	client       *exchangebinance.PrivateClient
	environment  string
	market       string
	pollInterval time.Duration
}

func NewTestnetCredentialVerifier(
	database *gorm.DB,
	cipher *security.SecretCipher,
	client *exchangebinance.PrivateClient,
	pollInterval time.Duration,
) (*TestnetCredentialVerifier, error) {
	return NewPrivateCredentialVerifier(database, cipher, client, "testnet", "", pollInterval)
}

func NewPrivateCredentialVerifier(
	database *gorm.DB,
	cipher *security.SecretCipher,
	client *exchangebinance.PrivateClient,
	environment, market string,
	pollInterval time.Duration,
) (*TestnetCredentialVerifier, error) {
	if database == nil {
		return nil, errors.New("testnet credential verifier database is required")
	}
	if cipher == nil {
		return nil, errors.New("testnet credential verifier cipher is required")
	}
	if client == nil {
		return nil, errors.New("testnet credential verifier client is required")
	}
	if !validPrivateRuntimeScope(environment, market) {
		return nil, errors.New("private credential verifier scope is invalid")
	}
	if pollInterval <= 0 {
		pollInterval = defaultTestnetCredentialPollInterval
	}
	return &TestnetCredentialVerifier{
		database: database, cipher: cipher, client: client,
		environment: environment, market: market, pollInterval: pollInterval,
	}, nil
}

func (verifier *TestnetCredentialVerifier) Run(ctx context.Context) error {
	slog.InfoContext(ctx, "testnet credential verifier started")
	defer slog.Info("testnet credential verifier stopped")
	for {
		processed, retryAfter, err := verifier.ProcessNext(ctx)
		if err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "testnet credential verification failed", "error_category", "verification")
		}
		if ctx.Err() != nil {
			return nil
		}
		if processed && err == nil && retryAfter == 0 {
			continue
		}
		delay := verifier.pollInterval
		if retryAfter > delay {
			delay = retryAfter
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func (verifier *TestnetCredentialVerifier) ProcessNext(ctx context.Context) (bool, time.Duration, error) {
	var credential db.TradingAccountCredential
	query := verifier.database.WithContext(ctx).Model(&db.TradingAccountCredential{}).
		Select("trading_account_credentials.*").
		Joins("JOIN trading_accounts ON trading_accounts.id = trading_account_credentials.account_id").
		Where("trading_accounts.environment = ? AND trading_account_credentials.status = 'configured' AND trading_account_credentials.verification_status IN ('unverified', 'unknown')", verifier.scopeEnvironment())
	if verifier.market != "" {
		query = query.Where("trading_accounts.market_type = ?", verifier.market)
	}
	err := query.
		Order("trading_account_credentials.updated_at, trading_account_credentials.id").Take(&credential).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	var account db.TradingAccount
	if err := verifier.database.WithContext(ctx).
		Where("id = ? AND owner_user_id = ? AND environment = ?", credential.AccountID, credential.OwnerUserID, verifier.scopeEnvironment()).
		Take(&account).Error; err != nil {
		return true, 0, err
	}

	apiKey, apiSecret, err := decryptTestnetCredential(verifier.cipher, credential)
	if err == nil {
		err = verifier.client.VerifyAccount(ctx, marketdata.MarketType(account.Market), apiKey, apiSecret)
	}
	if ctx.Err() != nil {
		return true, 0, ctx.Err()
	}
	status, errorCode, retryAfter := credentialVerificationResult(err)
	if status == "unknown" {
		retryAfter = maxDuration(verifier.pollInterval, retryAfter)
	}
	now := time.Now().UTC()
	updates := map[string]any{
		"verification_status": status, "verification_error_code": errorCode,
		"last_verified_at": nil, "updated_at": now,
	}
	if status == "verified" {
		updates["last_verified_at"] = now
	}
	result := verifier.database.WithContext(ctx).Model(&db.TradingAccountCredential{}).
		Where("id = ? AND status = 'configured' AND updated_at = ?", credential.ID, credential.UpdatedAt).
		Updates(updates)
	if result.Error != nil {
		return true, retryAfter, result.Error
	}
	return true, retryAfter, nil
}

func (verifier *TestnetCredentialVerifier) scopeEnvironment() string {
	if verifier.environment == "" {
		return "testnet"
	}
	return verifier.environment
}

func decryptTestnetCredential(cipher *security.SecretCipher, credential db.TradingAccountCredential) (string, string, error) {
	if cipher == nil || credential.Status != "configured" {
		return "", "", ErrTradingCredentialsMissing
	}
	apiKey, err := cipher.Decrypt(credential.APIKeyCiphertext)
	if err != nil {
		return "", "", err
	}
	apiSecret, err := cipher.Decrypt(credential.APISecretCiphertext)
	if err != nil {
		return "", "", err
	}
	if apiKey == "" || apiSecret == "" {
		return "", "", ErrTradingCredentialsMissing
	}
	return apiKey, apiSecret, nil
}

func credentialVerificationResult(err error) (string, string, time.Duration) {
	if err == nil {
		return "verified", "", 0
	}
	var privateErr *exchangebinance.PrivateError
	if !errors.As(err, &privateErr) {
		return "unknown", "credential_decryption_failed", 0
	}
	switch privateErr.Kind {
	case exchangebinance.PrivateErrorAuthentication:
		return "invalid", "authentication_failed", 0
	case exchangebinance.PrivateErrorPermission:
		return "invalid", "permission_denied", 0
	case exchangebinance.PrivateErrorRateLimited:
		return "unknown", "rate_limited", privateErr.RetryAfter
	case exchangebinance.PrivateErrorClockSkew:
		return "unknown", "clock_skew", 0
	case exchangebinance.PrivateErrorRejected:
		return "unknown", "exchange_rejected", 0
	case exchangebinance.PrivateErrorProtocol:
		return "unknown", "protocol_error", 0
	default:
		return "unknown", "exchange_unavailable", 0
	}
}

func maxDuration(left, right time.Duration) time.Duration {
	if right > left {
		return right
	}
	return left
}
