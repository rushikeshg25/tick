# tick — implementation plan

Correctness-first distributed ID generation for Go.

The goal is not "another Snowflake clone". The goal is an ID library that is
explicit and tested about the four things almost every other one gets wrong:

1. **Clock regression** — NTP steps backward, VMs get live-migrated, clocks drift.
2. **Worker-ID safety** — two processes holding the same node ID mint duplicates.
3. **Ordering vs. entropy** — sortable IDs leak creation time and volume.
4. **Index locality** — random IDs shred B-tree write performance.

Everything below is ordered so that each milestone is independently useful.

---

## Design invariants

These hold for every generator in the library, and the test suite exists to
prove them:

- **I1 — Uniqueness.** No two calls ever return the same ID, under any clock
  behaviour, any goroutine interleaving, and any lease transition.
- **I2 — Per-node monotonicity.** IDs from a single generator strictly increase.
- **I3 — Lease safety.** For a given node ID, the emission windows of two
  distinct processes never overlap in real time.
- **I4 — Bounded k-sortability.** If `a` was generated at least `maxClockSkew`
  before `b` in real time, then `a < b`.
- **I5 — Fail loud.** When an invariant cannot be upheld, return an error.
  Never block forever, never emit a suspect ID.

Rule that makes all of this testable: **nothing outside `clock.go` may call
`time.Now()`.** Time is injected everywhere.

---

## Milestones

| # | Scope | Status |
|---|---|---|
| M1 | Clock abstraction, Snowflake-64, lock-free generator, regression handling | done |
| M2 | UUIDv7 + ULID behind a shared interface | done |
| M3 | Worker-ID allocators: static, ordinal, lease with fencing | done |
| M4 | Deterministic simulator with adversarial clocks | done |
| M5 | Index-locality benchmark | done |
| M6 | `monsoon` scenario against a real cluster | scenario validated; cluster not yet run |

Three things landed differently from the plan below, and the plan is left as
written so the difference is visible.

**The `Clock` interface needed a third method.** Waiting for the next
millisecond cannot live in the generator, and a fake clock that never advances
turns a busy-wait into a hang. `SleepUntil` is on the interface; see
docs/PITFALLS.md.

**No etcd `Store` ships.** Keeping the module dependency-free was worth more
than vendoring an etcd client, so `worker.Store` is a three-method interface
with an in-memory implementation for tests and an HTTP one for the cluster
demo. docs/WORKER-IDS.md sketches the etcd version.

**M5 measures against its own B+tree,** not `bp-tree` or `mvccdb`. Reaching
across repositories would have coupled this module to two others; the model in
`internal/btree` reproduces the mechanism that matters and stays in-tree.

---

## M1 — Snowflake-64

### Bit layout

```
 63  62                                    22 21        12 11         0
┌───┬────────────────────────────────────────┬───────────┬────────────┐
│ 0 │        timestamp ms (41 bits)          │ node (10) │  seq (12)  │
└───┴────────────────────────────────────────┴───────────┴────────────┘
```

- Bit 63 is always zero, so every ID is a positive `int64` and fits a Postgres
  `BIGINT` / `bigserial` column.
- 41 bits of milliseconds = **69.7 years** from a custom epoch. Pick the epoch
  once, write it down, never change it. Suggested: `2026-01-01T00:00:00Z`.
- 10 bits of node = **1024 concurrent generators**.
- 12 bits of sequence = **4096 IDs/ms/node** = 4.096M IDs/sec/node.

> **Gotcha to document:** IDs above 2^53 lose precision in JavaScript. Serialize
> to JSON as a **string**, always. Add a `MarshalJSON` that emits a quoted
> decimal and a test that round-trips through `encoding/json`.

### The packed-state trick

Timestamp and sequence must advance together or not at all. Pack them into a
single `uint64` and drive the whole generator with one CAS loop — no mutex, and
the two fields can never tear apart:

```go
// state layout: timestamp << seqBits | sequence
type Generator struct {
    state atomic.Uint64
    node  uint64 // pre-shifted: uint64(nodeID) << seqBits
    clock Clock
    epoch int64
    tol   time.Duration // clock regression tolerance
}
```

Generation loop, in pseudocode:

```
for {
    old            := state.Load()
    oldTs, oldSeq  := unpack(old)
    now            := clock.NowMillis() - epoch

    switch {
    case now > oldTs:
        newTs, newSeq = now, 0

    case now == oldTs:
        if oldSeq == maxSeq {
            // sequence exhausted this millisecond
            //   Next():    wait for the next ms, retry
            //   TryNext(): return ErrSequenceExhausted
            continue
        }
        newTs, newSeq = oldTs, oldSeq+1

    case now < oldTs:
        // CLOCK WENT BACKWARD
        if oldTs-now > tol {
            return ErrClockRegression   // do not guess, do not emit
        }
        // Within tolerance: hold the watermark and borrow sequence space.
        // Uniqueness is preserved because (ts, seq) is still never reused.
        if oldSeq == maxSeq {
            return ErrClockRegression   // borrowed space is gone too
        }
        newTs, newSeq = oldTs, oldSeq+1
    }

    if state.CompareAndSwap(old, pack(newTs, newSeq)) {
        return ID(newTs<<tsShift | node | newSeq)
    }
    // lost the race, retry
}
```

The regression branch is the whole point of the project. Note that it never
rewinds the watermark — the generator runs slightly ahead of the wall clock
until real time catches up.

### Clock abstraction

```go
type Clock interface {
    // NowMillis returns wall-clock milliseconds since the Unix epoch.
    NowMillis() int64
    // Since returns monotonic elapsed time since the clock was created.
    Since() time.Duration
}
```

Two implementations:

- `SystemClock` — the only place in the repo that calls `time.Now()`.
- `FakeClock` — settable, steppable, advanceable. Used by every test.

**Detecting a step vs. normal drift:** record `(wall0, mono0)` at construction.
Expected wall time is `wall0 + (mono - mono0)`. If the observed wall clock
deviates from that by more than a threshold, the clock was *stepped* (NTP,
`settimeofday`, VM restore) rather than drifting. Worth exposing as
`Clock.StepDetected()` and worth a counter in metrics.

### API

```go
func New(nodeID uint16, opts ...Option) (*Generator, error)

func (g *Generator) Next() (ID, error)     // may block up to ~1ms on exhaustion
func (g *Generator) TryNext() (ID, error)  // never blocks

func (id ID) Time() time.Time
func (id ID) Node() uint16
func (id ID) Seq() uint16
func (id ID) String() string  // base32 or decimal — pick one, document it
```

Errors: `ErrClockRegression`, `ErrSequenceExhausted`, `ErrNodeIDOutOfRange`.

### M1 test checklist

- 1M sequential IDs: all unique, strictly increasing.
- 64 goroutines × 100k IDs: all unique. This is the CAS correctness test; run it
  under `-race` **and** with `GOMAXPROCS=1` and `=8`.
- Fake clock frozen: exactly 4096 IDs succeed in one ms, the 4097th blocks
  (`Next`) or errors (`TryNext`).
- Fake clock stepped back 5ms, tolerance 10ms: still generates, still unique,
  watermark does not rewind.
- Fake clock stepped back 500ms, tolerance 10ms: returns `ErrClockRegression`.
- Encode/decode round-trip for random `(ts, node, seq)` triples.
- JSON round-trip preserves the exact value.

### M1 commit sequence

1. `go mod init`, LICENSE, `.gitignore`
2. `clock.go` — `Clock` interface, `SystemClock`, `FakeClock` **(start here)**
3. `clock_test.go` — fake clock semantics, step detection
4. `id.go` — bit layout constants, `ID` type, encode/decode, accessors
5. `id_test.go` — round-trip property test
6. `snowflake.go` — `Generator`, options, the CAS loop, happy path only
7. `snowflake_test.go` — uniqueness + monotonicity
8. sequence exhaustion: blocking and non-blocking paths + tests
9. clock regression: tolerance window, borrow, error + tests
10. `MarshalJSON` / `UnmarshalJSON` as strings + test
11. concurrency test under `-race`
12. `BenchmarkNext` single-core and parallel; record ns/op in the README

---

## M2 — UUIDv7 and ULID

Both are 128-bit, both are time-ordered, both need the same
monotonic-within-a-millisecond logic already written in M1. **Factor that logic
out of `snowflake.go` before adding the second format**, rather than after.

### UUIDv7 (RFC 9562)

```
48 bits  unix_ts_ms
 4 bits  version (0111)
12 bits  rand_a      <- use as a counter for within-ms monotonicity
 2 bits  variant (10)
62 bits  rand_b
```

Use RFC 9562 Method 2: seed `rand_a` randomly at the start of each millisecond
and increment it for subsequent IDs in that same millisecond. On overflow of the
12-bit counter, spin to the next millisecond. This gives sortability *within* a
millisecond, which plain v7 does not guarantee.

### ULID

48-bit millisecond timestamp + 80 bits of randomness, rendered as 26 characters
of Crockford base32 (alphabet excludes `I`, `L`, `O`, `U`). Monotonic variant:
within the same millisecond, increment the 80-bit random field as a big integer;
error on overflow.

Test both against published vectors from their specs, not just against
themselves.

---

## M3 — Worker-ID allocation

This is a **fencing** problem, not a naming problem.

| Strategy | Verdict |
|---|---|
| Static config | Fine for fixed fleets, dies with autoscaling |
| Random pick | Birthday problem: 1024 slots, 38 nodes ≈ 50% collision. No. |
| Pod IP hash | IPs get reused and hashes collide. No. |
| StatefulSet ordinal | Clean, but only works for StatefulSets |
| **etcd lease + TTL** | Correct, *if* expiry is handled properly |

### The interface

```go
type Allocator interface {
    // Acquire returns a node ID and a context that is cancelled the moment
    // the ID is no longer safe to use.
    Acquire(ctx context.Context) (nodeID uint16, safe context.Context, err error)
}
```

The `safe` context is the fence. The generator checks it via a cheap atomic
flag before emitting, and returns `ErrLeaseLost` once it trips.

### The safety deadline

A process must stop generating **before** its lease expires, not when it
notices the lease expired:

```
stopAt = leaseGrantedAt + leaseTTL - maxClockSkew - maxRoundTrip - safetyMargin
```

If you cross that line and keep minting, another pod may already have legally
acquired your node ID. This is Kleppmann's fencing-token argument applied to ID
generation, and it is the single most important correctness property in M3.

Implementations to ship: `worker.Static(n)`, `worker.FromHostname()` (parses the
trailing ordinal of `svc-3`), `worker.Etcd(client, prefix, ttl)`.

---

## M4 — Deterministic simulator

The part that separates this from the other 200 Snowflake clones. Same shape as
`mvccdb`'s `sim_test.go`: a seeded virtual world, no real time, no real network.

```go
type World struct {
    clock *VirtualClock       // true time
    nodes []*virtualNode      // each with its OWN clock offset and drift rate
    rng   *rand.Rand          // seeded — failures are reproducible
}
```

Faults injected from a seeded schedule:

- clock stepped backward on node *i*
- clock drift rate changed on node *i*
- network partition between node *i* and etcd
- **process pause** (GC / VM freeze) — the nastiest, because the process wakes
  up believing it still holds a lease it lost minutes ago

Every emission is appended to a global log of `(trueTime, processID, nodeID, id)`.
After the run, the checker asserts I1–I4 over the whole log — in particular I3,
that for any node ID the emission windows of two distinct processes never
overlap.

Pin a seed corpus in CI. Shrink on failure and commit the shrunk case as a
regression test.

---

## M5 — Index-locality benchmark

Everyone asserts UUIDv4 is bad for B-tree inserts. Almost nobody measures it.
`bp-tree` and `mvccdb` are already here, so measure it.

Insert 1M keys in each format and record:

- page splits
- final tree height
- bytes written (write amplification)
- buffer-pool hit rate
- on-disk size after compaction

Formats: UUIDv4, UUIDv7, ULID, Snowflake-64. Publish the table and the chart in
the README. This is the headline result.

---

## M6 — `monsoon` scenario

Translate the M4 fault schedule into a real `monsoon` scenario: a real cluster,
real containers, real NTP skew, real partitions. The simulator proves the logic;
this proves the integration.

---

## Non-goals

- Cryptographic unpredictability of the sortable IDs. Use `opaque/` for
  externally visible identifiers.
- Globally *total* ordering. Without TrueTime-grade clocks you get
  k-sortability, and the library should say so plainly rather than imply more.
- A coordination service of its own. `tick` consumes etcd; it does not become it.

---

## Later: `tick/opaque`

Sortable IDs leak creation time, and monotonic counters leak volume (the German
tank problem — a competitor can estimate your signup rate from two IDs). The fix
is two identifiers: internal sortable, external opaque.

Make the mapping a **Feistel network** over the internal ID: format-preserving,
reversible without a lookup table, and collision-free by construction. 4 rounds
with a keyed PRF is plenty for non-adversarial obfuscation; document clearly
that it is obfuscation, not encryption.
