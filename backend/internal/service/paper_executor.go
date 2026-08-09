package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/marketdata"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const defaultPaperExecutorPollInterval = time.Second

// PaperExecutor 串行消费数据库意图；Paper 阶段不包含任何交易所私有协议或凭据。
type PaperExecutor struct {
	database     *gorm.DB
	workerID     string
	pollInterval time.Duration
}

func NewPaperExecutor(database *gorm.DB, workerID string, pollInterval time.Duration) (*PaperExecutor, error) {
	if database == nil {
		return nil, errors.New("paper executor database is required")
	}
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return nil, errors.New("paper executor worker ID is required")
	}
	if pollInterval <= 0 {
		pollInterval = defaultPaperExecutorPollInterval
	}
	return &PaperExecutor{database: database, workerID: workerID, pollInterval: pollInterval}, nil
}

func (executor *PaperExecutor) Run(ctx context.Context) error {
	if err := executor.Recover(ctx); err != nil {
		return fmt.Errorf("recover paper executor: %w", err)
	}
	if err := executor.RebuildAllProjections(ctx); err != nil {
		return fmt.Errorf("rebuild paper projections: %w", err)
	}
	slog.InfoContext(ctx, "paper executor started")
	defer slog.Info("paper executor stopped")
	for {
		processed, err := executor.ProcessNext(ctx)
		if err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "paper executor operation failed", "operation", "process", "error_category", "database")
		}
		if ctx.Err() != nil {
			return nil
		}
		if processed && err == nil {
			continue
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

// Recover 只回收 Paper 的本地 processing 意图；外部订单未知状态将在 Testnet 阶段单独对账。
func (executor *PaperExecutor) Recover(ctx context.Context) error {
	return executor.database.WithContext(ctx).Model(&db.TradingIntent{}).
		Where("environment = 'paper' AND status = 'processing'").Updates(map[string]any{
		"status": "pending", "claimed_at": nil, "worker_id": nil, "updated_at": time.Now().UTC(),
	}).Error
}

func (executor *PaperExecutor) ProcessNext(ctx context.Context) (bool, error) {
	intent, err := executor.claimNextIntent(ctx)
	if err != nil || intent == nil {
		return false, err
	}
	err = executor.processIntent(ctx, *intent)
	if err != nil && ctx.Err() == nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = executor.database.WithContext(cleanupCtx).Model(&db.TradingIntent{}).
			Where("id = ? AND status = 'processing' AND worker_id = ?", intent.ID, executor.workerID).
			Updates(map[string]any{
				"status": "pending", "claimed_at": nil, "worker_id": nil, "updated_at": time.Now().UTC(),
			}).Error
	}
	return true, err
}

func (executor *PaperExecutor) claimNextIntent(ctx context.Context) (*db.TradingIntent, error) {
	var intent db.TradingIntent
	err := executor.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("environment = 'paper' AND status = 'pending'").Order("created_at, id").Take(&intent).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		result := tx.Model(&intent).Where("environment = 'paper' AND status = 'pending'").Updates(map[string]any{
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

type paperExecutionState struct {
	Intent     db.TradingIntent
	Account    db.TradingAccount
	Instance   db.StrategyInstance
	Signal     db.StrategySignal
	Instrument db.MarketInstrument
	Control    db.TradingControl
	Position   db.PaperPosition
	Balance    db.PaperBalance
	Price      decimal.Decimal
	TargetQty  decimal.Decimal
	DeltaQty   decimal.Decimal
	ReduceOnly bool
}

func (executor *PaperExecutor) processIntent(ctx context.Context, claimed db.TradingIntent) error {
	return executor.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", claimed.AccountID.String()).Error; err != nil {
			return err
		}
		var intent db.TradingIntent
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
			"id = ? AND status = 'processing' AND worker_id = ?", claimed.ID, executor.workerID,
		).Take(&intent).Error; err != nil {
			return err
		}
		state, blockReason, pause, err := loadAndValidatePaperExecution(tx, intent)
		if err != nil {
			return err
		}
		if blockReason != "" {
			return blockPaperIntent(tx, intent, blockReason, pause)
		}
		if state.DeltaQty.IsZero() {
			return finishPaperIntent(tx, intent, "executed", "target_already_reached")
		}
		if err := executor.executePaperFill(tx, state); err != nil {
			return err
		}
		return finishPaperIntent(tx, intent, "executed", "")
	})
}

func loadAndValidatePaperExecution(tx *gorm.DB, intent db.TradingIntent) (paperExecutionState, string, bool, error) {
	state := paperExecutionState{Intent: intent}
	if intent.Environment != "paper" {
		return state, "environment_not_paper", false, nil
	}
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", intent.AccountID).Take(&state.Account).Error; err != nil {
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
	state.Control = control
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("account_id = ?", intent.AccountID).
		Take(&state.Balance).Error; err != nil {
		return state, "", false, err
	}
	err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
		"account_id = ? AND instrument_id = ?", intent.AccountID, intent.InstrumentID,
	).Take(&state.Position).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		state.Position = db.PaperPosition{AccountID: intent.AccountID, InstrumentID: intent.InstrumentID}
	} else if err != nil {
		return state, "", false, err
	}
	if state.Account.OwnerUserID != intent.OwnerUserID || state.Instance.OwnerUserID != intent.OwnerUserID ||
		state.Signal.OwnerUserID != intent.OwnerUserID || state.Account.Market != intent.Market ||
		state.Instrument.Market != intent.Market || state.Instance.TradingAccountID == nil ||
		*state.Instance.TradingAccountID != intent.AccountID || state.Instance.AllocationUSDT == nil {
		return state, "execution_binding_mismatch", true, nil
	}
	if state.Signal.StrategyInstanceID != intent.StrategyInstanceID || state.Signal.InstrumentID != intent.InstrumentID ||
		state.Signal.Target.Cmp(intent.Target) != 0 || state.Signal.Mode != intent.Mode || !state.Instance.IsEnabled {
		return state, "strategy_state_changed", false, nil
	}
	if (intent.Mode == "manual" && state.Signal.Status != "approved") ||
		(intent.Mode == "auto" && state.Signal.Status != "active") {
		return state, "signal_state_changed", false, nil
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
	var ticker struct {
		OccurredAt time.Time
		LastPrice  decimal.Decimal
	}
	if err := tx.Table("market_ticker_snapshots").Select("occurred_at", "last_price").Where(
		"venue = ? AND instrument_id = ?", marketdata.VenueBinance, intent.InstrumentID,
	).Take(&ticker).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return state, "quote_missing", true, nil
		}
		return state, "", false, err
	}
	state.Price = ticker.LastPrice
	state.TargetQty = state.Instance.AllocationUSDT.Mul(intent.Target).Div(state.Price)
	state.TargetQty = quantizeTowardZero(state.TargetQty, state.Instrument.QuantityStep)
	state.DeltaQty = state.TargetQty.Sub(state.Position.Quantity)
	state.ReduceOnly = isPaperReduction(state.Position.Quantity, state.TargetQty)
	if !state.Position.Quantity.IsZero() && (state.Position.OwnerStrategyInstanceID == nil ||
		*state.Position.OwnerStrategyInstanceID != intent.StrategyInstanceID) {
		return state, "position_owner_conflict", true, nil
	}
	quoteAge := time.Now().UTC().Sub(ticker.OccurredAt.UTC())
	if state.Account.MaxQuoteAgeSeconds == nil || quoteAge < 0 ||
		quoteAge > time.Duration(valueOrZero(state.Account.MaxQuoteAgeSeconds))*time.Second {
		return state, "quote_stale", true, nil
	}
	complete, err := tradingRiskComplete(tx, state.Account)
	if err != nil {
		return state, "", false, err
	}
	if (!complete || state.Account.Status != "active" || state.Control.EmergencyStopped) && !state.ReduceOnly {
		switch {
		case state.Control.EmergencyStopped:
			return state, "global_emergency_stop", false, nil
		case state.Account.Status != "active":
			return state, "account_paused", false, nil
		default:
			return state, "risk_configuration_incomplete", true, nil
		}
	}
	if intent.Mode == "auto" && (!state.Account.AutomationEnabled || state.Account.AutomationAuthorizedAt == nil) && !state.ReduceOnly {
		return state, "automation_not_authorized", true, nil
	}
	if state.DeltaQty.IsZero() {
		return state, "", false, nil
	}
	reason, err := validatePaperNotionalRisk(tx, state)
	if err != nil {
		return state, "", false, err
	}
	if reason != "" {
		return state, reason, true, nil
	}
	return state, "", false, nil
}

func validatePaperNotionalRisk(tx *gorm.DB, state paperExecutionState) (string, error) {
	orderNotional := state.DeltaQty.Abs().Mul(state.Price)
	targetNotional := state.TargetQty.Abs().Mul(state.Price)
	if !state.ReduceOnly {
		if state.Account.MaxOrderNotional == nil || orderNotional.GreaterThan(*state.Account.MaxOrderNotional) {
			return "order_notional_limit", nil
		}
		if state.Account.MaxSymbolNotional == nil || targetNotional.GreaterThan(*state.Account.MaxSymbolNotional) {
			return "symbol_notional_limit", nil
		}
		var positions []db.PaperPosition
		if err := tx.Where("account_id = ?", state.Account.ID).Find(&positions).Error; err != nil {
			return "", err
		}
		total := decimal.Zero
		for _, position := range positions {
			if position.InstrumentID == state.Instrument.ID {
				continue
			}
			total = total.Add(position.Quantity.Abs().Mul(position.LastPrice))
		}
		total = total.Add(targetNotional)
		if state.Account.MaxTotalNotional == nil || total.GreaterThan(*state.Account.MaxTotalNotional) {
			return "account_notional_limit", nil
		}
		if currentRiskBreached(state.Account, state.Balance) {
			if state.Account.MaxDailyLoss != nil && state.Balance.DayStartEquity.Sub(state.Balance.Equity).
				GreaterThanOrEqual(*state.Account.MaxDailyLoss) {
				return "daily_loss_limit", nil
			}
			return "drawdown_limit", nil
		}
		fee := orderNotional.Mul(*state.Account.PaperFeeRate)
		if state.Account.Market == string(marketdata.MarketTypeSpot) && state.DeltaQty.IsPositive() &&
			state.DeltaQty.Mul(state.Price).Add(fee).GreaterThan(state.Balance.CashBalance) {
			return "insufficient_balance", nil
		}
		if state.Account.Market == string(marketdata.MarketTypeUSDM) {
			if state.Account.Leverage == nil || total.Div(decimal.NewFromInt(int64(*state.Account.Leverage))).
				Add(fee).GreaterThan(state.Balance.Equity) {
				return "insufficient_margin", nil
			}
		}
	}
	if state.DeltaQty.Abs().LessThan(state.Instrument.MinQuantity) && !state.ReduceOnly {
		return "order_below_minimum", nil
	}
	if orderNotional.LessThan(state.Instrument.MinNotional) && !state.ReduceOnly {
		return "order_below_minimum", nil
	}
	return "", nil
}

func (executor *PaperExecutor) executePaperFill(tx *gorm.DB, state paperExecutionState) error {
	now := time.Now().UTC()
	side := "buy"
	if state.DeltaQty.IsNegative() {
		side = "sell"
	}
	quantity := state.DeltaQty.Abs()
	orderID := state.Intent.ID
	orderEvent := db.TradingEvent{
		AccountID: state.Account.ID, IntentID: &state.Intent.ID, OrderID: &orderID,
		InstrumentID: state.Intent.InstrumentID, EventType: "order", Side: &side,
		Quantity: &quantity, Price: &state.Price, OccurredAt: now,
		DedupeKey: state.Intent.ID.String() + ":order",
	}
	if err := executor.appendAndProjectEvent(tx, orderEvent); err != nil {
		return err
	}
	fillEvent := orderEvent
	fillEvent.ID = 0
	fillEvent.EventID = uuid.Nil
	fillEvent.EventType = "fill"
	fillEvent.DedupeKey = state.Intent.ID.String() + ":fill"
	if err := executor.appendAndProjectEvent(tx, fillEvent); err != nil {
		return err
	}
	fee := quantity.Mul(state.Price).Mul(*state.Account.PaperFeeRate)
	feeEvent := db.TradingEvent{
		AccountID: state.Account.ID, IntentID: &state.Intent.ID, OrderID: &orderID,
		InstrumentID: state.Intent.InstrumentID, EventType: "fee", Amount: &fee,
		OccurredAt: now, DedupeKey: state.Intent.ID.String() + ":fee",
	}
	return executor.appendAndProjectEvent(tx, feeEvent)
}

func (executor *PaperExecutor) appendAndProjectEvent(tx *gorm.DB, event db.TradingEvent) error {
	eventID, err := uuid.NewV7()
	if err != nil {
		return err
	}
	event.EventID = eventID
	event.CreatedAt = time.Now().UTC()
	if err := tx.Create(&event).Error; err != nil {
		return err
	}
	return applyPaperEvent(tx, event)
}

func applyPaperEvent(tx *gorm.DB, event db.TradingEvent) error {
	var account db.TradingAccount
	if err := tx.Where("id = ?", event.AccountID).Take(&account).Error; err != nil {
		return err
	}
	balance, err := ensurePaperBalance(tx, account, event.OccurredAt)
	if err != nil {
		return err
	}
	advancePaperDay(&balance, event.OccurredAt)
	switch event.EventType {
	case "order":
		if event.IntentID == nil || event.OrderID == nil || event.Side == nil || event.Quantity == nil || event.Price == nil {
			return errors.New("invalid order event")
		}
		var intent db.TradingIntent
		if err := tx.Where("id = ?", *event.IntentID).Take(&intent).Error; err != nil {
			return err
		}
		order := db.PaperOrder{
			ID: *event.OrderID, AccountID: event.AccountID, IntentID: *event.IntentID,
			InstrumentID: event.InstrumentID, ClientOrderID: intent.ClientOrderID,
			Side: *event.Side, Quantity: *event.Quantity, AveragePrice: *event.Price,
			Status: "accepted", CreatedAt: event.OccurredAt, UpdatedAt: event.OccurredAt,
		}
		if err := tx.Create(&order).Error; err != nil {
			return err
		}
	case "fill":
		if event.IntentID == nil || event.OrderID == nil || event.Side == nil || event.Quantity == nil || event.Price == nil {
			return errors.New("invalid fill event")
		}
		if err := applyPaperFillProjection(tx, account, &balance, event); err != nil {
			return err
		}
		if err := tx.Model(&db.PaperOrder{}).Where("id = ?", *event.OrderID).Updates(map[string]any{
			"filled_quantity": *event.Quantity, "average_price": *event.Price,
			"status": "filled", "updated_at": event.OccurredAt,
		}).Error; err != nil {
			return err
		}
	case "fee":
		if event.Amount == nil {
			return errors.New("invalid fee event")
		}
		balance.CashBalance = balance.CashBalance.Sub(*event.Amount)
		balance.Fees = balance.Fees.Add(*event.Amount)
	case "funding":
		if event.Amount == nil || event.Price == nil {
			return errors.New("invalid funding event")
		}
		balance.CashBalance = balance.CashBalance.Add(*event.Amount)
		balance.Funding = balance.Funding.Add(*event.Amount)
		var position db.PaperPosition
		if err := tx.Where("account_id = ? AND instrument_id = ?", event.AccountID, event.InstrumentID).
			Take(&position).Error; err == nil {
			position.LastPrice = *event.Price
			position.UnrealizedPnl = position.LastPrice.Sub(position.AverageEntryPrice).Mul(position.Quantity)
			position.UpdatedAt = event.OccurredAt
			if err := tx.Save(&position).Error; err != nil {
				return err
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	default:
		return errors.New("unsupported trading event")
	}
	if err := recomputePaperBalance(tx, account, &balance, event.OccurredAt); err != nil {
		return err
	}
	return tx.Save(&balance).Error
}

func applyPaperFillProjection(tx *gorm.DB, account db.TradingAccount, balance *db.PaperBalance, event db.TradingEvent) error {
	var intent db.TradingIntent
	if err := tx.Where("id = ?", *event.IntentID).Take(&intent).Error; err != nil {
		return err
	}
	var position db.PaperPosition
	err := tx.Where("account_id = ? AND instrument_id = ?", event.AccountID, event.InstrumentID).Take(&position).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		position = db.PaperPosition{AccountID: event.AccountID, InstrumentID: event.InstrumentID}
	} else if err != nil {
		return err
	}
	signedQuantity := *event.Quantity
	if *event.Side == "sell" {
		signedQuantity = signedQuantity.Neg()
	}
	oldQuantity := position.Quantity
	newQuantity := oldQuantity.Add(signedQuantity)
	realized := decimal.Zero
	sameDirection := oldQuantity.IsZero() || oldQuantity.Sign() == signedQuantity.Sign()
	if sameDirection {
		if !newQuantity.IsZero() {
			position.AverageEntryPrice = oldQuantity.Abs().Mul(position.AverageEntryPrice).
				Add(signedQuantity.Abs().Mul(*event.Price)).Div(newQuantity.Abs())
		}
	} else {
		closingQuantity := decimal.Min(oldQuantity.Abs(), signedQuantity.Abs())
		realized = event.Price.Sub(position.AverageEntryPrice).Mul(closingQuantity).
			Mul(decimal.NewFromInt(int64(oldQuantity.Sign())))
		if newQuantity.IsZero() {
			position.AverageEntryPrice = decimal.Zero
		} else if newQuantity.Sign() != oldQuantity.Sign() {
			position.AverageEntryPrice = *event.Price
		}
	}
	position.Quantity = newQuantity
	position.LastPrice = *event.Price
	position.RealizedPnl = position.RealizedPnl.Add(realized)
	position.UnrealizedPnl = position.LastPrice.Sub(position.AverageEntryPrice).Mul(position.Quantity)
	position.UpdatedAt = event.OccurredAt
	if position.Quantity.IsZero() {
		position.OwnerStrategyInstanceID = nil
	} else {
		ownerID := intent.StrategyInstanceID
		position.OwnerStrategyInstanceID = &ownerID
	}
	if account.Market == string(marketdata.MarketTypeSpot) {
		balance.CashBalance = balance.CashBalance.Sub(signedQuantity.Mul(*event.Price))
	} else {
		balance.CashBalance = balance.CashBalance.Add(realized)
	}
	return tx.Save(&position).Error
}

func ensurePaperBalance(tx *gorm.DB, account db.TradingAccount, occurredAt time.Time) (db.PaperBalance, error) {
	var balance db.PaperBalance
	err := tx.Where("account_id = ?", account.ID).Take(&balance).Error
	if err == nil {
		return balance, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) || account.InitialBalance == nil {
		return balance, err
	}
	balance = db.PaperBalance{
		AccountID: account.ID, CashBalance: *account.InitialBalance, Equity: *account.InitialBalance,
		PeakEquity: *account.InitialBalance, DayStartDate: utcDay(occurredAt),
		DayStartEquity: *account.InitialBalance, UpdatedAt: occurredAt,
	}
	return balance, tx.Create(&balance).Error
}

func advancePaperDay(balance *db.PaperBalance, occurredAt time.Time) {
	day := utcDay(occurredAt)
	if day.After(utcDay(balance.DayStartDate)) {
		balance.DayStartDate = day
		balance.DayStartEquity = balance.Equity
	}
}

func recomputePaperBalance(tx *gorm.DB, account db.TradingAccount, balance *db.PaperBalance, occurredAt time.Time) error {
	var positions []db.PaperPosition
	if err := tx.Where("account_id = ?", account.ID).Find(&positions).Error; err != nil {
		return err
	}
	unrealized := decimal.Zero
	realized := decimal.Zero
	positionValue := decimal.Zero
	for _, position := range positions {
		unrealized = unrealized.Add(position.UnrealizedPnl)
		realized = realized.Add(position.RealizedPnl)
		positionValue = positionValue.Add(position.Quantity.Mul(position.LastPrice))
	}
	balance.RealizedPnl = realized
	balance.UnrealizedPnl = unrealized
	if account.Market == string(marketdata.MarketTypeSpot) {
		balance.Equity = balance.CashBalance.Add(positionValue)
	} else {
		balance.Equity = balance.CashBalance.Add(unrealized)
	}
	if balance.Equity.GreaterThan(balance.PeakEquity) {
		balance.PeakEquity = balance.Equity
	}
	balance.UpdatedAt = occurredAt
	return nil
}

func blockPaperIntent(tx *gorm.DB, intent db.TradingIntent, reason string, pause bool) error {
	now := time.Now().UTC()
	if pause {
		if err := tx.Model(&db.TradingAccount{}).Where("id = ?", intent.AccountID).Updates(map[string]any{
			"status": "paused", "pause_reason": reason, "automation_enabled": false, "updated_at": now,
		}).Error; err != nil {
			return err
		}
		if err := disableAutoInstances(tx, &intent.AccountID, now); err != nil {
			return err
		}
		outbox := db.DomainEventOutbox{
			EventType: "trading.risk.paused", AggregateType: "trading_account", AggregateID: intent.AccountID.String(),
			PayloadJSON:  dumpJSON(M{"accountId": intent.AccountID.String(), "intentId": intent.ID.String(), "reason": reason}),
			MetadataJSON: "{}", Status: "pending", AvailableAt: now, CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Create(&outbox).Error; err != nil {
			return err
		}
	}
	return finishPaperIntent(tx, intent, "blocked", reason)
}

func finishPaperIntent(tx *gorm.DB, intent db.TradingIntent, status, reason string) error {
	now := time.Now().UTC()
	result := tx.Model(&db.TradingIntent{}).Where("id = ? AND status = 'processing'", intent.ID).Updates(map[string]any{
		"status": status, "block_reason": reason, "claimed_at": nil, "worker_id": nil,
		"completed_at": now, "updated_at": now,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("paper intent claim was lost")
	}
	return nil
}

func (executor *PaperExecutor) RebuildAllProjections(ctx context.Context) error {
	var accountIDs []uuid.UUID
	if err := executor.database.WithContext(ctx).Model(&db.TradingAccount{}).
		Where("environment = 'paper'").Order("id").Pluck("id", &accountIDs).Error; err != nil {
		return err
	}
	for _, accountID := range accountIDs {
		if err := executor.RebuildAccountProjections(ctx, accountID); err != nil {
			return err
		}
	}
	return nil
}

func (executor *PaperExecutor) RebuildAccountProjections(ctx context.Context, accountID uuid.UUID) error {
	return executor.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", accountID.String()).Error; err != nil {
			return err
		}
		var account db.TradingAccount
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).
			Where("id = ? AND environment = 'paper'", accountID).Take(&account).Error; err != nil {
			return err
		}
		if err := tx.Where("account_id = ?", accountID).Delete(&db.PaperOrder{}).Error; err != nil {
			return err
		}
		if err := tx.Where("account_id = ?", accountID).Delete(&db.PaperPosition{}).Error; err != nil {
			return err
		}
		if err := tx.Where("account_id = ?", accountID).Delete(&db.PaperBalance{}).Error; err != nil {
			return err
		}
		if account.InitialBalance == nil {
			return errors.New("paper account initial balance is missing")
		}
		balance := db.PaperBalance{
			AccountID: account.ID, CashBalance: *account.InitialBalance, Equity: *account.InitialBalance,
			PeakEquity: *account.InitialBalance, DayStartDate: utcDay(account.CreatedAt),
			DayStartEquity: *account.InitialBalance, UpdatedAt: account.CreatedAt,
		}
		if err := tx.Create(&balance).Error; err != nil {
			return err
		}
		var events []db.TradingEvent
		if err := tx.Where("account_id = ?", accountID).Order("id").Find(&events).Error; err != nil {
			return err
		}
		for _, event := range events {
			if err := applyPaperEvent(tx, event); err != nil {
				return err
			}
		}
		return nil
	})
}

func quantizeTowardZero(value, step decimal.Decimal) decimal.Decimal {
	if step.IsZero() {
		return value
	}
	return value.Div(step).Truncate(0).Mul(step)
}

func isPaperReduction(current, target decimal.Decimal) bool {
	if current.IsZero() || target.Sign() != 0 && target.Sign() != current.Sign() {
		return false
	}
	return target.Abs().LessThan(current.Abs())
}

func valueOrZero(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}
