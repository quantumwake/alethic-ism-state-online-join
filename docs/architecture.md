# Architecture — Alethic ISM State Online Join

Real-time, in-memory **stream–stream join** processor for the Alethic ISM platform.
It consumes events off NATS, correlates them across two (or more) input sources by a
configurable join key, emits the merged records to a downstream sync route, and reports
processing status to a monitor route.

> TL;DR of the join semantics: events are grouped into **blocks** keyed by the join-key
> value; within a block, parts are bucketed **by source**; an inbound event joins against
> all *currently-valid* parts from **other** sources. A part's reuse is bounded by
> `blockPartMaxJoinCount` (code default **1** ⇒ effectively a one-time join), and the
> whole block is kept alive by a sliding window TTL. See
> [Caching, LRU & Eviction](#caching-lru--eviction).

---

## 1. Component map

```
┌──────────────────────────────────────────────────────────────────────────────┐
│  main.go                                                                        │
│   └─ handler.Startup(ctx) ──► run until SIGINT/SIGTERM ──► handler.Teardown(ctx)│
└──────────────────────────────────────────────────────────────────────────────┘
            │
            ▼
┌──────────────────────────────────────────────────────────────────────────────┐
│  pkg/handler  — transport + orchestration                                      │
│   setup.go        NATS routes, DB backends, the BlockStore cache               │
│   handler_join.go MessageCallback: the per-message join pipeline               │
│   config.go       processor-properties → join.WindowConfig                     │
│   monitoring.go   PublishRouteStatus / PublishStateSync                        │
│   logging.go      structured log strings                                       │
└──────────────────────────────────────────────────────────────────────────────┘
            │ store.AddData(source, event, emit)
            ▼
┌──────────────────────────────────────────────────────────────────────────────┐
│  pkg/correlate — the join engine (in-memory)                                   │
│   cache_block.go  CacheBlockStore   (one BlockStore per output state)          │
│   block.go        BlockStore        (blocks + min-heap + eviction loop)        │
│   datablock.go    Block / blockHeap (parts grouped by source, heap by TTL)     │
│   blockpart.go    BlockPart         (event + ExpireAt + JoinCount)             │
│   statistics.go   Statistics        (lap timing per AddData)                   │
│   logging.go      structured log strings                                       │
└──────────────────────────────────────────────────────────────────────────────┘

  pkg/store    — older heap-based prototype (CorrelateBlock); not wired into the
                 live path. Kept for reference / tests only.
  pkg/utils    — map-merge strategies + duration formatting helpers.
  pkg/models   — placeholder (Coalesce).
```

Most domain types come from the shared library
[`github.com/quantumwake/alethic-ism-core-go`](https://github.com/quantumwake/alethic-ism-core-go):
`routing` / `routing/nats` (NATS transport), `data/models` (`RouteMessage`,
`MonitorMessage`, `Data`), `repository/{route,processor,state}` (Postgres-backed,
cached DAOs), `repository/processor/join` (`WindowConfig`), and `cache` (local TTL cache).

---

## 2. Runtime data flow

```
            NATS subject: data/transformers/mixer/state-online-join-1.0
                                   │  (models.RouteMessage: RouteID + QueryState[])
                                   ▼
  ┌─────────────────────────────────────────────────────────────────────────┐
  │ handler.MessageCallback (handler_join.go)                                  │
  │                                                                           │
  │  1. unmarshal RouteMessage                                                 │
  │  2. monitor ◄── Running           (processor/monitor)                      │
  │  3. routeBackend.FindRouteWithOutputsByID(RouteID) ──► psIn, psOuts[]      │
  │        (Postgres, wrapped by 30s local cache)                             │
  │                                                                           │
  │  4. for each output state psOut:                                           │
  │     a. store = blockStoreCache.GetOrSet(psOut.ID, build…)                  │
  │          build once:                                                       │
  │            - getProcessorJoinConfig(psIn.ProcessorID)  → WindowConfig      │
  │            - stateBackend.FindStateConfigKeyDefinitionsByType(             │
  │                  psOut.StateID, DefinitionStateJoinKey) → joinKeys         │
  │            - correlate.NewBlockStore(joinKeys, …limits…)                   │
  │     b. for each event in QueryState:                                       │
  │            store.AddData(                                                  │
  │               source   = ingested RouteID,                                 │
  │               event    = queryState,                                       │
  │               emit(d)  = PublishStateSync(psOut.ID, [d]) )                 │
  │                                                                           │
  │  5. monitor ◄── Completed | Failed (deferred)                             │
  │  6. msg.Ack()                                                              │
  └─────────────────────────────────────────────────────────────────────────┘
                                   │  emit(joinedRecord)
                                   ▼
            NATS subject: processor/state/sync   (models.RouteMessage)
            NATS subject: processor/monitor      (models.MonitorMessage status)
```

Key points:

- **Source identity = the input `RouteID`.** Two different upstream routes feeding the
  same output state are two distinct "sources"; the engine only joins parts whose source
  differs (`if inboundSourceID == storedSourceID { continue }`).
- **One BlockStore per output processor-state** (`psOut.ID`). The output state's
  `DefinitionStateJoinKey` columns define what "matching" means.
- **At-least-once**, fire-and-forget emits: each successful pairing immediately calls the
  `emit` callback → `PublishStateSync`. There is no batching of joined output.
- A status update is published to the monitor route **per inbound message** (Running →
  Completed/Failed). `handler_join.go` flags this as a scaling concern (see Open issues).

---

## 3. Caching, LRU & Eviction

There are **three** independent caches/eviction mechanisms. They are layered:

```
 ┌────────────────────────────────────────────────────────────────────────────┐
 │ (A) Backend metadata cache   pkg/handler/setup.go → core-go cache.LocalCache │
 │     route + processor + state DB lookups, DefaultTTL = 30s                    │
 │     Purpose: avoid hammering Postgres on every message.                       │
 │     Tunable via env BACKEND_CACHE_TTL (default 30s).                          │
 └────────────────────────────────────────────────────────────────────────────┘
                                   ▲ used while building a BlockStore
 ┌────────────────────────────────────────────────────────────────────────────┐
 │ (B) CacheBlockStore   cache_block.go   map[outputStateID]*BlockStore         │
 │     storeIdleTTL = 2h, cleanupInterval = 5m                                   │
 │     Eviction: a BlockStore idle (no AddData) > 2h is Shutdown() + removed.    │
 │     Tunable via env BLOCK_STORE_IDLE_TTL / BLOCK_STORE_CLEANUP_INTERVAL.      │
 └────────────────────────────────────────────────────────────────────────────┘
                                   ▲ holds
 ┌────────────────────────────────────────────────────────────────────────────┐
 │ (C) BlockStore   block.go   the actual sliding-window join cache             │
 │     map[joinKeyValue]*Block  +  min-heap ordered by Block.evictionTime       │
 │     Two eviction dimensions: per-Block (window) and per-BlockPart (age/joins) │
 └────────────────────────────────────────────────────────────────────────────┘
```

> Note on "LRU": this engine does **not** use classic LRU. Block eviction is **TTL /
> sliding-window** ordered by a **min-heap** on `evictionTime`, gated by a soft size
> limit. Part eviction is **absolute age + join-count**. "Least-recently-updated" blocks
> happen to sit at the top of the heap, which is LRU-like in effect, but selection is by
> expiry time, not access recency.

### 3.1 Cache structure (C)

```
 BlockStore (one per output state)
 ├─ blocks : map[joinKeyValue] ─────────────► *Block
 │                                              ├─ key          "Utilitarian|class_a|"
 │                                              ├─ evictionTime  now + blockWindowTTL  (resets every event)
 │                                              ├─ heapIndex     position in min-heap
 │                                              └─ partsBySource : map[sourceRouteID][]*BlockPart
 │                                                   ├─ "route-A" → [ BlockPart{Data, ExpireAt, JoinCount} , … ]
 │                                                   └─ "route-B" → [ BlockPart{…} , … ]
 │
 └─ heap : min-heap of *Block ordered by evictionTime
              [0] = soonest-to-expire block  ← eviction inspects here first
```

`BlockPart` (`blockpart.go`):

```
type BlockPart struct {
    Data      models.Data   // the raw event
    ExpireAt  time.Time     // creation + blockPartMaxAge (absolute)
    JoinCount int           // ++ each time this part participates in a join
}
```

### 3.2 The join + compaction pass (`BlockStore.AddData`)

This is the heart of the engine and the answer to *"are joins one-time?"*.

```
AddData(inboundSourceID, event, emit):
  joinKeyValue = key(event)                       # built from JoinKeyDefinitions
  block        = GetOrAddBlock(joinKeyValue)      # new block ⇒ pushed onto heap
  inboundPart  = { Data:event, ExpireAt:now+maxAge, JoinCount:0 }
  block.partsBySource[inboundSourceID] += inboundPart

  for storedSourceID, storedParts in block.partsBySource:
      if storedSourceID == inboundSourceID: continue      # never self-join
      write = 0
      for storedPart in storedParts:
          expired       = storedPart.ExpireAt   <  now
          maxedOut      = storedPart.JoinCount  >= blockPartMaxJoinCount
          if expired or maxedOut:
              continue            # DROP — not copied to write position (compacted out)
          storedParts[write++] = storedPart      # KEEP
          merged = joinData(storedPart, inboundPart)   # JoinCount++ on BOTH
          emit(merged)                                  # → PublishStateSync
      storedParts = storedParts[:write]          # shrink: expired/maxed parts gone

  block.evictionTime = now + blockWindowTTL       # slide the window
  heap.Fix(block)                                 # re-order in the heap
```

```
 source A (route-A)        BLOCK  key="K"        source B (route-B)
 ───────────────►   ┌──────────────────────┐   ◄───────────────
  event a1          │ partsBySource:        │     event b1
  arrives  ─────────┤  A: [a1]              │
                    │  B: []                │   (no B parts yet ⇒ 0 joins, a1 stored)
                    └──────────────────────┘
                              │
  event b1          ┌──────────────────────┐
  arrives  ─────────┤  A: [a1]   ← valid    │  join(a1,b1) ⇒ emit  (a1.JoinCount=1,
                    │  B: [b1]              │                        b1.JoinCount=1)
                    └──────────────────────┘
                              │
  event b2          ┌──────────────────────┐  scan A parts:
  arrives  ─────────┤  a1.JoinCount(1) ≥    │  a1 maxedOut (default max=1) ⇒ SKIP+DROP
                    │  max(1) ⇒ compacted   │  ⇒ a1 removed, NO emit for b2
                    │  A: []   B: [b1,b2]   │
                    └──────────────────────┘
```

**So, with the default `blockPartMaxJoinCount = 1`:** each stored part is consumed by the
**first** matching event from another source, emits **once**, and is then **lazily
removed** on the next `AddData` pass that scans it. This is the "primarily one-time emit"
behaviour you observed. It is **configurable** — raising `blockPartMaxJoinCount` turns it
into a repeated/fan-out inner join (a part joins up to N times before being dropped),
bounded by `blockPartMaxAge`. Setting `blockPartMaxJoinCount = 0` (or any non-positive
value) means **unlimited** — a full **N:M inner join** within the window, where parts are
bounded only by `blockPartMaxAge` and block-window eviction (see the worked example below).

Important precisions:

- Removal is **lazy**, not instantaneous: a maxed/expired part is physically dropped on
  the **next** scan of its source slice (or when the block is window-evicted), not the
  instant its `JoinCount` hits the limit.
- The **inbound** part's own `JoinCount` is *not* re-checked inside the loop, so a single
  inbound event can fan out across *all* currently-valid parts of another source in one
  call (e.g. one B event joining many A parts when `max > 1`).
- The **join key / block is not evicted on match** — only parts are consumed. The block
  lives on under its sliding window.
- **Minimum-source gate:** if `ccc.MinSources > 0`, `AddData` emits nothing for a key until
  it has ≥ `MinSources` distinct *live* sources (`distinctLiveSources`); the window keeps
  sliding while gated so partial data persists. Default/floor is **2** (the natural pairwise
  case); raise it to require more sources present before any emit.

### 3.2.1 Worked example — N:M inner join (cartesian within the window)

Set `blockPartMaxJoinCount = 0` (unlimited) so every part on one side joins every part on
the other within the window. Events arrive interleaved from sources **A** and **B** on the
same join key:

```
arrivals:  a1 , b1 , a2 , b2
```

| Step | Block state `{A-parts \| B-parts}` | Joins evaluated this step | Emitted |
|------|------------------------------------|---------------------------|---------|
| `a1` | `{a1 \| }`           | none (no B parts yet) | —                |
| `b1` | `{a1 \| b1}`         | b1 × a1               | **a1·b1**        |
| `a2` | `{a1, a2 \| b1}`     | a2 × b1               | **a2·b1**        |
| `b2` | `{a1, a2 \| b1, b2}` | b2 × a1, b2 × a2      | **a1·b2, a2·b2** |

Final output = the full cartesian product **{a1·b1, a1·b2, a2·b1, a2·b2}** = **4 rows**,
each emitted exactly once. A pairing is produced only when the *second* of the pair arrives
(the inbound event joins against already-stored parts of the *other* source), so there are
no duplicates and no self-joins.

**Contrast — default `blockPartMaxJoinCount = 1` (one-to-one)** for the same arrivals:

| Step | Action | Emitted |
|------|--------|---------|
| `a1` | store a1 | — |
| `b1` | a1 × b1 → both now at cap (1) | **a1·b1** |
| `a2` | scan B: b1 is capped → dropped; a2 finds no partner | — |
| `b2` | scan A: a1 capped → dropped; a2 (cap 0) → join | **a2·b2** |

Output = **{a1·b1, a2·b2}** = **2 rows**. Each part is consumed by exactly one match, so the
result depends on arrival interleaving — this is the "primarily one-time emit" mode.

### 3.3 Block-level eviction (`evictionLoop`, ticks every 1s)

```
every 1s:
  if len(blocks) <= blockCountSoftLimit:  return          # soft limit: keep everything
  while heap not empty:
     top = heap[0]                                         # soonest evictionTime
     if top.evictionTime < now:  pop + delete(blocks,key)  # evict stale block
     else: break                                           # heap ordered ⇒ stop
```

- **Soft limit gate:** while the block count is at/under `blockCountSoftLimit`, **nothing
  is evicted** — blocks may sit past their window. Pressure only kicks in above the limit.
- Eviction is **min-heap ordered**: only blocks whose `evictionTime` has already passed
  are dropped, cheapest-first.
- `EvictExpiredBlocks()` exists as an unconditional variant but is **not** called on the
  live path.

### 3.4 Store-level eviction (`CacheBlockStore.cleanupLoop`, ticks every 5m)

```
every cleanupInterval (5m):
  for each BlockStore:
     if time.Since(lastAccessed) > storeIdleTTL (2h):
         store.Shutdown()        # stops its 1s eviction goroutine
         delete from cache       # frees all blocks/parts for that output state
```

### 3.5 Eviction summary

| Layer | Unit | Trigger | Default | Where |
|-------|------|---------|---------|-------|
| Backend cache | DB route/processor/state lookup | TTL expiry | 30s (env `BACKEND_CACHE_TTL`) | `setup.go` (core-go `cache.LocalCache`) |
| Part-level | `BlockPart` | `JoinCount >= blockPartMaxJoinCount` (when `> 0`; `0` = unlimited / N:M) **or** `ExpireAt < now` | maxJoins **1** / maxAge **15s** (per-processor via `CacheControlContext`, capped by env `MAX_RETENTION`) | `block.go AddData` (lazy compaction) |
| Block-level | `Block` (one join key) | `evictionTime < now` **and** `len(blocks) > blockCountSoftLimit` | window **1m** (per-processor), softLimit **10** (core: env `BLOCK_COUNT_SOFT_LIMIT`) | `block.go evictionLoop` (1s) |
| Store-level | `BlockStore` (one output state) | idle `> storeIdleTTL` | 2h / 5m (env `BLOCK_STORE_IDLE_TTL`, `BLOCK_STORE_CLEANUP_INTERVAL`) | `cache_block.go cleanupLoop` |

---

## 4. Merge semantics (`joinData`)

When two parts match, the merged `models.Data` is built as:

1. Copy the **join-key** fields (assumed identical on both sides).
2. Copy **non-key** fields from source 1, then source 2 — currently under their **plain
   names**, so overlapping non-key field names **overwrite** (source 2 wins). The source
   prefixing / remap is a known TODO in `block.go`.
3. Add `joinedAt` = RFC3339 timestamp.
4. `JoinCount++` on both parts.

---

## 5. Concurrency model

- `BlockStore` is guarded by a single `sync.Mutex`; `AddData` and `evictionLoop` both lock
  it, so block/part mutation is serialized per output state.
- `CacheBlockStore` uses a `sync.RWMutex` over the store map.
- Each `BlockStore` runs its own 1s eviction goroutine; `CacheBlockStore` runs one 5m
  cleanup goroutine. All stop via their `shutdownCh` on `Teardown`.
- **Single-instance assumption:** the cache is process-local. Running `replicas > 1`
  would split state across pods and break correctness unless the same join key always
  lands on the same instance. This is called out as a TODO (distributed/L2 cache) in
  `handler_join.go` and is why `k8s/deployment.yaml` pins `replicas: 1`.

---

## 6. Open issues / TODOs (from code comments)

- **Monitor spam / scaling:** a status update is sent per inbound message; high volume or
  replicas could flood `processor/monitor` (`handler_join.go:35`).
- **Distribution:** no distributed cache; correctness depends on single-instance or
  consistent key routing (`handler_join.go:59`).
- **Field collisions on merge:** non-key fields are merged by plain name and can clobber
  each other; needs prefixing or remap definitions (`block.go:105`).
- **README vs code default:** README example lists `blockPartMaxJoinCount: 100`; the code
  default when unset is `1` (`handler_join.go:103`).
</content>
</invoke>
