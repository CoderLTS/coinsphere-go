package api

import (
	"coinsphere/backend/internal/service"
	"github.com/gin-gonic/gin"
	"net/http"
)

func (s *Server) handlePublishWorkflowRevision(c *gin.Context) {
	id, err := pathInt64(c, "workflowId")
	if err != nil {
		respond(c, nil, err, "")
		return
	}
	payload, err := decodeBody[service.WorkflowPublishPayload](c)
	if err != nil {
		respond(c, nil, err, "")
		return
	}
	data, err := s.App.PublishWorkflowRevision(c.Request.Context(), id, *payload, currentPrincipal(c))
	respond(c, data, err, "")
}
func (s *Server) handleReplaceWorkflowGrants(c *gin.Context) {
	id, err := pathInt64(c, "workflowId")
	if err != nil {
		respond(c, nil, err, "")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128<<10)
	payload, err := decodeBody[struct {
		Grants []service.WorkflowGrant `json:"grants"`
	}](c)
	if err != nil {
		respond(c, nil, err, "")
		return
	}
	err = s.App.ReplaceWorkflowGrants(c.Request.Context(), id, payload.Grants)
	respond(c, M{}, err, "")
}
func (s *Server) handleGetWorkflowGrants(c *gin.Context) {
	id, err := pathInt64(c, "workflowId")
	if err != nil {
		respond(c, nil, err, "")
		return
	}
	data, err := s.App.GetWorkflowGrants(c.Request.Context(), id)
	respond(c, M{"items": data}, err, "")
}
func (s *Server) handleListCapabilities(c *gin.Context) {
	data, err := s.App.ListCapabilities(c.Request.Context())
	respond(c, M{"items": data}, err, "")
}
func (s *Server) handleWorkbench(c *gin.Context) {
	data, err := s.App.Workbench(c.Request.Context())
	respond(c, data, err, "")
}
func (s *Server) handlePluginCatalog(c *gin.Context) {
	ok(c, M{"items": s.App.PluginCatalog(c.Request.Context())})
}
