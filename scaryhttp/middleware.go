// Package scaryhttp provides net/http middleware for scarylog: it attaches a
// per-request id and a request-scoped logger to every request, and logs the
// request lifecycle. It depends only on the standard library.
package scaryhttp

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"

	scarylog "github.com/scarymovie/scarylog/v2"
)

// DefaultRequestIDHeader is the header read for an inbound request id and set on
// the response when one is generated.
const DefaultRequestIDHeader = "X-Request-ID"

// DefaultRequestIDAttrKey is the log attribute key the request id is logged under.
const DefaultRequestIDAttrKey = "request_id"

// CorrelationID describes one identifier the middleware propagates: it is read
// from Header, generated when absent, echoed on the response and logged under
// AttrKey. Generate overrides the shared generator for this id only.
type CorrelationID struct {
	Header   string
	AttrKey  string
	Generate func() string
}

// config holds the middleware settings, mutated through Option values.
type config struct {
	ids         []CorrelationID
	generate    func() string
	startLevel  slog.Level
	finishLevel slog.Level
	logStart    bool
	skip        func(*http.Request) bool
	skipWrap    func(*http.Request) bool
}

// Option customizes the middleware.
type Option func(*config)

// WithHeader overrides the request-id header name (default "X-Request-ID").
// It is sugar for the first correlation id; don't mix it with WithCorrelationIDs.
func WithHeader(name string) Option {
	return func(c *config) {
		if len(c.ids) > 0 {
			c.ids[0].Header = name
		}
	}
}

// WithAttrKey overrides the log attribute key for the request id (default "request_id").
// It is sugar for the first correlation id; don't mix it with WithCorrelationIDs.
func WithAttrKey(key string) Option {
	return func(c *config) {
		if len(c.ids) > 0 {
			c.ids[0].AttrKey = key
		}
	}
}

// WithCorrelationID adds another identifier alongside the ones already
// configured — e.g. an inbound end-to-end "X-Trace-ID" next to the per-hop
// "X-Request-ID". It may be applied several times.
func WithCorrelationID(header, attrKey string) Option {
	return func(c *config) {
		c.ids = append(c.ids, CorrelationID{Header: header, AttrKey: attrKey})
	}
}

// WithCorrelationIDs replaces the whole set of identifiers, including the
// default request id.
func WithCorrelationIDs(ids ...CorrelationID) Option {
	return func(c *config) {
		c.ids = append([]CorrelationID(nil), ids...)
	}
}

// WithGenerator overrides how an id is generated when none is inbound. It
// applies to every correlation id that doesn't carry its own Generate.
func WithGenerator(fn func() string) Option {
	return func(c *config) {
		if fn != nil {
			c.generate = fn
		}
	}
}

// WithLogStart enables a log line when the request starts (default: only finish).
func WithLogStart(enabled bool) Option {
	return func(c *config) { c.logStart = enabled }
}

// WithLevels sets the log levels for the start and finish lines.
func WithLevels(start, finish slog.Level) Option {
	return func(c *config) {
		c.startLevel = start
		c.finishLevel = finish
	}
}

// WithSkip skips middleware logging for requests where fn returns true
// (e.g. health checks). The request-scoped logger is still attached, and the
// response writer is still wrapped — use WithSkipWrap for that.
func WithSkip(fn func(*http.Request) bool) Option {
	return func(c *config) { c.skip = fn }
}

// WithSkipWrap hands the handler the original http.ResponseWriter, unwrapped,
// for requests where fn returns true. The request-scoped logger is still
// attached and the lifecycle is still logged, but status and bytes are then
// unknown and omitted.
//
// The middleware's wrapper already mirrors every optional interface of the
// writer it wraps, so this is only needed for writers with capabilities beyond
// http.Flusher, http.Hijacker, io.ReaderFrom and http.Pusher.
func WithSkipWrap(fn func(*http.Request) bool) Option {
	return func(c *config) { c.skipWrap = fn }
}

// Middleware returns net/http middleware that, for every request:
//  1. reads each configured correlation-id header, generating one if absent;
//  2. derives a request-scoped logger from base carrying those ids and
//     stores it in the request context (retrieve it with scarylog.FromContext);
//  3. echoes the ids back on the response headers;
//  4. logs the request lifecycle (status, latency) on finish, including when the
//     handler panics.
//
// The response writer handed to the next handler implements exactly the optional
// interfaces of the original, so WebSocket upgrades and streaming keep working.
func Middleware(base *scarylog.Logger, opts ...Option) func(http.Handler) http.Handler {
	if base == nil {
		base = scarylog.NewLogger()
	}
	cfg := config{
		ids: []CorrelationID{{
			Header:  DefaultRequestIDHeader,
			AttrKey: DefaultRequestIDAttrKey,
		}},
		generate:    generateID,
		startLevel:  slog.LevelInfo,
		finishLevel: slog.LevelInfo,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attrs := make([]any, 0, len(cfg.ids)*2)
			for _, id := range cfg.ids {
				value := r.Header.Get(id.Header)
				if value == "" {
					generate := id.Generate
					if generate == nil {
						generate = cfg.generate
					}
					value = generate()
				}
				w.Header().Set(id.Header, value)
				attrs = append(attrs, id.AttrKey, value)
			}

			// WithOverwrite, not With: the base logger may already carry these
			// keys as default attrs, and slog would then emit each key twice.
			logger := base.WithOverwrite(attrs...)
			ctx := scarylog.ToContext(r.Context(), logger)
			r = r.WithContext(ctx)

			skip := cfg.skip != nil && cfg.skip(r)

			if !skip && cfg.logStart {
				logger.Log(ctx, cfg.startLevel, "request started",
					"method", r.Method, "path", r.URL.Path)
			}

			var (
				rec = w
				sr  *recorder
			)
			if cfg.skipWrap == nil || !cfg.skipWrap(r) {
				sr, rec = wrapWriter(w)
			}

			start := time.Now()
			completed := false

			// Deferred so the request is still logged when the handler panics:
			// that is the one request worth having in the log. The panic value is
			// not recovered — it travels on to the application's recovery
			// middleware with its stack intact.
			defer func() {
				if skip {
					return
				}
				level := cfg.finishLevel
				args := []any{"method", r.Method, "path", r.URL.Path}
				switch {
				case sr == nil:
					// Writer left unwrapped by WithSkipWrap: nothing recorded.
				case sr.hijacked:
					// status/bytes describe the handshake, not the connection
					// that took over; latency is the connection's lifetime.
					args = append(args, "status", sr.status, "hijacked", true)
				default:
					args = append(args, "status", sr.status, "bytes", sr.written)
				}
				args = append(args, "latency_ms", time.Since(start).Milliseconds())
				if !completed {
					level = slog.LevelError
					args = append(args, "panicked", true)
				}
				logger.Log(ctx, level, "request finished", args...)
			}()

			next.ServeHTTP(rec, r)
			completed = true
		})
	}
}

// generateID returns a random 16-byte hex string, using only the stdlib.
func generateID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is extraordinary; fall back to a time-based id.
		return time.Now().UTC().Format("20060102T150405.000000000")
	}
	return hex.EncodeToString(b[:])
}
