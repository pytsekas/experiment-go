<!-- Generated: 2026-09-13 | Updated: 2026-09-14 -->

# experiment-go

## Purpose

A small Go REST microservice used as a starter and as a playground for running the same
image on docker-compose, Cloud Run and GKE, then load-testing it. It exposes a `tasks`
CRUD resource plus health probes. Gin handles HTTP, pgx talks to Postgres, migrations
are embedded in the binary and applied with golang-migrate as a library, logging is
`log/slog`, shutdown is graceful, and the whole developer workflow is driven from the
`Makefile`. Cloud infrastructure is Terraform-managed with `make` wrappers.

The same binary also runs a second role: `internal/consumption` streams an ENTSO-E ESMP
energy-consumption document from a Pub/Sub push into BigQuery, with a GCS quarantine for
permanently failed payloads. It shares the image and most of the code with the API; only
configuration (`ENABLE_INGEST_ENDPOINT` and friends) tells the two apart.

This is a **Go project**. The Spring Boot / Java rules in the global `~/.claude/CLAUDE.md`
do not apply here.

## Quick facts

| Item | Value |
| --- | --- |
| Module | `github.com/pytsekas/experiment-go` |
| Go version | `go 1.25.0` in `go.mod`; local toolchain is newer and works |
| Entry point | `cmd/api/main.go` (`api` serves, `api migrate ...` migrates) |
| HTTP port | `8080` (env `HTTP_PORT`) |
| Database | Postgres 17, driver `jackc/pgx/v5`, pool via `pgxpool` |
| Migrations | `migrations/*.sql`, embedded with `//go:embed`, run by `internal/migrator` |
| Lint | `golangci-lint` v2 config in `.golangci.yml`, binary installed into `bin/` |
| Workflows | `.github/workflows/ci.yml` (tidy check, gofmt, vet, lint, race tests, Docker build) and `release.yml` (`v*` tags: cross-compiled binaries + multi-arch image + GitHub Release) |
| Version control | **No `.git` directory as of 2026-09-13.** `VERSION` falls back to `dev`. |
| Cloud project | GCP: `mikroteenus` (gcloud config, `deploy/terraform/gcp/terraform.tfvars`). AWS: no credentials on this machine |
| Region / zone | `europe-north1` / `europe-north1-b` |

## Key files

| File | Description |
| --- | --- |
| `Makefile` | Single source of truth for every command: build, test, lint, compose, migrations, GCP, k8s, k6. `make` alone prints help. |
| `README.md` | Human-facing overview: endpoints, config table, Docker and migration usage. Keep in sync when behaviour changes. |
| `go.mod` / `go.sum` | Nine direct deps: gin, golang-migrate, google/uuid, pgx, plus the ingest role's cloud.google.com/go/bigquery, cloud.google.com/go/storage, google.golang.org/api, google.golang.org/grpc, google.golang.org/protobuf. |
| `Dockerfile` | Two-stage build. `golang:1.25-alpine` builder, `alpine:3.22` runtime, non-root uid 10001, `HEALTHCHECK` on `/healthz`, `MIGRATE_ON_START=false` by default. |
| `docker-compose.yml` | `db` (postgres:17-alpine, healthcheck, `pgdata` volume) and `api` (waits for db, migrates on boot). |
| `.golangci.yml` | Standard linters plus bodyclose, errorlint, gocritic, gosec, misspell, nilerr, noctx, revive, unconvert, unparam. goimports local prefix is the module path. |
| `.env` / `.env.example` | Local config, loaded by `make`. `.env` is gitignored and holds a DB password. Never print or commit it. |
| `.dockerignore` | Excludes `deploy/`, `loadtest/`, `Makefile`, README and env files from the image context. |
| `make/gcp.mk`, `make/aws.mk`, `make/k8s.mk` | Cloud and kubectl targets included by the Makefile. `k8s.mk` is switched with `CLOUD=gcp\|aws`. |

## Subdirectories

| Directory | Purpose |
| --- | --- |
| `cmd/api/` | `main.go` wires config, logger, pool, service and router; `migrate.go` implements `api migrate up|down|version|force`. |
| `internal/config/` | 12-factor env loading with defaults and validation. Fails fast when no DSN, bad port, bad log format, or `DB_MIN_CONNS > DB_MAX_CONNS`. |
| `internal/logger/` | `slog` handler factory: `json` or `text`, level from env. |
| `internal/database/` | `NewPool` builds a `pgxpool.Pool` and pings it. Caller owns `Close`. |
| `internal/migrator/` | Wraps golang-migrate over the embedded SQL. Rewrites `postgres://` to `pgx5://`. Up is idempotent and advisory-locked. |
| `internal/task/` | Example domain: `Task` model, `Service` interface and impl (business rules), `Repository` interface, `PostgresRepository` (SQL). Sentinel errors `ErrNotFound`, `ErrInvalidTitle`. |
| `internal/httpapi/` | Gin router, middleware (request ID, structured request log, panic recovery), handlers (`tasks.go`, `health.go`, `burn.go`), response envelope, `http.Server` lifecycle. |
| `migrations/` | `000001_create_tasks_table.{up,down}.sql` plus `embed.go`. New pairs via `make migrate-new name=x`. |
| `deploy/k8s/` | Kustomize: `base/` cloud-neutral manifests, `overlays/gke/` adds the Cloud SQL proxy sidecar and GKE LB, `overlays/eks/` sets the RDS host, `sslmode=require` and an NLB. Placeholders `IMAGE_PLACEHOLDER`, `SQL_INSTANCE_PLACEHOLDER`, `RDS_HOST_PLACEHOLDER` are filled by `make k8s-render`. |
| `deploy/terraform/` | `gcp/` and `aws/` roots with an identical contract (`README.md` there). Same-named modules `registry`, `database`, `serverless`, `kubernetes` (+ `network` on AWS). Toggles `create_db`, `image`, `create_k8s` via `*.auto.tfvars`. Passwords generated by Terraform into Secret Manager / Secrets Manager. Local state, gitignored. GCP only, so far: `create_ingest` adds a second `serverless` instance (`experiment-go-ingest`), the `warehouse` module (BigQuery dataset/table/view) and the `messaging` module (Pub/Sub push subscription + DLQ) — see `internal/consumption/`. |
| `deploy/README.md` | Phase-by-phase GCP walkthrough with costs, teardown and a gotchas list. `deploy/terraform/README.md` explains the contract; `deploy/terraform/gcp/README.md` and `aws/README.md` hold the toggle files, import commands and troubleshooting. |
| `loadtest/k6/` | `smoke.js` (deploy check, 5 iterations), `load.js` (constant arrival rate, read-heavy), `stress.js` (ramping load on the burn endpoint), `job.yaml` to run k6 in-cluster. |
| `bin/` | Build output and the locally installed `golangci-lint`. Gitignored. |
| `_to_delete/` | Old scaffold tarballs. Ignore; do not reference or extend. |
| `.omc/` | oh-my-claudecode runtime state. Not project code. |

## Architecture

Request flow: Gin handler (`internal/httpapi`) → `task.Service` → `task.Repository` →
Postgres. The service owns business rules (trims titles, rejects blank ones with
`ErrInvalidTitle` before any SQL). The repository owns SQL and maps `pgx.ErrNoRows` to
`ErrNotFound`. Handlers map sentinel errors to 404/400 and everything else to a logged 500.

Endpoints:

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/healthz` | Liveness. Never touches the DB. Returns `{"status":"ok","version":...}`. |
| GET | `/readyz` | Readiness. Pings Postgres with a 2s timeout, 503 when down. |
| GET | `/api/v1/tasks?limit=20&offset=0` | `limit` 1..100, `offset` >= 0. Newest first. |
| POST | `/api/v1/tasks` | Body `{"title": "..."}`, title 1..200 chars. 201 with `Location` header. |
| GET / PUT / DELETE | `/api/v1/tasks/:id` | `id` must be a positive integer. PUT body `{"title","done"}`. DELETE returns 204. |
| GET | `/api/v1/burn?ms=N` | CPU sink for autoscaling tests, capped at 2s. Only when `ENABLE_BURN_ENDPOINT=true`. |

Response envelopes: success is `{"data": ...}`, errors are `{"error": "..."}`. Every
response carries `X-Request-ID` (echoed from the request or generated), and the same ID
appears in the request log line. Unknown routes return 404 JSON, wrong methods 405 JSON.

Startup: load config → build logger → if `MIGRATE_ON_START` apply migrations → open pool
and ping → build router → serve until SIGINT/SIGTERM → drain within
`HTTP_SHUTDOWN_TIMEOUT`. Any startup failure exits non-zero with `fatal: ...` on stderr.

## Configuration

All from environment. Defaults in `internal/config/config.go`, documented in `README.md`.

| Variable | Default | Notes |
| --- | --- | --- |
| `APP_ENV` | `development` | `production` puts Gin in release mode |
| `HTTP_HOST` / `HTTP_PORT` | `0.0.0.0` / `8080` | |
| `HTTP_READ_TIMEOUT` / `HTTP_WRITE_TIMEOUT` / `HTTP_IDLE_TIMEOUT` | `10s` / `15s` / `60s` | |
| `HTTP_SHUTDOWN_TIMEOUT` | `15s` | Drain window |
| `LOG_LEVEL` / `LOG_FORMAT` | `info` / `json` | `text` for local reading |
| `DATABASE_URL` | none | Full DSN, wins over `POSTGRES_*` |
| `POSTGRES_HOST/PORT/USER/PASSWORD/DB/SSLMODE` | | Composed into a DSN when `DATABASE_URL` is unset |
| `DB_MAX_CONNS` / `DB_MIN_CONNS` | `10` / `1` | Per process. Watch `DB_MAX_CONNS × replicas` against the DB limit |
| `DB_MAX_CONN_LIFETIME` / `DB_CONNECT_TIMEOUT` | `1h` / `10s` | |
| `MIGRATE_ON_START` | `false` | compose and `make run` set `true`; k8s uses a Job instead |
| `ENABLE_BURN_ENDPOINT` | `false` | k8s ConfigMap and Cloud Run currently set `true` for experiments |

Compose defaults the DB user/password/db to `app` / `app` / `experiment`. The Makefile has
its own fallback credentials and `.env` overrides both.

## Commands

Local development (needs Docker and `make`, Go toolchain for `make run`):

```bash
make init        # create .env from .env.example, go mod download
make up          # build image, start db + api (api migrates itself)
make run         # run API on host against compose db, text logs, debug level
make smoke       # curl healthz, readyz, create + list tasks
make logs | ps | psql | sh
make down        # stop; make down-hard also deletes the db volume
```

Quality gate. `make check` is what CI runs and must pass before claiming work is done:

```bash
make fmt-check vet lint test   # or simply: make check
make test-race                  # race detector, -count=1
make cover                      # coverage.html
make lint-fix                   # golangci-lint --fix
```

Build and image:

```bash
make build                      # static binary to bin/api, version via -ldflags
make docker-build               # experiment-go:<VERSION> and :latest
make docker-run | docker-run-net
```

Migrations (DSN filled from `.env`, runs `go run ./cmd/api migrate ...`):

```bash
make migrate-new name=add_task_owner   # creates 00000N_add_task_owner.{up,down}.sql
make migrate-up | migrate-down n=2 | migrate-version | migrate-force version=1
make migrate-docker cmd=up             # same through the container image
```

Google Cloud (Terraform wrappers, these cost money):

```bash
make gcp-config                 # show resolved PROJECT_ID, REGION, IMAGE, SQL_CONN
make gcp-bootstrap              # APIs + Artifact Registry (always-on base)
make sql-create                 # Cloud SQL db-f1-micro, writes db.auto.tfvars
make image-push                 # buildx linux/amd64 to Artifact Registry
make run-deploy && make run-smoke
make gke-create                 # zonal cluster, spot pool, WI binding, writes k8s.auto.tfvars
make k8s-deploy                 # manifests + migration Job (k8s-migrate re-runs only the Job)
make loadtest-stress BASE_URL=$(make -s k8s-url)
make teardown-all               # removes toggle files, one apply destroys Run + GKE + SQL
make tf-plan                    # preview any wrapper with the Makefile's vars
```

```bash
make aws-config | aws-bootstrap | aws-image-push | aws-sql-create | aws-ecs-deploy | aws-eks-create
make k8s-deploy CLOUD=aws       # same k8s targets against EKS
make aws-teardown-all
```

## For AI agents

### Working in this directory

- Read `Makefile` before inventing a command. Almost everything has a target already.
- Keep `README.md` tables in sync when you add an endpoint, env var or make target.
- Follow the existing layering. New resources: copy the `internal/task` package shape
  (model + `Service` interface + `Repository` interface + Postgres impl), add a handler
  file in `internal/httpapi`, register routes in `internal/httpapi/router.go`.
- Business rules go in the service, SQL in the repository, HTTP mapping in the handler.
  Do not put validation that belongs to the domain into handlers or SQL.
- Schema changes are SQL migration pairs only. Use `make migrate-new`, write both `up` and
  `down`, keep the six-digit numbering. `internal/migrator/migrator_test.go` fails if a
  version lacks either file.
- Config: add new settings to `config.Load` with a default and, if needed, to `validate`.
  Then document in `README.md`, `.env.example`, `deploy/k8s/base/configmap.yaml`,
  `deploy/terraform/gcp/locals.tf` and `deploy/terraform/aws/locals.tf` (the `app_env` maps)
  where relevant.
- `/healthz` must never touch the database. `/readyz` is the one that pings.
- Never edit files in `_to_delete/`. Never commit `.env`, `deploy/terraform/{gcp,aws}/terraform.tfstate*`,
  `deploy/terraform/{gcp,aws}/*.auto.tfvars` or `deploy/terraform/{gcp,aws}/terraform.tfvars`. They hold
  credentials.
- A local hook blocks shell commands whose text mentions `.env`, `PASSWORD`, `SECRET`,
  `TOKEN` and similar. Read env var names from `config.go` or `README.md` instead of
  catting env files.
- Cloud targets create billable resources and teardown is destructive. Confirm with the
  user before running any `make sql-*`, `gke-*`, `run-deploy`, `k8s-*`, `teardown-*` or `aws-*`
  (all `aws-*` targets except `aws-config` and `aws-tf-plan` create or destroy billable resources).
- Apple Silicon: always use `make image-push` (buildx amd64) for cloud images. A plain
  `docker build` produces arm64 images that crash on GKE and Cloud Run with `exec format error`.
- Terraform changes go into the module that owns the resource; the roots only wire modules. Keep the variable and output names identical in gcp/ and aws/ (parity check: `diff <(grep '^variable' deploy/terraform/gcp/variables.tf | sort) <(grep '^variable' deploy/terraform/aws/variables.tf | sort)` should list only `project_id`, `zone`, `registry_repo` and `vpc_cidr`).
- Never write a password into a tfvars file. Toggle files hold only `create_db`, `image`, `create_k8s`.

### Testing requirements

- `go test ./...` and `make check` must pass. As of 2026-09-13 all four test packages pass
  (`config`, `httpapi`, `migrator`, `task`). `cmd/api`, `database`, `logger` and
  `migrations` have no tests.
- Tests need no database. Handlers are tested against a `stubService`, the service
  against a `stubRepo`. Both stubs fail the test on any method the test did not configure,
  so a request that should be rejected early cannot pass by accident. Keep that property.
- Style: table-driven, `t.Parallel()`, `httptest.NewRecorder`, `gin.SetMode(gin.TestMode)`
  in `TestMain`, `t.Setenv` for config, `io.Discard` loggers. Match it.
- New repository SQL has no automated test today. Verify manually with `make up` and
  `make smoke`, or `make psql`, and say so in your report.
- Linter exclusions: `gosec`, `unparam` and `govet` are relaxed for `_test.go` files.

### Common patterns

- Errors: wrap with `fmt.Errorf("context: %w", err)`, compare with `errors.Is`, define
  package-level sentinel errors. Early `return` with a blank line before it (gocritic style).
- Logging: `slog` only, always key/value attrs (`slog.String`, `slog.Int64`, ...). Never
  log DSNs, passwords or request bodies. Request logs are emitted once per request by the
  middleware; handlers only log unexpected errors.
- Interfaces are satisfied explicitly: `var _ Service = (*service)(nil)`.
- Constructors accept a `*slog.Logger` and fall back to `slog.Default()` when nil.
- Gin binding tags do transport validation (`binding:"required,min=1,max=200"`), the
  service does domain validation, the DB has a matching `CHECK` constraint as last resort.
- Imports grouped stdlib / third-party / module, enforced by goimports with local prefix.
- `int32` conversions from config carry `//nolint:gosec` with a reason.

## Deployment state and gotchas

- GCP Terraform state (`deploy/terraform/gcp/terraform.tfstate`) tracks only the base
  layer: enabled APIs and the Artifact Registry repo. No `*.auto.tfvars` files exist in
  either root, so nothing billable is deployed on either cloud. AWS has never been applied.
- Cloud Run requires Cloud SQL (a Terraform precondition). Tear down Run before SQL, or
  use `make teardown-all`.
- k8s: migrations run as a Job (`deploy/k8s/base/migrate-job.yaml`), which `make k8s-deploy`
  runs and waits for before the rollout. The proxy is a native sidecar (`initContainers`
  with `restartPolicy: Always`) so the Job can complete.
- k8s: no CPU limit on the API container by design, so latency measurements are not
  throttled. HPA targets 60% of the 100m request.
- `db-f1-micro` allows roughly 25 connections. `DB_MAX_CONNS=5` × 10 replicas exceeds it.
- Full gotcha list lives in `deploy/README.md` under "Gotchas, collected".

## Dependencies

### Internal

`cmd/api` → `internal/config`, `internal/logger`, `internal/database`, `internal/migrator`,
`internal/task`, `internal/httpapi`. `internal/httpapi` depends on `internal/task` and
`internal/config`. `internal/migrator` depends on `migrations`. Nothing imports `cmd/api`.

### External

- `github.com/gin-gonic/gin` v1.12 — HTTP router and binding
- `github.com/jackc/pgx/v5` v5.10 — Postgres driver and pool
- `github.com/golang-migrate/migrate/v4` v4.19 — migrations, `pgx/v5` driver and `iofs` source
- `github.com/google/uuid` — request IDs
- `cloud.google.com/go/bigquery` — BigQuery Storage Write API client (`internal/consumption/bqsink`)
- `cloud.google.com/go/storage` — GCS client for the quarantine writer (`internal/consumption/quarantine`)
- `google.golang.org/api`, `google.golang.org/grpc`, `google.golang.org/protobuf` — transitive requirements of the BigQuery managed-writer client, also imported directly for error classification and row encoding
- Tooling: golangci-lint v2, Docker with buildx, Terraform >= 1.5 with google ~> 6.0 and aws ~> 6.0, gcloud (with the `bq` component, for `make ingest-smoke` / `make bq-readings`), kubectl (kustomize built in), aws CLI (not installed locally as of 2026-09-14), k6 (not installed locally), cloud-sql-proxy

<!-- MANUAL: Any manually added notes below this line are preserved on regeneration -->
