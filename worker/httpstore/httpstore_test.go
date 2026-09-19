package httpstore

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rushikeshg25/tick"
	"github.com/rushikeshg25/tick/worker"
)

func newPair(t *testing.T) (*tick.FakeClock, *Client, *httptest.Server) {
	t.Helper()
	clock := tick.NewFakeClock(time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC))
	srv := httptest.NewServer(NewServer(clock).Handler())
	t.Cleanup(srv.Close)

	c, err := NewClient(srv.URL, time.Second)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return clock, c, srv
}

func TestStoreSatisfiesTheContract(t *testing.T) {
	clock, store, _ := newPair(t)
	ctx := context.Background()

	ok, err := store.TryAcquire(ctx, 0, "a", 10*time.Second)
	if err != nil || !ok {
		t.Fatalf("TryAcquire by a = %v, %v; want true, nil", ok, err)
	}

	// A second holder must be refused while the claim is live.
	ok, err = store.TryAcquire(ctx, 0, "b", 10*time.Second)
	if err != nil {
		t.Fatalf("TryAcquire by b: %v", err)
	}
	if ok {
		t.Fatal("TryAcquire by b succeeded while a held the claim")
	}

	// Renewal by the wrong holder must fail without disturbing the claim.
	ok, err = store.Renew(ctx, 0, "b", 10*time.Second)
	if err != nil {
		t.Fatalf("Renew by b: %v", err)
	}
	if ok {
		t.Fatal("Renew by b succeeded; only the holder may renew")
	}

	if ok, err := store.Renew(ctx, 0, "a", 10*time.Second); err != nil || !ok {
		t.Fatalf("Renew by a = %v, %v; want true, nil", ok, err)
	}

	// After expiry the claim is free.
	clock.Advance(11 * time.Second)
	if ok, err := store.TryAcquire(ctx, 0, "b", 10*time.Second); err != nil || !ok {
		t.Fatalf("TryAcquire by b after expiry = %v, %v; want true, nil", ok, err)
	}
}

func TestReleaseByTheWrongHolderIsANoOp(t *testing.T) {
	_, store, _ := newPair(t)
	ctx := context.Background()

	if _, err := store.TryAcquire(ctx, 3, "a", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := store.Release(ctx, 3, "b"); err != nil {
		t.Fatalf("Release by b: %v", err)
	}
	if ok, _ := store.TryAcquire(ctx, 3, "c", 10*time.Second); ok {
		t.Fatal("a stranger's Release freed the claim")
	}
}

func TestUnreachableCoordinatorIsAnError(t *testing.T) {
	_, store, srv := newPair(t)
	ctx := context.Background()

	if _, err := store.TryAcquire(ctx, 0, "a", 10*time.Second); err != nil {
		t.Fatal(err)
	}

	srv.Close() // the coordinator goes away mid-flight

	if _, err := store.Renew(ctx, 0, "a", 10*time.Second); err == nil {
		t.Fatal("Renew against a dead coordinator succeeded")
	}
}

// End to end: a real allocator over HTTP, driving a real generator.
func TestAllocatorOverHTTPDrivesAGenerator(t *testing.T) {
	clock, store, _ := newPair(t)

	alloc, err := worker.New(worker.Config{
		Store:  store,
		Holder: "pod-a",
		TTL:    10 * time.Second,
		Clock:  clock,
		Range:  4,
	})
	if err != nil {
		t.Fatalf("worker.New: %v", err)
	}

	lease, err := alloc.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer lease.Release(context.Background())

	gen, err := tick.New(lease.NodeID(), tick.WithClock(clock), tick.WithLease(lease.Safe()))
	if err != nil {
		t.Fatalf("tick.New: %v", err)
	}

	seen := make(map[tick.ID]bool)
	for i := 0; i < 5000; i++ {
		id, err := gen.Next()
		if err != nil {
			t.Fatalf("Next at %d: %v", i, err)
		}
		if seen[id] {
			t.Fatalf("duplicate ID %d", id)
		}
		seen[id] = true
		if i%1000 == 0 {
			clock.Advance(time.Millisecond)
		}
		if got := id.Node(); got != lease.NodeID() {
			t.Fatalf("id.Node() = %d, want %d", got, lease.NodeID())
		}
	}
}
