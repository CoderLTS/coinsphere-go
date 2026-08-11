package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/marketdata"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type testnetProtectionState struct {
	Account    db.TradingAccount
	Instance   db.StrategyInstance
	Instrument db.MarketInstrument
	Credential db.TradingAccountCredential
	Position   testnetManagedPosition
}

func (executor *TestnetExecutor) prepareProtectionAction(
	tx *gorm.DB,
	intent db.TradingIntent,
	rebalance *db.TestnetOrder,
	execution *testnetExecutionState,
) (*testnetExecutionAction, error) {
	state, err := loadTestnetProtectionState(tx, intent)
	if err != nil {
		return nil, err
	}
	if execution != nil {
		state.Account = execution.Account
		state.Instance = execution.Instance
		state.Instrument = execution.Instrument
		state.Credential = execution.Credential
		if position := execution.Projection.Positions[intent.InstrumentID]; position != nil {
			state.Position = *position
		}
	}

	previous, err := preparePreviousProtectionAction(tx, intent)
	if err != nil || previous != nil {
		return previous, err
	}

	if state.Position.Quantity.IsZero() {
		reason := "target_already_reached"
		if rebalance != nil {
			reason = "position_closed"
		}
		return nil, finishTestnetIntent(tx, intent, "executed", reason)
	}
	if state.Position.OwnerStrategyInstanceID == nil ||
		*state.Position.OwnerStrategyInstanceID != intent.StrategyInstanceID {
		return executor.prepareEmergencyFlattenWithState(
			tx, intent, state, "protection_position_owner_conflict",
		)
	}
	if !validTestnetStopLossRatio(state.Instance.StopLossRatio) || !state.Position.AverageEntryPrice.IsPositive() {
		return executor.prepareEmergencyFlattenWithState(
			tx, intent, state, "protection_configuration_invalid",
		)
	}

	stopPrice := protectiveStopPrice(
		state.Position.Quantity, state.Position.AverageEntryPrice,
		*state.Instance.StopLossRatio, state.Instrument.PriceTick,
	)
	if !stopPrice.IsPositive() {
		return executor.prepareEmergencyFlattenWithState(
			tx, intent, state, "protection_price_invalid",
		)
	}
	orderID, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	side := oppositeTestnetSide(state.Position.Quantity)
	quantity := state.Position.Quantity.Abs()
	orderType := "stop_loss"
	closePosition := false
	workingType := ""
	if state.Account.Market == string(marketdata.MarketTypeUSDM) {
		quantity = decimal.Zero
		orderType = "stop_market"
		closePosition = true
		workingType = "mark_price"
	}
	var replacesOrderID *uuid.UUID
	var latest db.TestnetOrder
	if err := tx.Where(
		"account_id = ? AND instrument_id = ? AND purpose = 'protection'",
		intent.AccountID, intent.InstrumentID,
	).Order("created_at DESC, id DESC").Take(&latest).Error; err == nil {
		replacesOrderID = &latest.ID
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	order := db.TestnetOrder{
		ID: orderID, AccountID: intent.AccountID, IntentID: intent.ID,
		StrategyInstanceID: intent.StrategyInstanceID, InstrumentID: intent.InstrumentID,
		CredentialUpdatedAt:       state.Credential.UpdatedAt,
		SubmittedAccountUpdatedAt: state.Account.UpdatedAt,
		ClientOrderID:             testnetChildClientOrderID("p", orderID),
		Side:                      side, Quantity: quantity, Purpose: "protection", OrderType: orderType,
		StopPrice: stopPrice, ClosePosition: closePosition, WorkingType: workingType,
		ReplacesOrderID: replacesOrderID, Status: "prepared", SubmitAttemptCount: 1,
		SubmittedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := tx.Create(&order).Error; err != nil {
		return nil, err
	}
	return &testnetExecutionAction{
		Intent: intent, Order: order, Account: state.Account, Instrument: state.Instrument,
		Credential: state.Credential, ExpectedAccountTime: state.Account.UpdatedAt,
	}, nil
}

func preparePreviousProtectionAction(
	tx *gorm.DB,
	intent db.TradingIntent,
) (*testnetExecutionAction, error) {
	var previous db.TestnetOrder
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
		"account_id = ? AND instrument_id = ? "+
			"AND purpose = 'protection' AND intent_id <> ? "+
			"AND status IN ('prepared', 'unknown', 'new', 'partially_filled')",
		intent.AccountID, intent.InstrumentID, intent.ID,
	).Order("created_at DESC, id DESC").Take(&previous).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	cancel := previous.Status == "new" || previous.Status == "partially_filled"
	return prepareExistingTestnetOrderAction(tx, intent, previous, !cancel, cancel)
}

func loadTestnetProtectionState(tx *gorm.DB, intent db.TradingIntent) (testnetProtectionState, error) {
	state := testnetProtectionState{}
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
		"id = ? AND owner_user_id = ? AND environment = ? AND market_type = ?",
		intent.AccountID, intent.OwnerUserID, intent.Environment, intent.Market,
	).Take(&state.Account).Error; err != nil {
		return state, err
	}
	if err := tx.Where(
		"id = ? AND owner_user_id = ? AND trading_account_id = ? AND environment = ?",
		intent.StrategyInstanceID, intent.OwnerUserID, intent.AccountID, intent.Environment,
	).Take(&state.Instance).Error; err != nil {
		return state, err
	}
	if err := tx.Where("id = ? AND market_type = ?", intent.InstrumentID, intent.Market).
		Take(&state.Instrument).Error; err != nil {
		return state, err
	}
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where(
		"account_id = ? AND owner_user_id = ? AND status = 'configured' AND verification_status = 'verified'",
		intent.AccountID, intent.OwnerUserID,
	).Take(&state.Credential).Error; err != nil {
		return state, err
	}
	position, err := loadTestnetManagedPosition(
		tx, intent.AccountID, state.Credential.UpdatedAt, intent.InstrumentID,
	)
	if err != nil {
		return state, err
	}
	state.Position = position
	return state, nil
}

func loadTestnetManagedPosition(
	tx *gorm.DB,
	accountID uuid.UUID,
	credentialUpdatedAt time.Time,
	instrumentID uuid.UUID,
) (testnetManagedPosition, error) {
	position := testnetManagedPosition{InstrumentID: instrumentID}
	var orders []db.TestnetOrder
	if err := tx.Where(
		"account_id = ? AND credential_updated_at = ? AND instrument_id = ? "+
			"AND filled_quantity > 0 AND observed_at IS NOT NULL",
		accountID, credentialUpdatedAt, instrumentID,
	).Order("created_at, id").Find(&orders).Error; err != nil {
		return position, err
	}
	for _, order := range orders {
		if _, err := applyTestnetProjectedFill(&position, order); err != nil {
			return position, err
		}
	}
	return position, nil
}

func (executor *TestnetExecutor) prepareEmergencyFlatten(
	ctx context.Context,
	source testnetExecutionAction,
	reason string,
) (*testnetExecutionAction, error) {
	var action *testnetExecutionAction
	err := executor.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockTestnetAccountExecution(tx, source.Intent.AccountID); err != nil {
			return err
		}
		intent, err := executor.lockClaimedIntent(tx, source.Intent.ID)
		if err != nil {
			return err
		}
		var order db.TestnetOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", source.Order.ID).
			Take(&order).Error; err != nil {
			return err
		}
		failureCode := protectionFailureCode(reason)
		if err := tx.Model(&order).Updates(map[string]any{
			"status": "unknown", "last_error_code": failureCode, "updated_at": time.Now().UTC(),
		}).Error; err != nil {
			return err
		}
		state, err := loadTestnetProtectionState(tx, intent)
		if err != nil {
			return err
		}
		action, err = executor.prepareEmergencyFlattenWithState(tx, intent, state, failureCode)
		return err
	})
	return action, err
}

func (executor *TestnetExecutor) scheduleProtectionFailure(
	tx *gorm.DB,
	intent db.TradingIntent,
	reason string,
) (*testnetExecutionAction, error) {
	state, err := loadTestnetProtectionState(tx, intent)
	if err != nil {
		return nil, err
	}
	return executor.prepareEmergencyFlattenWithState(tx, intent, state, reason)
}

func (executor *TestnetExecutor) prepareEmergencyFlattenWithState(
	tx *gorm.DB,
	intent db.TradingIntent,
	state testnetProtectionState,
	failureCode string,
) (*testnetExecutionAction, error) {
	failureCode = protectionFailureCode(failureCode)
	var existing db.TestnetOrder
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
		"intent_id = ? AND purpose = 'flatten'", intent.ID,
	).Take(&existing).Error
	if err == nil {
		return prepareExistingTestnetOrderAction(tx, intent, existing, true, false)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := pauseTestnetAccount(tx, state.Account.ID, failureCode, now); err != nil {
		return nil, err
	}
	if err := tx.Model(&db.StrategyInstance{}).Where(
		"id = ? AND owner_user_id = ?", intent.StrategyInstanceID, intent.OwnerUserID,
	).Updates(map[string]any{"is_enabled": false, "updated_at": now}).Error; err != nil {
		return nil, err
	}
	if err := createTestnetProtectionNotification(tx, state, failureCode, now); err != nil {
		return nil, err
	}
	state.Account.Status = "paused"
	state.Account.PauseReason = failureCode
	state.Account.AutomationEnabled = false
	state.Account.UpdatedAt = now
	if state.Position.Quantity.IsZero() {
		return nil, finishTestnetIntent(tx, intent, "failed", failureCode+"_position_flat")
	}

	orderID, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	order := db.TestnetOrder{
		ID: orderID, AccountID: intent.AccountID, IntentID: intent.ID,
		StrategyInstanceID: intent.StrategyInstanceID, InstrumentID: intent.InstrumentID,
		CredentialUpdatedAt:       state.Credential.UpdatedAt,
		SubmittedAccountUpdatedAt: state.Account.UpdatedAt,
		ClientOrderID:             testnetChildClientOrderID("f", orderID),
		Side:                      oppositeTestnetSide(state.Position.Quantity), Quantity: state.Position.Quantity.Abs(),
		Purpose: "flatten", OrderType: "market",
		ReduceOnly: state.Account.Market == string(marketdata.MarketTypeUSDM),
		Status:     "prepared", SubmitAttemptCount: 1, SubmittedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := tx.Create(&order).Error; err != nil {
		return nil, err
	}
	if err := tx.Model(&db.TradingIntent{}).Where(
		"id = ? AND status = 'processing' AND worker_id = ?", intent.ID, executor.workerID,
	).Update("block_reason", failureCode).Error; err != nil {
		return nil, err
	}
	return &testnetExecutionAction{
		Intent: intent, Order: order, Account: state.Account, Instrument: state.Instrument,
		Credential: state.Credential, ExpectedAccountTime: state.Account.UpdatedAt,
	}, nil
}

func (executor *TestnetExecutor) prepareMissingFlattenOrder(
	tx *gorm.DB,
	intent db.TradingIntent,
	order db.TestnetOrder,
) (*testnetExecutionAction, error) {
	state, err := loadTestnetProtectionState(tx, intent)
	if err != nil {
		return nil, err
	}
	if state.Position.Quantity.IsZero() {
		if err := markUnsubmittedTestnetOrder(tx, &order, "position_already_flat"); err != nil {
			return nil, err
		}
		return nil, finishTestnetIntent(tx, intent, "failed", "protection_failed_flattened")
	}
	now := time.Now().UTC()
	updates := map[string]any{
		"credential_updated_at":        state.Credential.UpdatedAt,
		"submitted_account_updated_at": state.Account.UpdatedAt,
		"side":                         oppositeTestnetSide(state.Position.Quantity), "quantity": state.Position.Quantity.Abs(),
		"reduce_only":     state.Account.Market == string(marketdata.MarketTypeUSDM),
		"filled_quantity": decimal.Zero, "cumulative_quote_quantity": decimal.Zero,
		"average_price": decimal.Zero, "status": "prepared", "last_error_code": "",
		"submit_attempt_count": gorm.Expr("submit_attempt_count + 1"),
		"submitted_at":         now, "observed_at": nil, "updated_at": now,
	}
	if err := tx.Model(&order).Updates(updates).Error; err != nil {
		return nil, err
	}
	order.CredentialUpdatedAt = state.Credential.UpdatedAt
	order.SubmittedAccountUpdatedAt = state.Account.UpdatedAt
	order.Side = oppositeTestnetSide(state.Position.Quantity)
	order.Quantity = state.Position.Quantity.Abs()
	order.ReduceOnly = state.Account.Market == string(marketdata.MarketTypeUSDM)
	order.Status = "prepared"
	order.LastErrorCode = ""
	order.SubmitAttemptCount++
	order.SubmittedAt = now
	order.ObservedAt = nil
	return &testnetExecutionAction{
		Intent: intent, Order: order, Account: state.Account, Instrument: state.Instrument,
		Credential: state.Credential, ExpectedAccountTime: state.Account.UpdatedAt,
	}, nil
}

func createTestnetProtectionNotification(
	tx *gorm.DB,
	state testnetProtectionState,
	failureCode string,
	now time.Time,
) error {
	ownerID := state.Account.OwnerUserID
	delivery := db.SystemNotifyDelivery{
		TargetType: "testnet_safety", RecipientUserID: &ownerID,
		ChannelType: "in_app", Status: "success",
		Title: "Critical: Testnet protection failed",
		Content: fmt.Sprintf(
			"Account %s paused and emergency flattening started for %s (%s).",
			state.Account.Name, state.Instrument.NativeSymbol, failureCode,
		),
		SentAt: &now, CreatedAt: now,
	}
	return tx.Create(&delivery).Error
}

func protectiveStopPrice(quantity, entryPrice, ratio, tick decimal.Decimal) decimal.Decimal {
	raw := entryPrice.Mul(decimal.NewFromInt(1).Sub(ratio))
	if quantity.IsNegative() {
		raw = entryPrice.Mul(decimal.NewFromInt(1).Add(ratio))
		if tick.IsPositive() {
			return raw.Div(tick).Ceil().Mul(tick)
		}
		return raw
	}
	return quantizeTowardZero(raw, tick)
}

func validTestnetStopLossRatio(ratio *decimal.Decimal) bool {
	return ratio != nil && ratio.IsPositive() && ratio.LessThan(decimal.NewFromInt(1))
}

func oppositeTestnetSide(quantity decimal.Decimal) string {
	if quantity.IsNegative() {
		return "buy"
	}
	return "sell"
}

func testnetChildClientOrderID(prefix string, id uuid.UUID) string {
	return "cs" + prefix + strings.ReplaceAll(id.String(), "-", "")[:29]
}

func protectionFailureCode(reason string) string {
	reason = strings.TrimSpace(strings.ToLower(reason))
	if strings.HasPrefix(reason, "protection_") {
		return reason
	}
	if reason == "" {
		reason = "unknown"
	}
	return "protection_" + reason
}
