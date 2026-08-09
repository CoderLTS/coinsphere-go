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
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const defaultTestnetReconciliationPollInterval = 30 * time.Second

type testnetOrderObservation struct {
	OrderID              uuid.UUID
	ExpectedOrderUpdated time.Time
	Result               exchangebinance.OrderResult
	Queried              bool
}

// testnetOrderQueryError 保留本地订单身份，同时让上层只持久化脱敏错误分类。
type testnetOrderQueryError struct {
	OrderID uuid.UUID
	Err     error
}

func (err *testnetOrderQueryError) Error() string { return "testnet order query failed" }

func (err *testnetOrderQueryError) Unwrap() error { return err.Err }

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
	cutoff := time.Now().UTC().Add(-reconciler.pollInterval)
	var credential db.TradingAccountCredential
	err := reconciler.database.WithContext(ctx).Model(&db.TradingAccountCredential{}).
		Select("trading_account_credentials.*").
		Joins("JOIN trading_accounts ON trading_accounts.id = trading_account_credentials.account_id").
		Joins("LEFT JOIN testnet_reconciliations ON testnet_reconciliations.account_id = trading_account_credentials.account_id").
		Where("trading_accounts.environment = 'testnet' AND trading_account_credentials.status = 'configured' AND trading_account_credentials.verification_status = 'verified'").
		Where(`testnet_reconciliations.account_id IS NULL
            OR testnet_reconciliations.credential_updated_at <> trading_account_credentials.updated_at
            OR testnet_reconciliations.status <> 'matched'
            OR (trading_accounts.status = 'active'
                AND (testnet_reconciliations.last_attempted_at IS NULL OR testnet_reconciliations.last_attempted_at <= ?))`, cutoff).
		Order("testnet_reconciliations.last_attempted_at NULLS FIRST, trading_account_credentials.updated_at, trading_account_credentials.id").
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
	account, continuous, current, err := reconciler.preparePendingAccount(ctx, credential, account)
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

	var status, errorCode string
	var observations []testnetOrderObservation
	if continuous {
		status, errorCode, observations, err = reconciler.inspectContinuousSnapshot(
			ctx, credential, account, snapshot, apiKey, apiSecret,
		)
	} else {
		status, errorCode, err = reconciler.classifySnapshot(ctx, account, snapshot)
	}
	if err != nil {
		var queryErr *testnetOrderQueryError
		if errors.As(err, &queryErr) {
			if ctx.Err() != nil {
				return true, 0, ctx.Err()
			}
			failureCode, retryAfter, _ := testnetReconciliationFailure(queryErr.Err)
			var privateErr *exchangebinance.PrivateError
			if errors.As(queryErr.Err, &privateErr) && privateErr.Kind == exchangebinance.PrivateErrorNotFound {
				failureCode = "managed_order_not_found"
				retryAfter = 0
			}
			_, _, invalidCredential := testnetReconciliationFailure(queryErr.Err)
			if invalidCredential {
				return true, 0, reconciler.invalidateCredential(ctx, credential, account, failureCode)
			}
			if persistErr := reconciler.persistUnknownOrder(ctx, credential, account, failureCode, queryErr.OrderID); persistErr != nil {
				return true, retryAfter, persistErr
			}
			return true, maxDuration(reconciler.pollInterval, retryAfter), nil
		}
		return true, 0, err
	}
	persisted, err := reconciler.persistSnapshotForMode(ctx, credential, account, snapshot, status, errorCode, continuous, observations)
	if err != nil {
		return true, 0, err
	}
	if !persisted {
		return true, reconciler.pollInterval, nil
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

func (reconciler *TestnetAccountReconciler) inspectContinuousSnapshot(
	ctx context.Context,
	credential db.TradingAccountCredential,
	account db.TradingAccount,
	snapshot exchangebinance.AccountSnapshot,
	apiKey, apiSecret string,
) (string, string, []testnetOrderObservation, error) {
	if !snapshot.CanTrade {
		return "mismatch", "trading_disabled", nil, nil
	}
	var instruments []db.MarketInstrument
	if err := reconciler.database.WithContext(ctx).Model(&db.MarketInstrument{}).
		Joins("JOIN trading_account_instruments ON trading_account_instruments.instrument_id = market_instruments.id").
		Where("trading_account_instruments.account_id = ?", account.ID).
		Find(&instruments).Error; err != nil {
		return "", "", nil, err
	}
	instrumentsByID := make(map[uuid.UUID]db.MarketInstrument, len(instruments))
	instrumentsBySymbol := make(map[string]db.MarketInstrument, len(instruments))
	for _, instrument := range instruments {
		instrumentsByID[instrument.ID] = instrument
		instrumentsBySymbol[instrument.NativeSymbol] = instrument
	}

	var orders []db.TestnetOrder
	if err := reconciler.database.WithContext(ctx).Where(
		"account_id = ? AND credential_updated_at = ?", account.ID, credential.UpdatedAt,
	).Order("created_at, id").Find(&orders).Error; err != nil {
		return "", "", nil, err
	}
	ordersByClientID := make(map[string]db.TestnetOrder, len(orders))
	activeOrders := make(map[string]db.TestnetOrder)
	for _, order := range orders {
		ordersByClientID[order.ClientOrderID] = order
		if activeTestnetOrderStatus(order.Status) {
			activeOrders[order.ClientOrderID] = order
		}
	}

	observations := make([]testnetOrderObservation, 0, len(activeOrders))
	seenOpenOrders := make(map[string]struct{}, len(snapshot.OpenOrders))
	for _, openOrder := range snapshot.OpenOrders {
		order, exists := ordersByClientID[openOrder.ClientOrderID]
		if !exists {
			return "mismatch", "unknown_external_order", nil, nil
		}
		if !activeTestnetOrderStatus(order.Status) {
			return "mismatch", "managed_order_state_mismatch", nil, nil
		}
		instrument, exists := instrumentsByID[order.InstrumentID]
		if !exists {
			return "mismatch", "managed_order_instrument_mismatch", nil, nil
		}
		result := testnetOpenOrderResult(snapshot.ObservedAt, openOrder)
		if !validContinuousOrderObservation(order, instrument, result) {
			return "mismatch", "managed_order_shape_mismatch", nil, nil
		}
		observations = append(observations, testnetOrderObservation{
			OrderID: order.ID, ExpectedOrderUpdated: order.UpdatedAt, Result: result,
		})
		seenOpenOrders[order.ClientOrderID] = struct{}{}
	}

	for clientOrderID, order := range activeOrders {
		if _, seen := seenOpenOrders[clientOrderID]; seen {
			continue
		}
		if order.Status == "prepared" {
			continue
		}
		// 快照可能早于本地未知态；较新的本地状态交给下一轮或 Executor 查询。
		if order.ObservedAt == nil && order.UpdatedAt.After(snapshot.ObservedAt) {
			continue
		}
		instrument, exists := instrumentsByID[order.InstrumentID]
		if !exists {
			return "mismatch", "managed_order_instrument_mismatch", nil, nil
		}
		result, err := reconciler.client.QueryOrder(
			ctx, marketdata.MarketType(account.Market),
			apiKey, apiSecret,
			instrument.NativeSymbol, order.ClientOrderID,
		)
		if err != nil {
			return "", "", nil, &testnetOrderQueryError{OrderID: order.ID, Err: err}
		}
		if !validContinuousOrderObservation(order, instrument, result) {
			return "mismatch", "managed_order_shape_mismatch", nil, nil
		}
		observations = append(observations, testnetOrderObservation{
			OrderID: order.ID, ExpectedOrderUpdated: order.UpdatedAt, Result: result, Queried: true,
		})
	}

	observedOrders := overlayTestnetOrderObservations(orders, observations)
	expectedPositions, err := expectedTestnetPositions(observedOrders)
	if err != nil {
		return "mismatch", "managed_position_projection_invalid", nil, nil
	}
	if errorCode := continuousSnapshotDifference(account, instrumentsByID, instrumentsBySymbol, expectedPositions, snapshot); errorCode != "" {
		return "mismatch", errorCode, nil, nil
	}
	return "matched", "", observations, nil
}

func activeTestnetOrderStatus(status string) bool {
	return status == "prepared" || status == "unknown" || status == "new" || status == "partially_filled"
}

func testnetOpenOrderResult(observedAt time.Time, order exchangebinance.OpenOrder) exchangebinance.OrderResult {
	return exchangebinance.OrderResult{
		Symbol: order.Symbol, ExchangeOrderID: order.ExchangeOrderID, ClientOrderID: order.ClientOrderID,
		Side: order.Side, OrderType: order.OrderType, Status: order.Status,
		OriginalQuantity: order.OriginalQuantity, ExecutedQuantity: order.ExecutedQuantity,
		CumulativeQuoteQuantity: order.CumulativeQuoteQuantity, AveragePrice: order.AveragePrice,
		StopPrice: order.StopPrice, ClosePosition: order.ClosePosition,
		ReduceOnly: order.ReduceOnly, WorkingType: order.WorkingType, ObservedAt: observedAt.UTC(),
	}
}

func validContinuousOrderObservation(
	order db.TestnetOrder,
	instrument db.MarketInstrument,
	result exchangebinance.OrderResult,
) bool {
	status, err := normalizeTestnetOrderStatus(result.Status)
	return err == nil && result.Symbol == instrument.NativeSymbol &&
		result.ClientOrderID == order.ClientOrderID && result.Side == order.Side &&
		result.ExchangeOrderID > 0 && !result.ObservedAt.IsZero() &&
		(order.ExchangeOrderID == nil || result.ExchangeOrderID == *order.ExchangeOrderID) &&
		validTestnetStoredOrderResult(order, status, result)
}

func overlayTestnetOrderObservations(
	orders []db.TestnetOrder,
	observations []testnetOrderObservation,
) []db.TestnetOrder {
	byID := make(map[uuid.UUID]exchangebinance.OrderResult, len(observations))
	for _, observation := range observations {
		byID[observation.OrderID] = observation.Result
	}
	result := append([]db.TestnetOrder(nil), orders...)
	for index := range result {
		observed, exists := byID[result[index].ID]
		if !exists {
			continue
		}
		status, err := normalizeTestnetOrderStatus(observed.Status)
		if err != nil {
			continue
		}
		exchangeOrderID := observed.ExchangeOrderID
		observedAt := observed.ObservedAt.UTC()
		result[index].ExchangeOrderID = &exchangeOrderID
		result[index].FilledQuantity = observed.ExecutedQuantity
		result[index].CumulativeQuoteQuantity = observed.CumulativeQuoteQuantity
		result[index].AveragePrice = observed.AveragePrice
		result[index].Status = status
		result[index].ObservedAt = &observedAt
	}
	return result
}

func expectedTestnetPositions(orders []db.TestnetOrder) (map[uuid.UUID]*testnetManagedPosition, error) {
	positions := make(map[uuid.UUID]*testnetManagedPosition)
	for _, order := range orders {
		if !order.FilledQuantity.IsPositive() || order.ObservedAt == nil {
			continue
		}
		position := positions[order.InstrumentID]
		if position == nil {
			position = &testnetManagedPosition{InstrumentID: order.InstrumentID}
			positions[order.InstrumentID] = position
		}
		if _, err := applyTestnetProjectedFill(position, order); err != nil {
			return nil, err
		}
	}
	return positions, nil
}

func continuousSnapshotDifference(
	account db.TradingAccount,
	instrumentsByID map[uuid.UUID]db.MarketInstrument,
	instrumentsBySymbol map[string]db.MarketInstrument,
	expectedPositions map[uuid.UUID]*testnetManagedPosition,
	snapshot exchangebinance.AccountSnapshot,
) string {
	actualPositions := make(map[uuid.UUID]decimal.Decimal, len(snapshot.Positions))
	for _, position := range snapshot.Positions {
		if position.PositionSide != "both" {
			return "hedge_mode_enabled"
		}
		instrument, exists := instrumentsBySymbol[position.Symbol]
		if !exists {
			return "unknown_instrument"
		}
		expected := decimal.Zero
		if projected := expectedPositions[instrument.ID]; projected != nil {
			expected = projected.Quantity
		}
		if !position.Quantity.Equal(expected) {
			if expected.IsZero() {
				return "unowned_position"
			}
			return "position_quantity_mismatch"
		}
		actualPositions[instrument.ID] = position.Quantity
	}
	if account.Market == string(marketdata.MarketTypeUSDM) {
		for instrumentID, position := range expectedPositions {
			if position.Quantity.IsZero() {
				continue
			}
			if actual, exists := actualPositions[instrumentID]; !exists || !actual.Equal(position.Quantity) {
				return "position_missing"
			}
		}
	}

	quoteAssets := make(map[string]struct{}, len(instrumentsByID))
	expectedBaseBalances := make(map[string]decimal.Decimal)
	for instrumentID, instrument := range instrumentsByID {
		quoteAssets[instrument.QuoteAsset] = struct{}{}
		if account.Market != string(marketdata.MarketTypeSpot) {
			continue
		}
		position := expectedPositions[instrumentID]
		if position == nil || position.Quantity.IsZero() {
			continue
		}
		if position.Quantity.IsNegative() {
			return "spot_position_negative"
		}
		expectedBaseBalances[instrument.BaseAsset] = expectedBaseBalances[instrument.BaseAsset].Add(position.Quantity)
	}
	seenBaseBalances := make(map[string]struct{}, len(expectedBaseBalances))
	for _, balance := range snapshot.Balances {
		if _, allowed := quoteAssets[balance.Asset]; allowed {
			continue
		}
		if account.Market == string(marketdata.MarketTypeSpot) {
			if expected, exists := expectedBaseBalances[balance.Asset]; exists {
				if !balance.Total.Equal(expected) {
					return "spot_balance_quantity_mismatch"
				}
				seenBaseBalances[balance.Asset] = struct{}{}
				continue
			}
			return "spot_inventory_present"
		}
		return "unsupported_collateral"
	}
	for asset, expected := range expectedBaseBalances {
		if expected.IsPositive() {
			if _, exists := seenBaseBalances[asset]; !exists {
				return "spot_balance_missing"
			}
		}
	}
	return ""
}

func (reconciler *TestnetAccountReconciler) preparePendingAccount(
	ctx context.Context,
	credential db.TradingAccountCredential,
	account db.TradingAccount,
) (db.TradingAccount, bool, bool, error) {
	current := true
	continuous := false
	err := reconciler.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockTestnetAccountExecution(tx, credential.AccountID); err != nil {
			return err
		}
		lockedAccount, err := lockCurrentTestnetState(tx, credential, nil)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				current = false
				return nil
			}
			return err
		}
		account = lockedAccount
		var reconciliation db.TestnetReconciliation
		reconciliationErr := tx.Where("account_id = ?", account.ID).Take(&reconciliation).Error
		if reconciliationErr != nil && !errors.Is(reconciliationErr, gorm.ErrRecordNotFound) {
			return reconciliationErr
		}
		continuous = reconciliationErr == nil &&
			reconciliation.CredentialUpdatedAt.Equal(credential.UpdatedAt) &&
			reconciliation.Status == "matched" && account.Status == "active"
		if !continuous {
			if err := pauseTestnetAccount(tx, account.ID, "testnet_reconciliation_required", time.Now().UTC()); err != nil {
				return err
			}
		}
		return tx.Where("id = ?", account.ID).Take(&account).Error
	})
	return account, continuous, current, err
}

func (reconciler *TestnetAccountReconciler) persistSnapshot(
	ctx context.Context,
	credential db.TradingAccountCredential,
	account db.TradingAccount,
	snapshot exchangebinance.AccountSnapshot,
	status, errorCode string,
) (bool, error) {
	return reconciler.persistSnapshotForMode(ctx, credential, account, snapshot, status, errorCode, false, nil)
}

func (reconciler *TestnetAccountReconciler) persistSnapshotForMode(
	ctx context.Context,
	credential db.TradingAccountCredential,
	account db.TradingAccount,
	snapshot exchangebinance.AccountSnapshot,
	status, errorCode string,
	continuous bool,
	observations []testnetOrderObservation,
) (bool, error) {
	persisted := true
	err := reconciler.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockTestnetAccountExecution(tx, account.ID); err != nil {
			return err
		}
		if _, err := lockCurrentTestnetState(tx, credential, &account.UpdatedAt); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				persisted = false
				return nil
			}
			return err
		}
		observedAt := snapshot.ObservedAt.UTC()
		if observedAt.IsZero() {
			return &exchangebinance.PrivateError{Kind: exchangebinance.PrivateErrorProtocol}
		}
		var previous db.TestnetReconciliation
		previousErr := tx.Where("account_id = ?", account.ID).Take(&previous).Error
		if previousErr != nil && !errors.Is(previousErr, gorm.ErrRecordNotFound) {
			return previousErr
		}
		if previousErr == nil && !previous.CredentialUpdatedAt.Equal(credential.UpdatedAt) {
			if err := clearTestnetReconciliation(tx, account.ID); err != nil {
				return err
			}
			previousErr = gorm.ErrRecordNotFound
		} else if previousErr == nil && previous.LastObservedAt != nil &&
			!observedAt.After(previous.LastObservedAt.UTC()) {
			now := time.Now().UTC()
			if err := tx.Model(&previous).Updates(map[string]any{
				"last_attempted_at": now, "updated_at": now,
			}).Error; err != nil {
				return err
			}
			persisted = false
			return nil
		}
		staleOrders, err := applyTestnetOrderObservations(tx, credential, observations)
		if err != nil {
			return err
		}
		if staleOrders {
			persisted = false
			return nil
		}
		if err := clearTestnetProjection(tx, account.ID); err != nil {
			return err
		}
		now := time.Now().UTC()
		reconciliation := db.TestnetReconciliation{
			AccountID: account.ID, CredentialUpdatedAt: credential.UpdatedAt,
			Status: status, ErrorCode: errorCode,
			BalanceCount: len(snapshot.Balances), PositionCount: len(snapshot.Positions), OpenOrderCount: len(snapshot.OpenOrders),
			LastAttemptedAt: now, LastObservedAt: &observedAt, UpdatedAt: now,
		}
		if previousErr == nil {
			if err := tx.Model(&previous).Updates(map[string]any{
				"credential_updated_at": credential.UpdatedAt, "status": status, "error_code": errorCode,
				"balance_count": len(snapshot.Balances), "position_count": len(snapshot.Positions),
				"open_order_count": len(snapshot.OpenOrders), "last_attempted_at": now,
				"last_observed_at": observedAt, "updated_at": now,
			}).Error; err != nil {
				return err
			}
		} else if err := tx.Create(&reconciliation).Error; err != nil {
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
				ExecutedQuantity: order.ExecutedQuantity, StopPrice: order.StopPrice,
				ClosePosition: order.ClosePosition, ReduceOnly: order.ReduceOnly, WorkingType: order.WorkingType,
				ObservedAt: observedAt,
			})
		}
		if len(orders) > 0 {
			if err := tx.Create(&orders).Error; err != nil {
				return err
			}
		}
		if status == "matched" {
			if err := persistTestnetRiskSnapshot(tx, account, credential, snapshot, observedAt); err != nil {
				return err
			}
		}
		if status == "matched" {
			if !continuous || account.Status != "active" {
				return pauseTestnetAccount(tx, account.ID, "testnet_reconciled_manual_release_required", now)
			}
			return nil
		}
		return pauseTestnetAccount(tx, account.ID, "testnet_reconciliation_mismatch", now)
	})
	return persisted, err
}

func applyTestnetOrderObservations(
	tx *gorm.DB,
	credential db.TradingAccountCredential,
	observations []testnetOrderObservation,
) (bool, error) {
	seen := make(map[uuid.UUID]struct{}, len(observations))
	for _, observation := range observations {
		if _, exists := seen[observation.OrderID]; exists {
			return false, &exchangebinance.PrivateError{Kind: exchangebinance.PrivateErrorProtocol}
		}
		seen[observation.OrderID] = struct{}{}
		var order db.TestnetOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
			"id = ? AND account_id = ? AND credential_updated_at = ?",
			observation.OrderID, credential.AccountID, credential.UpdatedAt,
		).Take(&order).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return true, nil
			}
			return false, err
		}
		if !observation.ExpectedOrderUpdated.IsZero() &&
			!order.UpdatedAt.Equal(observation.ExpectedOrderUpdated) {
			return true, nil
		}
		status, err := normalizeTestnetOrderStatus(observation.Result.Status)
		if err != nil {
			return false, &exchangebinance.PrivateError{Kind: exchangebinance.PrivateErrorProtocol}
		}
		if !activeTestnetOrderStatus(order.Status) {
			return true, nil
		}
		var instrument db.MarketInstrument
		if err := tx.Where("id = ?", order.InstrumentID).Take(&instrument).Error; err != nil {
			return false, err
		}
		if !validContinuousOrderObservation(order, instrument, observation.Result) {
			return false, &exchangebinance.PrivateError{Kind: exchangebinance.PrivateErrorProtocol}
		}
		if order.ObservedAt != nil && !observation.Result.ObservedAt.UTC().After(order.ObservedAt.UTC()) {
			return true, nil
		}
		lastErrorCode := ""
		if status == "rejected" {
			lastErrorCode = "exchange_rejected"
		}
		now := time.Now().UTC()
		updates := map[string]any{
			"exchange_order_id":         observation.Result.ExchangeOrderID,
			"filled_quantity":           observation.Result.ExecutedQuantity,
			"cumulative_quote_quantity": observation.Result.CumulativeQuoteQuantity,
			"average_price":             observation.Result.AveragePrice,
			"status":                    status, "last_error_code": lastErrorCode,
			"observed_at": observation.Result.ObservedAt.UTC(), "updated_at": now,
		}
		if observation.Queried {
			updates["query_attempt_count"] = gorm.Expr("query_attempt_count + 1")
			updates["last_queried_at"] = now
		}
		if err := tx.Model(&order).Updates(updates).Error; err != nil {
			return false, err
		}
		if err := markTestnetIntentReconciled(tx, order.IntentID, "authoritative_order_"+status); err != nil {
			return false, err
		}
	}
	return false, nil
}

func markTestnetIntentReconciled(tx *gorm.DB, intentID uuid.UUID, reason string) error {
	now := time.Now().UTC()
	return tx.Model(&db.TradingIntent{}).Where(
		"id = ? AND environment = 'testnet' AND status IN ('processing', 'reconciling')", intentID,
	).Updates(map[string]any{
		"status": "reconciling", "block_reason": reason,
		"claimed_at": nil, "worker_id": nil, "updated_at": now,
	}).Error
}

func persistTestnetRiskSnapshot(
	tx *gorm.DB,
	account db.TradingAccount,
	credential db.TradingAccountCredential,
	snapshot exchangebinance.AccountSnapshot,
	observedAt time.Time,
) error {
	equity, ok := testnetSnapshotEquity(account, snapshot)
	if !ok {
		return nil
	}
	var risk db.TestnetRiskState
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
		"account_id = ? AND credential_updated_at = ?", account.ID, credential.UpdatedAt,
	).Take(&risk).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		risk = db.TestnetRiskState{
			AccountID: account.ID, CredentialUpdatedAt: credential.UpdatedAt,
			BaselineEquity: equity, Equity: equity, PeakEquity: equity,
			DayStartDate: utcDay(observedAt), DayStartEquity: equity, UpdatedAt: observedAt,
		}
		return tx.Create(&risk).Error
	}
	if err != nil {
		return err
	}
	if risk.UpdatedAt.After(observedAt) {
		return nil
	}
	peak := risk.PeakEquity
	if equity.GreaterThan(peak) {
		peak = equity
	}
	dayStartDate := risk.DayStartDate
	dayStartEquity := risk.DayStartEquity
	if utcDay(observedAt).After(utcDay(dayStartDate)) {
		dayStartDate = utcDay(observedAt)
		dayStartEquity = equity
	}
	return tx.Model(&risk).Updates(map[string]any{
		"equity": equity, "peak_equity": peak, "day_start_date": dayStartDate,
		"day_start_equity": dayStartEquity, "updated_at": observedAt,
	}).Error
}

func testnetSnapshotEquity(account db.TradingAccount, snapshot exchangebinance.AccountSnapshot) (decimal.Decimal, bool) {
	equity := decimal.Zero
	for _, balance := range snapshot.Balances {
		if balance.Asset == "USDT" {
			equity = equity.Add(balance.Total)
		}
		if account.Market == string(marketdata.MarketTypeSpot) && balance.Asset != "USDT" && !balance.Total.IsZero() {
			return decimal.Zero, false
		}
	}
	if account.Market == string(marketdata.MarketTypeUSDM) {
		for _, position := range snapshot.Positions {
			equity = equity.Add(position.UnrealizedPnL)
		}
	}
	return equity, true
}

func (reconciler *TestnetAccountReconciler) persistUnknown(
	ctx context.Context,
	credential db.TradingAccountCredential,
	account db.TradingAccount,
	errorCode string,
) error {
	return reconciler.persistUnknownOrder(ctx, credential, account, errorCode, uuid.Nil)
}

func (reconciler *TestnetAccountReconciler) persistUnknownOrder(
	ctx context.Context,
	credential db.TradingAccountCredential,
	account db.TradingAccount,
	errorCode string,
	orderID uuid.UUID,
) error {
	return reconciler.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockTestnetAccountExecution(tx, account.ID); err != nil {
			return err
		}
		if _, err := lockCurrentTestnetState(tx, credential, &account.UpdatedAt); err != nil {
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
		if orderID != uuid.Nil {
			if err := tx.Model(&db.TestnetOrder{}).Where(
				"id = ? AND account_id = ? AND credential_updated_at = ? AND status IN ('prepared', 'new', 'partially_filled', 'unknown')",
				orderID, account.ID, credential.UpdatedAt,
			).Updates(map[string]any{
				"status": "unknown", "last_error_code": errorCode, "updated_at": now,
			}).Error; err != nil {
				return err
			}
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
		if err := lockTestnetAccountExecution(tx, account.ID); err != nil {
			return err
		}
		if _, err := lockCurrentTestnetState(tx, credential, &account.UpdatedAt); err != nil {
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
	for _, model := range []any{
		&db.TestnetRiskState{}, &db.TestnetOpenOrder{}, &db.TestnetPosition{},
		&db.TestnetBalance{}, &db.TestnetReconciliation{},
	} {
		if err := tx.Where("account_id = ?", accountID).Delete(model).Error; err != nil {
			return err
		}
	}
	return nil
}

func clearTestnetProjection(tx *gorm.DB, accountID uuid.UUID) error {
	for _, model := range []any{&db.TestnetOpenOrder{}, &db.TestnetPosition{}, &db.TestnetBalance{}} {
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
	case exchangebinance.PrivateErrorNotFound:
		return "managed_order_not_found", 0, false
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
