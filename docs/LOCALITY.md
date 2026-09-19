# Index locality

"Random UUIDs are bad for database inserts" is repeated everywhere and
measured almost nowhere. This is the measurement.

## Method

`internal/btree` is a B+tree instrumented for page accounting, fronted by a
fixed-size LRU buffer pool. It is a model, not a storage engine — nodes live
in memory and nothing is serialized — but it reproduces the mechanism that
matters: which pages a bounded pool has to write back.

A **page write** is a dirty page being evicted, plus the dirty pages still
resident at the end. That is the number to watch. Sequential keys dirty the
same rightmost leaf repeatedly and it is written once per eviction; random
keys scatter across every leaf, so nearly every insert eventually costs a
write.

200,000 inserts, tree order 64, buffer pool 256 pages, 64 IDs per virtual
millisecond. Reproduce with `make locality`.

## Results

| format | page writes | splits | height | pages | write amp | hit rate |
|---|---|---|---|---|---|---|
| snowflake64 | 6,444 | 6,440 | 4 | 6,444 | 0.032 | 99.1% |
| uuidv7 | 6,444 | 6,440 | 4 | 6,444 | 0.032 | 99.1% |
| ulid | 6,444 | 6,440 | 4 | 6,444 | 0.032 | 99.1% |
| **uuidv4** | **165,637** | 4,608 | 4 | 4,612 | **0.828** | **73.8%** |

**UUIDv4 causes 25.7 times more page writes than any time-ordered format.**

## Reading the numbers

**Write amplification 0.032 against 0.828.** A time-ordered key costs one page
write per thirty inserts, because thirty consecutive IDs land in the same leaf
and that leaf is written once. A random key costs a write on five inserts out
of six. This is the whole result; everything else explains it.

**Buffer pool hit rate 99.1% against 73.8%.** Time-ordered inserts touch a
working set of one root-to-leaf path. Random inserts touch the whole tree, so
a pool holding 256 of 4,612 pages misses constantly.

**Random keys produce fewer splits and fewer pages.** This looks like a win
and is not. Scattered inserts fill leaves evenly, so pages end up denser;
sequential inserts always split the rightmost leaf and leave the left half
half-empty. Denser pages are worth having, but they cost 25 times the write
traffic to build, and the density advantage disappears under any workload
with deletes.

**Tree height is 4 for all four formats.** Height follows from the key count
and the order, not from insert order. If you were hoping the random keys
produce a deeper tree, they do not — the damage is entirely in write traffic
and cache behaviour.

**The three ordered formats are identical.** They should be: all three embed
a millisecond timestamp in the high bits and a counter below it, so as B-tree
keys they behave the same. The test asserts they stay within 3x of each other,
because a large gap would mean one of them is not actually ordering.

## What this does not measure

Real page sizes, real serialization, real fsync behaviour, or LSM compaction.
Wiring this to an actual storage engine is the obvious next step; the
mechanism being measured would not change, but the constants would.
