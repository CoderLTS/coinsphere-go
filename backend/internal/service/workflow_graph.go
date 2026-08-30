package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/operators"
	"coinsphere/backend/plugin/sdk"
	"github.com/santhosh-tekuri/jsonschema/v6"
	exprpb "google.golang.org/genproto/googleapis/api/expr/v1alpha1"
)

const (
	maxWorkflowNodes    = 256
	maxWorkflowEdges    = 1024
	maxWorkflowCELBytes = 4096
)

var workflowNodeIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type validatedWorkflowGraph struct {
	graphJSON        string
	nodeVersionsJSON string
	mainTriggerID    string
	nodes            map[string]workflowGraphNode
	nodeTypes        map[string]string
	nodeVersions     map[string]workflowNodeVersion
	descriptors      map[string]sdk.NodeDescriptor
	requiredSecrets  map[workflowSecretKey]bool
}

type workflowNodeVersion struct {
	NodeType    string `json:"nodeType"`
	NodeVersion string `json:"nodeVersion"`
}

type workflowGraph struct {
	SchemaVersion int                 `json:"schemaVersion"`
	Nodes         []workflowGraphNode `json:"nodes"`
	Edges         []workflowGraphEdge `json:"edges"`
}

type workflowGraphNode struct {
	NodeInstanceID string                          `json:"nodeInstanceId"`
	NodeType       string                          `json:"nodeType"`
	NodeVersion    string                          `json:"nodeVersion"`
	Config         json.RawMessage                 `json:"config"`
	InputBindings  map[string]workflowInputBinding `json:"inputBindings,omitempty"`
	Position       *struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
	} `json:"position"`
}

type workflowGraphEdge struct {
	EdgeID               string `json:"edgeId"`
	SourceNodeInstanceID string `json:"sourceNodeInstanceId"`
	SourcePort           string `json:"sourcePort"`
	TargetNodeInstanceID string `json:"targetNodeInstanceId"`
	TargetPort           string `json:"targetPort"`
	Condition            string `json:"condition,omitempty"`
}

type workflowInputBinding struct {
	Kind           string                       `json:"kind"`
	NodeInstanceID string                       `json:"nodeInstanceId,omitempty"`
	Sources        []workflowInputBindingSource `json:"sources,omitempty"`
	FieldPath      []string                     `json:"fieldPath,omitempty"`
	Value          json.RawMessage              `json:"value,omitempty"`
	Expression     string                       `json:"expression,omitempty"`
}

type workflowInputBindingSource struct {
	NodeInstanceID string `json:"nodeInstanceId"`
	Branch         string `json:"branch,omitempty"`
}

type workflowLoopConfig struct {
	MaxIterations  int           `json:"maxIterations"`
	TimeoutSeconds int           `json:"timeoutSeconds"`
	ExitCondition  string        `json:"exitCondition"`
	Body           workflowGraph `json:"body"`
}

type validatedWorkflowLoop struct {
	config          workflowLoopConfig
	itemID          string
	endID           string
	nodes           map[string]workflowGraphNode
	descriptors     map[string]sdk.NodeDescriptor
	requiredSecrets map[workflowSecretKey]bool
}

func (a *App) ValidateWorkflowGraph(raw json.RawMessage) error {
	_, err := a.validateWorkflowGraph(raw)
	return err
}

func (a *App) validateWorkflowGraph(raw json.RawMessage) (validatedWorkflowGraph, error) {
	if len(raw) == 0 || len(raw) > maxWorkflowGraphBytes {
		return validatedWorkflowGraph{}, fmt.Errorf("workflow graph must contain 1 to %d bytes", maxWorkflowGraphBytes)
	}
	var graph workflowGraph
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&graph); err != nil {
		return validatedWorkflowGraph{}, errors.New("workflow graph must match schema version 1")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return validatedWorkflowGraph{}, errors.New("workflow graph must contain exactly one JSON object")
	}
	if graph.SchemaVersion != 1 {
		return validatedWorkflowGraph{}, errors.New("workflow graph schemaVersion must be 1")
	}
	if len(graph.Nodes) == 0 || len(graph.Nodes) > maxWorkflowNodes {
		return validatedWorkflowGraph{}, fmt.Errorf("workflow graph must contain 1 to %d nodes", maxWorkflowNodes)
	}
	if len(graph.Edges) > maxWorkflowEdges {
		return validatedWorkflowGraph{}, fmt.Errorf("workflow graph must not exceed %d edges", maxWorkflowEdges)
	}

	descriptorCatalog := a.workflowNodeDescriptors()
	nodes := make(map[string]workflowGraphNode, len(graph.Nodes))
	nodeTypes := make(map[string]string, len(graph.Nodes))
	descriptors := make(map[string]sdk.NodeDescriptor, len(graph.Nodes))
	versions := make(map[string]workflowNodeVersion, len(graph.Nodes))
	requiredSecrets := make(map[workflowSecretKey]bool)
	triggerID := ""
	for _, node := range graph.Nodes {
		if !workflowNodeIDPattern.MatchString(node.NodeInstanceID) || nodes[node.NodeInstanceID].NodeInstanceID != "" {
			return validatedWorkflowGraph{}, fmt.Errorf("invalid or duplicate nodeInstanceId %q", node.NodeInstanceID)
		}
		desc, ok := descriptorCatalog[node.NodeType]
		if !ok {
			return validatedWorkflowGraph{}, fmt.Errorf("unknown node type %q", node.NodeType)
		}
		if desc.Version != node.NodeVersion {
			return validatedWorkflowGraph{}, fmt.Errorf("node %q requires nodeVersion %s", node.NodeInstanceID, desc.Version)
		}
		if node.NodeType == "core.loop_item" || node.NodeType == "core.loop_end" {
			return validatedWorkflowGraph{}, fmt.Errorf("node %q is only valid inside core.loop", node.NodeInstanceID)
		}
		if desc.Kind == sdk.NodeKindTrigger {
			if triggerID != "" {
				return validatedWorkflowGraph{}, errors.New("workflow graph must contain exactly one main trigger")
			}
			triggerID = node.NodeInstanceID
		}
		if node.Position == nil {
			return validatedWorkflowGraph{}, fmt.Errorf("node %q position is required", node.NodeInstanceID)
		}
		config, err := decodeJSONObject(node.Config)
		if err != nil {
			return validatedWorkflowGraph{}, fmt.Errorf("node %q config must be a JSON object", node.NodeInstanceID)
		}
		secretFields, secretRequired := workflowSecretFields(desc.ConfigSchema)
		for field := range secretFields {
			if _, exists := config[field]; exists {
				return validatedWorkflowGraph{}, fmt.Errorf("node %q secret field %q must not be stored in graph config", node.NodeInstanceID, field)
			}
			if secretRequired[field] {
				requiredSecrets[workflowSecretKey{node.NodeInstanceID, field}] = true
			}
		}
		ordinarySchema, err := ordinaryWorkflowConfigSchema(desc.ConfigSchema, secretFields)
		if err != nil || validateWorkflowSchemaValue(ordinarySchema, config) != nil {
			return validatedWorkflowGraph{}, fmt.Errorf("node %q config does not match its JSON Schema", node.NodeInstanceID)
		}
		if node.NodeType == "core.schedule" {
			if _, err := nextWorkflowScheduledAt(node.Config, time.Now().UTC()); err != nil {
				return validatedWorkflowGraph{}, fmt.Errorf("node %q schedule config is invalid", node.NodeInstanceID)
			}
		}
		nodes[node.NodeInstanceID] = node
		nodeTypes[node.NodeInstanceID] = node.NodeType
		descriptors[node.NodeInstanceID] = desc
		versions[node.NodeInstanceID] = workflowNodeVersion{NodeType: node.NodeType, NodeVersion: node.NodeVersion}
	}
	if triggerID == "" {
		return validatedWorkflowGraph{}, errors.New("workflow graph must contain exactly one main trigger")
	}
	for _, node := range graph.Nodes {
		if node.NodeType != "core.loop" {
			continue
		}
		loop, err := validateWorkflowLoop(node, descriptorCatalog)
		if err != nil {
			return validatedWorkflowGraph{}, fmt.Errorf("node %q: %w", node.NodeInstanceID, err)
		}
		for bodyID, bodyNode := range loop.nodes {
			compositeID, err := workflowLoopNodeID(node.NodeInstanceID, bodyID)
			if err != nil {
				return validatedWorkflowGraph{}, fmt.Errorf("node %q: %w", node.NodeInstanceID, err)
			}
			if _, exists := nodeTypes[compositeID]; exists {
				return validatedWorkflowGraph{}, fmt.Errorf("node %q loop body id %q conflicts with another node", node.NodeInstanceID, bodyID)
			}
			nodeTypes[compositeID] = bodyNode.NodeType
			descriptors[compositeID] = loop.descriptors[bodyID]
			versions[compositeID] = workflowNodeVersion{NodeType: bodyNode.NodeType, NodeVersion: bodyNode.NodeVersion}
			for key := range loop.requiredSecrets {
				if key.nodeInstanceID == bodyID {
					requiredSecrets[workflowSecretKey{compositeID, key.field}] = true
				}
			}
		}
	}
	metadataSyncNodes := 0
	for _, nodeType := range nodeTypes {
		if nodeType == "official.quant.sync_instruments" {
			metadataSyncNodes++
		}
	}
	if metadataSyncNodes > 1 {
		return validatedWorkflowGraph{}, errors.New("workflow graph must not contain more than one instrument metadata sync node")
	}

	adjacency, reverse, err := validateWorkflowEdges(graph.Edges, nodes, descriptors, triggerID)
	if err != nil {
		return validatedWorkflowGraph{}, err
	}
	if err := validateWorkflowTopology(nodes, adjacency, reverse, triggerID); err != nil {
		return validatedWorkflowGraph{}, err
	}
	if err := validateWorkflowBindings(nodes, descriptors, adjacency, graph.Edges); err != nil {
		return validatedWorkflowGraph{}, err
	}

	canonical, err := json.Marshal(graph)
	if err != nil {
		return validatedWorkflowGraph{}, errors.New("encode workflow graph failed")
	}
	nodeVersions, err := json.Marshal(versions)
	if err != nil {
		return validatedWorkflowGraph{}, errors.New("encode workflow node versions failed")
	}
	return validatedWorkflowGraph{
		graphJSON: string(canonical), nodeVersionsJSON: string(nodeVersions), mainTriggerID: triggerID,
		nodes: nodes, nodeTypes: nodeTypes, nodeVersions: versions, descriptors: descriptors, requiredSecrets: requiredSecrets,
	}, nil
}

func validateWorkflowLoop(node workflowGraphNode, catalog map[string]sdk.NodeDescriptor) (validatedWorkflowLoop, error) {
	var config workflowLoopConfig
	decoder := json.NewDecoder(bytes.NewReader(node.Config))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil || config.MaxIterations < 1 || config.MaxIterations > 100 ||
		config.TimeoutSeconds < 1 || config.TimeoutSeconds > 86400 {
		return validatedWorkflowLoop{}, errors.New("loop config is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return validatedWorkflowLoop{}, errors.New("loop config must contain exactly one JSON object")
	}
	ast, err := compileWorkflowCEL(config.ExitCondition)
	if err != nil || ast.OutputType().TypeName() != "bool" {
		return validatedWorkflowLoop{}, errors.New("exitCondition must compile to Boolean CEL")
	}
	if config.Body.SchemaVersion != 1 || len(config.Body.Nodes) < 2 || len(config.Body.Nodes) > maxWorkflowNodes || len(config.Body.Edges) > maxWorkflowEdges {
		return validatedWorkflowLoop{}, errors.New("loop body must be a schema version 1 DAG with valid size limits")
	}

	nodes := make(map[string]workflowGraphNode, len(config.Body.Nodes))
	descriptors := make(map[string]sdk.NodeDescriptor, len(config.Body.Nodes))
	requiredSecrets := make(map[workflowSecretKey]bool)
	itemID, endID := "", ""
	for _, bodyNode := range config.Body.Nodes {
		if !workflowNodeIDPattern.MatchString(bodyNode.NodeInstanceID) || nodes[bodyNode.NodeInstanceID].NodeInstanceID != "" {
			return validatedWorkflowLoop{}, fmt.Errorf("invalid or duplicate body nodeInstanceId %q", bodyNode.NodeInstanceID)
		}
		desc, ok := catalog[bodyNode.NodeType]
		if !ok || desc.Version != bodyNode.NodeVersion {
			return validatedWorkflowLoop{}, fmt.Errorf("body node %q has an unknown type or version", bodyNode.NodeInstanceID)
		}
		if desc.Kind == sdk.NodeKindTrigger || bodyNode.NodeType == "core.loop" || bodyNode.NodeType == "core.end" || desc.SideEffect == sdk.SideEffectHumanAction {
			return validatedWorkflowLoop{}, fmt.Errorf("body node %q is not supported inside a loop", bodyNode.NodeInstanceID)
		}
		if bodyNode.NodeType == "core.loop_item" {
			if itemID != "" {
				return validatedWorkflowLoop{}, errors.New("loop body must contain exactly one core.loop_item")
			}
			itemID = bodyNode.NodeInstanceID
		}
		if bodyNode.NodeType == "core.loop_end" {
			if endID != "" {
				return validatedWorkflowLoop{}, errors.New("loop body must contain exactly one core.loop_end")
			}
			endID = bodyNode.NodeInstanceID
		}
		if bodyNode.Position == nil {
			return validatedWorkflowLoop{}, fmt.Errorf("body node %q position is required", bodyNode.NodeInstanceID)
		}
		values, err := decodeJSONObject(bodyNode.Config)
		if err != nil {
			return validatedWorkflowLoop{}, fmt.Errorf("body node %q config must be a JSON object", bodyNode.NodeInstanceID)
		}
		secretFields, secretRequired := workflowSecretFields(desc.ConfigSchema)
		for field := range secretFields {
			if _, exists := values[field]; exists {
				return validatedWorkflowLoop{}, fmt.Errorf("body node %q secret field %q must not be stored in graph config", bodyNode.NodeInstanceID, field)
			}
			if secretRequired[field] {
				requiredSecrets[workflowSecretKey{bodyNode.NodeInstanceID, field}] = true
			}
		}
		ordinarySchema, err := ordinaryWorkflowConfigSchema(desc.ConfigSchema, secretFields)
		if err != nil || validateWorkflowSchemaValue(ordinarySchema, values) != nil {
			return validatedWorkflowLoop{}, fmt.Errorf("body node %q config does not match its JSON Schema", bodyNode.NodeInstanceID)
		}
		nodes[bodyNode.NodeInstanceID] = bodyNode
		descriptors[bodyNode.NodeInstanceID] = desc
	}
	if itemID == "" || endID == "" {
		return validatedWorkflowLoop{}, errors.New("loop body must contain exactly one core.loop_item and one core.loop_end")
	}
	adjacency, reverse, err := validateWorkflowEdges(config.Body.Edges, nodes, descriptors, itemID)
	if err != nil {
		return validatedWorkflowLoop{}, err
	}
	if err := validateWorkflowTopology(nodes, adjacency, reverse, itemID); err != nil {
		return validatedWorkflowLoop{}, err
	}
	for id, next := range adjacency {
		if len(next) == 0 && id != endID {
			return validatedWorkflowLoop{}, fmt.Errorf("body node %q must lead to core.loop_end", id)
		}
	}
	if err := validateWorkflowBindings(nodes, descriptors, adjacency, config.Body.Edges); err != nil {
		return validatedWorkflowLoop{}, err
	}
	return validatedWorkflowLoop{
		config: config, itemID: itemID, endID: endID, nodes: nodes,
		descriptors: descriptors, requiredSecrets: requiredSecrets,
	}, nil
}

func workflowLoopNodeID(loopID, bodyID string) (string, error) {
	id := loopID + "." + bodyID
	if !workflowNodeIDPattern.MatchString(id) {
		return "", fmt.Errorf("loop body node id %q is too long", bodyID)
	}
	return id, nil
}

func validateWorkflowEdges(edges []workflowGraphEdge, nodes map[string]workflowGraphNode, descriptors map[string]sdk.NodeDescriptor, triggerID string) (map[string][]string, map[string][]string, error) {
	adjacency := make(map[string][]string, len(nodes))
	reverse := make(map[string][]string, len(nodes))
	seenIDs := make(map[string]bool, len(edges))
	seenEdges := make(map[string]bool, len(edges))
	for id := range nodes {
		adjacency[id] = nil
		reverse[id] = nil
	}
	for _, edge := range edges {
		if !workflowNodeIDPattern.MatchString(edge.EdgeID) || seenIDs[edge.EdgeID] {
			return nil, nil, fmt.Errorf("invalid or duplicate edgeId %q", edge.EdgeID)
		}
		seenIDs[edge.EdgeID] = true
		if nodes[edge.SourceNodeInstanceID].NodeInstanceID == "" || nodes[edge.TargetNodeInstanceID].NodeInstanceID == "" {
			return nil, nil, fmt.Errorf("edge %q references an unknown node", edge.EdgeID)
		}
		if edge.SourceNodeInstanceID == edge.TargetNodeInstanceID {
			return nil, nil, fmt.Errorf("edge %q must not create a self loop", edge.EdgeID)
		}
		_, sourcePorts := workflowPorts(descriptors[edge.SourceNodeInstanceID])
		targetPorts, _ := workflowPorts(descriptors[edge.TargetNodeInstanceID])
		if !containsString(sourcePorts, edge.SourcePort) || !containsString(targetPorts, edge.TargetPort) {
			return nil, nil, fmt.Errorf("edge %q references an invalid port", edge.EdgeID)
		}
		if edge.TargetNodeInstanceID == triggerID {
			return nil, nil, fmt.Errorf("edge %q must not target the main trigger", edge.EdgeID)
		}
		identity := strings.Join([]string{edge.SourceNodeInstanceID, edge.SourcePort, edge.TargetNodeInstanceID, edge.TargetPort}, "\x00")
		if seenEdges[identity] {
			return nil, nil, fmt.Errorf("edge %q duplicates an existing connection", edge.EdgeID)
		}
		seenEdges[identity] = true
		if expression := strings.TrimSpace(edge.Condition); expression != "" {
			ast, err := compileWorkflowCEL(expression)
			if err != nil || ast.OutputType().TypeName() != "bool" {
				return nil, nil, fmt.Errorf("edge %q condition must compile to Boolean CEL", edge.EdgeID)
			}
			expr, err := workflowCELExpr(ast)
			if err != nil {
				return nil, nil, fmt.Errorf("edge %q condition must compile to Boolean CEL", edge.EdgeID)
			}
			if celUsesDecimalArithmetic(expr, descriptors[edge.SourceNodeInstanceID].OutputSchema) {
				return nil, nil, fmt.Errorf("edge %q condition must not perform Decimal arithmetic", edge.EdgeID)
			}
		}
		adjacency[edge.SourceNodeInstanceID] = append(adjacency[edge.SourceNodeInstanceID], edge.TargetNodeInstanceID)
		reverse[edge.TargetNodeInstanceID] = append(reverse[edge.TargetNodeInstanceID], edge.SourceNodeInstanceID)
	}
	return adjacency, reverse, nil
}

func validateWorkflowTopology(nodes map[string]workflowGraphNode, adjacency, reverse map[string][]string, triggerID string) error {
	degrees := make(map[string]int, len(nodes))
	for id := range nodes {
		degrees[id] = len(reverse[id])
	}
	queue := make([]string, 0, len(nodes))
	for id, degree := range degrees {
		if degree == 0 {
			queue = append(queue, id)
		}
	}
	visited := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		visited++
		for _, next := range adjacency[id] {
			degrees[next]--
			if degrees[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	if visited != len(nodes) {
		return errors.New("workflow graph must not contain arbitrary cycles")
	}
	reachable := map[string]bool{}
	queue = []string{triggerID}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if reachable[id] {
			continue
		}
		reachable[id] = true
		queue = append(queue, adjacency[id]...)
	}
	if len(reachable) != len(nodes) {
		return errors.New("workflow graph contains nodes unreachable from the main trigger")
	}
	return nil
}

func validateWorkflowBindings(nodes map[string]workflowGraphNode, descriptors map[string]sdk.NodeDescriptor, adjacency map[string][]string, edges []workflowGraphEdge) error {
	for nodeID, node := range nodes {
		targetProperties, required := workflowSchemaProperties(descriptors[nodeID].InputSchema)
		for field := range required {
			if _, ok := node.InputBindings[field]; !ok {
				return fmt.Errorf("node %q requires input binding %q", nodeID, field)
			}
		}
		for field, binding := range node.InputBindings {
			targetSchema, ok := targetProperties[field]
			if !ok {
				return fmt.Errorf("node %q input binding %q is not declared by its input schema", nodeID, field)
			}
			switch binding.Kind {
			case "field":
				if binding.NodeInstanceID == "" || len(binding.Sources) != 0 || len(binding.FieldPath) == 0 || len(binding.Value) != 0 || binding.Expression != "" {
					return fmt.Errorf("node %q input binding %q has invalid field mapping", nodeID, field)
				}
				if nodes[binding.NodeInstanceID].NodeInstanceID == "" || !workflowPathExists(adjacency, binding.NodeInstanceID, nodeID) {
					return fmt.Errorf("node %q input binding %q must reference a topological upstream node", nodeID, field)
				}
				sourceSchema, ok := workflowSchemaField(descriptors[binding.NodeInstanceID].OutputSchema, binding.FieldPath)
				if !ok || !workflowSchemaTypesCompatible(sourceSchema, targetSchema) {
					return fmt.Errorf("node %q input binding %q has an unknown or incompatible field path", nodeID, field)
				}
			case "literal":
				if binding.NodeInstanceID != "" || len(binding.Sources) != 0 || len(binding.FieldPath) != 0 || len(binding.Value) == 0 || binding.Expression != "" {
					return fmt.Errorf("node %q input binding %q has invalid literal mapping", nodeID, field)
				}
				var value any
				if json.Unmarshal(binding.Value, &value) != nil || validateWorkflowSchemaValue(schemaFragment(targetSchema), value) != nil {
					return fmt.Errorf("node %q input binding %q literal has an incompatible type", nodeID, field)
				}
			case "cel":
				if binding.NodeInstanceID != "" || len(binding.Sources) != 0 || len(binding.FieldPath) != 0 || len(binding.Value) != 0 || strings.TrimSpace(binding.Expression) == "" {
					return fmt.Errorf("node %q input binding %q has invalid CEL mapping", nodeID, field)
				}
				ast, err := compileWorkflowCEL(binding.Expression)
				if err != nil || !workflowCELTypeCompatible(ast.OutputType().TypeName(), targetSchema) {
					return fmt.Errorf("node %q input binding %q CEL does not compile to a compatible type", nodeID, field)
				}
				expr, err := workflowCELExpr(ast)
				if err != nil {
					return fmt.Errorf("node %q input binding %q CEL does not compile to a compatible type", nodeID, field)
				}
				if celHasArithmetic(expr) && schemaBool(targetSchema, "x-coinsphere-decimal") {
					return fmt.Errorf("node %q input binding %q must not perform Decimal arithmetic", nodeID, field)
				}
			case "condition_entry", "condition_subject", "condition_message":
				if err := validateWorkflowConditionBinding(nodeID, field, binding, targetSchema, nodes, descriptors, edges); err != nil {
					return err
				}
			case "":
				return fmt.Errorf("node %q input binding %q kind is required", nodeID, field)
			default:
				return fmt.Errorf("node %q input binding %q has unknown kind %q", nodeID, field, binding.Kind)
			}
		}
	}
	return nil
}

func validateWorkflowConditionBinding(nodeID, field string, binding workflowInputBinding, targetSchema map[string]any, nodes map[string]workflowGraphNode, descriptors map[string]sdk.NodeDescriptor, edges []workflowGraphEdge) error {
	if binding.NodeInstanceID != "" || len(binding.FieldPath) != 0 || len(binding.Value) != 0 || binding.Expression != "" || len(binding.Sources) == 0 || len(binding.Sources) > maxWorkflowNodes {
		return fmt.Errorf("node %q input binding %q has invalid condition sources", nodeID, field)
	}
	types := schemaTypes(targetSchema)
	if binding.Kind == "condition_entry" && !types["boolean"] || binding.Kind != "condition_entry" && !types["string"] {
		return fmt.Errorf("node %q input binding %q has an incompatible condition target", nodeID, field)
	}
	seen := map[string]bool{}
	for _, source := range binding.Sources {
		if source.NodeInstanceID == "" || seen[source.NodeInstanceID+"\x00"+source.Branch] || nodes[source.NodeInstanceID].NodeInstanceID == "" ||
			!isWorkflowQuantConditionType(descriptors[source.NodeInstanceID].Type) {
			return fmt.Errorf("node %q input binding %q has an invalid condition source", nodeID, field)
		}
		if !containsString(descriptors[source.NodeInstanceID].Branches, source.Branch) ||
			binding.Kind != "condition_entry" && source.Branch != "true" {
			return fmt.Errorf("node %q input binding %q has an invalid condition branch", nodeID, field)
		}
		direct := false
		for _, edge := range edges {
			if edge.SourceNodeInstanceID == source.NodeInstanceID && edge.SourcePort == source.Branch && edge.TargetNodeInstanceID == nodeID {
				direct = true
				break
			}
		}
		if !direct {
			return fmt.Errorf("node %q input binding %q must reference a direct condition edge", nodeID, field)
		}
		seen[source.NodeInstanceID+"\x00"+source.Branch] = true
	}
	return nil
}

func compileWorkflowCEL(expression string) (*cel.Ast, error) {
	expression = strings.TrimSpace(expression)
	if len(expression) == 0 || len(expression) > maxWorkflowCELBytes {
		return nil, fmt.Errorf("CEL expression must contain 1 to %d bytes", maxWorkflowCELBytes)
	}
	env, err := workflowCELEnvironment()
	if err != nil {
		return nil, err
	}
	ast, issues := env.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return nil, issues.Err()
	}
	return ast, nil
}

func workflowCELEnvironment() (*cel.Env, error) {
	return cel.NewEnv(
		cel.Variable("event", cel.MapType(cel.StringType, cel.StringType)),
		cel.Variable("input", cel.MapType(cel.StringType, cel.DynType)),
	)
}

func workflowCELExpr(ast *cel.Ast) (*exprpb.Expr, error) {
	checked, err := cel.AstToCheckedExpr(ast)
	if err != nil {
		return nil, err
	}
	return checked.Expr, nil
}

func celHasArithmetic(expr *exprpb.Expr) bool {
	if expr == nil {
		return false
	}
	if call := expr.GetCallExpr(); call != nil {
		switch call.Function {
		case operators.Add, operators.Subtract, operators.Multiply, operators.Divide, operators.Modulo, operators.Negate:
			return true
		}
		if celHasArithmetic(call.Target) {
			return true
		}
		for _, arg := range call.Args {
			if celHasArithmetic(arg) {
				return true
			}
		}
	}
	if selectExpr := expr.GetSelectExpr(); selectExpr != nil {
		return celHasArithmetic(selectExpr.Operand)
	}
	if list := expr.GetListExpr(); list != nil {
		for _, element := range list.Elements {
			if celHasArithmetic(element) {
				return true
			}
		}
	}
	if object := expr.GetStructExpr(); object != nil {
		for _, entry := range object.Entries {
			if celHasArithmetic(entry.GetMapKey()) || celHasArithmetic(entry.Value) {
				return true
			}
		}
	}
	if comprehension := expr.GetComprehensionExpr(); comprehension != nil {
		return celHasArithmetic(comprehension.IterRange) || celHasArithmetic(comprehension.AccuInit) ||
			celHasArithmetic(comprehension.LoopCondition) || celHasArithmetic(comprehension.LoopStep) || celHasArithmetic(comprehension.Result)
	}
	return false
}

func celUsesDecimalArithmetic(expr *exprpb.Expr, schema json.RawMessage) bool {
	fields := workflowDecimalFields(schema)
	if expr == nil || len(fields) == 0 {
		return false
	}
	if call := expr.GetCallExpr(); call != nil {
		switch call.Function {
		case operators.Add, operators.Subtract, operators.Multiply, operators.Divide, operators.Modulo, operators.Negate:
			if celReferencesDecimal(call.Target, fields) {
				return true
			}
			for _, arg := range call.Args {
				if celReferencesDecimal(arg, fields) {
					return true
				}
			}
		}
		if celUsesDecimalArithmetic(call.Target, schema) {
			return true
		}
		for _, arg := range call.Args {
			if celUsesDecimalArithmetic(arg, schema) {
				return true
			}
		}
	}
	if selectExpr := expr.GetSelectExpr(); selectExpr != nil {
		return celUsesDecimalArithmetic(selectExpr.Operand, schema)
	}
	return false
}

func celReferencesDecimal(expr *exprpb.Expr, fields map[string]bool) bool {
	if expr == nil {
		return false
	}
	path := make([]string, 0, 4)
	current := expr
	for current != nil {
		if selectExpr := current.GetSelectExpr(); selectExpr != nil {
			path = append([]string{selectExpr.Field}, path...)
			current = selectExpr.Operand
			continue
		}
		call := current.GetCallExpr()
		if call == nil || call.Function != operators.Index || len(call.Args) != 2 {
			break
		}
		key := call.Args[1].GetConstExpr()
		if key == nil {
			return celReferencesInput(call.Args[0])
		}
		if _, ok := key.ConstantKind.(*exprpb.Constant_StringValue); !ok {
			return celReferencesInput(call.Args[0])
		}
		path = append([]string{key.GetStringValue()}, path...)
		current = call.Args[0]
	}
	ident := current.GetIdentExpr()
	if ident != nil && ident.Name == "input" && len(path) > 0 && fields[strings.Join(path, ".")] {
		return true
	}
	if call := expr.GetCallExpr(); call != nil {
		if celReferencesDecimal(call.Target, fields) {
			return true
		}
		for _, arg := range call.Args {
			if celReferencesDecimal(arg, fields) {
				return true
			}
		}
	}
	if selectExpr := expr.GetSelectExpr(); selectExpr != nil {
		return celReferencesDecimal(selectExpr.Operand, fields)
	}
	if list := expr.GetListExpr(); list != nil {
		for _, element := range list.Elements {
			if celReferencesDecimal(element, fields) {
				return true
			}
		}
	}
	if object := expr.GetStructExpr(); object != nil {
		for _, entry := range object.Entries {
			if celReferencesDecimal(entry.GetMapKey(), fields) || celReferencesDecimal(entry.Value, fields) {
				return true
			}
		}
	}
	if comprehension := expr.GetComprehensionExpr(); comprehension != nil {
		return celReferencesDecimal(comprehension.IterRange, fields) || celReferencesDecimal(comprehension.AccuInit, fields) ||
			celReferencesDecimal(comprehension.LoopCondition, fields) || celReferencesDecimal(comprehension.LoopStep, fields) ||
			celReferencesDecimal(comprehension.Result, fields)
	}
	return false
}

func celReferencesInput(expr *exprpb.Expr) bool {
	if expr == nil {
		return false
	}
	if ident := expr.GetIdentExpr(); ident != nil {
		return ident.Name == "input"
	}
	if selectExpr := expr.GetSelectExpr(); selectExpr != nil {
		return celReferencesInput(selectExpr.Operand)
	}
	if call := expr.GetCallExpr(); call != nil && call.Function == operators.Index && len(call.Args) == 2 {
		return celReferencesInput(call.Args[0])
	}
	return false
}

func workflowDecimalFields(raw json.RawMessage) map[string]bool {
	var schema map[string]any
	_ = json.Unmarshal(raw, &schema)
	fields := map[string]bool{}
	var walk func(map[string]any, []string)
	walk = func(current map[string]any, path []string) {
		if schemaBool(current, "x-coinsphere-decimal") && len(path) > 0 {
			fields[strings.Join(path, ".")] = true
		}
		properties, _ := current["properties"].(map[string]any)
		for name, value := range properties {
			if property, ok := value.(map[string]any); ok {
				walk(property, append(path, name))
			}
		}
	}
	walk(schema, nil)
	return fields
}

func ordinaryWorkflowConfigSchema(raw json.RawMessage, secretFields map[string]map[string]any) (json.RawMessage, error) {
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, err
	}
	properties, _ := schema["properties"].(map[string]any)
	for field := range secretFields {
		delete(properties, field)
	}
	if required, exists := schema["required"].([]any); exists {
		filtered := make([]any, 0, len(required))
		for _, value := range required {
			field, _ := value.(string)
			if _, secret := secretFields[field]; !secret {
				filtered = append(filtered, value)
			}
		}
		schema["required"] = filtered
	}
	return json.Marshal(schema)
}

func validateWorkflowSchemaValue(raw json.RawMessage, value any) error {
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		return err
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	if err := compiler.AddResource("schema.json", document); err != nil {
		return err
	}
	schema, err := compiler.Compile("schema.json")
	if err != nil {
		return err
	}
	return schema.Validate(value)
}

func decodeJSONObject(raw json.RawMessage) (map[string]any, error) {
	var value map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil || value == nil {
		return nil, errors.New("not an object")
	}
	return value, nil
}

func workflowSchemaProperties(raw json.RawMessage) (map[string]map[string]any, map[string]bool) {
	var schema map[string]any
	_ = json.Unmarshal(raw, &schema)
	properties, _ := schema["properties"].(map[string]any)
	result := make(map[string]map[string]any, len(properties))
	for name, value := range properties {
		if property, ok := value.(map[string]any); ok {
			result[name] = property
		}
	}
	return result, stringSetFromAny(schema["required"])
}

func workflowSchemaField(raw json.RawMessage, path []string) (map[string]any, bool) {
	if len(path) == 0 || len(path) > 32 {
		return nil, false
	}
	var current map[string]any
	if json.Unmarshal(raw, &current) != nil {
		return nil, false
	}
	for _, segment := range path {
		if strings.TrimSpace(segment) == "" || len(segment) > 128 {
			return nil, false
		}
		properties, _ := current["properties"].(map[string]any)
		next, _ := properties[segment].(map[string]any)
		if next == nil {
			return nil, false
		}
		current = next
	}
	return current, true
}

func workflowSchemaTypesCompatible(source, target map[string]any) bool {
	sourceTypes, targetTypes := schemaTypes(source), schemaTypes(target)
	if len(sourceTypes) == 0 || len(targetTypes) == 0 {
		return true
	}
	for sourceType := range sourceTypes {
		if targetTypes[sourceType] || sourceType == "integer" && targetTypes["number"] {
			return true
		}
	}
	return false
}

func workflowCELTypeCompatible(celType string, target map[string]any) bool {
	if celType == "dyn" {
		return true
	}
	types := schemaTypes(target)
	if len(types) == 0 {
		return true
	}
	switch celType {
	case "int", "uint":
		return types["integer"] || types["number"]
	case "double":
		return types["number"]
	case "bool":
		return types["boolean"]
	case "list":
		return types["array"]
	case "map":
		return types["object"]
	default:
		return types[celType]
	}
}

func schemaTypes(schema map[string]any) map[string]bool {
	result := map[string]bool{}
	switch value := schema["type"].(type) {
	case string:
		result[value] = true
	case []any:
		for _, item := range value {
			if text, ok := item.(string); ok {
				result[text] = true
			}
		}
	}
	return result
}

func schemaBool(schema map[string]any, key string) bool {
	value, _ := schema[key].(bool)
	return value
}

func schemaFragment(schema map[string]any) json.RawMessage {
	copy := make(map[string]any, len(schema)+1)
	for key, value := range schema {
		copy[key] = value
	}
	copy["$schema"] = workflowJSONSchema202012
	raw, _ := json.Marshal(copy)
	return raw
}

func workflowPathExists(adjacency map[string][]string, source, target string) bool {
	seen := map[string]bool{}
	queue := append([]string(nil), adjacency[source]...)
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == target {
			return true
		}
		if seen[current] {
			continue
		}
		seen[current] = true
		queue = append(queue, adjacency[current]...)
	}
	return false
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
