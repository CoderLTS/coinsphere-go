// Package official provides CoinSphere's built-in plugins through the public SDK registry.
package official

import (
	"encoding/json"

	"coinsphere/backend/plugin/sdk"
)

const (
	connectorPluginID = "official.connector"
	aiPluginID        = "official.ai"
)

var emptyObjectSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false}`)
var dynamicObjectSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`)

func RegisterAll(registry *sdk.Registry, allowedHosts []string) error {
	client, err := newSafeHTTPClient(allowedHosts)
	if err != nil {
		return err
	}
	if err := registry.RegisterPlugin(sdk.PluginDescriptor{
		ID: connectorPluginID, Name: "连接器", Version: "1.0.0",
		Contributes: []string{"nodes", "triggers", "resultPages"},
	}, func(registrar sdk.Registrar) error { return registerConnector(registrar, client) }); err != nil {
		return err
	}
	return registry.RegisterPlugin(sdk.PluginDescriptor{
		ID: aiPluginID, Name: "人工智能", Version: "1.0.0",
		Contributes: []string{"nodes", "resultPages"},
	}, func(registrar sdk.Registrar) error { return registerAI(registrar, client) })
}
