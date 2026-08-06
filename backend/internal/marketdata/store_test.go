package marketdata_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"coinsphere/backend/internal/marketdata"
	"coinsphere/backend/internal/migration"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/shopspring/decimal"
)

const storeTestPostgresDSN = "COINSPHERE_TEST_POSTGRES_DSN"

func TestPostgresStoreUpserts(t *testing.T) {
	database := openStoreTestDatabase(t)
	store := marketdata.NewPostgresStore(database)
	metadata := marketdata.InstrumentMetadata{
		Venue:        marketdata.VenueBinance,
		MarketType:   marketdata.MarketTypeSpot,
		NativeSymbol: "BTCUSDT",
		BaseAsset:    "BTC",
		QuoteAsset:   "USDT",
		Status:       marketdata.InstrumentStatusTrading,
		PriceTick:    decimal.RequireFromString("0.1"),
		QuantityStep: decimal.RequireFromString("0.001"),
		MinQuantity:  decimal.RequireFromString("0.001"),
		MinNotional:  decimal.RequireFromString("5"),
		UpdatedAt:    time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC),
	}

	instrument, err := store.UpsertInstrument(context.Background(), metadata)
	if err != nil {
		t.Fatalf("insert instrument: %v", err)
	}
	firstID := instrument.ID
	metadata.Status = marketdata.InstrumentStatusSuspended
	metadata.PriceTick = decimal.RequireFromString("0.2")
	instrument, err = store.UpsertInstrument(context.Background(), metadata)
	if err != nil {
		t.Fatalf("update instrument: %v", err)
	}
	if instrument.ID != firstID || instrument.Status != metadata.Status || !instrument.PriceTick.Equal(metadata.PriceTick) ||
		!instrument.MinQuantity.Equal(metadata.MinQuantity) || !instrument.MinNotional.Equal(metadata.MinNotional) ||
		!instrument.UpdatedAt.Equal(metadata.UpdatedAt) {
		t.Fatalf("instrument upsert = %#v", instrument)
	}

	concurrentMetadata := metadata
	concurrentMetadata.NativeSymbol = "ETHUSDT"
	concurrentMetadata.BaseAsset = "ETH"
	concurrentMetadata.Status = marketdata.InstrumentStatusTrading
	const concurrentWriters = 8
	type instrumentResult struct {
		instrument marketdata.Instrument
		err        error
	}
	ready := make(chan struct{}, concurrentWriters)
	start := make(chan struct{})
	results := make(chan instrumentResult, concurrentWriters)
	for index := 0; index < concurrentWriters; index++ {
		go func() {
			ready <- struct{}{}
			<-start
			upserted, err := store.UpsertInstrument(context.Background(), concurrentMetadata)
			results <- instrumentResult{instrument: upserted, err: err}
		}()
	}
	// 屏障确保所有写入者同时竞争同一自然键，而不是串行碰撞。
	for index := 0; index < concurrentWriters; index++ {
		<-ready
	}
	close(start)
	concurrentID := ""
	for index := 0; index < concurrentWriters; index++ {
		result := <-results
		if result.err != nil {
			t.Fatalf("concurrent instrument upsert: %v", result.err)
		}
		if err := marketdata.ValidateUUIDv7(result.instrument.ID); err != nil {
			t.Fatalf("concurrent instrument ID: %v", err)
		}
		if concurrentID == "" {
			concurrentID = result.instrument.ID.String()
		} else if result.instrument.ID.String() != concurrentID {
			t.Fatalf("concurrent instrument IDs differ: %s and %s", concurrentID, result.instrument.ID)
		}
	}
	var concurrentCount int
	err = database.QueryRow(`
SELECT COUNT(*)
FROM market_instruments
WHERE venue = $1 AND market_type = $2 AND native_symbol = $3
`, concurrentMetadata.Venue, concurrentMetadata.MarketType, concurrentMetadata.NativeSymbol).Scan(&concurrentCount)
	if err != nil {
		t.Fatalf("count concurrent instruments: %v", err)
	}
	if concurrentCount != 1 {
		t.Fatalf("concurrent instrument count = %d, want 1", concurrentCount)
	}

	openTime := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	candle := marketdata.Candle{
		Venue:        instrument.Venue,
		InstrumentID: instrument.ID,
		Interval:     marketdata.CandleInterval1m,
		OpenTime:     openTime,
		CloseTime:    openTime.Add(time.Minute),
		Open:         decimal.NewFromInt(100),
		High:         decimal.NewFromInt(101),
		Low:          decimal.NewFromInt(99),
		Close:        decimal.NewFromInt(100),
		BaseVolume:   decimal.NewFromInt(1),
	}
	if err := store.UpsertCandle(context.Background(), candle); err != nil {
		t.Fatalf("insert open candle: %v", err)
	}
	closed := candle
	closed.High = decimal.NewFromInt(102)
	closed.Close = decimal.NewFromInt(101)
	closed.BaseVolume = decimal.NewFromInt(2)
	closed.IsClosed = true
	if err := store.UpsertCandle(context.Background(), closed); err != nil {
		t.Fatalf("close candle: %v", err)
	}
	frozen := closed
	frozen.High = decimal.NewFromInt(103)
	frozen.Close = decimal.NewFromInt(102)
	frozen.BaseVolume = decimal.NewFromInt(3)
	frozen.IsClosed = false
	if err := store.UpsertCandle(context.Background(), frozen); err != nil {
		t.Fatalf("rewrite closed candle: %v", err)
	}
	var high, closePrice, volume decimal.Decimal
	var isClosed bool
	err = database.QueryRow(`
SELECT high_price, close_price, base_volume, is_closed
FROM market_candles
WHERE venue = $1 AND instrument_id = $2 AND interval_code = $3 AND open_time = $4
`, candle.Venue, candle.InstrumentID, candle.Interval, candle.OpenTime).Scan(&high, &closePrice, &volume, &isClosed)
	if err != nil {
		t.Fatalf("read candle: %v", err)
	}
	if !isClosed || !high.Equal(closed.High) || !closePrice.Equal(closed.Close) || !volume.Equal(closed.BaseVolume) {
		t.Fatalf("closed candle was overwritten: high=%s close=%s volume=%s closed=%t", high, closePrice, volume, isClosed)
	}

	ticker := marketdata.Ticker{
		Venue:        instrument.Venue,
		InstrumentID: instrument.ID,
		OccurredAt:   openTime.Add(30 * time.Second),
		LastPrice:    decimal.NewFromInt(101),
		BestBidPrice: decimal.RequireFromString("100.9"),
		BestAskPrice: decimal.RequireFromString("101.1"),
	}
	if err := store.UpsertTicker(context.Background(), ticker); err != nil {
		t.Fatalf("insert ticker: %v", err)
	}
	olderTicker := ticker
	olderTicker.OccurredAt = ticker.OccurredAt.Add(-time.Second)
	olderTicker.LastPrice = decimal.NewFromInt(99)
	if err := store.UpsertTicker(context.Background(), olderTicker); err != nil {
		t.Fatalf("insert older ticker: %v", err)
	}
	var occurredAt time.Time
	var lastPrice decimal.Decimal
	err = database.QueryRow(`
SELECT occurred_at, last_price
FROM market_ticker_snapshots
WHERE venue = $1 AND instrument_id = $2
`, ticker.Venue, ticker.InstrumentID).Scan(&occurredAt, &lastPrice)
	if err != nil {
		t.Fatalf("read ticker: %v", err)
	}
	if !occurredAt.Equal(ticker.OccurredAt) || !lastPrice.Equal(ticker.LastPrice) {
		t.Fatalf("older ticker replaced snapshot: occurredAt=%s lastPrice=%s", occurredAt, lastPrice)
	}

	canceledContext, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.UpsertTicker(canceledContext, ticker); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context error = %v", err)
	}
}

func openStoreTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv(storeTestPostgresDSN))
	if dsn == "" {
		if os.Getenv("CI") != "" {
			t.Fatalf("%s is required in CI", storeTestPostgresDSN)
		}
		t.Skipf("%s is not configured", storeTestPostgresDSN)
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse PostgreSQL test DSN: %v", err)
	}
	admin := stdlib.OpenDB(*config)
	if err := admin.Ping(); err != nil {
		_ = admin.Close()
		t.Fatalf("ping PostgreSQL test database: %v", err)
	}
	schema := fmt.Sprintf("market_store_test_%d_%d", os.Getpid(), time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec("CREATE SCHEMA " + quotedSchema); err != nil {
		_ = admin.Close()
		t.Fatalf("create PostgreSQL test schema: %v", err)
	}

	testConfig := config.Copy()
	if testConfig.RuntimeParams == nil {
		testConfig.RuntimeParams = make(map[string]string)
	}
	testConfig.RuntimeParams["search_path"] = schema
	database := stdlib.OpenDB(*testConfig)
	if err := database.Ping(); err != nil {
		_ = database.Close()
		_, _ = admin.Exec("DROP SCHEMA " + quotedSchema + " CASCADE")
		_ = admin.Close()
		t.Fatalf("ping PostgreSQL test schema: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close PostgreSQL test schema: %v", err)
		}
		if _, err := admin.Exec("DROP SCHEMA " + quotedSchema + " CASCADE"); err != nil {
			t.Errorf("drop PostgreSQL test schema: %v", err)
		}
		if err := admin.Close(); err != nil {
			t.Errorf("close PostgreSQL admin database: %v", err)
		}
	})

	runner, err := migration.New(database)
	if err != nil {
		t.Fatalf("create migration runner: %v", err)
	}
	if _, err := runner.Up(context.Background(), 0); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return database
}
