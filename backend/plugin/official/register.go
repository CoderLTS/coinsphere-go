// Package official provides CoinSphere's built-in plugins through the public SDK registry.
package official

import (
	"context"

	"coinsphere/backend/plugin/official/ai"
	"coinsphere/backend/plugin/official/connector"
	"coinsphere/backend/plugin/official/internal/safehttp"
	"coinsphere/backend/plugin/official/notification"
	"coinsphere/backend/plugin/official/qq"
	"coinsphere/backend/plugin/official/quant"
	"coinsphere/backend/plugin/sdk"
	"gorm.io/gorm"
)

func RegisterAll(registry *sdk.Registry, allowedHosts []string) error {
	client, err := safehttp.New(allowedHosts)
	if err != nil {
		return err
	}
	if err := connector.Register(registry, client); err != nil {
		return err
	}
	return ai.Register(registry, client)
}

func RegisterQuant(registry *sdk.Registry, database *gorm.DB, resolveProxy func(context.Context, int64) (string, error)) error {
	return quant.Register(registry, database, resolveProxy)
}

func RegisterNotification(registry *sdk.Registry, database *gorm.DB, publish func(context.Context, int64, int64)) error {
	return notification.Register(registry, database, publish)
}

func RegisterQQ(registry *sdk.Registry, database *gorm.DB) error {
	return qq.Register(registry, database)
}
