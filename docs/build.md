# Build

## Prerequisites

- **Go 1.24+** (`go.mod` declares `go 1.24.1`; the Docker image uses `golang:1.24`).
- Access to the private module `github.com/quantumwake/alethic-ism-core-go` (the only
  first-party dependency). For local builds this means a Git credential able to fetch it;
  for Docker builds it is injected via `GIT_USERNAME` / `GIT_TOKEN` build args.
- For running (not building): a reachable **NATS** server and **PostgreSQL** (DSN).

## Local build

```bash
# from the repo root
go build -o main .
```

This produces a single static-ish binary `./main`. Entry point is `main.go`, which calls
`handler.Startup(ctx)`, blocks on `SIGINT`/`SIGTERM`, then `handler.Teardown(ctx)`.

### Run tests

```bash
go test ./...
```

Notable tests:

- `pkg/correlate/block_test.go`
  - `TestBlockStore_AddData` — deterministic two-source join with `framework`+`category`
    keys; asserts emitted records (good place to understand expected join output).
  - `TestBlockStore` — a 120-second soak test (two goroutines append at random ticks);
    long-running, prints timing stats. Consider `-run TestBlockStore_AddData -short`
    style filtering for fast feedback.
- `pkg/store/message_store_test.go` — exercises the older prototype types.

> Tip: to develop against a local checkout of the core library, uncomment the `replace`
> directive in `go.mod`:
> `replace github.com/quantumwake/alethic-ism-core-go => ../alethic-ism-core-go`.

## Docker build

The `Dockerfile` is a single-stage `golang:1.24` build that injects Git credentials so it
can pull the private core module.

Directly:

```bash
docker build \
  --build-arg GIT_USERNAME=<user> \
  --build-arg GIT_TOKEN=<token> \
  -t krasaee/alethic-ism-state-online-join:latest .
```

Via the helper script (also tags `:latest` and supports multi-arch / buildpacks):

```bash
./docker_build.sh -i krasaee/alethic-ism-state-online-join:v0.0.1
./docker_build.sh -i krasaee/alethic-ism-state-online-join:v0.0.1 -p linux/arm64
./docker_build.sh -i krasaee/alethic-ism-state-online-join:v0.0.1 -b   # use buildpacks
```

Push:

```bash
./docker_push.sh -i krasaee/alethic-ism-state-online-join:v0.0.1
```

## Makefile targets

```bash
make build     # docker build -t $(IMAGE) .   (IMAGE overridable)
make version   # fetch tags, bump patch, create & push v<x.y.z> git tag
make clean     # docker system prune -f
make help      # list targets
```

> Note: the `swag` target and the default `IMAGE` value
> (`krasaee/alethic-ism-query-api:latest`) are copy-over leftovers from the API template
> — this service exposes no Swagger/HTTP API. Prefer `docker_build.sh` with an explicit
> `-i` image, or override `make build IMAGE=krasaee/alethic-ism-state-online-join:<tag>`.

## CI

GitHub Actions (`.github/workflows/`), both delegating to the shared
`quantumwake/alethic-ism-github-actions` action:

- **`build-main.yml`** — on push/PR to `main`: build & push
  `krasaee/alethic-ism-state-online-join` to Docker Hub.
- **`build-release.yml`** — on tag `v*`: build, push, create a GitHub release, and deploy
  to the `alethic` namespace on the `qwake-k8s-cluster` (DigitalOcean) using
  `k8s/deployment.yaml`.

So the release flow is: `make version` (tag) → `build-release.yml` → live deploy.
</content>
