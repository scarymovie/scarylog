package scarylog

import (
	"context"
	"sync"
	"sync/atomic"
)

// contextKey - это неэкспортируемый тип, чтобы избежать коллизий ключей в контексте.
type contextKey string

const loggerKey = contextKey("logger")

// defaultLogger holds the logger installed with SetDefault. When nothing was
// installed, builtinLogger is used instead: a lazy singleton, so FromContext
// doesn't build a new handler on every call.
var (
	defaultLogger atomic.Pointer[Logger]
	builtinLogger *Logger
	builtinOnce   sync.Once
)

func builtin() *Logger {
	builtinOnce.Do(func() {
		builtinLogger = NewLogger()
	})
	return builtinLogger
}

// SetDefault installs the logger returned by Default, and therefore by
// FromContext when the context carries none. Call it once during startup so
// records that miss the request-scoped logger still carry the application's
// attributes (service, version, environment) instead of silently landing in a
// bare stdout logger. Passing nil restores the built-in default.
//
// It is safe for concurrent use.
func SetDefault(l *Logger) {
	defaultLogger.Store(l)
}

// Default returns the logger installed with SetDefault, or a built-in
// INFO-level logger writing JSON to stdout when none was installed.
func Default() *Logger {
	if l := defaultLogger.Load(); l != nil {
		return l
	}
	return builtin()
}

// ToContext добавляет логгер в context.Context.
func ToContext(ctx context.Context, l *Logger) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, loggerKey, l)
}

// FromContextOK извлекает логгер из context.Context, сообщая, был ли он там.
// Позволяет отличить «логгер положили» от «логгера не было» — FromContext
// такой разницы не показывает, потому что всегда возвращает рабочий логгер.
func FromContextOK(ctx context.Context) (*Logger, bool) {
	if ctx == nil {
		return nil, false
	}
	// A nil *Logger stored via ToContext still satisfies the type assertion,
	// so reject it here rather than letting the caller panic on first use.
	if l, ok := ctx.Value(loggerKey).(*Logger); ok && l != nil {
		return l, true
	}
	return nil, false
}

// FromContext извлекает логгер из context.Context.
// Если логгер не найден в контексте, возвращается Default() — логгер,
// установленный через SetDefault, либо общий логгер по умолчанию (уровня INFO).
// Это сделано для предотвращения паники nil pointer.
func FromContext(ctx context.Context) *Logger {
	if l, ok := FromContextOK(ctx); ok {
		return l
	}
	return Default()
}
