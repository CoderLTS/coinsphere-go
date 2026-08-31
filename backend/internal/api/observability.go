package api

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"coinsphere/backend/internal/service"
	"github.com/gin-gonic/gin"
)

const (
	requestIDHeader          = "X-Request-ID"
	requestStateContextKey   = "requestState"
	responseFailedContextKey = "responseFailed"
)

type requestState struct {
	requestID   string
	actorUserID int64
}

type httpMetrics struct {
	startedAt          time.Time
	requestsTotal      atomic.Uint64
	requestsFailed     atomic.Uint64
	requestsInFlight   atomic.Int64
	auditWriteFailures atomic.Uint64
	bucketsMu          sync.Mutex
	buckets            [60]httpMetricBucket
}

type httpMetricBucket struct {
	minute         int64
	requests       uint64
	failed         uint64
	durationMillis uint64
}

func (s *Server) observe() gin.HandlerFunc {
	return func(c *gin.Context) {
		startedAt := time.Now()
		state := &requestState{requestID: incomingRequestID(c.GetHeader(requestIDHeader))}
		c.Set(requestStateContextKey, state)
		c.Header(requestIDHeader, state.requestID)

		s.metrics.requestsTotal.Add(1)
		s.metrics.requestsInFlight.Add(1)
		defer s.metrics.requestsInFlight.Add(-1)

		c.Next()
		statusCode := c.Writer.Status()
		markedFailed, _ := c.Get(responseFailedContextKey)
		failed := markedFailed == true || statusCode >= http.StatusBadRequest
		if failed {
			s.metrics.requestsFailed.Add(1)
		}
		duration := time.Since(startedAt)
		s.metrics.observeBucket(startedAt, failed, duration)

		pattern := routePattern(c)
		route := strings.TrimPrefix(pattern, c.Request.Method+" ")
		if route == "" {
			route = "unmatched"
		}
		if isMutation(c.Request.Method) && pattern != "" {
			s.recordAudit(c, state, pattern, statusCode, failed)
		}

		attrs := []any{
			"component", "http.access",
			"request_id", state.requestID,
			"method", c.Request.Method,
			"route", route,
			"status", statusCode,
			"duration_ms", duration.Milliseconds(),
		}
		if state.actorUserID > 0 {
			attrs = append(attrs, "user_id", state.actorUserID)
		}
		if failed {
			slog.WarnContext(c.Request.Context(), "http request completed", attrs...)
			return
		}
		slog.InfoContext(c.Request.Context(), "http request completed", attrs...)
	}
}

func (m *httpMetrics) observeBucket(startedAt time.Time, failed bool, duration time.Duration) {
	minute := startedAt.UTC().Unix() / 60
	index := int(minute % int64(len(m.buckets)))
	m.bucketsMu.Lock()
	bucket := &m.buckets[index]
	if bucket.minute != minute {
		*bucket = httpMetricBucket{minute: minute}
	}
	bucket.requests++
	if failed {
		bucket.failed++
	}
	millis := duration.Milliseconds()
	if millis < 0 {
		millis = 0
	}
	bucket.durationMillis += uint64(millis)
	m.bucketsMu.Unlock()
}

func (m *httpMetrics) snapshot() M {
	now := time.Now().UTC()
	currentMinute := now.Unix() / 60
	trend := make([]M, 0, len(m.buckets))
	m.bucketsMu.Lock()
	for offset := int64(len(m.buckets) - 1); offset >= 0; offset-- {
		minute := currentMinute - offset
		bucket := m.buckets[int(minute%int64(len(m.buckets)))]
		requests, failed, latency := uint64(0), uint64(0), float64(0)
		if bucket.minute == minute {
			requests, failed = bucket.requests, bucket.failed
			if requests > 0 {
				latency = float64(bucket.durationMillis) / float64(requests)
			}
		}
		trend = append(trend, M{
			"time":     time.Unix(minute*60, 0).UTC().Format(time.RFC3339),
			"requests": requests, "failed": failed, "averageLatencyMs": latency,
		})
	}
	m.bucketsMu.Unlock()
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	return M{
		"process": M{
			"uptimeSeconds":      int64(time.Since(m.startedAt).Seconds()),
			"goMemoryAllocBytes": memory.Alloc, "goMemorySysBytes": memory.Sys,
			"goroutines": runtime.NumGoroutine(),
		},
		"http": M{
			"requestsTotal": m.requestsTotal.Load(), "requestsFailed": m.requestsFailed.Load(),
			"requestsInFlight": m.requestsInFlight.Load(), "trend": trend,
		},
	}
}

func (s *Server) recordAudit(c *gin.Context, state *requestState, pattern string, statusCode int, failed bool) {
	resourcePath := c.Request.URL.EscapedPath()
	if len(resourcePath) > 500 {
		resourcePath = resourcePath[:500]
	}
	var actorUserID *int64
	if state.actorUserID > 0 {
		actorUserID = &state.actorUserID
	}
	outcome := "success"
	if failed {
		outcome = "failure"
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 2*time.Second)
	defer cancel()
	if err := s.App.RecordAudit(ctx, service.AuditRecordInput{
		RequestID: state.requestID, ActorUserID: actorUserID,
		Action: pattern, ResourcePath: resourcePath,
		Outcome: outcome, StatusCode: statusCode,
	}); err != nil {
		s.metrics.auditWriteFailures.Add(1)
		slog.ErrorContext(ctx, "audit write failed",
			"component", "audit",
			"request_id", state.requestID,
			"action", pattern,
			"error_category", "database")
	}
}

func (s *Server) handleMetrics(c *gin.Context) {
	c.Header("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = fmt.Fprintf(c.Writer, `# TYPE coinsphere_http_requests_total counter
coinsphere_http_requests_total %d
# TYPE coinsphere_http_requests_failed_total counter
coinsphere_http_requests_failed_total %d
# TYPE coinsphere_http_requests_in_flight gauge
coinsphere_http_requests_in_flight %d
# TYPE coinsphere_audit_write_failures_total counter
coinsphere_audit_write_failures_total %d
# TYPE coinsphere_process_uptime_seconds gauge
coinsphere_process_uptime_seconds %.0f
`,
		s.metrics.requestsTotal.Load(),
		s.metrics.requestsFailed.Load(),
		s.metrics.requestsInFlight.Load(),
		s.metrics.auditWriteFailures.Load(),
		time.Since(s.metrics.startedAt).Seconds(),
	)
}

func incomingRequestID(candidate string) string {
	if candidate != "" && len(candidate) <= 64 {
		for _, char := range candidate {
			if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
				(char >= '0' && char <= '9') || char == '.' || char == '_' || char == '-' {
				continue
			}
			return rand.Text()
		}
		return candidate
	}
	return rand.Text()
}

func isMutation(method string) bool {
	return method == http.MethodPost || method == http.MethodPut ||
		method == http.MethodPatch || method == http.MethodDelete
}

func setAuditActor(c *gin.Context, userID int64) {
	if state, ok := requestStateFrom(c); ok && userID > 0 {
		state.actorUserID = userID
	}
}

func requestStateFrom(c *gin.Context) (*requestState, bool) {
	value, ok := c.Get(requestStateContextKey)
	if !ok {
		return nil, false
	}
	state, ok := value.(*requestState)
	return state, ok
}

func requestIDFrom(c *gin.Context) string {
	state, _ := requestStateFrom(c)
	if state == nil {
		return ""
	}
	return state.requestID
}

func routePattern(c *gin.Context) string {
	path := c.FullPath()
	if path == "" {
		return ""
	}
	parts := strings.Split(path, "/")
	for index, part := range parts {
		if strings.HasPrefix(part, ":") {
			parts[index] = "{" + strings.TrimPrefix(part, ":") + "}"
		} else if strings.HasPrefix(part, "*") {
			parts[index] = ""
		}
	}
	method := c.Request.Method
	if method == http.MethodHead {
		method = http.MethodGet
	}
	return method + " " + strings.Join(parts, "/")
}
