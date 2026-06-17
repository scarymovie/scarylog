package scarylog

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
)

// benchLogger writes to io.Discard so benchmarks measure the library, not I/O.
func benchLogger(b *testing.B) *Logger {
	b.Helper()
	h := slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})
	return NewLogger(WithHandler(h), WithDefaultAttrs("traceId", "trace-xyz", "requestId", "req-0"))
}

func BenchmarkInfo(b *testing.B) {
	l := benchLogger(b)
	b.ReportAllocs()
	for b.Loop() {
		l.Info("processing", "user_id", 42, "path", "/api/users")
	}
}

func BenchmarkInfoContext(b *testing.B) {
	l := benchLogger(b)
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		l.InfoContext(ctx, "processing", "user_id", 42, "path", "/api/users")
	}
}

func BenchmarkWithOverwrite(b *testing.B) {
	l := benchLogger(b)
	b.ReportAllocs()
	for b.Loop() {
		_ = l.WithOverwrite("requestId", "req-n")
	}
}

func BenchmarkErrorWithStack(b *testing.B) {
	l := benchLogger(b)
	err := errors.New("boom")
	b.ReportAllocs()
	for b.Loop() {
		l.Error(err, "user_id", 42)
	}
}
