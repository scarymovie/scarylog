---
name: go-scarylog
description: Use this skill when writing or reviewing logs, designing logs, naming logs. Triggers on: "logging", "log", "logger".
---

# Scarylog Skill for AI Assistants

## Overview
`scarylog` is a Go logging library that provides a convenient wrapper around Go's structured logger `slog`. When generating Go code that requires logging, use this logger instead of raw `slog` or other logging libraries.

## Import
```go
import "github.com/scarymovie/scarylog/v2"
```

## Basic Usage

### Creating a Logger
```go
// Simple logger with default settings (INFO level)
logger := scarylog.NewLogger()

// Logger with custom options
logger := scarylog.NewLogger(
    scarylog.WithLevel(slog.LevelDebug),
    scarylog.WithDefaultAttrs("service", "my-service"),
)
```

### Logging Methods

#### Info - Informational messages
```go
logger.Info("server started", "port", 8080, "host", "localhost")
```

#### Warn - Warning messages
```go
logger.Warn("high memory usage", "percent", 85.5)
```

#### Error - Error messages with error objects
The error is the first argument and becomes the log message. Add context by
wrapping the error at the call site instead of passing a separate message string.
```go
err := someOperation()
if err != nil {
    // msg = err.Error(); a "caller" attr is added automatically.
    logger.Error(fmt.Errorf("operation failed: %w", err), "user_id", 123)
}
```
Passing `nil` is safe (it logs a placeholder, never panics). If the error renders
a stack trace under `%+v` (e.g. `github.com/pkg/errors`, `cockroachdb/errors`),
that stack is attached as a `stack` attribute automatically — including when the
error has been wrapped with `fmt.Errorf("...: %w", err)` or `errors.Join`, since
the whole error chain is searched. Errors that carry no trace (`errors.New`,
plain `fmt.Errorf`) produce no `stack` attribute; the `caller` attribute is
always present regardless.

#### ErrorMsg - Errors with a stable message
`Error` makes the rendered error the message, so the message carries whatever
variable data the error text contains (addresses, ids, timeouts). That breaks
grouping and alerting by message in log aggregators. Use `ErrorMsg` when the
message feeds either:
```go
// msg stays constant; the error text goes to the "error" attribute.
logger.ErrorMsg("send message failed", err, "user_id", 123)
// {"msg":"send message failed","error":"dial tcp 10.0.0.5:6379: connection refused", ...}
```
`caller` and `stack` behave exactly as with `Error`. A nil `err` is safe — only
`msg` is logged, with no `error` attribute. `ErrorMsgContext` forwards a `ctx`.

Rule of thumb: `Error` when a human reads the line, `ErrorMsg` when a machine
groups it.

#### Debug - Debug-level messages
```go
logger.Debug("processing request", "request_id", reqID)
```

### With - Adding Context
Create a child logger with additional context:
```go
ctxLogger := logger.With("user_id", userID, "session", sessionID)
ctxLogger.Info("user action") // Logs with user_id and session
```

### WithOverwrite - Overwriting Attributes
Create a logger with overwritten attributes:
```go
logger := scarylog.NewLogger(scarylog.WithDefaultAttrs("env", "dev", "version", "1.0"))
newLogger := logger.WithOverwrite("env", "prod") // env is now "prod", version remains "1.0"
```

### Group - Grouped Logging
Create a logger that groups attributes under a specific name:
```go
groupLogger := logger.Group("request")
groupLogger.Info("request received", "method", "GET", "path", "/api/users")
// Output will have request.method and request.path
```

### Reading Attributes
Inspect the logger's default attributes or resolve a remapped key name:
```go
traceID, ok := logger.GetString("traceId") // typed string lookup
val, ok := logger.GetAttr("count")          // any-typed lookup
key := logger.GetAttrName("level")          // "severity" if remapped, else "level"
```

## Context Integration

There are two complementary, opposite-direction mechanisms — don't confuse them:

1. **Logger *in* context** (`ToContext`/`FromContext`): store a logger value in a
   `context.Context` so request-scoped loggers can be retrieved downstream.
2. **Context *into* the log call** (`InfoContext`/`WarnContext`/`DebugContext`/
   `ErrorContext`): forward the `context.Context` to the slog handler, so
   context-aware handlers can enrich the record from request-scoped values
   (e.g. OpenTelemetry trace correlation).

### Storing Logger in Context
```go
// Add logger to context
ctx := scarylog.ToContext(ctx, logger)

// Retrieve logger from context
log := scarylog.FromContext(ctx)
log.Info("processing request")
```

### The application logger: SetDefault
`FromContext` never returns nil — when the context carries no logger it falls
back to `Default()`. Install the application's logger once at startup so that
fallback still carries the application's attributes; otherwise those records land
in a bare INFO/stdout logger, look fine, and quietly go missing from aggregation.
```go
func main() {
    base := scarylog.NewLogger(
        scarylog.WithDefaultAttrs("service", "my-service", "version", version),
    )
    scarylog.SetDefault(base) // FromContext now falls back to this
}
```
`SetDefault(nil)` restores the built-in default. It is safe for concurrent use.

Use `FromContextOK` when the difference matters:
```go
log, ok := scarylog.FromContextOK(ctx)
if !ok {
    // no request-scoped logger here — a wiring bug, not just a quiet fallback
    log = scarylog.Default()
}
```
Storing a nil `*Logger` with `ToContext` is safe: `FromContext` treats it as
absent rather than handing back something that panics on first use.

### Context-aware logging methods
Use the `*Context` variants when you want the handler to see your `ctx`. The plain
methods (`Info`/`Warn`/`Debug`/`Error`) pass an empty `context.Background()`, so a
context-aware handler won't see request-scoped values:
```go
log := scarylog.FromContext(ctx)
log.InfoContext(ctx, "processing request", "user_id", 42)
log.ErrorContext(ctx, fmt.Errorf("save user: %w", err))
```
The plain methods remain for code where no `ctx` is available (init, background
jobs, CLI). They are not deprecated.

## HTTP Middleware (`scaryhttp`)

`scaryhttp` provides stdlib-only `net/http` middleware that, per request: reads or
generates an `X-Request-ID`, attaches a request-scoped logger to the context,
echoes the id on the response, and logs the request lifecycle (status, latency),
including when the handler panics.
```go
import (
    "github.com/scarymovie/scarylog/v2"
    "github.com/scarymovie/scarylog/v2/scaryhttp"
)

base := scarylog.NewLogger()
mux := http.NewServeMux()
// ... register handlers ...
srv := scaryhttp.Middleware(base)(mux)

// Inside any handler, pull the request-scoped logger (carries request_id):
func handler(w http.ResponseWriter, r *http.Request) {
    log := scarylog.FromContext(r.Context())
    log.InfoContext(r.Context(), "handling")
}
```
Options: `WithHeader`, `WithAttrKey`, `WithCorrelationID`, `WithCorrelationIDs`,
`WithGenerator`, `WithLogStart`, `WithLevels`, `WithSkip` (e.g. skip health
checks), `WithSkipWrap`.

### WebSockets and streaming
The writer handed to the handler implements **exactly** the optional interfaces
of the original — `http.Flusher`, `http.Hijacker`, `io.ReaderFrom`,
`http.Pusher` — plus `Unwrap() http.ResponseWriter` for `http.ResponseController`.
So WebSocket upgrades (which type-assert `http.Hijacker` directly) and SSE both
keep working behind the middleware, and code that probes for a capability to pick
a fallback still gets the truthful answer.

A hijacked connection is logged once, on close, as
`status=101 hijacked=true latency_ms=<connection lifetime>`; `bytes` is omitted
because the middleware no longer sees the traffic.

`WithSkipWrap` bypasses the wrapper entirely for matching requests, for writers
with capabilities beyond the four above:
```go
scaryhttp.Middleware(base, scaryhttp.WithSkipWrap(
    func(r *http.Request) bool { return r.URL.Path == "/ws" },
))
```

### Multiple correlation ids
A common setup is an end-to-end `X-Trace-ID` that arrives from upstream plus a
per-hop `request_id`. Add as many as needed; each is read from its header,
generated when absent, echoed on the response and logged under its attribute key:
```go
srv := scaryhttp.Middleware(base,
    scaryhttp.WithCorrelationID("X-Trace-ID", "trace_id"),
)(mux)
// {"msg":"request finished","request_id":"...","trace_id":"trace-from-upstream",...}
```
`WithCorrelationIDs` replaces the whole set, including the default request id.

### Echo, gin, chi
The middleware is plain `func(http.Handler) http.Handler`, so it composes with
any framework that can adapt one — e.g. `echo.WrapMiddleware(scaryhttp.Middleware(base))`.

## Worker Pool Pattern: per-worker requestId via WithOverwrite

When a worker pool processes a stream of requests, you typically have a `traceId`
that is **shared for the whole run** and a `requestId` that **differs per worker /
per task**. The principle:

1. At app start, build one base logger carrying the shared `traceId` and an initial
   `requestId` as default attrs.
2. Each worker derives its own logger with `WithOverwrite("requestId", ...)`. This
   overwrites **only** `requestId` — `traceId` (and the custom handler) are kept.
3. Pass that per-worker logger through the per-task `context.Context` (the same ctx
   a pool already threads into each task), then read it back with `FromContext`.

```go
// App start: shared traceId + an initial requestId.
base := scarylog.NewLogger(
    scarylog.WithDefaultAttrs("traceId", traceID, "requestId", "req-initial"),
)

// Inside the pool, each task overwrites only requestId for its own worker.
func (p *Pool) Submit(ctx context.Context, reqID string, fn func(context.Context) error) error {
    logger := base.WithOverwrite("requestId", reqID) // traceId preserved
    ctx = scarylog.ToContext(ctx, logger)
    return p.submit(ctx, fn)
}

// In the task body, pull the worker-scoped logger from ctx.
func handle(ctx context.Context) error {
    log := scarylog.FromContext(ctx)
    log.Info("processing") // carries shared traceId + this worker's requestId
    return nil
}
```

`WithOverwrite` is safe to call concurrently from many workers: it only reads the
base logger's options and returns a fresh logger, so the shared `traceId` stays
intact while each worker gets a distinct `requestId`. For how to build the pool
itself (channels, graceful shutdown, panic recovery, per-task context), see the
separate `workerpool` skill.

## Advanced Options

### Custom Writer (preferred for tests)
```go
buf := &bytes.Buffer{}
logger := scarylog.NewLogger(
    scarylog.WithWriter(buf),          // built-in JSON handler, redirected
    scarylog.WithLevel(slog.LevelDebug),
    scarylog.WithAttrRemapping(map[string]string{"level": "severity"}),
)
```
`WithWriter` only changes where the built-in handler writes, so every other
option keeps working. Reach for it instead of `WithHandler` whenever all you need
is to capture the output.

### Custom Handler
```go
handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
    Level: slog.LevelDebug,
})
logger := scarylog.NewLogger(scarylog.WithHandler(handler))
```
`WithHandler` hands the output format to your handler, which limits what the
other options can still do:

| Option | With `WithHandler` |
|---|---|
| `WithLevel` | applied — an explicit level wins over the handler's own |
| `WithAttrRemapping` | applies to **your** attributes only |
| `WithTimeFormat` | applies to attributes of kind `time`, not the record's timestamp |
| `WithSource` | not applied — set `AddSource` in your own `slog.HandlerOptions` |

The record's own `time`/`level`/`msg` keys are written by the supplied handler
through its `ReplaceAttr`, which nothing outside that handler can reach. To remap
those, use the built-in handler (with `WithWriter` if you need to redirect it).

### Logging at a computed level
```go
level := slog.LevelInfo
if status >= 500 {
    level = slog.LevelError
}
logger.Log(ctx, level, "request finished", "status", status)

if logger.Enabled(ctx, slog.LevelDebug) {
    logger.DebugContext(ctx, "dump", "payload", expensiveToBuild())
}
```

### Attribute Remapping
```go
logger := scarylog.NewLogger(
    scarylog.WithAttrRemapping(map[string]string{
        "time": "timestamp",
        "level": "severity",
    }),
)
```

### Custom Time Format
```go
logger := scarylog.NewLogger(
    scarylog.WithTimeFormat("2006-01-02 15:04:05"),
)
```

### Source location
```go
logger := scarylog.NewLogger(scarylog.WithSource(true))
logger.Info("started") // {"source":{"function":"main.main","file":"/app/main.go","line":18},...}
```
`source` points at **your** call site, not inside scarylog, for every method
(`Info`/`Warn`/`Debug`/`Log`/`Error`/`ErrorMsg` and their `*Context` variants).
With a handler of your own, set `AddSource` in its `slog.HandlerOptions` instead —
the call site is reported correctly either way.

This is separate from the `caller` attribute that `Error`/`ErrorMsg` always add:
`caller` is a short `pkg/file.go:line` string, `source` is slog's structured
attribute and covers every level.

## Best Practices

1. **Use structured logging**: Always pass attributes as key-value pairs
   ```go
   // ✅ Good
   logger.Info("user created", "user_id", id, "email", email)
   
   // ❌ Bad
   logger.Info(fmt.Sprintf("user created: %d %s", id, email))
   ```

2. **Include context**: Use `With()` to add contextual information for related operations
   ```go
   func handleRequest(logger *scarylog.Logger, req *Request) {
       ctxLogger := logger.With("request_id", req.ID)
       ctxLogger.Info("request started")
       // ... process request
   }
   ```

3. **Use Error() for errors**: Pass the error as the first argument; wrap it to add
   context. Stack traces are captured automatically when the error supports `%+v`.
   ```go
   if err != nil {
       logger.Error(fmt.Errorf("database query failed: %w", err), "query", query)
   }
   ```

4. **Context propagation**: Store logger in context for request-scoped logging
   ```go
   func middleware(next http.Handler) http.Handler {
       return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
           logger := scarylog.NewLogger().With("request_id", generateID())
           ctx := scarylog.ToContext(r.Context(), logger)
           next.ServeHTTP(w, r.WithContext(ctx))
       })
   }
   ```

5. **Appropriate log levels**:
   - `Debug`: Detailed information for debugging
   - `Info`: General operational messages
   - `Warn`: Potential issues that don't stop execution
   - `Error`: Errors that prevent operations from completing

## Example: Complete Service

```go
package service

import (
    "context"
    "fmt"
    "github.com/scarymovie/scarylog/v2"
)

type UserService struct {
    logger *scarylog.Logger
}

func NewUserService(logger *scarylog.Logger) *UserService {
    return &UserService{
        logger: logger.With("component", "user_service"),
    }
}

func (s *UserService) GetUser(ctx context.Context, id int) (*User, error) {
    log := scarylog.FromContext(ctx)
    log.Info("getting user", "user_id", id)
    
    user, err := s.fetchUser(id)
    if err != nil {
        log.Error(fmt.Errorf("failed to fetch user: %w", err), "user_id", id)
        return nil, err
    }
    
    log.Debug("user fetched", "user_id", id, "email", user.Email)
    return user, nil
}
```

## Key Differences from Raw slog

1. **Automatic caller tracking**: Error logs automatically include caller information
2. **Stack trace capture**: Errors that render a trace under `%+v` (via `fmt.Formatter`) get a `stack` attribute automatically, anywhere in the wrap chain — no extra dependency required
3. **Group handling**: Simplified group-based attribute organization
4. **Context integration**: Built-in context.Context support, plus a configurable
   application-wide fallback via `SetDefault`
5. **Attribute overwrite**: `WithOverwrite()` method for updating existing
   attributes, order-preserving so record layout stays stable
6. **Low-cardinality errors**: `ErrorMsg()` keeps the message stable for grouping
   and alerting
