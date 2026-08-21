-- +goose Up

-- The console reset is allowed to remove strategy execution state, but never
-- financial facts. A Paper account always has one pristine balance snapshot;
-- allow only that untouched snapshot through the guard.
LOCK TABLE trading_accounts, trading_intents, paper_orders, paper_positions, paper_balances,
    trading_events, testnet_orders, testnet_trade_facts, testnet_balances,
    testnet_positions, testnet_open_orders, strategy_signals, strategy_instances
    IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE workflow_console_financial_guard (
    violations BIGINT NOT NULL CHECK (violations = 0)
) ON COMMIT DROP;

INSERT INTO workflow_console_financial_guard (violations)
SELECT
    (SELECT COUNT(*) FROM trading_intents)
    + (SELECT COUNT(*) FROM paper_orders)
    + (SELECT COUNT(*) FROM paper_positions)
    + (
        SELECT COUNT(*)
        FROM paper_balances AS balance
        JOIN trading_accounts AS account ON account.id = balance.account_id
        WHERE account.environment <> 'paper'
           OR balance.cash_balance <> account.initial_balance
           OR balance.equity <> account.initial_balance
           OR balance.peak_equity <> account.initial_balance
           OR balance.day_start_equity <> account.initial_balance
           OR balance.realized_pnl <> 0
           OR balance.unrealized_pnl <> 0
           OR balance.fees <> 0
           OR balance.funding <> 0
    )
    + (SELECT COUNT(*) FROM trading_events)
    + (SELECT COUNT(*) FROM testnet_orders)
    + (SELECT COUNT(*) FROM testnet_trade_facts)
    + (SELECT COUNT(*) FROM testnet_balances)
    + (SELECT COUNT(*) FROM testnet_positions)
    + (SELECT COUNT(*) FROM testnet_open_orders);

-- User workflows which contain strategy nodes are not valid after the binding
-- split. Their execution history is intentionally removed with the definition.
DELETE FROM workflow_executions
WHERE workflow_definition_id IN (
    SELECT id FROM workflow_definitions
    WHERE NOT is_builtin AND graph_json LIKE '%strategy.evaluate%'
);
DELETE FROM workflow_definitions
WHERE NOT is_builtin
  AND graph_json LIKE '%strategy.evaluate%';

DELETE FROM notification_deliveries
WHERE strategy_signal_id IS NOT NULL;

CREATE TEMPORARY TABLE workflow_console_strategy_tasks (id VARCHAR(36) PRIMARY KEY) ON COMMIT DROP;
INSERT INTO workflow_console_strategy_tasks (id)
SELECT worker_task_id FROM backtests
UNION
SELECT worker_task_id FROM strategy_versions;
DROP TRIGGER IF EXISTS strategy_versions_immutable ON strategy_versions;
DROP FUNCTION IF EXISTS reject_strategy_version_mutation();
DELETE FROM strategy_signals;
DELETE FROM strategy_instances;
DELETE FROM backtests;
DELETE FROM strategy_versions;
DELETE FROM strategies;
DELETE FROM worker_tasks
WHERE id IN (SELECT id FROM workflow_console_strategy_tasks);

ALTER TABLE strategies
    ADD COLUMN IF NOT EXISTS archived_at TIMESTAMPTZ;

ALTER TABLE strategy_versions
    DROP CONSTRAINT IF EXISTS ck_strategy_versions_market,
    DROP CONSTRAINT IF EXISTS ck_strategy_versions_symbol,
    DROP CONSTRAINT IF EXISTS ck_strategy_versions_interval,
    DROP CONSTRAINT IF EXISTS fk_strategy_versions_instrument;

ALTER TABLE strategies
	DROP CONSTRAINT IF EXISTS ck_strategies_market,
	DROP CONSTRAINT IF EXISTS ck_strategies_interval,
	DROP CONSTRAINT IF EXISTS fk_strategies_instrument,
	DROP COLUMN market_type,
	DROP COLUMN instrument_id,
	DROP COLUMN interval_code;

ALTER TABLE strategy_versions
	DROP COLUMN market_type,
	DROP COLUMN instrument_id,
	DROP COLUMN symbol,
	DROP COLUMN interval_code;

-- Published snapshots are still immutable; the trigger is recreated after the
-- one-time reset so the cleanup above can remove legacy rows.
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

ALTER TABLE backtests
    ADD COLUMN instrument_id UUID NOT NULL,
    ADD COLUMN interval_code VARCHAR(4) NOT NULL;

ALTER TABLE backtests
    ADD CONSTRAINT fk_backtests_instrument
        FOREIGN KEY (instrument_id) REFERENCES market_instruments (id) ON DELETE RESTRICT,
    ADD CONSTRAINT ck_backtests_interval
        CHECK (interval_code IN ('1m','5m','15m','1h','4h','1d'));

ALTER TABLE strategy_instances
    ADD COLUMN market_type VARCHAR(16) NOT NULL,
    ADD COLUMN instrument_id UUID NOT NULL,
    ADD COLUMN interval_code VARCHAR(4) NOT NULL,
    ADD COLUMN workflow_definition_id BIGINT NOT NULL,
    ADD COLUMN workflow_node_id VARCHAR(100) NOT NULL,
    ADD COLUMN archived_at TIMESTAMPTZ;

ALTER TABLE strategy_instances
    ADD CONSTRAINT fk_strategy_instances_instrument
        FOREIGN KEY (instrument_id) REFERENCES market_instruments (id) ON DELETE RESTRICT,
    ADD CONSTRAINT fk_strategy_instances_workflow_definition
        FOREIGN KEY (workflow_definition_id) REFERENCES workflow_definitions (id) ON DELETE RESTRICT,
    ADD CONSTRAINT ck_strategy_instances_market
        CHECK (market_type IN ('spot','usd_m')),
    ADD CONSTRAINT ck_strategy_instances_interval
        CHECK (interval_code IN ('1m','5m','15m','1h','4h','1d')),
    ADD CONSTRAINT ck_strategy_instances_binding
        CHECK (BTRIM(workflow_node_id) <> '');

CREATE UNIQUE INDEX ux_strategy_instances_workflow_binding
    ON strategy_instances (workflow_definition_id, workflow_node_id)
    WHERE archived_at IS NULL;

CREATE INDEX IF NOT EXISTS ix_strategy_instances_instrument_interval
    ON strategy_instances (instrument_id, interval_code)
    WHERE instrument_id IS NOT NULL;

ALTER TABLE trading_accounts
    ADD COLUMN IF NOT EXISTS archived_at TIMESTAMPTZ;

-- Only Binance Spot and USD-M metadata quoted in USDT is supported by the
-- console. Existing customized settings are normalized during this migration.
UPDATE market_sync_settings
SET market_types = '["spot","usd_m"]'::jsonb,
    quote_assets = '["USDT"]'::jsonb,
    updated_at = CURRENT_TIMESTAMP
WHERE id = 1;

ALTER TABLE market_sync_settings
    DROP CONSTRAINT IF EXISTS ck_market_sync_settings_quote_assets,
    ADD CONSTRAINT ck_market_sync_settings_quote_assets CHECK (
        quote_assets = '["USDT"]'::jsonb
    );

CREATE TABLE workflow_node_templates (
    id UUID NOT NULL,
    owner_user_id BIGINT NOT NULL,
    name VARCHAR(120) NOT NULL,
    description VARCHAR(500) NOT NULL DEFAULT '',
    icon VARCHAR(120) NOT NULL DEFAULT 'ri:node-tree',
    base_node_type VARCHAR(120) NOT NULL,
    default_config_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    is_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT workflow_node_templates_pkey PRIMARY KEY (id),
    CONSTRAINT fk_workflow_node_templates_owner
        FOREIGN KEY (owner_user_id) REFERENCES users (id) ON DELETE CASCADE,
    CONSTRAINT uq_workflow_node_templates_owner_name UNIQUE (owner_user_id, name),
    CONSTRAINT ck_workflow_node_templates_id_uuidv7 CHECK (
        id::text ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
    ),
    CONSTRAINT ck_workflow_node_templates_name CHECK (name = BTRIM(name) AND name <> ''),
    CONSTRAINT ck_workflow_node_templates_base CHECK (base_node_type = BTRIM(base_node_type) AND base_node_type <> ''),
    CONSTRAINT ck_workflow_node_templates_config CHECK (jsonb_typeof(default_config_json) = 'object'),
    CONSTRAINT ck_workflow_node_templates_times CHECK (isfinite(created_at) AND isfinite(updated_at))
);

CREATE INDEX ix_workflow_node_templates_owner_enabled
    ON workflow_node_templates (owner_user_id, is_enabled, updated_at DESC);

CREATE TABLE worker_heartbeats (
    worker_id VARCHAR(120) NOT NULL,
    lane VARCHAR(16) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'online',
    last_heartbeat_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    queue_depth INTEGER NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT worker_heartbeats_pkey PRIMARY KEY (worker_id, lane),
    CONSTRAINT ck_worker_heartbeats_lane CHECK (lane IN ('realtime','backtest')),
    CONSTRAINT ck_worker_heartbeats_status CHECK (status IN ('online','offline')),
    CONSTRAINT ck_worker_heartbeats_queue CHECK (queue_depth >= 0),
    CONSTRAINT ck_worker_heartbeats_times CHECK (isfinite(last_heartbeat_at) AND isfinite(updated_at))
);

CREATE INDEX ix_worker_heartbeats_lane_heartbeat
    ON worker_heartbeats (lane, last_heartbeat_at DESC);

UPDATE menus
SET is_active = FALSE, is_hidden = TRUE, updated_at = CURRENT_TIMESTAMP
WHERE name IN ('PaperTrading', 'StrategyCenter', 'PushData', 'NotifyChannels');

-- +goose Down

LOCK TABLE workflow_node_templates, worker_heartbeats, strategies,
    strategy_versions, strategy_instances, backtests, trading_accounts,
    market_sync_settings IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE workflow_console_down_guard (
    violations BIGINT NOT NULL CHECK (violations = 0)
) ON COMMIT DROP;

INSERT INTO workflow_console_down_guard (violations)
SELECT (SELECT COUNT(*) FROM workflow_node_templates)
     + (SELECT COUNT(*) FROM worker_heartbeats)
     + (SELECT COUNT(*) FROM strategies)
     + (SELECT COUNT(*) FROM strategy_versions)
     + (SELECT COUNT(*) FROM strategy_instances)
     + (SELECT COUNT(*) FROM backtests);

DROP INDEX IF EXISTS ix_worker_heartbeats_lane_heartbeat;
DROP TABLE worker_heartbeats;
DROP INDEX IF EXISTS ix_workflow_node_templates_owner_enabled;
DROP TABLE workflow_node_templates;

ALTER TABLE market_sync_settings
    DROP CONSTRAINT IF EXISTS ck_market_sync_settings_quote_assets,
    ADD CONSTRAINT ck_market_sync_settings_quote_assets CHECK (
        jsonb_typeof(quote_assets) = 'array'
        AND jsonb_array_length(quote_assets) BETWEEN 1 AND 3
        AND quote_assets <@ '["USDT","USDC","FDUSD"]'::jsonb
    );
UPDATE market_sync_settings
SET market_types = '["spot"]'::jsonb,
    quote_assets = '["USDT","USDC"]'::jsonb,
    updated_at = CURRENT_TIMESTAMP
WHERE id = 1;

ALTER TABLE trading_accounts DROP COLUMN IF EXISTS archived_at;

DROP INDEX IF EXISTS ux_strategy_instances_workflow_binding;
DROP INDEX IF EXISTS ix_strategy_instances_instrument_interval;

ALTER TABLE strategy_instances
    DROP CONSTRAINT IF EXISTS ck_strategy_instances_binding,
    DROP CONSTRAINT IF EXISTS ck_strategy_instances_interval,
    DROP CONSTRAINT IF EXISTS ck_strategy_instances_market,
    DROP CONSTRAINT IF EXISTS fk_strategy_instances_workflow_definition,
    DROP CONSTRAINT IF EXISTS fk_strategy_instances_instrument,
    DROP COLUMN IF EXISTS workflow_node_id,
    DROP COLUMN IF EXISTS workflow_definition_id,
    DROP COLUMN IF EXISTS interval_code,
    DROP COLUMN IF EXISTS instrument_id,
    DROP COLUMN IF EXISTS market_type,
    DROP COLUMN IF EXISTS archived_at;

ALTER TABLE backtests
    DROP CONSTRAINT IF EXISTS ck_backtests_interval,
    DROP CONSTRAINT IF EXISTS fk_backtests_instrument,
    DROP COLUMN IF EXISTS interval_code,
    DROP COLUMN IF EXISTS instrument_id;

DROP TRIGGER IF EXISTS strategy_versions_immutable ON strategy_versions;
DROP FUNCTION IF EXISTS reject_strategy_version_mutation();

ALTER TABLE strategy_versions
    ADD COLUMN market_type VARCHAR(32) NOT NULL,
    ADD COLUMN instrument_id UUID NOT NULL,
    ADD COLUMN symbol VARCHAR(64) NOT NULL,
    ADD COLUMN interval_code VARCHAR(4) NOT NULL,
    ADD CONSTRAINT fk_strategy_versions_instrument
        FOREIGN KEY (instrument_id) REFERENCES market_instruments (id) ON DELETE RESTRICT,
    ADD CONSTRAINT ck_strategy_versions_market CHECK (market_type IN ('spot','usd_m')),
    ADD CONSTRAINT ck_strategy_versions_symbol CHECK (symbol ~ '^[A-Z0-9][A-Z0-9._-]*$'),
    ADD CONSTRAINT ck_strategy_versions_interval CHECK (interval_code IN ('1m','5m','15m','1h','4h','1d'));

ALTER TABLE strategies
    ADD COLUMN market_type VARCHAR(32) NOT NULL,
    ADD COLUMN instrument_id UUID NOT NULL,
    ADD COLUMN interval_code VARCHAR(4) NOT NULL,
    ADD CONSTRAINT fk_strategies_instrument
        FOREIGN KEY (instrument_id) REFERENCES market_instruments (id) ON DELETE RESTRICT,
    ADD CONSTRAINT ck_strategies_market CHECK (market_type IN ('spot','usd_m')),
    ADD CONSTRAINT ck_strategies_interval CHECK (interval_code IN ('1m','5m','15m','1h','4h','1d'));

ALTER TABLE strategies DROP COLUMN IF EXISTS archived_at;

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
