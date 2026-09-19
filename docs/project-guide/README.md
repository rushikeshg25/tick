# tick — project guide

`tick` is a Go library that generates unique identifiers for systems running on
more than one machine. It ships three time-ordered formats — Snowflake-64,
UUIDv7 and ULID — over one shared core, plus the machinery to claim a node ID
safely, a deterministic simulator that stress-tests the whole thing against
misbehaving clocks, and a benchmark that measures what each format costs a
B-tree. The module has no dependencies outside the standard library
([go.mod:1](../../go.mod)).

## Running it

```sh
make test      # every package
make race      # go test -race -count=1 ./...
make sim       # the seeded simulator corpus
make locality  # the B-tree comparison
make bench
```

All targets are in [Makefile:1](../../Makefile). There is no service to start
for the library itself; `cmd/tickd` builds a demo cluster, covered in
[01-architecture.md](01-architecture.md#deployment-topology).

## A five-file tour

Read these in order and the rest of the codebase follows.

1. **[id.go](../../id.go)** — the 63-bit layout and the `ID` type. Every other
   file speaks in the vocabulary this one defines.
   → [03-structure.md](03-structure.md#root-package-tick)
2. **[clock.go](../../clock.go)** — time is an injected interface, not a call
   to `time.Now`. This is why the rest is testable.
   → [05-decisions.md](05-decisions.md#time-is-an-interface-with-exactly-one-real-implementation)
3. **[watermark.go](../../watermark.go)** — the CAS loop. One atomic word, and
   the two ordering rules the whole library rests on.
   → [02-flow.md](02-flow.md#the-headline-flow-generating-one-id)
4. **[snowflake.go](../../snowflake.go)** — how a format wraps the watermark
   and turns a `(timestamp, counter)` pair into an ID.
   → [03-structure.md](03-structure.md#root-package-tick)
5. **[worker/lease.go](../../worker/lease.go)** — how a node ID is claimed so
   two processes never hold it at once.
   → [02-flow.md](02-flow.md#flow-claiming-and-holding-a-node-id)

## Reading order

| Document | Read it for |
|---|---|
| [01-architecture.md](01-architecture.md) | The four components, what they own, and how the system fails |
| [02-flow.md](02-flow.md) | Generating an ID, claiming a lease, and running the simulator, step by step |
| [03-structure.md](03-structure.md) | What every file does and who calls it |
| [04-tech-stack.md](04-tech-stack.md) | Language, tooling, and the deliberate absence of dependencies |
| [05-decisions.md](05-decisions.md) | Why it is built this way, and the traps |

## What the project is about

The bit shifting is the easy part. The library exists for four problems that
most ID generators leave undefined, and the code is organised around them:

| Problem | Where it lives |
|---|---|
| Clock regression | [watermark.go:84](../../watermark.go#L84) |
| Worker-ID safety | [worker/lease.go:140](../../worker/lease.go#L140) |
| Ordering vs. entropy | Unresolved — see below |
| Index locality | [bench/locality_test.go](../../bench/locality_test.go) |

## Open questions

- **`tick/opaque` is referenced but does not exist.**
  [uuidv7.go:27](../../uuidv7.go#L27) tells the reader to "use tick/opaque for"
  externally visible identifiers. No such package is in the tree. `PLAN.md`
  lists it under "Later", so the doc comment points at unwritten code.
- **The monsoon cluster has never been run end to end.**
  [docs/MONSOON.md](../MONSOON.md) states this directly. The scenario validates
  with `monsoon plan`, and everything below the container boundary has Go
  tests, but no run has confirmed the three-minute scenario holds zero
  duplicates.
- **No `worker.Store` implementation is production-ready.** `MemStore` is
  in-process and `httpstore` has a single-process coordinator, described as a
  deliberate single point of failure at
  [worker/httpstore/httpstore.go:6](../../worker/httpstore/httpstore.go#L6).
  The etcd version exists only as prose in [docs/WORKER-IDS.md](../WORKER-IDS.md).
- **Whether the `[56]byte` padding at [watermark.go:28](../../watermark.go#L28)
  measurably helps is unverified.** The comment gives a sound reason, but no
  benchmark in the tree compares padded against unpadded.
