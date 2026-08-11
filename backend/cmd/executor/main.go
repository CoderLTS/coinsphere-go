// coinsphere-executor always runs Paper and only starts explicitly enabled private runtimes.
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
	if cfg.Trading.SpotLiveAutoEnabled && !cfg.Trading.SpotLiveManualEnabled {
		return errors.New("trading.spot_live_auto_enabled requires trading.spot_live_manual_enabled")
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
	if !cfg.Trading.TestnetPrivateAPIEnabled && !cfg.Trading.SpotLiveManualEnabled {
		return paperExecutor.Run(ctx)
	}
	if cfg.Auth.EncryptionKey == "" || cfg.Auth.EncryptionKey == config.DefaultInsecureSecret ||
		cfg.Auth.EncryptionKey != strings.TrimSpace(cfg.Auth.EncryptionKey) {
		return errors.New("a secure auth.encryption_key is required when private trading is enabled")
	}
	cipher, err := security.NewSecretCipher(cfg.Auth.EncryptionKey)
	if err != nil {
		return errors.New("build private credential cipher")
	}
	runtimes := []func(context.Context) error{paperExecutor.Run}
	if cfg.Trading.TestnetPrivateAPIEnabled {
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
		executor, err := service.NewTestnetExecutor(gdb, cipher, privateClient, workerID+":testnet", time.Second)
		if err != nil {
			return err
		}
		runtimes = append(runtimes, verifier.Run, reconciler.Run, executor.Run)
	}
	if cfg.Trading.SpotLiveManualEnabled {
		privateClient, err := exchangebinance.NewPrivateClient(exchangebinance.PrivateClientConfig{Environment: "live"})
		if err != nil {
			return errors.New("build Binance Spot Live private client")
		}
		verifier, err := service.NewPrivateCredentialVerifier(gdb, cipher, privateClient, "live", "spot", 30*time.Second)
		if err != nil {
			return err
		}
		reconciler, err := service.NewPrivateAccountReconciler(gdb, cipher, privateClient, "live", "spot", 30*time.Second)
		if err != nil {
			return err
		}
		executor, err := service.NewPrivateExecutor(gdb, cipher, privateClient, workerID+":live", "live", "spot", "manual", time.Second)
		if err != nil {
			return err
		}
		runtimes = append(runtimes, verifier.Run, reconciler.Run, executor.Run)
		if cfg.Trading.SpotLiveAutoEnabled {
			autoExecutor, err := service.NewPrivateExecutor(gdb, cipher, privateClient, workerID+":live:auto", "live", "spot", "auto", time.Second)
			if err != nil {
				return err
			}
			runtimes = append(runtimes, autoExecutor.Run)
		}
	}
	return runTradingRuntime(ctx, runtimes)
}

func runTradingRuntime(ctx context.Context, runtimes []func(context.Context) error) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan error, len(runtimes))
	for _, runtime := range runtimes {
		go func(run func(context.Context) error) { results <- run(runCtx) }(runtime)
	}
	first := <-results
	cancel()
	all := []error{first}
	for range len(runtimes) - 1 {
		all = append(all, <-results)
	}
	return errors.Join(all...)
}
