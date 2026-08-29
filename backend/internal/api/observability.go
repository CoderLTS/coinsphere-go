package api

import (
	"bufio"
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"coinsphere/backend/internal/service"
)

const requestIDHeader = "X-Request-ID"

type requestStateKey struct{}

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

type observedResponseWriter struct {
	http.ResponseWriter
	statusCode int
	failed     bool
}

func (w *observedResponseWriter) WriteHeader(statusCode int) {
	if w.statusCode == 0 {
		w.statusCode = statusCode
	}
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *observedResponseWriter) Write(body []byte) (int, error) {
	if w.statusCode == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (w *observedResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *observedResponseWriter) Flush() {
	if w.statusCode == 0 {
		w.WriteHeader(http.StatusOK)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *observedResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if w.statusCode == 0 {
		w.statusCode = http.StatusSwitchingProtocols
	}
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

func (w *observedResponseWriter) Push(target string, options *http.PushOptions) error {
	if pusher, ok := w.ResponseWriter.(http.Pusher); ok {
		return pusher.Push(target, options)
	}
	return http.ErrNotSupported
}

func (s *Server) observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedAt := time.Now()
		state := &requestState{requestID: incomingRequestID(r.Header.Get(requestIDHeader))}
		r = r.WithContext(context.WithValue(r.Context(), requestStateKey{}, state))
		w.Header().Set(requestIDHeader, state.requestID)

		s.metrics.requestsTotal.Add(1)
		s.metrics.requestsInFlight.Add(1)
		defer s.metrics.requestsInFlight.Add(-1)

		observed := &observedResponseWriter{ResponseWriter: w}
		next.ServeHTTP(observed, r)
		if observed.statusCode == 0 {
			observed.statusCode = http.StatusOK
		}
		failed := observed.failed || observed.statusCode >= http.StatusBadRequest
		if failed {
			s.metrics.requestsFailed.Add(1)
		}
		duration := time.Since(startedAt)
		s.metrics.observeBucket(startedAt, failed, duration)

		route := r.Pattern
		if route == "" {
			route = "unmatched"
		} else {
			route = strings.TrimPrefix(route, r.Method+" ")
		}
		if isMutation(r.Method) && r.Pattern != "" {
			s.recordAudit(r, state, observed, failed)
		}

		attrs := []any{
			"component", "http.access",
			"request_id", state.requestID,
			"method", r.Method,
			"route", route,
			"status", observed.statusCode,
			"duration_ms", duration.Milliseconds(),
		}
		if state.actorUserID > 0 {
			attrs = append(attrs, "user_id", state.actorUserID)
		}
		if failed {
			slog.WarnContext(r.Context(), "http request completed", attrs...)
			return
		}
		slog.InfoContext(r.Context(), "http request completed", attrs...)
	})
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

func (s *Server) recordAudit(r *http.Request, state *requestState, response *observedResponseWriter, failed bool) {
	resourcePath := r.URL.EscapedPath()
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
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
	defer cancel()
	if err := s.App.RecordAudit(ctx, service.AuditRecordInput{
		RequestID: state.requestID, ActorUserID: actorUserID,
		Action: r.Pattern, ResourcePath: resourcePath,
		Outcome: outcome, StatusCode: response.statusCode,
	}); err != nil {
		s.metrics.auditWriteFailures.Add(1)
		slog.ErrorContext(ctx, "audit write failed",
			"component", "audit",
			"request_id", state.requestID,
			"action", r.Pattern,
			"error_category", "database")
	}
}

func (s *Server) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = fmt.Fprintf(w, `# TYPE coinsphere_http_requests_total counter
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

func setAuditActor(r *http.Request, userID int64) {
	if state, ok := r.Context().Value(requestStateKey{}).(*requestState); ok && userID > 0 {
		state.actorUserID = userID
	}
}

func markResponseFailed(w http.ResponseWriter) {
	if observed, ok := w.(*observedResponseWriter); ok {
		observed.failed = true
	}
}
