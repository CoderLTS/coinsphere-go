package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"coinsphere/backend/internal/service"
	"github.com/gin-gonic/gin"
)

func (s *Server) handleListAIModels(c *gin.Context) {
	data, err := s.App.ListAIModels(c.Request.Context())
	respond(c, data, err, "")
}

func (s *Server) handleCreateAIModel(c *gin.Context) {
	payload, err := decodeBody[service.AIModelUpsertPayload](c)
	if err != nil {
		respond(c, nil, err, "")
		return
	}
	data, err := s.App.CreateAIModel(c.Request.Context(), *payload, currentPrincipal(c))
	respond(c, data, err, "")
}

func (s *Server) handleUpdateAIModel(c *gin.Context) {
	modelID, err := pathInt64(c, "modelId")
	if err != nil {
		respond(c, nil, err, "")
		return
	}
	payload, err := decodeBody[service.AIModelUpsertPayload](c)
	if err != nil {
		respond(c, nil, err, "")
		return
	}
	data, err := s.App.UpdateAIModel(c.Request.Context(), modelID, *payload, currentPrincipal(c))
	respond(c, data, err, "")
}

func (s *Server) handlePatchAIModel(c *gin.Context) {
	modelID, err := pathInt64(c, "modelId")
	if err != nil {
		respond(c, nil, err, "")
		return
	}
	payload, err := decodeBody[service.AIModelPatchPayload](c)
	if err != nil {
		respond(c, nil, err, "")
		return
	}
	data, err := s.App.PatchAIModel(c.Request.Context(), modelID, *payload, currentPrincipal(c))
	respond(c, data, err, "")
}

func (s *Server) handleDeleteAIModel(c *gin.Context) {
	modelID, err := pathInt64(c, "modelId")
	if err == nil {
		err = s.App.DeleteAIModel(c.Request.Context(), modelID)
	}
	respond(c, map[string]int64{"id": modelID}, err, "")
}

func (s *Server) handleValidateAIModel(c *gin.Context) {
	modelID, err := pathInt64(c, "modelId")
	if err != nil {
		respond(c, nil, err, "")
		return
	}
	data, err := s.App.ValidateAIModel(c.Request.Context(), modelID)
	respond(c, data, err, "")
}

func (s *Server) handleListAssistantModels(c *gin.Context) {
	data, err := s.App.ListAssistantModels(c.Request.Context())
	respond(c, data, err, "")
}

func (s *Server) handleListAssistantSessions(c *gin.Context) {
	data, err := s.App.ListAssistantSessions(c.Request.Context(), currentPrincipal(c))
	respond(c, data, err, "")
}

func (s *Server) handleCreateAssistantSession(c *gin.Context) {
	payload, err := decodeBody[service.AssistantSessionCreatePayload](c)
	if err != nil {
		respond(c, nil, err, "")
		return
	}
	data, err := s.App.CreateAssistantSession(c.Request.Context(), *payload, currentPrincipal(c))
	respond(c, data, err, "")
}

func (s *Server) handleGetAssistantSession(c *gin.Context) {
	sessionID, err := pathInt64(c, "sessionId")
	if err != nil {
		respond(c, nil, err, "")
		return
	}
	data, err := s.App.GetAssistantSession(c.Request.Context(), sessionID, currentPrincipal(c))
	respond(c, data, err, "")
}

func (s *Server) handleDeleteAssistantSession(c *gin.Context) {
	sessionID, err := pathInt64(c, "sessionId")
	if err == nil {
		err = s.App.DeleteAssistantSession(c.Request.Context(), sessionID, currentPrincipal(c))
	}
	respond(c, map[string]int64{"id": sessionID}, err, "")
}

func (s *Server) handleListAssistantMessages(c *gin.Context) {
	sessionID, err := pathInt64(c, "sessionId")
	if err != nil {
		respond(c, nil, err, "")
		return
	}
	data, err := s.App.ListAssistantMessages(c.Request.Context(), sessionID, currentPrincipal(c))
	respond(c, data, err, "")
}

func (s *Server) handleStreamAssistantSession(c *gin.Context) {
	sessionID, err := pathInt64(c, "sessionId")
	if err != nil {
		respond(c, nil, err, "")
		return
	}
	payload, err := decodeBody[service.AssistantStreamPayload](c)
	if err != nil {
		respond(c, nil, err, "")
		return
	}
	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	c.Header("Cache-Control", "no-cache, no-transform")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	c.Writer.Flush()
	emit := func(event service.AssistantStreamEvent) error {
		raw, err := json.Marshal(event.Data)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event.Name, raw); err != nil {
			return err
		}
		c.Writer.Flush()
		return nil
	}
	principal := currentPrincipal(c)
	if err := s.App.StreamAssistantSession(c.Request.Context(), sessionID, *payload, principal, emit); err != nil && c.Request.Context().Err() == nil {
		_ = emit(service.AssistantStreamEvent{Name: "error", Data: map[string]any{"code": 400, "msg": err.Error()}})
		_ = emit(service.AssistantStreamEvent{Name: "done", Data: map[string]any{}})
	}
}
