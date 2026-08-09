// coinsphere-executor 运行 Paper；显式启用后同时验证、对账并执行 Testnet 意图。
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
	exchangebinance "coinsphere/backend/internal/exchange/binance"
	"coinsphere/backend/internal/migration"
	"coinsphere/backend/internal/security"
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
		slog.Error("executor stopped", "error_category", "runtime")
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
	paperExecutor, err := service.NewPaperExecutor(gdb, workerID, time.Second)
	if err != nil {
		return err
	}
	if !cfg.Trading.TestnetPrivateAPIEnabled {
		return paperExecutor.Run(ctx)
	}
	if cfg.Auth.EncryptionKey == "" || cfg.Auth.EncryptionKey == config.DefaultInsecureSecret ||
		cfg.Auth.EncryptionKey != strings.TrimSpace(cfg.Auth.EncryptionKey) {
		return errors.New("a secure auth.encryption_key is required when the Testnet private API is enabled")
	}
	cipher, err := security.NewSecretCipher(cfg.Auth.EncryptionKey)
	if err != nil {
		return errors.New("build Testnet credential cipher")
	}
	privateClient, err := exchangebinance.NewPrivateClient(exchangebinance.PrivateClientConfig{})
	if err != nil {
		return errors.New("build Binance Testnet private client")
	}
	verifier, err := service.NewTestnetCredentialVerifier(gdb, cipher, privateClient, 30*time.Second)
	if err != nil {
		return err
	}
	reconciler, err := service.NewTestnetAccountReconciler(gdb, cipher, privateClient, 30*time.Second)
	if err != nil {
		return err
	}
	testnetExecutor, err := service.NewTestnetExecutor(gdb, cipher, privateClient, workerID, time.Second)
	if err != nil {
		return err
	}
	return runPaperAndTestnetRuntime(ctx, paperExecutor, verifier, reconciler, testnetExecutor)
}

func runPaperAndTestnetRuntime(
	ctx context.Context,
	paperExecutor *service.PaperExecutor,
	verifier *service.TestnetCredentialVerifier,
	reconciler *service.TestnetAccountReconciler,
	testnetExecutor *service.TestnetExecutor,
) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan error, 4)
	go func() { results <- paperExecutor.Run(runCtx) }()
	go func() { results <- verifier.Run(runCtx) }()
	go func() { results <- reconciler.Run(runCtx) }()
	go func() { results <- testnetExecutor.Run(runCtx) }()
	first := <-results
	cancel()
	return errors.Join(first, <-results, <-results, <-results)
}
