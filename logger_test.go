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
	"runtime"
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

// ctxAttrKey is a context key used by ctxAttrHandler in the tests below.
type ctxAttrKey string

// ctxAttrHandler is a context-aware handler: it pulls a request-scoped value out
// of ctx and adds it to the record, modelling trace correlation. It only sees
// that value when a real ctx is forwarded (i.e. via the *Context methods).
type ctxAttrHandler struct {
	slog.Handler
	key ctxAttrKey
}

func (h ctxAttrHandler) Handle(ctx context.Context, r slog.Record) error {
	if v, ok := ctx.Value(h.key).(string); ok {
		r.AddAttrs(slog.String("from_ctx", v))
	}
	return h.Handler.Handle(ctx, r)
}

func TestContextForwardedToHandler(t *testing.T) {
	buf := &bytes.Buffer{}
	key := ctxAttrKey("trace")
	h := ctxAttrHandler{
		Handler: slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}),
		key:     key,
	}
	l := NewLogger(WithHandler(h))
	ctx := context.WithValue(context.Background(), key, "abc-123")

	cases := []struct {
		name string
		log  func()
	}{
		{"InfoContext", func() { l.InfoContext(ctx, "hi") }},
		{"WarnContext", func() { l.WarnContext(ctx, "hi") }},
		{"DebugContext", func() { l.DebugContext(ctx, "hi") }},
		{"ErrorContext", func() { l.ErrorContext(ctx, errors.New("boom")) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			buf.Reset()
			c.log()
			m := decode(t, buf)
			if m["from_ctx"] != "abc-123" {
				t.Errorf("%s did not forward ctx to handler: from_ctx = %v", c.name, m["from_ctx"])
			}
		})
	}

	// The non-context methods must NOT carry request-scoped ctx values.
	buf.Reset()
	l.Info("hi")
	if m := decode(t, buf); m["from_ctx"] != nil {
		t.Errorf("Info should not forward request-scoped ctx value, got %v", m["from_ctx"])
	}
}

func TestCallerShortPath(t *testing.T) {
	l, buf := newTestLogger(t)
	l.Error(errors.New("x"))
	m := decode(t, buf)

	c, ok := m["caller"].(string)
	if !ok {
		t.Fatalf("missing caller attr")
	}
	if strings.HasPrefix(c, "/") {
		t.Errorf("caller should be a short path, got absolute %q", c)
	}
	if !strings.Contains(c, "logger_test.go:") {
		t.Errorf("caller = %q, want it to reference the call-site file", c)
	}
}

func TestGetAttrAfterWith(t *testing.T) {
	l, _ := newTestLogger(t, WithDefaultAttrs("traceId", "t1"))
	child := l.With("requestId", "r1")

	if v, ok := child.GetAttr("requestId"); !ok || v != "r1" {
		t.Errorf("GetAttr(requestId) after With = %v, %v; want r1, true", v, ok)
	}
	if v, ok := child.GetAttr("traceId"); !ok || v != "t1" {
		t.Errorf("GetAttr(traceId) after With = %v, %v; want t1, true (inherited)", v, ok)
	}
	if _, ok := l.GetAttr("requestId"); ok {
		t.Errorf("parent logger should not see child's With attr")
	}
}

func TestGetAttrHandlesSlogAttr(t *testing.T) {
	// WithOverwrite stores slog.Attr values as single elements; GetAttr must
	// still resolve them rather than misaligning the key-value stride.
	l, _ := newTestLogger(t, WithDefaultAttrs("env", "dev"))
	l2 := l.WithOverwrite(slog.String("region", "eu"))

	if v, ok := l2.GetAttr("region"); !ok || v != "eu" {
		t.Errorf("GetAttr(region) = %v, %v; want eu, true", v, ok)
	}
	if v, ok := l2.GetAttr("env"); !ok || v != "dev" {
		t.Errorf("GetAttr(env) = %v, %v; want dev, true", v, ok)
	}
}

// --- v2.1: SetDefault / Default / FromContextOK -----------------------------

func TestSetDefaultAndDefault(t *testing.T) {
	t.Cleanup(func() { SetDefault(nil) })

	buf := &bytes.Buffer{}
	app := NewLogger(WithWriter(buf), WithDefaultAttrs("service", "api", "version", "1.2.3"))
	SetDefault(app)

	if Default() != app {
		t.Errorf("Default() did not return the logger installed with SetDefault")
	}

	// A context carrying no logger must fall back to the application logger,
	// not to a bare stdout one that silently drops the app's attributes.
	FromContext(context.Background()).Info("no logger in ctx")

	m := decode(t, buf)
	if m["service"] != "api" || m["version"] != "1.2.3" {
		t.Errorf("fallback record lost the application attrs: %v", m)
	}

	SetDefault(nil)
	if Default() == app {
		t.Errorf("SetDefault(nil) did not restore the built-in default")
	}
	if Default() != Default() {
		t.Errorf("built-in default should stay a shared singleton")
	}
}

func TestFromContextOK(t *testing.T) {
	l, _ := newTestLogger(t)

	if _, ok := FromContextOK(context.Background()); ok {
		t.Errorf("FromContextOK reported a logger in an empty context")
	}

	got, ok := FromContextOK(ToContext(context.Background(), l))
	if !ok || got != l {
		t.Errorf("FromContextOK = %v, %v; want the stored logger and true", got, ok)
	}

	var nilCtx context.Context
	if _, ok := FromContextOK(nilCtx); ok {
		t.Errorf("FromContextOK(nil ctx) reported a logger")
	}
}

func TestToContextNilLoggerDoesNotPanic(t *testing.T) {
	t.Cleanup(func() { SetDefault(nil) })

	buf := &bytes.Buffer{}
	SetDefault(NewLogger(WithWriter(buf)))

	// A nil *Logger still satisfies the type assertion in FromContext, so it
	// must be rejected rather than handed back to panic on first use.
	ctx := ToContext(context.Background(), nil)
	if _, ok := FromContextOK(ctx); ok {
		t.Errorf("a nil logger stored in ctx must not be reported as present")
	}
	FromContext(ctx).Info("must not panic")

	if buf.Len() == 0 {
		t.Errorf("fallback logger was not used")
	}
}

func TestToContextNilContext(t *testing.T) {
	l, buf := newTestLogger(t)

	var nilCtx context.Context
	FromContext(ToContext(nilCtx, l)).Info("ok")

	if buf.Len() == 0 {
		t.Errorf("ToContext(nil ctx) lost the logger")
	}
}

// --- v2.1: WithWriter and options over a supplied handler -------------------

// The whole point of WithWriter: capture output without losing every other
// option, which is what happens with WithHandler.
func TestWithWriterAppliesAllOptions(t *testing.T) {
	buf := &bytes.Buffer{}
	l := NewLogger(
		WithWriter(buf),
		WithLevel(slog.LevelDebug),
		WithAttrRemapping(map[string]string{"msg": "message", "level": "severity"}),
		WithTimeFormat("2006"),
	)
	l.Debug("hello")

	m := decode(t, buf)
	if m["message"] != "hello" {
		t.Errorf("msg was not remapped to message: %v", m)
	}
	if m["severity"] != "DEBUG" {
		t.Errorf("severity = %v, want DEBUG (level remapped and WithLevel honored)", m["severity"])
	}
	if ts, ok := m["time"].(string); !ok || len(ts) != 4 {
		t.Errorf("time = %v, want a 4-character year from WithTimeFormat", m["time"])
	}
}

func TestWithHandlerHonorsLevel(t *testing.T) {
	buf := &bytes.Buffer{}
	// The supplied handler filters at Error; the explicit WithLevel must win.
	h := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelError})
	l := NewLogger(WithHandler(h), WithLevel(slog.LevelDebug))

	l.Debug("visible")
	if buf.Len() == 0 {
		t.Fatalf("WithLevel was silently ignored alongside WithHandler")
	}
	if m := decode(t, buf); m["msg"] != "visible" {
		t.Errorf("msg = %v, want visible", m["msg"])
	}
}

func TestWithHandlerAloneKeepsItsOwnLevel(t *testing.T) {
	buf := &bytes.Buffer{}
	h := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelError})
	l := NewLogger(WithHandler(h))

	l.Info("filtered")
	if buf.Len() != 0 {
		t.Errorf("handler's own level should apply when WithLevel is absent, got %s", buf.String())
	}
}

// Built-in keys belong to the supplied handler, but user attributes are ours to
// remap — including those added through With.
func TestWithHandlerRemapsUserAttrs(t *testing.T) {
	buf := &bytes.Buffer{}
	h := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	l := NewLogger(WithHandler(h), WithAttrRemapping(map[string]string{"user_id": "userId"}))

	l.With("user_id", 7).Info("hi", "user_id", 42)

	m := decode(t, buf)
	if _, ok := m["user_id"]; ok {
		t.Errorf("user_id was not remapped: %v", m)
	}
	if m["userId"] == nil {
		t.Errorf("userId missing after remapping: %v", m)
	}
}

// --- v2.1: ErrorMsg ---------------------------------------------------------

func TestErrorMsg(t *testing.T) {
	l, buf := newTestLogger(t)
	err := fmt.Errorf("dial tcp 10.0.0.5:6379: %w", errors.New("connection refused"))
	l.ErrorMsg("send message failed", err, "user_id", 7)

	m := decode(t, buf)
	if m["msg"] != "send message failed" {
		t.Errorf("msg = %v, want the stable message, not the error text", m["msg"])
	}
	if s, ok := m["error"].(string); !ok || !strings.Contains(s, "connection refused") {
		t.Errorf("error attr = %v, want the error text", m["error"])
	}
	if m["level"] != "ERROR" {
		t.Errorf("level = %v, want ERROR", m["level"])
	}
	if c, ok := m["caller"].(string); !ok || !strings.Contains(c, "logger_test.go") {
		t.Errorf("caller = %v, want this test's call site", m["caller"])
	}
	if m["user_id"] != float64(7) {
		t.Errorf("user_id = %v, want 7", m["user_id"])
	}
}

func TestErrorMsgNilError(t *testing.T) {
	l, buf := newTestLogger(t)
	l.ErrorMsg("nothing broke", nil)

	m := decode(t, buf)
	if m["msg"] != "nothing broke" {
		t.Errorf("msg = %v, want 'nothing broke'", m["msg"])
	}
	if _, ok := m["error"]; ok {
		t.Errorf("error attr should be absent for a nil error, got %v", m["error"])
	}
}

func TestErrorMsgWithStack(t *testing.T) {
	l, buf := newTestLogger(t)
	l.ErrorMsg("op failed", &stackError{msg: "kaboom"})

	m := decode(t, buf)
	stack, ok := m["stack"].(string)
	if !ok {
		t.Fatalf("expected stack attr for a formatter error: %v", m)
	}
	if !strings.Contains(stack, "main.go:42") {
		t.Errorf("stack = %q, want it to contain the trace", stack)
	}
}

func TestErrorMsgContext(t *testing.T) {
	l, buf := newTestLogger(t)
	l.ErrorMsgContext(context.Background(), "op failed", errors.New("boom"))

	if m := decode(t, buf); m["msg"] != "op failed" || m["error"] != "boom" {
		t.Errorf("got %v, want msg 'op failed' with error 'boom'", m)
	}
}

// --- v2.1: WithOverwrite fixes ----------------------------------------------

func TestGroupWithOverwritePreservesGroup(t *testing.T) {
	l, buf := newTestLogger(t, WithDefaultAttrs("env", "dev"))

	l.Group("req").WithOverwrite("env", "prod").Info("hit", "path", "/x")

	m := decode(t, buf)
	if m["env"] != "prod" {
		t.Errorf("env = %v, want prod", m["env"])
	}
	grp, ok := m["req"].(map[string]any)
	if !ok {
		t.Fatalf("group 'req' was lost by WithOverwrite: %v", m)
	}
	if grp["path"] != "/x" {
		t.Errorf("req.path = %v, want /x", grp["path"])
	}
}

// topLevelKeys returns the object's keys in the order they were serialized.
// json.Unmarshal into a map would lose that order (and silently collapse
// duplicates).
func topLevelKeys(t *testing.T, line string) []string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(line))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		t.Fatalf("expected a JSON object in %q, got %v (%v)", line, tok, err)
	}
	var keys []string
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			t.Fatalf("reading key: %v", err)
		}
		keys = append(keys, k.(string))
		var v any
		if err := dec.Decode(&v); err != nil {
			t.Fatalf("reading value for %v: %v", k, err)
		}
	}
	return keys
}

// WithOverwrite used to build its attrs by ranging over a map, so field order
// changed from record to record. The middleware calls it once per request, so
// that would have shown up as churn in every log line.
func TestWithOverwriteKeepsAttrOrderStable(t *testing.T) {
	var want string
	for i := 0; i < 50; i++ {
		buf := &bytes.Buffer{}
		l := NewLogger(WithWriter(buf), WithDefaultAttrs("a", 1, "b", 2, "c", 3, "d", 4))
		l.WithOverwrite("b", 20, "e", 5).Info("x")

		got := strings.Join(topLevelKeys(t, strings.TrimSpace(buf.String())), ",")
		if i == 0 {
			want = got
			continue
		}
		if got != want {
			t.Fatalf("attribute order is nondeterministic:\n got %s\nwant %s", got, want)
		}
	}

	// An overwritten key keeps its original position; a new key is appended.
	if want != "time,level,msg,a,b,c,d,e" {
		t.Errorf("key order = %s, want time,level,msg,a,b,c,d,e", want)
	}
}

func TestWithOverwriteAfterWith(t *testing.T) {
	l, buf := newTestLogger(t, WithDefaultAttrs("env", "dev"))

	l.With("req", "r-1").WithOverwrite("env", "prod").Info("x")

	m := decode(t, buf)
	if m["env"] != "prod" {
		t.Errorf("env = %v, want prod", m["env"])
	}
	if m["req"] != "r-1" {
		t.Errorf("req = %v, want r-1 (attr added by With must survive)", m["req"])
	}
}

// --- v2.1: Log / Enabled ----------------------------------------------------

func TestLogAtDynamicLevel(t *testing.T) {
	l, buf := newTestLogger(t)
	l.Log(context.Background(), slog.LevelWarn, "dynamic", "k", "v")

	m := decode(t, buf)
	if m["level"] != "WARN" {
		t.Errorf("level = %v, want WARN", m["level"])
	}
	if m["msg"] != "dynamic" || m["k"] != "v" {
		t.Errorf("got %v, want msg 'dynamic' with k=v", m)
	}
}

func TestEnabled(t *testing.T) {
	l, _ := newTestLogger(t) // handler admits Debug
	if !l.Enabled(context.Background(), slog.LevelDebug) {
		t.Errorf("Enabled(Debug) = false on a Debug-level logger")
	}

	quiet := NewLogger(WithWriter(io.Discard), WithLevel(slog.LevelError))
	if quiet.Enabled(context.Background(), slog.LevelInfo) {
		t.Errorf("Enabled(Info) = true on an Error-level logger")
	}
	if !quiet.Enabled(context.Background(), slog.LevelError) {
		t.Errorf("Enabled(Error) = false on an Error-level logger")
	}
}

// --- v2.1.1: stack traces survive wrapping ----------------------------------

// selfWrap is a pathological error that wraps itself, guarding the chain walk
// against spinning forever.
type selfWrap struct{}

func (selfWrap) Error() string { return "self" }
func (selfWrap) Unwrap() error { return selfWrap{} }

// fmt.Errorf("...: %w", err) returns *fmt.wrapError, which is not a
// fmt.Formatter — so looking only at the top error loses the trace, and the
// wrapping idiom this package documents would silently disable stacks.
func TestErrorStackSurvivesWrapping(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantStack bool
	}{
		{"unwrapped", &stackError{msg: "kaboom"}, true},
		{"wrapped once", fmt.Errorf("send: %w", &stackError{msg: "kaboom"}), true},
		{"wrapped twice", fmt.Errorf("a: %w", fmt.Errorf("b: %w", &stackError{msg: "kaboom"})), true},
		{"joined", errors.Join(errors.New("other"), &stackError{msg: "kaboom"}), true},
		{"plain error", errors.New("boom"), false},
		{"wrapped plain error", fmt.Errorf("send: %w", errors.New("boom")), false},
		{"self-wrapping error", selfWrap{}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l, buf := newTestLogger(t)
			l.Error(tc.err)

			m := decode(t, buf)
			stack, ok := m["stack"].(string)
			if ok != tc.wantStack {
				t.Fatalf("stack present = %v, want %v (record: %v)", ok, tc.wantStack, m)
			}
			if tc.wantStack && !strings.Contains(stack, "main.go:42") {
				t.Errorf("stack = %q, want it to contain the trace", stack)
			}
			// The message still carries the fully wrapped text.
			if m["msg"] != tc.err.Error() {
				t.Errorf("msg = %v, want %q", m["msg"], tc.err.Error())
			}
		})
	}
}

func TestErrorMsgStackSurvivesWrapping(t *testing.T) {
	l, buf := newTestLogger(t)
	l.ErrorMsg("send message failed", fmt.Errorf("send: %w", &stackError{msg: "kaboom"}))

	m := decode(t, buf)
	if stack, ok := m["stack"].(string); !ok || !strings.Contains(stack, "main.go:42") {
		t.Errorf("stack = %v, want the wrapped error's trace", m["stack"])
	}
	if m["msg"] != "send message failed" {
		t.Errorf("msg = %v, want the stable message", m["msg"])
	}
}

// --- v2.1.3: cyclic multi-errors don't blow the goroutine stack -------------

// cyclicMulti is the Unwrap() []error counterpart to selfWrap: a multi-error
// that can be wired into a cycle. Error() is deliberately non-recursive, so a
// test that hangs or dies is the chain walk's fault and nothing else.
type cyclicMulti struct{ errs []error }

func (m *cyclicMulti) Error() string   { return "multi" }
func (m *cyclicMulti) Unwrap() []error { return m.errs }

// selfCyclicMulti returns a multi-error that wraps itself.
func selfCyclicMulti() *cyclicMulti {
	m := &cyclicMulti{}
	m.errs = []error{m}
	return m
}

// mutualCyclicMulti returns two multi-errors that wrap each other.
func mutualCyclicMulti() *cyclicMulti {
	a, b := &cyclicMulti{}, &cyclicMulti{}
	a.errs = []error{b}
	b.errs = []error{a}
	return a
}

// A cycle reached through Unwrap() []error used to recurse with the depth
// counter reset on every branch, so the walk never terminated: the process died
// with an unrecoverable "fatal error: stack overflow" inside the error logging
// path itself. These cases must simply return.
func TestErrorCyclicMultiError(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantMsg string
	}{
		{"self-referential multi", selfCyclicMulti(), "multi"},
		{"mutually referential multi", mutualCyclicMulti(), "multi"},
		{"cycle behind a wrap", fmt.Errorf("op: %w", selfCyclicMulti()), "op: multi"},
		{"cycle joined with a plain error", errors.Join(errors.New("other"), selfCyclicMulti()), "other\nmulti"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l, buf := newTestLogger(t)
			l.Error(tc.err)

			m := decode(t, buf)
			if _, ok := m["stack"]; ok {
				t.Errorf("cyclic error should not produce a stack attr: %v", m)
			}
			if m["msg"] != tc.wantMsg {
				t.Errorf("msg = %v, want %q", m["msg"], tc.wantMsg)
			}
		})
	}
}

// The budget must not be spent before a branch that actually carries a trace:
// a cycle in one branch may not hide the stack sitting in an earlier one.
func TestErrorStackFoundBesideCyclicBranch(t *testing.T) {
	l, buf := newTestLogger(t)
	l.Error(errors.Join(&stackError{msg: "kaboom"}, selfCyclicMulti()))

	m := decode(t, buf)
	stack, ok := m["stack"].(string)
	if !ok || !strings.Contains(stack, "main.go:42") {
		t.Errorf("stack = %v, want the joined error's trace", m["stack"])
	}
}

// A deeply nested but acyclic tree of joins recursed once per level before the
// walk was made iterative. It now runs on a constant goroutine stack.
func TestErrorDeeplyNestedMultiError(t *testing.T) {
	var err error = errors.New("boom")
	for range 50_000 {
		err = errors.Join(err)
	}

	l, buf := newTestLogger(t)
	l.Error(err)

	if m := decode(t, buf); m["msg"] != "boom" {
		t.Errorf("msg = %v, want %q", m["msg"], "boom")
	}
}

// --- v2.1.2: AddSource points at the call site ------------------------------

// here returns the line number of its own call site.
func here() int {
	_, _, line, _ := runtime.Caller(1)
	return line
}

// sourceOf pulls the source attribute slog writes when AddSource is on.
func sourceOf(t *testing.T, m map[string]any) (file string, line int, fn string) {
	t.Helper()
	src, ok := m["source"].(map[string]any)
	if !ok {
		t.Fatalf("record has no source attribute: %v", m)
	}
	f, _ := src["file"].(string)
	n, _ := src["line"].(float64)
	name, _ := src["function"].(string)
	return f, int(n), name
}

// Every public logging method must report the caller's file and line, not a
// frame inside scarylog. slog.Logger.Log captures the PC of *its* caller, so a
// wrapper that forwards to it labels every record with the wrapper's own line;
// emit captures the PC itself, and callerSkip has to be right for each path.
//
// Each case keeps the logging call and here() on one source line, so the
// expected line number is computed rather than hardcoded.
func TestSourcePointsAtCallSite(t *testing.T) {
	ctx := context.Background()
	stackErr := &stackError{msg: "kaboom"}

	cases := []struct {
		name string
		// call logs exactly one record and returns its own line number.
		call func(l *Logger) int
	}{
		{"Info", func(l *Logger) int { l.Info("m"); return here() }},
		{"InfoContext", func(l *Logger) int { l.InfoContext(ctx, "m"); return here() }},
		{"Warn", func(l *Logger) int { l.Warn("m"); return here() }},
		{"WarnContext", func(l *Logger) int { l.WarnContext(ctx, "m"); return here() }},
		{"Debug", func(l *Logger) int { l.Debug("m"); return here() }},
		{"DebugContext", func(l *Logger) int { l.DebugContext(ctx, "m"); return here() }},
		{"Log", func(l *Logger) int { l.Log(ctx, slog.LevelInfo, "m"); return here() }},
		{"Error", func(l *Logger) int { l.Error(stackErr); return here() }},
		{"ErrorContext", func(l *Logger) int { l.ErrorContext(ctx, stackErr); return here() }},
		{"Error(nil)", func(l *Logger) int { l.Error(nil); return here() }},
		{"ErrorMsg", func(l *Logger) int { l.ErrorMsg("m", stackErr); return here() }},
		{"ErrorMsgContext", func(l *Logger) int { l.ErrorMsgContext(ctx, "m", stackErr); return here() }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf := &bytes.Buffer{}
			l := NewLogger(WithWriter(buf), WithLevel(slog.LevelDebug), WithSource(true))

			want := tc.call(l)

			file, line, fn := sourceOf(t, decode(t, buf))
			if !strings.HasSuffix(file, "logger_test.go") {
				t.Errorf("source.file = %q, want the caller's file, not a frame inside scarylog", file)
			}
			if line != want {
				t.Errorf("source.line = %d, want %d (the logging call site)", line, want)
			}
			if !strings.Contains(fn, "TestSourcePointsAtCallSite") {
				t.Errorf("source.function = %q, want this test's closure", fn)
			}
		})
	}
}

// The reported source must survive the derived-logger constructors, which
// rebuild the underlying slog.Logger.
func TestSourceThroughDerivedLoggers(t *testing.T) {
	for _, tc := range []struct {
		name   string
		derive func(*Logger) *Logger
	}{
		{"With", func(l *Logger) *Logger { return l.With("k", "v") }},
		{"WithOverwrite", func(l *Logger) *Logger { return l.WithOverwrite("k", "v") }},
		{"Group", func(l *Logger) *Logger { return l.Group("g") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			buf := &bytes.Buffer{}
			l := tc.derive(NewLogger(WithWriter(buf), WithSource(true)))

			l.Info("m")
			want := here()

			file, line, _ := sourceOf(t, decode(t, buf))
			if !strings.HasSuffix(file, "logger_test.go") || line != want-1 {
				t.Errorf("source = %s:%d, want logger_test.go:%d", file, line, want-1)
			}
		})
	}
}

// AddSource on a handler supplied through WithHandler must work too — that is
// how a consumer plugs in their own sink.
func TestSourceWithSuppliedHandler(t *testing.T) {
	buf := &bytes.Buffer{}
	h := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug, AddSource: true})
	l := NewLogger(WithHandler(h))

	l.Info("m")
	want := here()

	file, line, _ := sourceOf(t, decode(t, buf))
	if !strings.HasSuffix(file, "logger_test.go") || line != want-1 {
		t.Errorf("source = %s:%d, want logger_test.go:%d", file, line, want-1)
	}
}

// Without WithSource there must be no source attribute at all.
func TestSourceOffByDefault(t *testing.T) {
	buf := &bytes.Buffer{}
	NewLogger(WithWriter(buf)).Info("m")

	if _, ok := decode(t, buf)["source"]; ok {
		t.Errorf("source attribute present without WithSource")
	}
}
