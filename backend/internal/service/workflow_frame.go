package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/plugin/sdk"
)

type workflowFrameExecutor struct {
	app          *App
	run          db.WorkflowRun
	revision     db.WorkflowRevision
	graph        workflowRunGraph
	sourceNodeID string
	frameNodeIDs map[string]bool
}

func (e workflowFrameExecutor) ExecuteFrame(ctx context.Context, request sdk.FrameRequest) (sdk.FrameResult, error) {
	var source map[string]any
	if json.Unmarshal(request.SourceOutput, &source) != nil || source == nil || source["branch"] != "each" {
		return sdk.FrameResult{}, errors.New("workflow backtest frame source output is invalid")
	}
	outputs := map[string]map[string]any{e.sourceNodeID: source}
	result := sdk.FrameResult{NodeOutputs: map[string]json.RawMessage{e.sourceNodeID: request.SourceOutput}}
	event := map[string]string{"type": "backtest", "time": fmt.Sprint(source["evaluatedAt"]), "triggeredAt": fmt.Sprint(source["evaluatedAt"])}
	for _, nodeID := range e.graph.order {
		if nodeID == e.sourceNodeID || !e.frameNodeIDs[nodeID] {
			continue
		}
		node := e.graph.nodes[nodeID]
		if !workflowBacktestFrameNode(node.NodeType) {
			continue
		}
		reachable, err := workflowNodeReachable(e.graph.incoming[nodeID], outputs, event)
		if err != nil {
			return sdk.FrameResult{}, err
		}
		if !reachable {
			continue
		}
		if !workflowQuantPositionInputReady(node, outputs) {
			continue
		}
		input, err := resolveWorkflowNodeInput(node, e.graph.incoming[nodeID], outputs, event)
		if err != nil {
			return sdk.FrameResult{}, err
		}
		if err := injectWorkflowQuantInput(node, e.graph.incoming[nodeID], outputs, event, input); err != nil {
			return sdk.FrameResult{}, err
		}
		if node.NodeType == "official.quant.output_signal" {
			input["previousTargetPosition"] = request.PreviousTargetPosition
		}
		desc := e.graph.descriptors[nodeID]
		if validateWorkflowSchemaValue(desc.InputSchema, input) != nil {
			return sdk.FrameResult{}, fmt.Errorf("backtest frame node %q input does not match its JSON Schema", nodeID)
		}
		_, handler, ok := e.app.Plugins.Action(node.NodeType)
		if !ok {
			return sdk.FrameResult{}, fmt.Errorf("backtest frame node %q handler is unavailable", nodeID)
		}
		state := &bufferedNodeState{app: e.app, workflowID: e.run.WorkflowID, revisionID: e.revision.ID, node: node, stateMode: desc.State}
		actionResult, err := handler.Execute(ctx, sdk.ActionRequest{
			Revision:       sdk.RevisionRef{WorkflowID: fmt.Sprint(e.run.WorkflowID), RevisionID: fmt.Sprint(e.revision.ID)},
			NodeInstanceID: nodeID, OperationKey: workflowOperationKey(e.run.ID, nodeID, 0),
			Input: mustJSON(input), Config: append(json.RawMessage(nil), node.Config...),
			Secrets: workflowSecretReader{app: e.app, revisionID: e.revision.ID, nodeInstanceID: nodeID},
			State:   state, Artifacts: workflowArtifactStore{app: e.app}, ExecutionMode: sdk.ExecutionModeBacktestFrame,
			Logger: slog.Default(),
		})
		if err != nil {
			return sdk.FrameResult{}, fmt.Errorf("backtest frame node %q failed: %w", nodeID, err)
		}
		var output map[string]any
		if json.Unmarshal(actionResult.Output, &output) != nil || output == nil || validateWorkflowSchemaValue(desc.OutputSchema, output) != nil {
			return sdk.FrameResult{}, fmt.Errorf("backtest frame node %q output does not match its JSON Schema", nodeID)
		}
		if branch, _ := output["branch"].(string); len(desc.Branches) > 0 && !containsString(desc.Branches, branch) {
			return sdk.FrameResult{}, fmt.Errorf("backtest frame node %q returned an invalid branch", nodeID)
		}
		outputs[nodeID] = output
		result.NodeOutputs[nodeID] = append(json.RawMessage(nil), actionResult.Output...)
		if node.NodeType == "official.quant.output_signal" {
			result.Signals = append(result.Signals, append(json.RawMessage(nil), actionResult.Output...))
		}
	}
	return result, nil
}

func workflowBacktestFrameNodeIDs(graph workflowRunGraph, sourceNodeID string) map[string]bool {
	result := map[string]bool{}
	queue := []string{}
	for _, edge := range graph.graph.Edges {
		if edge.SourceNodeInstanceID == sourceNodeID && edge.SourcePort == "each" {
			queue = append(queue, edge.TargetNodeInstanceID)
		}
	}
	for len(queue) > 0 {
		nodeID := queue[0]
		queue = queue[1:]
		if result[nodeID] {
			continue
		}
		result[nodeID] = true
		if graph.nodes[nodeID].NodeType == "official.quant.output_signal" {
			continue
		}
		for _, edge := range graph.graph.Edges {
			if edge.SourceNodeInstanceID == nodeID {
				queue = append(queue, edge.TargetNodeInstanceID)
			}
		}
	}
	return result
}

func workflowBacktestFrameNode(nodeType string) bool {
	return isWorkflowQuantConditionType(nodeType) || nodeType == "official.quant.code_strategy" ||
		nodeType == "official.quant.position" || nodeType == "official.quant.output_signal"
}

func workflowQuantPositionInputReady(node workflowGraphNode, outputs map[string]map[string]any) bool {
	if node.NodeType != "official.quant.position" {
		return true
	}
	var config struct {
		TargetMode string `json:"targetMode"`
	}
	if json.Unmarshal(node.Config, &config) != nil || config.TargetMode != "input" {
		return true
	}
	binding := node.InputBindings["target"]
	ready, _ := outputs[binding.NodeInstanceID]["ready"].(bool)
	return ready
}

func injectWorkflowQuantInput(node workflowGraphNode, incoming []workflowGraphEdge, outputs map[string]map[string]any, event map[string]string, input map[string]any) error {
	if node.NodeType != "official.quant.output_signal" {
		return nil
	}
	candidates := make([]any, 0, len(incoming))
	for _, edge := range incoming {
		if output := outputs[edge.SourceNodeInstanceID]; output != nil {
			reached, err := workflowEdgeReached(edge, outputs, event)
			if err != nil {
				return err
			}
			if reached && eNodeTarget(output) != nil {
				candidates = append(candidates, eNodeTarget(output))
			}
		}
	}
	input["candidates"] = candidates
	nodeValues := make(map[string]any, len(outputs))
	for nodeID, output := range outputs {
		if output["ready"] != nil || output["targetPosition"] != nil {
			nodeValues[nodeID] = output
		}
	}
	input["nodeValues"] = nodeValues
	return nil
}

func eNodeTarget(output map[string]any) any {
	if output["targetPosition"] == nil {
		return nil
	}
	return map[string]any{
		"targetPosition": output["targetPosition"], "evaluatedAt": output["evaluatedAt"],
		"sourceNodeInstanceId": output["sourceNodeInstanceId"],
	}
}

var _ sdk.FrameExecutor = workflowFrameExecutor{}
