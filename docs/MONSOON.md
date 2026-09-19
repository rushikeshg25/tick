# Running the cluster under faults

The simulator in `sim` proves the generator's logic against adversarial
clocks. This proves the integration: real processes, real sockets, real
restarts.

## What it is

`deploy/compose.yaml` brings up four containers — one coordinator holding the
node ID claims, three generators leasing one each and minting IDs
continuously. `deploy/tick-cluster.yaml` is a
[monsoon](https://github.com/rushikeshg25/monsoon) scenario that breaks them.

The verifier is the only component that can observe a failure. Each generator
sees only its own output, so uniqueness across the cluster has to be checked
somewhere that sees all of it: `tickd verify` drains every generator once a
second and reports any ID it sees twice.

```sh
docker compose -f deploy/compose.yaml up -d --wait
monsoon run --compose deploy/compose.yaml deploy/tick-cluster.yaml
docker compose -f deploy/compose.yaml --profile verify run --rm verifier
```

The pass condition is zero duplicates, whatever the generators had to refuse
along the way. Refusals are the library working; duplicates are the library
failing.

## What the scenario covers

| at | target | fault | what it tests |
|---|---|---|---|
| 10s | coordinator | latency 400ms | renewals still land, so nothing should stop |
| 40s | coordinator | packet-loss 30% | retries survive a blip; a blip is not an expiry |
| 1m10s | gen1 | pause 25s | a frozen process wakes believing it holds a lease it lost |
| 1m45s | gen2 | disconnect 25s | a cut-off holder must stop before its ID is reassigned |
| 2m20s | coordinator | restart | every claim is lost at once |
| 2m35s | gen3 | restart | a successor must not reuse the ID until the claim expires |

The pause is the one worth watching. A container frozen for 25 seconds with a
15 second TTL wakes up holding a lease that expired while it was not running.
Its monotonic clock kept moving, which is exactly why the safety deadline is
measured against monotonic time and checked *before* each renewal rather than
only after.

## What it does not cover

Monsoon's fault vocabulary is `latency`, `jitter`, `packet-loss`,
`disconnect`, `bandwidth`, `pause`, `stop`, `restart` and `cpu-limit`. There
is no clock-skew fault, so **none of the clock regression paths are exercised
here** — those stay in `sim`, where the clock is injectable and a backward
step is one function call.

The split is deliberate:

- `sim` covers what the generator does when time misbehaves.
- This covers what the cluster does when the network and the scheduler
  misbehave.

Neither covers the other, and a green run of one says nothing about the other.

## Status

The scenario validates and plans against monsoon:

```
$ monsoon plan deploy/tick-cluster.yaml
tick-cluster: 6 events over 3m0s (seed 42)

AT     TARGET       FAULT
10s    coordinator  latency 400ms for 20s
40s    coordinator  packet-loss 30% for 20s
1m10s  gen1         pause for 25s
1m45s  gen2         disconnect for 25s
2m20s  coordinator  restart
2m35s  gen3         restart
```

The cluster itself has not been run end to end here; that needs a Docker
daemon. Everything below the container boundary — the HTTP store, the
allocator, the generator — is covered by ordinary Go tests.

## The coordinator is a single point of failure

Deliberately. `worker/httpstore` keeps claims in one process's memory so the
module stays dependency-free and the demo stays legible. The interesting
question here is what the *generators* do when they cannot reach it, not how
to build a consensus system.

For production, implement `worker.Store` against something replicated. It is
three methods; `docs/WORKER-IDS.md` sketches the etcd version.
