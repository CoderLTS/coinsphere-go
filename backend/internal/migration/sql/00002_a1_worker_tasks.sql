-- +goose Up
CREATE TABLE worker_tasks (
    id VARCHAR(36) PRIMARY KEY,
    task_type VARCHAR(120) NOT NULL,
    payload_json TEXT NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'queued',
    attempt_count INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 3,
    lease_id VARCHAR(36),
    worker_id VARCHAR(120),
    lease_expires_at TIMESTAMPTZ,
    last_heartbeat_at TIMESTAMPTZ,
    cancel_requested_at TIMESTAMPTZ,
    queued_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    claimed_at TIMESTAMPTZ,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    result_json TEXT,
    failure_category VARCHAR(64),
    error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_worker_tasks_status CHECK (
        status IN ('queued', 'claimed', 'running', 'cancelRequested', 'succeeded', 'failed', 'canceled')
    ),
    CONSTRAINT ck_worker_tasks_attempts CHECK (
        attempt_count >= 0 AND max_attempts > 0 AND attempt_count <= max_attempts
    ),
    CONSTRAINT ck_worker_tasks_active_lease CHECK (
        (
            status IN ('claimed', 'running', 'cancelRequested')
            AND lease_id IS NOT NULL
            AND worker_id IS NOT NULL
            AND lease_expires_at IS NOT NULL
            AND last_heartbeat_at IS NOT NULL
        )
        OR
        (
            status IN ('queued', 'succeeded', 'failed', 'canceled')
            AND lease_id IS NULL
            AND worker_id IS NULL
            AND lease_expires_at IS NULL
            AND last_heartbeat_at IS NULL
        )
    ),
    CONSTRAINT ck_worker_tasks_cancel_timestamp CHECK (
        (status IN ('cancelRequested', 'canceled') AND cancel_requested_at IS NOT NULL)
        OR
        (status NOT IN ('cancelRequested', 'canceled') AND cancel_requested_at IS NULL)
    ),
    CONSTRAINT ck_worker_tasks_finished_at CHECK (
        (status IN ('succeeded', 'failed', 'canceled') AND finished_at IS NOT NULL)
        OR
        (status NOT IN ('succeeded', 'failed', 'canceled') AND finished_at IS NULL)
    ),
    CONSTRAINT ux_worker_tasks_lease_id UNIQUE (lease_id)
);

CREATE INDEX ix_worker_tasks_claim
    ON worker_tasks (status, queued_at, id);

CREATE INDEX ix_worker_tasks_recovery
    ON worker_tasks (status, lease_expires_at);

-- +goose Down
-- Renaming takes an exclusive schema lock before counting. Concurrent writers
-- either commit before the count or wait until this transaction finishes.
ALTER TABLE worker_tasks RENAME TO worker_tasks_v2_down_candidate;

-- Refuse to remove a queue containing tasks. The migration transaction restores
-- the original table name and schema version when the CHECK fails.
CREATE TEMPORARY TABLE worker_tasks_down_guard (
    task_count BIGINT NOT NULL CHECK (task_count = 0)
);

INSERT INTO worker_tasks_down_guard (task_count)
SELECT COUNT(*) FROM worker_tasks_v2_down_candidate;

DROP TABLE worker_tasks_down_guard;
DROP TABLE worker_tasks_v2_down_candidate;
