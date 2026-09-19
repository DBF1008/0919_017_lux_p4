package logger

import (
	"context"
	"testing"
)

func TestRequestIDRoundTrip(t *testing.T) {
	ctx := context.Background()
	if id := RequestIDFrom(ctx); id != "" {
		t.Fatalf("expected empty request id, got %q", id)
	}
	id := NewRequestID()
	if len(id) != 16 {
		t.Fatalf("expected 16-char request id, got %q", id)
	}
	ctx = WithRequestID(ctx, id)
	if got := RequestIDFrom(ctx); got != id {
		t.Fatalf("expected %q, got %q", id, got)
	}
	if l := FromContext(ctx); l == nil {
		t.Fatal("expected non-nil logger")
	}
}

func TestNewRequestIDUnique(t *testing.T) {
	seen := make(map[string]struct{})
	for i := 0; i < 1000; i++ {
		id := NewRequestID()
		if _, ok := seen[id]; ok {
			t.Fatalf("duplicated request id %q", id)
		}
		seen[id] = struct{}{}
	}
}
