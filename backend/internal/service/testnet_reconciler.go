package service

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
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
	Recovered            bool
}

// testnetOrderQueryError 保留本地订单身份，同时让上层只持久化脱敏错误分类。
type testnetOrderQueryError struct {
	OrderID uuid.UUID
	Err     error
}

func (err *testnetOrderQueryError) Error() string { return "testnet order query failed" }

func (err *testnetOrderQueryError) Unwrap() error { return err.Err }

// testnetTradeQueryError keeps the managed order identity while preserving the
// same redacted exchange error classification used by snapshot reconciliation.
type testnetTradeQueryError struct {
	OrderID uuid.UUID
	Err     error
}

func (err *testnetTradeQueryError) Error() string { return "testnet trade query failed" }

func (err *testnetTradeQueryError) Unwrap() error { return err.Err }

type testnetLedgerMismatchError struct{ Code string }

func (err *testnetLedgerMismatchError) Error() string { return "testnet trade ledger mismatch" }

type testnetLedgerStaleError struct{}

func (err *testnetLedgerStaleError) Error() string { return "testnet trade ledger state changed" }

type testnetManagedTrade struct {
	Order                db.TestnetOrder
	Instrument           db.MarketInstrument
	Trade                exchangebinance.AccountTrade
	ExpectedOrderUpdated time.Time
}

type testnetTradeFactBatch struct {
	Trades  []testnetManagedTrade
	Funding []exchangebinance.FundingIncome
}

// TestnetAccountReconciler bootstraps a read-only Testnet projection before manual account release.
type TestnetAccountReconciler struct {
	database     *gorm.DB
	cipher       *security.SecretCipher
	client       *exchangebinance.PrivateClient
	environment  string
	market       string
	pollInterval time.Duration
}

func NewTestnetAccountReconciler(
	database *gorm.DB,
	cipher *security.SecretCipher,
	client *exchangebinance.PrivateClient,
	pollInterval time.Duration,
) (*TestnetAccountReconciler, error) {
	return NewPrivateAccountReconciler(database, cipher, client, "testnet", "", pollInterval)
}

func NewPrivateAccountReconciler(
	database *gorm.DB,
	cipher *security.SecretCipher,
	client *exchangebinance.PrivateClient,
	environment, market string,
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
	if !validPrivateRuntimeScope(environment, market) {
		return nil, errors.New("private account reconciler scope is invalid")
	}
	if pollInterval <= 0 {
		pollInterval = defaultTestnetReconciliationPollInterval
	}
	return &TestnetAccountReconciler{
		database: database, cipher: cipher, client: client,
		environment: environment, market: market, pollInterval: pollInterval,
	}, nil
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
	query := reconciler.database.WithContext(ctx).Model(&db.TradingAccountCredential{}).
		Select("trading_account_credentials.*").
		Joins("JOIN trading_accounts ON trading_accounts.id = trading_account_credentials.account_id").
		Joins("LEFT JOIN testnet_reconciliations ON testnet_reconciliations.account_id = trading_account_credentials.account_id").
		Where("trading_accounts.environment = ? AND trading_account_credentials.status = 'configured' AND trading_account_credentials.verification_status = 'verified'", reconciler.scopeEnvironment())
	if reconciler.market != "" {
		query = query.Where("trading_accounts.market_type = ?", reconciler.market)
	}
	err := query.
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
		Where("id = ? AND owner_user_id = ? AND environment = ?", credential.AccountID, credential.OwnerUserID, reconciler.scopeEnvironment()).
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
	ledgerBatch := testnetTradeFactBatch{}
	if continuous && status == "matched" && !hasRecoveredTestnetOrderObservation(observations) {
		ledgerBatch, err = reconciler.collectTestnetTradeFacts(
			ctx, credential, account, apiKey, apiSecret, observations,
		)
		if err != nil {
			var tradeQueryErr *testnetTradeQueryError
			if errors.As(err, &tradeQueryErr) {
				if ctx.Err() != nil {
					return true, 0, ctx.Err()
				}
				failureCode, retryAfter, _ := testnetReconciliationFailure(tradeQueryErr.Err)
				_, _, invalidCredential := testnetReconciliationFailure(tradeQueryErr.Err)
				if invalidCredential {
					return true, 0, reconciler.invalidateCredential(ctx, credential, account, failureCode)
				}
				if persistErr := reconciler.persistUnknownOrder(ctx, credential, account, failureCode, tradeQueryErr.OrderID); persistErr != nil {
					return true, retryAfter, persistErr
				}
				return true, maxDuration(reconciler.pollInterval, retryAfter), nil
			}
			var ledgerMismatch *testnetLedgerMismatchError
			if errors.As(err, &ledgerMismatch) {
				persisted, persistErr := reconciler.persistSnapshotForModeWithLedger(
					ctx, credential, account, snapshot, "mismatch", ledgerMismatch.Code,
					continuous, managedTestnetOrderObservations(observations), testnetTradeFactBatch{},
				)
				if persistErr != nil {
					return true, 0, persistErr
				}
				if !persisted {
					return true, reconciler.pollInterval, nil
				}
				return true, reconciler.pollInterval, nil
			}
			return true, 0, err
		}
	}
	persisted, err := reconciler.persistSnapshotForModeWithLedger(ctx, credential, account, snapshot, status, errorCode, continuous, observations, ledgerBatch)
	if err != nil {
		var ledgerMismatch *testnetLedgerMismatchError
		if !errors.As(err, &ledgerMismatch) {
			return true, 0, err
		}
		persisted, err = reconciler.persistSnapshotForModeWithLedger(
			ctx, credential, account, snapshot, "mismatch", ledgerMismatch.Code,
			continuous, managedTestnetOrderObservations(observations), testnetTradeFactBatch{},
		)
		if err != nil {
			return true, 0, err
		}
		if !persisted {
			return true, reconciler.pollInterval, nil
		}
		return true, reconciler.pollInterval, nil
	}
	if !persisted {
		return true, reconciler.pollInterval, nil
	}
	if status == "matched" {
		return true, 0, nil
	}
	return true, reconciler.pollInterval, nil
}

func (reconciler *TestnetAccountReconciler) scopeEnvironment() string {
	if reconciler.environment == "" {
		return "testnet"
	}
	return reconciler.environment
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
	projectedOrders := append([]db.TestnetOrder(nil), orders...)
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
	recoveredClientIDs := make(map[string]struct{})
	for _, openOrder := range snapshot.OpenOrders {
		if _, duplicate := seenOpenOrders[openOrder.ClientOrderID]; duplicate {
			return "mismatch", "unknown_external_order", nil, nil
		}
		order, exists := ordersByClientID[openOrder.ClientOrderID]
		if !exists {
			if _, duplicate := recoveredClientIDs[openOrder.ClientOrderID]; duplicate {
				return "mismatch", "unknown_external_order", nil, nil
			}
			intent, instrument, recoverable, err := reconciler.findExternalOrderRecovery(
				ctx, credential, account, instrumentsBySymbol, snapshot.ObservedAt, openOrder,
			)
			if err != nil {
				return "", "", nil, err
			}
			if !recoverable {
				return "mismatch", "unknown_external_order", nil, nil
			}
			result := testnetOpenOrderResult(snapshot.ObservedAt, openOrder)
			projectedOrders = append(projectedOrders, recoveredTestnetOrderProjection(
				intent, instrument, credential, account, result,
			))
			observations = append(observations, testnetOrderObservation{
				OrderID: intent.ID, Result: result, Recovered: true,
			})
			recoveredClientIDs[openOrder.ClientOrderID] = struct{}{}
			seenOpenOrders[openOrder.ClientOrderID] = struct{}{}
			continue
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

	observedOrders := overlayTestnetOrderObservations(projectedOrders, observations)
	expectedPositions, err := expectedTestnetPositions(observedOrders)
	if err != nil {
		return "mismatch", "managed_position_projection_invalid", nil, nil
	}
	if errorCode := continuousSnapshotDifference(account, instrumentsByID, instrumentsBySymbol, expectedPositions, snapshot); errorCode != "" {
		return "mismatch", errorCode, nil, nil
	}
	return "matched", "", observations, nil
}

func (reconciler *TestnetAccountReconciler) findExternalOrderRecovery(
	ctx context.Context,
	credential db.TradingAccountCredential,
	account db.TradingAccount,
	instrumentsBySymbol map[string]db.MarketInstrument,
	observedAt time.Time,
	openOrder exchangebinance.OpenOrder,
) (db.TradingIntent, db.MarketInstrument, bool, error) {
	var intents []db.TradingIntent
	if err := reconciler.database.WithContext(ctx).Where(
		"account_id = ? AND environment = ? AND client_order_id = ?",
		account.ID, account.Environment, openOrder.ClientOrderID,
	).Limit(2).Find(&intents).Error; err != nil {
		return db.TradingIntent{}, db.MarketInstrument{}, false, err
	}
	if len(intents) != 1 {
		return db.TradingIntent{}, db.MarketInstrument{}, false, nil
	}
	intent := intents[0]
	instrument, exists := instrumentsBySymbol[openOrder.Symbol]
	if !exists || intent.InstrumentID != instrument.ID ||
		intent.OwnerUserID != credential.OwnerUserID || intent.Market != account.Market ||
		intent.ClientOrderID != tradingClientOrderID(intent.ID) ||
		(intent.Status != "pending" && intent.Status != "reconciling") || intent.CompletedAt != nil ||
		instrument.Venue != string(marketdata.VenueBinance) ||
		instrument.Market != account.Market || instrument.Status != "trading" ||
		instrument.QuoteAsset != "USDT" {
		return db.TradingIntent{}, db.MarketInstrument{}, false, nil
	}
	var existing int64
	if err := reconciler.database.WithContext(ctx).Model(&db.TestnetOrder{}).Where(
		"account_id = ? AND client_order_id = ?", account.ID, intent.ClientOrderID,
	).Count(&existing).Error; err != nil {
		return db.TradingIntent{}, db.MarketInstrument{}, false, err
	}
	if existing != 0 {
		return db.TradingIntent{}, db.MarketInstrument{}, false, nil
	}
	result := testnetOpenOrderResult(observedAt, openOrder)
	status, err := normalizeTestnetOrderStatus(result.Status)
	if err != nil || (status != "new" && status != "partially_filled") ||
		!validRecoveredExternalOrder(account, instrument, result) {
		return db.TradingIntent{}, db.MarketInstrument{}, false, nil
	}
	return intent, instrument, true, nil
}

func validRecoveredExternalOrder(
	account db.TradingAccount,
	instrument db.MarketInstrument,
	result exchangebinance.OrderResult,
) bool {
	status, err := normalizeTestnetOrderStatus(result.Status)
	return err == nil && result.OrderType == "market" &&
		(result.Side == "buy" || result.Side == "sell") &&
		result.StopPrice.IsZero() && !result.ClosePosition && result.WorkingType == "" &&
		(account.Market != string(marketdata.MarketTypeSpot) || !result.ReduceOnly) &&
		result.ExchangeOrderID > 0 && !result.ObservedAt.IsZero() &&
		validTestnetOrderResult(status, result) &&
		instrument.NativeSymbol == result.Symbol
}

func recoveredTestnetOrderProjection(
	intent db.TradingIntent,
	instrument db.MarketInstrument,
	credential db.TradingAccountCredential,
	account db.TradingAccount,
	result exchangebinance.OrderResult,
) db.TestnetOrder {
	status, _ := normalizeTestnetOrderStatus(result.Status)
	exchangeOrderID := result.ExchangeOrderID
	observedAt := result.ObservedAt.UTC()
	return db.TestnetOrder{
		ID: intent.ID, AccountID: account.ID, IntentID: intent.ID,
		StrategyInstanceID: intent.StrategyInstanceID, InstrumentID: instrument.ID,
		CredentialUpdatedAt: credential.UpdatedAt, SubmittedAccountUpdatedAt: account.UpdatedAt,
		ClientOrderID: intent.ClientOrderID, ExchangeOrderID: &exchangeOrderID,
		Side: result.Side, Quantity: result.OriginalQuantity, FilledQuantity: result.ExecutedQuantity,
		CumulativeQuoteQuantity: result.CumulativeQuoteQuantity, AveragePrice: result.AveragePrice,
		Purpose: "rebalance", OrderType: "market", Status: status,
		SubmittedAt: intent.CreatedAt, ObservedAt: &observedAt,
		CreatedAt: intent.CreatedAt, UpdatedAt: intent.UpdatedAt,
	}
}

// collectTestnetTradeFacts fetches only fills belonging to locally managed
// exchange orders. A response that cannot explain the local cumulative fill is
// a reconciliation mismatch; it is never silently folded into the projection.
func (reconciler *TestnetAccountReconciler) collectTestnetTradeFacts(
	ctx context.Context,
	credential db.TradingAccountCredential,
	account db.TradingAccount,
	apiKey, apiSecret string,
	observations []testnetOrderObservation,
) (testnetTradeFactBatch, error) {
	var instruments []db.MarketInstrument
	if err := reconciler.database.WithContext(ctx).Model(&db.MarketInstrument{}).
		Joins("JOIN trading_account_instruments ON trading_account_instruments.instrument_id = market_instruments.id").
		Where("trading_account_instruments.account_id = ?", account.ID).
		Find(&instruments).Error; err != nil {
		return testnetTradeFactBatch{}, err
	}
	instrumentsByID := make(map[uuid.UUID]db.MarketInstrument, len(instruments))
	instrumentsBySymbol := make(map[string]db.MarketInstrument, len(instruments))
	for _, instrument := range instruments {
		instrumentsByID[instrument.ID] = instrument
		instrumentsBySymbol[instrument.NativeSymbol] = instrument
	}
	var orders []db.TestnetOrder
	if err := reconciler.database.WithContext(ctx).Where(
		"account_id = ? AND credential_updated_at = ?",
		account.ID, credential.UpdatedAt,
	).Order("created_at, id").Find(&orders).Error; err != nil {
		return testnetTradeFactBatch{}, err
	}
	orders = overlayTestnetOrderObservations(orders, observations)
	batch := testnetTradeFactBatch{Trades: make([]testnetManagedTrade, 0), Funding: make([]exchangebinance.FundingIncome, 0)}
	for _, order := range orders {
		if order.ExchangeOrderID == nil || !order.FilledQuantity.IsPositive() {
			continue
		}
		instrument, exists := instrumentsByID[order.InstrumentID]
		if !exists || instrument.NativeSymbol == "" {
			return testnetTradeFactBatch{}, &testnetLedgerMismatchError{Code: "managed_order_instrument_mismatch"}
		}
		trades, err := reconciler.client.QueryOrderTrades(
			ctx, marketdata.MarketType(account.Market), apiKey, apiSecret,
			instrument.NativeSymbol, *order.ExchangeOrderID,
		)
		if err != nil {
			return testnetTradeFactBatch{}, &testnetTradeQueryError{OrderID: order.ID, Err: err}
		}
		if err := validateTestnetTradeSet(account, order, instrument, trades); err != nil {
			return testnetTradeFactBatch{}, err
		}
		for _, trade := range trades {
			batch.Trades = append(batch.Trades, testnetManagedTrade{
				Order: order, Instrument: instrument, Trade: trade,
				ExpectedOrderUpdated: order.UpdatedAt,
			})
		}
	}
	if account.Market != string(marketdata.MarketTypeUSDM) {
		return batch, nil
	}
	now := time.Now().UTC()
	funding, err := reconciler.client.QueryFundingIncome(
		ctx, apiKey, apiSecret, "", now.Add(-7*24*time.Hour), now,
	)
	if err != nil {
		return testnetTradeFactBatch{}, &testnetTradeQueryError{Err: err}
	}
	for _, item := range funding {
		if item.Symbol != "" {
			if _, exists := instrumentsBySymbol[item.Symbol]; !exists {
				return testnetTradeFactBatch{}, &testnetLedgerMismatchError{Code: "funding_unknown_instrument"}
			}
		}
		batch.Funding = append(batch.Funding, item)
	}
	return batch, nil
}

func validateTestnetTradeSet(
	account db.TradingAccount,
	order db.TestnetOrder,
	instrument db.MarketInstrument,
	trades []exchangebinance.AccountTrade,
) error {
	if len(trades) == 0 {
		return &testnetLedgerMismatchError{Code: "filled_order_without_trades"}
	}
	quantity := decimal.Zero
	quoteQuantity := decimal.Zero
	seen := make(map[int64]struct{}, len(trades))
	for _, trade := range trades {
		if _, exists := seen[trade.ExchangeTradeID]; exists {
			return &testnetLedgerMismatchError{Code: "duplicate_trade_fact"}
		}
		seen[trade.ExchangeTradeID] = struct{}{}
		if trade.Symbol != instrument.NativeSymbol || trade.ExchangeOrderID != valueOrZeroInt64(order.ExchangeOrderID) ||
			trade.Side != order.Side || trade.Quantity.IsNegative() || !trade.Quantity.IsPositive() ||
			!trade.Price.IsPositive() || !trade.QuoteQuantity.IsPositive() || trade.Commission.IsNegative() ||
			trade.OccurredAt.IsZero() {
			return &testnetLedgerMismatchError{Code: "trade_fact_shape_mismatch"}
		}
		if account.Market == string(marketdata.MarketTypeUSDM) && trade.PositionSide != "both" {
			return &testnetLedgerMismatchError{Code: "hedge_mode_trade_fact"}
		}
		quantity = quantity.Add(trade.Quantity)
		quoteQuantity = quoteQuantity.Add(trade.QuoteQuantity)
	}
	if !quantity.Equal(order.FilledQuantity) || !quoteQuantity.Equal(order.CumulativeQuoteQuantity) {
		return &testnetLedgerMismatchError{Code: "trade_totals_mismatch"}
	}
	return nil
}

func valueOrZeroInt64(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
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
		lockedAccount, err := lockCurrentTestnetState(tx, credential, account.Environment, nil)
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
	return reconciler.persistSnapshotForModeWithLedger(
		ctx, credential, account, snapshot, status, errorCode, continuous, observations, testnetTradeFactBatch{},
	)
}

func (reconciler *TestnetAccountReconciler) persistSnapshotForModeWithLedger(
	ctx context.Context,
	credential db.TradingAccountCredential,
	account db.TradingAccount,
	snapshot exchangebinance.AccountSnapshot,
	status, errorCode string,
	continuous bool,
	observations []testnetOrderObservation,
	ledgerBatch testnetTradeFactBatch,
) (bool, error) {
	persisted := true
	err := reconciler.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockTestnetAccountExecution(tx, account.ID); err != nil {
			return err
		}
		if _, err := lockCurrentTestnetState(tx, credential, account.Environment, &account.UpdatedAt); err != nil {
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
		managedObservations := managedTestnetOrderObservations(observations)
		if err := persistTestnetTradeFacts(tx, credential, ledgerBatch); err != nil {
			var staleLedger *testnetLedgerStaleError
			if errors.As(err, &staleLedger) {
				persisted = false
				return nil
			}
			return err
		}
		staleOrders, err := applyTestnetOrderObservations(tx, credential, managedObservations)
		if err != nil {
			return err
		}
		if staleOrders {
			persisted = false
			return nil
		}
		recovered, err := recoverTestnetExternalOrders(tx, credential, account, observations)
		if err != nil {
			return err
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
			if recovered {
				return pauseTestnetAccount(tx, account.ID, "testnet_external_order_recovered", now)
			}
			if !continuous || account.Status != "active" {
				return pauseTestnetAccount(tx, account.ID, "testnet_reconciled_manual_release_required", now)
			}
			return nil
		}
		return pauseTestnetAccount(tx, account.ID, "testnet_reconciliation_mismatch", now)
	})
	return persisted, err
}

func managedTestnetOrderObservations(observations []testnetOrderObservation) []testnetOrderObservation {
	if len(observations) == 0 {
		return nil
	}
	managed := make([]testnetOrderObservation, 0, len(observations))
	for _, observation := range observations {
		if !observation.Recovered {
			managed = append(managed, observation)
		}
	}
	return managed
}

func hasRecoveredTestnetOrderObservation(observations []testnetOrderObservation) bool {
	for _, observation := range observations {
		if observation.Recovered {
			return true
		}
	}
	return false
}

func recoverTestnetExternalOrders(
	tx *gorm.DB,
	credential db.TradingAccountCredential,
	account db.TradingAccount,
	observations []testnetOrderObservation,
) (bool, error) {
	candidates := make([]testnetOrderObservation, 0)
	seen := make(map[uuid.UUID]struct{})
	for _, observation := range observations {
		if !observation.Recovered {
			continue
		}
		if _, exists := seen[observation.OrderID]; exists {
			return false, &testnetLedgerMismatchError{Code: "unknown_external_order"}
		}
		seen[observation.OrderID] = struct{}{}
		candidates = append(candidates, observation)
	}
	if len(candidates) == 0 {
		return false, nil
	}
	if len(candidates) > 1 {
		return false, &testnetLedgerMismatchError{Code: "unknown_external_order"}
	}
	intentIDs := make([]uuid.UUID, 0, len(candidates))
	for _, candidate := range candidates {
		intentIDs = append(intentIDs, candidate.OrderID)
	}
	var activeCount int64
	if err := tx.Model(&db.TradingIntent{}).Where(
		"account_id = ? AND environment = ? AND status IN ('processing', 'reconciling') AND id NOT IN ?",
		account.ID, account.Environment, intentIDs,
	).Count(&activeCount).Error; err != nil {
		return false, err
	}
	if activeCount != 0 {
		return false, &testnetLedgerMismatchError{Code: "unknown_external_order"}
	}
	for _, candidate := range candidates {
		var intent db.TradingIntent
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
			"id = ? AND account_id = ? AND owner_user_id = ? AND environment = ?",
			candidate.OrderID, account.ID, credential.OwnerUserID, account.Environment,
		).Take(&intent).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return false, &testnetLedgerMismatchError{Code: "unknown_external_order"}
			}
			return false, err
		}
		if intent.ClientOrderID != candidate.Result.ClientOrderID ||
			intent.ClientOrderID != tradingClientOrderID(intent.ID) ||
			(intent.Status != "pending" && intent.Status != "reconciling") || intent.CompletedAt != nil {
			return false, &testnetLedgerMismatchError{Code: "unknown_external_order"}
		}
		var instrument db.MarketInstrument
		if err := tx.Where(
			"id = ? AND market_type = ? AND venue = ? AND status = 'trading'",
			intent.InstrumentID, account.Market, string(marketdata.VenueBinance),
		).Take(&instrument).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return false, &testnetLedgerMismatchError{Code: "unknown_external_order"}
			}
			return false, err
		}
		if !validRecoveredExternalOrder(account, instrument, candidate.Result) {
			return false, &testnetLedgerMismatchError{Code: "unknown_external_order"}
		}
		var existing db.TestnetOrder
		existingErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
			"account_id = ? AND client_order_id = ?", account.ID, intent.ClientOrderID,
		).Take(&existing).Error
		if existingErr == nil {
			return false, &testnetLedgerMismatchError{Code: "unknown_external_order"}
		}
		if !errors.Is(existingErr, gorm.ErrRecordNotFound) {
			return false, existingErr
		}
		var exchangeOrderCount int64
		if err := tx.Model(&db.TestnetOrder{}).Where(
			"account_id = ? AND exchange_order_id = ?", account.ID, candidate.Result.ExchangeOrderID,
		).Count(&exchangeOrderCount).Error; err != nil {
			return false, err
		}
		if exchangeOrderCount != 0 {
			return false, &testnetLedgerMismatchError{Code: "unknown_external_order"}
		}
		status, _ := normalizeTestnetOrderStatus(candidate.Result.Status)
		exchangeOrderID := candidate.Result.ExchangeOrderID
		observedAt := candidate.Result.ObservedAt.UTC()
		recoveredAt := time.Now().UTC()
		order := db.TestnetOrder{
			ID: intent.ID, AccountID: account.ID, IntentID: intent.ID,
			StrategyInstanceID: intent.StrategyInstanceID, InstrumentID: intent.InstrumentID,
			CredentialUpdatedAt: credential.UpdatedAt, SubmittedAccountUpdatedAt: account.UpdatedAt,
			ClientOrderID: intent.ClientOrderID, ExchangeOrderID: &exchangeOrderID,
			Side: candidate.Result.Side, Quantity: candidate.Result.OriginalQuantity,
			FilledQuantity:          candidate.Result.ExecutedQuantity,
			CumulativeQuoteQuantity: candidate.Result.CumulativeQuoteQuantity,
			AveragePrice:            candidate.Result.AveragePrice, Purpose: "rebalance", OrderType: "market",
			ReduceOnly: candidate.Result.ReduceOnly, Status: status, SubmitAttemptCount: 1,
			SubmittedAt: recoveredAt, RecoveredAt: &recoveredAt, ObservedAt: &observedAt,
			CreatedAt: recoveredAt, UpdatedAt: recoveredAt,
		}
		if err := tx.Create(&order).Error; err != nil {
			return false, err
		}
		result := tx.Model(&db.TradingIntent{}).Where(
			"id = ? AND status IN ('pending', 'reconciling') AND completed_at IS NULL", intent.ID,
		).Updates(map[string]any{
			"status": "reconciling", "block_reason": "testnet_external_order_recovered",
			"claimed_at": nil, "worker_id": nil, "updated_at": recoveredAt,
		})
		if result.Error != nil {
			return false, result.Error
		}
		if result.RowsAffected != 1 {
			return false, &testnetLedgerMismatchError{Code: "unknown_external_order"}
		}
	}
	return true, nil
}

func persistTestnetTradeFacts(
	tx *gorm.DB,
	credential db.TradingAccountCredential,
	batch testnetTradeFactBatch,
) error {
	seen := make(map[string]struct{}, len(batch.Trades)*2+len(batch.Funding))
	for _, managed := range batch.Trades {
		var order db.TestnetOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
			"id = ? AND account_id = ? AND credential_updated_at = ?",
			managed.Order.ID, credential.AccountID, credential.UpdatedAt,
		).Take(&order).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return &testnetLedgerStaleError{}
			}
			return err
		}
		if !order.UpdatedAt.Equal(managed.ExpectedOrderUpdated) {
			return &testnetLedgerStaleError{}
		}
		instrumentID := order.InstrumentID
		orderID := order.ID
		intentID := order.IntentID
		tradeID := managed.Trade.ExchangeTradeID
		tradeKey := "trade:" + managed.Instrument.NativeSymbol + ":" + strconv.FormatInt(tradeID, 10)
		fillKey := tradeKey + ":fill"
		feeKey := tradeKey + ":fee"
		if _, exists := seen[fillKey]; exists {
			return &testnetLedgerMismatchError{Code: "duplicate_trade_fact"}
		}
		seen[fillKey], seen[feeKey] = struct{}{}, struct{}{}
		createdAt := time.Now().UTC()
		fill := db.TestnetTradeFact{
			AccountID: credential.AccountID, CredentialUpdatedAt: credential.UpdatedAt,
			InstrumentID: &instrumentID, OrderID: &orderID, IntentID: &intentID,
			EventType: "fill", Symbol: managed.Instrument.NativeSymbol, ExternalTradeID: &tradeID,
			Side: managed.Trade.Side, PositionSide: managed.Trade.PositionSide,
			Quantity: managed.Trade.Quantity, Price: managed.Trade.Price,
			QuoteQuantity: managed.Trade.QuoteQuantity, Asset: managed.Instrument.BaseAsset,
			RealizedPnL: managed.Trade.RealizedPnL, Buyer: managed.Trade.Buyer,
			Maker: managed.Trade.Maker, OccurredAt: managed.Trade.OccurredAt,
			DedupeKey: fillKey, CreatedAt: createdAt,
		}
		if err := appendTestnetTradeFact(tx, fill); err != nil {
			return err
		}
		fee := db.TestnetTradeFact{
			AccountID: credential.AccountID, CredentialUpdatedAt: credential.UpdatedAt,
			InstrumentID: &instrumentID, OrderID: &orderID, IntentID: &intentID,
			EventType: "fee", Symbol: managed.Instrument.NativeSymbol, ExternalTradeID: &tradeID,
			Amount: managed.Trade.Commission, Asset: managed.Trade.CommissionAsset,
			OccurredAt: managed.Trade.OccurredAt, DedupeKey: feeKey, CreatedAt: createdAt,
		}
		if err := appendTestnetTradeFact(tx, fee); err != nil {
			return err
		}
	}
	for _, item := range batch.Funding {
		key := "funding:" + item.TransactionID
		if _, exists := seen[key]; exists {
			return &testnetLedgerMismatchError{Code: "duplicate_funding_fact"}
		}
		seen[key] = struct{}{}
		var instrumentID *uuid.UUID
		if item.Symbol != "" {
			var instrument db.MarketInstrument
			if err := tx.Where("native_symbol = ? AND market_type = ? AND venue = ?", item.Symbol,
				string(marketdata.MarketTypeUSDM), string(marketdata.VenueBinance)).Take(&instrument).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return &testnetLedgerMismatchError{Code: "funding_unknown_instrument"}
				}
				return err
			}
			instrumentID = &instrument.ID
		}
		fact := db.TestnetTradeFact{
			AccountID: credential.AccountID, CredentialUpdatedAt: credential.UpdatedAt,
			InstrumentID: instrumentID, EventType: "funding", Symbol: item.Symbol,
			ExternalTransactionID: item.TransactionID, Amount: item.Amount, Asset: item.Asset,
			OccurredAt: item.OccurredAt, DedupeKey: key, CreatedAt: time.Now().UTC(),
		}
		if err := appendTestnetTradeFact(tx, fact); err != nil {
			return err
		}
	}
	return nil
}

func appendTestnetTradeFact(tx *gorm.DB, fact db.TestnetTradeFact) error {
	result := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "account_id"}, {Name: "credential_updated_at"}, {Name: "dedupe_key"}},
		DoNothing: true,
	}).Create(&fact)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 1 {
		return nil
	}
	var existing db.TestnetTradeFact
	if err := tx.Where(
		"account_id = ? AND credential_updated_at = ? AND dedupe_key = ?",
		fact.AccountID, fact.CredentialUpdatedAt, fact.DedupeKey,
	).Take(&existing).Error; err != nil {
		return err
	}
	if !sameTestnetTradeFact(existing, fact) {
		return &testnetLedgerMismatchError{Code: "trade_fact_mutated"}
	}
	return nil
}

func sameTestnetTradeFact(left, right db.TestnetTradeFact) bool {
	return left.AccountID == right.AccountID && left.CredentialUpdatedAt.Equal(right.CredentialUpdatedAt) &&
		equalUUIDPointer(left.InstrumentID, right.InstrumentID) && equalUUIDPointer(left.OrderID, right.OrderID) &&
		equalUUIDPointer(left.IntentID, right.IntentID) && left.EventType == right.EventType &&
		left.Symbol == right.Symbol && equalInt64Pointer(left.ExternalTradeID, right.ExternalTradeID) &&
		left.ExternalTransactionID == right.ExternalTransactionID && left.Side == right.Side &&
		left.PositionSide == right.PositionSide && left.Quantity.Equal(right.Quantity) && left.Price.Equal(right.Price) &&
		left.QuoteQuantity.Equal(right.QuoteQuantity) && left.Amount.Equal(right.Amount) && left.Asset == right.Asset &&
		left.RealizedPnL.Equal(right.RealizedPnL) && left.Buyer == right.Buyer && left.Maker == right.Maker &&
		left.OccurredAt.Equal(right.OccurredAt) && left.DedupeKey == right.DedupeKey
}

func equalUUIDPointer(left, right *uuid.UUID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func equalInt64Pointer(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
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
		"id = ? AND environment IN ('testnet', 'live') AND status IN ('processing', 'reconciling')", intentID,
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
		if _, err := lockCurrentTestnetState(tx, credential, account.Environment, &account.UpdatedAt); err != nil {
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
		if _, err := lockCurrentTestnetState(tx, credential, account.Environment, &account.UpdatedAt); err != nil {
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
	environment string,
	expectedAccountUpdatedAt *time.Time,
) (db.TradingAccount, error) {
	var account db.TradingAccount
	accountQuery := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? AND owner_user_id = ? AND environment = ?", credential.AccountID, credential.OwnerUserID, environment)
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
	if err := tx.Model(&db.TradingAccount{}).Where("id = ? AND environment IN ('testnet', 'live')", accountID).Updates(map[string]any{
		"status": "paused", "pause_reason": reason, "automation_enabled": false,
		"manual_authorized_at":         gorm.Expr("CASE WHEN environment = 'live' THEN NULL ELSE manual_authorized_at END"),
		"manual_authorized_by_user_id": gorm.Expr("CASE WHEN environment = 'live' THEN NULL ELSE manual_authorized_by_user_id END"),
		"auto_authorized_at":           gorm.Expr("CASE WHEN environment = 'live' THEN NULL ELSE auto_authorized_at END"),
		"auto_authorized_by_user_id":   gorm.Expr("CASE WHEN environment = 'live' THEN NULL ELSE auto_authorized_by_user_id END"),
		"updated_at":                   now,
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
