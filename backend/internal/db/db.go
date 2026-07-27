package db

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"coinsphere/backend/internal/config"
)

// Open 按配置的 driver 打开数据库连接并完成建表。
func Open(cfg config.DatabaseConfig) (*gorm.DB, error) {
	var dialector gorm.Dialector
	switch cfg.Driver {
	case "sqlite":
		if cfg.Path == "" {
			cfg.Path = "data/coinsphere.db"
		}
		if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o755); err != nil {
			return nil, fmt.Errorf("create sqlite dir: %w", err)
		}
		// 开启外键约束保证级联删除,busy_timeout 缓解并发写锁冲突。
		dsn := cfg.Path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
		if cfg.Params != "" {
			dsn += "&" + cfg.Params
		}
		dialector = sqlite.Open(dsn)
	case "mysql":
		port := cfg.Port
		if port == 0 {
			port = 3306
		}
		dsn := fmt.Sprintf(
			"%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=true&loc=Local",
			cfg.User, cfg.Password, cfg.Host, port, cfg.Database,
		)
		if cfg.Params != "" {
			dsn += "&" + cfg.Params
		}
		dialector = mysql.Open(dsn)
	case "postgres":
		port := cfg.Port
		if port == 0 {
			port = 5432
		}
		dsn := fmt.Sprintf(
			"host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
			cfg.Host, port, cfg.User, cfg.Password, cfg.Database,
		)
		if cfg.Schema != "" {
			dsn += fmt.Sprintf(" search_path=%s", cfg.Schema)
		}
		if cfg.Params != "" {
			dsn += " " + cfg.Params
		}
		dialector = postgres.Open(dsn)
	default:
		return nil, fmt.Errorf("unsupported database driver: %s", cfg.Driver)
	}

	gdb, err := gorm.Open(dialector, &gorm.Config{
		Logger: logger.New(
			log.New(os.Stderr, "\r\n", log.LstdFlags),
			logger.Config{
				SlowThreshold:             500 * time.Millisecond,
				LogLevel:                  logger.Warn,
				IgnoreRecordNotFoundError: true, // First() 未命中是正常业务分支,不刷日志。
			},
		),
	})
	if err != nil {
		return nil, fmt.Errorf("open %s database: %w", cfg.Driver, err)
	}

	if cfg.Driver == "postgres" && cfg.Schema != "" {
		if err := gdb.Exec(fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %q`, cfg.Schema)).Error; err != nil {
			return nil, fmt.Errorf("create schema: %w", err)
		}
	}

	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, err
	}
	if cfg.Driver == "sqlite" {
		// ponytail: SQLite 单写连接避免 database-is-locked;换 mysql/pg 获得真正并发。
		sqlDB.SetMaxOpenConns(1)
	} else {
		sqlDB.SetMaxOpenConns(40)
		sqlDB.SetMaxIdleConns(10)
		sqlDB.SetConnMaxIdleTime(5 * time.Minute)
	}

	if err := gdb.AutoMigrate(AllModels()...); err != nil {
		return nil, fmt.Errorf("auto migrate: %w", err)
	}
	return gdb, nil
}
