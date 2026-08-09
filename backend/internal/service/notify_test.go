package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/security"
)

func TestQQBotAuthenticationAndTokenCache(t *testing.T) {
	var tokenCalls atomic.Int32
	var messageCalls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/getAppAccessToken":
			tokenCalls.Add(1)
			if r.Host != "bots.qq.com" {
				t.Errorf("token host = %q", r.Host)
			}
			var payload map[string]string
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode token request: %v", err)
			}
			if payload["appId"] != "test-app" || payload["clientSecret"] != "test-client-secret" {
				t.Errorf("unexpected token request: %#v", payload)
			}
			_, _ = io.WriteString(w, `{"access_token":"test-access-token","expires_in":"120"}`)
		case "/v2/groups/test-group/messages":
			messageCalls.Add(1)
			if r.Host != "api.sgroup.qq.com" {
				t.Errorf("message host = %q", r.Host)
			}
			if got := r.Header.Get("Authorization"); got != "QQBot test-access-token" {
				t.Errorf("authorization header = %q", got)
			}
			if got := r.Header.Get("X-Union-Appid"); got != "test-app" {
				t.Errorf("union app id = %q", got)
			}
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode message request: %v", err)
			}
			if payload["content"] != "Signal\nReview required" {
				t.Errorf("message content = %#v", payload["content"])
			}
			if payload["msg_type"] != float64(0) {
				t.Errorf("message type = %#v", payload["msg_type"])
			}
			w.Header().Set("X-Tps-trace-ID", "trace-test")
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":"message-id"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := server.Client()
	transport := client.Transport.(*http.Transport).Clone()
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	transport.TLSClientConfig.ServerName = server.Certificate().DNSNames[0]
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	client.Transport = transport
	client.Timeout = 8 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	app := &App{qqTokens: map[int64]qqAccessToken{}, qqHTTPClient: client}
	channel := &notifyRuntimeChannel{
		ChannelID: 17, ChannelType: "qq_bot",
		Config: M{
			"appId": "test-app", "targetType": "group", "targetId": "test-group",
			"tokenBaseUrl": "https://untrusted.invalid", "apiBaseUrl": "https://untrusted.invalid",
		},
		Secrets: M{"clientSecret": "test-client-secret"},
	}
	for range 2 {
		ok, response := app.sendQQBot(context.Background(), channel, "Signal", "Review required")
		if !ok || !strings.Contains(response, `"status":201`) || !strings.Contains(response, `"traceId":"trace-test"`) {
			t.Fatalf("QQ Bot response = ok:%v response:%s", ok, response)
		}
		for _, secret := range []string{"test-access-token", "test-client-secret"} {
			if strings.Contains(response, secret) {
				t.Fatalf("QQ Bot response exposed secret %q", secret)
			}
		}
	}
	if tokenCalls.Load() != 1 || messageCalls.Load() != 2 {
		t.Fatalf("QQ Bot calls = token:%d message:%d", tokenCalls.Load(), messageCalls.Load())
	}
}

func TestUpdateNotifyChannelMergesNonEmptySecrets(t *testing.T) {
	gdb := openMigratedServiceDatabase(t)
	cipher, err := security.NewSecretCipher("notify-secret-merge-test-key")
	if err != nil {
		t.Fatalf("create test cipher: %v", err)
	}
	owner := db.SystemUser{Username: "notify-secret-owner", PasswordHash: "test-only", IsActive: true}
	if err := gdb.Create(&owner).Error; err != nil {
		t.Fatalf("create owner: %v", err)
	}
	ownerID := owner.ID
	channel := db.SystemNotifyChannel{
		ChannelType: "dingtalk_webhook", OwnerID: &ownerID, DisplayName: "secret merge",
		IsEnabled: true, SettingsJSON: "{}",
		EncryptedSecretsJSON: cipher.Encrypt(dumpJSON(M{
			"accessToken": "old-token", "secret": "keep-signing-secret",
		})),
	}
	if err := gdb.Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	app := &App{DB: gdb, Cipher: cipher}
	principal := &Principal{User: &owner}

	blank := ""
	if _, err := app.UpdateNotifyChannel(channel.ID, NotifyChannelUpsertPayload{
		DisplayName: channel.DisplayName, SecretJSON: &blank,
	}, principal); err != nil {
		t.Fatalf("update channel with blank secret: %v", err)
	}
	patch := `{"accessToken":"new-token"}`
	if _, err := app.UpdateNotifyChannel(channel.ID, NotifyChannelUpsertPayload{
		DisplayName: channel.DisplayName, SecretJSON: &patch,
	}, principal); err != nil {
		t.Fatalf("merge channel secret: %v", err)
	}
	if err := gdb.First(&channel, channel.ID).Error; err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	decrypted, err := cipher.Decrypt(channel.EncryptedSecretsJSON)
	if err != nil {
		t.Fatalf("decrypt merged secret: %v", err)
	}
	secrets := loadJSONObject(decrypted)
	if secrets["accessToken"] != "new-token" || secrets["secret"] != "keep-signing-secret" {
		t.Fatalf("merged secrets = %#v", secrets)
	}
}

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

func TestNotificationOwnershipDoesNotGrantSuperuserBypass(t *testing.T) {
	gdb := openMigratedServiceDatabase(t)
	cipher, err := security.NewSecretCipher("notify-ownership-test-key")
	if err != nil {
		t.Fatalf("create test cipher: %v", err)
	}
	owner := db.SystemUser{Username: "notify-owner", PasswordHash: "test-only", IsActive: true}
	admin := db.SystemUser{Username: "notify-admin", PasswordHash: "test-only", IsActive: true}
	if err := gdb.Create(&owner).Error; err != nil {
		t.Fatalf("create owner: %v", err)
	}
	if err := gdb.Create(&admin).Error; err != nil {
		t.Fatalf("create admin: %v", err)
	}
	ownerID := owner.ID
	channel := db.SystemNotifyChannel{
		ChannelType: "smtp_email", OwnerID: &ownerID, DisplayName: "owner-only",
		IsEnabled: true, SettingsJSON: "{}", EncryptedSecretsJSON: cipher.Encrypt("{}"),
	}
	if err := gdb.Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	delivery := db.SystemNotifyDelivery{
		RecipientUserID: &ownerID, ChannelType: "in_app", Status: "success",
		Title: "owner-only", Content: "owner-only", CreatedAt: time.Now(),
	}
	if err := gdb.Create(&delivery).Error; err != nil {
		t.Fatalf("create delivery: %v", err)
	}

	app := &App{DB: gdb, Cipher: cipher}
	principal := &Principal{User: &admin, RoleCodes: []string{"R_SUPER"}}
	for _, item := range app.ListNotifyChannels(principal) {
		if item["id"] == channel.ID {
			t.Fatal("superuser ordinary channel list exposed another user's channel")
		}
	}
	if _, err := app.UpdateNotifyChannel(channel.ID, NotifyChannelUpsertPayload{DisplayName: "changed"}, principal); err == nil {
		t.Fatal("superuser updated another user's ordinary channel")
	}
	if _, err := app.SetNotifyChannelEnabled(channel.ID, false, principal); err == nil {
		t.Fatal("superuser toggled another user's ordinary channel")
	}
	if err := app.DeleteNotifyChannel(channel.ID, principal); err == nil {
		t.Fatal("superuser deleted another user's ordinary channel")
	}

	page, err := app.ListInAppNotifications(admin.ID, CursorPage{Limit: 50})
	if err != nil {
		t.Fatalf("list admin notifications: %v", err)
	}
	if records, ok := page["records"].([]M); !ok || len(records) != 0 {
		t.Fatalf("superuser personal list exposed another user's notification: %#v", page["records"])
	}
	if err := app.MarkInAppRead(admin.ID, delivery.ID); err == nil {
		t.Fatal("superuser marked another user's personal notification as read")
	}
	var stored db.SystemNotifyDelivery
	if err := gdb.First(&stored, delivery.ID).Error; err != nil {
		t.Fatalf("reload delivery: %v", err)
	}
	if stored.IsRead {
		t.Fatal("cross-user read attempt changed notification state")
	}
}
