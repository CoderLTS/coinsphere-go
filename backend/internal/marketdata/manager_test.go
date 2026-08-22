package marketdata_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"coinsphere/backend/internal/marketdata"
	"github.com/shopspring/decimal"
)

type fakeMarketSource struct {
	configure func(map[marketdata.MarketType]string, string) error
	check     func(context.Context, marketdata.MarketType) error
	snapshot  func(context.Context, marketdata.MarketType) ([]marketdata.InstrumentMetadata, error)
	fetch     func(context.Context, marketdata.CandlePageRequest) (marketdata.CandlePage, error)
	subscribe func(context.Context, marketdata.Instrument, marketdata.CandleInterval, marketdata.CandleHandler) error
}

func (source fakeMarketSource) ConfigurePublicAccess(values map[marketdata.MarketType]string, proxyURL string) error {
	if source.configure == nil {
		return nil
	}
	return source.configure(values, proxyURL)
}

func (source fakeMarketSource) CheckConnectivity(ctx context.Context, marketType marketdata.MarketType) error {
	if source.check == nil {
		return nil
	}
	return source.check(ctx, marketType)
}

func (source fakeMarketSource) SnapshotInstruments(ctx context.Context, marketType marketdata.MarketType) ([]marketdata.InstrumentMetadata, error) {
	if source.snapshot == nil {
		return nil, nil
	}
	return source.snapshot(ctx, marketType)
}

func (source fakeMarketSource) FetchCandlePage(ctx context.Context, request marketdata.CandlePageRequest) (marketdata.CandlePage, error) {
	if source.fetch == nil {
		return marketdata.CandlePage{}, nil
	}
	return source.fetch(ctx, request)
}

func (source fakeMarketSource) SubscribeCandles(ctx context.Context, instrument marketdata.Instrument, interval marketdata.CandleInterval, handler marketdata.CandleHandler) error {
	return source.subscribe(ctx, instrument, interval, handler)
}

func (fakeMarketSource) SubscribeTickers(context.Context, marketdata.Instrument, marketdata.TickerHandler) error {
	return nil
}

func TestManagerBackfillIsIdempotentAndDoesNotTriggerRealtimeCallback(t *testing.T) {
	database := openStoreTestDatabase(t)
	store := marketdata.NewPostgresStore(database)
	instrument, err := store.UpsertInstrument(t.Context(), managerTestMetadata())
	if err != nil {
		t.Fatalf("insert instrument: %v", err)
	}
	start := time.Date(2026, time.August, 6, 8, 0, 0, 0, time.UTC)
	candles := []marketdata.Candle{
		managerTestCandle(instrument, start),
		managerTestCandle(instrument, start.Add(time.Minute)),
	}
	source := fakeMarketSource{fetch: func(_ context.Context, request marketdata.CandlePageRequest) (marketdata.CandlePage, error) {
		if request.Cursor == "" {
			return marketdata.CandlePage{
				Candles:    []marketdata.Candle{candles[0]},
				NextCursor: marketdata.CandleCursor(start.Add(time.Minute).Format(time.RFC3339Nano)),
			}, nil
		}
		return marketdata.CandlePage{Candles: []marketdata.Candle{candles[1]}}, nil
	}}
	var callbacks atomic.Int32
	manager, err := marketdata.NewManager(database, source, marketdata.ManagerConfig{
		BackfillPageSize: 1,
		OnFirstClosed: func(marketdata.Candle) error {
			callbacks.Add(1)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("create manager: %v", err)
	}

	written, err := manager.Backfill(t.Context(), instrument, marketdata.CandleInterval1m, start, start.Add(2*time.Minute))
	if err != nil || written != 2 {
		t.Fatalf("first backfill written=%d err=%v", written, err)
	}
	written, err = manager.Backfill(t.Context(), instrument, marketdata.CandleInterval1m, start, start.Add(2*time.Minute))
	if err != nil || written != 0 || callbacks.Load() != 0 {
		t.Fatalf("repeat backfill written=%d callbacks=%d err=%v", written, callbacks.Load(), err)
	}
}

func TestManagerStartupDoesNotFetchInstrumentMetadata(t *testing.T) {
	database := openStoreTestDatabase(t)
	var snapshots atomic.Int32
	manager, err := marketdata.NewManager(database, fakeMarketSource{
		snapshot: func(context.Context, marketdata.MarketType) ([]marketdata.InstrumentMetadata, error) {
			snapshots.Add(1)
			return nil, nil
		},
	}, marketdata.ManagerConfig{ReconcileInterval: 10 * time.Millisecond, BackfillPageSize: 10})
	if err != nil {
		t.Fatalf("create manager: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if runErr := manager.Run(ctx); !errors.Is(runErr, context.DeadlineExceeded) {
		t.Fatalf("manager shutdown error = %v", runErr)
	}
	if snapshots.Load() != 0 {
		t.Fatalf("startup fetched %d metadata snapshots", snapshots.Load())
	}
}

func TestManagerSyncInstrumentsFiltersMarketAndQuoteAsset(t *testing.T) {
	database := openStoreTestDatabase(t)
	usdt := managerTestMetadata()
	usdc := usdt
	usdc.NativeSymbol, usdc.QuoteAsset = "BTCUSDC", "USDC"
	var spotSnapshots, usdmSnapshots atomic.Int32
	configuredSpotURL := ""
	manager, err := marketdata.NewManager(database, fakeMarketSource{
		configure: func(values map[marketdata.MarketType]string, _ string) error {
			configuredSpotURL = values[marketdata.MarketTypeSpot]
			return nil
		},
		snapshot: func(_ context.Context, marketType marketdata.MarketType) ([]marketdata.InstrumentMetadata, error) {
			if marketType == marketdata.MarketTypeSpot {
				spotSnapshots.Add(1)
				return []marketdata.InstrumentMetadata{usdt, usdc}, nil
			}
			usdmSnapshots.Add(1)
			return nil, nil
		},
	}, marketdata.ManagerConfig{BackfillPageSize: 10})
	if err != nil {
		t.Fatalf("create manager: %v", err)
	}
	result, err := manager.SyncInstruments(
		t.Context(),
		[]marketdata.MarketType{marketdata.MarketTypeSpot, marketdata.MarketTypeSpot},
		[]string{"USDC"},
		map[marketdata.MarketType]string{
			marketdata.MarketTypeSpot: "https://data-api.binance.vision",
			marketdata.MarketTypeUSDM: "https://fapi.binance.com",
		},
		"",
	)
	if err != nil {
		t.Fatalf("sync instruments: %v", err)
	}
	if result.SyncedCount != 1 || result.ByMarket["spot"] != 1 || spotSnapshots.Load() != 1 || usdmSnapshots.Load() != 0 || configuredSpotURL != "https://data-api.binance.vision" {
		t.Fatalf("sync result=%#v spot=%d usdm=%d", result, spotSnapshots.Load(), usdmSnapshots.Load())
	}
	var storedQuote string
	if err := database.QueryRow("SELECT quote_asset FROM market_instruments").Scan(&storedQuote); err != nil {
		t.Fatalf("read synced instrument: %v", err)
	}
	if storedQuote != "USDC" {
		t.Fatalf("synced quote asset = %s", storedQuote)
	}
}

func TestManagerReconcilesDisconnectAndTriggersOneFirstClose(t *testing.T) {
	database := openStoreTestDatabase(t)
	store := marketdata.NewPostgresStore(database)
	metadata := managerTestMetadata()
	instrument, err := store.UpsertInstrument(t.Context(), metadata)
	if err != nil {
		t.Fatalf("insert instrument: %v", err)
	}
	var ownerID int64
	if err := database.QueryRow(`INSERT INTO users (username) VALUES ('manager-owner') RETURNING id`).Scan(&ownerID); err != nil {
		t.Fatalf("insert owner: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO watchlist_items (id, owner_user_id, instrument_id, interval_code)
VALUES ('019c2f6d-7c00-7000-8000-000000000020', $1, $2, '1m')
`, ownerID, instrument.ID); err != nil {
		t.Fatalf("insert watchlist: %v", err)
	}

	closed := managerTestCandle(instrument, time.Now().UTC().Truncate(time.Minute))
	var subscribeCalls, callbacks atomic.Int32
	source := fakeMarketSource{
		snapshot: func(_ context.Context, marketType marketdata.MarketType) ([]marketdata.InstrumentMetadata, error) {
			if marketType == marketdata.MarketTypeSpot {
				return []marketdata.InstrumentMetadata{metadata}, nil
			}
			return nil, nil
		},
		fetch: func(context.Context, marketdata.CandlePageRequest) (marketdata.CandlePage, error) {
			return marketdata.CandlePage{}, nil
		},
		subscribe: func(ctx context.Context, _ marketdata.Instrument, _ marketdata.CandleInterval, handler marketdata.CandleHandler) error {
			if subscribeCalls.Add(1) == 1 {
				return &marketdata.SourceError{Kind: marketdata.SourceErrorUnavailable}
			}
			if err := handler(closed); err != nil {
				return err
			}
			if err := handler(closed); err != nil {
				return err
			}
			<-ctx.Done()
			return ctx.Err()
		},
	}
	manager, err := marketdata.NewManager(database, source, marketdata.ManagerConfig{
		ReconcileInterval: 20 * time.Millisecond,
		BackfillPageSize:  10,
		OnFirstClosed: func(marketdata.Candle) error {
			callbacks.Add(1)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("create manager: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- manager.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for (subscribeCalls.Load() < 2 || callbacks.Load() < 1) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if subscribeCalls.Load() < 2 || callbacks.Load() != 1 {
		cancel()
		t.Fatalf("subscriptions=%d callbacks=%d", subscribeCalls.Load(), callbacks.Load())
	}
	if err := manager.ConfigurePublicAccess(map[marketdata.MarketType]string{
		marketdata.MarketTypeSpot: "https://data-api.binance.vision",
		marketdata.MarketTypeUSDM: "https://fapi.binance.com",
	}, "http://proxy.internal:7890", true); err != nil {
		cancel()
		t.Fatalf("reload public access: %v", err)
	}
	for subscribeCalls.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if subscribeCalls.Load() < 3 {
		cancel()
		t.Fatal("network configuration did not restart the candle subscription")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("manager shutdown error = %v", err)
	}
	if callbacks.Load() != 1 {
		t.Fatalf("duplicate close callbacks = %d", callbacks.Load())
	}
}

func TestManagerSubscribesEnabledPublishedStrategyInstances(t *testing.T) {
	database := openStoreTestDatabase(t)
	store := marketdata.NewPostgresStore(database)
	metadata := managerTestMetadata()
	instrument, err := store.UpsertInstrument(t.Context(), metadata)
	if err != nil {
		t.Fatalf("insert instrument: %v", err)
	}
	var ownerID, recordID, workflowDefinitionID int64
	if err := database.QueryRow(`INSERT INTO users (username) VALUES ('manager-strategy-owner') RETURNING id`).Scan(&ownerID); err != nil {
		t.Fatalf("insert strategy owner: %v", err)
	}
	if err := database.QueryRow(`
INSERT INTO idempotency_records (user_id, scope, key_hash, request_hash, expires_at, created_at)
VALUES ($1, 'strategy:publish:manager', repeat('m', 64), repeat('n', 64),
        CURRENT_TIMESTAMP + INTERVAL '1 day', CURRENT_TIMESTAMP)
RETURNING id
`, ownerID).Scan(&recordID); err != nil {
		t.Fatalf("insert strategy idempotency record: %v", err)
	}
	if err := database.QueryRow(`
INSERT INTO workflow_definitions (code, version, display_name, graph_json, created_by, created_at)
VALUES ('manager-strategy-workflow', 1, 'manager strategy workflow', '{"nodes":[],"edges":[]}', $1, CURRENT_TIMESTAMP)
RETURNING id
`, ownerID).Scan(&workflowDefinitionID); err != nil {
		t.Fatalf("insert strategy workflow definition: %v", err)
	}
	const strategyID = "019d5000-0000-7000-8000-000000000001"
	const versionID = "019d5000-0000-7000-8000-000000000002"
	const publishTaskID = "019d5000-0000-7000-8000-000000000003"
	const instanceID = "019d5000-0000-7000-8000-000000000004"
	const sourceCode = "def on_bar(candles, params): return Decimal('0')"
	if _, err := database.Exec(`
INSERT INTO strategies (
    id, name, source_code, lookback_bars,
    parameter_schema_json, created_by_user_id, updated_by_user_id
) VALUES ($1, 'manager strategy', $2, 1, '{}', $3, $3)
`, strategyID, sourceCode, ownerID); err != nil {
		t.Fatalf("insert strategy draft: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO worker_tasks (id, task_type, payload_json, status, attempt_count, lane, finished_at)
VALUES ($1, 'strategy.publish', $2, 'succeeded', 1, 'backtest', CURRENT_TIMESTAMP)
`, publishTaskID, `{"strategyId":"`+strategyID+`","strategyVersionId":"`+versionID+`"}`); err != nil {
		t.Fatalf("insert publish task: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO strategy_versions (
    id, strategy_id, version_number, status, worker_task_id, idempotency_record_id,
    name, source_code, code_sha256, runtime_version, lookback_bars,
    parameter_schema_json, published_by_user_id, published_at
) VALUES ($1, $2, 1, 'published', $3, $4, 'manager strategy', $5, repeat('a', 64),
          'python3.12', 1, '{}', $6, CURRENT_TIMESTAMP)
`, versionID, strategyID, publishTaskID, recordID, sourceCode, ownerID); err != nil {
		t.Fatalf("insert strategy version: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO strategy_instances (
    id, owner_user_id, strategy_version_id, market_type, instrument_id, interval_code,
    workflow_definition_id, workflow_node_id, name, mode, environment, is_enabled
) VALUES ($1, $2, $3, 'spot', $4, '1m', $5, 'strategy-node',
          'enabled manager strategy', 'signal_only', 'paper', TRUE)
`, instanceID, ownerID, versionID, instrument.ID, workflowDefinitionID); err != nil {
		t.Fatalf("insert enabled strategy instance: %v", err)
	}

	subscribed := make(chan struct{}, 1)
	source := fakeMarketSource{
		snapshot: func(_ context.Context, marketType marketdata.MarketType) ([]marketdata.InstrumentMetadata, error) {
			if marketType == marketdata.MarketTypeSpot {
				return []marketdata.InstrumentMetadata{metadata}, nil
			}
			return nil, nil
		},
		fetch: func(context.Context, marketdata.CandlePageRequest) (marketdata.CandlePage, error) {
			return marketdata.CandlePage{}, nil
		},
		subscribe: func(ctx context.Context, _ marketdata.Instrument, _ marketdata.CandleInterval, _ marketdata.CandleHandler) error {
			subscribed <- struct{}{}
			<-ctx.Done()
			return ctx.Err()
		},
	}
	manager, err := marketdata.NewManager(database, source, marketdata.ManagerConfig{
		ReconcileInterval: 20 * time.Millisecond,
		BackfillPageSize:  10,
	})
	if err != nil {
		t.Fatalf("create manager: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- manager.Run(ctx) }()
	select {
	case <-subscribed:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("enabled strategy instance did not create a candle subscription")
	}
	cancel()
	if runErr := <-done; !errors.Is(runErr, context.Canceled) {
		t.Fatalf("manager shutdown error = %v", runErr)
	}
}

func managerTestMetadata() marketdata.InstrumentMetadata {
	return marketdata.InstrumentMetadata{
		Venue: marketdata.VenueBinance, MarketType: marketdata.MarketTypeSpot,
		NativeSymbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT",
		Status:    marketdata.InstrumentStatusTrading,
		PriceTick: decimal.RequireFromString("0.1"), QuantityStep: decimal.RequireFromString("0.001"),
		MinQuantity: decimal.RequireFromString("0.001"), MinNotional: decimal.RequireFromString("5"),
		UpdatedAt: time.Date(2026, time.August, 6, 0, 0, 0, 0, time.UTC),
	}
}

func managerTestCandle(instrument marketdata.Instrument, openTime time.Time) marketdata.Candle {
	return marketdata.Candle{
		Venue: instrument.Venue, InstrumentID: instrument.ID, Interval: marketdata.CandleInterval1m,
		OpenTime: openTime, CloseTime: openTime.Add(time.Minute),
		Open: decimal.NewFromInt(100), High: decimal.NewFromInt(101),
		Low: decimal.NewFromInt(99), Close: decimal.NewFromInt(100),
		BaseVolume: decimal.NewFromInt(1), IsClosed: true,
	}
}
