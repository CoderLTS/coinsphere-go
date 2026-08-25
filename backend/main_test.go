package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
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
	configPath, database := writeLifecycleConfig(t, port)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, configPath) }()

	client := &http.Client{Timeout: 100 * time.Millisecond}
	healthURL := fmt.Sprintf("http://127.0.0.1:%d/health/ready", port)
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

	client.Timeout = 2 * time.Second
	requestID := "lifecycle-audit-request"
	loginRequest, err := http.NewRequest(http.MethodPost,
		fmt.Sprintf("http://127.0.0.1:%d/api/v1/auth/login?token=must-not-persist", port),
		strings.NewReader(`{"username":"","password":""}`))
	if err != nil {
		t.Fatalf("build audit request: %v", err)
	}
	loginRequest.Header.Set("Content-Type", "application/json")
	loginRequest.Header.Set("X-Request-ID", requestID)
	response, err := client.Do(loginRequest)
	if err != nil {
		t.Fatalf("send audit request: %v", err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized || response.Header.Get("X-Request-ID") != requestID {
		t.Fatalf("audit response = status:%d request-id:%q", response.StatusCode, response.Header.Get("X-Request-ID"))
	}
	var action, resourcePath, outcome string
	var statusCode int
	if err := database.QueryRow(`
SELECT action, resource_path, outcome, status_code
FROM audit_records
WHERE request_id = $1
`, requestID).Scan(&action, &resourcePath, &outcome, &statusCode); err != nil {
		t.Fatalf("load HTTP audit record: %v", err)
	}
	if action != "POST /api/v1/auth/login" || resourcePath != "/api/v1/auth/login" || outcome != "failure" || statusCode != http.StatusUnauthorized {
		t.Fatalf("unexpected HTTP audit record: action=%q path=%q outcome=%q status=%d", action, resourcePath, outcome, statusCode)
	}

	metricsURL := fmt.Sprintf("http://127.0.0.1:%d/metrics", port)
	anonymousMetricsResponse, err := client.Get(metricsURL)
	if err != nil {
		t.Fatalf("read anonymous metrics response: %v", err)
	}
	_, _ = io.Copy(io.Discard, anonymousMetricsResponse.Body)
	_ = anonymousMetricsResponse.Body.Close()
	if anonymousMetricsResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous metrics status = %d, want %d", anonymousMetricsResponse.StatusCode, http.StatusUnauthorized)
	}

	validLoginRequest, err := http.NewRequest(http.MethodPost,
		fmt.Sprintf("http://127.0.0.1:%d/api/v1/auth/login", port),
		strings.NewReader(`{"username":"coinsphere","password":"test-only-admin-password"}`))
	if err != nil {
		t.Fatalf("build valid login request: %v", err)
	}
	validLoginRequest.Header.Set("Content-Type", "application/json")
	validLoginResponse, err := client.Do(validLoginRequest)
	if err != nil {
		t.Fatalf("send valid login request: %v", err)
	}
	var loginEnvelope struct {
		Data struct {
			AccessToken string `json:"accessToken"`
		} `json:"data"`
	}
	decodeErr := json.NewDecoder(validLoginResponse.Body).Decode(&loginEnvelope)
	_ = validLoginResponse.Body.Close()
	if decodeErr != nil || validLoginResponse.StatusCode != http.StatusOK || loginEnvelope.Data.AccessToken == "" {
		t.Fatalf("valid login response = status:%d token:%t decode:%v", validLoginResponse.StatusCode, loginEnvelope.Data.AccessToken != "", decodeErr)
	}

	createWorkflowRequest, err := http.NewRequest(http.MethodPost,
		fmt.Sprintf("http://127.0.0.1:%d/api/v1/workflows", port),
		strings.NewReader(`{"name":"Lifecycle workflow","templateKey":"blank"}`))
	if err != nil {
		t.Fatalf("build workflow request: %v", err)
	}
	createWorkflowRequest.Header.Set("Authorization", "Bearer "+loginEnvelope.Data.AccessToken)
	createWorkflowRequest.Header.Set("Content-Type", "application/json")
	createWorkflowResponse, err := client.Do(createWorkflowRequest)
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	var workflowEnvelope struct {
		Data struct {
			ID               int64  `json:"id"`
			Status           string `json:"status"`
			ActiveRevisionID int64  `json:"activeRevisionId"`
		} `json:"data"`
	}
	decodeErr = json.NewDecoder(createWorkflowResponse.Body).Decode(&workflowEnvelope)
	_ = createWorkflowResponse.Body.Close()
	if decodeErr != nil || createWorkflowResponse.StatusCode != http.StatusOK || workflowEnvelope.Data.ID <= 0 || workflowEnvelope.Data.Status != "paused" || workflowEnvelope.Data.ActiveRevisionID <= 0 {
		t.Fatalf("create workflow response = status:%d data:%#v decode:%v", createWorkflowResponse.StatusCode, workflowEnvelope.Data, decodeErr)
	}

	startWorkflowRequest, err := http.NewRequest(http.MethodPost,
		fmt.Sprintf("http://127.0.0.1:%d/api/v1/workflows/%d/lifecycle", port, workflowEnvelope.Data.ID),
		strings.NewReader(`{"action":"start"}`))
	if err != nil {
		t.Fatalf("build start workflow request: %v", err)
	}
	startWorkflowRequest.Header.Set("Authorization", "Bearer "+loginEnvelope.Data.AccessToken)
	startWorkflowRequest.Header.Set("Content-Type", "application/json")
	startWorkflowResponse, err := client.Do(startWorkflowRequest)
	if err != nil {
		t.Fatalf("start workflow: %v", err)
	}
	var startedEnvelope struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	decodeErr = json.NewDecoder(startWorkflowResponse.Body).Decode(&startedEnvelope)
	_ = startWorkflowResponse.Body.Close()
	if decodeErr != nil || startWorkflowResponse.StatusCode != http.StatusOK || startedEnvelope.Data.Status != "running" {
		t.Fatalf("start workflow response = status:%d data:%#v decode:%v", startWorkflowResponse.StatusCode, startedEnvelope.Data, decodeErr)
	}

	metricsRequest, err := http.NewRequest(http.MethodGet, metricsURL, nil)
	if err != nil {
		t.Fatalf("build metrics request: %v", err)
	}
	metricsRequest.Header.Set("Authorization", "Bearer "+loginEnvelope.Data.AccessToken)
	metricsResponse, err := client.Do(metricsRequest)
	if err != nil {
		t.Fatalf("read metrics: %v", err)
	}
	metricsBody, _ := io.ReadAll(metricsResponse.Body)
	_ = metricsResponse.Body.Close()
	metricsText := string(metricsBody)
	if metricsResponse.StatusCode != http.StatusOK {
		t.Fatalf("authenticated metrics status = %d: %s", metricsResponse.StatusCode, metricsText)
	}
	for _, name := range []string{
		"coinsphere_http_requests_total",
		"coinsphere_http_requests_failed_total",
		"coinsphere_http_requests_in_flight",
		"coinsphere_audit_write_failures_total",
		"coinsphere_process_uptime_seconds",
	} {
		if !strings.Contains(metricsText, name) {
			t.Fatalf("metrics output missing %s: %s", name, metricsText)
		}
	}
	if strings.Contains(metricsText, "{") {
		t.Fatalf("metrics unexpectedly contain cardinality-bearing labels: %s", metricsText)
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
	configPath, _ := writeLifecycleConfig(t, port)

	err = run(context.Background(), configPath)
	if err == nil || !strings.Contains(err.Error(), "http server") {
		t.Fatalf("run error = %v, want HTTP listen failure", err)
	}
}

func writeLifecycleConfig(t *testing.T, port int) (string, *sql.DB) {
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
	lock, err := admin.Conn(context.Background())
	if err != nil {
		_ = admin.Close()
		t.Fatalf("reserve lifecycle PostgreSQL connection: %v", err)
	}
	if _, err := lock.ExecContext(context.Background(), "SELECT pg_advisory_lock(671908427)"); err != nil {
		_ = lock.Close()
		_ = admin.Close()
		t.Fatalf("lock shared Quant test schema: %v", err)
	}
	schema := fmt.Sprintf("main_lifecycle_test_%d_%d_%d", os.Getpid(), time.Now().UnixNano(), lifecycleSchemaSequence.Add(1))
	if _, err := admin.Exec("CREATE SCHEMA " + pgx.Identifier{schema}.Sanitize()); err != nil {
		_, _ = lock.ExecContext(context.Background(), "SELECT pg_advisory_unlock(671908427)")
		_ = lock.Close()
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
		_, _ = lock.ExecContext(context.Background(), "SELECT pg_advisory_unlock(671908427)")
		_ = lock.Close()
		_ = admin.Close()
		t.Fatalf("ping lifecycle PostgreSQL schema: %v", err)
	}
	t.Cleanup(func() {
		_ = database.Close()
		_, _ = admin.Exec("DROP SCHEMA IF EXISTS plugin_quant CASCADE")
		_, _ = admin.Exec("DROP SCHEMA " + pgx.Identifier{schema}.Sanitize() + " CASCADE")
		_, _ = lock.ExecContext(context.Background(), "SELECT pg_advisory_unlock(671908427)")
		_ = lock.Close()
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
  bootstrap_admin_password: test-only-admin-password
  password_iterations: 1000
`, port, lifecyclePostgresURL(t, baseDSN, schema))
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return configPath, database
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
