package api

import (
	"coinsphere/backend/internal/service"
	"github.com/gin-gonic/gin"
)

func (s *Server) handleListOutboundProxies(c *gin.Context) {
	data, err := s.App.ListOutboundProxies(c.Request.Context())
	respond(c, data, err, "")
}

func (s *Server) handleCreateOutboundProxy(c *gin.Context) {
	payload, err := decodeBody[service.OutboundProxyUpsertPayload](c)
	if err != nil {
		respond(c, nil, err, "")
		return
	}
	data, err := s.App.CreateOutboundProxy(c.Request.Context(), *payload, currentPrincipal(c))
	respond(c, data, err, "")
}

func (s *Server) handleUpdateOutboundProxy(c *gin.Context) {
	proxyID, err := pathInt64(c, "proxyId")
	if err != nil {
		respond(c, nil, err, "")
		return
	}
	payload, err := decodeBody[service.OutboundProxyUpsertPayload](c)
	if err != nil {
		respond(c, nil, err, "")
		return
	}
	data, err := s.App.UpdateOutboundProxy(c.Request.Context(), proxyID, *payload, currentPrincipal(c))
	respond(c, data, err, "")
}

func (s *Server) handlePatchOutboundProxy(c *gin.Context) {
	proxyID, err := pathInt64(c, "proxyId")
	if err != nil {
		respond(c, nil, err, "")
		return
	}
	payload, err := decodeBody[service.OutboundProxyPatchPayload](c)
	if err != nil {
		respond(c, nil, err, "")
		return
	}
	data, err := s.App.PatchOutboundProxy(c.Request.Context(), proxyID, *payload, currentPrincipal(c))
	respond(c, data, err, "")
}

func (s *Server) handleDeleteOutboundProxy(c *gin.Context) {
	proxyID, err := pathInt64(c, "proxyId")
	if err == nil {
		err = s.App.DeleteOutboundProxy(c.Request.Context(), proxyID)
	}
	respond(c, map[string]int64{"id": proxyID}, err, "")
}

func (s *Server) handleValidateOutboundProxy(c *gin.Context) {
	proxyID, err := pathInt64(c, "proxyId")
	if err != nil {
		respond(c, nil, err, "")
		return
	}
	data, err := s.App.ValidateOutboundProxy(c.Request.Context(), proxyID)
	respond(c, data, err, "")
}
