# Docs — Alethic ISM State Online Join

Detailed documentation for this service. Start with the synopsis, then drill in.

| Doc | What's in it |
|-----|--------------|
| [synopsis.md](synopsis.md) | **One-pager.** What it is, how it works, the cache layers, eviction table, and the "are joins one-time?" answer. |
| [architecture.md](architecture.md) | Component map, runtime data flow, **caching / LRU / eviction with ASCII diagrams**, join + compaction pass, merge semantics, concurrency, open issues. |
| [usage.md](usage.md) | Config (env vars, NATS selectors, join-window properties), message shapes, worked example, local run. |
| [build.md](build.md) | Local build, tests, Docker build, Makefile, CI. |
| [deployment.md](deployment.md) | Kubernetes manifest, secrets, the single-replica constraint, deploy flows, ops notes. |

The repository-level [`../README.md`](../README.md) is the high-level overview; these docs
are the implementation-accurate companion (validated against `pkg/correlate` and
`pkg/handler` source).
</content>
