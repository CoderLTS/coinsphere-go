package service

import (
	"testing"
	"time"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/security"
)

func TestReauthTokenIsBoundAndSingleUse(t *testing.T) {
	app := &App{
		reauthTokens:        map[string]reauthTokenRecord{},
		revokedAccessTokens: map[string]time.Time{},
	}
	first := &Principal{User: &db.SystemUser{ID: 1}, AccessTokenID: "session-a"}
	otherUser := &Principal{User: &db.SystemUser{ID: 2}, AccessTokenID: "session-a"}
	otherSession := &Principal{User: &db.SystemUser{ID: 1}, AccessTokenID: "session-b"}
	token := app.issueReauthToken(first, time.Now())

	if app.ConsumeReauthToken(token, otherUser) || app.ConsumeReauthToken(token, otherSession) {
		t.Fatal("reauth token crossed its user or session binding")
	}
	if !app.ConsumeReauthToken(token, first) {
		t.Fatal("matching reauth token was rejected")
	}
	if app.ConsumeReauthToken(token, first) {
		t.Fatal("reauth token was reusable")
	}
}

func TestReauthTokenExpires(t *testing.T) {
	app := &App{
		reauthTokens:        map[string]reauthTokenRecord{},
		revokedAccessTokens: map[string]time.Time{},
	}
	principal := &Principal{User: &db.SystemUser{ID: 1}, AccessTokenID: "session-a"}
	token := security.RandomURLSafe(32)
	app.reauthTokens[security.HashToken(token)] = reauthTokenRecord{
		userID: principal.User.ID, accessTokenID: principal.AccessTokenID,
		expiresAt: time.Now().Add(-time.Second),
	}
	if app.ConsumeReauthToken(token, principal) {
		t.Fatal("expired reauth token was accepted")
	}
}

func TestLogoutRevokesAccessToken(t *testing.T) {
	app := &App{
		reauthTokens:        map[string]reauthTokenRecord{},
		revokedAccessTokens: map[string]time.Time{},
	}
	principal := &Principal{
		User: &db.SystemUser{ID: 1}, AccessTokenID: "session-a",
		AccessTokenExp: time.Now().Add(time.Minute),
	}
	app.LogoutAccessToken(principal)
	if !app.isAccessTokenRevoked(principal.AccessTokenID) {
		t.Fatal("logout did not revoke access token")
	}
}
