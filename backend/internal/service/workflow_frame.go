package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/plugin/sdk"
)

type workflowFrameExecutor struct {
	states       map[string]*bufferedNodeState
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
	event := request.Event
	if event == nil {
		event = map[string]string{}
	}
	event["input"] = string(request.SourceOutput)
	triggeredAt, _ := time.Parse(time.RFC3339Nano, event["triggeredAt"])
	outputs := map[string]workflowNodeOutput{e.sourceNodeID: {Data: source, Port: request.SourcePort}}
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
		reachable, err := workflowNodeReachableForNode(node, e.graph.incoming[nodeID], outputs, event)
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
		state := e.states[nodeID]
		if state == nil {
			state = &bufferedNodeState{isolated: true, node: node, stateMode: desc.State}
			e.states[nodeID] = state
		}
		actionResult, _, err := e.app.callWorkflowNode(ctx, e.run, e.revision, node, sdk.ActionRequest{
			Revision:    sdk.RevisionRef{WorkflowID: fmt.Sprint(e.run.WorkflowID), RevisionID: fmt.Sprint(e.revision.ID)},
			TriggeredAt: triggeredAt, NodeInstanceID: nodeID, OperationKey: workflowOperationKey(e.run.ID, nodeID, 0),
			Input: mustJSON(input), Config: append(json.RawMessage(nil), node.Config...),
			Secrets: workflowSecretReader{app: e.app, revisionID: e.revision.ID, nodeInstanceID: nodeID},
			State:   state, Artifacts: workflowArtifactStore{app: e.app}, ExecutionMode: sdk.ExecutionModeBacktestFrame,
			Profiles: e.app.Profiles, ProfileBindings: node.ProfileBindings,
			Incoming: workflowIncomingOutputs(e.graph.incoming[nodeID], outputs, event), FrameContext: append(json.RawMessage(nil), request.Context...),
			Logger: slog.Default(),
		}, event)
		if err != nil {
			return sdk.FrameResult{}, fmt.Errorf("workflow frame node %q failed: %w", nodeID, err)
		}
		var output map[string]any
		if json.Unmarshal(actionResult.Output, &output) != nil || output == nil || validateWorkflowSchemaValue(desc.OutputSchema, output) != nil {
			return sdk.FrameResult{}, fmt.Errorf("workflow frame node %q output does not match its JSON Schema", nodeID)
		}
		if actionResult.Port == "" {
			actionResult.Port = "out"
		}
		if actionResult.Wait != nil || (!actionResult.Skip && !containsString(workflowOutputPorts(desc), actionResult.Port)) {
			return sdk.FrameResult{}, fmt.Errorf("workflow frame node %q returned an invalid branch", nodeID)
		}
		outputs[nodeID] = workflowNodeOutput{Data: output, Port: actionResult.Port, Skipped: actionResult.Skip}
		result.NodeOutputs[nodeID] = append(json.RawMessage(nil), actionResult.Output...)
		if resultNodes[nodeID] {
			result.Results = append(result.Results, append(json.RawMessage(nil), actionResult.Output...))
		}
	}
	return result, nil
}

func workflowFrameNodeIDs(graph workflowRunGraph, sourceNodeID, sourcePort string, resultNodeIDs []string) map[string]bool {
	ancestors := map[string]bool{}
	queue := append([]string(nil), resultNodeIDs...)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if ancestors[id] {
			continue
		}
		ancestors[id] = true
		for _, edge := range graph.incoming[id] {
			queue = append(queue, edge.SourceNodeInstanceID)
		}
	}
	result := map[string]bool{}
	queue = []string{sourceNodeID}
	terminals := workflowFrameStringSet(resultNodeIDs)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if result[id] {
			continue
		}
		if id != sourceNodeID {
			result[id] = true
		}
		if terminals[id] {
			continue
		}
		for _, edge := range graph.graph.Edges {
			if edge.SourceNodeInstanceID == id && (len(resultNodeIDs) == 0 || ancestors[edge.TargetNodeInstanceID]) && (id != sourceNodeID || sourcePort == "" || edge.SourcePort == sourcePort) {
				queue = append(queue, edge.TargetNodeInstanceID)
			}
		}
	}
	return result
}

func workflowIncomingOutputs(incoming []workflowGraphEdge, outputs map[string]workflowNodeOutput, event map[string]string) []sdk.NodeOutput {
	result := make([]sdk.NodeOutput, 0, len(incoming))
	for _, edge := range incoming {
		output := outputs[edge.SourceNodeInstanceID]
		if output.Data == nil {
			continue
		}
		reached, err := workflowEdgeReached(edge, outputs, event)
		if err == nil && reached {
			result = append(result, sdk.NodeOutput{NodeInstanceID: edge.SourceNodeInstanceID, Output: mustJSON(output.Data)})
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
