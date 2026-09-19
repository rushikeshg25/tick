# Tech stack

[← 03-structure.md](03-structure.md) · [index](README.md) · next: [05-decisions.md](05-decisions.md)

## Language and runtime

| | Version | Pinned at |
|---|---|---|
| Go | 1.24 | [go.mod:3](../../go.mod#L3) |

Go 1.24 is not incidental. Four things the code relies on are recent:

- **`math/rand/v2`** (Go 1.22) — used at [uuidv7.go](../../uuidv7.go) and
  [ulid.go](../../ulid.go) for entropy, and at
  [sim/sim.go:188](../../sim/sim.go#L188) via `rand.NewPCG` for a seeded,
  reproducible generator. The global source is ChaCha8 seeded from the OS,
  which is why [uuidv7.go:25](../../uuidv7.go#L25) can describe it as ample for
  collision avoidance while explicitly disclaiming unpredictability.
- **`atomic.Uint64`** (Go 1.19) — the typed atomic at
  [watermark.go:23](../../watermark.go#L23), and `atomic.Bool` for the lease
  flag at [snowflake.go:33](../../snowflake.go#L33).
- **`slices`** (Go 1.21) — sorting in the invariant checkers, e.g.
  [sim/invariants.go:42](../../sim/invariants.go#L42).
- **Method patterns in `http.ServeMux`** (Go 1.22) — routes registered as
  `"POST /acquire"` at
  [worker/httpstore/httpstore.go:61](../../worker/httpstore/httpstore.go#L61)
  and `"GET /drain"` at
  [cmd/tickd/generator.go:77](../../cmd/tickd/generator.go#L77). These are
  literal method-prefixed patterns, not manual method checks.

`min`/`max` builtins (Go 1.21) also appear, at
[sim/sim.go:319](../../sim/sim.go#L319) and
[cmd/tickd/generator.go:98](../../cmd/tickd/generator.go#L98).

## Dependencies

**There are none.** [go.mod](../../go.mod) is three lines with no `require`
block, and there is no `go.sum`. Everything is standard library.

This is a constraint the project defends rather than a happy accident. It is
why `worker.Store` is a three-method interface instead of an etcd client
([worker/lease.go:19](../../worker/lease.go#L19)), why `worker/httpstore`
exists at all, and why the locality benchmark measures against an in-tree
B+tree instead of importing another repository — recorded as a deliberate
divergence from the original plan in [PLAN.md](../../PLAN.md).

## Datastores

None. The library persists nothing; see
[01-architecture.md](01-architecture.md#state-and-persistence). The two
`worker.Store` implementations in the tree —
[MemStore](../../worker/memstore.go) and
[httpstore](../../worker/httpstore/httpstore.go) — both hold claims in memory
and are for tests and the demo cluster respectively.

## Infrastructure

Only for the demo cluster, not for the library.

| | Used for | Defined in |
|---|---|---|
| Docker | Two-stage static build on `golang:1.24-alpine` → `alpine:3.20` | [deploy/Dockerfile](../../deploy/Dockerfile) |
| Docker Compose | One coordinator, three generators, a verifier behind a `verify` profile | [deploy/compose.yaml](../../deploy/compose.yaml) |
| [monsoon](https://github.com/rushikeshg25/monsoon) | Injecting timed faults into the running cluster | [deploy/tick-cluster.yaml](../../deploy/tick-cluster.yaml) |

The Dockerfile builds with `CGO_ENABLED=0` so the binary is static and the
runtime image needs nothing but `ca-certificates`.

Monsoon's fault vocabulary is `latency`, `jitter`, `packet-loss`,
`disconnect`, `bandwidth`, `pause`, `stop`, `restart` and `cpu-limit`. Notably
it has **no clock-skew fault**, which is why every clock-regression path is
tested in `sim` instead — stated at [docs/MONSOON.md](../MONSOON.md).

## Dev tooling

Everything is the Go toolchain. No linter config, no formatter config, no
pre-commit hooks, no CI workflow in the tree.

| Target | Command | Line |
|---|---|---|
| `make test` | `go test ./...` | [Makefile:3](../../Makefile#L3) |
| `make race` | `go test -race -count=1 ./...` | [Makefile:6](../../Makefile#L6) |
| `make bench` | `go test -run '^$' -bench . -benchmem ./...` | [Makefile:9](../../Makefile#L9) |
| `make vet` | `go vet ./...` | [Makefile:12](../../Makefile#L12) |
| `make cover` | coverage profile plus an HTML report | [Makefile:15](../../Makefile#L15) |
| `make locality` | the B-tree comparison, verbose | [Makefile:22](../../Makefile#L22) |
| `make sim` | the pinned simulator seed corpus, verbose | [Makefile:25](../../Makefile#L25) |

## CI

There is no CI configuration in the repository — no `.github/`, no pipeline
file. The `race`, `locality` and `sim` targets exist and are the obvious
contents of one, but nothing runs them automatically. The simulator's seed
corpus is described in [sim/sim_test.go](../../sim/sim_test.go) as being "pinned
so CI runs the same worlds every time", so the intent is there ahead of the
pipeline.

## Testing approach

No test framework, no assertion library, no mocks package — plain
`testing` throughout, in four distinct styles:

| Style | Example | What it buys |
|---|---|---|
| Table and property tests | [id_test.go](../../id_test.go) | 100k random layout round-trips |
| Injected-clock tests | [regression_test.go](../../regression_test.go) | Stepping a clock backward is one call |
| Contention tests | `TestNoFalseRegressionUnderContention` in [regression_test.go](../../regression_test.go) | Catches a bug that only appears under load |
| Seeded simulation | [sim/sim_test.go](../../sim/sim_test.go) | Adversarial worlds that replay exactly |

Two habits are worth copying. Concurrency tests are run at several
`GOMAXPROCS` values, because at `-cpu 1` the CAS essentially never fails and
none of the interesting interleavings happen. And the invariant checker is
itself tested against deliberately broken logs
([sim/sim_test.go](../../sim/sim_test.go)) — a checker that has never caught a
violation is not known to work.
