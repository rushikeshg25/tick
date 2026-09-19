# Implementation pitfalls

Traps found while building M1. Each one is either silent, load-dependent, or
both — the kind that passes a casual test suite and corrupts data later. The
code notes where each is handled.

---

## 1. Read order in the CAS loop decides correctness

The nastiest bug in the design is two lines in the wrong order.

```go
now := g.clock.NowMillis()   // WRONG
old := g.state.Load()
```

```go
old := g.state.Load()        // RIGHT
now := g.clock.NowMillis()
```

Read the clock first and this happens: you observe `now = 100`; another
goroutine wins the race and advances `state` to `ts = 101`; you then load
`old` and see `ts = 101`. You compute `now < oldTs` and conclude the clock
moved backward — when it never did. The generator then burns sequence space it
did not need to, or returns `ErrClockRegression` on a perfectly healthy host.
It only happens under contention, so it will not show up until production.

Loading first is safe. If the loaded state says `ts = 101`, some goroutine
already observed the clock at `>= 101` strictly before your load, so your later
read is also `>= 101` unless the clock genuinely regressed.

The same rule applies to the retry path: after a failed CAS, re-read **both**
values in that order. Reusing a stale `now` reintroduces the bug.

See `snowflake.go`, `Generator.next`.

## 2. A clock that cannot wait

`Next` waits for the next millisecond when the current one's sequence space is
exhausted. Doing that with `time.Sleep` breaks the rule that only `clock.go`
touches the time package, and — worse — it hangs forever against a `FakeClock`,
which never advances on its own. Busy-spinning on `NowMillis` hangs the same
way, with a pegged core.

So waiting belongs in the `Clock` interface:

```go
SleepUntil(ms int64)
```

`SystemClock` sleeps the remainder. `FakeClock` defaults to *auto-advance*: it
jumps its wall and monotonic readings straight to the target, so exhaustion
tests are deterministic and instant with no helper goroutines. Turning
auto-advance off makes `SleepUntil` block on a condition variable until another
goroutine advances the clock, which is what the M4 simulator will want.

See `clock.go`, `FakeClock.SleepUntil`.

## 3. The wait target is the watermark, not the clock

A direct consequence of pitfall 2:

```go
g.clock.SleepUntil(now + 1)    // infinite loop
g.clock.SleepUntil(oldTs + 1)  // correct
```

When the generator is clock-regressed *and* sequence-exhausted, its watermark
is already ahead of the wall clock. Waiting for `now + 1` returns immediately,
the loop retries, the sequence is still full, and it waits again — spinning
until the wall clock catches up, potentially the whole regression tolerance.
Always wait relative to the watermark.

`TestNextWaitsRelativeToWatermarkNotWallClock` fails with an infinite loop if
this regresses, so it is worth keeping.

## 4. A clock before the epoch produces garbage, silently

`Epoch` is 2026-01-01. A container whose RTC was never set boots at 1970, so
`now - Epoch` is about `-1.77e12`. Shifted into the layout that yields a
negative, meaningless ID — no panic, no error, just corrupt rows.

Both bounds are checked on every read, which costs one comparison and also
handles the 2095 overflow into the sign bit:

```go
if ms < 0 || ms > MaxTimestamp {
    return 0, ErrTimestampOutOfRange
}
```

`New` performs the same check at construction so a misconfigured host fails at
startup rather than at the first request.

See `snowflake.go`, `Generator.timestamp`.

## 5. The race detector proves nothing here

`-race` finds unsynchronized memory access. A CAS loop has none by
construction, so it will pass while the logic is wrong — a lost update or a
wrong branch is not a data race. Run it anyway, but **uniqueness is established
only by the property tests.**

Two practical notes:

- Run with `-cpu 1,2,8`. At `GOMAXPROCS=1` the CAS essentially never fails and
  none of the interesting interleavings are exercised.
- 64 goroutines x 100k IDs in a `map[ID]struct{}` is 6.4M entries and costs
  well over a gigabyte. Collect per-goroutine slices, concatenate, sort, then
  scan adjacent pairs. Much faster, and the sorted order gives the
  k-sortability check for free.

## 6. Contention looks bad because it is bad

One atomic word is one cache line, and every core issuing a CAS against it
makes that line ping-pong between them. Roughly what to expect:

| | ns/op |
|---|---|
| single goroutine | tens |
| 8 goroutines | several hundred |

That is inherent to a single globally ordered sequence, not a defect in the
loop. Do **not** "fix" it by splitting the state across fields: the whole point
of packing timestamp and sequence into one word is that a single CAS publishes
both. The legitimate fix is architectural — give one process several node IDs
and shard across them.

Note also that a large share of the single-threaded cost is `time.Now` itself,
not the atomic. `BenchmarkSystemClockNowMillis` measures that floor so the
generator numbers can be read against it.

---

## Smaller ones

**False sharing.** `state` sits on its own cache line, padded away from the
read-only `node`, `clock` and `tol` fields. Without the padding every CAS
invalidates the line holding fields that are only ever read.

**JSON needs a pointer receiver.** `MarshalJSON` is on the value, but
`UnmarshalJSON` must be on `*ID`, so a struct field of type `ID` only
round-trips when addressable. `UnmarshalJSON` also accepts a bare JSON number,
so documents written by other tools still load, and rejects negatives.

**Test that borrowing actually borrows.** When asserting the
within-tolerance regression path, check that the emitted IDs carry the *old*
timestamp. It is easy to write a borrow test that passes without borrowing.

**The first ID may have sequence 1.** The zero state is `(ts=0, seq=0)`, so a
generator whose very first call lands exactly on the epoch millisecond takes
the `now == oldTs` branch and emits `seq = 1`. Harmless, but surprising in a
test that hardcodes zero.
