package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRoutesExposeOnlyV2BaselineSurface(t *testing.T) {
	mux := http.NewServeMux()
	server := &Server{StaticDir: t.TempDir(), UploadsDir: t.TempDir()}
	server.registerRoutes(mux)

	for _, route := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/auth/login"},
		{http.MethodPost, "/api/v1/auth/logout"},
		{http.MethodPost, "/api/v1/auth/reauth"},
		{http.MethodGet, "/api/v1/me"},
		{http.MethodGet, "/api/v1/home/meta"},
		{http.MethodGet, "/api/v1/home/overview"},
		{http.MethodGet, "/api/v1/workflows/templates"},
		{http.MethodGet, "/api/v1/workflows/node-definitions"},
		{http.MethodGet, "/api/v1/workflows"},
		{http.MethodPost, "/api/v1/workflows"},
		{http.MethodGet, "/api/v1/workflows/1"},
		{http.MethodGet, "/api/v1/workflows/1/revisions"},
		{http.MethodPost, "/api/v1/workflows/1/revisions"},
		{http.MethodGet, "/api/v1/workflows/1/revisions/1"},
		{http.MethodPost, "/api/v1/workflows/1/lifecycle"},
		{http.MethodGet, "/api/v1/admin/users"},
		{http.MethodGet, "/api/v1/system/roles"},
		{http.MethodGet, "/api/v1/system/menus"},
	} {
		request := httptest.NewRequest(route.method, route.path, nil)
		if _, pattern := mux.Handler(request); pattern == "" {
			t.Fatalf("baseline route %s %q is missing", route.method, route.path)
		}
	}

	for _, route := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/auth/login"},
		{http.MethodGet, "/api/v1/ws/notifications"},
		{http.MethodGet, "/api/v1/markets/symbols"},
		{http.MethodGet, "/api/v1/data/news"},
		{http.MethodGet, "/api/v1/admin/strategies"},
		{http.MethodPost, "/api/v1/backtests"},
		{http.MethodGet, "/api/v1/trading/overview"},
		{http.MethodGet, "/api/v1/config/ai-models"},
		{http.MethodGet, "/api/v1/assistant/agents"},
		{http.MethodGet, "/api/v1/notification-deliveries"},
	} {
		request := httptest.NewRequest(route.method, route.path, nil)
		if _, pattern := mux.Handler(request); pattern != "" {
			t.Fatalf("removed route %s %q still has route %q", route.method, route.path, pattern)
		}
	}
}

func TestDecodeBodyRejectsUnknownAndTrailingFields(t *testing.T) {
	for _, body := range []string{`{"name":"test","unknown":true}`, `{"name":"test"}{"name":"again"}`} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/items", strings.NewReader(body))
		if _, err := decodeBody[struct {
			Name string `json:"name"`
		}](request); err == nil {
			t.Fatalf("invalid body %q was accepted", body)
		}
	}
}

func TestCursorPageDefaultsAndRejectsInvalidInput(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/items?keyword=btc", nil)
	request.Pattern = "GET /api/v1/items"
	page, err := queryCursorPage(request)
	if err != nil || page.Limit != 50 {
		t.Fatalf("default cursor page = %#v, err = %v", page, err)
	}
	for _, query := range []string{"?limit=0", "?limit=201", "?cursor=not-base64"} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/items"+query, nil)
		request.Pattern = "GET /api/v1/items"
		if _, err := queryCursorPage(request); err == nil {
			t.Fatalf("invalid query %q was accepted", query)
		}
	}
}
