package api

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const workflowActivityWebSocketProtocol = "coinsphere.workflow-activity.v1"

var workflowActivityUpgrader = websocket.Upgrader{
	ReadBufferSize: 4096, WriteBufferSize: 4096,
	CheckOrigin: checkWorkflowWebSocketOrigin, Subprotocols: []string{workflowActivityWebSocketProtocol},
}

func (s *Server) handleWorkflowActivityWebSocket(w http.ResponseWriter, r *http.Request) {
	workflowID, err := pathInt64(r, "workflowId")
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	after, ok := workflowActivityWebSocketCursor(r)
	if !ok || !checkWorkflowWebSocketOrigin(r) {
		writeProblem(w, r, http.StatusForbidden, "invalid workflow activity websocket request")
		return
	}
	token, ok := workflowActivityWebSocketToken(r)
	if !ok {
		writeProblem(w, r, http.StatusUnauthorized, "invalid websocket authentication")
		return
	}
	principal, err := s.App.AuthenticateAccessToken(token)
	if err != nil {
		writeProblem(w, r, http.StatusUnauthorized, "invalid access token")
		return
	}
	if !principal.HasRole("R_SUPER") {
		writeProblem(w, r, http.StatusForbidden, "permission denied")
		return
	}
	if _, _, err := s.App.ListWorkflowActivities(r.Context(), workflowID, after, 1); err != nil {
		respond(w, nil, err, "")
		return
	}
	connection, err := workflowActivityUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer connection.Close()
	connection.SetReadLimit(1024)
	poll := time.NewTicker(250 * time.Millisecond)
	ping := time.NewTicker(20 * time.Second)
	defer poll.Stop()
	defer ping.Stop()
	for {
		items, next, err := s.App.ListWorkflowActivities(r.Context(), workflowID, after, 200)
		if err != nil {
			return
		}
		for _, item := range items {
			_ = connection.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if connection.WriteJSON(item) != nil {
				return
			}
		}
		after = next
		select {
		case <-r.Context().Done():
			return
		case <-poll.C:
		case <-ping.C:
			_ = connection.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if connection.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)) != nil {
				return
			}
		}
	}
}

func workflowActivityWebSocketToken(r *http.Request) (string, bool) {
	if len(r.Header.Values("Sec-WebSocket-Protocol")) != 1 {
		return "", false
	}
	protocols := websocket.Subprotocols(r)
	if len(protocols) != 2 || protocols[0] != workflowActivityWebSocketProtocol {
		return "", false
	}
	token := strings.TrimSpace(protocols[1])
	return token, token != ""
}

func workflowActivityWebSocketCursor(r *http.Request) (int64, bool) {
	for key := range r.URL.Query() {
		if key != "after" {
			return 0, false
		}
	}
	raw := strings.TrimSpace(r.URL.Query().Get("after"))
	if raw == "" {
		return 0, true
	}
	after, err := strconv.ParseInt(raw, 10, 64)
	return after, err == nil && after >= 0
}

func checkWorkflowWebSocketOrigin(r *http.Request) bool {
	origins := r.Header.Values("Origin")
	if len(origins) != 1 {
		return false
	}
	origin, err := url.Parse(origins[0])
	if err != nil || origin.Scheme == "" || origin.Host == "" || origin.User != nil || origin.Opaque != "" ||
		origin.Path != "" || origin.RawPath != "" || origin.ForceQuery || origin.RawQuery != "" || origin.Fragment != "" {
		return false
	}
	scheme, ok := workflowRequestScheme(r)
	if !ok {
		return false
	}
	requestOrigin, err := url.Parse(scheme + "://" + r.Host)
	if err != nil || requestOrigin.Host == "" || requestOrigin.User != nil {
		return false
	}
	originPort, originPortOK := workflowOriginPort(origin)
	requestPort, requestPortOK := workflowOriginPort(requestOrigin)
	return originPortOK && requestPortOK && strings.EqualFold(origin.Scheme, requestOrigin.Scheme) &&
		strings.EqualFold(origin.Hostname(), requestOrigin.Hostname()) && originPort == requestPort
}

func workflowRequestScheme(r *http.Request) (string, bool) {
	if r.TLS != nil {
		return "https", true
	}
	forwarded := r.Header.Values("X-Forwarded-Proto")
	if len(forwarded) == 0 {
		return "http", true
	}
	if len(forwarded) != 1 {
		return "", false
	}
	scheme := strings.ToLower(strings.TrimSpace(forwarded[0]))
	return scheme, scheme == "http" || scheme == "https"
}

func workflowOriginPort(origin *url.URL) (string, bool) {
	if strings.HasSuffix(origin.Host, ":") {
		return "", false
	}
	if port := origin.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return "", false
		}
		return strconv.Itoa(value), true
	}
	if strings.EqualFold(origin.Scheme, "https") {
		return "443", true
	}
	return "80", true
}
