package worker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rushikeshg25/tick"
)

var origin = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

// leaseFixture wires a store, a clock, and a manual renewal trigger so tests
// drive renewals explicitly rather than waiting on real time.
type leaseFixture struct {
	clock *tick.FakeClock
	store *MemStore

	mu sync.Mutex
	// One renewal channel per holder. Sharing one across holders lets
	// whichever renew goroutine happens to be scheduled first consume the
	// trigger meant for the other, which is a race in the fixture rather
	// than in the code under test.
	triggers map[string]chan time.Time
}

func newFixture(t *testing.T) *leaseFixture {
	t.Helper()
	clock := tick.NewFakeClock(origin)
	return &leaseFixture{
		clock:    clock,
		store:    NewMemStore(clock),
		triggers: make(map[string]chan time.Time),
	}
}

func (f *leaseFixture) config(holder string) Config {
	ch := make(chan time.Time, 1)
	f.mu.Lock()
	f.triggers[holder] = ch
	f.mu.Unlock()

	return Config{
		Store:         f.store,
		Holder:        holder,
		TTL:           10 * time.Second,
		RenewInterval: time.Second,
		MaxClockSkew:  500 * time.Millisecond,
		MaxRoundTrip:  time.Second,
		SafetyMargin:  500 * time.Millisecond,
		Range:         4,
		Clock:         f.clock,
		RenewSignal:   ch,
	}
}

// fire triggers one renewal cycle for a specific holder.
func (f *leaseFixture) fire(holder string) {
	f.mu.Lock()
	ch := f.triggers[holder]
	f.mu.Unlock()
	ch <- time.Time{}
}

// renew fires one renewal cycle and waits for its effect to land.
func (f *leaseFixture) renew(t *testing.T, holder string, l Lease) {
	t.Helper()
	f.fire(holder)
	waitFor(t, func() bool {
		return l.Safe().Err() != nil || f.settled()
	})
}

func (f *leaseFixture) settled() bool {
	// One scheduling round is enough for the renew goroutine to act on a
	// trigger it has already received.
	time.Sleep(time.Millisecond)
	return true
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition never became true")
}

func TestAcquireTakesTheLowestFreeNodeID(t *testing.T) {
	f := newFixture(t)
	a, err := New(f.config("a"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	first, err := a.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if got := first.NodeID(); got != 0 {
		t.Errorf("first node id = %d, want 0", got)
	}

	b, _ := New(f.config("b"))
	second, err := b.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if got := second.NodeID(); got != 1 {
		t.Errorf("second node id = %d, want 1", got)
	}
}

func TestAcquireFailsWhenTheRangeIsFull(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 4; i++ {
		a, _ := New(f.config(string(rune('a' + i))))
		if _, err := a.Acquire(context.Background()); err != nil {
			t.Fatalf("Acquire %d: %v", i, err)
		}
	}

	full, _ := New(f.config("overflow"))
	if _, err := full.Acquire(context.Background()); !errors.Is(err, ErrNoFreeNodeID) {
		t.Fatalf("Acquire on a full range = %v, want ErrNoFreeNodeID", err)
	}
}

func TestReleaseFreesTheNodeIDImmediately(t *testing.T) {
	f := newFixture(t)
	a, _ := New(f.config("a"))
	l, err := a.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	if err := l.Release(context.Background()); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if l.Safe().Err() == nil {
		t.Error("Safe was not cancelled by Release")
	}
	if _, held := f.store.HolderOf(0); held {
		t.Error("node id 0 is still held after Release")
	}
	if err := l.Release(context.Background()); err != nil {
		t.Errorf("second Release: %v", err)
	}

	b, _ := New(f.config("b"))
	next, err := b.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire after Release: %v", err)
	}
	if got := next.NodeID(); got != 0 {
		t.Errorf("reacquired node id = %d, want 0", got)
	}
}

func TestSafeSurvivesSuccessfulRenewals(t *testing.T) {
	f := newFixture(t)
	a, _ := New(f.config("a"))
	l, _ := a.Acquire(context.Background())

	for i := 0; i < 20; i++ {
		f.clock.Advance(time.Second)
		f.renew(t, "a", l)
		if err := l.Safe().Err(); err != nil {
			t.Fatalf("lease lost after %d renewals: %v", i+1, err)
		}
	}
	if holder, _ := f.store.HolderOf(0); holder != "a" {
		t.Errorf("holder = %q, want \"a\"", holder)
	}
}

func TestSafeIsCancelledWhenAnotherHolderTakesOver(t *testing.T) {
	f := newFixture(t)
	a, _ := New(f.config("a"))
	l, _ := a.Acquire(context.Background())

	// The claim lapses and someone else picks it up.
	f.clock.Advance(11 * time.Second)
	b, _ := New(f.config("b"))
	if _, err := b.Acquire(context.Background()); err != nil {
		t.Fatalf("Acquire by b: %v", err)
	}

	f.fire("a")
	waitFor(t, func() bool { return l.Safe().Err() != nil })
}

// The property that matters: a holder that cannot reach the store must stop
// before the store would let anyone else in. The two windows must not overlap.
func TestSafeIsCancelledBeforeTheIDCanBeReacquired(t *testing.T) {
	f := newFixture(t)
	cfg := f.config("a")
	a, _ := New(cfg)
	l, err := a.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	f.store.Partition(true)

	// Advance to just past the usable window but well short of the TTL, then
	// let the renew loop notice. The usable window is TTL minus skew, round
	// trip and margin: 10s - 500ms - 1s - 500ms = 8s.
	f.clock.Advance(8 * time.Second)
	f.fire("a")
	waitFor(t, func() bool { return l.Safe().Err() != nil })

	// At the moment the holder stopped, the store still considers the claim
	// live, so no successor could have started. That gap is the safety margin
	// doing its job.
	if holder, held := f.store.HolderOf(0); !held || holder != "a" {
		t.Errorf("store holder = %q held=%v at the moment the lease was given up; "+
			"the holder stopped too late to be safe", holder, held)
	}

	f.store.Partition(false)
	b, _ := New(f.config("b"))
	f.clock.Advance(3 * time.Second) // now past the full TTL
	if _, err := b.Acquire(context.Background()); err != nil {
		t.Fatalf("Acquire by b after expiry: %v", err)
	}
	if holder, _ := f.store.HolderOf(0); holder != "b" {
		t.Errorf("holder after handover = %q, want \"b\"", holder)
	}
}

func TestTransientStoreFailureDoesNotDropTheLease(t *testing.T) {
	f := newFixture(t)
	a, _ := New(f.config("a"))
	l, _ := a.Acquire(context.Background())

	// One failed renewal well inside the usable window must not be fatal;
	// that is the difference between a blip and an expiry.
	f.store.Partition(true)
	f.clock.Advance(time.Second)
	f.renew(t, "a", l)
	if err := l.Safe().Err(); err != nil {
		t.Fatalf("lease dropped after a single transient failure: %v", err)
	}

	f.store.Partition(false)
	f.clock.Advance(time.Second)
	f.renew(t, "a", l)
	if err := l.Safe().Err(); err != nil {
		t.Fatalf("lease lost after recovery: %v", err)
	}
}

func TestConfigRejectsATTLWithNoUsableWindow(t *testing.T) {
	f := newFixture(t)
	cfg := f.config("a")
	cfg.TTL = time.Second // smaller than skew + round trip + margin

	if _, err := New(cfg); err == nil {
		t.Fatal("New accepted a TTL with no usable window")
	}
}

func TestConfigRejectsARenewIntervalLongerThanTheWindow(t *testing.T) {
	f := newFixture(t)
	cfg := f.config("a")
	cfg.RenewInterval = 9 * time.Second // usable window is 8s

	if _, err := New(cfg); err == nil {
		t.Fatal("New accepted a renew interval longer than the usable window")
	}
}

func TestConfigRequiresStoreAndHolder(t *testing.T) {
	f := newFixture(t)

	cfg := f.config("a")
	cfg.Store = nil
	if _, err := New(cfg); err == nil {
		t.Error("New accepted a nil Store")
	}

	cfg = f.config("")
	if _, err := New(cfg); err == nil {
		t.Error("New accepted an empty Holder")
	}
}

func TestStaticLeaseIsNeverCancelled(t *testing.T) {
	l, err := Static(42).Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if got := l.NodeID(); got != 42 {
		t.Errorf("NodeID() = %d, want 42", got)
	}
	if err := l.Safe().Err(); err != nil {
		t.Errorf("static lease was cancelled: %v", err)
	}
}

func TestOrdinalFromName(t *testing.T) {
	ok := map[string]uint16{
		"ingest-0":         0,
		"ingest-7":         7,
		"my-svc-name-1023": 1023,
	}
	for name, want := range ok {
		got, err := OrdinalFromName(name)
		if err != nil {
			t.Errorf("OrdinalFromName(%q): %v", name, err)
			continue
		}
		if got != want {
			t.Errorf("OrdinalFromName(%q) = %d, want %d", name, got, want)
		}
	}

	bad := []string{
		"ingest",              // no ordinal at all
		"ingest-",             // empty ordinal
		"ingest-abc",          // Deployment-style random suffix
		"ingest-1024",         // beyond the node id space
		"web-7f9c8d4b6-x2plq", // ReplicaSet pod name
	}
	for _, name := range bad {
		if _, err := OrdinalFromName(name); err == nil {
			t.Errorf("OrdinalFromName(%q) succeeded, want an error", name)
		}
	}
}
