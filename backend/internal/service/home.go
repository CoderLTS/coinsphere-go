package service

import (
	"context"
	"time"
)

func (a *App) GetHomeMeta() M {
	return M{"service": "coinsphere", "version": "5.0.0"}
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
