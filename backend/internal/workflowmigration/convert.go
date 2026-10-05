// Package workflowmigration is the one-time, offline generation 3 to 4 importer.
package workflowmigration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"coinsphere/backend/plugin/sdk"
	"coinsphere/backend/workflow/graph"
)

type NodeVersionMapping struct {
	NodeType    string            `json:"nodeType"`
	NodeVersion string            `json:"nodeVersion"`
	Fields      map[string]string `json:"fields,omitempty"`
}
type ExpressionMapping struct {
	Expression string                   `json:"expression,omitempty"`
	Sources    map[string]graph.Binding `json:"sources,omitempty"`
}
type Mappings struct {
	NodeVersions map[string]NodeVersionMapping `json:"nodeVersions,omitempty"`
	Expressions  map[string]ExpressionMapping  `json:"expressions,omitempty"`
	Owners       map[int64]int64               `json:"owners,omitempty"`
	Results      map[int64]ResultMapping       `json:"results,omitempty"`
}
type ResultMapping struct {
	PluginID       string          `json:"pluginId"`
	PageKey        string          `json:"pageKey"`
	Scope          json.RawMessage `json:"scope"`
	Filters        json.RawMessage `json:"filters"`
	AllowedActions []string        `json:"allowedActions"`
}
type legacySource struct {
	NodeInstanceID string `json:"nodeInstanceId"`
	Branch         string `json:"branch"`
}
type legacyBinding struct {
	graph.Binding
	Sources []legacySource `json:"sources,omitempty"`
}
type legacyNode struct {
	NodeInstanceID string                   `json:"nodeInstanceId"`
	NodeType       string                   `json:"nodeType"`
	NodeVersion    string                   `json:"nodeVersion"`
	Config         json.RawMessage          `json:"config"`
	InputBindings  map[string]legacyBinding `json:"inputBindings,omitempty"`
	Position       *graph.Position          `json:"position"`
}
type legacyGraph struct {
	SchemaVersion int               `json:"schemaVersion"`
	EntryPoints   map[string]string `json:"entryPoints,omitempty"`
	Nodes         []legacyNode      `json:"nodes"`
	Edges         []graph.Edge      `json:"edges"`
}
type RequiredSecret struct{ NodeID, Field string }

type Converted struct {
	RequiredSecrets []RequiredSecret
	Graph           graph.Graph
	NodeIDs         map[string]string
	SecretFields    map[string]map[string]string
	Dependencies    []string
}
type Converter struct {
	Catalog     map[string]sdk.NodeDescriptor
	NodePlugins map[string]string
	Mappings    Mappings
	Validate    func(json.RawMessage) error
}

func decode(raw []byte, value any) error {
	if len(raw) == 0 || len(raw) > 8<<20 {
		return errors.New("invalid migration document size")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return errors.New("invalid migration document shape")
	}
	if !errors.Is(d.Decode(new(any)), io.EOF) {
		return errors.New("unexpected trailing migration data")
	}
	return nil
}
func Digest(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		panic("migration facts must be valid JSON before hashing")
	}
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}
func (c Converter) Convert(workflowID int64, raw json.RawMessage) (Converted, error) {
	result, err := c.convert(fmt.Sprint(workflowID), raw, false, "")
	if err != nil {
		return result, err
	}
	if c.Validate != nil {
		data, _ := json.Marshal(result.Graph)
		if err = c.Validate(data); err != nil {
			return result, errors.New("converted graph failed current schema or plugin validation")
		}
	}
	return result, nil
}
func (c Converter) convert(location string, raw json.RawMessage, body bool, prefix string) (Converted, error) {
	var old legacyGraph
	if err := decode(raw, &old); err != nil {
		return Converted{}, err
	}
	if old.SchemaVersion != 1 && old.SchemaVersion != 2 {
		return Converted{}, errors.New("source graph must be schema version 1 or 2")
	}
	result := Converted{Graph: graph.Graph{SchemaVersion: 3, EntryPoints: map[string]string{}, Nodes: []graph.Node{}, Edges: append([]graph.Edge(nil), old.Edges...)}, NodeIDs: map[string]string{}, SecretFields: map[string]map[string]string{}}
	oldNodes := map[string]legacyNode{}
	deps := map[string]bool{}
	for _, node := range old.Nodes {
		if oldNodes[node.NodeInstanceID].NodeInstanceID != "" {
			return result, errors.New("duplicate source node")
		}
		oldNodes[node.NodeInstanceID] = node
		result.NodeIDs[prefix+node.NodeInstanceID] = prefix + node.NodeInstanceID
	}
	trigger := ""
	for _, node := range old.Nodes {
		key := node.NodeType + "@" + node.NodeVersion
		mapping, mapped := c.Mappings.NodeVersions[key]
		if !strings.HasPrefix(node.NodeType, "core.") && !strings.HasPrefix(node.NodeType, "official.") && !mapped {
			return result, fmt.Errorf("external node %s requires an explicit version mapping", key)
		}
		next := graph.Node{NodeInstanceID: node.NodeInstanceID, NodeType: node.NodeType, NodeVersion: node.NodeVersion, Config: node.Config, Position: node.Position, InputBindings: map[string]graph.Binding{}}
		if mapped {
			next.NodeType, next.NodeVersion = mapping.NodeType, mapping.NodeVersion
		}
		desc, ok := c.Catalog[next.NodeType]
		if !ok || desc.Version != next.NodeVersion {
			return result, fmt.Errorf("node %s needs a compiled target descriptor or explicit version mapping", node.NodeInstanceID)
		}
		if desc.Kind == sdk.NodeKindTrigger {
			if trigger != "" {
				return result, errors.New("multiple automatic triggers need an explicit graph redesign")
			}
			trigger = node.NodeInstanceID
		}
		if plugin := c.NodePlugins[next.NodeType]; plugin != "" {
			deps[plugin] = true
		}
		if next.Position == nil {
			return result, errors.New("source node position is missing")
		}
		var config map[string]json.RawMessage
		if json.Unmarshal(next.Config, &config) != nil {
			return result, errors.New("source config must be an object")
		}
		if mapped {
			for from, to := range mapping.Fields {
				if value, exists := config[from]; exists {
					if to == "" {
						return result, errors.New("config fields cannot be silently discarded")
					}
					if _, conflict := config[to]; conflict && from != to {
						return result, errors.New("config field mapping conflicts")
					}
					delete(config, from)
					config[to] = value
				}
			}
		}
		var schema struct {
			Properties map[string]map[string]any `json:"properties"`
			Required   []string                  `json:"required"`
		}
		_ = json.Unmarshal(desc.ConfigSchema, &schema)
		fields := map[string]string{}
		for field, property := range schema.Properties {
			if property["x-coinsphere-secret"] == true {
				if _, inline := config[field]; inline {
					return result, errors.New("inline credentials require server-side extraction before planning")
				}
				fields[field] = field
				for _, required := range schema.Required {
					if required == field {
						result.RequiredSecrets = append(result.RequiredSecrets, RequiredSecret{prefix + node.NodeInstanceID, field})
					}
				}
			}
		}
		if mapped {
			for from, to := range mapping.Fields {
				if _, secret := fields[to]; secret {
					fields[from] = to
					if from != to {
						delete(fields, to)
					}
				}
			}
		}
		result.SecretFields[prefix+node.NodeInstanceID] = fields
		if next.NodeType == "core.loop" {
			var loop struct {
				MaxIterations  int             `json:"maxIterations"`
				TimeoutSeconds int             `json:"timeoutSeconds"`
				ExitCondition  string          `json:"exitCondition"`
				Body           json.RawMessage `json:"body"`
			}
			if err := decode(next.Config, &loop); err != nil {
				return result, err
			}
			child, err := c.convert(location+"/"+node.NodeInstanceID+"/body", loop.Body, true, prefix+node.NodeInstanceID+".")
			if err != nil {
				return result, err
			}
			for from, to := range child.NodeIDs {
				result.NodeIDs[from] = to
				result.SecretFields[from] = child.SecretFields[from]
			}
			result.RequiredSecrets = append(result.RequiredSecrets, child.RequiredSecrets...)
			for _, id := range child.Dependencies {
				deps[id] = true
			}
			exitSources := c.legacySources(child.Graph.Nodes, "", nil)
			exitSources["iteration"] = graph.Binding{Kind: "input", FieldPath: []string{"iteration"}}
			exitSources["value"] = graph.Binding{Kind: "input", FieldPath: []string{"value"}}
			loop.ExitCondition, err = c.expression(location+"/"+node.NodeInstanceID+"/exit", loop.ExitCondition, exitSources)
			if err != nil {
				return result, err
			}
			loop.Body, _ = json.Marshal(child.Graph)
			next.Config, _ = json.Marshal(loop)
		} else {
			next.Config, _ = json.Marshal(config)
		}
		for field, binding := range node.InputBindings {
			next.InputBindings[field] = binding.Binding
		}
		result.Graph.Nodes = append(result.Graph.Nodes, next)
	}
	if !body {
		if trigger == "" {
			return result, errors.New("source workflow has no automatic trigger")
		}
		if old.SchemaVersion == 1 {
			result.Graph.EntryPoints["main"] = trigger
		} else {
			for name, id := range old.EntryPoints {
				if name == "realtime" {
					name = "main"
				}
				if existing := result.Graph.EntryPoints[name]; existing != "" && existing != id {
					return result, errors.New("entry point mapping conflicts")
				}
				result.Graph.EntryPoints[name] = id
			}
			if result.Graph.EntryPoints["main"] != trigger {
				return result, errors.New("source primary entry does not identify its trigger")
			}
		}
	}
	for index := range result.Graph.Edges {
		edge := &result.Graph.Edges[index]
		if edge.Condition != "" {
			sources := c.legacySources(result.Graph.Nodes, edge.SourceNodeInstanceID, nil)
			var err error
			edge.Condition, err = c.expression(location+"/edge/"+edge.EdgeID, edge.Condition, sources)
			if err != nil {
				return result, err
			}
		}
		// Old Core gated every outgoing edge on an optional ready Boolean.
		if hasOutputField(c.Catalog[nodeType(result.Graph.Nodes, edge.SourceNodeInstanceID)], "ready") {
			node := "nodes[" + quoted(edge.SourceNodeInstanceID) + "]"
			ready := "(!has(" + node + ".ready) || type(" + node + ".ready) != bool || " + node + ".ready)"
			if edge.Condition == "" {
				edge.Condition = ready
			} else {
				edge.Condition = "(" + ready + ") && (" + edge.Condition + ")"
			}
		}
	}
	for index := range result.Graph.Nodes {
		node := &result.Graph.Nodes[index]
		oldNode := oldNodes[node.NodeInstanceID]
		for field, binding := range oldNode.InputBindings {
			if binding.Kind == "cel" {
				sources := c.legacySources(result.Graph.Nodes, "", func(id string) bool { return upstream(result.Graph.Edges, id, node.NodeInstanceID) })
				expression, err := c.expression(location+"/"+node.NodeInstanceID+"/binding/"+field, binding.Expression, sources)
				if err != nil {
					return result, err
				}
				node.InputBindings[field] = graph.Binding{Kind: "cel", Expression: expression}
			}
			if binding.Kind == "condition_entry" {
				parts := []string{}
				for _, source := range binding.Sources {
					parts = append(parts, "incoming.exists(e, e.nodeInstanceId == "+quoted(source.NodeInstanceID)+" && e.sourcePort == "+quoted(source.Branch)+") && nodes["+quoted(source.NodeInstanceID)+"].entered")
				}
				expression := "false"
				if len(parts) > 0 {
					expression = "(" + strings.Join(parts, ") || (") + ")"
				}
				if mapped := c.Mappings.Expressions[location+"/"+node.NodeInstanceID+"/binding/"+field].Expression; mapped != "" {
					var err error
					expression, err = c.expression(location+"/"+node.NodeInstanceID+"/binding/"+field, expression, nil)
					if err != nil {
						return result, err
					}
				}
				node.InputBindings[field] = graph.Binding{Kind: "cel", Expression: expression}
			}
		}
	}
	// A composition node receives the original edges, so its Incoming preserves
	// actual branch selection; the delivery receives ordinary typed fields.
	for _, oldNode := range old.Nodes {
		subject, message := []legacySource{}, []legacySource{}
		special := false
		for _, b := range oldNode.InputBindings {
			if b.Kind == "condition_subject" {
				subject = b.Sources
				special = true
			}
			if b.Kind == "condition_message" {
				message = b.Sources
				special = true
			}
		}
		if !special {
			continue
		}
		for field, binding := range oldNode.InputBindings {
			if binding.Kind == "condition_entry" || binding.Kind == "cel" && strings.Contains(binding.Expression, "incoming") {
				mapped := c.Mappings.Expressions[location+"/"+oldNode.NodeInstanceID+"/binding/"+field].Expression
				if mapped == "" || strings.Contains(mapped, "incoming") {
					return result, fmt.Errorf("message composition binding at %s/%s/binding/%s requires an explicit expression mapping independent of incoming", location, oldNode.NodeInstanceID, field)
				}
			}
		}
		desc, ok := c.Catalog["official.notification.compose"]
		if !ok {
			return result, errors.New("message composition requires the notification plugin")
		}
		id := "compose_" + Digest(location + "/" + oldNode.NodeInstanceID)[:16]
		if oldNodes[id].NodeInstanceID != "" {
			return result, errors.New("generated composition ID conflicts")
		}
		config, _ := json.Marshal(map[string]any{"subjectSources": subject, "messageSources": message})
		result.Graph.Nodes = append(result.Graph.Nodes, graph.Node{NodeInstanceID: id, NodeType: desc.Type, NodeVersion: desc.Version, Config: config, InputBindings: map[string]graph.Binding{}, Position: &graph.Position{X: oldNode.Position.X - 260, Y: oldNode.Position.Y}})
		for i := range result.Graph.Edges {
			if result.Graph.Edges[i].TargetNodeInstanceID == oldNode.NodeInstanceID {
				result.Graph.Edges[i].TargetNodeInstanceID = id
			}
		}
		result.Graph.Edges = append(result.Graph.Edges, graph.Edge{EdgeID: id + "_out", SourceNodeInstanceID: id, SourcePort: "out", TargetNodeInstanceID: oldNode.NodeInstanceID, TargetPort: "in"})
		for i := range result.Graph.Nodes {
			n := &result.Graph.Nodes[i]
			if n.NodeInstanceID != oldNode.NodeInstanceID {
				continue
			}
			for field, b := range oldNode.InputBindings {
				if b.Kind == "condition_subject" || b.Kind == "condition_message" {
					path := "message"
					if b.Kind == "condition_subject" {
						path = "subjectKey"
					}
					n.InputBindings[field] = graph.Binding{Kind: "field", NodeInstanceID: id, FieldPath: []string{path}}
				}
			}
		}
		deps["official.notification"] = true
	}
	for id := range deps {
		result.Dependencies = append(result.Dependencies, id)
	}
	sort.Strings(result.Dependencies)
	return result, nil
}
func (c Converter) expression(location, expression string, sources map[string]graph.Binding) (string, error) {
	mapping := c.Mappings.Expressions[location]
	if mapping.Expression != "" {
		if _, err := graph.Compile(mapping.Expression); err != nil {
			return "", errors.New("invalid explicit expression mapping")
		}
		return mapping.Expression, nil
	}
	for field, binding := range mapping.Sources {
		sources[field] = binding
	}
	value, err := graph.RewriteLegacyInput(expression, sources)
	if err != nil {
		return "", fmt.Errorf("expression at %s requires an explicit source or expression mapping", location)
	}
	return value, nil
}
func (c Converter) legacySources(nodes []graph.Node, source string, allowed func(string) bool) map[string]graph.Binding {
	result := map[string]graph.Binding{}
	counts := map[string]int{}
	open := []map[string]any{}
	for _, node := range nodes {
		if source != "" && node.NodeInstanceID != source {
			continue
		}
		var schema struct {
			Properties           map[string]any `json:"properties"`
			AdditionalProperties any            `json:"additionalProperties"`
		}
		_ = json.Unmarshal(c.Catalog[node.NodeType].OutputSchema, &schema)
		if schema.AdditionalProperties != false {
			open = append(open, schema.Properties)
		}
		for field := range schema.Properties {
			counts[field]++
			if allowed == nil || allowed(node.NodeInstanceID) {
				result[field] = graph.Binding{Kind: "field", NodeInstanceID: node.NodeInstanceID, FieldPath: []string{field}}
			}
		}
	}
	for field, count := range counts {
		for _, properties := range open {
			if _, declared := properties[field]; !declared {
				count++
			}
		}
		if count != 1 {
			delete(result, field)
		}
	}
	return result
}
func hasOutputField(desc sdk.NodeDescriptor, field string) bool {
	var schema struct {
		Properties map[string]any `json:"properties"`
	}
	_ = json.Unmarshal(desc.OutputSchema, &schema)
	_, ok := schema.Properties[field]
	return ok
}
func nodeType(nodes []graph.Node, id string) string {
	for _, node := range nodes {
		if node.NodeInstanceID == id {
			return node.NodeType
		}
	}
	return ""
}
func quoted(value string) string { raw, _ := json.Marshal(value); return string(raw) }
func upstream(edges []graph.Edge, source, target string) bool {
	seen := map[string]bool{}
	queue := []string{source}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		for _, edge := range edges {
			if edge.SourceNodeInstanceID == id {
				if edge.TargetNodeInstanceID == target {
					return true
				}
				queue = append(queue, edge.TargetNodeInstanceID)
			}
		}
	}
	return false
}
