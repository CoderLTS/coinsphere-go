// Package service contains the V2 baseline application services.
package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"coinsphere/backend/internal/config"
	"coinsphere/backend/internal/security"
	"coinsphere/backend/plugin/sdk"
	"gorm.io/gorm"
)

var (
	ErrPermission = errors.New("permission denied")
	ErrNotFound   = errors.New("not found")
	ErrConflict   = errors.New("conflict")
)

type App struct {
	DB           *gorm.DB
	Cfg          *config.AppConfig
	Hasher       *security.PasswordHasher
	Tokens       *security.TokenManager
	Cipher       *security.SecretCipher
	Plugins      *sdk.Registry
	ArtifactRoot string

	authStateMu         sync.Mutex
	reauthTokens        map[string]reauthTokenRecord
	revokedAccessTokens map[string]time.Time
	dummyHash           string
	batchClaimMu        sync.Mutex
	batchCancelMu       sync.Mutex
	batchCancels        map[int64]context.CancelFunc
	batchWG             sync.WaitGroup
	streamSlots         chan struct{}
	computeSlots        chan struct{}
}

func NewApp(gdb *gorm.DB, cfg *config.AppConfig, plugins *sdk.Registry) *App {
	hasher := security.NewPasswordHasher(cfg.Auth.PasswordIterations)
	cipher, _ := security.NewSecretCipher(cfg.Auth.SecretKey)
	return &App{
		DB:                  gdb,
		Cfg:                 cfg,
		Hasher:              hasher,
		Tokens:              security.NewTokenManager(cfg.Auth.SecretKey, cfg.Auth.AccessTokenTTLMinutes),
		Cipher:              cipher,
		Plugins:             plugins,
		dummyHash:           hasher.HashPassword(security.RandomToken()),
		reauthTokens:        map[string]reauthTokenRecord{},
		revokedAccessTokens: map[string]time.Time{},
		batchCancels:        map[int64]context.CancelFunc{},
		streamSlots:         make(chan struct{}, 4),
		computeSlots:        make(chan struct{}, 1),
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
