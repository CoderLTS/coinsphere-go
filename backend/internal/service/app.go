// Package service contains the V2 baseline application services.
package service

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"coinsphere/backend/internal/config"
	"coinsphere/backend/internal/security"
	"coinsphere/backend/plugin/sdk"
	"gorm.io/gorm"
)

var ErrPermission = errors.New("permission denied")

type App struct {
	DB      *gorm.DB
	Cfg     *config.AppConfig
	Hasher  *security.PasswordHasher
	Tokens  *security.TokenManager
	Plugins *sdk.Registry

	authStateMu         sync.Mutex
	reauthTokens        map[string]reauthTokenRecord
	revokedAccessTokens map[string]time.Time
	dummyHash           string
}

func NewApp(gdb *gorm.DB, cfg *config.AppConfig, plugins *sdk.Registry) *App {
	hasher := security.NewPasswordHasher(cfg.Auth.PasswordIterations)
	return &App{
		DB:                  gdb,
		Cfg:                 cfg,
		Hasher:              hasher,
		Tokens:              security.NewTokenManager(cfg.Auth.SecretKey, cfg.Auth.AccessTokenTTLMinutes),
		Plugins:             plugins,
		dummyHash:           hasher.HashPassword(security.RandomToken()),
		reauthTokens:        map[string]reauthTokenRecord{},
		revokedAccessTokens: map[string]time.Time{},
	}
}

type M = map[string]any

func fmtTimeV(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format("2006-01-02 15:04:05")
}

func bizErr(format string, args ...any) error { return fmt.Errorf(format, args...) }
