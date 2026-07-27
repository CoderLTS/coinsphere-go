// coinsphere Go 后端:单二进制,同一进程内运行 HTTP API 与工作流运行时。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"coinsphere/backend/internal/api"
	"coinsphere/backend/internal/config"
	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/security"
	"coinsphere/backend/internal/service"
)

func main() {
	configPath := flag.String("config", "", "配置文件路径(默认 config.yml,可用 COINSPHERE_CONFIG_PATH 覆盖)")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	gdb, err := db.Open(cfg.Database)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	hasher := security.NewPasswordHasher(cfg.Auth.PasswordIterations)
	if err := db.Seed(gdb, hasher); err != nil {
		log.Fatalf("seed database: %v", err)
	}
	log.Printf("database ready: driver=%s", cfg.Database.Driver)

	hostname, _ := os.Hostname()
	workerID := fmt.Sprintf("%s:%d", hostname, os.Getpid())
	app, err := service.NewApp(gdb, cfg, workerID)
	if err != nil {
		log.Fatalf("build app: %v", err)
	}
	app.StartRuntime()

	executable, _ := os.Executable()
	baseDir := filepath.Dir(executable)
	staticDir := filepath.Join(baseDir, "volumes", "static")
	uploadsDir := filepath.Join(baseDir, "volumes", "uploads")
	mux := api.NewServer(app, staticDir, uploadsDir)

	server := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler: mux,
	}
	go func() {
		log.Printf("http server listening on %s", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit
	log.Printf("shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
	app.StopRuntime()
	log.Printf("bye")
}
