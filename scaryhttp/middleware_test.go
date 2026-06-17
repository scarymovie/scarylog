package scaryhttp

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	scarylog "github.com/scarymovie/scarylog/v2"
)

// newCapturingLogger returns a scarylog logger writing JSON to buf.
func newCapturingLogger(buf *bytes.Buffer) *scarylog.Logger {
	h := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return scarylog.NewLogger(scarylog.WithHandler(h))
}

func TestMiddlewareGeneratesAndEchoesRequestID(t *testing.T) {
	buf := &bytes.Buffer{}
	base := newCapturingLogger(buf)

	var seenInHandler string
	h := Middleware(base)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The request-scoped logger must be retrievable from ctx.
		if id, ok := scarylog.FromContext(r.Context()).GetString("request_id"); ok {
			seenInHandler = id
		}
		w.WriteHeader(http.StatusCreated)
	}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/users", nil))

	id := rr.Header().Get(DefaultRequestIDHeader)
	if id == "" {
		t.Fatalf("response is missing %s header", DefaultRequestIDHeader)
	}
	if seenInHandler != id {
		t.Errorf("handler saw request_id %q, response header had %q", seenInHandler, id)
	}

	m := decodeLine(t, buf)
	if m["msg"] != "request finished" {
		t.Errorf("msg = %v, want 'request finished'", m["msg"])
	}
	if m["status"] != float64(http.StatusCreated) {
		t.Errorf("status = %v, want 201", m["status"])
	}
	if m["request_id"] != id {
		t.Errorf("log request_id = %v, want %q", m["request_id"], id)
	}
	if _, ok := m["latency_ms"]; !ok {
		t.Errorf("finish log missing latency_ms")
	}
}

func TestMiddlewarePropagatesInboundID(t *testing.T) {
	buf := &bytes.Buffer{}
	h := Middleware(newCapturingLogger(buf))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(DefaultRequestIDHeader, "inbound-42")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if got := rr.Header().Get(DefaultRequestIDHeader); got != "inbound-42" {
		t.Errorf("response request id = %q, want inbound-42 (propagated)", got)
	}
	if m := decodeLine(t, buf); m["request_id"] != "inbound-42" {
		t.Errorf("log request_id = %v, want inbound-42", m["request_id"])
	}
}

func TestMiddlewareSkip(t *testing.T) {
	buf := &bytes.Buffer{}
	mw := Middleware(newCapturingLogger(buf),
		WithSkip(func(r *http.Request) bool { return r.URL.Path == "/health" }),
	)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Even when skipped, the logger must still be in ctx.
		if scarylog.FromContext(r.Context()) == nil {
			t.Errorf("logger missing from ctx on skipped path")
		}
	}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/health", nil))

	if strings.TrimSpace(buf.String()) != "" {
		t.Errorf("skipped path should produce no log, got: %s", buf.String())
	}
	if rr.Header().Get(DefaultRequestIDHeader) == "" {
		t.Errorf("request id header should still be set on skipped path")
	}
}

func TestMiddlewareLogStart(t *testing.T) {
	buf := &bytes.Buffer{}
	h := Middleware(newCapturingLogger(buf), WithLogStart(true))(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/x", nil))

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want start+finish (2 lines), got %d: %s", len(lines), buf.String())
	}
	var start map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &start); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if start["msg"] != "request started" {
		t.Errorf("first line msg = %v, want 'request started'", start["msg"])
	}
}

func TestMiddlewareNilBaseDoesNotPanic(t *testing.T) {
	h := Middleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil)) // must not panic
}

// decodeLine parses the first JSON log line in buf.
func decodeLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatalf("no log output")
	}
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("invalid JSON %q: %v", line, err)
	}
	return m
}
