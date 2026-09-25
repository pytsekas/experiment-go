# experiment-go

A Go REST microservice starter: [Gin](https://gin-gonic.com/) for HTTP, Go modules for
dependencies, pgx for Postgres with migrations embedded in the binary via
[golang-migrate](https://github.com/golang-migrate/migrate) as a library, structured
logging with `log/slog`, graceful shutdown, and a Docker / docker-compose setup driven
entirely from the `Makefile`.

The same image also has a deployment path to Google Cloud and AWS (Terraform for the
infrastructure, kustomize for Kubernetes, k6 for load tests) and a release workflow that
turns a `v*` tag into cross-compiled binaries and a multi-arch image on GHCR.

## Quick start

Everything runs in containers — you only need Docker and `make`. Go 1.25+ on the host is
optional, and only needed for `make run`, `make build` and `make test`.

```bash
make init   # creates .env from .env.example, downloads modules
make up     # builds the image, starts Postgres, starts the API (which migrates itself)
make smoke  # sanity-check the running endpoints
make logs   # follow API logs
make down   # stop the stack
```

The API is on <http://localhost:8080>.

Working on the code without rebuilding the image each time:

```bash
make up      # start the stack (Postgres is published on localhost:5432)
make run     # run the API on the host with text logs and debug level
```

## Endpoints

| Method | Path                 | Description                                   |
| ------ | -------------------- | --------------------------------------------- |
| GET    | `/healthz`           | Liveness — never touches the database         |
| GET    | `/readyz`            | Readiness — pings Postgres, 503 when it's out |
| GET    | `/api/v1/tasks`      | List tasks, newest first (`?limit=20&offset=0`, max 100) |
| POST   | `/api/v1/tasks`      | Create a task — 201                           |
| GET    | `/api/v1/tasks/:id`  | Fetch one task                                |
| PUT    | `/api/v1/tasks/:id`  | Replace a task (`title` and `done`)           |
| DELETE | `/api/v1/tasks/:id`  | Delete a task — 204                           |
| GET    | `/api/v1/burn?ms=N`  | Burns CPU for N ms — only when `ENABLE_BURN_ENDPOINT=true` |
| POST   | `/internal/pubsub/consumption` | Pub/Sub push: parse a consumption document into BigQuery — only when `ENABLE_INGEST_ENDPOINT=true` |

```bash
curl -s localhost:8080/api/v1/tasks \
  -H 'Content-Type: application/json' \
  -d '{"title":"ship the microservice"}'
```

```json
{"data":{"id":1,"title":"ship the microservice","done":false,
         "created_at":"2026-09-19T08:12:03Z","updated_at":"2026-09-19T08:12:03Z"}}
```

Successful `/api/v1` responses are wrapped in `{"data": ...}` and errors in
`{"error": "..."}` — a blank or over-long title is a 400, an unknown id a 404, and an
unparsable id a 400. The health probes are outside the envelope: `/healthz` returns
`{"status":"ok","version":"..."}` and `/readyz` returns `{"status":"ready"}` or a 503
naming the dependency that is down. Every response carries an `X-Request-ID` header
(echoed from the request when present) which also appears in the log line for that
request.

## Layout

```
cmd/api/                  entrypoint: config, wiring, signal handling
internal/config/          environment-based configuration + validation
internal/logger/          slog setup (json|text, level from env)
internal/database/        pgx connection pool
internal/migrator/        golang-migrate as a library over the embedded SQL
internal/task/            example domain: model, Service (business rules), Repository contract, Postgres impl
internal/consumption/     ingest domain: readings, parser registry, service, BigQuery sink, quarantine
internal/httpapi/         router, middleware, handlers, http.Server lifecycle
migrations/               SQL files + embed.go (//go:embed *.sql)
deploy/terraform/         Terraform: gcp/ and aws/ roots with the same contract (see deploy/terraform/README.md)
deploy/k8s/               kustomize base + overlays/gke + overlays/eks
make/                     gcp.mk, aws.mk, k8s.mk included by the Makefile
loadtest/k6/              k6 scripts (smoke, load, stress) + a Job to run them in-cluster
.github/workflows/        ci.yml (every push and PR) and release.yml (v* tags)
.claude/skills/           rest-endpoint: the house rules for changing the HTTP surface
docs/                     design notes and plans kept alongside the code
```

Requests flow handler → `task.Service` → `task.Repository` → Postgres. The service owns
business rules (it trims titles and rejects blank ones with a 400 before any SQL runs),
the repository owns SQL. Both are interfaces, so the handlers are tested with a stub
service (`internal/httpapi/tasks_test.go`) and the service with a stub repository
(`internal/task/service_test.go`), neither needing a database. Add a new resource by
copying the `task` package and registering its handlers in `internal/httpapi/router.go`.

Two documents go deeper than this README: `AGENTS.md` is the working brief for the repo
(conventions, invariants, where each thing lives), and `.claude/skills/rest-endpoint/`
walks through adding an endpoint or a whole resource — which layer owns what, how errors
map to status codes, and which tests and doc tables have to move with the change.

## Make targets

Run `make` for the full list. The ones you'll use most:

| Target                        | What it does                                        |
| ----------------------------- | --------------------------------------------------- |
| `make init`                   | Create `.env`, download modules                     |
| `make up` / `make down`       | Start / stop the compose stack                      |
| `make restart`                | Rebuild and restart only the API container          |
| `make logs` / `make ps`       | Follow logs / show status                           |
| `make run`                    | Run the API on the host against the compose DB      |
| `make build`                  | Static binary into `bin/api`                        |
| `make test` / `make test-race`| Tests, with or without the race detector            |
| `make cover`                  | Coverage report (`coverage.html`)                   |
| `make lint` / `make lint-fix` | golangci-lint (installed into `bin/` on first use)  |
| `make check`                  | fmt-check + vet + lint + test — same as CI          |
| `make docker-build`           | Build the production image                          |
| `make migrate-up`             | Apply pending migrations                            |
| `make migrate-down`           | Roll back the last migration (`n=2`, `n=all`)       |
| `make migrate-new name=x`     | Create `migrations/00000N_x.{up,down}.sql`          |
| `make migrate-docker cmd=up`  | Same, but through the built container image         |
| `make psql`                   | psql shell on the compose database                  |
| `make smoke`                  | curl the health and task endpoints                  |
| `make gcp-config` / `make aws-config` | Show resolved cloud settings                 |
| `make k8s-deploy CLOUD=gcp\|aws`  | Render the overlay and apply it to the right cluster |

## Configuration

All configuration comes from the environment (see `.env.example`). `make up` and
`make run` load `.env` automatically.

| Variable                 | Default       | Notes                                     |
| ------------------------ | ------------- | ----------------------------------------- |
| `APP_ENV`                | `development` | `production` puts Gin in release mode      |
| `HTTP_HOST`              | `0.0.0.0`     | Listen address                             |
| `HTTP_PORT`              | `8080`        |                                            |
| `HTTP_READ_TIMEOUT`      | `10s`         |                                            |
| `HTTP_WRITE_TIMEOUT`     | `15s`         |                                            |
| `HTTP_IDLE_TIMEOUT`      | `60s`         |                                            |
| `HTTP_SHUTDOWN_TIMEOUT`  | `15s`         | Drain window for in-flight requests        |
| `LOG_LEVEL`              | `info`        | `debug` / `info` / `warn` / `error`        |
| `LOG_FORMAT`             | `json`        | `json` for prod, `text` for local reading  |
| `DATABASE_URL`           | —             | Full DSN; wins over the `POSTGRES_*` vars  |
| `POSTGRES_HOST/PORT/USER/PASSWORD/DB` | — | Used to compose the DSN               |
| `POSTGRES_SSLMODE`       | `disable`     | Set it for anything but a local database   |
| `DB_MAX_CONNS` / `DB_MIN_CONNS` | `10` / `1` | pgx pool bounds                        |
| `DB_MAX_CONN_LIFETIME`   | `1h`          | Recycle connections after this long        |
| `DB_CONNECT_TIMEOUT`     | `10s`         | Startup ping budget                        |
| `MIGRATE_ON_START`       | `false`       | Apply migrations on boot; compose sets it  |
| `ENABLE_BURN_ENDPOINT`   | `false`       | Exposes the CPU-burn endpoint for load tests |
| `ENABLE_INGEST_ENDPOINT` | `false`       | Registers the Pub/Sub push route; on for the ingest service only |
| `PUBSUB_AUDIENCE`        | —             | Expected OIDC `aud` claim — a constant shared with the Pub/Sub subscription, not the service's own URL |
| `PUBSUB_PUSH_SERVICE_ACCOUNT` | —        | Expected OIDC token email claim            |
| `BQ_PROJECT`             | —             | BigQuery project; empty uses the runtime project |
| `BQ_DATASET` / `BQ_TABLE`| — / `readings`| Destination table for parsed readings      |
| `QUARANTINE_BUCKET`      | —             | GCS bucket for permanently failed payloads |
| `INGEST_BATCH_ROWS`      | `5000`        | Rows per BigQuery Storage Write append     |

The service fails fast on startup if the configuration is invalid or Postgres is
unreachable (Postgres is not required when `ENABLE_INGEST_ENDPOINT=true`), so a bad
deploy never reports itself as healthy.

## Docker

`Dockerfile` is a two-stage build: `golang:1.25-alpine` compiles a static,
`-trimpath`ed binary with the version stamped in via `-ldflags`, and the runtime stage
is Alpine with a non-root user (uid 10001), a `HEALTHCHECK` on `/healthz`, and nothing
else. Build it directly with:

```bash
make docker-build              # experiment-go:<git describe> + :latest
make docker-images             # see what got tagged
DOCKER_IMAGE=ghcr.io/pytsekas/experiment-go make docker-build docker-push
```

### Running the image

The container needs a Postgres it can reach, and `localhost` inside a container is the
container itself — so pick one of these:

```bash
make up                        # simplest: compose runs db + api together

make up-db && make docker-run  # image on the host network path, db via host.docker.internal
make up-db && make docker-run-net   # image joined to the compose network, db as "db"
```

The equivalent raw commands, if you'd rather not go through make:

```bash
# database published on localhost:5432 (make up-db)
docker run --rm -p 8080:8080 \
  -e DATABASE_URL="postgres://app:app@host.docker.internal:5432/experiment?sslmode=disable" \
  -e MIGRATE_ON_START=true \
  --add-host=host.docker.internal:host-gateway \
  experiment-go:latest

# or attached to the compose network, talking to the db service directly
docker run --rm -p 8080:8080 --network experiment-go_default \
  -e POSTGRES_HOST=db -e POSTGRES_USER=app -e POSTGRES_PASSWORD=app \
  -e POSTGRES_DB=experiment -e MIGRATE_ON_START=true \
  experiment-go:latest

# the migration tool is the same image
docker run --rm --network experiment-go_default \
  -e POSTGRES_HOST=db -e POSTGRES_USER=app -e POSTGRES_PASSWORD=app \
  -e POSTGRES_DB=experiment \
  experiment-go:latest migrate version
```

`docker run` without any database configuration exits immediately with
`fatal: config: DATABASE_URL (or POSTGRES_*) must be set` — that is the fail-fast check
working, not a broken image.

`docker-compose.yml` runs two services: `db` (Postgres 17 with a healthcheck and a named
volume) and `api` (waits for the database to be healthy, then migrates and serves).

## Migrations

Migrations are SQL pairs in `migrations/`, compiled into the binary with `//go:embed`
and applied by golang-migrate used **as a library** (`internal/migrator`) — there is no
separate migration image, no mounted directory, and no CLI to install. The binary is the
migration tool:

```bash
api migrate up             # apply everything pending
api migrate down [n|all]   # roll back n migrations (default 1)
api migrate version        # current version, and whether it is dirty
api migrate force <v>      # clear a dirty state after fixing a failed migration
```

Through make (which fills in the DSN from `.env`):

```bash
make migrate-new name=add_task_owner   # create the files, then edit them
make migrate-up
make migrate-down n=2
make migrate-version
make migrate-force version=1
make migrate-docker cmd=up             # run it inside the container image instead
```

**On boot:** set `MIGRATE_ON_START=true` and the service applies pending migrations
before it starts listening — that is what `make up` and `make run` do, so a fresh
database needs no extra step. golang-migrate wraps the run in a Postgres advisory lock,
so several replicas starting at once serialise instead of colliding; the losers simply
report "database schema up to date".

The image ships with `MIGRATE_ON_START=false`. For production the safer shape is a
release step or init container running `api migrate up` (same image, same embedded SQL),
so schema changes are an explicit, observable deploy phase rather than a side effect of
every pod restart.

Prefer a different library? `internal/migrator` is the only place that knows about
golang-migrate, so swapping in [goose](https://github.com/pressly/goose) or
[tern](https://github.com/jackc/tern) means rewriting one file — the SQL files and the
`api migrate ...` interface stay as they are.

## Ingest

The same binary also runs a second role. An ENTSO-E market document arrives as
a Pub/Sub push to `POST /internal/pubsub/consumption`, is streamed through a
decoder-based parser whose memory stays proportional to one batch of readings rather than
the whole document, and its readings are appended to BigQuery through the Storage Write
API. A permanently invalid payload — one the parser rejects, or one Pub/Sub could not
even deliver as valid JSON — is acknowledged (200) and parked in a GCS quarantine bucket
under a key that includes a timestamp and a digest of the message id, instead of being
retried forever. A transient storage failure replies 503 so Pub/Sub retries the delivery,
and duplicate rows from a retry are resolved by the `readings_current` view rather than
prevented at write time.

### Document formats

A registry maps a document's root element to the parser that understands it, so a new
format is a new file in `internal/consumption/xmlfmt` plus one line in `cmd/api/main.go`.
A document whose root element matches nothing registered is quarantined, not retried.

| Root element | Namespace | Parser |
| --- | --- | --- |
| `GL_MarketDocument` | `…451-6:generationloaddocument:3:0` | `xmlfmt.ESMP` |
| `EnergyAccount_MarketDocument` | `…451-4:energyaccountdocument:4:0` | `xmlfmt.EnergyAccount` |

### The reading model

Every format normalises onto one `Reading` row per quantity, keyed by metering point and
interval and distinguished by two dimensions:

- `direction` — `consumption` or `production`.
- `measure` — `gross` (what the meter registered) or `net` (the same interval after the
  source netted the two directions against each other).

ESMP states one quantity per point and is always `gross`. An energy-account point states
up to four — `in`/`out` and `netIn`/`netOut` — and becomes up to four rows. The netted
pair is carried rather than recomputed in SQL because sources do not net by subtraction:
a real document reports `out=201.367` alongside `netOut=71.917` for the same point. A
quantity the document omits produces no row, so a point stating only the netted pair does
not gain fabricated gross zeros.

Both dimensions are in the `readings_current` view's dedup key, so gross and net rows for
one interval coexist rather than overwrite each other.

One caveat, because the view cannot currently express it: a document may state the same
metering point and interval twice — three meters in the sample do, a full series plus a
net-only restatement. `Service.Ingest` stamps a single `ingested_at` for the whole
message, so those rows tie on the view's `ORDER BY` and which one it surfaces is
unspecified. In the observed samples the restated values are identical, so the choice is
invisible; if a source ever restates with *different* values, picking a winner needs a
deterministic tie-break (a document-order column) that does not exist yet.

One image serves both roles — the API and the ingest service are the same container,
distinguished only by `ENABLE_INGEST_ENDPOINT` and the settings below it. The ingest role
needs no database: `internal/config` makes Postgres optional once
`ENABLE_INGEST_ENDPOINT=true`.

| Variable                      | Default        | Notes                                    |
| ------------------------------ | -------------- | ---------------------------------------- |
| `ENABLE_INGEST_ENDPOINT`       | `false`        | Registers the push route. On for the ingest service only. |
| `PUBSUB_AUDIENCE`              | —              | Expected OIDC `aud` claim — a constant shared with the Pub/Sub subscription, not the service's own URL |
| `PUBSUB_PUSH_SERVICE_ACCOUNT`  | —              | Expected OIDC token email claim |
| `BQ_PROJECT`                   | —              | BigQuery project; empty uses the runtime project |
| `BQ_DATASET` / `BQ_TABLE`      | — / `readings` | Destination table for parsed readings |
| `QUARANTINE_BUCKET`            | —              | GCS bucket for permanently failed payloads |
| `INGEST_BATCH_ROWS`            | `5000`         | Rows per BigQuery Storage Write append |

`PUBSUB_AUDIENCE`, `PUBSUB_PUSH_SERVICE_ACCOUNT`, `BQ_DATASET` and `QUARANTINE_BUCKET`
must all be set once `ENABLE_INGEST_ENDPOINT=true` — the service fails fast at startup
otherwise. `BQ_TABLE` defaults to `readings` and `BQ_PROJECT` to the runtime project.

```bash
make ingest-deploy                  # Terraform: ingest Cloud Run service, subscription, dataset
make ingest-publish file=doc.xml    # publish one document to the upstream topic
make ingest-smoke                   # publish the golden file and wait for its rows
make bq-readings                    # query the deduplicated readings_current view
```

`deploy/README.md` has the full walkthrough: the dataset, the subscription and its
dead-letter topic, the quarantine bucket, and what each of those costs.

## Google Cloud, AWS, Kubernetes and load tests

`deploy/README.md` is a full walkthrough: Cloud SQL → Artifact Registry → Cloud Run →
ingest (Pub/Sub → BigQuery) → GKE → performance testing, with costs, teardown commands
and the gotchas that actually bite, plus the same path on AWS (RDS → ECR → ECS Fargate →
EKS). Infrastructure is
Terraform in `deploy/terraform/`, one root per cloud with an identical variable and
output contract so the setup can be reused for any container + database app. The
short version:

```bash
make gcp-bootstrap                 # APIs + image repo            (AWS: make aws-bootstrap)
make sql-create                    # cheapest Cloud SQL Postgres  (AWS: make aws-sql-create)
make image-push                    # linux/amd64 image            (AWS: make aws-image-push)
make run-deploy && make run-smoke  # Cloud Run + k6 smoke         (AWS: make aws-ecs-deploy aws-ecs-smoke)
make gke-create                    # cheap zonal cluster, spot    (AWS: make aws-eks-create)
make k8s-deploy                    # kustomize overlay + Job      (AWS: make k8s-deploy CLOUD=aws)
make loadtest-stress BASE_URL=$(make -s k8s-url)   # watch the HPA react
make teardown-all                  # stop paying for all of it    (AWS: make aws-teardown-all)
```

- `deploy/k8s/` — kustomize `base/` (namespace, ServiceAccount, config, migration Job,
  Deployment, Services, HPA, PDB) plus `overlays/gke/` (Cloud SQL Auth Proxy native
  sidecar, GKE load balancer) and `overlays/eks/` (RDS host, TLS, NLB).
- `loadtest/k6/` — `smoke.js` (deploy check), `load.js` (constant arrival rate, read-heavy
  mix), `stress.js` (ramping load against the burn endpoint), plus a Job to run k6 inside
  the cluster.

## CI

`.github/workflows/ci.yml` runs on every push to `main` and every pull request. It
checks tidiness, formatting, `go vet`, golangci-lint and race-enabled tests, then builds
the Docker image with layer caching and asserts the binary exits non-zero when required
configuration is missing. `make check` runs the same lint-and-test half locally.

## Releases

`.github/workflows/release.yml` turns a version tag into a release:

```bash
git tag v1.2.3 && git push origin v1.2.3
```

That publishes

- **binaries** for `linux/amd64`, `linux/arm64`, `darwin/amd64` and `darwin/arm64`, built
  with `make build` so the `-trimpath` and version ldflags stay defined in one place,
  packed as `experiment-go_<version>_<os>_<arch>.tar.gz` with a `SHA256SUMS` file;
- a **multi-arch image** (`linux/amd64,linux/arm64`) as
  `ghcr.io/<owner>/experiment-go:<version>` and `:latest`;
- a **GitHub Release** with generated notes, the archives and the image reference.

A tag containing a hyphen (`v1.2.3-rc.1`) is published as a pre-release. Running the
workflow manually (`workflow_dispatch`) builds everything under a throwaway
`0.0.0-dev.<run>` version and publishes nothing — that is how to test a change to the
workflow without cutting a tag. The linux/amd64 binary is smoke-tested in the workflow
the same way the image is in CI: started with no database, it must exit non-zero.

Locally, `make build` stamps the version from `git describe --tags --always --dirty`, so
`api` and `/healthz` report the same version string the release does.
