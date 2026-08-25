-- +goose Up

CREATE TABLE contract_runs (
    operation_key TEXT PRIMARY KEY,
    output JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- +goose Down

DROP TABLE contract_runs;
