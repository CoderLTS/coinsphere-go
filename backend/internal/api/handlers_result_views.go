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

func (s *Server) handleListResultViewBatches(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
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
	batches, err := s.App.ListResultScopeBatches(r.Context(), scope)
	respond(w, M{"items": batches}, err, "")
}

func (s *Server) handleResultViewBatchAction(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	action := r.PathValue("action")
	viewID, viewErr := pathInt64(r, "viewId")
	batchID, batchErr := pathInt64(r, "batchId")
	if viewErr != nil || batchErr != nil {
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
	batch, err := s.App.ApplyResultScopeBatchAction(r.Context(), scope, batchID, action)
	respond(w, batch, err, "")
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
