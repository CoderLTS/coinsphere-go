package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"coinsphere/backend/internal/service"
)

func TestAuthResponseContainsOnlyAccessToken(t *testing.T) {
	w := httptest.NewRecorder()
	ok(w, M{"accessToken": "test-access-token"})

	if cookies := w.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("authentication response set %d cookies, want none", len(cookies))
	}
	var envelope map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	data, ok := envelope["data"].(map[string]any)
	if !ok || data["accessToken"] != "test-access-token" {
		t.Fatalf("authentication response data = %#v", envelope["data"])
	}
	if _, present := data["refreshToken"]; present {
		t.Fatal("authentication response exposed refresh token")
	}
}

func TestRequireAuthRejectsAnonymousWithProblemDetails(t *testing.T) {
	server := &Server{}
	handler := server.requireAuth(func(http.ResponseWriter, *http.Request, *service.Principal) {})
	r := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	r = r.WithContext(r.Context())
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
	if got := w.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Fatalf("content type = %q, want problem+json", got)
	}
	var problem map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	for _, field := range []string{"type", "title", "status", "detail", "requestId"} {
		if problem[field] == nil || problem[field] == "" {
			t.Fatalf("problem field %q missing: %#v", field, problem)
		}
	}
}
