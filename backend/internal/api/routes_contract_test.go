package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRoutesUseV1AndRemoveLegacyEntrypoints(t *testing.T) {
	mux := http.NewServeMux()
	server := &Server{StaticDir: t.TempDir(), UploadsDir: t.TempDir()}
	server.registerRoutes(mux)

	for _, route := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/auth/login"},
		{http.MethodGet, "/api/auth/me"},
		{http.MethodPost, "/api/auth/register"},
		{http.MethodPost, "/api/v1/auth/register"},
		{http.MethodPost, "/api/auth/refresh"},
		{http.MethodPost, "/api/v1/auth/refresh"},
		{http.MethodPost, "/api/auth/logout"},
		{http.MethodGet, "/ws/notifications"},
		{http.MethodGet, "/api/data/push-deliveries"},
		{http.MethodGet, "/api/v1/data/push-deliveries"},
	} {
		request := httptest.NewRequest(route.method, route.path, nil)
		if _, pattern := mux.Handler(request); pattern != "" {
			t.Fatalf("legacy route %s %q still has route %q", route.method, route.path, pattern)
		}
	}
	for _, route := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/auth/login"},
		{http.MethodPost, "/api/v1/auth/logout"},
		{http.MethodPost, "/api/v1/auth/reauth"},
		{http.MethodGet, "/api/v1/me"},
		{http.MethodGet, "/api/v1/ws/notifications"},
		{http.MethodGet, "/api/v1/admin/strategies"},
		{http.MethodPost, "/api/v1/admin/strategies/019c2f6d-7c00-7000-8000-000000000001/publish"},
		{http.MethodGet, "/api/v1/strategies"},
		{http.MethodPost, "/api/v1/backtests"},
		{http.MethodPost, "/api/v1/backtests/019c2f6d-7c00-7000-8000-000000000001/cancel"},
	} {
		request := httptest.NewRequest(route.method, route.path, nil)
		if _, pattern := mux.Handler(request); pattern == "" {
			t.Fatalf("v1 path %q has no route", route.path)
		}
	}
}

func TestCursorPageDefaultsAndRejectsInvalidInput(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/data/news?keyword=btc", nil)
	request.Pattern = "GET /api/v1/data/news"
	page, err := queryCursorPage(request)
	if err != nil || page.Limit != 50 {
		t.Fatalf("default cursor page = %#v, err = %v", page, err)
	}
	for _, query := range []string{"?limit=0", "?limit=201", "?cursor=not-base64"} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/data/news"+query, nil)
		request.Pattern = "GET /api/v1/data/news"
		if _, err := queryCursorPage(request); err == nil {
			t.Fatalf("invalid query %q was accepted", query)
		}
	}
}
