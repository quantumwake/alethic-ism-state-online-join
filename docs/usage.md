# Usage

This service is **not** an HTTP API and has no CLI flags. It is a long-running NATS worker:
it subscribes to a join subject, joins events in memory, and publishes results to other
NATS subjects. You drive it by publishing messages and by configuring the join window via
processor properties in Postgres.

## Configuration

### Environment variables

| Variable | Required | Used by | Notes |
|----------|----------|---------|-------|
| `DSN` | yes | `pkg/handler/setup.go` | PostgreSQL connection string for the route/processor/state DAOs (`alethic-ism-core-go`). |
| `ROUTING_FILE` | yes (deploy) | core-go `routing/nats` | Path/contents of the NATS routing config (`.routing.yaml`). In k8s it is mounted from the `alethic-ism-routes-secret`. Locally, `routing.yaml` is a symlink to `../routing-nats.yaml`. |
| `BACKEND_CACHE_TTL` | no | `setup.go` | TTL for the **local DB metadata cache** (route/processor/state lookups). Go duration (`30s`, `1m`, …). Default `30s`. |
| `BLOCK_STORE_IDLE_TTL` | no | `setup.go` | Idle threshold after which a per-output-state **BlockStore** (join cache) is dropped. Default `2h`. |
| `BLOCK_STORE_CLEANUP_INTERVAL` | no | `setup.go` | How often idle BlockStores are swept. Default `5m`. |
| `BLOCK_COUNT_SOFT_LIMIT` | no | `setup.go` | **Core limit:** max blocks per store before sliding-window eviction kicks in (memory guard). Default `10`. Operator-controlled, not per-processor. |
| `MAX_RETENTION` | no | `setup.go` | **Core limit:** ceiling for any per-processor retention knob (`blockWindowTTL`, `blockPartMaxAge`); larger values are clamped with a warning. Default `24h`. |

> These cache settings are process-global env vars for now (the caches are intentionally
> process-local — there is no central cache yet). `BLOCK_COUNT_SOFT_LIMIT` and
> `MAX_RETENTION` are **core limits** that bound what a processor may request; per-tenant /
> per-project scoping is a future concern.

There are **no** command-line arguments — `main.go` only wires startup/teardown and waits
on a termination signal.

### NATS routes (selectors)

Defined in `pkg/handler/setup.go` and resolved through the routing config:

| Constant | Selector | Direction | Payload |
|----------|----------|-----------|---------|
| `SelectorSubscriber` | `data/transformers/mixer/state-online-join-1.0` | **in** (subscribe) | `models.RouteMessage` |
| `SelectorStoreSync` | `processor/state/sync` | **out** (joined results) | `models.RouteMessage` |
| `SelectorMonitor` | `processor/monitor` | **out** (status) | `models.MonitorMessage` |

In `routing.yaml`, the inbound selector maps to subject `processor.transform.state.online.join`
on queue `processor_transform_state_online_join`.

### Join window configuration (per processor, in Postgres)

On **every message**, the handler resolves the join config from the **routed-to processor's**
`Properties` JSON (overlaid on `join.DefaultWindowConfig()`) into a `CacheControlContext` and
threads it into the engine (`pkg/handler/config.go` → `resolveCacheControl`). Because it is
re-resolved per message (served from the `BACKEND_CACHE_TTL` metadata cache), **editing a
processor's properties takes effect within `BACKEND_CACHE_TTL`** — no restart or store
rebuild needed. Retention values are clamped by the system `MAX_RETENTION` ceiling.

These are the **per-processor controllable** knobs (defaults applied when absent):

| Property | Default | Meaning |
|----------|---------|---------|
| `blockWindowTTL` | `"1m"` | Sliding window per join-key block; reset on every event. Keeps a key "alive". Capped by `MAX_RETENTION`. |
| `blockPartMaxAge` | `"15s"` | Absolute lifetime of a single event part from arrival. Capped by `MAX_RETENTION`. |
| `blockPartMaxJoinCount` | `1` | Max joins a part may take part in before it's dropped. `1` ⇒ one-time join; `0` ⇒ unlimited (full **N:M** inner join within the window). See the worked example in [architecture.md](architecture.md#321-worked-example--nm-inner-join-cartesian-within-the-window). |
| `minSources` | `2` | Minimum distinct sources that must contribute to a key before any join emits (a completeness gate). **Floor 2** — values below 2 are raised to 2. Raise to require more sources present on the key first (emission stays pairwise once the gate opens). |

> `blockCountSoftLimit` is **no longer** a per-processor property — it is a core cache limit
> set via `BLOCK_COUNT_SOFT_LIMIT` (see env vars above).

Example processor `Properties`:

```json
{
  "blockWindowTTL": "1m",
  "blockPartMaxAge": "15s",
  "blockPartMaxJoinCount": 1,
  "minSources": 2
}
```

> Joins are **pairwise** across sources: with 3+ sources an event joins every *other*
> source's parts and emits one row per pair (no single combined N-tuple — that's the n-way
> join on the roadmap). `blockPartMaxJoinCount` is a per-part *total* budget across all
> sources, so use `0` (unlimited) for full correlation across 3+ sources.

> Durations are Go `time.ParseDuration` strings (`"500ms"`, `"15s"`, `"2m"`, …). Unparseable
> values fall back to the default and log a warning (`parseDurationWithDefault`).
>
> ⚠️ The README's example shows `blockPartMaxJoinCount: 100`; the **actual** default when
> the property is missing is `1`. Set it explicitly if you want fan-out / repeated joins.

## What the join needs to work

For a meaningful join you need, in the ISM control plane:

1. An **output processor-state** whose state config has columns of type
   `DefinitionStateJoinKey` — these define the join key. Without any, `AddData` fails with
   *"no join keys defined"*.
2. **Two or more input routes** feeding that output state. The engine joins only across
   **different** input `RouteID`s (same-source events never join each other).
3. Events whose payloads contain every join-key column (a missing column ⇒
   *"field not present in event"* and that event is rejected).

## Message shapes

Inbound (`models.RouteMessage`) on `data/transformers/mixer/state-online-join-1.0`:

```jsonc
{
  "type": "query_state_route",
  "route_id": "<input-route-id>",          // becomes the join "source"
  "query_state": [                          // batch of events
    { "framework": "Utilitarian", "category": "class_a", "scenario": "…" }
  ]
}
```

Outbound joined record (`processor/state/sync`), one `RouteMessage` per pairing, e.g.:

```jsonc
{
  "type": "query_state_route",
  "route_id": "<output-state-id>",
  "query_state": [
    {
      "framework": "Utilitarian",          // join-key fields
      "category": "class_a",
      "scenario": "…",                      // non-key fields merged from both sources
      "framework_definitions": "…",
      "joinedAt": "2026-05-30T14:51:00Z"
    }
  ]
}
```

Outbound status (`processor/monitor`): `Running` on receipt, then `Completed` (or `Failed`
on error/panic) per inbound message.

## Worked example (from the tests)

`pkg/correlate/block_test.go::TestBlockStore_AddData` is the clearest behavioural spec:

- Join keys: `framework`, `category`.
- Two `source1` events are added first → no output (only one source present).
- Each `source2` event then joins the matching `source1` part → exactly one emit per
  match (`blockPartMaxJoinCount = 1`), carrying the `id`/`scenario` from source2 plus the
  `framework_definitions` from source1.

## Local run

```bash
# 1. Start NATS + Postgres and point routing.yaml at your NATS server
# 2. Provide DSN
export DSN='postgres://user:pass@localhost:5432/alethic?sslmode=disable'
go run .          # or ./main after `go build`
# 3. Publish a RouteMessage to data/transformers/mixer/state-online-join-1.0
#    and observe joined output on processor/state/sync
```

The process logs each new block, part addition, join, skip (expired/maxed), and eviction
(see `pkg/correlate/logging.go` and `pkg/handler/logging.go`), which makes the window
behaviour easy to follow at runtime.
</content>
