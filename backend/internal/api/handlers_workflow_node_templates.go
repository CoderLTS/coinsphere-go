package api

import (
	"errors"
	"net/http"

	"coinsphere/backend/internal/service"
)

func (s *Server) handleListWorkflowNodeTemplates(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	data, err := s.App.ListWorkflowNodeTemplates(r.Context(), principal.User.ID)
	writeWorkflowNodeTemplateResult(w, r, data, err)
}

func (s *Server) handleCreateWorkflowNodeTemplate(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	payload, err := decodeStrictBody[service.WorkflowNodeTemplatePayload](r)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "节点模板参数无效")
		return
	}
	data, err := s.App.CreateWorkflowNodeTemplate(r.Context(), principal.User.ID, *payload)
	writeWorkflowNodeTemplateResult(w, r, data, err)
}

func (s *Server) handleUpdateWorkflowNodeTemplate(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	payload, err := decodeStrictBody[service.WorkflowNodeTemplatePayload](r)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "节点模板参数无效")
		return
	}
	data, err := s.App.UpdateWorkflowNodeTemplate(r.Context(), principal.User.ID, r.PathValue("templateId"), *payload)
	writeWorkflowNodeTemplateResult(w, r, data, err)
}

func (s *Server) handleDeleteWorkflowNodeTemplate(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	err := s.App.DeleteWorkflowNodeTemplate(r.Context(), principal.User.ID, r.PathValue("templateId"))
	if err != nil {
		writeWorkflowNodeTemplateResult(w, r, nil, err)
		return
	}
	ok(w, M{"deleted": true})
}

func writeWorkflowNodeTemplateResult(w http.ResponseWriter, r *http.Request, data any, err error) {
	if err == nil {
		ok(w, data)
		return
	}
	if errors.Is(err, service.ErrWorkflowNodeTemplateMissing) {
		writeProblem(w, r, http.StatusNotFound, "节点模板不存在")
		return
	}
	writeProblem(w, r, http.StatusBadRequest, err.Error())
}
