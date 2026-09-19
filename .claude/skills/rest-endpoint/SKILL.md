---
name: rest-endpoint
description: >-
  Add or change a REST endpoint in this Go service (Gin + pgx + Postgres, layered
  handler -> Service -> Repository). Use this skill whenever a request touches the HTTP
  surface of the app: "add an endpoint for X", "expose Y over the API", "add a projects
  resource", "add PATCH /tasks/:id/done", "let callers filter tasks by done", "add
  pagination to Z", "I need a route that returns ...". Use it even when the user never
  says "endpoint" or "REST", and even when the change sounds like a one-liner, because
  this repo's layering, response envelopes, error mapping, test stubs and doc tables are
  each easy to get subtly wrong in ways the compiler will not catch.
---

# Adding a REST endpoint

## The shape of this app

Every request travels the same four layers:

```
internal/httpapi (Gin handler) -> <resource>.Service -> <resource>.Repository -> Postgres
```

Each layer has exactly one job. Putting logic one layer off is the most common way a
change here goes wrong, because nothing fails loudly — the code compiles, the tests pass,
and the rule quietly stops applying to the other callers that should have shared it.

| Layer | Owns | Never contains |
| --- | --- | --- |
| Handler (`internal/httpapi/<resource>.go`) | Binding, transport validation, status codes, response envelope | Business rules, SQL |
| Service (`internal/<resource>/service.go`) | Business rules, normalisation, sentinel errors | Gin types, SQL |
| Repository (`internal/<resource>/postgres.go`) | SQL, scanning, `pgx.ErrNoRows` -> `ErrNotFound` | Business rules, HTTP concerns |

Validation lands in three places on purpose, and they are not redundant: gin `binding`
tags reject malformed *transport* input (wrong type, missing field, out-of-range page
size); the service enforces *domain* rules (a title that is blank after trimming); the
database `CHECK` constraint is the last resort that protects rows written by anything
other than this service. When you add a field, decide which of the three it needs — often
all three.

## Step 0 — which change is this?

**Case A — a new endpoint on a resource that already exists** (`task` today). You are
adding a method to an existing `Service`/`Repository` pair and a handler func beside its
siblings. Read `references/new-endpoint.md` for a full worked change.

**Case B — a new resource** (`projects`, `users`, ...). You are creating a migration
pair, a new `internal/<resource>/` package, a new handler file, new routes and new
wiring in `main.go`. Read `references/new-resource.md` for the file-by-file templates.

If the user's request is vague about which ("I want to track projects"), it is Case B.
If they name a path and a verb, it is Case A.

## Case A — new endpoint on an existing resource

Work in this order; each step compiles on top of the last.

1. **Params and errors** in `internal/<resource>/<resource>.go` — add the `...Params`
   struct if the operation takes input, and a sentinel error (`var ErrThing = errors.New(...)`)
   if it introduces a new failure the handler must map to something other than 500.
2. **`Repository` interface + Postgres impl** — add the method to the interface in
   `<resource>.go`, then implement it in `postgres.go`. Map `pgx.ErrNoRows` to the
   sentinel with `errors.Is`, wrap everything else as `fmt.Errorf("op: %w", err)`.
3. **`Service` interface + impl** in `service.go` — add the method to the interface and
   the `service` struct. Domain rules go here, not in the handler.
4. **Update the test stubs.** Adding a method to `Service` breaks `stubService` in
   `internal/httpapi/<resource>_test.go`, and adding one to `Repository` breaks `stubRepo`
   in `internal/<resource>/service_test.go` — both assert `var _ Service = ...` at compile
   time. Give the stub a `<name>Fn` field and a method that calls `s.t.Fatal` when the
   field is nil. That "fail on unconfigured call" property is what stops a request that
   should have been rejected early from silently reaching the service and passing.
5. **Handler func** in `internal/httpapi/<resource>.go`, beside its siblings, using the
   existing `h.fail(c, err, "op")` for the error path. Extend `fail`'s switch with the new
   sentinel.
6. **Route** in `internal/httpapi/router.go`, inside the `v1` group with the others.
7. **Tests** — add rows to the existing table test. Cover the happy path, each sentinel
   error, a malformed input rejected before the service, and a store failure mapped to 500.
8. **Docs** — the endpoint tables in `README.md` and `AGENTS.md` are expected to list
   every route. A new route that is missing from them is an incomplete change.

## Case B — new resource

Same ordering principle, one layer wider. Full templates are in
`references/new-resource.md`; the outline is:

1. `make migrate-new name=create_<plural>` then write **both** the `.up.sql` and the
   `.down.sql`. `internal/migrator/migrator_test.go` fails if a version is missing either
   half, and the six-digit numbering is what orders them.
2. `internal/<resource>/<resource>.go` — package doc, model with `json` tags, sentinel
   errors, `...Params` structs, `Repository` interface.
3. `internal/<resource>/postgres.go` — `PostgresRepository`, a `const columns = ...`, one
   method per `Repository` method, `var _ Repository = (*PostgresRepository)(nil)`.
4. `internal/<resource>/service.go` — `Service` interface, unexported `service` struct,
   `NewService(repo, log)` falling back to `slog.Default()` on a nil logger,
   `var _ Service = (*service)(nil)`.
5. `internal/httpapi/<resource>.go` — handler struct `{svc, log}`, request structs with
   `binding` tags, one func per route, one `fail` method.
6. `internal/httpapi/router.go` — add a field to `Deps`, build the handler and register
   the routes in the `v1` group.
7. `cmd/api/main.go` — construct the service in `serve` and pass it into `httpapi.Deps`.
8. Tests for both the service (`stubRepo`) and the handlers (`stubService`).
9. `README.md` and `AGENTS.md` endpoint tables.

## Conventions that are easy to miss

These are enforced by `golangci-lint` (`.golangci.yml`) or by existing tests, so getting
them wrong means a red build rather than a review comment:

- **Blank line before an early `return`.** `gocritic` is configured such that the codebase
  consistently writes guard clauses as `if err != nil {` / body / blank line / `return`.
  Match the surrounding file exactly.
- **Response envelopes.** Success is `c.JSON(status, gin.H{"data": x})`; errors only ever
  go through `respondError(c, status, msg)`, which produces `{"error": "..."}`. A 204 uses
  `c.Status(http.StatusNoContent)` with no body. A 201 also sets a `Location` header.
- **Exported identifiers need doc comments starting with their own name** (`revive`'s
  `exported` rule). This includes every new interface method and struct field type.
- **Import grouping**: stdlib, blank line, third-party, blank line, `github.com/pytsekas/experiment-go/...`.
  `goimports` is configured with that local prefix and will fail `make check` otherwise.
- **Compile-time interface checks**: `var _ Service = (*service)(nil)` next to the impl.
- **Context flows through**: handlers pass `c.Request.Context()`, never `context.Background()`.
- **Gin wildcard names must agree across a path segment.** The router already has
  `/tasks/:id`; registering `/tasks/:taskID/done` panics at startup with a wildcard
  conflict. Reuse `:id`.
- **Logging**: `slog` with typed attrs only. The middleware already logs one line per
  request, so handlers log *only* unexpected errors — which `h.fail` already does. Never
  log request bodies, DSNs or credentials.
- **Never edit `src/generated`-style outputs or anything in `_to_delete/`.**

## Verify before claiming it works

Run these and report what they actually printed:

```bash
make check      # fmt-check + vet + lint + test — the same gate CI runs
```

`make check` needs no database; the handler and service tests run entirely against stubs,
and keeping it that way is deliberate. If you touched SQL, that path has no automated
test, so exercise it for real and say so in your report:

```bash
make up         # db + api in docker
make smoke      # hits the running API
make psql       # or inspect by hand
```

If you add a new endpoint to `make smoke`, keep it to calls that are safe to repeat.
