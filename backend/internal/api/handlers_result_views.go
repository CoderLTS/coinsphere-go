package api

import (
	"fmt"
	"net/http"

	"coinsphere/backend/internal/service"
)

func (s *Server) handleListResultViews(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	items, err := s.App.ListResultViews(r.Context(), principal)
	respond(w, M{"items": items}, err, "")
}

func (s *Server) handleCreateResultView(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	r.Body = http.MaxBytesReader(w, r.Body, maxWorkflowRequestBytes)
	payload, err := decodeBody[service.ResultViewCreatePayload](r)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	view, err := s.App.CreateResultView(r.Context(), *payload, principal)
	respond(w, view, err, "")
}

func (s *Server) handleGetResultView(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	viewID, err := pathInt64(r, "viewId")
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	view, err := s.App.GetResultView(r.Context(), viewID, principal)
	respond(w, view, err, "")
}

func (s *Server) handleReplaceResultViewGrants(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	viewID, err := pathInt64(r, "viewId")
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxWorkflowRequestBytes)
	payload, err := decodeBody[service.ResultViewGrantPayload](r)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	view, err := s.App.ReplaceResultViewGrants(r.Context(), viewID, *payload, principal)
	respond(w, view, err, "")
}

func (s *Server) handleRevokeResultView(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	viewID, err := pathInt64(r, "viewId")
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	view, err := s.App.RevokeResultView(r.Context(), viewID, principal)
	respond(w, view, err, "")
}

func (s *Server) handleListResultViewRuns(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	viewID, err := pathInt64(r, "viewId")
	if err != nil {
		writeProblem(w, r, http.StatusNotFound, service.ErrNotFound.Error())
		return
	}
	scope, err := s.App.ResolveResultScope(r.Context(), viewID, "", principal)
	if err != nil {
		respond(w, nil, fmt.Errorf("%w: result view", service.ErrNotFound), "")
		return
	}
	runs, err := s.App.ListResultScopeRuns(r.Context(), scope)
	respond(w, M{"items": runs}, err, "")
}

func (s *Server) handleResultViewRunAction(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	action := r.PathValue("action")
	viewID, viewErr := pathInt64(r, "viewId")
	runID, runErr := pathInt64(r, "runId")
	if viewErr != nil || runErr != nil {
		writeProblem(w, r, http.StatusNotFound, service.ErrNotFound.Error())
		return
	}
	scope, err := s.App.ResolveResultScope(r.Context(), viewID, action, principal)
	if err != nil {
		respond(w, nil, fmt.Errorf("%w: result view", service.ErrNotFound), "")
		return
	}
	if !authorizeResultAction(w, r, principal, action) {
		return
	}
	run, err := s.App.ApplyResultScopeRunAction(r.Context(), scope, runID, action)
	respond(w, run, err, "")
}

func (s *Server) handleResultViewWorkflowPause(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	viewID, err := pathInt64(r, "viewId")
	if err != nil {
		writeProblem(w, r, http.StatusNotFound, service.ErrNotFound.Error())
		return
	}
	scope, err := s.App.ResolveResultScope(r.Context(), viewID, "pause", principal)
	if err != nil {
		respond(w, nil, fmt.Errorf("%w: result view", service.ErrNotFound), "")
		return
	}
	if !authorizeResultAction(w, r, principal, "pause") {
		return
	}
	workflow, err := s.App.PauseResultScopeWorkflow(r.Context(), scope)
	respond(w, workflow, err, "")
}
