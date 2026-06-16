package scarylog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
)

// syncBuffer is a concurrency-safe writer for collecting log output from many
// workers under -race.
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

// newTestLogger returns a logger writing JSON to buf, plus the buffer.
func newTestLogger(t *testing.T, extra ...Option) (*Logger, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	handler := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	opts := append([]Option{WithHandler(handler)}, extra...)
	return NewLogger(opts...), buf
}

// decode parses the single JSON log line in buf.
func decode(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatalf("no log output")
	}
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i] // first record only
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("invalid JSON %q: %v", line, err)
	}
	return m
}

func TestLevels(t *testing.T) {
	cases := []struct {
		name  string
		level string
		log   func(l *Logger)
	}{
		{"info", "INFO", func(l *Logger) { l.Info("hi") }},
		{"warn", "WARN", func(l *Logger) { l.Warn("hi") }},
		{"debug", "DEBUG", func(l *Logger) { l.Debug("hi") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l, buf := newTestLogger(t)
			c.log(l)
			m := decode(t, buf)
			if m["level"] != c.level {
				t.Errorf("level = %v, want %v", m["level"], c.level)
			}
			if m["msg"] != "hi" {
				t.Errorf("msg = %v, want hi", m["msg"])
			}
		})
	}
}

// stackError is a pkg/errors-style error that renders a stack under %+v.
type stackError struct{ msg string }

func (e *stackError) Error() string { return e.msg }

func (e *stackError) Format(s fmt.State, verb rune) {
	if verb == 'v' && s.Flag('+') {
		io.WriteString(s, e.msg+"\nmain.foo\n\t/app/main.go:42")
		return
	}
	io.WriteString(s, e.msg)
}

func TestErrorBasic(t *testing.T) {
	l, buf := newTestLogger(t)
	l.Error(errors.New("boom"))
	m := decode(t, buf)

	if m["level"] != "ERROR" {
		t.Errorf("level = %v, want ERROR", m["level"])
	}
	if m["msg"] != "boom" {
		t.Errorf("msg = %v, want boom (err.Error())", m["msg"])
	}
	if _, ok := m["caller"]; !ok {
		t.Errorf("missing caller attr")
	}
	if _, ok := m["stack"]; ok {
		t.Errorf("plain error should not produce a stack attr")
	}
}

func TestErrorWithStack(t *testing.T) {
	l, buf := newTestLogger(t)
	l.Error(&stackError{msg: "kaboom"})
	m := decode(t, buf)

	if m["msg"] != "kaboom" {
		t.Errorf("msg = %v, want kaboom", m["msg"])
	}
	stack, ok := m["stack"].(string)
	if !ok {
		t.Fatalf("expected stack attr for formatter error")
	}
	if !strings.Contains(stack, "main.go:42") {
		t.Errorf("stack = %q, want it to contain the trace", stack)
	}
}

func TestErrorNilDoesNotPanic(t *testing.T) {
	l, buf := newTestLogger(t)
	l.Error(nil) // must not panic
	m := decode(t, buf)
	if m["level"] != "ERROR" {
		t.Errorf("level = %v, want ERROR", m["level"])
	}
}

func TestGroupNesting(t *testing.T) {
	l, buf := newTestLogger(t)
	l.Group("req").Info("handled", "path", "/x")
	m := decode(t, buf)

	grp, ok := m["req"].(map[string]any)
	if !ok {
		t.Fatalf("expected group 'req', got %v", m["req"])
	}
	if grp["path"] != "/x" {
		t.Errorf("req.path = %v, want /x", grp["path"])
	}
}

func TestWithOverwrite(t *testing.T) {
	buf := &bytes.Buffer{}
	handler := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	// Custom handler must survive WithOverwrite (regression for handler-drop bug).
	l := NewLogger(WithHandler(handler), WithDefaultAttrs("env", "dev", "svc", "api"))

	l2 := l.WithOverwrite("env", "prod")
	l2.Info("up")

	m := decode(t, buf)
	if m["env"] != "prod" {
		t.Errorf("env = %v, want prod (overwritten)", m["env"])
	}
	if m["svc"] != "api" {
		t.Errorf("svc = %v, want api (preserved)", m["svc"])
	}
	if buf.Len() == 0 {
		t.Errorf("custom handler was dropped: no output")
	}
}

// captureStdout runs fn while os.Stdout is redirected to a pipe, returning what
// was written. WithAttrRemapping/WithTimeFormat only affect the library's
// built-in handler, which writes to os.Stdout, so we exercise that real path.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = orig
	out, _ := io.ReadAll(r)
	return string(out)
}

func TestAttrRemappingAndTimeFormat(t *testing.T) {
	out := captureStdout(t, func() {
		l := NewLogger(
			WithAttrRemapping(map[string]string{"msg": "message"}),
			WithTimeFormat("2006"),
		)
		l.Info("hello")
	})

	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		t.Fatalf("invalid JSON %q: %v", out, err)
	}
	if m["message"] != "hello" {
		t.Errorf("expected msg remapped to 'message', got %v", m)
	}
	if _, ok := m["msg"]; ok {
		t.Errorf("original 'msg' key should be gone, got %v", m)
	}
	if ts, ok := m["time"].(string); !ok || len(ts) != 4 {
		t.Errorf("time = %v, want a 4-digit year per TimeFormat", m["time"])
	}
}

func TestWith(t *testing.T) {
	l, buf := newTestLogger(t)
	child := l.With("user_id", 42, "session", "abc")
	child.Info("action")

	m := decode(t, buf)
	if m["user_id"] != float64(42) {
		t.Errorf("user_id = %v, want 42", m["user_id"])
	}
	if m["session"] != "abc" {
		t.Errorf("session = %v, want abc", m["session"])
	}
}

func TestWithDefaultAttrs(t *testing.T) {
	l, buf := newTestLogger(t, WithDefaultAttrs("service", "my-service"))
	l.Info("up")

	m := decode(t, buf)
	if m["service"] != "my-service" {
		t.Errorf("service = %v, want my-service", m["service"])
	}
}

func TestWithLevel(t *testing.T) {
	// WithLevel only affects the built-in stdout handler, so exercise that path.
	out := captureStdout(t, func() {
		l := NewLogger(WithLevel(slog.LevelWarn))
		l.Info("suppressed")
		l.Warn("shown")
	})
	if strings.Contains(out, "suppressed") {
		t.Errorf("Info below WithLevel(Warn) should be filtered out, got: %s", out)
	}
	if !strings.Contains(out, "shown") {
		t.Errorf("Warn should pass WithLevel(Warn), got: %s", out)
	}
}

func TestGetAttr(t *testing.T) {
	l, _ := newTestLogger(t, WithDefaultAttrs("traceId", "trace-1", "count", 7))

	if v, ok := l.GetAttr("traceId"); !ok || v != "trace-1" {
		t.Errorf("GetAttr(traceId) = %v, %v; want trace-1, true", v, ok)
	}
	if v, ok := l.GetAttr("count"); !ok || v != 7 {
		t.Errorf("GetAttr(count) = %v, %v; want 7, true", v, ok)
	}
	if _, ok := l.GetAttr("missing"); ok {
		t.Errorf("GetAttr(missing) should report ok=false")
	}
}

func TestGetString(t *testing.T) {
	l, _ := newTestLogger(t, WithDefaultAttrs("traceId", "trace-1", "count", 7))

	if s, ok := l.GetString("traceId"); !ok || s != "trace-1" {
		t.Errorf("GetString(traceId) = %q, %v; want trace-1, true", s, ok)
	}
	// non-string value must report ok=false
	if s, ok := l.GetString("count"); ok || s != "" {
		t.Errorf("GetString(count) = %q, %v; want \"\", false", s, ok)
	}
}

func TestGetAttrName(t *testing.T) {
	l, _ := newTestLogger(t, WithAttrRemapping(map[string]string{"level": "severity"}))

	if got := l.GetAttrName("level"); got != "severity" {
		t.Errorf("GetAttrName(level) = %q, want severity", got)
	}
	if got := l.GetAttrName("msg"); got != "msg" {
		t.Errorf("GetAttrName(msg) = %q, want msg (unmapped passthrough)", got)
	}

	// no AttrMap configured -> passthrough
	plain, _ := newTestLogger(t)
	if got := plain.GetAttrName("level"); got != "level" {
		t.Errorf("GetAttrName without AttrMap = %q, want level", got)
	}
}

func TestContextRoundTrip(t *testing.T) {
	l, _ := newTestLogger(t)
	ctx := ToContext(context.Background(), l)
	if got := FromContext(ctx); got != l {
		t.Errorf("FromContext returned a different logger")
	}
}

// TestWorkerPoolRequestIDOverwrite models the worker-pool logging principle:
// at app start the base logger carries a shared traceId plus an initial
// requestId. Each worker overwrites ONLY requestId via WithOverwrite (traceId and
// the custom handler are preserved) and passes its logger through the per-task
// context, exactly as a pool would carry it in task ctx.
func TestWorkerPoolRequestIDOverwrite(t *testing.T) {
	sw := &syncBuffer{}
	handler := slog.NewJSONHandler(sw, &slog.HandlerOptions{Level: slog.LevelDebug})
	base := NewLogger(
		WithHandler(handler),
		WithDefaultAttrs("traceId", "trace-xyz", "requestId", "req-initial"),
	)

	const workers = 8
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := range workers {
		go func() {
			defer wg.Done()
			reqID := fmt.Sprintf("req-%d", i)
			// Overwrite only requestId; traceId stays shared across the run.
			ctx := ToContext(context.Background(), base.WithOverwrite("requestId", reqID))
			FromContext(ctx).Info("processing")
		}()
	}
	wg.Wait()

	lines := strings.Split(strings.TrimSpace(sw.String()), "\n")
	if len(lines) != workers {
		t.Fatalf("got %d log lines, want %d", len(lines), workers)
	}

	seen := make(map[string]bool)
	for _, line := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("invalid JSON %q: %v", line, err)
		}
		if m["traceId"] != "trace-xyz" {
			t.Errorf("traceId = %v, want shared trace-xyz preserved", m["traceId"])
		}
		rid, _ := m["requestId"].(string)
		if rid == "" || rid == "req-initial" {
			t.Errorf("requestId was not overwritten per worker: %v", m["requestId"])
		}
		seen[rid] = true
	}
	if len(seen) != workers {
		t.Errorf("expected %d distinct requestIds, got %d", workers, len(seen))
	}
}

func TestFromContextDefaultSingleton(t *testing.T) {
	a := FromContext(context.Background())
	b := FromContext(context.Background())
	if a == nil {
		t.Fatalf("default logger is nil")
	}
	if a != b {
		t.Errorf("FromContext default should be a shared singleton")
	}
}
