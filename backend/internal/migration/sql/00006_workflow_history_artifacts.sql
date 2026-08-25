-- +goose Up

CREATE TABLE workflow_activities (
    cursor BIGSERIAL PRIMARY KEY,
    workflow_id BIGINT NOT NULL REFERENCES workflows (id) ON DELETE RESTRICT,
    batch_id BIGINT REFERENCES execution_batches (id) ON DELETE CASCADE,
    node_run_id BIGINT REFERENCES workflow_node_runs (id) ON DELETE CASCADE,
    event_type VARCHAR(64) NOT NULL,
    status VARCHAR(16),
    summary VARCHAR(240) NOT NULL,
    error_category VARCHAR(32),
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_workflow_activities_event CHECK (BTRIM(event_type) <> ''),
    CONSTRAINT ck_workflow_activities_summary CHECK (BTRIM(summary) <> '')
);
CREATE INDEX ix_workflow_activities_cursor ON workflow_activities (workflow_id, cursor);
CREATE INDEX ix_workflow_activities_batch ON workflow_activities (batch_id, cursor);

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
    checkpoint_id BIGINT NOT NULL REFERENCES workflow_checkpoints (id) ON DELETE CASCADE,
    artifact_sha256 CHAR(64) NOT NULL REFERENCES workflow_artifacts (sha256) ON DELETE RESTRICT,
    ordinal INTEGER NOT NULL,
    media_type VARCHAR(255) NOT NULL,
    size_bytes BIGINT NOT NULL,
    PRIMARY KEY (checkpoint_id, ordinal),
    CONSTRAINT ck_workflow_artifact_refs_ordinal CHECK (ordinal >= 0),
    CONSTRAINT ck_workflow_artifact_refs_media CHECK (BTRIM(media_type) <> ''),
    CONSTRAINT ck_workflow_artifact_refs_size CHECK (size_bytes >= 0)
);
CREATE INDEX ix_workflow_artifact_refs_sha ON workflow_artifact_refs (artifact_sha256);

-- +goose StatementBegin
CREATE FUNCTION append_workflow_activity(
    target_workflow_id BIGINT,
    target_batch_id BIGINT,
    target_node_run_id BIGINT,
    target_event_type VARCHAR,
    target_status VARCHAR,
    target_summary VARCHAR,
    target_error_category VARCHAR,
    target_occurred_at TIMESTAMPTZ
) RETURNS VOID AS $$
DECLARE
    next_cursor BIGINT;
BEGIN
    INSERT INTO workflow_activities (
        workflow_id, batch_id, node_run_id, event_type, status, summary, error_category, occurred_at
    ) VALUES (
        target_workflow_id, target_batch_id, target_node_run_id, target_event_type,
        target_status, target_summary, target_error_category, target_occurred_at
    ) RETURNING cursor INTO next_cursor;

    UPDATE workflow_runtimes
    SET activity_cursor = next_cursor, updated_at = target_occurred_at
    WHERE workflow_id = target_workflow_id;
END
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION record_workflow_batch_activity() RETURNS TRIGGER AS $$
DECLARE
    activity_type VARCHAR(64);
    activity_summary VARCHAR(240);
BEGIN
    IF TG_OP = 'INSERT' THEN
        activity_type := 'batch.queued';
        activity_summary := '批次已进入队列';
    ELSIF OLD.cancel_requested_at IS NULL AND NEW.cancel_requested_at IS NOT NULL
        AND OLD.status = NEW.status THEN
        activity_type := 'batch.cancel_requested';
        activity_summary := '批次已请求取消';
    ELSIF OLD.status = NEW.status THEN
        RETURN NEW;
    ELSE
        activity_type := 'batch.' || NEW.status;
        activity_summary := CASE NEW.status
            WHEN 'queued' THEN '批次已重新排队'
            WHEN 'running' THEN '批次开始执行'
            WHEN 'retrying' THEN '批次等待重试'
            WHEN 'succeeded' THEN '批次执行成功'
            WHEN 'failed' THEN '批次执行失败'
            WHEN 'cancelled' THEN '批次已取消'
            ELSE '批次状态已更新'
        END;
    END IF;

    PERFORM append_workflow_activity(
        NEW.workflow_id, NEW.id, NULL, activity_type, NEW.status,
        activity_summary, NEW.error_category, NEW.updated_at
    );
    RETURN NEW;
END
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER trg_execution_batches_activity
AFTER INSERT OR UPDATE ON execution_batches
FOR EACH ROW EXECUTE FUNCTION record_workflow_batch_activity();

-- +goose StatementBegin
CREATE FUNCTION record_workflow_node_run_activity() RETURNS TRIGGER AS $$
DECLARE
    target_workflow_id BIGINT;
    activity_summary VARCHAR(240);
BEGIN
    IF TG_OP = 'UPDATE' AND OLD.status = NEW.status THEN
        RETURN NEW;
    END IF;

    SELECT workflow_id INTO target_workflow_id FROM execution_batches WHERE id = NEW.batch_id;
    activity_summary := CASE NEW.status
        WHEN 'running' THEN '节点 ' || NEW.node_instance_id || ' 开始执行'
        WHEN 'succeeded' THEN '节点 ' || NEW.node_instance_id || ' 执行成功'
        WHEN 'failed' THEN '节点 ' || NEW.node_instance_id || ' 执行失败'
        WHEN 'cancelled' THEN '节点 ' || NEW.node_instance_id || ' 已取消'
        WHEN 'skipped' THEN '节点 ' || NEW.node_instance_id || ' 已跳过'
        ELSE '节点 ' || NEW.node_instance_id || ' 状态已更新'
    END;

    PERFORM append_workflow_activity(
        target_workflow_id, NEW.batch_id, NEW.id, 'node.' || NEW.status,
        NEW.status, activity_summary, NEW.error_category, COALESCE(NEW.completed_at, NEW.started_at)
    );
    RETURN NEW;
END
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER trg_workflow_node_runs_activity
AFTER INSERT OR UPDATE ON workflow_node_runs
FOR EACH ROW EXECUTE FUNCTION record_workflow_node_run_activity();

-- Retention cleanup is the only allowed deletion path for immutable execution history.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION reject_workflow_checkpoint_mutation() RETURNS TRIGGER AS $$
DECLARE
    retention_expired BOOLEAN;
BEGIN
    IF TG_OP = 'DELETE' THEN
        SELECT EXISTS (
            SELECT 1 FROM execution_batches eb
            JOIN workflows w ON w.id = eb.workflow_id
            WHERE eb.id = OLD.batch_id
              AND eb.completed_at IS NOT NULL
              AND eb.completed_at < CURRENT_TIMESTAMP - make_interval(days => w.retention_days)
        ) INTO retention_expired;
        IF retention_expired THEN
            RETURN OLD;
        END IF;
    END IF;
    RAISE EXCEPTION 'workflow checkpoints are immutable';
END
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION reject_terminal_workflow_node_run_mutation() RETURNS TRIGGER AS $$
DECLARE
    retention_expired BOOLEAN;
BEGIN
    IF TG_OP = 'DELETE' THEN
        SELECT EXISTS (
            SELECT 1 FROM execution_batches eb
            JOIN workflows w ON w.id = eb.workflow_id
            WHERE eb.id = OLD.batch_id
              AND eb.completed_at IS NOT NULL
              AND eb.completed_at < CURRENT_TIMESTAMP - make_interval(days => w.retention_days)
        ) INTO retention_expired;
        IF retention_expired THEN
            RETURN OLD;
        END IF;
    ELSIF OLD.status = 'running' THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'terminal workflow node runs are immutable';
END
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose Down

LOCK TABLE workflow_activities, workflow_artifact_refs, workflow_artifacts IN ACCESS EXCLUSIVE MODE;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM workflow_activities LIMIT 1)
        OR EXISTS (SELECT 1 FROM workflow_artifact_refs LIMIT 1)
        OR EXISTS (SELECT 1 FROM workflow_artifacts LIMIT 1)
    THEN
        RAISE EXCEPTION 'refusing to roll back workflow history or artifacts';
    END IF;
END
$$;
-- +goose StatementEnd

DROP TRIGGER trg_workflow_node_runs_activity ON workflow_node_runs;
DROP FUNCTION record_workflow_node_run_activity();
DROP TRIGGER trg_execution_batches_activity ON execution_batches;
DROP FUNCTION record_workflow_batch_activity();
DROP FUNCTION append_workflow_activity(BIGINT, BIGINT, BIGINT, VARCHAR, VARCHAR, VARCHAR, VARCHAR, TIMESTAMPTZ);
DROP TABLE workflow_artifact_refs;
DROP TABLE workflow_artifacts;
DROP TABLE workflow_activities;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION reject_workflow_checkpoint_mutation() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'workflow checkpoints are immutable';
END
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION reject_terminal_workflow_node_run_mutation() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' OR OLD.status <> 'running' THEN
        RAISE EXCEPTION 'terminal workflow node runs are immutable';
    END IF;
    RETURN NEW;
END
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
