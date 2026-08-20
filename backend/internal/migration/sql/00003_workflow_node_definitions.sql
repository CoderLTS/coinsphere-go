-- +goose Up

DELETE FROM workflow_executions
WHERE workflow_definition_id IN (
    SELECT id FROM workflow_definitions WHERE code = 'blockbeats_news_sync'
);

DELETE FROM workflow_runtime_states
WHERE workflow_code = 'blockbeats_news_sync';

DELETE FROM workflow_definitions
WHERE code = 'blockbeats_news_sync';

DELETE FROM i18n_texts
WHERE biz_type = 'button'
  AND biz_id IN (
      SELECT id FROM menu_buttons
      WHERE permission_code = 'scheduler.task_definitions.update'
  );

DELETE FROM menu_buttons
WHERE permission_code = 'scheduler.task_definitions.update';

UPDATE menus
SET name = 'NodeDefinitions',
    path = 'node-definition',
    permission_code = 'scheduler.workflow_definitions.view',
    component = '/scheduler/node-definition',
    title = '节点定义',
    updated_at = CURRENT_TIMESTAMP
WHERE name = 'TaskDefinitions';

UPDATE i18n_texts
SET text = CASE locale WHEN 'zh' THEN '节点定义' ELSE 'Node Definitions' END,
    updated_at = CURRENT_TIMESTAMP
WHERE biz_type = 'menu'
  AND biz_id IN (SELECT id FROM menus WHERE name = 'NodeDefinitions')
  AND locale IN ('zh', 'en');

DROP TABLE task_definition_configs;

-- +goose Down

CREATE TABLE task_definition_configs (
    id BIGSERIAL PRIMARY KEY,
    task_definition_code VARCHAR(120),
    parameter_overrides_json TEXT,
    updated_by BIGINT,
    created_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX idx_task_definition_configs_task_definition_code
    ON task_definition_configs (task_definition_code);

UPDATE menus
SET name = 'TaskDefinitions',
    path = 'task-definition',
    permission_code = 'scheduler.task_definitions.view',
    component = '/scheduler/task-definition',
    title = '任务定义',
    updated_at = CURRENT_TIMESTAMP
WHERE name = 'NodeDefinitions';

UPDATE i18n_texts
SET text = CASE locale WHEN 'zh' THEN '任务定义' ELSE 'Task Definitions' END,
    updated_at = CURRENT_TIMESTAMP
WHERE biz_type = 'menu'
  AND biz_id IN (SELECT id FROM menus WHERE name = 'TaskDefinitions')
  AND locale IN ('zh', 'en');

-- 旧版本应用启动后会重新 seed 内置工作流与按钮；已删除的执行历史不可恢复。
