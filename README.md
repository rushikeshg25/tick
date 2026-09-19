# tick

Correctness-first distributed ID generation for Go.

```go
g, err := tick.New(nodeID)
id, err := g.Next()
```

Snowflake-64 today; UUIDv7, ULID and an HLC-backed format behind the same
interface later. What the library is actually about is the four things most ID
generators leave undefined:

- **Clock regression.** NTP steps backward, VMs get live-migrated. `tick` holds
  a monotonic watermark, borrows sequence space within a bounded window, and
  returns `ErrClockRegression` rather than emitting a duplicate.
- **Worker-ID safety.** A node ID held by two processes mints duplicates.
  Allocators hand back a context that is cancelled *before* the lease expires,
  not when expiry is noticed.
- **Ordering vs. entropy.** Sortable IDs leak creation time, and monotonic
  counters leak volume. `tick/opaque` maps internal IDs through a Feistel
  network for external use.
- **Index locality.** Random IDs shred B-tree insert performance. This one gets
  measured rather than asserted.

## Status

Early. The clock abstraction is implemented and tested; the generator is
scaffolded and not yet written. See [PLAN.md](PLAN.md) for the full design, the
invariants the test suite exists to prove, and the milestone breakdown.

## Layout

| File | |
|---|---|
| `clock.go` | `Clock`, `SystemClock`, `FakeClock` — the only place `time.Now` is called |
| `id.go` | Bit layout, `ID` accessors, JSON encoding |
| `snowflake.go` | The generator and its CAS loop |
| `errors.go` | Sentinel errors |

## Development

```sh
make test    # go test ./...
make race    # go test -race -count=1 ./...
make bench   # benchmarks with allocation counts
make vet
```
