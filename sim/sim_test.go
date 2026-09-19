package sim

import (
	"strconv"
	"testing"
	"time"

	"github.com/rushikeshg25/tick"
)

// seedCorpus is pinned so CI runs the same worlds every time. Add the seed of
// any failure found by TestRandomSeeds here, with a comment saying what it
// caught.
var seedCorpus = []uint64{1, 2, 3, 7, 11, 42, 1337, 99991}

func TestSeedCorpus(t *testing.T) {
	for _, seed := range seedCorpus {
		t.Run(name(seed), func(t *testing.T) {
			rep, err := Run(Default(seed))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			requireNoViolations(t, rep)

			if len(rep.Emissions) == 0 {
				t.Fatal("run emitted nothing; the world is not exercising the generator")
			}
			t.Logf("emitted %d IDs across %d windows; faults %v; errors %v",
				len(rep.Emissions), len(rep.Windows), rep.Faults, rep.Errors)
		})
	}
}

// A run is only useful if it actually reached the interesting states. This
// asserts the world is adversarial enough to be worth running.
func TestWorldExercisesEveryFaultAndError(t *testing.T) {
	cfg := Default(42)
	cfg.Steps = 20000
	rep, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	requireNoViolations(t, rep)

	for _, fault := range []string{
		"step-back-small", "step-back-large", "step-forward",
		"drift-change", "pause", "crash",
	} {
		if rep.Faults[fault] == 0 {
			t.Errorf("fault %q was never injected", fault)
		}
	}

	// Refusing to generate through a large backward step is the library
	// working, so the run should contain plenty of these.
	if rep.Errors["clock-regression"] == 0 {
		t.Error("no clock regression was ever refused; the large backward steps are not biting")
	}
	if len(rep.Windows) <= int(cfg.NodeIDs) {
		t.Errorf("only %d windows for %d node IDs; no handover happened", len(rep.Windows), cfg.NodeIDs)
	}
}

func TestRunIsDeterministic(t *testing.T) {
	a, err := Run(Default(2024))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	b, err := Run(Default(2024))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(a.Emissions) != len(b.Emissions) {
		t.Fatalf("same seed produced %d and %d emissions", len(a.Emissions), len(b.Emissions))
	}
	for i := range a.Emissions {
		if a.Emissions[i] != b.Emissions[i] {
			t.Fatalf("same seed diverged at emission %d: %+v vs %+v", i, a.Emissions[i], b.Emissions[i])
		}
	}
}

func TestDifferentSeedsProduceDifferentWorlds(t *testing.T) {
	a, _ := Run(Default(1))
	b, _ := Run(Default(2))
	if len(a.Emissions) == len(b.Emissions) && a.Faults["crash"] == b.Faults["crash"] {
		t.Error("two seeds produced suspiciously identical worlds")
	}
}

// Random exploration. A failure here is reproducible from the logged seed;
// pin it into seedCorpus.
func TestRandomSeeds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping random exploration in short mode")
	}
	for seed := uint64(1000); seed < 1060; seed++ {
		rep, err := Run(Default(seed))
		if err != nil {
			t.Fatalf("seed %d: Run: %v", seed, err)
		}
		if v := Check(rep); len(v) > 0 {
			t.Fatalf("seed %d violated invariants (pin it into seedCorpus):\n%s", seed, format(v))
		}
	}
}

func TestZeroToleranceStillHoldsInvariants(t *testing.T) {
	cfg := Default(7)
	cfg.Tolerance = 0
	rep, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	requireNoViolations(t, rep)
}

func TestSingleNodeManyHandovers(t *testing.T) {
	cfg := Default(5)
	cfg.NodeIDs = 1
	cfg.FaultRate = 0.05
	cfg.Steps = 8000
	rep, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	requireNoViolations(t, rep)
	if len(rep.Windows) < 5 {
		t.Errorf("only %d tenures on the single node ID; want the handover path exercised", len(rep.Windows))
	}
}

// The checker is only worth anything if it catches a real violation, so feed
// it broken logs and confirm each invariant fires.
func TestCheckerCatchesInjectedViolations(t *testing.T) {
	base := func() *Report {
		return &Report{
			Config: Config{
				Steps: 10, StepDuration: time.Millisecond,
				MaxOffset: 0, MaxDriftPPM: 0, Tolerance: 0,
			},
			Faults: map[string]int{},
			Errors: map[string]int{},
		}
	}

	t.Run("I1 uniqueness", func(t *testing.T) {
		rep := base()
		dup := tick.ID(1 << 40)
		rep.Emissions = []Emission{
			{TrueMs: 1, Process: 0, NodeID: 0, ID: dup},
			{TrueMs: 2, Process: 1, NodeID: 1, ID: dup},
		}
		requireViolation(t, rep, "I1 uniqueness")
	})

	t.Run("I2 monotonicity", func(t *testing.T) {
		rep := base()
		rep.Emissions = []Emission{
			{TrueMs: 1, Process: 0, ID: tick.ID(1 << 41)},
			{TrueMs: 2, Process: 0, ID: tick.ID(1 << 40)},
		}
		requireViolation(t, rep, "I2 monotonicity")
	})

	t.Run("I3 lease safety", func(t *testing.T) {
		rep := base()
		rep.Windows = []Window{
			{Process: 0, NodeID: 3, FirstMs: 0, LastMs: 100, Generated: 5},
			{Process: 1, NodeID: 3, FirstMs: 50, LastMs: 200, Generated: 5},
		}
		requireViolation(t, rep, "I3 lease safety")
	})

	t.Run("I4 k-sortability", func(t *testing.T) {
		rep := base()
		rep.Emissions = []Emission{
			{TrueMs: 0, Process: 0, ID: tick.ID(1 << 45)},
			{TrueMs: 5000, Process: 1, ID: tick.ID(1 << 40)},
		}
		requireViolation(t, rep, "I4 k-sortability")
	})
}

func requireNoViolations(t *testing.T, rep *Report) {
	t.Helper()
	if v := Check(rep); len(v) > 0 {
		t.Fatalf("invariants violated:\n%s", format(v))
	}
}

func requireViolation(t *testing.T, rep *Report, invariant string) {
	t.Helper()
	found := Check(rep)
	for _, v := range found {
		if v.Invariant == invariant {
			return
		}
	}
	t.Fatalf("checker missed an injected %s violation; it reported %v", invariant, found)
}

func format(vs []Violation) string {
	s := ""
	for _, v := range vs {
		s += "  " + v.String() + "\n"
	}
	return s
}

func name(seed uint64) string {
	return "seed-" + strconv.FormatUint(seed, 10)
}
