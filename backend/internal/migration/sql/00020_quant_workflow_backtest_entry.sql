-- +goose Up

LOCK TABLE workflows, workflow_revisions, workflow_secret_bindings IN ACCESS EXCLUSIVE MODE;

-- +goose StatementBegin
DO $$
DECLARE
    target RECORD;
    old_revision workflow_revisions%ROWTYPE;
    backfill_id TEXT;
    frame_driver_id TEXT;
    matching_edge_count BIGINT;
    connected_edge_count BIGINT;
    next_revision_number BIGINT;
    new_revision_id BIGINT;
    new_graph JSONB;
BEGIN
    FOR target IN
        SELECT workflow.id AS workflow_id
        FROM workflows workflow
        JOIN workflow_revisions revision ON revision.id = workflow.active_revision_id
        WHERE revision.graph_json ->> 'schemaVersion' = '2'
          AND EXISTS (
              SELECT 1
              FROM jsonb_array_elements(revision.graph_json -> 'nodes') node(value)
              WHERE node.value ->> 'nodeInstanceId' = revision.graph_json #>> '{entryPoints,backtest}'
                AND node.value ->> 'nodeType' = 'official.binance.backfill_candles'
          )
        ORDER BY workflow.id
    LOOP
        SELECT revision.*
        INTO old_revision
        FROM workflows workflow
        JOIN workflow_revisions revision ON revision.id = workflow.active_revision_id
        WHERE workflow.id = target.workflow_id
        FOR UPDATE OF workflow;

        backfill_id := old_revision.graph_json #>> '{entryPoints,backtest}';

        SELECT MIN(edge.value ->> 'targetNodeInstanceId'), COUNT(*)
        INTO frame_driver_id, matching_edge_count
        FROM jsonb_array_elements(old_revision.graph_json -> 'edges') edge(value)
        JOIN LATERAL (
            SELECT node.value
            FROM jsonb_array_elements(old_revision.graph_json -> 'nodes') node(value)
            WHERE node.value ->> 'nodeInstanceId' = edge.value ->> 'targetNodeInstanceId'
        ) target_node ON TRUE
        WHERE edge.value ->> 'sourceNodeInstanceId' = backfill_id
          AND edge.value ->> 'sourcePort' = 'out'
          AND edge.value ->> 'targetPort' = 'in'
          AND target_node.value ->> 'nodeType' = 'official.quant.backtest_start';

        SELECT COUNT(*)
        INTO connected_edge_count
        FROM jsonb_array_elements(old_revision.graph_json -> 'edges') edge(value)
        WHERE edge.value ->> 'sourceNodeInstanceId' = backfill_id
           OR edge.value ->> 'targetNodeInstanceId' = backfill_id;

        IF matching_edge_count <> 1 OR connected_edge_count <> 1 THEN
            RAISE EXCEPTION 'workflow % backtest entry cannot be converted safely', target.workflow_id;
        END IF;

        IF EXISTS (
            SELECT 1
            FROM jsonb_array_elements(old_revision.graph_json -> 'nodes') node(value)
            CROSS JOIN LATERAL jsonb_each(COALESCE(node.value -> 'inputBindings', '{}'::JSONB)) binding(key, value)
            WHERE node.value ->> 'nodeInstanceId' = frame_driver_id
              AND (
                  binding.value ->> 'kind' IS DISTINCT FROM 'field'
                  OR binding.value ->> 'nodeInstanceId' IS DISTINCT FROM backfill_id
              )
        ) OR EXISTS (
            SELECT 1
            FROM jsonb_array_elements(old_revision.graph_json -> 'nodes') node(value)
            CROSS JOIN LATERAL jsonb_each(COALESCE(node.value -> 'inputBindings', '{}'::JSONB)) binding(key, value)
            WHERE node.value ->> 'nodeInstanceId' <> frame_driver_id
              AND binding.value ->> 'nodeInstanceId' = backfill_id
        ) THEN
            RAISE EXCEPTION 'workflow % backtest bindings cannot be converted safely', target.workflow_id;
        END IF;

        SELECT jsonb_set(
            jsonb_set(
                jsonb_set(
                    old_revision.graph_json,
                    '{entryPoints,backtest}',
                    to_jsonb(frame_driver_id),
                    false
                ),
                '{nodes}',
                (
                    SELECT jsonb_agg(
                        CASE
                            WHEN node.value ->> 'nodeInstanceId' = frame_driver_id
                                THEN node.value - 'inputBindings'
                            ELSE node.value
                        END
                        ORDER BY node.ordinality
                    )
                    FROM jsonb_array_elements(old_revision.graph_json -> 'nodes')
                        WITH ORDINALITY node(value, ordinality)
                    WHERE node.value ->> 'nodeInstanceId' <> backfill_id
                ),
                false
            ),
            '{edges}',
            (
                SELECT COALESCE(jsonb_agg(edge.value ORDER BY edge.ordinality), '[]'::JSONB)
                FROM jsonb_array_elements(old_revision.graph_json -> 'edges')
                    WITH ORDINALITY edge(value, ordinality)
                WHERE edge.value ->> 'sourceNodeInstanceId' <> backfill_id
                  AND edge.value ->> 'targetNodeInstanceId' <> backfill_id
            ),
            false
        )
        INTO new_graph;

        IF new_graph = old_revision.graph_json
            OR new_graph #>> '{entryPoints,backtest}' IS DISTINCT FROM frame_driver_id
            OR EXISTS (
                SELECT 1
                FROM jsonb_array_elements(new_graph -> 'nodes') node(value)
                WHERE node.value ->> 'nodeInstanceId' = backfill_id
            )
        THEN
            RAISE EXCEPTION 'workflow % backtest entry conversion was incomplete', target.workflow_id;
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
            old_revision.node_versions - backfill_id,
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
        WHERE revision_id = old_revision.id
          AND node_instance_id <> backfill_id;

        UPDATE workflows
        SET active_revision_id = new_revision_id, updated_at = CURRENT_TIMESTAMP
        WHERE id = target.workflow_id AND active_revision_id = old_revision.id;

        IF NOT FOUND THEN
            RAISE EXCEPTION 'workflow % active revision changed during backtest entry conversion', target.workflow_id;
        END IF;
    END LOOP;
END
$$;
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
DO $$
BEGIN
    RAISE EXCEPTION 'Quant workflow backtest entry migration cannot be rolled back automatically; restore the previous active revisions from backup';
END
$$;
-- +goose StatementEnd
