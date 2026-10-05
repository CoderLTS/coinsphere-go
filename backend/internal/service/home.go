package service

import (
	"coinsphere/backend/internal/pluginregistry"
	"coinsphere/backend/plugin/sdk"
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

func compiledPluginVersions() map[string]string {
	compiled := map[string]string{}
	for id, version := range version.BuiltinPlugins {
		compiled[id] = version
	}
	for _, plugin := range pluginregistry.CompiledPlugins {
		compiled[plugin.ID] = plugin.Version
	}
	return compiled
}

// Runtime registration is restricted to the exact installed build. Other installations
// remain visible in the administrative inventory, but cannot execute stale node versions.
func (a *App) EnabledCompiledPlugins(ctx context.Context) (map[string]bool, error) {
	var rows []struct{ PluginID, Version string }
	if err := a.DB.WithContext(ctx).Table("plugin_installations").Where("status = ?", "installed").Find(&rows).Error; err != nil {
		return nil, err
	}
	compiled, enabled := compiledPluginVersions(), map[string]bool{}
	for _, row := range rows {
		enabled[row.PluginID] = compiled[row.PluginID] != "" && compiled[row.PluginID] == row.Version
	}
	return enabled, nil
}

func (a *App) ListInstalledPlugins() ([]M, error) {
	var rows []struct{ PluginID, Version, Status string }
	if err := a.DB.Table("plugin_installations").Order("plugin_id").Find(&rows).Error; err != nil {
		return nil, err
	}
	compiled := compiledPluginVersions()
	metadata := map[string]sdk.PluginDescriptor{}
	for _, p := range version.BuiltinCatalog {
		metadata[p.ID] = sdk.PluginDescriptor{ID: p.ID, Name: p.Name, Contributes: p.Contributes}
	}
	for _, p := range pluginregistry.CompiledPlugins {
		compiled[p.ID] = p.Version
		metadata[p.ID] = p
	}
	loaded := map[string]bool{}
	for _, p := range a.Plugins.Plugins() {
		loaded[p.ID] = true
		metadata[p.ID] = p
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
				if compiled[id] != row.Version {
					reason = "version_mismatch"
				}
			}
		}
		if loaded[id] {
			status = "loaded"
			reason = ""
		}
		nodes, pages := []M{}, []M{}
		for _, node := range a.Plugins.PluginNodes(id) {
			nodes = append(nodes, M{"type": node.Type, "title": node.Title, "version": node.Version, "kind": node.Kind, "configSchema": node.ConfigSchema})
		}
		for _, page := range a.Plugins.Pages() {
			if page.PluginID == id {
				pages = append(pages, M{"pageKey": page.PageKey, "title": page.Title, "kind": "page"})
			}
		}
		for _, page := range a.Plugins.ResultPages(id) {
			pages = append(pages, M{"pageKey": page.PageKey, "title": page.Title, "kind": "resultPage"})
		}
		name := metadata[id].Name
		if name == "" {
			name = id
		}
		contributes := metadata[id].Contributes
		if contributes == nil {
			contributes = []string{}
		}
		result = append(result, M{"id": id, "name": name, "contributes": contributes, "nodes": nodes, "pages": pages, "version": row.Version, "installed": row.Status == "installed", "compiled": compiled[id] != "", "compiledVersion": compiled[id], "loaded": loaded[id], "status": status, "reason": reason})
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
