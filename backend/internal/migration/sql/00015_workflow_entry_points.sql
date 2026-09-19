-- +goose Up

ALTER TABLE workflow_runs
    ADD COLUMN entry_point VARCHAR(32) NOT NULL DEFAULT 'realtime',
    ADD COLUMN input_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD CONSTRAINT ck_workflow_runs_entry_point CHECK (entry_point IN ('realtime', 'backtest')),
    ADD CONSTRAINT ck_workflow_runs_input CHECK (jsonb_typeof(input_json) = 'object');

-- +goose Down

ALTER TABLE workflow_runs
    DROP CONSTRAINT ck_workflow_runs_input,
    DROP CONSTRAINT ck_workflow_runs_entry_point,
    DROP COLUMN input_json,
    DROP COLUMN entry_point;
