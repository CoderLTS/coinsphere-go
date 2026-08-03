package db

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"
	"time"

	"coinsphere/backend/internal/config"
	"gorm.io/gorm/logger"
)

const postgresMigrationDSNEnv = "COINSPHERE_TEST_POSTGRES_DSN"

// TestRedactingGORMLogger 固定两层日志边界：SQL 参数保持占位，驱动错误和直接 Error 调用只输出统一分类。
func TestRedactingGORMLogger(t *testing.T) {
	var output bytes.Buffer
	wrapped := redactingGORMLogger{Interface: logger.New(
		log.New(&output, "", 0),
		logger.Config{LogLevel: logger.Warn, ParameterizedQueries: true},
	)}

	query, params := wrapped.ParamsFilter(context.Background(), "INSERT INTO events(payload) VALUES (?)", "payload-secret-marker")
	if query != "INSERT INTO events(payload) VALUES (?)" || len(params) != 0 {
		t.Fatalf("database logger did not retain placeholders: query=%q params=%v", query, params)
	}
	wrapped.Trace(context.Background(), time.Now(), func() (string, int64) { return query, 0 }, errors.New("driver-secret-marker"))
	wrapped.Error(context.Background(), "direct-secret-marker: %s", "secret")

	logs := output.String()
	for _, marker := range []string{"payload-secret-marker", "driver-secret-marker", "direct-secret-marker"} {
		if strings.Contains(logs, marker) {
			t.Fatalf("database logger leaked marker %q: %s", marker, logs)
		}
	}
	if strings.Count(logs, errDatabaseOperation.Error()) != 2 || !strings.Contains(logs, "VALUES (?)") {
		t.Fatalf("database logger lost fixed classification or parameterized SQL: %s", logs)
	}
}

func TestConnectRequiresDSN(t *testing.T) {
	if _, err := Connect(context.Background(), config.DatabaseConfig{}); err == nil {
		t.Fatal("Connect accepted an empty PostgreSQL DSN")
	}
}

func TestConnectDoesNotExposeDSN(t *testing.T) {
	const secretMarker = "database-password-must-not-leak"
	_, err := Connect(context.Background(), config.DatabaseConfig{
		DSN: "postgresql://coinsphere:" + secretMarker + "@%zz",
	})
	if err == nil {
		t.Fatal("Connect accepted a malformed PostgreSQL DSN")
	}
	if strings.Contains(err.Error(), secretMarker) {
		t.Fatalf("Connect error exposed the PostgreSQL DSN: %v", err)
	}
}
