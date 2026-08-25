-- +goose Up

ALTER TABLE workflow_runtimes
    ADD COLUMN max_concurrent_batches INTEGER NOT NULL DEFAULT 2,
    ADD COLUMN backlog_limit INTEGER NOT NULL DEFAULT 100,
    ADD COLUMN next_scheduled_at TIMESTAMPTZ,
    ADD COLUMN last_scheduled_at TIMESTAMPTZ,
    ADD CONSTRAINT ck_workflow_runtimes_concurrency CHECK (max_concurrent_batches BETWEEN 1 AND 32),
    ADD CONSTRAINT ck_workflow_runtimes_backlog CHECK (backlog_limit BETWEEN 1 AND 10000);

CREATE TABLE execution_batches (
    id BIGSERIAL PRIMARY KEY,
    workflow_id BIGINT NOT NULL REFERENCES workflows (id) ON DELETE RESTRICT,
    revision_id BIGINT NOT NULL,
    trigger_type VARCHAR(16) NOT NULL,
    trigger_key VARCHAR(128) NOT NULL,
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
    error_category VARCHAR(32),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_execution_batch_revision
        FOREIGN KEY (workflow_id, revision_id)
        REFERENCES workflow_revisions (workflow_id, id)
        ON DELETE RESTRICT,
    CONSTRAINT ux_execution_batch_trigger UNIQUE (workflow_id, trigger_type, trigger_key),
    CONSTRAINT ck_execution_batches_trigger CHECK (trigger_type IN ('manual', 'schedule')),
    CONSTRAINT ck_execution_batches_status CHECK (status IN ('queued', 'running', 'retrying', 'succeeded', 'failed', 'cancelled')),
    CONSTRAINT ck_execution_batches_trigger_key CHECK (BTRIM(trigger_key) <> ''),
    CONSTRAINT ck_execution_batches_lease CHECK (
        (status = 'running' AND lease_token IS NOT NULL AND lease_expires_at IS NOT NULL)
        OR (status <> 'running' AND lease_token IS NULL AND lease_expires_at IS NULL)
    ),
    CONSTRAINT ck_execution_batches_completion CHECK (
        (status IN ('succeeded', 'failed', 'cancelled')) = (completed_at IS NOT NULL)
    )
);
CREATE INDEX ix_execution_batches_queue ON execution_batches (status, not_before, created_at, id);
CREATE INDEX ix_execution_batches_workflow ON execution_batches (workflow_id, created_at DESC, id DESC);
CREATE INDEX ix_execution_batches_lease ON execution_batches (lease_expires_at) WHERE status = 'running';

CREATE TABLE workflow_node_runs (
    id BIGSERIAL PRIMARY KEY,
    batch_id BIGINT NOT NULL REFERENCES execution_batches (id) ON DELETE RESTRICT,
    node_instance_id VARCHAR(128) NOT NULL,
    node_type VARCHAR(128) NOT NULL,
    node_version VARCHAR(32) NOT NULL,
    execution_pool VARCHAR(16) NOT NULL,
    attempt INTEGER NOT NULL,
    loop_iteration INTEGER NOT NULL DEFAULT 0,
    operation_key CHAR(64) NOT NULL,
    status VARCHAR(16) NOT NULL,
    error_category VARCHAR(32),
    started_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    duration_ms BIGINT,
    CONSTRAINT ux_workflow_node_run_attempt UNIQUE (batch_id, node_instance_id, loop_iteration, attempt),
    CONSTRAINT ck_workflow_node_runs_identity CHECK (BTRIM(node_instance_id) <> '' AND BTRIM(node_type) <> ''),
    CONSTRAINT ck_workflow_node_runs_pool CHECK (execution_pool IN ('stream', 'compute')),
    CONSTRAINT ck_workflow_node_runs_attempt CHECK (attempt BETWEEN 1 AND 100 AND loop_iteration >= 0),
    CONSTRAINT ck_workflow_node_runs_operation_key CHECK (operation_key ~ '^[0-9a-f]{64}$'),
    CONSTRAINT ck_workflow_node_runs_status CHECK (status IN ('running', 'succeeded', 'failed', 'cancelled', 'skipped')),
    CONSTRAINT ck_workflow_node_runs_terminal CHECK (
        (status = 'running' AND completed_at IS NULL AND duration_ms IS NULL)
        OR (status <> 'running' AND completed_at IS NOT NULL AND duration_ms IS NOT NULL AND duration_ms >= 0)
    )
);
CREATE INDEX ix_workflow_node_runs_batch ON workflow_node_runs (batch_id, id);
CREATE INDEX ix_workflow_node_runs_operation ON workflow_node_runs (operation_key);

CREATE TABLE workflow_checkpoints (
    id BIGSERIAL PRIMARY KEY,
    batch_id BIGINT NOT NULL REFERENCES execution_batches (id) ON DELETE RESTRICT,
    workflow_id BIGINT NOT NULL REFERENCES workflows (id) ON DELETE RESTRICT,
    revision_id BIGINT NOT NULL,
    node_instance_id VARCHAR(128) NOT NULL,
    loop_iteration INTEGER NOT NULL DEFAULT 0,
    operation_key CHAR(64) NOT NULL,
    output_json JSONB NOT NULL,
    artifacts_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_workflow_checkpoint_revision
        FOREIGN KEY (workflow_id, revision_id)
        REFERENCES workflow_revisions (workflow_id, id)
        ON DELETE RESTRICT,
    CONSTRAINT ux_workflow_checkpoint_node UNIQUE (batch_id, node_instance_id, loop_iteration),
    CONSTRAINT ux_workflow_checkpoint_operation UNIQUE (operation_key),
    CONSTRAINT ck_workflow_checkpoints_node CHECK (BTRIM(node_instance_id) <> '' AND loop_iteration >= 0),
    CONSTRAINT ck_workflow_checkpoints_output CHECK (jsonb_typeof(output_json) = 'object'),
    CONSTRAINT ck_workflow_checkpoints_artifacts CHECK (jsonb_typeof(artifacts_json) = 'array')
);
CREATE INDEX ix_workflow_checkpoints_batch ON workflow_checkpoints (batch_id, id);

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

-- +goose StatementBegin
CREATE FUNCTION reject_workflow_checkpoint_mutation() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'workflow checkpoints are immutable';
END
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER trg_workflow_checkpoints_immutable
BEFORE UPDATE OR DELETE ON workflow_checkpoints
FOR EACH ROW EXECUTE FUNCTION reject_workflow_checkpoint_mutation();

-- +goose StatementBegin
CREATE FUNCTION reject_terminal_workflow_node_run_mutation() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' OR OLD.status <> 'running' THEN
        RAISE EXCEPTION 'terminal workflow node runs are immutable';
    END IF;
    RETURN NEW;
END
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER trg_workflow_node_runs_terminal_immutable
BEFORE UPDATE OR DELETE ON workflow_node_runs
FOR EACH ROW EXECUTE FUNCTION reject_terminal_workflow_node_run_mutation();

-- +goose Down

LOCK TABLE execution_batches, workflow_node_runs, workflow_checkpoints, workflow_node_states IN ACCESS EXCLUSIVE MODE;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM execution_batches LIMIT 1)
        OR EXISTS (SELECT 1 FROM workflow_node_runs LIMIT 1)
        OR EXISTS (SELECT 1 FROM workflow_checkpoints LIMIT 1)
        OR EXISTS (SELECT 1 FROM workflow_node_states LIMIT 1)
    THEN
        RAISE EXCEPTION 'refusing to roll back workflow batch execution data';
    END IF;
END
$$;
-- +goose StatementEnd

DROP TRIGGER trg_workflow_node_runs_terminal_immutable ON workflow_node_runs;
DROP FUNCTION reject_terminal_workflow_node_run_mutation();
DROP TRIGGER trg_workflow_checkpoints_immutable ON workflow_checkpoints;
DROP FUNCTION reject_workflow_checkpoint_mutation();
DROP TABLE workflow_node_states;
DROP TABLE workflow_checkpoints;
DROP TABLE workflow_node_runs;
DROP TABLE execution_batches;
ALTER TABLE workflow_runtimes
    DROP CONSTRAINT ck_workflow_runtimes_backlog,
    DROP CONSTRAINT ck_workflow_runtimes_concurrency,
    DROP COLUMN last_scheduled_at,
    DROP COLUMN next_scheduled_at,
    DROP COLUMN backlog_limit,
    DROP COLUMN max_concurrent_batches;
