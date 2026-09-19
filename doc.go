// Package tick generates distributed-system-friendly identifiers.
//
// The package is built around a small set of invariants that the test suite
// exists to prove. They are stated in full in PLAN.md; in short:
//
//	I1  Uniqueness           no two calls ever return the same ID
//	I2  Monotonicity         IDs from one generator strictly increase
//	I3  Lease safety         two processes never share a node ID in real time
//	I4  k-sortability        ordered up to the bound of inter-node clock skew
//	I5  Fail loud            return an error rather than emit a suspect ID
//
// Time is read exclusively through a [Clock]. SystemClock is the only
// implementation in this package that calls time.Now, and nothing outside
// clock.go may call it. That single rule is what makes clock regression,
// lease expiry, and the deterministic simulator testable.
package tick
