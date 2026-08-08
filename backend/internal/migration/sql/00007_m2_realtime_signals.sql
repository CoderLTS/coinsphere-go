-- +goose Up
ALTER TABLE worker_tasks
    ADD COLUMN dedupe_key VARCHAR(255),
    ADD CONSTRAINT ck_worker_tasks_realtime_lane CHECK (
        task_type <> 'strategy.realtime' OR lane = 'realtime'
    ),
    ADD CONSTRAINT ck_worker_tasks_realtime_dedupe CHECK (
        task_type <> 'strategy.realtime' OR dedupe_key IS NOT NULL
    ),
    ADD CONSTRAINT ck_worker_tasks_realtime_payload CHECK (
        CASE task_type
        WHEN 'strategy.realtime' THEN (
            jsonb_typeof(payload_json::jsonb) = 'object'
            AND payload_json::jsonb ?& ARRAY['instanceId', 'candleOpenTime']
            AND payload_json::jsonb - 'instanceId' - 'candleOpenTime' = '{}'::jsonb
            AND COALESCE(
                payload_json::jsonb ->> 'instanceId'
                    ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$',
                FALSE
            )
            AND COALESCE(
                payload_json::jsonb ->> 'candleOpenTime'
                    ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?Z$',
                FALSE
            )
        )
        ELSE TRUE
        END
    );

CREATE UNIQUE INDEX ux_worker_tasks_type_dedupe
    ON worker_tasks (task_type, dedupe_key)
    WHERE dedupe_key IS NOT NULL;

CREATE TABLE strategy_instances (
    id UUID NOT NULL,
    owner_user_id BIGINT NOT NULL,
    strategy_version_id UUID NOT NULL,
    name VARCHAR(120) NOT NULL,
    mode VARCHAR(16) NOT NULL DEFAULT 'signal_only',
    environment VARCHAR(16) NOT NULL DEFAULT 'paper',
    parameters_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    is_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT strategy_instances_pkey PRIMARY KEY (id),
    CONSTRAINT fk_strategy_instances_owner
        FOREIGN KEY (owner_user_id) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT fk_strategy_instances_version
        FOREIGN KEY (strategy_version_id) REFERENCES strategy_versions (id) ON DELETE RESTRICT,
    CONSTRAINT ck_strategy_instances_id_uuidv7 CHECK (
        id::text ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
    ),
    CONSTRAINT ck_strategy_instances_name CHECK (name = BTRIM(name) AND name <> ''),
    CONSTRAINT ck_strategy_instances_mode CHECK (mode IN ('signal_only', 'manual', 'auto')),
    CONSTRAINT ck_strategy_instances_environment CHECK (environment IN ('paper', 'testnet', 'live')),
    CONSTRAINT ck_strategy_instances_parameters CHECK (jsonb_typeof(parameters_json) = 'object'),
    CONSTRAINT ck_strategy_instances_times CHECK (isfinite(created_at) AND isfinite(updated_at))
);

CREATE TABLE strategy_signals (
    id UUID NOT NULL,
    owner_user_id BIGINT NOT NULL,
    strategy_instance_id UUID NOT NULL,
    strategy_version_id UUID NOT NULL,
    instrument_id UUID NOT NULL,
    interval_code VARCHAR(4) NOT NULL,
    candle_open_time TIMESTAMPTZ NOT NULL,
    candle_close_time TIMESTAMPTZ NOT NULL,
    target NUMERIC(38,18) NOT NULL,
    mode VARCHAR(16) NOT NULL,
    environment VARCHAR(16) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT strategy_signals_pkey PRIMARY KEY (id),
    CONSTRAINT fk_strategy_signals_owner
        FOREIGN KEY (owner_user_id) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT fk_strategy_signals_instance
        FOREIGN KEY (strategy_instance_id) REFERENCES strategy_instances (id) ON DELETE RESTRICT,
    CONSTRAINT fk_strategy_signals_version
        FOREIGN KEY (strategy_version_id) REFERENCES strategy_versions (id) ON DELETE RESTRICT,
    CONSTRAINT fk_strategy_signals_instrument
        FOREIGN KEY (instrument_id) REFERENCES market_instruments (id) ON DELETE RESTRICT,
    CONSTRAINT uq_strategy_signals_instance_candle UNIQUE (strategy_instance_id, candle_open_time),
    CONSTRAINT ck_strategy_signals_id_uuidv7 CHECK (
        id::text ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
    ),
    CONSTRAINT ck_strategy_signals_interval CHECK (interval_code IN ('1m', '5m', '15m', '1h', '4h', '1d')),
    CONSTRAINT ck_strategy_signals_times CHECK (
        isfinite(candle_open_time)
        AND isfinite(candle_close_time)
        AND candle_open_time < candle_close_time
        AND isfinite(created_at)
        AND (expires_at IS NULL OR isfinite(expires_at) AND expires_at > candle_close_time)
    ),
    CONSTRAINT ck_strategy_signals_target CHECK (target >= -1 AND target <= 1),
    CONSTRAINT ck_strategy_signals_mode CHECK (mode IN ('signal_only', 'manual', 'auto')),
    CONSTRAINT ck_strategy_signals_environment CHECK (environment IN ('paper', 'testnet', 'live')),
    CONSTRAINT ck_strategy_signals_status CHECK (status IN ('active', 'expired')),
    CONSTRAINT ck_strategy_signals_expiry CHECK (
        (mode = 'manual' AND expires_at IS NOT NULL)
        OR (mode <> 'manual' AND expires_at IS NULL)
    )
);

CREATE INDEX ix_strategy_instances_owner_enabled
    ON strategy_instances (owner_user_id, is_enabled, id DESC);
CREATE INDEX ix_strategy_signals_owner_created
    ON strategy_signals (owner_user_id, created_at DESC, id DESC);

-- +goose Down
LOCK TABLE worker_tasks, strategy_signals, strategy_instances IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE m2_realtime_signals_down_guard (
    row_count BIGINT NOT NULL CHECK (row_count = 0)
) ON COMMIT DROP;

INSERT INTO m2_realtime_signals_down_guard (row_count)
SELECT
    (SELECT COUNT(*) FROM strategy_instances)
    + (SELECT COUNT(*) FROM strategy_signals)
    + (SELECT COUNT(*) FROM worker_tasks WHERE task_type = 'strategy.realtime');

DROP INDEX ux_worker_tasks_type_dedupe;
ALTER TABLE worker_tasks
    DROP CONSTRAINT ck_worker_tasks_realtime_payload,
    DROP CONSTRAINT ck_worker_tasks_realtime_dedupe,
    DROP CONSTRAINT ck_worker_tasks_realtime_lane,
    DROP COLUMN dedupe_key;
DROP TABLE strategy_signals;
DROP TABLE strategy_instances;
