package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

var objectSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`)

type testAction struct{}

func (testAction) Execute(context.Context, ActionRequest) (ActionResult, error) {
	return ActionResult{}, nil
}

func TestRegistryRejectsCrossPluginNodeConflictWithoutPartialRegistration(t *testing.T) {
	registry := NewRegistry()
	if err := registry.RegisterPlugin(testPlugin("official.first"), func(registrar Registrar) error {
		return registrar.Action(testNode("shared.action"), testAction{})
	}); err != nil {
		t.Fatalf("register first plugin: %v", err)
	}
	err := registry.RegisterPlugin(testPlugin("official.second"), func(registrar Registrar) error {
		if err := registrar.Action(testNode("shared.action"), testAction{}); err != nil {
			return err
		}
		return nil
	})
	if err == nil {
		t.Fatal("duplicate node type was accepted")
	}
	plugins := registry.Plugins()
	if len(plugins) != 1 || plugins[0].ID != "official.first" {
		t.Fatalf("failed registration changed registry: %#v", plugins)
	}
}

func TestRegistryRejectsUndeclaredAndInvalidContributions(t *testing.T) {
	for name, register := range map[string]RegisterFunc{
		"undeclared route": func(registrar Registrar) error {
			return registrar.Route(RouteDescriptor{Method: "GET", Pattern: "/health", Scope: ScopeSystem}, func(http.ResponseWriter, *http.Request, RouteScope) {})
		},
		"invalid schema": func(registrar Registrar) error {
			desc := testNode("official.invalid")
			desc.ConfigSchema = json.RawMessage(`{"type":"object"}`)
			return registrar.Action(desc, testAction{})
		},
		"plugin error": func(Registrar) error { return errors.New("registration stopped") },
	} {
		t.Run(name, func(t *testing.T) {
			registry := NewRegistry()
			if err := registry.RegisterPlugin(testPlugin("official.test"), register); err == nil {
				t.Fatal("invalid registration was accepted")
			}
			if len(registry.Plugins()) != 0 {
				t.Fatal("failed registration changed registry")
			}
		})
	}
}

func testPlugin(id string) PluginDescriptor {
	return PluginDescriptor{ID: id, Name: "Test", Version: "1.0.0", Contributes: []string{"nodes"}}
}

func testNode(nodeType string) NodeDescriptor {
	return NodeDescriptor{
		Type: nodeType, Version: "1.0.0", Kind: NodeKindAction,
		ConfigSchema: objectSchema, UISchema: json.RawMessage(`{}`),
		InputSchema: objectSchema, OutputSchema: objectSchema,
		Pool: PoolStream, SideEffect: SideEffectNone, State: StateStateless,
	}
}
