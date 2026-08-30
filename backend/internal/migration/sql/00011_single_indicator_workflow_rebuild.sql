-- +goose Up

LOCK TABLE
    workflows,
    workflow_revisions,
    workflow_runtimes,
    workflow_secret_bindings,
    workflow_runs,
    workflow_run_nodes,
    workflow_node_logs,
    workflow_run_checkpoints,
    workflow_node_states,
    workflow_artifact_refs,
    workflow_event_deliveries,
    workflow_human_tasks,
    plugin_notification.deliveries,
    plugin_quant.instrument_sources,
    plugin_quant.backtests,
    plugin_quant.signals,
    plugin_quant.paper_accounts
IN ACCESS EXCLUSIVE MODE;

CREATE TEMP TABLE quant_indicator_workflow_rebuild (
    old_workflow_id BIGINT PRIMARY KEY,
    new_workflow_id BIGINT NOT NULL UNIQUE,
    new_revision_id BIGINT NOT NULL UNIQUE
) ON COMMIT DROP;

-- +goose StatementBegin
DO $$
DECLARE
    target_ids BIGINT[];
BEGIN
    SELECT ARRAY_AGG(DISTINCT workflow_id ORDER BY workflow_id)
    INTO target_ids
    FROM workflow_revisions
    WHERE jsonb_path_exists(
        graph_json,
        '$.nodes[*] ? (@.nodeType == "official.quant.indicator_condition")'
    );

    IF target_ids IS NULL THEN
        RETURN;
    END IF;
    IF target_ids <> ARRAY[3::BIGINT] THEN
        RAISE EXCEPTION 'old quant condition workflow inventory changed: %', target_ids;
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM workflows w
        JOIN workflow_revisions r ON r.id = w.active_revision_id
        WHERE w.id = 3
          AND w.active_revision_id = 7
          AND md5(r.graph_json::TEXT) = 'ceb29098af6afa49b7a20d9698f53fa7'
    ) THEN
        RAISE EXCEPTION 'YGGUSDT workflow active graph changed after migration preflight';
    END IF;
    IF EXISTS (SELECT 1 FROM plugin_quant.backtests WHERE workflow_id = ANY(target_ids))
        OR EXISTS (SELECT 1 FROM plugin_quant.signals WHERE workflow_id = ANY(target_ids))
        OR EXISTS (SELECT 1 FROM plugin_quant.paper_accounts WHERE workflow_id = ANY(target_ids))
    THEN
        RAISE EXCEPTION 'refusing to delete workflows with Quant or Paper financial facts';
    END IF;
    IF EXISTS (
        SELECT 1
        FROM workflow_artifact_refs owned
        JOIN workflow_run_nodes owned_node ON owned_node.id = owned.run_node_id
        JOIN workflow_runs owned_run ON owned_run.id = owned_node.run_id
        JOIN workflow_artifact_refs shared ON shared.artifact_sha256 = owned.artifact_sha256
        JOIN workflow_run_nodes shared_node ON shared_node.id = shared.run_node_id
        JOIN workflow_runs shared_run ON shared_run.id = shared_node.run_id
        WHERE owned_run.workflow_id = ANY(target_ids)
          AND shared_run.workflow_id <> ALL(target_ids)
    ) THEN
        RAISE EXCEPTION 'refusing to delete workflows with shared artifacts';
    END IF;
    IF EXISTS (
        SELECT 1
        FROM workflow_runs external_run
        JOIN workflow_runs owned_run ON owned_run.id = external_run.original_run_id
        WHERE owned_run.workflow_id = ANY(target_ids)
          AND external_run.workflow_id <> ALL(target_ids)
    ) THEN
        RAISE EXCEPTION 'refusing to delete workflows referenced by external diagnostic runs';
    END IF;

    INSERT INTO quant_indicator_workflow_rebuild (old_workflow_id, new_workflow_id, new_revision_id)
    VALUES (
        3,
        nextval(pg_get_serial_sequence('workflows', 'id')),
        nextval(pg_get_serial_sequence('workflow_revisions', 'id'))
    );
END
$$;
-- +goose StatementEnd

ALTER TABLE workflows DISABLE TRIGGER trg_workflows_active_revision;
ALTER TABLE workflow_revisions DISABLE TRIGGER trg_workflow_revisions_immutable;
ALTER TABLE workflow_secret_bindings DISABLE TRIGGER trg_workflow_secret_bindings_immutable;
ALTER TABLE workflow_run_checkpoints DISABLE TRIGGER trg_workflow_run_checkpoints_immutable;
ALTER TABLE workflow_run_nodes DISABLE TRIGGER trg_workflow_run_nodes_terminal_immutable;
ALTER TABLE workflow_node_logs DISABLE TRIGGER trg_workflow_node_logs_immutable;

INSERT INTO workflows (
    id, name, description, mode, status, active_revision_id, main_trigger_node_id,
    retention_days, created_by, created_at, updated_at
)
SELECT
    rebuild.new_workflow_id, old.name, old.description, old.mode, old.status, NULL,
    old.main_trigger_node_id, old.retention_days, old.created_by, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
FROM quant_indicator_workflow_rebuild rebuild
JOIN workflows old ON old.id = rebuild.old_workflow_id;

INSERT INTO workflow_revisions (
    id, workflow_id, revision_number, graph_json, node_versions,
    main_trigger_node_id, created_by, created_at
)
SELECT
    rebuild.new_revision_id,
    rebuild.new_workflow_id,
    1,
    $graph${
      "schemaVersion": 1,
      "nodes": [
        {
          "nodeInstanceId": "market-stream",
          "nodeType": "official.quant.realtime_candles",
          "nodeVersion": "1.0.0",
          "config": {"market": "spot", "instrument": "YGGUSDT", "intervals": ["1m"]},
          "position": {"x": 100, "y": 220}
        },
        {
          "nodeInstanceId": "price-condition",
          "nodeType": "official.quant.price_change_condition",
          "nodeVersion": "1.0.0",
          "config": {
            "market": "spot",
            "instrument": "YGGUSDT",
            "checkInterval": "1m",
            "name": "1分钟绝对涨跌幅达到1%",
            "interval": "1m",
            "parameters": {"lookback": 1, "mode": "absolute", "threshold": "1"}
          },
          "inputBindings": {
            "eventTime": {"kind": "field", "nodeInstanceId": "market-stream", "fieldPath": ["closeTime"]}
          },
          "position": {"x": 480, "y": 212}
        },
        {
          "nodeInstanceId": "in-app-notification",
          "nodeType": "official.notification.in_app",
          "nodeVersion": "1.0.0",
          "config": {"title": "YGGUSDT 1分钟波动提醒"},
          "inputBindings": {
            "subjectKey": {"kind": "condition_subject", "sources": [{"nodeInstanceId": "price-condition", "branch": "true"}]},
            "message": {"kind": "condition_message", "sources": [{"nodeInstanceId": "price-condition", "branch": "true"}]}
          },
          "position": {"x": 1020, "y": 20}
        },
        {
          "nodeInstanceId": "end_1",
          "nodeType": "core.end",
          "nodeVersion": "1.0.0",
          "config": {},
          "inputBindings": {"result": {"kind": "literal", "value": ""}},
          "position": {"x": 1020, "y": 316}
        },
        {
          "nodeInstanceId": "hourly-price-notification",
          "nodeType": "official.notification.in_app",
          "nodeVersion": "1.0.0",
          "config": {"title": "YGGUSDT 每小时最新价格（USDT）"},
          "inputBindings": {
            "subjectKey": {"kind": "literal", "value": "YGGUSDT-hourly-latest-price"},
            "message": {"kind": "field", "nodeInstanceId": "market-stream", "fieldPath": ["close"]}
          },
          "position": {"x": 480, "y": 400}
        }
      ],
      "edges": [
        {
          "edgeId": "market-to-condition",
          "sourceNodeInstanceId": "market-stream",
          "sourcePort": "out",
          "targetNodeInstanceId": "price-condition",
          "targetPort": "in"
        },
        {
          "edgeId": "condition-to-notification",
          "sourceNodeInstanceId": "price-condition",
          "sourcePort": "true",
          "targetNodeInstanceId": "in-app-notification",
          "targetPort": "in",
          "condition": "input.triggered == true && event.time >= \"2026-08-29T14:58:03.254169Z\""
        },
        {
          "edgeId": "edge_ukj5h81k",
          "sourceNodeInstanceId": "price-condition",
          "sourcePort": "false",
          "targetNodeInstanceId": "end_1",
          "targetPort": "in"
        },
        {
          "edgeId": "market-to-hourly-price",
          "sourceNodeInstanceId": "market-stream",
          "sourcePort": "out",
          "targetNodeInstanceId": "hourly-price-notification",
          "targetPort": "in",
          "condition": "event.time >= \"2026-08-29T15:14:22.324904Z\" && event.time.endsWith(\":59:59.999Z\")"
        }
      ]
    }$graph$::JSONB,
    $versions${
      "market-stream": {"nodeType": "official.quant.realtime_candles", "nodeVersion": "1.0.0"},
      "price-condition": {"nodeType": "official.quant.price_change_condition", "nodeVersion": "1.0.0"},
      "in-app-notification": {"nodeType": "official.notification.in_app", "nodeVersion": "1.0.0"},
      "end_1": {"nodeType": "core.end", "nodeVersion": "1.0.0"},
      "hourly-price-notification": {"nodeType": "official.notification.in_app", "nodeVersion": "1.0.0"}
    }$versions$::JSONB,
    old.main_trigger_node_id,
    old.created_by,
    CURRENT_TIMESTAMP
FROM quant_indicator_workflow_rebuild rebuild
JOIN workflows old ON old.id = rebuild.old_workflow_id;

UPDATE workflows replacement
SET active_revision_id = rebuild.new_revision_id
FROM quant_indicator_workflow_rebuild rebuild
WHERE replacement.id = rebuild.new_workflow_id;

INSERT INTO workflow_runtimes (
    workflow_id, max_concurrent_runs, backlog_limit,
    next_scheduled_at, last_scheduled_at, updated_at
)
SELECT
    rebuild.new_workflow_id, runtime.max_concurrent_runs, runtime.backlog_limit,
    runtime.next_scheduled_at, runtime.last_scheduled_at, CURRENT_TIMESTAMP
FROM quant_indicator_workflow_rebuild rebuild
JOIN workflow_runtimes runtime ON runtime.workflow_id = rebuild.old_workflow_id;

DELETE FROM plugin_notification.deliveries
WHERE workflow_id IN (SELECT old_workflow_id FROM quant_indicator_workflow_rebuild);
DELETE FROM workflow_event_deliveries
WHERE workflow_id IN (SELECT old_workflow_id FROM quant_indicator_workflow_rebuild);
DELETE FROM workflow_human_tasks
WHERE workflow_id IN (SELECT old_workflow_id FROM quant_indicator_workflow_rebuild);
DELETE FROM workflow_artifact_refs
WHERE run_node_id IN (
    SELECT node.id
    FROM workflow_run_nodes node
    JOIN workflow_runs run ON run.id = node.run_id
    WHERE run.workflow_id IN (SELECT old_workflow_id FROM quant_indicator_workflow_rebuild)
);
DELETE FROM workflow_run_checkpoints
WHERE workflow_id IN (SELECT old_workflow_id FROM quant_indicator_workflow_rebuild);
DELETE FROM workflow_node_logs
WHERE workflow_id IN (SELECT old_workflow_id FROM quant_indicator_workflow_rebuild);
DELETE FROM workflow_run_nodes
WHERE run_id IN (
    SELECT id FROM workflow_runs
    WHERE workflow_id IN (SELECT old_workflow_id FROM quant_indicator_workflow_rebuild)
);
DELETE FROM workflow_runs
WHERE workflow_id IN (SELECT old_workflow_id FROM quant_indicator_workflow_rebuild)
  AND original_run_id IS NOT NULL;
DELETE FROM workflow_runs
WHERE workflow_id IN (SELECT old_workflow_id FROM quant_indicator_workflow_rebuild);
DELETE FROM workflow_node_states
WHERE workflow_id IN (SELECT old_workflow_id FROM quant_indicator_workflow_rebuild);
DELETE FROM workflow_secret_bindings
WHERE workflow_id IN (SELECT old_workflow_id FROM quant_indicator_workflow_rebuild);
DELETE FROM plugin_quant.instrument_sources
WHERE workflow_id IN (SELECT old_workflow_id FROM quant_indicator_workflow_rebuild);
DELETE FROM workflow_runtimes
WHERE workflow_id IN (SELECT old_workflow_id FROM quant_indicator_workflow_rebuild);

UPDATE workflows
SET active_revision_id = NULL
WHERE id IN (SELECT old_workflow_id FROM quant_indicator_workflow_rebuild);
DELETE FROM workflow_revisions
WHERE workflow_id IN (SELECT old_workflow_id FROM quant_indicator_workflow_rebuild);
DELETE FROM workflows
WHERE id IN (SELECT old_workflow_id FROM quant_indicator_workflow_rebuild);

SET CONSTRAINTS ALL IMMEDIATE;

ALTER TABLE workflow_node_logs ENABLE TRIGGER trg_workflow_node_logs_immutable;
ALTER TABLE workflow_run_nodes ENABLE TRIGGER trg_workflow_run_nodes_terminal_immutable;
ALTER TABLE workflow_run_checkpoints ENABLE TRIGGER trg_workflow_run_checkpoints_immutable;
ALTER TABLE workflow_secret_bindings ENABLE TRIGGER trg_workflow_secret_bindings_immutable;
ALTER TABLE workflow_revisions ENABLE TRIGGER trg_workflow_revisions_immutable;
ALTER TABLE workflows ENABLE TRIGGER trg_workflows_active_revision;

-- +goose Down

-- +goose StatementBegin
DO $$
BEGIN
    RAISE EXCEPTION 'migration 00011 permanently deleted workflow history; restore the pre-deploy database backup instead';
END
$$;
-- +goose StatementEnd
