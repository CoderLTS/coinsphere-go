package pluginbuild

import (
	"bytes"
	"strings"
	"testing"

	"coinsphere/backend/plugin/manifest"
)

func TestRenderRegistriesAreDeterministic(t *testing.T) {
	plugins := []manifest.Package{
		{Manifest: manifest.Manifest{ID: "official.zeta", Name: "Zeta", Version: "1.0.0", Backend: manifest.Backend{Module: "example.com/zeta"}, Frontend: manifest.Frontend{Entry: "./frontend/index.ts"}, Contributes: []string{"nodes"}}},
		{Manifest: manifest.Manifest{ID: "official.alpha", Name: "Alpha", Version: "2.0.0", Backend: manifest.Backend{Module: "example.com/alpha"}, Frontend: manifest.Frontend{Entry: "./frontend/main.ts"}, Contributes: []string{"resultPages", "nodes"}}},
	}
	backend, err := RenderBackend(plugins)
	if err != nil {
		t.Fatalf("RenderBackend: %v", err)
	}
	frontend, err := RenderFrontend(plugins)
	if err != nil {
		t.Fatalf("RenderFrontend: %v", err)
	}
	if bytes.Index(backend, []byte("official.alpha")) > bytes.Index(backend, []byte("official.zeta")) {
		t.Fatalf("backend registry is not sorted:\n%s", backend)
	}
	if bytes.Index(frontend, []byte("official.alpha")) > bytes.Index(frontend, []byte("official.zeta")) {
		t.Fatalf("frontend registry is not sorted:\n%s", frontend)
	}
	if !strings.Contains(string(frontend), `import("./installed/official_alpha/frontend/main.ts")`) {
		t.Fatalf("frontend registry has unexpected import path:\n%s", frontend)
	}
	if !strings.Contains(string(frontend), "Promise<FrontendPluginModule>") {
		t.Fatalf("frontend registry does not enforce the plugin module contract:\n%s", frontend)
	}
}

func TestRenderFrontendRejectsDirectoryCollision(t *testing.T) {
	plugins := []manifest.Package{
		{Manifest: manifest.Manifest{ID: "official.a-b", Frontend: manifest.Frontend{Entry: "./frontend/index.ts"}}},
		{Manifest: manifest.Manifest{ID: "official-a.b", Frontend: manifest.Frontend{Entry: "./frontend/index.ts"}}},
	}
	if _, err := RenderFrontend(plugins); err == nil {
		t.Fatal("frontend directory collision was accepted")
	}
}
