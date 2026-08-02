package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

const signalHelperEnv = "COINSPHERE_TEST_SIGNAL_HELPER"

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
	configPath, databasePath := writeLifecycleConfig(t, port)

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
	if err := os.Remove(databasePath); err != nil {
		t.Fatalf("database connection was not closed: %v", err)
	}
}

func TestRunCleansUpAfterListenFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	configPath, databasePath := writeLifecycleConfig(t, port)

	err = run(context.Background(), configPath)
	if err == nil || !strings.Contains(err.Error(), "http server") {
		t.Fatalf("run error = %v, want HTTP listen failure", err)
	}
	if err := os.Remove(databasePath); err != nil {
		t.Fatalf("database connection was not closed: %v", err)
	}
}

func writeLifecycleConfig(t *testing.T, port int) (string, string) {
	t.Helper()
	directory := t.TempDir()
	configPath := filepath.Join(directory, "config.yml")
	databasePath := filepath.Join(directory, "lifecycle.db")
	content := fmt.Sprintf(`server:
  host: 127.0.0.1
  port: %d
database:
  driver: sqlite
  path: lifecycle.db
auth:
  secret_key: test-only-root-context-secret
  encryption_key: test-only-encryption-secret
  bootstrap_admin_password: test-only-admin-password
  password_iterations: 1000
`, port)
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return configPath, databasePath
}
