package quant

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"coinsphere/backend/plugin/sdk"
)

type quantWorkflowGraph struct {
	SchemaVersion int `json:"schemaVersion"`
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
		return errors.New("quant workflow graph is invalid")
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
				return fmt.Errorf("quant output signal %q only accepts position candidates", node.ID)
			}
		}
		if incoming == 0 {
			return fmt.Errorf("quant output signal %q requires a position candidate", node.ID)
		}
	}
	return nil
}

func validateQuantBindings(graph quantWorkflowGraph, nodes map[string]struct {
	Type   string
	Config json.RawMessage
}) error {
	for _, node := range graph.Nodes {
		if node.Type == "official.quant.position" {
			var config struct{ TargetMode string }
			_ = json.Unmarshal(node.Config, &config)
			if config.TargetMode == "input" {
				if _, ok := node.InputBindings["target"]; !ok {
					return fmt.Errorf("node %q requires a target input", node.ID)
				}
			}
		}
		for field, binding := range node.InputBindings {
			source := nodes[binding.NodeInstanceID]
			if binding.Kind != "node" || source.Type != "official.quant.code_strategy" || len(binding.FieldPath) != 2 ||
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

func validateQuantSeriesIdentity(graph quantWorkflowGraph, nodes map[string]struct {
	Type   string
	Config json.RawMessage
}) error {
	for _, node := range graph.Nodes {
		if node.Type != "official.quant.output_signal" {
			continue
		}
		var output map[string]any
		if json.Unmarshal(node.Config, &output) != nil {
			return fmt.Errorf("node %q output signal identity is invalid", node.ID)
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
