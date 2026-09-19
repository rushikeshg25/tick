# Decisions and gotchas

[← 04-tech-stack.md](04-tech-stack.md) · [index](README.md)

Choices inferred from the code, each with the evidence and the tradeoff it
accepts. Where the code states its own reasoning in a comment, that is quoted
as evidence rather than treated as proof the reasoning is right.

## Timestamp and counter share one atomic word

**Evidence:** [watermark.go:20](../../watermark.go#L20). The struct holds a
single `atomic.Uint64` encoding `timestamp<<bits | counter`, with
`pack`/`unpack` at [watermark.go:118](../../watermark.go#L118).

**Why:** the two fields must advance together or not at all. One
`CompareAndSwap` publishes both, so no goroutine can observe a new timestamp
beside a stale counter. Keeping them in separate atomics reintroduces exactly
that interleaving.

**Tradeoff:** the timestamp is capped at `64 - bits` bits inside the
watermark, and the whole generator funnels through one cache line. The second
cost is real but invisible: [docs/PITFALLS.md](../PITFALLS.md) measures
parallel generation at the same 341ns/op as serial, because the
4096-per-millisecond ceiling dominates long before contention does.

## Time is an interface with exactly one real implementation

**Evidence:** `Clock` at [clock.go:16](../../clock.go#L16); the rule that only
`clock.go` may call `time.Now` is stated in the package doc
([doc.go](../../doc.go)) and holds — `SystemClock`
([clock.go:47](../../clock.go#L47)) is the only caller in the library.

**Why:** two payoffs. Clock regression becomes one function call to test
([regression_test.go](../../regression_test.go)), and the simulator can give
each of eight virtual processes its own clock with its own offset and drift
([sim/sim.go:287](../../sim/sim.go#L287)).

**Tradeoff:** an interface call on the hot path, and `SleepUntil` had to be
added to the interface purely because waiting could not live in the generator
— a divergence from the original plan, recorded in
[PLAN.md](../../PLAN.md).

### `Advance` and `Step` are different operations

**Evidence:** [clock.go:158](../../clock.go#L158) and
[clock.go:173](../../clock.go#L173). `Advance` moves the wall *and* monotonic
readings; `Step` moves only the wall reading. `Advance` panics on a negative
duration.

**Why:** they model different physical events. Advancing is time passing;
stepping is the clock being *set* — NTP correcting an offset, a VM restored
from a snapshot, someone running `date -s`. `Step` with a negative duration is
the regression case the library exists to survive, and it is the only way to
produce one.

## The fake clock auto-advances by default

**Evidence:** [clock.go:113](../../clock.go#L113) sets `auto: true`;
`SleepUntil` at [clock.go:130](../../clock.go#L130) jumps to the target when
auto is on and blocks on a `sync.Cond` when it is off.

**Why:** a fake clock that never advances turns `Next`'s
wait-for-next-millisecond into a hang. Auto-advance keeps exhaustion tests
deterministic and instant with no helper goroutines.

**Tradeoff:** the default hides a genuine wait, so a test that needs to observe
blocking must opt out with `SetAutoAdvance(false)`. The simulator does exactly
that ([sim/sim.go:294](../../sim/sim.go#L294)) so an exhausted sequence cannot
silently move a process's clock.

## All three formats cap at 4096 IDs per millisecond

**Evidence:** every generator builds its watermark with `SequenceBits`
([snowflake.go:97](../../snowflake.go#L97),
[uuidv7.go](../../uuidv7.go), [ulid.go](../../ulid.go)).

**Why:** the counter is what orders IDs *within* a millisecond. Plain UUIDv7
leaves that ordering undefined, and the ULID specification leaves it to the
implementation. Routing all three through one watermark gives all three the
same guarantee and the same clock safety, and means the regression logic is
written and tested once.

**Tradeoff:** UUIDv7 and ULID have far more spare bits than 12 and could run
much faster per generator. The ceiling is the price of one shared core.
`format_test.go` asserts the three stay within 3× of each other on the
locality benchmark, which would catch one of them quietly losing its ordering.

## Entropy is drawn fresh on every call, not per millisecond

**Evidence:** [uuidv7.go:70](../../uuidv7.go#L70) and
[ulid.go:163](../../ulid.go#L163) both call `rand.Uint64()` unconditionally.

**Why:** the textbook approach reseeds the random field when the millisecond
rolls over, which raises the question of *which* goroutine reseeds it. Drawing
fresh randomness every time removes the race entirely: ordering comes from the
counter, collision resistance from the random bits, and the two never interact.

**Tradeoff:** one PRNG call per ID that a per-millisecond scheme would amortise.
At 18ns per ID against a fake clock ([docs/PITFALLS.md](../PITFALLS.md)) it is
not the bottleneck.

## The lease deadline is monotonic, and checked before renewing

**Evidence:** `deadline` is a `time.Duration` from `Clock.Since()`, not a
wall-clock instant — [worker/lease.go:190](../../worker/lease.go#L190) and
[worker/lease.go:215](../../worker/lease.go#L215). The renew loop checks it
*before* attempting a renewal at
[worker/lease.go:245](../../worker/lease.go#L245).

**Why:** a lease must survive the wall clock being stepped, so expiry cannot be
measured against it. And a goroutine descheduled past its deadline is already
unsafe however the renewal turns out — checking only afterwards means a frozen
container wakes up, renews successfully, and carries on holding an ID someone
else may already own.

**Tradeoff:** the usable window
([worker/lease.go:140](../../worker/lease.go#L140)) is the TTL minus three
safety terms, so a 30-second TTL with default bounds yields about 26 seconds of
usable lease. Buying safety with lease lifetime.

## `Static` returns a lease that is never cancelled

**Evidence:** [worker/static.go:22](../../worker/static.go#L22) returns
`context.Background()`.

**Why:** nothing is watching, because the caller is asserting exclusivity.
Fabricating a cancellable context that never fires would imply a guarantee that
does not exist.

**Tradeoff:** silent if misused. The doc comment
([worker/static.go:5](../../worker/static.go#L5)) is explicit that this is
reasonable for a fixed fleet and wrong for anything that autoscales, but
nothing in the code enforces it.

## `WithLease` is rejected, not ignored, by the formats without a node ID

**Evidence:** [uuidv7.go](../../uuidv7.go) and [ulid.go](../../ulid.go) both
return an error when `cfg.safe != nil`; covered by
`TestWithLeaseIsRejectedByFormatsWithoutANodeID` in
[lease_test.go](../../lease_test.go).

**Why:** the comment at [snowflake.go:64](../../snowflake.go#L64) puts it
plainly — a safety option that is silently dropped is how incidents happen.
UUIDv7 and ULID have no node field, so a lease means nothing to them.

## Snowflake IDs marshal to JSON as strings

**Evidence:** [id.go:86](../../id.go#L86) writes a quoted decimal;
[id.go:96](../../id.go#L96) accepts both quoted and bare forms on the way back.

**Why:** values above 2^53 lose precision when parsed by JavaScript, so an ID
serialized as a bare number can return from a browser as a *different* ID,
silently. The comment marks it "must not be 'simplified' later".

**Tradeoff:** non-obvious to anyone expecting a number, and asymmetric —
`UnmarshalJSON` accepts numbers so documents written by other tools still load.

## The simulator clamps every clock disturbance

**Evidence:** `stepClock` at [sim/sim.go:318](../../sim/sim.go#L318) clamps
total deviation to `MaxOffset`; every fault in
[sim/faults.go](../../sim/faults.go) goes through it.

**Why:** the comment records that the first version did not clamp, and produced
duplicate IDs — a process whose clock had wandered far ahead would die, and its
successor on the same node ID would reuse timestamps the predecessor had
already issued. The hazard is real, but bounded clock skew is what rules it
out, and a world that breaks its own stated bound tests nothing.

**Tradeoff:** the simulator cannot explore unbounded skew, so that failure mode
is now assumed away rather than tested. Worth remembering if the deployment
assumption ever stops holding.

## The B-tree is a model, not an engine

**Evidence:** [internal/btree/btree.go:4](../../internal/btree/btree.go#L4)
says so; nodes are in-memory structs and nothing is serialized.

**Why:** the quantity that matters is which pages a bounded buffer pool has to
write back, and that can be counted without real I/O. `internal/` keeps it out
of the public API.

**Tradeoff:** the absolute numbers are not real page writes. The mechanism
holds, the constants do not, and [docs/LOCALITY.md](../LOCALITY.md) says so.
Its correctness is checked against a plain map in
[btree_test.go](../../internal/btree/btree_test.go), which matters because a
broken tree would still produce plausible-looking statistics.

---

## Gotchas

Things that will bite a newcomer, roughly in the order they will hit them.

**Two lines in `advance` are order-dependent.** `state.Load()` must come before
`clock.NowMillis()` — [watermark.go:71](../../watermark.go#L71). Swap them and a
concurrent advance looks exactly like the clock moving backward, producing
spurious `ErrClockRegression` on a healthy host, under contention only.
`TestNoFalseRegressionUnderContention` in
[regression_test.go](../../regression_test.go) exists to catch this and nothing
else.

**The exhaustion wait targets the watermark, not the clock.**
[watermark.go:105](../../watermark.go#L105) waits for `Epoch + oldTs + 1`.
Using `now + 1` returns immediately while regressed and spins. The test that
guards it records `SleepUntil` targets rather than just checking the result,
because a wall-clock target still terminates against an auto-advancing fake
clock and would otherwise pass.

**`Epoch` can never change.** [id.go:15](../../id.go#L15). Changing it rewrites
the meaning of every ID already issued and silently reorders them against new
ones.

**The first ID may have sequence 1, not 0.** The zero state is
`(ts=0, seq=0)`, so a generator whose first call lands exactly on the epoch
millisecond takes the same-millisecond branch. Harmless, surprising in a test
that hardcodes zero. Noted in [docs/PITFALLS.md](../PITFALLS.md).

**`-race` proves nothing about the CAS loop.** It finds unsynchronised memory
access; a CAS loop has none by construction and will pass while the logic is
wrong. Uniqueness is established only by the property tests, and only when run
at several `GOMAXPROCS` values.

**`Next` at 341ns/op is not slow code.** It is the 4096-per-millisecond
ceiling. `BenchmarkNextFakeClock` in [bench_test.go](../../bench_test.go)
removes the ceiling and shows 18ns. Do not "optimise" the loop in response to
the first number.

**Tests must not share a `RenewSignal` channel between holders.** Each
`worker.Config` gets its own; sharing one lets whichever renew goroutine is
scheduled first consume a trigger meant for the other. This failed under
`-race` at low `GOMAXPROCS` and the fixture in
[worker/lease_test.go](../../worker/lease_test.go) now allocates one per
holder.

**`tick/opaque` does not exist.** [uuidv7.go:27](../../uuidv7.go#L27) directs
the reader to it for externally visible identifiers. It is listed under
"Later" in [PLAN.md](../../PLAN.md) and is not in the tree. Until it is, a
UUIDv7 from this library leaks its creation time and its within-millisecond
counter, and nothing in the package prevents you from exposing one publicly.
