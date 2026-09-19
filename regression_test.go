package tick

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// recordingClock notes every SleepUntil target so a test can assert not just
// that the generator waited, but what it waited for.
type recordingClock struct {
	*FakeClock
	targets []int64
}

func (c *recordingClock) SleepUntil(ms int64) {
	c.targets = append(c.targets, ms)
	c.FakeClock.SleepUntil(ms)
}

func TestRegressionWithinToleranceBorrowsSequenceSpace(t *testing.T) {
	g, clock := testGenerator(t, WithRegressionTolerance(10*time.Millisecond))

	first, err := g.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	watermark := first.Time().UnixMilli()

	clock.Step(-5 * time.Millisecond)

	second, err := g.Next()
	if err != nil {
		t.Fatalf("Next after a 5ms backward step: %v", err)
	}

	// The point of the borrow: the emitted ID keeps the old timestamp rather
	// than the regressed wall clock, and takes the next sequence number. A
	// test that only checks uniqueness here passes without borrowing at all.
	if got := second.Time().UnixMilli(); got != watermark {
		t.Errorf("borrowed ID has timestamp %d, want the watermark %d", got, watermark)
	}
	if got, want := second.Seq(), first.Seq()+1; got != want {
		t.Errorf("borrowed ID has sequence %d, want %d", got, want)
	}
	if second <= first {
		t.Errorf("borrowed ID %d is not greater than %d", second, first)
	}
}

func TestRegressionBeyondToleranceFails(t *testing.T) {
	g, clock := testGenerator(t, WithRegressionTolerance(10*time.Millisecond))

	if _, err := g.Next(); err != nil {
		t.Fatalf("Next: %v", err)
	}

	clock.Step(-500 * time.Millisecond)

	_, err := g.Next()
	if !errors.Is(err, ErrClockRegression) {
		t.Fatalf("Next after a 500ms backward step = %v, want ErrClockRegression", err)
	}
}

func TestWatermarkIsNeverRewound(t *testing.T) {
	g, clock := testGenerator(t, WithRegressionTolerance(50*time.Millisecond))

	var prev ID
	for i := 0; i < 200; i++ {
		id, err := g.Next()
		if err != nil {
			t.Fatalf("Next at %d: %v", i, err)
		}
		if id <= prev {
			t.Fatalf("ID went backward at %d: %d then %d", i, prev, id)
		}
		prev = id

		switch i % 4 {
		case 0:
			clock.Advance(3 * time.Millisecond)
		case 2:
			clock.Step(-2 * time.Millisecond)
		}
	}
}

func TestRecoveryAfterTheClockCatchesUp(t *testing.T) {
	g, clock := testGenerator(t, WithRegressionTolerance(10*time.Millisecond))

	first, err := g.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	clock.Step(-5 * time.Millisecond)
	if _, err := g.Next(); err != nil {
		t.Fatalf("Next while regressed: %v", err)
	}

	// Once real time overtakes the watermark the generator returns to normal
	// operation, with a fresh sequence.
	clock.Advance(20 * time.Millisecond)
	id, err := g.Next()
	if err != nil {
		t.Fatalf("Next after recovery: %v", err)
	}
	if got := id.Seq(); got != 0 {
		t.Errorf("sequence after recovery = %d, want 0", got)
	}
	if got, want := id.Time().UnixMilli(), clock.NowMillis(); got != want {
		t.Errorf("timestamp after recovery = %d, want %d", got, want)
	}
	if id <= first {
		t.Errorf("recovered ID %d is not greater than %d", id, first)
	}
}

// Pitfall 3: while regressed the watermark is ahead of the wall clock, so a
// wait computed from the wall clock returns immediately and the loop spins.
func TestNextWaitsRelativeToWatermarkNotWallClock(t *testing.T) {
	clock := &recordingClock{FakeClock: NewFakeClock(testStart)}
	g, err := New(7, WithClock(clock), WithRegressionTolerance(10*time.Millisecond))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for i := 0; i <= MaxSequence; i++ {
		if _, err := g.Next(); err != nil {
			t.Fatalf("Next at %d: %v", i, err)
		}
	}
	watermark := testStart.UnixMilli()

	clock.Step(-5 * time.Millisecond)
	clock.targets = nil

	id, err := g.Next()
	if err != nil {
		t.Fatalf("Next while regressed and exhausted: %v", err)
	}

	if len(clock.targets) != 1 {
		t.Fatalf("SleepUntil called %d times with targets %v, want exactly one wait", len(clock.targets), clock.targets)
	}
	if got, want := clock.targets[0], watermark+1; got != want {
		t.Errorf("waited for %d, want %d; the target must come from the watermark, not the wall clock", got, want)
	}
	if got, want := id.Time().UnixMilli(), watermark+1; got != want {
		t.Errorf("ID landed in millisecond %d, want %d", got, want)
	}
}

// Pitfall 1: reading the clock before loading the state lets a concurrent
// advance look exactly like the clock moving backward. With a zero tolerance
// any such false positive becomes an error, and the clock here only ever moves
// forward, so a single ErrClockRegression means the read order is wrong.
func TestNoFalseRegressionUnderContention(t *testing.T) {
	clock := NewFakeClock(testStart)
	g, err := New(7, WithClock(clock), WithRegressionTolerance(0))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

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

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20000; j++ {
				if _, err := g.Next(); err != nil {
					t.Errorf("spurious error with a forward-only clock: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(stop)
	ticker.Wait()
}

func TestClockBeforeEpochIsRejectedAtConstruction(t *testing.T) {
	// A host whose real-time clock was never set.
	clock := NewFakeClock(time.Unix(0, 0))

	_, err := New(7, WithClock(clock))
	if !errors.Is(err, ErrTimestampOutOfRange) {
		t.Fatalf("New with a 1970 clock = %v, want ErrTimestampOutOfRange", err)
	}
}

func TestClockSteppedBeforeEpochIsRejectedAtGeneration(t *testing.T) {
	g, clock := testGenerator(t)
	if _, err := g.Next(); err != nil {
		t.Fatalf("Next: %v", err)
	}

	clock.Step(testStart.Sub(time.Unix(0, 0)) * -1)

	_, err := g.Next()
	if !errors.Is(err, ErrTimestampOutOfRange) {
		t.Fatalf("Next with a 1970 clock = %v, want ErrTimestampOutOfRange", err)
	}
}

func TestNodeIDOutOfRangeIsRejected(t *testing.T) {
	_, err := New(MaxNodeID+1, WithClock(NewFakeClock(testStart)))
	if !errors.Is(err, ErrNodeIDOutOfRange) {
		t.Fatalf("New(%d) = %v, want ErrNodeIDOutOfRange", MaxNodeID+1, err)
	}
}

func TestNegativeToleranceIsRejected(t *testing.T) {
	_, err := New(7, WithClock(NewFakeClock(testStart)), WithRegressionTolerance(-time.Second))
	if err == nil {
		t.Fatal("New with a negative tolerance succeeded, want an error")
	}
}
