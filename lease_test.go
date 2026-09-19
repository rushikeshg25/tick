package tick

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestGeneratorStopsWhenTheLeaseIsLost(t *testing.T) {
	safe, cancel := context.WithCancel(context.Background())
	g, err := New(7, WithClock(NewFakeClock(testStart)), WithLease(safe))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := g.Next(); err != nil {
		t.Fatalf("Next while the lease is held: %v", err)
	}

	cancel()

	// The watcher is a goroutine, so give it a moment to observe the
	// cancellation before asserting.
	deadline := time.Now().Add(time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		_, lastErr = g.Next()
		if lastErr != nil {
			break
		}
		time.Sleep(time.Millisecond)
	}

	if !errors.Is(lastErr, ErrLeaseLost) {
		t.Fatalf("Next after the lease was lost = %v, want ErrLeaseLost", lastErr)
	}

	// A generator that has lost its lease never issues again.
	for i := 0; i < 100; i++ {
		if _, err := g.TryNext(); !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("TryNext = %v, want ErrLeaseLost", err)
		}
	}
}

func TestGeneratorRejectsAnAlreadyCancelledLease(t *testing.T) {
	safe, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := New(7, WithClock(NewFakeClock(testStart)), WithLease(safe))
	if !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("New with a dead lease = %v, want ErrLeaseLost", err)
	}
}

func TestWithLeaseIsRejectedByFormatsWithoutANodeID(t *testing.T) {
	safe := context.Background()
	clock := NewFakeClock(testStart)

	if _, err := NewUUIDv7Generator(WithClock(clock), WithLease(safe)); err == nil {
		t.Error("NewUUIDv7Generator silently accepted WithLease")
	}
	if _, err := NewULIDGenerator(WithClock(clock), WithLease(safe)); err == nil {
		t.Error("NewULIDGenerator silently accepted WithLease")
	}
}
