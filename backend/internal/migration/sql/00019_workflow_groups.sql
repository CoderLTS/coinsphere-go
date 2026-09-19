-- +goose Up

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

ALTER TABLE workflows
    ADD COLUMN group_id BIGINT REFERENCES workflow_groups (id) ON DELETE SET NULL;

CREATE INDEX ix_workflows_group_updated ON workflows (group_id, updated_at DESC, id DESC);

-- +goose Down

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM workflow_groups LIMIT 1)
        OR EXISTS (SELECT 1 FROM workflows WHERE group_id IS NOT NULL LIMIT 1)
    THEN
        RAISE EXCEPTION 'refusing to roll back non-empty workflow groups';
    END IF;
END
$$;
-- +goose StatementEnd

DROP INDEX ix_workflows_group_updated;
ALTER TABLE workflows DROP COLUMN group_id;
DROP TABLE workflow_groups;
