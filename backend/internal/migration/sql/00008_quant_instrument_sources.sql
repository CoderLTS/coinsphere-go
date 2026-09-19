-- +goose Up

CREATE TABLE plugin_quant.instrument_sources (
    workflow_id BIGINT NOT NULL REFERENCES workflows (id) ON DELETE CASCADE,
    market VARCHAR(8) NOT NULL,
    symbol VARCHAR(32) NOT NULL,
    synced_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (workflow_id, market, symbol),
    CONSTRAINT fk_quant_instrument_source_instrument
        FOREIGN KEY (market, symbol)
        REFERENCES plugin_quant.instruments (market, symbol)
        ON DELETE CASCADE
);
CREATE INDEX ix_quant_instrument_sources_instrument
    ON plugin_quant.instrument_sources (market, symbol);

-- +goose Down

LOCK TABLE plugin_quant.instrument_sources IN ACCESS EXCLUSIVE MODE;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM plugin_quant.instrument_sources LIMIT 1)
    THEN
        RAISE EXCEPTION 'refusing to roll back Quant instrument source data';
    END IF;
END
$$;
-- +goose StatementEnd

DROP TABLE plugin_quant.instrument_sources;
