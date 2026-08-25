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

type Workflow struct {
	ID                int64  `gorm:"primaryKey;autoIncrement"`
	Name              string `gorm:"size:120"`
	Description       string `gorm:"size:500"`
	Mode              string `gorm:"size:16"`
	Status            string `gorm:"size:32"`
	ActiveRevisionID  *int64 `gorm:"column:active_revision_id"`
	MainTriggerNodeID string `gorm:"column:main_trigger_node_id;size:128"`
	RetentionDays     int    `gorm:"column:retention_days"`
	CreatedBy         int64  `gorm:"column:created_by"`
	CreatedAt         time.Time
	UpdatedAt         time.Time
	ArchivedAt        *time.Time
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

type WorkflowRuntime struct {
	WorkflowID     int64  `gorm:"column:workflow_id;primaryKey"`
	ActivityCursor int64  `gorm:"column:activity_cursor"`
	HealthSummary  string `gorm:"column:health_summary;size:32"`
	UpdatedAt      time.Time
}

func (WorkflowRuntime) TableName() string { return "workflow_runtimes" }
