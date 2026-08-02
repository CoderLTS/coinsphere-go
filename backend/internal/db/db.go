package db

import (
	"context"
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
// 本项目用 GORM 这个 ORM 框架:把 Go struct 当数据库表来读写,基本不用手写 SQL(见 GO入门笔记『框架:GORM』)。
func Open(ctx context.Context, cfg config.DatabaseConfig) (*gorm.DB, error) {
	gdb, err := Connect(ctx, cfg)
	if err != nil {
		return nil, err
	}

	// A1 切换到版本化 SQL migration 前，应用启动仍保留现有 AutoMigrate 行为。
	if err := gdb.WithContext(ctx).AutoMigrate(AllModels()...); err != nil {
		closeDatabase(gdb)
		return nil, fmt.Errorf("auto migrate: %w", err)
	}
	return gdb, nil
}

// Connect 只建立数据库连接并配置连接池，不修改业务 schema。
// migration 命令使用此入口，避免在执行版本化 SQL 前触发 GORM AutoMigrate。
func Connect(ctx context.Context, cfg config.DatabaseConfig) (*gorm.DB, error) {
	// gorm.Dialector 是"数据库方言"接口(interface):只约定要实现哪些方法,不关心具体是谁。
	// sqlite/mysql/postgres 各自返回一个满足该接口的对象,于是下面一套代码就能支持三种数据库(见 GO入门笔记『接口』)。
	var dialector gorm.Dialector
	// switch 按 driver 选方言;Go 的 case 默认不"穿透"到下一个,不用手写 break。
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

	// gorm.Open 用选好的方言真正建立连接,返回 *gorm.DB —— 之后所有数据库操作都通过它。
	// 顺带配置查询日志:慢于 500ms 的 SQL 才告警;查不到记录(First 未命中)是正常业务分支,不刷日志。
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

	// postgres 独有:若指定了 schema,先确保它存在。%q 会给标识符自动加引号。
	if cfg.Driver == "postgres" && cfg.Schema != "" {
		if err := gdb.WithContext(ctx).Exec(fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %q`, cfg.Schema)).Error; err != nil {
			closeDatabase(gdb)
			return nil, fmt.Errorf("create schema: %w", err)
		}
	}

	// 从 GORM 取出底层标准库的 *sql.DB,用来设置连接池参数。
	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, err
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping %s database: %w", cfg.Driver, err)
	}
	// 连接池:sqlite 是单文件数据库,多个连接同时写会报 "database is locked",
	// 所以把最大连接数限成 1(写操作串行);mysql/postgres 是真正的并发数据库,给更大的池并回收空闲连接。
	if cfg.Driver == "sqlite" {
		// ponytail: SQLite 单写连接避免 database-is-locked;换 mysql/pg 获得真正并发。
		sqlDB.SetMaxOpenConns(1)
	} else {
		sqlDB.SetMaxOpenConns(40)
		sqlDB.SetMaxIdleConns(10)
		sqlDB.SetConnMaxIdleTime(5 * time.Minute)
	}

	return gdb, nil
}

func closeDatabase(gdb *gorm.DB) {
	if sqlDB, err := gdb.DB(); err == nil {
		_ = sqlDB.Close()
	}
}
