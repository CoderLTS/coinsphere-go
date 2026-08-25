package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
		"reserved core node": func(registrar Registrar) error {
			return registrar.Action(testNode("core.invalid"), testAction{})
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

func TestRegistryRejectsUnsafeResultPageEntry(t *testing.T) {
	for _, entry := range []string{"../Result.vue", "C:/Result.vue"} {
		registry := NewRegistry()
		err := registry.RegisterPlugin(PluginDescriptor{
			ID: "official.test", Name: "Test", Version: "1.0.0", Contributes: []string{"resultPages"},
		}, func(registrar Registrar) error {
			return registrar.ResultPage(ResultPageDescriptor{
				PageKey: "result", Title: "Result", ComponentEntry: entry, ScopeSchema: objectSchema,
			})
		})
		if err == nil {
			t.Fatalf("unsafe result component entry %q was accepted", entry)
		}
	}
}

func TestRegistryExposesRegisteredActionRouteAndResultPage(t *testing.T) {
	registry := NewRegistry()
	routeCalled := false
	err := registry.RegisterPlugin(PluginDescriptor{
		ID: "official.test", Name: "Test", Version: "1.0.0",
		Contributes: []string{"nodes", "apiRoutes", "resultPages"},
	}, func(registrar Registrar) error {
		if err := registrar.Action(testNode("official.test.action"), testAction{}); err != nil {
			return err
		}
		if err := registrar.Route(RouteDescriptor{Method: "get", Pattern: "/result", Scope: ScopeResult}, func(http.ResponseWriter, *http.Request, RouteScope) {
			routeCalled = true
		}); err != nil {
			return err
		}
		return registrar.ResultPage(ResultPageDescriptor{
			PageKey: "result", Title: "Result", ComponentEntry: "./Result.vue", ScopeSchema: objectSchema,
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := registry.Action("official.test.action"); !ok {
		t.Fatal("registered action is unavailable")
	}
	handler, ok := registry.Route("official.test", RouteDescriptor{Method: "GET", Pattern: "/result", Scope: ScopeResult})
	if !ok {
		t.Fatal("registered route is unavailable")
	}
	handler(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/result", nil), ResultScope{})
	if !routeCalled {
		t.Fatal("registered route handler was not called")
	}
	if _, ok := registry.ResultPage("official.test", "result"); !ok {
		t.Fatal("registered result page is unavailable")
	}
	nodes := registry.Nodes()
	if len(nodes) != 1 || nodes[0].Type != "official.test.action" {
		t.Fatalf("registered node catalog = %#v", nodes)
	}
	nodes[0].ConfigSchema[0] = '['
	if registry.Nodes()[0].ConfigSchema[0] == '[' {
		t.Fatal("node catalog leaked mutable schema storage")
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
