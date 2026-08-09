-- +goose Up

ALTER TABLE trading_account_credentials
    ADD CONSTRAINT uq_trading_account_credentials_version UNIQUE (account_id, updated_at);

CREATE TABLE testnet_reconciliations (
    account_id UUID NOT NULL,
    credential_updated_at TIMESTAMPTZ NOT NULL,
    status VARCHAR(16) NOT NULL,
    error_code VARCHAR(64) NOT NULL DEFAULT '',
    balance_count INTEGER NOT NULL DEFAULT 0,
    position_count INTEGER NOT NULL DEFAULT 0,
    open_order_count INTEGER NOT NULL DEFAULT 0,
    last_attempted_at TIMESTAMPTZ NOT NULL,
    last_observed_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT testnet_reconciliations_pkey PRIMARY KEY (account_id),
    CONSTRAINT uq_testnet_reconciliations_version UNIQUE (account_id, credential_updated_at),
    CONSTRAINT fk_testnet_reconciliations_credential
        FOREIGN KEY (account_id, credential_updated_at)
        REFERENCES trading_account_credentials (account_id, updated_at) ON DELETE RESTRICT ON UPDATE RESTRICT,
    CONSTRAINT ck_testnet_reconciliations_status CHECK (status IN ('matched', 'mismatch', 'unknown')),
    CONSTRAINT ck_testnet_reconciliations_shape CHECK (
        (status = 'matched' AND error_code = '' AND last_observed_at IS NOT NULL)
        OR (status = 'mismatch' AND error_code <> '' AND last_observed_at IS NOT NULL)
        OR (status = 'unknown' AND error_code <> '')
    ),
    CONSTRAINT ck_testnet_reconciliations_counts CHECK (
        balance_count >= 0 AND position_count >= 0 AND open_order_count >= 0
    ),
    CONSTRAINT ck_testnet_reconciliations_times CHECK (
        isfinite(credential_updated_at)
        AND isfinite(last_attempted_at)
        AND (last_observed_at IS NULL OR isfinite(last_observed_at))
        AND isfinite(updated_at)
    )
);

CREATE TABLE testnet_balances (
    account_id UUID NOT NULL,
    credential_updated_at TIMESTAMPTZ NOT NULL,
    asset VARCHAR(32) NOT NULL,
    total_balance NUMERIC(38,18) NOT NULL,
    available_balance NUMERIC(38,18) NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT testnet_balances_pkey PRIMARY KEY (account_id, asset),
    CONSTRAINT fk_testnet_balances_reconciliation
        FOREIGN KEY (account_id, credential_updated_at)
        REFERENCES testnet_reconciliations (account_id, credential_updated_at) ON DELETE RESTRICT ON UPDATE RESTRICT,
    CONSTRAINT ck_testnet_balances_asset CHECK (asset = BTRIM(asset) AND asset <> ''),
    CONSTRAINT ck_testnet_balances_values CHECK (total_balance >= 0),
    CONSTRAINT ck_testnet_balances_observed_at CHECK (isfinite(observed_at))
);

CREATE TABLE testnet_positions (
    account_id UUID NOT NULL,
    credential_updated_at TIMESTAMPTZ NOT NULL,
    native_symbol VARCHAR(64) NOT NULL,
    position_side VARCHAR(8) NOT NULL,
    quantity NUMERIC(38,18) NOT NULL,
    entry_price NUMERIC(38,18) NOT NULL,
    unrealized_pnl NUMERIC(38,18) NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT testnet_positions_pkey PRIMARY KEY (account_id, native_symbol, position_side),
    CONSTRAINT fk_testnet_positions_reconciliation
        FOREIGN KEY (account_id, credential_updated_at)
        REFERENCES testnet_reconciliations (account_id, credential_updated_at) ON DELETE RESTRICT ON UPDATE RESTRICT,
    CONSTRAINT ck_testnet_positions_symbol CHECK (native_symbol = BTRIM(native_symbol) AND native_symbol <> ''),
    CONSTRAINT ck_testnet_positions_side CHECK (position_side IN ('both', 'long', 'short')),
    CONSTRAINT ck_testnet_positions_values CHECK (
        entry_price >= 0 AND (quantity = 0 OR entry_price > 0)
    ),
    CONSTRAINT ck_testnet_positions_observed_at CHECK (isfinite(observed_at))
);

CREATE TABLE testnet_open_orders (
    account_id UUID NOT NULL,
    credential_updated_at TIMESTAMPTZ NOT NULL,
    native_symbol VARCHAR(64) NOT NULL,
    exchange_order_id BIGINT NOT NULL,
    client_order_id VARCHAR(64) NOT NULL,
    side VARCHAR(8) NOT NULL,
    order_type VARCHAR(32) NOT NULL,
    status VARCHAR(32) NOT NULL,
    price NUMERIC(38,18) NOT NULL,
    original_quantity NUMERIC(38,18) NOT NULL,
    executed_quantity NUMERIC(38,18) NOT NULL,
    stop_price NUMERIC(38,18) NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT testnet_open_orders_pkey PRIMARY KEY (account_id, native_symbol, exchange_order_id),
    CONSTRAINT fk_testnet_open_orders_reconciliation
        FOREIGN KEY (account_id, credential_updated_at)
        REFERENCES testnet_reconciliations (account_id, credential_updated_at) ON DELETE RESTRICT ON UPDATE RESTRICT,
    CONSTRAINT ck_testnet_open_orders_identity CHECK (
        native_symbol = BTRIM(native_symbol) AND native_symbol <> ''
        AND exchange_order_id > 0
        AND client_order_id = BTRIM(client_order_id) AND client_order_id <> ''
    ),
    CONSTRAINT ck_testnet_open_orders_side CHECK (side IN ('buy', 'sell')),
    CONSTRAINT ck_testnet_open_orders_values CHECK (
        price >= 0 AND stop_price >= 0
        AND original_quantity > 0
        AND executed_quantity >= 0 AND executed_quantity <= original_quantity
    ),
    CONSTRAINT ck_testnet_open_orders_observed_at CHECK (isfinite(observed_at))
);

CREATE INDEX ix_testnet_reconciliations_status
    ON testnet_reconciliations (updated_at, account_id) WHERE status <> 'matched';
CREATE INDEX ix_testnet_balances_account ON testnet_balances (account_id, asset);
CREATE INDEX ix_testnet_positions_account ON testnet_positions (account_id, native_symbol);
CREATE INDEX ix_testnet_open_orders_account ON testnet_open_orders (account_id, native_symbol);

-- +goose Down
LOCK TABLE
    testnet_open_orders,
    testnet_positions,
    testnet_balances,
    testnet_reconciliations,
    trading_account_credentials
IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE m3_testnet_reconciliation_down_guard (
    row_count BIGINT NOT NULL CHECK (row_count = 0)
) ON COMMIT DROP;

INSERT INTO m3_testnet_reconciliation_down_guard (row_count)
SELECT
    (SELECT COUNT(*) FROM testnet_reconciliations)
    + (SELECT COUNT(*) FROM testnet_balances)
    + (SELECT COUNT(*) FROM testnet_positions)
    + (SELECT COUNT(*) FROM testnet_open_orders);

DROP INDEX ix_testnet_open_orders_account;
DROP INDEX ix_testnet_positions_account;
DROP INDEX ix_testnet_balances_account;
DROP INDEX ix_testnet_reconciliations_status;
DROP TABLE testnet_open_orders;
DROP TABLE testnet_positions;
DROP TABLE testnet_balances;
DROP TABLE testnet_reconciliations;

ALTER TABLE trading_account_credentials
    DROP CONSTRAINT uq_trading_account_credentials_version;
