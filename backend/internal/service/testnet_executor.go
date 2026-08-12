package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"coinsphere/backend/internal/db"
	exchangebinance "coinsphere/backend/internal/exchange/binance"
	"coinsphere/backend/internal/marketdata"
	"coinsphere/backend/internal/security"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const defaultTestnetExecutorPollInterval = time.Second

type testnetOrderClient interface {
	QueryOrder(
		context.Context, marketdata.MarketType, string, string, string, string,
	) (exchangebinance.OrderResult, error)
	PlaceMarketOrder(
		context.Context, marketdata.MarketType, string, string, string, string, string, decimal.Decimal, bool,
	) (exchangebinance.OrderResult, error)
	PlaceProtectiveOrder(
		context.Context, marketdata.MarketType, string, string, string, string, string, decimal.Decimal, decimal.Decimal,
	) (exchangebinance.OrderResult, error)
	CancelOrder(
		context.Context, marketdata.MarketType, string, string, string, string,
	) (exchangebinance.OrderResult, error)
}

// TestnetExecutor serializes deterministic private orders per account and mode.
type TestnetExecutor struct {
	database     *gorm.DB
	cipher       *security.SecretCipher
	client       testnetOrderClient
	workerID     string
	environment  string
	market       string
	mode         string
	pollInterval time.Duration
}

func NewTestnetExecutor(
	database *gorm.DB,
	cipher *security.SecretCipher,
	client testnetOrderClient,
	workerID string,
	pollInterval time.Duration,
) (*TestnetExecutor, error) {
	return NewPrivateExecutor(database, cipher, client, workerID, "testnet", "", "", pollInterval)
}

func NewPrivateExecutor(
	database *gorm.DB,
	cipher *security.SecretCipher,
	client testnetOrderClient,
	workerID, environment, market, mode string,
	pollInterval time.Duration,
) (*TestnetExecutor, error) {
	if database == nil {
		return nil, errors.New("testnet executor database is required")
	}
	if cipher == nil {
		return nil, errors.New("testnet executor cipher is required")
	}
	if client == nil {
		return nil, errors.New("testnet executor client is required")
	}
	if !validPrivateRuntimeScope(environment, market) ||
		(environment == "live" && mode != "manual" && mode != "auto") ||
		(environment == "testnet" && mode != "") {
		return nil, errors.New("private executor scope is invalid")
	}
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return nil, errors.New("testnet executor worker ID is required")
	}
	if pollInterval <= 0 {
		pollInterval = defaultTestnetExecutorPollInterval
	}
	return &TestnetExecutor{
		database: database, cipher: cipher, client: client,
		workerID: workerID, environment: environment, market: market, mode: mode,
		pollInterval: pollInterval,
	}, nil
}

func (executor *TestnetExecutor) scopeEnvironment() string {
	if executor.environment == "" {
		return "testnet"
	}
	return executor.environment
}

func (executor *TestnetExecutor) Run(ctx context.Context) error {
	if err := executor.Recover(ctx); err != nil {
		return fmt.Errorf("recover testnet executor: %w", err)
	}
	slog.InfoContext(ctx, "testnet executor started")
	defer slog.Info("testnet executor stopped")
	for {
		_, err := executor.ProcessNext(ctx)
		if err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "testnet executor operation failed", "error_category", "execution")
		}
		if ctx.Err() != nil {
			return nil
		}
		timer := time.NewTimer(executor.pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

// Recover never assumes that an interrupted external submission did not happen.
func (executor *TestnetExecutor) Recover(ctx context.Context) error {
	return executor.database.WithContext(ctx).Exec(`
UPDATE trading_intents AS intent
SET status = CASE
        WHEN EXISTS (SELECT 1 FROM testnet_orders AS orders WHERE orders.intent_id = intent.id)
            THEN 'reconciling'
        ELSE 'pending'
    END,
    claimed_at = NULL,
    worker_id = NULL,
    updated_at = CURRENT_TIMESTAMP
WHERE intent.environment = ?
  AND (? = '' OR intent.market_type = ?)
  AND (? = '' OR intent.mode = ?)
  AND intent.status = 'processing'
`, executor.scopeEnvironment(), executor.market, executor.market, executor.mode, executor.mode).Error
}

func (executor *TestnetExecutor) ProcessNext(ctx context.Context) (bool, error) {
	intent, err := executor.claimNextIntent(ctx)
	if err != nil || intent == nil {
		return false, err
	}
	err = executor.processIntent(ctx, *intent)
	if err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = executor.releaseClaim(cleanupCtx, intent.ID)
	}
	return true, err
}

func (executor *TestnetExecutor) releaseClaim(ctx context.Context, intentID uuid.UUID) error {
	return executor.database.WithContext(ctx).Exec(`
UPDATE trading_intents AS intent
SET status = CASE
        WHEN EXISTS (SELECT 1 FROM testnet_orders AS orders WHERE orders.intent_id = intent.id)
            THEN 'reconciling'
        ELSE 'pending'
    END,
    claimed_at = NULL,
    worker_id = NULL,
    updated_at = CURRENT_TIMESTAMP
WHERE intent.id = ? AND intent.environment = ?
  AND intent.status = 'processing' AND intent.worker_id = ?
`, intentID, executor.scopeEnvironment(), executor.workerID).Error
}

func (executor *TestnetExecutor) claimNextIntent(ctx context.Context) (*db.TradingIntent, error) {
	var intent db.TradingIntent
	err := executor.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Select without a row lock first so every execution path acquires the account
		// advisory lock before locking an intent row.
		var candidate db.TradingIntent
		if err := tx.
			Where(`environment = ?
				AND (? = '' OR market_type = ?)
				AND (? = '' OR mode = ?)
				AND (
                status = 'reconciling'
                OR (
                    status = 'pending'
                    AND NOT EXISTS (
                        SELECT 1 FROM trading_intents AS active
                        WHERE active.account_id = trading_intents.account_id
                          AND active.environment = ?
                          AND active.status IN ('processing', 'reconciling')
                    )
                )
				)`, executor.scopeEnvironment(), executor.market, executor.market,
				executor.mode, executor.mode, executor.scopeEnvironment()).
			Order("CASE status WHEN 'reconciling' THEN 0 ELSE 1 END, created_at, id").
			Take(&candidate).Error; err != nil {
			return err
		}
		if err := lockTestnetAccountExecution(tx, candidate.AccountID); err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND environment = ?", candidate.ID, executor.scopeEnvironment()).
			Take(&intent).Error; err != nil {
			return err
		}
		if intent.Status != "pending" && intent.Status != "reconciling" {
			return gorm.ErrRecordNotFound
		}
		var activeCount int64
		if err := tx.Model(&db.TradingIntent{}).
			Where("account_id = ? AND environment = ? AND status IN ('processing', 'reconciling') AND id <> ?",
				intent.AccountID, executor.scopeEnvironment(), intent.ID).
			Count(&activeCount).Error; err != nil {
			return err
		}
		if activeCount != 0 {
			return gorm.ErrRecordNotFound
		}
		now := time.Now().UTC()
		result := tx.Model(&intent).
			Where("environment = ? AND status IN ('pending', 'reconciling')", executor.scopeEnvironment()).
			Updates(map[string]any{
				"status": "processing", "attempt_count": gorm.Expr("attempt_count + 1"),
				"claimed_at": now, "worker_id": executor.workerID, "updated_at": now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		intent.Status = "processing"
		intent.AttemptCount++
		intent.ClaimedAt = &now
		intent.WorkerID = &executor.workerID
		return nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &intent, nil
}

func lockTestnetAccountExecution(tx *gorm.DB, accountID uuid.UUID) error {
	return tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", accountID.String()).Error
}

type testnetExecutionAction struct {
	Intent              db.TradingIntent
	Order               db.TestnetOrder
	Account             db.TradingAccount
	Instrument          db.MarketInstrument
	Credential          db.TradingAccountCredential
	ExpectedAccountTime time.Time
	Query               bool
	Cancel              bool
	PreflightErrorCode  string
}

type testnetManagedPosition struct {
	InstrumentID            uuid.UUID
	OwnerStrategyInstanceID *uuid.UUID
	Quantity                decimal.Decimal
	AverageEntryPrice       decimal.Decimal
	LastPrice               decimal.Decimal
	RealizedPnL             decimal.Decimal
}

type testnetProjection struct {
	Positions map[uuid.UUID]*testnetManagedPosition
	Cash      decimal.Decimal
	Equity    decimal.Decimal
}

type testnetExecutionState struct {
	Intent       db.TradingIntent
	Account      db.TradingAccount
	Instance     db.StrategyInstance
	Signal       db.StrategySignal
	Instrument   db.MarketInstrument
	Credential   db.TradingAccountCredential
	Risk         db.TestnetRiskState
	Projection   testnetProjection
	Price        decimal.Decimal
	TargetQty    decimal.Decimal
	DeltaQty     decimal.Decimal
	ReduceOnly   bool
	CurrentOwner *uuid.UUID
}

func (executor *TestnetExecutor) processIntent(ctx context.Context, claimed db.TradingIntent) error {
	action, err := executor.prepareAction(ctx, claimed)
	if err != nil || action == nil {
		return err
	}
	return executor.executeAction(ctx, *action)
}

func (executor *TestnetExecutor) executeAction(ctx context.Context, action testnetExecutionAction) error {
	if action.PreflightErrorCode != "" {
		if action.Order.Purpose == "protection" {
			flatten, err := executor.prepareEmergencyFlatten(ctx, action, action.PreflightErrorCode)
			if err != nil || flatten == nil {
				return err
			}
			return executor.executeAction(ctx, *flatten)
		}
		return executor.persistUnknown(ctx, action, action.PreflightErrorCode, true)
	}
	apiKey, apiSecret, err := decryptTestnetCredential(executor.cipher, action.Credential)
	if err != nil {
		if action.Order.Purpose == "protection" {
			flatten, flattenErr := executor.prepareEmergencyFlatten(ctx, action, "credential_decryption_failed")
			if flattenErr != nil || flatten == nil {
				return flattenErr
			}
			return executor.executeAction(ctx, *flatten)
		}
		return executor.persistUnknown(ctx, action, "credential_decryption_failed", true)
	}
	if action.Query {
		result, queryErr := executor.client.QueryOrder(
			ctx, marketdata.MarketType(action.Account.Market), apiKey, apiSecret,
			action.Instrument.NativeSymbol, action.Order.ClientOrderID,
		)
		if queryErr == nil {
			return executor.persistResult(ctx, action, result)
		}
		if action.Order.Purpose == "protection" {
			code := testnetProtectionQueryFailure(queryErr)
			flatten, err := executor.prepareEmergencyFlatten(ctx, action, code)
			if err != nil || flatten == nil {
				return err
			}
			return executor.executeAction(ctx, *flatten)
		}
		var privateErr *exchangebinance.PrivateError
		if errors.As(queryErr, &privateErr) && privateErr.Kind == exchangebinance.PrivateErrorNotFound {
			prepared, err := executor.prepareMissingOrder(ctx, action)
			if err != nil || prepared == nil {
				return err
			}
			action = *prepared
			apiKey, apiSecret, err = decryptTestnetCredential(executor.cipher, action.Credential)
			if err != nil {
				return executor.persistUnknown(ctx, action, "credential_decryption_failed", true)
			}
		} else {
			code, pause := testnetOrderFailure(queryErr)
			return executor.persistUnknown(ctx, action, code, pause)
		}
	}
	if action.Cancel {
		result, cancelErr := executor.client.CancelOrder(
			ctx, marketdata.MarketType(action.Account.Market), apiKey, apiSecret,
			action.Instrument.NativeSymbol, action.Order.ClientOrderID,
		)
		if cancelErr == nil {
			return executor.persistResult(ctx, action, result)
		}
		code, _ := testnetOrderFailure(cancelErr)
		return executor.persistUnknown(ctx, action, code, false)
	}
	if action.Order.Purpose == "protection" {
		result, placeErr := executor.client.PlaceProtectiveOrder(
			ctx, marketdata.MarketType(action.Account.Market), apiKey, apiSecret,
			action.Instrument.NativeSymbol, action.Order.ClientOrderID, action.Order.Side,
			action.Order.Quantity, action.Order.StopPrice,
		)
		if placeErr == nil {
			return executor.persistResult(ctx, action, result)
		}
		code, _ := testnetOrderFailure(placeErr)
		return executor.persistUnknown(ctx, action, code, false)
	}

	result, placeErr := executor.client.PlaceMarketOrder(
		ctx, marketdata.MarketType(action.Account.Market), apiKey, apiSecret,
		action.Instrument.NativeSymbol, action.Order.ClientOrderID, action.Order.Side, action.Order.Quantity,
		action.Order.ReduceOnly,
	)
	if placeErr == nil {
		return executor.persistResult(ctx, action, result)
	}
	code, pause := testnetOrderFailure(placeErr)
	if action.Order.Purpose == "flatten" {
		pause = true
	}
	return executor.persistUnknown(ctx, action, code, pause)
}

func (executor *TestnetExecutor) prepareAction(
	ctx context.Context,
	claimed db.TradingIntent,
) (*testnetExecutionAction, error) {
	var action *testnetExecutionAction
	err := executor.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockTestnetAccountExecution(tx, claimed.AccountID); err != nil {
			return err
		}
		intent, err := executor.lockClaimedIntent(tx, claimed.ID)
		if err != nil {
			return err
		}
		for _, purpose := range []string{"flatten", "protection", "rebalance"} {
			var order db.TestnetOrder
			err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
				"intent_id = ? AND purpose = ?", intent.ID, purpose,
			).Take(&order).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			if purpose == "rebalance" && order.Status == "filled" {
				prepared, err := executor.prepareProtectionAction(tx, intent, &order, nil)
				if err != nil {
					return err
				}
				action = prepared
				return nil
			}
			prepared, err := prepareExistingTestnetOrderAction(tx, intent, order, true, false)
			if err != nil {
				return err
			}
			action = prepared
			return nil
		}

		state, blockReason, pause, err := loadAndValidateTestnetExecution(tx, intent, executor.scopeEnvironment())
		if err != nil {
			return err
		}
		if blockReason != "" {
			return blockTestnetIntent(tx, intent, blockReason, pause)
		}
		if state.Account.Market == string(marketdata.MarketTypeSpot) && state.ReduceOnly {
			previous, err := preparePreviousProtectionAction(tx, intent)
			if err != nil {
				return err
			}
			if previous != nil {
				action = previous
				return nil
			}
		}
		if state.DeltaQty.IsZero() {
			prepared, err := executor.prepareProtectionAction(tx, intent, nil, &state)
			if err != nil {
				return err
			}
			action = prepared
			return nil
		}
		now := time.Now().UTC()
		side := "buy"
		if state.DeltaQty.IsNegative() {
			side = "sell"
		}
		reduceOnly := state.ReduceOnly && state.Account.Market == string(marketdata.MarketTypeUSDM)
		order := db.TestnetOrder{
			ID: intent.ID, AccountID: intent.AccountID, IntentID: intent.ID,
			StrategyInstanceID: intent.StrategyInstanceID, InstrumentID: intent.InstrumentID,
			CredentialUpdatedAt:       state.Credential.UpdatedAt,
			SubmittedAccountUpdatedAt: state.Account.UpdatedAt,
			ClientOrderID:             intent.ClientOrderID, Side: side, Quantity: state.DeltaQty.Abs(),
			Purpose: "rebalance", OrderType: "market", ReduceOnly: reduceOnly,
			Status: "prepared", SubmitAttemptCount: 1, SubmittedAt: now,
			CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Create(&order).Error; err != nil {
			return err
		}
		action = &testnetExecutionAction{
			Intent: intent, Order: order, Account: state.Account, Instrument: state.Instrument,
			Credential: state.Credential, ExpectedAccountTime: state.Account.UpdatedAt,
		}
		return nil
	})
	return action, err
}

func prepareExistingTestnetOrderAction(
	tx *gorm.DB,
	intent db.TradingIntent,
	order db.TestnetOrder,
	query, cancel bool,
) (*testnetExecutionAction, error) {
	prepared, errorCode, err := loadTestnetQueryAction(tx, intent, order)
	if err != nil {
		return nil, err
	}
	if query && errorCode == "" {
		now := time.Now().UTC()
		if err := tx.Model(&order).Updates(map[string]any{
			"query_attempt_count": gorm.Expr("query_attempt_count + 1"),
			"last_queried_at":     now, "updated_at": now,
		}).Error; err != nil {
			return nil, err
		}
		order.QueryAttemptCount++
		order.LastQueriedAt = &now
	}
	prepared.PreflightErrorCode = errorCode
	prepared.Query = query
	prepared.Cancel = cancel
	prepared.Order = order
	return &prepared, nil
}

func (executor *TestnetExecutor) lockClaimedIntent(tx *gorm.DB, intentID uuid.UUID) (db.TradingIntent, error) {
	var intent db.TradingIntent
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
		"id = ? AND environment = ? AND status = 'processing' AND worker_id = ?",
		intentID, executor.scopeEnvironment(), executor.workerID,
	).Take(&intent).Error
	return intent, err
}

func loadTestnetQueryAction(
	tx *gorm.DB,
	intent db.TradingIntent,
	order db.TestnetOrder,
) (testnetExecutionAction, string, error) {
	action := testnetExecutionAction{Intent: intent, Order: order, ExpectedAccountTime: time.Time{}}
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
		"id = ? AND owner_user_id = ? AND environment = ? AND market_type = ?",
		intent.AccountID, intent.OwnerUserID, intent.Environment, intent.Market,
	).Take(&action.Account).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return action, "execution_binding_mismatch", nil
		}
		return action, "", err
	}
	action.ExpectedAccountTime = action.Account.UpdatedAt
	if err := tx.Where("id = ? AND market_type = ?", intent.InstrumentID, intent.Market).
		Take(&action.Instrument).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return action, "execution_binding_mismatch", nil
		}
		return action, "", err
	}
	if !validTestnetOrderIntentBinding(intent, order) {
		return action, "execution_binding_mismatch", nil
	}
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where(
		"account_id = ? AND owner_user_id = ? AND status = 'configured' "+
			"AND verification_status = 'verified' AND updated_at = ?",
		intent.AccountID, intent.OwnerUserID, order.CredentialUpdatedAt,
	).Take(&action.Credential).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return action, "execution_state_changed", nil
		}
		return action, "", err
	}
	return action, "", nil
}

func (executor *TestnetExecutor) prepareMissingOrder(
	ctx context.Context,
	previous testnetExecutionAction,
) (*testnetExecutionAction, error) {
	var action *testnetExecutionAction
	err := executor.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockTestnetAccountExecution(tx, previous.Intent.AccountID); err != nil {
			return err
		}
		intent, err := executor.lockClaimedIntent(tx, previous.Intent.ID)
		if err != nil {
			return err
		}
		var order db.TestnetOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND intent_id = ?", previous.Order.ID, intent.ID).
			Take(&order).Error; err != nil {
			return err
		}
		if order.Purpose == "flatten" {
			prepared, err := executor.prepareMissingFlattenOrder(tx, intent, order)
			if err != nil {
				return err
			}
			action = prepared
			return nil
		}
		if order.Purpose != "rebalance" {
			return errors.New("unsupported missing testnet order purpose")
		}
		if order.ExchangeOrderID != nil || order.FilledQuantity.IsPositive() {
			return reconcileTestnetIntent(tx, intent, &order, "missing_observed_order", true)
		}
		state, blockReason, pause, err := loadAndValidateTestnetExecution(tx, intent, executor.scopeEnvironment())
		if err != nil {
			return err
		}
		if blockReason != "" {
			if err := markUnsubmittedTestnetOrder(tx, &order, blockReason); err != nil {
				return err
			}
			return blockTestnetIntent(tx, intent, blockReason, pause)
		}
		if state.DeltaQty.IsZero() {
			if err := markUnsubmittedTestnetOrder(tx, &order, "target_already_reached"); err != nil {
				return err
			}
			return finishTestnetIntent(tx, intent, "executed", "target_already_reached")
		}
		now := time.Now().UTC()
		side := "buy"
		if state.DeltaQty.IsNegative() {
			side = "sell"
		}
		reduceOnly := state.ReduceOnly && state.Account.Market == string(marketdata.MarketTypeUSDM)
		updates := map[string]any{
			"credential_updated_at":        state.Credential.UpdatedAt,
			"submitted_account_updated_at": state.Account.UpdatedAt,
			"side":                         side, "quantity": state.DeltaQty.Abs(),
			"reduce_only":     reduceOnly,
			"filled_quantity": decimal.Zero, "cumulative_quote_quantity": decimal.Zero,
			"average_price": decimal.Zero, "status": "prepared", "last_error_code": "",
			"submit_attempt_count": gorm.Expr("submit_attempt_count + 1"),
			"submitted_at":         now, "observed_at": nil, "updated_at": now,
		}
		if err := tx.Model(&order).Updates(updates).Error; err != nil {
			return err
		}
		order.CredentialUpdatedAt = state.Credential.UpdatedAt
		order.SubmittedAccountUpdatedAt = state.Account.UpdatedAt
		order.Side = side
		order.Quantity = state.DeltaQty.Abs()
		order.ReduceOnly = reduceOnly
		order.Status = "prepared"
		order.LastErrorCode = ""
		order.SubmitAttemptCount++
		order.SubmittedAt = now
		order.ObservedAt = nil
		action = &testnetExecutionAction{
			Intent: intent, Order: order, Account: state.Account, Instrument: state.Instrument,
			Credential: state.Credential, ExpectedAccountTime: state.Account.UpdatedAt,
		}
		return nil
	})
	return action, err
}

func markUnsubmittedTestnetOrder(tx *gorm.DB, order *db.TestnetOrder, reason string) error {
	now := time.Now().UTC()
	order.Status = "rejected"
	order.LastErrorCode = reason
	order.UpdatedAt = now
	return tx.Model(order).Updates(map[string]any{
		"status": "rejected", "last_error_code": reason, "updated_at": now,
	}).Error
}

func loadAndValidateTestnetExecution(
	tx *gorm.DB,
	intent db.TradingIntent,
	expectedEnvironment string,
) (testnetExecutionState, string, bool, error) {
	state := testnetExecutionState{Intent: intent}
	if intent.Environment != expectedEnvironment {
		return state, "execution_environment_mismatch", false, nil
	}
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", intent.AccountID).
		Take(&state.Account).Error; err != nil {
		return state, "", false, err
	}
	if err := tx.Where("id = ?", intent.StrategyInstanceID).Take(&state.Instance).Error; err != nil {
		return state, "", false, err
	}
	if err := tx.Where("id = ?", intent.StrategySignalID).Take(&state.Signal).Error; err != nil {
		return state, "", false, err
	}
	if err := tx.Where("id = ?", intent.InstrumentID).Take(&state.Instrument).Error; err != nil {
		return state, "", false, err
	}
	control, err := loadTradingControl(tx, clause.Locking{Strength: "SHARE"})
	if err != nil {
		return state, "", false, err
	}
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where(
		"account_id = ? AND owner_user_id = ? AND status = 'configured' AND verification_status = 'verified'",
		intent.AccountID, intent.OwnerUserID,
	).Take(&state.Credential).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return state, "credentials_not_verified", true, nil
		}
		return state, "", false, err
	}
	var reconciliation db.TestnetReconciliation
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where(
		"account_id = ? AND credential_updated_at = ? AND status = 'matched'",
		intent.AccountID, state.Credential.UpdatedAt,
	).Take(&reconciliation).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return state, "testnet_reconciliation_required", true, nil
		}
		return state, "", false, err
	}
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
		"account_id = ? AND credential_updated_at = ?", intent.AccountID, state.Credential.UpdatedAt,
	).Take(&state.Risk).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return state, "testnet_risk_state_missing", true, nil
		}
		return state, "", false, err
	}

	if state.Account.OwnerUserID != intent.OwnerUserID || state.Instance.OwnerUserID != intent.OwnerUserID ||
		state.Signal.OwnerUserID != intent.OwnerUserID || state.Account.Market != intent.Market ||
		state.Instrument.Market != intent.Market || state.Instance.TradingAccountID == nil ||
		*state.Instance.TradingAccountID != intent.AccountID || state.Instance.AllocationUSDT == nil ||
		state.Instance.Environment != intent.Environment || state.Account.Environment != intent.Environment {
		return state, "execution_binding_mismatch", true, nil
	}
	if !validTestnetStopLossRatio(state.Instance.StopLossRatio) {
		return state, "protection_configuration_invalid", true, nil
	}
	if state.Signal.StrategyInstanceID != intent.StrategyInstanceID || state.Signal.InstrumentID != intent.InstrumentID ||
		state.Signal.Target.Cmp(intent.Target) != 0 || state.Signal.Mode != intent.Mode || !state.Instance.IsEnabled {
		return state, "strategy_state_changed", false, nil
	}
	if (intent.Mode == "manual" && state.Signal.Status != "approved") ||
		(intent.Mode == "auto" && state.Signal.Status != "active") {
		return state, "signal_state_changed", false, nil
	}
	if intent.Environment == "live" && ((intent.Mode != "manual" && intent.Mode != "auto") ||
		state.Account.ManualAuthorizedAt == nil) {
		return state, "live_manual_not_authorized", true, nil
	}
	if state.Instrument.Venue != string(marketdata.VenueBinance) || state.Instrument.Status != "trading" ||
		state.Instrument.QuoteAsset != "USDT" {
		return state, "instrument_not_testnet_executable", true, nil
	}
	var whitelistCount int64
	if err := tx.Model(&db.TradingAccountInstrument{}).Where(
		"account_id = ? AND instrument_id = ?", intent.AccountID, intent.InstrumentID,
	).Count(&whitelistCount).Error; err != nil {
		return state, "", false, err
	}
	if whitelistCount != 1 {
		return state, "instrument_not_whitelisted", true, nil
	}

	now := time.Now().UTC()
	state.Price, err = loadCurrentTestnetPrice(tx, state.Instrument.ID, state.Account.MaxQuoteAgeSeconds, now)
	if err != nil {
		return state, testnetQuoteFailure(err), true, nil
	}
	state.Projection, err = loadTestnetProjection(tx, state.Account, &state.Risk, now)
	if err != nil {
		return state, testnetQuoteFailure(err), true, nil
	}
	currentQuantity := decimal.Zero
	if position := state.Projection.Positions[intent.InstrumentID]; position != nil {
		currentQuantity = position.Quantity
		state.CurrentOwner = position.OwnerStrategyInstanceID
	}
	state.TargetQty = state.Instance.AllocationUSDT.Mul(intent.Target).Div(state.Price)
	state.TargetQty = quantizeTowardZero(state.TargetQty, state.Instrument.QuantityStep)
	state.DeltaQty = state.TargetQty.Sub(currentQuantity)
	state.ReduceOnly = isPaperReduction(currentQuantity, state.TargetQty)
	if !currentQuantity.IsZero() && (state.CurrentOwner == nil || *state.CurrentOwner != intent.StrategyInstanceID) {
		return state, "position_owner_conflict", true, nil
	}
	complete, err := tradingRiskComplete(tx, state.Account)
	if err != nil {
		return state, "", false, err
	}
	if (!complete || state.Account.Status != "active" || control.EmergencyStopped) && !state.ReduceOnly {
		switch {
		case control.EmergencyStopped:
			return state, "global_emergency_stop", false, nil
		case state.Account.Status != "active":
			return state, "account_paused", false, nil
		default:
			return state, "risk_configuration_incomplete", true, nil
		}
	}
	if intent.Mode == "auto" && (!state.Account.AutomationEnabled || state.Account.AutomationAuthorizedAt == nil ||
		(intent.Environment == "live" && state.Account.AutoAuthorizedAt == nil)) &&
		!state.ReduceOnly {
		return state, "automation_not_authorized", true, nil
	}
	if state.DeltaQty.IsZero() {
		return state, "", false, nil
	}
	reason := validateTestnetNotionalRisk(state)
	if reason != "" {
		return state, reason, true, nil
	}
	return state, "", false, nil
}

var (
	errTestnetQuoteMissing = errors.New("testnet risk quote missing")
	errTestnetQuoteStale   = errors.New("testnet risk quote stale")
)

func loadCurrentTestnetPrice(
	tx *gorm.DB,
	instrumentID uuid.UUID,
	maxAgeSeconds *int,
	now time.Time,
) (decimal.Decimal, error) {
	var ticker struct {
		OccurredAt time.Time
		LastPrice  decimal.Decimal
	}
	if err := tx.Table("market_ticker_snapshots").Select("occurred_at", "last_price").Where(
		"venue = ? AND instrument_id = ?", marketdata.VenueBinance, instrumentID,
	).Take(&ticker).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return decimal.Zero, errTestnetQuoteMissing
		}
		return decimal.Zero, err
	}
	age := now.Sub(ticker.OccurredAt.UTC())
	if maxAgeSeconds == nil || age < 0 || age > time.Duration(*maxAgeSeconds)*time.Second ||
		!ticker.LastPrice.IsPositive() {
		return decimal.Zero, errTestnetQuoteStale
	}
	return ticker.LastPrice, nil
}

func testnetQuoteFailure(err error) string {
	switch {
	case errors.Is(err, errTestnetQuoteMissing), errors.Is(err, gorm.ErrRecordNotFound):
		return "quote_missing"
	case errors.Is(err, errTestnetQuoteStale):
		return "quote_stale"
	default:
		return "testnet_projection_failed"
	}
}

func loadTestnetProjection(
	tx *gorm.DB,
	account db.TradingAccount,
	risk *db.TestnetRiskState,
	now time.Time,
) (testnetProjection, error) {
	projection := testnetProjection{
		Positions: map[uuid.UUID]*testnetManagedPosition{},
		Cash:      risk.BaselineEquity,
	}
	var orders []db.TestnetOrder
	if err := tx.Where(
		"account_id = ? AND credential_updated_at = ? AND filled_quantity > 0 AND observed_at IS NOT NULL",
		account.ID, risk.CredentialUpdatedAt,
	).Order("created_at, id").Find(&orders).Error; err != nil {
		return projection, err
	}
	feeRate := decimal.Zero
	if account.PaperFeeRate != nil {
		feeRate = *account.PaperFeeRate
	}
	for _, order := range orders {
		position := projection.Positions[order.InstrumentID]
		if position == nil {
			position = &testnetManagedPosition{InstrumentID: order.InstrumentID}
			projection.Positions[order.InstrumentID] = position
		}
		realized, err := applyTestnetProjectedFill(position, order)
		if err != nil {
			return projection, err
		}
		fee := order.CumulativeQuoteQuantity.Mul(feeRate)
		if account.Market == string(marketdata.MarketTypeSpot) {
			if order.Side == "buy" {
				projection.Cash = projection.Cash.Sub(order.CumulativeQuoteQuantity).Sub(fee)
			} else {
				projection.Cash = projection.Cash.Add(order.CumulativeQuoteQuantity).Sub(fee)
			}
		} else {
			projection.Cash = projection.Cash.Add(realized).Sub(fee)
		}
	}

	positionValue := decimal.Zero
	unrealized := decimal.Zero
	for _, position := range projection.Positions {
		if position.Quantity.IsZero() {
			continue
		}
		price, err := loadCurrentTestnetPrice(tx, position.InstrumentID, account.MaxQuoteAgeSeconds, now)
		if err != nil {
			return projection, err
		}
		position.LastPrice = price
		positionValue = positionValue.Add(position.Quantity.Mul(price))
		unrealized = unrealized.Add(price.Sub(position.AverageEntryPrice).Mul(position.Quantity))
	}
	if account.Market == string(marketdata.MarketTypeSpot) {
		projection.Equity = projection.Cash.Add(positionValue)
	} else {
		projection.Equity = projection.Cash.Add(unrealized)
	}
	if projection.Equity.GreaterThan(risk.PeakEquity) {
		risk.PeakEquity = projection.Equity
	}
	if utcDay(now).After(utcDay(risk.DayStartDate)) {
		risk.DayStartDate = utcDay(now)
		risk.DayStartEquity = projection.Equity
	}
	risk.Equity = projection.Equity
	risk.UpdatedAt = now
	if err := tx.Model(risk).Updates(map[string]any{
		"equity": risk.Equity, "peak_equity": risk.PeakEquity,
		"day_start_date": risk.DayStartDate, "day_start_equity": risk.DayStartEquity,
		"updated_at": risk.UpdatedAt,
	}).Error; err != nil {
		return projection, err
	}
	return projection, nil
}

func applyTestnetProjectedFill(position *testnetManagedPosition, order db.TestnetOrder) (decimal.Decimal, error) {
	if position == nil || !order.FilledQuantity.IsPositive() || !order.AveragePrice.IsPositive() {
		return decimal.Zero, errors.New("invalid testnet fill projection")
	}
	if !position.Quantity.IsZero() && position.OwnerStrategyInstanceID != nil &&
		*position.OwnerStrategyInstanceID != order.StrategyInstanceID {
		return decimal.Zero, errors.New("testnet position owner conflict")
	}
	signedQuantity := order.FilledQuantity
	if order.Side == "sell" {
		signedQuantity = signedQuantity.Neg()
	}
	oldQuantity := position.Quantity
	newQuantity := oldQuantity.Add(signedQuantity)
	realized := decimal.Zero
	if oldQuantity.IsZero() || oldQuantity.Sign() == signedQuantity.Sign() {
		if !newQuantity.IsZero() {
			position.AverageEntryPrice = oldQuantity.Abs().Mul(position.AverageEntryPrice).
				Add(signedQuantity.Abs().Mul(order.AveragePrice)).Div(newQuantity.Abs())
		}
	} else {
		closingQuantity := decimal.Min(oldQuantity.Abs(), signedQuantity.Abs())
		realized = order.AveragePrice.Sub(position.AverageEntryPrice).Mul(closingQuantity).
			Mul(decimal.NewFromInt(int64(oldQuantity.Sign())))
		if newQuantity.IsZero() {
			position.AverageEntryPrice = decimal.Zero
		} else if newQuantity.Sign() != oldQuantity.Sign() {
			position.AverageEntryPrice = order.AveragePrice
		}
	}
	position.Quantity = newQuantity
	position.RealizedPnL = position.RealizedPnL.Add(realized)
	if newQuantity.IsZero() {
		position.OwnerStrategyInstanceID = nil
	} else {
		ownerID := order.StrategyInstanceID
		position.OwnerStrategyInstanceID = &ownerID
	}
	return realized, nil
}

func validateTestnetNotionalRisk(state testnetExecutionState) string {
	orderNotional := state.DeltaQty.Abs().Mul(state.Price)
	targetNotional := state.TargetQty.Abs().Mul(state.Price)
	if state.DeltaQty.Abs().LessThan(state.Instrument.MinQuantity) || orderNotional.LessThan(state.Instrument.MinNotional) {
		return "order_below_minimum"
	}
	if state.ReduceOnly {
		return ""
	}
	if state.Account.MaxOrderNotional == nil || orderNotional.GreaterThan(*state.Account.MaxOrderNotional) {
		return "order_notional_limit"
	}
	if state.Account.MaxSymbolNotional == nil || targetNotional.GreaterThan(*state.Account.MaxSymbolNotional) {
		return "symbol_notional_limit"
	}
	total := targetNotional
	for instrumentID, position := range state.Projection.Positions {
		if instrumentID == state.Instrument.ID {
			continue
		}
		total = total.Add(position.Quantity.Abs().Mul(position.LastPrice))
	}
	if state.Account.MaxTotalNotional == nil || total.GreaterThan(*state.Account.MaxTotalNotional) {
		return "account_notional_limit"
	}
	if testnetRiskBreached(state.Account, state.Risk) {
		if state.Account.MaxDailyLoss != nil && state.Risk.DayStartEquity.Sub(state.Risk.Equity).
			GreaterThanOrEqual(*state.Account.MaxDailyLoss) {
			return "daily_loss_limit"
		}
		return "drawdown_limit"
	}
	feeRate := decimal.Zero
	if state.Account.PaperFeeRate != nil {
		feeRate = *state.Account.PaperFeeRate
	}
	fee := orderNotional.Mul(feeRate)
	if state.Account.Market == string(marketdata.MarketTypeSpot) && state.DeltaQty.IsPositive() &&
		orderNotional.Add(fee).GreaterThan(state.Projection.Cash) {
		return "insufficient_balance"
	}
	if state.Account.Market == string(marketdata.MarketTypeUSDM) {
		if state.Account.Leverage == nil || total.Div(decimal.NewFromInt(int64(*state.Account.Leverage))).
			Add(fee).GreaterThan(state.Risk.Equity) {
			return "insufficient_margin"
		}
	}
	return ""
}

func testnetRiskBreached(account db.TradingAccount, risk db.TestnetRiskState) bool {
	if account.MaxDailyLoss == nil || account.MaxDrawdown == nil {
		return true
	}
	dailyLoss := risk.DayStartEquity.Sub(risk.Equity)
	drawdown := risk.PeakEquity.Sub(risk.Equity)
	return dailyLoss.GreaterThanOrEqual(*account.MaxDailyLoss) || drawdown.GreaterThanOrEqual(*account.MaxDrawdown)
}

func (executor *TestnetExecutor) persistResult(
	ctx context.Context,
	action testnetExecutionAction,
	result exchangebinance.OrderResult,
) error {
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	var nextAction *testnetExecutionAction
	err := executor.database.WithContext(persistCtx).Transaction(func(tx *gorm.DB) error {
		intent, order, account, current, err := executor.lockCurrentOrderState(tx, action)
		if err != nil {
			return err
		}
		if !current {
			return reconcileTestnetIntent(tx, intent, &order, "execution_state_changed", true)
		}
		status, err := normalizeTestnetOrderStatus(result.Status)
		invalidResult := err != nil || result.Symbol != action.Instrument.NativeSymbol ||
			result.ClientOrderID != order.ClientOrderID || result.Side != order.Side ||
			result.ExchangeOrderID <= 0 || result.ObservedAt.IsZero() ||
			(order.ExchangeOrderID != nil && result.ExchangeOrderID != *order.ExchangeOrderID) ||
			!validTestnetStoredOrderResult(order, status, result) ||
			(action.Cancel && status != "canceled" && status != "filled" && status != "expired" && status != "rejected")
		if invalidResult {
			if order.Purpose == "protection" {
				failureCode := protectionFailureCode("order_protocol_error")
				if err := tx.Model(&order).Updates(map[string]any{
					"status": "unknown", "last_error_code": failureCode, "updated_at": time.Now().UTC(),
				}).Error; err != nil {
					return err
				}
				nextAction, err = executor.scheduleProtectionFailure(tx, intent, failureCode)
				return err
			}
			return reconcileTestnetIntent(tx, intent, &order, "order_protocol_error", true)
		}
		errorCode := ""
		if status == "rejected" {
			errorCode = "exchange_rejected"
		}
		observedAt := result.ObservedAt.UTC()
		exchangeOrderID := result.ExchangeOrderID
		if err := tx.Model(&order).Updates(map[string]any{
			"exchange_order_id":         exchangeOrderID,
			"filled_quantity":           result.ExecutedQuantity,
			"cumulative_quote_quantity": result.CumulativeQuoteQuantity,
			"average_price":             result.AveragePrice,
			"status":                    status, "last_error_code": errorCode,
			"observed_at": observedAt, "updated_at": time.Now().UTC(),
		}).Error; err != nil {
			return err
		}
		order.ExchangeOrderID = &exchangeOrderID
		order.FilledQuantity = result.ExecutedQuantity
		order.CumulativeQuoteQuantity = result.CumulativeQuoteQuantity
		order.AveragePrice = result.AveragePrice
		order.Status = status
		order.LastErrorCode = errorCode
		order.ObservedAt = &observedAt
		riskPaused, err := refreshTestnetRiskAfterFill(tx, account, order)
		if err != nil {
			return err
		}

		switch order.Purpose {
		case "protection":
			var err error
			nextAction, err = executor.persistProtectionResult(tx, intent, order, status)
			return err
		case "flatten":
			return persistFlattenResult(tx, intent, status)
		default:
			switch status {
			case "new", "partially_filled":
				return setTestnetIntentReconciling(tx, intent, "order_"+status, false)
			case "filled":
				return setTestnetIntentReconciling(tx, intent, "protection_required", false)
			default:
				if (status == "canceled" || status == "expired") && order.FilledQuantity.IsPositive() {
					if order.FilledQuantity.Equal(order.Quantity) {
						return setTestnetIntentReconciling(tx, intent, "protection_required", false)
					}
					nextAction, err = executor.scheduleProtectionFailure(
						tx, intent, "rebalance_"+status+"_partial_fill",
					)
					return err
				}
				if err := finishTestnetIntent(tx, intent, "failed", "exchange_"+status); err != nil {
					return err
				}
				if riskPaused {
					return nil
				}
				return pauseTestnetAccount(tx, account.ID, "testnet_order_"+status, time.Now().UTC())
			}
		}
	})
	if err != nil || nextAction == nil {
		return err
	}
	return executor.executeAction(ctx, *nextAction)
}

func (executor *TestnetExecutor) persistProtectionResult(
	tx *gorm.DB,
	intent db.TradingIntent,
	order db.TestnetOrder,
	status string,
) (*testnetExecutionAction, error) {
	replacing := order.IntentID != intent.ID
	if replacing && order.FilledQuantity.IsPositive() {
		return executor.scheduleProtectionFailure(tx, intent, "protection_replacement_race")
	}
	switch status {
	case "new":
		if replacing {
			return nil, setTestnetIntentReconciling(tx, intent, "protection_cancel_required", false)
		}
		return nil, finishTestnetIntent(tx, intent, "executed", "")
	case "partially_filled":
		return nil, setTestnetIntentReconciling(tx, intent, "protective_order_partially_filled", false)
	case "filled":
		return nil, finishTestnetIntent(tx, intent, "executed", "protective_order_filled")
	case "canceled", "expired", "rejected":
		if replacing {
			return nil, setTestnetIntentReconciling(tx, intent, "protection_required", false)
		}
		return executor.scheduleProtectionFailure(tx, intent, "protection_"+status)
	default:
		return nil, errors.New("unsupported protection result status")
	}
}

func persistFlattenResult(tx *gorm.DB, intent db.TradingIntent, status string) error {
	switch status {
	case "new", "partially_filled":
		return setTestnetIntentReconciling(tx, intent, "emergency_flatten_pending", false)
	case "filled":
		return finishTestnetIntent(tx, intent, "failed", "protection_failed_flattened")
	case "canceled", "expired", "rejected":
		return finishTestnetIntent(tx, intent, "failed", "emergency_flatten_"+status)
	default:
		return errors.New("unsupported flatten result status")
	}
}

func refreshTestnetRiskAfterFill(tx *gorm.DB, account db.TradingAccount, order db.TestnetOrder) (bool, error) {
	if !order.FilledQuantity.IsPositive() {
		return false, nil
	}
	now := time.Now().UTC()
	var risk db.TestnetRiskState
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
		"account_id = ? AND credential_updated_at = ?", account.ID, order.CredentialUpdatedAt,
	).Take(&risk).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return true, pauseTestnetAccount(tx, account.ID, "testnet_risk_state_missing", now)
		}
		return false, err
	}
	if _, err := loadTestnetProjection(tx, account, &risk, now); err != nil {
		if errors.Is(err, errTestnetQuoteMissing) || errors.Is(err, errTestnetQuoteStale) ||
			strings.Contains(err.Error(), "testnet position owner conflict") {
			return true, pauseTestnetAccount(tx, account.ID, testnetQuoteFailure(err), now)
		}
		return false, err
	}
	if testnetRiskBreached(account, risk) {
		return true, pauseTestnetAccount(tx, account.ID, "testnet_risk_limit", now)
	}
	return false, nil
}

func (executor *TestnetExecutor) persistUnknown(
	ctx context.Context,
	action testnetExecutionAction,
	errorCode string,
	pause bool,
) error {
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return executor.database.WithContext(persistCtx).Transaction(func(tx *gorm.DB) error {
		if err := lockTestnetAccountExecution(tx, action.Intent.AccountID); err != nil {
			return err
		}
		intent, err := executor.lockClaimedIntent(tx, action.Intent.ID)
		if err != nil {
			return err
		}
		var order db.TestnetOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
			"id = ?", action.Order.ID,
		).Take(&order).Error; err != nil {
			return err
		}
		if !validTestnetOrderIntentBinding(intent, order) || !sameTestnetOrderAction(order, action.Order) {
			return setTestnetIntentReconciling(tx, intent, "execution_state_changed", true)
		}
		return reconcileTestnetIntent(tx, intent, &order, errorCode, pause)
	})
}

func (executor *TestnetExecutor) lockCurrentOrderState(
	tx *gorm.DB,
	action testnetExecutionAction,
) (db.TradingIntent, db.TestnetOrder, db.TradingAccount, bool, error) {
	if err := lockTestnetAccountExecution(tx, action.Intent.AccountID); err != nil {
		return db.TradingIntent{}, db.TestnetOrder{}, db.TradingAccount{}, false, err
	}
	intent, err := executor.lockClaimedIntent(tx, action.Intent.ID)
	if err != nil {
		return intent, db.TestnetOrder{}, db.TradingAccount{}, false, err
	}
	var order db.TestnetOrder
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
		"id = ?", action.Order.ID,
	).Take(&order).Error; err != nil {
		return intent, order, db.TradingAccount{}, false, err
	}
	var account db.TradingAccount
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
		"id = ? AND owner_user_id = ? AND environment = ?", intent.AccountID, intent.OwnerUserID, intent.Environment,
	).Take(&account).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return intent, order, account, false, nil
		}
		return intent, order, account, false, err
	}
	var credential db.TradingAccountCredential
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where(
		"id = ? AND account_id = ? AND status = 'configured' AND verification_status = 'verified'",
		action.Credential.ID, intent.AccountID,
	).Take(&credential).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return intent, order, account, false, nil
		}
		return intent, order, account, false, err
	}
	current := validTestnetOrderIntentBinding(intent, order) && sameTestnetOrderAction(order, action.Order) &&
		account.UpdatedAt.Equal(action.ExpectedAccountTime) && credential.UpdatedAt.Equal(action.Credential.UpdatedAt)
	return intent, order, account, current, nil
}

func validTestnetOrderIntentBinding(intent db.TradingIntent, order db.TestnetOrder) bool {
	if order.AccountID != intent.AccountID || order.StrategyInstanceID != intent.StrategyInstanceID ||
		order.InstrumentID != intent.InstrumentID {
		return false
	}
	switch order.Purpose {
	case "rebalance":
		return order.ID == intent.ID && order.IntentID == intent.ID && order.ClientOrderID == intent.ClientOrderID
	case "flatten":
		return order.IntentID == intent.ID && order.ClientOrderID != intent.ClientOrderID
	case "protection":
		return order.ClientOrderID != intent.ClientOrderID
	default:
		return false
	}
}

func sameTestnetOrderAction(current, submitted db.TestnetOrder) bool {
	return current.ID == submitted.ID && current.AccountID == submitted.AccountID &&
		current.IntentID == submitted.IntentID && current.StrategyInstanceID == submitted.StrategyInstanceID &&
		current.InstrumentID == submitted.InstrumentID &&
		current.CredentialUpdatedAt.Equal(submitted.CredentialUpdatedAt) &&
		current.SubmittedAccountUpdatedAt.Equal(submitted.SubmittedAccountUpdatedAt) &&
		current.ClientOrderID == submitted.ClientOrderID && current.Side == submitted.Side &&
		current.Quantity.Equal(submitted.Quantity) && current.Purpose == submitted.Purpose &&
		current.OrderType == submitted.OrderType && current.StopPrice.Equal(submitted.StopPrice) &&
		current.ClosePosition == submitted.ClosePosition && current.ReduceOnly == submitted.ReduceOnly &&
		current.WorkingType == submitted.WorkingType && equalOptionalUUID(current.ReplacesOrderID, submitted.ReplacesOrderID) &&
		current.Status == submitted.Status && current.SubmitAttemptCount == submitted.SubmitAttemptCount &&
		current.QueryAttemptCount == submitted.QueryAttemptCount
}

func equalOptionalUUID(left, right *uuid.UUID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func reconcileTestnetIntent(
	tx *gorm.DB,
	intent db.TradingIntent,
	order *db.TestnetOrder,
	errorCode string,
	pause bool,
) error {
	now := time.Now().UTC()
	if err := tx.Model(order).Updates(map[string]any{
		"status": "unknown", "last_error_code": errorCode, "updated_at": now,
	}).Error; err != nil {
		return err
	}
	return setTestnetIntentReconciling(tx, intent, errorCode, pause)
}

func setTestnetIntentReconciling(
	tx *gorm.DB,
	intent db.TradingIntent,
	reason string,
	pause bool,
) error {
	now := time.Now().UTC()
	result := tx.Model(&db.TradingIntent{}).Where(
		"id = ? AND environment = ? AND status = 'processing'", intent.ID, intent.Environment,
	).Updates(map[string]any{
		"status": "reconciling", "block_reason": reason,
		"claimed_at": nil, "worker_id": nil, "updated_at": now,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	if pause {
		return pauseTestnetAccount(tx, intent.AccountID, reason, now)
	}
	return nil
}

func blockTestnetIntent(tx *gorm.DB, intent db.TradingIntent, reason string, pause bool) error {
	if err := finishTestnetIntent(tx, intent, "blocked", reason); err != nil {
		return err
	}
	if pause {
		return pauseTestnetAccount(tx, intent.AccountID, reason, time.Now().UTC())
	}
	return nil
}

func finishTestnetIntent(tx *gorm.DB, intent db.TradingIntent, status, reason string) error {
	now := time.Now().UTC()
	result := tx.Model(&db.TradingIntent{}).Where(
		"id = ? AND environment = ? AND status = 'processing'", intent.ID, intent.Environment,
	).Updates(map[string]any{
		"status": status, "block_reason": reason, "claimed_at": nil, "worker_id": nil,
		"completed_at": now, "updated_at": now,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func normalizeTestnetOrderStatus(status string) (string, error) {
	switch status {
	case "new", "partially_filled", "filled", "canceled", "rejected", "expired":
		return status, nil
	case "pending_cancel":
		return "new", nil
	case "expired_in_match":
		return "expired", nil
	default:
		return "", errors.New("unsupported testnet order status")
	}
}

func validTestnetOrderResult(status string, result exchangebinance.OrderResult) bool {
	if !result.OriginalQuantity.IsPositive() || result.ExecutedQuantity.IsNegative() ||
		result.ExecutedQuantity.GreaterThan(result.OriginalQuantity) || result.CumulativeQuoteQuantity.IsNegative() ||
		result.AveragePrice.IsNegative() {
		return false
	}
	if (result.ExecutedQuantity.IsZero() &&
		(!result.CumulativeQuoteQuantity.IsZero() || !result.AveragePrice.IsZero())) ||
		(result.ExecutedQuantity.IsPositive() &&
			(!result.CumulativeQuoteQuantity.IsPositive() || !result.AveragePrice.IsPositive())) {
		return false
	}
	switch status {
	case "new", "rejected":
		return result.ExecutedQuantity.IsZero()
	case "partially_filled":
		return result.ExecutedQuantity.IsPositive() && result.ExecutedQuantity.LessThan(result.OriginalQuantity)
	case "filled":
		return result.ExecutedQuantity.Equal(result.OriginalQuantity)
	case "canceled", "expired":
		return true
	default:
		return false
	}
}

func validTestnetStoredOrderResult(
	order db.TestnetOrder,
	status string,
	result exchangebinance.OrderResult,
) bool {
	if result.OrderType != order.OrderType || !result.OriginalQuantity.Equal(order.Quantity) ||
		!result.StopPrice.Equal(order.StopPrice) || result.WorkingType != order.WorkingType ||
		result.ClosePosition != order.ClosePosition || result.ReduceOnly != order.ReduceOnly {
		return false
	}
	if !order.ClosePosition {
		return validTestnetOrderResult(status, result)
	}
	if !result.OriginalQuantity.IsZero() || result.ExecutedQuantity.IsNegative() ||
		result.CumulativeQuoteQuantity.IsNegative() || result.AveragePrice.IsNegative() {
		return false
	}
	if (result.ExecutedQuantity.IsZero() &&
		(!result.CumulativeQuoteQuantity.IsZero() || !result.AveragePrice.IsZero())) ||
		(result.ExecutedQuantity.IsPositive() &&
			(!result.CumulativeQuoteQuantity.IsPositive() || !result.AveragePrice.IsPositive())) {
		return false
	}
	switch status {
	case "new", "rejected":
		return result.ExecutedQuantity.IsZero()
	case "partially_filled", "filled":
		return result.ExecutedQuantity.IsPositive()
	case "canceled", "expired":
		return true
	default:
		return false
	}
}

func testnetProtectionQueryFailure(err error) string {
	var privateErr *exchangebinance.PrivateError
	if errors.As(err, &privateErr) && privateErr.Kind == exchangebinance.PrivateErrorNotFound {
		return "not_found"
	}
	code, _ := testnetOrderFailure(err)
	return code
}

func testnetOrderFailure(err error) (string, bool) {
	var privateErr *exchangebinance.PrivateError
	if !errors.As(err, &privateErr) {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return "request_interrupted", false
		}
		return "exchange_unavailable", false
	}
	switch privateErr.Kind {
	case exchangebinance.PrivateErrorAuthentication:
		return "authentication_failed", true
	case exchangebinance.PrivateErrorPermission:
		return "permission_denied", true
	case exchangebinance.PrivateErrorRateLimited:
		return "rate_limited", false
	case exchangebinance.PrivateErrorClockSkew:
		return "clock_skew", true
	case exchangebinance.PrivateErrorRejected:
		return "exchange_rejected", true
	case exchangebinance.PrivateErrorProtocol:
		return "protocol_error", true
	default:
		return "exchange_unavailable", false
	}
}
