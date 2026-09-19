# Worker IDs

A node ID held by two processes at once mints duplicate IDs, and nothing
inside the generator can prevent it. Allocation is therefore a fencing
problem, and `tick/worker` treats it as one.

## Choosing a strategy

| Strategy | Verdict |
|---|---|
| Static configuration | Fine for a fixed fleet. Dies with autoscaling. |
| Random selection | Birthday problem: 1024 slots and 38 nodes is roughly a 50% chance of collision. No. |
| Hash of the pod IP | IPs are reused and hashes collide. No. |
| StatefulSet ordinal | Clean and coordination-free, but only for stable ordinals. |
| Lease over a shared store | Correct under autoscaling, if expiry is handled properly. |

`worker.Static` and `worker.FromHostname` cover the first and fourth rows.
`worker.New` covers the last.

## The safety deadline

The rule that makes leases correct is that a holder must stop generating
**before** its claim expires, not when it notices the expiry:

```
usable window = TTL - MaxClockSkew - MaxRoundTrip - SafetyMargin
```

Three things can sit between "my claim ended" and "I found out":

- **MaxClockSkew** — the store's clock may run ahead of ours, so the claim can
  expire there before it looks expired here.
- **MaxRoundTrip** — a renewal already in flight may not arrive.
- **SafetyMargin** — this process may be descheduled at the worst moment, by
  the CPU scheduler, a garbage collection pause, or a hypervisor.

Cross that line while still generating and a successor may already hold the
ID legally. This is Kleppmann's fencing-token argument applied to ID
generation.

Two details in the implementation follow from it:

- The deadline is tracked with the clock's **monotonic** reading, never the
  wall clock. A lease must survive NTP stepping the wall clock.
- The renew loop checks the deadline **before** attempting a renewal as well
  as after. A goroutine that was descheduled past the deadline is already
  unsafe, however the renewal turns out.

A transient store failure is not an expiry. The loop keeps retrying until the
deadline actually runs out, which is what the window is for.

## Wiring it to a generator

```go
alloc, err := worker.New(worker.Config{
    Store:  store,
    Holder: os.Getenv("POD_UID"),
    TTL:    30 * time.Second,
})
lease, err := alloc.Acquire(ctx)
gen, err := tick.New(lease.NodeID(), tick.WithLease(lease.Safe()))
```

`tick.WithLease` makes the generator enforce the fence itself: once the
context is cancelled, every call returns `ErrLeaseLost` and the generator
never issues another ID.

## Implementing a Store

`tick` has no dependencies, so no concrete store ships with it beyond
`MemStore`. The interface is three methods, and an etcd implementation is
about forty lines:

- **TryAcquire** — `Grant` a lease for the TTL, then a transaction comparing
  the key's create revision to zero, putting the holder with that lease
  attached. The comparison is what makes two concurrent acquisitions safe.
- **Renew** — `KeepAliveOnce` on the lease, plus a read confirming the key
  still names this holder. The read matters: a lease can be kept alive after
  the key was taken over.
- **Release** — delete the key and revoke the lease.

The only hard requirement is that the store be linearizable per node ID: two
concurrent `TryAcquire` calls for the same ID must not both succeed.
