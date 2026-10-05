-- +goose Up
CREATE SCHEMA IF NOT EXISTS plugin_quant;
CREATE TABLE plugin_quant.backtests (
    id BIGSERIAL PRIMARY KEY,
    operation_key VARCHAR(64) NOT NULL UNIQUE,
    workflow_id BIGINT NOT NULL,
    revision_id BIGINT NOT NULL,
    node_instance_id VARCHAR(128) NOT NULL,
    strategy_id VARCHAR(128) NOT NULL,
    strategy_version VARCHAR(32) NOT NULL,
    venue VARCHAR(32) NOT NULL CHECK (venue ~ '^[a-z][a-z0-9_-]{1,31}$'),
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
    detail JSONB NOT NULL,
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
        AND trade_count >= 0 AND candle_count > 0
    ),
    CONSTRAINT ck_quant_backtest_json CHECK (
        jsonb_typeof(parameters) = 'object' AND jsonb_typeof(data_manifest) = 'object' AND jsonb_typeof(detail)='object'
    )
);
CREATE INDEX ix_quant_backtests_created ON plugin_quant.backtests (created_at DESC, id DESC);
CREATE INDEX ix_quant_backtests_scope
    ON plugin_quant.backtests (market, instrument, interval, created_at DESC, id DESC);

CREATE TABLE plugin_quant.signals (
    id BIGSERIAL PRIMARY KEY,
    operation_key VARCHAR(64) NOT NULL UNIQUE,
    workflow_id BIGINT NOT NULL,
    revision_id BIGINT NOT NULL,
    node_instance_id VARCHAR(128) NOT NULL,
    strategy_id VARCHAR(128) NOT NULL,
    strategy_version VARCHAR(32) NOT NULL,
    venue VARCHAR(32) NOT NULL CHECK (venue ~ '^[a-z][a-z0-9_-]{1,31}$'),
    market VARCHAR(8) NOT NULL,
    instrument VARCHAR(32) NOT NULL,
    business_key VARCHAR(256) NOT NULL,
    target NUMERIC(38,18) NOT NULL,
    evaluated_at TIMESTAMPTZ NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    superseded_by BIGINT REFERENCES plugin_quant.signals (id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    decided_at TIMESTAMPTZ,
    executed_at TIMESTAMPTZ,
    CONSTRAINT ck_quant_signal_identity CHECK (
        BTRIM(node_instance_id) <> '' AND BTRIM(strategy_id) <> ''
        AND BTRIM(instrument) <> '' AND BTRIM(business_key) <> ''
    ),
    CONSTRAINT ck_quant_signal_market CHECK (market IN ('spot', 'usdm')),
    CONSTRAINT ck_quant_signal_target CHECK (target BETWEEN -1 AND 1),
    CONSTRAINT ck_quant_signal_status CHECK (status IN ('pending', 'superseded', 'approved', 'rejected', 'executed')),
    CONSTRAINT ck_quant_signal_times CHECK (
        (status = 'pending' AND decided_at IS NULL AND executed_at IS NULL)
        OR (status = 'superseded' AND decided_at IS NOT NULL AND executed_at IS NULL)
        OR (status IN ('approved', 'rejected') AND decided_at IS NOT NULL AND executed_at IS NULL)
        OR (status = 'executed' AND decided_at IS NOT NULL AND executed_at IS NOT NULL)
    )
);
CREATE UNIQUE INDEX ux_quant_signal_pending_business
    ON plugin_quant.signals (workflow_id, node_instance_id, business_key)
    WHERE status = 'pending';
CREATE INDEX ix_quant_signals_scope
    ON plugin_quant.signals (market, instrument, created_at DESC, id DESC);


CREATE TABLE plugin_quant.market_signals (
    id BIGSERIAL PRIMARY KEY,
    operation_key VARCHAR(64) NOT NULL UNIQUE,
    workflow_id BIGINT NOT NULL,
    revision_id BIGINT NOT NULL,
    node_instance_id VARCHAR(128) NOT NULL,
    venue VARCHAR(32) NOT NULL CHECK (venue ~ '^[a-z][a-z0-9_-]{1,31}$'),
    market VARCHAR(8) NOT NULL,
    instrument VARCHAR(32) NOT NULL,
    interval VARCHAR(8) NOT NULL,
    name VARCHAR(80) NOT NULL,
    indicator VARCHAR(32) NOT NULL,
    candle_close_time TIMESTAMPTZ NOT NULL,
    summary VARCHAR(2000) NOT NULL,
    "values" JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_quant_market_signal_identity CHECK (
        BTRIM(node_instance_id) <> '' AND BTRIM(instrument) <> ''
        AND instrument = UPPER(instrument) AND BTRIM(interval) <> '' AND BTRIM(name) <> ''
    ),
    CONSTRAINT ck_quant_market_signal_market CHECK (market IN ('spot', 'usdm')),
    CONSTRAINT ck_quant_market_signal_indicator CHECK (
        indicator IN ('volume_spike', 'price_change', 'macd', 'kdj', 'rsi', 'bollinger')
    ),
    CONSTRAINT ck_quant_market_signal_values CHECK (jsonb_typeof("values") = 'object')
);

CREATE INDEX ix_quant_market_signals_scope
    ON plugin_quant.market_signals (market, instrument, interval, candle_close_time DESC, id DESC);


-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'generation 4 rollback requires the matching database recovery point and image'; END $$;
-- +goose StatementEnd
