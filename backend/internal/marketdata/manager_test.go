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
	snapshot  func(context.Context, marketdata.MarketType) ([]marketdata.InstrumentMetadata, error)
	fetch     func(context.Context, marketdata.CandlePageRequest) (marketdata.CandlePage, error)
	subscribe func(context.Context, marketdata.Instrument, marketdata.CandleInterval, marketdata.CandleHandler) error
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
	var ownerID, recordID int64
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
	const strategyID = "019d5000-0000-7000-8000-000000000001"
	const versionID = "019d5000-0000-7000-8000-000000000002"
	const publishTaskID = "019d5000-0000-7000-8000-000000000003"
	const instanceID = "019d5000-0000-7000-8000-000000000004"
	const sourceCode = "def on_bar(candles, params): return Decimal('0')"
	if _, err := database.Exec(`
INSERT INTO strategies (
    id, name, source_code, market_type, instrument_id, interval_code, lookback_bars,
    parameter_schema_json, created_by_user_id, updated_by_user_id
) VALUES ($1, 'manager strategy', $2, 'spot', $3, '1m', 1, '{}', $4, $4)
`, strategyID, sourceCode, instrument.ID, ownerID); err != nil {
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
    name, source_code, code_sha256, runtime_version, market_type, instrument_id, symbol,
    interval_code, lookback_bars, parameter_schema_json, published_by_user_id, published_at
) VALUES ($1, $2, 1, 'published', $3, $4, 'manager strategy', $5, repeat('a', 64),
          'python3.12', 'spot', $6, 'BTCUSDT', '1m', 1, '{}', $7, CURRENT_TIMESTAMP)
`, versionID, strategyID, publishTaskID, recordID, sourceCode, instrument.ID, ownerID); err != nil {
		t.Fatalf("insert strategy version: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO strategy_instances (
    id, owner_user_id, strategy_version_id, name, mode, environment, is_enabled
) VALUES ($1, $2, $3, 'enabled manager strategy', 'signal_only', 'paper', TRUE)
`, instanceID, ownerID, versionID); err != nil {
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
