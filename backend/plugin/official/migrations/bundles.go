// Package migrations assembles the storage baselines owned by the official plugins.
package migrations

import (
	binance "coinsphere/backend/plugin/official/binance/migrations"
	notification "coinsphere/backend/plugin/official/notification/migrations"
	quant "coinsphere/backend/plugin/official/quant/migrations"
	"io/fs"
)

type Bundle struct {
	ID, Schema string
	Files      fs.FS
}

func Bundles() []Bundle {
	b, _ := fs.Sub(binance.Files, "sql")
	n, _ := fs.Sub(notification.Files, "sql")
	q, _ := fs.Sub(quant.Files, "sql")
	return []Bundle{{"official.binance", "plugin_binance", b}, {"official.notification", "plugin_notification", n}, {"official.quant", "plugin_quant", q}}
}
