package db

import "time"

type Permission struct {
	Code      string `gorm:"primaryKey;size:160"`
	Title     string
	PluginID  string
	Protected bool
}

func (Permission) TableName() string { return "permissions" }

type RolePermission struct {
	RoleID         int64  `gorm:"primaryKey"`
	PermissionCode string `gorm:"primaryKey"`
}

func (RolePermission) TableName() string { return "role_permissions" }

type RevokedSession struct {
	TokenID   string `gorm:"primaryKey;size:128"`
	ExpiresAt time.Time
}

func (RevokedSession) TableName() string { return "revoked_sessions" }
