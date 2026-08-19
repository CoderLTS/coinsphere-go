package marketdata

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	defaultReconcileInterval = 10 * time.Second
	defaultBackfillPageSize  = 300
)

type ManagerConfig struct {
	ReconcileInterval time.Duration
	BackfillPageSize  int
	OnFirstClosed     CandleHandler
}

type Manager struct {
	store  *PostgresStore
	source MarketSource
	config ManagerConfig

	mu            sync.Mutex
	subscriptions map[subscriptionKey]runningSubscription
	nextRunID     uint64
	wg            sync.WaitGroup
}

type subscriptionKey struct {
	instrumentID uuid.UUID
	interval     CandleInterval
}

type marketSubscription struct {
	instrument Instrument
	interval   CandleInterval
}

type runningSubscription struct {
	id     uint64
	cancel context.CancelFunc
}

func NewManager(database *sql.DB, source MarketSource, config ManagerConfig) (*Manager, error) {
	if database == nil || source == nil {
		return nil, errors.New("market database and source are required")
	}
	if config.ReconcileInterval < 0 || config.ReconcileInterval > 30*time.Second || config.BackfillPageSize < 0 || config.BackfillPageSize > 300 {
		return nil, errors.New("invalid market manager config")
	}
	if config.ReconcileInterval == 0 {
		config.ReconcileInterval = defaultReconcileInterval
	}
	if config.BackfillPageSize == 0 {
		config.BackfillPageSize = defaultBackfillPageSize
	}
	return &Manager{
		store: NewPostgresStore(database), source: source, config: config,
		subscriptions: make(map[subscriptionKey]runningSubscription),
	}, nil
}

// Run 持续协调自选、策略与工作流声明的行情订阅。
func (manager *Manager) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("market manager context is required")
	}
	defer manager.stopSubscriptions()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}

		if err := manager.reconcile(ctx); err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "market subscription reconcile failed", "error_category", "market_data")
		}
		timer.Reset(manager.config.ReconcileInterval)
	}
}

// Backfill 通过同一已收盘 K 线写入路径补充 REST 历史数据，且不触发回调。
func (manager *Manager) Backfill(ctx context.Context, instrument Instrument, interval CandleInterval, start, end time.Time) (int, error) {
	request := CandlePageRequest{
		Instrument: instrument, Interval: interval, StartTime: start, EndTime: end,
		Limit: manager.config.BackfillPageSize,
	}
	if err := ValidateCandlePageRequest(request); err != nil {
		return 0, err
	}
	written := 0
	for {
		page, err := manager.source.FetchCandlePage(ctx, request)
		if err != nil {
			return written, err
		}
		if err := ValidateCandlePage(request, page); err != nil {
			return written, errors.New("invalid market source candle page")
		}
		for _, candle := range page.Candles {
			if !candle.IsClosed {
				return written, errors.New("historical candle is not closed")
			}
			result, err := manager.store.UpsertCandle(ctx, candle)
			if err != nil {
				return written, err
			}
			if result.Changed {
				written++
			}
		}
		if page.NextCursor == "" {
			return written, nil
		}
		request.Cursor = page.NextCursor
	}
}

type InstrumentSyncResult struct {
	SyncedCount int
	ByMarket    map[string]int
}

// SyncInstruments 按显式范围同步元数据；启动行情运行时不会调用它。
func (manager *Manager) SyncInstruments(ctx context.Context, marketTypes []MarketType, quoteAssets []string, restBaseURLs map[MarketType]string, proxyURL string) (InstrumentSyncResult, error) {
	result := InstrumentSyncResult{ByMarket: map[string]int{}}
	if err := manager.source.ConfigurePublicAccess(restBaseURLs, proxyURL); err != nil {
		return result, err
	}
	quotes := make(map[string]bool, len(quoteAssets))
	for _, quote := range quoteAssets {
		quotes[quote] = true
	}
	seenMarkets := map[MarketType]bool{}
	for _, marketType := range marketTypes {
		if seenMarkets[marketType] || marketType != MarketTypeSpot && marketType != MarketTypeUSDM {
			continue
		}
		seenMarkets[marketType] = true
		metadata, err := manager.source.SnapshotInstruments(ctx, marketType)
		if err != nil {
			return result, err
		}
		for _, instrument := range metadata {
			if !quotes[instrument.QuoteAsset] {
				continue
			}
			if _, err := manager.store.UpsertInstrument(ctx, instrument); err != nil {
				return result, err
			}
			result.SyncedCount++
			result.ByMarket[string(marketType)]++
		}
	}
	return result, nil
}

func (manager *Manager) CheckConnectivity(ctx context.Context, restBaseURLs map[MarketType]string, proxyURL string, marketType MarketType) (time.Duration, error) {
	if err := manager.source.ConfigurePublicAccess(restBaseURLs, proxyURL); err != nil {
		return 0, err
	}
	startedAt := time.Now()
	err := manager.source.CheckConnectivity(ctx, marketType)
	return time.Since(startedAt), err
}

func (manager *Manager) ConfigurePublicAccess(restBaseURLs map[MarketType]string, proxyURL string, restartSubscriptions bool) error {
	if err := manager.source.ConfigurePublicAccess(restBaseURLs, proxyURL); err != nil {
		return err
	}
	if restartSubscriptions {
		manager.mu.Lock()
		for _, running := range manager.subscriptions {
			running.cancel()
		}
		manager.mu.Unlock()
	}
	return nil
}

func (manager *Manager) reconcile(ctx context.Context) error {
	desired, err := manager.store.listWatchlistSubscriptions(ctx)
	if err != nil {
		return err
	}
	desiredKeys := make(map[subscriptionKey]struct{}, len(desired))
	var reconcileErr error
	for _, subscription := range desired {
		key := subscriptionKey{instrumentID: subscription.instrument.ID, interval: subscription.interval}
		desiredKeys[key] = struct{}{}
		if err := manager.backfillGap(ctx, subscription); err != nil {
			reconcileErr = errors.Join(reconcileErr, err)
		}
		manager.ensureSubscription(ctx, key, subscription)
	}

	manager.mu.Lock()
	for key, running := range manager.subscriptions {
		if _, ok := desiredKeys[key]; !ok {
			running.cancel()
		}
	}
	manager.mu.Unlock()
	return reconcileErr
}

func (manager *Manager) backfillGap(ctx context.Context, subscription marketSubscription) error {
	duration, _ := CandleIntervalDuration(subscription.interval)
	end := time.Now().UTC().Truncate(duration)
	start, found, err := manager.store.latestClosedCandleTime(ctx, subscription.instrument.ID, subscription.interval)
	if err != nil {
		return err
	}
	if !found {
		start = end.Add(-duration)
	}
	if !start.Before(end) {
		return nil
	}
	_, err = manager.Backfill(ctx, subscription.instrument, subscription.interval, start, end)
	return err
}

func (manager *Manager) ensureSubscription(ctx context.Context, key subscriptionKey, subscription marketSubscription) {
	manager.mu.Lock()
	if _, ok := manager.subscriptions[key]; ok {
		manager.mu.Unlock()
		return
	}
	subscriptionCtx, cancel := context.WithCancel(ctx)
	manager.nextRunID++
	runID := manager.nextRunID
	manager.subscriptions[key] = runningSubscription{id: runID, cancel: cancel}
	manager.wg.Add(1)
	manager.mu.Unlock()

	go func() {
		defer manager.wg.Done()
		err := manager.source.SubscribeCandles(subscriptionCtx, subscription.instrument, subscription.interval, func(candle Candle) error {
			result, err := manager.store.UpsertCandle(subscriptionCtx, candle)
			if err != nil || !result.FirstClosed || manager.config.OnFirstClosed == nil {
				return err
			}
			return manager.config.OnFirstClosed(candle)
		})
		if err != nil && subscriptionCtx.Err() == nil {
			slog.WarnContext(subscriptionCtx, "market candle subscription stopped", "error_category", "external_data")
		}
		manager.mu.Lock()
		if running, ok := manager.subscriptions[key]; ok && running.id == runID {
			delete(manager.subscriptions, key)
		}
		manager.mu.Unlock()
	}()
}

func (manager *Manager) stopSubscriptions() {
	manager.mu.Lock()
	for _, running := range manager.subscriptions {
		running.cancel()
	}
	manager.mu.Unlock()
	manager.wg.Wait()
}

func (store *PostgresStore) listWatchlistSubscriptions(ctx context.Context) ([]marketSubscription, error) {
	rows, err := store.db.QueryContext(ctx, `
SELECT DISTINCT
    instrument.id, instrument.venue, instrument.market_type, instrument.native_symbol,
    instrument.base_asset, instrument.quote_asset, instrument.status, instrument.price_tick,
    instrument.quantity_step, instrument.min_quantity, instrument.min_notional,
    instrument.updated_at, desired.interval_code
FROM (
    SELECT instrument_id, interval_code
    FROM watchlist_items
    UNION
    SELECT version.instrument_id, version.interval_code
    FROM strategy_instances AS instance
    JOIN strategy_versions AS version ON version.id = instance.strategy_version_id
    WHERE instance.is_enabled AND version.status = 'published'
	UNION
	SELECT subscription.instrument_id, subscription.interval_code
	FROM market_workflow_subscriptions AS subscription
	JOIN workflow_runtime_states AS state
	  ON state.active_workflow_definition_id = subscription.workflow_definition_id
) AS desired
JOIN market_instruments AS instrument ON instrument.id = desired.instrument_id
WHERE instrument.venue = 'binance' AND instrument.status = 'trading'
ORDER BY instrument.id, desired.interval_code
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var subscriptions []marketSubscription
	for rows.Next() {
		var subscription marketSubscription
		if err := rows.Scan(
			&subscription.instrument.ID, &subscription.instrument.Venue, &subscription.instrument.MarketType,
			&subscription.instrument.NativeSymbol, &subscription.instrument.BaseAsset, &subscription.instrument.QuoteAsset,
			&subscription.instrument.Status, &subscription.instrument.PriceTick, &subscription.instrument.QuantityStep,
			&subscription.instrument.MinQuantity, &subscription.instrument.MinNotional, &subscription.instrument.UpdatedAt,
			&subscription.interval,
		); err != nil {
			return nil, err
		}
		subscription.instrument.UpdatedAt = subscription.instrument.UpdatedAt.UTC()
		subscriptions = append(subscriptions, subscription)
	}
	return subscriptions, rows.Err()
}

func (store *PostgresStore) latestClosedCandleTime(ctx context.Context, instrumentID uuid.UUID, interval CandleInterval) (time.Time, bool, error) {
	var closeTime time.Time
	err := store.db.QueryRowContext(ctx, `
SELECT close_time
FROM market_candles
WHERE venue = 'binance' AND instrument_id = $1 AND interval_code = $2 AND is_closed
ORDER BY open_time DESC
LIMIT 1
`, instrumentID, interval).Scan(&closeTime)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return closeTime.UTC(), true, nil
}
