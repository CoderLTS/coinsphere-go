package api

import (
	"fmt"
	"net/http"

	"coinsphere/backend/internal/service"
	"github.com/gin-gonic/gin"
)

func (s *Server) handleListResultViews(c *gin.Context) {
	items, err := s.App.ListResultViews(c.Request.Context(), currentPrincipal(c))
	respond(c, M{"items": items}, err, "")
}

func (s *Server) handleCreateResultView(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxWorkflowRequestBytes)
	payload, err := decodeBody[service.ResultViewCreatePayload](c)
	if err != nil {
		writeProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	view, err := s.App.CreateResultView(c.Request.Context(), *payload, currentPrincipal(c))
	respond(c, view, err, "")
}

func (s *Server) handleGetResultView(c *gin.Context) {
	viewID, err := pathInt64(c, "viewId")
	if err != nil {
		writeProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	view, err := s.App.GetResultView(c.Request.Context(), viewID, currentPrincipal(c))
	respond(c, view, err, "")
}

func (s *Server) handleReplaceResultViewGrants(c *gin.Context) {
	viewID, err := pathInt64(c, "viewId")
	if err != nil {
		writeProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxWorkflowRequestBytes)
	payload, err := decodeBody[service.ResultViewGrantPayload](c)
	if err != nil {
		writeProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	view, err := s.App.ReplaceResultViewGrants(c.Request.Context(), viewID, *payload, currentPrincipal(c))
	respond(c, view, err, "")
}

func (s *Server) handleRevokeResultView(c *gin.Context) {
	viewID, err := pathInt64(c, "viewId")
	if err != nil {
		writeProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	view, err := s.App.RevokeResultView(c.Request.Context(), viewID, currentPrincipal(c))
	respond(c, view, err, "")
}

func (s *Server) handleListResultViewRuns(c *gin.Context) {
	viewID, err := pathInt64(c, "viewId")
	if err != nil {
		writeProblem(c, http.StatusNotFound, service.ErrNotFound.Error())
		return
	}
	scope, err := s.App.ResolveResultScope(c.Request.Context(), viewID, "", currentPrincipal(c))
	if err != nil {
		respond(c, nil, fmt.Errorf("%w: result view", service.ErrNotFound), "")
		return
	}
	runs, err := s.App.ListResultScopeRuns(c.Request.Context(), scope)
	respond(c, M{"items": runs}, err, "")
}

func (s *Server) handleResultViewRunAction(c *gin.Context) {
	action := c.Param("action")
	viewID, viewErr := pathInt64(c, "viewId")
	runID, runErr := pathInt64(c, "runId")
	if viewErr != nil || runErr != nil {
		writeProblem(c, http.StatusNotFound, service.ErrNotFound.Error())
		return
	}
	principal := currentPrincipal(c)
	scope, err := s.App.ResolveResultScope(c.Request.Context(), viewID, action, principal)
	if err != nil {
		respond(c, nil, fmt.Errorf("%w: result view", service.ErrNotFound), "")
		return
	}
	if !authorizeResultAction(c, principal, action) {
		return
	}
	run, err := s.App.ApplyResultScopeRunAction(c.Request.Context(), scope, runID, action)
	respond(c, run, err, "")
}

func (s *Server) handleResultViewWorkflowPause(c *gin.Context) {
	viewID, err := pathInt64(c, "viewId")
	if err != nil {
		writeProblem(c, http.StatusNotFound, service.ErrNotFound.Error())
		return
	}
	principal := currentPrincipal(c)
	scope, err := s.App.ResolveResultScope(c.Request.Context(), viewID, "pause", principal)
	if err != nil {
		respond(c, nil, fmt.Errorf("%w: result view", service.ErrNotFound), "")
		return
	}
	if !authorizeResultAction(c, principal, "pause") {
		return
	}
	workflow, err := s.App.PauseResultScopeWorkflow(c.Request.Context(), scope)
	respond(c, workflow, err, "")
}
