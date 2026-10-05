package official_test

import (
	"context"
	"testing"

	"coinsphere/backend/internal/config"
	"coinsphere/backend/internal/service"
	"coinsphere/backend/internal/testdb"
	"coinsphere/backend/plugin/official"
	"coinsphere/backend/plugin/sdk"
	"coinsphere/backend/version"
)

func TestOfficialTemplatesAndGeneralSystemWithoutTradingPlugins(t *testing.T) {
	_, database := testdb.Open(t, true)
	for _, financial := range []bool{false, true} {
		registry := sdk.NewRegistry()
		app := service.NewApp(database, &config.AppConfig{Auth: config.AuthConfig{SecretKey: "synthetic-template-key", PasswordIterations: 1}}, registry)
		host := sdk.Host{Inbox: app, Stores: sdk.GormPluginStores{Database: database}, Network: official.NetworkClientFactory{}, OutboundProxy: app, Realtime: app, Events: app}
		enabled := map[string]bool{}
		for _, p := range version.BuiltinCatalog {
			enabled[p.ID] = financial || p.ID != "official.quant" && p.ID != "official.binance"
		}
		if err := official.RegisterAll(registry, host, enabled); err != nil {
			t.Fatal(err)
		}
		if err := app.SyncCapabilities(context.Background()); err != nil {
			t.Fatal(err)
		}
		for _, template := range registry.Templates() {
			if err := app.ValidateWorkflowGraph(template.Graph); err != nil {
				t.Fatalf("%s: %v", template.Key, err)
			}
		}
		if !financial {
			for _, node := range app.WorkflowNodeCatalog() {
				if registry.NodePlugin(node.Type) == "official.quant" || registry.NodePlugin(node.Type) == "official.binance" {
					t.Fatal("financial nodes leaked into the general platform")
				}
			}
		}
	}
	registry := sdk.NewRegistry()
	host := sdk.Host{Stores: sdk.GormPluginStores{Database: database}, Network: official.NetworkClientFactory{}}
	if err := official.RegisterAll(registry, host, map[string]bool{"official.qq": true}); err == nil {
		t.Fatal("missing required plugin was hidden")
	}
}
