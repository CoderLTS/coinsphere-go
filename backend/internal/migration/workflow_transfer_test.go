package migration

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeWorkflowGraphConvertsLegacyFieldAndEdgeCondition(t *testing.T) {
	legacy := json.RawMessage(`{
      "schemaVersion": 1,
      "profileRefs": [],
      "nodes": [
        {"nodeInstanceId":"trigger","nodeType":"core.event","nodeVersion":"1.0.0","config":{},"position":{"x":0,"y":0}},
        {"nodeInstanceId":"notify","nodeType":"core.end","nodeVersion":"1.0.0","config":{},"inputBindings":{"message":{"kind":"field","nodeInstanceId":"trigger","fieldPath":["message"]}},"position":{"x":200,"y":0}}
      ],
      "edges": [{"edgeId":"trigger-notify","sourceNodeInstanceId":"trigger","sourcePort":"out","targetNodeInstanceId":"notify","targetPort":"in","condition":"input.triggered == true"}]
    }`)
	converted, err := normalizeWorkflowGraph(legacy)
	if err != nil {
		t.Fatalf("normalize legacy graph: %v", err)
	}
	var graph map[string]any
	if err := json.Unmarshal(converted, &graph); err != nil {
		t.Fatalf("decode converted graph: %v", err)
	}
	if graph["schemaVersion"] != float64(3) {
		t.Fatalf("schemaVersion = %v, want 3", graph["schemaVersion"])
	}
	nodes := graph["nodes"].([]any)
	if len(nodes) != 3 {
		t.Fatalf("node count = %d, want 3", len(nodes))
	}
	bindings := nodes[1].(map[string]any)["inputBindings"].(map[string]any)
	if bindings["message"].(map[string]any)["kind"] != "node" {
		t.Fatalf("legacy field binding was not converted to node binding")
	}
	edges := graph["edges"].([]any)
	if len(edges) != 2 {
		t.Fatalf("edge count = %d, want 2", len(edges))
	}
	for _, raw := range edges {
		if _, exists := raw.(map[string]any)["condition"]; exists {
			t.Fatal("converted graph still contains an edge condition")
		}
	}
}

func TestNormalizeWorkflowGraphRejectsUnsupportedCEL(t *testing.T) {
	legacy := `{"schemaVersion":2,"nodes":[{"nodeInstanceId":"a","nodeType":"core.event","nodeVersion":"1.0.0","config":{},"position":{"x":0,"y":0}},{"nodeInstanceId":"b","nodeType":"core.end","nodeVersion":"1.0.0","config":{},"position":{"x":1,"y":1}}],"edges":[{"edgeId":"e","sourceNodeInstanceId":"a","sourcePort":"out","targetNodeInstanceId":"b","targetPort":"in","condition":"input.value.endsWith(\"x\")"}]}`
	_, err := normalizeWorkflowGraph(json.RawMessage(legacy))
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("error = %v, want unsupported condition error", err)
	}
}

func TestNormalizeWorkflowGraphRenamesLegacyConnectionReference(t *testing.T) {
	legacy := json.RawMessage(`{"schemaVersion":2,"nodes":[{"nodeInstanceId":"trigger","nodeType":"core.event","nodeVersion":"1.0.0","config":{},"position":{"x":0,"y":0}},{"nodeInstanceId":"notify","nodeType":"core.end","nodeVersion":"1.0.0","connectionId":"profile-1","connectionType":"smtp","connectionFields":["host"],"config":{},"position":{"x":1,"y":1}}],"edges":[{"edgeId":"e","sourceNodeInstanceId":"trigger","sourcePort":"out","targetNodeInstanceId":"notify","targetPort":"in"}]}`)
	converted, err := normalizeWorkflowGraph(legacy)
	if err != nil {
		t.Fatalf("normalize legacy graph: %v", err)
	}
	var graph map[string]any
	if err := json.Unmarshal(converted, &graph); err != nil {
		t.Fatalf("decode converted graph: %v", err)
	}
	node := graph["nodes"].([]any)[1].(map[string]any)
	if node["profileId"] != "profile-1" {
		t.Fatalf("profileId = %v, want profile-1", node["profileId"])
	}
	if _, ok := node["connectionId"]; ok {
		t.Fatal("legacy connectionId was not removed")
	}
}

func TestGraphNeedsCredentialsForExportedProfile(t *testing.T) {
	graph := json.RawMessage(`{"schemaVersion":3,"profileRefs":[],"nodes":[{"nodeInstanceId":"call","nodeType":"official.ai.model_call","nodeVersion":"1.0.0","profileId":"profile-1","config":{},"position":{"x":0,"y":0}}],"edges":[]}`)
	profiles := map[string]TransferProfile{"profile-1": {SourceID: "profile-1", SecretFields: json.RawMessage(`{"apiKey":true}`)}}
	if !graphNeedsCredentials(graph, profiles) {
		t.Fatal("graph with a secret-bearing profile should require credentials")
	}
}
