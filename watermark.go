package tick

import (
	"fmt"
	"sync/atomic"
	"time"
)

// watermark advances a (timestamp, counter) pair and is the shared core of
// every format in this package. Snowflake turns the pair into a 64-bit
// integer; UUIDv7 and ULID use the counter to order IDs within a millisecond.
//
// Both fields live in one atomic word. They must advance together or not at
// all, and packing them makes that structural: a single CompareAndSwap
// publishes both, so no goroutine can observe a new timestamp beside a stale
// counter. That interleaving is what breaks lock-free generators which keep
// the two fields in separate atomics.
//
// A watermark is safe for concurrent use.
type watermark struct {
	// state holds timestamp<<bits | counter, where timestamp is milliseconds
	// since Epoch.
	state atomic.Uint64

	// Keep the read-mostly fields below off the cache line that state lives
	// on. Without this every CompareAndSwap invalidates the line holding
	// fields that are only ever read.
	_ [56]byte

	clock Clock
	tol   time.Duration
	bits  uint
	max   uint64
}

func newWatermark(clock Clock, tol time.Duration, bits uint) *watermark {
	return &watermark{
		clock: clock,
		tol:   tol,
		bits:  bits,
		max:   1<<bits - 1,
	}
}

// timestamp reads the wall clock as an offset from Epoch, rejecting readings
// the layout cannot represent.
//
// The lower bound is the one that fires in practice: a host whose real-time
// clock was never set boots in 1970, and the resulting negative offset would
// otherwise be shifted into the layout and emitted as a meaningless ID.
func (w *watermark) timestamp() (uint64, error) {
	wall := w.clock.NowMillis()
	ms := wall - Epoch
	if ms < 0 || ms > MaxTimestamp {
		return 0, fmt.Errorf("%w: clock reads %s, outside [%s, %s]",
			ErrTimestampOutOfRange,
			time.UnixMilli(wall).UTC().Format(time.RFC3339),
			time.UnixMilli(Epoch).UTC().Format(time.RFC3339),
			time.UnixMilli(Epoch+MaxTimestamp).UTC().Format(time.RFC3339))
	}
	return uint64(ms), nil
}

// advance moves the watermark forward and returns the position it claimed.
// With block set it waits out an exhausted counter; otherwise it reports
// ErrSequenceExhausted.
func (w *watermark) advance(block bool) (ts, seq uint64, err error) {
	tolMs := uint64(w.tol / time.Millisecond)

	for {
		// Load the state before reading the clock, never the other way round.
		// Reading the clock first lets a concurrent advance move the watermark
		// past the reading we already took, which then looks exactly like the
		// clock having moved backward. See docs/PITFALLS.md, pitfall 1.
		old := w.state.Load()
		oldTs, oldSeq := w.unpack(old)

		now, err := w.timestamp()
		if err != nil {
			return 0, 0, err
		}

		var newTs, newSeq uint64
		if now > oldTs {
			newTs, newSeq = now, 0
		} else {
			// Two cases share this branch. When now == oldTs it is the
			// ordinary same-millisecond path and the regression below is zero.
			// When now < oldTs the wall clock has moved backward, and holding
			// the watermark while borrowing counter space keeps uniqueness:
			// no (timestamp, counter) pair is ever reused. The watermark is
			// never rewound.
			if oldTs-now > tolMs {
				return 0, 0, fmt.Errorf("%w: wall clock is %dms behind the watermark, tolerance is %v",
					ErrClockRegression, oldTs-now, w.tol)
			}
			if oldSeq >= w.max {
				if !block {
					return 0, 0, ErrSequenceExhausted
				}
				// Wait relative to the watermark rather than the wall clock.
				// While regressed the watermark is already ahead of the clock,
				// so waiting for now+1 would return immediately and spin.
				// See docs/PITFALLS.md, pitfall 3.
				w.clock.SleepUntil(Epoch + int64(oldTs) + 1)
				continue
			}
			newTs, newSeq = oldTs, oldSeq+1
		}

		if w.state.CompareAndSwap(old, w.pack(newTs, newSeq)) {
			return newTs, newSeq, nil
		}
		// Lost the race. Retry, re-reading both values in the same order.
	}
}

func (w *watermark) pack(ts, seq uint64) uint64 { return ts<<w.bits | seq }

func (w *watermark) unpack(state uint64) (ts, seq uint64) {
	return state >> w.bits, state & w.max
}
