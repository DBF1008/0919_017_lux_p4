// Package logger provides structured logging based on log/slog and
// request_id propagation helpers.
package logger

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"os"
)

type contextKey struct{}

// Init initializes the default slog logger. When debug is true the log
// level is set to DEBUG, otherwise INFO. Logs are written to stderr so
// they never mix with the terminal UI output on stdout.
func Init(debug bool) {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(handler))
}

// NewRequestID generates a unique request ID.
func NewRequestID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand should never fail; fall back to a fixed-size zero ID
		return "0000000000000000"
	}
	return hex.EncodeToString(b)
}

// WithRequestID returns a context carrying the given request ID.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, contextKey{}, requestID)
}

// RequestIDFrom extracts the request ID from the context, or returns an
// empty string if there is none.
func RequestIDFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if id, ok := ctx.Value(contextKey{}).(string); ok {
		return id
	}
	return ""
}

// FromContext returns a slog logger enriched with the request_id found in
// the context (if any).
func FromContext(ctx context.Context) *slog.Logger {
	if id := RequestIDFrom(ctx); id != "" {
		return slog.With(slog.String("request_id", id))
	}
	return slog.Default()
}
