package marketdata

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
)

// PostgresStore 是行情事实写入 PostgreSQL 的最小边界。
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore 使用调用方管理的连接池创建 Store。
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// UpsertInstrument 以自然键合并元数据，并保留首次生成的 UUIDv7。
func (store *PostgresStore) UpsertInstrument(ctx context.Context, metadata InstrumentMetadata) (Instrument, error) {
	if err := ValidateInstrumentMetadata(metadata); err != nil {
		return Instrument{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Instrument{}, err
	}

	var instrument Instrument
	err = store.db.QueryRowContext(ctx, `
INSERT INTO market_instruments (
    id, venue, market_type, native_symbol, base_asset, quote_asset, status,
    price_tick, quantity_step, min_quantity, min_notional, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (venue, market_type, native_symbol) DO UPDATE SET
    base_asset = EXCLUDED.base_asset,
    quote_asset = EXCLUDED.quote_asset,
    status = EXCLUDED.status,
    price_tick = EXCLUDED.price_tick,
    quantity_step = EXCLUDED.quantity_step,
    min_quantity = EXCLUDED.min_quantity,
    min_notional = EXCLUDED.min_notional,
    updated_at = EXCLUDED.updated_at
RETURNING id, venue, market_type, native_symbol, base_asset, quote_asset, status,
    price_tick, quantity_step, min_quantity, min_notional, updated_at
`, id, metadata.Venue, metadata.MarketType, metadata.NativeSymbol, metadata.BaseAsset, metadata.QuoteAsset,
		metadata.Status, metadata.PriceTick, metadata.QuantityStep, metadata.MinQuantity, metadata.MinNotional, metadata.UpdatedAt,
	).Scan(
		&instrument.ID, &instrument.Venue, &instrument.MarketType, &instrument.NativeSymbol, &instrument.BaseAsset,
		&instrument.QuoteAsset, &instrument.Status, &instrument.PriceTick, &instrument.QuantityStep,
		&instrument.MinQuantity, &instrument.MinNotional, &instrument.UpdatedAt,
	)
	if err != nil {
		return Instrument{}, err
	}
	instrument.UpdatedAt = instrument.UpdatedAt.UTC()
	return instrument, nil
}

// UpsertCandle 允许未闭合 K 线更新，首次闭合后由冲突条件冻结。
func (store *PostgresStore) UpsertCandle(ctx context.Context, candle Candle) (CandleWriteResult, error) {
	if err := ValidateCandle(candle); err != nil {
		return CandleWriteResult{}, err
	}
	if store == nil || store.db == nil {
		return CandleWriteResult{}, errors.New("postgres store is required")
	}

	var closed bool
	err := store.db.QueryRowContext(ctx, `
INSERT INTO market_candles (
    venue, instrument_id, interval_code, open_time, close_time,
    open_price, high_price, low_price, close_price, base_volume, is_closed
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (venue, instrument_id, interval_code, open_time) DO UPDATE SET
    close_time = EXCLUDED.close_time,
    open_price = EXCLUDED.open_price,
    high_price = EXCLUDED.high_price,
    low_price = EXCLUDED.low_price,
    close_price = EXCLUDED.close_price,
    base_volume = EXCLUDED.base_volume,
    is_closed = EXCLUDED.is_closed
WHERE NOT market_candles.is_closed
RETURNING is_closed
`, candle.Venue, candle.InstrumentID, candle.Interval, candle.OpenTime, candle.CloseTime, candle.Open,
		candle.High, candle.Low, candle.Close, candle.BaseVolume, candle.IsClosed,
	).Scan(&closed)
	if errors.Is(err, sql.ErrNoRows) {
		return CandleWriteResult{}, nil
	}
	if err != nil {
		return CandleWriteResult{}, err
	}
	return CandleWriteResult{Changed: true, FirstClosed: closed}, nil
}

// UpsertTicker 只以不早于当前快照的事件推进最新行情。
func (store *PostgresStore) UpsertTicker(ctx context.Context, ticker Ticker) error {
	if err := ValidateTicker(ticker); err != nil {
		return err
	}
	_, err := store.db.ExecContext(ctx, `
INSERT INTO market_ticker_snapshots (
    venue, instrument_id, occurred_at, last_price, best_bid_price, best_ask_price
) VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (venue, instrument_id) DO UPDATE SET
    occurred_at = EXCLUDED.occurred_at,
    last_price = EXCLUDED.last_price,
    best_bid_price = EXCLUDED.best_bid_price,
    best_ask_price = EXCLUDED.best_ask_price
WHERE EXCLUDED.occurred_at >= market_ticker_snapshots.occurred_at
`, ticker.Venue, ticker.InstrumentID, ticker.OccurredAt, ticker.LastPrice, ticker.BestBidPrice, ticker.BestAskPrice)
	return err
}
