package tick

import (
	"errors"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"
)

func testGenerator(t *testing.T, opts ...Option) (*Generator, *FakeClock) {
	t.Helper()
	c := NewFakeClock(testStart)
	g, err := New(7, append([]Option{WithClock(c)}, opts...)...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return g, c
}

// assertUnique sorts in place and reports the first duplicate. Sorting beats a
// map here: 6.4M map entries cost well over a gigabyte, and the sorted order
// is needed for the ordering checks anyway.
func assertUnique(t *testing.T, ids []ID) {
	t.Helper()
	slices.Sort(ids)
	for i := 1; i < len(ids); i++ {
		if ids[i] == ids[i-1] {
			t.Fatalf("duplicate ID %d at sorted position %d of %d", ids[i], i, len(ids))
		}
	}
}

func TestNextIsUniqueAndMonotonic(t *testing.T) {
	g, clock := testGenerator(t)

	const n = 200000
	ids := make([]ID, 0, n)
	prev := ID(-1)
	for i := 0; i < n; i++ {
		id, err := g.Next()
		if err != nil {
			t.Fatalf("Next at %d: %v", i, err)
		}
		if id <= prev {
			t.Fatalf("Next went backward at %d: %d then %d", i, prev, id)
		}
		prev = id
		ids = append(ids, id)

		if i%500 == 0 {
			clock.Advance(time.Millisecond)
		}
	}
	assertUnique(t, ids)
}

func TestNextEmbedsNodeID(t *testing.T) {
	c := NewFakeClock(testStart)
	for _, node := range []uint16{0, 1, 511, MaxNodeID} {
		g, err := New(node, WithClock(c))
		if err != nil {
			t.Fatalf("New(%d): %v", node, err)
		}
		id, err := g.Next()
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if got := id.Node(); got != node {
			t.Errorf("id.Node() = %d, want %d", got, node)
		}
		if got := g.NodeID(); got != node {
			t.Errorf("g.NodeID() = %d, want %d", got, node)
		}
	}
}

func TestNextTimestampTracksTheClock(t *testing.T) {
	g, clock := testGenerator(t)

	clock.Advance(1234 * time.Millisecond)
	id, err := g.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if got, want := id.Time().UnixMilli(), clock.NowMillis(); got != want {
		t.Errorf("id.Time() = %d, want %d", got, want)
	}
}

func TestConcurrentNextIsUnique(t *testing.T) {
	// Runs under -cpu 1,2,8: at GOMAXPROCS=1 the CAS essentially never fails
	// and none of the interesting interleavings are exercised.
	t.Logf("GOMAXPROCS=%d", runtime.GOMAXPROCS(0))

	g, clock := testGenerator(t)

	const goroutines = 32
	const perGoroutine = 20000

	stop := make(chan struct{})
	var ticker sync.WaitGroup
	ticker.Add(1)
	go func() {
		defer ticker.Done()
		for {
			select {
			case <-stop:
				return
			default:
				clock.Advance(time.Millisecond)
			}
		}
	}()

	batches := make([][]ID, goroutines)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			local := make([]ID, 0, perGoroutine)
			prev := ID(-1)
			for j := 0; j < perGoroutine; j++ {
				id, err := g.Next()
				if err != nil {
					t.Errorf("Next: %v", err)
					return
				}
				// A single goroutine must still observe its own IDs
				// increasing, because the watermark only ever moves forward.
				if id <= prev {
					t.Errorf("IDs went backward within one goroutine: %d then %d", prev, id)
					return
				}
				prev = id
				local = append(local, id)
			}
			batches[i] = local
		}(i)
	}
	wg.Wait()
	close(stop)
	ticker.Wait()

	all := make([]ID, 0, goroutines*perGoroutine)
	for _, b := range batches {
		all = append(all, b...)
	}
	assertUnique(t, all)
}

func TestSequenceExhaustionBlocksUntilNextMillisecond(t *testing.T) {
	g, clock := testGenerator(t)
	start := clock.NowMillis()

	for i := 0; i <= MaxSequence; i++ {
		id, err := g.Next()
		if err != nil {
			t.Fatalf("Next at %d: %v", i, err)
		}
		if got := id.Time().UnixMilli(); got != start {
			t.Fatalf("ID %d landed in millisecond %d, want %d", i, got, start)
		}
	}

	// The next one cannot fit in this millisecond, so it must wait for the
	// following one and restart the sequence.
	id, err := g.Next()
	if err != nil {
		t.Fatalf("Next after exhaustion: %v", err)
	}
	if got, want := id.Time().UnixMilli(), start+1; got != want {
		t.Errorf("ID after exhaustion is in millisecond %d, want %d", got, want)
	}
	if got := id.Seq(); got != 0 {
		t.Errorf("sequence after rollover = %d, want 0", got)
	}
}

func TestTryNextReportsExhaustionInsteadOfWaiting(t *testing.T) {
	g, clock := testGenerator(t)
	before := clock.NowMillis()

	for i := 0; i <= MaxSequence; i++ {
		if _, err := g.TryNext(); err != nil {
			t.Fatalf("TryNext at %d: %v", i, err)
		}
	}

	_, err := g.TryNext()
	if !errors.Is(err, ErrSequenceExhausted) {
		t.Fatalf("TryNext after exhaustion = %v, want ErrSequenceExhausted", err)
	}
	if got := clock.NowMillis(); got != before {
		t.Errorf("TryNext advanced the clock from %d to %d; it must not wait", before, got)
	}
}

func TestSequenceCapacityIsExactlyMaxSequencePlusOne(t *testing.T) {
	g, _ := testGenerator(t)

	n := 0
	for {
		if _, err := g.TryNext(); err != nil {
			break
		}
		n++
		if n > MaxSequence+2 {
			t.Fatalf("generated %d IDs in one millisecond, want %d", n, MaxSequence+1)
		}
	}
	if want := MaxSequence + 1; n != want {
		t.Errorf("generated %d IDs in one millisecond, want %d", n, want)
	}
}
