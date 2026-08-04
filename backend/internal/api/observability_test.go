package api

import (
	"bufio"
	"errors"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
)

var errHijackObserved = errors.New("hijack observed")

type optionalResponseWriter struct {
	*httptest.ResponseRecorder
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
