package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"coinsphere/backend/internal/service"
)

func TestWriteWorkflowProblemMapsContractStatuses(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
	}{
		{name: "bad request", err: errors.New("bad request"), status: http.StatusBadRequest},
		{name: "conflict", err: service.ErrIdempotencyConflict, status: http.StatusConflict},
		{name: "backlog", err: service.ErrBacklogExceeded, status: http.StatusTooManyRequests},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/1/executions", nil)
			writeWorkflowProblem(recorder, request, test.err)
			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d", recorder.Code, test.status)
			}
			if got := recorder.Header().Get("Content-Type"); got != "application/problem+json" {
				t.Fatalf("content type = %q", got)
			}
		})
	}
}

func TestWorkflowCatchAllWritesProblemDetails(t *testing.T) {
	server := &Server{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/workflows/not-found", nil)
	request.SetPathValue("workflowPath", "not-found")

	server.handleWorkflowCatchAll(recorder, request, nil)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Fatalf("content type = %q", got)
	}
	var problem map[string]any
	if err := json.NewDecoder(recorder.Body).Decode(&problem); err != nil {
		t.Fatalf("decode problem response: %v", err)
	}
	if problem["status"] != float64(http.StatusNotFound) {
		t.Fatalf("problem status = %#v", problem["status"])
	}
}
