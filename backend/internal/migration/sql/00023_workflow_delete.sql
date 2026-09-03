-- +goose Up

-- Deletion is allowed only from the service's explicit, single-transaction cleanup path.
-- Ordinary updates and retention cleanup keep the existing immutability rules.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION reject_workflow_revision_mutation() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' AND current_setting('coinsphere.workflow_delete', true) = 'on' THEN
        RETURN OLD;
    END IF;
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
    IF TG_OP = 'DELETE' AND current_setting('coinsphere.workflow_delete', true) = 'on' THEN
        RETURN OLD;
    END IF;
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

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enforce_workflow_active_revision() RETURNS TRIGGER AS $$
DECLARE
    current_revision BIGINT;
BEGIN
    IF NEW.active_revision_id IS NULL AND current_setting('coinsphere.workflow_delete', true) = 'on' THEN
        RETURN NULL;
    END IF;
    SELECT active_revision_id INTO current_revision FROM workflows WHERE id = NEW.id;
    IF current_revision IS NULL THEN
        RAISE EXCEPTION 'workflow requires an active revision';
    END IF;
    RETURN NULL;
END
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION reject_workflow_run_checkpoint_mutation() RETURNS TRIGGER AS $$
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
CREATE OR REPLACE FUNCTION reject_terminal_workflow_run_node_mutation() RETURNS TRIGGER AS $$
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
CREATE OR REPLACE FUNCTION reject_workflow_node_log_mutation() RETURNS TRIGGER AS $$
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

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION plugin_quant.protect_paper_fact() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' AND current_setting('coinsphere.workflow_delete', true) = 'on' THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'Paper facts are immutable';
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

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enforce_workflow_active_revision() RETURNS TRIGGER AS $$
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

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION reject_workflow_run_checkpoint_mutation() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' AND workflow_run_retention_expired(OLD.run_id) THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'workflow run checkpoints are immutable';
END
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION reject_terminal_workflow_run_node_mutation() RETURNS TRIGGER AS $$
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

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION reject_workflow_node_log_mutation() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' AND workflow_run_retention_expired(OLD.run_id) THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'workflow node logs are immutable';
END
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION plugin_quant.protect_paper_fact() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'Paper facts are immutable';
END
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
