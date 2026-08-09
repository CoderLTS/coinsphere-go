-- +goose Up

ALTER TABLE testnet_orders
    ADD COLUMN recovered_at TIMESTAMPTZ,
    ADD CONSTRAINT ck_testnet_orders_recovered_at CHECK (
        recovered_at IS NULL OR isfinite(recovered_at)
    );

-- +goose Down
LOCK TABLE testnet_orders IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE m3_testnet_external_order_recovery_down_guard (
    row_count BIGINT NOT NULL CHECK (row_count = 0)
) ON COMMIT DROP;

INSERT INTO m3_testnet_external_order_recovery_down_guard (row_count)
SELECT COUNT(*) FROM testnet_orders WHERE recovered_at IS NOT NULL;

ALTER TABLE testnet_orders
    DROP CONSTRAINT ck_testnet_orders_recovered_at,
    DROP COLUMN recovered_at;
