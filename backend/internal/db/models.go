// Package db GORM 模型定义。表名与列名与原 Python(Peewee)后端保持一致。
package db

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// BlockbeatsNews 新闻数据行。
type BlockbeatsNews struct {
	// 一个 struct 就是一张数据库表,一个字段就是一列。反引号里的 `gorm:"..."` 是给 GORM 看的标签:
	//   primaryKey;autoIncrement = 主键且自增;column:xxx = 指定列名(不写就用字段名转小写下划线);
	//   size:1000 = 字符串列长度;type:text = 长文本;index = 建普通索引。
	// 字段用指针类型(如 *int64、*time.Time)表示"可为 NULL / 可选";非指针字段则非空、缺省用零值。
	// 字段首字母大写才会被 GORM(外部包)访问到(见 GO入门笔记『可见性』),所以字段名都大写开头。
	ID              int64      `gorm:"primaryKey;autoIncrement"`
	SourceMessageID *int64     `gorm:"index"`
	PublishedAt     *time.Time `gorm:""`
	SourceURL       string     `gorm:"column:source_url;size:1000"`
	Title           string     `gorm:"size:255"`
	Content         string     `gorm:"type:text"`
	OriginalURL     string     `gorm:"column:original_url;size:1000"`
	ImageURL        string     `gorm:"column:image_url;size:1000"`
}

// TableName 方法让 GORM 用返回的字符串当表名,而不是按 struct 名自动推断(这样才能对齐原 Python 表名)。
// 接收者写成 (BlockbeatsNews) 不带变量名,是因为方法体里用不到具体对象。下面每个模型都有一个同名方法。
func (BlockbeatsNews) TableName() string { return "news_items" }

// MarketInstrument 是共享的 Binance 交易标的元数据。
type MarketInstrument struct {
	ID           uuid.UUID       `gorm:"type:uuid;primaryKey"`
	Venue        string          `gorm:"size:16"`
	Market       string          `gorm:"column:market_type;size:32"`
	NativeSymbol string          `gorm:"size:64"`
	BaseAsset    string          `gorm:"size:32"`
	QuoteAsset   string          `gorm:"size:32"`
	Status       string          `gorm:"size:16"`
	PriceTick    decimal.Decimal `gorm:"type:numeric(38,18)"`
	QuantityStep decimal.Decimal `gorm:"type:numeric(38,18)"`
	MinQuantity  decimal.Decimal `gorm:"type:numeric(38,18)"`
	MinNotional  decimal.Decimal `gorm:"type:numeric(38,18)"`
	UpdatedAt    time.Time
}

func (MarketInstrument) TableName() string { return "market_instruments" }

// MarketCandle 是持久化的标准化 OHLCV 数据。
type MarketCandle struct {
	Venue        string          `gorm:"size:16;primaryKey"`
	InstrumentID uuid.UUID       `gorm:"column:instrument_id;type:uuid;primaryKey"`
	Interval     string          `gorm:"column:interval_code;size:4;primaryKey"`
	OpenTime     time.Time       `gorm:"column:open_time;primaryKey"`
	CloseTime    time.Time       `gorm:"column:close_time"`
	Open         decimal.Decimal `gorm:"column:open_price;type:numeric(38,18)"`
	High         decimal.Decimal `gorm:"column:high_price;type:numeric(38,18)"`
	Low          decimal.Decimal `gorm:"column:low_price;type:numeric(38,18)"`
	Close        decimal.Decimal `gorm:"column:close_price;type:numeric(38,18)"`
	BaseVolume   decimal.Decimal `gorm:"column:base_volume;type:numeric(38,18)"`
	IsClosed     bool            `gorm:"column:is_closed"`
}

func (MarketCandle) TableName() string { return "market_candles" }

// WatchlistItem 是用户私有自选，资源 API 始终按 OwnerUserID 过滤。
type WatchlistItem struct {
	ID           uuid.UUID `gorm:"type:uuid;primaryKey"`
	OwnerUserID  int64     `gorm:"column:owner_user_id"`
	InstrumentID uuid.UUID `gorm:"column:instrument_id;type:uuid"`
	Interval     string    `gorm:"column:interval_code;size:4"`
	CreatedAt    time.Time `gorm:"column:created_at"`
}

func (WatchlistItem) TableName() string { return "watchlist_items" }

// StrategyDraft 是管理员可编辑的单文件策略草稿。
type StrategyDraft struct {
	ID                  uuid.UUID `gorm:"type:uuid;primaryKey"`
	Name                string
	SourceCode          string `gorm:"column:source_code;type:text"`
	Market              string `gorm:"column:market_type"`
	InstrumentID        uuid.UUID
	Interval            string `gorm:"column:interval_code"`
	LookbackBars        int
	ParameterSchemaJSON string `gorm:"column:parameter_schema_json;type:jsonb"`
	RuntimeVersion      string
	CreatedByUserID     int64
	UpdatedByUserID     int64
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

func (StrategyDraft) TableName() string { return "strategies" }

// StrategyVersion 是发布后由数据库保护的不可变策略快照。
type StrategyVersion struct {
	ID                  uuid.UUID `gorm:"type:uuid;primaryKey"`
	StrategyID          uuid.UUID
	VersionNumber       int
	Status              string
	WorkerTaskID        string
	IdempotencyRecordID int64
	Name                string
	SourceCode          string `gorm:"column:source_code;type:text"`
	CodeSHA256          string `gorm:"column:code_sha256"`
	RuntimeVersion      string
	Market              string `gorm:"column:market_type"`
	InstrumentID        uuid.UUID
	Symbol              string
	Interval            string `gorm:"column:interval_code"`
	LookbackBars        int
	ParameterSchemaJSON string `gorm:"column:parameter_schema_json;type:jsonb"`
	PublishedByUserID   int64
	PublishedAt         *time.Time
	CreatedAt           time.Time
}

func (StrategyVersion) TableName() string { return "strategy_versions" }

// Backtest 是用户私有回测请求；执行状态由关联的 worker_tasks 行持有。
type Backtest struct {
	ID                     uuid.UUID `gorm:"type:uuid;primaryKey"`
	OwnerUserID            int64
	StrategyVersionID      uuid.UUID
	WorkerTaskID           string
	IdempotencyRecordID    int64
	SimulatorVersion       string
	ParametersJSON         string `gorm:"column:parameters_json;type:jsonb"`
	StartTime              time.Time
	EndTime                time.Time
	AllocationUSDT         decimal.Decimal  `gorm:"column:allocation_usdt;type:numeric(38,18)"`
	InitialEquity          decimal.Decimal  `gorm:"type:numeric(38,18)"`
	FeeRate                decimal.Decimal  `gorm:"type:numeric(38,18)"`
	SlippageRate           decimal.Decimal  `gorm:"type:numeric(38,18)"`
	FundingRatesJSON       string           `gorm:"column:funding_rates_json;type:jsonb"`
	StopLossRatio          *decimal.Decimal `gorm:"type:numeric(38,18)"`
	MaintenanceMarginRatio *decimal.Decimal `gorm:"type:numeric(38,18)"`
	SummaryJSON            *string          `gorm:"column:summary_json;type:jsonb"`
	InputSHA256            *string          `gorm:"column:input_sha256"`
	ResultSHA256           *string          `gorm:"column:result_sha256"`
	ManifestSHA256         *string          `gorm:"column:manifest_sha256"`
	CreatedAt              time.Time
}

func (Backtest) TableName() string { return "backtests" }

// StrategyInstance 是用户启用的实时策略配置；下单能力不属于本阶段。
type StrategyInstance struct {
	ID                uuid.UUID `gorm:"type:uuid;primaryKey"`
	OwnerUserID       int64
	StrategyVersionID uuid.UUID
	TradingAccountID  *uuid.UUID       `gorm:"column:trading_account_id;type:uuid"`
	AllocationUSDT    *decimal.Decimal `gorm:"column:allocation_usdt;type:numeric(38,18)"`
	Name              string
	Mode              string
	Environment       string
	ParametersJSON    string `gorm:"column:parameters_json;type:jsonb"`
	IsEnabled         bool   `gorm:"column:is_enabled"`
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

func (StrategyInstance) TableName() string { return "strategy_instances" }

// StrategySignal 是闭合 K 线上的持久化策略目标，不代表订单或成交。
type StrategySignal struct {
	ID                          uuid.UUID `gorm:"type:uuid;primaryKey"`
	OwnerUserID                 int64
	StrategyInstanceID          uuid.UUID
	StrategyVersionID           uuid.UUID
	InstrumentID                uuid.UUID
	Interval                    string `gorm:"column:interval_code"`
	CandleOpenTime              time.Time
	CandleCloseTime             time.Time
	Target                      decimal.Decimal `gorm:"type:numeric(38,18)"`
	Mode                        string
	Environment                 string
	Status                      string
	ExpiresAt                   *time.Time
	DecisionIdempotencyRecordID *int64
	DecidedByUserID             *int64
	DecidedAt                   *time.Time
	CreatedAt                   time.Time
}

func (StrategySignal) TableName() string { return "strategy_signals" }

// TradingControl 持有唯一的全局 Paper 交易急停状态。
type TradingControl struct {
	ID               int16      `gorm:"primaryKey"`
	EmergencyStopped bool       `gorm:"column:emergency_stopped"`
	StopReason       string     `gorm:"column:stop_reason;size:255"`
	StoppedAt        time.Time  `gorm:"column:stopped_at"`
	StoppedByUserID  *int64     `gorm:"column:stopped_by_user_id"`
	ReleasedAt       *time.Time `gorm:"column:released_at"`
	ReleasedByUserID *int64     `gorm:"column:released_by_user_id"`
	UpdatedAt        time.Time  `gorm:"column:updated_at"`
}

func (TradingControl) TableName() string { return "trading_controls" }

// TradingAccount 是用户交易账户与硬风控配置。Live 账户仍由数据库约束禁止。
type TradingAccount struct {
	ID                          uuid.UUID        `gorm:"type:uuid;primaryKey"`
	OwnerUserID                 int64            `gorm:"column:owner_user_id"`
	Name                        string           `gorm:"size:120"`
	Market                      string           `gorm:"column:market_type;size:16"`
	Environment                 string           `gorm:"size:16"`
	Status                      string           `gorm:"size:16"`
	PauseReason                 string           `gorm:"column:pause_reason;size:255"`
	AutomationEnabled           bool             `gorm:"column:automation_enabled"`
	AutomationAuthorizedAt      *time.Time       `gorm:"column:automation_authorized_at"`
	AutomationAuthorizedByID    *int64           `gorm:"column:automation_authorized_by_user_id"`
	InitialBalance              *decimal.Decimal `gorm:"column:initial_balance;type:numeric(38,18)"`
	PaperFeeRate                *decimal.Decimal `gorm:"column:paper_fee_rate;type:numeric(38,18)"`
	MaxTotalNotional            *decimal.Decimal `gorm:"column:max_total_notional;type:numeric(38,18)"`
	MaxSymbolNotional           *decimal.Decimal `gorm:"column:max_symbol_notional;type:numeric(38,18)"`
	MaxOrderNotional            *decimal.Decimal `gorm:"column:max_order_notional;type:numeric(38,18)"`
	MaxDailyLoss                *decimal.Decimal `gorm:"column:max_daily_loss;type:numeric(38,18)"`
	MaxDrawdown                 *decimal.Decimal `gorm:"column:max_drawdown;type:numeric(38,18)"`
	MaxQuoteAgeSeconds          *int             `gorm:"column:max_quote_age_seconds"`
	Leverage                    *int
	CreationIdempotencyRecordID *int64    `gorm:"column:creation_idempotency_record_id"`
	CreatedAt                   time.Time `gorm:"column:created_at"`
	UpdatedAt                   time.Time `gorm:"column:updated_at"`
}

func (TradingAccount) TableName() string { return "trading_accounts" }

// TradingAccountCredential 保存 Testnet 凭据的密文和验证状态；明文只在写入和 Executor 解密边界短暂存在。
type TradingAccountCredential struct {
	ID                    uuid.UUID  `gorm:"type:uuid;primaryKey"`
	AccountID             uuid.UUID  `gorm:"column:account_id;type:uuid;uniqueIndex"`
	OwnerUserID           int64      `gorm:"column:owner_user_id"`
	APIKeyCiphertext      string     `gorm:"column:api_key_ciphertext;type:text"`
	APISecretCiphertext   string     `gorm:"column:api_secret_ciphertext;type:text"`
	WithdrawalDisabled    bool       `gorm:"column:withdrawal_disabled"`
	IPWhitelistConfigured bool       `gorm:"column:ip_whitelist_configured"`
	Status                string     `gorm:"size:16"`
	VerificationStatus    string     `gorm:"column:verification_status;size:16"`
	VerificationErrorCode string     `gorm:"column:verification_error_code;size:64"`
	LastVerifiedAt        *time.Time `gorm:"column:last_verified_at"`
	CreatedAt             time.Time  `gorm:"column:created_at"`
	UpdatedAt             time.Time  `gorm:"column:updated_at"`
}

func (TradingAccountCredential) TableName() string { return "trading_account_credentials" }

// TradingAccountInstrument 是账户品种白名单。
type TradingAccountInstrument struct {
	AccountID    uuid.UUID `gorm:"column:account_id;type:uuid;primaryKey"`
	InstrumentID uuid.UUID `gorm:"column:instrument_id;type:uuid;primaryKey"`
	CreatedAt    time.Time `gorm:"column:created_at"`
}

func (TradingAccountInstrument) TableName() string { return "trading_account_instruments" }

// TradingIntent 是策略目标到 Executor 的持久化幂等命令。
type TradingIntent struct {
	ID                 uuid.UUID       `gorm:"type:uuid;primaryKey"`
	AccountID          uuid.UUID       `gorm:"column:account_id;type:uuid"`
	StrategySignalID   uuid.UUID       `gorm:"column:strategy_signal_id;type:uuid;uniqueIndex"`
	StrategyInstanceID uuid.UUID       `gorm:"column:strategy_instance_id;type:uuid"`
	OwnerUserID        int64           `gorm:"column:owner_user_id"`
	InstrumentID       uuid.UUID       `gorm:"column:instrument_id;type:uuid"`
	Market             string          `gorm:"column:market_type;size:16"`
	Mode               string          `gorm:"size:16"`
	Environment        string          `gorm:"size:16"`
	Target             decimal.Decimal `gorm:"type:numeric(38,18)"`
	Status             string          `gorm:"size:16"`
	BlockReason        string          `gorm:"column:block_reason;size:255"`
	ClientOrderID      string          `gorm:"column:client_order_id;size:64;uniqueIndex:ux_trading_intents_account_client_order"`
	AttemptCount       int             `gorm:"column:attempt_count"`
	ClaimedAt          *time.Time      `gorm:"column:claimed_at"`
	WorkerID           *string         `gorm:"column:worker_id;size:120"`
	CompletedAt        *time.Time      `gorm:"column:completed_at"`
	CreatedAt          time.Time       `gorm:"column:created_at"`
	UpdatedAt          time.Time       `gorm:"column:updated_at"`
}

func (TradingIntent) TableName() string { return "trading_intents" }

// PaperOrder 是 Paper 内部订单投影。
type PaperOrder struct {
	ID             uuid.UUID       `gorm:"type:uuid;primaryKey"`
	AccountID      uuid.UUID       `gorm:"column:account_id;type:uuid"`
	IntentID       uuid.UUID       `gorm:"column:intent_id;type:uuid;uniqueIndex"`
	InstrumentID   uuid.UUID       `gorm:"column:instrument_id;type:uuid"`
	ClientOrderID  string          `gorm:"column:client_order_id;size:64;uniqueIndex:ux_paper_orders_account_client_order"`
	Side           string          `gorm:"size:4"`
	Quantity       decimal.Decimal `gorm:"type:numeric(38,18)"`
	FilledQuantity decimal.Decimal `gorm:"column:filled_quantity;type:numeric(38,18)"`
	AveragePrice   decimal.Decimal `gorm:"column:average_price;type:numeric(38,18)"`
	Status         string          `gorm:"size:16"`
	CreatedAt      time.Time       `gorm:"column:created_at"`
	UpdatedAt      time.Time       `gorm:"column:updated_at;autoUpdateTime:false"`
}

func (PaperOrder) TableName() string { return "paper_orders" }

// TradingEvent 是受限的追加式 order/fill/fee/funding 事实。
type TradingEvent struct {
	ID           int64            `gorm:"primaryKey;autoIncrement"`
	EventID      uuid.UUID        `gorm:"column:event_id;type:uuid;uniqueIndex"`
	AccountID    uuid.UUID        `gorm:"column:account_id;type:uuid"`
	IntentID     *uuid.UUID       `gorm:"column:intent_id;type:uuid"`
	OrderID      *uuid.UUID       `gorm:"column:order_id;type:uuid"`
	InstrumentID uuid.UUID        `gorm:"column:instrument_id;type:uuid"`
	EventType    string           `gorm:"column:event_type;size:16"`
	Side         *string          `gorm:"size:4"`
	Quantity     *decimal.Decimal `gorm:"type:numeric(38,18)"`
	Price        *decimal.Decimal `gorm:"type:numeric(38,18)"`
	Amount       *decimal.Decimal `gorm:"type:numeric(38,18)"`
	OccurredAt   time.Time        `gorm:"column:occurred_at"`
	DedupeKey    string           `gorm:"column:dedupe_key;size:160"`
	CorrectionOf *int64           `gorm:"column:correction_of"`
	CreatedAt    time.Time        `gorm:"column:created_at"`
}

func (TradingEvent) TableName() string { return "trading_events" }

// PaperPosition 是由交易事件重建的账户品种仓位。
type PaperPosition struct {
	AccountID               uuid.UUID       `gorm:"column:account_id;type:uuid;primaryKey"`
	InstrumentID            uuid.UUID       `gorm:"column:instrument_id;type:uuid;primaryKey"`
	OwnerStrategyInstanceID *uuid.UUID      `gorm:"column:owner_strategy_instance_id;type:uuid"`
	Quantity                decimal.Decimal `gorm:"type:numeric(38,18)"`
	AverageEntryPrice       decimal.Decimal `gorm:"column:average_entry_price;type:numeric(38,18)"`
	LastPrice               decimal.Decimal `gorm:"column:last_price;type:numeric(38,18)"`
	RealizedPnl             decimal.Decimal `gorm:"column:realized_pnl;type:numeric(38,18)"`
	UnrealizedPnl           decimal.Decimal `gorm:"column:unrealized_pnl;type:numeric(38,18)"`
	UpdatedAt               time.Time       `gorm:"column:updated_at;autoUpdateTime:false"`
}

func (PaperPosition) TableName() string { return "paper_positions" }

// PaperBalance 是由事件重建的账户余额与盈亏汇总。
type PaperBalance struct {
	AccountID      uuid.UUID       `gorm:"column:account_id;type:uuid;primaryKey"`
	CashBalance    decimal.Decimal `gorm:"column:cash_balance;type:numeric(38,18)"`
	Equity         decimal.Decimal `gorm:"type:numeric(38,18)"`
	PeakEquity     decimal.Decimal `gorm:"column:peak_equity;type:numeric(38,18)"`
	DayStartDate   time.Time       `gorm:"column:day_start_date;type:date"`
	DayStartEquity decimal.Decimal `gorm:"column:day_start_equity;type:numeric(38,18)"`
	RealizedPnl    decimal.Decimal `gorm:"column:realized_pnl;type:numeric(38,18)"`
	UnrealizedPnl  decimal.Decimal `gorm:"column:unrealized_pnl;type:numeric(38,18)"`
	Fees           decimal.Decimal `gorm:"type:numeric(38,18)"`
	Funding        decimal.Decimal `gorm:"type:numeric(38,18)"`
	UpdatedAt      time.Time       `gorm:"column:updated_at;autoUpdateTime:false"`
}

func (PaperBalance) TableName() string { return "paper_balances" }

// WorkflowDefinition 不可变的工作流定义版本。
type WorkflowDefinition struct {
	// 多个字段共用同一个 uniqueIndex 名(ux_workflow_def_code_version)并带 priority = 联合唯一索引:
	// 这里约束 (code, version) 组合唯一。default:1 表示这一列在数据库里的默认值。
	ID          int64  `gorm:"primaryKey;autoIncrement"`
	Code        string `gorm:"size:120;uniqueIndex:ux_workflow_def_code_version,priority:1"`
	Version     int    `gorm:"default:1;uniqueIndex:ux_workflow_def_code_version,priority:2"`
	DisplayName string `gorm:"size:255"`
	Description string `gorm:"type:text"`
	GraphJSON   string `gorm:"column:graph_json;type:text"`
	IsBuiltin   bool   `gorm:"default:false"`
	CreatedBy   *int64
	CreatedAt   time.Time
}

func (WorkflowDefinition) TableName() string { return "workflow_definitions" }

// WorkflowRuntimeState 每个 workflow code 的激活状态。
type WorkflowRuntimeState struct {
	// "外键 ID 列 + 关联对象"成对出现,是 GORM 表关联的写法:
	// ActiveWorkflowDefinitionID 是真正存进表里的外键列;ActiveWorkflowDefinition 是查询时可一并加载的关联对象(表里没有这一列)。
	// foreignKey 指明用哪个字段做外键;constraint:OnDelete:SET NULL = 被引用行被删时,把这里的外键置为 NULL。
	ID                         int64               `gorm:"primaryKey;autoIncrement"`
	WorkflowCode               string              `gorm:"size:120;uniqueIndex"`
	ActiveWorkflowDefinitionID *int64              `gorm:"column:active_workflow_definition_id"`
	ActiveWorkflowDefinition   *WorkflowDefinition `gorm:"foreignKey:ActiveWorkflowDefinitionID;constraint:OnDelete:SET NULL"`
	ActivatedAt                *time.Time
	ActivatedBy                *int64
	UpdatedAt                  time.Time
}

func (WorkflowRuntimeState) TableName() string { return "workflow_runtime_states" }

// WorkflowRuntimeEntry 激活定义中的一个开始入口的运行注册状态。
type WorkflowRuntimeEntry struct {
	ID                     int64                 `gorm:"primaryKey;autoIncrement"`
	WorkflowRuntimeStateID int64                 `gorm:"column:workflow_runtime_state_id;uniqueIndex:ux_runtime_entry_state_key,priority:1"`
	WorkflowRuntimeState   *WorkflowRuntimeState `gorm:"foreignKey:WorkflowRuntimeStateID;constraint:OnDelete:CASCADE"`
	WorkflowDefinitionID   int64                 `gorm:"column:workflow_definition_id;index:ix_runtime_entry_def_key,priority:1"`
	WorkflowDefinition     *WorkflowDefinition   `gorm:"foreignKey:WorkflowDefinitionID;constraint:OnDelete:CASCADE"`
	StartNodeID            string                `gorm:"column:start_node_id;size:100"`
	EntryKey               string                `gorm:"size:64;uniqueIndex:ux_runtime_entry_state_key,priority:2;index:ix_runtime_entry_def_key,priority:2"`
	StartType              string                `gorm:"size:32"`
	IsEnabled              bool                  `gorm:"default:true"`
	RegistrationStatus     string                `gorm:"size:20;default:ready"`
	ScheduleJobID          string                `gorm:"column:schedule_job_id;size:255"`
	NextRunAt              *time.Time
	LastTriggeredAt        *time.Time
	LastErrorMessage       string `gorm:"type:text"`
	SecretHash             string `gorm:"size:255"`
	SecretHint             string `gorm:"size:32"`
	SecretRotatedAt        *time.Time
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

func (WorkflowRuntimeEntry) TableName() string { return "workflow_runtime_entries" }

// WorkflowExecution 一次具体执行。status 即队列状态,替代原 Redis Stream。
type WorkflowExecution struct {
	ID                   int64               `gorm:"primaryKey;autoIncrement"`
	WorkflowDefinitionID int64               `gorm:"column:workflow_definition_id;index"`
	WorkflowDefinition   *WorkflowDefinition `gorm:"foreignKey:WorkflowDefinitionID;constraint:OnDelete:RESTRICT"`
	StartEntryKey        string              `gorm:"size:64"`
	StartNodeID          string              `gorm:"column:start_node_id;size:100"`
	StartNodeType        string              `gorm:"size:32"`
	TriggerType          string              `gorm:"size:32;uniqueIndex:ux_workflow_exec_trigger_idem,priority:1"`
	TriggeredBy          *int64
	TriggerKey           *string `gorm:"size:255"`
	IdempotencyKey       *string `gorm:"size:255;uniqueIndex:ux_workflow_exec_trigger_idem,priority:2"`
	ConcurrencyKey       string  `gorm:"size:255;index:ix_workflow_exec_backlog,priority:1"`
	TriggerOutboxID      *int64
	Status               string    `gorm:"size:32;default:queued;index:ix_workflow_exec_queue,priority:1;index:ix_workflow_exec_backlog,priority:2"`
	QueuedAt             time.Time `gorm:"index:ix_workflow_exec_queue,priority:2"`
	ClaimedAt            *time.Time
	StartedAt            *time.Time
	FinishedAt           *time.Time
	LastHeartbeatAt      *time.Time `gorm:"index"`
	WorkerID             *string    `gorm:"column:worker_id;size:120"`
	AttemptCount         int        `gorm:"default:0"`
	MaxAttempts          int        `gorm:"default:4"`
	DurationMs           *int64
	NextRetryAt          *time.Time `gorm:"index"`
	FailureCategory      string     `gorm:"size:64"`
	InputSnapshotJSON    string     `gorm:"column:input_snapshot_json;type:text"`
	ContextSnapshotJSON  string     `gorm:"column:context_snapshot_json;type:text"`
	ResultSnapshotJSON   string     `gorm:"column:result_snapshot_json;type:text"`
	ErrorMessage         string     `gorm:"type:text"`
}

func (WorkflowExecution) TableName() string { return "workflow_executions" }

// IdempotencyRecord binds a public command key to one authenticated user and request payload.
// Only hashes are persisted; workflow executions use a key derived from this record's ID.
type IdempotencyRecord struct {
	ID          int64     `gorm:"primaryKey;autoIncrement"`
	UserID      int64     `gorm:"column:user_id;uniqueIndex:ux_idempotency_records_user_scope_key,priority:1"`
	Scope       string    `gorm:"size:255;uniqueIndex:ux_idempotency_records_user_scope_key,priority:2"`
	KeyHash     string    `gorm:"column:key_hash;size:64;uniqueIndex:ux_idempotency_records_user_scope_key,priority:3"`
	RequestHash string    `gorm:"column:request_hash;size:64"`
	ExpiresAt   time.Time `gorm:"index:ix_idempotency_records_expires_at"`
	CreatedAt   time.Time
}

func (IdempotencyRecord) TableName() string { return "idempotency_records" }

// WorkflowExecutionAttempt 单次执行的 attempt 历史。
type WorkflowExecutionAttempt struct {
	ID                  int64              `gorm:"primaryKey;autoIncrement"`
	WorkflowExecutionID int64              `gorm:"column:workflow_execution_id;uniqueIndex:ux_workflow_exec_attempt,priority:1"`
	WorkflowExecution   *WorkflowExecution `gorm:"foreignKey:WorkflowExecutionID;constraint:OnDelete:CASCADE"`
	Attempt             int                `gorm:"default:1;uniqueIndex:ux_workflow_exec_attempt,priority:2"`
	WorkerID            string             `gorm:"column:worker_id;size:120"`
	StartedAt           time.Time
	FinishedAt          *time.Time
	FailureCategory     string `gorm:"size:64"`
	ErrorSummary        string `gorm:"type:text"`
	Status              string `gorm:"size:32;default:running"`
}

func (WorkflowExecutionAttempt) TableName() string { return "workflow_execution_attempts" }

// WorkflowExecutionNode 节点级执行日志。
type WorkflowExecutionNode struct {
	ID                  int64              `gorm:"primaryKey;autoIncrement"`
	WorkflowExecutionID int64              `gorm:"column:workflow_execution_id;index:ix_exec_node_execution"`
	WorkflowExecution   *WorkflowExecution `gorm:"foreignKey:WorkflowExecutionID;constraint:OnDelete:CASCADE"`
	NodeID              string             `gorm:"column:node_id;size:100"`
	NodeType            string             `gorm:"size:100"`
	Status              string             `gorm:"size:32;default:pending"`
	StartedAt           time.Time
	FinishedAt          *time.Time
	DurationMs          *int64
	InputSnapshotJSON   string `gorm:"column:input_snapshot_json;type:text"`
	OutputSnapshotJSON  string `gorm:"column:output_snapshot_json;type:text"`
	ErrorMessage        string `gorm:"type:text"`
}

func (WorkflowExecutionNode) TableName() string { return "workflow_execution_nodes" }

// WorkflowExecutionTransition 边级流转日志。
type WorkflowExecutionTransition struct {
	ID                  int64              `gorm:"primaryKey;autoIncrement"`
	WorkflowExecutionID int64              `gorm:"column:workflow_execution_id;index:ix_exec_transition_execution"`
	WorkflowExecution   *WorkflowExecution `gorm:"foreignKey:WorkflowExecutionID;constraint:OnDelete:CASCADE"`
	EdgeID              string             `gorm:"column:edge_id;size:100"`
	SourceNodeID        string             `gorm:"column:source_node_id;size:100"`
	TargetNodeID        string             `gorm:"column:target_node_id;size:100"`
	TraversalIndex      int                `gorm:"default:0"`
	IterationIndex      *int
	BranchKey           string `gorm:"size:32"`
	PayloadSnapshotJSON string `gorm:"column:payload_snapshot_json;type:text"`
	CreatedAt           time.Time
}

func (WorkflowExecutionTransition) TableName() string { return "workflow_execution_transitions" }

// TaskDefinitionConfig 任务定义的全局默认参数覆盖。
type TaskDefinitionConfig struct {
	ID                     int64  `gorm:"primaryKey;autoIncrement"`
	TaskDefinitionCode     string `gorm:"size:120;uniqueIndex"`
	ParameterOverridesJSON string `gorm:"column:parameter_overrides_json;type:text"`
	UpdatedBy              *int64
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

func (TaskDefinitionConfig) TableName() string { return "task_definition_configs" }

// DomainEventOutbox 领域事件 outbox。租约字段只允许 dispatcher 更新，producer 创建事件时由数据库基线补齐默认值。
type DomainEventOutbox struct {
	ID                      int64                  `gorm:"primaryKey;autoIncrement"`
	EventType               string                 `gorm:"size:120;index"`
	AggregateType           string                 `gorm:"size:120"`
	AggregateID             string                 `gorm:"column:aggregate_id;size:120"`
	WorkflowExecutionID     *int64                 `gorm:"column:workflow_execution_id"`
	WorkflowExecution       *WorkflowExecution     `gorm:"foreignKey:WorkflowExecutionID;constraint:OnDelete:SET NULL"`
	WorkflowExecutionNodeID *int64                 `gorm:"column:workflow_execution_node_id"`
	WorkflowExecutionNode   *WorkflowExecutionNode `gorm:"foreignKey:WorkflowExecutionNodeID;constraint:OnDelete:SET NULL"`
	PayloadJSON             string                 `gorm:"column:payload_json;type:text"`
	MetadataJSON            string                 `gorm:"column:metadata_json;type:text"`
	Status                  string                 `gorm:"size:20;default:pending;index:ix_event_outbox_pending,priority:1"`
	AttemptCount            int                    `gorm:"default:0"`
	AvailableAt             time.Time              `gorm:"index:ix_event_outbox_pending,priority:2"`
	MaxAttempts             int                    `gorm:"column:max_attempts;<-:update"`
	LeaseID                 *string                `gorm:"column:lease_id;<-:update"`
	WorkerID                *string                `gorm:"column:worker_id;<-:update"`
	LeaseExpiresAt          *time.Time             `gorm:"column:lease_expires_at;<-:update"`
	ClaimedAt               *time.Time             `gorm:"column:claimed_at;<-:update"`
	ProcessedAt             *time.Time
	LastErrorCategory       *string    `gorm:"column:last_error_category;<-:update"`
	LastErrorMessage        string     `gorm:"type:text"`
	DeadLetteredAt          *time.Time `gorm:"column:dead_lettered_at;<-:update"`
	AlertedAt               *time.Time `gorm:"column:alerted_at;<-:update"`
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

func (DomainEventOutbox) TableName() string { return "domain_event_outbox" }

// SystemRole 角色。
type SystemRole struct {
	ID          int64  `gorm:"primaryKey;autoIncrement"`
	DisplayName string `gorm:"size:100"`
	Code        string `gorm:"size:50;uniqueIndex"`
	Description string `gorm:"size:255"`
	IsEnabled   bool   `gorm:"default:true"`
	IsSystem    bool   `gorm:"default:false"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (SystemRole) TableName() string { return "roles" }

// SystemUser 用户。
type SystemUser struct {
	ID           int64  `gorm:"primaryKey;autoIncrement"`
	Username     string `gorm:"size:100;uniqueIndex"`
	PasswordHash string `gorm:"size:255"`
	Nickname     string `gorm:"size:100"`
	FullName     string `gorm:"size:100"`
	Gender       string `gorm:"size:20;default:unknown"`
	Phone        string `gorm:"size:32"`
	Email        string `gorm:"size:150"`
	Avatar       string `gorm:"size:500"`
	IsActive     bool   `gorm:"default:true"`
	JobTitle     string `gorm:"size:100"`
	Location     string `gorm:"size:120"`
	Company      string `gorm:"size:120"`
	Bio          string `gorm:"type:text"`
	TagsJSON     string `gorm:"column:tags_json;type:text"`
	CreatedBy    string `gorm:"size:100;default:system"`
	UpdatedBy    string `gorm:"size:100;default:system"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
	LastLoginAt  *time.Time
}

func (SystemUser) TableName() string { return "users" }

// SystemUserRole 用户-角色关联。
type SystemUserRole struct {
	ID        int64       `gorm:"primaryKey;autoIncrement"`
	UserID    int64       `gorm:"column:user_id;uniqueIndex:ux_user_role,priority:1"`
	User      *SystemUser `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
	RoleID    int64       `gorm:"column:role_id;uniqueIndex:ux_user_role,priority:2"`
	Role      *SystemRole `gorm:"foreignKey:RoleID;constraint:OnDelete:CASCADE"`
	CreatedAt time.Time
}

func (SystemUserRole) TableName() string { return "user_roles" }

// SystemMenu 菜单。
type SystemMenu struct {
	ID             int64       `gorm:"primaryKey;autoIncrement"`
	ParentID       *int64      `gorm:"column:parent_id"`
	Parent         *SystemMenu `gorm:"foreignKey:ParentID;constraint:OnDelete:CASCADE"`
	Path           string      `gorm:"size:255"`
	Name           string      `gorm:"size:100;uniqueIndex"`
	PermissionCode *string     `gorm:"size:120;uniqueIndex"`
	Component      string      `gorm:"size:255"`
	Title          string      `gorm:"size:100"`
	Icon           string      `gorm:"size:100"`
	MenuType       string      `gorm:"size:20;default:menu"`
	ExternalURL    string      `gorm:"column:external_url;size:500"`
	ActiveMenuPath string      `gorm:"size:255"`
	Sort           int         `gorm:"default:0"`
	KeepAlive      bool        `gorm:"default:false"`
	IsHidden       bool        `gorm:"default:false"`
	IsHideTab      bool        `gorm:"default:false"`
	IsFullScreen   bool        `gorm:"default:false"`
	IsActive       bool        `gorm:"default:true"`
	UseIframe      bool        `gorm:"default:false"`
	FixedTab       bool        `gorm:"default:false"`
	BadgeLabel     string      `gorm:"size:50"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (SystemMenu) TableName() string { return "menus" }

// SystemMenuButton 菜单按钮权限。
type SystemMenuButton struct {
	ID             int64       `gorm:"primaryKey;autoIncrement"`
	MenuID         int64       `gorm:"column:menu_id"`
	Menu           *SystemMenu `gorm:"foreignKey:MenuID;constraint:OnDelete:CASCADE"`
	Title          string      `gorm:"size:100"`
	PermissionCode string      `gorm:"size:120;uniqueIndex"`
	Sort           int         `gorm:"default:0"`
	CreatedAt      time.Time
}

func (SystemMenuButton) TableName() string { return "menu_buttons" }

// SystemRoleMenu 角色-菜单绑定。
type SystemRoleMenu struct {
	ID        int64       `gorm:"primaryKey;autoIncrement"`
	RoleID    int64       `gorm:"column:role_id;uniqueIndex:ux_role_menu,priority:1"`
	Role      *SystemRole `gorm:"foreignKey:RoleID;constraint:OnDelete:CASCADE"`
	MenuID    int64       `gorm:"column:menu_id;uniqueIndex:ux_role_menu,priority:2"`
	Menu      *SystemMenu `gorm:"foreignKey:MenuID;constraint:OnDelete:CASCADE"`
	CreatedAt time.Time
}

func (SystemRoleMenu) TableName() string { return "role_menus" }

// SystemRoleButton 角色-按钮绑定。
type SystemRoleButton struct {
	ID        int64             `gorm:"primaryKey;autoIncrement"`
	RoleID    int64             `gorm:"column:role_id;uniqueIndex:ux_role_button,priority:1"`
	Role      *SystemRole       `gorm:"foreignKey:RoleID;constraint:OnDelete:CASCADE"`
	ButtonID  int64             `gorm:"column:button_id;uniqueIndex:ux_role_button,priority:2"`
	Button    *SystemMenuButton `gorm:"foreignKey:ButtonID;constraint:OnDelete:CASCADE"`
	CreatedAt time.Time
}

func (SystemRoleButton) TableName() string { return "role_menu_buttons" }

// SystemI18nText 菜单/按钮的国际化文案。
type SystemI18nText struct {
	ID        int64  `gorm:"primaryKey;autoIncrement"`
	BizType   string `gorm:"size:20;uniqueIndex:ux_i18n_biz,priority:1"`
	BizID     int64  `gorm:"column:biz_id;uniqueIndex:ux_i18n_biz,priority:2"`
	I18nKey   string `gorm:"column:i18n_key;size:255;uniqueIndex:ux_i18n_key_locale,priority:1"`
	Locale    string `gorm:"size:10;uniqueIndex:ux_i18n_biz,priority:3;uniqueIndex:ux_i18n_key_locale,priority:2"`
	Text      string `gorm:"size:255"`
	UpdatedAt time.Time
}

func (SystemI18nText) TableName() string { return "i18n_texts" }

// SystemAiModelConfig 用户拥有的 AI 模型配置。
type SystemAiModelConfig struct {
	ID                    int64       `gorm:"primaryKey;autoIncrement"`
	OwnerID               int64       `gorm:"column:owner_id;index"`
	Owner                 *SystemUser `gorm:"foreignKey:OwnerID;constraint:OnDelete:CASCADE"`
	Provider              string      `gorm:"size:50"`
	ProviderName          string      `gorm:"size:100"`
	DisplayName           string      `gorm:"size:100"`
	ModelIdentifier       string      `gorm:"size:150"`
	BaseURL               string      `gorm:"column:base_url;size:500"`
	EncryptedAPIKey       string      `gorm:"column:encrypted_api_key;type:text"`
	IsEnabled             bool        `gorm:"default:true"`
	Priority              int         `gorm:"default:100"`
	RequestHeadersJSON    string      `gorm:"column:request_headers_json;type:text"`
	RequestBodyJSON       string      `gorm:"column:request_body_json;type:text"`
	TimeoutMs             int         `gorm:"default:60000"`
	Remark                string      `gorm:"type:text"`
	LastValidationStatus  string      `gorm:"size:20;default:unknown"`
	LastValidationMessage string      `gorm:"type:text"`
	LastValidatedAt       *time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

func (SystemAiModelConfig) TableName() string { return "ai_model_configs" }

// AssistantAgent 全局智能体模板。
type AssistantAgent struct {
	ID                 int64  `gorm:"primaryKey;autoIncrement"`
	Code               string `gorm:"size:64;uniqueIndex"`
	DisplayName        string `gorm:"size:100"`
	Avatar             string `gorm:"size:500"`
	Description        string `gorm:"size:500"`
	SystemPrompt       string `gorm:"type:text"`
	WelcomeMessage     string `gorm:"type:text"`
	StarterPromptsJSON string `gorm:"column:starter_prompts_json;type:text"`
	DataSourceType     string `gorm:"size:32;default:none"`
	IsEnabled          bool   `gorm:"default:true"`
	Sort               int    `gorm:"default:0"`
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

func (AssistantAgent) TableName() string { return "assistant_agents" }

// AiModelAgentBinding 模型-智能体绑定。
type AiModelAgentBinding struct {
	ID            int64                `gorm:"primaryKey;autoIncrement"`
	ModelConfigID int64                `gorm:"column:model_config_id;uniqueIndex:ux_model_agent,priority:1"`
	ModelConfig   *SystemAiModelConfig `gorm:"foreignKey:ModelConfigID;constraint:OnDelete:CASCADE"`
	AgentID       int64                `gorm:"column:agent_id;uniqueIndex:ux_model_agent,priority:2"`
	Agent         *AssistantAgent      `gorm:"foreignKey:AgentID;constraint:OnDelete:CASCADE"`
	CreatedAt     time.Time
}

func (AiModelAgentBinding) TableName() string { return "ai_model_agent_bindings" }

// SystemNotifyChannel 通知渠道配置。
type SystemNotifyChannel struct {
	ID                   int64       `gorm:"primaryKey;autoIncrement"`
	ChannelType          string      `gorm:"size:50"`
	OwnerID              *int64      `gorm:"column:owner_id"`
	Owner                *SystemUser `gorm:"foreignKey:OwnerID;constraint:OnDelete:SET NULL"`
	DisplayName          string      `gorm:"size:100"`
	IsEnabled            bool        `gorm:"default:true"`
	IsBuiltin            bool        `gorm:"default:false"`
	IsSystem             bool        `gorm:"default:false"`
	SettingsJSON         string      `gorm:"column:settings_json;type:text"`
	EncryptedSecretsJSON string      `gorm:"column:encrypted_secrets_json;type:text"`
	Remark               string      `gorm:"type:text"`
	LastTestStatus       string      `gorm:"size:20;default:unknown"`
	LastTestMessage      string      `gorm:"type:text"`
	LastTestedAt         *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

func (SystemNotifyChannel) TableName() string { return "notification_channels" }

// SystemNotifyDelivery 通知投递记录。
type SystemNotifyDelivery struct {
	ID                      int64                  `gorm:"primaryKey;autoIncrement"`
	WorkflowExecutionID     *int64                 `gorm:"column:workflow_execution_id"`
	WorkflowExecution       *WorkflowExecution     `gorm:"foreignKey:WorkflowExecutionID;constraint:OnDelete:SET NULL"`
	WorkflowExecutionNodeID *int64                 `gorm:"column:workflow_execution_node_id"`
	WorkflowExecutionNode   *WorkflowExecutionNode `gorm:"foreignKey:WorkflowExecutionNodeID;constraint:OnDelete:SET NULL"`
	OutboxEventID           *int64                 `gorm:"column:outbox_event_id"`
	OutboxEvent             *DomainEventOutbox     `gorm:"foreignKey:OutboxEventID;constraint:OnDelete:SET NULL"`
	StrategySignalID        *uuid.UUID             `gorm:"column:strategy_signal_id;type:uuid"`
	StrategySignal          *StrategySignal        `gorm:"foreignKey:StrategySignalID;constraint:OnDelete:RESTRICT"`
	TargetType              string                 `gorm:"size:20"`
	TargetID                *int64                 `gorm:"column:target_id"`
	RecipientUserID         *int64                 `gorm:"column:recipient_user_id;index"`
	RecipientUser           *SystemUser            `gorm:"foreignKey:RecipientUserID;constraint:OnDelete:SET NULL"`
	ChannelID               *int64                 `gorm:"column:channel_id"`
	Channel                 *SystemNotifyChannel   `gorm:"foreignKey:ChannelID;constraint:OnDelete:SET NULL"`
	ChannelType             string                 `gorm:"size:50"`
	Status                  string                 `gorm:"size:20;default:pending"`
	Title                   string                 `gorm:"type:text"`
	Content                 string                 `gorm:"type:text"`
	ProviderResponseText    string                 `gorm:"type:text"`
	ErrorMessage            string                 `gorm:"type:text"`
	IsRead                  bool                   `gorm:"default:false"`
	ReadAt                  *time.Time
	SentAt                  *time.Time
	CreatedAt               time.Time
}

func (SystemNotifyDelivery) TableName() string { return "notification_deliveries" }

// AssistantSession 助手会话。
type AssistantSession struct {
	ID                       int64                `gorm:"primaryKey;autoIncrement"`
	UserID                   int64                `gorm:"column:user_id;index"`
	User                     *SystemUser          `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
	AgentID                  int64                `gorm:"column:agent_id;index"`
	Agent                    *AssistantAgent      `gorm:"foreignKey:AgentID;constraint:OnDelete:CASCADE"`
	NewsID                   *int64               `gorm:"column:news_id"`
	News                     *BlockbeatsNews      `gorm:"foreignKey:NewsID;constraint:OnDelete:SET NULL"`
	ModelConfigID            *int64               `gorm:"column:model_config_id"`
	ModelConfig              *SystemAiModelConfig `gorm:"foreignKey:ModelConfigID;constraint:OnDelete:SET NULL"`
	ModelDisplayNameSnapshot string               `gorm:"size:100"`
	ProviderLabelSnapshot    string               `gorm:"size:100"`
	Title                    string               `gorm:"size:255"`
	CreatedAt                time.Time
	UpdatedAt                time.Time
	LastMessageAt            time.Time
}

func (AssistantSession) TableName() string { return "assistant_sessions" }

// AssistantMessage 单条助手消息。
type AssistantMessage struct {
	ID          int64             `gorm:"primaryKey;autoIncrement"`
	SessionID   int64             `gorm:"column:session_id;index:ix_assistant_msg_session"`
	Session     *AssistantSession `gorm:"foreignKey:SessionID;constraint:OnDelete:CASCADE"`
	Role        string            `gorm:"size:20"`
	ContentType string            `gorm:"size:40;default:text"`
	Content     string            `gorm:"type:text"`
	Reasoning   string            `gorm:"type:text"`
	// token 消耗:模型在流末尾给出的 usage,用来算成本、看哪个智能体最贵。
	// 模型没返回 usage 时保持 0。
	PromptTokens     int64     `gorm:"column:prompt_tokens;default:0"`
	CompletionTokens int64     `gorm:"column:completion_tokens;default:0"`
	TotalTokens      int64     `gorm:"column:total_tokens;default:0"`
	CreatedAt        time.Time `gorm:"index:ix_assistant_msg_session,priority:2"`
}

func (AssistantMessage) TableName() string { return "assistant_messages" }

// AuditRecord 保存写请求的最小审计元数据，不接收请求正文、Header、查询串或错误正文。
type AuditRecord struct {
	ID           int64  `gorm:"primaryKey;autoIncrement"`
	RequestID    string `gorm:"column:request_id;size:64"`
	ActorUserID  *int64 `gorm:"column:actor_user_id"`
	Action       string `gorm:"size:255"`
	ResourcePath string `gorm:"column:resource_path;size:500"`
	Outcome      string `gorm:"size:16"`
	StatusCode   int    `gorm:"column:status_code"`
	CreatedAt    time.Time
}

func (AuditRecord) TableName() string { return "audit_records" }
