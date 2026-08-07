-- +goose Up
ALTER TABLE worker_tasks
    ADD CONSTRAINT ck_worker_tasks_quant_lane CHECK (
        task_type NOT IN ('strategy.publish', 'strategy.backtest')
        OR lane = 'backtest'
    ),
    ADD CONSTRAINT ck_worker_tasks_quant_payload CHECK (
        task_type NOT IN ('strategy.publish', 'strategy.backtest')
        OR CASE task_type
            WHEN 'strategy.publish' THEN
                jsonb_typeof(payload_json::jsonb) = 'object'
                AND payload_json::jsonb ?& ARRAY['strategyId', 'strategyVersionId']
                AND payload_json::jsonb - 'strategyId' - 'strategyVersionId' = '{}'::jsonb
                AND COALESCE(
                    payload_json::jsonb ->> 'strategyId'
                        ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$',
                    FALSE
                )
                AND COALESCE(
                    payload_json::jsonb ->> 'strategyVersionId'
                        ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$',
                    FALSE
                )
            WHEN 'strategy.backtest' THEN
                jsonb_typeof(payload_json::jsonb) = 'object'
                AND payload_json::jsonb ? 'backtestId'
                AND payload_json::jsonb - 'backtestId' = '{}'::jsonb
                AND COALESCE(
                    payload_json::jsonb ->> 'backtestId'
                        ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$',
                    FALSE
                )
            ELSE TRUE
        END
    );

CREATE TABLE strategies (
    id UUID NOT NULL,
    name VARCHAR(120) NOT NULL,
    source_code TEXT NOT NULL,
    market_type VARCHAR(32) NOT NULL,
    instrument_id UUID NOT NULL,
    interval_code VARCHAR(4) NOT NULL,
    lookback_bars INTEGER NOT NULL,
    parameter_schema_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    runtime_version VARCHAR(32) NOT NULL DEFAULT 'python3.12',
    created_by_user_id BIGINT NOT NULL,
    updated_by_user_id BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT strategies_pkey PRIMARY KEY (id),
    CONSTRAINT fk_strategies_instrument
        FOREIGN KEY (instrument_id) REFERENCES market_instruments (id) ON DELETE RESTRICT,
    CONSTRAINT fk_strategies_created_by
        FOREIGN KEY (created_by_user_id) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT fk_strategies_updated_by
        FOREIGN KEY (updated_by_user_id) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT ck_strategies_id_uuidv7 CHECK (
        id::text ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
    ),
    CONSTRAINT ck_strategies_name CHECK (name = BTRIM(name) AND name <> ''),
    CONSTRAINT ck_strategies_source CHECK (
        OCTET_LENGTH(source_code) BETWEEN 1 AND 65536
        AND BTRIM(source_code) <> ''
    ),
    CONSTRAINT ck_strategies_market CHECK (market_type IN ('spot', 'usd_m')),
    CONSTRAINT ck_strategies_interval CHECK (interval_code IN ('1m', '5m', '15m', '1h', '4h', '1d')),
    CONSTRAINT ck_strategies_lookback CHECK (lookback_bars BETWEEN 1 AND 10000),
    CONSTRAINT ck_strategies_parameter_schema CHECK (jsonb_typeof(parameter_schema_json) = 'object'),
    CONSTRAINT ck_strategies_runtime CHECK (runtime_version = 'python3.12'),
    CONSTRAINT ck_strategies_times CHECK (isfinite(created_at) AND isfinite(updated_at))
);

CREATE TABLE strategy_versions (
    id UUID NOT NULL,
    strategy_id UUID NOT NULL,
    version_number INTEGER NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    worker_task_id VARCHAR(36) NOT NULL,
    idempotency_record_id BIGINT NOT NULL,
    name VARCHAR(120) NOT NULL,
    source_code TEXT NOT NULL,
    code_sha256 CHAR(64) NOT NULL,
    runtime_version VARCHAR(32) NOT NULL,
    market_type VARCHAR(32) NOT NULL,
    instrument_id UUID NOT NULL,
    symbol VARCHAR(64) NOT NULL,
    interval_code VARCHAR(4) NOT NULL,
    lookback_bars INTEGER NOT NULL,
    parameter_schema_json JSONB NOT NULL,
    published_by_user_id BIGINT NOT NULL,
    published_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT strategy_versions_pkey PRIMARY KEY (id),
    CONSTRAINT fk_strategy_versions_strategy
        FOREIGN KEY (strategy_id) REFERENCES strategies (id) ON DELETE RESTRICT,
    CONSTRAINT fk_strategy_versions_instrument
        FOREIGN KEY (instrument_id) REFERENCES market_instruments (id) ON DELETE RESTRICT,
    CONSTRAINT fk_strategy_versions_worker_task
        FOREIGN KEY (worker_task_id) REFERENCES worker_tasks (id) ON DELETE RESTRICT,
    CONSTRAINT fk_strategy_versions_idempotency
        FOREIGN KEY (idempotency_record_id) REFERENCES idempotency_records (id) ON DELETE RESTRICT,
    CONSTRAINT fk_strategy_versions_published_by
        FOREIGN KEY (published_by_user_id) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT uq_strategy_versions_number UNIQUE (strategy_id, version_number),
    CONSTRAINT uq_strategy_versions_worker_task UNIQUE (worker_task_id),
    CONSTRAINT uq_strategy_versions_idempotency UNIQUE (idempotency_record_id),
    CONSTRAINT ck_strategy_versions_id_uuidv7 CHECK (
        id::text ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
    ),
    CONSTRAINT ck_strategy_versions_number CHECK (version_number > 0),
    CONSTRAINT ck_strategy_versions_status CHECK (status IN ('pending', 'published', 'failed')),
    CONSTRAINT ck_strategy_versions_name CHECK (name = BTRIM(name) AND name <> ''),
    CONSTRAINT ck_strategy_versions_source CHECK (
        OCTET_LENGTH(source_code) BETWEEN 1 AND 65536
        AND BTRIM(source_code) <> ''
    ),
    CONSTRAINT ck_strategy_versions_sha256 CHECK (code_sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT ck_strategy_versions_runtime CHECK (runtime_version = 'python3.12'),
    CONSTRAINT ck_strategy_versions_market CHECK (market_type IN ('spot', 'usd_m')),
    CONSTRAINT ck_strategy_versions_symbol CHECK (symbol ~ '^[A-Z0-9][A-Z0-9._-]*$'),
    CONSTRAINT ck_strategy_versions_interval CHECK (interval_code IN ('1m', '5m', '15m', '1h', '4h', '1d')),
    CONSTRAINT ck_strategy_versions_lookback CHECK (lookback_bars BETWEEN 1 AND 10000),
    CONSTRAINT ck_strategy_versions_parameter_schema CHECK (jsonb_typeof(parameter_schema_json) = 'object'),
    CONSTRAINT ck_strategy_versions_published_at CHECK (
        isfinite(created_at)
        AND (
            (status = 'published' AND published_at IS NOT NULL AND isfinite(published_at))
            OR (status <> 'published' AND published_at IS NULL)
        )
    )
);

-- +goose StatementBegin
CREATE FUNCTION reject_strategy_version_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'strategy version mutation is not allowed';
    ELSIF OLD.status <> 'pending'
        OR NEW.status NOT IN ('published', 'failed')
        OR NEW.id IS DISTINCT FROM OLD.id
        OR NEW.strategy_id IS DISTINCT FROM OLD.strategy_id
        OR NEW.version_number IS DISTINCT FROM OLD.version_number
        OR NEW.worker_task_id IS DISTINCT FROM OLD.worker_task_id
        OR NEW.idempotency_record_id IS DISTINCT FROM OLD.idempotency_record_id
        OR NEW.name IS DISTINCT FROM OLD.name
        OR NEW.source_code IS DISTINCT FROM OLD.source_code
        OR NEW.code_sha256 IS DISTINCT FROM OLD.code_sha256
        OR NEW.runtime_version IS DISTINCT FROM OLD.runtime_version
        OR NEW.market_type IS DISTINCT FROM OLD.market_type
        OR NEW.instrument_id IS DISTINCT FROM OLD.instrument_id
        OR NEW.symbol IS DISTINCT FROM OLD.symbol
        OR NEW.interval_code IS DISTINCT FROM OLD.interval_code
        OR NEW.lookback_bars IS DISTINCT FROM OLD.lookback_bars
        OR NEW.parameter_schema_json IS DISTINCT FROM OLD.parameter_schema_json
        OR NEW.published_by_user_id IS DISTINCT FROM OLD.published_by_user_id
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
        OR (NEW.status = 'published' AND NEW.published_at IS NULL)
        OR (NEW.status = 'failed' AND NEW.published_at IS NOT NULL) THEN
        RAISE EXCEPTION 'strategy version mutation is not allowed';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER strategy_versions_immutable
BEFORE UPDATE OR DELETE ON strategy_versions
FOR EACH ROW EXECUTE FUNCTION reject_strategy_version_mutation();

CREATE TABLE backtests (
    id UUID NOT NULL,
    owner_user_id BIGINT NOT NULL,
    strategy_version_id UUID NOT NULL,
    worker_task_id VARCHAR(36) NOT NULL,
    idempotency_record_id BIGINT NOT NULL,
    simulator_version VARCHAR(32) NOT NULL,
    parameters_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    start_time TIMESTAMPTZ NOT NULL,
    end_time TIMESTAMPTZ NOT NULL,
    allocation_usdt NUMERIC(38,18) NOT NULL,
    initial_equity NUMERIC(38,18) NOT NULL,
    fee_rate NUMERIC(38,18) NOT NULL,
    slippage_rate NUMERIC(38,18) NOT NULL,
    funding_rates_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    stop_loss_ratio NUMERIC(38,18),
    maintenance_margin_ratio NUMERIC(38,18),
    summary_json JSONB,
    input_sha256 CHAR(64),
    result_sha256 CHAR(64),
    manifest_sha256 CHAR(64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT backtests_pkey PRIMARY KEY (id),
    CONSTRAINT fk_backtests_owner
        FOREIGN KEY (owner_user_id) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT fk_backtests_strategy_version
        FOREIGN KEY (strategy_version_id) REFERENCES strategy_versions (id) ON DELETE RESTRICT,
    CONSTRAINT fk_backtests_worker_task
        FOREIGN KEY (worker_task_id) REFERENCES worker_tasks (id) ON DELETE RESTRICT,
    CONSTRAINT fk_backtests_idempotency
        FOREIGN KEY (idempotency_record_id) REFERENCES idempotency_records (id) ON DELETE RESTRICT,
    CONSTRAINT uq_backtests_worker_task UNIQUE (worker_task_id),
    CONSTRAINT uq_backtests_idempotency UNIQUE (idempotency_record_id),
    CONSTRAINT ck_backtests_simulator_version CHECK (simulator_version = 'decimal-bar-v1'),
    CONSTRAINT ck_backtests_id_uuidv7 CHECK (
        id::text ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
    ),
    CONSTRAINT ck_backtests_parameters CHECK (jsonb_typeof(parameters_json) = 'object'),
    CONSTRAINT ck_backtests_window CHECK (
        isfinite(start_time) AND isfinite(end_time) AND start_time < end_time
    ),
    CONSTRAINT ck_backtests_amounts CHECK (
        allocation_usdt > 0
        AND initial_equity > 0
        AND allocation_usdt <= initial_equity
    ),
    CONSTRAINT ck_backtests_rates CHECK (
        fee_rate >= 0 AND fee_rate < 1
        AND slippage_rate >= 0 AND slippage_rate < 1
        AND (stop_loss_ratio IS NULL OR stop_loss_ratio > 0 AND stop_loss_ratio < 1)
        AND (
            maintenance_margin_ratio IS NULL
            OR maintenance_margin_ratio > 0 AND maintenance_margin_ratio < 1
        )
    ),
    CONSTRAINT ck_backtests_funding_rates CHECK (jsonb_typeof(funding_rates_json) = 'array'),
    CONSTRAINT ck_backtests_results CHECK (
        (summary_json IS NULL AND input_sha256 IS NULL AND result_sha256 IS NULL AND manifest_sha256 IS NULL)
        OR
        (
            jsonb_typeof(summary_json) = 'object'
            AND input_sha256 ~ '^[0-9a-f]{64}$'
            AND result_sha256 ~ '^[0-9a-f]{64}$'
            AND manifest_sha256 ~ '^[0-9a-f]{64}$'
        )
    ),
    CONSTRAINT ck_backtests_created_at CHECK (isfinite(created_at))
);

CREATE INDEX ix_strategy_versions_published
    ON strategy_versions (published_at DESC, id DESC);
CREATE INDEX ix_backtests_owner_created
    ON backtests (owner_user_id, created_at DESC, id DESC);

-- +goose Down
LOCK TABLE worker_tasks, backtests, strategy_versions, strategies IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE m1_4_strategy_runtime_down_guard (
    row_count BIGINT NOT NULL CHECK (row_count = 0)
) ON COMMIT DROP;

INSERT INTO m1_4_strategy_runtime_down_guard (row_count)
SELECT
    (SELECT COUNT(*) FROM strategies)
    + (SELECT COUNT(*) FROM strategy_versions)
    + (SELECT COUNT(*) FROM backtests)
    + (SELECT COUNT(*) FROM worker_tasks WHERE task_type IN ('strategy.publish', 'strategy.backtest'));

ALTER TABLE worker_tasks
    DROP CONSTRAINT ck_worker_tasks_quant_payload,
    DROP CONSTRAINT ck_worker_tasks_quant_lane;
DROP TABLE backtests;
DROP TRIGGER strategy_versions_immutable ON strategy_versions;
DROP FUNCTION reject_strategy_version_mutation();
DROP TABLE strategy_versions;
DROP TABLE strategies;
