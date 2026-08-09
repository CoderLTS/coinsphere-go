-- +goose Up

CREATE TABLE testnet_trade_facts (
    id BIGSERIAL NOT NULL,
    account_id UUID NOT NULL,
    credential_updated_at TIMESTAMPTZ NOT NULL,
    instrument_id UUID,
    order_id UUID,
    intent_id UUID,
    event_type VARCHAR(8) NOT NULL,
    symbol VARCHAR(64) NOT NULL DEFAULT '',
    external_trade_id BIGINT,
    external_transaction_id VARCHAR(64) NOT NULL DEFAULT '',
    side VARCHAR(4) NOT NULL DEFAULT '',
    position_side VARCHAR(8) NOT NULL DEFAULT '',
    quantity NUMERIC(38,18) NOT NULL DEFAULT 0,
    price NUMERIC(38,18) NOT NULL DEFAULT 0,
    quote_quantity NUMERIC(38,18) NOT NULL DEFAULT 0,
    amount NUMERIC(38,18) NOT NULL DEFAULT 0,
    asset VARCHAR(32) NOT NULL,
    realized_pnl NUMERIC(38,18) NOT NULL DEFAULT 0,
    buyer BOOLEAN NOT NULL DEFAULT FALSE,
    maker BOOLEAN NOT NULL DEFAULT FALSE,
    occurred_at TIMESTAMPTZ NOT NULL,
    dedupe_key VARCHAR(192) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT testnet_trade_facts_pkey PRIMARY KEY (id),
    CONSTRAINT fk_testnet_trade_facts_account
        FOREIGN KEY (account_id) REFERENCES trading_accounts (id) ON DELETE RESTRICT,
    CONSTRAINT fk_testnet_trade_facts_credential
        FOREIGN KEY (account_id, credential_updated_at)
        REFERENCES trading_account_credentials (account_id, updated_at) ON DELETE RESTRICT ON UPDATE RESTRICT,
    CONSTRAINT fk_testnet_trade_facts_instrument
        FOREIGN KEY (instrument_id) REFERENCES market_instruments (id) ON DELETE RESTRICT,
    CONSTRAINT fk_testnet_trade_facts_order
        FOREIGN KEY (order_id) REFERENCES testnet_orders (id) ON DELETE RESTRICT,
    CONSTRAINT fk_testnet_trade_facts_intent
        FOREIGN KEY (intent_id) REFERENCES trading_intents (id) ON DELETE RESTRICT,
    CONSTRAINT uq_testnet_trade_facts_dedupe UNIQUE (account_id, credential_updated_at, dedupe_key),
    CONSTRAINT ck_testnet_trade_facts_event_type CHECK (event_type IN ('fill', 'fee', 'funding')),
    CONSTRAINT ck_testnet_trade_facts_identity CHECK (
        symbol = BTRIM(symbol)
        AND asset = BTRIM(asset) AND asset <> ''
        AND dedupe_key = BTRIM(dedupe_key) AND dedupe_key <> ''
        AND external_transaction_id = BTRIM(external_transaction_id)
    ),
    CONSTRAINT ck_testnet_trade_facts_shape CHECK (
        (
            event_type = 'fill'
            AND instrument_id IS NOT NULL
            AND order_id IS NOT NULL
            AND intent_id IS NOT NULL
            AND symbol <> ''
            AND external_trade_id IS NOT NULL AND external_trade_id > 0
            AND external_transaction_id = ''
            AND side IN ('buy', 'sell')
            AND position_side IN ('both', 'long', 'short')
            AND quantity > 0 AND price > 0 AND quote_quantity > 0
            AND amount = 0
        )
        OR (
            event_type = 'fee'
            AND instrument_id IS NOT NULL
            AND order_id IS NOT NULL
            AND intent_id IS NOT NULL
            AND symbol <> ''
            AND external_trade_id IS NOT NULL AND external_trade_id > 0
            AND external_transaction_id = ''
            AND side = '' AND position_side = ''
            AND quantity = 0 AND price = 0 AND quote_quantity = 0
            AND amount >= 0
            AND NOT buyer AND NOT maker
            AND realized_pnl = 0
        )
        OR (
            event_type = 'funding'
            AND order_id IS NULL
            AND intent_id IS NULL
            AND external_trade_id IS NULL
            AND external_transaction_id <> ''
            AND (
                (instrument_id IS NULL AND symbol = '')
                OR (instrument_id IS NOT NULL AND symbol <> '')
            )
            AND side = '' AND position_side = ''
            AND quantity = 0 AND price = 0 AND quote_quantity = 0
            AND NOT buyer AND NOT maker
            AND realized_pnl = 0
        )
    ),
    CONSTRAINT ck_testnet_trade_facts_times CHECK (
        isfinite(credential_updated_at) AND isfinite(occurred_at) AND isfinite(created_at)
    )
);

-- The service supplies local order/intent IDs only after checking the exact
-- account, credential version and instrument. Keep the same invariant in SQL
-- so a direct writer cannot attach an authoritative fact to another account.
-- +goose StatementBegin
CREATE FUNCTION validate_testnet_trade_fact_binding() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
    order_account UUID;
    order_credential TIMESTAMPTZ;
    order_intent UUID;
    order_instrument UUID;
    intent_account UUID;
    intent_instrument UUID;
    intent_environment VARCHAR(16);
    account_market VARCHAR(16);
    instrument_symbol VARCHAR(64);
    instrument_market VARCHAR(16);
    instrument_venue VARCHAR(16);
BEGIN
    SELECT market_type INTO account_market
    FROM trading_accounts WHERE id = NEW.account_id;

    IF NEW.event_type = 'funding' AND account_market IS DISTINCT FROM 'usd_m' THEN
        RAISE EXCEPTION 'testnet funding fact account market is invalid';
    END IF;

    IF NEW.instrument_id IS NOT NULL THEN
        SELECT native_symbol, market_type, venue
        INTO instrument_symbol, instrument_market, instrument_venue
        FROM market_instruments WHERE id = NEW.instrument_id;
        IF instrument_symbol IS DISTINCT FROM NEW.symbol
           OR instrument_market IS DISTINCT FROM account_market
           OR instrument_venue IS DISTINCT FROM 'binance'
           OR NOT EXISTS (
               SELECT 1 FROM trading_account_instruments
               WHERE account_id = NEW.account_id
                 AND instrument_id = NEW.instrument_id
           ) THEN
            RAISE EXCEPTION 'testnet trade fact instrument binding is invalid';
        END IF;
    END IF;

    IF NEW.order_id IS NOT NULL THEN
        SELECT account_id, credential_updated_at, intent_id, instrument_id
        INTO order_account, order_credential, order_intent, order_instrument
        FROM testnet_orders WHERE id = NEW.order_id;
        IF order_account IS DISTINCT FROM NEW.account_id
           OR order_credential IS DISTINCT FROM NEW.credential_updated_at
           OR order_intent IS DISTINCT FROM NEW.intent_id
           OR order_instrument IS DISTINCT FROM NEW.instrument_id THEN
            RAISE EXCEPTION 'testnet trade fact order binding is invalid';
        END IF;
    END IF;

    IF NEW.intent_id IS NOT NULL THEN
        SELECT account_id, instrument_id, environment
        INTO intent_account, intent_instrument, intent_environment
        FROM trading_intents WHERE id = NEW.intent_id;
        IF intent_account IS DISTINCT FROM NEW.account_id
           OR intent_instrument IS DISTINCT FROM NEW.instrument_id
           OR intent_environment IS DISTINCT FROM 'testnet' THEN
            RAISE EXCEPTION 'testnet trade fact intent binding is invalid';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER testnet_trade_facts_validate_binding
BEFORE INSERT ON testnet_trade_facts
FOR EACH ROW EXECUTE FUNCTION validate_testnet_trade_fact_binding();

-- Trade facts are the audit source and may only be corrected by appending a
-- later fact; mutation would make a previous reconciliation unverifiable.
-- +goose StatementBegin
CREATE FUNCTION reject_testnet_trade_fact_mutation() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'testnet_trade_facts is append-only';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER testnet_trade_facts_append_only
BEFORE UPDATE OR DELETE ON testnet_trade_facts
FOR EACH ROW EXECUTE FUNCTION reject_testnet_trade_fact_mutation();

CREATE INDEX ix_testnet_trade_facts_account
    ON testnet_trade_facts (account_id, occurred_at, id);
CREATE INDEX ix_testnet_trade_facts_order
    ON testnet_trade_facts (order_id, occurred_at, id)
    WHERE order_id IS NOT NULL;

-- +goose Down
LOCK TABLE testnet_trade_facts IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE m3_testnet_trade_facts_down_guard (
    row_count BIGINT NOT NULL CHECK (row_count = 0)
) ON COMMIT DROP;

INSERT INTO m3_testnet_trade_facts_down_guard (row_count)
SELECT COUNT(*) FROM testnet_trade_facts;

DROP INDEX ix_testnet_trade_facts_order;
DROP INDEX ix_testnet_trade_facts_account;
DROP TRIGGER testnet_trade_facts_append_only ON testnet_trade_facts;
DROP FUNCTION reject_testnet_trade_fact_mutation();
DROP TRIGGER testnet_trade_facts_validate_binding ON testnet_trade_facts;
DROP FUNCTION validate_testnet_trade_fact_binding();
DROP TABLE testnet_trade_facts;
