package scarylog

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"runtime"
	"strings"
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

// wrap nests the given args inside the logger's group when one is configured,
// so every leveled method shares identical grouping behavior.
func (l *Logger) wrap(args []any) []any {
	if l.groupName != "" && len(args) > 0 {
		return []any{slog.Group(l.groupName, args...)}
	}
	return args
}

// log is the shared sink for every leveled method. It forwards the given
// context.Context to slog so context-aware handlers (e.g. trace correlation)
// can enrich the record from request-scoped values in ctx.
func (l *Logger) log(ctx context.Context, level slog.Level, msg string, args ...any) {
	l.logger.Log(ctx, level, msg, l.wrap(args)...)
}

func (l *Logger) Info(msg string, args ...any) {
	l.log(context.Background(), slog.LevelInfo, msg, args...)
}

func (l *Logger) InfoContext(ctx context.Context, msg string, args ...any) {
	l.log(ctx, slog.LevelInfo, msg, args...)
}

func (l *Logger) Warn(msg string, args ...any) {
	l.log(context.Background(), slog.LevelWarn, msg, args...)
}

func (l *Logger) WarnContext(ctx context.Context, msg string, args ...any) {
	l.log(ctx, slog.LevelWarn, msg, args...)
}

func (l *Logger) Debug(msg string, args ...any) {
	l.log(context.Background(), slog.LevelDebug, msg, args...)
}

func (l *Logger) DebugContext(ctx context.Context, msg string, args ...any) {
	l.log(ctx, slog.LevelDebug, msg, args...)
}

func caller(skip int) string {
	_, file, line, ok := runtime.Caller(skip)
	if !ok {
		return "unknown"
	}
	return fmt.Sprintf("%s:%d", shortPath(file), line)
}

// shortPath trims an absolute path down to its last two segments
// (e.g. "pkg/file.go"), so caller attributes don't leak the build
// machine's filesystem layout and read like slog's own source field.
func shortPath(file string) string {
	idx := strings.LastIndexByte(file, '/')
	if idx < 0 {
		return file
	}
	if prev := strings.LastIndexByte(file[:idx], '/'); prev >= 0 {
		return file[prev+1:]
	}
	return file
}

// Error logs err at the error level. The error itself is the message, so add
// context by wrapping it at the call site, e.g. fmt.Errorf("save user: %w", err).
// If err implements fmt.Formatter and renders a stack trace under %+v (as
// github.com/pkg/errors or cockroachdb/errors do), that stack is attached.
func (l *Logger) Error(err error, args ...any) {
	l.errorLog(context.Background(), err, caller(2), args...)
}

// ErrorContext behaves like Error but forwards ctx to the handler.
func (l *Logger) ErrorContext(ctx context.Context, err error, args ...any) {
	l.errorLog(ctx, err, caller(2), args...)
}

// errorLog is the shared implementation for Error/ErrorContext. callerStr is
// captured by the public method so the reported caller is the user's call site.
func (l *Logger) errorLog(ctx context.Context, err error, callerStr string, args ...any) {
	if err == nil {
		l.logger.Log(ctx, slog.LevelError, "Error called with nil error", append([]any{"caller", callerStr}, l.wrap(args)...)...)
		return
	}

	allArgs := []any{
		"caller", callerStr,
	}

	if _, ok := err.(fmt.Formatter); ok {
		if s := fmt.Sprintf("%+v", err); s != err.Error() {
			allArgs = append(allArgs, slog.String("stack", s))
		}
	}

	allArgs = append(allArgs, l.wrap(args)...)
	l.logger.Log(ctx, slog.LevelError, err.Error(), allArgs...)
}

func (l *Logger) With(args ...any) *Logger {
	// Mirror the new attrs into a fresh Options so that attribute readers
	// (GetAttr/GetString) see them too, without mutating the shared opts.
	newOpts := *l.opts
	newOpts.DefaultAttrs = append(append([]any{}, l.opts.DefaultAttrs...), args...)
	return &Logger{
		logger:    l.logger.With(args...),
		groupName: l.groupName,
		opts:      &newOpts,
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
	maps.Copy(oldAttrsMap, newAttrsMap)

	// 3. Convert the merged map back to a slice of any for the logger.
	finalAttrs := make([]any, 0, len(oldAttrsMap)*2)
	for k, v := range oldAttrsMap {
		// If the value is an Attr struct (like a group), add it directly.
		// Otherwise, reconstruct the key-value pair.
		if attr, ok := v.(slog.Attr); ok {
			finalAttrs = append(finalAttrs, attr)
		} else {
			finalAttrs = append(finalAttrs, k, v)
		}
	}

	// 4. Create a new Options struct for the new logger, preserving all settings
	// including any custom handler.
	newOptions := &Options{
		Level:        l.opts.Level,
		DefaultAttrs: finalAttrs,
		GroupName:    l.opts.GroupName,
		AttrMap:      l.opts.AttrMap,
		TimeFormat:   l.opts.TimeFormat,
		Handler:      l.opts.Handler,
	}

	return newLoggerWithOptions(newOptions)
}

// Group returns a new logger that directs its output to the specified group.
// The new logger inherits all the settings of the parent logger.
func (l *Logger) Group(name string) *Logger {
	// Create a shallow copy of the logger, but with a new group name.
	return &Logger{
		logger:    l.logger,
		groupName: name,
		opts:      l.opts,
	}
}

// GetAttr retrieves an attribute value from the logger's DefaultAttrs by key.
// Returns the value and true if found, or nil and false if not found.
// It handles both key-value pairs (string, any) and slog.Attr entries, so it
// stays correct regardless of whether the attr was added via WithDefaultAttrs,
// With, or WithOverwrite.
func (l *Logger) GetAttr(key string) (any, bool) {
	args := l.opts.DefaultAttrs
	for i := 0; i < len(args); {
		switch v := args[i].(type) {
		case string:
			if i+1 >= len(args) {
				return nil, false
			}
			if v == key {
				return args[i+1], true
			}
			i += 2
		case slog.Attr:
			if v.Key == key {
				return v.Value.Any(), true
			}
			i++
		default:
			i++
		}
	}
	return nil, false
}

// GetString retrieves a string attribute from the logger's DefaultAttrs by key.
// Returns the value and true if found and is a string, or empty string and false otherwise.
func (l *Logger) GetString(key string) (string, bool) {
	if val, ok := l.GetAttr(key); ok {
		if s, ok := val.(string); ok {
			return s, true
		}
	}
	return "", false
}

// GetAttrName returns the remapped attribute name if it exists in AttrMap, otherwise returns the original key.
func (l *Logger) GetAttrName(key string) string {
	if l.opts.AttrMap == nil {
		return key
	}
	if newName, ok := l.opts.AttrMap[key]; ok {
		return newName
	}
	return key
}
