package quant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"coinsphere/backend/plugin/sdk"
	workflowgraph "coinsphere/backend/workflow/graph"
)

type quantRegistrar struct {
	sdk.Registrar
	runtime *quantRuntime
}

func (r quantRegistrar) Action(desc sdk.NodeDescriptor, handler sdk.ActionHandler) error {
	if r.runtime.frameActions == nil {
		r.runtime.frameActions = map[string]sdk.ActionHandler{}
		r.runtime.frameDescriptors = map[string]sdk.NodeDescriptor{}
	}
	if desc.SideEffect == sdk.SideEffectNone || desc.Type == "official.quant.output_signal" {
		r.runtime.frameActions[desc.Type] = handler
		r.runtime.frameDescriptors[desc.Type] = desc
	}
	return r.Registrar.Action(desc, handler)
}

type quantFrameRequest struct {
	SourcePort    string
	SourceOutput  json.RawMessage
	Event         map[string]string
	Context       json.RawMessage
	ResultNodeIDs []string
}
type quantFrameResult struct {
	NodeOutputs map[string]json.RawMessage
	Results     []json.RawMessage
}
type quantFrameExecutor interface {
	ExecuteFrame(context.Context, quantFrameRequest) (quantFrameResult, error)
}
type quantFrameContextKey struct{}
type quantFrameContext struct{ Data json.RawMessage }

func isQuantFrame(ctx context.Context) bool { return ctx.Value(quantFrameContextKey{}) != nil }
func quantFrameData(ctx context.Context) json.RawMessage {
	v, _ := ctx.Value(quantFrameContextKey{}).(quantFrameContext)
	return v.Data
}

type quantWorkflowFrames struct {
	runtime   *quantRuntime
	request   sdk.ActionRequest
	graph     workflowgraph.Graph
	sourceID  string
	order     []string
	incoming  map[string][]workflowgraph.Edge
	nodes     map[string]workflowgraph.Node
	resultIDs []string
}

func (q *quantRuntime) compileWorkflowFrames(request sdk.ActionRequest) (*quantWorkflowFrames, error) {
	var g workflowgraph.Graph
	if json.Unmarshal(request.GraphSnapshot, &g) != nil || g.SchemaVersion != workflowgraph.SchemaVersion {
		return nil, errors.New("Quant requires a fixed workflow graph snapshot")
	}
	nodes := map[string]workflowgraph.Node{}
	for _, node := range g.Nodes {
		nodes[node.NodeInstanceID] = node
	}
	selected := map[string]bool{request.NodeInstanceID: true}
	queue := []string{}
	for _, edge := range g.Edges {
		if edge.SourceNodeInstanceID == request.NodeInstanceID && edge.SourcePort == "each" {
			queue = append(queue, edge.TargetNodeInstanceID)
		}
	}
	results := []string{}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if selected[id] {
			continue
		}
		selected[id] = true
		node := nodes[id]
		if q.frameActions[node.NodeType] == nil {
			return nil, fmt.Errorf("Quant strategy node %q is not supported in backtests", id)
		}
		if node.NodeType == "official.quant.output_signal" {
			results = append(results, id)
			continue
		}
		for _, edge := range g.Edges {
			if edge.SourceNodeInstanceID == id {
				queue = append(queue, edge.TargetNodeInstanceID)
			}
		}
	}
	if len(results) == 0 {
		return nil, errors.New("Quant backtest must reach an output signal")
	}
	strategy := workflowgraph.Graph{SchemaVersion: 3}
	for _, node := range g.Nodes {
		if selected[node.NodeInstanceID] {
			strategy.Nodes = append(strategy.Nodes, node)
		}
	}
	for _, edge := range g.Edges {
		if selected[edge.SourceNodeInstanceID] && selected[edge.TargetNodeInstanceID] && nodes[edge.SourceNodeInstanceID].NodeType != "official.quant.output_signal" {
			strategy.Edges = append(strategy.Edges, edge)
		}
	}
	order, incoming, err := workflowgraph.Order(strategy, request.NodeInstanceID)
	if err != nil {
		return nil, err
	}
	return &quantWorkflowFrames{runtime: q, request: request, graph: strategy, sourceID: request.NodeInstanceID, order: order, incoming: incoming, nodes: nodes, resultIDs: results}, nil
}
func (f *quantWorkflowFrames) ExecuteFrame(ctx context.Context, request quantFrameRequest) (quantFrameResult, error) {
	var source map[string]any
	if json.Unmarshal(request.SourceOutput, &source) != nil {
		return quantFrameResult{}, errors.New("invalid Quant frame source")
	}
	outputs := map[string]map[string]any{f.sourceID: source}
	result := quantFrameResult{NodeOutputs: map[string]json.RawMessage{f.sourceID: request.SourceOutput}}
	ctx = context.WithValue(ctx, quantFrameContextKey{}, quantFrameContext{Data: request.Context})
	var entryInput map[string]any
	_ = json.Unmarshal(f.request.Input, &entryInput)
	for _, id := range f.order {
		if id == f.sourceID {
			continue
		}
		gctx := workflowgraph.Context{Event: request.Event, Input: entryInput, Nodes: outputs}
		reached, err := workflowgraph.Reached(f.incoming[id], gctx)
		if err != nil {
			return result, err
		}
		if len(reached) == 0 {
			continue
		}
		node := f.nodes[id]
		input, err := workflowgraph.Resolve(node, f.incoming[id], gctx)
		if err != nil {
			return result, err
		}
		action := f.request
		desc := f.runtime.frameDescriptors[node.NodeType]
		if desc.Version != node.NodeVersion || workflowgraph.ValidateValue(desc.InputSchema, input) != nil {
			return result, errors.New("Quant frame input does not match its fixed node schema")
		}
		action.NodeInstanceID = id
		action.Input = mustMarshal(input)
		action.Config = node.Config
		// 帧只允许无密钥纯计算；财务输出在帧上下文中只返回候选值。
		action.Secrets = nil
		action.State = nil
		action.Incoming = nil
		for _, edge := range reached {
			action.Incoming = append(action.Incoming, sdk.NodeOutput{NodeInstanceID: edge["nodeInstanceId"].(string), SourcePort: edge["sourcePort"].(string), Output: mustMarshal(edge["output"])})
		}
		out, err := f.runtime.frameActions[node.NodeType].Execute(ctx, action)
		if err != nil {
			return result, err
		}
		var output map[string]any
		if json.Unmarshal(out.Output, &output) != nil || output == nil || workflowgraph.ValidateValue(desc.OutputSchema, output) != nil {
			return result, errors.New("invalid Quant frame output")
		}
		if len(desc.Branches) > 0 && !containsQuantString(desc.Branches, fmt.Sprint(output["branch"])) {
			return result, errors.New("Quant frame returned an undeclared branch")
		}
		outputs[id] = output
		result.NodeOutputs[id] = out.Output
		if node.NodeType == "official.quant.output_signal" {
			result.Results = append(result.Results, out.Output)
		}
	}
	return result, nil
}
