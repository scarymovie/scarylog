package scarylog

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"strings"
)

type Logger struct {
	logger *slog.Logger
	// root is the handler slog.New was built from. Keeping it lets WithOverwrite
	// rebuild the attribute set on the same handler instead of constructing a
	// fresh one on every call.
	root      slog.Handler
	groupName string
	opts      *Options
}

type Options struct {
	// Level is nil when the caller never passed WithLevel. The built-in handler
	// then defaults to slog.LevelInfo; a handler supplied via WithHandler is left
	// alone. An explicit level is honored in both cases.
	Level        slog.Leveler
	DefaultAttrs []any
	GroupName    string
	AttrMap      map[string]string
	TimeFormat   string
	Handler      slog.Handler
	Writer       io.Writer
}

type Option func(*Options)

func WithLevel(level slog.Leveler) Option {
	return func(o *Options) {
		o.Level = level
	}
}

// WithHandler replaces the built-in handler entirely. The supplied handler owns
// its own output format, so WithAttrRemapping and WithTimeFormat can only reach
// the attributes scarylog passes it, never the handler's own time/level/msg keys
// — those are set by the handler's ReplaceAttr, which scarylog cannot reach from
// the outside. WithLevel is applied as an outer filter and does take effect.
// To keep every option working, use WithWriter instead.
func WithHandler(h slog.Handler) Option {
	return func(o *Options) {
		o.Handler = h
	}
}

// WithWriter sends the built-in JSON handler's output to w instead of os.Stdout.
// Unlike WithHandler it keeps every other option in effect (WithLevel,
// WithAttrRemapping, WithTimeFormat), which makes it the right choice for tests.
func WithWriter(w io.Writer) Option {
	return func(o *Options) {
		if w != nil {
			o.Writer = w
		}
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
	options := &Options{}

	for _, opt := range opts {
		opt(options)
	}

	return newLoggerWithOptions(options)
}

func newLoggerWithOptions(options *Options) *Logger {
	root := buildHandler(options)

	slogLogger := slog.New(root)
	if len(options.DefaultAttrs) > 0 {
		slogLogger = slogLogger.With(options.DefaultAttrs...)
	}

	return &Logger{
		logger:    slogLogger,
		root:      root,
		groupName: options.GroupName,
		opts:      options,
	}
}

// buildHandler resolves the options into the handler the logger writes through:
// either the caller's handler (wrapped so the options that *can* apply to it do)
// or the built-in JSON handler with every option applied natively.
func buildHandler(o *Options) slog.Handler {
	if o.Handler != nil {
		return wrapHandler(o.Handler, o)
	}

	w := o.Writer
	if w == nil {
		w = os.Stdout
	}
	level := o.Level
	if level == nil {
		level = slog.LevelInfo
	}

	handlerOpts := &slog.HandlerOptions{Level: level}
	if len(o.AttrMap) > 0 || o.TimeFormat != "" {
		handlerOpts.ReplaceAttr = func(groups []string, a slog.Attr) slog.Attr {
			if newKey, ok := o.AttrMap[a.Key]; ok {
				a.Key = newKey
			}

			if a.Key == slog.TimeKey && o.TimeFormat != "" {
				a.Value = slog.StringValue(a.Value.Time().Format(o.TimeFormat))
			}

			return a
		}
	}

	return slog.NewJSONHandler(w, handlerOpts)
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

// Log records msg at an arbitrary level, for callers that compute the level at
// runtime. ctx is forwarded to the handler.
func (l *Logger) Log(ctx context.Context, level slog.Level, msg string, args ...any) {
	if ctx == nil {
		ctx = context.Background()
	}
	l.log(ctx, level, msg, args...)
}

// Enabled reports whether a record at the given level would be handled. Use it
// to skip building expensive attributes.
func (l *Logger) Enabled(ctx context.Context, level slog.Level) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	return l.logger.Enabled(ctx, level)
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

// maxUnwrapDepth bounds the error chain walk in stackOf, so a self-referential
// error can't spin forever.
const maxUnwrapDepth = 100

// stackOf returns a stack trace rendered by err or by any error it wraps.
//
// Stack-carrying error libraries (github.com/pkg/errors, cockroachdb/errors)
// expose their trace through fmt.Formatter under %+v. Wrapping such an error
// with fmt.Errorf("...: %w", err) hides it again: *fmt.wrapError does not
// implement fmt.Formatter. Since wrapping is exactly what this package tells
// callers to do for context, the whole chain is searched rather than only the
// error on top — otherwise the documented idiom would silently disable stacks.
//
// The outermost trace wins: when several errors in the chain carry one, that is
// the most complete. The returned text belongs to that error, not to the full
// wrapped message — the record's message already carries err.Error() in full.
func stackOf(err error) (string, bool) {
	for depth := 0; err != nil && depth < maxUnwrapDepth; depth++ {
		if _, ok := err.(fmt.Formatter); ok {
			if s := fmt.Sprintf("%+v", err); s != err.Error() {
				return s, true
			}
		}

		switch e := err.(type) {
		case interface{ Unwrap() error }:
			err = e.Unwrap()
		case interface{ Unwrap() []error }:
			// errors.Join and multi-error wrappers: first branch with a trace.
			for _, sub := range e.Unwrap() {
				if s, ok := stackOf(sub); ok {
					return s, true
				}
			}
			return "", false
		default:
			return "", false
		}
	}
	return "", false
}

// Error logs err at the error level. The error itself is the message, so add
// context by wrapping it at the call site, e.g. fmt.Errorf("save user: %w", err).
// If err implements fmt.Formatter and renders a stack trace under %+v (as
// github.com/pkg/errors or cockroachdb/errors do), that stack is attached.
//
// Because the message is the fully rendered error, it carries whatever variable
// data the error text contains. When you need a stable, low-cardinality message
// for grouping and alerting, use ErrorMsg.
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

	if s, ok := stackOf(err); ok {
		allArgs = append(allArgs, slog.String("stack", s))
	}

	allArgs = append(allArgs, l.wrap(args)...)
	l.logger.Log(ctx, slog.LevelError, err.Error(), allArgs...)
}

// ErrorMsg logs err at the error level under the stable message msg, putting the
// error text in the "error" attribute instead of the message. Prefer it over
// Error when the message feeds grouping or alerting: err.Error() usually embeds
// variable data (addresses, ids, timeouts), which makes every record a distinct
// message. Passing a nil err is safe — only msg is logged.
func (l *Logger) ErrorMsg(msg string, err error, args ...any) {
	l.errorMsgLog(context.Background(), msg, err, caller(2), args...)
}

// ErrorMsgContext behaves like ErrorMsg but forwards ctx to the handler.
func (l *Logger) ErrorMsgContext(ctx context.Context, msg string, err error, args ...any) {
	l.errorMsgLog(ctx, msg, err, caller(2), args...)
}

// errorMsgLog is the shared implementation for ErrorMsg/ErrorMsgContext.
func (l *Logger) errorMsgLog(ctx context.Context, msg string, err error, callerStr string, args ...any) {
	allArgs := []any{
		"caller", callerStr,
	}

	if err != nil {
		allArgs = append(allArgs, slog.String("error", err.Error()))
		if s, ok := stackOf(err); ok {
			allArgs = append(allArgs, slog.String("stack", s))
		}
	}

	allArgs = append(allArgs, l.wrap(args)...)
	l.logger.Log(ctx, slog.LevelError, msg, allArgs...)
}

func (l *Logger) With(args ...any) *Logger {
	// Mirror the new attrs into a fresh Options so that attribute readers
	// (GetAttr/GetString) and WithOverwrite see them too, without mutating
	// the shared opts.
	newOpts := *l.opts
	newOpts.DefaultAttrs = append(append([]any{}, l.opts.DefaultAttrs...), args...)
	return &Logger{
		logger:    l.logger.With(args...),
		root:      l.root,
		groupName: l.groupName,
		opts:      &newOpts,
	}
}

// WithOverwrite creates a new logger with the given attributes, overwriting any existing attributes with the same key.
// It can handle attributes provided as key-value pairs (string, any), or as slog.Attr structs (including slog.Group).
// Attribute order is preserved: an overwritten key keeps its original position,
// and genuinely new keys are appended in the order given.
func (l *Logger) WithOverwrite(args ...any) *Logger {
	final := mergeAttrs(l.opts.DefaultAttrs, args)

	newOpts := *l.opts
	newOpts.DefaultAttrs = final
	// Group() only sets the logger's field, so l.groupName — not opts.GroupName —
	// is the group actually in force.
	newOpts.GroupName = l.groupName

	return &Logger{
		// Rebuild on the same root handler: reconstructing it here would drop a
		// custom handler's identity and allocate a new one on every call.
		logger:    slog.New(l.root).With(final...),
		root:      l.root,
		groupName: l.groupName,
		opts:      &newOpts,
	}
}

// attrEntry is one merged attribute, kept in insertion order. Either a key/value
// pair (isAttr false) or a slog.Attr, mirroring the two arg styles slog accepts.
type attrEntry struct {
	key    string
	val    any
	attr   slog.Attr
	isAttr bool
}

// mergeAttrs merges overrides onto base by key, preserving the order in which
// keys first appeared. Args in either style (string+any or slog.Attr) are
// accepted; anything else is skipped, exactly as slog would.
func mergeAttrs(base, overrides []any) []any {
	entries := make([]attrEntry, 0, len(base)/2+len(overrides)/2)
	index := make(map[string]int, len(base)/2+len(overrides)/2)

	put := func(e attrEntry) {
		if i, ok := index[e.key]; ok {
			entries[i] = e
			return
		}
		index[e.key] = len(entries)
		entries = append(entries, e)
	}

	collect := func(args []any) {
		for i := 0; i < len(args); {
			switch v := args[i].(type) {
			case string:
				if i+1 >= len(args) {
					// Dangling key with no value: slog would log it as a
					// "!BADKEY" record; drop it here as before.
					i++
					continue
				}
				put(attrEntry{key: v, val: args[i+1]})
				i += 2
			case slog.Attr:
				put(attrEntry{key: v.Key, attr: v, isAttr: true})
				i++
			default:
				i++
			}
		}
	}

	collect(base)
	collect(overrides)

	out := make([]any, 0, len(entries)*2)
	for _, e := range entries {
		if e.isAttr {
			out = append(out, e.attr)
		} else {
			out = append(out, e.key, e.val)
		}
	}
	return out
}

// Group returns a new logger that directs its output to the specified group.
// The new logger inherits all the settings of the parent logger.
func (l *Logger) Group(name string) *Logger {
	// Create a shallow copy of the logger, but with a new group name.
	return &Logger{
		logger:    l.logger,
		root:      l.root,
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
