package pluginlifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"coinsphere/backend/internal/testdb"
	"coinsphere/backend/plugin/manifest"
	"coinsphere/backend/version"
)

func syntheticPackage(t *testing.T, v string) string {
	t.Helper()
	root := t.TempDir()
	m := manifest.Manifest{SchemaVersion: 1, ID: "example.business", Name: "Synthetic business", Version: v, SDKMajor: version.SDKMajor, RequiresCore: ">=4.0.0", Backend: manifest.Backend{Module: "example.test/business", Package: "backend"}, Frontend: manifest.Frontend{Entry: "frontend/index.ts"}, Migrations: manifest.Migrations{Directory: "migrations"}, Contributes: []string{"nodes", "migrations"}}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string]string{
		manifest.FileName:               string(raw),
		"backend/go.mod":                "module example.test/business\n\ngo 1.26.0\n",
		"backend/plugin.go":             "package business\n",
		"frontend/index.ts":             "export default { nodeEditors: {} }\n",
		"migrations/00001_business.sql": "-- +goose Up\nCREATE TABLE cases(id bigint PRIMARY KEY);\n-- +goose Down\nDROP TABLE cases;\n",
	} {
		target := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestPluginReferencesUpgradeAndFailedBuildRollback(t *testing.T) {
	database, _ := testdb.Open(t, true)
	root := t.TempDir()
	layout := Layout{BackendRoot: filepath.Join(root, "backend"), FrontendRoot: filepath.Join(root, "frontend")}
	for name, content := range map[string]string{layout.goModPath(): "module coinsphere/backend\n\ngo 1.26.0\n", layout.goSumPath(): ""} {
		if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	installer := New(Options{Layout: layout, DB: database})
	first, second := syntheticPackage(t, "1.0.0"), syntheticPackage(t, "2.0.0")
	if _, err := installer.Install(ctx, first, false); err != nil {
		t.Fatal(err)
	}
	if database.Stats().MaxOpenConnections == 1 {
		t.Fatal("migrations changed the shared pool capacity")
	}
	for _, kind := range []string{"revision", "run", "result_view"} {
		if _, err := database.ExecContext(ctx, "INSERT INTO plugin_references(plugin_id,reference_type,reference_id,active) VALUES('example.business',$1,'synthetic',true)", kind); err != nil {
			t.Fatal(err)
		}
		if _, err := installer.Install(ctx, second, true); err == nil {
			t.Fatal("upgrade ignored active " + kind)
		}
		if err := installer.Uninstall(ctx, "example.business"); err == nil {
			t.Fatal("uninstall ignored active " + kind)
		}
		if _, err := database.ExecContext(ctx, "DELETE FROM plugin_references WHERE plugin_id='example.business'"); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(second, "migrations/00002_failure.sql"), []byte("-- +goose Up\nALTER TABLE cases ADD COLUMN status text;\n-- +goose Down\nALTER TABLE cases DROP COLUMN status;\n"), 0644); err != nil {
		t.Fatal(err)
	}
	failed := New(Options{Layout: layout, DB: database, Rebuild: func(context.Context, Layout) error { return errors.New("synthetic build failure") }})
	if _, err := failed.Install(ctx, second, true); err == nil {
		t.Fatal("build failure did not fail upgrade")
	}
	installed, err := installer.Installed()
	if err != nil || len(installed) != 1 || installed[0].Manifest.Version != "1.0.0" {
		t.Fatal("source restore failed", err)
	}
	var v string
	if err := database.QueryRowContext(ctx, "SELECT version FROM plugin_installations WHERE plugin_id='example.business'").Scan(&v); err != nil || v != "1.0.0" {
		t.Fatal("installation version survived failed upgrade", err)
	}
	var added bool
	if err := database.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='plugin_example_business' AND table_name='cases' AND column_name='status')").Scan(&added); err != nil || added {
		t.Fatal("failed upgrade left a schema change", err)
	}
	if _, err := installer.Install(ctx, second, true); err != nil {
		t.Fatal(err)
	}
	var sourcePath string
	if err := database.QueryRowContext(ctx, "SELECT source_path FROM plugin_installations WHERE plugin_id='example.business'").Scan(&sourcePath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(layout.BackendRoot, "plugin", sourcePath)); err != nil {
		t.Fatal("installation recorded a removed staging path", err)
	}
	if err := installer.Uninstall(ctx, "example.business"); err != nil {
		t.Fatal(err)
	}
	var remains bool
	if err := database.QueryRowContext(ctx, "SELECT to_regclass('plugin_example_business.cases') IS NOT NULL").Scan(&remains); err != nil || !remains {
		t.Fatal("uninstall deleted owned data", err)
	}
}
