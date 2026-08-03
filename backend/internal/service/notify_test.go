package service

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"coinsphere/backend/internal/db"
)

func TestSMTPDialCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	address := listener.Addr().(*net.TCPAddr)
	go func() {
		client, stopCancel, err := smtpDial(ctx, address.IP.String(), address.Port, false)
		if stopCancel != nil {
			stopCancel()
		}
		if client != nil {
			_ = client.Close()
		}
		done <- err
	}()

	var serverConn net.Conn
	select {
	case serverConn = <-accepted:
	case <-time.After(time.Second):
		t.Fatal("SMTP client did not connect")
	}
	defer serverConn.Close()
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("smtpDial error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("SMTP greeting wait did not stop after cancellation")
	}
}

func TestExternalDeliveryFinalizesAfterCancellation(t *testing.T) {
	gdb := openMigratedServiceDatabase(t)

	definition := db.WorkflowDefinition{Code: "notify-cancel", Version: 1}
	if err := gdb.Create(&definition).Error; err != nil {
		t.Fatalf("create definition: %v", err)
	}
	execution := db.WorkflowExecution{WorkflowDefinitionID: definition.ID, Status: "running", QueuedAt: time.Now()}
	if err := gdb.Create(&execution).Error; err != nil {
		t.Fatalf("create execution: %v", err)
	}
	node := db.WorkflowExecutionNode{WorkflowExecutionID: execution.ID, NodeID: "notify", Status: "running", StartedAt: time.Now()}
	if err := gdb.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}
	user := db.SystemUser{Username: "notify-cancel-user", PasswordHash: "test-only"}
	if err := gdb.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	channel := db.SystemNotifyChannel{ChannelType: "dingtalk_webhook", DisplayName: "取消测试"}
	if err := gdb.Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}

	requestStarted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		select {
		case <-r.Context().Done():
		case <-time.After(100 * time.Millisecond):
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	app := &App{DB: gdb.WithContext(ctx), database: gdb}
	runtimeChannel := &notifyRuntimeChannel{
		ChannelID: channel.ID, ChannelType: channel.ChannelType,
		Config: M{"webhookBaseUrl": server.URL}, Secrets: M{"accessToken": "test-token"},
	}
	done := make(chan bool, 1)
	go func() {
		done <- app.dispatchExternalChannel(ctx, &execution, &node, nil, "user", user.ID, user.ID, runtimeChannel, "title", "content", "text")
	}()
	select {
	case <-requestStarted:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("notification request did not start")
	}
	select {
	case ok := <-done:
		if ok {
			t.Fatal("canceled notification unexpectedly succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("notification dispatch did not stop after cancellation")
	}

	var delivery db.SystemNotifyDelivery
	if err := gdb.First(&delivery).Error; err != nil {
		t.Fatalf("reload delivery: %v", err)
	}
	if delivery.Status != "failed" {
		t.Fatalf("delivery status = %q, want failed", delivery.Status)
	}
}
