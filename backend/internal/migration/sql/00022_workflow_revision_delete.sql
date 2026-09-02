-- +goose Up

-- 修订内容仍不可更新；历史修订只能经服务层事务删除，外键继续保护运行事实。
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION reject_workflow_revision_mutation() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE'
        AND OLD.id IS DISTINCT FROM (
            SELECT active_revision_id FROM workflows WHERE id = OLD.workflow_id
        )
    THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'workflow revisions are immutable';
END
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION reject_workflow_secret_binding_mutation() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE'
        AND OLD.revision_id IS DISTINCT FROM (
            SELECT active_revision_id FROM workflows WHERE id = OLD.workflow_id
        )
    THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'workflow secret bindings are immutable';
END
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION reject_workflow_revision_mutation() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE'
        AND OLD.id IS DISTINCT FROM (
            SELECT active_revision_id FROM workflows WHERE id = OLD.workflow_id
        )
        AND OLD.id = (
            SELECT id
            FROM workflow_revisions
            WHERE workflow_id = OLD.workflow_id
            ORDER BY revision_number, id
            LIMIT 1
        )
        AND (
            SELECT COUNT(*) FROM workflow_revisions WHERE workflow_id = OLD.workflow_id
        ) > 10
    THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'workflow revisions are immutable';
END
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION reject_workflow_secret_binding_mutation() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE'
        AND OLD.revision_id IS DISTINCT FROM (
            SELECT active_revision_id FROM workflows WHERE id = OLD.workflow_id
        )
        AND OLD.revision_id = (
            SELECT id
            FROM workflow_revisions
            WHERE workflow_id = OLD.workflow_id
            ORDER BY revision_number, id
            LIMIT 1
        )
        AND (
            SELECT COUNT(*) FROM workflow_revisions WHERE workflow_id = OLD.workflow_id
        ) > 10
    THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'workflow secret bindings are immutable';
END
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
