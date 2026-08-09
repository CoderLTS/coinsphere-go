-- +goose Up

ALTER TABLE trading_accounts
    DROP CONSTRAINT ck_trading_accounts_environment,
    ADD CONSTRAINT ck_trading_accounts_environment CHECK (environment IN ('paper', 'testnet'));

ALTER TABLE trading_intents
    DROP CONSTRAINT ck_trading_intents_environment,
    ADD CONSTRAINT ck_trading_intents_environment CHECK (environment IN ('paper', 'testnet'));

CREATE TABLE trading_account_credentials (
    id UUID NOT NULL,
    account_id UUID NOT NULL,
    owner_user_id BIGINT NOT NULL,
    api_key_ciphertext TEXT NOT NULL DEFAULT '',
    api_secret_ciphertext TEXT NOT NULL DEFAULT '',
    withdrawal_disabled BOOLEAN NOT NULL DEFAULT FALSE,
    ip_whitelist_configured BOOLEAN NOT NULL DEFAULT FALSE,
    status VARCHAR(16) NOT NULL DEFAULT 'configured',
    verification_status VARCHAR(16) NOT NULL DEFAULT 'unverified',
    verification_error_code VARCHAR(64) NOT NULL DEFAULT '',
    last_verified_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT trading_account_credentials_pkey PRIMARY KEY (id),
    CONSTRAINT fk_trading_account_credentials_account
        FOREIGN KEY (account_id) REFERENCES trading_accounts (id) ON DELETE RESTRICT,
    CONSTRAINT fk_trading_account_credentials_owner
        FOREIGN KEY (owner_user_id) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT uq_trading_account_credentials_account UNIQUE (account_id),
    CONSTRAINT ck_trading_account_credentials_id_uuidv7 CHECK (
        id::text ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
    ),
    CONSTRAINT ck_trading_account_credentials_status CHECK (status IN ('configured', 'revoked')),
    CONSTRAINT ck_trading_account_credentials_verification CHECK (
        verification_status IN ('unverified', 'verified', 'invalid', 'unknown')
    ),
    CONSTRAINT ck_trading_account_credentials_shape CHECK (
        (
            status = 'configured'
            AND api_key_ciphertext <> ''
            AND api_secret_ciphertext <> ''
            AND withdrawal_disabled
            AND ip_whitelist_configured
        )
        OR (
            status = 'revoked'
            AND api_key_ciphertext = ''
            AND api_secret_ciphertext = ''
        )
    ),
    CONSTRAINT ck_trading_account_credentials_verified CHECK (
        (verification_status <> 'verified' AND last_verified_at IS NULL)
        OR (verification_status = 'verified' AND last_verified_at IS NOT NULL AND isfinite(last_verified_at))
    ),
    CONSTRAINT ck_trading_account_credentials_error CHECK (length(verification_error_code) <= 64),
    CONSTRAINT ck_trading_account_credentials_times CHECK (
        isfinite(created_at) AND isfinite(updated_at)
        AND (last_verified_at IS NULL OR isfinite(last_verified_at))
    )
);

-- Credentials are a Testnet-only boundary. Keep the invariant in the database
-- so a direct SQL writer cannot attach a secret to a Paper account.
-- +goose StatementBegin
CREATE FUNCTION validate_testnet_trading_credential() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
    account_environment VARCHAR(16);
    account_owner BIGINT;
BEGIN
    SELECT environment, owner_user_id INTO account_environment, account_owner
    FROM trading_accounts WHERE id = NEW.account_id;
    IF account_environment IS DISTINCT FROM 'testnet' THEN
        RAISE EXCEPTION 'trading credentials require a testnet account';
    END IF;
    IF account_owner IS DISTINCT FROM NEW.owner_user_id THEN
        RAISE EXCEPTION 'trading credential owner does not match account owner';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER trading_account_credentials_testnet_only
BEFORE INSERT OR UPDATE ON trading_account_credentials
FOR EACH ROW EXECUTE FUNCTION validate_testnet_trading_credential();

-- +goose StatementBegin
CREATE FUNCTION preserve_testnet_trading_credential_account() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM trading_account_credentials
        WHERE account_id = OLD.id
          AND (NEW.environment IS DISTINCT FROM 'testnet' OR owner_user_id IS DISTINCT FROM NEW.owner_user_id)
    ) THEN
        RAISE EXCEPTION 'trading account update would invalidate its credential binding';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER trading_accounts_preserve_credential_binding
BEFORE UPDATE OF environment, owner_user_id ON trading_accounts
FOR EACH ROW EXECUTE FUNCTION preserve_testnet_trading_credential_account();

CREATE INDEX ix_trading_account_credentials_owner
    ON trading_account_credentials (owner_user_id, account_id);

-- +goose Down
LOCK TABLE trading_account_credentials, trading_accounts, trading_intents
IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE m3_testnet_credentials_down_guard (
    row_count BIGINT NOT NULL CHECK (row_count = 0)
) ON COMMIT DROP;

INSERT INTO m3_testnet_credentials_down_guard (row_count)
SELECT
    (SELECT COUNT(*) FROM trading_account_credentials)
    + (SELECT COUNT(*) FROM trading_accounts WHERE environment = 'testnet')
    + (SELECT COUNT(*) FROM trading_intents WHERE environment = 'testnet');

DROP INDEX ix_trading_account_credentials_owner;
DROP TRIGGER trading_accounts_preserve_credential_binding ON trading_accounts;
DROP FUNCTION preserve_testnet_trading_credential_account();
DROP TRIGGER trading_account_credentials_testnet_only ON trading_account_credentials;
DROP FUNCTION validate_testnet_trading_credential();
DROP TABLE trading_account_credentials;

ALTER TABLE trading_intents
    DROP CONSTRAINT ck_trading_intents_environment,
    ADD CONSTRAINT ck_trading_intents_environment CHECK (environment = 'paper');

ALTER TABLE trading_accounts
    DROP CONSTRAINT ck_trading_accounts_environment,
    ADD CONSTRAINT ck_trading_accounts_environment CHECK (environment = 'paper');
