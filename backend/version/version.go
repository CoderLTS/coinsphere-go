// Package version owns the core versions and the single official plugin catalog.
package version

import (
	"embed"
	"encoding/json"
)

const (
	Core     = "4.0.0"
	SDKMajor = 4
)

//go:embed builtin.json
var catalog embed.FS

type BuiltinPlugin struct {
	ID              string                             `json:"id"`
	Name            string                             `json:"name"`
	Version         string                             `json:"version"`
	Contributes     []string                           `json:"contributes"`
	RequiresPlugins map[string]string                  `json:"requiresPlugins"`
	Menu            struct{ Mode, Title, Icon string } `json:"menu"`
}

var BuiltinCatalog []BuiltinPlugin
var BuiltinPlugins = map[string]string{}
var BuiltinPluginDependencies = map[string]map[string]string{}

func init() {
	raw, err := catalog.ReadFile("builtin.json")
	if err != nil {
		panic(err)
	}
	if err = json.Unmarshal(raw, &BuiltinCatalog); err != nil {
		panic(err)
	}
	for _, p := range BuiltinCatalog {
		BuiltinPlugins[p.ID] = p.Version
		BuiltinPluginDependencies[p.ID] = p.RequiresPlugins
	}
}
