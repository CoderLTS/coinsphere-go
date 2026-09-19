package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/plugin/sdk"
)

type workflowOperation struct {
	Config          json.RawMessage           `json:"config"`
	ResultNodeIDs   []string                  `json:"resultNodeIds"`
	ProfileBindings map[string]sdk.ProfileRef `json:"profileBindings,omitempty"`
}

// Workflow operations may be implemented by a hidden Action or by a
// draggable Trigger that also implements ActionHandler (for example Quant
// replay). Core uses the same entry descriptor for both paths.
func (a *App) workflowOperationHandler(operationType string) (sdk.NodeDescriptor, sdk.ActionHandler, bool) {
	if a.Plugins == nil {
		return sdk.NodeDescriptor{}, nil, false
	}
	if desc, handler, ok := a.Plugins.Action(operationType); ok {
		return desc, handler, true
	}
	return a.Plugins.TriggerAction(operationType)
}

func (a *App) prepareWorkflowOperation(graph workflowRunGraph, operationType string, results []string, input map[string]any) (workflowOperation, error) {
	desc, _, ok := a.workflowOperationHandler(operationType)
	if !ok || !desc.Capabilities.FrameDriver || desc.OperationConfig == nil {
		return workflowOperation{}, errors.New("插件工作流操作不可用")
	}
	if len(results) != 1 {
		return workflowOperation{}, errors.New("请选择一个策略结果节点")
	}
	if len(graph.order) == 0 {
		return workflowOperation{}, errors.New("工作流入口不可用")
	}
	source := graph.nodes[graph.order[0]]
	if source.NodeType != operationType {
		return workflowOperation{}, errors.New("操作入口与所选触发节点不一致")
	}
	resultConfigs := make([]json.RawMessage, 0, len(results))
	for _, id := range results {
		node, exists := graph.nodes[id]
		if !exists {
			return workflowOperation{}, errors.New("所选策略结果节点不存在")
		}
		resultConfigs = append(resultConfigs, node.Config)
	}
	config, err := desc.OperationConfig(source.Config, resultConfigs)
	if err != nil {
		return workflowOperation{}, fmt.Errorf("入口不支持此操作: %w", err)
	}
	if _, err := decodeJSONObject(config); err != nil {
		return workflowOperation{}, errors.New("入口操作配置必须是 JSON 对象")
	}
	if validateWorkflowSchemaValue(desc.InputSchema, input) != nil {
		return workflowOperation{}, errors.New("操作参数不符合要求")
	}
	// A frame driver can explicitly restrict the branch that feeds each frame.
	// Quant replay emits `each` for per-candle frames and `completed` only for
	// its final summary; allowing the latter here would produce a run that can
	// never reach the selected result node.
	framePorts := desc.FrameSourcePorts
	if len(framePorts) == 0 {
		framePorts = []string{""}
	}
	allowed := make(map[string]bool)
	for _, port := range framePorts {
		for id := range workflowFrameNodeIDs(graph, source.NodeInstanceID, port, results) {
			allowed[id] = true
		}
	}
	for _, id := range results {
		if !allowed[id] || !graph.descriptors[id].Capabilities.FrameResult {
			return workflowOperation{}, errors.New("所选结果节点不在该入口的策略子图中")
		}
	}
	for id := range allowed {
		nodeDesc := graph.descriptors[id]
		if !nodeDesc.Capabilities.FrameSafe || !nodeDesc.Capabilities.Deterministic || graph.nodes[id].ProfileID != "" {
			return workflowOperation{}, fmt.Errorf("节点 %s 不支持确定性回测；等待、人工和外部调用不能位于策略结果之前", graph.nodes[id].Label)
		}
	}
	profileBindings := make(map[string]sdk.ProfileRef, len(source.ProfileBindings))
	for key, ref := range source.ProfileBindings {
		profileBindings[key] = ref
	}
	return workflowOperation{Config: config, ResultNodeIDs: results, ProfileBindings: profileBindings}, nil
}

func (a *App) executeWorkflowOperation(ctx context.Context, run db.WorkflowRun, revision db.WorkflowRevision, graph workflowRunGraph) {
	var operation workflowOperation
	var input map[string]any
	if json.Unmarshal([]byte(run.OperationJSON), &operation) != nil || json.Unmarshal([]byte(run.InputJSON), &input) != nil {
		a.failWorkflowRun(run.ID, "operation", run.LeaseToken)
		return
	}
	if _, err := a.prepareWorkflowOperation(graph, run.OperationType, operation.ResultNodeIDs, input); err != nil {
		a.failWorkflowRun(run.ID, "operation", run.LeaseToken)
		return
	}
	desc, _, ok := a.workflowOperationHandler(run.OperationType)
	if !ok {
		a.failWorkflowRun(run.ID, "operation", run.LeaseToken)
		return
	}
	node := workflowGraphNode{NodeInstanceID: "$operation", Label: desc.Title, NodeType: desc.Type, NodeVersion: desc.Version, Config: operation.Config, ProfileBindings: operation.ProfileBindings}
	graph.descriptors[node.NodeInstanceID] = desc
	outputs, err := a.loadWorkflowRunCheckpoints(ctx, run.ID)
	if err != nil {
		a.failWorkflowRun(run.ID, "checkpoint", run.LeaseToken)
		return
	}
	if _, completed := outputs[node.NodeInstanceID]; completed {
		a.completeWorkflowRun(run.ID, run.LeaseToken)
		return
	}
	outcome := a.executeWorkflowNode(ctx, run, revision, graph, node, input, outputs, map[string]string{}, 0)
	if outcome.err != nil {
		if ctx.Err() != nil {
			cancelled, _ := a.runShouldStop(ctx, run.ID, run.WorkflowID)
			if cancelled {
				a.cancelWorkflowRun(run.ID, run.LeaseToken)
			} else {
				a.requeueWorkflowRun(run.ID, run.LeaseToken)
			}
		} else {
			a.failWorkflowRun(run.ID, outcome.category, run.LeaseToken)
		}
		return
	}
	if outcome.waiting {
		a.failWorkflowRun(run.ID, "operation_wait", run.LeaseToken)
		return
	}
	a.completeWorkflowRun(run.ID, run.LeaseToken)
}
