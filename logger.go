package scarylog

import (
	"log/slog"
	"os"
)

type Logger struct {
	logger    *slog.Logger
	groupName string
	opts      *Options
}

type Options struct {
	Level        slog.Leveler
	DefaultAttrs []any
	GroupName    string
	AttrMap      map[string]string
	TimeFormat   string
	Handler      slog.Handler
}

type Option func(*Options)

func WithLevel(level slog.Leveler) Option {
	return func(o *Options) {
		o.Level = level
	}
}

func WithHandler(h slog.Handler) Option {
	return func(o *Options) {
		o.Handler = h
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

func WithAttrRemapping(attrMap map[string]string) Option {
	return func(o *Options) {
		o.AttrMap = attrMap
	}
}

func WithTimeFormat(timeFormat string) Option {
	return func(o *Options) {
		o.TimeFormat = timeFormat
	}
}

func NewLogger(opts ...Option) *Logger {
	options := &Options{
		Level: slog.LevelInfo,
	}

	for _, opt := range opts {
		opt(options)
	}

	return newLoggerWithOptions(options)
}

func newLoggerWithOptions(options *Options) *Logger {
	var handler slog.Handler
	if options.Handler != nil {
		handler = options.Handler
	} else {
		handlerOpts := &slog.HandlerOptions{
			Level: options.Level,
		}

		if len(options.AttrMap) > 0 || options.TimeFormat != "" {
			handlerOpts.ReplaceAttr = func(groups []string, a slog.Attr) slog.Attr {
				if newKey, ok := options.AttrMap[a.Key]; ok {
					a.Key = newKey
				}

				if a.Key == slog.TimeKey && options.TimeFormat != "" {
					a.Value = slog.StringValue(a.Value.Time().Format(options.TimeFormat))
				}

				return a
			}
		}
		handler = slog.NewJSONHandler(os.Stdout, handlerOpts)
	}

	slogLogger := slog.New(handler)
	if len(options.DefaultAttrs) > 0 {
		slogLogger = slogLogger.With(options.DefaultAttrs...)
	}

	return &Logger{
		logger:    slogLogger,
		groupName: options.GroupName,
		opts:      options,
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

func (l *Logger) With(args ...any) *Logger {
	return &Logger{
		logger:    l.logger.With(args...),
		groupName: l.groupName,
		opts:      l.opts,
	}
}

// WithOverwrite creates a new logger with the given attributes, overwriting any existing attributes with the same key.
// It can handle attributes provided as key-value pairs (string, any), or as slog.Attr structs (including slog.Group).
func (l *Logger) WithOverwrite(args ...any) *Logger {
	// Helper to parse different argument styles (string+any or slog.Attr) into a map.
	parseArgsToMap := func(args []any) map[string]any {
		attrs := make(map[string]any)
		for i := 0; i < len(args); {
			switch v := args[i].(type) {
			case string:
				if i+1 < len(args) {
					attrs[v] = args[i+1]
					i += 2
				} else {
					i++
				}
			case slog.Attr:
				attrs[v.Key] = v
				i++
			default:
				i++
			}
		}
		return attrs
	}

	// 1. Parse original and new arguments into maps.
	oldAttrsMap := parseArgsToMap(l.opts.DefaultAttrs)
	newAttrsMap := parseArgsToMap(args)

	// 2. Merge maps. New attributes overwrite old ones.
	for k, v := range newAttrsMap {
		oldAttrsMap[k] = v
	}

	// 3. Convert the merged map back to a slice of any for the logger.
	finalAttrs := make([]any, 0, len(oldAttrsMap))
	for _, v := range oldAttrsMap {
		// If the value is an Attr struct (like a group), add it directly.
		// Otherwise, reconstruct the key-value pair.
		if attr, ok := v.(slog.Attr); ok {
			finalAttrs = append(finalAttrs, attr)
		} else {
			// This branch is less likely if keys are unique, but handles the general case.
			// We need a key for this. Let's iterate the map properly.
		}
	}
	// A better way to convert map back to slice
	finalAttrs = make([]any, 0, len(oldAttrsMap)*2)
	for k, v := range oldAttrsMap {
		if attr, ok := v.(slog.Attr); ok {
			finalAttrs = append(finalAttrs, attr)
		} else {
			finalAttrs = append(finalAttrs, k, v)
		}
	}

	// 4. Create a new Options struct for the new logger.
	newOptions := &Options{
		Level:        l.opts.Level,
		DefaultAttrs: finalAttrs,
		GroupName:    l.opts.GroupName,
		AttrMap:      l.opts.AttrMap,
		TimeFormat:   l.opts.TimeFormat,
	}

	return newLoggerWithOptions(newOptions)

}

// Group returns a new logger that directs its output to the specified group.

// The new logger inherits all the settings of the parent logger.

func (l *Logger) Group(name string) *Logger {

	// Create a shallow copy of the logger, but with a new group name.

	return &Logger{

		logger: l.logger,

		groupName: name,

		opts: l.opts,
	}

}
