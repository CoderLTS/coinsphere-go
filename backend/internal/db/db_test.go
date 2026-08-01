package db

import (
	"path/filepath"
	"testing"

	"coinsphere/backend/internal/config"
)

func TestOpenPreservesAutoMigrateBehavior(t *testing.T) {
	gdb, err := Open(config.DatabaseConfig{
		Driver: "sqlite",
		Path:   filepath.Join(t.TempDir(), "application.db"),
	})
	if err != nil {
		t.Fatalf("open application database: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("get sql database: %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close application database: %v", err)
		}
	})

	if !gdb.Migrator().HasTable(&SystemUser{}) {
		t.Fatal("db.Open no longer applies the existing GORM AutoMigrate schema")
	}
}
