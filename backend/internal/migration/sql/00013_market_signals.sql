-- +goose Up

CREATE TABLE plugin_quant.market_signals (
    id BIGSERIAL PRIMARY KEY,
    operation_key VARCHAR(64) NOT NULL UNIQUE,
    workflow_id BIGINT NOT NULL,
    revision_id BIGINT NOT NULL,
    node_instance_id VARCHAR(128) NOT NULL,
    market VARCHAR(8) NOT NULL,
    instrument VARCHAR(32) NOT NULL,
    interval VARCHAR(8) NOT NULL,
    name VARCHAR(80) NOT NULL,
    indicator VARCHAR(32) NOT NULL,
    candle_close_time TIMESTAMPTZ NOT NULL,
    summary VARCHAR(2000) NOT NULL,
    "values" JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_quant_market_signal_identity CHECK (
        BTRIM(node_instance_id) <> '' AND BTRIM(instrument) <> ''
        AND instrument = UPPER(instrument) AND BTRIM(interval) <> '' AND BTRIM(name) <> ''
    ),
    CONSTRAINT ck_quant_market_signal_market CHECK (market IN ('spot', 'usdm')),
    CONSTRAINT ck_quant_market_signal_indicator CHECK (
        indicator IN ('volume_spike', 'price_change', 'macd', 'kdj', 'rsi', 'bollinger')
    ),
    CONSTRAINT ck_quant_market_signal_values CHECK (jsonb_typeof("values") = 'object')
);

CREATE INDEX ix_quant_market_signals_scope
    ON plugin_quant.market_signals (market, instrument, interval, candle_close_time DESC, id DESC);

LOCK TABLE workflows, workflow_revisions, workflow_secret_bindings IN ACCESS EXCLUSIVE MODE;

-- +goose StatementBegin
DO $$
DECLARE
    target_count BIGINT;
    target_workflow_id BIGINT;
    old_revision workflow_revisions%ROWTYPE;
    next_revision_number BIGINT;
    new_revision_id BIGINT;
    new_graph JSONB;
BEGIN
    SELECT COUNT(*), MIN(w.id)
    INTO target_count, target_workflow_id
    FROM workflows w
    JOIN workflow_revisions revision ON revision.id = w.active_revision_id
    JOIN LATERAL jsonb_array_elements(revision.graph_json -> 'nodes') node ON TRUE
    WHERE w.status = 'active'
      AND node ->> 'nodeInstanceId' = 'price-condition'
      AND node ->> 'nodeType' = 'official.quant.price_change_condition'
      AND node ->> 'nodeVersion' = '1.0.0'
      AND node -> 'config' @> '{
        "market":"spot","instrument":"YGGUSDT","checkInterval":"1m",
        "name":"1分钟绝对涨跌幅达到1%","interval":"1m",
        "parameters":{"lookback":1,"mode":"absolute","threshold":"1"}
      }'::JSONB;

    IF target_count = 0 THEN
        RETURN;
    END IF;
    IF target_count <> 1 THEN
        RAISE EXCEPTION 'expected at most one active YGGUSDT 1m price-change workflow, found %', target_count;
    END IF;

    SELECT revision.*
    INTO old_revision
    FROM workflows w
    JOIN workflow_revisions revision ON revision.id = w.active_revision_id
    WHERE w.id = target_workflow_id
    FOR UPDATE OF w;

    IF jsonb_typeof(old_revision.graph_json -> 'nodes') <> 'array'
        OR jsonb_typeof(old_revision.graph_json -> 'edges') <> 'array'
        OR EXISTS (
            SELECT 1 FROM jsonb_array_elements(old_revision.graph_json -> 'nodes') node
            WHERE node ->> 'nodeInstanceId' = 'market-signal'
               OR node ->> 'nodeType' = 'official.quant.market_signal'
        )
        OR EXISTS (
            SELECT 1 FROM jsonb_array_elements(old_revision.graph_json -> 'edges') edge
            WHERE edge ->> 'edgeId' = 'condition-to-market-signal'
        )
    THEN
        RAISE EXCEPTION 'YGGUSDT workflow cannot be upgraded to market signals without guessing';
    END IF;

    new_graph := jsonb_set(
        jsonb_set(
            old_revision.graph_json,
            '{nodes}',
            (old_revision.graph_json -> 'nodes') || jsonb_build_array(jsonb_build_object(
                'nodeInstanceId', 'market-signal',
                'nodeType', 'official.quant.market_signal',
                'nodeVersion', '1.0.0',
                'config', '{}'::JSONB,
                'inputBindings', jsonb_build_object(
                    'market', jsonb_build_object('kind', 'field', 'nodeInstanceId', 'price-condition', 'fieldPath', jsonb_build_array('market')),
                    'instrument', jsonb_build_object('kind', 'field', 'nodeInstanceId', 'price-condition', 'fieldPath', jsonb_build_array('instrument')),
                    'interval', jsonb_build_object('kind', 'field', 'nodeInstanceId', 'price-condition', 'fieldPath', jsonb_build_array('interval')),
                    'name', jsonb_build_object('kind', 'field', 'nodeInstanceId', 'price-condition', 'fieldPath', jsonb_build_array('formula')),
                    'indicator', jsonb_build_object('kind', 'field', 'nodeInstanceId', 'price-condition', 'fieldPath', jsonb_build_array('indicator')),
                    'candleCloseTime', jsonb_build_object('kind', 'field', 'nodeInstanceId', 'price-condition', 'fieldPath', jsonb_build_array('candleCloseTime')),
                    'summary', jsonb_build_object('kind', 'field', 'nodeInstanceId', 'price-condition', 'fieldPath', jsonb_build_array('summary')),
                    'values', jsonb_build_object('kind', 'field', 'nodeInstanceId', 'price-condition', 'fieldPath', jsonb_build_array('value'))
                ),
                'position', jsonb_build_object('x', 760, 'y', 212)
            ))
        ),
        '{edges}',
        (old_revision.graph_json -> 'edges') || jsonb_build_array(jsonb_build_object(
            'edgeId', 'condition-to-market-signal',
            'sourceNodeInstanceId', 'price-condition',
            'sourcePort', 'true',
            'targetNodeInstanceId', 'market-signal',
            'targetPort', 'in'
        ))
    );

    SELECT COALESCE(MAX(revision_number), 0) + 1
    INTO next_revision_number
    FROM workflow_revisions
    WHERE workflow_id = target_workflow_id;

    INSERT INTO workflow_revisions (
        workflow_id, revision_number, graph_json, node_versions,
        main_trigger_node_id, created_by, created_at
    ) VALUES (
        target_workflow_id,
        next_revision_number,
        new_graph,
        old_revision.node_versions || jsonb_build_object(
            'market-signal', jsonb_build_object(
                'nodeType', 'official.quant.market_signal', 'nodeVersion', '1.0.0'
            )
        ),
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
    WHERE id = target_workflow_id AND active_revision_id = old_revision.id;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'YGGUSDT workflow active revision changed during market signal upgrade';
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE
    target RECORD;
    old_revision workflow_revisions%ROWTYPE;
    old_node JSONB;
    old_node_count BIGINT;
    old_node_id TEXT;
    old_interval TEXT;
    next_revision_number BIGINT;
    new_revision_id BIGINT;
    new_graph JSONB;
BEGIN
    FOR target IN
        SELECT workflow.id AS workflow_id
        FROM workflows workflow
        JOIN workflow_revisions revision ON revision.id = workflow.active_revision_id
        WHERE EXISTS (
            SELECT 1
            FROM jsonb_array_elements(revision.graph_json -> 'nodes') node(value)
            WHERE node.value ->> 'nodeType' = 'official.quant.binance_candles'
        )
        ORDER BY workflow.id
    LOOP
        SELECT revision.*
        INTO old_revision
        FROM workflows workflow
        JOIN workflow_revisions revision ON revision.id = workflow.active_revision_id
        WHERE workflow.id = target.workflow_id
        FOR UPDATE OF workflow;

        IF jsonb_typeof(old_revision.graph_json -> 'nodes') <> 'array' THEN
            RAISE EXCEPTION 'workflow % has an invalid node list', target.workflow_id;
        END IF;

        SELECT COUNT(*)
        INTO old_node_count
        FROM jsonb_array_elements(old_revision.graph_json -> 'nodes') node(value)
        WHERE node.value ->> 'nodeType' = 'official.quant.binance_candles';

        IF old_node_count <> 1 THEN
            RAISE EXCEPTION 'workflow % must have exactly one legacy Binance candle node, found %', target.workflow_id, old_node_count;
        END IF;

        SELECT node.value
        INTO old_node
        FROM jsonb_array_elements(old_revision.graph_json -> 'nodes') node(value)
        WHERE node.value ->> 'nodeType' = 'official.quant.binance_candles';

        old_node_id := old_node ->> 'nodeInstanceId';
        old_interval := old_node -> 'config' ->> 'interval';
        IF old_node_id IS NULL
            OR COALESCE(old_node -> 'config' ->> 'market', '') NOT IN ('spot', 'usdm')
            OR COALESCE(old_node -> 'config' ->> 'instrument', '') !~ '^[A-Z0-9]{2,32}$'
            OR COALESCE(old_interval, '') NOT IN ('1m', '3m', '5m', '15m', '30m', '1h', '2h', '4h', '6h', '8h', '12h', '1d', '3d', '1w')
            OR old_revision.node_versions -> old_node_id ->> 'nodeType' IS DISTINCT FROM 'official.quant.binance_candles'
            OR old_revision.node_versions -> old_node_id ->> 'nodeVersion' IS DISTINCT FROM '1.0.0'
        THEN
            RAISE EXCEPTION 'workflow % legacy Binance candle node cannot be converted safely', target.workflow_id;
        END IF;

        SELECT jsonb_set(
            old_revision.graph_json,
            '{nodes}',
            jsonb_agg(
                CASE
                    WHEN node.value ->> 'nodeInstanceId' = old_node_id THEN
                        jsonb_set(
                            jsonb_set(
                                node.value,
                                '{nodeType}',
                                to_jsonb('official.quant.realtime_candles'::TEXT)
                            ),
                            '{config}',
                            ((node.value -> 'config') - 'interval')
                                || jsonb_build_object('intervals', jsonb_build_array(old_interval))
                        )
                    ELSE node.value
                END
                ORDER BY node.ordinality
            )
        )
        INTO new_graph
        FROM jsonb_array_elements(old_revision.graph_json -> 'nodes') WITH ORDINALITY node(value, ordinality);

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
            old_revision.node_versions || jsonb_build_object(
                old_node_id,
                jsonb_build_object('nodeType', 'official.quant.realtime_candles', 'nodeVersion', '1.0.0')
            ),
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
            RAISE EXCEPTION 'workflow % active revision changed during Binance candle node conversion', target.workflow_id;
        END IF;
    END LOOP;
END
$$;
-- +goose StatementEnd

-- +goose Down

LOCK TABLE
    workflows,
    workflow_revisions,
    workflow_secret_bindings,
    workflow_runs,
    plugin_quant.market_signals
IN ACCESS EXCLUSIVE MODE;

-- +goose StatementBegin
DO $$
DECLARE
    unsupported_count BIGINT;
BEGIN
    CREATE TEMP TABLE realtime_candle_revision_rollback ON COMMIT DROP AS
    SELECT
        workflow.id AS workflow_id,
        current_revision.id AS current_revision_id,
        previous_revision.id AS previous_revision_id
    FROM workflows workflow
    JOIN workflow_revisions current_revision ON current_revision.id = workflow.active_revision_id
    JOIN workflow_revisions previous_revision
      ON previous_revision.workflow_id = current_revision.workflow_id
     AND previous_revision.revision_number = current_revision.revision_number - 1
    CROSS JOIN LATERAL (
        SELECT node.value, node.value ->> 'nodeInstanceId' AS node_id
        FROM jsonb_array_elements(current_revision.graph_json -> 'nodes') node(value)
        WHERE node.value ->> 'nodeType' = 'official.quant.realtime_candles'
    ) realtime_node
    CROSS JOIN LATERAL (
        SELECT jsonb_set(
            current_revision.graph_json,
            '{nodes}',
            jsonb_agg(
                CASE
                    WHEN node.value ->> 'nodeInstanceId' = realtime_node.node_id THEN
                        jsonb_set(
                            jsonb_set(
                                node.value,
                                '{nodeType}',
                                to_jsonb('official.quant.binance_candles'::TEXT)
                            ),
                            '{config}',
                            ((node.value -> 'config') - 'intervals')
                                || jsonb_build_object('interval', node.value -> 'config' -> 'intervals' -> 0)
                        )
                    ELSE node.value
                END
                ORDER BY node.ordinality
            )
        ) AS graph_json
        FROM jsonb_array_elements(current_revision.graph_json -> 'nodes') WITH ORDINALITY node(value, ordinality)
    ) reverted
    WHERE jsonb_typeof(realtime_node.value -> 'config' -> 'intervals') = 'array'
      AND jsonb_array_length(realtime_node.value -> 'config' -> 'intervals') = 1
      AND (
          SELECT COUNT(*)
          FROM jsonb_array_elements(current_revision.graph_json -> 'nodes') node(value)
          WHERE node.value ->> 'nodeType' = 'official.quant.realtime_candles'
      ) = 1
      AND reverted.graph_json = previous_revision.graph_json
      AND jsonb_set(
          current_revision.node_versions,
          ARRAY[realtime_node.node_id],
          jsonb_build_object('nodeType', 'official.quant.binance_candles', 'nodeVersion', '1.0.0'),
          false
      ) = previous_revision.node_versions;

    SELECT COUNT(*)
    INTO unsupported_count
    FROM workflows workflow
    JOIN workflow_revisions revision ON revision.id = workflow.active_revision_id
    WHERE EXISTS (
        SELECT 1
        FROM jsonb_array_elements(revision.graph_json -> 'nodes') node(value)
        WHERE node.value ->> 'nodeType' = 'official.quant.realtime_candles'
    )
      AND revision.id NOT IN (SELECT current_revision_id FROM realtime_candle_revision_rollback);

    IF unsupported_count > 0 THEN
        RAISE EXCEPTION 'refusing to roll back workflows that still reference realtime Quant candles';
    END IF;
    IF EXISTS (
        SELECT 1 FROM workflow_runs run
        WHERE run.revision_id IN (SELECT current_revision_id FROM realtime_candle_revision_rollback)
    ) THEN
        RAISE EXCEPTION 'refusing to remove a realtime candle workflow revision with run history';
    END IF;

    UPDATE workflows workflow
    SET active_revision_id = rollback.previous_revision_id, updated_at = CURRENT_TIMESTAMP
    FROM realtime_candle_revision_rollback rollback
    WHERE workflow.id = rollback.workflow_id
      AND workflow.active_revision_id = rollback.current_revision_id;

    ALTER TABLE workflow_secret_bindings DISABLE TRIGGER trg_workflow_secret_bindings_immutable;
    DELETE FROM workflow_secret_bindings
    WHERE revision_id IN (SELECT current_revision_id FROM realtime_candle_revision_rollback);
    ALTER TABLE workflow_secret_bindings ENABLE TRIGGER trg_workflow_secret_bindings_immutable;

    ALTER TABLE workflow_revisions DISABLE TRIGGER trg_workflow_revisions_immutable;
    DELETE FROM workflow_revisions
    WHERE id IN (SELECT current_revision_id FROM realtime_candle_revision_rollback);
    ALTER TABLE workflow_revisions ENABLE TRIGGER trg_workflow_revisions_immutable;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE
    unsupported_count BIGINT;
BEGIN
    IF EXISTS (SELECT 1 FROM plugin_quant.market_signals LIMIT 1) THEN
        RAISE EXCEPTION 'refusing to roll back persisted Quant market signals';
    END IF;

    CREATE TEMP TABLE market_signal_revision_rollback ON COMMIT DROP AS
    SELECT
        workflow.id AS workflow_id,
        current_revision.id AS current_revision_id,
        previous_revision.id AS previous_revision_id
    FROM workflows workflow
    JOIN workflow_revisions current_revision ON current_revision.id = workflow.active_revision_id
    JOIN workflow_revisions previous_revision
      ON previous_revision.workflow_id = current_revision.workflow_id
     AND previous_revision.revision_number = current_revision.revision_number - 1
    WHERE current_revision.node_versions - 'market-signal' = previous_revision.node_versions
      AND jsonb_set(
            jsonb_set(
                current_revision.graph_json,
                '{nodes}',
                COALESCE((
                    SELECT jsonb_agg(node.value ORDER BY node.ordinality)
                    FROM jsonb_array_elements(current_revision.graph_json -> 'nodes') WITH ORDINALITY node(value, ordinality)
                    WHERE node.value ->> 'nodeInstanceId' <> 'market-signal'
                ), '[]'::JSONB)
            ),
            '{edges}',
            COALESCE((
                SELECT jsonb_agg(edge.value ORDER BY edge.ordinality)
                FROM jsonb_array_elements(current_revision.graph_json -> 'edges') WITH ORDINALITY edge(value, ordinality)
                WHERE edge.value ->> 'edgeId' <> 'condition-to-market-signal'
            ), '[]'::JSONB)
          ) = previous_revision.graph_json
      AND EXISTS (
          SELECT 1 FROM jsonb_array_elements(current_revision.graph_json -> 'nodes') node
          WHERE node ->> 'nodeInstanceId' = 'market-signal'
            AND node ->> 'nodeType' = 'official.quant.market_signal'
      );

    SELECT COUNT(*)
    INTO unsupported_count
    FROM workflow_revisions revision
    WHERE jsonb_path_exists(revision.graph_json, '$.nodes[*] ? (@.nodeType == "official.quant.market_signal")')
      AND revision.id NOT IN (SELECT current_revision_id FROM market_signal_revision_rollback);

    IF unsupported_count > 0 THEN
        RAISE EXCEPTION 'refusing to roll back workflows that still reference Quant market signals';
    END IF;
    IF EXISTS (
        SELECT 1 FROM workflow_runs run
        WHERE run.revision_id IN (SELECT current_revision_id FROM market_signal_revision_rollback)
    ) THEN
        RAISE EXCEPTION 'refusing to remove a market signal workflow revision with run history';
    END IF;

    UPDATE workflows workflow
    SET active_revision_id = rollback.previous_revision_id, updated_at = CURRENT_TIMESTAMP
    FROM market_signal_revision_rollback rollback
    WHERE workflow.id = rollback.workflow_id
      AND workflow.active_revision_id = rollback.current_revision_id;

    ALTER TABLE workflow_secret_bindings DISABLE TRIGGER trg_workflow_secret_bindings_immutable;
    DELETE FROM workflow_secret_bindings
    WHERE revision_id IN (SELECT current_revision_id FROM market_signal_revision_rollback);
    ALTER TABLE workflow_secret_bindings ENABLE TRIGGER trg_workflow_secret_bindings_immutable;

    ALTER TABLE workflow_revisions DISABLE TRIGGER trg_workflow_revisions_immutable;
    DELETE FROM workflow_revisions
    WHERE id IN (SELECT current_revision_id FROM market_signal_revision_rollback);
    ALTER TABLE workflow_revisions ENABLE TRIGGER trg_workflow_revisions_immutable;
END
$$;
-- +goose StatementEnd

DROP TABLE plugin_quant.market_signals;
