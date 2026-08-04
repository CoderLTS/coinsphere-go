package db

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"coinsphere/backend/internal/config"
)

var errDatabaseOperation = errors.New("database operation failed")

// redactingGORMLogger 保留参数占位后的 SQL、耗时和行数，但不把驱动错误正文写入日志。
// 数据库错误可能回显约束值或业务内容；调用方应另行记录固定操作名、资源 ID 和错误分类。
type redactingGORMLogger struct {
	logger.Interface
}

func (l redactingGORMLogger) LogMode(level logger.LogLevel) logger.Interface {
	return redactingGORMLogger{Interface: l.Interface.LogMode(level)}
}

func (l redactingGORMLogger) Error(ctx context.Context, _ string, _ ...interface{}) {
	l.Interface.Error(ctx, errDatabaseOperation.Error())
}

func (l redactingGORMLogger) Trace(
	ctx context.Context,
	begin time.Time,
	fc func() (string, int64),
	err error,
) {
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		err = errDatabaseOperation
	}
	l.Interface.Trace(ctx, begin, fc, err)
}

// ParamsFilter 显式透传底层参数过滤器；未知实现则 fail-closed 丢弃参数，避免包装 logger 后重新展开 payload。
func (l redactingGORMLogger) ParamsFilter(ctx context.Context, query string, params ...interface{}) (string, []interface{}) {
	if filter, ok := l.Interface.(gorm.ParamsFilter); ok {
		return filter.ParamsFilter(ctx, query, params...)
	}
	return query, nil
}

// Connect 只建立 PostgreSQL 连接并配置连接池，不修改业务 schema。
// 服务和 migration 共用此入口，所有 DDL 只能来自独立的版本化 SQL 命令。
func Connect(ctx context.Context, cfg config.DatabaseConfig) (*gorm.DB, error) {
	if strings.TrimSpace(cfg.DSN) == "" {
		return nil, errors.New("database DSN is required")
	}

	gdb, err := gorm.Open(postgres.Open(cfg.DSN), &gorm.Config{
		Logger: redactingGORMLogger{Interface: logger.New(
			slog.NewLogLogger(slog.Default().Handler(), slog.LevelWarn),
			logger.Config{
				SlowThreshold:             500 * time.Millisecond,
				LogLevel:                  logger.Warn,
				IgnoreRecordNotFoundError: true, // First() 未命中是正常业务分支,不刷日志。
				ParameterizedQueries:      true, // SQL 错误和慢查询日志保留占位符，禁止展开 Outbox payload 等参数。
			},
		)},
	})
	if err != nil {
		return nil, errors.New("postgres database initialization failed")
	}

	// 从 GORM 取出底层标准库的 *sql.DB,用来设置连接池参数。
	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, errors.New("postgres connection pool initialization failed")
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("ping postgres database: %w", err)
		}
		return nil, errors.New("postgres database is unavailable")
	}
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetConnMaxIdleTime(time.Duration(cfg.ConnMaxIdleTimeSeconds) * time.Second)

	return gdb, nil
}
