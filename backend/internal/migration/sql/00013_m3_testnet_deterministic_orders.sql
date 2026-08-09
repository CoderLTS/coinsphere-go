-- +goose Up

ALTER TABLE trading_intents
    DROP CONSTRAINT ck_trading_intents_status,
    DROP CONSTRAINT ck_trading_intents_terminal_state,
    DROP CONSTRAINT ck_trading_intents_claim_state,
    ADD CONSTRAINT ck_trading_intents_status CHECK (
        status IN ('pending', 'processing', 'reconciling', 'executed', 'blocked', 'failed')
    ),
    ADD CONSTRAINT ck_trading_intents_terminal_state CHECK (
        (status IN ('pending', 'processing', 'reconciling') AND completed_at IS NULL)
        OR (status IN ('executed', 'blocked', 'failed') AND completed_at IS NOT NULL AND isfinite(completed_at))
    ),
    ADD CONSTRAINT ck_trading_intents_claim_state CHECK (
        (status = 'processing' AND claimed_at IS NOT NULL AND worker_id IS NOT NULL)
        OR (status <> 'processing' AND claimed_at IS NULL AND worker_id IS NULL)
    );

CREATE UNIQUE INDEX uq_trading_intents_testnet_account_active
    ON trading_intents (account_id)
    WHERE environment = 'testnet' AND status IN ('processing', 'reconciling');

CREATE INDEX ix_trading_intents_testnet_runnable
    ON trading_intents (created_at, id)
    WHERE environment = 'testnet' AND status IN ('pending', 'reconciling');

CREATE TABLE testnet_risk_states (
    account_id UUID NOT NULL,
    credential_updated_at TIMESTAMPTZ NOT NULL,
    baseline_equity NUMERIC(38,18) NOT NULL,
    equity NUMERIC(38,18) NOT NULL,
    peak_equity NUMERIC(38,18) NOT NULL,
    day_start_date DATE NOT NULL,
    day_start_equity NUMERIC(38,18) NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT testnet_risk_states_pkey PRIMARY KEY (account_id),
    CONSTRAINT fk_testnet_risk_states_reconciliation
        FOREIGN KEY (account_id, credential_updated_at)
        REFERENCES testnet_reconciliations (account_id, credential_updated_at)
        ON DELETE RESTRICT ON UPDATE RESTRICT,
    CONSTRAINT ck_testnet_risk_states_values CHECK (
        baseline_equity >= 0 AND peak_equity >= equity
    ),
    CONSTRAINT ck_testnet_risk_states_times CHECK (
        isfinite(credential_updated_at)
        AND isfinite(day_start_date)
        AND isfinite(updated_at)
    )
);

CREATE TABLE testnet_orders (
    id UUID NOT NULL,
    account_id UUID NOT NULL,
    intent_id UUID NOT NULL,
    strategy_instance_id UUID NOT NULL,
    instrument_id UUID NOT NULL,
    credential_updated_at TIMESTAMPTZ NOT NULL,
    submitted_account_updated_at TIMESTAMPTZ NOT NULL,
    client_order_id VARCHAR(64) NOT NULL,
    exchange_order_id BIGINT,
    side VARCHAR(4) NOT NULL,
    quantity NUMERIC(38,18) NOT NULL,
    filled_quantity NUMERIC(38,18) NOT NULL DEFAULT 0,
    cumulative_quote_quantity NUMERIC(38,18) NOT NULL DEFAULT 0,
    average_price NUMERIC(38,18) NOT NULL DEFAULT 0,
    status VARCHAR(24) NOT NULL DEFAULT 'prepared',
    last_error_code VARCHAR(64) NOT NULL DEFAULT '',
    submit_attempt_count INTEGER NOT NULL DEFAULT 1,
    query_attempt_count INTEGER NOT NULL DEFAULT 0,
    submitted_at TIMESTAMPTZ NOT NULL,
    last_queried_at TIMESTAMPTZ,
    observed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT testnet_orders_pkey PRIMARY KEY (id),
    CONSTRAINT fk_testnet_orders_account
        FOREIGN KEY (account_id) REFERENCES trading_accounts (id) ON DELETE RESTRICT,
    CONSTRAINT fk_testnet_orders_intent
        FOREIGN KEY (intent_id) REFERENCES trading_intents (id) ON DELETE RESTRICT,
    CONSTRAINT fk_testnet_orders_strategy_instance
        FOREIGN KEY (strategy_instance_id) REFERENCES strategy_instances (id) ON DELETE RESTRICT,
    CONSTRAINT fk_testnet_orders_instrument
        FOREIGN KEY (instrument_id) REFERENCES market_instruments (id) ON DELETE RESTRICT,
    CONSTRAINT uq_testnet_orders_intent UNIQUE (intent_id),
    CONSTRAINT uq_testnet_orders_account_client_order UNIQUE (account_id, client_order_id),
    CONSTRAINT uq_testnet_orders_account_exchange_order UNIQUE (account_id, exchange_order_id),
    CONSTRAINT ck_testnet_orders_id_uuidv7 CHECK (
        id::text ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
    ),
    CONSTRAINT ck_testnet_orders_identity CHECK (
        client_order_id = BTRIM(client_order_id) AND client_order_id <> ''
        AND (exchange_order_id IS NULL OR exchange_order_id > 0)
    ),
    CONSTRAINT ck_testnet_orders_side CHECK (side IN ('buy', 'sell')),
    CONSTRAINT ck_testnet_orders_values CHECK (
        quantity > 0
        AND filled_quantity >= 0 AND filled_quantity <= quantity
        AND cumulative_quote_quantity >= 0
        AND (
            (filled_quantity = 0 AND cumulative_quote_quantity = 0 AND average_price = 0)
            OR (filled_quantity > 0 AND cumulative_quote_quantity > 0 AND average_price > 0)
        )
    ),
    CONSTRAINT ck_testnet_orders_status CHECK (
        status IN (
            'prepared', 'unknown', 'new', 'partially_filled',
            'filled', 'canceled', 'rejected', 'expired'
        )
    ),
    CONSTRAINT ck_testnet_orders_state CHECK (
        (
            status = 'prepared'
            AND exchange_order_id IS NULL
            AND filled_quantity = 0
            AND observed_at IS NULL
            AND last_error_code = ''
        )
        OR (status = 'unknown' AND last_error_code <> '')
        OR (status = 'rejected' AND last_error_code <> '' AND filled_quantity = 0)
        OR (
            status = 'new'
            AND exchange_order_id IS NOT NULL
            AND filled_quantity = 0
            AND observed_at IS NOT NULL
            AND last_error_code = ''
        )
        OR (
            status = 'partially_filled'
            AND exchange_order_id IS NOT NULL
            AND filled_quantity > 0 AND filled_quantity < quantity
            AND observed_at IS NOT NULL
            AND last_error_code = ''
        )
        OR (
            status = 'filled'
            AND exchange_order_id IS NOT NULL
            AND filled_quantity = quantity
            AND observed_at IS NOT NULL
            AND last_error_code = ''
        )
        OR (
            status IN ('canceled', 'expired')
            AND exchange_order_id IS NOT NULL
            AND observed_at IS NOT NULL
            AND last_error_code = ''
        )
    ),
    CONSTRAINT ck_testnet_orders_attempts CHECK (
        submit_attempt_count >= 1
        AND query_attempt_count >= 0
        AND (
            (query_attempt_count = 0 AND last_queried_at IS NULL)
            OR (query_attempt_count > 0 AND last_queried_at IS NOT NULL)
        )
    ),
    CONSTRAINT ck_testnet_orders_times CHECK (
        isfinite(credential_updated_at)
        AND isfinite(submitted_account_updated_at)
        AND isfinite(submitted_at)
        AND (last_queried_at IS NULL OR isfinite(last_queried_at))
        AND (observed_at IS NULL OR isfinite(observed_at))
        AND isfinite(created_at)
        AND isfinite(updated_at)
    )
);

-- The order row must remain bound to the immutable intent identity and to the
-- exact verified credential version used for its latest submission attempt.
-- +goose StatementBegin
CREATE FUNCTION validate_testnet_order_binding() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
    intent_account UUID;
    intent_instance UUID;
    intent_instrument UUID;
    intent_client_order_id VARCHAR(64);
    intent_environment VARCHAR(16);
    account_environment VARCHAR(16);
    account_version TIMESTAMPTZ;
    credential_version TIMESTAMPTZ;
BEGIN
    SELECT account_id, strategy_instance_id, instrument_id, client_order_id, environment
    INTO intent_account, intent_instance, intent_instrument, intent_client_order_id, intent_environment
    FROM trading_intents WHERE id = NEW.intent_id;

    SELECT environment, updated_at INTO account_environment, account_version
    FROM trading_accounts WHERE id = NEW.account_id;

    SELECT updated_at INTO credential_version
    FROM trading_account_credentials
    WHERE account_id = NEW.account_id
      AND status = 'configured'
      AND verification_status = 'verified';

    IF intent_environment IS DISTINCT FROM 'testnet'
       OR account_environment IS DISTINCT FROM 'testnet'
       OR intent_account IS DISTINCT FROM NEW.account_id
       OR intent_instance IS DISTINCT FROM NEW.strategy_instance_id
       OR intent_instrument IS DISTINCT FROM NEW.instrument_id
       OR intent_client_order_id IS DISTINCT FROM NEW.client_order_id
       OR account_version IS DISTINCT FROM NEW.submitted_account_updated_at
       OR credential_version IS DISTINCT FROM NEW.credential_updated_at THEN
        RAISE EXCEPTION 'testnet order binding does not match current execution state';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER testnet_orders_validate_binding
BEFORE INSERT OR UPDATE OF
    account_id,
    intent_id,
    strategy_instance_id,
    instrument_id,
    credential_updated_at,
    submitted_account_updated_at,
    client_order_id
ON testnet_orders
FOR EACH ROW EXECUTE FUNCTION validate_testnet_order_binding();

CREATE INDEX ix_testnet_orders_account ON testnet_orders (account_id, created_at DESC, id DESC);
CREATE INDEX ix_testnet_orders_recovery
    ON testnet_orders (updated_at, id) WHERE status IN ('prepared', 'unknown', 'new', 'partially_filled');

-- +goose Down
LOCK TABLE testnet_orders, testnet_risk_states, trading_intents
IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE m3_testnet_orders_down_guard (
    row_count BIGINT NOT NULL CHECK (row_count = 0)
) ON COMMIT DROP;

INSERT INTO m3_testnet_orders_down_guard (row_count)
SELECT
    (SELECT COUNT(*) FROM testnet_orders)
    + (SELECT COUNT(*) FROM testnet_risk_states)
    + (SELECT COUNT(*) FROM trading_intents
        WHERE environment = 'testnet' AND status = 'reconciling');

DROP INDEX ix_testnet_orders_recovery;
DROP INDEX ix_testnet_orders_account;
DROP TRIGGER testnet_orders_validate_binding ON testnet_orders;
DROP FUNCTION validate_testnet_order_binding();
DROP TABLE testnet_orders;
DROP TABLE testnet_risk_states;

DROP INDEX ix_trading_intents_testnet_runnable;
DROP INDEX uq_trading_intents_testnet_account_active;

ALTER TABLE trading_intents
    DROP CONSTRAINT ck_trading_intents_claim_state,
    DROP CONSTRAINT ck_trading_intents_terminal_state,
    DROP CONSTRAINT ck_trading_intents_status,
    ADD CONSTRAINT ck_trading_intents_status CHECK (
        status IN ('pending', 'processing', 'executed', 'blocked', 'failed')
    ),
    ADD CONSTRAINT ck_trading_intents_terminal_state CHECK (
        (status IN ('pending', 'processing') AND completed_at IS NULL)
        OR (status IN ('executed', 'blocked', 'failed') AND completed_at IS NOT NULL AND isfinite(completed_at))
    ),
    ADD CONSTRAINT ck_trading_intents_claim_state CHECK (
        (status = 'processing' AND claimed_at IS NOT NULL AND worker_id IS NOT NULL)
        OR status <> 'processing'
    );
