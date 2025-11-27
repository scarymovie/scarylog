package scarySlog

import (
	"log/slog"
	"os"
)

type Logger struct {
	logger    *slog.Logger
	groupName string
}

type Options struct {
	Level        slog.Leveler
	DefaultAttrs []any
	GroupName    string
}

type Option func(*Options)

func WithLevel(level slog.Leveler) Option {
	return func(o *Options) {
		o.Level = level
	}
}

func WithDefaultAttrs(args ...any) Option {
	return func(o *Options) {
		o.DefaultAttrs = append(o.DefaultAttrs, args...)
	}
}

func WithGroup(name string) Option {
	return func(o *Options) {
		o.GroupName = name
	}
}

func NewLogger(opts ...Option) *Logger {
	options := &Options{
		Level: slog.LevelInfo,
	}

	for _, opt := range opts {
		opt(options)
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: options.Level,
	})

	slogLogger := slog.New(handler)
	if len(options.DefaultAttrs) > 0 {
		slogLogger = slogLogger.With(options.DefaultAttrs...)
	}

	return &Logger{
		logger:    slogLogger,
		groupName: options.GroupName,
	}
}

func (l *Logger) Info(msg string, args ...any) {
	if l.groupName != "" && len(args) > 0 {
		l.logger.Info(msg, slog.Group(l.groupName, args...))
	} else {
		l.logger.Info(msg, args...)
	}
}

func (l *Logger) Warn(msg string, args ...any) {
	if l.groupName != "" && len(args) > 0 {
		l.logger.Warn(msg, slog.Group(l.groupName, args...))
	} else {
		l.logger.Warn(msg, args...)
	}
}

func (l *Logger) Error(msg string, err error, args ...any) {
	allArgs := []any{"error", err}
	if l.groupName != "" && len(args) > 0 {
		allArgs = append(allArgs, slog.Group(l.groupName, args...))
	} else {
		allArgs = append(allArgs, args...)
	}
	l.logger.Error(msg, allArgs...)
}

func (l *Logger) Debug(msg string, args ...any) {
	if l.groupName != "" && len(args) > 0 {
		l.logger.Debug(msg, slog.Group(l.groupName, args...))
	} else {
		l.logger.Debug(msg, args...)
	}
}
