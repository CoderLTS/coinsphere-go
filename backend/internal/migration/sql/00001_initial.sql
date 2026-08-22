-- +goose Up

-- CoinSphere 当前模型的唯一 PostgreSQL/TimescaleDB 空库基线。
CREATE TABLE news_items (
    id BIGSERIAL PRIMARY KEY,
    source_message_id BIGINT,
    published_at TIMESTAMPTZ,
    source_url VARCHAR(1000),
    title VARCHAR(255),
    content TEXT,
    original_url VARCHAR(1000),
    image_url VARCHAR(1000)
);
CREATE INDEX idx_news_items_source_message_id ON news_items (source_message_id);

CREATE TABLE workflow_definitions (
    id BIGSERIAL PRIMARY KEY,
    code VARCHAR(120),
    version BIGINT DEFAULT 1,
    display_name VARCHAR(255),
    description TEXT,
    graph_json TEXT,
    is_builtin BOOLEAN DEFAULT FALSE,
    created_by BIGINT,
    created_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX ux_workflow_def_code_version ON workflow_definitions (code, version);

CREATE TABLE workflow_runtime_states (
    id BIGSERIAL PRIMARY KEY,
    workflow_code VARCHAR(120),
    active_workflow_definition_id BIGINT,
    activated_at TIMESTAMPTZ,
    activated_by BIGINT,
    updated_at TIMESTAMPTZ,
    CONSTRAINT fk_workflow_runtime_states_active_workflow_definition
        FOREIGN KEY (active_workflow_definition_id) REFERENCES workflow_definitions (id) ON DELETE SET NULL
);
CREATE UNIQUE INDEX idx_workflow_runtime_states_workflow_code ON workflow_runtime_states (workflow_code);

CREATE TABLE workflow_runtime_entries (
    id BIGSERIAL PRIMARY KEY,
    workflow_runtime_state_id BIGINT,
    workflow_definition_id BIGINT,
    start_node_id VARCHAR(100),
    entry_key VARCHAR(64),
    start_type VARCHAR(32),
    is_enabled BOOLEAN DEFAULT TRUE,
    registration_status VARCHAR(20) DEFAULT 'ready',
    schedule_job_id VARCHAR(255),
    next_run_at TIMESTAMPTZ,
    last_triggered_at TIMESTAMPTZ,
    last_error_message TEXT,
    secret_hash VARCHAR(255),
    secret_hint VARCHAR(32),
    secret_rotated_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ,
    CONSTRAINT fk_workflow_runtime_entries_workflow_runtime_state
        FOREIGN KEY (workflow_runtime_state_id) REFERENCES workflow_runtime_states (id) ON DELETE CASCADE,
    CONSTRAINT fk_workflow_runtime_entries_workflow_definition
        FOREIGN KEY (workflow_definition_id) REFERENCES workflow_definitions (id) ON DELETE CASCADE
);
CREATE INDEX ix_runtime_entry_def_key ON workflow_runtime_entries (workflow_definition_id, entry_key);
CREATE UNIQUE INDEX ux_runtime_entry_state_key ON workflow_runtime_entries (workflow_runtime_state_id, entry_key);

CREATE TABLE workflow_executions (
    id BIGSERIAL PRIMARY KEY,
    workflow_definition_id BIGINT,
    start_entry_key VARCHAR(64),
    start_node_id VARCHAR(100),
    start_node_type VARCHAR(32),
    trigger_type VARCHAR(32),
    triggered_by BIGINT,
    trigger_key VARCHAR(255),
    idempotency_key VARCHAR(255),
    concurrency_key VARCHAR(255),
    trigger_outbox_id BIGINT,
    status VARCHAR(32) DEFAULT 'queued',
    queued_at TIMESTAMPTZ,
    claimed_at TIMESTAMPTZ,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    last_heartbeat_at TIMESTAMPTZ,
    worker_id VARCHAR(120),
    attempt_count BIGINT DEFAULT 0,
    max_attempts BIGINT DEFAULT 4,
    duration_ms BIGINT,
    next_retry_at TIMESTAMPTZ,
    failure_category VARCHAR(64),
    input_snapshot_json TEXT,
    context_snapshot_json TEXT,
    result_snapshot_json TEXT,
    error_message TEXT,
    CONSTRAINT fk_workflow_executions_workflow_definition
        FOREIGN KEY (workflow_definition_id) REFERENCES workflow_definitions (id) ON DELETE RESTRICT
);
CREATE INDEX idx_workflow_executions_next_retry_at ON workflow_executions (next_retry_at);
CREATE INDEX idx_workflow_executions_last_heartbeat_at ON workflow_executions (last_heartbeat_at);
CREATE INDEX ix_workflow_exec_queue ON workflow_executions (status, queued_at);
CREATE INDEX ix_workflow_exec_backlog ON workflow_executions (concurrency_key, status);
CREATE UNIQUE INDEX ux_workflow_exec_trigger_idem ON workflow_executions (trigger_type, idempotency_key);
CREATE INDEX idx_workflow_executions_workflow_definition_id ON workflow_executions (workflow_definition_id);

CREATE TABLE workflow_execution_attempts (
    id BIGSERIAL PRIMARY KEY,
    workflow_execution_id BIGINT,
    attempt BIGINT DEFAULT 1,
    worker_id VARCHAR(120),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    failure_category VARCHAR(64),
    error_summary TEXT,
    status VARCHAR(32) DEFAULT 'running',
    CONSTRAINT fk_workflow_execution_attempts_workflow_execution
        FOREIGN KEY (workflow_execution_id) REFERENCES workflow_executions (id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX ux_workflow_exec_attempt ON workflow_execution_attempts (workflow_execution_id, attempt);

CREATE TABLE workflow_execution_nodes (
    id BIGSERIAL PRIMARY KEY,
    workflow_execution_id BIGINT,
    node_id VARCHAR(100),
    node_type VARCHAR(100),
    status VARCHAR(32) DEFAULT 'pending',
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    duration_ms BIGINT,
    input_snapshot_json TEXT,
    output_snapshot_json TEXT,
    error_message TEXT,
    CONSTRAINT fk_workflow_execution_nodes_workflow_execution
        FOREIGN KEY (workflow_execution_id) REFERENCES workflow_executions (id) ON DELETE CASCADE
);
CREATE INDEX ix_exec_node_execution ON workflow_execution_nodes (workflow_execution_id);

CREATE TABLE workflow_execution_transitions (
    id BIGSERIAL PRIMARY KEY,
    workflow_execution_id BIGINT,
    edge_id VARCHAR(100),
    source_node_id VARCHAR(100),
    target_node_id VARCHAR(100),
    traversal_index BIGINT DEFAULT 0,
    iteration_index BIGINT,
    branch_key VARCHAR(32),
    payload_snapshot_json TEXT,
    created_at TIMESTAMPTZ,
    CONSTRAINT fk_workflow_execution_transitions_workflow_execution
        FOREIGN KEY (workflow_execution_id) REFERENCES workflow_executions (id) ON DELETE CASCADE
);
CREATE INDEX ix_exec_transition_execution ON workflow_execution_transitions (workflow_execution_id);

CREATE TABLE task_definition_configs (
    id BIGSERIAL PRIMARY KEY,
    task_definition_code VARCHAR(120),
    parameter_overrides_json TEXT,
    updated_by BIGINT,
    created_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX idx_task_definition_configs_task_definition_code
    ON task_definition_configs (task_definition_code);

-- Outbox 的状态、租约和终态字段由数据库约束为一个原子契约，运行时只执行受 fencing 保护的转换。
CREATE TABLE domain_event_outbox (
    id BIGSERIAL PRIMARY KEY,
    event_type VARCHAR(120),
    aggregate_type VARCHAR(120),
    aggregate_id VARCHAR(120),
    workflow_execution_id BIGINT,
    workflow_execution_node_id BIGINT,
    payload_json TEXT,
    metadata_json TEXT,
    status VARCHAR(20) DEFAULT 'pending',
    attempt_count INTEGER DEFAULT 0,
    available_at TIMESTAMPTZ,
    max_attempts INTEGER NOT NULL DEFAULT 3,
    lease_id VARCHAR(36),
    worker_id VARCHAR(120),
    lease_expires_at TIMESTAMPTZ,
    claimed_at TIMESTAMPTZ,
    processed_at TIMESTAMPTZ,
    last_error_category VARCHAR(64),
    last_error_message TEXT,
    dead_lettered_at TIMESTAMPTZ,
    alerted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ,
    CONSTRAINT fk_domain_event_outbox_workflow_execution
        FOREIGN KEY (workflow_execution_id) REFERENCES workflow_executions (id) ON DELETE SET NULL,
    CONSTRAINT fk_domain_event_outbox_workflow_execution_node
        FOREIGN KEY (workflow_execution_node_id) REFERENCES workflow_execution_nodes (id) ON DELETE SET NULL,
    CONSTRAINT ck_event_outbox_status CHECK (
        status IS NOT NULL AND status IN ('pending', 'claimed', 'processed', 'failed', 'dead_letter')
    ),
    CONSTRAINT ck_event_outbox_attempts CHECK (
        attempt_count IS NOT NULL
        AND attempt_count >= 0
        AND max_attempts > 0
        AND attempt_count <= max_attempts
    ),
    CONSTRAINT ck_event_outbox_available_at CHECK (available_at IS NOT NULL),
    CONSTRAINT ck_event_outbox_state_fields CHECK (
        (
            status = 'pending'
            AND attempt_count < max_attempts
            AND processed_at IS NULL
            AND lease_id IS NULL
            AND worker_id IS NULL
            AND lease_expires_at IS NULL
            AND claimed_at IS NULL
            AND dead_lettered_at IS NULL
            AND alerted_at IS NULL
        )
        OR
        (
            status = 'claimed'
            AND attempt_count > 0
            AND processed_at IS NULL
            AND lease_id IS NOT NULL
            AND worker_id IS NOT NULL
            AND lease_expires_at IS NOT NULL
            AND claimed_at IS NOT NULL
            AND lease_expires_at > claimed_at
            AND dead_lettered_at IS NULL
            AND alerted_at IS NULL
        )
        OR
        (
            status IN ('processed', 'failed')
            AND processed_at IS NOT NULL
            AND lease_id IS NULL
            AND worker_id IS NULL
            AND lease_expires_at IS NULL
            AND claimed_at IS NULL
            AND dead_lettered_at IS NULL
            AND alerted_at IS NULL
        )
        OR
        (
            status = 'dead_letter'
            AND attempt_count = max_attempts
            AND processed_at IS NOT NULL
            AND lease_id IS NULL
            AND worker_id IS NULL
            AND lease_expires_at IS NULL
            AND claimed_at IS NULL
            AND dead_lettered_at IS NOT NULL
            AND dead_lettered_at = processed_at
            AND (alerted_at IS NULL OR alerted_at >= dead_lettered_at)
        )
    )
);
CREATE INDEX idx_domain_event_outbox_event_type ON domain_event_outbox (event_type);
CREATE INDEX ix_event_outbox_pending ON domain_event_outbox (status, available_at, id);
CREATE INDEX ix_event_outbox_recovery ON domain_event_outbox (status, lease_expires_at, id);
CREATE UNIQUE INDEX ux_event_outbox_lease_id ON domain_event_outbox (lease_id);
CREATE INDEX ix_event_outbox_dead_letter_alert ON domain_event_outbox (status, alerted_at, dead_lettered_at, id);
CREATE INDEX ix_event_outbox_terminal_retention ON domain_event_outbox (status, processed_at, id);

CREATE TABLE roles (
    id BIGSERIAL PRIMARY KEY,
    display_name VARCHAR(100),
    code VARCHAR(50),
    description VARCHAR(255),
    is_enabled BOOLEAN DEFAULT TRUE,
    is_system BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX idx_roles_code ON roles (code);

CREATE TABLE users (
    id BIGSERIAL PRIMARY KEY,
    username VARCHAR(100),
    password_hash VARCHAR(255),
    nickname VARCHAR(100),
    full_name VARCHAR(100),
    gender VARCHAR(20) DEFAULT 'unknown',
    phone VARCHAR(32),
    email VARCHAR(150),
    avatar VARCHAR(500),
    is_active BOOLEAN DEFAULT TRUE,
    job_title VARCHAR(100),
    location VARCHAR(120),
    company VARCHAR(120),
    bio TEXT,
    tags_json TEXT,
    created_by VARCHAR(100) DEFAULT 'system',
    updated_by VARCHAR(100) DEFAULT 'system',
    created_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ,
    last_login_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX idx_users_username ON users (username);

CREATE TABLE idempotency_records (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL,
    scope VARCHAR(255) NOT NULL,
    key_hash VARCHAR(64) NOT NULL,
    request_hash VARCHAR(64) NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT fk_idempotency_records_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX ux_idempotency_records_user_scope_key
    ON idempotency_records (user_id, scope, key_hash);
CREATE INDEX ix_idempotency_records_expires_at ON idempotency_records (expires_at);

CREATE TABLE user_roles (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT,
    role_id BIGINT,
    created_at TIMESTAMPTZ,
    CONSTRAINT fk_user_roles_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE,
    CONSTRAINT fk_user_roles_role FOREIGN KEY (role_id) REFERENCES roles (id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX ux_user_role ON user_roles (user_id, role_id);

CREATE TABLE menus (
    id BIGSERIAL PRIMARY KEY,
    parent_id BIGINT,
    path VARCHAR(255),
    name VARCHAR(100),
    permission_code VARCHAR(120),
    component VARCHAR(255),
    title VARCHAR(100),
    icon VARCHAR(100),
    menu_type VARCHAR(20) DEFAULT 'menu',
    external_url VARCHAR(500),
    active_menu_path VARCHAR(255),
    sort BIGINT DEFAULT 0,
    keep_alive BOOLEAN DEFAULT FALSE,
    is_hidden BOOLEAN DEFAULT FALSE,
    is_hide_tab BOOLEAN DEFAULT FALSE,
    is_full_screen BOOLEAN DEFAULT FALSE,
    is_active BOOLEAN DEFAULT TRUE,
    use_iframe BOOLEAN DEFAULT FALSE,
    fixed_tab BOOLEAN DEFAULT FALSE,
    badge_label VARCHAR(50),
    created_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ,
    CONSTRAINT fk_menus_parent FOREIGN KEY (parent_id) REFERENCES menus (id) ON DELETE CASCADE
);
CREATE INDEX idx_menus_permission_code ON menus (permission_code);
CREATE UNIQUE INDEX idx_menus_name ON menus (name);

CREATE TABLE menu_buttons (
    id BIGSERIAL PRIMARY KEY,
    menu_id BIGINT,
    title VARCHAR(100),
    permission_code VARCHAR(120),
    sort BIGINT DEFAULT 0,
    created_at TIMESTAMPTZ,
    CONSTRAINT fk_menu_buttons_menu FOREIGN KEY (menu_id) REFERENCES menus (id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX idx_menu_buttons_permission_code ON menu_buttons (permission_code);

CREATE TABLE role_menus (
    id BIGSERIAL PRIMARY KEY,
    role_id BIGINT,
    menu_id BIGINT,
    created_at TIMESTAMPTZ,
    CONSTRAINT fk_role_menus_role FOREIGN KEY (role_id) REFERENCES roles (id) ON DELETE CASCADE,
    CONSTRAINT fk_role_menus_menu FOREIGN KEY (menu_id) REFERENCES menus (id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX ux_role_menu ON role_menus (role_id, menu_id);

CREATE TABLE role_menu_buttons (
    id BIGSERIAL PRIMARY KEY,
    role_id BIGINT,
    button_id BIGINT,
    created_at TIMESTAMPTZ,
    CONSTRAINT fk_role_menu_buttons_role FOREIGN KEY (role_id) REFERENCES roles (id) ON DELETE CASCADE,
    CONSTRAINT fk_role_menu_buttons_button FOREIGN KEY (button_id) REFERENCES menu_buttons (id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX ux_role_button ON role_menu_buttons (role_id, button_id);

CREATE TABLE i18n_texts (
    id BIGSERIAL PRIMARY KEY,
    biz_type VARCHAR(20),
    biz_id BIGINT,
    i18n_key VARCHAR(255),
    locale VARCHAR(10),
    text VARCHAR(255),
    updated_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX ux_i18n_key_locale ON i18n_texts (i18n_key, locale);
CREATE UNIQUE INDEX ux_i18n_biz ON i18n_texts (biz_type, biz_id, locale);

CREATE TABLE ai_model_configs (
    id BIGSERIAL PRIMARY KEY,
    owner_id BIGINT,
    provider VARCHAR(50),
    provider_name VARCHAR(100),
    display_name VARCHAR(100),
    model_identifier VARCHAR(150),
    base_url VARCHAR(500),
    encrypted_api_key TEXT,
    is_enabled BOOLEAN DEFAULT TRUE,
    priority BIGINT DEFAULT 100,
    request_headers_json TEXT,
    request_body_json TEXT,
    timeout_ms BIGINT DEFAULT 60000,
    remark TEXT,
    last_validation_status VARCHAR(20) DEFAULT 'unknown',
    last_validation_message TEXT,
    last_validated_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ,
    CONSTRAINT fk_ai_model_configs_owner FOREIGN KEY (owner_id) REFERENCES users (id) ON DELETE CASCADE
);
CREATE INDEX idx_ai_model_configs_owner_id ON ai_model_configs (owner_id);

CREATE TABLE assistant_agents (
    id BIGSERIAL PRIMARY KEY,
    code VARCHAR(64),
    display_name VARCHAR(100),
    avatar VARCHAR(500),
    description VARCHAR(500),
    system_prompt TEXT,
    welcome_message TEXT,
    starter_prompts_json TEXT,
    data_source_type VARCHAR(32) DEFAULT 'none',
    is_enabled BOOLEAN DEFAULT TRUE,
    sort BIGINT DEFAULT 0,
    created_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX idx_assistant_agents_code ON assistant_agents (code);

CREATE TABLE ai_model_agent_bindings (
    id BIGSERIAL PRIMARY KEY,
    model_config_id BIGINT,
    agent_id BIGINT,
    created_at TIMESTAMPTZ,
    CONSTRAINT fk_ai_model_agent_bindings_model_config
        FOREIGN KEY (model_config_id) REFERENCES ai_model_configs (id) ON DELETE CASCADE,
    CONSTRAINT fk_ai_model_agent_bindings_agent
        FOREIGN KEY (agent_id) REFERENCES assistant_agents (id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX ux_model_agent ON ai_model_agent_bindings (model_config_id, agent_id);

CREATE TABLE notification_channels (
    id BIGSERIAL PRIMARY KEY,
    channel_type VARCHAR(50),
    owner_id BIGINT,
    display_name VARCHAR(100),
    is_enabled BOOLEAN DEFAULT TRUE,
    is_builtin BOOLEAN DEFAULT FALSE,
    is_system BOOLEAN DEFAULT FALSE,
    settings_json TEXT,
    encrypted_secrets_json TEXT,
    remark TEXT,
    last_test_status VARCHAR(20) DEFAULT 'unknown',
    last_test_message TEXT,
    last_tested_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ,
    CONSTRAINT fk_notification_channels_owner FOREIGN KEY (owner_id) REFERENCES users (id) ON DELETE SET NULL
);

CREATE TABLE notification_deliveries (
    id BIGSERIAL PRIMARY KEY,
    workflow_execution_id BIGINT,
    workflow_execution_node_id BIGINT,
    outbox_event_id BIGINT,
    target_type VARCHAR(20),
    target_id BIGINT,
    recipient_user_id BIGINT,
    channel_id BIGINT,
    channel_type VARCHAR(50),
    status VARCHAR(20) DEFAULT 'pending',
    title TEXT,
    content TEXT,
    provider_response_text TEXT,
    error_message TEXT,
    is_read BOOLEAN DEFAULT FALSE,
    read_at TIMESTAMPTZ,
    sent_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ,
    CONSTRAINT fk_notification_deliveries_workflow_execution
        FOREIGN KEY (workflow_execution_id) REFERENCES workflow_executions (id) ON DELETE SET NULL,
    CONSTRAINT fk_notification_deliveries_workflow_execution_node
        FOREIGN KEY (workflow_execution_node_id) REFERENCES workflow_execution_nodes (id) ON DELETE SET NULL,
    CONSTRAINT fk_notification_deliveries_outbox_event
        FOREIGN KEY (outbox_event_id) REFERENCES domain_event_outbox (id) ON DELETE SET NULL,
    CONSTRAINT fk_notification_deliveries_recipient_user
        FOREIGN KEY (recipient_user_id) REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT fk_notification_deliveries_channel
        FOREIGN KEY (channel_id) REFERENCES notification_channels (id) ON DELETE SET NULL
);
CREATE INDEX idx_notification_deliveries_recipient_user_id ON notification_deliveries (recipient_user_id);

CREATE TABLE assistant_sessions (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT,
    agent_id BIGINT,
    news_id BIGINT,
    model_config_id BIGINT,
    model_display_name_snapshot VARCHAR(100),
    provider_label_snapshot VARCHAR(100),
    title VARCHAR(255),
    created_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ,
    last_message_at TIMESTAMPTZ,
    CONSTRAINT fk_assistant_sessions_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE,
    CONSTRAINT fk_assistant_sessions_agent FOREIGN KEY (agent_id) REFERENCES assistant_agents (id) ON DELETE CASCADE,
    CONSTRAINT fk_assistant_sessions_news FOREIGN KEY (news_id) REFERENCES news_items (id) ON DELETE SET NULL,
    CONSTRAINT fk_assistant_sessions_model_config
        FOREIGN KEY (model_config_id) REFERENCES ai_model_configs (id) ON DELETE SET NULL
);
CREATE INDEX idx_assistant_sessions_agent_id ON assistant_sessions (agent_id);
CREATE INDEX idx_assistant_sessions_user_id ON assistant_sessions (user_id);

CREATE TABLE assistant_messages (
    id BIGSERIAL PRIMARY KEY,
    session_id BIGINT,
    role VARCHAR(20),
    content_type VARCHAR(40) DEFAULT 'text',
    content TEXT,
    reasoning TEXT,
    prompt_tokens BIGINT DEFAULT 0,
    completion_tokens BIGINT DEFAULT 0,
    total_tokens BIGINT DEFAULT 0,
    created_at TIMESTAMPTZ,
    CONSTRAINT fk_assistant_messages_session FOREIGN KEY (session_id) REFERENCES assistant_sessions (id) ON DELETE CASCADE
);
CREATE INDEX ix_assistant_msg_session ON assistant_messages (created_at, session_id);

-- Python Worker 与 Backend 使用同一 PostgreSQL schema；活跃租约、取消与终态时间由数据库保持一致。
CREATE TABLE worker_tasks (
    id VARCHAR(36) PRIMARY KEY,
    task_type VARCHAR(120) NOT NULL,
    payload_json TEXT NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'queued',
    attempt_count INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 3,
    lease_id VARCHAR(36),
    worker_id VARCHAR(120),
    lease_expires_at TIMESTAMPTZ,
    last_heartbeat_at TIMESTAMPTZ,
    cancel_requested_at TIMESTAMPTZ,
    queued_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    claimed_at TIMESTAMPTZ,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    result_json TEXT,
    failure_category VARCHAR(64),
    error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_worker_tasks_status CHECK (
        status IN ('queued', 'claimed', 'running', 'cancelRequested', 'succeeded', 'failed', 'canceled')
    ),
    CONSTRAINT ck_worker_tasks_attempts CHECK (
        attempt_count >= 0 AND max_attempts > 0 AND attempt_count <= max_attempts
    ),
    CONSTRAINT ck_worker_tasks_active_lease CHECK (
        (
            status IN ('claimed', 'running', 'cancelRequested')
            AND lease_id IS NOT NULL
            AND worker_id IS NOT NULL
            AND lease_expires_at IS NOT NULL
            AND last_heartbeat_at IS NOT NULL
        )
        OR
        (
            status IN ('queued', 'succeeded', 'failed', 'canceled')
            AND lease_id IS NULL
            AND worker_id IS NULL
            AND lease_expires_at IS NULL
            AND last_heartbeat_at IS NULL
        )
    ),
    CONSTRAINT ck_worker_tasks_cancel_timestamp CHECK (
        (status IN ('cancelRequested', 'canceled') AND cancel_requested_at IS NOT NULL)
        OR
        (status NOT IN ('cancelRequested', 'canceled') AND cancel_requested_at IS NULL)
    ),
    CONSTRAINT ck_worker_tasks_finished_at CHECK (
        (status IN ('succeeded', 'failed', 'canceled') AND finished_at IS NOT NULL)
        OR
        (status NOT IN ('succeeded', 'failed', 'canceled') AND finished_at IS NULL)
    ),
    CONSTRAINT ux_worker_tasks_lease_id UNIQUE (lease_id)
);
CREATE INDEX ix_worker_tasks_claim ON worker_tasks (status, queued_at, id);
CREATE INDEX ix_worker_tasks_recovery ON worker_tasks (status, lease_expires_at);


CREATE TABLE audit_records (
    id BIGSERIAL PRIMARY KEY,
    request_id VARCHAR(64) NOT NULL,
    actor_user_id BIGINT,
    action VARCHAR(255) NOT NULL,
    resource_path VARCHAR(500) NOT NULL,
    outcome VARCHAR(16) NOT NULL,
    status_code INTEGER NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_audit_records_request_id
        CHECK (request_id ~ '^[A-Za-z0-9._-]{1,64}$'),
    CONSTRAINT ck_audit_records_outcome
        CHECK (outcome IN ('success', 'failure')),
    CONSTRAINT ck_audit_records_status_code
        CHECK (status_code BETWEEN 100 AND 599)
);
CREATE INDEX ix_audit_records_created_at ON audit_records (created_at DESC, id DESC);
CREATE INDEX ix_audit_records_actor_created_at ON audit_records (actor_user_id, created_at DESC, id DESC);
CREATE INDEX ix_audit_records_request_id ON audit_records (request_id);


CREATE EXTENSION IF NOT EXISTS timescaledb WITH SCHEMA public;

CREATE TABLE market_instruments (
    id UUID NOT NULL,
    venue VARCHAR(16) NOT NULL,
    market_type VARCHAR(32) NOT NULL,
    native_symbol VARCHAR(64) NOT NULL,
    base_asset VARCHAR(32) NOT NULL,
    quote_asset VARCHAR(32) NOT NULL,
    status VARCHAR(16) NOT NULL,
    price_tick NUMERIC(38,18) NOT NULL,
    quantity_step NUMERIC(38,18) NOT NULL,
    min_quantity NUMERIC(38,18) NOT NULL,
    min_notional NUMERIC(38,18) NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT market_instruments_pkey PRIMARY KEY (id),
    CONSTRAINT uq_market_instruments_natural_key
        UNIQUE (venue, market_type, native_symbol),
    CONSTRAINT uq_market_instruments_venue_id
        UNIQUE (venue, id),
    CONSTRAINT ck_market_instruments_id_uuidv7
        CHECK (id::text ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
    CONSTRAINT ck_market_instruments_venue
        CHECK (venue = 'binance'),
    CONSTRAINT ck_market_instruments_market_type
        CHECK (market_type IN ('spot', 'usd_m')),
    CONSTRAINT ck_market_instruments_native_symbol
        CHECK (native_symbol ~ '^[A-Z0-9][A-Z0-9._-]*$'),
    CONSTRAINT ck_market_instruments_base_asset
        CHECK (
            base_asset ~ '^[A-Z0-9][A-Z0-9._-]*$'
            AND base_asset = UPPER(base_asset)
        ),
    CONSTRAINT ck_market_instruments_quote_asset
        CHECK (
            quote_asset ~ '^[A-Z0-9][A-Z0-9._-]*$'
            AND quote_asset = UPPER(quote_asset)
        ),
    CONSTRAINT ck_market_instruments_status
        CHECK (status IN ('trading', 'suspended')),
    CONSTRAINT ck_market_instruments_steps
        CHECK (
            price_tick > 0
            AND quantity_step > 0
            AND min_quantity > 0
            AND min_notional > 0
            AND isfinite(updated_at)
        )
);

CREATE TABLE market_candles (
    venue VARCHAR(16) NOT NULL,
    instrument_id UUID NOT NULL,
    interval_code VARCHAR(4) NOT NULL,
    open_time TIMESTAMPTZ NOT NULL,
    close_time TIMESTAMPTZ NOT NULL,
    open_price NUMERIC(38,18) NOT NULL,
    high_price NUMERIC(38,18) NOT NULL,
    low_price NUMERIC(38,18) NOT NULL,
    close_price NUMERIC(38,18) NOT NULL,
    base_volume NUMERIC(38,18) NOT NULL,
    is_closed BOOLEAN NOT NULL,
    CONSTRAINT market_candles_pkey
        PRIMARY KEY (venue, instrument_id, interval_code, open_time),
    CONSTRAINT fk_market_candles_instrument
        FOREIGN KEY (venue, instrument_id)
        REFERENCES market_instruments (venue, id)
        ON DELETE RESTRICT,
    CONSTRAINT ck_market_candles_venue
        CHECK (venue = 'binance'),
    CONSTRAINT ck_market_candles_interval
        CHECK (interval_code IN ('1m', '5m', '15m', '1h', '4h', '1d')),
    CONSTRAINT ck_market_candles_time
        CHECK (
            isfinite(open_time)
            AND isfinite(close_time)
            AND date_bin(
                CASE interval_code
                    WHEN '1m' THEN INTERVAL '1 minute'
                    WHEN '5m' THEN INTERVAL '5 minutes'
                    WHEN '15m' THEN INTERVAL '15 minutes'
                    WHEN '1h' THEN INTERVAL '1 hour'
                    WHEN '4h' THEN INTERVAL '4 hours'
                    WHEN '1d' THEN INTERVAL '1 day'
                END,
                open_time,
                TIMESTAMPTZ '1970-01-01 00:00:00+00'
            ) = open_time
            AND close_time = open_time + CASE interval_code
                WHEN '1m' THEN INTERVAL '1 minute'
                WHEN '5m' THEN INTERVAL '5 minutes'
                WHEN '15m' THEN INTERVAL '15 minutes'
                WHEN '1h' THEN INTERVAL '1 hour'
                WHEN '4h' THEN INTERVAL '4 hours'
                WHEN '1d' THEN INTERVAL '1 day'
            END
        ),
    CONSTRAINT ck_market_candles_prices
        CHECK (
            open_price > 0
            AND high_price > 0
            AND low_price > 0
            AND close_price > 0
        ),
    CONSTRAINT ck_market_candles_volume
        CHECK (base_volume >= 0),
    CONSTRAINT ck_market_candles_ohlc
        CHECK (
            low_price <= LEAST(open_price, close_price)
            AND high_price >= GREATEST(open_price, close_price)
        )
) WITH (
    tsdb.hypertable = true,
    tsdb.columnstore = false,
    tsdb.partition_column = 'open_time',
    tsdb.chunk_interval = '7 days',
    tsdb.create_default_indexes = false
);

ALTER TABLE market_candles SET (
    timescaledb.enable_columnstore,
    timescaledb.orderby = 'open_time DESC',
    timescaledb.segmentby = 'venue,instrument_id,interval_code'
);

CALL public.add_columnstore_policy(
    'market_candles',
    after => INTERVAL '30 days'
);

SELECT public.add_retention_policy(
    'market_candles',
    drop_after => INTERVAL '2 years'
);

CREATE TABLE market_ticker_snapshots (
    venue VARCHAR(16) NOT NULL,
    instrument_id UUID NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    last_price NUMERIC(38,18) NOT NULL,
    best_bid_price NUMERIC(38,18) NOT NULL,
    best_ask_price NUMERIC(38,18) NOT NULL,
    CONSTRAINT market_ticker_snapshots_pkey
        PRIMARY KEY (venue, instrument_id),
    CONSTRAINT fk_market_ticker_snapshots_instrument
        FOREIGN KEY (venue, instrument_id)
        REFERENCES market_instruments (venue, id)
        ON DELETE RESTRICT,
    CONSTRAINT ck_market_ticker_snapshots_venue
        CHECK (venue = 'binance'),
    CONSTRAINT ck_market_ticker_snapshots_time
        CHECK (isfinite(occurred_at)),
    CONSTRAINT ck_market_ticker_snapshots_prices
        CHECK (
            last_price > 0
            AND best_bid_price > 0
            AND best_ask_price > 0
        ),
    CONSTRAINT ck_market_ticker_snapshots_spread
        CHECK (best_bid_price <= best_ask_price)
);


CREATE TABLE watchlist_items (
    id UUID NOT NULL,
    owner_user_id BIGINT NOT NULL,
    instrument_id UUID NOT NULL,
    interval_code VARCHAR(4) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT watchlist_items_pkey PRIMARY KEY (id),
    CONSTRAINT fk_watchlist_items_owner
        FOREIGN KEY (owner_user_id) REFERENCES users (id) ON DELETE CASCADE,
    CONSTRAINT fk_watchlist_items_instrument
        FOREIGN KEY (instrument_id) REFERENCES market_instruments (id) ON DELETE RESTRICT,
    CONSTRAINT uq_watchlist_items_owner_instrument_interval
        UNIQUE (owner_user_id, instrument_id, interval_code),
    CONSTRAINT ck_watchlist_items_id_uuidv7
        CHECK (id::text ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
    CONSTRAINT ck_watchlist_items_interval
        CHECK (interval_code IN ('1m', '5m', '15m', '1h', '4h', '1d')),
    CONSTRAINT ck_watchlist_items_created_at
        CHECK (isfinite(created_at))
);

CREATE INDEX ix_watchlist_items_instrument_interval
    ON watchlist_items (instrument_id, interval_code);


ALTER TABLE worker_tasks
    ADD COLUMN lane VARCHAR(16) NOT NULL DEFAULT 'realtime',
    ADD COLUMN priority INTEGER NOT NULL DEFAULT 0;

ALTER TABLE worker_tasks
    ADD CONSTRAINT ck_worker_tasks_lane
        CHECK (lane IN ('realtime', 'backtest')),
    ADD CONSTRAINT ck_worker_tasks_priority
        CHECK (priority >= 0);

CREATE INDEX ix_worker_tasks_lane_claim
    ON worker_tasks (lane, status, priority DESC, queued_at, id);


ALTER TABLE worker_tasks
    ADD CONSTRAINT ck_worker_tasks_quant_lane CHECK (
        task_type NOT IN ('strategy.publish', 'strategy.backtest')
        OR lane = 'backtest'
    ),
    ADD CONSTRAINT ck_worker_tasks_quant_payload CHECK (
        task_type NOT IN ('strategy.publish', 'strategy.backtest')
        OR CASE task_type
            WHEN 'strategy.publish' THEN
                jsonb_typeof(payload_json::jsonb) = 'object'
                AND payload_json::jsonb ?& ARRAY['strategyId', 'strategyVersionId']
                AND payload_json::jsonb - 'strategyId' - 'strategyVersionId' = '{}'::jsonb
                AND COALESCE(
                    payload_json::jsonb ->> 'strategyId'
                        ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$',
                    FALSE
                )
                AND COALESCE(
                    payload_json::jsonb ->> 'strategyVersionId'
                        ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$',
                    FALSE
                )
            WHEN 'strategy.backtest' THEN
                jsonb_typeof(payload_json::jsonb) = 'object'
                AND payload_json::jsonb ? 'backtestId'
                AND payload_json::jsonb - 'backtestId' = '{}'::jsonb
                AND COALESCE(
                    payload_json::jsonb ->> 'backtestId'
                        ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$',
                    FALSE
                )
            ELSE TRUE
        END
    );

CREATE TABLE strategies (
    id UUID NOT NULL,
    name VARCHAR(120) NOT NULL,
    source_code TEXT NOT NULL,
    market_type VARCHAR(32) NOT NULL,
    instrument_id UUID NOT NULL,
    interval_code VARCHAR(4) NOT NULL,
    lookback_bars INTEGER NOT NULL,
    parameter_schema_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    runtime_version VARCHAR(32) NOT NULL DEFAULT 'python3.12',
    created_by_user_id BIGINT NOT NULL,
    updated_by_user_id BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT strategies_pkey PRIMARY KEY (id),
    CONSTRAINT fk_strategies_instrument
        FOREIGN KEY (instrument_id) REFERENCES market_instruments (id) ON DELETE RESTRICT,
    CONSTRAINT fk_strategies_created_by
        FOREIGN KEY (created_by_user_id) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT fk_strategies_updated_by
        FOREIGN KEY (updated_by_user_id) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT ck_strategies_id_uuidv7 CHECK (
        id::text ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
    ),
    CONSTRAINT ck_strategies_name CHECK (name = BTRIM(name) AND name <> ''),
    CONSTRAINT ck_strategies_source CHECK (
        OCTET_LENGTH(source_code) BETWEEN 1 AND 65536
        AND BTRIM(source_code) <> ''
    ),
    CONSTRAINT ck_strategies_market CHECK (market_type IN ('spot', 'usd_m')),
    CONSTRAINT ck_strategies_interval CHECK (interval_code IN ('1m', '5m', '15m', '1h', '4h', '1d')),
    CONSTRAINT ck_strategies_lookback CHECK (lookback_bars BETWEEN 1 AND 10000),
    CONSTRAINT ck_strategies_parameter_schema CHECK (jsonb_typeof(parameter_schema_json) = 'object'),
    CONSTRAINT ck_strategies_runtime CHECK (runtime_version = 'python3.12'),
    CONSTRAINT ck_strategies_times CHECK (isfinite(created_at) AND isfinite(updated_at))
);

CREATE TABLE strategy_versions (
    id UUID NOT NULL,
    strategy_id UUID NOT NULL,
    version_number INTEGER NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    worker_task_id VARCHAR(36) NOT NULL,
    idempotency_record_id BIGINT NOT NULL,
    name VARCHAR(120) NOT NULL,
    source_code TEXT NOT NULL,
    code_sha256 CHAR(64) NOT NULL,
    runtime_version VARCHAR(32) NOT NULL,
    market_type VARCHAR(32) NOT NULL,
    instrument_id UUID NOT NULL,
    symbol VARCHAR(64) NOT NULL,
    interval_code VARCHAR(4) NOT NULL,
    lookback_bars INTEGER NOT NULL,
    parameter_schema_json JSONB NOT NULL,
    published_by_user_id BIGINT NOT NULL,
    published_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT strategy_versions_pkey PRIMARY KEY (id),
    CONSTRAINT fk_strategy_versions_strategy
        FOREIGN KEY (strategy_id) REFERENCES strategies (id) ON DELETE RESTRICT,
    CONSTRAINT fk_strategy_versions_instrument
        FOREIGN KEY (instrument_id) REFERENCES market_instruments (id) ON DELETE RESTRICT,
    CONSTRAINT fk_strategy_versions_worker_task
        FOREIGN KEY (worker_task_id) REFERENCES worker_tasks (id) ON DELETE RESTRICT,
    CONSTRAINT fk_strategy_versions_idempotency
        FOREIGN KEY (idempotency_record_id) REFERENCES idempotency_records (id) ON DELETE RESTRICT,
    CONSTRAINT fk_strategy_versions_published_by
        FOREIGN KEY (published_by_user_id) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT uq_strategy_versions_number UNIQUE (strategy_id, version_number),
    CONSTRAINT uq_strategy_versions_worker_task UNIQUE (worker_task_id),
    CONSTRAINT uq_strategy_versions_idempotency UNIQUE (idempotency_record_id),
    CONSTRAINT ck_strategy_versions_id_uuidv7 CHECK (
        id::text ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
    ),
    CONSTRAINT ck_strategy_versions_number CHECK (version_number > 0),
    CONSTRAINT ck_strategy_versions_status CHECK (status IN ('pending', 'published', 'failed')),
    CONSTRAINT ck_strategy_versions_name CHECK (name = BTRIM(name) AND name <> ''),
    CONSTRAINT ck_strategy_versions_source CHECK (
        OCTET_LENGTH(source_code) BETWEEN 1 AND 65536
        AND BTRIM(source_code) <> ''
    ),
    CONSTRAINT ck_strategy_versions_sha256 CHECK (code_sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT ck_strategy_versions_runtime CHECK (runtime_version = 'python3.12'),
    CONSTRAINT ck_strategy_versions_market CHECK (market_type IN ('spot', 'usd_m')),
    CONSTRAINT ck_strategy_versions_symbol CHECK (symbol ~ '^[A-Z0-9][A-Z0-9._-]*$'),
    CONSTRAINT ck_strategy_versions_interval CHECK (interval_code IN ('1m', '5m', '15m', '1h', '4h', '1d')),
    CONSTRAINT ck_strategy_versions_lookback CHECK (lookback_bars BETWEEN 1 AND 10000),
    CONSTRAINT ck_strategy_versions_parameter_schema CHECK (jsonb_typeof(parameter_schema_json) = 'object'),
    CONSTRAINT ck_strategy_versions_published_at CHECK (
        isfinite(created_at)
        AND (
            (status = 'published' AND published_at IS NOT NULL AND isfinite(published_at))
            OR (status <> 'published' AND published_at IS NULL)
        )
    )
);

-- +goose StatementBegin
CREATE FUNCTION reject_strategy_version_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'strategy version mutation is not allowed';
    ELSIF OLD.status <> 'pending'
        OR NEW.status NOT IN ('published', 'failed')
        OR NEW.id IS DISTINCT FROM OLD.id
        OR NEW.strategy_id IS DISTINCT FROM OLD.strategy_id
        OR NEW.version_number IS DISTINCT FROM OLD.version_number
        OR NEW.worker_task_id IS DISTINCT FROM OLD.worker_task_id
        OR NEW.idempotency_record_id IS DISTINCT FROM OLD.idempotency_record_id
        OR NEW.name IS DISTINCT FROM OLD.name
        OR NEW.source_code IS DISTINCT FROM OLD.source_code
        OR NEW.code_sha256 IS DISTINCT FROM OLD.code_sha256
        OR NEW.runtime_version IS DISTINCT FROM OLD.runtime_version
        OR NEW.market_type IS DISTINCT FROM OLD.market_type
        OR NEW.instrument_id IS DISTINCT FROM OLD.instrument_id
        OR NEW.symbol IS DISTINCT FROM OLD.symbol
        OR NEW.interval_code IS DISTINCT FROM OLD.interval_code
        OR NEW.lookback_bars IS DISTINCT FROM OLD.lookback_bars
        OR NEW.parameter_schema_json IS DISTINCT FROM OLD.parameter_schema_json
        OR NEW.published_by_user_id IS DISTINCT FROM OLD.published_by_user_id
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
        OR (NEW.status = 'published' AND NEW.published_at IS NULL)
        OR (NEW.status = 'failed' AND NEW.published_at IS NOT NULL) THEN
        RAISE EXCEPTION 'strategy version mutation is not allowed';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER strategy_versions_immutable
BEFORE UPDATE OR DELETE ON strategy_versions
FOR EACH ROW EXECUTE FUNCTION reject_strategy_version_mutation();

CREATE TABLE backtests (
    id UUID NOT NULL,
    owner_user_id BIGINT NOT NULL,
    strategy_version_id UUID NOT NULL,
    worker_task_id VARCHAR(36) NOT NULL,
    idempotency_record_id BIGINT NOT NULL,
    simulator_version VARCHAR(32) NOT NULL,
    parameters_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    start_time TIMESTAMPTZ NOT NULL,
    end_time TIMESTAMPTZ NOT NULL,
    allocation_usdt NUMERIC(38,18) NOT NULL,
    initial_equity NUMERIC(38,18) NOT NULL,
    fee_rate NUMERIC(38,18) NOT NULL,
    slippage_rate NUMERIC(38,18) NOT NULL,
    funding_rates_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    stop_loss_ratio NUMERIC(38,18),
    maintenance_margin_ratio NUMERIC(38,18),
    summary_json JSONB,
    input_sha256 CHAR(64),
    result_sha256 CHAR(64),
    manifest_sha256 CHAR(64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT backtests_pkey PRIMARY KEY (id),
    CONSTRAINT fk_backtests_owner
        FOREIGN KEY (owner_user_id) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT fk_backtests_strategy_version
        FOREIGN KEY (strategy_version_id) REFERENCES strategy_versions (id) ON DELETE RESTRICT,
    CONSTRAINT fk_backtests_worker_task
        FOREIGN KEY (worker_task_id) REFERENCES worker_tasks (id) ON DELETE RESTRICT,
    CONSTRAINT fk_backtests_idempotency
        FOREIGN KEY (idempotency_record_id) REFERENCES idempotency_records (id) ON DELETE RESTRICT,
    CONSTRAINT uq_backtests_worker_task UNIQUE (worker_task_id),
    CONSTRAINT uq_backtests_idempotency UNIQUE (idempotency_record_id),
    CONSTRAINT ck_backtests_simulator_version CHECK (simulator_version = 'decimal-bar-v1'),
    CONSTRAINT ck_backtests_id_uuidv7 CHECK (
        id::text ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
    ),
    CONSTRAINT ck_backtests_parameters CHECK (jsonb_typeof(parameters_json) = 'object'),
    CONSTRAINT ck_backtests_window CHECK (
        isfinite(start_time) AND isfinite(end_time) AND start_time < end_time
    ),
    CONSTRAINT ck_backtests_amounts CHECK (
        allocation_usdt > 0
        AND initial_equity > 0
        AND allocation_usdt <= initial_equity
    ),
    CONSTRAINT ck_backtests_rates CHECK (
        fee_rate >= 0 AND fee_rate < 1
        AND slippage_rate >= 0 AND slippage_rate < 1
        AND (stop_loss_ratio IS NULL OR stop_loss_ratio > 0 AND stop_loss_ratio < 1)
        AND (
            maintenance_margin_ratio IS NULL
            OR maintenance_margin_ratio > 0 AND maintenance_margin_ratio < 1
        )
    ),
    CONSTRAINT ck_backtests_funding_rates CHECK (jsonb_typeof(funding_rates_json) = 'array'),
    CONSTRAINT ck_backtests_results CHECK (
        (summary_json IS NULL AND input_sha256 IS NULL AND result_sha256 IS NULL AND manifest_sha256 IS NULL)
        OR
        (
            summary_json IS NOT NULL
            AND input_sha256 IS NOT NULL
            AND result_sha256 IS NOT NULL
            AND manifest_sha256 IS NOT NULL
            AND jsonb_typeof(summary_json) = 'object'
            AND input_sha256 ~ '^[0-9a-f]{64}$'
            AND result_sha256 ~ '^[0-9a-f]{64}$'
            AND manifest_sha256 ~ '^[0-9a-f]{64}$'
        )
    ),
    CONSTRAINT ck_backtests_created_at CHECK (isfinite(created_at))
);

CREATE INDEX ix_strategy_versions_published
    ON strategy_versions (published_at DESC, id DESC);
CREATE INDEX ix_backtests_owner_created
    ON backtests (owner_user_id, created_at DESC, id DESC);


ALTER TABLE worker_tasks
    ADD COLUMN dedupe_key VARCHAR(255),
    ADD CONSTRAINT ck_worker_tasks_realtime_lane CHECK (
        task_type <> 'strategy.realtime' OR lane = 'realtime'
    ),
    ADD CONSTRAINT ck_worker_tasks_realtime_dedupe CHECK (
        task_type <> 'strategy.realtime' OR dedupe_key IS NOT NULL
    ),
    ADD CONSTRAINT ck_worker_tasks_realtime_payload CHECK (
        CASE task_type
        WHEN 'strategy.realtime' THEN (
            jsonb_typeof(payload_json::jsonb) = 'object'
            AND payload_json::jsonb ?& ARRAY['instanceId', 'candleOpenTime']
            AND payload_json::jsonb - 'instanceId' - 'candleOpenTime' = '{}'::jsonb
            AND COALESCE(
                payload_json::jsonb ->> 'instanceId'
                    ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$',
                FALSE
            )
            AND COALESCE(
                payload_json::jsonb ->> 'candleOpenTime'
                    ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?Z$',
                FALSE
            )
        )
        ELSE TRUE
        END
    );

CREATE UNIQUE INDEX ux_worker_tasks_type_dedupe
    ON worker_tasks (task_type, dedupe_key)
    WHERE dedupe_key IS NOT NULL;

CREATE TABLE strategy_instances (
    id UUID NOT NULL,
    owner_user_id BIGINT NOT NULL,
    strategy_version_id UUID NOT NULL,
    name VARCHAR(120) NOT NULL,
    mode VARCHAR(16) NOT NULL DEFAULT 'signal_only',
    environment VARCHAR(16) NOT NULL DEFAULT 'paper',
    parameters_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    is_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT strategy_instances_pkey PRIMARY KEY (id),
    CONSTRAINT fk_strategy_instances_owner
        FOREIGN KEY (owner_user_id) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT fk_strategy_instances_version
        FOREIGN KEY (strategy_version_id) REFERENCES strategy_versions (id) ON DELETE RESTRICT,
    CONSTRAINT ck_strategy_instances_id_uuidv7 CHECK (
        id::text ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
    ),
    CONSTRAINT ck_strategy_instances_name CHECK (name = BTRIM(name) AND name <> ''),
    CONSTRAINT ck_strategy_instances_mode CHECK (mode IN ('signal_only', 'manual', 'auto')),
    CONSTRAINT ck_strategy_instances_environment CHECK (environment IN ('paper', 'testnet', 'live')),
    CONSTRAINT ck_strategy_instances_parameters CHECK (jsonb_typeof(parameters_json) = 'object'),
    CONSTRAINT ck_strategy_instances_times CHECK (isfinite(created_at) AND isfinite(updated_at))
);

CREATE TABLE strategy_signals (
    id UUID NOT NULL,
    owner_user_id BIGINT NOT NULL,
    strategy_instance_id UUID NOT NULL,
    strategy_version_id UUID NOT NULL,
    instrument_id UUID NOT NULL,
    interval_code VARCHAR(4) NOT NULL,
    candle_open_time TIMESTAMPTZ NOT NULL,
    candle_close_time TIMESTAMPTZ NOT NULL,
    target NUMERIC(38,18) NOT NULL,
    mode VARCHAR(16) NOT NULL,
    environment VARCHAR(16) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT strategy_signals_pkey PRIMARY KEY (id),
    CONSTRAINT fk_strategy_signals_owner
        FOREIGN KEY (owner_user_id) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT fk_strategy_signals_instance
        FOREIGN KEY (strategy_instance_id) REFERENCES strategy_instances (id) ON DELETE RESTRICT,
    CONSTRAINT fk_strategy_signals_version
        FOREIGN KEY (strategy_version_id) REFERENCES strategy_versions (id) ON DELETE RESTRICT,
    CONSTRAINT fk_strategy_signals_instrument
        FOREIGN KEY (instrument_id) REFERENCES market_instruments (id) ON DELETE RESTRICT,
    CONSTRAINT uq_strategy_signals_instance_candle UNIQUE (strategy_instance_id, candle_open_time),
    CONSTRAINT ck_strategy_signals_id_uuidv7 CHECK (
        id::text ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
    ),
    CONSTRAINT ck_strategy_signals_interval CHECK (interval_code IN ('1m', '5m', '15m', '1h', '4h', '1d')),
    CONSTRAINT ck_strategy_signals_times CHECK (
        isfinite(candle_open_time)
        AND isfinite(candle_close_time)
        AND candle_open_time < candle_close_time
        AND isfinite(created_at)
        AND (expires_at IS NULL OR isfinite(expires_at) AND expires_at > candle_close_time)
    ),
    CONSTRAINT ck_strategy_signals_target CHECK (target >= -1 AND target <= 1),
    CONSTRAINT ck_strategy_signals_mode CHECK (mode IN ('signal_only', 'manual', 'auto')),
    CONSTRAINT ck_strategy_signals_environment CHECK (environment IN ('paper', 'testnet', 'live')),
    CONSTRAINT ck_strategy_signals_status CHECK (status IN ('active', 'expired')),
    CONSTRAINT ck_strategy_signals_expiry CHECK (
        (mode = 'manual' AND expires_at IS NOT NULL)
        OR (mode <> 'manual' AND expires_at IS NULL)
    )
);

CREATE INDEX ix_strategy_instances_owner_enabled
    ON strategy_instances (owner_user_id, is_enabled, id DESC);
CREATE INDEX ix_strategy_signals_owner_created
    ON strategy_signals (owner_user_id, created_at DESC, id DESC);


ALTER TABLE strategy_signals
    DROP CONSTRAINT ck_strategy_signals_status,
    ADD COLUMN decision_idempotency_record_id BIGINT,
    ADD COLUMN decided_by_user_id BIGINT,
    ADD COLUMN decided_at TIMESTAMPTZ,
    ADD CONSTRAINT fk_strategy_signals_decision_idempotency
        FOREIGN KEY (decision_idempotency_record_id) REFERENCES idempotency_records (id) ON DELETE RESTRICT,
    ADD CONSTRAINT fk_strategy_signals_decided_by
        FOREIGN KEY (decided_by_user_id) REFERENCES users (id) ON DELETE RESTRICT,
    ADD CONSTRAINT uq_strategy_signals_decision_idempotency UNIQUE (decision_idempotency_record_id),
    ADD CONSTRAINT ck_strategy_signals_status
        CHECK (status IN ('active', 'approved', 'rejected', 'expired')),
    ADD CONSTRAINT ck_strategy_signals_decision_state CHECK (
        (
            status IN ('active', 'expired')
            AND decision_idempotency_record_id IS NULL
            AND decided_by_user_id IS NULL
            AND decided_at IS NULL
        )
        OR
        (
            status IN ('approved', 'rejected')
            AND mode = 'manual'
            AND decision_idempotency_record_id IS NOT NULL
            AND decided_by_user_id = owner_user_id
            AND decided_at IS NOT NULL
            AND isfinite(decided_at)
            AND expires_at IS NOT NULL
            AND decided_at < expires_at
        )
    );

ALTER TABLE notification_deliveries
    ADD COLUMN strategy_signal_id UUID,
    ADD CONSTRAINT fk_notification_deliveries_strategy_signal
        FOREIGN KEY (strategy_signal_id) REFERENCES strategy_signals (id) ON DELETE RESTRICT,
    ADD CONSTRAINT ck_notification_deliveries_strategy_signal CHECK (
        strategy_signal_id IS NULL
        OR (
            target_type = 'strategy_signal'
            AND target_id IS NULL
            AND recipient_user_id IS NOT NULL
        )
    );

CREATE UNIQUE INDEX ux_notification_deliveries_in_app_signal
    ON notification_deliveries (strategy_signal_id)
    WHERE strategy_signal_id IS NOT NULL AND channel_type = 'in_app';

CREATE UNIQUE INDEX ux_strategy_signals_manual_active_instance
    ON strategy_signals (strategy_instance_id)
    WHERE mode = 'manual' AND status = 'active';


CREATE UNIQUE INDEX ux_notification_deliveries_signal_channel
    ON notification_deliveries (strategy_signal_id, channel_id)
    WHERE strategy_signal_id IS NOT NULL AND channel_id IS NOT NULL;


CREATE TABLE trading_controls (
    id SMALLINT NOT NULL,
    emergency_stopped BOOLEAN NOT NULL DEFAULT TRUE,
    stop_reason VARCHAR(255) NOT NULL DEFAULT 'initial_safety_stop',
    stopped_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    stopped_by_user_id BIGINT,
    released_at TIMESTAMPTZ,
    released_by_user_id BIGINT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT trading_controls_pkey PRIMARY KEY (id),
    CONSTRAINT fk_trading_controls_stopped_by
        FOREIGN KEY (stopped_by_user_id) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT fk_trading_controls_released_by
        FOREIGN KEY (released_by_user_id) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT ck_trading_controls_singleton CHECK (id = 1),
    CONSTRAINT ck_trading_controls_state CHECK (
        (
            emergency_stopped
            AND stop_reason <> ''
            AND isfinite(stopped_at)
            AND released_at IS NULL
            AND released_by_user_id IS NULL
        )
        OR
        (
            NOT emergency_stopped
            AND stop_reason = ''
            AND released_at IS NOT NULL
            AND isfinite(released_at)
            AND released_at >= stopped_at
            AND released_by_user_id IS NOT NULL
        )
    ),
    CONSTRAINT ck_trading_controls_updated_at CHECK (isfinite(updated_at))
);

INSERT INTO trading_controls (id) VALUES (1);

CREATE TABLE trading_accounts (
    id UUID NOT NULL,
    owner_user_id BIGINT NOT NULL,
    name VARCHAR(120) NOT NULL,
    market_type VARCHAR(16) NOT NULL,
    environment VARCHAR(16) NOT NULL DEFAULT 'paper',
    status VARCHAR(16) NOT NULL DEFAULT 'paused',
    pause_reason VARCHAR(255) NOT NULL DEFAULT 'configuration_required',
    automation_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    automation_authorized_at TIMESTAMPTZ,
    automation_authorized_by_user_id BIGINT,
    initial_balance NUMERIC(38,18) NOT NULL,
    paper_fee_rate NUMERIC(38,18) NOT NULL,
    max_total_notional NUMERIC(38,18),
    max_symbol_notional NUMERIC(38,18),
    max_order_notional NUMERIC(38,18),
    max_daily_loss NUMERIC(38,18),
    max_drawdown NUMERIC(38,18),
    max_quote_age_seconds INTEGER,
    leverage INTEGER,
    creation_idempotency_record_id BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT trading_accounts_pkey PRIMARY KEY (id),
    CONSTRAINT fk_trading_accounts_owner
        FOREIGN KEY (owner_user_id) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT fk_trading_accounts_authorized_by
        FOREIGN KEY (automation_authorized_by_user_id) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT fk_trading_accounts_creation_idempotency
        FOREIGN KEY (creation_idempotency_record_id) REFERENCES idempotency_records (id) ON DELETE RESTRICT,
    CONSTRAINT uq_trading_accounts_creation_idempotency UNIQUE (creation_idempotency_record_id),
    CONSTRAINT ck_trading_accounts_id_uuidv7 CHECK (
        id::text ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
    ),
    CONSTRAINT ck_trading_accounts_name CHECK (name = BTRIM(name) AND name <> ''),
    CONSTRAINT ck_trading_accounts_market CHECK (market_type IN ('spot', 'usd_m')),
    CONSTRAINT ck_trading_accounts_environment CHECK (environment = 'paper'),
    CONSTRAINT ck_trading_accounts_status CHECK (status IN ('active', 'paused')),
    CONSTRAINT ck_trading_accounts_pause_state CHECK (
        (status = 'active' AND pause_reason = '')
        OR (status = 'paused' AND pause_reason <> '')
    ),
    CONSTRAINT ck_trading_accounts_authorization CHECK (
        (automation_authorized_at IS NULL AND automation_authorized_by_user_id IS NULL)
        OR (
            automation_authorized_at IS NOT NULL
            AND isfinite(automation_authorized_at)
            AND automation_authorized_by_user_id IS NOT NULL
        )
    ),
    CONSTRAINT ck_trading_accounts_initial_balance CHECK (initial_balance > 0),
    CONSTRAINT ck_trading_accounts_fee_rate CHECK (paper_fee_rate >= 0 AND paper_fee_rate <= 0.01),
    CONSTRAINT ck_trading_accounts_limits CHECK (
        (max_total_notional IS NULL OR max_total_notional > 0)
        AND (max_symbol_notional IS NULL OR max_symbol_notional > 0)
        AND (max_order_notional IS NULL OR max_order_notional > 0)
        AND (max_daily_loss IS NULL OR max_daily_loss > 0)
        AND (max_drawdown IS NULL OR max_drawdown > 0)
        AND (max_quote_age_seconds IS NULL OR max_quote_age_seconds BETWEEN 1 AND 300)
    ),
    CONSTRAINT ck_trading_accounts_leverage CHECK (
        (market_type = 'spot' AND leverage IS NULL)
        OR (market_type = 'usd_m' AND (leverage IS NULL OR leverage BETWEEN 1 AND 5))
    ),
    CONSTRAINT ck_trading_accounts_times CHECK (isfinite(created_at) AND isfinite(updated_at))
);

CREATE TABLE trading_account_instruments (
    account_id UUID NOT NULL,
    instrument_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT trading_account_instruments_pkey PRIMARY KEY (account_id, instrument_id),
    CONSTRAINT fk_trading_account_instruments_account
        FOREIGN KEY (account_id) REFERENCES trading_accounts (id) ON DELETE CASCADE,
    CONSTRAINT fk_trading_account_instruments_instrument
        FOREIGN KEY (instrument_id) REFERENCES market_instruments (id) ON DELETE RESTRICT,
    CONSTRAINT ck_trading_account_instruments_created_at CHECK (isfinite(created_at))
);

ALTER TABLE strategy_instances
    ADD COLUMN trading_account_id UUID,
    ADD COLUMN allocation_usdt NUMERIC(38,18),
    ADD CONSTRAINT fk_strategy_instances_trading_account
        FOREIGN KEY (trading_account_id) REFERENCES trading_accounts (id) ON DELETE RESTRICT,
    ADD CONSTRAINT ck_strategy_instances_allocation CHECK (
        (trading_account_id IS NULL AND allocation_usdt IS NULL)
        OR (trading_account_id IS NOT NULL AND allocation_usdt IS NOT NULL AND allocation_usdt > 0)
    );

CREATE TABLE trading_intents (
    id UUID NOT NULL,
    account_id UUID NOT NULL,
    strategy_signal_id UUID NOT NULL,
    strategy_instance_id UUID NOT NULL,
    owner_user_id BIGINT NOT NULL,
    instrument_id UUID NOT NULL,
    market_type VARCHAR(16) NOT NULL,
    mode VARCHAR(16) NOT NULL,
    environment VARCHAR(16) NOT NULL,
    target NUMERIC(38,18) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    block_reason VARCHAR(255) NOT NULL DEFAULT '',
    client_order_id VARCHAR(64) NOT NULL,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    claimed_at TIMESTAMPTZ,
    worker_id VARCHAR(120),
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT trading_intents_pkey PRIMARY KEY (id),
    CONSTRAINT fk_trading_intents_account
        FOREIGN KEY (account_id) REFERENCES trading_accounts (id) ON DELETE RESTRICT,
    CONSTRAINT fk_trading_intents_signal
        FOREIGN KEY (strategy_signal_id) REFERENCES strategy_signals (id) ON DELETE RESTRICT,
    CONSTRAINT fk_trading_intents_instance
        FOREIGN KEY (strategy_instance_id) REFERENCES strategy_instances (id) ON DELETE RESTRICT,
    CONSTRAINT fk_trading_intents_owner
        FOREIGN KEY (owner_user_id) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT fk_trading_intents_instrument
        FOREIGN KEY (instrument_id) REFERENCES market_instruments (id) ON DELETE RESTRICT,
    CONSTRAINT uq_trading_intents_signal UNIQUE (strategy_signal_id),
    CONSTRAINT uq_trading_intents_account_client_order UNIQUE (account_id, client_order_id),
    CONSTRAINT ck_trading_intents_id_uuidv7 CHECK (
        id::text ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
    ),
    CONSTRAINT ck_trading_intents_market CHECK (market_type IN ('spot', 'usd_m')),
    CONSTRAINT ck_trading_intents_mode CHECK (mode IN ('manual', 'auto')),
    CONSTRAINT ck_trading_intents_environment CHECK (environment = 'paper'),
    CONSTRAINT ck_trading_intents_target CHECK (
        target >= -1 AND target <= 1 AND (market_type <> 'spot' OR target >= 0)
    ),
    CONSTRAINT ck_trading_intents_status CHECK (
        status IN ('pending', 'processing', 'executed', 'blocked', 'failed')
    ),
    CONSTRAINT ck_trading_intents_terminal_state CHECK (
        (status IN ('pending', 'processing') AND completed_at IS NULL)
        OR (status IN ('executed', 'blocked', 'failed') AND completed_at IS NOT NULL AND isfinite(completed_at))
    ),
    CONSTRAINT ck_trading_intents_claim_state CHECK (
        (status = 'processing' AND claimed_at IS NOT NULL AND worker_id IS NOT NULL)
        OR status <> 'processing'
    ),
    CONSTRAINT ck_trading_intents_attempts CHECK (attempt_count >= 0),
    CONSTRAINT ck_trading_intents_times CHECK (
        isfinite(created_at)
        AND isfinite(updated_at)
        AND (claimed_at IS NULL OR isfinite(claimed_at))
    )
);

CREATE TABLE paper_orders (
    id UUID NOT NULL,
    account_id UUID NOT NULL,
    intent_id UUID NOT NULL,
    instrument_id UUID NOT NULL,
    client_order_id VARCHAR(64) NOT NULL,
    side VARCHAR(4) NOT NULL,
    quantity NUMERIC(38,18) NOT NULL,
    filled_quantity NUMERIC(38,18) NOT NULL,
    average_price NUMERIC(38,18) NOT NULL,
    status VARCHAR(16) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT paper_orders_pkey PRIMARY KEY (id),
    CONSTRAINT fk_paper_orders_account
        FOREIGN KEY (account_id) REFERENCES trading_accounts (id) ON DELETE RESTRICT,
    CONSTRAINT fk_paper_orders_intent
        FOREIGN KEY (intent_id) REFERENCES trading_intents (id) ON DELETE RESTRICT,
    CONSTRAINT fk_paper_orders_instrument
        FOREIGN KEY (instrument_id) REFERENCES market_instruments (id) ON DELETE RESTRICT,
    CONSTRAINT uq_paper_orders_intent UNIQUE (intent_id),
    CONSTRAINT uq_paper_orders_account_client_order UNIQUE (account_id, client_order_id),
    CONSTRAINT ck_paper_orders_side CHECK (side IN ('buy', 'sell')),
    CONSTRAINT ck_paper_orders_values CHECK (
        quantity > 0
        AND filled_quantity >= 0
        AND filled_quantity <= quantity
        AND average_price > 0
    ),
    CONSTRAINT ck_paper_orders_status CHECK (status IN ('accepted', 'filled', 'canceled', 'failed')),
    CONSTRAINT ck_paper_orders_times CHECK (isfinite(created_at) AND isfinite(updated_at))
);

CREATE TABLE trading_events (
    id BIGSERIAL NOT NULL,
    event_id UUID NOT NULL,
    account_id UUID NOT NULL,
    intent_id UUID,
    order_id UUID,
    instrument_id UUID NOT NULL,
    event_type VARCHAR(16) NOT NULL,
    side VARCHAR(4),
    quantity NUMERIC(38,18),
    price NUMERIC(38,18),
    amount NUMERIC(38,18),
    occurred_at TIMESTAMPTZ NOT NULL,
    dedupe_key VARCHAR(160) NOT NULL,
    correction_of BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT trading_events_pkey PRIMARY KEY (id),
    CONSTRAINT uq_trading_events_event_id UNIQUE (event_id),
    CONSTRAINT uq_trading_events_account_dedupe UNIQUE (account_id, dedupe_key),
    CONSTRAINT fk_trading_events_account
        FOREIGN KEY (account_id) REFERENCES trading_accounts (id) ON DELETE RESTRICT,
    CONSTRAINT fk_trading_events_intent
        FOREIGN KEY (intent_id) REFERENCES trading_intents (id) ON DELETE RESTRICT,
    CONSTRAINT fk_trading_events_instrument
        FOREIGN KEY (instrument_id) REFERENCES market_instruments (id) ON DELETE RESTRICT,
    CONSTRAINT fk_trading_events_correction
        FOREIGN KEY (correction_of) REFERENCES trading_events (id) ON DELETE RESTRICT,
    CONSTRAINT ck_trading_events_id_uuidv7 CHECK (
        event_id::text ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
    ),
    CONSTRAINT ck_trading_events_type CHECK (event_type IN ('order', 'fill', 'fee', 'funding')),
    CONSTRAINT ck_trading_events_shape CHECK (
        (
            event_type IN ('order', 'fill')
            AND side IN ('buy', 'sell')
            AND quantity IS NOT NULL AND quantity > 0
            AND price IS NOT NULL AND price > 0
            AND amount IS NULL
            AND order_id IS NOT NULL
            AND intent_id IS NOT NULL
        )
        OR (
            event_type = 'fee'
            AND side IS NULL
            AND quantity IS NULL
            AND price IS NULL
            AND amount IS NOT NULL AND amount >= 0
            AND order_id IS NOT NULL
            AND intent_id IS NOT NULL
        )
        OR (
            event_type = 'funding'
            AND side IS NULL
            AND quantity IS NULL
            AND price IS NOT NULL AND price > 0
            AND amount IS NOT NULL
        )
    ),
    CONSTRAINT ck_trading_events_times CHECK (isfinite(occurred_at) AND isfinite(created_at))
);

-- +goose StatementBegin
CREATE FUNCTION reject_trading_event_mutation() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'trading_events is append-only';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER trading_events_append_only
BEFORE UPDATE OR DELETE ON trading_events
FOR EACH ROW EXECUTE FUNCTION reject_trading_event_mutation();

CREATE TABLE paper_positions (
    account_id UUID NOT NULL,
    instrument_id UUID NOT NULL,
    owner_strategy_instance_id UUID,
    quantity NUMERIC(38,18) NOT NULL DEFAULT 0,
    average_entry_price NUMERIC(38,18) NOT NULL DEFAULT 0,
    last_price NUMERIC(38,18) NOT NULL DEFAULT 0,
    realized_pnl NUMERIC(38,18) NOT NULL DEFAULT 0,
    unrealized_pnl NUMERIC(38,18) NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT paper_positions_pkey PRIMARY KEY (account_id, instrument_id),
    CONSTRAINT fk_paper_positions_account
        FOREIGN KEY (account_id) REFERENCES trading_accounts (id) ON DELETE RESTRICT,
    CONSTRAINT fk_paper_positions_instrument
        FOREIGN KEY (instrument_id) REFERENCES market_instruments (id) ON DELETE RESTRICT,
    CONSTRAINT fk_paper_positions_owner_instance
        FOREIGN KEY (owner_strategy_instance_id) REFERENCES strategy_instances (id) ON DELETE RESTRICT,
    CONSTRAINT ck_paper_positions_owner CHECK (
        (quantity = 0 AND owner_strategy_instance_id IS NULL AND average_entry_price = 0)
        OR (quantity <> 0 AND owner_strategy_instance_id IS NOT NULL AND average_entry_price > 0)
    ),
    CONSTRAINT ck_paper_positions_prices CHECK (last_price >= 0),
    CONSTRAINT ck_paper_positions_updated_at CHECK (isfinite(updated_at))
);

CREATE TABLE paper_balances (
    account_id UUID NOT NULL,
    cash_balance NUMERIC(38,18) NOT NULL,
    equity NUMERIC(38,18) NOT NULL,
    peak_equity NUMERIC(38,18) NOT NULL,
    day_start_date DATE NOT NULL,
    day_start_equity NUMERIC(38,18) NOT NULL,
    realized_pnl NUMERIC(38,18) NOT NULL DEFAULT 0,
    unrealized_pnl NUMERIC(38,18) NOT NULL DEFAULT 0,
    fees NUMERIC(38,18) NOT NULL DEFAULT 0,
    funding NUMERIC(38,18) NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT paper_balances_pkey PRIMARY KEY (account_id),
    CONSTRAINT fk_paper_balances_account
        FOREIGN KEY (account_id) REFERENCES trading_accounts (id) ON DELETE RESTRICT,
    CONSTRAINT ck_paper_balances_peak CHECK (peak_equity >= equity),
    CONSTRAINT ck_paper_balances_updated_at CHECK (isfinite(updated_at))
);

CREATE INDEX ix_trading_accounts_owner ON trading_accounts (owner_user_id, id DESC);
CREATE INDEX ix_trading_intents_pending ON trading_intents (created_at, id) WHERE status = 'pending';
CREATE INDEX ix_trading_intents_owner ON trading_intents (owner_user_id, id DESC);
CREATE INDEX ix_paper_orders_account ON paper_orders (account_id, id DESC);
CREATE INDEX ix_trading_events_account ON trading_events (account_id, id);



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

    IF NEW.purpose = 'rebalance'
       AND account_market = 'spot'
       AND NEW.reduce_only THEN
        RAISE EXCEPTION 'testnet Spot rebalance cannot set reduceOnly';
    END IF;

    IF NEW.purpose = 'flatten'
       AND NEW.reduce_only IS DISTINCT FROM (account_market = 'usd_m') THEN
        RAISE EXCEPTION 'testnet flatten order shape does not match account market';
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
    reduce_only,
    working_type,
    replaces_order_id
ON testnet_orders
FOR EACH ROW EXECUTE FUNCTION validate_testnet_order_binding();

CREATE UNIQUE INDEX uq_testnet_orders_active_protection
    ON testnet_orders (account_id, instrument_id)
    WHERE purpose = 'protection'
      AND status IN ('prepared', 'unknown', 'new', 'partially_filled');



ALTER TABLE testnet_open_orders
    DROP CONSTRAINT ck_testnet_open_orders_values,
    ADD COLUMN close_position BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN reduce_only BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN working_type VARCHAR(24) NOT NULL DEFAULT '',
    ADD CONSTRAINT ck_testnet_open_orders_values CHECK (
        price >= 0 AND stop_price >= 0
        AND (
            (close_position AND original_quantity = 0 AND executed_quantity = 0)
            OR (
                NOT close_position
                AND original_quantity > 0
                AND executed_quantity >= 0
                AND executed_quantity <= original_quantity
            )
        )
    );



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



ALTER TABLE testnet_orders
    ADD COLUMN recovered_at TIMESTAMPTZ,
    ADD CONSTRAINT ck_testnet_orders_recovered_at CHECK (
        recovered_at IS NULL OR isfinite(recovered_at)
    );



LOCK TABLE
    trading_accounts,
    trading_intents,
    strategy_instances,
    trading_account_credentials,
    testnet_orders,
    testnet_trade_facts
IN ACCESS EXCLUSIVE MODE;

ALTER TABLE trading_accounts
    ADD COLUMN manual_authorized_at TIMESTAMPTZ,
    ADD COLUMN manual_authorized_by_user_id BIGINT,
    ADD CONSTRAINT fk_trading_accounts_manual_authorized_by
        FOREIGN KEY (manual_authorized_by_user_id) REFERENCES users (id) ON DELETE RESTRICT,
    ADD CONSTRAINT ck_trading_accounts_manual_authorization CHECK (
        (manual_authorized_at IS NULL AND manual_authorized_by_user_id IS NULL)
        OR (
            environment = 'live'
            AND manual_authorized_at IS NOT NULL
            AND isfinite(manual_authorized_at)
            AND manual_authorized_by_user_id IS NOT NULL
            AND manual_authorized_by_user_id = owner_user_id
        )
    ),
    DROP CONSTRAINT ck_trading_accounts_environment,
    ADD CONSTRAINT ck_trading_accounts_environment CHECK (environment IN ('paper', 'testnet', 'live')),
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

ALTER TABLE trading_intents
    DROP CONSTRAINT ck_trading_intents_environment,
    ADD CONSTRAINT ck_trading_intents_environment CHECK (environment IN ('paper', 'testnet', 'live')),
    ADD CONSTRAINT ck_trading_intents_spot_live_manual CHECK (
        environment <> 'live' OR (market_type = 'spot' AND mode = 'manual')
    );

ALTER TABLE strategy_instances
    ADD CONSTRAINT ck_strategy_instances_spot_live_manual CHECK (
        environment <> 'live' OR mode IN ('signal_only', 'manual')
    );

CREATE UNIQUE INDEX uq_trading_intents_live_account_active
    ON trading_intents (account_id)
    WHERE environment = 'live' AND status IN ('processing', 'reconciling');

CREATE INDEX ix_trading_intents_live_runnable
    ON trading_intents (created_at, id)
    WHERE environment = 'live' AND market_type = 'spot' AND mode = 'manual'
      AND status IN ('pending', 'reconciling');

-- Private projections are shared by isolated Testnet and Live accounts; the
-- owning account remains the authoritative environment boundary.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION validate_testnet_trading_credential() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
    account_environment VARCHAR(16);
    account_market VARCHAR(16);
    account_owner BIGINT;
BEGIN
    SELECT environment, market_type, owner_user_id
    INTO account_environment, account_market, account_owner
    FROM trading_accounts WHERE id = NEW.account_id;
    IF account_environment NOT IN ('testnet', 'live')
       OR (account_environment = 'live' AND account_market <> 'spot') THEN
        RAISE EXCEPTION 'trading credentials require an enabled private account shape';
    END IF;
    IF account_owner IS DISTINCT FROM NEW.owner_user_id THEN
        RAISE EXCEPTION 'trading credential owner does not match account owner';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION preserve_testnet_trading_credential_account() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM trading_account_credentials
        WHERE account_id = OLD.id
          AND (NEW.environment IS DISTINCT FROM OLD.environment
               OR NEW.market_type IS DISTINCT FROM OLD.market_type
               OR owner_user_id IS DISTINCT FROM NEW.owner_user_id)
    ) THEN
        RAISE EXCEPTION 'trading account update would invalidate its credential binding';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

DROP TRIGGER trading_accounts_preserve_credential_binding ON trading_accounts;
CREATE TRIGGER trading_accounts_preserve_credential_binding
BEFORE UPDATE OF environment, market_type, owner_user_id ON trading_accounts
FOR EACH ROW EXECUTE FUNCTION preserve_testnet_trading_credential_account();

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

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION validate_testnet_trade_fact_binding() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
    order_account UUID;
    order_credential TIMESTAMPTZ;
    order_intent UUID;
    order_instrument UUID;
    intent_account UUID;
    intent_instrument UUID;
    intent_environment VARCHAR(16);
    account_environment VARCHAR(16);
    account_market VARCHAR(16);
    instrument_symbol VARCHAR(64);
    instrument_market VARCHAR(16);
    instrument_venue VARCHAR(16);
BEGIN
    SELECT environment, market_type INTO account_environment, account_market
    FROM trading_accounts WHERE id = NEW.account_id;

    IF account_environment NOT IN ('testnet', 'live')
       OR (account_environment = 'live' AND account_market <> 'spot') THEN
        RAISE EXCEPTION 'private trade fact account environment is invalid';
    END IF;
    IF NEW.event_type = 'funding' AND account_market IS DISTINCT FROM 'usd_m' THEN
        RAISE EXCEPTION 'private funding fact account market is invalid';
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
               WHERE account_id = NEW.account_id AND instrument_id = NEW.instrument_id
           ) THEN
            RAISE EXCEPTION 'private trade fact instrument binding is invalid';
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
            RAISE EXCEPTION 'private trade fact order binding is invalid';
        END IF;
    END IF;

    IF NEW.intent_id IS NOT NULL THEN
        SELECT account_id, instrument_id, environment
        INTO intent_account, intent_instrument, intent_environment
        FROM trading_intents WHERE id = NEW.intent_id;
        IF intent_account IS DISTINCT FROM NEW.account_id
           OR intent_instrument IS DISTINCT FROM NEW.instrument_id
           OR intent_environment IS DISTINCT FROM account_environment THEN
            RAISE EXCEPTION 'private trade fact intent binding is invalid';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd



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



LOCK TABLE
    trading_accounts,
    trading_intents,
    trading_account_credentials,
    testnet_positions,
    testnet_orders,
    testnet_trade_facts
IN ACCESS EXCLUSIVE MODE;

ALTER TABLE trading_accounts
    DROP CONSTRAINT ck_trading_accounts_spot_live_auto,
    ADD CONSTRAINT ck_trading_accounts_usdm_live_manual CHECK (
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
        OR (
            market_type = 'usd_m'
            AND NOT automation_enabled
            AND automation_authorized_at IS NULL
            AND automation_authorized_by_user_id IS NULL
            AND auto_authorized_at IS NULL
            AND auto_authorized_by_user_id IS NULL
            AND (status = 'paused' OR manual_authorized_at IS NOT NULL)
        )
    );

ALTER TABLE trading_intents
    DROP CONSTRAINT ck_trading_intents_spot_live_auto,
    ADD CONSTRAINT ck_trading_intents_usdm_live_manual CHECK (
        environment <> 'live'
        OR (market_type = 'spot' AND mode IN ('manual', 'auto'))
        OR (market_type = 'usd_m' AND mode = 'manual')
    );

DROP INDEX ix_trading_intents_live_runnable;
CREATE INDEX ix_trading_intents_live_runnable
    ON trading_intents (created_at, id)
    WHERE environment = 'live'
      AND ((market_type = 'spot' AND mode IN ('manual', 'auto'))
           OR (market_type = 'usd_m' AND mode = 'manual'))
      AND status IN ('pending', 'reconciling');

ALTER TABLE testnet_positions
    ADD COLUMN mark_price NUMERIC(38,18) NOT NULL DEFAULT 0,
    ADD COLUMN liquidation_price NUMERIC(38,18) NOT NULL DEFAULT 0,
    ADD COLUMN liquidation_distance_ratio NUMERIC(38,18) NOT NULL DEFAULT 0,
    ADD COLUMN leverage INTEGER,
    ADD COLUMN isolated BOOLEAN,
    ADD CONSTRAINT ck_testnet_positions_live_risk_values CHECK (
        mark_price >= 0
        AND liquidation_price >= 0
        AND liquidation_distance_ratio >= 0
    ),
    ADD CONSTRAINT ck_testnet_positions_live_risk_shape CHECK (
        (leverage IS NULL AND isolated IS NULL)
        OR (leverage BETWEEN 1 AND 125 AND isolated IS NOT NULL)
    );

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION validate_testnet_trading_credential() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
    account_environment VARCHAR(16);
    account_owner BIGINT;
BEGIN
    SELECT environment, owner_user_id
    INTO account_environment, account_owner
    FROM trading_accounts WHERE id = NEW.account_id;
    IF account_environment NOT IN ('testnet', 'live') THEN
        RAISE EXCEPTION 'trading credentials require an enabled private account shape';
    END IF;
    IF account_owner IS DISTINCT FROM NEW.owner_user_id THEN
        RAISE EXCEPTION 'trading credential owner does not match account owner';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

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
       OR (account_environment = 'live' AND NOT (
            (account_market = 'spot' AND intent_mode IN ('manual', 'auto'))
            OR (account_market = 'usd_m' AND intent_mode = 'manual')
       ))
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

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION validate_testnet_trade_fact_binding() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
    order_account UUID;
    order_credential TIMESTAMPTZ;
    order_intent UUID;
    order_instrument UUID;
    intent_account UUID;
    intent_instrument UUID;
    intent_environment VARCHAR(16);
    account_environment VARCHAR(16);
    account_market VARCHAR(16);
    instrument_symbol VARCHAR(64);
    instrument_market VARCHAR(16);
    instrument_venue VARCHAR(16);
BEGIN
    SELECT environment, market_type INTO account_environment, account_market
    FROM trading_accounts WHERE id = NEW.account_id;

    IF account_environment NOT IN ('testnet', 'live') THEN
        RAISE EXCEPTION 'private trade fact account environment is invalid';
    END IF;
    IF NEW.event_type = 'funding' AND account_market IS DISTINCT FROM 'usd_m' THEN
        RAISE EXCEPTION 'private funding fact account market is invalid';
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
               WHERE account_id = NEW.account_id AND instrument_id = NEW.instrument_id
           ) THEN
            RAISE EXCEPTION 'private trade fact instrument binding is invalid';
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
            RAISE EXCEPTION 'private trade fact order binding is invalid';
        END IF;
    END IF;

    IF NEW.intent_id IS NOT NULL THEN
        SELECT account_id, instrument_id, environment
        INTO intent_account, intent_instrument, intent_environment
        FROM trading_intents WHERE id = NEW.intent_id;
        IF intent_account IS DISTINCT FROM NEW.account_id
           OR intent_instrument IS DISTINCT FROM NEW.instrument_id
           OR intent_environment IS DISTINCT FROM account_environment THEN
            RAISE EXCEPTION 'private trade fact intent binding is invalid';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd



LOCK TABLE
    trading_accounts,
    trading_intents,
    testnet_orders
IN ACCESS EXCLUSIVE MODE;

ALTER TABLE trading_accounts
    DROP CONSTRAINT ck_trading_accounts_usdm_live_manual,
    ADD CONSTRAINT ck_trading_accounts_usdm_live_auto CHECK (
        environment <> 'live'
        OR (
            market_type IN ('spot', 'usd_m')
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
    DROP CONSTRAINT ck_trading_intents_usdm_live_manual,
    ADD CONSTRAINT ck_trading_intents_usdm_live_auto CHECK (
        environment <> 'live'
        OR (market_type IN ('spot', 'usd_m') AND mode IN ('manual', 'auto'))
    );

DROP INDEX ix_trading_intents_live_runnable;
CREATE INDEX ix_trading_intents_live_runnable
    ON trading_intents (created_at, id)
    WHERE environment = 'live'
      AND market_type IN ('spot', 'usd_m')
      AND mode IN ('manual', 'auto')
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
       OR (account_environment = 'live' AND NOT (
            account_market IN ('spot', 'usd_m') AND intent_mode IN ('manual', 'auto')
       ))
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


CREATE TABLE market_sync_settings (
    id SMALLINT PRIMARY KEY CHECK (id = 1),
    venue VARCHAR(16) NOT NULL DEFAULT 'binance' CHECK (venue = 'binance'),
    market_types JSONB NOT NULL DEFAULT '["spot","usd_m"]'::jsonb,
    quote_assets JSONB NOT NULL DEFAULT '["USDT","USDC"]'::jsonb,
    updated_by_user_id BIGINT REFERENCES users (id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_market_sync_settings_market_types CHECK (
        jsonb_typeof(market_types) = 'array'
        AND jsonb_array_length(market_types) BETWEEN 1 AND 2
        AND market_types <@ '["spot","usd_m"]'::jsonb
    ),
    CONSTRAINT ck_market_sync_settings_quote_assets CHECK (
        jsonb_typeof(quote_assets) = 'array'
        AND jsonb_array_length(quote_assets) BETWEEN 1 AND 3
        AND quote_assets <@ '["USDT","USDC","FDUSD"]'::jsonb
    ),
    CONSTRAINT ck_market_sync_settings_times CHECK (
        isfinite(created_at) AND isfinite(updated_at)
    )
);

INSERT INTO market_sync_settings (id) VALUES (1);

CREATE TABLE market_workflow_subscriptions (
    workflow_definition_id BIGINT NOT NULL
        REFERENCES workflow_definitions (id) ON DELETE CASCADE,
    node_id VARCHAR(100) NOT NULL,
    instrument_id UUID NOT NULL
        REFERENCES market_instruments (id) ON DELETE RESTRICT,
    interval_code VARCHAR(4) NOT NULL
        CHECK (interval_code IN ('1m','5m','15m','1h','4h','1d')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (workflow_definition_id, node_id),
    CONSTRAINT ck_market_workflow_subscriptions_node CHECK (
        node_id = BTRIM(node_id) AND node_id <> ''
    ),
    CONSTRAINT ck_market_workflow_subscriptions_times CHECK (
        isfinite(created_at) AND isfinite(updated_at)
    )
);

CREATE INDEX ix_market_workflow_subscriptions_instrument_interval
    ON market_workflow_subscriptions (instrument_id, interval_code);

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

DELETE FROM workflow_executions
WHERE workflow_definition_id IN (
    SELECT id FROM workflow_definitions WHERE code = 'blockbeats_news_sync'
);

DELETE FROM workflow_runtime_states
WHERE workflow_code = 'blockbeats_news_sync';

DELETE FROM workflow_definitions
WHERE code = 'blockbeats_news_sync';

DELETE FROM i18n_texts
WHERE biz_type = 'button'
  AND biz_id IN (
      SELECT id FROM menu_buttons
      WHERE permission_code = 'scheduler.task_definitions.update'
  );

DELETE FROM menu_buttons
WHERE permission_code = 'scheduler.task_definitions.update';

UPDATE menus
SET name = 'NodeDefinitions',
    path = 'node-definition',
    permission_code = 'scheduler.workflow_definitions.view',
    component = '/scheduler/node-definition',
    title = '节点定义',
    updated_at = CURRENT_TIMESTAMP
WHERE name = 'TaskDefinitions';

UPDATE i18n_texts
SET text = CASE locale WHEN 'zh' THEN '节点定义' ELSE 'Node Definitions' END,
    updated_at = CURRENT_TIMESTAMP
WHERE biz_type = 'menu'
  AND biz_id IN (SELECT id FROM menus WHERE name = 'NodeDefinitions')
  AND locale IN ('zh', 'en');

DROP TABLE task_definition_configs;

-- The console reset is allowed to remove strategy execution state, but never
-- financial facts. A Paper account always has one pristine balance snapshot;
-- allow only that untouched snapshot through the guard.
LOCK TABLE trading_accounts, trading_intents, paper_orders, paper_positions, paper_balances,
    trading_events, testnet_orders, testnet_trade_facts, testnet_balances,
    testnet_positions, testnet_open_orders, strategy_signals, strategy_instances
    IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE workflow_console_financial_guard (
    violations BIGINT NOT NULL CHECK (violations = 0)
) ON COMMIT DROP;

INSERT INTO workflow_console_financial_guard (violations)
SELECT
    (SELECT COUNT(*) FROM trading_intents)
    + (SELECT COUNT(*) FROM paper_orders)
    + (SELECT COUNT(*) FROM paper_positions)
    + (
        SELECT COUNT(*)
        FROM paper_balances AS balance
        JOIN trading_accounts AS account ON account.id = balance.account_id
        WHERE account.environment <> 'paper'
           OR balance.cash_balance <> account.initial_balance
           OR balance.equity <> account.initial_balance
           OR balance.peak_equity <> account.initial_balance
           OR balance.day_start_equity <> account.initial_balance
           OR balance.realized_pnl <> 0
           OR balance.unrealized_pnl <> 0
           OR balance.fees <> 0
           OR balance.funding <> 0
    )
    + (SELECT COUNT(*) FROM trading_events)
    + (SELECT COUNT(*) FROM testnet_orders)
    + (SELECT COUNT(*) FROM testnet_trade_facts)
    + (SELECT COUNT(*) FROM testnet_balances)
    + (SELECT COUNT(*) FROM testnet_positions)
    + (SELECT COUNT(*) FROM testnet_open_orders);

-- User workflows which contain strategy nodes are not valid after the binding
-- split. Their execution history is intentionally removed with the definition.
DELETE FROM workflow_executions
WHERE workflow_definition_id IN (
    SELECT id FROM workflow_definitions
    WHERE NOT is_builtin AND graph_json LIKE '%strategy.evaluate%'
);
DELETE FROM workflow_definitions
WHERE NOT is_builtin
  AND graph_json LIKE '%strategy.evaluate%';

DELETE FROM notification_deliveries
WHERE strategy_signal_id IS NOT NULL;

CREATE TEMPORARY TABLE workflow_console_strategy_tasks (id VARCHAR(36) PRIMARY KEY) ON COMMIT DROP;
INSERT INTO workflow_console_strategy_tasks (id)
SELECT worker_task_id FROM backtests
UNION
SELECT worker_task_id FROM strategy_versions;
DROP TRIGGER IF EXISTS strategy_versions_immutable ON strategy_versions;
DROP FUNCTION IF EXISTS reject_strategy_version_mutation();
DELETE FROM strategy_signals;
DELETE FROM strategy_instances;
DELETE FROM backtests;
DELETE FROM strategy_versions;
DELETE FROM strategies;
DELETE FROM worker_tasks
WHERE id IN (SELECT id FROM workflow_console_strategy_tasks);

ALTER TABLE strategies
    ADD COLUMN IF NOT EXISTS archived_at TIMESTAMPTZ;

ALTER TABLE strategy_versions
    DROP CONSTRAINT IF EXISTS ck_strategy_versions_market,
    DROP CONSTRAINT IF EXISTS ck_strategy_versions_symbol,
    DROP CONSTRAINT IF EXISTS ck_strategy_versions_interval,
    DROP CONSTRAINT IF EXISTS fk_strategy_versions_instrument;

ALTER TABLE strategies
	DROP CONSTRAINT IF EXISTS ck_strategies_market,
	DROP CONSTRAINT IF EXISTS ck_strategies_interval,
	DROP CONSTRAINT IF EXISTS fk_strategies_instrument,
	DROP COLUMN market_type,
	DROP COLUMN instrument_id,
	DROP COLUMN interval_code;

ALTER TABLE strategy_versions
	DROP COLUMN market_type,
	DROP COLUMN instrument_id,
	DROP COLUMN symbol,
	DROP COLUMN interval_code;

-- Published snapshots are still immutable; the trigger is recreated after the
-- one-time reset so the cleanup above can remove legacy rows.
-- +goose StatementBegin
CREATE FUNCTION reject_strategy_version_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'strategy version mutation is not allowed';
    ELSIF OLD.status <> 'pending'
        OR NEW.status NOT IN ('published', 'failed')
        OR NEW.id IS DISTINCT FROM OLD.id
        OR NEW.strategy_id IS DISTINCT FROM OLD.strategy_id
        OR NEW.version_number IS DISTINCT FROM OLD.version_number
        OR NEW.worker_task_id IS DISTINCT FROM OLD.worker_task_id
        OR NEW.idempotency_record_id IS DISTINCT FROM OLD.idempotency_record_id
        OR NEW.name IS DISTINCT FROM OLD.name
        OR NEW.source_code IS DISTINCT FROM OLD.source_code
        OR NEW.code_sha256 IS DISTINCT FROM OLD.code_sha256
        OR NEW.runtime_version IS DISTINCT FROM OLD.runtime_version
        OR NEW.lookback_bars IS DISTINCT FROM OLD.lookback_bars
        OR NEW.parameter_schema_json IS DISTINCT FROM OLD.parameter_schema_json
        OR NEW.published_by_user_id IS DISTINCT FROM OLD.published_by_user_id
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
        OR (NEW.status = 'published' AND NEW.published_at IS NULL)
        OR (NEW.status = 'failed' AND NEW.published_at IS NOT NULL) THEN
        RAISE EXCEPTION 'strategy version mutation is not allowed';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER strategy_versions_immutable
BEFORE UPDATE OR DELETE ON strategy_versions
FOR EACH ROW EXECUTE FUNCTION reject_strategy_version_mutation();

ALTER TABLE backtests
    ADD COLUMN instrument_id UUID NOT NULL,
    ADD COLUMN interval_code VARCHAR(4) NOT NULL;

ALTER TABLE backtests
    ADD CONSTRAINT fk_backtests_instrument
        FOREIGN KEY (instrument_id) REFERENCES market_instruments (id) ON DELETE RESTRICT,
    ADD CONSTRAINT ck_backtests_interval
        CHECK (interval_code IN ('1m','5m','15m','1h','4h','1d'));

ALTER TABLE strategy_instances
    ADD COLUMN market_type VARCHAR(16) NOT NULL,
    ADD COLUMN instrument_id UUID NOT NULL,
    ADD COLUMN interval_code VARCHAR(4) NOT NULL,
    ADD COLUMN workflow_definition_id BIGINT NOT NULL,
    ADD COLUMN workflow_node_id VARCHAR(100) NOT NULL,
    ADD COLUMN archived_at TIMESTAMPTZ;

ALTER TABLE strategy_instances
    ADD CONSTRAINT fk_strategy_instances_instrument
        FOREIGN KEY (instrument_id) REFERENCES market_instruments (id) ON DELETE RESTRICT,
    ADD CONSTRAINT fk_strategy_instances_workflow_definition
        FOREIGN KEY (workflow_definition_id) REFERENCES workflow_definitions (id) ON DELETE RESTRICT,
    ADD CONSTRAINT ck_strategy_instances_market
        CHECK (market_type IN ('spot','usd_m')),
    ADD CONSTRAINT ck_strategy_instances_interval
        CHECK (interval_code IN ('1m','5m','15m','1h','4h','1d')),
    ADD CONSTRAINT ck_strategy_instances_binding
        CHECK (BTRIM(workflow_node_id) <> '');

CREATE UNIQUE INDEX ux_strategy_instances_workflow_binding
    ON strategy_instances (workflow_definition_id, workflow_node_id)
    WHERE archived_at IS NULL;

CREATE INDEX IF NOT EXISTS ix_strategy_instances_instrument_interval
    ON strategy_instances (instrument_id, interval_code)
    WHERE instrument_id IS NOT NULL;

ALTER TABLE trading_accounts
    ADD COLUMN IF NOT EXISTS archived_at TIMESTAMPTZ;

-- Only Binance Spot and USD-M metadata quoted in USDT is supported by the
-- console. Existing customized settings are normalized during this migration.
UPDATE market_sync_settings
SET market_types = '["spot","usd_m"]'::jsonb,
    quote_assets = '["USDT"]'::jsonb,
    updated_at = CURRENT_TIMESTAMP
WHERE id = 1;

ALTER TABLE market_sync_settings
    DROP CONSTRAINT IF EXISTS ck_market_sync_settings_quote_assets,
    ADD CONSTRAINT ck_market_sync_settings_quote_assets CHECK (
        quote_assets = '["USDT"]'::jsonb
    );

CREATE TABLE workflow_node_templates (
    id UUID NOT NULL,
    owner_user_id BIGINT NOT NULL,
    name VARCHAR(120) NOT NULL,
    description VARCHAR(500) NOT NULL DEFAULT '',
    icon VARCHAR(120) NOT NULL DEFAULT 'ri:node-tree',
    base_node_type VARCHAR(120) NOT NULL,
    default_config_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    is_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT workflow_node_templates_pkey PRIMARY KEY (id),
    CONSTRAINT fk_workflow_node_templates_owner
        FOREIGN KEY (owner_user_id) REFERENCES users (id) ON DELETE CASCADE,
    CONSTRAINT uq_workflow_node_templates_owner_name UNIQUE (owner_user_id, name),
    CONSTRAINT ck_workflow_node_templates_id_uuidv7 CHECK (
        id::text ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
    ),
    CONSTRAINT ck_workflow_node_templates_name CHECK (name = BTRIM(name) AND name <> ''),
    CONSTRAINT ck_workflow_node_templates_base CHECK (base_node_type = BTRIM(base_node_type) AND base_node_type <> ''),
    CONSTRAINT ck_workflow_node_templates_config CHECK (jsonb_typeof(default_config_json) = 'object'),
    CONSTRAINT ck_workflow_node_templates_times CHECK (isfinite(created_at) AND isfinite(updated_at))
);

CREATE INDEX ix_workflow_node_templates_owner_enabled
    ON workflow_node_templates (owner_user_id, is_enabled, updated_at DESC);

CREATE TABLE worker_heartbeats (
    worker_id VARCHAR(120) NOT NULL,
    lane VARCHAR(16) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'online',
    last_heartbeat_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    queue_depth INTEGER NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT worker_heartbeats_pkey PRIMARY KEY (worker_id, lane),
    CONSTRAINT ck_worker_heartbeats_lane CHECK (lane IN ('realtime','backtest')),
    CONSTRAINT ck_worker_heartbeats_status CHECK (status IN ('online','offline')),
    CONSTRAINT ck_worker_heartbeats_queue CHECK (queue_depth >= 0),
    CONSTRAINT ck_worker_heartbeats_times CHECK (isfinite(last_heartbeat_at) AND isfinite(updated_at))
);

CREATE INDEX ix_worker_heartbeats_lane_heartbeat
    ON worker_heartbeats (lane, last_heartbeat_at DESC);

UPDATE menus
SET is_active = FALSE, is_hidden = TRUE, updated_at = CURRENT_TIMESTAMP
WHERE name IN ('PaperTrading', 'StrategyCenter', 'PushData', 'NotifyChannels');

ALTER TABLE workflow_definitions
    ADD COLUMN owner_user_id BIGINT,
    ALTER COLUMN graph_json TYPE JSONB USING COALESCE(NULLIF(graph_json, ''), '{}')::jsonb;

UPDATE workflow_definitions
SET owner_user_id = created_by
WHERE NOT is_builtin;

ALTER TABLE workflow_definitions
    ADD CONSTRAINT fk_workflow_definitions_owner
        FOREIGN KEY (owner_user_id) REFERENCES users (id) ON DELETE CASCADE,
    ADD CONSTRAINT ck_workflow_definitions_owner CHECK (
        (is_builtin AND owner_user_id IS NULL)
        OR (NOT is_builtin AND owner_user_id IS NOT NULL)
    ),
    ADD CONSTRAINT ck_workflow_definitions_graph_v2 CHECK (
        jsonb_typeof(graph_json) = 'object'
        AND graph_json ->> 'schemaVersion' = '2'
    );

DROP INDEX ux_workflow_def_code_version;
CREATE UNIQUE INDEX ux_workflow_def_owner_code_version
    ON workflow_definitions (owner_user_id, code, version)
    WHERE owner_user_id IS NOT NULL;
CREATE UNIQUE INDEX ux_workflow_def_builtin_code_version
    ON workflow_definitions (code, version)
    WHERE is_builtin;

ALTER TABLE workflow_runtime_states ADD COLUMN owner_user_id BIGINT;
UPDATE workflow_runtime_states AS state
SET owner_user_id = definition.owner_user_id
FROM workflow_definitions AS definition
WHERE definition.id = state.active_workflow_definition_id;
DELETE FROM workflow_runtime_states WHERE owner_user_id IS NULL;
ALTER TABLE workflow_runtime_states
    ALTER COLUMN owner_user_id SET NOT NULL,
    ADD CONSTRAINT fk_workflow_runtime_states_owner
        FOREIGN KEY (owner_user_id) REFERENCES users (id) ON DELETE CASCADE;
DROP INDEX idx_workflow_runtime_states_workflow_code;
CREATE UNIQUE INDEX ux_workflow_runtime_owner_code
    ON workflow_runtime_states (owner_user_id, workflow_code);

ALTER TABLE workflow_executions
    ADD COLUMN owner_user_id BIGINT,
    ADD COLUMN cancel_requested_at TIMESTAMPTZ,
    ADD COLUMN rerun_of_execution_id BIGINT;
UPDATE workflow_executions AS execution
SET owner_user_id = definition.owner_user_id
FROM workflow_definitions AS definition
WHERE definition.id = execution.workflow_definition_id;
DELETE FROM workflow_executions WHERE owner_user_id IS NULL;
ALTER TABLE workflow_executions
    ALTER COLUMN owner_user_id SET NOT NULL,
    ADD CONSTRAINT fk_workflow_executions_owner
        FOREIGN KEY (owner_user_id) REFERENCES users (id) ON DELETE CASCADE,
    ADD CONSTRAINT fk_workflow_executions_rerun
        FOREIGN KEY (rerun_of_execution_id) REFERENCES workflow_executions (id) ON DELETE SET NULL;
CREATE INDEX ix_workflow_exec_owner_queue
    ON workflow_executions (owner_user_id, status, id DESC);

CREATE TABLE workflow_execution_waits (
    id UUID PRIMARY KEY,
    owner_user_id BIGINT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    workflow_execution_id BIGINT NOT NULL REFERENCES workflow_executions (id) ON DELETE CASCADE,
    workflow_execution_node_id BIGINT REFERENCES workflow_execution_nodes (id) ON DELETE SET NULL,
    kind VARCHAR(32) NOT NULL,
    action_type VARCHAR(120) NOT NULL DEFAULT '',
    target_type VARCHAR(64) NOT NULL DEFAULT '',
    target_id VARCHAR(120) NOT NULL DEFAULT '',
    status VARCHAR(24) NOT NULL DEFAULT 'pending',
    request_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    result_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    resume_node_id VARCHAR(100) NOT NULL DEFAULT '',
    resume_branch VARCHAR(32) NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ,
    resolved_at TIMESTAMPTZ,
    resolved_by BIGINT REFERENCES users (id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_workflow_wait_kind CHECK (kind IN ('worker_job', 'human_action')),
    CONSTRAINT ck_workflow_wait_status CHECK (status IN ('pending', 'processing', 'completed', 'rejected', 'expired', 'canceled')),
    CONSTRAINT ck_workflow_wait_json CHECK (
        jsonb_typeof(request_json) = 'object' AND jsonb_typeof(result_json) = 'object'
    )
);
CREATE UNIQUE INDEX ux_workflow_wait_active_execution
    ON workflow_execution_waits (workflow_execution_id)
    WHERE status IN ('pending', 'processing');
CREATE INDEX ix_workflow_wait_owner_status
    ON workflow_execution_waits (owner_user_id, status, created_at DESC);

-- +goose Down

DROP TABLE workflow_execution_waits;
DROP INDEX ix_workflow_exec_owner_queue;
ALTER TABLE workflow_executions
    DROP CONSTRAINT fk_workflow_executions_rerun,
    DROP CONSTRAINT fk_workflow_executions_owner,
    DROP COLUMN rerun_of_execution_id,
    DROP COLUMN cancel_requested_at,
    DROP COLUMN owner_user_id;

DROP INDEX ux_workflow_runtime_owner_code;
CREATE UNIQUE INDEX idx_workflow_runtime_states_workflow_code
    ON workflow_runtime_states (workflow_code);
ALTER TABLE workflow_runtime_states
    DROP CONSTRAINT fk_workflow_runtime_states_owner,
    DROP COLUMN owner_user_id;

DROP INDEX ux_workflow_def_builtin_code_version;
DROP INDEX ux_workflow_def_owner_code_version;
CREATE UNIQUE INDEX ux_workflow_def_code_version
    ON workflow_definitions (code, version);
ALTER TABLE workflow_definitions
    DROP CONSTRAINT ck_workflow_definitions_graph_v2,
    DROP CONSTRAINT ck_workflow_definitions_owner,
    DROP CONSTRAINT fk_workflow_definitions_owner,
    DROP COLUMN owner_user_id,
    ALTER COLUMN graph_json TYPE TEXT USING graph_json::text;

LOCK TABLE workflow_node_templates, worker_heartbeats, strategies,
    strategy_versions, strategy_instances, backtests, trading_accounts,
    market_sync_settings IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE workflow_console_down_guard (
    violations BIGINT NOT NULL CHECK (violations = 0)
) ON COMMIT DROP;

INSERT INTO workflow_console_down_guard (violations)
SELECT (SELECT COUNT(*) FROM workflow_node_templates)
     + (SELECT COUNT(*) FROM worker_heartbeats)
     + (SELECT COUNT(*) FROM strategies)
     + (SELECT COUNT(*) FROM strategy_versions)
     + (SELECT COUNT(*) FROM strategy_instances)
     + (SELECT COUNT(*) FROM backtests);

DROP INDEX IF EXISTS ix_worker_heartbeats_lane_heartbeat;
DROP TABLE worker_heartbeats;
DROP INDEX IF EXISTS ix_workflow_node_templates_owner_enabled;
DROP TABLE workflow_node_templates;

ALTER TABLE market_sync_settings
    DROP CONSTRAINT IF EXISTS ck_market_sync_settings_quote_assets,
    ADD CONSTRAINT ck_market_sync_settings_quote_assets CHECK (
        jsonb_typeof(quote_assets) = 'array'
        AND jsonb_array_length(quote_assets) BETWEEN 1 AND 3
        AND quote_assets <@ '["USDT","USDC","FDUSD"]'::jsonb
    );
UPDATE market_sync_settings
SET market_types = '["spot"]'::jsonb,
    quote_assets = '["USDT","USDC"]'::jsonb,
    updated_at = CURRENT_TIMESTAMP
WHERE id = 1;

ALTER TABLE trading_accounts DROP COLUMN IF EXISTS archived_at;

DROP INDEX IF EXISTS ux_strategy_instances_workflow_binding;
DROP INDEX IF EXISTS ix_strategy_instances_instrument_interval;

ALTER TABLE strategy_instances
    DROP CONSTRAINT IF EXISTS ck_strategy_instances_binding,
    DROP CONSTRAINT IF EXISTS ck_strategy_instances_interval,
    DROP CONSTRAINT IF EXISTS ck_strategy_instances_market,
    DROP CONSTRAINT IF EXISTS fk_strategy_instances_workflow_definition,
    DROP CONSTRAINT IF EXISTS fk_strategy_instances_instrument,
    DROP COLUMN IF EXISTS workflow_node_id,
    DROP COLUMN IF EXISTS workflow_definition_id,
    DROP COLUMN IF EXISTS interval_code,
    DROP COLUMN IF EXISTS instrument_id,
    DROP COLUMN IF EXISTS market_type,
    DROP COLUMN IF EXISTS archived_at;

ALTER TABLE backtests
    DROP CONSTRAINT IF EXISTS ck_backtests_interval,
    DROP CONSTRAINT IF EXISTS fk_backtests_instrument,
    DROP COLUMN IF EXISTS interval_code,
    DROP COLUMN IF EXISTS instrument_id;

DROP TRIGGER IF EXISTS strategy_versions_immutable ON strategy_versions;
DROP FUNCTION IF EXISTS reject_strategy_version_mutation();

ALTER TABLE strategy_versions
    ADD COLUMN market_type VARCHAR(32) NOT NULL,
    ADD COLUMN instrument_id UUID NOT NULL,
    ADD COLUMN symbol VARCHAR(64) NOT NULL,
    ADD COLUMN interval_code VARCHAR(4) NOT NULL,
    ADD CONSTRAINT fk_strategy_versions_instrument
        FOREIGN KEY (instrument_id) REFERENCES market_instruments (id) ON DELETE RESTRICT,
    ADD CONSTRAINT ck_strategy_versions_market CHECK (market_type IN ('spot','usd_m')),
    ADD CONSTRAINT ck_strategy_versions_symbol CHECK (symbol ~ '^[A-Z0-9][A-Z0-9._-]*$'),
    ADD CONSTRAINT ck_strategy_versions_interval CHECK (interval_code IN ('1m','5m','15m','1h','4h','1d'));

ALTER TABLE strategies
    ADD COLUMN market_type VARCHAR(32) NOT NULL,
    ADD COLUMN instrument_id UUID NOT NULL,
    ADD COLUMN interval_code VARCHAR(4) NOT NULL,
    ADD CONSTRAINT fk_strategies_instrument
        FOREIGN KEY (instrument_id) REFERENCES market_instruments (id) ON DELETE RESTRICT,
    ADD CONSTRAINT ck_strategies_market CHECK (market_type IN ('spot','usd_m')),
    ADD CONSTRAINT ck_strategies_interval CHECK (interval_code IN ('1m','5m','15m','1h','4h','1d'));

ALTER TABLE strategies DROP COLUMN IF EXISTS archived_at;

-- +goose StatementBegin
CREATE FUNCTION reject_strategy_version_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'strategy version mutation is not allowed';
    ELSIF OLD.status <> 'pending'
        OR NEW.status NOT IN ('published', 'failed')
        OR NEW.id IS DISTINCT FROM OLD.id
        OR NEW.strategy_id IS DISTINCT FROM OLD.strategy_id
        OR NEW.version_number IS DISTINCT FROM OLD.version_number
        OR NEW.worker_task_id IS DISTINCT FROM OLD.worker_task_id
        OR NEW.idempotency_record_id IS DISTINCT FROM OLD.idempotency_record_id
        OR NEW.name IS DISTINCT FROM OLD.name
        OR NEW.source_code IS DISTINCT FROM OLD.source_code
        OR NEW.code_sha256 IS DISTINCT FROM OLD.code_sha256
        OR NEW.runtime_version IS DISTINCT FROM OLD.runtime_version
        OR NEW.market_type IS DISTINCT FROM OLD.market_type
        OR NEW.instrument_id IS DISTINCT FROM OLD.instrument_id
        OR NEW.symbol IS DISTINCT FROM OLD.symbol
        OR NEW.interval_code IS DISTINCT FROM OLD.interval_code
        OR NEW.lookback_bars IS DISTINCT FROM OLD.lookback_bars
        OR NEW.parameter_schema_json IS DISTINCT FROM OLD.parameter_schema_json
        OR NEW.published_by_user_id IS DISTINCT FROM OLD.published_by_user_id
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
        OR (NEW.status = 'published' AND NEW.published_at IS NULL)
        OR (NEW.status = 'failed' AND NEW.published_at IS NOT NULL) THEN
        RAISE EXCEPTION 'strategy version mutation is not allowed';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER strategy_versions_immutable
BEFORE UPDATE OR DELETE ON strategy_versions
FOR EACH ROW EXECUTE FUNCTION reject_strategy_version_mutation();

CREATE TABLE task_definition_configs (
    id BIGSERIAL PRIMARY KEY,
    task_definition_code VARCHAR(120),
    parameter_overrides_json TEXT,
    updated_by BIGINT,
    created_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX idx_task_definition_configs_task_definition_code
    ON task_definition_configs (task_definition_code);

UPDATE menus
SET name = 'TaskDefinitions',
    path = 'task-definition',
    permission_code = 'scheduler.task_definitions.view',
    component = '/scheduler/task-definition',
    title = '任务定义',
    updated_at = CURRENT_TIMESTAMP
WHERE name = 'NodeDefinitions';

UPDATE i18n_texts
SET text = CASE locale WHEN 'zh' THEN '任务定义' ELSE 'Task Definitions' END,
    updated_at = CURRENT_TIMESTAMP
WHERE biz_type = 'menu'
  AND biz_id IN (SELECT id FROM menus WHERE name = 'TaskDefinitions')
  AND locale IN ('zh', 'en');

-- 旧版本应用启动后会重新 seed 内置工作流与按钮；已删除的执行历史不可恢复。

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

LOCK TABLE market_workflow_subscriptions, market_sync_settings IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE workflow_market_sync_down_guard (
    violations BIGINT NOT NULL CHECK (violations = 0)
) ON COMMIT DROP;

INSERT INTO workflow_market_sync_down_guard (violations)
SELECT
    (SELECT COUNT(*) FROM market_workflow_subscriptions)
    + CASE WHEN (
        SELECT COUNT(*)
        FROM market_sync_settings
        WHERE id = 1
          AND venue = 'binance'
          AND market_types = '["spot","usd_m"]'::jsonb
          AND quote_assets = '["USDT","USDC"]'::jsonb
          AND updated_by_user_id IS NULL
    ) = 1 THEN 0 ELSE 1 END;

DROP TABLE market_workflow_subscriptions;
DROP TABLE market_sync_settings;


LOCK TABLE
    trading_accounts,
    trading_intents,
    strategy_instances,
    testnet_orders
IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE m4_usdm_live_auto_down_guard (
    row_count BIGINT NOT NULL CHECK (row_count = 0)
) ON COMMIT DROP;

INSERT INTO m4_usdm_live_auto_down_guard (row_count)
SELECT
    (SELECT COUNT(*) FROM trading_accounts
     WHERE environment = 'live' AND market_type = 'usd_m'
       AND (automation_enabled OR automation_authorized_at IS NOT NULL OR auto_authorized_at IS NOT NULL))
    + (SELECT COUNT(*) FROM trading_intents
       WHERE environment = 'live' AND market_type = 'usd_m' AND mode = 'auto')
    + (SELECT COUNT(*) FROM strategy_instances AS instance
       JOIN trading_accounts AS account ON account.id = instance.trading_account_id
       WHERE instance.environment = 'live' AND instance.mode = 'auto'
         AND account.market_type = 'usd_m');

DROP INDEX ix_trading_intents_live_runnable;
CREATE INDEX ix_trading_intents_live_runnable
    ON trading_intents (created_at, id)
    WHERE environment = 'live'
      AND ((market_type = 'spot' AND mode IN ('manual', 'auto'))
           OR (market_type = 'usd_m' AND mode = 'manual'))
      AND status IN ('pending', 'reconciling');

ALTER TABLE trading_intents
    DROP CONSTRAINT ck_trading_intents_usdm_live_auto,
    ADD CONSTRAINT ck_trading_intents_usdm_live_manual CHECK (
        environment <> 'live'
        OR (market_type = 'spot' AND mode IN ('manual', 'auto'))
        OR (market_type = 'usd_m' AND mode = 'manual')
    );

ALTER TABLE trading_accounts
    DROP CONSTRAINT ck_trading_accounts_usdm_live_auto,
    ADD CONSTRAINT ck_trading_accounts_usdm_live_manual CHECK (
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
        OR (
            market_type = 'usd_m'
            AND NOT automation_enabled
            AND automation_authorized_at IS NULL
            AND automation_authorized_by_user_id IS NULL
            AND auto_authorized_at IS NULL
            AND auto_authorized_by_user_id IS NULL
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
       OR (account_environment = 'live' AND NOT (
            (account_market = 'spot' AND intent_mode IN ('manual', 'auto'))
            OR (account_market = 'usd_m' AND intent_mode = 'manual')
       ))
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


LOCK TABLE
    trading_accounts,
    trading_intents,
    trading_account_credentials,
    testnet_positions,
    testnet_orders,
    testnet_trade_facts
IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE m4_usdm_live_manual_down_guard (
    row_count BIGINT NOT NULL CHECK (row_count = 0)
) ON COMMIT DROP;

INSERT INTO m4_usdm_live_manual_down_guard (row_count)
SELECT COUNT(*) FROM trading_accounts
WHERE environment = 'live' AND market_type = 'usd_m';

ALTER TABLE testnet_positions
    DROP CONSTRAINT ck_testnet_positions_live_risk_shape,
    DROP CONSTRAINT ck_testnet_positions_live_risk_values,
    DROP COLUMN isolated,
    DROP COLUMN leverage,
    DROP COLUMN liquidation_distance_ratio,
    DROP COLUMN liquidation_price,
    DROP COLUMN mark_price;

DROP INDEX ix_trading_intents_live_runnable;
CREATE INDEX ix_trading_intents_live_runnable
    ON trading_intents (created_at, id)
    WHERE environment = 'live' AND market_type = 'spot' AND mode IN ('manual', 'auto')
      AND status IN ('pending', 'reconciling');

ALTER TABLE trading_intents
    DROP CONSTRAINT ck_trading_intents_usdm_live_manual,
    ADD CONSTRAINT ck_trading_intents_spot_live_auto CHECK (
        environment <> 'live' OR (market_type = 'spot' AND mode IN ('manual', 'auto'))
    );

ALTER TABLE trading_accounts
    DROP CONSTRAINT ck_trading_accounts_usdm_live_manual,
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

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION validate_testnet_trading_credential() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
    account_environment VARCHAR(16);
    account_market VARCHAR(16);
    account_owner BIGINT;
BEGIN
    SELECT environment, market_type, owner_user_id
    INTO account_environment, account_market, account_owner
    FROM trading_accounts WHERE id = NEW.account_id;
    IF account_environment NOT IN ('testnet', 'live')
       OR (account_environment = 'live' AND account_market <> 'spot') THEN
        RAISE EXCEPTION 'trading credentials require an enabled private account shape';
    END IF;
    IF account_owner IS DISTINCT FROM NEW.owner_user_id THEN
        RAISE EXCEPTION 'trading credential owner does not match account owner';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

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

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION validate_testnet_trade_fact_binding() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
    order_account UUID;
    order_credential TIMESTAMPTZ;
    order_intent UUID;
    order_instrument UUID;
    intent_account UUID;
    intent_instrument UUID;
    intent_environment VARCHAR(16);
    account_environment VARCHAR(16);
    account_market VARCHAR(16);
    instrument_symbol VARCHAR(64);
    instrument_market VARCHAR(16);
    instrument_venue VARCHAR(16);
BEGIN
    SELECT environment, market_type INTO account_environment, account_market
    FROM trading_accounts WHERE id = NEW.account_id;

    IF account_environment NOT IN ('testnet', 'live')
       OR (account_environment = 'live' AND account_market <> 'spot') THEN
        RAISE EXCEPTION 'private trade fact account environment is invalid';
    END IF;
    IF NEW.event_type = 'funding' AND account_market IS DISTINCT FROM 'usd_m' THEN
        RAISE EXCEPTION 'private funding fact account market is invalid';
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
               WHERE account_id = NEW.account_id AND instrument_id = NEW.instrument_id
           ) THEN
            RAISE EXCEPTION 'private trade fact instrument binding is invalid';
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
            RAISE EXCEPTION 'private trade fact order binding is invalid';
        END IF;
    END IF;

    IF NEW.intent_id IS NOT NULL THEN
        SELECT account_id, instrument_id, environment
        INTO intent_account, intent_instrument, intent_environment
        FROM trading_intents WHERE id = NEW.intent_id;
        IF intent_account IS DISTINCT FROM NEW.account_id
           OR intent_instrument IS DISTINCT FROM NEW.instrument_id
           OR intent_environment IS DISTINCT FROM account_environment THEN
            RAISE EXCEPTION 'private trade fact intent binding is invalid';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd


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


LOCK TABLE
    trading_accounts,
    trading_intents,
    strategy_instances,
    trading_account_credentials,
    testnet_orders,
    testnet_trade_facts
IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE m4_spot_live_manual_down_guard (
    row_count BIGINT NOT NULL CHECK (row_count = 0)
) ON COMMIT DROP;

INSERT INTO m4_spot_live_manual_down_guard (row_count)
SELECT
    (SELECT COUNT(*) FROM trading_accounts WHERE environment = 'live')
    + (SELECT COUNT(*) FROM trading_intents WHERE environment = 'live')
    + (SELECT COUNT(*) FROM strategy_instances
       WHERE environment = 'live' AND (trading_account_id IS NOT NULL OR stop_loss_ratio IS NOT NULL));

DROP INDEX ix_trading_intents_live_runnable;
DROP INDEX uq_trading_intents_live_account_active;

ALTER TABLE strategy_instances
    DROP CONSTRAINT ck_strategy_instances_spot_live_manual;

ALTER TABLE trading_intents
    DROP CONSTRAINT ck_trading_intents_spot_live_manual,
    DROP CONSTRAINT ck_trading_intents_environment,
    ADD CONSTRAINT ck_trading_intents_environment CHECK (environment IN ('paper', 'testnet'));

DROP TRIGGER trading_accounts_preserve_credential_binding ON trading_accounts;

ALTER TABLE trading_accounts
    DROP CONSTRAINT ck_trading_accounts_spot_live_manual,
    DROP CONSTRAINT ck_trading_accounts_manual_authorization,
    DROP CONSTRAINT fk_trading_accounts_manual_authorized_by,
    DROP COLUMN manual_authorized_by_user_id,
    DROP COLUMN manual_authorized_at,
    DROP CONSTRAINT ck_trading_accounts_environment,
    ADD CONSTRAINT ck_trading_accounts_environment CHECK (environment IN ('paper', 'testnet'));

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION validate_testnet_trading_credential() RETURNS TRIGGER
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

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION preserve_testnet_trading_credential_account() RETURNS TRIGGER
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

-- Testnet projections retain stricter environment predicates while sharing
-- the same order, protection and append-only fact shapes.
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
    WHERE account_id = NEW.account_id AND status = 'configured' AND verification_status = 'verified';

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
                NEW.order_type <> 'stop_loss' OR NEW.close_position OR NEW.quantity <= 0 OR NEW.working_type <> ''
            )) OR (account_market = 'usd_m' AND (
                NEW.order_type <> 'stop_market' OR NOT NEW.close_position OR NEW.quantity <> 0 OR NEW.working_type <> 'mark_price'
            ))) THEN
        RAISE EXCEPTION 'testnet protection order shape does not match account market';
    END IF;
    IF NEW.purpose = 'rebalance' AND account_market = 'spot' AND NEW.reduce_only THEN
        RAISE EXCEPTION 'testnet Spot rebalance cannot set reduceOnly';
    END IF;
    IF NEW.purpose = 'flatten' AND NEW.reduce_only IS DISTINCT FROM (account_market = 'usd_m') THEN
        RAISE EXCEPTION 'testnet flatten order shape does not match account market';
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

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION validate_testnet_trade_fact_binding() RETURNS TRIGGER
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
    SELECT market_type INTO account_market FROM trading_accounts WHERE id = NEW.account_id;
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
               WHERE account_id = NEW.account_id AND instrument_id = NEW.instrument_id
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

LOCK TABLE testnet_orders IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE m3_testnet_external_order_recovery_down_guard (
    row_count BIGINT NOT NULL CHECK (row_count = 0)
) ON COMMIT DROP;

INSERT INTO m3_testnet_external_order_recovery_down_guard (row_count)
SELECT COUNT(*) FROM testnet_orders WHERE recovered_at IS NOT NULL;

ALTER TABLE testnet_orders
    DROP CONSTRAINT ck_testnet_orders_recovered_at,
    DROP COLUMN recovered_at;

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

LOCK TABLE testnet_open_orders IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE m3_testnet_open_order_shape_down_guard (
    row_count BIGINT NOT NULL CHECK (row_count = 0)
) ON COMMIT DROP;

INSERT INTO m3_testnet_open_order_shape_down_guard (row_count)
SELECT COUNT(*)
FROM testnet_open_orders
WHERE close_position OR reduce_only OR working_type <> '' OR original_quantity <= 0;

ALTER TABLE testnet_open_orders
    DROP CONSTRAINT ck_testnet_open_orders_values,
    DROP COLUMN working_type,
    DROP COLUMN reduce_only,
    DROP COLUMN close_position,
    ADD CONSTRAINT ck_testnet_open_orders_values CHECK (
        price >= 0 AND stop_price >= 0
        AND original_quantity > 0
        AND executed_quantity >= 0 AND executed_quantity <= original_quantity
    );

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

LOCK TABLE
    strategy_instances,
    trading_controls,
    trading_accounts,
    trading_account_instruments,
    trading_intents,
    paper_orders,
    trading_events,
    paper_positions,
    paper_balances
IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE m2_paper_executor_down_guard (
    row_count BIGINT NOT NULL CHECK (row_count = 0)
) ON COMMIT DROP;

INSERT INTO m2_paper_executor_down_guard (row_count)
SELECT
    (SELECT COUNT(*) FROM trading_accounts)
    + (SELECT COUNT(*) FROM trading_intents)
    + (SELECT COUNT(*) FROM trading_events)
    + (SELECT COUNT(*) FROM paper_orders)
    + (SELECT COUNT(*) FROM paper_positions)
    + (SELECT COUNT(*) FROM paper_balances)
    + (SELECT COUNT(*) FROM strategy_instances
        WHERE trading_account_id IS NOT NULL OR allocation_usdt IS NOT NULL);

DROP INDEX ix_trading_events_account;
DROP INDEX ix_paper_orders_account;
DROP INDEX ix_trading_intents_owner;
DROP INDEX ix_trading_intents_pending;
DROP INDEX ix_trading_accounts_owner;
DROP TABLE paper_balances;
DROP TABLE paper_positions;
DROP TRIGGER trading_events_append_only ON trading_events;
DROP FUNCTION reject_trading_event_mutation();
DROP TABLE trading_events;
DROP TABLE paper_orders;
DROP TABLE trading_intents;

ALTER TABLE strategy_instances
    DROP CONSTRAINT ck_strategy_instances_allocation,
    DROP CONSTRAINT fk_strategy_instances_trading_account,
    DROP COLUMN allocation_usdt,
    DROP COLUMN trading_account_id;

DROP TABLE trading_account_instruments;
DROP TABLE trading_accounts;
DROP TABLE trading_controls;

DROP INDEX ux_notification_deliveries_signal_channel;

LOCK TABLE strategy_signals, notification_deliveries IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE m2_signal_decisions_down_guard (
    row_count BIGINT NOT NULL CHECK (row_count = 0)
) ON COMMIT DROP;

INSERT INTO m2_signal_decisions_down_guard (row_count)
SELECT
    (SELECT COUNT(*) FROM strategy_signals
        WHERE status IN ('approved', 'rejected')
           OR decision_idempotency_record_id IS NOT NULL
           OR decided_by_user_id IS NOT NULL
           OR decided_at IS NOT NULL)
    + (SELECT COUNT(*) FROM notification_deliveries WHERE strategy_signal_id IS NOT NULL);

DROP INDEX ux_strategy_signals_manual_active_instance;
DROP INDEX ux_notification_deliveries_in_app_signal;
ALTER TABLE notification_deliveries
    DROP CONSTRAINT ck_notification_deliveries_strategy_signal,
    DROP CONSTRAINT fk_notification_deliveries_strategy_signal,
    DROP COLUMN strategy_signal_id;

ALTER TABLE strategy_signals
    DROP CONSTRAINT ck_strategy_signals_decision_state,
    DROP CONSTRAINT ck_strategy_signals_status,
    DROP CONSTRAINT uq_strategy_signals_decision_idempotency,
    DROP CONSTRAINT fk_strategy_signals_decided_by,
    DROP CONSTRAINT fk_strategy_signals_decision_idempotency,
    DROP COLUMN decided_at,
    DROP COLUMN decided_by_user_id,
    DROP COLUMN decision_idempotency_record_id,
    ADD CONSTRAINT ck_strategy_signals_status CHECK (status IN ('active', 'expired'));

LOCK TABLE worker_tasks, strategy_signals, strategy_instances IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE m2_realtime_signals_down_guard (
    row_count BIGINT NOT NULL CHECK (row_count = 0)
) ON COMMIT DROP;

INSERT INTO m2_realtime_signals_down_guard (row_count)
SELECT
    (SELECT COUNT(*) FROM strategy_instances)
    + (SELECT COUNT(*) FROM strategy_signals)
    + (SELECT COUNT(*) FROM worker_tasks WHERE task_type = 'strategy.realtime');

DROP INDEX ux_worker_tasks_type_dedupe;
ALTER TABLE worker_tasks
    DROP CONSTRAINT ck_worker_tasks_realtime_payload,
    DROP CONSTRAINT ck_worker_tasks_realtime_dedupe,
    DROP CONSTRAINT ck_worker_tasks_realtime_lane,
    DROP COLUMN dedupe_key;
DROP TABLE strategy_signals;
DROP TABLE strategy_instances;

LOCK TABLE worker_tasks, backtests, strategy_versions, strategies IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE m1_4_strategy_runtime_down_guard (
    row_count BIGINT NOT NULL CHECK (row_count = 0)
) ON COMMIT DROP;

INSERT INTO m1_4_strategy_runtime_down_guard (row_count)
SELECT
    (SELECT COUNT(*) FROM strategies)
    + (SELECT COUNT(*) FROM strategy_versions)
    + (SELECT COUNT(*) FROM backtests)
    + (SELECT COUNT(*) FROM worker_tasks WHERE task_type IN ('strategy.publish', 'strategy.backtest'));

ALTER TABLE worker_tasks
    DROP CONSTRAINT ck_worker_tasks_quant_payload,
    DROP CONSTRAINT ck_worker_tasks_quant_lane;
DROP TABLE backtests;
DROP TRIGGER strategy_versions_immutable ON strategy_versions;
DROP FUNCTION reject_strategy_version_mutation();
DROP TABLE strategy_versions;
DROP TABLE strategies;

LOCK TABLE worker_tasks IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE m1_4_strategy_backtest_down_guard (
    row_count BIGINT NOT NULL CHECK (row_count = 0)
) ON COMMIT DROP;

INSERT INTO m1_4_strategy_backtest_down_guard (row_count)
SELECT COUNT(*)
FROM worker_tasks
WHERE lane <> 'realtime' OR priority <> 0;

DROP INDEX ix_worker_tasks_lane_claim;
ALTER TABLE worker_tasks
    DROP CONSTRAINT ck_worker_tasks_priority,
    DROP CONSTRAINT ck_worker_tasks_lane,
    DROP COLUMN priority,
    DROP COLUMN lane;

LOCK TABLE watchlist_items IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE m1_market_watchlists_down_guard (
    row_count BIGINT NOT NULL CHECK (row_count = 0)
) ON COMMIT DROP;

INSERT INTO m1_market_watchlists_down_guard (row_count)
SELECT COUNT(*) FROM watchlist_items;

DROP TABLE watchlist_items;

-- Down 先锁定三张行情表，再以空表 guard 保护无损回滚。
LOCK TABLE
    market_candles,
    market_ticker_snapshots,
    market_instruments
IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE a2_market_contract_down_guard (
    row_count BIGINT NOT NULL CHECK (row_count = 0)
) ON COMMIT DROP;

INSERT INTO a2_market_contract_down_guard (row_count)
SELECT
    (SELECT COUNT(*) FROM market_candles)
    + (SELECT COUNT(*) FROM market_ticker_snapshots)
    + (SELECT COUNT(*) FROM market_instruments);

DROP TABLE market_candles;
DROP TABLE market_ticker_snapshots;
DROP TABLE market_instruments;

-- 审计记录不可静默丢弃；只有空表才能回滚本 migration。
LOCK TABLE audit_records IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE a1_observability_down_guard (
    row_count BIGINT NOT NULL CHECK (row_count = 0)
) ON COMMIT DROP;

INSERT INTO a1_observability_down_guard (row_count)
SELECT COUNT(*) FROM audit_records;

DROP TABLE audit_records;

-- 基线只允许在完全空的业务 schema 上回滚；任何数据都会让 guard 失败并原子保留全部表和版本记录。
-- 先锁住全部表再计数，避免并发写入在空表检查后、DROP 前提交而被静默删除。
LOCK TABLE
    news_items,
    workflow_definitions,
    workflow_runtime_states,
    workflow_runtime_entries,
    workflow_executions,
    workflow_execution_attempts,
    workflow_execution_nodes,
    workflow_execution_transitions,
    task_definition_configs,
    domain_event_outbox,
    roles,
    users,
    idempotency_records,
    user_roles,
    menus,
    menu_buttons,
    role_menus,
    role_menu_buttons,
    i18n_texts,
    ai_model_configs,
    assistant_agents,
    ai_model_agent_bindings,
    notification_channels,
    notification_deliveries,
    assistant_sessions,
    assistant_messages,
    worker_tasks
IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE a1_baseline_down_guard (
    row_count BIGINT NOT NULL CHECK (row_count = 0)
) ON COMMIT DROP;

INSERT INTO a1_baseline_down_guard (row_count)
SELECT
    (SELECT COUNT(*) FROM news_items)
    + (SELECT COUNT(*) FROM workflow_definitions)
    + (SELECT COUNT(*) FROM workflow_runtime_states)
    + (SELECT COUNT(*) FROM workflow_runtime_entries)
    + (SELECT COUNT(*) FROM workflow_executions)
    + (SELECT COUNT(*) FROM workflow_execution_attempts)
    + (SELECT COUNT(*) FROM workflow_execution_nodes)
    + (SELECT COUNT(*) FROM workflow_execution_transitions)
    + (SELECT COUNT(*) FROM task_definition_configs)
    + (SELECT COUNT(*) FROM domain_event_outbox)
    + (SELECT COUNT(*) FROM roles)
    + (SELECT COUNT(*) FROM users)
    + (SELECT COUNT(*) FROM idempotency_records)
    + (SELECT COUNT(*) FROM user_roles)
    + (SELECT COUNT(*) FROM menus)
    + (SELECT COUNT(*) FROM menu_buttons)
    + (SELECT COUNT(*) FROM role_menus)
    + (SELECT COUNT(*) FROM role_menu_buttons)
    + (SELECT COUNT(*) FROM i18n_texts)
    + (SELECT COUNT(*) FROM ai_model_configs)
    + (SELECT COUNT(*) FROM assistant_agents)
    + (SELECT COUNT(*) FROM ai_model_agent_bindings)
    + (SELECT COUNT(*) FROM notification_channels)
    + (SELECT COUNT(*) FROM notification_deliveries)
    + (SELECT COUNT(*) FROM assistant_sessions)
    + (SELECT COUNT(*) FROM assistant_messages)
    + (SELECT COUNT(*) FROM worker_tasks);

DROP TABLE worker_tasks;
DROP TABLE assistant_messages;
DROP TABLE assistant_sessions;
DROP TABLE notification_deliveries;
DROP TABLE notification_channels;
DROP TABLE ai_model_agent_bindings;
DROP TABLE assistant_agents;
DROP TABLE ai_model_configs;
DROP TABLE i18n_texts;
DROP TABLE role_menu_buttons;
DROP TABLE role_menus;
DROP TABLE menu_buttons;
DROP TABLE menus;
DROP TABLE user_roles;
DROP TABLE idempotency_records;
DROP TABLE users;
DROP TABLE roles;
DROP TABLE domain_event_outbox;
DROP TABLE task_definition_configs;
DROP TABLE workflow_execution_transitions;
DROP TABLE workflow_execution_nodes;
DROP TABLE workflow_execution_attempts;
DROP TABLE workflow_executions;
DROP TABLE workflow_runtime_entries;
DROP TABLE workflow_runtime_states;
DROP TABLE workflow_definitions;
DROP TABLE news_items;
