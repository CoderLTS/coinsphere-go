-- +goose Up
CREATE TABLE trading_controls (
    id SMALLINT NOT NULL,
    emergency_stopped BOOLEAN NOT NULL DEFAULT TRUE,
    stop_reason VARCHAR(255) NOT NULL DEFAULT 'initial_safety_stop',
    stopped_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    stopped_by_user_id BIGINT,
    released_at TIMESTAMPTZ,
    released_by_user_id BIGINT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT trading_controls_pkey PRIMARY KEY (id),
    CONSTRAINT fk_trading_controls_stopped_by
        FOREIGN KEY (stopped_by_user_id) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT fk_trading_controls_released_by
        FOREIGN KEY (released_by_user_id) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT ck_trading_controls_singleton CHECK (id = 1),
    CONSTRAINT ck_trading_controls_state CHECK (
        (
            emergency_stopped
            AND stop_reason <> ''
            AND isfinite(stopped_at)
            AND released_at IS NULL
            AND released_by_user_id IS NULL
        )
        OR
        (
            NOT emergency_stopped
            AND stop_reason = ''
            AND released_at IS NOT NULL
            AND isfinite(released_at)
            AND released_at >= stopped_at
            AND released_by_user_id IS NOT NULL
        )
    ),
    CONSTRAINT ck_trading_controls_updated_at CHECK (isfinite(updated_at))
);

INSERT INTO trading_controls (id) VALUES (1);

CREATE TABLE trading_accounts (
    id UUID NOT NULL,
    owner_user_id BIGINT NOT NULL,
    name VARCHAR(120) NOT NULL,
    market_type VARCHAR(16) NOT NULL,
    environment VARCHAR(16) NOT NULL DEFAULT 'paper',
    status VARCHAR(16) NOT NULL DEFAULT 'paused',
    pause_reason VARCHAR(255) NOT NULL DEFAULT 'configuration_required',
    automation_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    automation_authorized_at TIMESTAMPTZ,
    automation_authorized_by_user_id BIGINT,
    initial_balance NUMERIC(38,18) NOT NULL,
    paper_fee_rate NUMERIC(38,18) NOT NULL,
    max_total_notional NUMERIC(38,18),
    max_symbol_notional NUMERIC(38,18),
    max_order_notional NUMERIC(38,18),
    max_daily_loss NUMERIC(38,18),
    max_drawdown NUMERIC(38,18),
    max_quote_age_seconds INTEGER,
    leverage INTEGER,
    creation_idempotency_record_id BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT trading_accounts_pkey PRIMARY KEY (id),
    CONSTRAINT fk_trading_accounts_owner
        FOREIGN KEY (owner_user_id) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT fk_trading_accounts_authorized_by
        FOREIGN KEY (automation_authorized_by_user_id) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT fk_trading_accounts_creation_idempotency
        FOREIGN KEY (creation_idempotency_record_id) REFERENCES idempotency_records (id) ON DELETE RESTRICT,
    CONSTRAINT uq_trading_accounts_creation_idempotency UNIQUE (creation_idempotency_record_id),
    CONSTRAINT ck_trading_accounts_id_uuidv7 CHECK (
        id::text ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
    ),
    CONSTRAINT ck_trading_accounts_name CHECK (name = BTRIM(name) AND name <> ''),
    CONSTRAINT ck_trading_accounts_market CHECK (market_type IN ('spot', 'usd_m')),
    CONSTRAINT ck_trading_accounts_environment CHECK (environment = 'paper'),
    CONSTRAINT ck_trading_accounts_status CHECK (status IN ('active', 'paused')),
    CONSTRAINT ck_trading_accounts_pause_state CHECK (
        (status = 'active' AND pause_reason = '')
        OR (status = 'paused' AND pause_reason <> '')
    ),
    CONSTRAINT ck_trading_accounts_authorization CHECK (
        (automation_authorized_at IS NULL AND automation_authorized_by_user_id IS NULL)
        OR (
            automation_authorized_at IS NOT NULL
            AND isfinite(automation_authorized_at)
            AND automation_authorized_by_user_id IS NOT NULL
        )
    ),
    CONSTRAINT ck_trading_accounts_initial_balance CHECK (initial_balance > 0),
    CONSTRAINT ck_trading_accounts_fee_rate CHECK (paper_fee_rate >= 0 AND paper_fee_rate <= 0.01),
    CONSTRAINT ck_trading_accounts_limits CHECK (
        (max_total_notional IS NULL OR max_total_notional > 0)
        AND (max_symbol_notional IS NULL OR max_symbol_notional > 0)
        AND (max_order_notional IS NULL OR max_order_notional > 0)
        AND (max_daily_loss IS NULL OR max_daily_loss > 0)
        AND (max_drawdown IS NULL OR max_drawdown > 0)
        AND (max_quote_age_seconds IS NULL OR max_quote_age_seconds BETWEEN 1 AND 300)
    ),
    CONSTRAINT ck_trading_accounts_leverage CHECK (
        (market_type = 'spot' AND leverage IS NULL)
        OR (market_type = 'usd_m' AND (leverage IS NULL OR leverage BETWEEN 1 AND 5))
    ),
    CONSTRAINT ck_trading_accounts_times CHECK (isfinite(created_at) AND isfinite(updated_at))
);

CREATE TABLE trading_account_instruments (
    account_id UUID NOT NULL,
    instrument_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT trading_account_instruments_pkey PRIMARY KEY (account_id, instrument_id),
    CONSTRAINT fk_trading_account_instruments_account
        FOREIGN KEY (account_id) REFERENCES trading_accounts (id) ON DELETE CASCADE,
    CONSTRAINT fk_trading_account_instruments_instrument
        FOREIGN KEY (instrument_id) REFERENCES market_instruments (id) ON DELETE RESTRICT,
    CONSTRAINT ck_trading_account_instruments_created_at CHECK (isfinite(created_at))
);

ALTER TABLE strategy_instances
    ADD COLUMN trading_account_id UUID,
    ADD COLUMN allocation_usdt NUMERIC(38,18),
    ADD CONSTRAINT fk_strategy_instances_trading_account
        FOREIGN KEY (trading_account_id) REFERENCES trading_accounts (id) ON DELETE RESTRICT,
    ADD CONSTRAINT ck_strategy_instances_allocation CHECK (
        (trading_account_id IS NULL AND allocation_usdt IS NULL)
        OR (trading_account_id IS NOT NULL AND allocation_usdt IS NOT NULL AND allocation_usdt > 0)
    );

CREATE TABLE trading_intents (
    id UUID NOT NULL,
    account_id UUID NOT NULL,
    strategy_signal_id UUID NOT NULL,
    strategy_instance_id UUID NOT NULL,
    owner_user_id BIGINT NOT NULL,
    instrument_id UUID NOT NULL,
    market_type VARCHAR(16) NOT NULL,
    mode VARCHAR(16) NOT NULL,
    environment VARCHAR(16) NOT NULL,
    target NUMERIC(38,18) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    block_reason VARCHAR(255) NOT NULL DEFAULT '',
    client_order_id VARCHAR(64) NOT NULL,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    claimed_at TIMESTAMPTZ,
    worker_id VARCHAR(120),
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT trading_intents_pkey PRIMARY KEY (id),
    CONSTRAINT fk_trading_intents_account
        FOREIGN KEY (account_id) REFERENCES trading_accounts (id) ON DELETE RESTRICT,
    CONSTRAINT fk_trading_intents_signal
        FOREIGN KEY (strategy_signal_id) REFERENCES strategy_signals (id) ON DELETE RESTRICT,
    CONSTRAINT fk_trading_intents_instance
        FOREIGN KEY (strategy_instance_id) REFERENCES strategy_instances (id) ON DELETE RESTRICT,
    CONSTRAINT fk_trading_intents_owner
        FOREIGN KEY (owner_user_id) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT fk_trading_intents_instrument
        FOREIGN KEY (instrument_id) REFERENCES market_instruments (id) ON DELETE RESTRICT,
    CONSTRAINT uq_trading_intents_signal UNIQUE (strategy_signal_id),
    CONSTRAINT uq_trading_intents_account_client_order UNIQUE (account_id, client_order_id),
    CONSTRAINT ck_trading_intents_id_uuidv7 CHECK (
        id::text ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
    ),
    CONSTRAINT ck_trading_intents_market CHECK (market_type IN ('spot', 'usd_m')),
    CONSTRAINT ck_trading_intents_mode CHECK (mode IN ('manual', 'auto')),
    CONSTRAINT ck_trading_intents_environment CHECK (environment = 'paper'),
    CONSTRAINT ck_trading_intents_target CHECK (
        target >= -1 AND target <= 1 AND (market_type <> 'spot' OR target >= 0)
    ),
    CONSTRAINT ck_trading_intents_status CHECK (
        status IN ('pending', 'processing', 'executed', 'blocked', 'failed')
    ),
    CONSTRAINT ck_trading_intents_terminal_state CHECK (
        (status IN ('pending', 'processing') AND completed_at IS NULL)
        OR (status IN ('executed', 'blocked', 'failed') AND completed_at IS NOT NULL AND isfinite(completed_at))
    ),
    CONSTRAINT ck_trading_intents_claim_state CHECK (
        (status = 'processing' AND claimed_at IS NOT NULL AND worker_id IS NOT NULL)
        OR status <> 'processing'
    ),
    CONSTRAINT ck_trading_intents_attempts CHECK (attempt_count >= 0),
    CONSTRAINT ck_trading_intents_times CHECK (
        isfinite(created_at)
        AND isfinite(updated_at)
        AND (claimed_at IS NULL OR isfinite(claimed_at))
    )
);

CREATE TABLE paper_orders (
    id UUID NOT NULL,
    account_id UUID NOT NULL,
    intent_id UUID NOT NULL,
    instrument_id UUID NOT NULL,
    client_order_id VARCHAR(64) NOT NULL,
    side VARCHAR(4) NOT NULL,
    quantity NUMERIC(38,18) NOT NULL,
    filled_quantity NUMERIC(38,18) NOT NULL,
    average_price NUMERIC(38,18) NOT NULL,
    status VARCHAR(16) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT paper_orders_pkey PRIMARY KEY (id),
    CONSTRAINT fk_paper_orders_account
        FOREIGN KEY (account_id) REFERENCES trading_accounts (id) ON DELETE RESTRICT,
    CONSTRAINT fk_paper_orders_intent
        FOREIGN KEY (intent_id) REFERENCES trading_intents (id) ON DELETE RESTRICT,
    CONSTRAINT fk_paper_orders_instrument
        FOREIGN KEY (instrument_id) REFERENCES market_instruments (id) ON DELETE RESTRICT,
    CONSTRAINT uq_paper_orders_intent UNIQUE (intent_id),
    CONSTRAINT uq_paper_orders_account_client_order UNIQUE (account_id, client_order_id),
    CONSTRAINT ck_paper_orders_side CHECK (side IN ('buy', 'sell')),
    CONSTRAINT ck_paper_orders_values CHECK (
        quantity > 0
        AND filled_quantity >= 0
        AND filled_quantity <= quantity
        AND average_price > 0
    ),
    CONSTRAINT ck_paper_orders_status CHECK (status IN ('accepted', 'filled', 'canceled', 'failed')),
    CONSTRAINT ck_paper_orders_times CHECK (isfinite(created_at) AND isfinite(updated_at))
);

CREATE TABLE trading_events (
    id BIGSERIAL NOT NULL,
    event_id UUID NOT NULL,
    account_id UUID NOT NULL,
    intent_id UUID,
    order_id UUID,
    instrument_id UUID NOT NULL,
    event_type VARCHAR(16) NOT NULL,
    side VARCHAR(4),
    quantity NUMERIC(38,18),
    price NUMERIC(38,18),
    amount NUMERIC(38,18),
    occurred_at TIMESTAMPTZ NOT NULL,
    dedupe_key VARCHAR(160) NOT NULL,
    correction_of BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT trading_events_pkey PRIMARY KEY (id),
    CONSTRAINT uq_trading_events_event_id UNIQUE (event_id),
    CONSTRAINT uq_trading_events_account_dedupe UNIQUE (account_id, dedupe_key),
    CONSTRAINT fk_trading_events_account
        FOREIGN KEY (account_id) REFERENCES trading_accounts (id) ON DELETE RESTRICT,
    CONSTRAINT fk_trading_events_intent
        FOREIGN KEY (intent_id) REFERENCES trading_intents (id) ON DELETE RESTRICT,
    CONSTRAINT fk_trading_events_instrument
        FOREIGN KEY (instrument_id) REFERENCES market_instruments (id) ON DELETE RESTRICT,
    CONSTRAINT fk_trading_events_correction
        FOREIGN KEY (correction_of) REFERENCES trading_events (id) ON DELETE RESTRICT,
    CONSTRAINT ck_trading_events_id_uuidv7 CHECK (
        event_id::text ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
    ),
    CONSTRAINT ck_trading_events_type CHECK (event_type IN ('order', 'fill', 'fee', 'funding')),
    CONSTRAINT ck_trading_events_shape CHECK (
        (
            event_type IN ('order', 'fill')
            AND side IN ('buy', 'sell')
            AND quantity IS NOT NULL AND quantity > 0
            AND price IS NOT NULL AND price > 0
            AND amount IS NULL
            AND order_id IS NOT NULL
            AND intent_id IS NOT NULL
        )
        OR (
            event_type = 'fee'
            AND side IS NULL
            AND quantity IS NULL
            AND price IS NULL
            AND amount IS NOT NULL AND amount >= 0
            AND order_id IS NOT NULL
            AND intent_id IS NOT NULL
        )
        OR (
            event_type = 'funding'
            AND side IS NULL
            AND quantity IS NULL
            AND price IS NOT NULL AND price > 0
            AND amount IS NOT NULL
        )
    ),
    CONSTRAINT ck_trading_events_times CHECK (isfinite(occurred_at) AND isfinite(created_at))
);

-- +goose StatementBegin
CREATE FUNCTION reject_trading_event_mutation() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'trading_events is append-only';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER trading_events_append_only
BEFORE UPDATE OR DELETE ON trading_events
FOR EACH ROW EXECUTE FUNCTION reject_trading_event_mutation();

CREATE TABLE paper_positions (
    account_id UUID NOT NULL,
    instrument_id UUID NOT NULL,
    owner_strategy_instance_id UUID,
    quantity NUMERIC(38,18) NOT NULL DEFAULT 0,
    average_entry_price NUMERIC(38,18) NOT NULL DEFAULT 0,
    last_price NUMERIC(38,18) NOT NULL DEFAULT 0,
    realized_pnl NUMERIC(38,18) NOT NULL DEFAULT 0,
    unrealized_pnl NUMERIC(38,18) NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT paper_positions_pkey PRIMARY KEY (account_id, instrument_id),
    CONSTRAINT fk_paper_positions_account
        FOREIGN KEY (account_id) REFERENCES trading_accounts (id) ON DELETE RESTRICT,
    CONSTRAINT fk_paper_positions_instrument
        FOREIGN KEY (instrument_id) REFERENCES market_instruments (id) ON DELETE RESTRICT,
    CONSTRAINT fk_paper_positions_owner_instance
        FOREIGN KEY (owner_strategy_instance_id) REFERENCES strategy_instances (id) ON DELETE RESTRICT,
    CONSTRAINT ck_paper_positions_owner CHECK (
        (quantity = 0 AND owner_strategy_instance_id IS NULL AND average_entry_price = 0)
        OR (quantity <> 0 AND owner_strategy_instance_id IS NOT NULL AND average_entry_price > 0)
    ),
    CONSTRAINT ck_paper_positions_prices CHECK (last_price >= 0),
    CONSTRAINT ck_paper_positions_updated_at CHECK (isfinite(updated_at))
);

CREATE TABLE paper_balances (
    account_id UUID NOT NULL,
    cash_balance NUMERIC(38,18) NOT NULL,
    equity NUMERIC(38,18) NOT NULL,
    peak_equity NUMERIC(38,18) NOT NULL,
    day_start_date DATE NOT NULL,
    day_start_equity NUMERIC(38,18) NOT NULL,
    realized_pnl NUMERIC(38,18) NOT NULL DEFAULT 0,
    unrealized_pnl NUMERIC(38,18) NOT NULL DEFAULT 0,
    fees NUMERIC(38,18) NOT NULL DEFAULT 0,
    funding NUMERIC(38,18) NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT paper_balances_pkey PRIMARY KEY (account_id),
    CONSTRAINT fk_paper_balances_account
        FOREIGN KEY (account_id) REFERENCES trading_accounts (id) ON DELETE RESTRICT,
    CONSTRAINT ck_paper_balances_peak CHECK (peak_equity >= equity),
    CONSTRAINT ck_paper_balances_updated_at CHECK (isfinite(updated_at))
);

CREATE INDEX ix_trading_accounts_owner ON trading_accounts (owner_user_id, id DESC);
CREATE INDEX ix_trading_intents_pending ON trading_intents (created_at, id) WHERE status = 'pending';
CREATE INDEX ix_trading_intents_owner ON trading_intents (owner_user_id, id DESC);
CREATE INDEX ix_paper_orders_account ON paper_orders (account_id, id DESC);
CREATE INDEX ix_trading_events_account ON trading_events (account_id, id);

-- +goose Down
LOCK TABLE
    strategy_instances,
    trading_controls,
    trading_accounts,
    trading_account_instruments,
    trading_intents,
    paper_orders,
    trading_events,
    paper_positions,
    paper_balances
IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE m2_paper_executor_down_guard (
    row_count BIGINT NOT NULL CHECK (row_count = 0)
) ON COMMIT DROP;

INSERT INTO m2_paper_executor_down_guard (row_count)
SELECT
    (SELECT COUNT(*) FROM trading_accounts)
    + (SELECT COUNT(*) FROM trading_intents)
    + (SELECT COUNT(*) FROM trading_events)
    + (SELECT COUNT(*) FROM paper_orders)
    + (SELECT COUNT(*) FROM paper_positions)
    + (SELECT COUNT(*) FROM paper_balances)
    + (SELECT COUNT(*) FROM strategy_instances
        WHERE trading_account_id IS NOT NULL OR allocation_usdt IS NOT NULL);

DROP INDEX ix_trading_events_account;
DROP INDEX ix_paper_orders_account;
DROP INDEX ix_trading_intents_owner;
DROP INDEX ix_trading_intents_pending;
DROP INDEX ix_trading_accounts_owner;
DROP TABLE paper_balances;
DROP TABLE paper_positions;
DROP TRIGGER trading_events_append_only ON trading_events;
DROP FUNCTION reject_trading_event_mutation();
DROP TABLE trading_events;
DROP TABLE paper_orders;
DROP TABLE trading_intents;

ALTER TABLE strategy_instances
    DROP CONSTRAINT ck_strategy_instances_allocation,
    DROP CONSTRAINT fk_strategy_instances_trading_account,
    DROP COLUMN allocation_usdt,
    DROP COLUMN trading_account_id;

DROP TABLE trading_account_instruments;
DROP TABLE trading_accounts;
DROP TABLE trading_controls;
