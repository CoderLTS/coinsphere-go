package service

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestHubConcurrentPushUsesSingleOrderedWriter(t *testing.T) {
	hub := NewHub()
	clientA := openRealtimeTestSocket(t, hub, 7, RealtimeEvent{Type: "notice.unread", Data: M{"unreadCount": 2}})
	clientB := openRealtimeTestSocket(t, hub, 7, RealtimeEvent{Type: "notice.unread", Data: M{"unreadCount": 2}})
	assertRealtimeEnvelope(t, readRealtimeEnvelope(t, clientA), "notice.unread", 1)
	assertRealtimeEnvelope(t, readRealtimeEnvelope(t, clientB), "notice.unread", 1)

	const pushes = 32
	start := make(chan struct{})
	results := make(chan bool, pushes)
	var senders sync.WaitGroup
	for id := 1; id <= pushes; id++ {
		senders.Add(1)
		go func(id int) {
			defer senders.Done()
			<-start
			results <- hub.SendToUser(7, RealtimeEvent{Type: "notice.created", Data: M{"id": id}})
		}(id)
	}
	close(start)
	senders.Wait()
	close(results)
	for accepted := range results {
		if !accepted {
			t.Fatal("concurrent notification was not accepted")
		}
	}

	seen := make(map[int]bool, pushes)
	for sequence := uint64(2); sequence <= pushes+1; sequence++ {
		envelopeA := readRealtimeEnvelope(t, clientA)
		envelopeB := readRealtimeEnvelope(t, clientB)
		assertRealtimeEnvelope(t, envelopeA, "notice.created", sequence)
		assertRealtimeEnvelope(t, envelopeB, "notice.created", sequence)
		if envelopeA.OccurredAt != envelopeB.OccurredAt || string(envelopeA.Data) != string(envelopeB.Data) {
			t.Fatalf("same event diverged between connections at sequence %d", sequence)
		}
		var data struct {
			ID int `json:"id"`
		}
		if err := json.Unmarshal(envelopeA.Data, &data); err != nil {
			t.Fatalf("decode event data: %v", err)
		}
		if data.ID < 1 || data.ID > pushes || seen[data.ID] {
			t.Fatalf("unexpected or duplicate event id %d", data.ID)
		}
		seen[data.ID] = true
	}
}

func TestHubConnectOrdersInitialSnapshotBeforeConcurrentPush(t *testing.T) {
	hub := NewHub()
	initialStarted := make(chan struct{})
	releaseInitial := make(chan struct{})
	connected := make(chan error, 1)
	serverDone := make(chan struct{})
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(serverDone)
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			connected <- err
			return
		}
		if !hub.Connect(8, conn, func() RealtimeEvent {
			close(initialStarted)
			<-releaseInitial
			return RealtimeEvent{Type: "notice.unread", Data: M{"unreadCount": 1}}
		}) {
			connected <- errors.New("hub rejected test connection")
			_ = conn.Close()
			return
		}
		connected <- nil
		defer hub.Disconnect(8, conn)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))

	client, _, err := websocket.DefaultDialer.Dial(strings.Replace(server.URL, "http", "ws", 1), nil)
	if err != nil {
		server.Close()
		t.Fatalf("dial realtime test socket: %v", err)
	}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseInitial) }) }
	defer release()
	t.Cleanup(func() {
		_ = client.Close()
		hub.CloseAll()
		server.Close()
		select {
		case <-serverDone:
		case <-time.After(2 * time.Second):
			t.Error("realtime test handler did not exit")
		}
	})

	<-initialStarted
	// 初始查询仅占用该用户闸门；全局在线状态查询必须保持可用。
	online := make(chan bool, 1)
	go func() { online <- hub.IsOnline(99) }()
	select {
	case got := <-online:
		if got {
			t.Fatal("unrelated user unexpectedly online")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("initial snapshot blocked the global Hub lock")
	}

	sendStarted := make(chan struct{})
	sent := make(chan bool, 1)
	go func() {
		close(sendStarted)
		sent <- hub.SendToUser(8, RealtimeEvent{Type: "notice.created", Data: M{"id": 1}})
	}()
	<-sendStarted
	select {
	case <-sent:
		t.Fatal("concurrent push passed the user gate before the initial snapshot")
	case <-time.After(50 * time.Millisecond):
	}

	release()
	if err := <-connected; err != nil {
		t.Fatalf("connect realtime test socket: %v", err)
	}
	if accepted := <-sent; !accepted {
		t.Fatal("concurrent push was not accepted after connection publication")
	}
	assertRealtimeEnvelope(t, readRealtimeEnvelope(t, client), "notice.unread", 1)
	assertRealtimeEnvelope(t, readRealtimeEnvelope(t, client), "notice.created", 2)
}

func TestHubBackpressureDropsOnlySlowClient(t *testing.T) {
	settings := defaultRealtimeSettings()
	settings.sendQueueSize = 1
	hub := newHub(settings)
	t.Cleanup(hub.CloseAll)

	slow := newRealtimeClient(9, nil, settings.sendQueueSize)
	fast := newRealtimeClient(9, nil, settings.sendQueueSize)
	queued, ok := prepareRealtimeMessage(RealtimeEvent{Type: "notice.created", Data: M{"id": 1}})
	if !ok || !slow.enqueue(queued) {
		t.Fatal("failed to prime slow client queue")
	}
	hub.connections[9] = map[*realtimeClient]struct{}{slow: {}, fast: {}}

	startedAt := time.Now()
	if !hub.SendToUser(9, RealtimeEvent{Type: "notice.created", Data: M{"id": 2}}) {
		t.Fatal("healthy client did not accept event")
	}
	if elapsed := time.Since(startedAt); elapsed > 100*time.Millisecond {
		t.Fatalf("backpressure blocked producer for %s", elapsed)
	}
	select {
	case <-slow.done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("slow client was not closed after its queue filled")
	}
	select {
	case message := <-fast.send:
		if message.eventType != "notice.created" {
			t.Fatalf("healthy client received type %q", message.eventType)
		}
	default:
		t.Fatal("healthy client did not receive event while slow peer was removed")
	}
	if !hub.IsOnline(9) {
		t.Fatal("healthy client was removed with slow peer")
	}
}

func TestHubWriterExitRejectsEventsBeforeRegistryRemoval(t *testing.T) {
	hub := NewHub()
	client := openRealtimeTestSocket(t, hub, 10, RealtimeEvent{Type: "notice.unread", Data: M{"unreadCount": 0}})
	assertRealtimeEnvelope(t, readRealtimeEnvelope(t, client), "notice.unread", 1)

	hub.mu.Lock()
	var target *realtimeClient
	for candidate := range hub.connections[10] {
		target = candidate
		break
	}
	if target == nil {
		hub.mu.Unlock()
		t.Fatal("test connection was not registered")
	}
	_ = target.conn.Close()
	message, ok := prepareRealtimeMessage(RealtimeEvent{Type: "notice.created", Data: M{"id": 1}})
	if !ok || !target.enqueue(message) {
		hub.mu.Unlock()
		t.Fatal("failed to trigger writer exit")
	}
	select {
	case <-target.done:
		// writer 已停止但仍被测试持有的 Hub 锁挡在连接表摘除之前。
	case <-time.After(time.Second):
		hub.mu.Unlock()
		t.Fatal("writer exit did not mark client stopped before registry removal")
	}

	sendStarted := make(chan struct{})
	accepted := make(chan bool, 1)
	go func() {
		close(sendStarted)
		accepted <- hub.SendToUser(10, RealtimeEvent{Type: "notice.created", Data: M{"id": 2}})
	}()
	<-sendStarted
	hub.mu.Unlock()
	if <-accepted {
		t.Fatal("event was accepted after the only writer had exited")
	}
}

func TestHubHeartbeatKeepsResponsiveClientAndExpiresSilentClient(t *testing.T) {
	settings := defaultRealtimeSettings()
	settings.writeWait = 200 * time.Millisecond
	settings.pongWait = 500 * time.Millisecond
	settings.pingPeriod = 100 * time.Millisecond

	t.Run("pong extends read deadline", func(t *testing.T) {
		hub := newHub(settings)
		client := openRealtimeTestSocket(t, hub, 11, RealtimeEvent{Type: "notice.unread", Data: M{"unreadCount": 0}})
		assertRealtimeEnvelope(t, readRealtimeEnvelope(t, client), "notice.unread", 1)
		_ = client.SetReadDeadline(time.Time{})

		events := make(chan realtimeEnvelope, 1)
		readErrors := make(chan error, 1)
		go func() {
			for {
				_, raw, err := client.ReadMessage()
				if err != nil {
					readErrors <- err
					return
				}
				var envelope realtimeEnvelope
				if err := json.Unmarshal(raw, &envelope); err != nil {
					readErrors <- err
					return
				}
				events <- envelope
			}
		}()

		time.Sleep(3 * settings.pongWait)
		if !hub.IsOnline(11) {
			t.Fatal("responsive client expired despite automatic pong frames")
		}
		if !hub.SendToUser(11, RealtimeEvent{Type: "notice.created", Data: M{"id": 1}}) {
			t.Fatal("responsive client did not accept event after heartbeat window")
		}
		select {
		case envelope := <-events:
			// 多轮 Ping 不占业务序号，因此下一条通知仍必须是 sequence=2。
			assertRealtimeEnvelope(t, envelope, "notice.created", 2)
		case err := <-readErrors:
			t.Fatalf("responsive client read failed: %v", err)
		case <-time.After(time.Second):
			t.Fatal("responsive client did not receive event")
		}
	})

	t.Run("missing pong closes connection", func(t *testing.T) {
		hub := newHub(settings)
		client := openRealtimeTestSocket(t, hub, 12, RealtimeEvent{Type: "notice.unread", Data: M{"unreadCount": 0}})
		assertRealtimeEnvelope(t, readRealtimeEnvelope(t, client), "notice.unread", 1)

		deadline := time.Now().Add(3 * time.Second)
		for hub.IsOnline(12) && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if hub.IsOnline(12) {
			t.Fatal("silent client remained online after pong deadline")
		}
	})
}

func TestHubCloseAllStopsWriterAndRejectsLateConnection(t *testing.T) {
	hub := NewHub()
	client := openRealtimeTestSocket(t, hub, 21, RealtimeEvent{Type: "notice.unread", Data: M{"unreadCount": 0}})
	assertRealtimeEnvelope(t, readRealtimeEnvelope(t, client), "notice.unread", 1)

	closed := make(chan struct{})
	go func() {
		hub.CloseAll()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("CloseAll did not wait for and stop writer")
	}
	if hub.IsOnline(21) {
		t.Fatal("closed hub still reports client online")
	}
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := client.ReadMessage(); err == nil {
		t.Fatal("client connection remained readable after CloseAll")
	}

	accepted := make(chan bool, 1)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			accepted <- false
			return
		}
		ok := hub.Connect(22, conn, func() RealtimeEvent {
			return RealtimeEvent{Type: "notice.unread", Data: M{"unreadCount": 0}}
		})
		accepted <- ok
		if !ok {
			_ = conn.Close()
		}
	}))
	defer server.Close()
	lateClient, _, err := websocket.DefaultDialer.Dial(strings.Replace(server.URL, "http", "ws", 1), nil)
	if err != nil {
		t.Fatalf("dial late connection: %v", err)
	}
	defer lateClient.Close()
	if <-accepted {
		t.Fatal("closed hub accepted a valid late WebSocket connection")
	}
}

func openRealtimeTestSocket(t *testing.T, hub *Hub, userID int64, initial RealtimeEvent) *websocket.Conn {
	t.Helper()
	connected := make(chan error, 1)
	serverDone := make(chan struct{})
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(serverDone)
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			connected <- err
			return
		}
		if !hub.Connect(userID, conn, func() RealtimeEvent { return initial }) {
			connected <- errors.New("hub rejected test connection")
			_ = conn.Close()
			return
		}
		connected <- nil
		defer hub.Disconnect(userID, conn)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))

	client, _, err := websocket.DefaultDialer.Dial(strings.Replace(server.URL, "http", "ws", 1), nil)
	if err != nil {
		server.Close()
		t.Fatalf("dial realtime test socket: %v", err)
	}
	if err := <-connected; err != nil {
		_ = client.Close()
		server.Close()
		t.Fatalf("connect realtime test socket: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		hub.CloseAll()
		server.Close()
		select {
		case <-serverDone:
		case <-time.After(2 * time.Second):
			t.Error("realtime test handler did not exit")
		}
	})
	return client
}

func readRealtimeEnvelope(t *testing.T, conn *websocket.Conn) realtimeEnvelope {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	messageType, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read realtime envelope: %v", err)
	}
	if messageType != websocket.TextMessage {
		t.Fatalf("message type = %d, want text", messageType)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("decode realtime fields: %v", err)
	}
	for _, name := range []string{"type", "version", "sequence", "occurredAt", "data"} {
		if _, ok := fields[name]; !ok {
			t.Fatalf("realtime envelope missing %q: %s", name, raw)
		}
	}
	if len(fields) != 5 {
		t.Fatalf("realtime envelope has extra fields: %s", raw)
	}
	var envelope realtimeEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("decode realtime envelope: %v", err)
	}
	return envelope
}

func assertRealtimeEnvelope(t *testing.T, envelope realtimeEnvelope, eventType string, sequence uint64) {
	t.Helper()
	if envelope.Type != eventType || envelope.Version != realtimeEventVersion || envelope.Sequence != sequence {
		t.Fatalf("envelope = type:%s version:%d sequence:%d", envelope.Type, envelope.Version, envelope.Sequence)
	}
	occurredAt, err := time.Parse(time.RFC3339Nano, envelope.OccurredAt)
	if err != nil || occurredAt.Location() != time.UTC || !strings.HasSuffix(envelope.OccurredAt, "Z") {
		t.Fatalf("occurredAt %q is not UTC RFC3339Nano: %v", envelope.OccurredAt, err)
	}
}
