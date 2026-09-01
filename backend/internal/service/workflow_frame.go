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
}

func (e workflowFrameExecutor) ExecuteFrame(ctx context.Context, request sdk.FrameRequest) (sdk.FrameResult, error) {
	var source map[string]any
	if json.Unmarshal(request.SourceOutput, &source) != nil || source == nil || request.SourcePort == "" {
		return sdk.FrameResult{}, errors.New("workflow frame source output is invalid")
	}
	if branch, _ := source["branch"].(string); branch != "" && branch != request.SourcePort {
		return sdk.FrameResult{}, errors.New("workflow frame source port does not match its output branch")
	}
	event := request.Event
	if event == nil {
		event = map[string]string{}
	}
	outputs := map[string]map[string]any{e.sourceNodeID: source}
	result := sdk.FrameResult{NodeOutputs: map[string]json.RawMessage{e.sourceNodeID: request.SourceOutput}}
	frameNodes := workflowFrameNodeIDs(e.graph, e.sourceNodeID, request.SourcePort, request.ResultNodeIDs)
	resultNodes := workflowFrameStringSet(request.ResultNodeIDs)
	for _, nodeID := range e.graph.order {
		if nodeID == e.sourceNodeID || !frameNodes[nodeID] {
			continue
		}
		node := e.graph.nodes[nodeID]
		desc := e.graph.descriptors[nodeID]
		if !desc.Capabilities.FrameSafe {
			return sdk.FrameResult{}, fmt.Errorf("workflow frame node %q is not frame-safe", nodeID)
		}
		reachable, err := workflowNodeReachable(e.graph.incoming[nodeID], outputs, event)
		if err != nil {
			return sdk.FrameResult{}, err
		}
		if !reachable {
			continue
		}
		input, err := resolveWorkflowNodeInput(node, e.graph.incoming[nodeID], outputs, event)
		if err != nil {
			return sdk.FrameResult{}, err
		}
		if validateWorkflowSchemaValue(desc.InputSchema, input) != nil {
			return sdk.FrameResult{}, fmt.Errorf("workflow frame node %q input does not match its JSON Schema", nodeID)
		}
		_, handler, ok := e.app.Plugins.Action(node.NodeType)
		if !ok {
			return sdk.FrameResult{}, fmt.Errorf("workflow frame node %q handler is unavailable", nodeID)
		}
		state := &bufferedNodeState{app: e.app, workflowID: e.run.WorkflowID, revisionID: e.revision.ID, node: node, stateMode: desc.State}
		actionResult, err := handler.Execute(ctx, sdk.ActionRequest{
			Revision:       sdk.RevisionRef{WorkflowID: fmt.Sprint(e.run.WorkflowID), RevisionID: fmt.Sprint(e.revision.ID)},
			NodeInstanceID: nodeID, OperationKey: workflowOperationKey(e.run.ID, nodeID, 0),
			Input: mustJSON(input), Config: append(json.RawMessage(nil), node.Config...),
			Secrets: workflowSecretReader{app: e.app, revisionID: e.revision.ID, nodeInstanceID: nodeID},
			State:   state, Artifacts: workflowArtifactStore{app: e.app}, ExecutionMode: sdk.ExecutionModeBacktestFrame,
			Incoming: workflowIncomingOutputs(e.graph.incoming[nodeID], outputs, event), FrameContext: append(json.RawMessage(nil), request.Context...),
			Logger: slog.Default(),
		})
		if err != nil {
			return sdk.FrameResult{}, fmt.Errorf("workflow frame node %q failed: %w", nodeID, err)
		}
		var output map[string]any
		if json.Unmarshal(actionResult.Output, &output) != nil || output == nil || validateWorkflowSchemaValue(desc.OutputSchema, output) != nil {
			return sdk.FrameResult{}, fmt.Errorf("workflow frame node %q output does not match its JSON Schema", nodeID)
		}
		if branch, _ := output["branch"].(string); len(desc.Branches) > 0 && !containsString(desc.Branches, branch) {
			return sdk.FrameResult{}, fmt.Errorf("workflow frame node %q returned an invalid branch", nodeID)
		}
		outputs[nodeID] = output
		result.NodeOutputs[nodeID] = append(json.RawMessage(nil), actionResult.Output...)
		if resultNodes[nodeID] {
			result.Results = append(result.Results, append(json.RawMessage(nil), actionResult.Output...))
		}
	}
	return result, nil
}

func workflowFrameNodeIDs(graph workflowRunGraph, sourceNodeID, sourcePort string, resultNodeIDs []string) map[string]bool {
	result, terminal := map[string]bool{}, workflowFrameStringSet(resultNodeIDs)
	queue := []string{}
	for _, edge := range graph.graph.Edges {
		if edge.SourceNodeInstanceID == sourceNodeID && edge.SourcePort == sourcePort {
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
		if terminal[nodeID] {
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

func workflowIncomingOutputs(incoming []workflowGraphEdge, outputs map[string]map[string]any, event map[string]string) []sdk.NodeOutput {
	result := make([]sdk.NodeOutput, 0, len(incoming))
	for _, edge := range incoming {
		output := outputs[edge.SourceNodeInstanceID]
		if output == nil {
			continue
		}
		reached, err := workflowEdgeReached(edge, outputs, event)
		if err == nil && reached {
			result = append(result, sdk.NodeOutput{NodeInstanceID: edge.SourceNodeInstanceID, Output: mustJSON(output)})
		}
	}
	return result
}

func workflowFrameStringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

var _ sdk.FrameExecutor = workflowFrameExecutor{}
