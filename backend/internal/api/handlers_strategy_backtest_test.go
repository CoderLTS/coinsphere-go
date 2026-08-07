package api

import (
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"coinsphere/backend/internal/service"
)

func TestStrategyAdminBoundaryAndStrictBody(t *testing.T) {
	request := httptest.NewRequest("POST", "/api/v1/admin/strategies", nil)
	response := httptest.NewRecorder()
	if requireStrategyAdmin(response, request, &service.Principal{RoleCodes: []string{"R_GUEST"}}) {
		t.Fatal("non-admin passed strategy boundary")
	}
	if response.Code != 403 {
		t.Fatalf("non-admin status = %d", response.Code)
	}
	if !requireStrategyAdmin(httptest.NewRecorder(), request, &service.Principal{RoleCodes: []string{"R_SUPER"}}) {
		t.Fatal("super admin was rejected")
	}

	request = httptest.NewRequest("POST", "/api/v1/backtests", strings.NewReader(`{"strategyVersionId":"id","unknown":true}`))
	if _, err := decodeStrictBody[service.BacktestCreatePayload](request); err == nil {
		t.Fatal("unknown request field was accepted")
	}
	request = httptest.NewRequest("POST", "/api/v1/backtests", strings.NewReader(`{} {}`))
	if _, err := decodeStrictBody[service.BacktestCreatePayload](request); err == nil || errors.Is(err, io.EOF) {
		t.Fatalf("multiple JSON values error = %v", err)
	}
}
