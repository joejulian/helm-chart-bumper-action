package logutil

import (
	"context"

	"go.uber.org/zap"
)

type ctxKey struct{}

// WithLogger returns a new context with the provided logger attached.
func WithLogger(ctx context.Context, log *zap.Logger) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if log == nil {
		log = zap.NewNop()
	}
	return context.WithValue(ctx, ctxKey{}, log)
}

// FromContext returns the logger from ctx (or a no-op logger if none was set).
func FromContext(ctx context.Context) *zap.Logger {
	if ctx == nil {
		return zap.NewNop()
	}
	v := ctx.Value(ctxKey{})
	if v == nil {
		return zap.NewNop()
	}
	if l, ok := v.(*zap.Logger); ok && l != nil {
		return l
	}
	return zap.NewNop()
}
