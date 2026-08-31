package api

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// rateLimiter 进程内按 key(客户端 IP)的固定窗口限流,用于挡住登录暴力尝试(见评审 #6)。
// ponytail: 单实例内存实现,重启即清零、不跨实例共享;多实例部署需换成 Redis 等集中式限流。
// ponytail: counts 无主动淘汰,登录来源 IP 有限、过期窗口会被下次访问覆盖,故可接受;IP 面极大时再加定期清扫。
type rateLimiter struct {
	mu     sync.Mutex
	counts map[string]*hitWindow
	limit  int
	window time.Duration
}

// hitWindow 记录某个 key 当前窗口内的计数与窗口截止时刻。
type hitWindow struct {
	count int
	reset time.Time
}

func newRateLimiter(limitPerMinute int) *rateLimiter {
	if limitPerMinute <= 0 {
		limitPerMinute = 10
	}
	return &rateLimiter{counts: map[string]*hitWindow{}, limit: limitPerMinute, window: time.Minute}
}

// allow 记录一次访问:窗口内首次或窗口已过则重置计数并放行;否则计数 +1,超过上限返回 false。
// 用 mu 加锁保证多 goroutine(每个请求一个)并发读写 counts 时安全。见 GO入门笔记『并发』。
func (rl *rateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	w := rl.counts[key]
	if w == nil || now.After(w.reset) {
		rl.counts[key] = &hitWindow{count: 1, reset: now.Add(rl.window)}
		return true
	}
	if w.count >= rl.limit {
		return false
	}
	w.count++
	return true
}

// clientIP 取真实客户端 IP:经 nginx 反代时优先取 X-Forwarded-For 的第一个,否则用 RemoteAddr。
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// rateLimit 给登录接口套一层限流:同一 IP 在窗口内尝试过多返回 429。
func (s *Server) rateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !s.loginLimiter.allow(clientIP(c.Request)) {
			failStatus(c, http.StatusTooManyRequests, "尝试过于频繁,请稍后再试")
			c.Abort()
			return
		}
		c.Next()
	}
}
