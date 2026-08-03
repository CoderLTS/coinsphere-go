package service

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const realtimeEventVersion = 1

type RealtimeEvent struct {
	Type string
	Data any
}

type realtimeEnvelope struct {
	Type       string          `json:"type"`
	Version    int             `json:"version"`
	Sequence   uint64          `json:"sequence"`
	OccurredAt string          `json:"occurredAt"`
	Data       json.RawMessage `json:"data"`
}

type realtimeMessage struct {
	eventType  string
	occurredAt string
	data       json.RawMessage
}

type realtimeSettings struct {
	sendQueueSize int
	writeWait     time.Duration
	pongWait      time.Duration
	pingPeriod    time.Duration
	readLimit     int64
}

func defaultRealtimeSettings() realtimeSettings {
	return realtimeSettings{
		sendQueueSize: 64,
		writeWait:     10 * time.Second,
		pongWait:      60 * time.Second,
		pingPeriod:    54 * time.Second,
		readLimit:     1024,
	}
}

// Hub 按用户管理通知 WebSocket。共享锁只保护连接表，网络写入全部由每连接唯一的 writer 执行。
type Hub struct {
	mu          sync.Mutex
	connections map[int64]map[*realtimeClient]struct{}
	closed      bool
	settings    realtimeSettings
	writers     sync.WaitGroup
	userGates   sync.Map
}

type realtimeClient struct {
	userID int64
	conn   *websocket.Conn
	send   chan realtimeMessage
	done   chan struct{}

	mu      sync.Mutex
	stopped bool
}

func NewHub() *Hub {
	return newHub(defaultRealtimeSettings())
}

func newHub(settings realtimeSettings) *Hub {
	return &Hub{
		connections: map[int64]map[*realtimeClient]struct{}{},
		settings:    settings,
	}
}

func newRealtimeClient(userID int64, conn *websocket.Conn, queueSize int) *realtimeClient {
	return &realtimeClient{
		userID: userID,
		conn:   conn,
		send:   make(chan realtimeMessage, queueSize),
		done:   make(chan struct{}),
	}
}

// userGate 让同一用户的连接快照与通知入队共享线性顺序；闸门在 Hub 生命周期内保留，避免删除重建造成锁身份竞态。
func (h *Hub) userGate(userID int64) *sync.Mutex {
	gate, _ := h.userGates.LoadOrStore(userID, &sync.Mutex{})
	return gate.(*sync.Mutex)
}

func (c *realtimeClient) enqueue(message realtimeMessage) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped {
		return false
	}
	select {
	case c.send <- message:
		return true
	default:
		return false
	}
}

func prepareRealtimeMessage(event RealtimeEvent) (realtimeMessage, bool) {
	data, err := json.Marshal(event.Data)
	if err != nil {
		return realtimeMessage{}, false
	}
	return realtimeMessage{
		eventType:  event.Type,
		occurredAt: time.Now().UTC().Format(time.RFC3339Nano),
		data:       data,
	}, true
}

func (c *realtimeClient) stop() {
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return
	}
	c.stopped = true
	close(c.done)
	c.mu.Unlock()
	if c.conn != nil {
		_ = c.conn.Close()
	}
}

// Connect 在同一锁序中生成初始快照、预入队并公开连接，保证并发通知只能排在 sequence=1 之后。
func (h *Hub) Connect(userID int64, conn *websocket.Conn, initial func() RealtimeEvent) bool {
	h.mu.Lock()
	closed := h.closed
	h.mu.Unlock()
	if closed || conn == nil || initial == nil {
		return false
	}

	conn.SetReadLimit(h.settings.readLimit)
	if err := conn.SetReadDeadline(time.Now().Add(h.settings.pongWait)); err != nil {
		return false
	}
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(h.settings.pongWait))
	})

	client := newRealtimeClient(userID, conn, h.settings.sendQueueSize)
	gate := h.userGate(userID)
	gate.Lock()
	defer gate.Unlock()

	initialMessage, ok := prepareRealtimeMessage(initial())
	if !ok || !client.enqueue(initialMessage) {
		client.stop()
		return false
	}

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		client.stop()
		return false
	}
	if h.connections[userID] == nil {
		h.connections[userID] = map[*realtimeClient]struct{}{}
	}
	h.connections[userID][client] = struct{}{}
	h.writers.Add(1)
	h.mu.Unlock()

	go h.writeLoop(client)
	return true
}

// Disconnect 摘除并关闭指定连接；重复调用是安全的。
func (h *Hub) Disconnect(userID int64, conn *websocket.Conn) {
	var target *realtimeClient
	h.mu.Lock()
	for client := range h.connections[userID] {
		if client.conn == conn {
			target = client
			delete(h.connections[userID], client)
			break
		}
	}
	if len(h.connections[userID]) == 0 {
		delete(h.connections, userID)
	}
	h.mu.Unlock()
	if target != nil {
		target.stop()
	}
}

func (h *Hub) removeClient(client *realtimeClient) {
	// writer 一旦退出，先拒绝后续入队，再等待连接表锁完成摘除，避免把无人消费的事件报告为已接受。
	client.stop()
	h.mu.Lock()
	clients := h.connections[client.userID]
	delete(clients, client)
	if len(clients) == 0 {
		delete(h.connections, client.userID)
	}
	h.mu.Unlock()
}

// CloseAll 阻止晚注册、关闭升级连接并等待 writer 退出；http.Server.Shutdown 不会接管 WebSocket。
func (h *Hub) CloseAll() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		h.writers.Wait()
		return
	}
	h.closed = true
	clients := make([]*realtimeClient, 0)
	for _, userClients := range h.connections {
		for client := range userClients {
			clients = append(clients, client)
		}
	}
	h.connections = map[int64]map[*realtimeClient]struct{}{}
	h.mu.Unlock()
	log.Printf("[realtime] hub closing: client_count=%d", len(clients))

	for _, client := range clients {
		client.stop()
	}
	h.writers.Wait()
}

func (h *Hub) IsOnline(userID int64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.connections[userID]) > 0
}

// SendToUser 只做非阻塞入队。队列满表示客户端已经落后，立即摘除，避免拖住生产者和其他连接。
func (h *Hub) SendToUser(userID int64, event RealtimeEvent) bool {
	message, ok := prepareRealtimeMessage(event)
	if !ok {
		return false
	}
	gate := h.userGate(userID)
	gate.Lock()
	defer gate.Unlock()

	dropped := make([]*realtimeClient, 0)
	accepted := false
	h.mu.Lock()
	for client := range h.connections[userID] {
		if client.enqueue(message) {
			accepted = true
			continue
		}
		delete(h.connections[userID], client)
		dropped = append(dropped, client)
	}
	if len(h.connections[userID]) == 0 {
		delete(h.connections, userID)
	}
	h.mu.Unlock()

	for _, client := range dropped {
		client.stop()
		log.Printf("[realtime] slow client disconnected: reason=send_queue_full")
	}
	return accepted
}

// writeLoop 是连接的唯一写协程：业务帧与控制帧都不允许从处理器或通知生产者直接写入。
func (h *Hub) writeLoop(client *realtimeClient) {
	defer h.writers.Done()
	defer h.removeClient(client)

	ticker := time.NewTicker(h.settings.pingPeriod)
	defer ticker.Stop()
	var sequence uint64

	for {
		select {
		case <-client.done:
			return
		case message := <-client.send:
			nextSequence := sequence + 1
			envelope := realtimeEnvelope{
				Type:       message.eventType,
				Version:    realtimeEventVersion,
				Sequence:   nextSequence,
				OccurredAt: message.occurredAt,
				Data:       message.data,
			}
			if err := client.conn.SetWriteDeadline(time.Now().Add(h.settings.writeWait)); err != nil {
				return
			}
			if err := client.conn.WriteJSON(envelope); err != nil {
				return
			}
			sequence = nextSequence
		case <-ticker.C:
			deadline := time.Now().Add(h.settings.writeWait)
			if err := client.conn.WriteControl(websocket.PingMessage, nil, deadline); err != nil {
				return
			}
		}
	}
}
