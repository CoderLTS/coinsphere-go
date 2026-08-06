-- +goose Up
-- A1 以未投产系统的当前模型建立唯一 PostgreSQL 空库基线，不承接旧方言或旧数据升级。
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
CREATE UNIQUE INDEX idx_menus_permission_code ON menus (permission_code);
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

-- +goose Down
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
