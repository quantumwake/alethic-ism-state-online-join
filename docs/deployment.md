# Deployment

## Target

- **Platform:** Kubernetes, namespace `alethic`, on `qwake-k8s-cluster` (DigitalOcean).
- **Manifest:** `k8s/deployment.yaml` (Deployment named
  `alethic-ism-state-online-join-deployment`).
- **Image:** `krasaee/alethic-ism-state-online-join` (Docker Hub).

## Critical constraint: single replica

```yaml
spec:
  replicas: 1
```

The join cache is **process-local in-memory** (`CacheBlockStore` → `BlockStore`). Running
more than one replica would shard join state across pods, and events for the same join key
arriving at different pods would silently fail to correlate. Do **not** scale horizontally
without first introducing a distributed/sticky-key cache (tracked as a TODO in
`handler_join.go`). Vertical scaling (CPU/RAM) is fine.

## Configuration & secrets

The deployment wires two Kubernetes secrets:

| Source | Key | Mounted as | Purpose |
|--------|-----|-----------|---------|
| `alethic-ism-state-online-join-secret` | `ROUTING_FILE` | env `ROUTING_FILE` | NATS routing config selector/path |
| `alethic-ism-state-online-join-secret` | `DSN` | env `DSN` | PostgreSQL connection string |
| `alethic-ism-routes-secret` | `.routing.yaml` | file `/app/repo/.routing.yaml` (readOnly) | NATS routing definitions |

Image pull uses the `regcred` imagePullSecret. The container declares `containerPort: 8080`
(`EXPOSE 8080`), but the service does not currently serve HTTP — no Service/Ingress is
defined and none is needed for the NATS worker.

## Deploy manually

`docker_deploy.sh` substitutes the image into the manifest and applies it:

```bash
./docker_deploy.sh -i krasaee/alethic-ism-state-online-join:v0.0.1
# expands <IMAGE> in k8s/deployment.yaml → k8s/deployment-output.yaml → kubectl apply
```

Full manual cycle:

```bash
./docker_build.sh -i krasaee/alethic-ism-state-online-join:v0.0.1
./docker_push.sh  -i krasaee/alethic-ism-state-online-join:v0.0.1
./docker_deploy.sh -i krasaee/alethic-ism-state-online-join:v0.0.1
```

## Deploy via CI (preferred)

Tagging a release triggers the full pipeline (`.github/workflows/build-release.yml`):

```bash
make version     # creates & pushes v<x.y.z>
```

`build-release.yml` then builds, pushes, creates a GitHub release, saves kubeconfig via
`doctl` for `qwake-k8s-cluster`, and rolls out `k8s/deployment.yaml` to namespace
`alethic` (deployment `alethic-ism-state-online-join-deployment`). Pushes to `main`
(`build-main.yml`) build & push the image but do not deploy.

## Runtime dependencies

The pod must be able to reach:

- the **NATS** server referenced in the routing config (default `nats://127.0.0.1:4222`
  in the local `routing.yaml`; in cluster, the secret's routing config), and
- the **PostgreSQL** instance named in `DSN`.

## Operational notes

- **Graceful shutdown:** on `SIGTERM` the process unsubscribes, shuts down the
  `CacheBlockStore` (stopping all per-store eviction goroutines), closes the backend cache,
  and disconnects routes. In-flight in-memory join state is **not** persisted — it is lost
  on restart by design (windowed, ephemeral correlation).
- **Memory footprint** scales with: number of active output states (BlockStores) ×
  distinct join keys (Blocks) × parts per key. Tune `blockCountSoftLimit`,
  `blockWindowTTL`, `blockPartMaxAge`, and `blockPartMaxJoinCount` (per processor
  properties) to bound it. See [architecture.md](architecture.md#3-caching-lru--eviction).
- **Idle reclamation:** BlockStores idle for > 2h are dropped automatically (5m sweep). Both are tunable via `BLOCK_STORE_IDLE_TTL` / `BLOCK_STORE_CLEANUP_INTERVAL`.
- **Cache tuning (env):** `BACKEND_CACHE_TTL` (DB metadata cache, default `30s`), `BLOCK_STORE_IDLE_TTL` (default `2h`), `BLOCK_STORE_CLEANUP_INTERVAL` (default `5m`). Process-global today; per-tenant/project scoping is planned. See [usage.md](usage.md#configuration).
</content>
