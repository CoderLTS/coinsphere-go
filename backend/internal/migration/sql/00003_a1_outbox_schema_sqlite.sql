-- +goose Up
-- A1-3 只建立可靠投递所需的数据契约；原子认领、续租、恢复、退避、死信告警由后续运行时 PR 实现。
-- 空 migration 数据库尚无 GORM 业务表，因此先建立与现有模型兼容的 Outbox 基表。SQLite 允许父表稍后创建，
-- 既有 GORM 数据库则由 IF NOT EXISTS 原样保留表及 notification_deliveries 的入站外键，禁止重建造成引用丢失。
CREATE TABLE IF NOT EXISTS domain_event_outbox (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type VARCHAR(120),
    aggregate_type VARCHAR(120),
    aggregate_id VARCHAR(120),
    workflow_execution_id INTEGER,
    workflow_execution_node_id INTEGER,
    payload_json TEXT,
    metadata_json TEXT,
    status VARCHAR(20) DEFAULT 'pending',
    attempt_count INTEGER DEFAULT 0,
    available_at DATETIME,
    processed_at DATETIME,
    last_error_message TEXT,
    created_at DATETIME,
    updated_at DATETIME,
    -- 约束名与 FOREIGN KEY 必须保持同一行：A1-10 前的 GORM 依赖 sqlite_master.sql 文本识别命名约束；
    -- 若在两者之间换行会误判约束缺失并重建表，从而删除本 migration 的触发器与专用索引。
    CONSTRAINT fk_domain_event_outbox_workflow_execution FOREIGN KEY (workflow_execution_id) REFERENCES workflow_executions(id) ON DELETE SET NULL,
    CONSTRAINT fk_domain_event_outbox_workflow_execution_node FOREIGN KEY (workflow_execution_node_id) REFERENCES workflow_execution_nodes(id) ON DELETE SET NULL
);

-- 每个 ADD COLUMN 都在 Goose 的单文件事务内执行。任一步或后续存量校验失败时，全部新增列与版本记录一并回滚。
ALTER TABLE domain_event_outbox ADD COLUMN max_attempts INTEGER NOT NULL DEFAULT 3;
ALTER TABLE domain_event_outbox ADD COLUMN lease_id VARCHAR(36);
ALTER TABLE domain_event_outbox ADD COLUMN worker_id VARCHAR(120);
ALTER TABLE domain_event_outbox ADD COLUMN lease_expires_at DATETIME;
ALTER TABLE domain_event_outbox ADD COLUMN claimed_at DATETIME;
ALTER TABLE domain_event_outbox ADD COLUMN last_error_category VARCHAR(64);
ALTER TABLE domain_event_outbox ADD COLUMN dead_lettered_at DATETIME;
ALTER TABLE domain_event_outbox ADD COLUMN alerted_at DATETIME;

-- 旧运行时只产生 pending/processed/failed。只修复可无歧义推导的 NULL 与默认值；未知状态、负次数、
-- 缺少终态时间等异常数据由下方 guard 拒绝，避免 migration 猜测业务语义。
UPDATE domain_event_outbox SET attempt_count = 0 WHERE attempt_count IS NULL;
UPDATE domain_event_outbox
SET max_attempts = CASE WHEN attempt_count > 3 THEN attempt_count ELSE 3 END;

-- 临时 guard 把存量检查置于同一事务中。INSERT 失败会回滚上方所有 DDL/DML，版本继续停留在 00002。
CREATE TEMPORARY TABLE outbox_v3_up_guard (
    invalid_count INTEGER NOT NULL CHECK (invalid_count = 0)
);

INSERT INTO outbox_v3_up_guard (invalid_count)
SELECT COUNT(*)
FROM domain_event_outbox
WHERE status IS NULL
   OR status NOT IN ('pending', 'processed', 'failed')
   OR attempt_count IS NULL
   OR attempt_count < 0
   OR max_attempts <= 0
   OR attempt_count > max_attempts
   OR available_at IS NULL
   OR (status = 'pending' AND (attempt_count >= max_attempts OR processed_at IS NOT NULL))
   OR (status IN ('processed', 'failed') AND processed_at IS NULL);

DROP TABLE outbox_v3_up_guard;

-- SQLite 不能给既有表追加命名 CHECK，使用对 INSERT/UPDATE 完全相同的 BEFORE 触发器实现约束。
-- 每个条件显式处理 NULL，避免 SQLite 三值逻辑把未知值误判为合法。
-- +goose StatementBegin
CREATE TRIGGER ck_event_outbox_contract_insert
BEFORE INSERT ON domain_event_outbox
FOR EACH ROW
WHEN NEW.status IS NULL
  OR NEW.status NOT IN ('pending', 'claimed', 'processed', 'failed', 'dead_letter')
  OR NEW.attempt_count IS NULL
  OR NEW.max_attempts IS NULL
  OR NEW.attempt_count < 0
  OR NEW.max_attempts <= 0
  OR NEW.attempt_count > NEW.max_attempts
  OR NEW.available_at IS NULL
  OR (
      NEW.status = 'pending'
      AND (
          NEW.attempt_count >= NEW.max_attempts
          OR NEW.processed_at IS NOT NULL
          OR NEW.lease_id IS NOT NULL
          OR NEW.worker_id IS NOT NULL
          OR NEW.lease_expires_at IS NOT NULL
          OR NEW.claimed_at IS NOT NULL
          OR NEW.dead_lettered_at IS NOT NULL
          OR NEW.alerted_at IS NOT NULL
      )
  )
  OR (
      NEW.status = 'claimed'
      AND (
          NEW.attempt_count <= 0
          OR NEW.processed_at IS NOT NULL
          OR NEW.lease_id IS NULL
          OR NEW.worker_id IS NULL
          OR NEW.lease_expires_at IS NULL
          OR NEW.claimed_at IS NULL
          OR NEW.lease_expires_at <= NEW.claimed_at
          OR NEW.dead_lettered_at IS NOT NULL
          OR NEW.alerted_at IS NOT NULL
      )
  )
  OR (
      NEW.status IN ('processed', 'failed')
      AND (
          NEW.processed_at IS NULL
          OR NEW.lease_id IS NOT NULL
          OR NEW.worker_id IS NOT NULL
          OR NEW.lease_expires_at IS NOT NULL
          OR NEW.claimed_at IS NOT NULL
          OR NEW.dead_lettered_at IS NOT NULL
          OR NEW.alerted_at IS NOT NULL
      )
  )
  OR (
      NEW.status = 'dead_letter'
      AND (
          NEW.attempt_count <> NEW.max_attempts
          OR NEW.processed_at IS NULL
          OR NEW.lease_id IS NOT NULL
          OR NEW.worker_id IS NOT NULL
          OR NEW.lease_expires_at IS NOT NULL
          OR NEW.claimed_at IS NOT NULL
          OR NEW.dead_lettered_at IS NULL
          OR NEW.dead_lettered_at <> NEW.processed_at
          OR (NEW.alerted_at IS NOT NULL AND NEW.alerted_at < NEW.dead_lettered_at)
      )
  )
BEGIN
    SELECT RAISE(ABORT, 'domain_event_outbox contract violation');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER ck_event_outbox_contract_update
BEFORE UPDATE ON domain_event_outbox
FOR EACH ROW
WHEN NEW.status IS NULL
  OR NEW.status NOT IN ('pending', 'claimed', 'processed', 'failed', 'dead_letter')
  OR NEW.attempt_count IS NULL
  OR NEW.max_attempts IS NULL
  OR NEW.attempt_count < 0
  OR NEW.max_attempts <= 0
  OR NEW.attempt_count > NEW.max_attempts
  OR NEW.available_at IS NULL
  OR (
      NEW.status = 'pending'
      AND (
          NEW.attempt_count >= NEW.max_attempts
          OR NEW.processed_at IS NOT NULL
          OR NEW.lease_id IS NOT NULL
          OR NEW.worker_id IS NOT NULL
          OR NEW.lease_expires_at IS NOT NULL
          OR NEW.claimed_at IS NOT NULL
          OR NEW.dead_lettered_at IS NOT NULL
          OR NEW.alerted_at IS NOT NULL
      )
  )
  OR (
      NEW.status = 'claimed'
      AND (
          NEW.attempt_count <= 0
          OR NEW.processed_at IS NOT NULL
          OR NEW.lease_id IS NULL
          OR NEW.worker_id IS NULL
          OR NEW.lease_expires_at IS NULL
          OR NEW.claimed_at IS NULL
          OR NEW.lease_expires_at <= NEW.claimed_at
          OR NEW.dead_lettered_at IS NOT NULL
          OR NEW.alerted_at IS NOT NULL
      )
  )
  OR (
      NEW.status IN ('processed', 'failed')
      AND (
          NEW.processed_at IS NULL
          OR NEW.lease_id IS NOT NULL
          OR NEW.worker_id IS NOT NULL
          OR NEW.lease_expires_at IS NOT NULL
          OR NEW.claimed_at IS NOT NULL
          OR NEW.dead_lettered_at IS NOT NULL
          OR NEW.alerted_at IS NOT NULL
      )
  )
  OR (
      NEW.status = 'dead_letter'
      AND (
          NEW.attempt_count <> NEW.max_attempts
          OR NEW.processed_at IS NULL
          OR NEW.lease_id IS NOT NULL
          OR NEW.worker_id IS NOT NULL
          OR NEW.lease_expires_at IS NOT NULL
          OR NEW.claimed_at IS NOT NULL
          OR NEW.dead_lettered_at IS NULL
          OR NEW.dead_lettered_at <> NEW.processed_at
          OR (NEW.alerted_at IS NOT NULL AND NEW.alerted_at < NEW.dead_lettered_at)
      )
  )
BEGIN
    SELECT RAISE(ABORT, 'domain_event_outbox contract violation');
END;
-- +goose StatementEnd

-- 认领、过期恢复和死信告警分别有稳定扫描顺序；nullable 唯一索引让每次租约成为 fencing token。
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
-- Down 只允许完全空的 Outbox。先改名取得 SQLite schema 写锁，再计数；并发写者要么先提交并被计数，
-- 要么等待本事务结束。guard 失败时改名、索引、触发器、列和 migration 版本都会原子恢复。
ALTER TABLE domain_event_outbox RENAME TO domain_event_outbox_v3_down_candidate;

CREATE TEMPORARY TABLE outbox_v3_down_guard (
    event_count INTEGER NOT NULL CHECK (event_count = 0)
);

INSERT INTO outbox_v3_down_guard (event_count)
SELECT COUNT(*) FROM domain_event_outbox_v3_down_candidate;

DROP TABLE outbox_v3_down_guard;
DROP TRIGGER ck_event_outbox_contract_insert;
DROP TRIGGER ck_event_outbox_contract_update;
DROP INDEX ix_event_outbox_pending;
DROP INDEX ix_event_outbox_recovery;
DROP INDEX ux_event_outbox_lease_id;
DROP INDEX ix_event_outbox_dead_letter_alert;
DROP INDEX ix_event_outbox_terminal_retention;

ALTER TABLE domain_event_outbox_v3_down_candidate DROP COLUMN alerted_at;
ALTER TABLE domain_event_outbox_v3_down_candidate DROP COLUMN dead_lettered_at;
ALTER TABLE domain_event_outbox_v3_down_candidate DROP COLUMN last_error_category;
ALTER TABLE domain_event_outbox_v3_down_candidate DROP COLUMN claimed_at;
ALTER TABLE domain_event_outbox_v3_down_candidate DROP COLUMN lease_expires_at;
ALTER TABLE domain_event_outbox_v3_down_candidate DROP COLUMN worker_id;
ALTER TABLE domain_event_outbox_v3_down_candidate DROP COLUMN lease_id;
ALTER TABLE domain_event_outbox_v3_down_candidate DROP COLUMN max_attempts;

ALTER TABLE domain_event_outbox_v3_down_candidate RENAME TO domain_event_outbox;
CREATE INDEX ix_event_outbox_pending
    ON domain_event_outbox (status, available_at);
