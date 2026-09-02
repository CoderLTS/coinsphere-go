-- +goose Up

-- Revisions stay immutable, but the save transaction may remove the oldest
-- revision after creating an eleventh one.
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

-- +goose Down

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION reject_workflow_revision_mutation() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'workflow revisions are immutable';
END
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION reject_workflow_secret_binding_mutation() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'workflow secret bindings are immutable';
END
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
