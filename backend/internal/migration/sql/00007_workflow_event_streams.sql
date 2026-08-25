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

ALTER TABLE execution_batches
    ADD COLUMN event_record_id BIGINT REFERENCES workflow_event_records (id) ON DELETE RESTRICT,
    ADD COLUMN partition_key VARCHAR(256) NOT NULL DEFAULT '',
    ADD COLUMN diagnostic BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN original_batch_id BIGINT REFERENCES execution_batches (id) ON DELETE RESTRICT;
ALTER TABLE execution_batches DROP CONSTRAINT ck_execution_batches_trigger;
ALTER TABLE execution_batches ADD CONSTRAINT ck_execution_batches_trigger
    CHECK (trigger_type IN ('manual', 'schedule', 'event', 'stream', 'webhook', 'failure'));
ALTER TABLE execution_batches DROP CONSTRAINT ck_execution_batches_status;
ALTER TABLE execution_batches ADD CONSTRAINT ck_execution_batches_status
    CHECK (status IN ('queued', 'running', 'waiting', 'retrying', 'succeeded', 'failed', 'cancelled'));
ALTER TABLE execution_batches ADD CONSTRAINT ck_execution_batches_event
    CHECK ((trigger_type IN ('event', 'stream', 'webhook', 'failure')) = (event_record_id IS NOT NULL));
ALTER TABLE execution_batches ADD CONSTRAINT ck_execution_batches_diagnostic
    CHECK (diagnostic = (original_batch_id IS NOT NULL));
CREATE INDEX ix_execution_batches_partition ON execution_batches (workflow_id, partition_key, created_at, id)
    WHERE partition_key <> '' AND status IN ('queued', 'running', 'retrying');

ALTER TABLE workflow_node_runs DROP CONSTRAINT ck_workflow_node_runs_status;
ALTER TABLE workflow_node_runs ADD CONSTRAINT ck_workflow_node_runs_status
    CHECK (status IN ('running', 'waiting', 'succeeded', 'failed', 'cancelled', 'skipped'));

CREATE TABLE workflow_event_deliveries (
    id BIGSERIAL PRIMARY KEY,
    event_record_id BIGINT NOT NULL REFERENCES workflow_event_records (id) ON DELETE RESTRICT,
    workflow_id BIGINT NOT NULL REFERENCES workflows (id) ON DELETE RESTRICT,
    revision_id BIGINT NOT NULL,
    batch_id BIGINT NOT NULL REFERENCES execution_batches (id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_workflow_event_delivery_revision
        FOREIGN KEY (workflow_id, revision_id)
        REFERENCES workflow_revisions (workflow_id, id)
        ON DELETE RESTRICT,
    CONSTRAINT ux_workflow_event_delivery UNIQUE (event_record_id, workflow_id),
    CONSTRAINT ux_workflow_event_delivery_batch UNIQUE (batch_id)
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
    batch_id BIGINT NOT NULL REFERENCES execution_batches (id) ON DELETE RESTRICT,
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
    CONSTRAINT ux_workflow_human_task_node UNIQUE (batch_id, node_instance_id),
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

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION record_workflow_batch_activity() RETURNS TRIGGER AS $$
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
            WHEN 'waiting' THEN '批次等待人工决定'
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

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION record_workflow_node_run_activity() RETURNS TRIGGER AS $$
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
        WHEN 'waiting' THEN '节点 ' || NEW.node_instance_id || ' 等待人工决定'
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

-- +goose StatementBegin
CREATE FUNCTION record_workflow_human_task_activity() RETURNS TRIGGER AS $$
DECLARE
    activity_summary VARCHAR(240);
BEGIN
    IF TG_OP = 'UPDATE' AND OLD.status = NEW.status THEN
        RETURN NEW;
    END IF;
    activity_summary := CASE NEW.status
        WHEN 'pending' THEN '人工任务已创建'
        WHEN 'approved' THEN '人工任务已批准'
        WHEN 'rejected' THEN '人工任务已拒绝'
        WHEN 'expired' THEN '人工任务已过期'
        WHEN 'superseded' THEN '人工任务已被取代'
        ELSE '人工任务状态已更新'
    END;
    PERFORM append_workflow_activity(
        NEW.workflow_id, NEW.batch_id, NULL, 'human_task.' || NEW.status,
        NEW.status, activity_summary, NULL, NEW.updated_at
    );
    RETURN NEW;
END
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER trg_workflow_human_tasks_activity
AFTER INSERT OR UPDATE ON workflow_human_tasks
FOR EACH ROW EXECUTE FUNCTION record_workflow_human_task_activity();

-- +goose Down

LOCK TABLE execution_batches, workflow_event_records, workflow_event_deliveries, workflow_event_outbox, workflow_human_tasks IN ACCESS EXCLUSIVE MODE;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM workflow_event_records LIMIT 1)
        OR EXISTS (SELECT 1 FROM workflow_event_deliveries LIMIT 1)
        OR EXISTS (SELECT 1 FROM workflow_event_outbox LIMIT 1)
        OR EXISTS (SELECT 1 FROM workflow_human_tasks LIMIT 1)
        OR EXISTS (SELECT 1 FROM execution_batches WHERE diagnostic LIMIT 1)
    THEN
        RAISE EXCEPTION 'refusing to roll back workflow event stream data';
    END IF;
END
$$;
-- +goose StatementEnd

DROP TRIGGER trg_workflow_human_tasks_activity ON workflow_human_tasks;
DROP FUNCTION record_workflow_human_task_activity();
DROP TABLE workflow_human_tasks;
DROP TABLE workflow_event_outbox;
DROP TABLE workflow_event_deliveries;
ALTER TABLE workflow_node_runs DROP CONSTRAINT ck_workflow_node_runs_status;
ALTER TABLE workflow_node_runs ADD CONSTRAINT ck_workflow_node_runs_status
    CHECK (status IN ('running', 'succeeded', 'failed', 'cancelled', 'skipped'));
DROP INDEX ix_execution_batches_partition;
ALTER TABLE execution_batches DROP CONSTRAINT ck_execution_batches_diagnostic;
ALTER TABLE execution_batches DROP CONSTRAINT ck_execution_batches_event;
ALTER TABLE execution_batches DROP CONSTRAINT ck_execution_batches_status;
ALTER TABLE execution_batches ADD CONSTRAINT ck_execution_batches_status
    CHECK (status IN ('queued', 'running', 'retrying', 'succeeded', 'failed', 'cancelled'));
ALTER TABLE execution_batches DROP CONSTRAINT ck_execution_batches_trigger;
ALTER TABLE execution_batches ADD CONSTRAINT ck_execution_batches_trigger
    CHECK (trigger_type IN ('manual', 'schedule'));
ALTER TABLE execution_batches
    DROP COLUMN original_batch_id,
    DROP COLUMN diagnostic,
    DROP COLUMN partition_key,
    DROP COLUMN event_record_id;
DROP TABLE workflow_event_records;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION record_workflow_batch_activity() RETURNS TRIGGER AS $$
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

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION record_workflow_node_run_activity() RETURNS TRIGGER AS $$
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
