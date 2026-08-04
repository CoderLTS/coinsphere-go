-- +goose Up
CREATE TABLE audit_records (
    id BIGSERIAL PRIMARY KEY,
    request_id VARCHAR(64) NOT NULL,
    actor_user_id BIGINT,
    action VARCHAR(255) NOT NULL,
    resource_path VARCHAR(500) NOT NULL,
    outcome VARCHAR(16) NOT NULL,
    status_code INTEGER NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_audit_records_request_id
        CHECK (request_id ~ '^[A-Za-z0-9._-]{1,64}$'),
    CONSTRAINT ck_audit_records_outcome
        CHECK (outcome IN ('success', 'failure')),
    CONSTRAINT ck_audit_records_status_code
        CHECK (status_code BETWEEN 100 AND 599)
);
CREATE INDEX ix_audit_records_created_at ON audit_records (created_at DESC, id DESC);
CREATE INDEX ix_audit_records_actor_created_at ON audit_records (actor_user_id, created_at DESC, id DESC);
CREATE INDEX ix_audit_records_request_id ON audit_records (request_id);

-- +goose Down
-- 审计记录不可静默丢弃；只有空表才能回滚本 migration。
LOCK TABLE audit_records IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE a1_observability_down_guard (
    row_count BIGINT NOT NULL CHECK (row_count = 0)
) ON COMMIT DROP;

INSERT INTO a1_observability_down_guard (row_count)
SELECT COUNT(*) FROM audit_records;

DROP TABLE audit_records;
