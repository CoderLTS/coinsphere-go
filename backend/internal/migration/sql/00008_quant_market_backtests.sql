-- +goose Up

CREATE SCHEMA plugin_quant;

CREATE TABLE plugin_quant.instruments (
    market VARCHAR(8) NOT NULL,
    symbol VARCHAR(32) NOT NULL,
    base_asset VARCHAR(32) NOT NULL,
    quote_asset VARCHAR(32) NOT NULL,
    status VARCHAR(32) NOT NULL,
    price_tick NUMERIC(38,18) NOT NULL,
    quantity_step NUMERIC(38,18) NOT NULL,
    min_quantity NUMERIC(38,18) NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (market, symbol),
    CONSTRAINT ck_quant_instrument_market CHECK (market IN ('spot', 'usdm')),
    CONSTRAINT ck_quant_instrument_symbol CHECK (BTRIM(symbol) <> '' AND symbol = UPPER(symbol)),
    CONSTRAINT ck_quant_instrument_assets CHECK (BTRIM(base_asset) <> '' AND BTRIM(quote_asset) <> ''),
    CONSTRAINT ck_quant_instrument_decimal CHECK (price_tick > 0 AND quantity_step > 0 AND min_quantity >= 0)
);

CREATE TABLE plugin_quant.candles (
    market VARCHAR(8) NOT NULL,
    instrument VARCHAR(32) NOT NULL,
    interval VARCHAR(8) NOT NULL,
    open_time TIMESTAMPTZ NOT NULL,
    close_time TIMESTAMPTZ NOT NULL,
    open NUMERIC(38,18) NOT NULL,
    high NUMERIC(38,18) NOT NULL,
    low NUMERIC(38,18) NOT NULL,
    close NUMERIC(38,18) NOT NULL,
    volume NUMERIC(38,18) NOT NULL,
    source_event_id VARCHAR(128) NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (market, instrument, interval, open_time),
    CONSTRAINT ux_quant_candle_event UNIQUE (open_time, source_event_id),
    CONSTRAINT ck_quant_candle_market CHECK (market IN ('spot', 'usdm')),
    CONSTRAINT ck_quant_candle_identity CHECK (
        BTRIM(instrument) <> '' AND instrument = UPPER(instrument)
        AND BTRIM(interval) <> '' AND BTRIM(source_event_id) <> ''
    ),
    CONSTRAINT ck_quant_candle_time CHECK (close_time > open_time),
    CONSTRAINT ck_quant_candle_prices CHECK (
        open > 0 AND high > 0 AND low > 0 AND close > 0 AND volume >= 0
        AND high >= GREATEST(open, close, low)
        AND low <= LEAST(open, close, high)
    )
);
SELECT create_hypertable('plugin_quant.candles', 'open_time', if_not_exists => TRUE);
CREATE INDEX ix_quant_candles_lookup
    ON plugin_quant.candles (market, instrument, interval, open_time DESC);

CREATE TABLE plugin_quant.backtests (
    id BIGSERIAL PRIMARY KEY,
    operation_key VARCHAR(64) NOT NULL UNIQUE,
    workflow_id BIGINT NOT NULL,
    revision_id BIGINT NOT NULL,
    node_instance_id VARCHAR(128) NOT NULL,
    strategy_id VARCHAR(128) NOT NULL,
    strategy_version VARCHAR(32) NOT NULL,
    market VARCHAR(8) NOT NULL,
    instrument VARCHAR(32) NOT NULL,
    interval VARCHAR(8) NOT NULL,
    start_time TIMESTAMPTZ NOT NULL,
    end_time TIMESTAMPTZ NOT NULL,
    initial_capital NUMERIC(38,18) NOT NULL,
    final_equity NUMERIC(38,18) NOT NULL,
    total_return NUMERIC(38,18) NOT NULL,
    max_drawdown NUMERIC(38,18) NOT NULL,
    total_fees NUMERIC(38,18) NOT NULL,
    trade_count INTEGER NOT NULL,
    candle_count INTEGER NOT NULL,
    parameters JSONB NOT NULL,
    data_manifest JSONB NOT NULL,
    detail_sha256 VARCHAR(64) NOT NULL,
    detail_size_bytes BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_quant_backtest_identity CHECK (
        BTRIM(node_instance_id) <> '' AND BTRIM(strategy_id) <> ''
        AND BTRIM(instrument) <> '' AND BTRIM(interval) <> ''
    ),
    CONSTRAINT ck_quant_backtest_market CHECK (market IN ('spot', 'usdm')),
    CONSTRAINT ck_quant_backtest_time CHECK (end_time > start_time),
    CONSTRAINT ck_quant_backtest_amounts CHECK (
        initial_capital > 0 AND final_equity >= 0 AND total_fees >= 0
        AND max_drawdown >= 0 AND max_drawdown <= 1
        AND trade_count >= 0 AND candle_count > 0 AND detail_size_bytes > 0
    ),
    CONSTRAINT ck_quant_backtest_json CHECK (
        jsonb_typeof(parameters) = 'object' AND jsonb_typeof(data_manifest) = 'object'
    ),
    CONSTRAINT ck_quant_backtest_digest CHECK (detail_sha256 ~ '^[0-9a-f]{64}$')
);
CREATE INDEX ix_quant_backtests_created ON plugin_quant.backtests (created_at DESC, id DESC);
CREATE INDEX ix_quant_backtests_scope
    ON plugin_quant.backtests (market, instrument, interval, created_at DESC, id DESC);

-- +goose Down

LOCK TABLE plugin_quant.instruments, plugin_quant.candles, plugin_quant.backtests IN ACCESS EXCLUSIVE MODE;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM plugin_quant.instruments LIMIT 1)
        OR EXISTS (SELECT 1 FROM plugin_quant.candles LIMIT 1)
        OR EXISTS (SELECT 1 FROM plugin_quant.backtests LIMIT 1)
    THEN
        RAISE EXCEPTION 'refusing to roll back Quant data';
    END IF;
END
$$;
-- +goose StatementEnd

DROP SCHEMA plugin_quant CASCADE;
