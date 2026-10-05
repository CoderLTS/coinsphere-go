-- +goose Up
CREATE SCHEMA IF NOT EXISTS plugin_binance;


CREATE TABLE plugin_binance.instruments (
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

CREATE TABLE plugin_binance.candles (
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
CREATE INDEX ix_quant_candles_lookup
    ON plugin_binance.candles (market, instrument, interval, open_time DESC);


CREATE TABLE plugin_binance.instrument_sources (
    workflow_id BIGINT NOT NULL REFERENCES workflows (id) ON DELETE CASCADE,
    market VARCHAR(8) NOT NULL,
    symbol VARCHAR(32) NOT NULL,
    synced_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (workflow_id, market, symbol),
    CONSTRAINT fk_quant_instrument_source_instrument
        FOREIGN KEY (market, symbol)
        REFERENCES plugin_binance.instruments (market, symbol)
        ON DELETE CASCADE
);
CREATE INDEX ix_quant_instrument_sources_instrument
    ON plugin_binance.instrument_sources (market, symbol);

CREATE TABLE plugin_binance.orders (
    id BIGSERIAL PRIMARY KEY,
    workflow_id BIGINT NOT NULL,
    node_instance_id VARCHAR(128) NOT NULL,
    account VARCHAR(128) NOT NULL,
    market VARCHAR(8) NOT NULL,
    instrument VARCHAR(32) NOT NULL,
    provider_order_id VARCHAR(128) NOT NULL DEFAULT '',
    client_order_id VARCHAR(36) NOT NULL UNIQUE,
    side VARCHAR(4) NOT NULL,
    request_quantity NUMERIC(38,18) NOT NULL,
    request_quote_amount NUMERIC(38,18) NOT NULL,
    position_effect VARCHAR(8) NOT NULL,
    quantity NUMERIC(38,18) NOT NULL,
    executed NUMERIC(38,18) NOT NULL DEFAULT 0,
    average_price NUMERIC(38,18) NOT NULL DEFAULT 0,
    notional NUMERIC(38,18) NOT NULL DEFAULT 0,
    status VARCHAR(32) NOT NULL,
    mode VARCHAR(8) NOT NULL,
    operation_key VARCHAR(64) NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_binance_order_identity CHECK (
        BTRIM(node_instance_id) <> '' AND account ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$'
        AND BTRIM(instrument) <> '' AND instrument = UPPER(instrument)
        AND client_order_id ~ '^[A-Za-z0-9._:/-]{1,36}$' AND BTRIM(operation_key) <> ''
        AND BTRIM(status) <> ''
    ),
    CONSTRAINT ck_binance_order_market CHECK (market IN ('spot', 'usdm')),
    CONSTRAINT ck_binance_order_side CHECK (side IN ('buy', 'sell')),
    CONSTRAINT ck_binance_order_request CHECK (
        (request_quantity > 0 AND request_quote_amount = 0)
        OR (request_quantity = 0 AND request_quote_amount > 0)
    ),
    CONSTRAINT ck_binance_order_position_effect CHECK (position_effect IN ('open', 'reduce')),
    CONSTRAINT ck_binance_order_mode CHECK (mode IN ('paper', 'live')),
    CONSTRAINT ck_binance_order_amounts CHECK (
        quantity >= 0 AND executed >= 0 AND (quantity = 0 OR executed <= quantity)
        AND average_price >= 0 AND notional >= 0
    ),
    CONSTRAINT ck_binance_order_time CHECK (updated_at >= created_at)
);
CREATE INDEX ix_binance_orders_scope
    ON plugin_binance.orders (workflow_id, node_instance_id, created_at DESC, id DESC);
CREATE INDEX ix_binance_orders_account
    ON plugin_binance.orders (account, mode, market, created_at DESC, id DESC);

CREATE TABLE plugin_binance.fills (
    id BIGSERIAL PRIMARY KEY,
    order_id BIGINT NOT NULL REFERENCES plugin_binance.orders (id) ON DELETE RESTRICT,
    provider_trade_id VARCHAR(128) NOT NULL UNIQUE,
    quantity NUMERIC(38,18) NOT NULL,
    price NUMERIC(38,18) NOT NULL,
    fee NUMERIC(38,18) NOT NULL,
    fee_asset VARCHAR(32) NOT NULL,
    filled_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT ck_binance_fill_identity CHECK (BTRIM(provider_trade_id) <> ''),
    CONSTRAINT ck_binance_fill_amounts CHECK (quantity > 0 AND price > 0 AND fee >= 0),
    CONSTRAINT ck_binance_fill_fee_asset CHECK (fee = 0 OR BTRIM(fee_asset) <> '')
);
CREATE INDEX ix_binance_fills_order ON plugin_binance.fills (order_id, filled_at, id);

CREATE TABLE plugin_binance.fees (
    id BIGSERIAL PRIMARY KEY,
    fill_id BIGINT NOT NULL UNIQUE REFERENCES plugin_binance.fills (id) ON DELETE RESTRICT,
    amount NUMERIC(38,18) NOT NULL,
    asset VARCHAR(32) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_binance_fee_amount CHECK (amount >= 0),
    CONSTRAINT ck_binance_fee_asset CHECK (amount = 0 OR BTRIM(asset) <> '')
);

CREATE TABLE plugin_binance.paper_ledger_entries (
    id BIGSERIAL PRIMARY KEY,
    account VARCHAR(128) NOT NULL,
    operation_key VARCHAR(64) NOT NULL,
    entry_type VARCHAR(32) NOT NULL,
    amount NUMERIC(38,18) NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT ux_binance_paper_ledger_operation UNIQUE (operation_key, entry_type),
    CONSTRAINT ck_binance_paper_ledger_identity CHECK (
        account ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$' AND BTRIM(operation_key) <> ''
    ),
    CONSTRAINT ck_binance_paper_ledger_type CHECK (entry_type IN ('opening_balance', 'trade', 'fee'))
);
CREATE INDEX ix_binance_paper_ledger_account
    ON plugin_binance.paper_ledger_entries (account, occurred_at, id);

CREATE TABLE plugin_binance.positions (
    account VARCHAR(128) NOT NULL,
    mode VARCHAR(8) NOT NULL,
    market VARCHAR(8) NOT NULL,
    instrument VARCHAR(32) NOT NULL,
    quantity NUMERIC(38,18) NOT NULL,
    average_price NUMERIC(38,18) NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (account, mode, market, instrument),
    CONSTRAINT ck_binance_position_identity CHECK (
        account ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$'
        AND BTRIM(instrument) <> '' AND instrument = UPPER(instrument)
    ),
    CONSTRAINT ck_binance_position_mode CHECK (mode IN ('paper', 'live')),
    CONSTRAINT ck_binance_position_market CHECK (market IN ('spot', 'usdm')),
    CONSTRAINT ck_binance_position_price CHECK (average_price >= 0)
);

CREATE TABLE plugin_binance.account_snapshots (
    id BIGSERIAL PRIMARY KEY,
    account VARCHAR(128) NOT NULL,
    market VARCHAR(8) NOT NULL,
    asset VARCHAR(32) NOT NULL,
    equity NUMERIC(38,18) NOT NULL,
    available NUMERIC(38,18) NOT NULL,
    captured_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT ck_binance_snapshot_identity CHECK (
        account ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$'
        AND BTRIM(asset) <> '' AND asset = UPPER(asset)
    ),
    CONSTRAINT ck_binance_snapshot_market CHECK (market IN ('spot', 'usdm'))
);
CREATE INDEX ix_binance_account_snapshots_scope
    ON plugin_binance.account_snapshots (account, market, asset, captured_at DESC, id DESC);

CREATE TABLE plugin_binance.live_account_releases (
    account VARCHAR(128) NOT NULL,
    market VARCHAR(8) NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    confirmed_by BIGINT NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    confirmed_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (account, market),
    CONSTRAINT ck_binance_live_release_identity CHECK (
        account ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$'
    ),
    CONSTRAINT ck_binance_live_release_market CHECK (market IN ('spot', 'usdm')),
    CONSTRAINT ck_binance_live_release_time CHECK (updated_at >= confirmed_at)
);


-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'generation 4 rollback requires the matching database recovery point and image'; END $$;
-- +goose StatementEnd
