-- +goose Up
-- A1-3 只建立可靠投递所需的数据契约；原子认领、续租、恢复、退避、死信告警由后续运行时 PR 实现。
-- 空 migration 数据库尚无 GORM 业务表，因此基表不声明指向不存在表的外键；已有 GORM 表及其外键原样保留。
CREATE TABLE IF NOT EXISTS domain_event_outbox (
    id BIGSERIAL PRIMARY KEY,
    event_type VARCHAR(120),
    aggregate_type VARCHAR(120),
    aggregate_id VARCHAR(120),
    workflow_execution_id BIGINT,
    workflow_execution_node_id BIGINT,
    payload_json TEXT,
    metadata_json TEXT,
    status VARCHAR(20) DEFAULT 'pending',
    attempt_count INTEGER DEFAULT 0,
    available_at TIMESTAMPTZ,
    processed_at TIMESTAMPTZ,
    last_error_message TEXT,
    created_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ
);

-- ACCESS EXCLUSIVE 使存量校验、加列、约束和索引相对于业务写入成为单一事务边界。
LOCK TABLE domain_event_outbox IN ACCESS EXCLUSIVE MODE;

ALTER TABLE domain_event_outbox ADD COLUMN max_attempts INTEGER NOT NULL DEFAULT 3;
ALTER TABLE domain_event_outbox ADD COLUMN lease_id VARCHAR(36);
ALTER TABLE domain_event_outbox ADD COLUMN worker_id VARCHAR(120);
ALTER TABLE domain_event_outbox ADD COLUMN lease_expires_at TIMESTAMPTZ;
ALTER TABLE domain_event_outbox ADD COLUMN claimed_at TIMESTAMPTZ;
ALTER TABLE domain_event_outbox ADD COLUMN last_error_category VARCHAR(64);
ALTER TABLE domain_event_outbox ADD COLUMN dead_lettered_at TIMESTAMPTZ;
ALTER TABLE domain_event_outbox ADD COLUMN alerted_at TIMESTAMPTZ;

-- 旧运行时只产生 pending/processed/failed。只修复可无歧义推导的次数默认值，其余异常由 CHECK VALIDATE 原子拒绝。
UPDATE domain_event_outbox SET attempt_count = 0 WHERE attempt_count IS NULL;
UPDATE domain_event_outbox
SET max_attempts = GREATEST(3, attempt_count);

ALTER TABLE domain_event_outbox
    ADD CONSTRAINT ck_event_outbox_status CHECK (
        status IS NOT NULL
        AND status IN ('pending', 'claimed', 'processed', 'failed', 'dead_letter')
    ) NOT VALID,
    ADD CONSTRAINT ck_event_outbox_attempts CHECK (
        attempt_count IS NOT NULL
        AND max_attempts IS NOT NULL
        AND attempt_count >= 0
        AND max_attempts > 0
        AND attempt_count <= max_attempts
    ) NOT VALID,
    ADD CONSTRAINT ck_event_outbox_available_at CHECK (
        available_at IS NOT NULL
    ) NOT VALID,
    ADD CONSTRAINT ck_event_outbox_state_fields CHECK (
        (
            status = 'pending'
            AND attempt_count < max_attempts
            AND processed_at IS NULL
            AND lease_id IS NULL
            AND worker_id IS NULL
            AND lease_expires_at IS NULL
            AND claimed_at IS NULL
            AND dead_lettered_at IS NULL
            AND alerted_at IS NULL
        )
        OR
        (
            status = 'claimed'
            AND attempt_count > 0
            AND processed_at IS NULL
            AND lease_id IS NOT NULL
            AND worker_id IS NOT NULL
            AND lease_expires_at IS NOT NULL
            AND claimed_at IS NOT NULL
            AND lease_expires_at > claimed_at
            AND dead_lettered_at IS NULL
            AND alerted_at IS NULL
        )
        OR
        (
            status IN ('processed', 'failed')
            AND processed_at IS NOT NULL
            AND lease_id IS NULL
            AND worker_id IS NULL
            AND lease_expires_at IS NULL
            AND claimed_at IS NULL
            AND dead_lettered_at IS NULL
            AND alerted_at IS NULL
        )
        OR
        (
            status = 'dead_letter'
            AND attempt_count = max_attempts
            AND processed_at IS NOT NULL
            AND lease_id IS NULL
            AND worker_id IS NULL
            AND lease_expires_at IS NULL
            AND claimed_at IS NULL
            AND dead_lettered_at IS NOT NULL
            AND dead_lettered_at = processed_at
            AND (alerted_at IS NULL OR alerted_at >= dead_lettered_at)
        )
    ) NOT VALID;

-- VALIDATE 在同一事务中检查全部旧行；失败时新增列、约束和版本记录全部回滚。
ALTER TABLE domain_event_outbox VALIDATE CONSTRAINT ck_event_outbox_status;
ALTER TABLE domain_event_outbox VALIDATE CONSTRAINT ck_event_outbox_attempts;
ALTER TABLE domain_event_outbox VALIDATE CONSTRAINT ck_event_outbox_available_at;
ALTER TABLE domain_event_outbox VALIDATE CONSTRAINT ck_event_outbox_state_fields;

-- event_type 索引是旧 GORM 模型的既有查询基线；空 migration 库必须显式建立，Down 也保留它。
CREATE INDEX IF NOT EXISTS idx_domain_event_outbox_event_type
    ON domain_event_outbox (event_type);
DROP INDEX IF EXISTS ix_event_outbox_pending;
CREATE INDEX ix_event_outbox_pending
    ON domain_event_outbox (status, available_at, id);
CREATE INDEX ix_event_outbox_recovery
    ON domain_event_outbox (status, lease_expires_at, id);
CREATE UNIQUE INDEX ux_event_outbox_lease_id
    ON domain_event_outbox (lease_id);
CREATE INDEX ix_event_outbox_dead_letter_alert
    ON domain_event_outbox (status, alerted_at, dead_lettered_at, id);
CREATE INDEX ix_event_outbox_terminal_retention
    ON domain_event_outbox (status, processed_at, id);

-- +goose Down
-- 排他锁先于计数，保证并发写入不能越过空表检查。非空时 guard 违反 CHECK，整个 Down 与版本记录原子回滚。
LOCK TABLE domain_event_outbox IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE outbox_v3_down_guard (
    event_count BIGINT NOT NULL CHECK (event_count = 0)
) ON COMMIT DROP;

INSERT INTO outbox_v3_down_guard (event_count)
SELECT COUNT(*) FROM domain_event_outbox;

DROP INDEX ix_event_outbox_pending;
DROP INDEX ix_event_outbox_recovery;
DROP INDEX ux_event_outbox_lease_id;
DROP INDEX ix_event_outbox_dead_letter_alert;
DROP INDEX ix_event_outbox_terminal_retention;

ALTER TABLE domain_event_outbox
    DROP CONSTRAINT ck_event_outbox_state_fields,
    DROP CONSTRAINT ck_event_outbox_available_at,
    DROP CONSTRAINT ck_event_outbox_attempts,
    DROP CONSTRAINT ck_event_outbox_status,
    DROP COLUMN alerted_at,
    DROP COLUMN dead_lettered_at,
    DROP COLUMN last_error_category,
    DROP COLUMN claimed_at,
    DROP COLUMN lease_expires_at,
    DROP COLUMN worker_id,
    DROP COLUMN lease_id,
    DROP COLUMN max_attempts;

CREATE INDEX ix_event_outbox_pending
    ON domain_event_outbox (status, available_at);
