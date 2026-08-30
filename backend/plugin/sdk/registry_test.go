package sdk

import "testing"

func TestPluginContributionsByOwner(t *testing.T) {
	registry := NewRegistry()
	registry.nodes["official.one.action"] = registeredNode{
		pluginID: "official.one",
		desc:     NodeDescriptor{Type: "official.one.action"},
	}
	registry.nodes["official.two.action"] = registeredNode{
		pluginID: "official.two",
		desc:     NodeDescriptor{Type: "official.two.action"},
	}
	registry.resultPages["official.one/result"] = ResultPageDescriptor{PageKey: "result"}
	registry.resultPages["official.two/result"] = ResultPageDescriptor{PageKey: "result"}

	if nodes := registry.PluginNodes("official.one"); len(nodes) != 1 || nodes[0].Type != "official.one.action" {
		t.Fatalf("unexpected plugin nodes: %#v", nodes)
	}
	if pages := registry.PluginResultPages("official.one"); len(pages) != 1 || pages[0].PageKey != "result" {
		t.Fatalf("unexpected plugin result pages: %#v", pages)
	}
}
