-- ============================================
-- CoinSphere 工作流优化 - 数据库迁移
-- 版本：00024
-- 说明：添加配置模板、节点升级支持
-- ============================================

-- Up Migration

-- 1. 节点配置模板表
CREATE TABLE IF NOT EXISTS node_config_templates (
    id VARCHAR(128) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    category VARCHAR(64) NOT NULL,
    node_type VARCHAR(255) NOT NULL,
    level VARCHAR(32) NOT NULL CHECK (level IN ('basic', 'advanced', 'expert')),
    preset JSONB NOT NULL,
    tags TEXT[] DEFAULT '{}',
    is_builtin BOOLEAN DEFAULT false,
    created_by VARCHAR(64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_node_config_templates_node_type ON node_config_templates(node_type);
CREATE INDEX IF NOT EXISTS idx_node_config_templates_category ON node_config_templates(category);
CREATE INDEX IF NOT EXISTS idx_node_config_templates_level ON node_config_templates(level);
CREATE INDEX IF NOT EXISTS idx_node_config_templates_builtin ON node_config_templates(is_builtin) WHERE is_builtin = true;

COMMENT ON TABLE node_config_templates IS '节点配置模板表';
COMMENT ON COLUMN node_config_templates.level IS '配置层级: basic=基础, advanced=高级, expert=专家';
COMMENT ON COLUMN node_config_templates.preset IS '预填充配置 JSON';

-- 2. 工作流执行缓存元数据表（用于性能监控）
CREATE TABLE IF NOT EXISTS workflow_cache_stats (
    id BIGSERIAL PRIMARY KEY,
    workflow_id BIGINT NOT NULL,
    run_id BIGINT NOT NULL,
    cache_key VARCHAR(512) NOT NULL,
    hit_count INT DEFAULT 0,
    miss_count INT DEFAULT 0,
    data_size_bytes BIGINT DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_workflow_cache_workflow FOREIGN KEY (workflow_id) REFERENCES workflows(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_workflow_cache_stats_workflow_run ON workflow_cache_stats(workflow_id, run_id);
CREATE INDEX IF NOT EXISTS idx_workflow_cache_stats_created_at ON workflow_cache_stats(created_at);

COMMENT ON TABLE workflow_cache_stats IS '工作流缓存统计（性能监控）';

-- 3. 插入内置配置模板
INSERT INTO node_config_templates (id, name, description, category, node_type, level, preset, tags, is_builtin) VALUES
-- SMA 双均线交叉策略（基础）
('builtin.strategy.sma_crossover_basic', 'SMA 双均线交叉', '快线上穿慢线做多，下穿做空', 'strategy', 'official.quant.custom_strategy', 'basic',
 '{"instrument": "BTCUSDT", "interval": "1h", "fastPeriod": 10, "slowPeriod": 20}',
 ARRAY['趋势', '简单', '入门'], true),

-- MACD + RSI 组合策略（高级）
('builtin.strategy.macd_rsi_combo', 'MACD+RSI 组合策略', 'MACD 金叉且 RSI 超卖时做多', 'strategy', 'official.quant.custom_strategy', 'advanced',
 '{"instrument": "ETHUSDT", "interval": "4h", "indicators": [{"type": "macd", "params": {"fast": 12, "slow": 26, "signal": 9}}, {"type": "rsi", "params": {"period": 14}}]}',
 ARRAY['趋势', 'MACD', 'RSI'], true),

-- 网格交易策略（专家）
('builtin.strategy.grid_trading', '网格交易策略', '价格区间内网格买卖', 'strategy', 'official.quant.custom_strategy', 'expert',
 '{"instrument": "BNBUSDT", "interval": "15m", "gridLevels": 10, "priceRange": {"min": "500", "max": "600"}}',
 ARRAY['网格', '震荡'], true),

-- 均线突破策略
('builtin.strategy.ma_breakout', '均线突破策略', '价格突破均线入场', 'strategy', 'official.quant.custom_strategy', 'basic',
 '{"instrument": "BTCUSDT", "interval": "1h", "maPeriod": 20, "breakoutType": "above"}',
 ARRAY['趋势', '突破'], true),

-- 指标模板：RSI 超卖
('builtin.indicator.rsi_oversold', 'RSI 超卖监控', 'RSI 低于 30 时触发', 'indicator', 'official.quant.technical_indicator', 'basic',
 '{"instrument": "BTCUSDT", "interval": "1h", "indicator": {"type": "rsi", "parameters": {"period": 14, "threshold": "30", "direction": "below"}}}',
 ARRAY['RSI', '超卖'], true),

-- 指标模板：MACD 金叉
('builtin.indicator.macd_golden', 'MACD 金叉监控', 'MACD 出现金叉时触发', 'indicator', 'official.quant.technical_indicator', 'basic',
 '{"instrument": "BTCUSDT", "interval": "4h", "indicator": {"type": "macd", "parameters": {"fastPeriod": 12, "slowPeriod": 26, "signalPeriod": 9, "signal": "golden_cross"}}}',
 ARRAY['MACD', '金叉'], true),

-- 指标模板：成交量放大
('builtin.indicator.volume_spike', '成交量放大监控', '成交量超过均值 2 倍时触发', 'indicator', 'official.quant.technical_indicator', 'basic',
 '{"instrument": "BTCUSDT", "interval": "1h", "indicator": {"type": "volume_spike", "parameters": {"lookback": 20, "multiplier": "2"}}}',
 ARRAY['成交量', '放量'], true),

-- 指标模板：布林带突破
('builtin.indicator.bollinger_breakout', '布林带突破监控', '价格突破布林带上轨', 'indicator', 'official.quant.technical_indicator', 'basic',
 '{"instrument": "BTCUSDT", "interval": "1h", "indicator": {"type": "bollinger", "parameters": {"period": 20, "multiplier": "2", "signal": "close_above_upper"}}}',
 ARRAY['布林带', '突破'], true);

-- 4. 添加节点元数据扩展（支持分组、层级配置）
-- 现有 workflows 表已包含 graph JSONB，无需修改表结构
-- 节点配置通过 graph.nodes[].config 存储，支持动态 schema

-- 5. 创建工作流迁移日志表
CREATE TABLE IF NOT EXISTS workflow_migration_logs (
    id BIGSERIAL PRIMARY KEY,
    workflow_id BIGINT NOT NULL,
    old_revision_id BIGINT,
    new_revision_id BIGINT,
    migration_type VARCHAR(64) NOT NULL, -- 'node_upgrade', 'config_migration', 'manual'
    migrated_nodes JSONB, -- [{nodeInstanceId, oldType, newType, success, error}]
    summary JSONB, -- {totalNodes, migratedCount, errorCount}
    executed_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    executed_by VARCHAR(64),
    CONSTRAINT fk_migration_workflow FOREIGN KEY (workflow_id) REFERENCES workflows(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_migration_logs_workflow ON workflow_migration_logs(workflow_id);
CREATE INDEX IF NOT EXISTS idx_migration_logs_executed_at ON workflow_migration_logs(executed_at DESC);

COMMENT ON TABLE workflow_migration_logs IS '工作流迁移日志';

-- 6. 更新 workflows 表注释（无需修改结构，仅添加文档）
COMMENT ON COLUMN workflows.graph IS '工作流图定义 JSON，支持分层配置和参数分组';

-- Down Migration（如需回滚）
-- DROP TABLE IF EXISTS workflow_migration_logs;
-- DROP TABLE IF EXISTS workflow_cache_stats;
-- DROP TABLE IF EXISTS node_config_templates;
