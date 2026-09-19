-- +goose Up

LOCK TABLE plugin_quant.backtests IN ACCESS EXCLUSIVE MODE;

TRUNCATE TABLE plugin_quant.backtests;

ALTER TABLE plugin_quant.backtests
    DROP CONSTRAINT ck_quant_backtest_digest,
    DROP CONSTRAINT ck_quant_backtest_amounts,
    DROP CONSTRAINT ck_quant_backtest_json,
    DROP COLUMN detail_sha256,
    DROP COLUMN detail_size_bytes,
    ADD COLUMN detail JSONB NOT NULL,
    ADD CONSTRAINT ck_quant_backtest_amounts CHECK (
        initial_capital > 0 AND final_equity >= 0 AND total_fees >= 0
        AND max_drawdown >= 0 AND max_drawdown <= 1
        AND trade_count >= 0 AND candle_count > 0
    ),
    ADD CONSTRAINT ck_quant_backtest_json CHECK (
        jsonb_typeof(parameters) = 'object' AND jsonb_typeof(data_manifest) = 'object'
        AND jsonb_typeof(detail) = 'object'
    );

-- +goose Down

-- +goose StatementBegin
DO $$
BEGIN
    RAISE EXCEPTION 'quant backtest detail migration cannot be rolled back; restore the pre-migration backup';
END
$$;
-- +goose StatementEnd
