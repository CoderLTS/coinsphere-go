package api

import (
	"coinsphere/backend/internal/service"
	"github.com/gin-gonic/gin"
	"net/http"
)

func (s *Server) handleListConnections(c *gin.Context) {
	data, err := s.App.ListConnections(c.Request.Context())
	respond(c, service.M{"items": data}, err, "")
}
func (s *Server) handleConnectionTypes(c *gin.Context) {
	respond(c, service.M{"items": s.App.ListConnectionTypes()}, nil, "")
}
func (s *Server) handleSaveConnection(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	payload, err := decodeBody[service.ConnectionPayload](c)
	if err != nil {
		respond(c, nil, err, "")
		return
	}
	data, err := s.App.SaveConnection(c.Request.Context(), c.Param("connectionId"), *payload)
	respond(c, data, err, "")
}
func (s *Server) handleDeleteConnection(c *gin.Context) {
	respond(c, nil, s.App.DeleteConnection(c.Request.Context(), c.Param("connectionId")), "")
}
