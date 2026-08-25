package contracttestplugin

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"coinsphere/backend/plugin/contracttest"
	"coinsphere/backend/plugin/sdk"
)

func TestPluginContract(t *testing.T) {
	contract := contracttest.Load(t, "..", Register)
	result := contract.Execute(context.Background(), ActionType, sdk.ActionRequest{
		Revision:       sdk.RevisionRef{WorkflowID: "workflow-1", RevisionID: "revision-1"},
		NodeInstanceID: "echo-1", OperationKey: "operation-1",
		Input:  json.RawMessage(`{"message":"contract accepted"}`),
		Config: json.RawMessage(`{}`), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	var output map[string]string
	if err := json.Unmarshal(result.Output, &output); err != nil || output["echoed"] != "contract accepted" || output["operationKey"] != "operation-1" {
		t.Fatalf("unexpected action output %s: %v", result.Output, err)
	}

	request := httptest.NewRequest(http.MethodGet, "/snapshot?batchId=untrusted", nil)
	response := contract.ServeRoute(sdk.RouteDescriptor{
		Method: http.MethodGet, Pattern: "/snapshot", Scope: sdk.ScopeResult,
	}, request, sdk.ResultScope{
		ViewID: "view-1", PluginID: PluginID, PageKey: PageKey,
		Filters:        json.RawMessage(`{"batchId":"fixed-batch"}`),
		AllowedActions: []string{"refresh"}, UserID: 42, RoleCodes: []string{"R_USER"},
	})
	if response.Code != http.StatusOK {
		t.Fatalf("result route status = %d: %s", response.Code, response.Body.String())
	}
	var snapshot struct {
		ViewID  string          `json:"viewId"`
		Filters json.RawMessage `json:"filters"`
		UserID  int64           `json:"userId"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil ||
		snapshot.ViewID != "view-1" || string(snapshot.Filters) != `{"batchId":"fixed-batch"}` || snapshot.UserID != 42 {
		t.Fatalf("unexpected result route body %s: %v", response.Body.String(), err)
	}

	page := contract.ResultPage(PageKey)
	if page.ComponentEntry != "./frontend/ResultPage.vue" || !page.Mobile {
		t.Fatalf("unexpected result page: %#v", page)
	}
}
