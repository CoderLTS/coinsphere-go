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
	registeredPages := a.Plugins.Pages()
	result := make([]M, 0, len(plugins))
	for _, plugin := range plugins {
		nodes := make([]M, 0)
		for _, node := range a.Plugins.PluginNodes(plugin.ID) {
			nodes = append(nodes, M{
				"type": node.Type, "title": node.Title,
				"version": node.Version, "kind": node.Kind, "configSchema": node.ConfigSchema,
			})
		}
		pages := make([]M, 0)
		for _, page := range registeredPages {
			if page.PluginID == plugin.ID {
				pages = append(pages, M{"pageKey": page.PageKey, "title": page.Title, "kind": "page"})
			}
		}
		for _, page := range a.Plugins.PluginResultPages(plugin.ID) {
			pages = append(pages, M{"pageKey": page.PageKey, "title": page.Title, "kind": "resultPage"})
		}
		result = append(result, M{
			"id": plugin.ID, "name": plugin.Name, "version": plugin.Version,
			"contributes": plugin.Contributes, "status": "loaded", "nodes": nodes, "pages": pages,
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
