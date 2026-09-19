package logging

import (
	"context"
	"testing"
)

func TestRequestIDRoundTrip(t *testing.T) {
	ctx := WithRequestID(context.Background(), "abc123")
	if got := RequestIDFromContext(ctx); got != "abc123" {
		t.Fatalf("RequestIDFromContext() = %q, want %q", got, "abc123")
	}
}

func TestWithRequestIDGeneratesWhenEmpty(t *testing.T) {
	ctx := WithRequestID(context.Background(), "")
	id := RequestIDFromContext(ctx)
	if len(id) != 32 {
		t.Fatalf("generated request id length = %d, want 32 (hex of 16 bytes)", len(id))
	}
}

func TestNewRequestIDUnique(t *testing.T) {
	seen := make(map[string]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		id := NewRequestID()
		if _, ok := seen[id]; ok {
			t.Fatalf("duplicate request id generated: %s", id)
		}
		seen[id] = struct{}{}
	}
}

func TestRequestIDFromContextMissing(t *testing.T) {
	if got := RequestIDFromContext(context.Background()); got != "" {
		t.Fatalf("RequestIDFromContext() = %q, want empty", got)
	}
	if got := RequestIDFromContext(nil); got != "" { // nolint
		t.Fatalf("RequestIDFromContext(nil) = %q, want empty", got)
	}
}

func TestFromContextNeverNil(t *testing.T) {
	if FromContext(context.Background()) == nil {
		t.Fatal("FromContext() returned nil without request id")
	}
	ctx := WithRequestID(context.Background(), "abc123")
	if FromContext(ctx) == nil {
		t.Fatal("FromContext() returned nil with request id")
	}
}

func TestShortID(t *testing.T) {
	if got := ShortID("0123456789abcdef"); got != "01234567" {
		t.Fatalf("ShortID() = %q, want %q", got, "01234567")
	}
	if got := ShortID("abc"); got != "abc" {
		t.Fatalf("ShortID() = %q, want %q", got, "abc")
	}
}
