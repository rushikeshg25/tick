// Package sim runs tick's generators against adversarial clocks in a
// deterministic virtual world, and checks the library's invariants over
// everything that was emitted.
//
// The world has no real time, no goroutines and no network. Every decision
// comes from a seeded PRNG, so a failing run is reproduced exactly by its
// seed. That is the point: uniqueness bugs in ID generation are rare events
// under unusual clock behaviour, and a test that cannot replay the schedule
// that produced one is of little use.
//
// What this package covers and what it does not:
//
//   - It covers the generator under clock steps, drift, offsets between
//     hosts, pauses, and node-ID handover between a process and its
//     successor.
//   - It does not cover concurrency. Processes are stepped one at a time in a
//     seeded order, because real goroutines would make runs irreproducible.
//     The race detector and the contended property tests in package tick
//     cover that instead.
//   - It does not drive package worker's renewal goroutines. Handover here
//     obeys the same safety window by construction; worker's own tests prove
//     the window is enforced.
//
// Generation uses TryNext, so nothing ever blocks: an exhausted sequence is
// simply a step in which that process emitted less.
package sim

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/rushikeshg25/tick"
)

// Config describes a virtual world.
type Config struct {
	// Seed makes the run reproducible.
	Seed uint64

	// NodeIDs is the size of the node ID space. Each one is held by at most
	// one process at a time.
	NodeIDs uint16

	// Steps is how many ticks of virtual time to run.
	Steps int

	// StepDuration is how much true time passes per step.
	StepDuration time.Duration

	// MaxOffset bounds how far any process's clock may sit from true time.
	// Every clock disturbance is clamped to keep the process inside this
	// envelope, so the bound is a real guarantee about the world rather than
	// a description of its initial state.
	MaxOffset time.Duration

	// MaxDriftPPM bounds each process's clock drift, in parts per million.
	MaxDriftPPM int64

	// Tolerance is the generators' clock regression tolerance.
	Tolerance time.Duration

	// FaultRate is the chance per process per step of injecting a fault.
	FaultRate float64

	// MaxPerStep bounds how many IDs a process attempts per step.
	MaxPerStep int
}

// Default returns a configuration that exercises every fault type.
func Default(seed uint64) Config {
	return Config{
		Seed:         seed,
		NodeIDs:      8,
		Steps:        6000,
		StepDuration: time.Millisecond,
		MaxOffset:    250 * time.Millisecond,
		MaxDriftPPM:  200,
		Tolerance:    10 * time.Millisecond,
		FaultRate:    0.02,
		MaxPerStep:   6,
	}
}

func (c *Config) withDefaults() error {
	if c.NodeIDs == 0 {
		c.NodeIDs = 8
	}
	if c.Steps <= 0 {
		c.Steps = 1000
	}
	if c.StepDuration <= 0 {
		c.StepDuration = time.Millisecond
	}
	if c.MaxPerStep <= 0 {
		c.MaxPerStep = 4
	}
	if c.Tolerance < 0 {
		return errors.New("sim: Tolerance must not be negative")
	}
	if c.NodeIDs > tick.MaxNodeID+1 {
		return fmt.Errorf("sim: NodeIDs %d exceeds the node id space", c.NodeIDs)
	}
	return nil
}

// skewBound is the widest gap that can open between two processes' notions of
// now.
//
// Every clock stays within MaxOffset of true time, so at a true instant t a
// process reads somewhere in [t-MaxOffset, t+MaxOffset]. An emitted timestamp
// is never below the reading at the time it was emitted, and never above the
// highest reading that clock has ever produced, because the watermark only
// holds values the clock actually reported. Two emissions more than twice
// MaxOffset apart in true time are therefore ordered.
//
// The extra step duration is the granularity of the world, and the tolerance
// is slack.
func (c *Config) skewBound() time.Duration {
	return 2*c.MaxOffset + c.Tolerance + c.StepDuration
}

// Emission is one ID, recorded against true time rather than any process's
// idea of it.
type Emission struct {
	TrueMs  int64
	Process int
	NodeID  uint16
	ID      tick.ID
}

// Report is everything a run produced.
type Report struct {
	Config    Config
	Emissions []Emission

	// Windows records, per process, the true-time interval over which it held
	// and used its node ID. Overlapping windows for one node ID are an I3
	// violation.
	Windows []Window

	// Faults counts each kind of fault that was injected.
	Faults map[string]int

	// Errors counts each kind of error the generators returned. These are
	// expected outcomes, not failures: refusing to generate is the library
	// working.
	Errors map[string]int
}

// Window is one process's tenure on a node ID.
type Window struct {
	Process   int
	NodeID    uint16
	FirstMs   int64
	LastMs    int64
	Generated int
}

type process struct {
	index  int
	nodeID uint16
	clock  *tick.FakeClock
	gen    *tick.Generator

	// deviation is how far this process's wall clock currently sits from true
	// time. Every disturbance goes through stepClock, which clamps it to the
	// configured envelope.
	deviation time.Duration
	driftPPM  int64

	pausedUntil int
	alive       bool

	firstMs   int64
	lastMs    int64
	generated int
}

// Run executes a world and returns everything it emitted. It does not check
// invariants; pass the report to Check.
func Run(cfg Config) (*Report, error) {
	if err := cfg.withDefaults(); err != nil {
		return nil, err
	}

	rng := rand.New(rand.NewPCG(cfg.Seed, cfg.Seed^0x9e3779b97f4a7c15))
	origin := time.UnixMilli(tick.Epoch).Add(365 * 24 * time.Hour)

	rep := &Report{
		Config: cfg,
		Faults: map[string]int{},
		Errors: map[string]int{},
	}

	// handoverGap is how long a node ID stays idle between holders. It must
	// exceed the widest disagreement between the two processes' clocks, or a
	// successor could legitimately emit a timestamp its predecessor already
	// used. This is the simulated equivalent of worker's safety window.
	handoverGap := cfg.skewBound() + cfg.Tolerance + cfg.StepDuration

	holders := make([]*process, cfg.NodeIDs)
	freeAtMs := make([]int64, cfg.NodeIDs)
	nextIndex := 0

	trueMs := origin.UnixMilli()

	for step := 0; step < cfg.Steps; step++ {
		trueMs += cfg.StepDuration.Milliseconds()

		// Fill empty node IDs whose cooldown has passed.
		for n := uint16(0); n < cfg.NodeIDs; n++ {
			if holders[n] != nil || trueMs < freeAtMs[n] {
				continue
			}
			p, err := newProcess(nextIndex, n, origin, trueMs, cfg, rng)
			if err != nil {
				return nil, err
			}
			nextIndex++
			holders[n] = p
		}

		// Step the processes in a seeded order so no process is
		// systematically first.
		order := rng.Perm(int(cfg.NodeIDs))
		for _, oi := range order {
			p := holders[uint16(oi)]
			if p == nil {
				continue
			}

			// True time passes for everyone, including paused processes:
			// monotonic time does not stop because a process was descheduled.
			p.clock.Advance(cfg.StepDuration)
			// Drift is clock error, so it moves the wall reading only.
			p.stepClock(driftFor(cfg.StepDuration, p.driftPPM), cfg.MaxOffset)

			if rng.Float64() < cfg.FaultRate {
				applyFault(p, step, cfg, rng, rep)
			}

			if step < p.pausedUntil || !p.alive {
				if !p.alive {
					rep.Windows = append(rep.Windows, p.window())
					holders[uint16(oi)] = nil
					freeAtMs[oi] = trueMs + handoverGap.Milliseconds()
				}
				continue
			}

			n := rng.IntN(cfg.MaxPerStep + 1)
			for i := 0; i < n; i++ {
				id, err := p.gen.TryNext()
				if err != nil {
					rep.Errors[classify(err)]++
					break
				}
				if p.generated == 0 {
					p.firstMs = trueMs
				}
				p.lastMs = trueMs
				p.generated++
				rep.Emissions = append(rep.Emissions, Emission{
					TrueMs: trueMs, Process: p.index, NodeID: p.nodeID, ID: id,
				})
			}
		}
	}

	for _, p := range holders {
		if p != nil {
			rep.Windows = append(rep.Windows, p.window())
		}
	}
	return rep, nil
}

func (p *process) window() Window {
	return Window{
		Process: p.index, NodeID: p.nodeID,
		FirstMs: p.firstMs, LastMs: p.lastMs, Generated: p.generated,
	}
}

func newProcess(index int, nodeID uint16, origin time.Time, trueMs int64, cfg Config, rng *rand.Rand) (*process, error) {
	offset := time.Duration(rng.Int64N(int64(2*cfg.MaxOffset)+1)) - cfg.MaxOffset
	drift := rng.Int64N(2*cfg.MaxDriftPPM+1) - cfg.MaxDriftPPM

	clock := tick.NewFakeClock(time.UnixMilli(trueMs).Add(offset))
	// Nothing in the simulator blocks, so auto-advance would only mask an
	// exhausted sequence by silently moving this process's clock.
	clock.SetAutoAdvance(false)

	gen, err := tick.New(nodeID,
		tick.WithClock(clock),
		tick.WithRegressionTolerance(cfg.Tolerance))
	if err != nil {
		return nil, fmt.Errorf("sim: creating process %d on node %d: %w", index, nodeID, err)
	}

	return &process{
		index: index, nodeID: nodeID, clock: clock, gen: gen,
		deviation: offset, driftPPM: drift, alive: true,
	}, nil
}

// stepClock moves the wall reading by d, clamped so the process never leaves
// the configured offset envelope.
//
// The clamp is what makes the world self-consistent. Without it, a run of
// forward corrections would let one process's clock wander arbitrarily far
// ahead, and a successor on the same node ID could then legitimately reuse
// timestamps its predecessor had already issued. That is a real hazard, but
// it is one bounded clock skew is assumed to rule out, and a world that
// violates its own stated bound tests nothing.
func (p *process) stepClock(d time.Duration, maxOffset time.Duration) {
	target := min(max(p.deviation+d, -maxOffset), maxOffset)
	if delta := target - p.deviation; delta != 0 {
		p.clock.Step(delta)
		p.deviation = target
	}
}

func driftFor(step time.Duration, ppm int64) time.Duration {
	return time.Duration(int64(step) * ppm / 1_000_000)
}

func classify(err error) string {
	switch {
	case errors.Is(err, tick.ErrClockRegression):
		return "clock-regression"
	case errors.Is(err, tick.ErrSequenceExhausted):
		return "sequence-exhausted"
	case errors.Is(err, tick.ErrTimestampOutOfRange):
		return "timestamp-out-of-range"
	case errors.Is(err, tick.ErrLeaseLost):
		return "lease-lost"
	default:
		return "other:" + err.Error()
	}
}
