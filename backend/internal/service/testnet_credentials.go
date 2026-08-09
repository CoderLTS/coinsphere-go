package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"coinsphere/backend/internal/db"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrTradingCredentialInvalid = errors.New("invalid testnet trading credentials")

func (a *App) SaveTradingCredentials(
	ctx context.Context,
	principal *Principal,
	rawID string,
	payload TradingCredentialPayload,
	idempotencyKey, reauthToken string,
) (TradingCredentialView, error) {
	if principal == nil || principal.User == nil {
		return TradingCredentialView{}, invalidTrading("owner is required")
	}
	accountID, err := requiredTradingUUID(rawID, "accountId")
	if err != nil {
		return TradingCredentialView{}, err
	}
	apiKey, err := normalizeCredentialValue(payload.APIKey, "apiKey")
	if err != nil {
		return TradingCredentialView{}, err
	}
	apiSecret, err := normalizeCredentialValue(payload.APISecret, "apiSecret")
	if err != nil {
		return TradingCredentialView{}, err
	}
	if !payload.WithdrawalDisabled || !payload.IPWhitelistConfigured {
		return TradingCredentialView{}, fmt.Errorf("%w: withdrawals must be disabled and an IP whitelist must be configured", ErrTradingCredentialInvalid)
	}
	if a.Cipher == nil {
		return TradingCredentialView{}, errors.New("credential encryption is unavailable")
	}
	requestHash, err := credentialRequestHash(apiKey, apiSecret, payload)
	if err != nil {
		return TradingCredentialView{}, err
	}

	var row db.TradingAccountCredential
	err = a.dbWithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var account db.TradingAccount
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND owner_user_id = ?", accountID, principal.User.ID).Take(&account).Error; err != nil {
			return tradingAccountLookupError(err)
		}
		if account.Environment != "testnet" {
			return invalidTrading("credentials can only be configured for testnet accounts")
		}
		_, reused, err := a.reserveIdempotencyRecord(
			tx, principal.User.ID, "trading-credentials:save:"+accountID.String(), idempotencyKey, requestHash,
		)
		if err != nil {
			return err
		}
		if reused {
			if err := tx.Where("account_id = ?", accountID).Take(&row).Error; err != nil {
				return err
			}
			return nil
		}
		if !a.ConsumeReauthToken(reauthToken, principal) {
			return ErrTradingReauthentication
		}
		keyCiphertext := a.Cipher.Encrypt(apiKey)
		secretCiphertext := a.Cipher.Encrypt(apiSecret)
		if keyCiphertext == "" || secretCiphertext == "" {
			return errors.New("credential encryption failed")
		}
		now := time.Now().UTC()
		if err := tx.Where("account_id = ?", accountID).Take(&row).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			id, err := uuid.NewV7()
			if err != nil {
				return err
			}
			row = db.TradingAccountCredential{
				ID: id, AccountID: accountID, OwnerUserID: principal.User.ID,
				APIKeyCiphertext: keyCiphertext, APISecretCiphertext: secretCiphertext,
				WithdrawalDisabled: true, IPWhitelistConfigured: true,
				Status: "configured", VerificationStatus: "unverified",
				CreatedAt: now, UpdatedAt: now,
			}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else {
			if err := tx.Model(&row).Updates(map[string]any{
				"api_key_ciphertext": keyCiphertext, "api_secret_ciphertext": secretCiphertext,
				"withdrawal_disabled": true, "ip_whitelist_configured": true,
				"status": "configured", "verification_status": "unverified",
				"verification_error_code": "", "last_verified_at": nil, "updated_at": now,
			}).Error; err != nil {
				return err
			}
			if err := tx.Where("account_id = ?", accountID).Take(&row).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&account).Updates(map[string]any{
			"status": "paused", "pause_reason": "testnet_credentials_changed",
			"automation_enabled": false, "updated_at": now,
		}).Error; err != nil {
			return err
		}
		return disableAutoInstances(tx, &accountID, now)
	})
	if err != nil {
		return TradingCredentialView{}, err
	}
	return serializeTradingCredential(row), nil
}

func (a *App) RevokeTradingCredentials(
	ctx context.Context,
	principal *Principal,
	rawID, idempotencyKey, reauthToken string,
) (TradingCredentialView, error) {
	if principal == nil || principal.User == nil {
		return TradingCredentialView{}, invalidTrading("owner is required")
	}
	accountID, err := requiredTradingUUID(rawID, "accountId")
	if err != nil {
		return TradingCredentialView{}, err
	}
	requestHash, err := canonicalRequestHash(M{"revoked": true})
	if err != nil {
		return TradingCredentialView{}, err
	}
	var row db.TradingAccountCredential
	err = a.dbWithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var account db.TradingAccount
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND owner_user_id = ?", accountID, principal.User.ID).Take(&account).Error; err != nil {
			return tradingAccountLookupError(err)
		}
		if account.Environment != "testnet" {
			return invalidTrading("credentials can only be revoked for testnet accounts")
		}
		_, reused, err := a.reserveIdempotencyRecord(
			tx, principal.User.ID, "trading-credentials:revoke:"+accountID.String(), idempotencyKey, requestHash,
		)
		if err != nil {
			return err
		}
		if reused {
			return tx.Where("account_id = ?", accountID).Take(&row).Error
		}
		if !a.ConsumeReauthToken(reauthToken, principal) {
			return ErrTradingReauthentication
		}
		if err := tx.Where("account_id = ?", accountID).Take(&row).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			id, createErr := uuid.NewV7()
			if createErr != nil {
				return createErr
			}
			now := time.Now().UTC()
			row = db.TradingAccountCredential{
				ID: id, AccountID: accountID, OwnerUserID: principal.User.ID,
				Status: "revoked", VerificationStatus: "unverified", CreatedAt: now, UpdatedAt: now,
			}
			if createErr := tx.Create(&row).Error; createErr != nil {
				return createErr
			}
		} else if err != nil {
			return err
		} else {
			now := time.Now().UTC()
			if err := tx.Model(&row).Updates(map[string]any{
				"api_key_ciphertext": "", "api_secret_ciphertext": "",
				"withdrawal_disabled": false, "ip_whitelist_configured": false,
				"status": "revoked", "verification_status": "unverified",
				"verification_error_code": "", "last_verified_at": nil, "updated_at": now,
			}).Error; err != nil {
				return err
			}
			if err := tx.Where("account_id = ?", accountID).Take(&row).Error; err != nil {
				return err
			}
		}
		now := time.Now().UTC()
		if err := tx.Model(&account).Updates(map[string]any{
			"status": "paused", "pause_reason": "testnet_credentials_revoked",
			"automation_enabled": false, "updated_at": now,
		}).Error; err != nil {
			return err
		}
		return disableAutoInstances(tx, &accountID, now)
	})
	if err != nil {
		return TradingCredentialView{}, err
	}
	return serializeTradingCredential(row), nil
}

func testnetCredentialsConfigured(database *gorm.DB, accountID uuid.UUID) (bool, error) {
	var count int64
	if err := database.Model(&db.TradingAccountCredential{}).Where(
		"account_id = ? AND status = 'configured' AND api_key_ciphertext <> '' AND api_secret_ciphertext <> '' AND withdrawal_disabled AND ip_whitelist_configured",
		accountID,
	).Count(&count).Error; err != nil {
		return false, err
	}
	return count == 1, nil
}

func testnetCredentialsVerified(database *gorm.DB, accountID uuid.UUID) (bool, error) {
	var count int64
	if err := database.Model(&db.TradingAccountCredential{}).Where(
		"account_id = ? AND status = 'configured' AND verification_status = 'verified' AND last_verified_at IS NOT NULL "+
			"AND api_key_ciphertext <> '' AND api_secret_ciphertext <> '' AND withdrawal_disabled AND ip_whitelist_configured",
		accountID,
	).Count(&count).Error; err != nil {
		return false, err
	}
	return count == 1, nil
}

func testnetCredentialReadinessError(database *gorm.DB, accountID uuid.UUID) error {
	configured, err := testnetCredentialsConfigured(database, accountID)
	if err != nil {
		return err
	}
	if !configured {
		return ErrTradingCredentialsMissing
	}
	return ErrTradingCredentialsUnverified
}

func loadTradingCredential(database *gorm.DB, accountID uuid.UUID) (db.TradingAccountCredential, error) {
	var row db.TradingAccountCredential
	err := database.Where("account_id = ?", accountID).Take(&row).Error
	return row, err
}

func serializeTradingCredential(row db.TradingAccountCredential) TradingCredentialView {
	view := TradingCredentialView{
		AccountID: row.AccountID.String(), Configured: row.Status == "configured" && row.APIKeyCiphertext != "" && row.APISecretCiphertext != "",
		Status: row.Status, VerificationStatus: row.VerificationStatus,
		VerificationError: row.VerificationErrorCode, UpdatedAt: formatUTC(row.UpdatedAt),
	}
	if row.LastVerifiedAt != nil {
		value := formatUTC(*row.LastVerifiedAt)
		view.LastVerifiedAt = &value
	}
	return view
}

func normalizeCredentialValue(value, field string) (string, error) {
	if value != strings.TrimSpace(value) || value == "" || len(value) > 512 {
		return "", fmt.Errorf("%w: %s must be a non-empty value without surrounding whitespace", ErrTradingCredentialInvalid, field)
	}
	for _, runeValue := range value {
		if runeValue < 0x21 || runeValue > 0x7e {
			return "", fmt.Errorf("%w: %s must contain visible ASCII characters only", ErrTradingCredentialInvalid, field)
		}
	}
	return value, nil
}

func credentialRequestHash(apiKey, apiSecret string, payload TradingCredentialPayload) (string, error) {
	keyDigest := sha256.Sum256([]byte(apiKey))
	secretDigest := sha256.Sum256([]byte(apiSecret))
	return canonicalRequestHash(M{
		"apiKeySha256": hex.EncodeToString(keyDigest[:]), "apiSecretSha256": hex.EncodeToString(secretDigest[:]),
		"withdrawalDisabled": payload.WithdrawalDisabled, "ipWhitelistConfigured": payload.IPWhitelistConfigured,
	})
}

func (a *App) decryptTradingCredential(row db.TradingAccountCredential) (string, string, error) {
	if a == nil || a.Cipher == nil || row.Status != "configured" {
		return "", "", ErrTradingCredentialsMissing
	}
	apiKey, err := a.Cipher.Decrypt(row.APIKeyCiphertext)
	if err != nil {
		return "", "", err
	}
	apiSecret, err := a.Cipher.Decrypt(row.APISecretCiphertext)
	if err != nil {
		return "", "", err
	}
	if apiKey == "" || apiSecret == "" {
		return "", "", ErrTradingCredentialsMissing
	}
	return apiKey, apiSecret, nil
}
