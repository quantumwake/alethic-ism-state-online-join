# Synopsis — Alethic ISM State Online Join (1-pager)

## What it is
A single-purpose Go microservice in the Alethic ISM platform that performs a **real-time,
in-memory stream–stream join**. It subscribes to events on NATS, correlates them across
two (or more) input sources by a configurable join key inside a **sliding time window**,
and publishes the merged records downstream. No HTTP API, no CLI flags — a long-running
NATS worker.

## How it works (one breath)
NATS (`data/transformers/mixer/state-online-join-1.0`) → `handler.MessageCallback` looks up
the input route's output states (Postgres, 30s-cached) → for each output state it gets/creates
an in-memory `BlockStore` keyed by that state's join-key columns → each event is wrapped as a
`BlockPart` and joined against currently-valid parts from **other** sources → every match is
published immediately to `processor/state/sync`, with status to `processor/monitor`.

## The cache, in three layers
1. **Backend metadata cache** — Postgres route/processor/state lookups, 30s TTL.
2. **`CacheBlockStore`** — `map[outputStateID] → BlockStore`; a store idle > **2h** is
   dropped (swept every 5m).
3. **`BlockStore`** — the join cache: `map[joinKeyValue] → Block`, plus a **min-heap** on
   each block's `evictionTime`. Inside a block, parts are bucketed **by source**.

## Eviction (not classic LRU — TTL + heap + soft limit)
| Unit | Rule | Default |
|------|------|---------|
| `BlockPart` | dropped when `JoinCount ≥ blockPartMaxJoinCount` **or** past `blockPartMaxAge` | maxJoins **1**, age **15s** |
| `Block` | window-evicted when `evictionTime < now` **and** block count `> blockCountSoftLimit` | window **1m**, soft limit **10** |
| `BlockStore` | removed when idle `> storeIdleTTL` | **2h** |

Block `evictionTime` slides (resets to `now + blockWindowTTL`) on every event; the heap keeps
the soonest-to-expire block on top so eviction is cheap.

## Are joins one-time? — yes, by default
With the **code default `blockPartMaxJoinCount = 1`**, a stored part joins the **first**
matching event from another source, emits **once**, and is then **lazily compacted out** on
the next pass that scans it (not the instant it matches). So you observe a "one emit per
key, then gone" pattern. It's **configurable**: raise `blockPartMaxJoinCount` for repeated /
fan-out joins (bounded by `blockPartMaxAge`). The **join key (block) itself is not evicted on
match** — only its parts are consumed; the block survives under its sliding window.
> Caveat: the README example lists `100`; the real default is `1`.

## Merge result
Join-key fields + non-key fields from both events (plain names → overlapping non-key fields
overwrite; prefixing is a TODO) + a `joinedAt` RFC3339 stamp.

## Operational shape
- **Stack:** Go 1.24, `quantumwake/alethic-ism-core-go`, NATS, PostgreSQL.
- **Config:** env `DSN` + `ROUTING_FILE`; join window via the input processor's `Properties` JSON.
- **Deploy:** Docker → k8s namespace `alethic`, **`replicas: 1` (mandatory)** — cache is
  process-local, so horizontal scaling would split join state. CI: push→build, tag `v*`→deploy.
- **Lifecycle:** graceful SIGTERM teardown; in-memory join state is ephemeral by design.

## Known gaps
Per-message monitor status updates (volume risk) · no distributed cache (single-instance
correctness) · non-key field name collisions on merge · README/default mismatch on
`blockPartMaxJoinCount`.

## Where to look
`pkg/handler/handler_join.go` (pipeline) · `pkg/correlate/block.go` (join + eviction) ·
`pkg/correlate/cache_block.go` (store cache) · `pkg/correlate/block_test.go` (behavioural spec).
Full detail: [architecture.md](architecture.md) · [usage.md](usage.md) ·
[build.md](build.md) · [deployment.md](deployment.md).
</content>
