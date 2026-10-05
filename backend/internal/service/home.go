package service

import (
	"coinsphere/backend/internal/pluginregistry"
	"context"
	"sort"
	"time"

	"coinsphere/backend/version"
)

func (a *App) GetHomeMeta() M {
	return M{
		"service": "coinsphere", "version": version.Core,
		"sdkMajor": version.SDKMajor, "pluginCount": len(a.Plugins.Plugins()),
	}
}

func (a *App) ListInstalledPlugins() ([]M, error) {
	var rows []struct{ PluginID, Version, Status string }
	if err := a.DB.Table("plugin_installations").Order("plugin_id").Find(&rows).Error; err != nil {
		return nil, err
	}
	compiled := map[string]string{}
	for id, v := range version.BuiltinPlugins {
		compiled[id] = v
	}
	for _, p := range pluginregistry.CompiledPlugins {
		compiled[p.ID] = p.Version
	}
	loaded := map[string]bool{}
	for _, p := range a.Plugins.Plugins() {
		loaded[p.ID] = true
	}
	ids := map[string]bool{}
	installations := map[string]struct{ PluginID, Version, Status string }{}
	for _, row := range rows {
		ids[row.PluginID] = true
		installations[row.PluginID] = row
	}
	for id := range compiled {
		ids[id] = true
	}
	sorted := []string{}
	for id := range ids {
		sorted = append(sorted, id)
	}
	sort.Strings(sorted)
	result := []M{}
	for _, id := range sorted {
		row := installations[id]
		status := "unavailable"
		reason := "not_installed"
		if row.Status == "installed" {
			reason = "not_compiled"
			if compiled[id] != "" {
				reason = "not_loaded"
			}
		}
		if loaded[id] {
			status = "loaded"
			reason = ""
		}
		result = append(result, M{"id": id, "version": row.Version, "installed": row.Status == "installed", "compiled": compiled[id] != "", "compiledVersion": compiled[id], "loaded": loaded[id], "status": status, "reason": reason})
	}
	return result, nil
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
FROM schema_migrations_g4
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
