package sim

import (
	"math/rand/v2"
	"time"
)

// applyFault injects one disturbance into a process. Each kind reproduces
// something that really happens to servers, and each is chosen by the seeded
// PRNG so a run replays exactly.
func applyFault(p *process, step int, cfg Config, rng *rand.Rand, rep *Report) {
	switch rng.IntN(6) {
	case 0:
		// A small NTP correction backward, inside the tolerance. The
		// generator should absorb this by borrowing sequence space.
		jump := time.Duration(rng.Int64N(int64(cfg.Tolerance) + 1))
		p.stepClock(-jump, cfg.MaxOffset)
		rep.Faults["step-back-small"]++

	case 1:
		// A large backward step: a VM restored from a snapshot, or an
		// operator setting the clock. The generator should refuse rather than
		// emit.
		jump := cfg.Tolerance*2 + time.Duration(rng.Int64N(int64(2*cfg.MaxOffset)+1))
		p.stepClock(-jump, cfg.MaxOffset)
		rep.Faults["step-back-large"]++

	case 2:
		// A forward correction. Harmless for uniqueness, but it widens the
		// gap between this process and its peers.
		jump := time.Duration(rng.Int64N(int64(2*cfg.MaxOffset) + 1))
		p.stepClock(jump, cfg.MaxOffset)
		rep.Faults["step-forward"]++

	case 3:
		// The drift rate changes, as it does when a machine warms up or NTP
		// starts slewing.
		p.driftPPM = rng.Int64N(2*cfg.MaxDriftPPM+1) - cfg.MaxDriftPPM
		rep.Faults["drift-change"]++

	case 4:
		// The process stops running: a garbage collection pause, a
		// descheduled container, a frozen VM. Its monotonic clock keeps
		// moving, which is the part that catches naive lease logic.
		p.pausedUntil = step + 1 + rng.IntN(40)
		rep.Faults["pause"]++

	case 5:
		// The process dies. Its node ID stays idle for the handover gap
		// before a successor takes it, which is the invariant I3 exists to
		// check.
		p.alive = false
		rep.Faults["crash"]++
	}
}
