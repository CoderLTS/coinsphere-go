-- +goose Up

-- 00006-00013 were already released with Binance data under plugin_quant.
-- Move those owned tables once, then keep Quant facts in plugin_quant.
CREATE SCHEMA plugin_binance;
ALTER TABLE plugin_quant.instruments SET SCHEMA plugin_binance;
ALTER TABLE plugin_quant.candles SET SCHEMA plugin_binance;
ALTER TABLE plugin_quant.instrument_sources SET SCHEMA plugin_binance;

ALTER TABLE plugin_quant.backtests
    ADD COLUMN venue VARCHAR(32) NOT NULL DEFAULT 'binance';
ALTER TABLE plugin_quant.market_signals
    ADD COLUMN venue VARCHAR(32) NOT NULL DEFAULT 'binance';
ALTER TABLE plugin_quant.signals
    ADD COLUMN venue VARCHAR(32) NOT NULL DEFAULT 'binance';

CREATE TABLE plugin_binance.orders (
    id BIGSERIAL PRIMARY KEY,
    workflow_id BIGINT NOT NULL,
    node_instance_id VARCHAR(128) NOT NULL,
    account VARCHAR(128) NOT NULL,
    market VARCHAR(8) NOT NULL,
    instrument VARCHAR(32) NOT NULL,
    provider_order_id VARCHAR(128) NOT NULL DEFAULT '',
    client_order_id VARCHAR(36) NOT NULL UNIQUE,
    side VARCHAR(4) NOT NULL,
    request_quantity NUMERIC(38,18) NOT NULL,
    request_quote_amount NUMERIC(38,18) NOT NULL,
    position_effect VARCHAR(8) NOT NULL,
    quantity NUMERIC(38,18) NOT NULL,
    executed NUMERIC(38,18) NOT NULL DEFAULT 0,
    average_price NUMERIC(38,18) NOT NULL DEFAULT 0,
    notional NUMERIC(38,18) NOT NULL DEFAULT 0,
    status VARCHAR(32) NOT NULL,
    mode VARCHAR(8) NOT NULL,
    operation_key VARCHAR(64) NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_binance_order_identity CHECK (
        BTRIM(node_instance_id) <> '' AND account ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$'
        AND BTRIM(instrument) <> '' AND instrument = UPPER(instrument)
        AND client_order_id ~ '^[A-Za-z0-9._:/-]{1,36}$' AND BTRIM(operation_key) <> ''
        AND BTRIM(status) <> ''
    ),
    CONSTRAINT ck_binance_order_market CHECK (market IN ('spot', 'usdm')),
    CONSTRAINT ck_binance_order_side CHECK (side IN ('buy', 'sell')),
    CONSTRAINT ck_binance_order_request CHECK (
        (request_quantity > 0 AND request_quote_amount = 0)
        OR (request_quantity = 0 AND request_quote_amount > 0)
    ),
    CONSTRAINT ck_binance_order_position_effect CHECK (position_effect IN ('open', 'reduce')),
    CONSTRAINT ck_binance_order_mode CHECK (mode IN ('paper', 'live')),
    CONSTRAINT ck_binance_order_amounts CHECK (
        quantity >= 0 AND executed >= 0 AND (quantity = 0 OR executed <= quantity)
        AND average_price >= 0 AND notional >= 0
    ),
    CONSTRAINT ck_binance_order_time CHECK (updated_at >= created_at)
);
CREATE INDEX ix_binance_orders_scope
    ON plugin_binance.orders (workflow_id, node_instance_id, created_at DESC, id DESC);
CREATE INDEX ix_binance_orders_account
    ON plugin_binance.orders (account, mode, market, created_at DESC, id DESC);

CREATE TABLE plugin_binance.fills (
    id BIGSERIAL PRIMARY KEY,
    order_id BIGINT NOT NULL REFERENCES plugin_binance.orders (id) ON DELETE RESTRICT,
    provider_trade_id VARCHAR(128) NOT NULL UNIQUE,
    quantity NUMERIC(38,18) NOT NULL,
    price NUMERIC(38,18) NOT NULL,
    fee NUMERIC(38,18) NOT NULL,
    fee_asset VARCHAR(32) NOT NULL,
    filled_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT ck_binance_fill_identity CHECK (BTRIM(provider_trade_id) <> ''),
    CONSTRAINT ck_binance_fill_amounts CHECK (quantity > 0 AND price > 0 AND fee >= 0),
    CONSTRAINT ck_binance_fill_fee_asset CHECK (fee = 0 OR BTRIM(fee_asset) <> '')
);
CREATE INDEX ix_binance_fills_order ON plugin_binance.fills (order_id, filled_at, id);

CREATE TABLE plugin_binance.fees (
    id BIGSERIAL PRIMARY KEY,
    fill_id BIGINT NOT NULL UNIQUE REFERENCES plugin_binance.fills (id) ON DELETE RESTRICT,
    amount NUMERIC(38,18) NOT NULL,
    asset VARCHAR(32) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_binance_fee_amount CHECK (amount >= 0),
    CONSTRAINT ck_binance_fee_asset CHECK (amount = 0 OR BTRIM(asset) <> '')
);

CREATE TABLE plugin_binance.paper_ledger_entries (
    id BIGSERIAL PRIMARY KEY,
    account VARCHAR(128) NOT NULL,
    operation_key VARCHAR(64) NOT NULL,
    entry_type VARCHAR(32) NOT NULL,
    amount NUMERIC(38,18) NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT ux_binance_paper_ledger_operation UNIQUE (operation_key, entry_type),
    CONSTRAINT ck_binance_paper_ledger_identity CHECK (
        account ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$' AND BTRIM(operation_key) <> ''
    ),
    CONSTRAINT ck_binance_paper_ledger_type CHECK (entry_type IN ('opening_balance', 'trade', 'fee'))
);
CREATE INDEX ix_binance_paper_ledger_account
    ON plugin_binance.paper_ledger_entries (account, occurred_at, id);

CREATE TABLE plugin_binance.positions (
    account VARCHAR(128) NOT NULL,
    mode VARCHAR(8) NOT NULL,
    market VARCHAR(8) NOT NULL,
    instrument VARCHAR(32) NOT NULL,
    quantity NUMERIC(38,18) NOT NULL,
    average_price NUMERIC(38,18) NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (account, mode, market, instrument),
    CONSTRAINT ck_binance_position_identity CHECK (
        account ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$'
        AND BTRIM(instrument) <> '' AND instrument = UPPER(instrument)
    ),
    CONSTRAINT ck_binance_position_mode CHECK (mode IN ('paper', 'live')),
    CONSTRAINT ck_binance_position_market CHECK (market IN ('spot', 'usdm')),
    CONSTRAINT ck_binance_position_price CHECK (average_price >= 0)
);

CREATE TABLE plugin_binance.account_snapshots (
    id BIGSERIAL PRIMARY KEY,
    account VARCHAR(128) NOT NULL,
    market VARCHAR(8) NOT NULL,
    asset VARCHAR(32) NOT NULL,
    equity NUMERIC(38,18) NOT NULL,
    available NUMERIC(38,18) NOT NULL,
    captured_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT ck_binance_snapshot_identity CHECK (
        account ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$'
        AND BTRIM(asset) <> '' AND asset = UPPER(asset)
    ),
    CONSTRAINT ck_binance_snapshot_market CHECK (market IN ('spot', 'usdm'))
);
CREATE INDEX ix_binance_account_snapshots_scope
    ON plugin_binance.account_snapshots (account, market, asset, captured_at DESC, id DESC);

CREATE TABLE plugin_binance.live_account_releases (
    account VARCHAR(128) NOT NULL,
    market VARCHAR(8) NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    confirmed_by BIGINT NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    confirmed_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (account, market),
    CONSTRAINT ck_binance_live_release_identity CHECK (
        account ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$'
    ),
    CONSTRAINT ck_binance_live_release_market CHECK (market IN ('spot', 'usdm')),
    CONSTRAINT ck_binance_live_release_time CHECK (updated_at >= confirmed_at)
);

LOCK TABLE workflows, workflow_revisions, workflow_secret_bindings IN ACCESS EXCLUSIVE MODE;

-- +goose StatementBegin
DO $$
DECLARE
    target RECORD;
    old_revision workflow_revisions%ROWTYPE;
    next_revision_number BIGINT;
    new_revision_id BIGINT;
    new_graph JSONB;
    new_node_versions JSONB;
BEGIN
    FOR target IN
        SELECT workflow.id AS workflow_id
        FROM workflows workflow
        JOIN workflow_revisions revision ON revision.id = workflow.active_revision_id
        WHERE EXISTS (
            SELECT 1
            FROM jsonb_array_elements(revision.graph_json -> 'nodes') node(value)
            WHERE node.value ->> 'nodeType' IN (
                'official.quant.realtime_candles',
                'official.quant.backfill_candles',
                'official.quant.sync_instruments'
            )
        )
        ORDER BY workflow.id
    LOOP
        SELECT revision.*
        INTO old_revision
        FROM workflows workflow
        JOIN workflow_revisions revision ON revision.id = workflow.active_revision_id
        WHERE workflow.id = target.workflow_id
        FOR UPDATE OF workflow;

        IF jsonb_typeof(old_revision.graph_json -> 'nodes') <> 'array'
            OR jsonb_typeof(old_revision.node_versions) <> 'object'
        THEN
            RAISE EXCEPTION 'workflow % has an invalid graph or node version map', target.workflow_id;
        END IF;

        SELECT jsonb_set(
            old_revision.graph_json,
            '{nodes}',
            jsonb_agg(
                CASE node.value ->> 'nodeType'
                    WHEN 'official.quant.realtime_candles' THEN
                        jsonb_set(node.value, '{nodeType}', to_jsonb('official.binance.realtime_candles'::TEXT))
                    WHEN 'official.quant.backfill_candles' THEN
                        jsonb_set(node.value, '{nodeType}', to_jsonb('official.binance.backfill_candles'::TEXT))
                    WHEN 'official.quant.sync_instruments' THEN
                        jsonb_set(node.value, '{nodeType}', to_jsonb('official.binance.sync_instruments'::TEXT))
                    ELSE node.value
                END
                ORDER BY node.ordinality
            )
        )
        INTO new_graph
        FROM jsonb_array_elements(old_revision.graph_json -> 'nodes') WITH ORDINALITY node(value, ordinality);

        SELECT COALESCE(jsonb_object_agg(entry.key,
            CASE entry.value ->> 'nodeType'
                WHEN 'official.quant.realtime_candles' THEN
                    jsonb_set(entry.value, '{nodeType}', to_jsonb('official.binance.realtime_candles'::TEXT))
                WHEN 'official.quant.backfill_candles' THEN
                    jsonb_set(entry.value, '{nodeType}', to_jsonb('official.binance.backfill_candles'::TEXT))
                WHEN 'official.quant.sync_instruments' THEN
                    jsonb_set(entry.value, '{nodeType}', to_jsonb('official.binance.sync_instruments'::TEXT))
                ELSE entry.value
            END
        ), '{}'::JSONB)
        INTO new_node_versions
        FROM jsonb_each(old_revision.node_versions) entry;

        SELECT COALESCE(MAX(revision_number), 0) + 1
        INTO next_revision_number
        FROM workflow_revisions
        WHERE workflow_id = target.workflow_id;

        INSERT INTO workflow_revisions (
            workflow_id, revision_number, graph_json, node_versions,
            main_trigger_node_id, created_by, created_at
        ) VALUES (
            target.workflow_id,
            next_revision_number,
            new_graph,
            new_node_versions,
            old_revision.main_trigger_node_id,
            old_revision.created_by,
            CURRENT_TIMESTAMP
        ) RETURNING id INTO new_revision_id;

        INSERT INTO workflow_secret_bindings (
            revision_id, workflow_id, node_instance_id, field_name, encrypted_value, created_at
        )
        SELECT
            new_revision_id, workflow_id, node_instance_id, field_name, encrypted_value, CURRENT_TIMESTAMP
        FROM workflow_secret_bindings
        WHERE revision_id = old_revision.id;

        UPDATE workflows
        SET active_revision_id = new_revision_id, updated_at = CURRENT_TIMESTAMP
        WHERE id = target.workflow_id AND active_revision_id = old_revision.id;

        IF NOT FOUND THEN
            RAISE EXCEPTION 'workflow % active revision changed during Quant node conversion', target.workflow_id;
        END IF;
    END LOOP;
END
$$;
-- +goose StatementEnd

INSERT INTO plugin_installations (plugin_id, version, schema_name, source_path, status)
VALUES
    ('official.ai', '3.0.0', 'plugin_official_ai', 'builtin', 'installed'),
    ('official.binance', '3.0.0', 'plugin_binance', 'builtin', 'installed'),
    ('official.connector', '3.0.0', 'plugin_official_connector', 'builtin', 'installed'),
    ('official.notification', '3.0.0', 'plugin_notification', 'builtin', 'installed'),
    ('official.qq', '3.0.0', 'plugin_official_qq', 'builtin', 'installed'),
    ('official.quant', '3.0.0', 'plugin_quant', 'builtin', 'installed')
ON CONFLICT (plugin_id) DO UPDATE SET
    version = EXCLUDED.version,
    schema_name = EXCLUDED.schema_name,
    source_path = EXCLUDED.source_path,
    status = 'installed',
    updated_at = CURRENT_TIMESTAMP;

-- +goose Down

LOCK TABLE
    workflows,
    workflow_revisions,
    workflow_secret_bindings,
    workflow_runs,
    plugin_binance.orders,
    plugin_binance.fills,
    plugin_binance.fees,
    plugin_binance.paper_ledger_entries,
    plugin_binance.positions,
    plugin_binance.account_snapshots,
    plugin_binance.live_account_releases,
    plugin_binance.instruments,
    plugin_binance.candles,
    plugin_binance.instrument_sources,
    plugin_quant.backtests,
    plugin_quant.market_signals,
    plugin_quant.signals
IN ACCESS EXCLUSIVE MODE;

-- +goose StatementBegin
DO $$
DECLARE
    unsupported_count BIGINT;
BEGIN
    CREATE TEMP TABLE quant_binance_revision_rollback ON COMMIT DROP AS
    SELECT
        workflow.id AS workflow_id,
        current_revision.id AS current_revision_id,
        previous_revision.id AS previous_revision_id
    FROM workflows workflow
    JOIN workflow_revisions current_revision ON current_revision.id = workflow.active_revision_id
    JOIN workflow_revisions previous_revision
      ON previous_revision.workflow_id = current_revision.workflow_id
     AND previous_revision.revision_number = current_revision.revision_number - 1
    WHERE EXISTS (
        SELECT 1
        FROM jsonb_array_elements(current_revision.graph_json -> 'nodes') node(value)
        WHERE node.value ->> 'nodeType' IN (
            'official.binance.realtime_candles',
            'official.binance.backfill_candles',
            'official.binance.sync_instruments'
        )
    )
      AND previous_revision.graph_json = (
          SELECT jsonb_set(
              current_revision.graph_json,
              '{nodes}',
              jsonb_agg(
                  CASE node.value ->> 'nodeType'
                      WHEN 'official.binance.realtime_candles' THEN
                          jsonb_set(node.value, '{nodeType}', to_jsonb('official.quant.realtime_candles'::TEXT))
                      WHEN 'official.binance.backfill_candles' THEN
                          jsonb_set(node.value, '{nodeType}', to_jsonb('official.quant.backfill_candles'::TEXT))
                      WHEN 'official.binance.sync_instruments' THEN
                          jsonb_set(node.value, '{nodeType}', to_jsonb('official.quant.sync_instruments'::TEXT))
                      ELSE node.value
                  END
                  ORDER BY node.ordinality
              )
          )
          FROM jsonb_array_elements(current_revision.graph_json -> 'nodes') WITH ORDINALITY node(value, ordinality)
      )
      AND previous_revision.node_versions = (
          SELECT COALESCE(jsonb_object_agg(entry.key,
              CASE entry.value ->> 'nodeType'
                  WHEN 'official.binance.realtime_candles' THEN
                      jsonb_set(entry.value, '{nodeType}', to_jsonb('official.quant.realtime_candles'::TEXT))
                  WHEN 'official.binance.backfill_candles' THEN
                      jsonb_set(entry.value, '{nodeType}', to_jsonb('official.quant.backfill_candles'::TEXT))
                  WHEN 'official.binance.sync_instruments' THEN
                      jsonb_set(entry.value, '{nodeType}', to_jsonb('official.quant.sync_instruments'::TEXT))
                  ELSE entry.value
              END
          ), '{}'::JSONB)
          FROM jsonb_each(current_revision.node_versions) entry
      );

    SELECT COUNT(*)
    INTO unsupported_count
    FROM workflows workflow
    JOIN workflow_revisions revision ON revision.id = workflow.active_revision_id
    WHERE EXISTS (
        SELECT 1
        FROM jsonb_array_elements(revision.graph_json -> 'nodes') node(value)
        WHERE node.value ->> 'nodeType' IN (
            'official.binance.realtime_candles',
            'official.binance.backfill_candles',
            'official.binance.sync_instruments'
        )
    )
      AND revision.id NOT IN (SELECT current_revision_id FROM quant_binance_revision_rollback);

    IF unsupported_count > 0 THEN
        RAISE EXCEPTION 'refusing to roll back workflows with Binance market nodes not created by this migration';
    END IF;
    IF EXISTS (
        SELECT 1 FROM workflow_runs run
        WHERE run.revision_id IN (SELECT current_revision_id FROM quant_binance_revision_rollback)
    ) THEN
        RAISE EXCEPTION 'refusing to remove a Binance market workflow revision with run history';
    END IF;

    UPDATE workflows workflow
    SET active_revision_id = rollback.previous_revision_id, updated_at = CURRENT_TIMESTAMP
    FROM quant_binance_revision_rollback rollback
    WHERE workflow.id = rollback.workflow_id
      AND workflow.active_revision_id = rollback.current_revision_id;

    ALTER TABLE workflow_secret_bindings DISABLE TRIGGER trg_workflow_secret_bindings_immutable;
    DELETE FROM workflow_secret_bindings
    WHERE revision_id IN (SELECT current_revision_id FROM quant_binance_revision_rollback);
    ALTER TABLE workflow_secret_bindings ENABLE TRIGGER trg_workflow_secret_bindings_immutable;

    ALTER TABLE workflow_revisions DISABLE TRIGGER trg_workflow_revisions_immutable;
    DELETE FROM workflow_revisions
    WHERE id IN (SELECT current_revision_id FROM quant_binance_revision_rollback);
    ALTER TABLE workflow_revisions ENABLE TRIGGER trg_workflow_revisions_immutable;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM plugin_binance.orders LIMIT 1)
        OR EXISTS (SELECT 1 FROM plugin_binance.fills LIMIT 1)
        OR EXISTS (SELECT 1 FROM plugin_binance.fees LIMIT 1)
        OR EXISTS (SELECT 1 FROM plugin_binance.paper_ledger_entries LIMIT 1)
        OR EXISTS (SELECT 1 FROM plugin_binance.positions LIMIT 1)
        OR EXISTS (SELECT 1 FROM plugin_binance.account_snapshots LIMIT 1)
        OR EXISTS (SELECT 1 FROM plugin_binance.live_account_releases LIMIT 1)
        OR EXISTS (SELECT 1 FROM plugin_binance.instruments LIMIT 1)
        OR EXISTS (SELECT 1 FROM plugin_binance.candles LIMIT 1)
        OR EXISTS (SELECT 1 FROM plugin_binance.instrument_sources LIMIT 1)
        OR EXISTS (SELECT 1 FROM plugin_quant.backtests LIMIT 1)
        OR EXISTS (SELECT 1 FROM plugin_quant.market_signals LIMIT 1)
        OR EXISTS (SELECT 1 FROM plugin_quant.signals LIMIT 1)
    THEN
        RAISE EXCEPTION 'refusing to roll back Quant or Binance data';
    END IF;
END
$$;
-- +goose StatementEnd

DELETE FROM plugin_installations WHERE source_path = 'builtin';

DROP TABLE plugin_binance.live_account_releases;
DROP TABLE plugin_binance.account_snapshots;
DROP TABLE plugin_binance.positions;
DROP TABLE plugin_binance.paper_ledger_entries;
DROP TABLE plugin_binance.fees;
DROP TABLE plugin_binance.fills;
DROP TABLE plugin_binance.orders;

ALTER TABLE plugin_binance.instrument_sources SET SCHEMA plugin_quant;
ALTER TABLE plugin_binance.candles SET SCHEMA plugin_quant;
ALTER TABLE plugin_binance.instruments SET SCHEMA plugin_quant;
ALTER TABLE plugin_quant.signals DROP COLUMN venue;
ALTER TABLE plugin_quant.market_signals DROP COLUMN venue;
ALTER TABLE plugin_quant.backtests DROP COLUMN venue;
DROP SCHEMA plugin_binance;
