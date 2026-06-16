package scarylog

import (
	"context"
	"sync"
)

// contextKey - это неэкспортируемый тип, чтобы избежать коллизий ключей в контексте.
type contextKey string

const loggerKey = contextKey("logger")

// defaultLogger - ленивый singleton, возвращаемый FromContext, когда логгер
// отсутствует в контексте. Это избегает создания нового хендлера на каждый вызов.
var (
	defaultLogger     *Logger
	defaultLoggerOnce sync.Once
)

func getDefaultLogger() *Logger {
	defaultLoggerOnce.Do(func() {
		defaultLogger = NewLogger()
	})
	return defaultLogger
}

// ToContext добавляет логгер в context.Context.
func ToContext(ctx context.Context, l *Logger) context.Context {
	return context.WithValue(ctx, loggerKey, l)
}

// FromContext извлекает логгер из context.Context.
// Если логгер не найден в контексте, возвращается общий логгер по умолчанию (уровня INFO).
// Это сделано для предотвращения паники nil pointer.
func FromContext(ctx context.Context) *Logger {
	if l, ok := ctx.Value(loggerKey).(*Logger); ok {
		return l
	}
	// Возвращаем безопасный логгер по умолчанию, если в контексте ничего нет.
	return getDefaultLogger()
}
