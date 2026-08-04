// coinsphere Go 后端:单二进制,同一进程内运行 HTTP API 与工作流运行时。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"coinsphere/backend/internal/api"
	"coinsphere/backend/internal/config"
	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/migration"
	"coinsphere/backend/internal/service"
)

const (
	shutdownTimeout   = 30 * time.Second
	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 60 * time.Second
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	configPath := flag.String("config", "", "配置文件路径(默认 config.yml,可用 COINSPHERE_CONFIG_PATH 覆盖)")
	flag.Parse()

	ctx, stop := signalContext()
	go func() {
		<-ctx.Done()
		stop() // 首次信号开始收尾后恢复默认处理,第二次信号可强制退出。
	}()
	err := run(ctx, *configPath)
	stop()
	if err != nil {
		slog.Error("backend stopped", "error_category", "runtime")
		os.Exit(1)
	}
}

// httpServerErrorWriter 丢弃可能携带客户端地址或 panic 正文的原始文本，只记录固定分类。
type httpServerErrorWriter struct{}

func (httpServerErrorWriter) Write(message []byte) (int, error) {
	slog.Error("http server error", "error_category", "http_server")
	return len(message), nil
}

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func run(parentCtx context.Context, configPath string) (runErr error) {
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}
	if err := configureLogger(cfg.Log.Level); err != nil {
		return err
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

	hostname, _ := os.Hostname()
	workerID := fmt.Sprintf("%s:%d", hostname, os.Getpid())
	app, err := service.NewApp(ctx, gdb, cfg, workerID)
	if err != nil {
		return fmt.Errorf("build app: %w", err)
	}
	if err := db.Seed(ctx, gdb, app.Hasher, cfg.Auth.BootstrapAdminPassword); err != nil {
		return fmt.Errorf("seed database: %w", err)
	}
	if cfg.Auth.BootstrapAdminPassword == "coinsphere" {
		slog.Warn("内置超管仍使用默认初始密码，请登录后尽快修改")
	}
	slog.Info("database ready", "engine", "postgres")

	app.StartRuntime()

	executable, _ := os.Executable()
	baseDir := filepath.Dir(executable)
	mux := api.NewServer(app, filepath.Join(baseDir, "volumes", "static"), filepath.Join(baseDir, "volumes", "uploads"))
	server := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler:           mux,
		ErrorLog:          log.New(httpServerErrorWriter{}, "", 0),
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		BaseContext: func(net.Listener) context.Context {
			return ctx
		},
	}
	serveErr := make(chan error, 1)
	go func() {
		slog.Info("http server started")
		err := server.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveErr <- err
	}()

	var cause error
	select {
	case <-ctx.Done():
		slog.Info("backend shutdown started")
	case err := <-serveErr:
		if err != nil {
			cause = fmt.Errorf("http server: %w", err)
		}
	}

	// 一个取消信号同时阻止新请求、新认领,并传到在途工作流及外部 I/O。
	cancel()
	app.Hub.CloseAll()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()

	var shutdownErrs []error
	if err := server.Shutdown(shutdownCtx); err != nil {
		shutdownErrs = append(shutdownErrs, fmt.Errorf("shutdown http server: %w", err))
		_ = server.Close()
	}
	if err := app.StopRuntime(shutdownCtx); err != nil {
		shutdownErrs = append(shutdownErrs, fmt.Errorf("stop runtime: %w", err))
	}
	if cause == nil && len(shutdownErrs) == 0 {
		slog.Info("backend shutdown completed")
	}
	return errors.Join(append([]error{cause}, shutdownErrs...)...)
}

func configureLogger(levelText string) error {
	var level slog.Level
	if err := level.UnmarshalText([]byte(strings.TrimSpace(levelText))); err != nil {
		return fmt.Errorf("invalid log level: %w", err)
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})))
	return nil
}
