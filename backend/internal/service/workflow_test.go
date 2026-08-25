package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"coinsphere/backend/plugin/official"
	"coinsphere/backend/plugin/sdk"
	cloudevents "github.com/cloudevents/sdk-go/v2"
)

type workflowTestAction struct {
	mu       sync.Mutex
	attempts map[string]int
}

func (a *workflowTestAction) Execute(ctx context.Context, request sdk.ActionRequest) (sdk.ActionResult, error) {
	var input map[string]any
	if json.Unmarshal(request.Input, &input) != nil {
		return sdk.ActionResult{}, errors.New("invalid input")
	}
	label, _ := input["label"].(string)
	if label == "wait" {
		<-ctx.Done()
		return sdk.ActionResult{}, ctx.Err()
	}
	if label == "interrupt" {
		a.mu.Lock()
		if a.attempts == nil {
			a.attempts = map[string]int{}
		}
		a.attempts[request.OperationKey]++
		attempt := a.attempts[request.OperationKey]
		a.mu.Unlock()
		if attempt == 1 {
			<-ctx.Done()
			return sdk.ActionResult{}, ctx.Err()
		}
	}
	if label == "retry" {
		a.mu.Lock()
		if a.attempts == nil {
			a.attempts = map[string]int{}
		}
		a.attempts[request.OperationKey]++
		attempt := a.attempts[request.OperationKey]
		a.mu.Unlock()
		if attempt == 1 {
			return sdk.ActionResult{}, errors.New("retryable test failure")
		}
	}
	if label == "invalid" {
		if err := request.State.Save(ctx, json.RawMessage(`{"saved":true}`)); err != nil {
			return sdk.ActionResult{}, err
		}
		return sdk.ActionResult{Output: json.RawMessage(`{"invalid":true}`)}, nil
	}
	if _, err := request.Secrets.Read(ctx, "token"); err != nil {
		return sdk.ActionResult{}, err
	}
	var artifacts []sdk.Artifact
	if label == "artifact" {
		artifact, err := request.Artifacts.Put(ctx, "application/json", strings.NewReader(`{"rows":[1,2,3]}`))
		if err != nil {
			return sdk.ActionResult{}, err
		}
		artifacts = append(artifacts, artifact)
	}
	return sdk.ActionResult{Output: append(json.RawMessage(nil), request.Input...), Artifacts: artifacts}, nil
}

const testPluginWorkflowGraph = `{
  "schemaVersion":1,
  "nodes":[
    {"nodeInstanceId":"manual","nodeType":"core.manual","nodeVersion":"1.0.0","config":{},"position":{"x":80,"y":120}},
    {"nodeInstanceId":"transform","nodeType":"official.test.transform","nodeVersion":"1.0.0","config":{},"inputBindings":{"price":{"kind":"literal","value":"1.25"},"label":{"kind":"cel","expression":"event.type + ':'"}},"position":{"x":360,"y":120}},
    {"nodeInstanceId":"end","nodeType":"core.end","nodeVersion":"1.0.0","config":{},"inputBindings":{"result":{"kind":"field","nodeInstanceId":"transform","fieldPath":["label"]}},"position":{"x":640,"y":120}}
  ],
  "edges":[
    {"edgeId":"manual-transform","sourceNodeInstanceId":"manual","sourcePort":"out","targetNodeInstanceId":"transform","targetPort":"in"},
    {"edgeId":"transform-end","sourceNodeInstanceId":"transform","sourcePort":"out","targetNodeInstanceId":"end","targetPort":"in","condition":"event.type == 'manual'"}
  ]
}`

const testLoopWorkflowGraph = `{
  "schemaVersion":1,
  "nodes":[
    {"nodeInstanceId":"manual","nodeType":"core.manual","nodeVersion":"1.0.0","config":{},"position":{"x":80,"y":120}},
    {"nodeInstanceId":"loop","nodeType":"core.loop","nodeVersion":"1.0.0","config":{"maxIterations":3,"timeoutSeconds":60,"exitCondition":"input.iteration == 2","body":{"schemaVersion":1,"nodes":[
      {"nodeInstanceId":"item","nodeType":"core.loop_item","nodeVersion":"1.0.0","config":{},"position":{"x":80,"y":80}},
      {"nodeInstanceId":"constant","nodeType":"core.constant","nodeVersion":"1.0.0","config":{"value":"tick"},"position":{"x":280,"y":80}},
      {"nodeInstanceId":"done","nodeType":"core.loop_end","nodeVersion":"1.0.0","config":{},"position":{"x":480,"y":80}}
    ],"edges":[
      {"edgeId":"item-constant","sourceNodeInstanceId":"item","sourcePort":"out","targetNodeInstanceId":"constant","targetPort":"in"},
      {"edgeId":"constant-done","sourceNodeInstanceId":"constant","sourcePort":"out","targetNodeInstanceId":"done","targetPort":"in"}
    ]}},"position":{"x":360,"y":120}},
    {"nodeInstanceId":"end","nodeType":"core.end","nodeVersion":"1.0.0","config":{},"position":{"x":640,"y":120}}
  ],
  "edges":[
    {"edgeId":"manual-loop","sourceNodeInstanceId":"manual","sourcePort":"out","targetNodeInstanceId":"loop","targetPort":"in"},
    {"edgeId":"loop-end","sourceNodeInstanceId":"loop","sourcePort":"out","targetNodeInstanceId":"end","targetPort":"in"}
  ]
}`

func TestWorkflowGraphValidationAndLifecycle(t *testing.T) {
	app := workflowTestApp(t)
	graph, err := app.validateWorkflowGraph(json.RawMessage(blankWorkflowGraph))
	if err != nil || graph.mainTriggerID != "manual-trigger" || !json.Valid([]byte(graph.nodeVersionsJSON)) {
		t.Fatalf("blank graph = %#v, err = %v", graph, err)
	}
	if graph, err := app.validateWorkflowGraph(json.RawMessage(scheduledWorkflowGraph)); err != nil || graph.mainTriggerID != "schedule-trigger" {
		t.Fatalf("scheduled graph = %#v, err = %v", graph, err)
	}
	if value, err := evaluateWorkflowCEL("event.type + ':'", map[string]string{"type": "manual"}, map[string]any{}); err != nil || value != "manual:" {
		t.Fatalf("runtime CEL value = %#v, err = %v", value, err)
	}

	validated, err := app.validateWorkflowGraph(json.RawMessage(testPluginWorkflowGraph))
	if err != nil || !validated.requiredSecrets[workflowSecretKey{"transform", "token"}] {
		t.Fatalf("plugin graph required secrets = %#v, err = %v", validated.requiredSecrets, err)
	}
	secret := "test-only-secret"
	if _, err := validateWorkflowSecretChanges(validated, []WorkflowSecretChange{{NodeInstanceID: "transform", Field: "token", Value: &secret}}); err != nil {
		t.Fatalf("valid secret change: %v", err)
	}
	loop, err := app.validateWorkflowGraph(json.RawMessage(testLoopWorkflowGraph))
	if err != nil || loop.nodeTypes["loop.item"] != "core.loop_item" || loop.nodeTypes["loop.done"] != "core.loop_end" {
		t.Fatalf("loop graph node types = %#v, err = %v", loop.nodeTypes, err)
	}
	ordinaryArithmeticCondition := strings.Replace(testPluginWorkflowGraph, `event.type == 'manual'`, `event.type + ':' == 'manual:'`, 1)
	if _, err := app.validateWorkflowGraph(json.RawMessage(ordinaryArithmeticCondition)); err != nil {
		t.Fatalf("ordinary CEL arithmetic was rejected: %v", err)
	}

	for name, raw := range map[string]string{
		"malformed":             `{`,
		"unknown node":          strings.Replace(blankWorkflowGraph, "core.end", "official.unknown", 1),
		"duplicate trigger":     strings.Replace(blankWorkflowGraph, "core.end", "core.manual", 1),
		"unknown field":         strings.Replace(blankWorkflowGraph, `"schemaVersion": 1,`, `"schemaVersion": 1,"unknown":true,`, 1),
		"core config":           strings.Replace(blankWorkflowGraph, `"config":{}`, `"config":{"unknown":true}`, 1),
		"unsupported version":   strings.Replace(blankWorkflowGraph, `"nodeVersion":"1.0.0"`, `"nodeVersion":"2.0.0"`, 1),
		"invalid port":          strings.Replace(blankWorkflowGraph, `"sourcePort":"out"`, `"sourcePort":"missing"`, 1),
		"unreachable":           strings.Replace(blankWorkflowGraph, `"nodes": [`, `"nodes": [{"nodeInstanceId":"orphan","nodeType":"core.constant","nodeVersion":"1.0.0","config":{"value":"x"},"position":{"x":0,"y":0}},`, 1),
		"bad condition":         strings.Replace(blankWorkflowGraph, `"targetPort":"in"`, `"targetPort":"in","condition":"1"`, 1),
		"secret in config":      strings.Replace(testPluginWorkflowGraph, `"config":{}`, `"config":{"token":"leak"}`, 1),
		"bad field path":        strings.Replace(testPluginWorkflowGraph, `"fieldPath":["label"]`, `"fieldPath":["missing"]`, 1),
		"decimal arithmetic":    strings.Replace(testPluginWorkflowGraph, `"kind":"literal","value":"1.25"`, `"kind":"cel","expression":"'1' + '2'"`, 1),
		"decimal edge math":     strings.Replace(testPluginWorkflowGraph, `event.type == 'manual'`, `input.price + '1' == '2'`, 1),
		"decimal index math":    strings.Replace(testPluginWorkflowGraph, `event.type == 'manual'`, `input['price'] + '1' == '2'`, 1),
		"decimal wrapped math":  strings.Replace(testPluginWorkflowGraph, `event.type == 'manual'`, `string(input.price) + '1' == '2'`, 1),
		"decimal dynamic math":  strings.Replace(testPluginWorkflowGraph, `event.type == 'manual'`, `input[event.type] + '1' == '2'`, 1),
		"loop non-boolean exit": strings.Replace(testLoopWorkflowGraph, `input.iteration == 2`, `input.iteration`, 1),
		"loop missing end":      strings.Replace(testLoopWorkflowGraph, `"nodeType":"core.loop_end"`, `"nodeType":"core.constant"`, 1),
		"loop item at top":      strings.Replace(blankWorkflowGraph, `"nodeType":"core.end"`, `"nodeType":"core.loop_item"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := app.validateWorkflowGraph(json.RawMessage(raw)); err == nil {
				t.Fatal("invalid graph was accepted")
			}
		})
	}

	status := WorkflowStatusPaused
	for _, action := range []string{"start", "pause", "archive"} {
		status, err = nextWorkflowStatus(status, action)
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
	}
	if status != WorkflowStatusArchived {
		t.Fatalf("final status = %q", status)
	}
	if _, err := nextWorkflowStatus(status, "start"); !errors.Is(err, ErrConflict) {
		t.Fatalf("archived start error = %v", err)
	}
	if status, err := nextWorkflowStatus(WorkflowStatusAttention, "pause"); err != nil || status != WorkflowStatusPaused {
		t.Fatalf("resolved attention status = %q, err = %v", status, err)
	}

	event := cloudevents.NewEvent()
	event.SetID("event-1")
	event.SetSource("urn:test")
	event.SetType("test.event")
	event.SetTime(time.Date(2026, 1, 1, 8, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60)))
	event.SetExtension("partitionkey", "partition-1")
	if err := event.SetData(cloudevents.ApplicationJSON, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := validateWorkflowCloudEvent(event); err == nil {
		t.Fatal("non-UTC CloudEvent time was accepted")
	}
}

func workflowTestApp(t *testing.T) *App {
	t.Helper()
	registry := sdk.NewRegistry()
	if err := official.RegisterAll(registry, nil); err != nil {
		t.Fatal(err)
	}
	err := registry.RegisterPlugin(sdk.PluginDescriptor{
		ID: "official.test", Name: "Test", Version: "1.0.0", Contributes: []string{"nodes"},
	}, func(registrar sdk.Registrar) error {
		return registrar.Action(sdk.NodeDescriptor{
			Type: "official.test.transform", Version: "1.0.0", Kind: sdk.NodeKindAction,
			ConfigSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"token":{"type":"string","x-coinsphere-secret":true}},"required":["token"],"additionalProperties":false}`),
			UISchema:     json.RawMessage(`{}`),
			InputSchema:  json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"price":{"type":"string","x-coinsphere-decimal":true},"label":{"type":"string"}},"required":["price","label"],"additionalProperties":false}`),
			OutputSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"price":{"type":"string","x-coinsphere-decimal":true},"label":{"type":"string"}},"required":["price","label"],"additionalProperties":false}`),
			Pool:         sdk.PoolCompute, SideEffect: sdk.SideEffectNone, State: sdk.StatePersistent,
		}, &workflowTestAction{})
	})
	if err != nil {
		t.Fatal(err)
	}
	return &App{
		Plugins: registry, batchCancels: map[int64]context.CancelFunc{},
		streamSlots: make(chan struct{}, 4), computeSlots: make(chan struct{}, 1),
	}
}
