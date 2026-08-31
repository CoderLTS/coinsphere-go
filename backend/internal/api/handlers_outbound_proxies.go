package api

import (
	"net/http"

	"coinsphere/backend/internal/service"
)

func (s *Server) handleListOutboundProxies(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
	data, err := s.App.ListOutboundProxies(r.Context())
	respond(w, data, err, "")
}

func (s *Server) handleCreateOutboundProxy(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	payload, err := decodeBody[service.OutboundProxyUpsertPayload](r)
	if err != nil {
		respond(w, nil, err, "")
		return
	}
	data, err := s.App.CreateOutboundProxy(r.Context(), *payload, principal)
	respond(w, data, err, "")
}

func (s *Server) handleUpdateOutboundProxy(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	proxyID, err := pathInt64(r, "proxyId")
	if err != nil {
		respond(w, nil, err, "")
		return
	}
	payload, err := decodeBody[service.OutboundProxyUpsertPayload](r)
	if err != nil {
		respond(w, nil, err, "")
		return
	}
	data, err := s.App.UpdateOutboundProxy(r.Context(), proxyID, *payload, principal)
	respond(w, data, err, "")
}

func (s *Server) handlePatchOutboundProxy(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	proxyID, err := pathInt64(r, "proxyId")
	if err != nil {
		respond(w, nil, err, "")
		return
	}
	payload, err := decodeBody[service.OutboundProxyPatchPayload](r)
	if err != nil {
		respond(w, nil, err, "")
		return
	}
	data, err := s.App.PatchOutboundProxy(r.Context(), proxyID, *payload, principal)
	respond(w, data, err, "")
}

func (s *Server) handleDeleteOutboundProxy(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
	proxyID, err := pathInt64(r, "proxyId")
	if err == nil {
		err = s.App.DeleteOutboundProxy(r.Context(), proxyID)
	}
	respond(w, map[string]int64{"id": proxyID}, err, "")
}

func (s *Server) handleValidateOutboundProxy(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
	proxyID, err := pathInt64(r, "proxyId")
	if err != nil {
		respond(w, nil, err, "")
		return
	}
	data, err := s.App.ValidateOutboundProxy(r.Context(), proxyID)
	respond(w, data, err, "")
}
