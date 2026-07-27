package scarylog

import (
	"context"
	"log/slog"
)

// optionsHandler applies the options that can still be honored on top of a
// handler supplied through WithHandler.
//
// A supplied handler owns its own output format: it renders time, level and msg
// under its own keys via its own ReplaceAttr. An outer handler cannot reach
// those — by the time a record arrives here they are struct fields on
// slog.Record, not attributes. So WithAttrRemapping and WithTimeFormat are
// applied to the record's attributes only. WithLevel, in contrast, is enforced
// here and takes precedence over the inner handler's own level, because it is an
// explicit instruction from the caller.
type optionsHandler struct {
	inner   slog.Handler
	level   slog.Leveler
	attrMap map[string]string
	timeFmt string
}

// wrapHandler returns h untouched unless one of the options actually applies.
func wrapHandler(h slog.Handler, o *Options) slog.Handler {
	if o.Level == nil && len(o.AttrMap) == 0 && o.TimeFormat == "" {
		return h
	}
	return &optionsHandler{
		inner:   h,
		level:   o.Level,
		attrMap: o.AttrMap,
		timeFmt: o.TimeFormat,
	}
}

func (h *optionsHandler) Enabled(ctx context.Context, level slog.Level) bool {
	if h.level != nil {
		return level >= h.level.Level()
	}
	return h.inner.Enabled(ctx, level)
}

func (h *optionsHandler) Handle(ctx context.Context, r slog.Record) error {
	if !h.rewrites() {
		return h.inner.Handle(ctx, r)
	}

	out := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(h.rewrite(a))
		return true
	})
	return h.inner.Handle(ctx, out)
}

func (h *optionsHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if h.rewrites() {
		out := make([]slog.Attr, len(attrs))
		for i, a := range attrs {
			out[i] = h.rewrite(a)
		}
		attrs = out
	}
	return &optionsHandler{
		inner:   h.inner.WithAttrs(attrs),
		level:   h.level,
		attrMap: h.attrMap,
		timeFmt: h.timeFmt,
	}
}

func (h *optionsHandler) WithGroup(name string) slog.Handler {
	return &optionsHandler{
		inner:   h.inner.WithGroup(name),
		level:   h.level,
		attrMap: h.attrMap,
		timeFmt: h.timeFmt,
	}
}

// rewrites reports whether any attribute-level option is configured.
func (h *optionsHandler) rewrites() bool {
	return len(h.attrMap) > 0 || h.timeFmt != ""
}

// rewrite applies the key remapping and time format to a single attribute,
// descending into groups. Values of kind LogValuer are left alone: the inner
// handler resolves them.
func (h *optionsHandler) rewrite(a slog.Attr) slog.Attr {
	if newKey, ok := h.attrMap[a.Key]; ok {
		a.Key = newKey
	}

	switch a.Value.Kind() {
	case slog.KindGroup:
		src := a.Value.Group()
		out := make([]slog.Attr, len(src))
		for i, g := range src {
			out[i] = h.rewrite(g)
		}
		a.Value = slog.GroupValue(out...)
	case slog.KindTime:
		if h.timeFmt != "" {
			a.Value = slog.StringValue(a.Value.Time().Format(h.timeFmt))
		}
	}

	return a
}
