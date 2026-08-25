-- +goose Up

CREATE TABLE plugin_installations (
    plugin_id VARCHAR(160) PRIMARY KEY,
    version VARCHAR(32) NOT NULL,
    schema_name VARCHAR(63) NOT NULL UNIQUE,
    source_path TEXT NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'installed',
    installed_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_plugin_installations_status CHECK (status IN ('installed', 'uninstalled')),
    CONSTRAINT ck_plugin_installations_id CHECK (plugin_id <> ''),
    CONSTRAINT ck_plugin_installations_schema CHECK (schema_name <> '')
);

CREATE TABLE plugin_references (
    id BIGSERIAL PRIMARY KEY,
    plugin_id VARCHAR(160) NOT NULL REFERENCES plugin_installations (plugin_id) ON DELETE CASCADE,
    reference_type VARCHAR(64) NOT NULL,
    reference_id VARCHAR(160) NOT NULL,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_plugin_references_type CHECK (reference_type <> ''),
    CONSTRAINT ck_plugin_references_id CHECK (reference_id <> '')
);
CREATE UNIQUE INDEX ux_plugin_references_identity ON plugin_references (plugin_id, reference_type, reference_id);
CREATE INDEX ix_plugin_references_active ON plugin_references (plugin_id, active);

CREATE FUNCTION enforce_installed_plugin_reference() RETURNS TRIGGER AS $$
DECLARE
    plugin_status VARCHAR(16);
BEGIN
    IF NEW.active THEN
        SELECT status INTO plugin_status
        FROM plugin_installations
        WHERE plugin_id = NEW.plugin_id
        FOR SHARE;
        IF plugin_status IS DISTINCT FROM 'installed' THEN
            RAISE EXCEPTION 'active plugin references require an installed plugin';
        END IF;
    END IF;
    RETURN NEW;
END
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_plugin_references_installed
BEFORE INSERT OR UPDATE OF plugin_id, active ON plugin_references
FOR EACH ROW EXECUTE FUNCTION enforce_installed_plugin_reference();

-- +goose Down

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM plugin_references LIMIT 1)
        OR EXISTS (SELECT 1 FROM plugin_installations LIMIT 1)
    THEN
        RAISE EXCEPTION 'refusing to roll back plugin lifecycle data';
    END IF;
END
$$;
DROP TABLE plugin_references;
DROP TABLE plugin_installations;
DROP FUNCTION enforce_installed_plugin_reference();
