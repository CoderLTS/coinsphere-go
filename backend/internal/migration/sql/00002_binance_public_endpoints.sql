-- +goose Up

ALTER TABLE market_sync_settings
    ADD COLUMN spot_rest_base_url VARCHAR(255) NOT NULL DEFAULT 'https://data-api.binance.vision',
    ADD COLUMN usdm_rest_base_url VARCHAR(255) NOT NULL DEFAULT 'https://fapi.binance.com',
    ADD COLUMN proxy_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN proxy_url VARCHAR(512) NOT NULL DEFAULT '',
    ADD COLUMN proxy_username VARCHAR(255) NOT NULL DEFAULT '',
    ADD COLUMN proxy_password_ciphertext TEXT NOT NULL DEFAULT '',
    ADD COLUMN proxy_last_check_status VARCHAR(16) NOT NULL DEFAULT 'unchecked',
    ADD COLUMN proxy_last_checked_at TIMESTAMPTZ,
    ADD COLUMN proxy_last_latency_ms INTEGER,
    ADD COLUMN proxy_last_error VARCHAR(255) NOT NULL DEFAULT '',
    ADD CONSTRAINT ck_market_sync_settings_rest_base_urls CHECK (
        spot_rest_base_url ~ '^https://([a-z0-9-]+\.)*binance\.(com|vision)$'
        AND usdm_rest_base_url ~ '^https://([a-z0-9-]+\.)*binance\.(com|vision)$'
    ),
    ADD CONSTRAINT ck_market_sync_settings_proxy CHECK (
        (proxy_url = '' OR proxy_url ~ '^(http|socks5)://[^/?#]+:[0-9]{1,5}$')
        AND (NOT proxy_enabled OR proxy_url <> '')
        AND proxy_last_check_status IN ('unchecked', 'healthy', 'failed')
        AND (proxy_last_latency_ms IS NULL OR proxy_last_latency_ms >= 0)
        AND (
            (proxy_last_check_status = 'unchecked' AND proxy_last_checked_at IS NULL AND proxy_last_latency_ms IS NULL AND proxy_last_error = '')
            OR (proxy_last_check_status = 'healthy' AND proxy_last_checked_at IS NOT NULL AND proxy_last_latency_ms IS NOT NULL AND proxy_last_error = '')
            OR (proxy_last_check_status = 'failed' AND proxy_last_checked_at IS NOT NULL AND proxy_last_latency_ms IS NULL AND proxy_last_error <> '')
        )
    );

UPDATE market_sync_settings
SET market_types = '["spot"]'::jsonb,
    updated_at = CURRENT_TIMESTAMP
WHERE id = 1
  AND updated_by_user_id IS NULL
  AND market_types = '["spot","usd_m"]'::jsonb;

-- +goose Down

LOCK TABLE market_sync_settings IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE binance_public_endpoints_down_guard (
    violations BIGINT NOT NULL CHECK (violations = 0)
) ON COMMIT DROP;

INSERT INTO binance_public_endpoints_down_guard (violations)
SELECT COUNT(*)
FROM market_sync_settings
WHERE spot_rest_base_url <> 'https://data-api.binance.vision'
   OR usdm_rest_base_url <> 'https://fapi.binance.com'
   OR market_types <> '["spot"]'::jsonb
   OR quote_assets <> '["USDT","USDC"]'::jsonb
   OR proxy_enabled
   OR proxy_url <> ''
   OR proxy_username <> ''
   OR proxy_password_ciphertext <> ''
   OR proxy_last_check_status <> 'unchecked'
   OR proxy_last_checked_at IS NOT NULL
   OR proxy_last_latency_ms IS NOT NULL
   OR proxy_last_error <> ''
   OR updated_by_user_id IS NOT NULL;

UPDATE market_sync_settings
SET market_types = '["spot","usd_m"]'::jsonb,
    updated_at = CURRENT_TIMESTAMP
WHERE id = 1 AND market_types = '["spot"]'::jsonb;

ALTER TABLE market_sync_settings
    DROP CONSTRAINT ck_market_sync_settings_proxy,
    DROP CONSTRAINT ck_market_sync_settings_rest_base_urls,
    DROP COLUMN proxy_last_error,
    DROP COLUMN proxy_last_latency_ms,
    DROP COLUMN proxy_last_checked_at,
    DROP COLUMN proxy_last_check_status,
    DROP COLUMN proxy_password_ciphertext,
    DROP COLUMN proxy_username,
    DROP COLUMN proxy_url,
    DROP COLUMN proxy_enabled,
    DROP COLUMN usdm_rest_base_url,
    DROP COLUMN spot_rest_base_url;
