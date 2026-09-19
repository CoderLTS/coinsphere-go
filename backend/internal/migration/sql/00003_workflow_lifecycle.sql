-- +goose Up

CREATE TABLE workflows (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(120) NOT NULL,
    description VARCHAR(500) NOT NULL DEFAULT '',
    mode VARCHAR(16) NOT NULL DEFAULT 'batch',
    status VARCHAR(16) NOT NULL DEFAULT 'inactive',
    active_revision_id BIGINT,
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
    CONSTRAINT ck_workflow_revisions_graph CHECK (jsonb_typeof(graph_json) = 'object'),
    CONSTRAINT ck_workflow_revisions_versions CHECK (jsonb_typeof(node_versions) = 'object'),
    CONSTRAINT ck_workflow_revisions_trigger CHECK (BTRIM(main_trigger_node_id) <> ''),
    CONSTRAINT ux_workflow_revision_identity UNIQUE (workflow_id, id),
    CONSTRAINT ux_workflow_revision_number UNIQUE (workflow_id, revision_number)
);
CREATE INDEX ix_workflow_revisions_created ON workflow_revisions (workflow_id, created_at DESC, id DESC);

ALTER TABLE workflows
    ADD CONSTRAINT fk_workflows_active_revision
    FOREIGN KEY (id, active_revision_id)
    REFERENCES workflow_revisions (workflow_id, id)
    DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE workflow_runtimes (
    workflow_id BIGINT PRIMARY KEY REFERENCES workflows (id) ON DELETE CASCADE,
    max_concurrent_runs INTEGER NOT NULL DEFAULT 2,
    backlog_limit INTEGER NOT NULL DEFAULT 100,
    next_scheduled_at TIMESTAMPTZ,
    last_scheduled_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_workflow_runtimes_concurrency CHECK (max_concurrent_runs BETWEEN 1 AND 32),
    CONSTRAINT ck_workflow_runtimes_backlog CHECK (backlog_limit BETWEEN 1 AND 10000)
);

-- +goose StatementBegin
CREATE FUNCTION reject_workflow_revision_mutation() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'workflow revisions are immutable';
END
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER trg_workflow_revisions_immutable
BEFORE UPDATE OR DELETE ON workflow_revisions
FOR EACH ROW EXECUTE FUNCTION reject_workflow_revision_mutation();

-- +goose StatementBegin
CREATE FUNCTION enforce_workflow_active_revision() RETURNS TRIGGER AS $$
DECLARE
    current_revision BIGINT;
BEGIN
    SELECT active_revision_id INTO current_revision FROM workflows WHERE id = NEW.id;
    IF current_revision IS NULL THEN
        RAISE EXCEPTION 'workflow requires an active revision';
    END IF;
    RETURN NULL;
END
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER trg_workflows_active_revision
AFTER INSERT OR UPDATE ON workflows
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION enforce_workflow_active_revision();

-- +goose Down

LOCK TABLE workflows, workflow_revisions, workflow_runtimes IN ACCESS EXCLUSIVE MODE;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM workflow_runtimes LIMIT 1)
        OR EXISTS (SELECT 1 FROM workflow_revisions LIMIT 1)
        OR EXISTS (SELECT 1 FROM workflows LIMIT 1)
    THEN
        RAISE EXCEPTION 'refusing to roll back workflow lifecycle data';
    END IF;
END
$$;
-- +goose StatementEnd

DROP TRIGGER trg_workflows_active_revision ON workflows;
DROP FUNCTION enforce_workflow_active_revision();
DROP TABLE workflow_runtimes;
ALTER TABLE workflows DROP CONSTRAINT fk_workflows_active_revision;
DROP TRIGGER trg_workflow_revisions_immutable ON workflow_revisions;
DROP FUNCTION reject_workflow_revision_mutation();
DROP TABLE workflow_revisions;
DROP TABLE workflows;
