-- +goose Up

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

-- +goose StatementBegin
CREATE FUNCTION reject_workflow_secret_binding_mutation() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'workflow secret bindings are immutable';
END
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER trg_workflow_secret_bindings_immutable
BEFORE UPDATE OR DELETE ON workflow_secret_bindings
FOR EACH ROW EXECUTE FUNCTION reject_workflow_secret_binding_mutation();

-- +goose Down

LOCK TABLE workflows, workflow_revisions, workflow_secret_bindings IN ACCESS EXCLUSIVE MODE;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM workflow_revisions LIMIT 1) THEN
        RAISE EXCEPTION 'refusing to roll back schema workbench data';
    END IF;
END
$$;
-- +goose StatementEnd

DROP TRIGGER trg_workflow_secret_bindings_immutable ON workflow_secret_bindings;
DROP FUNCTION reject_workflow_secret_binding_mutation();
DROP TABLE workflow_secret_bindings;
