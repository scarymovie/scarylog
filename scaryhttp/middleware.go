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

// config holds the middleware settings, mutated through Option values.
type config struct {
	header      string
	attrKey     string
	generate    func() string
	startLevel  slog.Level
	finishLevel slog.Level
	logStart    bool
	skip        func(*http.Request) bool
}

// Option customizes the middleware.
type Option func(*config)

// WithHeader overrides the request-id header name (default "X-Request-ID").
func WithHeader(name string) Option {
	return func(c *config) { c.header = name }
}

// WithAttrKey overrides the log attribute key for the request id (default "request_id").
func WithAttrKey(key string) Option {
	return func(c *config) { c.attrKey = key }
}

// WithGenerator overrides how a request id is generated when none is inbound.
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
// (e.g. health checks). The request-scoped logger is still attached.
func WithSkip(fn func(*http.Request) bool) Option {
	return func(c *config) { c.skip = fn }
}

// Middleware returns net/http middleware that, for every request:
//  1. reads the request-id header, generating one if absent;
//  2. derives a request-scoped logger from base carrying the request id and
//     stores it in the request context (retrieve it with scarylog.FromContext);
//  3. echoes the request id back on the response header;
//  4. logs the request lifecycle (status, latency) on finish.
func Middleware(base *scarylog.Logger, opts ...Option) func(http.Handler) http.Handler {
	if base == nil {
		base = scarylog.NewLogger()
	}
	cfg := config{
		header:      DefaultRequestIDHeader,
		attrKey:     "request_id",
		generate:    generateID,
		startLevel:  slog.LevelInfo,
		finishLevel: slog.LevelInfo,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(cfg.header)
			if id == "" {
				id = cfg.generate()
			}
			w.Header().Set(cfg.header, id)

			logger := base.With(cfg.attrKey, id)
			ctx := scarylog.ToContext(r.Context(), logger)
			r = r.WithContext(ctx)

			skip := cfg.skip != nil && cfg.skip(r)

			if !skip && cfg.logStart {
				logger.InfoContext(ctx, "request started",
					"method", r.Method, "path", r.URL.Path)
			}

			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(rec, r)

			if !skip {
				logger.InfoContext(ctx, "request finished",
					"method", r.Method,
					"path", r.URL.Path,
					"status", rec.status,
					"bytes", rec.written,
					"latency_ms", time.Since(start).Milliseconds(),
				)
			}
		})
	}
}

// statusRecorder wraps http.ResponseWriter to capture the status code and the
// number of bytes written for the finish log line.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	written     int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.status = code
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.wroteHeader = true
	}
	n, err := r.ResponseWriter.Write(b)
	r.written += n
	return n, err
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
