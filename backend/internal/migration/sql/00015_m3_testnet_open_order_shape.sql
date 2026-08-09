-- +goose Up

ALTER TABLE testnet_open_orders
    DROP CONSTRAINT ck_testnet_open_orders_values,
    ADD COLUMN close_position BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN reduce_only BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN working_type VARCHAR(24) NOT NULL DEFAULT '',
    ADD CONSTRAINT ck_testnet_open_orders_values CHECK (
        price >= 0 AND stop_price >= 0
        AND (
            (close_position AND original_quantity = 0 AND executed_quantity = 0)
            OR (
                NOT close_position
                AND original_quantity > 0
                AND executed_quantity >= 0
                AND executed_quantity <= original_quantity
            )
        )
    );

-- +goose Down
LOCK TABLE testnet_open_orders IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE m3_testnet_open_order_shape_down_guard (
    row_count BIGINT NOT NULL CHECK (row_count = 0)
) ON COMMIT DROP;

INSERT INTO m3_testnet_open_order_shape_down_guard (row_count)
SELECT COUNT(*)
FROM testnet_open_orders
WHERE close_position OR reduce_only OR working_type <> '' OR original_quantity <= 0;

ALTER TABLE testnet_open_orders
    DROP CONSTRAINT ck_testnet_open_orders_values,
    DROP COLUMN working_type,
    DROP COLUMN reduce_only,
    DROP COLUMN close_position,
    ADD CONSTRAINT ck_testnet_open_orders_values CHECK (
        price >= 0 AND stop_price >= 0
        AND original_quantity > 0
        AND executed_quantity >= 0 AND executed_quantity <= original_quantity
    );
