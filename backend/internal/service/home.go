package service

import (
	"context"
	"time"

	"coinsphere/backend/version"
)

func (a *App) GetHomeMeta() M {
	return M{
		"service": "coinsphere", "version": version.Core,
		"sdkMajor": version.SDKMajor, "pluginCount": len(a.Plugins.Plugins()),
	}
}

func (a *App) ListInstalledPlugins() []M {
	plugins := a.Plugins.Plugins()
	result := make([]M, 0, len(plugins))
	for _, plugin := range plugins {
		result = append(result, M{
			"id": plugin.ID, "name": plugin.Name, "version": plugin.Version,
			"contributes": plugin.Contributes, "status": "loaded",
		})
	}
	return result
}

func (a *App) GetHomeOverview(ctx context.Context) (M, error) {
	database := a.DB.WithContext(ctx)
	sqlDB, err := database.DB()
	if err != nil {
		return nil, err
	}
	databaseStatus := "healthy"
	pingCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		databaseStatus = "unavailable"
	}
	pool := sqlDB.Stats()
	var schemaVersion int64
	if err := database.Raw(`
SELECT version_id
FROM schema_migrations
WHERE is_applied = TRUE
ORDER BY id DESC
LIMIT 1
`).Scan(&schemaVersion).Error; err != nil {
		return nil, err
	}

	return M{"database": M{
		"status": databaseStatus, "maxOpenConnections": pool.MaxOpenConnections,
		"openConnections": pool.OpenConnections, "inUse": pool.InUse, "idle": pool.Idle,
		"waitCount": pool.WaitCount, "schemaVersion": schemaVersion,
	}}, nil
}
