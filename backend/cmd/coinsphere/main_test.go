package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunValidatesAndSortsPlugins(t *testing.T) {
	zeta := writePlugin(t, "official.zeta", "1.0.0", ">=2.0.0 <3.0.0")
	alpha := writePlugin(t, "official.alpha", "2.0.0", ">=2.0.0 <3.0.0")
	var output bytes.Buffer
	if err := run([]string{"plugin", "validate", zeta, alpha}, &output); err != nil {
		t.Fatalf("run: %v", err)
	}
	if output.String() != "valid plugin official.alpha@2.0.0\nvalid plugin official.zeta@1.0.0\n" {
		t.Fatalf("output = %q", output.String())
	}
}

func TestRunRejectsInvalidCommandsAndPlugins(t *testing.T) {
	duplicateA := writePlugin(t, "official.same", "1.0.0", ">=2.0.0 <3.0.0")
	duplicateB := writePlugin(t, "official.same", "1.1.0", ">=2.0.0 <3.0.0")
	incompatible := writePlugin(t, "official.future", "1.0.0", ">=3.0.0")
	for name, args := range map[string][]string{
		"unknown command":   {"plugin", "install", duplicateA},
		"missing directory": {"plugin", "validate"},
		"duplicate id":      {"plugin", "validate", duplicateA, duplicateB},
		"incompatible core": {"plugin", "validate", incompatible},
	} {
		t.Run(name, func(t *testing.T) {
			if err := run(args, &bytes.Buffer{}); err == nil {
				t.Fatal("invalid command succeeded")
			}
		})
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
	module := "example.com/" + strings.ReplaceAll(id, ".", "-")
	if err := os.WriteFile(filepath.Join(root, "backend", "go.mod"), []byte("module "+module+"\n\ngo 1.26.6\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "frontend", "index.ts"), []byte("export default {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf(`{
  "schemaVersion": 1,
  "id": %q,
  "name": "Test Plugin",
  "version": %q,
  "sdkMajor": 1,
  "requiresCore": %q,
  "backend": {"module": %q, "package": "./backend"},
  "frontend": {"entry": "./frontend/index.ts"},
  "migrations": {"directory": "./migrations"},
  "contributes": ["nodes"]
}`, id, pluginVersion, constraint, module)
	if err := os.WriteFile(filepath.Join(root, "coinsphere-plugin.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}
