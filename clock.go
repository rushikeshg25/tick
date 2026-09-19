package tick

import (
	"sync"
	"time"
)

// Clock reports the passage of time to a Generator.
//
// Two readings are exposed because they fail differently. NowMillis is the
// wall clock: it is what IDs embed, and it can be stepped backward by NTP, by
// settimeofday, or by restoring a VM snapshot. Since is monotonic: it never
// moves backward, but it is only meaningful relative to when the Clock was
// created. Comparing the two is how a step is distinguished from ordinary
// drift.
type Clock interface {
	// NowMillis returns wall-clock milliseconds since the Unix epoch.
	// It may move backward.
	NowMillis() int64

	// Since returns monotonic time elapsed since the Clock was created.
	// It never moves backward.
	Since() time.Duration
}

// SystemClock reads the host clock. It is the only implementation in this
// package that calls time.Now.
type SystemClock struct {
	// origin carries both a wall reading and a monotonic reading, taken once
	// at construction. Holding both is what makes Drift computable.
	origin time.Time
}

// NewSystemClock returns a Clock backed by the host clock, with its drift
// baseline anchored at the moment of the call.
func NewSystemClock() *SystemClock {
	return &SystemClock{origin: time.Now()}
}

func (c *SystemClock) NowMillis() int64 { return time.Now().UnixMilli() }

func (c *SystemClock) Since() time.Duration { return time.Since(c.origin) }

// Drift reports how far the wall clock has diverged from what the monotonic
// clock says it should read, measured since this Clock was created.
//
// A steadily growing value is ordinary drift being corrected by NTP slew. A
// sudden jump is a step: the clock was set rather than allowed to run. A
// negative value means the wall clock moved backward, which is the condition
// a Generator must refuse to generate through.
//
// Round(0) strips the monotonic reading from origin, which forces the
// subtraction below to use wall-clock arithmetic; time.Since above keeps the
// monotonic reading and so uses monotonic arithmetic. The difference between
// those two answers is the drift.
func (c *SystemClock) Drift() time.Duration {
	wall := time.Now().Sub(c.origin.Round(0))
	mono := time.Since(c.origin)
	return wall - mono
}

// FakeClock is a Clock under test control. Wall time and monotonic time are
// advanced independently so that tests can reproduce the cases that break
// naive generators.
//
// Use Advance for ordinary time passing: both readings move together. Use
// Step for a clock that was set rather than allowed to run: only the wall
// reading moves, and a negative duration reproduces an NTP step backward.
//
// A FakeClock is safe for concurrent use.
type FakeClock struct {
	mu     sync.Mutex
	origin time.Time // wall reading at construction, the drift baseline
	wall   time.Time
	mono   time.Duration
}

// NewFakeClock returns a FakeClock whose wall reading starts at start and
// whose monotonic reading starts at zero.
func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{origin: start, wall: start}
}

func (c *FakeClock) NowMillis() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.wall.UnixMilli()
}

func (c *FakeClock) Since() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.mono
}

// Advance moves both the wall and monotonic readings forward by d. This is
// ordinary time passing, and d must not be negative.
func (c *FakeClock) Advance(d time.Duration) {
	if d < 0 {
		panic("tick: FakeClock.Advance with negative duration; use Step to move the wall clock backward")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.wall = c.wall.Add(d)
	c.mono += d
}

// Step moves only the wall reading by d, leaving monotonic time untouched.
// This models the clock being set: NTP correcting a large offset, a VM being
// restored from a snapshot, an operator running date -s. A negative d is the
// regression case a Generator must detect.
func (c *FakeClock) Step(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.wall = c.wall.Add(d)
}

// Drift reports the divergence between the wall and monotonic readings,
// matching the semantics of SystemClock.Drift.
func (c *FakeClock) Drift() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.wall.Sub(c.origin) - c.mono
}

var (
	_ Clock = (*SystemClock)(nil)
	_ Clock = (*FakeClock)(nil)
)
