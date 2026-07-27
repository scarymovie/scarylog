// Package scarylog is a thin, opinionated wrapper around the standard library
// log/slog. It standardizes structured logging across services: leveled methods,
// automatic caller and stack capture on errors, attribute grouping and overwrite,
// and context.Context propagation — with no third-party dependencies.
//
// # Basic use
//
//	logger := scarylog.NewLogger(scarylog.WithLevel(slog.LevelDebug))
//	logger.Info("server started", "port", 8080)
//	logger.Error(fmt.Errorf("save user: %w", err), "user_id", 42)
//
// WithSource adds slog's source attribute; it reports the caller's file and
// line, not a frame inside this package.
//
// The error passed to Error becomes the log message; add context by wrapping it
// at the call site rather than passing a separate message string. Passing nil is
// safe. If the error, or any error it wraps, renders a stack trace under %+v,
// that stack is attached — wrapping with fmt.Errorf("...: %w", err) does not
// lose it.
//
// When the message feeds grouping or alerting, use ErrorMsg instead: it keeps the
// message stable and low-cardinality and moves the error text into the "error"
// attribute.
//
//	logger.ErrorMsg("send message failed", err, "user_id", 42)
//
// # Context propagation
//
// Two complementary mechanisms exist. ToContext/FromContext carry a logger value
// through a context.Context so request-scoped loggers can be retrieved downstream.
// The *Context methods (InfoContext, WarnContext, DebugContext, ErrorContext)
// forward the context.Context to the slog handler, so context-aware handlers can
// enrich records from request-scoped values (e.g. trace correlation):
//
//	log := scarylog.FromContext(ctx)
//	log.InfoContext(ctx, "processing request")
//
// Install the application's logger with SetDefault during startup so records that
// miss the request-scoped logger still carry the application's attributes rather
// than landing in a bare stdout logger. FromContextOK reports whether a logger was
// actually present.
//
// # HTTP
//
// Subpackage scaryhttp provides net/http middleware that attaches per-request
// correlation ids and a request-scoped logger to every request and logs the
// request lifecycle. The response writer it passes down mirrors the optional
// interfaces of the original, so WebSocket upgrades and streaming keep working.
package scarylog
