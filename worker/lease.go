package worker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rushikeshg25/tick"
)

const maxNodeID = tick.MaxNodeID

// A Store is the shared state a Lease allocator coordinates through: etcd,
// Consul, a database table, anything that can hold a keyed claim with an
// expiry.
//
// Implementations must be linearizable with respect to a single node ID. Two
// concurrent TryAcquire calls for the same ID must not both report success.
//
// The interface is deliberately this small so that tick itself stays free of
// dependencies. An etcd implementation is roughly forty lines over a lease
// and a transaction; see docs/WORKER-IDS.md.
type Store interface {
	// TryAcquire claims nodeID for holder until ttl from now. It reports
	// false without error when the ID is already held by someone else.
	TryAcquire(ctx context.Context, nodeID uint16, holder string, ttl time.Duration) (bool, error)

	// Renew extends an existing claim. It reports false when holder no longer
	// owns the ID, which is the signal that the claim was lost.
	Renew(ctx context.Context, nodeID uint16, holder string, ttl time.Duration) (bool, error)

	// Release drops the claim. Releasing an ID held by someone else must be a
	// no-op rather than an error.
	Release(ctx context.Context, nodeID uint16, holder string) error
}

// Config describes how a Lease allocator behaves.
type Config struct {
	// Store is the shared state. Required.
	Store Store

	// Holder identifies this process. It must be unique across the fleet;
	// a pod UID or a random string per process start both work. Required.
	Holder string

	// TTL is how long a claim survives without renewal. Required.
	TTL time.Duration

	// RenewInterval is how often to renew. It should divide TTL several times
	// over so a claim survives transient failures. Defaults to TTL/4.
	RenewInterval time.Duration

	// MaxClockSkew bounds the disagreement between this process's clock and
	// the store's. Defaults to DefaultMaxClockSkew.
	MaxClockSkew time.Duration

	// MaxRoundTrip bounds how long a renewal can take to reach the store.
	// Defaults to DefaultMaxRoundTrip.
	MaxRoundTrip time.Duration

	// SafetyMargin is slack on top of the two bounds above, covering
	// scheduler delay and garbage collection pauses. Defaults to
	// DefaultSafetyMargin.
	SafetyMargin time.Duration

	// Range limits which node IDs are scanned, as [0, Range). Zero means the
	// whole space.
	Range uint16

	// Clock is the time source. Defaults to a tick.SystemClock. The safety
	// deadline is measured with its monotonic reading, never its wall clock,
	// because a lease must survive the wall clock being stepped.
	Clock tick.Clock

	// RenewSignal replaces the internal ticker when non-nil. Tests and the
	// simulator use it to drive renewals deterministically.
	RenewSignal <-chan time.Time
}

// Defaults for the three terms that make up the safety deadline.
const (
	DefaultMaxClockSkew = 500 * time.Millisecond
	DefaultMaxRoundTrip = 2 * time.Second
	DefaultSafetyMargin = time.Second
)

func (c *Config) withDefaults() error {
	if c.Store == nil {
		return errors.New("worker: Config.Store is required")
	}
	if c.Holder == "" {
		return errors.New("worker: Config.Holder is required and must be unique per process")
	}
	if c.TTL <= 0 {
		return fmt.Errorf("worker: Config.TTL must be positive, got %v", c.TTL)
	}
	if c.RenewInterval == 0 {
		c.RenewInterval = c.TTL / 4
	}
	if c.MaxClockSkew == 0 {
		c.MaxClockSkew = DefaultMaxClockSkew
	}
	if c.MaxRoundTrip == 0 {
		c.MaxRoundTrip = DefaultMaxRoundTrip
	}
	if c.SafetyMargin == 0 {
		c.SafetyMargin = DefaultSafetyMargin
	}
	if c.Clock == nil {
		c.Clock = tick.NewSystemClock()
	}
	if c.Range == 0 {
		c.Range = maxNodeID + 1
	}

	if budget := c.usableTTL(); budget <= 0 {
		return fmt.Errorf(
			"worker: TTL %v leaves no usable time after clock skew %v, round trip %v and margin %v; "+
				"raise the TTL or lower the bounds",
			c.TTL, c.MaxClockSkew, c.MaxRoundTrip, c.SafetyMargin)
	}
	if c.RenewInterval >= c.usableTTL() {
		return fmt.Errorf(
			"worker: renew interval %v is not shorter than the usable lease window %v; "+
				"the claim would expire before its first renewal",
			c.RenewInterval, c.usableTTL())
	}
	return nil
}

// usableTTL is how long a claim may actually be relied on.
//
// A holder must stop generating before its claim expires, not when it notices
// the expiry. Three things can sit between the two: the store's clock running
// ahead of ours, a renewal still in flight, and this process being descheduled
// at the wrong moment. Subtracting all three leaves the window during which
// the ID is provably ours.
func (c *Config) usableTTL() time.Duration {
	return c.TTL - c.MaxClockSkew - c.MaxRoundTrip - c.SafetyMargin
}

// New returns an Allocator that claims a node ID from a shared store and
// keeps it alive for as long as it safely can.
func New(cfg Config) (Allocator, error) {
	if err := cfg.withDefaults(); err != nil {
		return nil, err
	}
	return &leaseAllocator{cfg: cfg}, nil
}

type leaseAllocator struct {
	cfg Config
}

func (a *leaseAllocator) Acquire(ctx context.Context) (Lease, error) {
	for id := uint16(0); id < a.cfg.Range; id++ {
		ok, err := a.cfg.Store.TryAcquire(ctx, id, a.cfg.Holder, a.cfg.TTL)
		if err != nil {
			return nil, fmt.Errorf("worker: acquiring node id %d: %w", id, err)
		}
		if !ok {
			continue
		}

		safeCtx, cancel := context.WithCancel(context.Background())
		l := &lease{
			cfg:      a.cfg,
			nodeID:   id,
			safe:     safeCtx,
			cancel:   cancel,
			deadline: a.cfg.Clock.Since() + a.cfg.usableTTL(),
			done:     make(chan struct{}),
		}
		go l.renew()
		return l, nil
	}
	return nil, ErrNoFreeNodeID
}

type lease struct {
	cfg    Config
	nodeID uint16
	safe   context.Context
	cancel context.CancelFunc
	done   chan struct{}

	mu sync.Mutex
	// deadline is a monotonic reading, not a wall-clock instant. Lease expiry
	// must not be affected by the wall clock being stepped.
	deadline time.Duration
	released bool
}

func (l *lease) NodeID() uint16        { return l.nodeID }
func (l *lease) Safe() context.Context { return l.safe }

func (l *lease) Release(ctx context.Context) error {
	l.mu.Lock()
	if l.released {
		l.mu.Unlock()
		return nil
	}
	l.released = true
	l.mu.Unlock()

	l.cancel()
	close(l.done)
	return l.cfg.Store.Release(ctx, l.nodeID, l.cfg.Holder)
}

// expired reports whether the safety deadline has passed, using monotonic
// time only.
func (l *lease) expired() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cfg.Clock.Since() >= l.deadline
}

func (l *lease) extend() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.deadline = l.cfg.Clock.Since() + l.cfg.usableTTL()
}

func (l *lease) renew() {
	signal := l.cfg.RenewSignal
	if signal == nil {
		t := time.NewTicker(l.cfg.RenewInterval)
		defer t.Stop()
		signal = t.C
	}

	for {
		select {
		case <-l.done:
			return
		case <-signal:
		}

		// Check the deadline before attempting a renewal, not only after. If
		// this goroutine was descheduled past the deadline, the claim is
		// already unsafe however the renewal turns out.
		if l.expired() {
			l.cancel()
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), l.cfg.MaxRoundTrip)
		ok, err := l.cfg.Store.Renew(ctx, l.nodeID, l.cfg.Holder, l.cfg.TTL)
		cancel()

		switch {
		case err != nil:
			// The store is unreachable. The claim is not lost yet, so keep
			// trying until the deadline runs out; that is what the deadline
			// is for.
			if l.expired() {
				l.cancel()
				return
			}
		case !ok:
			// Someone else owns the ID. Stop immediately.
			l.cancel()
			return
		default:
			l.extend()
		}
	}
}
