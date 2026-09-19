package tick

import (
	"sync/atomic"
	"time"
)

// DefaultRegressionTolerance is how far the wall clock may move backward
// before Generator refuses to issue IDs. Within this window the generator
// holds its timestamp watermark and borrows sequence space instead; beyond
// it, the only safe answer is ErrClockRegression.
const DefaultRegressionTolerance = 10 * time.Millisecond

// Generator issues Snowflake-64 IDs for a single node.
//
// All generator state lives in one atomic word. The timestamp and the
// sequence must advance together or not at all, and packing them into a
// single uint64 makes that structural: one CompareAndSwap publishes both, so
// there is no interleaving in which another goroutine observes a new
// timestamp beside a stale sequence. That race is what breaks lock-free
// generators that keep the two fields apart.
//
// A Generator is safe for concurrent use by multiple goroutines.
type Generator struct {
	// state packs the generator's position as timestamp<<SequenceBits | sequence,
	// where timestamp is milliseconds since Epoch.
	state atomic.Uint64

	// node is the node ID, pre-shifted into its final bit position so that
	// the hot path is a single OR.
	node uint64

	clock Clock
	tol   time.Duration
}

type config struct {
	clock Clock
	tol   time.Duration
}

// Option configures a Generator.
type Option func(*config)

// WithClock sets the time source. The default is a SystemClock. Tests and the
// simulator pass a FakeClock here.
func WithClock(c Clock) Option {
	return func(cfg *config) { cfg.clock = c }
}

// WithRegressionTolerance sets how far the wall clock may move backward
// before generation fails. Larger values survive bigger NTP corrections at
// the cost of clustering more IDs into a single millisecond.
func WithRegressionTolerance(d time.Duration) Option {
	return func(cfg *config) { cfg.tol = d }
}

// New returns a Generator for the given node ID.
//
// The caller is responsible for the node ID being exclusively held. See the
// worker package for allocators that make that guarantee.
func New(nodeID uint16, opts ...Option) (*Generator, error) {
	// TODO(M1): validate nodeID against MaxNodeID, apply options over the
	// defaults, pre-shift the node ID.
	panic("tick: New not implemented")
}

// Next returns the next ID, waiting for the next millisecond if this one's
// sequence space is exhausted. It never waits longer than that.
func (g *Generator) Next() (ID, error) {
	// TODO(M1): the CAS loop. Pseudocode is in PLAN.md, "The packed-state
	// trick". Three cases against the loaded watermark:
	//
	//   now > oldTs   new millisecond, reset the sequence to 0
	//   now == oldTs  same millisecond, take the next sequence; on exhaustion
	//                 wait for the clock to tick and retry
	//   now < oldTs   the clock moved backward. Within tol, hold oldTs and
	//                 borrow sequence space: uniqueness survives because no
	//                 (ts, seq) pair is ever reused. Beyond tol, or with the
	//                 borrowed space also gone, return ErrClockRegression.
	//
	// Never rewind the watermark.
	panic("tick: Generator.Next not implemented")
}

// TryNext returns the next ID without waiting, reporting
// ErrSequenceExhausted rather than blocking for the next millisecond.
func (g *Generator) TryNext() (ID, error) {
	// TODO(M1)
	panic("tick: Generator.TryNext not implemented")
}

// pack folds a timestamp and sequence into the single word held in
// Generator.state.
func pack(ts, seq uint64) uint64 {
	// TODO(M1)
	panic("tick: pack not implemented")
}

// unpack splits the word held in Generator.state back into its two fields.
func unpack(state uint64) (ts, seq uint64) {
	// TODO(M1)
	panic("tick: unpack not implemented")
}
