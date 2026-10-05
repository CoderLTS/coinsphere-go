// Package official provides CoinSphere's built-in plugins through the public SDK registry.
package official

import (
	"coinsphere/backend/plugin/contracts/trading"
	"coinsphere/backend/plugin/official/ai"
	"coinsphere/backend/plugin/official/binance"
	"coinsphere/backend/plugin/official/connector"
	"coinsphere/backend/plugin/official/notification"
	"coinsphere/backend/plugin/official/qq"
	"coinsphere/backend/plugin/official/quant"
	"coinsphere/backend/plugin/sdk"
	"coinsphere/backend/version"
	"fmt"
)

func RegisterAll(registry *sdk.Registry, host sdk.Host, enabled map[string]bool) error {
	financial := trading.NewRegistry()
	handlers := map[string]sdk.RegisterFunc{
		"official.ai": ai.Register, "official.connector": connector.Register, "official.notification": notification.Register, "official.qq": qq.Register,
		"official.quant":   func(r sdk.Registrar, h sdk.Host) error { return quant.Register(r, h, financial) },
		"official.binance": func(r sdk.Registrar, h sdk.Host) error { return binance.Register(r, h, financial) },
	}
	plugins := []struct {
		descriptor sdk.PluginDescriptor
		register   sdk.RegisterFunc
	}{}
	for _, item := range version.BuiltinCatalog {
		plugins = append(plugins, struct {
			descriptor sdk.PluginDescriptor
			register   sdk.RegisterFunc
		}{sdk.PluginDescriptor{ID: item.ID, Name: item.Name, Version: item.Version, Contributes: item.Contributes, RequiresPlugins: item.RequiresPlugins, Menu: sdk.PluginMenuDescriptor{Mode: item.Menu.Mode, Title: item.Menu.Title, Icon: item.Menu.Icon}}, handlers[item.ID]})
	}
	registered := make(map[string]bool, len(plugins))
	for pending := append([]struct {
		descriptor sdk.PluginDescriptor
		register   sdk.RegisterFunc
	}{}, plugins...); len(pending) > 0; {
		next := pending[:0]
		progress := false
		for _, plugin := range pending {
			plugin.descriptor.RequiresPlugins = version.BuiltinPluginDependencies[plugin.descriptor.ID]
			plugin.descriptor.Permissions = nil
			for _, capability := range []string{"read", "manage", "execute", "live_release"} {
				plugin.descriptor.Permissions = append(plugin.descriptor.Permissions, sdk.PermissionDescriptor{Code: "plugins." + plugin.descriptor.ID + "." + capability, Title: plugin.descriptor.Name + " " + capability, Protected: capability == "live_release"})
			}
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
		if !progress && len(next) > 0 {
			return fmt.Errorf("enabled plugins have missing or cyclic dependencies")
		}
		if !progress {
			break
		}
		pending = next
	}
	return nil
}
