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
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const defaultTestnetReconciliationPollInterval = 30 * time.Second

// TestnetAccountReconciler bootstraps a read-only Testnet projection before manual account release.
type TestnetAccountReconciler struct {
	database     *gorm.DB
	cipher       *security.SecretCipher
	client       *exchangebinance.PrivateClient
	pollInterval time.Duration
}

func NewTestnetAccountReconciler(
	database *gorm.DB,
	cipher *security.SecretCipher,
	client *exchangebinance.PrivateClient,
	pollInterval time.Duration,
) (*TestnetAccountReconciler, error) {
	if database == nil {
		return nil, errors.New("testnet account reconciler database is required")
	}
	if cipher == nil {
		return nil, errors.New("testnet account reconciler cipher is required")
	}
	if client == nil {
		return nil, errors.New("testnet account reconciler client is required")
	}
	if pollInterval <= 0 {
		pollInterval = defaultTestnetReconciliationPollInterval
	}
	return &TestnetAccountReconciler{database: database, cipher: cipher, client: client, pollInterval: pollInterval}, nil
}

func (reconciler *TestnetAccountReconciler) Run(ctx context.Context) error {
	slog.InfoContext(ctx, "testnet account reconciler started")
	defer slog.Info("testnet account reconciler stopped")
	for {
		processed, retryAfter, err := reconciler.ProcessNext(ctx)
		if err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "testnet account reconciliation failed", "error_category", "reconciliation")
		}
		if ctx.Err() != nil {
			return nil
		}
		if processed && err == nil && retryAfter == 0 {
			continue
		}
		delay := maxDuration(reconciler.pollInterval, retryAfter)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func (reconciler *TestnetAccountReconciler) ProcessNext(ctx context.Context) (bool, time.Duration, error) {
	var credential db.TradingAccountCredential
	err := reconciler.database.WithContext(ctx).Model(&db.TradingAccountCredential{}).
		Select("trading_account_credentials.*").
		Joins("JOIN trading_accounts ON trading_accounts.id = trading_account_credentials.account_id").
		Joins("LEFT JOIN testnet_reconciliations ON testnet_reconciliations.account_id = trading_account_credentials.account_id").
		Where("trading_accounts.environment = 'testnet' AND trading_account_credentials.status = 'configured' AND trading_account_credentials.verification_status = 'verified'").
		Where("testnet_reconciliations.account_id IS NULL OR testnet_reconciliations.credential_updated_at <> trading_account_credentials.updated_at OR testnet_reconciliations.status <> 'matched'").
		Order("testnet_reconciliations.updated_at NULLS FIRST, trading_account_credentials.updated_at, trading_account_credentials.id").
		Take(&credential).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	var account db.TradingAccount
	if err := reconciler.database.WithContext(ctx).
		Where("id = ? AND owner_user_id = ? AND environment = 'testnet'", credential.AccountID, credential.OwnerUserID).
		Take(&account).Error; err != nil {
		return true, 0, err
	}
	account, current, err := reconciler.pausePendingAccount(ctx, credential, account)
	if err != nil {
		return true, 0, err
	}
	if !current {
		return true, 0, nil
	}

	apiKey, apiSecret, err := decryptTestnetCredential(reconciler.cipher, credential)
	var snapshot exchangebinance.AccountSnapshot
	if err == nil {
		snapshot, err = reconciler.client.SnapshotAccount(ctx, marketdata.MarketType(account.Market), apiKey, apiSecret)
	}
	if ctx.Err() != nil {
		return true, 0, ctx.Err()
	}
	if err != nil {
		errorCode, retryAfter, invalidCredential := testnetReconciliationFailure(err)
		if invalidCredential {
			return true, 0, reconciler.invalidateCredential(ctx, credential, account, errorCode)
		}
		if persistErr := reconciler.persistUnknown(ctx, credential, account, errorCode); persistErr != nil {
			return true, retryAfter, persistErr
		}
		return true, maxDuration(reconciler.pollInterval, retryAfter), nil
	}

	status, errorCode, err := reconciler.classifySnapshot(ctx, account, snapshot)
	if err != nil {
		return true, 0, err
	}
	persisted, err := reconciler.persistSnapshot(ctx, credential, account, snapshot, status, errorCode)
	if err != nil {
		return true, 0, err
	}
	if !persisted {
		return true, 0, nil
	}
	if status == "matched" {
		return true, 0, nil
	}
	return true, reconciler.pollInterval, nil
}

func (reconciler *TestnetAccountReconciler) classifySnapshot(
	ctx context.Context,
	account db.TradingAccount,
	snapshot exchangebinance.AccountSnapshot,
) (string, string, error) {
	var instruments []db.MarketInstrument
	if err := reconciler.database.WithContext(ctx).Model(&db.MarketInstrument{}).
		Joins("JOIN trading_account_instruments ON trading_account_instruments.instrument_id = market_instruments.id").
		Where("trading_account_instruments.account_id = ?", account.ID).
		Find(&instruments).Error; err != nil {
		return "", "", err
	}
	quoteAssets := make(map[string]struct{}, len(instruments))
	knownSymbols := make(map[string]struct{}, len(instruments))
	for _, instrument := range instruments {
		quoteAssets[instrument.QuoteAsset] = struct{}{}
		knownSymbols[instrument.NativeSymbol] = struct{}{}
	}
	if !snapshot.CanTrade {
		return "mismatch", "trading_disabled", nil
	}
	if len(snapshot.OpenOrders) > 0 {
		return "mismatch", "open_orders_present", nil
	}
	for _, position := range snapshot.Positions {
		if position.PositionSide != "both" {
			return "mismatch", "hedge_mode_enabled", nil
		}
		if _, ok := knownSymbols[position.Symbol]; !ok {
			return "mismatch", "unknown_instrument", nil
		}
		if !position.Quantity.IsZero() || !position.UnrealizedPnL.IsZero() {
			return "mismatch", "positions_present", nil
		}
	}
	for _, balance := range snapshot.Balances {
		if _, ok := quoteAssets[balance.Asset]; !ok && (!balance.Total.IsZero() || !balance.Available.IsZero()) {
			if account.Market == string(marketdata.MarketTypeSpot) {
				return "mismatch", "spot_inventory_present", nil
			}
			return "mismatch", "unsupported_collateral", nil
		}
	}
	return "matched", "", nil
}

func (reconciler *TestnetAccountReconciler) pausePendingAccount(
	ctx context.Context,
	credential db.TradingAccountCredential,
	account db.TradingAccount,
) (db.TradingAccount, bool, error) {
	current := true
	err := reconciler.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		lockedAccount, err := lockCurrentTestnetState(tx, credential, nil)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				current = false
				return nil
			}
			return err
		}
		account = lockedAccount
		if err := pauseTestnetAccount(tx, account.ID, "testnet_reconciliation_required", time.Now().UTC()); err != nil {
			return err
		}
		return tx.Where("id = ?", account.ID).Take(&account).Error
	})
	return account, current, err
}

func (reconciler *TestnetAccountReconciler) persistSnapshot(
	ctx context.Context,
	credential db.TradingAccountCredential,
	account db.TradingAccount,
	snapshot exchangebinance.AccountSnapshot,
	status, errorCode string,
) (bool, error) {
	persisted := true
	err := reconciler.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockCurrentTestnetState(tx, credential, &account.UpdatedAt); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				persisted = false
				return nil
			}
			return err
		}
		if err := clearTestnetReconciliation(tx, account.ID); err != nil {
			return err
		}
		now := time.Now().UTC()
		observedAt := snapshot.ObservedAt.UTC()
		reconciliation := db.TestnetReconciliation{
			AccountID: account.ID, CredentialUpdatedAt: credential.UpdatedAt,
			Status: status, ErrorCode: errorCode,
			BalanceCount: len(snapshot.Balances), PositionCount: len(snapshot.Positions), OpenOrderCount: len(snapshot.OpenOrders),
			LastAttemptedAt: now, LastObservedAt: &observedAt, UpdatedAt: now,
		}
		if err := tx.Create(&reconciliation).Error; err != nil {
			return err
		}
		balances := make([]db.TestnetBalance, 0, len(snapshot.Balances))
		for _, balance := range snapshot.Balances {
			balances = append(balances, db.TestnetBalance{
				AccountID: account.ID, CredentialUpdatedAt: credential.UpdatedAt, Asset: balance.Asset,
				TotalBalance: balance.Total, AvailableBalance: balance.Available, ObservedAt: observedAt,
			})
		}
		if len(balances) > 0 {
			if err := tx.Create(&balances).Error; err != nil {
				return err
			}
		}
		positions := make([]db.TestnetPosition, 0, len(snapshot.Positions))
		for _, position := range snapshot.Positions {
			positions = append(positions, db.TestnetPosition{
				AccountID: account.ID, CredentialUpdatedAt: credential.UpdatedAt,
				NativeSymbol: position.Symbol, PositionSide: position.PositionSide,
				Quantity: position.Quantity, EntryPrice: position.EntryPrice,
				UnrealizedPnL: position.UnrealizedPnL, ObservedAt: observedAt,
			})
		}
		if len(positions) > 0 {
			if err := tx.Create(&positions).Error; err != nil {
				return err
			}
		}
		orders := make([]db.TestnetOpenOrder, 0, len(snapshot.OpenOrders))
		for _, order := range snapshot.OpenOrders {
			orders = append(orders, db.TestnetOpenOrder{
				AccountID: account.ID, CredentialUpdatedAt: credential.UpdatedAt,
				NativeSymbol: order.Symbol, ExchangeOrderID: order.ExchangeOrderID, ClientOrderID: order.ClientOrderID,
				Side: order.Side, OrderType: order.OrderType, Status: order.Status,
				Price: order.Price, OriginalQuantity: order.OriginalQuantity,
				ExecutedQuantity: order.ExecutedQuantity, StopPrice: order.StopPrice, ObservedAt: observedAt,
			})
		}
		if len(orders) > 0 {
			if err := tx.Create(&orders).Error; err != nil {
				return err
			}
		}
		reason := "testnet_reconciled_manual_release_required"
		if status != "matched" {
			reason = "testnet_reconciliation_mismatch"
		}
		return pauseTestnetAccount(tx, account.ID, reason, now)
	})
	return persisted, err
}

func (reconciler *TestnetAccountReconciler) persistUnknown(
	ctx context.Context,
	credential db.TradingAccountCredential,
	account db.TradingAccount,
	errorCode string,
) error {
	return reconciler.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockCurrentTestnetState(tx, credential, nil); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		now := time.Now().UTC()
		var row db.TestnetReconciliation
		err := tx.Where("account_id = ? AND credential_updated_at = ?", account.ID, credential.UpdatedAt).Take(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			row = db.TestnetReconciliation{
				AccountID: account.ID, CredentialUpdatedAt: credential.UpdatedAt,
				Status: "unknown", ErrorCode: errorCode, LastAttemptedAt: now, UpdatedAt: now,
			}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if err := tx.Model(&row).Updates(map[string]any{
			"status": "unknown", "error_code": errorCode, "last_attempted_at": now, "updated_at": now,
		}).Error; err != nil {
			return err
		}
		return pauseTestnetAccount(tx, account.ID, "testnet_reconciliation_unknown", now)
	})
}

func (reconciler *TestnetAccountReconciler) invalidateCredential(
	ctx context.Context,
	credential db.TradingAccountCredential,
	account db.TradingAccount,
	errorCode string,
) error {
	return reconciler.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockCurrentTestnetState(tx, credential, nil); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if err := clearTestnetReconciliation(tx, account.ID); err != nil {
			return err
		}
		now := time.Now().UTC()
		if err := tx.Model(&db.TradingAccountCredential{}).Where("id = ?", credential.ID).Updates(map[string]any{
			"verification_status": "invalid", "verification_error_code": errorCode,
			"last_verified_at": nil, "updated_at": now,
		}).Error; err != nil {
			return err
		}
		return pauseTestnetAccount(tx, account.ID, "testnet_credentials_invalid", now)
	})
}

func lockCurrentTestnetState(
	tx *gorm.DB,
	credential db.TradingAccountCredential,
	expectedAccountUpdatedAt *time.Time,
) (db.TradingAccount, error) {
	var account db.TradingAccount
	accountQuery := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? AND owner_user_id = ? AND environment = 'testnet'", credential.AccountID, credential.OwnerUserID)
	if expectedAccountUpdatedAt != nil {
		accountQuery = accountQuery.Where("updated_at = ?", *expectedAccountUpdatedAt)
	}
	if err := accountQuery.Take(&account).Error; err != nil {
		return db.TradingAccount{}, err
	}
	var current db.TradingAccountCredential
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? AND account_id = ? AND status = 'configured' AND verification_status = 'verified' AND updated_at = ?",
			credential.ID, credential.AccountID, credential.UpdatedAt).
		Take(&current).Error; err != nil {
		return db.TradingAccount{}, err
	}
	return account, nil
}

func clearTestnetReconciliation(tx *gorm.DB, accountID uuid.UUID) error {
	for _, model := range []any{&db.TestnetOpenOrder{}, &db.TestnetPosition{}, &db.TestnetBalance{}, &db.TestnetReconciliation{}} {
		if err := tx.Where("account_id = ?", accountID).Delete(model).Error; err != nil {
			return err
		}
	}
	return nil
}

func pauseTestnetAccount(tx *gorm.DB, accountID uuid.UUID, reason string, now time.Time) error {
	if err := tx.Model(&db.TradingAccount{}).Where("id = ? AND environment = 'testnet'", accountID).Updates(map[string]any{
		"status": "paused", "pause_reason": reason, "automation_enabled": false, "updated_at": now,
	}).Error; err != nil {
		return err
	}
	return disableAutoInstances(tx, &accountID, now)
}

func testnetReconciliationFailure(err error) (string, time.Duration, bool) {
	var privateErr *exchangebinance.PrivateError
	if !errors.As(err, &privateErr) {
		return "credential_decryption_failed", 0, false
	}
	switch privateErr.Kind {
	case exchangebinance.PrivateErrorAuthentication:
		return "authentication_failed", 0, true
	case exchangebinance.PrivateErrorPermission:
		return "permission_denied", 0, true
	case exchangebinance.PrivateErrorRateLimited:
		return "rate_limited", privateErr.RetryAfter, false
	case exchangebinance.PrivateErrorClockSkew:
		return "clock_skew", 0, false
	case exchangebinance.PrivateErrorRejected:
		return "exchange_rejected", 0, false
	case exchangebinance.PrivateErrorProtocol:
		return "protocol_error", 0, false
	default:
		return "exchange_unavailable", 0, false
	}
}
