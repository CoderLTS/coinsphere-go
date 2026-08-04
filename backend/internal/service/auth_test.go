package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"coinsphere/backend/internal/config"
	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/security"
	"gorm.io/gorm"
)

type authRefreshResult struct {
	session *AuthSession
	err     error
}

func TestAuthRefreshRotationPostgres(t *testing.T) {
	database := openPostgresWorkflowContractDatabase(t)
	app := newAuthContractApp(t, database.primary)
	user := createAuthContractUser(t, app, "auth-rotation-user")

	session, err := app.Login(user.Username, "test-password")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	rotated, err := app.RefreshAccessToken(session.RefreshToken)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if rotated.AccessToken == "" || rotated.RefreshToken == "" ||
		rotated.RefreshToken == session.RefreshToken {
		t.Fatal("refresh did not issue a new session")
	}

	var oldRecord, newRecord db.RefreshTokenRecord
	if err := database.primary.First(&oldRecord, "id = ?", authTokenID(t, app, session.RefreshToken)).Error; err != nil {
		t.Fatalf("load old refresh record: %v", err)
	}
	if err := database.primary.First(&newRecord, "id = ?", authTokenID(t, app, rotated.RefreshToken)).Error; err != nil {
		t.Fatalf("load new refresh record: %v", err)
	}
	if !oldRecord.IsRevoked || newRecord.IsRevoked {
		t.Fatalf("rotation states = old:%v new:%v, want old revoked and new active",
			oldRecord.IsRevoked, newRecord.IsRevoked)
	}
}

func TestAuthConcurrentRefreshReuseRevokesSessionFamily(t *testing.T) {
	database := openPostgresWorkflowContractDatabase(t)
	primary := newAuthContractApp(t, database.primary)
	peerDB := database.openPeer(t)
	peer := newAuthContractApp(t, peerDB)
	user := createAuthContractUser(t, primary, "auth-concurrent-user")
	session, err := primary.Login(user.Username, "test-password")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	registerAuthQueryBarrier(t, database.primary, "auth-primary-query-barrier", arrived, release)
	registerAuthQueryBarrier(t, peerDB, "auth-peer-query-barrier", arrived, release)

	results := make(chan authRefreshResult, 2)
	start := make(chan struct{})
	for _, current := range []*App{primary, peer} {
		go func(app *App) {
			<-start
			next, refreshErr := app.RefreshAccessToken(session.RefreshToken)
			results <- authRefreshResult{session: next, err: refreshErr}
		}(current)
	}
	close(start)
	waitAuthSignal(t, arrived, "first refresh did not reach the record lock")
	waitAuthSignal(t, arrived, "second refresh did not reach the record lock")
	close(release)

	successes := 0
	invalid := 0
	for range 2 {
		result := waitAuthResult(t, results)
		switch {
		case result.err == nil && result.session != nil:
			successes++
		case errors.Is(result.err, security.ErrInvalidToken):
			invalid++
		default:
			t.Fatalf("unexpected concurrent refresh result: %v", result.err)
		}
	}
	if successes != 1 || invalid != 1 {
		t.Fatalf("concurrent refresh results = success:%d invalid:%d, want 1 and 1", successes, invalid)
	}

	var active int64
	if err := database.primary.Model(&db.RefreshTokenRecord{}).
		Where("user_id = ? AND is_revoked = ?", user.ID, false).Count(&active).Error; err != nil {
		t.Fatalf("count active refresh records: %v", err)
	}
	if active != 0 {
		t.Fatalf("active refresh records after reuse = %d, want 0", active)
	}
}

func newAuthContractApp(t *testing.T, database *gorm.DB) *App {
	t.Helper()
	cfg := &config.AppConfig{Auth: config.AuthConfig{
		SecretKey: "test-only-auth-secret", EncryptionKey: "test-only-encryption-secret",
		AccessTokenTTLMinutes: 15, RefreshTokenTTLDays: 7, PasswordIterations: 1000,
	}}
	app, err := NewApp(context.Background(), database, cfg, "auth-contract")
	if err != nil {
		t.Fatalf("create auth contract app: %v", err)
	}
	t.Cleanup(app.runtimeCancel)
	return app
}

func createAuthContractUser(t *testing.T, app *App, username string) db.SystemUser {
	t.Helper()
	user := db.SystemUser{
		Username: username, PasswordHash: app.Hasher.HashPassword("test-password"),
		Nickname: username, IsActive: true,
	}
	if err := app.DB.Create(&user).Error; err != nil {
		t.Fatalf("create auth contract user: %v", err)
	}
	return user
}

func authTokenID(t *testing.T, app *App, raw string) string {
	t.Helper()
	payload, err := app.Tokens.VerifyToken(raw, "refresh")
	if err != nil {
		t.Fatalf("verify generated refresh token: %v", err)
	}
	return payload.TokenID
}

func registerAuthQueryBarrier(
	t *testing.T,
	database *gorm.DB,
	name string,
	arrived chan<- struct{},
	release <-chan struct{},
) {
	t.Helper()
	var once sync.Once
	if err := database.Callback().Query().Before("gorm:query").Register(name, func(tx *gorm.DB) {
		if tx.Statement.Table != "refresh_tokens" {
			return
		}
		once.Do(func() {
			arrived <- struct{}{}
			<-release
		})
	}); err != nil {
		t.Fatalf("register auth query barrier: %v", err)
	}
	t.Cleanup(func() {
		_ = database.Callback().Query().Remove(name)
	})
}

func waitAuthSignal(t *testing.T, signal <-chan struct{}, failure string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(10 * time.Second):
		t.Fatal(failure)
	}
}

func waitAuthResult(t *testing.T, results <-chan authRefreshResult) authRefreshResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent refresh did not finish")
		return authRefreshResult{}
	}
}
