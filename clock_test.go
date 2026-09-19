package tick

import (
	"sync"
	"testing"
	"time"
)

var testStart = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

func TestFakeClockAdvanceMovesBothReadings(t *testing.T) {
	c := NewFakeClock(testStart)

	if got, want := c.NowMillis(), testStart.UnixMilli(); got != want {
		t.Fatalf("NowMillis at start = %d, want %d", got, want)
	}
	if got := c.Since(); got != 0 {
		t.Fatalf("Since at start = %v, want 0", got)
	}

	c.Advance(1500 * time.Millisecond)

	if got, want := c.NowMillis(), testStart.Add(1500*time.Millisecond).UnixMilli(); got != want {
		t.Errorf("NowMillis after Advance = %d, want %d", got, want)
	}
	if got, want := c.Since(), 1500*time.Millisecond; got != want {
		t.Errorf("Since after Advance = %v, want %v", got, want)
	}
	if got := c.Drift(); got != 0 {
		t.Errorf("Drift after Advance = %v, want 0; advancing is not drifting", got)
	}
}

func TestFakeClockStepMovesOnlyWallClock(t *testing.T) {
	c := NewFakeClock(testStart)
	c.Advance(time.Second)

	c.Step(250 * time.Millisecond)

	if got, want := c.Since(), time.Second; got != want {
		t.Errorf("Since after Step = %v, want %v; a step must not move monotonic time", got, want)
	}
	if got, want := c.NowMillis(), testStart.Add(1250*time.Millisecond).UnixMilli(); got != want {
		t.Errorf("NowMillis after Step = %d, want %d", got, want)
	}
	if got, want := c.Drift(), 250*time.Millisecond; got != want {
		t.Errorf("Drift after forward Step = %v, want %v", got, want)
	}
}

func TestFakeClockStepBackwardIsDetectable(t *testing.T) {
	c := NewFakeClock(testStart)
	c.Advance(10 * time.Second)

	before := c.NowMillis()
	c.Step(-3 * time.Second)
	after := c.NowMillis()

	if after >= before {
		t.Fatalf("wall clock did not move backward: before=%d after=%d", before, after)
	}
	if got, want := c.Since(), 10*time.Second; got != want {
		t.Errorf("Since after backward Step = %v, want %v", got, want)
	}
	if got, want := c.Drift(), -3*time.Second; got != want {
		t.Errorf("Drift after backward Step = %v, want %v", got, want)
	}
}

func TestFakeClockAdvanceRejectsNegative(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("Advance(-1) did not panic; negative durations belong in Step")
		}
	}()
	NewFakeClock(testStart).Advance(-time.Millisecond)
}

func TestFakeClockConcurrentAccess(t *testing.T) {
	c := NewFakeClock(testStart)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				c.Advance(time.Millisecond)
				_ = c.NowMillis()
				_ = c.Since()
			}
		}()
	}
	wg.Wait()

	if got, want := c.Since(), 8000*time.Millisecond; got != want {
		t.Errorf("Since after concurrent advances = %v, want %v", got, want)
	}
}

func TestSystemClockMonotonicNeverGoesBackward(t *testing.T) {
	c := NewSystemClock()

	prev := c.Since()
	for i := 0; i < 10000; i++ {
		now := c.Since()
		if now < prev {
			t.Fatalf("Since went backward: %v then %v", prev, now)
		}
		prev = now
	}
}

func TestSystemClockNowMillisTracksWallClock(t *testing.T) {
	c := NewSystemClock()

	got := c.NowMillis()
	want := time.Now().UnixMilli()
	if diff := got - want; diff < -50 || diff > 50 {
		t.Errorf("NowMillis = %d, want within 50ms of %d", got, want)
	}
}

func TestFakeClockSleepUntilAutoAdvances(t *testing.T) {
	c := NewFakeClock(testStart)
	target := testStart.Add(5 * time.Millisecond).UnixMilli()

	c.SleepUntil(target)

	if got := c.NowMillis(); got != target {
		t.Errorf("NowMillis after SleepUntil = %d, want %d", got, target)
	}
	if got, want := c.Since(), 5*time.Millisecond; got != want {
		t.Errorf("Since after SleepUntil = %v, want %v; auto-advance moves both readings", got, want)
	}
}

func TestFakeClockSleepUntilPastIsNoOp(t *testing.T) {
	c := NewFakeClock(testStart)
	c.Advance(time.Second)
	before := c.NowMillis()

	c.SleepUntil(testStart.UnixMilli())

	if got := c.NowMillis(); got != before {
		t.Errorf("SleepUntil in the past moved the clock: %d -> %d", before, got)
	}
}

func TestFakeClockSleepUntilBlocksWithoutAutoAdvance(t *testing.T) {
	c := NewFakeClock(testStart)
	c.SetAutoAdvance(false)

	released := make(chan struct{})
	go func() {
		c.SleepUntil(testStart.Add(3 * time.Millisecond).UnixMilli())
		close(released)
	}()

	select {
	case <-released:
		t.Fatal("SleepUntil returned before the clock reached its target")
	case <-time.After(20 * time.Millisecond):
	}

	c.Advance(3 * time.Millisecond)

	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("SleepUntil did not wake after the clock advanced past its target")
	}
}

func TestSystemClockSleepUntilWaits(t *testing.T) {
	c := NewSystemClock()
	target := time.Now().Add(15 * time.Millisecond).UnixMilli()

	start := time.Now()
	c.SleepUntil(target)
	elapsed := time.Since(start)

	if time.Now().UnixMilli() < target {
		t.Errorf("SleepUntil returned early: now=%d target=%d", time.Now().UnixMilli(), target)
	}
	if elapsed > time.Second {
		t.Errorf("SleepUntil took %v, far longer than requested", elapsed)
	}
}
