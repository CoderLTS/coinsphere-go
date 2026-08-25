package pluginlifecycle

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"coinsphere/backend/internal/migration"
)

func TestPluginDataLifecycle(t *testing.T) {
	dsn := os.Getenv("COINSPHERE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("COINSPHERE_TEST_POSTGRES_DSN is not configured")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*config)
	defer admin.Close()
	suffix := fmt.Sprintf("%d_%d", os.Getpid(), time.Now().UnixNano())
	testSchema := "plugin_lifecycle_test_" + suffix
	if _, err := admin.Exec("CREATE SCHEMA " + pgx.Identifier{testSchema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if config.RuntimeParams == nil {
		config.RuntimeParams = make(map[string]string)
	}
	config.RuntimeParams["search_path"] = testSchema
	database := stdlib.OpenDB(*config)
	defer database.Close()
	pluginID := "official.lifecycle" + fmt.Sprintf("%d", time.Now().UnixNano())
	pluginSchema, err := migration.PluginSchemaName(pluginID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec("DROP SCHEMA " + pgx.Identifier{pluginSchema}.Sanitize() + " CASCADE")
		_, _ = admin.Exec("DROP SCHEMA " + pgx.Identifier{testSchema}.Sanitize() + " CASCADE")
	})
	core, err := migration.New(database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.Up(context.Background(), 0); err != nil {
		t.Fatal(err)
	}

	_, layout := writeRepository(t, "module coinsphere/backend\n\ngo 1.26.6\n")
	installer := New(Options{Layout: layout, DB: database, CoreVersion: "2.0.0", SDKMajor: 1})
	if _, err := installer.Install(context.Background(), writePlugin(t, pluginID, "1.0.0"), false); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO plugin_references (plugin_id, reference_type, reference_id) VALUES ($1, 'workflow', 'wf-1')`, pluginID); err != nil {
		t.Fatal(err)
	}
	if err := installer.Uninstall(context.Background(), pluginID); err == nil {
		t.Fatal("uninstall accepted an active reference")
	}
	if _, err := database.Exec(`UPDATE plugin_references SET active = FALSE WHERE plugin_id = $1`, pluginID); err != nil {
		t.Fatal(err)
	}
	if err := installer.Uninstall(context.Background(), pluginID); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	assertSchemaExists(t, database, pluginSchema, true)
	if _, err := database.Exec(`UPDATE plugin_references SET active = TRUE WHERE plugin_id = $1`, pluginID); err == nil {
		t.Fatal("database reactivated a reference to an uninstalled plugin")
	}
	if err := installer.PurgeData(context.Background(), pluginID, "PURGE wrong"); err == nil {
		t.Fatal("purge accepted the wrong confirmation")
	}
	if err := installer.PurgeData(context.Background(), pluginID, "PURGE "+pluginID); err == nil {
		t.Fatal("purge accepted a retained reference")
	}
	if _, err := database.Exec(`DELETE FROM plugin_references WHERE plugin_id = $1`, pluginID); err != nil {
		t.Fatal(err)
	}
	if err := installer.PurgeData(context.Background(), pluginID, "PURGE "+pluginID); err != nil {
		t.Fatalf("PurgeData: %v", err)
	}
	assertSchemaExists(t, database, pluginSchema, false)
	var installations int
	if err := database.QueryRow(`SELECT COUNT(*) FROM plugin_installations WHERE plugin_id = $1`, pluginID).Scan(&installations); err != nil || installations != 0 {
		t.Fatalf("plugin installation count=%d err=%v", installations, err)
	}
}

func assertSchemaExists(t *testing.T, database *sql.DB, schema string, expected bool) {
	t.Helper()
	var exists bool
	if err := database.QueryRow(`SELECT EXISTS (SELECT 1 FROM information_schema.schemata WHERE schema_name = $1)`, schema).Scan(&exists); err != nil || exists != expected {
		t.Fatalf("schema %s exists=%t want=%t err=%v", schema, exists, expected, err)
	}
}
