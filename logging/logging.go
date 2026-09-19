package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"os"
)

type contextKey struct{}

// requestIDKey is the context key carrying the per-HTTP-request correlation ID.
var requestIDKey = contextKey{}

// Init configures the global structured logger.
// JSON logs go to stderr so that JSON output on stdout (the --json flag) is
// not polluted; colored user-facing messages still go to the terminal.
func Init(debug bool) {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(handler))
}

// NewRequestID generates a random hexadecimal request id.
func NewRequestID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand should never fail; fall back to a readable placeholder.
		return "unknown-request-id"
	}
	return hex.EncodeToString(b)
}

// WithRequestID returns a copy of ctx carrying the request id. When id is
// empty a fresh id is generated.
func WithRequestID(ctx context.Context, id string) context.Context {
	if id == "" {
		id = NewRequestID()
	}
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestIDFromContext returns the request id stored in ctx, or "" if none.
func RequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// FromContext returns a logger annotated with the request id from ctx.
func FromContext(ctx context.Context) *slog.Logger {
	id := RequestIDFromContext(ctx)
	if id == "" {
		return slog.Default()
	}
	return slog.With("request_id", id)
}

// ShortID returns the first 8 characters of id for compact terminal display.
func ShortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
