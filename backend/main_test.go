package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"coinsphere/backend/internal/migration"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

const signalHelperEnv = "COINSPHERE_TEST_SIGNAL_HELPER"

var lifecycleSchemaSequence atomic.Uint64

func TestSignalContextHandlesSIGTERM(t *testing.T) {
	if os.Getenv(signalHelperEnv) == "1" {
		ctx, stop := signalContext()
		defer stop()
		fmt.Println("ready")
		<-ctx.Done()
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not support the Unix SIGTERM subprocess check")
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestSignalContextHandlesSIGTERM$")
	cmd.Env = append(os.Environ(), signalHelperEnv+"=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("open helper stdout: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start signal helper: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	finished := false
	defer func() {
		if !finished {
			_ = cmd.Process.Kill()
			<-done
		}
	}()

	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			ready <- scanner.Text()
			return
		}
		ready <- ""
	}()
	select {
	case line := <-ready:
		if strings.TrimSpace(line) != "ready" {
			t.Fatalf("signal helper output = %q", line)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("signal helper did not become ready")
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}
	select {
	case err := <-done:
		finished = true
		if err != nil {
			t.Fatalf("signal helper exit: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("signal helper did not stop after SIGTERM")
	}
}

func TestRunStopsCleanlyWhenRootContextIsCanceled(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	configPath := writeLifecycleConfig(t, port)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, configPath) }()

	client := &http.Client{Timeout: 100 * time.Millisecond}
	healthURL := fmt.Sprintf("http://127.0.0.1:%d/health", port)
	deadline := time.Now().Add(5 * time.Second)
	for {
		response, err := client.Get(healthURL)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("backend did not become healthy")
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run after cancellation: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("backend did not finish graceful shutdown")
	}
}

func TestRunCleansUpAfterListenFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	configPath := writeLifecycleConfig(t, port)

	err = run(context.Background(), configPath)
	if err == nil || !strings.Contains(err.Error(), "http server") {
		t.Fatalf("run error = %v, want HTTP listen failure", err)
	}
}

func writeLifecycleConfig(t *testing.T, port int) string {
	t.Helper()
	baseDSN := strings.TrimSpace(os.Getenv("COINSPHERE_TEST_POSTGRES_DSN"))
	if baseDSN == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("COINSPHERE_TEST_POSTGRES_DSN is required in CI")
		}
		t.Skip("COINSPHERE_TEST_POSTGRES_DSN is not configured")
	}
	adminConfig, err := pgx.ParseConfig(baseDSN)
	if err != nil {
		t.Fatalf("parse lifecycle PostgreSQL DSN: %v", err)
	}
	admin := stdlib.OpenDB(*adminConfig)
	if err := admin.Ping(); err != nil {
		_ = admin.Close()
		t.Fatalf("ping lifecycle PostgreSQL database: %v", err)
	}
	schema := fmt.Sprintf("main_lifecycle_test_%d_%d_%d", os.Getpid(), time.Now().UnixNano(), lifecycleSchemaSequence.Add(1))
	if _, err := admin.Exec("CREATE SCHEMA " + pgx.Identifier{schema}.Sanitize()); err != nil {
		_ = admin.Close()
		t.Fatalf("create lifecycle PostgreSQL schema: %v", err)
	}
	testConfig := adminConfig.Copy()
	if testConfig.RuntimeParams == nil {
		testConfig.RuntimeParams = make(map[string]string)
	}
	testConfig.RuntimeParams["search_path"] = schema
	database := stdlib.OpenDB(*testConfig)
	if err := database.Ping(); err != nil {
		_ = database.Close()
		_, _ = admin.Exec("DROP SCHEMA " + pgx.Identifier{schema}.Sanitize() + " CASCADE")
		_ = admin.Close()
		t.Fatalf("ping lifecycle PostgreSQL schema: %v", err)
	}
	t.Cleanup(func() {
		_ = database.Close()
		_, _ = admin.Exec("DROP SCHEMA " + pgx.Identifier{schema}.Sanitize() + " CASCADE")
		_ = admin.Close()
	})
	runner, err := migration.New(database)
	if err != nil {
		t.Fatalf("create lifecycle migration runner: %v", err)
	}
	if _, err := runner.Up(context.Background(), 0); err != nil {
		t.Fatalf("migrate lifecycle PostgreSQL schema: %v", err)
	}

	directory := t.TempDir()
	configPath := filepath.Join(directory, "config.yml")
	content := fmt.Sprintf(`server:
  host: 127.0.0.1
  port: %d
database:
  dsn: %q
auth:
  secret_key: test-only-root-context-secret
  encryption_key: test-only-encryption-secret
  bootstrap_admin_password: test-only-admin-password
  password_iterations: 1000
`, port, lifecyclePostgresURL(t, baseDSN, schema))
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return configPath
}

func lifecyclePostgresURL(t *testing.T, dsn, schema string) string {
	t.Helper()
	parsed, err := url.Parse(dsn)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		t.Fatalf("PostgreSQL lifecycle test DSN must be a postgres:// or postgresql:// URL")
	}
	query := parsed.Query()
	query.Set("options", "-csearch_path="+schema)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
