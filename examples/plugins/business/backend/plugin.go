// Package business is a small, non-financial SDK 4 extension.
package business

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"coinsphere/backend/plugin/sdk"
	"github.com/gin-gonic/gin"
)

const read = "plugins.example.business.read"
const execute = "plugins.example.business.execute"

var empty = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false}`)

func resources(raw json.RawMessage) ([]sdk.WorkflowResource, error) {
	var value struct {
		WorkflowID int64 `json:"workflowId"`
	}
	if json.Unmarshal(raw, &value) != nil || value.WorkflowID <= 0 {
		return nil, errors.New("invalid business workflow scope")
	}
	return []sdk.WorkflowResource{{WorkflowID: value.WorkflowID}}, nil
}

func Register(r sdk.Registrar, _ sdk.Host) error {
	if err := r.Action(sdk.NodeDescriptor{
		Type: "example.business.task", Version: "1.0.0", Kind: sdk.NodeKindAction,
		Title: "业务事项", Description: "生成一个可在结果视图确认的业务事项", Category: "business",
		ConfigSchema: empty, InputSchema: empty, UISchema: json.RawMessage(`{}`),
		OutputSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"message":{"type":"string"}},"required":["message"],"additionalProperties":false}`),
		Pool:         sdk.PoolCompute, State: sdk.StateStateless, SideEffect: sdk.SideEffectNone,
		ExecutionPermissions: []string{execute},
	}, task{}); err != nil {
		return err
	}
	if err := r.RunPanel(sdk.RunPanelDescriptor{PanelKey: "task", Title: "业务事项", NodeTypes: []string{"example.business.task"}, ComponentEntry: "TaskPanel.vue"}); err != nil {
		return err
	}
	if err := r.ResultPage(sdk.ResultPageDescriptor{
		PageKey: "tasks", Title: "业务事项", ComponentEntry: "TaskResults.vue", PermissionCode: read,
		ScopeSchema:  json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"workflowId":{"type":"integer","minimum":1}},"required":["workflowId"],"additionalProperties":false}`),
		FilterSchema: empty, Actions: []string{"ack"}, ActionPermissions: map[string]string{"ack": execute}, Resources: resources,
	}); err != nil {
		return err
	}
	if err := r.Route(sdk.RouteDescriptor{Method: http.MethodGet, Pattern: "/summary", Scope: sdk.ScopeWorkflow, PermissionCode: read}, func(c *gin.Context, scope sdk.RouteScope) {
		workflow := scope.(sdk.WorkflowScope)
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "", "data": gin.H{"workflowId": workflow.WorkflowID, "message": "业务事项可读取"}})
	}); err != nil {
		return err
	}
	if err := r.Route(sdk.RouteDescriptor{Method: http.MethodGet, Pattern: "/tasks", Scope: sdk.ScopeResult, PermissionCode: read}, func(c *gin.Context, scope sdk.RouteScope) {
		result := scope.(sdk.ResultScope)
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "", "data": gin.H{"viewId": result.ViewID, "message": "业务事项可读取"}})
	}); err != nil {
		return err
	}
	// The custom action confirms this scoped response; it has no external effect.
	return r.Route(sdk.RouteDescriptor{Method: http.MethodPost, Pattern: "/ack", Scope: sdk.ScopeResult, Action: "ack", PermissionCode: execute}, func(c *gin.Context, scope sdk.RouteScope) {
		result := scope.(sdk.ResultScope)
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "", "data": gin.H{"viewId": result.ViewID, "message": "事项已确认"}})
	})
}

type task struct{}

func (task) Execute(context.Context, sdk.ActionRequest) (sdk.ActionResult, error) {
	return sdk.ActionResult{Output: json.RawMessage(`{"message":"业务事项可读取"}`)}, nil
}
