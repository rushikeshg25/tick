package tick

import (
	"errors"
	"fmt"
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

	return &Generator{wm: wm, node: uint64(nodeID) << nodeShift}, nil
}

// NodeID returns the node this Generator issues IDs for.
func (g *Generator) NodeID() uint16 { return uint16(g.node >> nodeShift) }

// Next returns the next ID, waiting for the next millisecond if this one's
// sequence space is exhausted. It never waits longer than the regression
// tolerance.
func (g *Generator) Next() (ID, error) {
	ts, seq, err := g.wm.advance(true)
	if err != nil {
		return 0, err
	}
	return compose(ts, g.node, seq), nil
}

// TryNext returns the next ID without waiting, reporting
// ErrSequenceExhausted rather than blocking for the next millisecond.
func (g *Generator) TryNext() (ID, error) {
	ts, seq, err := g.wm.advance(false)
	if err != nil {
		return 0, err
	}
	return compose(ts, g.node, seq), nil
}
