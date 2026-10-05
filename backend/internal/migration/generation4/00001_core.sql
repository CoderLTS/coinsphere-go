-- +goose Up
-- Generation 4 starts in a separate empty PostgreSQL 16 database.
-- +goose StatementBegin
DO $$ BEGIN IF current_setting('server_version_num')::integer / 10000 <> 16 THEN RAISE EXCEPTION 'PostgreSQL 16 is required'; END IF; END $$;
-- +goose StatementEnd
CREATE TABLE roles (
    id BIGSERIAL PRIMARY KEY,
    display_name VARCHAR(100) NOT NULL DEFAULT '',
    code VARCHAR(50) NOT NULL,
    description VARCHAR(255) NOT NULL DEFAULT '',
    is_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    is_system BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_roles_code CHECK (code <> '')
);
CREATE UNIQUE INDEX idx_roles_code ON roles (code);

CREATE TABLE users (
    id BIGSERIAL PRIMARY KEY,
    username VARCHAR(100) NOT NULL,
    password_hash VARCHAR(255) NOT NULL DEFAULT '',
    nickname VARCHAR(100) NOT NULL DEFAULT '',
    full_name VARCHAR(100) NOT NULL DEFAULT '',
    gender VARCHAR(20) NOT NULL DEFAULT 'unknown',
    phone VARCHAR(32) NOT NULL DEFAULT '',
    email VARCHAR(150) NOT NULL DEFAULT '',
    avatar VARCHAR(500) NOT NULL DEFAULT '',
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    job_title VARCHAR(100) NOT NULL DEFAULT '',
    location VARCHAR(120) NOT NULL DEFAULT '',
    company VARCHAR(120) NOT NULL DEFAULT '',
    bio TEXT NOT NULL DEFAULT '',
    tags_json TEXT NOT NULL DEFAULT '',
    created_by VARCHAR(100) NOT NULL DEFAULT 'system',
    updated_by VARCHAR(100) NOT NULL DEFAULT 'system',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_login_at TIMESTAMPTZ,
    CONSTRAINT ck_users_username CHECK (username <> '')
);
CREATE UNIQUE INDEX idx_users_username ON users (username);

CREATE TABLE user_roles (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role_id BIGINT NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX ux_user_role ON user_roles (user_id, role_id);

CREATE TABLE menus (
    id BIGSERIAL PRIMARY KEY,
    parent_id BIGINT REFERENCES menus (id) ON DELETE CASCADE,
    path VARCHAR(255) NOT NULL DEFAULT '',
    name VARCHAR(100) NOT NULL,
    permission_code VARCHAR(120),
    component VARCHAR(255) NOT NULL DEFAULT '',
    title VARCHAR(100) NOT NULL DEFAULT '',
    icon VARCHAR(100) NOT NULL DEFAULT '',
    menu_type VARCHAR(20) NOT NULL DEFAULT 'menu',
    external_url VARCHAR(500) NOT NULL DEFAULT '',
    active_menu_path VARCHAR(255) NOT NULL DEFAULT '',
    sort BIGINT NOT NULL DEFAULT 0,
    keep_alive BOOLEAN NOT NULL DEFAULT FALSE,
    is_hidden BOOLEAN NOT NULL DEFAULT FALSE,
    is_hide_tab BOOLEAN NOT NULL DEFAULT FALSE,
    is_full_screen BOOLEAN NOT NULL DEFAULT FALSE,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    use_iframe BOOLEAN NOT NULL DEFAULT FALSE,
    fixed_tab BOOLEAN NOT NULL DEFAULT FALSE,
    badge_label VARCHAR(50) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_menus_name CHECK (name <> '')
);
CREATE UNIQUE INDEX idx_menus_name ON menus (name);
CREATE INDEX idx_menus_permission_code ON menus (permission_code);

CREATE TABLE menu_buttons (
    id BIGSERIAL PRIMARY KEY,
    menu_id BIGINT NOT NULL REFERENCES menus (id) ON DELETE CASCADE,
    title VARCHAR(100) NOT NULL DEFAULT '',
    permission_code VARCHAR(120) NOT NULL,
    sort BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_menu_buttons_permission_code CHECK (permission_code <> '')
);
CREATE UNIQUE INDEX idx_menu_buttons_permission_code ON menu_buttons (permission_code);

CREATE TABLE role_menus (
    id BIGSERIAL PRIMARY KEY,
    role_id BIGINT NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
    menu_id BIGINT NOT NULL REFERENCES menus (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX ux_role_menu ON role_menus (role_id, menu_id);

CREATE TABLE role_menu_buttons (
    id BIGSERIAL PRIMARY KEY,
    role_id BIGINT NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
    button_id BIGINT NOT NULL REFERENCES menu_buttons (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX ux_role_button ON role_menu_buttons (role_id, button_id);

CREATE TABLE i18n_texts (
    id BIGSERIAL PRIMARY KEY,
    biz_type VARCHAR(20) NOT NULL,
    biz_id BIGINT NOT NULL,
    i18n_key VARCHAR(255) NOT NULL,
    locale VARCHAR(10) NOT NULL,
    text VARCHAR(255) NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_i18n_texts_biz_type CHECK (biz_type IN ('menu', 'button')),
    CONSTRAINT ck_i18n_texts_locale CHECK (locale IN ('zh', 'en'))
);
CREATE UNIQUE INDEX ux_i18n_key_locale ON i18n_texts (i18n_key, locale);
CREATE UNIQUE INDEX ux_i18n_biz ON i18n_texts (biz_type, biz_id, locale);

CREATE TABLE audit_records (
    id BIGSERIAL PRIMARY KEY,
    request_id VARCHAR(64) NOT NULL,
    actor_user_id BIGINT REFERENCES users (id) ON DELETE SET NULL,
    action VARCHAR(255) NOT NULL,
    resource_path VARCHAR(500) NOT NULL,
    outcome VARCHAR(16) NOT NULL,
    status_code INTEGER NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_audit_records_request_id CHECK (request_id ~ '^[A-Za-z0-9._-]{1,64}$'),
    CONSTRAINT ck_audit_records_outcome CHECK (outcome IN ('success', 'failure')),
    CONSTRAINT ck_audit_records_status_code CHECK (status_code BETWEEN 100 AND 599)
);
CREATE INDEX ix_audit_records_created_at ON audit_records (created_at DESC, id DESC);
CREATE INDEX ix_audit_records_actor_created_at ON audit_records (actor_user_id, created_at DESC, id DESC);
CREATE INDEX ix_audit_records_request_id ON audit_records (request_id);

CREATE TABLE plugin_installations (
    plugin_id VARCHAR(160) PRIMARY KEY,
    version VARCHAR(32) NOT NULL,
    schema_name VARCHAR(63) NOT NULL UNIQUE,
    source_path TEXT NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'installed',
    installed_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_plugin_installations_status CHECK (status IN ('installed', 'uninstalled')),
    CONSTRAINT ck_plugin_installations_id CHECK (plugin_id <> ''),
    CONSTRAINT ck_plugin_installations_schema CHECK (schema_name <> '')
);

CREATE TABLE plugin_references (
    id BIGSERIAL PRIMARY KEY,
    plugin_id VARCHAR(160) NOT NULL REFERENCES plugin_installations (plugin_id) ON DELETE CASCADE,
    reference_type VARCHAR(64) NOT NULL,
    reference_id VARCHAR(160) NOT NULL,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_plugin_references_type CHECK (reference_type <> ''),
    CONSTRAINT ck_plugin_references_id CHECK (reference_id <> '')
);
CREATE UNIQUE INDEX ux_plugin_references_identity ON plugin_references (plugin_id, reference_type, reference_id);
CREATE INDEX ix_plugin_references_active ON plugin_references (plugin_id, active);

-- +goose StatementBegin
CREATE FUNCTION enforce_installed_plugin_reference() RETURNS TRIGGER AS $$
DECLARE
    plugin_status VARCHAR(16);
BEGIN
    IF NEW.active THEN
        SELECT status INTO plugin_status
        FROM plugin_installations
        WHERE plugin_id = NEW.plugin_id
        FOR SHARE;
        IF plugin_status IS DISTINCT FROM 'installed' THEN
            RAISE EXCEPTION 'active plugin references require an installed plugin';
        END IF;
    END IF;
    RETURN NEW;
END
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER trg_plugin_references_installed
BEFORE INSERT OR UPDATE OF plugin_id, active ON plugin_references
FOR EACH ROW EXECUTE FUNCTION enforce_installed_plugin_reference();

CREATE TABLE workflow_groups (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(80) NOT NULL,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_workflow_groups_name CHECK (BTRIM(name) <> ''),
    CONSTRAINT ck_workflow_groups_sort_order CHECK (sort_order >= 0)
);

CREATE UNIQUE INDEX ux_workflow_groups_name_ci ON workflow_groups (LOWER(name));
CREATE INDEX ix_workflow_groups_order ON workflow_groups (sort_order, id);


CREATE TABLE workflows (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(120) NOT NULL,
    description VARCHAR(500) NOT NULL DEFAULT '',
    mode VARCHAR(16) NOT NULL DEFAULT 'batch',
    status VARCHAR(16) NOT NULL DEFAULT 'inactive',
    draft_revision_id BIGINT,
    published_revision_id BIGINT,
    group_id BIGINT REFERENCES workflow_groups(id) ON DELETE SET NULL,
    owner_user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    main_trigger_node_id VARCHAR(128) NOT NULL,
    retention_days INTEGER NOT NULL DEFAULT 30,
    created_by BIGINT NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_workflows_name CHECK (BTRIM(name) <> ''),
    CONSTRAINT ck_workflows_mode CHECK (mode IN ('batch', 'event', 'stream')),
    CONSTRAINT ck_workflows_status CHECK (status IN ('inactive', 'active', 'error')),
    CONSTRAINT ck_workflows_trigger CHECK (BTRIM(main_trigger_node_id) <> ''),
    CONSTRAINT ck_workflows_retention CHECK (retention_days BETWEEN 1 AND 3650)
);
CREATE INDEX ix_workflows_status_updated ON workflows (status, updated_at DESC, id DESC);

CREATE TABLE workflow_revisions (
    id BIGSERIAL PRIMARY KEY,
    workflow_id BIGINT NOT NULL REFERENCES workflows (id) ON DELETE RESTRICT,
    revision_number BIGINT NOT NULL,
    graph_json JSONB NOT NULL,
    node_versions JSONB NOT NULL,
    main_trigger_node_id VARCHAR(128) NOT NULL,
    created_by BIGINT NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_workflow_revisions_number CHECK (revision_number > 0),
    CONSTRAINT ck_workflow_revisions_graph CHECK (jsonb_typeof(graph_json) = 'object' AND graph_json->>'schemaVersion'='3' AND jsonb_typeof(graph_json->'entryPoints')='object'),
    CONSTRAINT ck_workflow_revisions_versions CHECK (jsonb_typeof(node_versions) = 'object'),
    CONSTRAINT ck_workflow_revisions_trigger CHECK (BTRIM(main_trigger_node_id) <> ''),
    CONSTRAINT ux_workflow_revision_identity UNIQUE (workflow_id, id),
    CONSTRAINT ux_workflow_revision_number UNIQUE (workflow_id, revision_number)
);
CREATE INDEX ix_workflow_revisions_created ON workflow_revisions (workflow_id, created_at DESC, id DESC);

ALTER TABLE workflows
    ADD CONSTRAINT fk_workflows_draft_revision
    FOREIGN KEY (id, draft_revision_id)
    REFERENCES workflow_revisions (workflow_id, id)
    DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE workflow_runtimes (
    workflow_id BIGINT PRIMARY KEY REFERENCES workflows (id) ON DELETE CASCADE,
    max_concurrent_runs INTEGER NOT NULL DEFAULT 2,
    backlog_limit INTEGER NOT NULL DEFAULT 100,
    next_scheduled_at TIMESTAMPTZ,
    trigger_lease_token VARCHAR(64),
    trigger_lease_expires_at TIMESTAMPTZ,
    CONSTRAINT ck_trigger_lease_pair CHECK ((trigger_lease_token IS NULL)=(trigger_lease_expires_at IS NULL)),
    last_scheduled_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_workflow_runtimes_concurrency CHECK (max_concurrent_runs BETWEEN 1 AND 32),
    CONSTRAINT ck_workflow_runtimes_backlog CHECK (backlog_limit BETWEEN 1 AND 10000)
);


CREATE TABLE workflow_secret_bindings (
    revision_id BIGINT NOT NULL,
    workflow_id BIGINT NOT NULL,
    node_instance_id VARCHAR(128) NOT NULL,
    field_name VARCHAR(128) NOT NULL,
    encrypted_value TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (revision_id, node_instance_id, field_name),
    CONSTRAINT fk_workflow_secret_binding_revision
        FOREIGN KEY (workflow_id, revision_id)
        REFERENCES workflow_revisions (workflow_id, id)
        ON DELETE RESTRICT,
    CONSTRAINT ck_workflow_secret_binding_node CHECK (BTRIM(node_instance_id) <> ''),
    CONSTRAINT ck_workflow_secret_binding_field CHECK (BTRIM(field_name) <> ''),
    CONSTRAINT ck_workflow_secret_binding_value CHECK (BTRIM(encrypted_value) <> '')
);
CREATE INDEX ix_workflow_secret_bindings_workflow ON workflow_secret_bindings (workflow_id, revision_id);


CREATE TABLE workflow_event_records (
    id BIGSERIAL PRIMARY KEY,
    source VARCHAR(500) NOT NULL,
    event_id VARCHAR(128) NOT NULL,
    spec_version VARCHAR(8) NOT NULL,
    event_type VARCHAR(255) NOT NULL,
    subject VARCHAR(500) NOT NULL DEFAULT '',
    event_time TIMESTAMPTZ NOT NULL,
    data_content_type VARCHAR(128) NOT NULL DEFAULT 'application/json',
    partition_key VARCHAR(256) NOT NULL,
    event_json JSONB NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ux_workflow_event_identity UNIQUE (source, event_id),
    CONSTRAINT ck_workflow_event_version CHECK (spec_version = '1.0'),
    CONSTRAINT ck_workflow_event_identity CHECK (BTRIM(source) <> '' AND BTRIM(event_id) <> ''),
    CONSTRAINT ck_workflow_event_type CHECK (BTRIM(event_type) <> ''),
    CONSTRAINT ck_workflow_event_partition CHECK (BTRIM(partition_key) <> ''),
    CONSTRAINT ck_workflow_event_json CHECK (jsonb_typeof(event_json) = 'object')
);
CREATE INDEX ix_workflow_event_records_received ON workflow_event_records (received_at, id);
CREATE INDEX ix_workflow_event_records_partition ON workflow_event_records (partition_key, id);

CREATE TABLE workflow_runs (
    id BIGSERIAL PRIMARY KEY,
    workflow_id BIGINT NOT NULL REFERENCES workflows (id) ON DELETE RESTRICT,
    revision_id BIGINT NOT NULL,
    execution_user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    requires_serial BOOLEAN NOT NULL DEFAULT FALSE,
    entry_point VARCHAR(32) NOT NULL,
    input_json JSONB NOT NULL DEFAULT '{}',
    CONSTRAINT ck_run_input CHECK (jsonb_typeof(input_json)='object'),
    CONSTRAINT ck_run_entry CHECK (entry_point ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$'),
    trigger_type VARCHAR(16) NOT NULL,
    trigger_key VARCHAR(128) NOT NULL,
    event_record_id BIGINT REFERENCES workflow_event_records (id) ON DELETE RESTRICT,
    partition_key VARCHAR(256) NOT NULL DEFAULT '',
    diagnostic BOOLEAN NOT NULL DEFAULT FALSE,
    original_run_id BIGINT REFERENCES workflow_runs (id) ON DELETE RESTRICT,
    status VARCHAR(16) NOT NULL DEFAULT 'queued',
    current_node_instance_id VARCHAR(128),
    not_before TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    lease_token VARCHAR(64),
    lease_expires_at TIMESTAMPTZ,
    cancel_requested_at TIMESTAMPTZ,
    triggered_at TIMESTAMPTZ NOT NULL,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_by BIGINT REFERENCES users (id) ON DELETE RESTRICT,
    result_summary JSONB NOT NULL DEFAULT '{}'::jsonb,
    error_category VARCHAR(32),
    error_message VARCHAR(1000),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_workflow_run_revision
        FOREIGN KEY (workflow_id, revision_id)
        REFERENCES workflow_revisions (workflow_id, id)
        ON DELETE RESTRICT,
    CONSTRAINT ux_workflow_run_trigger UNIQUE (workflow_id, trigger_type, trigger_key),
    CONSTRAINT ck_workflow_runs_trigger CHECK (trigger_type IN ('manual', 'schedule', 'event', 'stream', 'webhook', 'failure')),
    CONSTRAINT ck_workflow_runs_trigger_key CHECK (BTRIM(trigger_key) <> ''),
    CONSTRAINT ck_workflow_runs_status CHECK (status IN ('queued', 'running', 'waiting', 'retrying', 'succeeded', 'failed', 'cancelled')),
    CONSTRAINT ck_workflow_runs_event CHECK ((trigger_type IN ('event', 'stream', 'webhook', 'failure')) = (event_record_id IS NOT NULL)),
    CONSTRAINT ck_workflow_runs_diagnostic CHECK (diagnostic = (original_run_id IS NOT NULL)),
    CONSTRAINT ck_workflow_runs_lease CHECK (
        (status = 'running' AND lease_token IS NOT NULL AND lease_expires_at IS NOT NULL)
        OR (status <> 'running' AND lease_token IS NULL AND lease_expires_at IS NULL)
    ),
    CONSTRAINT ck_workflow_runs_completion CHECK (
        (status IN ('succeeded', 'failed', 'cancelled')) = (completed_at IS NOT NULL)
    ),
    CONSTRAINT ck_workflow_runs_summary CHECK (jsonb_typeof(result_summary) = 'object')
);
CREATE INDEX ix_workflow_runs_queue ON workflow_runs (status, not_before, created_at, id);
CREATE INDEX ix_workflow_runs_workflow ON workflow_runs (workflow_id, triggered_at DESC, id DESC);
CREATE INDEX ix_workflow_runs_lease ON workflow_runs (lease_expires_at) WHERE status = 'running';
CREATE INDEX ix_workflow_runs_partition ON workflow_runs (workflow_id, partition_key, created_at, id)
    WHERE partition_key <> '' AND status IN ('queued', 'running', 'waiting', 'retrying');

CREATE TABLE workflow_run_nodes (
    id BIGSERIAL PRIMARY KEY,
    run_id BIGINT NOT NULL REFERENCES workflow_runs (id) ON DELETE RESTRICT,
    node_instance_id VARCHAR(128) NOT NULL,
    node_type VARCHAR(128) NOT NULL,
    node_version VARCHAR(32) NOT NULL,
    invocation_started BOOLEAN NOT NULL DEFAULT FALSE,
    execution_pool VARCHAR(16) NOT NULL,
    attempt INTEGER NOT NULL,
    loop_iteration INTEGER NOT NULL DEFAULT 0,
    operation_key CHAR(64) NOT NULL,
    status VARCHAR(16) NOT NULL,
    input_summary JSONB NOT NULL DEFAULT '{}'::jsonb,
    output_summary JSONB NOT NULL DEFAULT '{}'::jsonb,
    error_category VARCHAR(32),
    error_message VARCHAR(1000),
    started_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    duration_ms BIGINT,
    CONSTRAINT ux_workflow_run_node_attempt UNIQUE (run_id, node_instance_id, loop_iteration, attempt),
    CONSTRAINT ck_workflow_run_nodes_identity CHECK (BTRIM(node_instance_id) <> '' AND BTRIM(node_type) <> ''),
    CONSTRAINT ck_workflow_run_nodes_pool CHECK (execution_pool IN ('stream', 'compute')),
    CONSTRAINT ck_workflow_run_nodes_attempt CHECK (attempt BETWEEN 1 AND 100 AND loop_iteration >= 0),
    CONSTRAINT ck_workflow_run_nodes_operation_key CHECK (operation_key ~ '^[0-9a-f]{64}$'),
    CONSTRAINT ck_workflow_run_nodes_status CHECK (status IN ('running', 'waiting', 'succeeded', 'failed', 'cancelled', 'skipped')),
    CONSTRAINT ck_workflow_run_nodes_summary CHECK (
        jsonb_typeof(input_summary) = 'object' AND jsonb_typeof(output_summary) = 'object'
    ),
    CONSTRAINT ck_workflow_run_nodes_terminal CHECK (
        (status = 'running' AND completed_at IS NULL AND duration_ms IS NULL)
        OR (status <> 'running' AND completed_at IS NOT NULL AND duration_ms IS NOT NULL AND duration_ms >= 0)
    )
);
CREATE INDEX ix_workflow_run_nodes_run ON workflow_run_nodes (run_id, id);
CREATE INDEX ix_workflow_run_nodes_operation ON workflow_run_nodes (operation_key);

CREATE TABLE workflow_node_logs (
    id BIGSERIAL PRIMARY KEY,
    workflow_id BIGINT NOT NULL REFERENCES workflows (id) ON DELETE RESTRICT,
    run_id BIGINT NOT NULL REFERENCES workflow_runs (id) ON DELETE RESTRICT,
    run_node_id BIGINT NOT NULL REFERENCES workflow_run_nodes (id) ON DELETE RESTRICT,
    logged_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    level VARCHAR(8) NOT NULL,
    message VARCHAR(1000) NOT NULL,
    fields_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    CONSTRAINT ck_workflow_node_logs_level CHECK (level IN ('debug', 'info', 'warn', 'error')),
    CONSTRAINT ck_workflow_node_logs_message CHECK (BTRIM(message) <> ''),
    CONSTRAINT ck_workflow_node_logs_fields CHECK (
        jsonb_typeof(fields_json) = 'object' AND OCTET_LENGTH(fields_json::TEXT) <= 4096
    )
);
CREATE INDEX ix_workflow_node_logs_run ON workflow_node_logs (run_id, run_node_id, logged_at, id);
CREATE INDEX ix_workflow_node_logs_workflow ON workflow_node_logs (workflow_id, logged_at DESC, id DESC);

CREATE TABLE workflow_run_checkpoints (
    id BIGSERIAL PRIMARY KEY,
    run_id BIGINT NOT NULL REFERENCES workflow_runs (id) ON DELETE RESTRICT,
    run_node_id BIGINT NOT NULL REFERENCES workflow_run_nodes (id) ON DELETE RESTRICT,
    workflow_id BIGINT NOT NULL REFERENCES workflows (id) ON DELETE RESTRICT,
    revision_id BIGINT NOT NULL,
    node_instance_id VARCHAR(128) NOT NULL,
    loop_iteration INTEGER NOT NULL DEFAULT 0,
    operation_key CHAR(64) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'succeeded',
    output_json JSONB NOT NULL,
    artifacts_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_workflow_run_checkpoint_revision
        FOREIGN KEY (workflow_id, revision_id)
        REFERENCES workflow_revisions (workflow_id, id)
        ON DELETE RESTRICT,
    CONSTRAINT ux_workflow_run_checkpoint_node UNIQUE (run_id, node_instance_id, loop_iteration),
    CONSTRAINT ux_workflow_run_checkpoint_attempt UNIQUE (run_node_id),
    CONSTRAINT ux_workflow_run_checkpoint_operation UNIQUE (operation_key),
    CONSTRAINT ck_workflow_run_checkpoints_node CHECK (BTRIM(node_instance_id) <> '' AND loop_iteration >= 0),
    CONSTRAINT ck_workflow_run_checkpoints_status CHECK (status = 'succeeded'),
    CONSTRAINT ck_workflow_run_checkpoints_output CHECK (jsonb_typeof(output_json) = 'object'),
    CONSTRAINT ck_workflow_run_checkpoints_artifacts CHECK (jsonb_typeof(artifacts_json) = 'array')
);
CREATE INDEX ix_workflow_run_checkpoints_run ON workflow_run_checkpoints (run_id, id);

CREATE TABLE workflow_node_states (
    workflow_id BIGINT NOT NULL REFERENCES workflows (id) ON DELETE RESTRICT,
    node_instance_id VARCHAR(128) NOT NULL,
    node_type VARCHAR(128) NOT NULL,
    revision_id BIGINT NOT NULL,
    state_json JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (workflow_id, revision_id, node_instance_id),
    CONSTRAINT fk_workflow_node_state_revision
        FOREIGN KEY (workflow_id, revision_id)
        REFERENCES workflow_revisions (workflow_id, id)
        ON DELETE RESTRICT,
    CONSTRAINT ck_workflow_node_states_identity CHECK (BTRIM(node_instance_id) <> '' AND BTRIM(node_type) <> '')
);

CREATE TABLE workflow_artifacts (
    sha256 CHAR(64) PRIMARY KEY,
    media_type VARCHAR(255) NOT NULL,
    encoding VARCHAR(16) NOT NULL,
    size_bytes BIGINT NOT NULL,
    stored_size_bytes BIGINT NOT NULL,
    storage_key VARCHAR(160) NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_workflow_artifacts_sha CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT ck_workflow_artifacts_media CHECK (BTRIM(media_type) <> ''),
    CONSTRAINT ck_workflow_artifacts_encoding CHECK (encoding = 'gzip'),
    CONSTRAINT ck_workflow_artifacts_sizes CHECK (size_bytes >= 0 AND stored_size_bytes > 0),
    CONSTRAINT ck_workflow_artifacts_key CHECK (BTRIM(storage_key) <> '')
);

CREATE TABLE workflow_artifact_refs (
    run_node_id BIGINT NOT NULL REFERENCES workflow_run_nodes (id) ON DELETE CASCADE,
    artifact_sha256 CHAR(64) NOT NULL REFERENCES workflow_artifacts (sha256) ON DELETE RESTRICT,
    ordinal INTEGER NOT NULL,
    media_type VARCHAR(255) NOT NULL,
    size_bytes BIGINT NOT NULL,
    PRIMARY KEY (run_node_id, ordinal),
    CONSTRAINT ck_workflow_artifact_refs_ordinal CHECK (ordinal >= 0),
    CONSTRAINT ck_workflow_artifact_refs_media CHECK (BTRIM(media_type) <> ''),
    CONSTRAINT ck_workflow_artifact_refs_size CHECK (size_bytes >= 0)
);
CREATE INDEX ix_workflow_artifact_refs_sha ON workflow_artifact_refs (artifact_sha256);

CREATE TABLE workflow_event_deliveries (
    id BIGSERIAL PRIMARY KEY,
    event_record_id BIGINT NOT NULL REFERENCES workflow_event_records (id) ON DELETE RESTRICT,
    workflow_id BIGINT NOT NULL REFERENCES workflows (id) ON DELETE RESTRICT,
    revision_id BIGINT NOT NULL,
    run_id BIGINT NOT NULL REFERENCES workflow_runs (id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_workflow_event_delivery_revision
        FOREIGN KEY (workflow_id, revision_id)
        REFERENCES workflow_revisions (workflow_id, id)
        ON DELETE RESTRICT,
    CONSTRAINT ux_workflow_event_delivery UNIQUE (event_record_id, workflow_id),
    CONSTRAINT ux_workflow_event_delivery_run UNIQUE (run_id)
);
CREATE INDEX ix_workflow_event_deliveries_workflow ON workflow_event_deliveries (workflow_id, created_at, id);

CREATE TABLE workflow_event_outbox (
    id BIGSERIAL PRIMARY KEY,
    source VARCHAR(500) NOT NULL,
    event_id VARCHAR(128) NOT NULL,
    event_json JSONB NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    attempt_count INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 10,
    available_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    published_at TIMESTAMPTZ,
    last_error_category VARCHAR(32),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ux_workflow_event_outbox_identity UNIQUE (source, event_id),
    CONSTRAINT ck_workflow_event_outbox_identity CHECK (BTRIM(source) <> '' AND BTRIM(event_id) <> ''),
    CONSTRAINT ck_workflow_event_outbox_json CHECK (jsonb_typeof(event_json) = 'object'),
    CONSTRAINT ck_workflow_event_outbox_status CHECK (status IN ('pending', 'published', 'dead_letter')),
    CONSTRAINT ck_workflow_event_outbox_attempts CHECK (attempt_count BETWEEN 0 AND max_attempts AND max_attempts BETWEEN 1 AND 100),
    CONSTRAINT ck_workflow_event_outbox_published CHECK ((status = 'published') = (published_at IS NOT NULL))
);
CREATE INDEX ix_workflow_event_outbox_pending ON workflow_event_outbox (available_at, id)
    WHERE status = 'pending';

CREATE TABLE workflow_human_tasks (
    id BIGSERIAL PRIMARY KEY,
    workflow_id BIGINT NOT NULL REFERENCES workflows (id) ON DELETE RESTRICT,
    run_id BIGINT NOT NULL REFERENCES workflow_runs (id) ON DELETE RESTRICT,
    node_instance_id VARCHAR(128) NOT NULL,
    task_type VARCHAR(64) NOT NULL,
    business_key VARCHAR(256) NOT NULL,
    prompt VARCHAR(500) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    expires_at TIMESTAMPTZ NOT NULL,
    decision_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    decided_by BIGINT REFERENCES users (id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    decided_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ux_workflow_human_task_node UNIQUE (run_id, node_instance_id),
    CONSTRAINT ck_workflow_human_task_identity CHECK (
        BTRIM(node_instance_id) <> '' AND BTRIM(task_type) <> '' AND BTRIM(business_key) <> ''
    ),
    CONSTRAINT ck_workflow_human_task_status CHECK (status IN ('pending', 'approved', 'rejected', 'expired', 'superseded')),
    CONSTRAINT ck_workflow_human_task_decision CHECK (
        (status = 'pending' AND decided_at IS NULL AND decided_by IS NULL)
        OR (status IN ('expired', 'superseded') AND decided_at IS NOT NULL AND decided_by IS NULL)
        OR (status IN ('approved', 'rejected') AND decided_at IS NOT NULL AND decided_by IS NOT NULL)
    ),
    CONSTRAINT ck_workflow_human_task_json CHECK (jsonb_typeof(decision_json) = 'object')
);
CREATE UNIQUE INDEX ux_workflow_human_task_pending_business
    ON workflow_human_tasks (workflow_id, node_instance_id, business_key)
    WHERE status = 'pending';
CREATE INDEX ix_workflow_human_tasks_status ON workflow_human_tasks (status, expires_at, id);


CREATE TABLE result_views (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(120) NOT NULL,
    plugin_id VARCHAR(128) NOT NULL,
    page_key VARCHAR(128) NOT NULL,
    scope_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    filters_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    allowed_actions JSONB NOT NULL DEFAULT '[]'::jsonb,
    status VARCHAR(16) NOT NULL DEFAULT 'inactive',
    created_by BIGINT NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    revoked_at TIMESTAMPTZ,
    CONSTRAINT ck_result_views_identity CHECK (
        BTRIM(name) <> '' AND BTRIM(plugin_id) <> '' AND BTRIM(page_key) <> ''
    ),
    CONSTRAINT ck_result_views_json CHECK (
        jsonb_typeof(scope_json) = 'object'
        AND jsonb_typeof(filters_json) = 'object'
        AND jsonb_typeof(allowed_actions) = 'array'
    ),
    CONSTRAINT ck_result_views_status CHECK (status IN ('inactive', 'active', 'revoked')),
    CONSTRAINT ck_result_views_revoked CHECK ((status = 'revoked') = (revoked_at IS NOT NULL))
);
CREATE INDEX ix_result_views_status ON result_views (status, created_at DESC, id DESC);

CREATE TABLE result_view_user_grants (
    view_id BIGINT NOT NULL REFERENCES result_views (id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (view_id, user_id)
);
CREATE INDEX ix_result_view_user_grants_user ON result_view_user_grants (user_id, view_id);

CREATE TABLE result_view_role_grants (
    view_id BIGINT NOT NULL REFERENCES result_views (id) ON DELETE CASCADE,
    role_id BIGINT NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (view_id, role_id)
);
CREATE INDEX ix_result_view_role_grants_role ON result_view_role_grants (role_id, view_id);

-- +goose StatementBegin
CREATE FUNCTION protect_result_view_scope() RETURNS TRIGGER AS $$
BEGIN
    IF OLD.plugin_id <> NEW.plugin_id OR OLD.page_key <> NEW.page_key
        OR OLD.scope_json <> NEW.scope_json OR OLD.filters_json <> NEW.filters_json
        OR OLD.allowed_actions <> NEW.allowed_actions
        OR OLD.created_by <> NEW.created_by OR OLD.created_at <> NEW.created_at
    THEN
        RAISE EXCEPTION 'result view scope is immutable';
    END IF;
    RETURN NEW;
END
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER trg_result_views_immutable
BEFORE UPDATE ON result_views
FOR EACH ROW EXECUTE FUNCTION protect_result_view_scope();


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

ALTER TABLE workflows ADD CONSTRAINT fk_workflows_published_revision FOREIGN KEY(id,published_revision_id) REFERENCES workflow_revisions(workflow_id,id) DEFERRABLE INITIALLY DEFERRED;

-- Retention cleanup is the only allowed deletion path for immutable run facts.
-- +goose StatementBegin
CREATE FUNCTION workflow_run_retention_expired(target_run_id BIGINT) RETURNS BOOLEAN AS $$
    SELECT EXISTS (
        SELECT 1
        FROM workflow_runs r
        JOIN workflows w ON w.id = r.workflow_id
        WHERE r.id = target_run_id
          AND r.completed_at IS NOT NULL
          AND r.completed_at < CURRENT_TIMESTAMP - make_interval(days => w.retention_days)
    );
$$ LANGUAGE sql STABLE;
-- +goose StatementEnd


-- Deletion is allowed only from the service's explicit, single-transaction cleanup path.
-- Ordinary updates and retention cleanup keep the existing immutability rules.
-- +goose StatementBegin
CREATE FUNCTION reject_workflow_revision_mutation() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' AND current_setting('coinsphere.workflow_delete', true) = 'on' THEN
        RETURN OLD;
    END IF;
    IF TG_OP = 'DELETE'
        AND NOT EXISTS(SELECT 1 FROM workflows WHERE id=OLD.workflow_id AND (draft_revision_id=OLD.id OR published_revision_id=OLD.id))
    THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'workflow revisions are immutable';
END
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION reject_workflow_secret_binding_mutation() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' AND current_setting('coinsphere.workflow_delete', true) = 'on' THEN
        RETURN OLD;
    END IF;
    IF TG_OP = 'DELETE'
        AND NOT EXISTS(SELECT 1 FROM workflows WHERE id=OLD.workflow_id AND (draft_revision_id=OLD.revision_id OR published_revision_id=OLD.revision_id))
    THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'workflow secret bindings are immutable';
END
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION enforce_workflow_draft_revision() RETURNS TRIGGER AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM workflows WHERE id=NEW.id AND (draft_revision_id IS NULL OR (status='active' AND published_revision_id IS NULL))) THEN
  RAISE EXCEPTION 'workflow requires a draft and active workflows require a published revision';
 END IF;
 RETURN NULL;
END $$ LANGUAGE plpgsql;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION reject_workflow_run_checkpoint_mutation() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' AND current_setting('coinsphere.workflow_delete', true) = 'on' THEN
        RETURN OLD;
    END IF;
    IF TG_OP = 'DELETE' AND workflow_run_retention_expired(OLD.run_id) THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'workflow run checkpoints are immutable';
END
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION reject_terminal_workflow_run_node_mutation() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' AND current_setting('coinsphere.workflow_delete', true) = 'on' THEN
        RETURN OLD;
    END IF;
    IF TG_OP = 'DELETE' AND workflow_run_retention_expired(OLD.run_id) THEN
        RETURN OLD;
    END IF;
    IF TG_OP = 'UPDATE' AND OLD.status = 'running' THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'terminal workflow run nodes are immutable';
END
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION reject_workflow_node_log_mutation() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' AND current_setting('coinsphere.workflow_delete', true) = 'on' THEN
        RETURN OLD;
    END IF;
    IF TG_OP = 'DELETE' AND workflow_run_retention_expired(OLD.run_id) THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'workflow node logs are immutable';
END
$$ LANGUAGE plpgsql;
-- +goose StatementEnd


CREATE TRIGGER trg_workflow_revisions_immutable BEFORE UPDATE OR DELETE ON workflow_revisions FOR EACH ROW EXECUTE FUNCTION reject_workflow_revision_mutation();
CREATE TRIGGER trg_workflow_secret_bindings_immutable BEFORE UPDATE OR DELETE ON workflow_secret_bindings FOR EACH ROW EXECUTE FUNCTION reject_workflow_secret_binding_mutation();
CREATE CONSTRAINT TRIGGER trg_workflows_draft_revision AFTER INSERT OR UPDATE ON workflows DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION enforce_workflow_draft_revision();
CREATE TRIGGER trg_workflow_run_checkpoints_immutable BEFORE UPDATE OR DELETE ON workflow_run_checkpoints FOR EACH ROW EXECUTE FUNCTION reject_workflow_run_checkpoint_mutation();
CREATE TRIGGER trg_workflow_run_nodes_terminal_immutable BEFORE UPDATE OR DELETE ON workflow_run_nodes FOR EACH ROW EXECUTE FUNCTION reject_terminal_workflow_run_node_mutation();
CREATE TRIGGER trg_workflow_node_logs_immutable BEFORE UPDATE OR DELETE ON workflow_node_logs FOR EACH ROW EXECUTE FUNCTION reject_workflow_node_log_mutation();
CREATE TABLE permissions(code VARCHAR(160) PRIMARY KEY,title VARCHAR(255) NOT NULL,plugin_id VARCHAR(160) NOT NULL DEFAULT '',protected BOOLEAN NOT NULL DEFAULT FALSE);
CREATE TABLE role_permissions(role_id BIGINT NOT NULL REFERENCES roles(id) ON DELETE CASCADE,permission_code VARCHAR(160) NOT NULL REFERENCES permissions(code) ON DELETE RESTRICT,PRIMARY KEY(role_id,permission_code));
CREATE TABLE workflow_user_grants(workflow_id BIGINT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,permissions JSONB NOT NULL CHECK(jsonb_typeof(permissions)='array'),PRIMARY KEY(workflow_id,user_id));
CREATE TABLE workflow_role_grants(workflow_id BIGINT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,role_id BIGINT NOT NULL REFERENCES roles(id) ON DELETE CASCADE,permissions JSONB NOT NULL CHECK(jsonb_typeof(permissions)='array'),PRIMARY KEY(workflow_id,role_id));
CREATE TABLE revoked_sessions(token_id VARCHAR(128) PRIMARY KEY,expires_at TIMESTAMPTZ NOT NULL);
CREATE INDEX ix_revoked_sessions_expiry ON revoked_sessions(expires_at);
CREATE TABLE schema_generation(id BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK(id),generation INTEGER NOT NULL CHECK(generation=4),database_id TEXT NOT NULL UNIQUE DEFAULT gen_random_uuid()::text);
INSERT INTO schema_generation(generation) VALUES(4);
CREATE TABLE workflow_migration_batches(plan_hash CHAR(64) PRIMARY KEY,source_fingerprint CHAR(64) NOT NULL,target_identity TEXT NOT NULL,manifest JSONB NOT NULL,applied_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp());
CREATE TABLE workflow_migration_items(plan_hash CHAR(64) NOT NULL REFERENCES workflow_migration_batches(plan_hash),kind VARCHAR(32) NOT NULL,source_id TEXT NOT NULL,target_id TEXT NOT NULL,digest CHAR(64) NOT NULL,PRIMARY KEY(plan_hash,kind,source_id));
CREATE TABLE notification_inbox(
 id BIGSERIAL PRIMARY KEY,operation_key VARCHAR(64) NOT NULL,workflow_id BIGINT NOT NULL REFERENCES workflows(id) ON DELETE RESTRICT,revision_id BIGINT NOT NULL,
 node_instance_id VARCHAR(128) NOT NULL,channel VARCHAR(32) NOT NULL CHECK(channel='in_app'),recipient_user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 subject_key VARCHAR(256) NOT NULL,title VARCHAR(255) NOT NULL,message VARCHAR(10000) NOT NULL,status VARCHAR(16) NOT NULL CHECK(status='delivered'),attempt_count INTEGER NOT NULL DEFAULT 1,
 delivered_at TIMESTAMPTZ NOT NULL,is_read BOOLEAN NOT NULL DEFAULT FALSE,read_at TIMESTAMPTZ,last_error_category VARCHAR(64),created_at TIMESTAMPTZ NOT NULL,updated_at TIMESTAMPTZ NOT NULL,
 UNIQUE(operation_key,recipient_user_id),FOREIGN KEY(workflow_id,revision_id) REFERENCES workflow_revisions(workflow_id,id) ON DELETE RESTRICT,
 CHECK (is_read=(read_at IS NOT NULL))
);
CREATE INDEX ix_notification_inbox_recipient ON notification_inbox(recipient_user_id,is_read,id DESC);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'generation 4 rollback requires the matching database recovery point and image'; END $$;
-- +goose StatementEnd
