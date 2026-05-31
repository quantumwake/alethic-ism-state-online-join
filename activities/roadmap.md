# Activities / Roadmap — Alethic ISM State Online Join

Living plan for evolving the online-join processor from a fixed one-to-one join into a
fully **configurable, per-processor, strategy-driven** join engine.

**Core principle:** behavior is defined **per processor id → its `processor.properties`**.
That JSON holds the configurable knobs (window/eviction) and, later, source configs and the
join strategy. Different join processors = different properties = different behavior, with no
code change.

**Status legend:** ✅ done · 🔜 next · ⏳ planned · ❓ open decision

---

## ✅ P0 — Enable N:M inner join + configurable caches (DONE 2026-05-30)

- `pkg/correlate/block.go`: `blockPartMaxJoinCount <= 0` ⇒ **unlimited** (full N:M cartesian
  within the window). Positive values unchanged (default one-to-one preserved).
- Tests: `TestBlockStore_NMInnerJoin` (4 rows), `TestBlockStore_OneToOneJoin` (2 rows).
- UI (`alethic-ism-ui-enterprise`): fixed dead `provider_id` dispatch (`'online-I testjoin'`
  → `'state-online-join'`); `ProcessorJoinConfig` Max Join Count allows `0` (= unlimited).
- Caches now env-tunable: `BACKEND_CACHE_TTL` (30s), `BLOCK_STORE_IDLE_TTL` (2h),
  `BLOCK_STORE_CLEANUP_INTERVAL` (5m). Global for now; per-tenant/project later.
- Docs: `docs/architecture.md` §3.2.1 worked example + cache notes; usage/deployment/README.

---

## 🟡 P0.5 — Per-message `CacheControlContext` (IMPLEMENTED) + verify

**Core implemented (2026-05-30):** knobs are now resolved per message into a
`correlate.CacheControlContext` (`resolveCacheControl` in `pkg/handler/config.go`), keyed by
the routed-to `psIn.ProcessorID`, and threaded through `AddData` — so a properties edit takes
effect within `BACKEND_CACHE_TTL`, no store rebuild. `blockCountSoftLimit` moved to system
env (`BLOCK_COUNT_SOFT_LIMIT`); new `MAX_RETENTION` (24h) clamps `blockWindowTTL` /
`blockPartMaxAge`. Build + vet + join tests green. Remaining = the verification items below.

The knobs only matter if they actually reach the engine, keyed correctly per processor, and
take effect.

### How it works today (verified by code trace)

```
inbound RouteMessage.RouteID
  → routeBackend.FindRouteWithOutputsByID(RouteID)         → psIn, psOuts[]
  → for each psOut:
       blockStoreCache.GetOrSet(psOut.ID, build):          # built ONCE per output state
         getProcessorJoinConfig(psIn.ProcessorID)          # ← the JOIN processor's id
           → processorBackend.FindProcessorByID(id)        # 30s metadata cache
           → proc.Properties (*data.JSON)
           → parseWindowConfig(props)                      # defaults ⊕ props (JSON unmarshal)
         stateBackend.FindStateConfigKeyDefinitionsByType(psOut.StateID, join_key)
         correlate.NewBlockStore(joinKeys, …knobs…)
```

- Properties are read from **`psIn.ProcessorID`** = the join processor → **per-processor-id**
  config. ✅ (matches the intended model.)
- `parseWindowConfig` starts from `DefaultWindowConfig()` then JSON-unmarshals the processor
  properties over it, so absent fields keep defaults and explicit fields (incl. `0`) win.
- Defaults when unset: `blockWindowTTL=1m`, `blockPartMaxAge=15s`, `blockCountSoftLimit=10`,
  `blockPartMaxJoinCount=1`. `LogBlockStoreConfig` logs the resolved config at store creation.

### ⚠️ Caveat to resolve — config is snapshotted, not live

The `BlockStore` is cached by `psOut.ID` and built **once** (`GetOrSet`). Changing a
processor's properties does **not** re-read into an existing store until it is evicted
(idle `BLOCK_STORE_IDLE_TTL`, default 2h) or the process restarts. So "edit properties in
the UI → new join behavior" is **not** immediate today.

### Action items
- [ ] Add a focused test: properties JSON → `WindowConfig` → `BlockStore` knobs, including
      `blockPartMaxJoinCount: 0` (unlimited) and absent-field defaulting.
- [ ] Manual e2e: set properties on a join processor, publish events from two sources,
      confirm one-to-one (`1`) vs N:M (`0`) output matches expectation; confirm a *second*
      join processor with different properties behaves independently.
- [ ] Confirm the real provider id substring (`state-online-join`) used by the UI dispatch
      matches the seeded provider in the DB.
- [ ] **Config-refresh policy — chosen: `CacheControlContext` injected per message,
      keyed by the routed-to processor id.** A struct carrying the resolved join-window +
      cache-control knobs, built in the handler from `psIn.ProcessorID`'s properties
      (`ResolveCacheControl` = existing `getProcessorJoinConfig` → `parseWindowConfig`
      `defaults ⊕ props`, lifted to run per message, served from the 30s backend cache) and
      threaded through `AddData` so knobs are read per event, not snapshotted at store
      creation. Shape: `{ProcessorID, BlockWindowTTL, BlockPartMaxAge, BlockPartMaxJoinCount
      (0=unlimited), BlockCountSoftLimit, +P1 Strategy, +P2 Sources}`. Details:
      - **Hot (per-call):** `blockPartMaxAge`, `blockPartMaxJoinCount`, `blockWindowTTL`,
        and (P1) `strategy`. `blockWindowTTL` covers eviction for free because `AddData`
        stamps `block.evictionTime`; the loop only compares it.
      - **`blockCountSoftLimit`** is the only knob read solely by the background
        `evictionLoop` → have `AddData` refresh it onto the store (under the existing mutex)
        so the loop sees the latest.
      - **Structural (NOT hot):** `JoinKeyDefinitions` — changing keys invalidates existing
        blocks; still requires a store rebuild. Same caution for strategy changes that alter
        retention semantics.
      - **Resolve once at the edge**, pass the fully-resolved config down (no nil fields), so
        engine levels don't each re-default.
      - ❓ Sub-decision: explicit typed param (`AddData(ctx, cfg, …)` — recommended,
        idiomatic) vs `context.WithValue` (less churn, but hides a required dependency).
      - Payoff: refresh latency = `BACKEND_CACHE_TTL` (config re-resolved per message but
        served from the 30s cache); no separate invalidation machinery. `NewBlockStore` then
        owns only state (blocks/heap/mutex) + structural join keys, not the knobs.
- [ ] Confirm `getProcessorJoinConfig` failure path: on error it falls back to
      `DefaultWindowConfig()` — make sure that's the desired behavior (vs failing the route).

---

## ⏳ P1 — Strategy enum + presets (config model)

- core-go `join.WindowConfig`: add `Strategy *JoinStrategy` (`one_to_one | inner_nm | …`);
  `DefaultWindowConfig()` defaults to `one_to_one` (today's behavior).
- Map strategy → engine behavior (drives `maxJoinCount` + combine fn). core-go already has
  `pkg/windowing` with a `CombineFunc` seam (`JoinCombine`, `MergeCombine`).
- ❓ **Engine consolidation:** adopt core-go `pkg/windowing` as the single engine (recommended)
  vs keep this service's `pkg/correlate`. If consolidating, **mirror the `0 = unlimited`
  sentinel** into `pkg/windowing/block.go`.
- UI: extend `ProcessorJoinConfig` with a Strategy dropdown (presets) + collapsible
  "Advanced" raw knobs; follow the `ProcessorLLMConfig` / edge-function config pattern.
- ❓ **Processor scope:** one join processor with a `strategy` field, or unify the existing
  `state-online-join` / `state-online-merge` / `state-online-cross-join` trio behind it?

## 🟡 P1.5 — Multi-source: minimum-source gate (DONE) + n-way output (future)

**Minimum-source gate — IMPLEMENTED (2026-05-30).** `minSources` (per-processor property,
default & floor **2**) gates emission: no join is emitted for a key until ≥ `minSources`
distinct sources have a *live* part. Resolved in `resolveCacheControl` →
`CacheControlContext.MinSources`; enforced in `BlockStore.AddData` via `distinctLiveSources`
(window keeps sliding while gated so partial data persists). UI exposes a "Minimum Sources"
field. Engine treats `MinSources<=0` as off; the handler always applies the floor of 2.
Tests: `TestBlockStore_MinSources_GateThree`, `TestBlockStore_MinSources_DefaultTwo`.

**Still pairwise output.** With 3+ sources, emission is one merged row **per pair**
(A,B,C → `a·b`, then `c·a`, `c·b`) — never a single combined `{a,b,c}` tuple.
`blockPartMaxJoinCount` is a *per-part total budget across all sources*, so `0`/unlimited is
needed for full pairwise correlation across 3+ sources. Test:
`TestBlockStore_MultiSourcePairwise`.

**Future — n-way combined output:** emit one combined N-tuple once the gate opens, instead of
pairwise rows.
- ❓ Output shape: `joinData` merges two parts today; an n-way merge must combine N parts and
  resolve field collisions (ties into the merge/remap TODO).
- ❓ Relationship between `minSources` (gate) and join *arity* (parts per emitted row).

## ⏳ P2 — `latest_only` + `one_to_many` + source configs

- `latest_only`: replace the part for a source instead of appending.
- `one_to_many`: per-source **role** (dimension = retained/reused vs fact = consumed once);
  asymmetric retention in the engine.
- ❓ **Role/source declaration:** where does "which input is the one vs the many" live —
  route metadata, join-key alias, or an explicit **sources config in `processor.properties`**
  (the "sources configs (tbd)" the team flagged)? Define the properties schema for sources.

## ⏳ P3 — Outer joins (left / right / full)

- Biggest behavioral change: emit **unmatched** rows when the window/age closes.
- Needs a `matched` flag per `BlockPart` + an **emit-on-eviction** callback threaded from the
  service (`PublishStateSync`) into the eviction loop (which today only deletes).
- ❓ Late-data policy after a row is emitted as unmatched on window close.

## ⏳ P4 — Window types (optional)

- Tumbling / session windows in addition to the current sliding window.

---

## Cross-cutting / tech debt

- **Per-tenant/project cache scoping** — the env cache knobs are global today; scope per
  tenant/project later (the reason these are not yet in per-processor UI properties).
- **Defaults drift** — Go `DefaultWindowConfig()` vs UI `defaultConfig` are hand-duplicated;
  consider a single source / defaults endpoint.
- **Monitor spam** — one status update per inbound message; flood risk at volume/replicas
  (`handler_join.go:35`).
- **Distributed cache** — engine is process-local; correctness depends on `replicas: 1` /
  sticky-key routing (`handler_join.go:59`). Needed before horizontal scale.
- **Merge field collisions** — non-key fields merge by plain name and can clobber
  (`block.go:105`); needs prefix/remap.

## Properties schema (target)

```jsonc
{
  // window / eviction knobs (P0 — live)
  "blockWindowTTL": "1m",
  "blockPartMaxAge": "15s",
  "blockCountSoftLimit": 10,
  "blockPartMaxJoinCount": 1,        // 0 = unlimited (N:M)

  // strategy (P1)
  "strategy": "one_to_one",          // one_to_one | inner_nm | one_to_many | latest_only | left_outer | right_outer | full_outer

  // sources config (P2 — tbd)
  "sources": { /* roles / which input is one vs many, etc. */ }
}
```
</content>
