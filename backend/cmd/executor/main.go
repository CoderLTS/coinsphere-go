// coinsphere-executor 只运行 Paper 意图消费、硬风控与事件投影。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"coinsphere/backend/internal/config"
	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/migration"
	"coinsphere/backend/internal/service"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	configPath := flag.String("config", "", "配置文件路径")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	hostname, _ := os.Hostname()
	workerID := fmt.Sprintf("paper:%s:%d", hostname, os.Getpid())
	if err := run(ctx, *configPath, workerID); err != nil {
		slog.Error("paper executor stopped", "error_category", "runtime")
		os.Exit(1)
	}
}

func run(ctx context.Context, configPath, workerID string) (runErr error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if strings.TrimSpace(cfg.Database.DSN) == "" {
		return errors.New("database DSN is required")
	}
	gdb, err := db.Connect(ctx, cfg.Database)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		return fmt.Errorf("get sql database: %w", err)
	}
	defer func() { runErr = errors.Join(runErr, sqlDB.Close()) }()
	runner, err := migration.New(sqlDB)
	if err != nil {
		return fmt.Errorf("build migration validator: %w", err)
	}
	if err := runner.ValidateCurrent(ctx); err != nil {
		return fmt.Errorf("validate database schema: %w", err)
	}
	executor, err := service.NewPaperExecutor(gdb, workerID, time.Second)
	if err != nil {
		return err
	}
	return executor.Run(ctx)
}
