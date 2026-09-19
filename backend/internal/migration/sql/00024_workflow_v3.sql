-- +goose Up
-- 新执行契约只用于重新初始化的数据库，不对旧图进行猜测性转换。
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM workflows) THEN
        RAISE EXCEPTION 'Core 4 requires an empty workflow database. Restore the matching backup to roll back.';
    END IF;
END $$;
-- +goose StatementEnd

-- Core 4 ships the built-in plugins at the same API/SDK boundary.  Keep the
-- installation registry in step so dependency checks do not resolve the old
-- 3.x baseline after the reset.
UPDATE plugin_installations
SET version = '4.0.0', updated_at = CURRENT_TIMESTAMP
WHERE source_path = 'builtin'
  AND plugin_id IN ('official.ai', 'official.binance', 'official.connector',
                    'official.notification', 'official.qq', 'official.quant');

ALTER TABLE workflows DROP CONSTRAINT IF EXISTS ck_workflows_trigger;
ALTER TABLE workflows DROP CONSTRAINT IF EXISTS ck_workflows_mode;
ALTER TABLE workflows DROP CONSTRAINT IF EXISTS ck_workflows_status;
ALTER TABLE workflows ADD CONSTRAINT ck_workflows_status CHECK (status IN ('inactive', 'active'));
ALTER TABLE workflow_revisions DROP CONSTRAINT IF EXISTS ck_workflow_revisions_trigger;
ALTER TABLE workflow_runs DROP CONSTRAINT IF EXISTS ck_workflow_runs_entry_point;
ALTER TABLE workflows DROP COLUMN main_trigger_node_id;
ALTER TABLE workflows DROP COLUMN mode;
ALTER TABLE workflow_revisions DROP COLUMN main_trigger_node_id;
ALTER TABLE workflow_revisions ADD CONSTRAINT ck_workflow_graph_v3 CHECK (graph_json->>'schemaVersion' = '3');
ALTER TABLE workflow_runtimes DROP COLUMN next_scheduled_at, DROP COLUMN last_scheduled_at;
ALTER TABLE workflow_runs DROP COLUMN entry_point;
ALTER TABLE workflow_runs ADD COLUMN trigger_node_id VARCHAR(128) NOT NULL CHECK (BTRIM(trigger_node_id) <> '');
ALTER TABLE workflow_runs ADD COLUMN entry_node_instance_id VARCHAR(128) NOT NULL CHECK (BTRIM(entry_node_instance_id) <> '');
ALTER TABLE workflow_runs ADD COLUMN trigger_instance_id VARCHAR(128) NOT NULL CHECK (BTRIM(trigger_instance_id) <> '');
ALTER TABLE workflow_runs ADD COLUMN trigger_event_id VARCHAR(128) NOT NULL DEFAULT '';
ALTER TABLE workflow_runs ADD COLUMN profile_snapshot JSONB NOT NULL DEFAULT '[]';
ALTER TABLE workflow_runs ADD COLUMN operation_json JSONB NOT NULL DEFAULT '{}';
ALTER TABLE workflow_runs ADD COLUMN operation_type VARCHAR(128) NOT NULL DEFAULT '';
ALTER TABLE workflow_runs DROP CONSTRAINT ux_workflow_run_trigger;
ALTER TABLE workflow_runs ADD CONSTRAINT ux_workflow_run_trigger UNIQUE (workflow_id, trigger_node_id, trigger_type, trigger_key);
ALTER TABLE workflow_event_deliveries ADD COLUMN trigger_node_id VARCHAR(128) NOT NULL;
ALTER TABLE workflow_event_deliveries DROP CONSTRAINT ux_workflow_event_delivery;
ALTER TABLE workflow_event_deliveries ADD CONSTRAINT ux_workflow_event_delivery UNIQUE (event_record_id, workflow_id, trigger_node_id);
ALTER TABLE workflow_run_checkpoints ADD COLUMN port VARCHAR(128) NOT NULL DEFAULT 'out';
ALTER TABLE workflow_run_checkpoints ADD COLUMN skipped BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE workflow_node_states ADD COLUMN scope_key VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE workflow_node_states DROP CONSTRAINT workflow_node_states_pkey;
ALTER TABLE workflow_node_states ADD PRIMARY KEY (workflow_id, node_instance_id, scope_key);

CREATE TABLE workflow_trigger_runtimes (
    workflow_id BIGINT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    node_instance_id VARCHAR(128) NOT NULL,
    revision_id BIGINT NOT NULL REFERENCES workflow_revisions(id),
    status VARCHAR(16) NOT NULL CHECK (status IN ('running', 'waiting', 'error', 'disabled')),
    error_category VARCHAR(128) NOT NULL DEFAULT '',
    retry_count INTEGER NOT NULL DEFAULT 0 CHECK (retry_count >= 0 AND retry_count <= 3),
    next_retry_at TIMESTAMPTZ,
    next_scheduled_at TIMESTAMPTZ,
    last_scheduled_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (workflow_id, node_instance_id)
);
CREATE INDEX ix_workflow_trigger_due ON workflow_trigger_runtimes(next_scheduled_at) WHERE status = 'running';

ALTER TABLE workflow_run_nodes DROP CONSTRAINT ck_workflow_run_nodes_attempt;
ALTER TABLE workflow_run_nodes ADD CONSTRAINT ck_workflow_run_nodes_attempt CHECK (attempt>=1 AND loop_iteration>=0);
CREATE TABLE workflow_waits (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 workflow_id BIGINT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
 revision_id BIGINT NOT NULL REFERENCES workflow_revisions(id),
 run_id BIGINT NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE,
 node_instance_id VARCHAR(128) NOT NULL,
 wait_key VARCHAR(256) NOT NULL,
 block_following_runs BOOLEAN NOT NULL DEFAULT FALSE,
 until TIMESTAMPTZ NOT NULL,
 wake_at TIMESTAMPTZ NOT NULL,
 subscription_json JSONB NOT NULL CHECK(jsonb_typeof(subscription_json)='object'),
 data_json JSONB NOT NULL CHECK(jsonb_typeof(data_json)='object'),
 status VARCHAR(16) NOT NULL CHECK(status IN ('active','completed','cancelled')),
 created_at TIMESTAMPTZ NOT NULL,
 updated_at TIMESTAMPTZ NOT NULL,
 UNIQUE(run_id,node_instance_id)
);
CREATE UNIQUE INDEX ux_workflow_active_wait ON workflow_waits(workflow_id,node_instance_id,wait_key) WHERE status='active';
CREATE INDEX ix_workflow_wait_due ON workflow_waits(wake_at) WHERE status='active';

CREATE TABLE workflow_connections (
 id VARCHAR(128) PRIMARY KEY, name VARCHAR(120) NOT NULL, type VARCHAR(128) NOT NULL,
 version BIGINT NOT NULL CHECK(version>0), enabled BOOLEAN NOT NULL,
 config_json JSONB NOT NULL CHECK(jsonb_typeof(config_json)='object'),
 secrets_ciphertext TEXT NOT NULL, secret_fields_json JSONB NOT NULL,
 created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL
);
CREATE TABLE workflow_run_connections (
 run_id BIGINT NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE,
 node_instance_id VARCHAR(128) NOT NULL,
 connection_id VARCHAR(128) NOT NULL, version BIGINT NOT NULL,
 config_json JSONB NOT NULL CHECK(jsonb_typeof(config_json)='object'), secrets_ciphertext TEXT NOT NULL,
 PRIMARY KEY(run_id,node_instance_id)
);

-- Plugin-owned Profile tables are part of the reset baseline.  They are
-- created here so plugin registration never performs DDL during application
-- startup; each plugin still owns the rows and runtime validation.
CREATE TABLE plugin_quant.profiles_quant_backtest (
 id VARCHAR(160) PRIMARY KEY,
 name VARCHAR(120) NOT NULL,
 summary VARCHAR(500) NOT NULL,
 type VARCHAR(160) NOT NULL CHECK(type='quant.backtest'),
 status VARCHAR(16) NOT NULL CHECK(status IN ('draft','active','disabled')),
 draft_config_json JSONB NOT NULL CHECK(jsonb_typeof(draft_config_json)='object'),
 latest_published_version VARCHAR(128) NOT NULL DEFAULT '',
 created_by BIGINT NOT NULL, updated_by BIGINT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL,
 disabled_at TIMESTAMPTZ
);
CREATE INDEX ix_quant_profile_status ON plugin_quant.profiles_quant_backtest(status);
CREATE TABLE plugin_quant.profile_versions_quant_backtest (
 profile_id VARCHAR(160) NOT NULL REFERENCES plugin_quant.profiles_quant_backtest(id) ON DELETE CASCADE,
 version VARCHAR(128) NOT NULL,
 config_json JSONB NOT NULL CHECK(jsonb_typeof(config_json)='object'),
 status VARCHAR(16) NOT NULL CHECK(status IN ('published','disabled')),
 published_at TIMESTAMPTZ, created_by BIGINT NOT NULL, created_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(profile_id,version)
);

CREATE TABLE plugin_binance.profiles_market_data (
 id VARCHAR(160) PRIMARY KEY,
 name VARCHAR(120) NOT NULL,
 summary VARCHAR(500) NOT NULL,
 type VARCHAR(160) NOT NULL CHECK(type='market.data'),
 status VARCHAR(16) NOT NULL CHECK(status IN ('draft','active','disabled')),
 draft_config_json JSONB NOT NULL CHECK(jsonb_typeof(draft_config_json)='object'),
 latest_published_version VARCHAR(128) NOT NULL DEFAULT '',
 created_by BIGINT NOT NULL, updated_by BIGINT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL,
 disabled_at TIMESTAMPTZ
);
CREATE INDEX ix_binance_market_profile_status ON plugin_binance.profiles_market_data(status);
CREATE TABLE plugin_binance.profile_versions_market_data (
 profile_id VARCHAR(160) NOT NULL REFERENCES plugin_binance.profiles_market_data(id) ON DELETE CASCADE,
 version VARCHAR(128) NOT NULL,
 config_json JSONB NOT NULL CHECK(jsonb_typeof(config_json)='object'),
 status VARCHAR(16) NOT NULL CHECK(status IN ('published','disabled')),
 published_at TIMESTAMPTZ, created_by BIGINT NOT NULL, created_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(profile_id,version)
);

CREATE TABLE plugin_binance.profiles_trading_account (
 id VARCHAR(160) PRIMARY KEY,
 name VARCHAR(120) NOT NULL,
 summary VARCHAR(500) NOT NULL,
 type VARCHAR(160) NOT NULL CHECK(type='trading.account'),
 status VARCHAR(16) NOT NULL CHECK(status IN ('draft','active','disabled')),
 draft_config_json JSONB NOT NULL CHECK(jsonb_typeof(draft_config_json)='object'),
 latest_published_version VARCHAR(128) NOT NULL DEFAULT '',
 created_by BIGINT NOT NULL, updated_by BIGINT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL,
 disabled_at TIMESTAMPTZ
);
CREATE INDEX ix_binance_account_profile_status ON plugin_binance.profiles_trading_account(status);
CREATE TABLE plugin_binance.profile_versions_trading_account (
 profile_id VARCHAR(160) NOT NULL REFERENCES plugin_binance.profiles_trading_account(id) ON DELETE CASCADE,
 version VARCHAR(128) NOT NULL,
 config_json JSONB NOT NULL CHECK(jsonb_typeof(config_json)='object'),
 status VARCHAR(16) NOT NULL CHECK(status IN ('published','disabled')),
 published_at TIMESTAMPTZ, created_by BIGINT NOT NULL, created_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(profile_id,version)
);

CREATE TABLE plugin_binance.profiles_trading_risk (
 id VARCHAR(160) PRIMARY KEY,
 name VARCHAR(120) NOT NULL,
 summary VARCHAR(500) NOT NULL,
 type VARCHAR(160) NOT NULL CHECK(type='trading.risk'),
 status VARCHAR(16) NOT NULL CHECK(status IN ('draft','active','disabled')),
 draft_config_json JSONB NOT NULL CHECK(jsonb_typeof(draft_config_json)='object'),
 latest_published_version VARCHAR(128) NOT NULL DEFAULT '',
 created_by BIGINT NOT NULL, updated_by BIGINT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL,
 disabled_at TIMESTAMPTZ
);
CREATE INDEX ix_binance_risk_profile_status ON plugin_binance.profiles_trading_risk(status);
CREATE TABLE plugin_binance.profile_versions_trading_risk (
 profile_id VARCHAR(160) NOT NULL REFERENCES plugin_binance.profiles_trading_risk(id) ON DELETE CASCADE,
 version VARCHAR(128) NOT NULL,
 config_json JSONB NOT NULL CHECK(jsonb_typeof(config_json)='object'),
 status VARCHAR(16) NOT NULL CHECK(status IN ('published','disabled')),
 published_at TIMESTAMPTZ, created_by BIGINT NOT NULL, created_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(profile_id,version)
);

CREATE TABLE plugin_binance.candle_sources (
 workflow_id BIGINT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
 node_instance_id VARCHAR(128) NOT NULL, market VARCHAR(8) NOT NULL,
 instrument VARCHAR(32) NOT NULL, interval VARCHAR(8) NOT NULL, updated_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(workflow_id,node_instance_id,market,instrument,interval)
);
CREATE INDEX ix_binance_candle_sources_series ON plugin_binance.candle_sources(market,instrument,interval);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    RAISE EXCEPTION 'Core 4 is a breaking baseline. Restore the prior database backup with the prior application image.';
END $$;
-- +goose StatementEnd
