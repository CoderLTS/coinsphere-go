// Package db GORM 模型定义。表名与列名与原 Python(Peewee)后端保持一致。
package db

import "time"

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

// DomainEventOutbox 领域事件 outbox。
// A1-3 新增字段由版本化 00003 migration 独占 DDL 所有权：<-:update 允许后续 dispatcher 更新，
// -:migration 阻止当前仍保留的 AutoMigrate 抢先建列或改写双方言约束。禁止 Create 写入这些字段，
// 使尚未执行 00003 的旧库仍可由现有 producer 插入事件；数据库在迁移后负责补齐 max_attempts 默认值。
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
	MaxAttempts             int                    `gorm:"column:max_attempts;<-:update;-:migration"`
	LeaseID                 *string                `gorm:"column:lease_id;<-:update;-:migration"`
	WorkerID                *string                `gorm:"column:worker_id;<-:update;-:migration"`
	LeaseExpiresAt          *time.Time             `gorm:"column:lease_expires_at;<-:update;-:migration"`
	ClaimedAt               *time.Time             `gorm:"column:claimed_at;<-:update;-:migration"`
	ProcessedAt             *time.Time
	LastErrorCategory       *string    `gorm:"column:last_error_category;<-:update;-:migration"`
	LastErrorMessage        string     `gorm:"type:text"`
	DeadLetteredAt          *time.Time `gorm:"column:dead_lettered_at;<-:update;-:migration"`
	AlertedAt               *time.Time `gorm:"column:alerted_at;<-:update;-:migration"`
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

// RefreshTokenRecord refresh token 哈希存储。
type RefreshTokenRecord struct {
	ID        string      `gorm:"primaryKey;size:64"`
	UserID    int64       `gorm:"column:user_id"`
	User      *SystemUser `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
	TokenHash string      `gorm:"size:128;uniqueIndex"`
	ExpiresAt time.Time
	IsRevoked bool `gorm:"default:false"`
	CreatedAt time.Time
}

func (RefreshTokenRecord) TableName() string { return "refresh_tokens" }

// AllModels 按依赖顺序返回全部模型,用于 AutoMigrate。
// 返回 []any(any = 任意类型的切片)。顺序有讲究:被外键引用的表要先建,否则加外键约束时会失败。
func AllModels() []any {
	return []any{
		&BlockbeatsNews{},
		&WorkflowDefinition{},
		&WorkflowRuntimeState{},
		&WorkflowRuntimeEntry{},
		&WorkflowExecution{},
		&WorkflowExecutionAttempt{},
		&WorkflowExecutionNode{},
		&WorkflowExecutionTransition{},
		&TaskDefinitionConfig{},
		&DomainEventOutbox{},
		&SystemRole{},
		&SystemUser{},
		&SystemUserRole{},
		&SystemMenu{},
		&SystemMenuButton{},
		&SystemRoleMenu{},
		&SystemRoleButton{},
		&SystemI18nText{},
		&SystemAiModelConfig{},
		&AssistantAgent{},
		&AiModelAgentBinding{},
		&SystemNotifyChannel{},
		&SystemNotifyDelivery{},
		&AssistantSession{},
		&AssistantMessage{},
		&RefreshTokenRecord{},
	}
}

// versionedDomainEventOutbox 是 00003 应用后的 AutoMigrate 占位模型。
// 两个外键列使用 -:migration，保证 GORM 不改列；关系本身保留，使 PostgreSQL 空 migration 库在父表
// 由现有 AutoMigrate 建立后补齐 Outbox 外键。SQLite migration 已声明同名约束，因此不会触发表重建。
type versionedDomainEventOutbox struct {
	WorkflowExecutionID     *int64                 `gorm:"column:workflow_execution_id;-:migration"`
	WorkflowExecution       *WorkflowExecution     `gorm:"foreignKey:WorkflowExecutionID;constraint:OnDelete:SET NULL"`
	WorkflowExecutionNodeID *int64                 `gorm:"column:workflow_execution_node_id;-:migration"`
	WorkflowExecutionNode   *WorkflowExecutionNode `gorm:"foreignKey:WorkflowExecutionNodeID;constraint:OnDelete:SET NULL"`
}

func (versionedDomainEventOutbox) TableName() string { return "domain_event_outbox" }

// autoMigrateModels 在 00003 尚未应用时保留既有 Outbox AutoMigrate；一旦检测到版本化字段，
// 用同表名空模型替换真实模型，避免 GORM 沿 notification_deliveries 关联把真实 Outbox 重新加入 DDL。
// 这种单表隔离保留其他模型的关系迁移；A1-10 才整体移除生产 AutoMigrate。
func autoMigrateModels(skipVersionedOutbox bool) []any {
	models := AllModels()
	if !skipVersionedOutbox {
		return models
	}

	for index, model := range models {
		if _, ok := model.(*DomainEventOutbox); ok {
			models[index] = &versionedDomainEventOutbox{}
			break
		}
	}
	return models
}
