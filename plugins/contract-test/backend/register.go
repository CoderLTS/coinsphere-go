package contracttestplugin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"coinsphere/backend/plugin/sdk"
)

const (
	PluginID   = "official.contract-test"
	ActionType = "official.contract-test.echo"
	PageKey    = "contract-run"
)

var objectSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false}`)
var inputSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"message":{"type":"string","minLength":1}},"required":["message"],"additionalProperties":false}`)
var outputSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"echoed":{"type":"string"},"operationKey":{"type":"string"}},"required":["echoed","operationKey"],"additionalProperties":false}`)
var scopeSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"batchId":{"type":"string","minLength":1}},"required":["batchId"],"additionalProperties":false}`)

func Register(registrar sdk.Registrar) error {
	if err := registrar.Action(sdk.NodeDescriptor{
		Type: ActionType, Version: "1.0.0", Kind: sdk.NodeKindAction,
		ConfigSchema: objectSchema, UISchema: json.RawMessage(`{}`),
		InputSchema: inputSchema, OutputSchema: outputSchema,
		Pool: sdk.PoolStream, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless,
	}, echoAction{}); err != nil {
		return err
	}
	if err := registrar.Route(sdk.RouteDescriptor{
		Method: http.MethodGet, Pattern: "/snapshot", Scope: sdk.ScopeResult,
	}, snapshotRoute); err != nil {
		return err
	}
	return registrar.ResultPage(sdk.ResultPageDescriptor{
		PageKey: PageKey, Title: "Contract run", ComponentEntry: "./frontend/ResultPage.vue",
		ScopeSchema: scopeSchema, Actions: []string{"refresh"}, Mobile: true,
	})
}

type echoAction struct{}

func (echoAction) Execute(ctx context.Context, request sdk.ActionRequest) (sdk.ActionResult, error) {
	if err := ctx.Err(); err != nil {
		return sdk.ActionResult{}, err
	}
	if request.Revision.WorkflowID == "" || request.Revision.RevisionID == "" ||
		request.NodeInstanceID == "" || request.OperationKey == "" {
		return sdk.ActionResult{}, errors.New("fixed revision, node instance, and operation key are required")
	}
	var input struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(request.Input, &input); err != nil || strings.TrimSpace(input.Message) == "" {
		return sdk.ActionResult{}, errors.New("message is required")
	}
	output, err := json.Marshal(map[string]string{
		"echoed": input.Message, "operationKey": request.OperationKey,
	})
	return sdk.ActionResult{Output: output}, err
}

func snapshotRoute(w http.ResponseWriter, _ *http.Request, routeScope sdk.RouteScope) {
	scope, ok := routeScope.(sdk.ResultScope)
	if !ok || scope.PluginID != PluginID || scope.PageKey != PageKey || !slices.Contains(scope.AllowedActions, "refresh") {
		http.Error(w, "invalid result scope", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"viewId": scope.ViewID, "filters": scope.Filters, "userId": scope.UserID,
	})
}
