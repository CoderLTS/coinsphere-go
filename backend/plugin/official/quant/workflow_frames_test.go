package quant

import (
	"context"
	"encoding/json"
	"testing"

	"coinsphere/backend/internal/testdb"
	"coinsphere/backend/plugin/sdk"
	"coinsphere/backend/workflow/graph"
)

func TestFrameAndRealtimeUseSameDecimalAndUTCSemantics(t *testing.T) {
	_, database := testdb.Open(t, true)
	runtime := &quantRuntime{db: database, frameActions: map[string]sdk.ActionHandler{"official.quant.position": quantPositionAction{}, "official.quant.output_signal": quantOutputSignalAction{}}, frameDescriptors: map[string]sdk.NodeDescriptor{
		"official.quant.position":      {Version: "1.0.0", InputSchema: quantPositionInputSchema, OutputSchema: quantPositionOutputSchema},
		"official.quant.output_signal": {Version: "1.0.0", InputSchema: quantOutputSignalInputSchema, OutputSchema: quantOutputSignalOutputSchema, Branches: []string{"realtime", "unchanged"}},
	}}
	runtime.frameActions["official.quant.output_signal"] = quantOutputSignalAction{runtime}
	series := json.RawMessage(`{"venue":"binance","market":"spot","instrument":"BTCUSDT","interval":"1h"}`)
	target := "0.123456789012345678"
	definition := graph.Graph{SchemaVersion: 3, Nodes: []graph.Node{
		{NodeInstanceID: "history", NodeType: "official.quant.backtest_start", NodeVersion: "1.0.0", Config: series},
		{NodeInstanceID: "position", NodeType: "official.quant.position", NodeVersion: "1.0.0", Config: json.RawMessage(`{"market":"spot","targetMode":"input","fixedTarget":"0","decimalField":"target"}`), InputBindings: map[string]graph.Binding{"target": {Kind: "literal", Value: target}, "evaluatedAt": {Kind: "field", NodeInstanceID: "history", FieldPath: []string{"evaluatedAt"}}}},
		{NodeInstanceID: "out", NodeType: "official.quant.output_signal", NodeVersion: "1.0.0", Config: series},
	}, Edges: []graph.Edge{{EdgeID: "each", SourceNodeInstanceID: "history", SourcePort: "each", TargetNodeInstanceID: "position", TargetPort: "in"}, {EdgeID: "result", SourceNodeInstanceID: "position", SourcePort: "out", TargetNodeInstanceID: "out", TargetPort: "in"}}}
	raw, _ := json.Marshal(definition)
	request := sdk.ActionRequest{GraphSnapshot: raw, NodeInstanceID: "history", Revision: sdk.RevisionRef{WorkflowID: "7", RevisionID: "11"}, OperationKey: "synthetic-frame", Input: json.RawMessage(`{}`)}
	frames, err := runtime.compileWorkflowFrames(request)
	if err != nil {
		t.Fatal(err)
	}
	source := json.RawMessage(`{"branch":"each","evaluatedAt":"2026-01-01T00:00:00Z"}`)
	frame, err := frames.ExecuteFrame(context.Background(), quantFrameRequest{SourcePort: "each", SourceOutput: source, Context: json.RawMessage(`{"previousTargetPosition":"0"}`)})
	if err != nil || len(frame.Results) != 1 {
		t.Fatal("frame failed", err)
	}
	position, err := (quantPositionAction{}).Execute(context.Background(), sdk.ActionRequest{NodeInstanceID: "position", Config: definition.Nodes[1].Config, Input: json.RawMessage(`{"target":"0.123456789012345678","evaluatedAt":"2026-01-01T00:00:00Z"}`)})
	if err != nil {
		t.Fatal(err)
	}
	realtime, err := (quantOutputSignalAction{runtime}).Execute(context.Background(), sdk.ActionRequest{NodeInstanceID: "out", Revision: request.Revision, OperationKey: "synthetic-realtime", Config: series, Incoming: []sdk.NodeOutput{{NodeInstanceID: "position", SourcePort: "out", Output: position.Output}}})
	if err != nil {
		t.Fatal(err)
	}
	var a, b map[string]any
	_ = json.Unmarshal(frame.Results[0], &a)
	_ = json.Unmarshal(realtime.Output, &b)
	for _, key := range []string{"targetPosition", "previousTargetPosition", "action", "evaluatedAt", "branch", "target"} {
		if a[key] != b[key] {
			t.Fatalf("frame/realtime differ in %s", key)
		}
	}
	if a["targetPosition"] != target || a["evaluatedAt"] != "2026-01-01T00:00:00Z" || a["signalId"] != float64(0) || b["signalId"].(float64) <= 0 {
		t.Fatal("Decimal/UTC output or effect boundary changed")
	}
	var count int64
	if err := database.Model(&quantSignal{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatal("frame emitted a persistent signal", err)
	}
	if _, err := parseQuantUTCTime("2026-01-01T08:00:00+08:00"); err == nil {
		t.Fatal("non-UTC financial time was accepted")
	}
}
