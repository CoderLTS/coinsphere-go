package pluginlifecycle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallUpgradeAndUninstall(t *testing.T) {
	backend, layout := writeRepository(t, "module coinsphere/backend\n\ngo 1.26.6\n")
	installer := New(Options{Layout: layout, CoreVersion: "2.0.0", SDKMajor: 1})
	first := writePlugin(t, "official.test", "1.0.0")

	if _, err := installer.Install(context.Background(), first, false); err != nil {
		t.Fatalf("Install: %v", err)
	}
	assertFileContains(t, filepath.Join(backend, "go.mod"), "replace example.com/official-test => ./plugin/installed/official_test/backend")
	assertFileContains(t, filepath.Join(backend, "internal", "pluginregistry", "registry.generated.go"), `ID: "official.test"`)
	assertFileContains(t, filepath.Join(layout.FrontendRoot, "src", "plugins", "registry.generated.ts"), `./installed/official_test/ui/components/index.ts`)
	if _, err := os.Stat(filepath.Join(layout.FrontendRoot, "src", "plugins", "installed", "official_test", "ui", "components", "index.ts")); err != nil {
		t.Fatalf("frontend entry was not copied at its manifest path: %v", err)
	}
	if _, err := installer.Install(context.Background(), first, false); err == nil || !strings.Contains(err.Error(), "already installed") {
		t.Fatalf("duplicate install error = %v", err)
	}
	if _, err := installer.Install(context.Background(), writePlugin(t, "official.test", "2.0.0"), true); err == nil || !strings.Contains(err.Error(), "major upgrade") {
		t.Fatalf("major upgrade error = %v", err)
	}

	if _, err := installer.Install(context.Background(), writePlugin(t, "official.test", "1.1.0"), true); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	assertFileContains(t, filepath.Join(backend, "plugin", "installed", "official_test", "coinsphere-plugin.json"), `"version":"1.1.0"`)
	if _, err := os.Stat(filepath.Join(backend, "plugin", "installed", "official_test.old")); !os.IsNotExist(err) {
		t.Fatalf("upgrade backup still exists: %v", err)
	}
	if err := installer.Uninstall(context.Background(), "official.test"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(backend, "plugin", "installed", "official_test")); !os.IsNotExist(err) {
		t.Fatalf("backend plugin still exists: %v", err)
	}
	assertFileContains(t, filepath.Join(backend, "internal", "pluginregistry", "registry.generated.go"), "func RegisterAll(registry *sdk.Registry) error")
}

func TestInstallRestoresSourceInputsAfterBuildInputFailure(t *testing.T) {
	backend, layout := writeRepository(t, "module coinsphere/backend\n\ngo 1.26.6\n")
	if err := os.WriteFile(filepath.Join(backend, "go.mod"), []byte("not a go.mod"), 0o600); err != nil {
		t.Fatal(err)
	}
	backendRegistry := filepath.Join(backend, "internal", "pluginregistry", "registry.generated.go")
	before, err := os.ReadFile(backendRegistry)
	if err != nil {
		t.Fatal(err)
	}
	installer := New(Options{Layout: layout, CoreVersion: "2.0.0", SDKMajor: 1})
	if _, err := installer.Install(context.Background(), writePlugin(t, "official.test", "1.0.0"), false); err == nil {
		t.Fatal("install with invalid repository go.mod succeeded")
	}
	after, err := os.ReadFile(backendRegistry)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("backend registry was not restored")
	}
	if _, err := os.Stat(filepath.Join(backend, "plugin", "installed", "official_test")); !os.IsNotExist(err) {
		t.Fatalf("failed install left plugin source behind: %v", err)
	}
}

func TestInstallRestoresSourceInputsAfterRebuildFailure(t *testing.T) {
	backend, layout := writeRepository(t, "module coinsphere/backend\n\ngo 1.26.6\n")
	installer := New(Options{
		Layout: layout, CoreVersion: "2.0.0", SDKMajor: 1,
		Rebuild: func(context.Context, Layout) error { return errors.New("build failed") },
	})
	if _, err := installer.Install(context.Background(), writePlugin(t, "official.test", "1.0.0"), false); err == nil || !strings.Contains(err.Error(), "build failed") {
		t.Fatalf("Install error = %v", err)
	}
	assertFileContains(t, filepath.Join(backend, "internal", "pluginregistry", "registry.generated.go"), "original backend registry")
	assertFileContains(t, filepath.Join(backend, "go.sum"), "original checksum")
	if _, err := os.Stat(filepath.Join(backend, "plugin", "installed", "official_test")); !os.IsNotExist(err) {
		t.Fatalf("failed rebuild left plugin source behind: %v", err)
	}
}

func TestValidateRejectsDuplicateMigrationVersions(t *testing.T) {
	root := writePlugin(t, "official.test", "1.0.0")
	for _, name := range []string{"00001_first.sql", "00001_second.sql"} {
		if err := os.WriteFile(filepath.Join(root, "migrations", name), []byte("-- +goose Up\nSELECT 1;\n-- +goose Down\nSELECT 1;\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	installer := New(Options{CoreVersion: "2.0.0", SDKMajor: 1})
	if _, err := installer.Validate(root); err == nil || !strings.Contains(err.Error(), "duplicate plugin migration version") {
		t.Fatalf("Validate error = %v", err)
	}
}

func TestUpgradePreservesInstalledMigrations(t *testing.T) {
	_, layout := writeRepository(t, "module coinsphere/backend\n\ngo 1.26.6\n")
	installer := New(Options{Layout: layout, CoreVersion: "2.0.0", SDKMajor: 1})
	first := writePlugin(t, "official.test", "1.0.0")
	writeMigration(t, first, "00001_init.sql", "CREATE TABLE records (id BIGINT PRIMARY KEY);")
	if _, err := installer.Install(context.Background(), first, false); err != nil {
		t.Fatalf("Install: %v", err)
	}
	changed := writePlugin(t, "official.test", "1.1.0")
	writeMigration(t, changed, "00001_init.sql", "CREATE TABLE changed (id BIGINT PRIMARY KEY);")
	if _, err := installer.Install(context.Background(), changed, true); err == nil || !strings.Contains(err.Error(), "preserve migration") {
		t.Fatalf("Upgrade error = %v", err)
	}
}

func writeRepository(t *testing.T, goMod string) (string, Layout) {
	t.Helper()
	root := t.TempDir()
	backend := filepath.Join(root, "backend")
	frontend := filepath.Join(root, "frontend")
	for _, directory := range []string{
		filepath.Join(backend, "internal", "pluginregistry"),
		filepath.Join(frontend, "src", "plugins"),
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(backend, "go.mod"), []byte(goMod), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backend, "go.sum"), []byte("original checksum\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(frontend, "package.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backend, "internal", "pluginregistry", "registry.generated.go"), []byte("// original backend registry\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(frontend, "src", "plugins", "registry.generated.ts"), []byte("// original frontend registry\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	layout, err := NewLayout(backend)
	if err != nil {
		t.Fatal(err)
	}
	return backend, layout
}

func writePlugin(t *testing.T, id, pluginVersion string) string {
	t.Helper()
	root := t.TempDir()
	for _, directory := range []string{"backend", "ui/components", "migrations"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(directory)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	module := "example.com/" + strings.ReplaceAll(id, ".", "-")
	if err := os.WriteFile(filepath.Join(root, "backend", "go.mod"), []byte("module "+module+"\n\ngo 1.26.6\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "backend", "register.go"), []byte("package plugin\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ui", "components", "index.ts"), []byte("export default {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeMigration(t, root, "00001_init.sql", "SELECT 1;")
	manifest := fmt.Sprintf(`{"schemaVersion":1,"id":%q,"name":"Test Plugin","version":%q,"sdkMajor":1,"requiresCore":">=2.0.0 <3.0.0","backend":{"module":%q,"package":"./backend"},"frontend":{"entry":"./ui/components/index.ts"},"migrations":{"directory":"./migrations"},"contributes":["nodes"]}`, id, pluginVersion, module)
	if err := os.WriteFile(filepath.Join(root, "coinsphere-plugin.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func assertFileContains(t *testing.T, path, expected string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), expected) {
		t.Fatalf("%s does not contain %q:\n%s", path, expected, content)
	}
}

func writeMigration(t *testing.T, root, name, up string) {
	t.Helper()
	content := "-- +goose Up\n" + up + "\n-- +goose Down\nSELECT 1;\n"
	if err := os.WriteFile(filepath.Join(root, "migrations", name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
