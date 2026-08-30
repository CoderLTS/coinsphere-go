-- +goose Up

CREATE TABLE ai_model_configs (
    id BIGSERIAL PRIMARY KEY,
    display_name VARCHAR(120) NOT NULL,
    base_url VARCHAR(1000) NOT NULL,
    model_name VARCHAR(255) NOT NULL,
    api_key_ciphertext TEXT NOT NULL DEFAULT '',
    is_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    priority INTEGER NOT NULL DEFAULT 100,
    timeout_ms INTEGER NOT NULL DEFAULT 60000,
    created_by BIGINT NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    updated_by BIGINT NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_ai_model_display_name CHECK (BTRIM(display_name) <> ''),
    CONSTRAINT ck_ai_model_base_url CHECK (BTRIM(base_url) <> ''),
    CONSTRAINT ck_ai_model_name CHECK (BTRIM(model_name) <> ''),
    CONSTRAINT ck_ai_model_priority CHECK (priority BETWEEN 1 AND 9999),
    CONSTRAINT ck_ai_model_timeout CHECK (timeout_ms BETWEEN 1000 AND 300000)
);

CREATE INDEX ix_ai_model_configs_enabled_priority
    ON ai_model_configs (is_enabled, priority, id);

CREATE TABLE assistant_sessions (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    model_config_id BIGINT NOT NULL REFERENCES ai_model_configs (id) ON DELETE RESTRICT,
    title VARCHAR(160) NOT NULL DEFAULT '新会话',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_message_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_assistant_session_title CHECK (BTRIM(title) <> '')
);

CREATE INDEX ix_assistant_sessions_user_recent
    ON assistant_sessions (user_id, last_message_at DESC, id DESC);
CREATE INDEX ix_assistant_sessions_model
    ON assistant_sessions (model_config_id);

CREATE TABLE assistant_messages (
    id BIGSERIAL PRIMARY KEY,
    session_id BIGINT NOT NULL REFERENCES assistant_sessions (id) ON DELETE CASCADE,
    role VARCHAR(16) NOT NULL,
    content TEXT NOT NULL DEFAULT '',
    metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_assistant_message_role CHECK (role IN ('user', 'assistant')),
    CONSTRAINT ck_assistant_message_content CHECK (CHAR_LENGTH(content) <= 2097152),
    CONSTRAINT ck_assistant_message_metadata CHECK (jsonb_typeof(metadata_json) = 'object')
);

CREATE INDEX ix_assistant_messages_session
    ON assistant_messages (session_id, id);

-- +goose Down

LOCK TABLE assistant_messages, assistant_sessions, ai_model_configs IN ACCESS EXCLUSIVE MODE;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM assistant_messages LIMIT 1)
        OR EXISTS (SELECT 1 FROM assistant_sessions LIMIT 1)
        OR EXISTS (SELECT 1 FROM ai_model_configs LIMIT 1)
    THEN
        RAISE EXCEPTION 'refusing to roll back platform assistant data';
    END IF;
END
$$;
-- +goose StatementEnd

DROP TABLE assistant_messages;
DROP TABLE assistant_sessions;
DROP TABLE ai_model_configs;
