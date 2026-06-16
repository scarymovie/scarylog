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
that stack is attached as a `stack` attribute automatically.

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

### Storing Logger in Context
```go
// Add logger to context
ctx := scarylog.ToContext(ctx, logger)

// Retrieve logger from context
log := scarylog.FromContext(ctx)
log.Info("processing request")
```

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

### Custom Handler
```go
handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
    Level: slog.LevelDebug,
})
logger := scarylog.NewLogger(scarylog.WithHandler(handler))
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
2. **Stack trace capture**: Errors that render a trace under `%+v` (via `fmt.Formatter`) get a `stack` attribute automatically — no extra dependency required
3. **Group handling**: Simplified group-based attribute organization
4. **Context integration**: Built-in context.Context support
5. **Attribute overwrite**: `WithOverwrite()` method for updating existing attributes
