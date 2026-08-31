package api

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

const notificationWSProtocol = "coinsphere.notifications.v1"

var notificationWSUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     checkNotificationWebSocketOrigin,
	Subprotocols:    []string{notificationWSProtocol},
}

type notificationWSEnvelope struct {
	Type       string `json:"type"`
	Version    int    `json:"version"`
	Sequence   uint64 `json:"sequence"`
	OccurredAt string `json:"occurredAt"`
	Data       M      `json:"data"`
}

func (s *Server) handleListInAppNotifications(c *gin.Context) {
	page, ok := cursorPage(c)
	if !ok {
		return
	}
	data, err := s.App.ListInAppNotifications(c.Request.Context(), currentPrincipal(c).User.ID, page)
	respond(c, data, err, "")
}

func (s *Server) handleReadInAppNotification(c *gin.Context) {
	deliveryID, err := pathInt64(c, "deliveryId")
	if err != nil {
		writeProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	unreadCount, err := s.App.MarkInAppRead(c.Request.Context(), currentPrincipal(c).User.ID, deliveryID)
	respond(c, M{"unreadCount": unreadCount}, err, "")
}

func (s *Server) handleReadAllInAppNotifications(c *gin.Context) {
	data, err := s.App.MarkAllInAppRead(c.Request.Context(), currentPrincipal(c).User.ID)
	respond(c, data, err, "")
}

func (s *Server) handleNotificationWebSocket(c *gin.Context) {
	token, ok := notificationWebSocketToken(c.Request)
	if !ok {
		writeProblem(c, http.StatusUnauthorized, "invalid websocket authentication")
		return
	}
	principal, err := s.App.AuthenticateAccessToken(token)
	if err != nil || principal == nil {
		writeProblem(c, http.StatusUnauthorized, "invalid access token")
		return
	}
	connection, err := notificationWSUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	events, unsubscribe := s.App.SubscribeNotificationEvents(c.Request.Context(), principal.User.ID)
	defer unsubscribe()
	defer connection.Close()
	const pongWait = 70 * time.Second
	connection.SetReadLimit(1024)
	_ = connection.SetReadDeadline(time.Now().Add(pongWait))
	connection.SetPongHandler(func(string) error {
		return connection.SetReadDeadline(time.Now().Add(pongWait))
	})
	done := make(chan struct{})
	defer close(done)
	go func() {
		defer connection.Close()
		ticker := time.NewTicker(54 * time.Second)
		defer ticker.Stop()
		var sequence uint64
		for {
			select {
			case <-done:
				return
			case event, open := <-events:
				if !open {
					return
				}
				sequence++
				_ = connection.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := connection.WriteJSON(notificationWSEnvelope{
					Type: event.Type, Version: 1, Sequence: sequence,
					OccurredAt: time.Now().UTC().Format(time.RFC3339Nano), Data: event.Data,
				}); err != nil {
					return
				}
			case <-ticker.C:
				if err := connection.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)); err != nil {
					return
				}
			}
		}
	}()
	for {
		if _, _, err := connection.ReadMessage(); err != nil {
			return
		}
	}
}

func notificationWebSocketToken(r *http.Request) (string, bool) {
	if r.URL.RawQuery != "" || len(r.Header.Values("Sec-WebSocket-Protocol")) != 1 {
		return "", false
	}
	protocols := websocket.Subprotocols(r)
	if len(protocols) != 2 || protocols[0] != notificationWSProtocol {
		return "", false
	}
	token := strings.TrimSpace(protocols[1])
	return token, token != ""
}

func checkNotificationWebSocketOrigin(r *http.Request) bool {
	origins := r.Header.Values("Origin")
	if len(origins) != 1 {
		return false
	}
	origin, err := url.Parse(origins[0])
	if err != nil || origin.Scheme != "http" && origin.Scheme != "https" || origin.Host == "" ||
		origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	} else if forwarded := r.Header.Values("X-Forwarded-Proto"); len(forwarded) == 1 {
		scheme = strings.ToLower(strings.TrimSpace(forwarded[0]))
		if scheme != "http" && scheme != "https" {
			return false
		}
	} else if len(r.Header.Values("X-Forwarded-Proto")) > 1 {
		return false
	}
	requestOrigin, err := url.Parse(scheme + "://" + r.Host)
	if err != nil || requestOrigin.Host == "" {
		return false
	}
	originPort, originPortOK := notificationOriginPort(origin)
	requestPort, requestPortOK := notificationOriginPort(requestOrigin)
	return originPortOK && requestPortOK &&
		strings.EqualFold(origin.Scheme, requestOrigin.Scheme) &&
		strings.EqualFold(origin.Hostname(), requestOrigin.Hostname()) &&
		originPort == requestPort
}

func notificationOriginPort(origin *url.URL) (string, bool) {
	if port := origin.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return "", false
		}
		return strconv.Itoa(value), true
	}
	if origin.Scheme == "https" {
		return "443", true
	}
	return "80", origin.Scheme == "http"
}
