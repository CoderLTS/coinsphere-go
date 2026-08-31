package api

import (
	"net/http"
	"strings"
	"time"

	"coinsphere/backend/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

const workflowRunsWSProtocol = "coinsphere.workflow-runs.v1"

var workflowRunsUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     checkWorkflowWebSocketOrigin,
}

type workflowRunWSMessage struct {
	Type       string                    `json:"type"`
	Version    int                       `json:"version"`
	OccurredAt string                    `json:"occurredAt"`
	Data       service.WorkflowRunUpdate `json:"data"`
}

func (s *Server) handleWorkflowRunsWebSocket(c *gin.Context) {
	workflowID, err := pathInt64(c, "workflowId")
	if err != nil {
		writeProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	token := workflowWebSocketToken(c.Request)
	principal, err := s.App.AuthenticateAccessToken(token)
	if err != nil || principal == nil || !principal.HasRole("R_SUPER") {
		writeProblem(c, http.StatusUnauthorized, "invalid access token")
		return
	}
	conn, err := workflowRunsUpgrader.Upgrade(c.Writer, c.Request, http.Header{
		"Sec-WebSocket-Protocol": []string{workflowRunsWSProtocol},
	})
	if err != nil {
		return
	}
	updates, unsubscribe := s.App.SubscribeWorkflowRuns(workflowID)
	defer unsubscribe()
	defer conn.Close()

	const pongWait = 70 * time.Second
	conn.SetReadLimit(1024)
	_ = conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(pongWait))
	})
	done := make(chan struct{})
	defer close(done)
	go func() {
		defer conn.Close()
		ticker := time.NewTicker(54 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case update := <-updates:
				_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := conn.WriteJSON(workflowRunWSMessage{
					Type: "workflow.run.updated", Version: 1,
					OccurredAt: time.Now().UTC().Format(time.RFC3339Nano), Data: update,
				}); err != nil {
					return
				}
			case <-ticker.C:
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)); err != nil {
					return
				}
			}
		}
	}()
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

func workflowWebSocketToken(r *http.Request) string {
	parts := strings.Split(r.Header.Get("Sec-WebSocket-Protocol"), ",")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) != workflowRunsWSProtocol {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

func checkWorkflowWebSocketOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return false
	}
	forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0])
	if forwarded != "http" && forwarded != "https" {
		forwarded = "http"
	}
	return origin == forwarded+"://"+r.Host
}
