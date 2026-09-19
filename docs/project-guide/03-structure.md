# Structure

[← 02-flow.md](02-flow.md) · [index](README.md) · next: [04-tech-stack.md](04-tech-stack.md)

The repository root *is* the library. `package tick` lives in the top-level
directory so that importing the module gives you the generators directly, and
everything else is a subdirectory with a narrower job: `worker/` claims node
IDs, `sim/` stress-tests the library, `internal/btree/` and `bench/` measure
it, `cmd/tickd/` and `deploy/` build a cluster that can be broken on purpose.
Dependencies point inward — `worker`, `sim` and `bench` all import `tick`, and
`tick` imports none of them.

Tests sit beside the code they cover rather than in a separate tree, except
for `bench/`, which is its own package so that importing `tick` there exercises
the public API the way a user would.

## Root package: `tick`

The library. Formats, time, and the CAS loop they share.

| File | Responsibility | Key exports | Called by |
|---|---|---|---|
| [doc.go](../../doc.go) | Package documentation: the five invariants I1–I5, and the rule that only `clock.go` calls `time.Now` | — | godoc |
| [errors.go](../../errors.go) | The five sentinel errors that define what the library refuses to do | `ErrClockRegression`, `ErrSequenceExhausted`, `ErrTimestampOutOfRange`, `ErrNodeIDOutOfRange`, `ErrLeaseLost` | everything |
| [clock.go](../../clock.go) | Time as an interface. Wall and monotonic readings exposed separately; `Drift` compares them to tell a step from drift | `Clock`, `SystemClock`, `FakeClock`, `NewSystemClock`, `NewFakeClock` | `watermark`, `worker`, `sim`, every test |
| [id.go](../../id.go) | The 63-bit layout, the `Epoch` constant, the `ID` type and its accessors, JSON as a string | `Epoch`, `MaxNodeID`, `MaxSequence`, `MaxTimestamp`, `ID` | `snowflake.go`, `worker`, `sim` |
| [watermark.go](../../watermark.go) | The CAS loop. One atomic word holding `(timestamp, counter)`, the regression rules, the exhaustion wait | *(unexported)* `watermark`, `advance` | all three generators |
| [snowflake.go](../../snowflake.go) | `Generator` and the option set. Validation, lease binding, `Next`/`TryNext` | `Generator`, `New`, `Option`, `WithClock`, `WithRegressionTolerance`, `WithLease`, `DefaultRegressionTolerance` | users, `cmd/tickd`, `sim`, `bench` |
| [uuid.go](../../uuid.go) | The `UUID` value type: RFC 9562 field accessors, canonical string form, parsing, text marshalling | `UUID`, `ParseUUID` | `uuidv7.go`, users |
| [uuidv7.go](../../uuidv7.go) | `UUIDv7Generator`, plus `NewUUIDv4` as the unordered baseline | `UUIDv7Generator`, `NewUUIDv7Generator`, `NewUUIDv4` | users, `bench` |
| [ulid.go](../../ulid.go) | The `ULID` type, Crockford base32 encode and decode, and its generator | `ULID`, `ParseULID`, `ULIDGenerator`, `NewULIDGenerator` | users, `bench` |
| [format.go](../../format.go) | The `Format` interface and the four adapters that satisfy it | `Format`, `UUIDv4Source` | `bench` |

### Root package tests

| File | Covers |
|---|---|
| [clock_test.go](../../clock_test.go) | `Advance` vs `Step` semantics, drift, `SleepUntil` in both auto-advance modes, monotonic never going backward |
| [id_test.go](../../id_test.go) | Layout round-trips, the boundary where the maximal ID equals `MaxInt64`, JSON above 2^53 |
| [snowflake_test.go](../../snowflake_test.go) | Uniqueness and monotonicity, contended generation, sequence exhaustion in both modes |
| [regression_test.go](../../regression_test.go) | Borrowing within tolerance, refusing beyond it, recovery, the watermark-relative wait, and the no-false-regression contention test |
| [format_test.go](../../format_test.go) | UUIDv7 and ULID layouts, within-millisecond ordering, string round-trips, ULID text order matching byte order |
| [lease_test.go](../../lease_test.go) | `WithLease` stopping the generator, and the formats without a node ID rejecting it |
| [bench_test.go](../../bench_test.go) | Benchmarks: the clock floor, `Next` with and without the millisecond ceiling, parallel, and the string encoders |

## `worker/` — node-ID allocation

Claiming a node ID and, more importantly, giving it up in time.

| File | Responsibility | Key exports | Called by |
|---|---|---|---|
| [worker.go](../../worker/worker.go) | The two interfaces and the package's framing: this is a fencing problem, not a naming problem | `Lease`, `Allocator`, `ErrNoFreeNodeID` | everything in the package |
| [static.go](../../worker/static.go) | A fixed node ID whose lease is never cancelled — the caller asserts exclusivity | `Static` | fixed fleets, `hostname.go` |
| [hostname.go](../../worker/hostname.go) | Derives a node ID from a StatefulSet pod's trailing ordinal | `FromHostname`, `OrdinalFromName` | Kubernetes deployments |
| [lease.go](../../worker/lease.go) | The real allocator: the `Store` interface, `Config` with its safety-window validation, the renew loop | `Store`, `Config`, `New`, `DefaultMaxClockSkew`, `DefaultMaxRoundTrip`, `DefaultSafetyMargin` | `cmd/tickd`, users |
| [memstore.go](../../worker/memstore.go) | An in-process `Store` with an injected clock and a `Partition` switch for tests | `MemStore`, `NewMemStore`, `ErrPartitioned` | `lease_test.go` |
| [lease_test.go](../../worker/lease_test.go) | Handover, expiry, transient failures, config validation, ordinal parsing | — | — |

The central test is `TestSafeIsCancelledBeforeTheIDCanBeReacquired`: it asserts
that at the moment a partitioned holder gives up, the store still considers its
claim live — which proves no successor could have started.

## `worker/httpstore/` — a networked store

| File | Responsibility | Key exports | Called by |
|---|---|---|---|
| [httpstore.go](../../worker/httpstore/httpstore.go) | `worker.Store` over plain HTTP: a coordinator holding claims in memory, and a client | `Server`, `NewServer`, `Client`, `NewClient` | `cmd/tickd` |
| [httpstore_test.go](../../worker/httpstore/httpstore_test.go) | The store contract, wrong-holder rejection, an unreachable coordinator, and an end-to-end allocator driving a generator | — | — |

Exists so `tick` can run as a real multi-process cluster without depending on
etcd. The coordinator is a deliberate single point of failure
([httpstore.go:6](../../worker/httpstore/httpstore.go#L6)).

## `sim/` — the deterministic simulator

| File | Responsibility | Key exports | Called by |
|---|---|---|---|
| [sim.go](../../sim/sim.go) | The virtual world: `Config`, the per-process clock model, the step loop, the emission log | `Config`, `Default`, `Run`, `Report`, `Emission`, `Window` | `sim_test.go` |
| [faults.go](../../sim/faults.go) | Six fault kinds, each chosen by the seeded PRNG | *(unexported)* `applyFault` | `sim.go` |
| [invariants.go](../../sim/invariants.go) | The four checkers, reading the log rather than the generators | `Check`, `Violation` | `sim_test.go` |
| [sim_test.go](../../sim/sim_test.go) | The pinned seed corpus, random exploration, determinism, and deliberately broken logs fed to the checker | — | — |

`stepClock` ([sim.go:318](../../sim/sim.go#L318)) is worth reading: it clamps
every clock disturbance to the configured offset envelope, which is what keeps
the world self-consistent.

## `internal/btree/` — the measurement model

| File | Responsibility | Key exports | Called by |
|---|---|---|---|
| [btree.go](../../internal/btree/btree.go) | A B+tree instrumented for page accounting. Splits, height, pages, and the `Stats` derived metrics | `Tree`, `New`, `Stats` | `bench` |
| [pool.go](../../internal/btree/pool.go) | A fixed-size LRU buffer pool. Dirty evictions are what makes locality visible | *(unexported)* `pool` | `btree.go` |
| [btree_test.go](../../internal/btree/btree_test.go) | Correctness against a plain map, key ordering, and that sequential beats random | — | — |

A model, not a storage engine: nodes live in memory and nothing is serialized
([btree.go:4](../../internal/btree/btree.go#L4)). `internal/` keeps it out of
the public API.

## `bench/` — the locality comparison

| File | Responsibility | Key exports | Called by |
|---|---|---|---|
| [locality_test.go](../../bench/locality_test.go) | Drives all four formats through `tick.Format` into a fresh tree each and compares page writes; also a per-format insert benchmark | — | `make locality` |

Its own package so it imports `tick` as a user would. Results are written up in
[docs/LOCALITY.md](../LOCALITY.md).

## `cmd/tickd/` — the demo cluster

| File | Responsibility | Key exports | Called by |
|---|---|---|---|
| [main.go](../../cmd/tickd/main.go) | Dispatch on `os.Args[1]` into one of three modes, and the usage text | `main` | the binary |
| [coordinator.go](../../cmd/tickd/coordinator.go) | Serves `httpstore.Server` over HTTP | — | `main.go` |
| [generator.go](../../cmd/tickd/generator.go) | Leases a node ID, mints on a ticker, exposes `/drain` and `/stats`, stops on `ErrLeaseLost` | — | `main.go` |
| [verify.go](../../cmd/tickd/verify.go) | Drains every generator and reports duplicates — the only component that can see one | — | `main.go` |

## `deploy/` — the cluster and its weather

| File | Responsibility |
|---|---|
| [Dockerfile](../../deploy/Dockerfile) | Two-stage build of a static `tickd` on Alpine |
| [compose.yaml](../../deploy/compose.yaml) | One coordinator, three generators, a verifier behind a profile |
| [tick-cluster.yaml](../../deploy/tick-cluster.yaml) | A [monsoon](https://github.com/rushikeshg25/monsoon) scenario: six faults over three minutes |

## Top-level files

| File | Responsibility |
|---|---|
| [go.mod](../../go.mod) | Module path and Go version. No `require` block |
| [Makefile](../../Makefile) | `test`, `race`, `bench`, `vet`, `cover`, `clean`, `locality`, `sim` |
| [README.md](../../README.md) | The pitch, the measured numbers, and links into `docs/` |
| [PLAN.md](../../PLAN.md) | The original design and milestone plan, with a note recording where the build diverged |
| [.gitignore](../../.gitignore) | Binaries, coverage output, editor files |

## The `docs/` tree

Prose that predates this guide and goes deeper on single topics.

| File | Contents |
|---|---|
| [PITFALLS.md](../PITFALLS.md) | The six traps, including the CAS read-order bug and the measured benchmark numbers |
| [WORKER-IDS.md](../WORKER-IDS.md) | Allocation strategies, the safety deadline, and a sketch of an etcd `Store` |
| [LOCALITY.md](../LOCALITY.md) | The benchmark method, results, and why random keys produce *fewer* splits |
| [MONSOON.md](../MONSOON.md) | Running the cluster under faults, and what that cannot cover |
