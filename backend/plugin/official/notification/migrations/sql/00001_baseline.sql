-- +goose Up
CREATE SCHEMA IF NOT EXISTS plugin_notification;
CREATE TABLE plugin_notification.deliveries(
 id BIGSERIAL PRIMARY KEY,operation_key VARCHAR(64) NOT NULL UNIQUE,workflow_id BIGINT NOT NULL,revision_id BIGINT NOT NULL,node_instance_id VARCHAR(128) NOT NULL,
 channel VARCHAR(32) NOT NULL CHECK(channel IN ('smtp','dingtalk','qq_bot')),subject_key VARCHAR(256) NOT NULL,title VARCHAR(160) NOT NULL,message VARCHAR(10000) NOT NULL,
 status VARCHAR(16) NOT NULL CHECK(status IN ('pending','delivered','failed','unknown')),attempt_count INTEGER NOT NULL DEFAULT 1,
 delivered_at TIMESTAMPTZ,last_error_category VARCHAR(64),created_at TIMESTAMPTZ NOT NULL,updated_at TIMESTAMPTZ NOT NULL,
 FOREIGN KEY(workflow_id,revision_id) REFERENCES workflow_revisions(workflow_id,id) ON DELETE RESTRICT
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'generation 4 rollback requires the matching database recovery point and image'; END $$;
-- +goose StatementEnd
