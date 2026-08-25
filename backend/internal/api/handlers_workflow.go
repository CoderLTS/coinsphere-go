package api

import (
	"net/http"

	"coinsphere/backend/internal/service"
)

const maxWorkflowRequestBytes = 2 << 20

func (s *Server) handleListWorkflowTemplates(w http.ResponseWriter, _ *http.Request, _ *service.Principal) {
	ok(w, M{"items": s.App.ListWorkflowTemplates()})
}

func (s *Server) handleListWorkflows(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
	items, err := s.App.ListWorkflows(r.Context(), queryStr(r, "status"))
	respond(w, M{"items": items}, err, "")
}

func (s *Server) handleCreateWorkflow(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	r.Body = http.MaxBytesReader(w, r.Body, maxWorkflowRequestBytes)
	payload, err := decodeBody[service.WorkflowCreatePayload](r)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	data, err := s.App.CreateWorkflow(r.Context(), *payload, principal)
	respond(w, data, err, "")
}

func (s *Server) handleGetWorkflow(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
	workflowID, err := pathInt64(r, "workflowId")
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	data, err := s.App.GetWorkflow(r.Context(), workflowID)
	respond(w, data, err, "")
}

func (s *Server) handleSaveWorkflowRevision(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	workflowID, err := pathInt64(r, "workflowId")
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxWorkflowRequestBytes)
	payload, err := decodeBody[service.WorkflowRevisionSavePayload](r)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	data, err := s.App.SaveWorkflowRevision(r.Context(), workflowID, *payload, principal)
	respond(w, data, err, "")
}

func (s *Server) handleListWorkflowRevisions(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
	workflowID, err := pathInt64(r, "workflowId")
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	items, err := s.App.ListWorkflowRevisions(r.Context(), workflowID)
	respond(w, M{"items": items}, err, "")
}

func (s *Server) handleGetWorkflowRevision(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
	workflowID, err := pathInt64(r, "workflowId")
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	revisionID, err := pathInt64(r, "revisionId")
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	data, err := s.App.GetWorkflowRevision(r.Context(), workflowID, revisionID)
	respond(w, data, err, "")
}

func (s *Server) handleWorkflowLifecycle(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
	workflowID, err := pathInt64(r, "workflowId")
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxWorkflowRequestBytes)
	payload, err := decodeBody[service.WorkflowLifecyclePayload](r)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	data, err := s.App.ApplyWorkflowLifecycle(r.Context(), workflowID, *payload)
	respond(w, data, err, "")
}
