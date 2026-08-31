-- +goose Up

CREATE TABLE outbound_proxies (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(120) NOT NULL,
    protocol VARCHAR(8) NOT NULL,
    host VARCHAR(253) NOT NULL,
    port INTEGER NOT NULL,
    username VARCHAR(255) NOT NULL DEFAULT '',
    password_ciphertext TEXT NOT NULL DEFAULT '',
    is_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    last_check_status VARCHAR(16) NOT NULL DEFAULT 'unchecked',
    last_checked_at TIMESTAMPTZ,
    last_latency_ms INTEGER,
    created_by BIGINT NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    updated_by BIGINT NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_outbound_proxy_name CHECK (BTRIM(name) <> ''),
    CONSTRAINT ck_outbound_proxy_protocol CHECK (protocol IN ('http', 'socks5')),
    CONSTRAINT ck_outbound_proxy_host CHECK (BTRIM(host) <> ''),
    CONSTRAINT ck_outbound_proxy_port CHECK (port BETWEEN 1 AND 65535),
    CONSTRAINT ck_outbound_proxy_check_status CHECK (last_check_status IN ('unchecked', 'healthy', 'failed')),
    CONSTRAINT ck_outbound_proxy_latency CHECK (last_latency_ms IS NULL OR last_latency_ms >= 0)
);

CREATE UNIQUE INDEX ux_outbound_proxies_name_ci ON outbound_proxies (LOWER(name));

-- +goose Down

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM outbound_proxies) THEN
        RAISE EXCEPTION 'cannot drop outbound_proxies while proxy configurations exist';
    END IF;
END
$$;
-- +goose StatementEnd

DROP TABLE outbound_proxies;
