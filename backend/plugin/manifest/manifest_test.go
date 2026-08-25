package manifest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadValidManifest(t *testing.T) {
	root := writePlugin(t, "official.test", "1.2.3", ">=2.0.0 <3.0.0")
	plugin, err := Load(root, "2.0.0", 1)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if plugin.Manifest.ID != "official.test" || !filepath.IsAbs(plugin.BackendPath) {
		t.Fatalf("plugin = %#v", plugin)
	}
}

func TestLoadRejectsIncompatibleAndEscapingManifest(t *testing.T) {
	for name, testCase := range map[string]struct {
		mutate    func(string)
		errorText string
	}{
		"core version": {
			func(root string) {
				rewriteManifest(t, root, strings.ReplaceAll(readManifest(t, root), ">=2.0.0 <3.0.0", ">=3.0.0"))
			},
			"does not include core",
		},
		"unknown field": {
			func(root string) {
				rewriteManifest(t, root, strings.Replace(readManifest(t, root), "{", `{"unknown":true,`, 1))
			},
			"unknown field",
		},
		"path escape": {
			func(root string) {
				rewriteManifest(t, root, strings.Replace(readManifest(t, root), `"./frontend/index.ts"`, `"../outside.ts"`, 1))
			},
			"path must name a child",
		},
		"windows absolute path": {
			func(root string) {
				rewriteManifest(t, root, strings.Replace(readManifest(t, root), `"./frontend/index.ts"`, `"C:/frontend/index.ts"`, 1))
			},
			"absolute paths",
		},
		"module mismatch": {
			func(root string) {
				rewriteManifest(t, root, strings.Replace(readManifest(t, root), `"coinsphere/plugin-test"`, `"coinsphere/wrong"`, 1))
			},
			"does not match",
		},
		"non-portable path": {
			func(root string) {
				rewriteManifest(t, root, strings.Replace(readManifest(t, root), `"./frontend/index.ts"`, `"frontend\\index.ts"`, 1))
			},
			"forward slashes",
		},
		"oversized": {
			func(root string) {
				rewriteManifest(t, root, readManifest(t, root)+strings.Repeat(" ", maxManifestBytes))
			},
			"exceeds",
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := writePlugin(t, "official.test", "1.2.3", ">=2.0.0 <3.0.0")
			testCase.mutate(root)
			if _, err := Load(root, "2.0.0", 1); err == nil || !strings.Contains(err.Error(), testCase.errorText) {
				t.Fatalf("Load error = %v, want %q", err, testCase.errorText)
			}
		})
	}
}

func TestLoadAllRejectsDuplicatePluginIDs(t *testing.T) {
	first := writePlugin(t, "official.same", "1.0.0", ">=2.0.0 <3.0.0")
	second := writePlugin(t, "official.same", "1.1.0", ">=2.0.0 <3.0.0")
	if _, err := LoadAll([]string{first, second}, "2.0.0", 1); err == nil || !strings.Contains(err.Error(), "duplicate plugin id") {
		t.Fatalf("LoadAll error = %v", err)
	}
}

func writePlugin(t *testing.T, id, pluginVersion, constraint string) string {
	t.Helper()
	root := t.TempDir()
	for _, directory := range []string{"backend", "frontend", "migrations"} {
		if err := os.Mkdir(filepath.Join(root, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "backend", "go.mod"), []byte("module coinsphere/plugin-test\n\ngo 1.26.6\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "frontend", "index.ts"), []byte("export default {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf(`{
  "schemaVersion": 1,
  "id": %q,
  "name": "Test Plugin",
  "version": %q,
  "sdkMajor": 1,
  "requiresCore": %q,
  "backend": {"module": "coinsphere/plugin-test", "package": "./backend"},
  "frontend": {"entry": "./frontend/index.ts"},
  "migrations": {"directory": "./migrations"},
  "contributes": ["nodes"]
}`, id, pluginVersion, constraint)
	rewriteManifest(t, root, manifest)
	return root
}

func readManifest(t *testing.T, root string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, FileName))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func rewriteManifest(t *testing.T, root, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, FileName), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
