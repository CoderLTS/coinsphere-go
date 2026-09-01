// Package official provides CoinSphere's built-in plugins through the public SDK registry.
package official

import (
	"coinsphere/backend/plugin/official/ai"
	"coinsphere/backend/plugin/official/binance"
	"coinsphere/backend/plugin/official/connector"
	"coinsphere/backend/plugin/official/notification"
	"coinsphere/backend/plugin/official/qq"
	"coinsphere/backend/plugin/official/quant"
	"coinsphere/backend/plugin/sdk"
	"coinsphere/backend/version"
)

func RegisterAll(registry *sdk.Registry, host sdk.Host, enabled map[string]bool) error {
	plugins := []struct {
		descriptor sdk.PluginDescriptor
		register   sdk.RegisterFunc
	}{
		{sdk.PluginDescriptor{ID: "official.ai", Name: "人工智能", Version: "3.0.0", Contributes: []string{"nodes", "resultPages"}}, ai.Register},
		{sdk.PluginDescriptor{ID: "official.connector", Name: "连接器", Version: "3.0.0", Contributes: []string{"nodes", "triggers", "resultPages"}}, connector.Register},
		{sdk.PluginDescriptor{ID: "official.notification", Name: "通知", Version: "3.0.0", Contributes: []string{"nodes"}}, notification.Register},
		{sdk.PluginDescriptor{ID: "official.qq", Name: "QQ机器人", Version: "3.0.0", Contributes: []string{"nodes", "triggers"}}, qq.Register},
		{sdk.PluginDescriptor{ID: "official.quant", Name: "量化", Version: "3.0.0", Contributes: []string{"nodes", "strategies", "apiRoutes", "resultPages", "assistantQueries", "workflowValidators", "templates"}}, quant.Register},
		{sdk.PluginDescriptor{ID: "official.binance", Name: "Binance", Version: "3.0.0", RequiresPlugins: version.BuiltinPluginDependencies["official.binance"], Contributes: []string{"nodes", "triggers", "marketDataProviders", "executionProviders", "apiRoutes", "pages", "resultPages", "templates"}}, binance.Register},
	}
	registered := make(map[string]bool, len(plugins))
	for pending := append([]struct {
		descriptor sdk.PluginDescriptor
		register   sdk.RegisterFunc
	}{}, plugins...); len(pending) > 0; {
		next := pending[:0]
		progress := false
		for _, plugin := range pending {
			if !enabled[plugin.descriptor.ID] {
				continue
			}
			ready := true
			for requiredID := range plugin.descriptor.RequiresPlugins {
				if !registered[requiredID] {
					ready = false
					break
				}
			}
			if !ready {
				next = append(next, plugin)
				continue
			}
			if err := registry.RegisterPlugin(plugin.descriptor, host, plugin.register); err != nil {
				return err
			}
			registered[plugin.descriptor.ID] = true
			progress = true
		}
		if !progress {
			break
		}
		pending = next
	}
	return nil
}
