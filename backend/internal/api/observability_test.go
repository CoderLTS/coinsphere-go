package api

import (
	"bufio"
	"errors"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var errHijackObserved = errors.New("hijack observed")

type optionalResponseWriter struct {
	*httptest.ResponseRecorder
}

func TestHTTPMetricsKeepsSixtyMinuteRuntimeTrend(t *testing.T) {
	metrics := &httpMetrics{startedAt: time.Now().Add(-time.Minute)}
	metrics.requestsTotal.Add(2)
	metrics.requestsFailed.Add(1)
	now := time.Now().UTC()
	metrics.observeBucket(now, false, 20*time.Millisecond)
	metrics.observeBucket(now, true, 40*time.Millisecond)
	snapshot := metrics.snapshot()
	httpSnapshot := snapshot["http"].(M)
	trend := httpSnapshot["trend"].([]M)
	if len(trend) != 60 {
		t.Fatalf("trend length = %d, want 60", len(trend))
	}
	current := trend[len(trend)-1]
	if current["requests"] != uint64(2) || current["failed"] != uint64(1) || current["averageLatencyMs"] != float64(30) {
		t.Fatalf("current trend bucket = %#v", current)
	}
}

func (optionalResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errHijackObserved
}

func TestIncomingRequestID(t *testing.T) {
	const upstream = "upstream.request-123"
	if got := incomingRequestID(upstream); got != upstream {
		t.Fatalf("valid upstream request ID = %q, want %q", got, upstream)
	}

	for _, candidate := range []string{"invalid request id", " upstream ", strings.Repeat("a", 65), "request-id-用户"} {
		generated := incomingRequestID(candidate)
		if generated == "" || generated == candidate || incomingRequestID(generated) != generated {
			t.Fatalf("invalid upstream request ID %q was not replaced safely: %q", candidate, generated)
		}
	}
}

func TestObservedResponseWriterPreservesStreamingAndHijacking(t *testing.T) {
	underlying := optionalResponseWriter{ResponseRecorder: httptest.NewRecorder()}
	stream := &observedResponseWriter{ResponseWriter: underlying}
	stream.Flush()
	if !underlying.Flushed || stream.statusCode != 200 {
		t.Fatalf("flush state = flushed:%t status:%d", underlying.Flushed, stream.statusCode)
	}

	websocket := &observedResponseWriter{ResponseWriter: underlying}
	_, _, err := websocket.Hijack()
	if !errors.Is(err, errHijackObserved) || websocket.statusCode != 101 {
		t.Fatalf("hijack state = error:%v status:%d", err, websocket.statusCode)
	}
}
