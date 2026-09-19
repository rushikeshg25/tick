package sim

import (
	"fmt"
	"slices"

	"github.com/rushikeshg25/tick"
)

// A Violation is one broken invariant, named by its identifier in PLAN.md.
type Violation struct {
	Invariant string
	Detail    string
}

func (v Violation) String() string { return v.Invariant + ": " + v.Detail }

// Check verifies every invariant over a completed run and returns each
// violation it finds.
//
// The checks run against the emission log rather than against the generators,
// so they hold whatever the generators did internally.
func Check(rep *Report) []Violation {
	var out []Violation
	out = append(out, checkUniqueness(rep)...)
	out = append(out, checkPerProcessMonotonicity(rep)...)
	out = append(out, checkNodeIDExclusivity(rep)...)
	out = append(out, checkSortability(rep)...)
	return out
}

// I1: no two calls ever return the same ID.
func checkUniqueness(rep *Report) []Violation {
	type keyed struct {
		id tick.ID
		at int
	}
	sorted := make([]keyed, len(rep.Emissions))
	for i, e := range rep.Emissions {
		sorted[i] = keyed{e.ID, i}
	}
	slices.SortFunc(sorted, func(a, b keyed) int {
		if a.id != b.id {
			return int(a.id - b.id)
		}
		return a.at - b.at
	})

	var out []Violation
	for i := 1; i < len(sorted); i++ {
		if sorted[i].id != sorted[i-1].id {
			continue
		}
		a, b := rep.Emissions[sorted[i-1].at], rep.Emissions[sorted[i].at]
		out = append(out, Violation{
			Invariant: "I1 uniqueness",
			Detail: fmt.Sprintf("ID %d emitted twice: process %d on node %d at %dms, and process %d on node %d at %dms",
				a.ID, a.Process, a.NodeID, a.TrueMs, b.Process, b.NodeID, b.TrueMs),
		})
		if len(out) >= 10 {
			break
		}
	}
	return out
}

// I2: IDs from a single generator strictly increase.
func checkPerProcessMonotonicity(rep *Report) []Violation {
	last := map[int]tick.ID{}
	var out []Violation
	for _, e := range rep.Emissions {
		prev, seen := last[e.Process]
		if seen && e.ID <= prev {
			out = append(out, Violation{
				Invariant: "I2 monotonicity",
				Detail: fmt.Sprintf("process %d emitted %d after %d at %dms",
					e.Process, e.ID, prev, e.TrueMs),
			})
			if len(out) >= 10 {
				return out
			}
		}
		last[e.Process] = e.ID
	}
	return out
}

// I3: for one node ID, the emission windows of two processes never overlap.
func checkNodeIDExclusivity(rep *Report) []Violation {
	byNode := map[uint16][]Window{}
	for _, w := range rep.Windows {
		if w.Generated == 0 {
			continue // never used the ID, so it cannot have collided on it
		}
		byNode[w.NodeID] = append(byNode[w.NodeID], w)
	}

	var out []Violation
	for node, ws := range byNode {
		slices.SortFunc(ws, func(a, b Window) int { return int(a.FirstMs - b.FirstMs) })
		for i := 1; i < len(ws); i++ {
			if ws[i].FirstMs <= ws[i-1].LastMs {
				out = append(out, Violation{
					Invariant: "I3 lease safety",
					Detail: fmt.Sprintf("node %d used by process %d through %dms and by process %d from %dms",
						node, ws[i-1].Process, ws[i-1].LastMs, ws[i].Process, ws[i].FirstMs),
				})
			}
		}
	}
	return out
}

// I4: if a was generated at least skewBound before b in true time, a < b.
//
// Anything closer together than that is unordered by design, because the
// processes' clocks genuinely disagree. Stating the bound is the honest
// version of "time-sortable".
func checkSortability(rep *Report) []Violation {
	bound := rep.Config.skewBound().Milliseconds()

	ordered := slices.Clone(rep.Emissions)
	slices.SortFunc(ordered, func(a, b Emission) int { return int(a.TrueMs - b.TrueMs) })

	var out []Violation
	// A sliding lower bound: the largest ID seen at or before trueMs - bound
	// must be smaller than everything from here on.
	j := 0
	var maxEarlier tick.ID
	var maxEarlierAt Emission
	for i := 0; i < len(ordered); i++ {
		for j < len(ordered) && ordered[j].TrueMs <= ordered[i].TrueMs-bound {
			if ordered[j].ID > maxEarlier {
				maxEarlier, maxEarlierAt = ordered[j].ID, ordered[j]
			}
			j++
		}
		if maxEarlier != 0 && ordered[i].ID <= maxEarlier {
			out = append(out, Violation{
				Invariant: "I4 k-sortability",
				Detail: fmt.Sprintf("ID %d at %dms (process %d) does not exceed %d from %dms (process %d), %dms earlier, beyond the %dms skew bound",
					ordered[i].ID, ordered[i].TrueMs, ordered[i].Process,
					maxEarlier, maxEarlierAt.TrueMs, maxEarlierAt.Process,
					ordered[i].TrueMs-maxEarlierAt.TrueMs, bound),
			})
			if len(out) >= 10 {
				return out
			}
		}
	}
	return out
}
