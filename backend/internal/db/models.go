// Package db owns the PostgreSQL models used by the V2 application baseline.
package db

import "time"

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

type SystemUserRole struct {
	ID        int64       `gorm:"primaryKey;autoIncrement"`
	UserID    int64       `gorm:"column:user_id;uniqueIndex:ux_user_role,priority:1"`
	User      *SystemUser `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
	RoleID    int64       `gorm:"column:role_id;uniqueIndex:ux_user_role,priority:2"`
	Role      *SystemRole `gorm:"foreignKey:RoleID;constraint:OnDelete:CASCADE"`
	CreatedAt time.Time
}

func (SystemUserRole) TableName() string { return "user_roles" }

type SystemMenu struct {
	ID             int64       `gorm:"primaryKey;autoIncrement"`
	ParentID       *int64      `gorm:"column:parent_id"`
	Parent         *SystemMenu `gorm:"foreignKey:ParentID;constraint:OnDelete:CASCADE"`
	Path           string      `gorm:"size:255"`
	Name           string      `gorm:"size:100;uniqueIndex"`
	PermissionCode *string     `gorm:"size:120;index"`
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

type SystemRoleMenu struct {
	ID        int64       `gorm:"primaryKey;autoIncrement"`
	RoleID    int64       `gorm:"column:role_id;uniqueIndex:ux_role_menu,priority:1"`
	Role      *SystemRole `gorm:"foreignKey:RoleID;constraint:OnDelete:CASCADE"`
	MenuID    int64       `gorm:"column:menu_id;uniqueIndex:ux_role_menu,priority:2"`
	Menu      *SystemMenu `gorm:"foreignKey:MenuID;constraint:OnDelete:CASCADE"`
	CreatedAt time.Time
}

func (SystemRoleMenu) TableName() string { return "role_menus" }

type SystemRoleButton struct {
	ID        int64             `gorm:"primaryKey;autoIncrement"`
	RoleID    int64             `gorm:"column:role_id;uniqueIndex:ux_role_button,priority:1"`
	Role      *SystemRole       `gorm:"foreignKey:RoleID;constraint:OnDelete:CASCADE"`
	ButtonID  int64             `gorm:"column:button_id;uniqueIndex:ux_role_button,priority:2"`
	Button    *SystemMenuButton `gorm:"foreignKey:ButtonID;constraint:OnDelete:CASCADE"`
	CreatedAt time.Time
}

func (SystemRoleButton) TableName() string { return "role_menu_buttons" }

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

// AuditRecord never stores request bodies, headers, query strings, or error details.
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

type SystemLog struct {
	ID          int64 `gorm:"primaryKey;autoIncrement"`
	LoggedAt    time.Time
	Level       string `gorm:"size:8"`
	Component   string `gorm:"size:64"`
	Message     string `gorm:"type:text"`
	RequestID   string `gorm:"column:request_id;size:64"`
	UserID      *int64 `gorm:"column:user_id"`
	Method      string `gorm:"size:8"`
	Route       string `gorm:"size:255"`
	StatusCode  *int   `gorm:"column:status_code"`
	DurationMS  *int64 `gorm:"column:duration_ms"`
	DetailsJSON string `gorm:"column:details_json;type:jsonb"`
}

func (SystemLog) TableName() string { return "system_logs" }

type SystemLogSettings struct {
	ID            int16  `gorm:"primaryKey"`
	Level         string `gorm:"size:8"`
	RetentionDays int
	UpdatedBy     *int64
	UpdatedAt     time.Time
}

func (SystemLogSettings) TableName() string { return "system_log_settings" }

type Workflow struct {
	ID                int64  `gorm:"primaryKey;autoIncrement"`
	Name              string `gorm:"size:120"`
	Description       string `gorm:"size:500"`
	Mode              string `gorm:"size:16"`
	Status            string `gorm:"size:16"`
	ActiveRevisionID  *int64 `gorm:"column:active_revision_id"`
	MainTriggerNodeID string `gorm:"column:main_trigger_node_id;size:128"`
	RetentionDays     int    `gorm:"column:retention_days"`
	CreatedBy         int64  `gorm:"column:created_by"`
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

func (Workflow) TableName() string { return "workflows" }

type WorkflowRevision struct {
	ID                int64  `gorm:"primaryKey;autoIncrement"`
	WorkflowID        int64  `gorm:"column:workflow_id"`
	RevisionNumber    int64  `gorm:"column:revision_number"`
	GraphJSON         string `gorm:"column:graph_json;type:jsonb"`
	NodeVersions      string `gorm:"column:node_versions;type:jsonb"`
	MainTriggerNodeID string `gorm:"column:main_trigger_node_id;size:128"`
	CreatedBy         int64  `gorm:"column:created_by"`
	CreatedAt         time.Time
}

func (WorkflowRevision) TableName() string { return "workflow_revisions" }

type WorkflowSecretBinding struct {
	RevisionID     int64  `gorm:"column:revision_id;primaryKey"`
	WorkflowID     int64  `gorm:"column:workflow_id"`
	NodeInstanceID string `gorm:"column:node_instance_id;size:128;primaryKey"`
	FieldName      string `gorm:"column:field_name;size:128;primaryKey"`
	EncryptedValue string `gorm:"column:encrypted_value;type:text"`
	CreatedAt      time.Time
}

func (WorkflowSecretBinding) TableName() string { return "workflow_secret_bindings" }

type WorkflowRuntime struct {
	WorkflowID        int64 `gorm:"column:workflow_id;primaryKey"`
	MaxConcurrentRuns int   `gorm:"column:max_concurrent_runs"`
	BacklogLimit      int   `gorm:"column:backlog_limit"`
	NextScheduledAt   *time.Time
	LastScheduledAt   *time.Time
	UpdatedAt         time.Time
}

func (WorkflowRuntime) TableName() string { return "workflow_runtimes" }

type WorkflowRun struct {
	ID                    int64  `gorm:"primaryKey;autoIncrement"`
	WorkflowID            int64  `gorm:"column:workflow_id"`
	RevisionID            int64  `gorm:"column:revision_id"`
	TriggerType           string `gorm:"column:trigger_type;size:16"`
	TriggerKey            string `gorm:"column:trigger_key;size:128"`
	EventRecordID         *int64 `gorm:"column:event_record_id"`
	PartitionKey          string `gorm:"column:partition_key;size:256"`
	Diagnostic            bool
	OriginalRunID         *int64 `gorm:"column:original_run_id"`
	Status                string `gorm:"size:16"`
	CurrentNodeInstanceID string `gorm:"column:current_node_instance_id;size:128"`
	NotBefore             time.Time
	LeaseToken            *string `gorm:"column:lease_token;size:64"`
	LeaseExpiresAt        *time.Time
	CancelRequestedAt     *time.Time
	TriggeredAt           time.Time
	StartedAt             *time.Time
	CompletedAt           *time.Time
	CreatedBy             *int64 `gorm:"column:created_by"`
	ResultSummary         string `gorm:"column:result_summary;type:jsonb"`
	ErrorCategory         *string
	ErrorMessage          *string
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

func (WorkflowRun) TableName() string { return "workflow_runs" }

type WorkflowEventRecord struct {
	ID              int64 `gorm:"primaryKey;autoIncrement"`
	Source          string
	EventID         string `gorm:"column:event_id"`
	SpecVersion     string
	EventType       string
	Subject         string
	EventTime       time.Time
	DataContentType string
	PartitionKey    string
	EventJSON       string `gorm:"type:jsonb"`
	ReceivedAt      time.Time
}

func (WorkflowEventRecord) TableName() string { return "workflow_event_records" }

type WorkflowEventDelivery struct {
	ID            int64 `gorm:"primaryKey;autoIncrement"`
	EventRecordID int64
	WorkflowID    int64
	RevisionID    int64
	RunID         int64
	CreatedAt     time.Time
}

func (WorkflowEventDelivery) TableName() string { return "workflow_event_deliveries" }

type WorkflowEventOutbox struct {
	ID                int64 `gorm:"primaryKey;autoIncrement"`
	Source            string
	EventID           string
	EventJSON         string `gorm:"type:jsonb"`
	Status            string
	AttemptCount      int
	MaxAttempts       int
	AvailableAt       time.Time
	PublishedAt       *time.Time
	LastErrorCategory *string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

func (WorkflowEventOutbox) TableName() string { return "workflow_event_outbox" }

type WorkflowRunNode struct {
	ID             int64 `gorm:"primaryKey;autoIncrement"`
	RunID          int64 `gorm:"column:run_id"`
	NodeInstanceID string
	NodeType       string
	NodeVersion    string
	ExecutionPool  string
	Attempt        int
	LoopIteration  int
	OperationKey   string
	Status         string
	InputSummary   string `gorm:"column:input_summary;type:jsonb"`
	OutputSummary  string `gorm:"column:output_summary;type:jsonb"`
	ErrorCategory  *string
	ErrorMessage   *string
	StartedAt      time.Time
	CompletedAt    *time.Time
	DurationMS     *int64 `gorm:"column:duration_ms"`
}

func (WorkflowRunNode) TableName() string { return "workflow_run_nodes" }

type WorkflowRunCheckpoint struct {
	ID             int64 `gorm:"primaryKey;autoIncrement"`
	RunID          int64 `gorm:"column:run_id"`
	RunNodeID      int64 `gorm:"column:run_node_id"`
	WorkflowID     int64 `gorm:"column:workflow_id"`
	RevisionID     int64 `gorm:"column:revision_id"`
	NodeInstanceID string
	LoopIteration  int
	OperationKey   string
	Status         string
	OutputJSON     string `gorm:"column:output_json;type:jsonb"`
	ArtifactsJSON  string `gorm:"column:artifacts_json;type:jsonb"`
	CreatedAt      time.Time
}

func (WorkflowRunCheckpoint) TableName() string { return "workflow_run_checkpoints" }

type WorkflowNodeState struct {
	WorkflowID     int64  `gorm:"column:workflow_id;primaryKey"`
	NodeInstanceID string `gorm:"column:node_instance_id;primaryKey"`
	NodeType       string
	RevisionID     int64
	StateJSON      string `gorm:"column:state_json;type:jsonb"`
	UpdatedAt      time.Time
}

func (WorkflowNodeState) TableName() string { return "workflow_node_states" }

type WorkflowNodeLog struct {
	ID         int64 `gorm:"primaryKey;autoIncrement"`
	WorkflowID int64
	RunID      int64
	RunNodeID  int64
	LoggedAt   time.Time
	Level      string
	Message    string
	FieldsJSON string `gorm:"column:fields_json;type:jsonb"`
}

func (WorkflowNodeLog) TableName() string { return "workflow_node_logs" }

type WorkflowArtifact struct {
	SHA256          string `gorm:"primaryKey;size:64"`
	MediaType       string
	Encoding        string
	SizeBytes       int64
	StoredSizeBytes int64
	StorageKey      string
	CreatedAt       time.Time
}

func (WorkflowArtifact) TableName() string { return "workflow_artifacts" }

type WorkflowArtifactRef struct {
	RunNodeID      int64 `gorm:"column:run_node_id;primaryKey"`
	ArtifactSHA256 string
	Ordinal        int `gorm:"primaryKey"`
	MediaType      string
	SizeBytes      int64
}

func (WorkflowArtifactRef) TableName() string { return "workflow_artifact_refs" }

type WorkflowHumanTask struct {
	ID             int64 `gorm:"primaryKey;autoIncrement"`
	WorkflowID     int64
	RunID          int64
	NodeInstanceID string
	TaskType       string
	BusinessKey    string
	Prompt         string
	Status         string
	ExpiresAt      time.Time
	DecisionJSON   string `gorm:"type:jsonb"`
	DecidedBy      *int64
	CreatedAt      time.Time
	DecidedAt      *time.Time
	UpdatedAt      time.Time
}

func (WorkflowHumanTask) TableName() string { return "workflow_human_tasks" }

type ResultView struct {
	ID             int64 `gorm:"primaryKey;autoIncrement"`
	Name           string
	PluginID       string
	PageKey        string
	ScopeJSON      string `gorm:"type:jsonb"`
	FiltersJSON    string `gorm:"type:jsonb"`
	AllowedActions string `gorm:"type:jsonb"`
	Status         string
	CreatedBy      int64
	CreatedAt      time.Time
	RevokedAt      *time.Time
}

func (ResultView) TableName() string { return "result_views" }

type ResultViewUserGrant struct {
	ViewID    int64 `gorm:"primaryKey"`
	UserID    int64 `gorm:"primaryKey"`
	CreatedAt time.Time
}

func (ResultViewUserGrant) TableName() string { return "result_view_user_grants" }

type ResultViewRoleGrant struct {
	ViewID    int64 `gorm:"primaryKey"`
	RoleID    int64 `gorm:"primaryKey"`
	CreatedAt time.Time
}

func (ResultViewRoleGrant) TableName() string { return "result_view_role_grants" }

type NotificationDelivery struct {
	ID                int64 `gorm:"primaryKey;autoIncrement"`
	OperationKey      string
	WorkflowID        int64
	RevisionID        int64
	NodeInstanceID    string
	Channel           string
	RecipientUserID   *int64
	SubjectKey        string
	Title             string
	Message           string
	Status            string
	AttemptCount      int
	DeliveredAt       *time.Time
	IsRead            bool
	ReadAt            *time.Time
	LastErrorCategory *string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

func (NotificationDelivery) TableName() string { return "plugin_notification.deliveries" }
