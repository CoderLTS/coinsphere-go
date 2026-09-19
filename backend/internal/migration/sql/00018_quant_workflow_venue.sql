-- +goose Up

LOCK TABLE workflows, workflow_revisions, workflow_secret_bindings IN ACCESS EXCLUSIVE MODE;

-- +goose StatementBegin
DO $$
DECLARE
    target RECORD;
    old_revision workflow_revisions%ROWTYPE;
    next_revision_number BIGINT;
    new_revision_id BIGINT;
    new_graph JSONB;
    venue_node_types CONSTANT TEXT[] := ARRAY[
        'official.quant.evaluate',
        'official.quant.backtest',
        'official.quant.backtest_start',
        'official.quant.output_signal',
        'official.quant.volume_spike_condition',
        'official.quant.price_change_condition',
        'official.quant.macd_condition',
        'official.quant.kdj_condition',
        'official.quant.rsi_condition',
        'official.quant.bollinger_condition'
    ];
    removed_node_types CONSTANT TEXT[] := ARRAY[
        'official.quant.realtime_candles',
        'official.quant.backfill_candles',
        'official.quant.sync_instruments',
        'official.quant.signal',
        'official.quant.paper_execute'
    ];
BEGIN
    IF EXISTS (
        SELECT 1
        FROM workflows workflow
        JOIN workflow_revisions revision ON revision.id = workflow.active_revision_id
        WHERE jsonb_typeof(revision.graph_json -> 'nodes') IS DISTINCT FROM 'array'
           OR jsonb_typeof(revision.node_versions) IS DISTINCT FROM 'object'
    ) THEN
        RAISE EXCEPTION 'active workflow revision has an invalid graph or node version map';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM workflows workflow
        JOIN workflow_revisions revision ON revision.id = workflow.active_revision_id
        CROSS JOIN LATERAL jsonb_array_elements(revision.graph_json -> 'nodes') node(value)
        WHERE node.value ->> 'nodeType' = ANY(removed_node_types)
    ) THEN
        RAISE EXCEPTION 'active workflow revision still references a removed Quant node and cannot be converted safely';
    END IF;

    FOR target IN
        SELECT workflow.id AS workflow_id
        FROM workflows workflow
        JOIN workflow_revisions revision ON revision.id = workflow.active_revision_id
        WHERE EXISTS (
            SELECT 1
            FROM jsonb_array_elements(revision.graph_json -> 'nodes') node(value)
            WHERE (
                node.value ->> 'nodeType' = ANY(venue_node_types)
                AND BTRIM(COALESCE(node.value #>> '{config,venue}', '')) = ''
            ) OR (
                node.value ->> 'nodeType' = 'official.quant.code_strategy'
                AND EXISTS (
                    SELECT 1
                    FROM jsonb_array_elements(
                        CASE
                            WHEN jsonb_typeof(node.value #> '{config,series}') = 'array'
                                THEN node.value #> '{config,series}'
                            ELSE '[]'::JSONB
                        END
                    ) series(value)
                    WHERE BTRIM(COALESCE(series.value ->> 'venue', '')) = ''
                )
            ) OR (
                node.value ->> 'nodeType' = 'core.event'
                AND node.value #>> '{config,source}' = 'urn:coinsphere:plugin:official.quant'
            ) OR (
                node.value ->> 'nodeType' = 'official.quant.market_signal'
                AND (
                    NOT (COALESCE(node.value -> 'inputBindings', '{}'::JSONB) ? 'venue')
                    OR node.value #> '{inputBindings,venue}' = 'null'::JSONB
                )
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

        IF EXISTS (
            SELECT 1
            FROM jsonb_array_elements(old_revision.graph_json -> 'nodes') node(value)
            WHERE (
                node.value ->> 'nodeType' = ANY(venue_node_types)
                OR node.value ->> 'nodeType' = 'official.quant.code_strategy'
            )
              AND jsonb_typeof(node.value -> 'config') IS DISTINCT FROM 'object'
        ) THEN
            RAISE EXCEPTION 'workflow % has an invalid Quant node configuration', target.workflow_id;
        END IF;

        IF EXISTS (
            SELECT 1
            FROM jsonb_array_elements(old_revision.graph_json -> 'nodes') node(value)
            WHERE node.value ->> 'nodeType' = 'official.quant.code_strategy'
              AND (
                  jsonb_typeof(node.value #> '{config,series}') IS DISTINCT FROM 'array'
                  OR CASE
                      WHEN jsonb_typeof(node.value #> '{config,series}') = 'array'
                          THEN jsonb_array_length(node.value #> '{config,series}') = 0
                      ELSE TRUE
                  END
              )
        ) OR EXISTS (
            SELECT 1
            FROM jsonb_array_elements(old_revision.graph_json -> 'nodes') node(value)
            CROSS JOIN LATERAL jsonb_array_elements(
                CASE
                    WHEN jsonb_typeof(node.value #> '{config,series}') = 'array'
                        THEN node.value #> '{config,series}'
                    ELSE '[]'::JSONB
                END
            ) series(value)
            WHERE node.value ->> 'nodeType' = 'official.quant.code_strategy'
              AND jsonb_typeof(series.value) IS DISTINCT FROM 'object'
        ) THEN
            RAISE EXCEPTION 'workflow % has an invalid Quant code strategy series', target.workflow_id;
        END IF;

        IF EXISTS (
            SELECT 1
            FROM jsonb_array_elements(old_revision.graph_json -> 'nodes') node(value)
            WHERE node.value ->> 'nodeType' = 'official.quant.market_signal'
              AND (
                  NOT (COALESCE(node.value -> 'inputBindings', '{}'::JSONB) ? 'venue')
                  OR node.value #> '{inputBindings,venue}' = 'null'::JSONB
              )
              AND (
                  jsonb_typeof(node.value -> 'inputBindings') IS DISTINCT FROM 'object'
                  OR jsonb_typeof(node.value #> '{inputBindings,market}') IS DISTINCT FROM 'object'
                  OR node.value #>> '{inputBindings,market,kind}' IS DISTINCT FROM 'field'
                  OR BTRIM(COALESCE(node.value #>> '{inputBindings,market,nodeInstanceId}', '')) = ''
                  OR node.value #> '{inputBindings,market,fieldPath}' IS DISTINCT FROM '["market"]'::JSONB
              )
        ) THEN
            RAISE EXCEPTION 'workflow % Quant market signal binding cannot be converted safely', target.workflow_id;
        END IF;

        SELECT jsonb_set(
            old_revision.graph_json,
            '{nodes}',
            jsonb_agg(
                CASE
                    WHEN node.value ->> 'nodeType' = ANY(venue_node_types)
                         AND BTRIM(COALESCE(node.value #>> '{config,venue}', '')) = '' THEN
                        jsonb_set(node.value, '{config,venue}', to_jsonb('binance'::TEXT), true)
                    WHEN node.value ->> 'nodeType' = 'official.quant.code_strategy' THEN
                        jsonb_set(
                            node.value,
                            '{config,series}',
                            (
                                SELECT jsonb_agg(
                                    CASE
                                        WHEN BTRIM(COALESCE(series.value ->> 'venue', '')) = ''
                                            THEN series.value || jsonb_build_object('venue', 'binance')
                                        ELSE series.value
                                    END
                                    ORDER BY series.ordinality
                                )
                                FROM jsonb_array_elements(node.value #> '{config,series}')
                                    WITH ORDINALITY series(value, ordinality)
                            ),
                            false
                        )
                    WHEN node.value ->> 'nodeType' = 'core.event'
                         AND node.value #>> '{config,source}' = 'urn:coinsphere:plugin:official.quant' THEN
                        jsonb_set(
                            node.value,
                            '{config,source}',
                            to_jsonb('urn:coinsphere:plugin:official.binance'::TEXT),
                            false
                        )
                    WHEN node.value ->> 'nodeType' = 'official.quant.market_signal'
                         AND (
                             NOT (COALESCE(node.value -> 'inputBindings', '{}'::JSONB) ? 'venue')
                             OR node.value #> '{inputBindings,venue}' = 'null'::JSONB
                         ) THEN
                        jsonb_set(
                            node.value,
                            '{inputBindings,venue}',
                            jsonb_build_object(
                                'kind', 'field',
                                'nodeInstanceId', node.value #>> '{inputBindings,market,nodeInstanceId}',
                                'fieldPath', jsonb_build_array('venue')
                            ),
                            true
                        )
                    ELSE node.value
                END
                ORDER BY node.ordinality
            )
        )
        INTO new_graph
        FROM jsonb_array_elements(old_revision.graph_json -> 'nodes') WITH ORDINALITY node(value, ordinality);

        IF new_graph = old_revision.graph_json OR EXISTS (
            SELECT 1
            FROM jsonb_array_elements(new_graph -> 'nodes') node(value)
            WHERE (
                node.value ->> 'nodeType' = ANY(venue_node_types)
                AND BTRIM(COALESCE(node.value #>> '{config,venue}', '')) = ''
            ) OR (
                node.value ->> 'nodeType' = 'official.quant.code_strategy'
                AND EXISTS (
                    SELECT 1
                    FROM jsonb_array_elements(node.value #> '{config,series}') series(value)
                    WHERE BTRIM(COALESCE(series.value ->> 'venue', '')) = ''
                )
            ) OR (
                node.value ->> 'nodeType' = 'core.event'
                AND node.value #>> '{config,source}' = 'urn:coinsphere:plugin:official.quant'
            ) OR (
                node.value ->> 'nodeType' = 'official.quant.market_signal'
                AND (
                    NOT (COALESCE(node.value -> 'inputBindings', '{}'::JSONB) ? 'venue')
                    OR node.value #> '{inputBindings,venue}' = 'null'::JSONB
                )
            )
        ) THEN
            RAISE EXCEPTION 'workflow % Quant configuration conversion was incomplete', target.workflow_id;
        END IF;

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
            old_revision.node_versions,
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
            RAISE EXCEPTION 'workflow % active revision changed during Quant configuration conversion', target.workflow_id;
        END IF;
    END LOOP;
END
$$;
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
DO $$
BEGIN
    RAISE EXCEPTION 'Quant workflow venue migration cannot be rolled back automatically; restore the previous active revisions from backup';
END
$$;
-- +goose StatementEnd
