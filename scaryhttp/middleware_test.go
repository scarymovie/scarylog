package scaryhttp

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	scarylog "github.com/scarymovie/scarylog/v2"
)

// newCapturingLogger returns a scarylog logger writing JSON to buf.
func newCapturingLogger(buf *bytes.Buffer) *scarylog.Logger {
	h := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return scarylog.NewLogger(scarylog.WithHandler(h))
}

// syncBuffer guards a bytes.Buffer, for tests where an httptest.Server handler
// logs from another goroutine.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// newServerLogger returns a logger writing JSON into a goroutine-safe buffer.
func newServerLogger() (*scarylog.Logger, *syncBuffer) {
	buf := &syncBuffer{}
	return scarylog.NewLogger(scarylog.WithWriter(buf), scarylog.WithLevel(slog.LevelDebug)), buf
}

// decodeFirst parses the first JSON log line in out.
func decodeFirst(t *testing.T, out string) map[string]any {
	t.Helper()
	line := firstLine(t, out)
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("invalid JSON %q: %v", line, err)
	}
	return m
}

func firstLine(t *testing.T, out string) string {
	t.Helper()
	line := strings.TrimSpace(out)
	if line == "" {
		t.Fatalf("no log output")
	}
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	return line
}

// waitLogDeadline bounds how long the real-server tests wait for a record that
// is written on the server's handler goroutine.
const waitLogDeadline = 2 * time.Second

// decodeFirstWait is decodeFirst for tests driving a real httptest.Server: it
// polls buf until a whole record is in it, or waitLogDeadline expires.
//
// srv.Close() is not a synchronization point for these records. The middleware
// emits "request finished" from a defer, and httptest.Server drops a connection
// from the WaitGroup that Close waits on as soon as it reaches
// http.StateHijacked - which net/http sets inside Hijack(), before the handler
// has even returned.
func decodeFirstWait(t *testing.T, buf *syncBuffer) map[string]any {
	t.Helper()
	deadline := time.Now().Add(waitLogDeadline)
	for {
		// A record is written with a single newline-terminated Write, so a
		// newline means there is a whole one to parse.
		if out := buf.String(); strings.Contains(out, "\n") {
			return decodeFirst(t, out)
		}
		if time.Now().After(deadline) {
			t.Fatalf("no log output after %v", waitLogDeadline)
		}
		time.Sleep(time.Millisecond)
	}
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
	h := Middleware(newCapturingLogger(buf))(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))

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
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
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
		http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))

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

func TestMiddlewareNilBaseDoesNotPanic(_ *testing.T) {
	h := Middleware(nil)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil)) // must not panic
}

// TestMiddlewarePreservesHijackOnRealServer is the regression test for the
// blocker: with the middleware in the chain a handler must still be able to
// hijack the connection, or every WebSocket upgrade behind it dies.
//
// It runs against httptest.NewServer on purpose. httptest.ResponseRecorder is
// not an http.Hijacker, so the defect is invisible to a recorder-based test.
func TestMiddlewarePreservesHijackOnRealServer(t *testing.T) {
	base, buf := newServerLogger()

	h := Middleware(base)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Errorf("handler did not receive an http.Hijacker: WebSocket upgrades would fail")
			return
		}
		if _, ok := w.(http.Flusher); !ok {
			t.Errorf("handler did not receive an http.Flusher")
		}

		conn, brw, err := hj.Hijack()
		if err != nil {
			t.Errorf("Hijack: %v", err)
			return
		}
		defer conn.Close()

		if _, err := brw.WriteString("HTTP/1.1 101 Switching Protocols\r\n" +
			"Upgrade: test\r\nConnection: Upgrade\r\n\r\nPONG"); err != nil {
			t.Errorf("write to hijacked conn: %v", err)
		}
		if err := brw.Flush(); err != nil {
			t.Errorf("flush hijacked conn: %v", err)
		}
	}))

	srv := httptest.NewServer(h)
	defer srv.Close()

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if _, err := io.WriteString(conn, "GET /ws HTTP/1.1\r\nHost: example\r\n"+
		"Connection: Upgrade\r\nUpgrade: test\r\n\r\n"); err != nil {
		t.Fatalf("write request: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	got, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	if !strings.Contains(string(got), "101 Switching Protocols") ||
		!strings.HasSuffix(string(got), "PONG") {
		t.Fatalf("upgrade response = %q, want a 101 ending in PONG", got)
	}

	srv.Close()

	// Close returned without waiting for the handler: httptest released the
	// connection the moment it was hijacked. Poll for the finish record.
	m := decodeFirstWait(t, buf)
	if m["msg"] != "request finished" {
		t.Errorf("msg = %v, want 'request finished'", m["msg"])
	}
	if m["hijacked"] != true {
		t.Errorf("hijacked = %v, want true", m["hijacked"])
	}
	if m["status"] != float64(http.StatusSwitchingProtocols) {
		t.Errorf("status = %v, want 101", m["status"])
	}
	if _, ok := m["bytes"]; ok {
		t.Errorf("bytes should be omitted for a hijacked connection, got %v", m["bytes"])
	}
}

// TestMiddlewarePreservesFlushOnRealServer proves SSE/streaming still reaches
// the client: the read only completes if Flush actually pushed the bytes out
// while the handler is still running.
func TestMiddlewarePreservesFlushOnRealServer(t *testing.T) {
	base, _ := newServerLogger()

	const chunk = "data: tick\n\n"
	released := make(chan struct{})

	h := Middleware(base)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			t.Errorf("handler did not receive an http.Flusher: streaming would stall")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if _, err := io.WriteString(w, chunk); err != nil {
			t.Errorf("write chunk: %v", err)
		}
		f.Flush()
		<-released
	}))

	srv := httptest.NewServer(h)
	defer srv.Close()
	defer close(released)

	resp, err := http.Get(srv.URL + "/events")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	read := make(chan error, 1)
	got := make([]byte, len(chunk))
	go func() {
		_, err := io.ReadFull(resp.Body, got)
		read <- err
	}()

	select {
	case err := <-read:
		if err != nil {
			t.Fatalf("read flushed chunk: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for the flushed chunk: Flush never reached the client")
	}

	if string(got) != chunk {
		t.Errorf("streamed %q, want %q", got, chunk)
	}
}

// TestMiddlewareResponseControllerReachesRealWriter covers the Unwrap path:
// SetWriteDeadline is not implemented by the wrapper, so it can only work if
// http.ResponseController can unwrap down to the real writer.
func TestMiddlewareResponseControllerReachesRealWriter(t *testing.T) {
	base, _ := newServerLogger()

	h := Middleware(base)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		rc := http.NewResponseController(w)
		if err := rc.SetWriteDeadline(time.Now().Add(time.Minute)); err != nil {
			t.Errorf("ResponseController.SetWriteDeadline: %v", err)
		}
		if err := rc.Flush(); err != nil {
			t.Errorf("ResponseController.Flush: %v", err)
		}
	}))

	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
}

func TestMiddlewareSkipWrapPassesOriginalWriter(t *testing.T) {
	buf := &bytes.Buffer{}

	rr := httptest.NewRecorder()
	var seen http.ResponseWriter
	h := Middleware(newCapturingLogger(buf),
		WithSkipWrap(func(r *http.Request) bool { return r.URL.Path == "/ws" }),
	)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		seen = w
	}))

	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/ws", nil))

	if seen != http.ResponseWriter(rr) {
		t.Errorf("handler got %T, want the original *httptest.ResponseRecorder", seen)
	}

	m := decodeFirst(t, buf.String())
	if m["msg"] != "request finished" {
		t.Errorf("msg = %v, want 'request finished'", m["msg"])
	}
	if _, ok := m["status"]; ok {
		t.Errorf("status must be omitted when the writer is not wrapped, got %v", m["status"])
	}
}

// TestMiddlewareLogsWhenHandlerPanics: the request that panicked is the one
// worth having in the log, and the panic must still reach the app's recovery
// middleware.
func TestMiddlewareLogsWhenHandlerPanics(t *testing.T) {
	buf := &bytes.Buffer{}
	h := Middleware(newCapturingLogger(buf))(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("boom")
	}))

	func() {
		defer func() {
			if rec := recover(); rec == nil {
				t.Errorf("panic did not propagate past the middleware")
			} else if rec != "boom" {
				t.Errorf("recovered %v, want the original panic value 'boom'", rec)
			}
		}()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	}()

	m := decodeFirst(t, buf.String())
	if m["msg"] != "request finished" {
		t.Fatalf("msg = %v, want 'request finished' even on panic", m["msg"])
	}
	if m["panicked"] != true {
		t.Errorf("panicked = %v, want true", m["panicked"])
	}
	if m["level"] != "ERROR" {
		t.Errorf("level = %v, want ERROR", m["level"])
	}
}

// TestMiddlewareDoesNotDuplicateRequestID: With appends, so a base logger that
// already carries request_id would emit the key twice and log collectors would
// collapse the duplicate nondeterministically.
func TestMiddlewareDoesNotDuplicateRequestID(t *testing.T) {
	buf := &syncBuffer{}
	base := scarylog.NewLogger(
		scarylog.WithWriter(buf),
		scarylog.WithDefaultAttrs(slog.String("request_id", "APP-UUID"), slog.String("svc", "api")),
	)

	h := Middleware(base)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(DefaultRequestIDHeader, "REQ-1")
	h.ServeHTTP(rr, req)

	line := firstLine(t, buf.String())
	if n := strings.Count(line, `"request_id":`); n != 1 {
		// json.Unmarshal into a map silently collapses duplicates, so count in
		// the raw line instead.
		t.Errorf("found %d request_id keys in %s, want exactly 1", n, line)
	}

	m := decodeFirst(t, buf.String())
	if m["request_id"] != "REQ-1" {
		t.Errorf("request_id = %v, want REQ-1 (the per-request id wins)", m["request_id"])
	}
	if m["svc"] != "api" {
		t.Errorf("svc = %v, want api (unrelated default attrs preserved)", m["svc"])
	}
}

func TestMiddlewareWithLevels(t *testing.T) {
	buf := &syncBuffer{}
	base := scarylog.NewLogger(scarylog.WithWriter(buf), scarylog.WithLevel(slog.LevelDebug))

	h := Middleware(base,
		WithLogStart(true),
		WithLevels(slog.LevelDebug, slog.LevelWarn),
	)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want start+finish (2 lines), got %d: %s", len(lines), buf.String())
	}

	var start, finish map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &start); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &finish); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if start["level"] != "DEBUG" {
		t.Errorf("start level = %v, want DEBUG", start["level"])
	}
	if finish["level"] != "WARN" {
		t.Errorf("finish level = %v, want WARN", finish["level"])
	}
}

func TestMiddlewareMultipleCorrelationIDs(t *testing.T) {
	buf := &syncBuffer{}
	base := scarylog.NewLogger(scarylog.WithWriter(buf))

	h := Middleware(base,
		WithCorrelationID("X-Trace-ID", "trace_id"),
	)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Trace-ID", "trace-from-upstream")
	h.ServeHTTP(rr, req)

	if got := rr.Header().Get("X-Trace-ID"); got != "trace-from-upstream" {
		t.Errorf("X-Trace-ID response header = %q, want the inbound value", got)
	}
	reqID := rr.Header().Get(DefaultRequestIDHeader)
	if reqID == "" {
		t.Fatalf("per-hop request id was not generated")
	}

	m := decodeFirst(t, buf.String())
	if m["trace_id"] != "trace-from-upstream" {
		t.Errorf("trace_id = %v, want trace-from-upstream", m["trace_id"])
	}
	if m["request_id"] != reqID {
		t.Errorf("request_id = %v, want %q", m["request_id"], reqID)
	}
}

func TestMiddlewareCorrelationIDsReplaceDefaults(t *testing.T) {
	buf := &syncBuffer{}
	base := scarylog.NewLogger(scarylog.WithWriter(buf))

	h := Middleware(base,
		WithCorrelationIDs(CorrelationID{Header: "X-Corr", AttrKey: "corr"}),
	)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))

	if rr.Header().Get(DefaultRequestIDHeader) != "" {
		t.Errorf("default request id should have been replaced")
	}
	if rr.Header().Get("X-Corr") == "" {
		t.Errorf("X-Corr was not generated or echoed")
	}

	m := decodeFirst(t, buf.String())
	if _, ok := m["request_id"]; ok {
		t.Errorf("request_id should be gone, got %v", m["request_id"])
	}
	if m["corr"] == nil {
		t.Errorf("corr attribute missing from the record")
	}
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
