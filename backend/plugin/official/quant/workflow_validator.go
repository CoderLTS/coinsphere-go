package quant

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"coinsphere/backend/plugin/sdk"
)

type quantWorkflowGraph struct {
	SchemaVersion int               `json:"schemaVersion"`
	EntryPoints   map[string]string `json:"entryPoints"`
	Nodes         []struct {
		ID            string                          `json:"nodeInstanceId"`
		Type          string                          `json:"nodeType"`
		Config        json.RawMessage                 `json:"config"`
		InputBindings map[string]quantWorkflowBinding `json:"inputBindings"`
	} `json:"nodes"`
	Edges []struct {
		Source, Port, Target string
	} `json:"-"`
	RawEdges []struct {
		Source string `json:"sourceNodeInstanceId"`
		Port   string `json:"sourcePort"`
		Target string `json:"targetNodeInstanceId"`
	} `json:"edges"`
}

type quantWorkflowBinding struct {
	Kind           string   `json:"kind"`
	NodeInstanceID string   `json:"nodeInstanceId"`
	FieldPath      []string `json:"fieldPath"`
}

func validateQuantWorkflow(input sdk.WorkflowValidationContext) error {
	var graph quantWorkflowGraph
	if json.Unmarshal(input.Graph, &graph) != nil {
		return errors.New("Quant workflow graph is invalid")
	}
	nodes := make(map[string]struct {
		Type   string
		Config json.RawMessage
	}, len(graph.Nodes))
	quantNodes := 0
	for _, node := range graph.Nodes {
		nodes[node.ID] = struct {
			Type   string
			Config json.RawMessage
		}{node.Type, node.Config}
		if strings.HasPrefix(node.Type, quantPluginID+".") {
			quantNodes++
		}
	}
	if quantNodes == 0 {
		return nil
	}
	if graph.SchemaVersion == 2 {
		entryID := graph.EntryPoints["backtest"]
		entry, exists := nodes[entryID]
		desc, descriptorExists := input.Nodes[entry.Type]
		if !exists || !descriptorExists || !desc.Capabilities.FrameDriver {
			return errors.New("Quant backtest entryPoint must reference its frame driver")
		}
		queue := make([]string, 0)
		for _, edge := range graph.RawEdges {
			if edge.Source == graph.EntryPoints["backtest"] && edge.Port == "each" {
				queue = append(queue, edge.Target)
			}
		}
		seen, resultFound := map[string]bool{}, false
		for len(queue) > 0 {
			id := queue[0]
			queue = queue[1:]
			if seen[id] {
				continue
			}
			seen[id] = true
			desc, ok := input.Nodes[nodes[id].Type]
			if !ok || !desc.Capabilities.FrameSafe || !desc.Capabilities.Deterministic || !desc.Capabilities.Stateless {
				return fmt.Errorf("Quant backtest node %q must be frame-safe", id)
			}
			if desc.Capabilities.FrameResult {
				resultFound = true
				continue
			}
			for _, edge := range graph.RawEdges {
				if edge.Source == id {
					queue = append(queue, edge.Target)
				}
			}
		}
		if !resultFound {
			return errors.New("Quant backtest frame must reach a result node")
		}
	}
	if err := validateQuantBindings(graph, nodes); err != nil {
		return err
	}
	if err := validateQuantSeriesIdentity(graph, nodes); err != nil {
		return err
	}
	for _, node := range graph.Nodes {
		if node.Type != "official.quant.output_signal" {
			continue
		}
		incoming := 0
		for _, edge := range graph.RawEdges {
			if edge.Target != node.ID {
				continue
			}
			incoming++
			if nodes[edge.Source].Type != "official.quant.position" {
				return fmt.Errorf("Quant output signal %q only accepts position candidates", node.ID)
			}
		}
		if incoming == 0 {
			return fmt.Errorf("Quant output signal %q requires a position candidate", node.ID)
		}
	}
	return nil
}

func validateQuantBindings(graph quantWorkflowGraph, nodes map[string]struct {
	Type   string
	Config json.RawMessage
}) error {
	for _, node := range graph.Nodes {
		if node.Type == "official.quant.market_signal" {
			var incoming []struct{ Source, Port, Target string }
			for _, edge := range graph.RawEdges {
				if edge.Target == node.ID {
					incoming = append(incoming, struct{ Source, Port, Target string }{edge.Source, edge.Port, edge.Target})
				}
			}
			if len(incoming) != 1 || incoming[0].Port != "true" || !strings.HasPrefix(nodes[incoming[0].Source].Type, quantPluginID+".") {
				return fmt.Errorf("node %q must connect to exactly one Quant condition true branch", node.ID)
			}
			if err := validateFieldBindings(node.ID, incoming[0].Source, node.InputBindings, map[string]string{
				"venue": "venue", "market": "market", "instrument": "instrument", "interval": "interval",
				"name": "formula", "indicator": "indicator", "candleCloseTime": "candleCloseTime", "summary": "summary", "values": "value",
			}); err != nil {
				return err
			}
		}
		if node.Type == "official.quant.position" {
			var config struct{ TargetMode, DecimalField string }
			if json.Unmarshal(node.Config, &config) != nil || config.TargetMode != "input" {
				continue
			}
			binding, ok := node.InputBindings["target"]
			if !ok || binding.Kind != "field" || len(binding.FieldPath) != 2 || binding.FieldPath[0] != "decimals" ||
				binding.FieldPath[1] != config.DecimalField || nodes[binding.NodeInstanceID].Type != "official.quant.code_strategy" {
				return fmt.Errorf("node %q input target must reference its configured code strategy Decimal output", node.ID)
			}
		}
		for field, binding := range node.InputBindings {
			source := nodes[binding.NodeInstanceID]
			if binding.Kind != "field" || source.Type != "official.quant.code_strategy" || len(binding.FieldPath) != 2 ||
				binding.FieldPath[0] != "booleans" && binding.FieldPath[0] != "decimals" {
				continue
			}
			var config struct {
				BooleanOutputs []string `json:"booleanOutputs"`
				DecimalOutputs []string `json:"decimalOutputs"`
			}
			if json.Unmarshal(source.Config, &config) != nil {
				return fmt.Errorf("node %q input binding %q references an invalid code strategy", node.ID, field)
			}
			declared := config.BooleanOutputs
			if binding.FieldPath[0] == "decimals" {
				declared = config.DecimalOutputs
			}
			if !containsQuantString(declared, binding.FieldPath[1]) {
				return fmt.Errorf("node %q input binding %q references an undeclared code strategy output", node.ID, field)
			}
		}
	}
	return nil
}

func validateFieldBindings(nodeID, sourceID string, bindings map[string]quantWorkflowBinding, expected map[string]string) error {
	for targetField, sourceField := range expected {
		binding, ok := bindings[targetField]
		if !ok || binding.Kind != "field" || binding.NodeInstanceID != sourceID || len(binding.FieldPath) != 1 || binding.FieldPath[0] != sourceField {
			return fmt.Errorf("node %q input binding %q must use its Quant condition source", nodeID, targetField)
		}
	}
	return nil
}

func validateQuantSeriesIdentity(graph quantWorkflowGraph, nodes map[string]struct {
	Type   string
	Config json.RawMessage
}) error {
	var main struct{ Venue, Market, Instrument, Interval string }
	if entry := graph.EntryPoints["backtest"]; entry != "" {
		if node, ok := nodes[entry]; ok && json.Unmarshal(node.Config, &main) != nil {
			return errors.New("Quant backtest entry identity is invalid")
		}
	}
	for _, node := range graph.Nodes {
		if node.Type != "official.quant.output_signal" {
			continue
		}
		var output struct{ Venue, Market, Instrument, Interval string }
		if json.Unmarshal(node.Config, &output) != nil {
			return fmt.Errorf("node %q output signal identity is invalid", node.ID)
		}
		if main.Venue != "" && (output.Venue != main.Venue || output.Market != main.Market || output.Instrument != main.Instrument || output.Interval != main.Interval) {
			return fmt.Errorf("node %q must use the Quant backtest main series", node.ID)
		}
		incoming := 0
		for _, edge := range graph.RawEdges {
			if edge.Target != node.ID {
				continue
			}
			incoming++
			if nodes[edge.Source].Type != "official.quant.position" {
				return fmt.Errorf("node %q only accepts Quant position candidates", node.ID)
			}
		}
		if incoming == 0 {
			return fmt.Errorf("node %q requires a Quant position candidate", node.ID)
		}
	}
	return nil
}
