-- +goose Up
ALTER TABLE worker_tasks
    ADD COLUMN lane VARCHAR(16) NOT NULL DEFAULT 'realtime',
    ADD COLUMN priority INTEGER NOT NULL DEFAULT 0;

ALTER TABLE worker_tasks
    ADD CONSTRAINT ck_worker_tasks_lane
        CHECK (lane IN ('realtime', 'backtest')),
    ADD CONSTRAINT ck_worker_tasks_priority
        CHECK (priority >= 0);

CREATE INDEX ix_worker_tasks_lane_claim
    ON worker_tasks (lane, status, priority DESC, queued_at, id);

-- +goose Down
LOCK TABLE worker_tasks IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE m1_4_strategy_backtest_down_guard (
    row_count BIGINT NOT NULL CHECK (row_count = 0)
) ON COMMIT DROP;

INSERT INTO m1_4_strategy_backtest_down_guard (row_count)
SELECT COUNT(*)
FROM worker_tasks
WHERE lane <> 'realtime' OR priority <> 0;

DROP INDEX ix_worker_tasks_lane_claim;
ALTER TABLE worker_tasks
    DROP CONSTRAINT ck_worker_tasks_priority,
    DROP CONSTRAINT ck_worker_tasks_lane,
    DROP COLUMN priority,
    DROP COLUMN lane;
