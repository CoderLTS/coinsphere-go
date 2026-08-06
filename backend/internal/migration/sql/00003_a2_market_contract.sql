-- +goose Up
CREATE EXTENSION IF NOT EXISTS timescaledb WITH SCHEMA public;

CREATE TABLE market_instruments (
    id UUID NOT NULL,
    venue VARCHAR(16) NOT NULL,
    market_type VARCHAR(32) NOT NULL,
    native_symbol VARCHAR(64) NOT NULL,
    base_asset VARCHAR(32) NOT NULL,
    quote_asset VARCHAR(32) NOT NULL,
    status VARCHAR(16) NOT NULL,
    price_tick NUMERIC(38,18) NOT NULL,
    quantity_step NUMERIC(38,18) NOT NULL,
    min_quantity NUMERIC(38,18) NOT NULL,
    min_notional NUMERIC(38,18) NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT market_instruments_pkey PRIMARY KEY (id),
    CONSTRAINT uq_market_instruments_natural_key
        UNIQUE (venue, market_type, native_symbol),
    CONSTRAINT uq_market_instruments_venue_id
        UNIQUE (venue, id),
    CONSTRAINT ck_market_instruments_id_uuidv7
        CHECK (id::text ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
    CONSTRAINT ck_market_instruments_venue
        CHECK (venue = 'binance'),
    CONSTRAINT ck_market_instruments_market_type
        CHECK (market_type IN ('spot', 'usd_m')),
    CONSTRAINT ck_market_instruments_native_symbol
        CHECK (native_symbol ~ '^[A-Z0-9][A-Z0-9._-]*$'),
    CONSTRAINT ck_market_instruments_base_asset
        CHECK (
            base_asset ~ '^[A-Z0-9][A-Z0-9._-]*$'
            AND base_asset = UPPER(base_asset)
        ),
    CONSTRAINT ck_market_instruments_quote_asset
        CHECK (
            quote_asset ~ '^[A-Z0-9][A-Z0-9._-]*$'
            AND quote_asset = UPPER(quote_asset)
        ),
    CONSTRAINT ck_market_instruments_status
        CHECK (status IN ('trading', 'suspended')),
    CONSTRAINT ck_market_instruments_steps
        CHECK (
            price_tick > 0
            AND quantity_step > 0
            AND min_quantity > 0
            AND min_notional > 0
            AND isfinite(updated_at)
        )
);

CREATE TABLE market_candles (
    venue VARCHAR(16) NOT NULL,
    instrument_id UUID NOT NULL,
    interval_code VARCHAR(4) NOT NULL,
    open_time TIMESTAMPTZ NOT NULL,
    close_time TIMESTAMPTZ NOT NULL,
    open_price NUMERIC(38,18) NOT NULL,
    high_price NUMERIC(38,18) NOT NULL,
    low_price NUMERIC(38,18) NOT NULL,
    close_price NUMERIC(38,18) NOT NULL,
    base_volume NUMERIC(38,18) NOT NULL,
    is_closed BOOLEAN NOT NULL,
    CONSTRAINT market_candles_pkey
        PRIMARY KEY (venue, instrument_id, interval_code, open_time),
    CONSTRAINT fk_market_candles_instrument
        FOREIGN KEY (venue, instrument_id)
        REFERENCES market_instruments (venue, id)
        ON DELETE RESTRICT,
    CONSTRAINT ck_market_candles_venue
        CHECK (venue = 'binance'),
    CONSTRAINT ck_market_candles_interval
        CHECK (interval_code IN ('1m', '5m', '15m', '1h', '4h', '1d')),
    CONSTRAINT ck_market_candles_time
        CHECK (
            isfinite(open_time)
            AND isfinite(close_time)
            AND date_bin(
                CASE interval_code
                    WHEN '1m' THEN INTERVAL '1 minute'
                    WHEN '5m' THEN INTERVAL '5 minutes'
                    WHEN '15m' THEN INTERVAL '15 minutes'
                    WHEN '1h' THEN INTERVAL '1 hour'
                    WHEN '4h' THEN INTERVAL '4 hours'
                    WHEN '1d' THEN INTERVAL '1 day'
                END,
                open_time,
                TIMESTAMPTZ '1970-01-01 00:00:00+00'
            ) = open_time
            AND close_time = open_time + CASE interval_code
                WHEN '1m' THEN INTERVAL '1 minute'
                WHEN '5m' THEN INTERVAL '5 minutes'
                WHEN '15m' THEN INTERVAL '15 minutes'
                WHEN '1h' THEN INTERVAL '1 hour'
                WHEN '4h' THEN INTERVAL '4 hours'
                WHEN '1d' THEN INTERVAL '1 day'
            END
        ),
    CONSTRAINT ck_market_candles_prices
        CHECK (
            open_price > 0
            AND high_price > 0
            AND low_price > 0
            AND close_price > 0
        ),
    CONSTRAINT ck_market_candles_volume
        CHECK (base_volume >= 0),
    CONSTRAINT ck_market_candles_ohlc
        CHECK (
            low_price <= LEAST(open_price, close_price)
            AND high_price >= GREATEST(open_price, close_price)
        )
) WITH (
    tsdb.hypertable = true,
    tsdb.columnstore = false,
    tsdb.partition_column = 'open_time',
    tsdb.chunk_interval = '7 days',
    tsdb.create_default_indexes = false
);

ALTER TABLE market_candles SET (
    timescaledb.enable_columnstore,
    timescaledb.orderby = 'open_time DESC',
    timescaledb.segmentby = 'venue,instrument_id,interval_code'
);

CALL public.add_columnstore_policy(
    'market_candles',
    after => INTERVAL '30 days'
);

SELECT public.add_retention_policy(
    'market_candles',
    drop_after => INTERVAL '2 years'
);

CREATE TABLE market_ticker_snapshots (
    venue VARCHAR(16) NOT NULL,
    instrument_id UUID NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    last_price NUMERIC(38,18) NOT NULL,
    best_bid_price NUMERIC(38,18) NOT NULL,
    best_ask_price NUMERIC(38,18) NOT NULL,
    CONSTRAINT market_ticker_snapshots_pkey
        PRIMARY KEY (venue, instrument_id),
    CONSTRAINT fk_market_ticker_snapshots_instrument
        FOREIGN KEY (venue, instrument_id)
        REFERENCES market_instruments (venue, id)
        ON DELETE RESTRICT,
    CONSTRAINT ck_market_ticker_snapshots_venue
        CHECK (venue = 'binance'),
    CONSTRAINT ck_market_ticker_snapshots_time
        CHECK (isfinite(occurred_at)),
    CONSTRAINT ck_market_ticker_snapshots_prices
        CHECK (
            last_price > 0
            AND best_bid_price > 0
            AND best_ask_price > 0
        ),
    CONSTRAINT ck_market_ticker_snapshots_spread
        CHECK (best_bid_price <= best_ask_price)
);

-- +goose Down
-- Down 先锁定三张行情表，再以空表 guard 保护无损回滚。
LOCK TABLE
    market_candles,
    market_ticker_snapshots,
    market_instruments
IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE a2_market_contract_down_guard (
    row_count BIGINT NOT NULL CHECK (row_count = 0)
) ON COMMIT DROP;

INSERT INTO a2_market_contract_down_guard (row_count)
SELECT
    (SELECT COUNT(*) FROM market_candles)
    + (SELECT COUNT(*) FROM market_ticker_snapshots)
    + (SELECT COUNT(*) FROM market_instruments);

DROP TABLE market_candles;
DROP TABLE market_ticker_snapshots;
DROP TABLE market_instruments;
