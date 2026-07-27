package service

import (
	"sync"

	"github.com/gorilla/websocket"
)

// Hub 按用户聚合的 WebSocket 连接管理器。
type Hub struct {
	mu          sync.Mutex
	connections map[int64]map[*websocket.Conn]bool
}

// NewHub 创建连接管理器。
func NewHub() *Hub {
	return &Hub{connections: map[int64]map[*websocket.Conn]bool{}}
}

// Connect 注册用户连接。
func (h *Hub) Connect(userID int64, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.connections[userID] == nil {
		h.connections[userID] = map[*websocket.Conn]bool{}
	}
	h.connections[userID][conn] = true
}

// Disconnect 移除用户连接。
func (h *Hub) Disconnect(userID int64, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	sockets := h.connections[userID]
	if sockets == nil {
		return
	}
	delete(sockets, conn)
	if len(sockets) == 0 {
		delete(h.connections, userID)
	}
}

// IsOnline 用户是否有活跃连接。
func (h *Hub) IsOnline(userID int64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.connections[userID]) > 0
}

// SendToUser 向用户全部连接推送 JSON 消息,任一成功即返回 true。
func (h *Hub) SendToUser(userID int64, payload M) bool {
	h.mu.Lock()
	sockets := make([]*websocket.Conn, 0, len(h.connections[userID]))
	for conn := range h.connections[userID] {
		sockets = append(sockets, conn)
	}
	h.mu.Unlock()
	if len(sockets) == 0 {
		return false
	}
	message := []byte(dumpJSON(payload))
	success := false
	for _, conn := range sockets {
		if err := conn.WriteMessage(websocket.TextMessage, message); err != nil {
			h.Disconnect(userID, conn)
			continue
		}
		success = true
	}
	return success
}
