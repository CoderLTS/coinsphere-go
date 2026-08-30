package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"coinsphere/backend/internal/service"
)

func (s *Server) handleListAIModels(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
	data, err := s.App.ListAIModels(r.Context())
	respond(w, data, err, "")
}

func (s *Server) handleCreateAIModel(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	payload, err := decodeBody[service.AIModelUpsertPayload](r)
	if err != nil {
		respond(w, nil, err, "")
		return
	}
	data, err := s.App.CreateAIModel(r.Context(), *payload, principal)
	respond(w, data, err, "")
}

func (s *Server) handleUpdateAIModel(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	modelID, err := pathInt64(r, "modelId")
	if err != nil {
		respond(w, nil, err, "")
		return
	}
	payload, err := decodeBody[service.AIModelUpsertPayload](r)
	if err != nil {
		respond(w, nil, err, "")
		return
	}
	data, err := s.App.UpdateAIModel(r.Context(), modelID, *payload, principal)
	respond(w, data, err, "")
}

func (s *Server) handlePatchAIModel(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	modelID, err := pathInt64(r, "modelId")
	if err != nil {
		respond(w, nil, err, "")
		return
	}
	payload, err := decodeBody[service.AIModelPatchPayload](r)
	if err != nil {
		respond(w, nil, err, "")
		return
	}
	data, err := s.App.PatchAIModel(r.Context(), modelID, *payload, principal)
	respond(w, data, err, "")
}

func (s *Server) handleDeleteAIModel(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
	modelID, err := pathInt64(r, "modelId")
	if err == nil {
		err = s.App.DeleteAIModel(r.Context(), modelID)
	}
	respond(w, map[string]int64{"id": modelID}, err, "")
}

func (s *Server) handleValidateAIModel(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
	modelID, err := pathInt64(r, "modelId")
	if err != nil {
		respond(w, nil, err, "")
		return
	}
	data, err := s.App.ValidateAIModel(r.Context(), modelID)
	respond(w, data, err, "")
}

func (s *Server) handleListAssistantModels(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
	data, err := s.App.ListAssistantModels(r.Context())
	respond(w, data, err, "")
}

func (s *Server) handleListAssistantSessions(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	data, err := s.App.ListAssistantSessions(r.Context(), principal)
	respond(w, data, err, "")
}

func (s *Server) handleCreateAssistantSession(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	payload, err := decodeBody[service.AssistantSessionCreatePayload](r)
	if err != nil {
		respond(w, nil, err, "")
		return
	}
	data, err := s.App.CreateAssistantSession(r.Context(), *payload, principal)
	respond(w, data, err, "")
}

func (s *Server) handleGetAssistantSession(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	sessionID, err := pathInt64(r, "sessionId")
	if err != nil {
		respond(w, nil, err, "")
		return
	}
	data, err := s.App.GetAssistantSession(r.Context(), sessionID, principal)
	respond(w, data, err, "")
}

func (s *Server) handleDeleteAssistantSession(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	sessionID, err := pathInt64(r, "sessionId")
	if err == nil {
		err = s.App.DeleteAssistantSession(r.Context(), sessionID, principal)
	}
	respond(w, map[string]int64{"id": sessionID}, err, "")
}

func (s *Server) handleListAssistantMessages(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	sessionID, err := pathInt64(r, "sessionId")
	if err != nil {
		respond(w, nil, err, "")
		return
	}
	data, err := s.App.ListAssistantMessages(r.Context(), sessionID, principal)
	respond(w, data, err, "")
}

func (s *Server) handleStreamAssistantSession(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	sessionID, err := pathInt64(r, "sessionId")
	if err != nil {
		respond(w, nil, err, "")
		return
	}
	payload, err := decodeBody[service.AssistantStreamPayload](r)
	if err != nil {
		respond(w, nil, err, "")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		respond(w, nil, errors.New("streaming is unavailable"), "")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	emit := func(event service.AssistantStreamEvent) error {
		raw, err := json.Marshal(event.Data)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Name, raw); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}
	if err := s.App.StreamAssistantSession(r.Context(), sessionID, *payload, principal, emit); err != nil && r.Context().Err() == nil {
		_ = emit(service.AssistantStreamEvent{Name: "error", Data: map[string]any{"code": 400, "msg": err.Error()}})
		_ = emit(service.AssistantStreamEvent{Name: "done", Data: map[string]any{}})
	}
}

func (s *Server) handleConfirmAssistantWorkflow(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	messageID, err := pathInt64(r, "messageId")
	if err != nil {
		respond(w, nil, err, "")
		return
	}
	data, err := s.App.ConfirmAssistantWorkflow(r.Context(), messageID, principal)
	respond(w, data, err, "")
}
