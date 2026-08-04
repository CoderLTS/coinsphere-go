package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"coinsphere/backend/internal/service"
)

func TestRefreshCookieContract(t *testing.T) {
	tests := []struct {
		name      string
		target    string
		forwarded string
		secure    bool
	}{
		{name: "direct http", target: "http://app/api/auth/login"},
		{name: "direct https", target: "https://app/api/auth/login", secure: true},
		{name: "trusted proxy https", target: "http://app/api/auth/login", forwarded: "https", secure: true},
	}
	expiresAt := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, test.target, nil)
			if test.forwarded != "" {
				r.Header.Set("X-Forwarded-Proto", test.forwarded)
			}
			w := httptest.NewRecorder()
			writeAuthSession(w, r, &service.AuthSession{
				AccessToken: "test-access-token", RefreshToken: "test-refresh-token",
				RefreshExpiresAt: expiresAt,
			})

			cookies := w.Result().Cookies()
			if len(cookies) != 1 {
				t.Fatalf("Set-Cookie count = %d, want 1", len(cookies))
			}
			cookie := cookies[0]
			if cookie.Name != refreshCookieName || cookie.Value != "test-refresh-token" {
				t.Fatal("refresh cookie name or value mismatch")
			}
			if cookie.Path != "/api/auth" || !cookie.HttpOnly ||
				cookie.SameSite != http.SameSiteStrictMode || cookie.Secure != test.secure {
				t.Fatalf("refresh cookie attributes = path:%q httpOnly:%v sameSite:%v secure:%v",
					cookie.Path, cookie.HttpOnly, cookie.SameSite, cookie.Secure)
			}
			if !cookie.Expires.Equal(expiresAt) {
				t.Fatalf("refresh cookie expiry = %v, want %v", cookie.Expires, expiresAt)
			}
		})
	}
}

func TestClearRefreshCookieExpiresImmediately(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "https://app/api/auth/logout", nil)
	w := httptest.NewRecorder()
	clearRefreshCookie(w, r)

	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("Set-Cookie count = %d, want 1", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != refreshCookieName || cookie.Value != "" || cookie.MaxAge != -1 ||
		cookie.Path != "/api/auth" || !cookie.HttpOnly || !cookie.Secure ||
		cookie.SameSite != http.SameSiteStrictMode || !cookie.Expires.Before(time.Now()) {
		t.Fatal("refresh cookie was not cleared with the authentication cookie attributes")
	}
}
