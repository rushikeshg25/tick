package tick

import (
	"context"
	"errors"
	"fmt"
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
// The caller is responsible for the node ID being held exclusively. The
// worker package provides allocators that make that guarantee.
//
// A Generator is safe for concurrent use by multiple goroutines.
type Generator struct {
	wm *watermark

	// node is the node ID pre-shifted into its final bit position, so the hot
	// path is a single OR.
	node uint64

	// lost is set once the node ID's lease is gone. It is a plain atomic
	// rather than a context check because the hot path cannot afford the
	// mutex inside context.Context.Err.
	lost atomic.Bool
}

type config struct {
	clock Clock
	tol   time.Duration
	safe  context.Context
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

// WithLease binds a Generator to the safety context of a worker-ID lease, as
// returned by worker.Lease.Safe. Once that context is cancelled the node ID
// may belong to someone else, so every subsequent call returns ErrLeaseLost
// and the generator never issues another ID.
//
// The option applies only to Generator. The UUID and ULID formats carry no
// node ID, and their constructors reject it rather than ignore it.
func WithLease(safe context.Context) Option {
	return func(cfg *config) { cfg.safe = safe }
}

func newConfig(opts []Option) (config, error) {
	cfg := config{clock: NewSystemClock(), tol: DefaultRegressionTolerance}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.clock == nil {
		return cfg, errors.New("tick: WithClock given a nil Clock")
	}
	if cfg.tol < 0 {
		return cfg, fmt.Errorf("tick: regression tolerance must not be negative, got %v", cfg.tol)
	}
	return cfg, nil
}

// New returns a Generator for the given node ID.
//
// It fails immediately if the host clock is outside the representable window,
// so a machine with an unset clock is rejected at startup rather than at its
// first request.
func New(nodeID uint16, opts ...Option) (*Generator, error) {
	if nodeID > MaxNodeID {
		return nil, fmt.Errorf("%w: %d exceeds %d", ErrNodeIDOutOfRange, nodeID, MaxNodeID)
	}
	cfg, err := newConfig(opts)
	if err != nil {
		return nil, err
	}

	wm := newWatermark(cfg.clock, cfg.tol, SequenceBits)
	if _, err := wm.timestamp(); err != nil {
		return nil, err
	}

	g := &Generator{wm: wm, node: uint64(nodeID) << nodeShift}

	if cfg.safe != nil {
		if cfg.safe.Err() != nil {
			return nil, fmt.Errorf("%w: lease was already over at construction", ErrLeaseLost)
		}
		// Watches for the rest of the lease's life. The goroutine ends when
		// the lease does, which for a generator is process lifetime.
		go func() {
			<-cfg.safe.Done()
			g.lost.Store(true)
		}()
	}

	return g, nil
}

// NodeID returns the node this Generator issues IDs for.
func (g *Generator) NodeID() uint16 { return uint16(g.node >> nodeShift) }

// Next returns the next ID, waiting for the next millisecond if this one's
// sequence space is exhausted. It never waits longer than the regression
// tolerance.
func (g *Generator) Next() (ID, error) {
	if g.lost.Load() {
		return 0, ErrLeaseLost
	}
	ts, seq, err := g.wm.advance(true)
	if err != nil {
		return 0, err
	}
	return compose(ts, g.node, seq), nil
}

// TryNext returns the next ID without waiting, reporting
// ErrSequenceExhausted rather than blocking for the next millisecond.
func (g *Generator) TryNext() (ID, error) {
	if g.lost.Load() {
		return 0, ErrLeaseLost
	}
	ts, seq, err := g.wm.advance(false)
	if err != nil {
		return 0, err
	}
	return compose(ts, g.node, seq), nil
}
