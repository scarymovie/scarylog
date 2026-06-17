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
// The error passed to Error becomes the log message; add context by wrapping it
// at the call site rather than passing a separate message string. Passing nil is
// safe. If the error renders a stack trace under %+v, that stack is attached.
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
// # HTTP
//
// Subpackage scaryhttp provides net/http middleware that attaches a per-request
// id and a request-scoped logger to every request and logs the request lifecycle.
package scarylog
