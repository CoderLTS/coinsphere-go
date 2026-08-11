-- +goose Up

LOCK TABLE
    trading_accounts,
    trading_intents,
    strategy_instances,
    testnet_orders
IN ACCESS EXCLUSIVE MODE;

ALTER TABLE trading_accounts
    DROP CONSTRAINT ck_trading_accounts_spot_live_manual,
    ADD COLUMN auto_authorized_at TIMESTAMPTZ,
    ADD COLUMN auto_authorized_by_user_id BIGINT,
    ADD CONSTRAINT fk_trading_accounts_auto_authorized_by
        FOREIGN KEY (auto_authorized_by_user_id) REFERENCES users (id) ON DELETE RESTRICT,
    ADD CONSTRAINT ck_trading_accounts_auto_authorization CHECK (
        (auto_authorized_at IS NULL AND auto_authorized_by_user_id IS NULL)
        OR (
            environment = 'live'
            AND auto_authorized_at IS NOT NULL
            AND isfinite(auto_authorized_at)
            AND auto_authorized_by_user_id IS NOT NULL
            AND auto_authorized_by_user_id = owner_user_id
        )
    ),
    ADD CONSTRAINT ck_trading_accounts_spot_live_auto CHECK (
        environment <> 'live'
        OR (
            market_type = 'spot'
            AND (status = 'paused' OR manual_authorized_at IS NOT NULL)
            AND (
                (
                    NOT automation_enabled
                    AND auto_authorized_at IS NULL
                    AND auto_authorized_by_user_id IS NULL
                )
                OR (
                    automation_enabled
                    AND status = 'active'
                    AND manual_authorized_at IS NOT NULL
                    AND automation_authorized_at IS NOT NULL
                    AND auto_authorized_at IS NOT NULL
                )
            )
        )
    );

ALTER TABLE trading_intents
    DROP CONSTRAINT ck_trading_intents_spot_live_manual,
    ADD CONSTRAINT ck_trading_intents_spot_live_auto CHECK (
        environment <> 'live' OR (market_type = 'spot' AND mode IN ('manual', 'auto'))
    );

ALTER TABLE strategy_instances
    DROP CONSTRAINT ck_strategy_instances_spot_live_manual,
    ADD CONSTRAINT ck_strategy_instances_spot_live_auto CHECK (
        environment <> 'live' OR mode IN ('signal_only', 'manual', 'auto')
    );

DROP INDEX ix_trading_intents_live_runnable;
CREATE INDEX ix_trading_intents_live_runnable
    ON trading_intents (created_at, id)
    WHERE environment = 'live' AND market_type = 'spot' AND mode IN ('manual', 'auto')
      AND status IN ('pending', 'reconciling');

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION validate_testnet_order_binding() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
    intent_account UUID;
    intent_instance UUID;
    intent_instrument UUID;
    intent_client_order_id VARCHAR(64);
    intent_environment VARCHAR(16);
    intent_mode VARCHAR(16);
    account_environment VARCHAR(16);
    account_market VARCHAR(16);
    account_status VARCHAR(16);
    account_automation_enabled BOOLEAN;
    account_manual_authorized_at TIMESTAMPTZ;
    account_automation_authorized_at TIMESTAMPTZ;
    account_auto_authorized_at TIMESTAMPTZ;
    account_version TIMESTAMPTZ;
    credential_version TIMESTAMPTZ;
    replaced_account UUID;
    replaced_instrument UUID;
    replaced_purpose VARCHAR(16);
BEGIN
    SELECT account_id, strategy_instance_id, instrument_id, client_order_id, environment, mode
    INTO intent_account, intent_instance, intent_instrument, intent_client_order_id, intent_environment, intent_mode
    FROM trading_intents WHERE id = NEW.intent_id;

    SELECT environment, market_type, status, automation_enabled, manual_authorized_at,
           automation_authorized_at, auto_authorized_at, updated_at
    INTO account_environment, account_market, account_status, account_automation_enabled,
         account_manual_authorized_at, account_automation_authorized_at,
         account_auto_authorized_at, account_version
    FROM trading_accounts WHERE id = NEW.account_id;

    SELECT updated_at INTO credential_version
    FROM trading_account_credentials
    WHERE account_id = NEW.account_id
      AND status = 'configured'
      AND verification_status = 'verified';

    IF intent_environment NOT IN ('testnet', 'live')
       OR account_environment IS DISTINCT FROM intent_environment
       OR (account_environment = 'live' AND (account_market <> 'spot' OR intent_mode NOT IN ('manual', 'auto')))
       OR (account_environment = 'live' AND NEW.purpose = 'rebalance'
           AND (account_status <> 'active' OR account_manual_authorized_at IS NULL))
       OR (account_environment = 'live' AND intent_mode = 'auto' AND NEW.purpose = 'rebalance'
           AND (NOT account_automation_enabled
                OR account_automation_authorized_at IS NULL
                OR account_auto_authorized_at IS NULL))
       OR intent_account IS DISTINCT FROM NEW.account_id
       OR intent_instance IS DISTINCT FROM NEW.strategy_instance_id
       OR intent_instrument IS DISTINCT FROM NEW.instrument_id
       OR account_version IS DISTINCT FROM NEW.submitted_account_updated_at
       OR credential_version IS DISTINCT FROM NEW.credential_updated_at
       OR (NEW.purpose = 'rebalance' AND intent_client_order_id IS DISTINCT FROM NEW.client_order_id)
       OR (NEW.purpose <> 'rebalance' AND intent_client_order_id IS NOT DISTINCT FROM NEW.client_order_id) THEN
        RAISE EXCEPTION 'private order binding does not match current execution state';
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
        RAISE EXCEPTION 'private protection order shape does not match account market';
    END IF;

    IF NEW.purpose = 'rebalance' AND account_market = 'spot' AND NEW.reduce_only THEN
        RAISE EXCEPTION 'private Spot rebalance cannot set reduceOnly';
    END IF;

    IF NEW.purpose = 'flatten'
       AND NEW.reduce_only IS DISTINCT FROM (account_market = 'usd_m') THEN
        RAISE EXCEPTION 'private flatten order shape does not match account market';
    END IF;

    IF NEW.replaces_order_id IS NOT NULL THEN
        SELECT account_id, instrument_id, purpose
        INTO replaced_account, replaced_instrument, replaced_purpose
        FROM testnet_orders WHERE id = NEW.replaces_order_id;
        IF replaced_account IS DISTINCT FROM NEW.account_id
           OR replaced_instrument IS DISTINCT FROM NEW.instrument_id
           OR replaced_purpose IS DISTINCT FROM 'protection' THEN
            RAISE EXCEPTION 'private replacement order binding is invalid';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down

LOCK TABLE
    trading_accounts,
    trading_intents,
    strategy_instances,
    testnet_orders
IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE m4_spot_live_auto_down_guard (
    row_count BIGINT NOT NULL CHECK (row_count = 0)
) ON COMMIT DROP;

INSERT INTO m4_spot_live_auto_down_guard (row_count)
SELECT
    (SELECT COUNT(*) FROM trading_accounts
     WHERE environment = 'live'
       AND (automation_enabled OR automation_authorized_at IS NOT NULL OR auto_authorized_at IS NOT NULL))
    + (SELECT COUNT(*) FROM trading_intents WHERE environment = 'live' AND mode = 'auto')
    + (SELECT COUNT(*) FROM strategy_instances WHERE environment = 'live' AND mode = 'auto');

DROP INDEX ix_trading_intents_live_runnable;
CREATE INDEX ix_trading_intents_live_runnable
    ON trading_intents (created_at, id)
    WHERE environment = 'live' AND market_type = 'spot' AND mode = 'manual'
      AND status IN ('pending', 'reconciling');

ALTER TABLE strategy_instances
    DROP CONSTRAINT ck_strategy_instances_spot_live_auto,
    ADD CONSTRAINT ck_strategy_instances_spot_live_manual CHECK (
        environment <> 'live' OR mode IN ('signal_only', 'manual')
    );

ALTER TABLE trading_intents
    DROP CONSTRAINT ck_trading_intents_spot_live_auto,
    ADD CONSTRAINT ck_trading_intents_spot_live_manual CHECK (
        environment <> 'live' OR (market_type = 'spot' AND mode = 'manual')
    );

ALTER TABLE trading_accounts
    DROP CONSTRAINT ck_trading_accounts_spot_live_auto,
    DROP CONSTRAINT ck_trading_accounts_auto_authorization,
    DROP CONSTRAINT fk_trading_accounts_auto_authorized_by,
    DROP COLUMN auto_authorized_by_user_id,
    DROP COLUMN auto_authorized_at,
    ADD CONSTRAINT ck_trading_accounts_spot_live_manual CHECK (
        environment <> 'live'
        OR (
            market_type = 'spot'
            AND NOT automation_enabled
            AND automation_authorized_at IS NULL
            AND automation_authorized_by_user_id IS NULL
            AND (status = 'paused' OR manual_authorized_at IS NOT NULL)
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
    intent_mode VARCHAR(16);
    account_environment VARCHAR(16);
    account_market VARCHAR(16);
    account_status VARCHAR(16);
    account_manual_authorized_at TIMESTAMPTZ;
    account_version TIMESTAMPTZ;
    credential_version TIMESTAMPTZ;
    replaced_account UUID;
    replaced_instrument UUID;
    replaced_purpose VARCHAR(16);
BEGIN
    SELECT account_id, strategy_instance_id, instrument_id, client_order_id, environment, mode
    INTO intent_account, intent_instance, intent_instrument, intent_client_order_id, intent_environment, intent_mode
    FROM trading_intents WHERE id = NEW.intent_id;

    SELECT environment, market_type, status, manual_authorized_at, updated_at
    INTO account_environment, account_market, account_status, account_manual_authorized_at, account_version
    FROM trading_accounts WHERE id = NEW.account_id;

    SELECT updated_at INTO credential_version
    FROM trading_account_credentials
    WHERE account_id = NEW.account_id
      AND status = 'configured'
      AND verification_status = 'verified';

    IF intent_environment NOT IN ('testnet', 'live')
       OR account_environment IS DISTINCT FROM intent_environment
       OR (account_environment = 'live' AND (account_market <> 'spot' OR intent_mode <> 'manual'))
       OR (account_environment = 'live' AND NEW.purpose <> 'flatten'
           AND (account_status <> 'active' OR account_manual_authorized_at IS NULL))
       OR intent_account IS DISTINCT FROM NEW.account_id
       OR intent_instance IS DISTINCT FROM NEW.strategy_instance_id
       OR intent_instrument IS DISTINCT FROM NEW.instrument_id
       OR account_version IS DISTINCT FROM NEW.submitted_account_updated_at
       OR credential_version IS DISTINCT FROM NEW.credential_updated_at
       OR (NEW.purpose = 'rebalance' AND intent_client_order_id IS DISTINCT FROM NEW.client_order_id)
       OR (NEW.purpose <> 'rebalance' AND intent_client_order_id IS NOT DISTINCT FROM NEW.client_order_id) THEN
        RAISE EXCEPTION 'private order binding does not match current execution state';
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
        RAISE EXCEPTION 'private protection order shape does not match account market';
    END IF;

    IF NEW.purpose = 'rebalance' AND account_market = 'spot' AND NEW.reduce_only THEN
        RAISE EXCEPTION 'private Spot rebalance cannot set reduceOnly';
    END IF;

    IF NEW.purpose = 'flatten'
       AND NEW.reduce_only IS DISTINCT FROM (account_market = 'usd_m') THEN
        RAISE EXCEPTION 'private flatten order shape does not match account market';
    END IF;

    IF NEW.replaces_order_id IS NOT NULL THEN
        SELECT account_id, instrument_id, purpose
        INTO replaced_account, replaced_instrument, replaced_purpose
        FROM testnet_orders WHERE id = NEW.replaces_order_id;
        IF replaced_account IS DISTINCT FROM NEW.account_id
           OR replaced_instrument IS DISTINCT FROM NEW.instrument_id
           OR replaced_purpose IS DISTINCT FROM 'protection' THEN
            RAISE EXCEPTION 'private replacement order binding is invalid';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
