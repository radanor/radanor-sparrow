package logging

import (
	"context"
	"io"
	"log/slog"
	"strings"
)

var sensitiveKeys = map[string]struct{}{
	"ticket": {}, "token": {}, "secret": {}, "cookie": {}, "authorization": {}, "csrf": {}, "session": {},
}

func New(out io.Writer, level slog.Level) *slog.Logger {
	return slog.New(&redactingHandler{next: slog.NewJSONHandler(out, &slog.HandlerOptions{Level: level})})
}

type redactingHandler struct{ next slog.Handler }

func (h *redactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}
func (h *redactingHandler) Handle(ctx context.Context, record slog.Record) error {
	clean := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	record.Attrs(func(a slog.Attr) bool { clean.AddAttrs(redact(a)); return true })
	return h.next.Handle(ctx, clean)
}
func (h *redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		clean[i] = redact(a)
	}
	return &redactingHandler{next: h.next.WithAttrs(clean)}
}
func (h *redactingHandler) WithGroup(name string) slog.Handler {
	return &redactingHandler{next: h.next.WithGroup(name)}
}

func redact(a slog.Attr) slog.Attr {
	if _, ok := sensitiveKeys[strings.ToLower(a.Key)]; ok {
		return slog.String(a.Key, "[REDACTED]")
	}
	if a.Value.Kind() == slog.KindGroup {
		group := a.Value.Group()
		for i := range group {
			group[i] = redact(group[i])
		}
		return slog.Group(a.Key, attrsToAny(group)...)
	}
	return a
}

func attrsToAny(attrs []slog.Attr) []any {
	out := make([]any, len(attrs))
	for i := range attrs {
		out[i] = attrs[i]
	}
	return out
}
