package service

import (
	// sync:标准库并发工具包。本文件用它的 Mutex(互斥锁)保护共享数据。见 GO入门笔记『并发』
	"sync"

	// gorilla/websocket:第三方 WebSocket 库(go.mod 里 require 的)。
	// HTTP 是"一问一答"、答完即断;WebSocket 是"长连接、双向、随时推送"。
	// 浏览器先发一个带特殊头的普通 HTTP 请求,服务端用 websocket.Upgrader 把这次连接"升级"成 WebSocket
	// (升级动作在 API 层完成),之后得到的 *websocket.Conn 就代表这条长连接,可反复读写。
	// 本文件不做升级,只集中管理"已经建立好的连接"。见 GO入门笔记『框架/并发』
	"github.com/gorilla/websocket"
)

// —— 本文件:WebSocket 连接注册表(Hub)——
// 通知要"实时"推给在线用户,就得记住"每个用户当前开着哪些 WebSocket 连接"。
// Hub 就是这张注册表 + 一套线程安全的增删查推方法;它被 App 持有(见 app.go 的 App.Hub),
// notify.go 里的 a.Hub.SendToUser(...) 就是通过它把站内通知实时推到前端。

// Hub 按用户聚合的 WebSocket 连接管理器。
// type ... struct 定义"结构体"(把若干字段打包成一个类型),是本文件第一个 struct。见 GO入门笔记『复合类型』
// 两个字段配合使用:
//   mu          —— 互斥锁。多个 goroutine(各连接的收发、后台推送)会同时读写 connections,
//                  而 Go 的 map 并发读写会直接崩溃;加锁 = 同一时刻只放一个 goroutine 进来改,保证安全。
//                  sync.Mutex 是本文件第一次出现锁。见 GO入门笔记『并发』
//   connections —— 连接注册表,是"嵌套 map":外层键=用户ID(int64),值又是一个 map;
//                  内层键=该用户的一条连接(*websocket.Conn 指针,多端登录就有多条),值恒为 true。
//                  内层 map 当"集合(set)"用,只关心连接在不在,bool 仅占位。map 见 GO入门笔记『复合类型』
type Hub struct {
	mu          sync.Mutex
	connections map[int64]map[*websocket.Conn]bool
}

// NewHub 创建连接管理器。
// 返回 *Hub(指向 Hub 的指针):Go 惯例用 NewXxx 当"构造器",返回指针可避免复制、并让各处共享同一实例。
// &Hub{...} 里的 & 是"取地址"得到指针;map 必须先初始化(这里用字面量建好空的外层表)才能写入。见 GO入门笔记『复合类型』
func NewHub() *Hub {
	return &Hub{connections: map[int64]map[*websocket.Conn]bool{}}
}

// Connect 注册用户连接。
// (h *Hub) 叫"方法接收者":表示这是挂在 Hub 上的方法,方法内用 h 指代当前 Hub(类似别的语言的 this/self)。
// 这是本文件第一个方法。见 GO入门笔记『方法与接收者』
func (h *Hub) Connect(userID int64, conn *websocket.Conn) {
	// 先加锁;defer 登记"函数返回前自动解锁",这样无论从哪条分支 return 都不会漏解锁。defer 见 GO入门笔记『defer』
	h.mu.Lock()
	defer h.mu.Unlock()
	// 该用户第一次连进来时,内层 map 还是 nil(空);向 nil map 写入会 panic(崩溃),所以要先建好。
	if h.connections[userID] == nil {
		h.connections[userID] = map[*websocket.Conn]bool{}
	}
	// 把这条连接放入该用户的连接集合(值 true 仅占位)。
	h.connections[userID][conn] = true
}

// Disconnect 移除用户连接。
func (h *Hub) Disconnect(userID int64, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	// := 是"短变量声明":自动推断类型并新建变量(只能在函数内用)。见 GO入门笔记『变量』
	// 读一个不存在的键会得到该类型的"零值",map 的零值是 nil。
	sockets := h.connections[userID]
	if sockets == nil {
		return
	}
	// delete(map, key):从 map 删除一个键;这里移除这一条具体连接。
	delete(sockets, conn)
	// 若该用户已无任何连接,顺手把外层 map 里的这个用户也删掉,避免残留空表。len 取长度。
	if len(sockets) == 0 {
		delete(h.connections, userID)
	}
}

// IsOnline 用户是否有活跃连接。
func (h *Hub) IsOnline(userID int64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	// 连接数 > 0 即在线;用户不存在时 len(nil) 为 0,自然返回 false。notify.go 靠它判断"离线跳过"。
	return len(h.connections[userID]) > 0
}

// SendToUser 向用户全部连接推送 JSON 消息,任一成功即返回 true。
// 这是"实时通知"的出口:notify.go 生成通知后调用它,把消息推到该用户所有在线的浏览器标签页。
func (h *Hub) SendToUser(userID int64, payload M) bool {
	// 【并发关键】先在锁内把该用户的连接"拍快照"复制成一个 slice(切片),然后立刻解锁,之后再脱离锁去发送。
	// 原因:下面的 WriteMessage 要走网络、可能很慢甚至阻塞;若整段都占着锁,其它 goroutine(新连接注册、
	// 断开、别的推送)就全被卡死。所以套路是"锁内快照 → 解锁 → 锁外慢慢发"。见 GO入门笔记『并发』
	h.mu.Lock()
	// make([]T, 0, n):新建长度 0、预留容量 n 的切片;预留容量可减少后续 append 扩容。slice 见 GO入门笔记『复合类型』
	sockets := make([]*websocket.Conn, 0, len(h.connections[userID]))
	// range 遍历 map:这里只要键(每条连接),忽略值。for 是 Go 唯一的循环关键字。见 GO入门笔记『复合类型』
	for conn := range h.connections[userID] {
		// append 往切片追加元素并返回新切片,必须赋回原变量。
		sockets = append(sockets, conn)
	}
	h.mu.Unlock()
	if len(sockets) == 0 {
		return false
	}
	// 把 payload 序列化成 JSON 文本,再转成字节切片 []byte 供发送(WebSocket 收发的是字节)。
	message := []byte(dumpJSON(payload))
	success := false
	// for i, v := range slice 遍历切片;这里用 _ 丢弃下标,只取每条连接 conn。
	for _, conn := range sockets {
		// if err := 表达式; err != nil { ... } 是最典型的错误处理:先执行并接住 err,再判断是否出错。见 GO入门笔记『错误处理』
		// conn.WriteMessage 把一帧文本(TextMessage)写到这条 WebSocket 连接上。
		if err := conn.WriteMessage(websocket.TextMessage, message); err != nil {
			// 写失败通常代表这条连接已经断了:顺手清理掉,再 continue 跳到下一条继续发。
			h.Disconnect(userID, conn)
			continue
		}
		success = true
	}
	return success
}
