package notification

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"coinsphere/backend/plugin/sdk"
	"coinsphere/backend/workflow/graph"
)

func TestComposePreservesConfiguredOrderTriggerDigestAndTextLimit(t *testing.T) {
	config := composeConfig{SubjectSources: []composeSource{{"b", "true"}, {"a", "true"}, {"c", "true"}}, MessageSources: []composeSource{{"b", "true"}, {"a", "true"}, {"c", "true"}}}
	raw, _ := json.Marshal(config)
	output := func(key, summary string, triggered bool) json.RawMessage {
		b, _ := json.Marshal(map[string]any{"businessKey": key, "summary": summary, "triggered": triggered})
		return b
	}
	r, err := (composeAction{}).Execute(context.Background(), sdk.ActionRequest{Config: raw, Incoming: []sdk.NodeOutput{{NodeInstanceID: "a", SourcePort: "true", Output: output("key-a", "A", true)}, {NodeInstanceID: "c", SourcePort: "true", Output: output("ignored", "ignored", false)}, {NodeInstanceID: "b", SourcePort: "false", Output: output("wrong-branch", "ignored", true)}, {NodeInstanceID: "b", SourcePort: "true", Output: output("key-b", strings.Repeat("文", 2100), true)}}})
	if err != nil {
		t.Fatal(err)
	}
	var got struct{ SubjectKey, Message string }
	if json.Unmarshal(r.Output, &got) != nil {
		t.Fatal("invalid compose output")
	}
	digest := sha256.Sum256([]byte("key-b\x00key-a"))
	if got.SubjectKey != "condition-subject:"+hex.EncodeToString(digest[:16]) || got.Message != strings.Repeat("文", 2000) {
		t.Fatal("source ordering, trigger selection or Unicode truncation changed")
	}
	rewritten, err := rewriteComposeSources(raw, map[string]string{"a": "loop.a", "b": "loop.b", "c": "loop.c"})
	if err != nil {
		t.Fatal(err)
	}
	var loop composeConfig
	if json.Unmarshal(rewritten, &loop) != nil || loop.SubjectSources[0].NodeInstanceID != "loop.b" {
		t.Fatal("Loop references were not expanded")
	}
	definition := graph.Graph{SchemaVersion: 3, Nodes: []graph.Node{{NodeInstanceID: "compose", NodeType: "official.notification.compose", Config: rewritten}}}
	for _, source := range loop.SubjectSources {
		definition.Edges = append(definition.Edges, graph.Edge{SourceNodeInstanceID: source.NodeInstanceID, SourcePort: source.Branch, TargetNodeInstanceID: "compose"})
	}
	g, _ := json.Marshal(definition)
	if err := validateComposeGraph(sdk.WorkflowValidationContext{Graph: g}); err != nil {
		t.Fatal(err)
	}
	definition.Edges = definition.Edges[1:]
	g, _ = json.Marshal(definition)
	if validateComposeGraph(sdk.WorkflowValidationContext{Graph: g}) == nil {
		t.Fatal("unknown composition source was accepted")
	}
}
