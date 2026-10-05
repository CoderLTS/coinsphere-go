// Package graph owns the portable workflow definition and pure graph evaluation.
package graph

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"

	"cel.dev/cel-go/cel"
)

const SchemaVersion = 3

type Graph struct {
	SchemaVersion int               `json:"schemaVersion"`
	EntryPoints   map[string]string `json:"entryPoints,omitempty"`
	Nodes         []Node            `json:"nodes"`
	Edges         []Edge            `json:"edges"`
}
type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}
type Node struct {
	NodeInstanceID string             `json:"nodeInstanceId"`
	NodeType       string             `json:"nodeType"`
	NodeVersion    string             `json:"nodeVersion"`
	Config         json.RawMessage    `json:"config"`
	InputBindings  map[string]Binding `json:"inputBindings,omitempty"`
	Position       *Position          `json:"position"`
}
type Edge struct {
	EdgeID               string `json:"edgeId"`
	SourceNodeInstanceID string `json:"sourceNodeInstanceId"`
	SourcePort           string `json:"sourcePort"`
	TargetNodeInstanceID string `json:"targetNodeInstanceId"`
	TargetPort           string `json:"targetPort"`
	Condition            string `json:"condition,omitempty"`
}
type Binding struct {
	Kind           string          `json:"kind"`
	NodeInstanceID string          `json:"nodeInstanceId,omitempty"`
	FieldPath      []string        `json:"fieldPath,omitempty"`
	Value          json.RawMessage `json:"value,omitempty"`
	Expression     string          `json:"expression,omitempty"`
}
type Context struct {
	Event    map[string]string
	Input    map[string]any
	Nodes    map[string]map[string]any
	Incoming []map[string]any
}

var environment, environmentError = cel.NewEnv(
	cel.Variable("event", cel.MapType(cel.StringType, cel.StringType)),
	cel.Variable("input", cel.MapType(cel.StringType, cel.DynType)),
	cel.Variable("nodes", cel.MapType(cel.StringType, cel.DynType)),
	cel.Variable("incoming", cel.ListType(cel.DynType)),
)
var programMu sync.Mutex

type compiledExpression struct {
	ast     *cel.Ast
	program cel.Program
}

var programs = map[string]compiledExpression{}

func Environment() (*cel.Env, error) { return environment, environmentError }
func Compile(expression string) (*cel.Ast, error) {
	compiled, err := compile(expression)
	return compiled.ast, err
}
func compile(expression string) (compiledExpression, error) {
	if environmentError != nil {
		return compiledExpression{}, environmentError
	}
	if len(strings.TrimSpace(expression)) == 0 || len(expression) > 4096 {
		return compiledExpression{}, errors.New("CEL expression must contain 1 to 4096 bytes")
	}
	programMu.Lock()
	defer programMu.Unlock()
	if result, ok := programs[expression]; ok {
		return result, nil
	}
	ast, issues := environment.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return compiledExpression{}, issues.Err()
	}
	program, err := environment.Program(ast)
	if err != nil {
		return compiledExpression{}, err
	}
	// ponytail: clear at 4096 expressions; use LRU only if real workloads churn.
	if len(programs) >= 4096 {
		programs = map[string]compiledExpression{}
	}
	result := compiledExpression{ast, program}
	programs[expression] = result
	return result, nil
}
func Evaluate(expression string, ctx Context) (any, error) {
	compiled, err := compile(expression)
	if err != nil {
		return nil, err
	}
	if ctx.Event == nil {
		ctx.Event = map[string]string{}
	}
	if ctx.Input == nil {
		ctx.Input = map[string]any{}
	}
	if ctx.Nodes == nil {
		ctx.Nodes = map[string]map[string]any{}
	}
	if ctx.Incoming == nil {
		ctx.Incoming = []map[string]any{}
	}
	value, _, err := compiled.program.Eval(map[string]any{"event": ctx.Event, "input": ctx.Input, "nodes": ctx.Nodes, "incoming": ctx.Incoming})
	if err != nil {
		return nil, err
	}
	return value.ConvertToNative(reflect.TypeOf((*any)(nil)).Elem())
}
func EdgeReached(edge Edge, ctx Context) (bool, error) {
	output := ctx.Nodes[edge.SourceNodeInstanceID]
	if output == nil {
		return false, nil
	}
	if edge.SourcePort != "out" && output["branch"] != edge.SourcePort {
		return false, nil
	}
	if strings.TrimSpace(edge.Condition) == "" {
		return true, nil
	}
	value, err := Evaluate(edge.Condition, ctx)
	if err != nil {
		return false, err
	}
	condition, ok := value.(bool)
	if !ok {
		return false, errors.New("edge condition must return Boolean")
	}
	return condition, nil
}
func Reached(edges []Edge, ctx Context) ([]map[string]any, error) {
	result := []map[string]any{}
	for _, edge := range edges {
		ok, err := EdgeReached(edge, ctx)
		if err != nil {
			return nil, err
		}
		if ok {
			result = append(result, map[string]any{"edgeId": edge.EdgeID, "nodeInstanceId": edge.SourceNodeInstanceID, "sourcePort": edge.SourcePort, "output": ctx.Nodes[edge.SourceNodeInstanceID]})
		}
	}
	return result, nil
}
func Resolve(node Node, edges []Edge, ctx Context) (map[string]any, error) {
	var err error
	ctx.Incoming, err = Reached(edges, ctx)
	if err != nil {
		return nil, err
	}
	input := map[string]any{}
	for field, binding := range node.InputBindings {
		switch binding.Kind {
		case "field", "input":
			root := ctx.Input
			if binding.Kind == "field" {
				root = ctx.Nodes[binding.NodeInstanceID]
			}
			value, ok := Field(root, binding.FieldPath)
			if !ok {
				return nil, fmt.Errorf("input field %q is unavailable", field)
			}
			input[field] = value
		case "literal":
			var value any
			if json.Unmarshal(binding.Value, &value) != nil {
				return nil, fmt.Errorf("input literal %q is invalid", field)
			}
			input[field] = value
		case "cel":
			value, err := Evaluate(binding.Expression, ctx)
			if err != nil {
				return nil, fmt.Errorf("input CEL %q failed: %w", field, err)
			}
			input[field] = value
		default:
			return nil, fmt.Errorf("unknown binding kind %q", binding.Kind)
		}
	}
	return input, nil
}
func Field(root map[string]any, path []string) (any, bool) {
	var current any = root
	for _, segment := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[segment]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

// Order visits each reachable node after all reachable upstreams have settled.
func Order(g Graph, start string) ([]string, map[string][]Edge, error) {
	adj := map[string][]string{}
	incoming := map[string][]Edge{}
	for _, edge := range g.Edges {
		adj[edge.SourceNodeInstanceID] = append(adj[edge.SourceNodeInstanceID], edge.TargetNodeInstanceID)
		incoming[edge.TargetNodeInstanceID] = append(incoming[edge.TargetNodeInstanceID], edge)
	}
	reachable := map[string]bool{}
	queue := []string{start}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if reachable[id] {
			continue
		}
		reachable[id] = true
		queue = append(queue, adj[id]...)
	}
	degrees := map[string]int{}
	for _, edge := range g.Edges {
		if reachable[edge.SourceNodeInstanceID] && reachable[edge.TargetNodeInstanceID] {
			degrees[edge.TargetNodeInstanceID]++
		}
	}
	queue = []string{}
	for id := range reachable {
		if degrees[id] == 0 {
			queue = append(queue, id)
		}
	}
	sort.Strings(queue)
	order := []string{}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		order = append(order, id)
		for _, target := range adj[id] {
			degrees[target]--
			if degrees[target] == 0 {
				queue = append(queue, target)
			}
		}
	}
	if len(order) != len(reachable) {
		return nil, nil, errors.New("workflow graph contains a cycle")
	}
	return order, incoming, nil
}
