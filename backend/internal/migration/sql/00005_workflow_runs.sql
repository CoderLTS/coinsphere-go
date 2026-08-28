-- +goose Up

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
    WHERE partition_key <> '' AND status IN ('queued', 'running', 'retrying');

CREATE TABLE workflow_run_nodes (
    id BIGSERIAL PRIMARY KEY,
    run_id BIGINT NOT NULL REFERENCES workflow_runs (id) ON DELETE RESTRICT,
    node_instance_id VARCHAR(128) NOT NULL,
    node_type VARCHAR(128) NOT NULL,
    node_version VARCHAR(32) NOT NULL,
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
    PRIMARY KEY (workflow_id, node_instance_id),
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

-- +goose StatementBegin
CREATE FUNCTION reject_workflow_run_checkpoint_mutation() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' AND workflow_run_retention_expired(OLD.run_id) THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'workflow run checkpoints are immutable';
END
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER trg_workflow_run_checkpoints_immutable
BEFORE UPDATE OR DELETE ON workflow_run_checkpoints
FOR EACH ROW EXECUTE FUNCTION reject_workflow_run_checkpoint_mutation();

-- +goose StatementBegin
CREATE FUNCTION reject_terminal_workflow_run_node_mutation() RETURNS TRIGGER AS $$
BEGIN
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

CREATE TRIGGER trg_workflow_run_nodes_terminal_immutable
BEFORE UPDATE OR DELETE ON workflow_run_nodes
FOR EACH ROW EXECUTE FUNCTION reject_terminal_workflow_run_node_mutation();

-- +goose StatementBegin
CREATE FUNCTION reject_workflow_node_log_mutation() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' AND workflow_run_retention_expired(OLD.run_id) THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'workflow node logs are immutable';
END
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER trg_workflow_node_logs_immutable
BEFORE UPDATE OR DELETE ON workflow_node_logs
FOR EACH ROW EXECUTE FUNCTION reject_workflow_node_log_mutation();

-- +goose Down

LOCK TABLE
    workflow_runs,
    workflow_run_nodes,
    workflow_node_logs,
    workflow_run_checkpoints,
    workflow_node_states,
    workflow_artifacts,
    workflow_artifact_refs,
    workflow_event_records,
    workflow_event_deliveries,
    workflow_event_outbox,
    workflow_human_tasks
IN ACCESS EXCLUSIVE MODE;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM workflow_runs LIMIT 1)
        OR EXISTS (SELECT 1 FROM workflow_run_nodes LIMIT 1)
        OR EXISTS (SELECT 1 FROM workflow_node_logs LIMIT 1)
        OR EXISTS (SELECT 1 FROM workflow_run_checkpoints LIMIT 1)
        OR EXISTS (SELECT 1 FROM workflow_node_states LIMIT 1)
        OR EXISTS (SELECT 1 FROM workflow_artifacts LIMIT 1)
        OR EXISTS (SELECT 1 FROM workflow_artifact_refs LIMIT 1)
        OR EXISTS (SELECT 1 FROM workflow_event_records LIMIT 1)
        OR EXISTS (SELECT 1 FROM workflow_event_deliveries LIMIT 1)
        OR EXISTS (SELECT 1 FROM workflow_event_outbox LIMIT 1)
        OR EXISTS (SELECT 1 FROM workflow_human_tasks LIMIT 1)
    THEN
        RAISE EXCEPTION 'refusing to roll back workflow run data';
    END IF;
END
$$;
-- +goose StatementEnd

DROP TRIGGER trg_workflow_node_logs_immutable ON workflow_node_logs;
DROP FUNCTION reject_workflow_node_log_mutation();
DROP TRIGGER trg_workflow_run_nodes_terminal_immutable ON workflow_run_nodes;
DROP FUNCTION reject_terminal_workflow_run_node_mutation();
DROP TRIGGER trg_workflow_run_checkpoints_immutable ON workflow_run_checkpoints;
DROP FUNCTION reject_workflow_run_checkpoint_mutation();
DROP FUNCTION workflow_run_retention_expired(BIGINT);
DROP TABLE workflow_human_tasks;
DROP TABLE workflow_event_outbox;
DROP TABLE workflow_event_deliveries;
DROP TABLE workflow_artifact_refs;
DROP TABLE workflow_artifacts;
DROP TABLE workflow_run_checkpoints;
DROP TABLE workflow_node_logs;
DROP TABLE workflow_run_nodes;
DROP TABLE workflow_node_states;
DROP TABLE workflow_runs;
DROP TABLE workflow_event_records;
