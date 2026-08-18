package service

import (
	"context"
	"testing"

	"coinsphere/backend/internal/config"
	"coinsphere/backend/internal/db"
)

func TestLoginIssuesAccessToken(t *testing.T) {
	database := openPostgresWorkflowContractDatabase(t)
	cfg := &config.AppConfig{Auth: config.AuthConfig{
		SecretKey: "test-only-auth-secret", EncryptionKey: "test-only-encryption-secret",
		AccessTokenTTLMinutes: 15, PasswordIterations: 1000,
	}}
	app, err := NewApp(context.Background(), database.primary, cfg, "auth-contract")
	if err != nil {
		t.Fatalf("create auth contract app: %v", err)
	}
	t.Cleanup(app.runtimeCancel)
	if app.Paper == nil {
		t.Fatal("app did not own the Paper executor")
	}
	user := db.SystemUser{
		Username: "access-only-user", PasswordHash: app.Hasher.HashPassword("test-password"),
		Nickname: "access-only-user", IsActive: true,
	}
	if err := app.DB.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	session, err := app.Login(user.Username, "test-password")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if session.UserID != user.ID || session.AccessToken == "" {
		t.Fatalf("login session = %#v", session)
	}
	payload, err := app.Tokens.VerifyAccessToken(session.AccessToken)
	if err != nil || payload.UserID != user.ID {
		t.Fatalf("verify access token: payload=%#v err=%v", payload, err)
	}
}
