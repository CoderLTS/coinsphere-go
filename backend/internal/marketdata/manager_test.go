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
