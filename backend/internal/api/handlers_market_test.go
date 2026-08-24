package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/service"
)

func TestMarketRoutesAreNotRegistered(t *testing.T) {
	mux := http.NewServeMux()
	server := &Server{StaticDir: t.TempDir(), UploadsDir: t.TempDir()}
	server.registerRoutes(mux)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/markets/symbols"},
		{http.MethodGet, "/api/v1/markets/candles"},
		{http.MethodGet, "/api/v1/watchlists"},
		{http.MethodPost, "/api/v1/watchlists"},
		{http.MethodPost, "/api/v1/markets/metadata-sync/proxy-check"},
		{http.MethodDelete, "/api/v1/watchlists/019c2f6d-7c00-7000-8000-000000000001"},
	} {
		request := httptest.NewRequest(route.method, route.path, nil)
		if _, pattern := mux.Handler(request); pattern != "" {
			t.Fatalf("removed market route %s %s still has route %q", route.method, route.path, pattern)
		}
	}
}

func TestMarketHandlersRejectInvalidBoundaryInput(t *testing.T) {
	server := &Server{App: &service.App{}}
	principal := &service.Principal{User: &db.SystemUser{ID: 42}}
	tests := []struct {
		name    string
		request *http.Request
		handle  func(http.ResponseWriter, *http.Request, *service.Principal)
	}{
		{name: "symbol market", request: httptest.NewRequest(http.MethodGet, "/api/v1/markets/symbols?market=options", nil), handle: server.handleListMarketSymbols},
		{name: "candle instrument", request: httptest.NewRequest(http.MethodGet, "/api/v1/markets/candles?interval=1m", nil), handle: server.handleListMarketCandles},
		{name: "watchlist interval", request: httptest.NewRequest(http.MethodPost, "/api/v1/watchlists", bytes.NewBufferString(`{"instrumentId":"019c2f6d-7c00-7000-8000-000000000001","interval":"2m"}`)), handle: server.handleCreateWatchlist},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			test.handle(recorder, test.request, principal)
			if recorder.Code != http.StatusBadRequest || recorder.Header().Get("Content-Type") != "application/problem+json" {
				t.Fatalf("status = %d, content type = %q", recorder.Code, recorder.Header().Get("Content-Type"))
			}
		})
	}
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/watchlists/not-v7", nil)
	request.SetPathValue("watchlistId", "not-v7")
	recorder := httptest.NewRecorder()
	server.handleDeleteWatchlist(recorder, request, principal)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid delete status = %d", recorder.Code)
	}
}
