# tick

Correctness-first distributed ID generation for Go. No dependencies.

```go
g, err := tick.New(nodeID)
id, err := g.Next()
```

Snowflake-64, UUIDv7 and ULID behind one interface. What the library is
actually about is the four things most ID generators leave undefined.

## Clock regression

NTP steps backward, VMs get live-migrated, containers boot with an unset
clock. `tick` holds a monotonic watermark that is never rewound, borrows
sequence space within a bounded window, and returns an error rather than a
duplicate.

```go
id, err := g.Next()
switch {
case errors.Is(err, tick.ErrClockRegression):     // clock moved backward too far
case errors.Is(err, tick.ErrTimestampOutOfRange): // clock outside the layout's window
}
```

A host whose clock was never set boots in 1970 and is rejected at
construction, not at its first request.

## Worker-ID safety

A node ID held by two processes mints duplicates, and no care inside the
generator can prevent it. A lease therefore carries a context cancelled
*before* the claim could pass to anyone else:

```go
lease, _ := alloc.Acquire(ctx)
gen, _ := tick.New(lease.NodeID(), tick.WithLease(lease.Safe()))
```

The usable window is `TTL - MaxClockSkew - MaxRoundTrip - SafetyMargin`, and
it is tracked with monotonic time so it survives the wall clock being
stepped. See [docs/WORKER-IDS.md](docs/WORKER-IDS.md).

## Index locality, measured

200,000 inserts into an instrumented B+tree with a 256-page buffer pool:

| format | page writes | write amp | pool hit rate |
|---|---|---|---|
| snowflake64 | 6,444 | 0.032 | 99.1% |
| uuidv7 | 6,444 | 0.032 | 99.1% |
| ulid | 6,444 | 0.032 | 99.1% |
| **uuidv4** | **165,637** | **0.828** | **73.8%** |

**UUIDv4 costs 25.7x more page writes than any time-ordered format.**
`make locality` reproduces it; [docs/LOCALITY.md](docs/LOCALITY.md) explains
why random keys produce *fewer* splits and why that is not a win.

## Proof, not assertion

`sim` runs the real generators in a seeded virtual world — clock steps in both
directions, drift, process pauses, crashes and node-ID handover — then checks
every invariant over the whole emission log. A failing run replays exactly
from its seed.

```
$ make sim
emitted 37423 IDs across 63 windows;
faults map[crash:57 drift-change:74 pause:74 step-back-large:65 step-back-small:68 step-forward:59];
errors map[clock-regression:4417]
```

Four thousand refusals, sixty-three tenures on eight node IDs, zero
violations. The checker is itself tested against deliberately broken logs, one
per invariant — a checker that has never caught a violation is not known to
work.

## Formats

| | bits | sortable | needs a node ID | text |
|---|---|---|---|---|
| `tick.New` | 64 | yes | yes | base-10, fits `BIGINT` |
| `tick.NewUUIDv7Generator` | 128 | yes | no | 36-char UUID |
| `tick.NewULIDGenerator` | 128 | yes | no | 26-char base32 |
| `tick.NewUUIDv4` | 128 | no | no | 36-char UUID |

All three time-ordered formats share one watermark, so all three inherit the
same clock safety, and all three cap at 4096 IDs per millisecond per
generator. UUIDv7 and ULID are strictly increasing *within* a millisecond,
which plain v7 does not guarantee.

Snowflake IDs serialize to JSON as **strings**: above 2^53 a bare number loses
precision in JavaScript and comes back as a different ID.

## Performance

Apple M1, `go test -bench`:

| | ns/op | allocs |
|---|---|---|
| `SystemClockNowMillis` | 43 | 0 |
| `NextFakeClock` | 18 | 0 |
| `Next` | 341 | 0 |
| `NextParallel` | 341 | 0 |

`Next` reports the layout's ceiling, not the atomic: 4096 IDs per millisecond
is 4.096M per second, about 244ns each. `NextFakeClock` removes the ceiling
and shows the CAS loop's real cost. Parallel is no worse than serial because
contention sits far below the rate limit. Scale past one node's ceiling with
more node IDs, not a different loop.

## Documentation

- [PLAN.md](PLAN.md) — design, invariants, milestones
- [docs/PITFALLS.md](docs/PITFALLS.md) — the traps, including the CAS read-order bug that only appears under contention
- [docs/WORKER-IDS.md](docs/WORKER-IDS.md) — allocation strategies and the safety deadline
- [docs/LOCALITY.md](docs/LOCALITY.md) — the benchmark and how to read it
- [docs/MONSOON.md](docs/MONSOON.md) — running a real cluster under injected faults

## Development

```sh
make test      # go test ./...
make race      # go test -race -count=1 ./...
make bench
make locality  # the B-tree comparison
make sim       # the seeded simulator corpus
make vet
```
