package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"coinsphere/backend/internal/config"
	"coinsphere/backend/internal/service"
	"coinsphere/backend/internal/testdb"
	"coinsphere/backend/plugin/sdk"
	"github.com/gorilla/websocket"
)

func TestHTTPResourceAuthorizationAndLongConnectionRevocation(t *testing.T) {
	_, database := testdb.Open(t, true)
	app := service.NewApp(database, &config.AppConfig{Auth: config.AuthConfig{SecretKey: "synthetic-api-key", PasswordIterations: 1, AccessTokenTTLMinutes: 60}}, sdk.NewRegistry())
	for _, statement := range []string{
		`INSERT INTO roles(id,code) VALUES(1,'R_SUPER'),(2,'R_USER')`,
		`INSERT INTO users(id,username) VALUES(1,'synthetic-owner'),(2,'synthetic-reader')`,
		`INSERT INTO user_roles(user_id,role_id) VALUES(1,1),(2,2)`,
	} {
		if err := database.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := app.SyncCapabilities(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`INSERT INTO role_permissions(role_id,permission_code) VALUES(2,'workflows.read'),(2,'workflows.update'),(2,'workflows.run')`).Error; err != nil {
		t.Fatal(err)
	}
	ownerToken, readerToken := app.Tokens.CreateAccessToken(1, false).Value, app.Tokens.CreateAccessToken(2, false).Value
	owner, err := app.AuthenticateAccessToken(ownerToken)
	if err != nil {
		t.Fatal(err)
	}
	ctx := service.WithPrincipal(context.Background(), owner)
	graph := json.RawMessage(`{"schemaVersion":3,"entryPoints":{"main":"manual"},"nodes":[{"nodeInstanceId":"manual","nodeType":"core.manual","nodeVersion":"1.0.0","config":{},"position":{"x":0,"y":0}},{"nodeInstanceId":"end","nodeType":"core.end","nodeVersion":"1.0.0","config":{},"position":{"x":100,"y":0}}],"edges":[{"edgeId":"end","sourceNodeInstanceId":"manual","sourcePort":"out","targetNodeInstanceId":"end","targetPort":"in"}]}`)
	w, err := app.CreateWorkflow(ctx, service.WorkflowCreatePayload{Name: "Synthetic", Graph: graph}, owner)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(app, t.TempDir(), t.TempDir(), t.TempDir())
	request := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+readerToken)
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	path := fmt.Sprintf("/api/v1/workflows/%d", w.ID)
	if response := request(http.MethodGet, path, ""); response.Code == http.StatusOK {
		t.Fatal("HTTP read bypassed the resource boundary")
	}
	if err := app.ReplaceWorkflowGrants(ctx, w.ID, []service.WorkflowGrant{{UserID: 2, Permissions: []string{"workflows.read"}}}); err != nil {
		t.Fatal(err)
	}
	if response := request(http.MethodGet, path, ""); response.Code != http.StatusOK {
		t.Fatal("HTTP rejected authorized resource read", response.Code)
	}
	if response := request(http.MethodPost, path+"/runs", fmt.Sprintf(`{"revisionId":%d}`, w.DraftRevisionID)); response.Code < http.StatusBadRequest {
		t.Fatal("HTTP run used role capability without a matching resource grant")
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	connect := func(path, protocol string) *websocket.Conn {
		t.Helper()
		dialer := websocket.Dialer{Subprotocols: []string{protocol, readerToken}}
		connection, _, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+path, http.Header{"Origin": []string{server.URL}})
		if err != nil {
			t.Fatal("cannot establish authorized long connection", err)
		}
		return connection
	}
	workflowConn := connect(fmt.Sprintf("/api/v1/ws/workflows/%d/runs", w.ID), workflowRunsWSProtocol)
	defer workflowConn.Close()
	notificationConn := connect("/api/v1/ws/notifications", notificationWSProtocol)
	defer notificationConn.Close()
	closedWithinRevalidation := func(connection *websocket.Conn) {
		t.Helper()
		if err := connection.SetReadDeadline(time.Now().Add(8 * time.Second)); err != nil {
			t.Fatal(err)
		}
		_, _, err := connection.ReadMessage()
		var networkError net.Error
		if err == nil || errors.As(err, &networkError) && networkError.Timeout() {
			t.Fatal("long connection did not close after authorization changed", err)
		}
	}
	if err := database.Exec(`DELETE FROM role_permissions WHERE role_id=2 AND permission_code='workflows.read'`).Error; err != nil {
		t.Fatal(err)
	}
	closedWithinRevalidation(workflowConn)
	reader, err := app.AuthenticateAccessToken(readerToken)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.LogoutAccessToken(reader); err != nil {
		t.Fatal(err)
	}
	closedWithinRevalidation(notificationConn)
}
