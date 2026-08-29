-- +goose Up

CREATE TABLE system_log_settings (
    id SMALLINT PRIMARY KEY,
    level VARCHAR(8) NOT NULL,
    retention_days INTEGER NOT NULL,
    updated_by BIGINT REFERENCES users (id) ON DELETE SET NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_system_log_settings_singleton CHECK (id = 1),
    CONSTRAINT ck_system_log_settings_level CHECK (level IN ('debug', 'info', 'warn', 'error')),
    CONSTRAINT ck_system_log_settings_retention CHECK (retention_days BETWEEN 1 AND 365)
);

CREATE TABLE system_logs (
    id BIGSERIAL PRIMARY KEY,
    logged_at TIMESTAMPTZ NOT NULL,
    level VARCHAR(8) NOT NULL,
    component VARCHAR(64) NOT NULL,
    message TEXT NOT NULL,
    request_id VARCHAR(64) NOT NULL DEFAULT '',
    user_id BIGINT,
    method VARCHAR(8) NOT NULL DEFAULT '',
    route VARCHAR(255) NOT NULL DEFAULT '',
    status_code INTEGER,
    duration_ms BIGINT,
    details_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    CONSTRAINT ck_system_logs_level CHECK (level IN ('debug', 'info', 'warn', 'error')),
    CONSTRAINT ck_system_logs_component CHECK (BTRIM(component) <> ''),
    CONSTRAINT ck_system_logs_message CHECK (BTRIM(message) <> '' AND CHAR_LENGTH(message) <= 1000),
    CONSTRAINT ck_system_logs_request_id CHECK (
        request_id = '' OR request_id ~ '^[A-Za-z0-9._-]{1,64}$'
    ),
    CONSTRAINT ck_system_logs_user_id CHECK (user_id IS NULL OR user_id > 0),
    CONSTRAINT ck_system_logs_status CHECK (status_code IS NULL OR status_code BETWEEN 100 AND 599),
    CONSTRAINT ck_system_logs_duration CHECK (duration_ms IS NULL OR duration_ms >= 0),
    CONSTRAINT ck_system_logs_details CHECK (jsonb_typeof(details_json) = 'object')
);

CREATE INDEX ix_system_logs_logged_at ON system_logs (logged_at DESC, id DESC);
CREATE INDEX ix_system_logs_level ON system_logs (level, logged_at DESC, id DESC);
CREATE INDEX ix_system_logs_component ON system_logs (component, logged_at DESC, id DESC);
CREATE INDEX ix_system_logs_request_id ON system_logs (request_id) WHERE request_id <> '';
CREATE INDEX ix_system_logs_user_id ON system_logs (user_id, logged_at DESC, id DESC) WHERE user_id IS NOT NULL;

-- +goose Down

LOCK TABLE system_logs, system_log_settings IN ACCESS EXCLUSIVE MODE;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM system_logs LIMIT 1)
        OR EXISTS (SELECT 1 FROM system_log_settings LIMIT 1)
    THEN
        RAISE EXCEPTION 'refusing to roll back system log data';
    END IF;
END
$$;
-- +goose StatementEnd

DROP TABLE system_logs;
DROP TABLE system_log_settings;
