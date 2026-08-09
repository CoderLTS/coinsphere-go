-- +goose Up

ALTER TABLE strategy_instances
    ADD COLUMN stop_loss_ratio NUMERIC(38,18),
    ADD CONSTRAINT ck_strategy_instances_stop_loss CHECK (
        stop_loss_ratio IS NULL OR (stop_loss_ratio > 0 AND stop_loss_ratio < 1)
    );

DROP TRIGGER testnet_orders_validate_binding ON testnet_orders;

ALTER TABLE testnet_orders
    DROP CONSTRAINT uq_testnet_orders_intent,
    DROP CONSTRAINT ck_testnet_orders_values,
    DROP CONSTRAINT ck_testnet_orders_state,
    ADD COLUMN purpose VARCHAR(16) NOT NULL DEFAULT 'rebalance',
    ADD COLUMN order_type VARCHAR(24) NOT NULL DEFAULT 'market',
    ADD COLUMN stop_price NUMERIC(38,18) NOT NULL DEFAULT 0,
    ADD COLUMN close_position BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN reduce_only BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN working_type VARCHAR(24) NOT NULL DEFAULT '',
    ADD COLUMN replaces_order_id UUID,
    ADD CONSTRAINT fk_testnet_orders_replaces_order
        FOREIGN KEY (replaces_order_id) REFERENCES testnet_orders (id) ON DELETE RESTRICT,
    ADD CONSTRAINT uq_testnet_orders_intent_purpose UNIQUE (intent_id, purpose),
    ADD CONSTRAINT ck_testnet_orders_purpose CHECK (
        purpose IN ('rebalance', 'protection', 'flatten')
    ),
    ADD CONSTRAINT ck_testnet_orders_order_shape CHECK (
        (purpose = 'rebalance'
            AND id = intent_id
            AND order_type = 'market'
            AND quantity > 0
            AND stop_price = 0
            AND NOT close_position
            AND working_type = ''
            AND replaces_order_id IS NULL)
        OR (purpose = 'flatten'
            AND order_type = 'market'
            AND quantity > 0
            AND stop_price = 0
            AND NOT close_position
            AND working_type = ''
            AND replaces_order_id IS NULL)
        OR (purpose = 'protection'
            AND stop_price > 0
            AND NOT reduce_only
            AND (
                (order_type = 'stop_loss'
                    AND quantity > 0
                    AND NOT close_position
                    AND working_type = '')
                OR (order_type = 'stop_market'
                    AND quantity = 0
                    AND close_position
                    AND working_type = 'mark_price')
            )
            AND (replaces_order_id IS NULL OR replaces_order_id <> id))
    ),
    ADD CONSTRAINT ck_testnet_orders_values CHECK (
        quantity >= 0
        AND filled_quantity >= 0
        AND (
            (purpose = 'protection' AND close_position)
            OR filled_quantity <= quantity
        )
        AND cumulative_quote_quantity >= 0
        AND (
            (filled_quantity = 0 AND cumulative_quote_quantity = 0 AND average_price = 0)
            OR (filled_quantity > 0 AND cumulative_quote_quantity > 0 AND average_price > 0)
        )
    ),
    ADD CONSTRAINT ck_testnet_orders_state CHECK (
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
            AND filled_quantity > 0
            AND (close_position OR filled_quantity < quantity)
            AND observed_at IS NOT NULL
            AND last_error_code = ''
        )
        OR (
            status = 'filled'
            AND exchange_order_id IS NOT NULL
            AND filled_quantity > 0
            AND (close_position OR filled_quantity = quantity)
            AND observed_at IS NOT NULL
            AND last_error_code = ''
        )
        OR (
            status IN ('canceled', 'expired')
            AND exchange_order_id IS NOT NULL
            AND observed_at IS NOT NULL
            AND last_error_code = ''
        )
    );

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION validate_testnet_order_binding() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
    intent_account UUID;
    intent_instance UUID;
    intent_instrument UUID;
    intent_client_order_id VARCHAR(64);
    intent_environment VARCHAR(16);
    account_environment VARCHAR(16);
    account_market VARCHAR(16);
    account_version TIMESTAMPTZ;
    credential_version TIMESTAMPTZ;
    replaced_account UUID;
    replaced_instrument UUID;
    replaced_purpose VARCHAR(16);
BEGIN
    SELECT account_id, strategy_instance_id, instrument_id, client_order_id, environment
    INTO intent_account, intent_instance, intent_instrument, intent_client_order_id, intent_environment
    FROM trading_intents WHERE id = NEW.intent_id;

    SELECT environment, market_type, updated_at
    INTO account_environment, account_market, account_version
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
       OR account_version IS DISTINCT FROM NEW.submitted_account_updated_at
       OR credential_version IS DISTINCT FROM NEW.credential_updated_at
       OR (NEW.purpose = 'rebalance' AND intent_client_order_id IS DISTINCT FROM NEW.client_order_id)
       OR (NEW.purpose <> 'rebalance' AND intent_client_order_id IS NOT DISTINCT FROM NEW.client_order_id) THEN
        RAISE EXCEPTION 'testnet order binding does not match current execution state';
    END IF;

    IF NEW.purpose = 'protection'
       AND ((account_market = 'spot' AND (
                NEW.order_type <> 'stop_loss'
                OR NEW.close_position
                OR NEW.quantity <= 0
                OR NEW.working_type <> ''
            ))
            OR (account_market = 'usd_m' AND (
                NEW.order_type <> 'stop_market'
                OR NOT NEW.close_position
                OR NEW.quantity <> 0
                OR NEW.working_type <> 'mark_price'
            ))) THEN
        RAISE EXCEPTION 'testnet protection order shape does not match account market';
    END IF;

    IF NEW.replaces_order_id IS NOT NULL THEN
        SELECT account_id, instrument_id, purpose
        INTO replaced_account, replaced_instrument, replaced_purpose
        FROM testnet_orders WHERE id = NEW.replaces_order_id;
        IF replaced_account IS DISTINCT FROM NEW.account_id
           OR replaced_instrument IS DISTINCT FROM NEW.instrument_id
           OR replaced_purpose IS DISTINCT FROM 'protection' THEN
            RAISE EXCEPTION 'testnet replacement order binding is invalid';
        END IF;
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
    client_order_id,
    purpose,
    order_type,
    close_position,
    working_type,
    replaces_order_id
ON testnet_orders
FOR EACH ROW EXECUTE FUNCTION validate_testnet_order_binding();

CREATE UNIQUE INDEX uq_testnet_orders_active_protection
    ON testnet_orders (account_id, instrument_id)
    WHERE purpose = 'protection'
      AND status IN ('prepared', 'unknown', 'new', 'partially_filled');

-- +goose Down
LOCK TABLE testnet_orders, strategy_instances IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE m3_testnet_protective_orders_down_guard (
    row_count BIGINT NOT NULL CHECK (row_count = 0)
) ON COMMIT DROP;

INSERT INTO m3_testnet_protective_orders_down_guard (row_count)
SELECT
    (SELECT COUNT(*) FROM testnet_orders WHERE purpose <> 'rebalance')
    + (SELECT COUNT(*) FROM strategy_instances WHERE stop_loss_ratio IS NOT NULL);

DROP INDEX uq_testnet_orders_active_protection;
DROP TRIGGER testnet_orders_validate_binding ON testnet_orders;

ALTER TABLE testnet_orders
    DROP CONSTRAINT ck_testnet_orders_state,
    DROP CONSTRAINT ck_testnet_orders_values,
    DROP CONSTRAINT ck_testnet_orders_order_shape,
    DROP CONSTRAINT ck_testnet_orders_purpose,
    DROP CONSTRAINT uq_testnet_orders_intent_purpose,
    DROP CONSTRAINT fk_testnet_orders_replaces_order,
    DROP COLUMN replaces_order_id,
    DROP COLUMN working_type,
    DROP COLUMN reduce_only,
    DROP COLUMN close_position,
    DROP COLUMN stop_price,
    DROP COLUMN order_type,
    DROP COLUMN purpose,
    ADD CONSTRAINT uq_testnet_orders_intent UNIQUE (intent_id),
    ADD CONSTRAINT ck_testnet_orders_values CHECK (
        quantity > 0
        AND filled_quantity >= 0 AND filled_quantity <= quantity
        AND cumulative_quote_quantity >= 0
        AND (
            (filled_quantity = 0 AND cumulative_quote_quantity = 0 AND average_price = 0)
            OR (filled_quantity > 0 AND cumulative_quote_quantity > 0 AND average_price > 0)
        )
    ),
    ADD CONSTRAINT ck_testnet_orders_state CHECK (
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
    );

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION validate_testnet_order_binding() RETURNS TRIGGER
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

ALTER TABLE strategy_instances
    DROP CONSTRAINT ck_strategy_instances_stop_loss,
    DROP COLUMN stop_loss_ratio;
