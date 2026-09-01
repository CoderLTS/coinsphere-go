// Package version exposes compatibility versions shared by the app and plugin tooling.
package version

const (
	Core     = "3.0.0"
	SDKMajor = 3
)

var BuiltinPlugins = map[string]string{
	"official.ai":           Core,
	"official.binance":      Core,
	"official.connector":    Core,
	"official.notification": Core,
	"official.qq":           Core,
	"official.quant":        Core,
}

var BuiltinPluginDependencies = map[string]map[string]string{
	"official.binance": {"official.quant": "^3.0.0"},
}
