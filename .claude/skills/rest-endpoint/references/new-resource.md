# Case B — a whole new resource

File-by-file templates for adding a resource, using `project` (table `projects`) as the
example. Replace `project`/`Project`/`projects` throughout, and keep the singular package
name — `internal/task` serves the `tasks` table, and the codebase reads better when that
holds.

## Contents

1. [Migration pair](#1-migration-pair)
2. [internal/project/project.go — model and contracts](#2-internalprojectprojectgo)
3. [internal/project/postgres.go — SQL](#3-internalprojectpostgresgo)
4. [internal/project/service.go — business rules](#4-internalprojectservicego)
5. [internal/httpapi/projects.go — handlers](#5-internalhttpapiprojectsgo)
6. [internal/httpapi/router.go — Deps and routes](#6-internalhttpapiroutergo)
7. [cmd/api/main.go — wiring](#7-cmdapimaingo)
8. [Tests](#8-tests)
9. [Docs and verification](#9-docs-and-verification)

Build in this order. Each step compiles on top of the last, so a failure always points at
the thing you just wrote.

## 1. Migration pair

```bash
make migrate-new name=create_projects
```

That creates the numbered pair for you — do not hand-name the files, the six-digit
sequence is what orders them and `internal/migrator/migrator_test.go` fails the build if a
version is missing either half.

`migrations/000002_create_projects_table.up.sql`:

```sql
CREATE TABLE IF NOT EXISTS projects (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT        NOT NULL CHECK (length(btrim(name)) > 0),
    description TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS projects_created_at_idx ON projects (created_at DESC);
```

`migrations/000002_create_projects_table.down.sql` — reverse order, always writable:

```sql
DROP INDEX IF EXISTS projects_created_at_idx;

DROP TABLE IF EXISTS projects;
```

The `CHECK` mirrors the service's domain rule on purpose. The service is what returns a
clean 400; the constraint is what protects the table from anything that is not this
service.

Migrations are embedded via `migrations/embed.go` (`//go:embed *.sql`), so a new pair
needs no registration — it ships in the binary automatically.

## 2. internal/project/project.go

```go
// Package project contains the projects resource: its model, its business rules
// (Service), its storage contract (Repository) and the Postgres implementation
// of that contract.
package project

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned when a project does not exist.
var ErrNotFound = errors.New("project not found")

// ErrInvalidName is returned when a project name is empty after trimming.
var ErrInvalidName = errors.New("project name must not be blank")

// Project is the domain model exposed by the API.
type Project struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// CreateParams carries the fields needed to create a project.
type CreateParams struct {
	Name        string
	Description string
}

// UpdateParams carries the fields needed to update a project.
type UpdateParams struct {
	Name        string
	Description string
}

// ListParams controls pagination when listing projects.
type ListParams struct {
	Limit  int
	Offset int
}

// Repository is the storage contract the service depends on. Keeping it an
// interface lets the service be tested without a database.
type Repository interface {
	List(ctx context.Context, p ListParams) ([]Project, error)
	Get(ctx context.Context, id int64) (Project, error)
	Create(ctx context.Context, p CreateParams) (Project, error)
	Update(ctx context.Context, id int64, p UpdateParams) (Project, error)
	Delete(ctx context.Context, id int64) error
}
```

## 3. internal/project/postgres.go

```go
package project

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresRepository stores projects in Postgres.
type PostgresRepository struct {
	pool *pgxpool.Pool
}

// NewPostgresRepository returns a Repository backed by the given pool.
func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

// Compile-time check that the implementation satisfies the contract.
var _ Repository = (*PostgresRepository)(nil)

const columns = `id, name, description, created_at, updated_at`

// List returns a page of projects, newest first.
func (r *PostgresRepository) List(ctx context.Context, p ListParams) ([]Project, error) {
	if p.Limit <= 0 || p.Limit > 100 {
		p.Limit = 20
	}
	if p.Offset < 0 {
		p.Offset = 0
	}

	rows, err := r.pool.Query(ctx,
		`SELECT `+columns+` FROM projects ORDER BY id DESC LIMIT $1 OFFSET $2`,
		p.Limit, p.Offset)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	projects := make([]Project, 0, p.Limit)
	for rows.Next() {
		var pr Project
		if err := rows.Scan(&pr.ID, &pr.Name, &pr.Description, &pr.CreatedAt, &pr.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		projects = append(projects, pr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate projects: %w", err)
	}

	return projects, nil
}

// Get returns a single project by ID.
func (r *PostgresRepository) Get(ctx context.Context, id int64) (Project, error) {
	var pr Project
	err := r.pool.QueryRow(ctx, `SELECT `+columns+` FROM projects WHERE id = $1`, id).
		Scan(&pr.ID, &pr.Name, &pr.Description, &pr.CreatedAt, &pr.UpdatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Project{}, ErrNotFound
	case err != nil:
		return Project{}, fmt.Errorf("get project %d: %w", id, err)
	}

	return pr, nil
}

// Create inserts a project and returns the stored row.
func (r *PostgresRepository) Create(ctx context.Context, p CreateParams) (Project, error) {
	var pr Project
	err := r.pool.QueryRow(ctx,
		`INSERT INTO projects (name, description) VALUES ($1, $2) RETURNING `+columns,
		p.Name, p.Description).
		Scan(&pr.ID, &pr.Name, &pr.Description, &pr.CreatedAt, &pr.UpdatedAt)
	if err != nil {
		return Project{}, fmt.Errorf("create project: %w", err)
	}

	return pr, nil
}

// Update replaces the mutable fields of a project and returns the stored row.
func (r *PostgresRepository) Update(ctx context.Context, id int64, p UpdateParams) (Project, error) {
	var pr Project
	err := r.pool.QueryRow(ctx,
		`UPDATE projects SET name = $1, description = $2, updated_at = now() WHERE id = $3 RETURNING `+columns,
		p.Name, p.Description, id).
		Scan(&pr.ID, &pr.Name, &pr.Description, &pr.CreatedAt, &pr.UpdatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Project{}, ErrNotFound
	case err != nil:
		return Project{}, fmt.Errorf("update project %d: %w", id, err)
	}

	return pr, nil
}

// Delete removes a project by ID.
func (r *PostgresRepository) Delete(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete project %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}

	return nil
}
```

`columns` is package-scoped and each resource package has its own, so the name does not
clash with `internal/task`.

## 4. internal/project/service.go

```go
package project

import (
	"context"
	"log/slog"
	"strings"
)

// Service is the business-logic contract the HTTP layer depends on. It sits
// between the transport and the Repository; business rules about projects, such
// as name normalisation, belong here rather than in handlers or SQL.
type Service interface {
	List(ctx context.Context, p ListParams) ([]Project, error)
	Get(ctx context.Context, id int64) (Project, error)
	Create(ctx context.Context, p CreateParams) (Project, error)
	Update(ctx context.Context, id int64, p UpdateParams) (Project, error)
	Delete(ctx context.Context, id int64) error
}

type service struct {
	repo Repository
	log  *slog.Logger
}

// Compile-time check that the implementation satisfies the contract.
var _ Service = (*service)(nil)

// NewService returns a Service backed by the given repository. A nil logger
// falls back to slog.Default.
func NewService(repo Repository, log *slog.Logger) Service {
	if log == nil {
		log = slog.Default()
	}

	return &service{repo: repo, log: log}
}

// List returns a page of projects.
func (s *service) List(ctx context.Context, p ListParams) ([]Project, error) {
	return s.repo.List(ctx, p)
}

// Get returns a single project by ID.
func (s *service) Get(ctx context.Context, id int64) (Project, error) {
	return s.repo.Get(ctx, id)
}

// Create normalises the input and stores a new project.
func (s *service) Create(ctx context.Context, p CreateParams) (Project, error) {
	name, err := normaliseName(p.Name)
	if err != nil {
		return Project{}, err
	}
	p.Name = name

	pr, err := s.repo.Create(ctx, p)
	if err != nil {
		return Project{}, err
	}

	s.log.InfoContext(ctx, "project created", slog.Int64("project_id", pr.ID))

	return pr, nil
}

// Update normalises the input and replaces the mutable fields of a project.
func (s *service) Update(ctx context.Context, id int64, p UpdateParams) (Project, error) {
	name, err := normaliseName(p.Name)
	if err != nil {
		return Project{}, err
	}
	p.Name = name

	return s.repo.Update(ctx, id, p)
}

// Delete removes a project by ID.
func (s *service) Delete(ctx context.Context, id int64) error {
	return s.repo.Delete(ctx, id)
}

// normaliseName trims surrounding whitespace and rejects a blank name, so both
// create and update store the same shape and the database CHECK constraint is
// never the first thing to complain.
func normaliseName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", ErrInvalidName
	}

	return name, nil
}
```

## 5. internal/httpapi/projects.go

```go
package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/pytsekas/experiment-go/internal/project"
)

// projectHandler exposes the projects resource over HTTP.
type projectHandler struct {
	svc project.Service
	log *slog.Logger
}

type createProjectRequest struct {
	Name        string `json:"name" binding:"required,min=1,max=200"`
	Description string `json:"description" binding:"omitempty,max=2000"`
}

type updateProjectRequest struct {
	Name        string `json:"name" binding:"required,min=1,max=200"`
	Description string `json:"description" binding:"omitempty,max=2000"`
}

type listProjectsQuery struct {
	Limit  int `form:"limit" binding:"omitempty,min=1,max=100"`
	Offset int `form:"offset" binding:"omitempty,min=0"`
}

// list handles GET /projects.
func (h projectHandler) list(c *gin.Context) {
	var q listProjectsQuery
	if err := c.ShouldBindQuery(&q); err != nil {
		respondError(c, http.StatusBadRequest, "invalid query parameters: "+err.Error())

		return
	}

	projects, err := h.svc.List(c.Request.Context(), project.ListParams{Limit: q.Limit, Offset: q.Offset})
	if err != nil {
		h.fail(c, err, "list projects")

		return
	}

	c.JSON(http.StatusOK, gin.H{"data": projects})
}

// get handles GET /projects/:id.
func (h projectHandler) get(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}

	pr, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		h.fail(c, err, "get project")

		return
	}

	c.JSON(http.StatusOK, gin.H{"data": pr})
}

// create handles POST /projects.
func (h projectHandler) create(c *gin.Context) {
	var req createProjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body: "+err.Error())

		return
	}

	pr, err := h.svc.Create(c.Request.Context(),
		project.CreateParams{Name: req.Name, Description: req.Description})
	if err != nil {
		h.fail(c, err, "create project")

		return
	}

	c.Header("Location", "/api/v1/projects/"+strconv.FormatInt(pr.ID, 10))
	c.JSON(http.StatusCreated, gin.H{"data": pr})
}

// update handles PUT /projects/:id.
func (h projectHandler) update(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}

	var req updateProjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body: "+err.Error())

		return
	}

	pr, err := h.svc.Update(c.Request.Context(), id,
		project.UpdateParams{Name: req.Name, Description: req.Description})
	if err != nil {
		h.fail(c, err, "update project")

		return
	}

	c.JSON(http.StatusOK, gin.H{"data": pr})
}

// remove handles DELETE /projects/:id.
func (h projectHandler) remove(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}

	if err := h.svc.Delete(c.Request.Context(), id); err != nil {
		h.fail(c, err, "delete project")

		return
	}

	c.Status(http.StatusNoContent)
}

// fail maps a service error onto an HTTP response, logging unexpected ones.
func (h projectHandler) fail(c *gin.Context, err error, op string) {
	switch {
	case errors.Is(err, project.ErrNotFound):
		respondError(c, http.StatusNotFound, "project not found")

		return
	case errors.Is(err, project.ErrInvalidName):
		respondError(c, http.StatusBadRequest, project.ErrInvalidName.Error())

		return
	}

	h.log.ErrorContext(c.Request.Context(), "request error",
		slog.String("op", op),
		slog.Any("error", err),
		slog.String(requestIDKey, c.GetString(requestIDKey)),
	)
	respondError(c, http.StatusInternalServerError, "internal server error")
}
```

`parseID` and `respondError` already live in the `httpapi` package. Reuse them —
redefining either is a compile error, and a per-resource copy of `parseID` would drift.

## 6. internal/httpapi/router.go

Add the service to `Deps`:

```go
type Deps struct {
	Logger   *slog.Logger
	Tasks    task.Service
	Projects project.Service // new
	DB       Pinger
	Version  string
	Prodlike bool
	EnableBurn bool
}
```

and register the routes inside the existing `v1` group:

```go
v1 := r.Group("/api/v1")
{
	tasks := taskHandler{svc: d.Tasks, log: d.Logger}
	v1.GET("/tasks", tasks.list)
	// ... existing task routes

	projects := projectHandler{svc: d.Projects, log: d.Logger}
	v1.GET("/projects", projects.list)
	v1.POST("/projects", projects.create)
	v1.GET("/projects/:id", projects.get)
	v1.PUT("/projects/:id", projects.update)
	v1.DELETE("/projects/:id", projects.remove)

	if d.EnableBurn {
		// ... unchanged
	}
}
```

Each resource owns its own path prefix, so wildcard names never collide across resources.
Within one prefix they must agree: `/projects/:id` and `/projects/:projectID/members`
would panic at startup.

## 7. cmd/api/main.go

Construct the service in `serve` and pass it in. The pool is already open at this point:

```go
router := httpapi.NewRouter(httpapi.Deps{
	Logger:     log,
	Tasks:      task.NewService(task.NewPostgresRepository(pool), log),
	Projects:   project.NewService(project.NewPostgresRepository(pool), log),
	DB:         pool,
	Version:    version,
	Prodlike:   cfg.IsProduction(),
	EnableBurn: cfg.Features.BurnEndpoint,
})
```

Add `"github.com/pytsekas/experiment-go/internal/project"` to the import block, in the
module group with the others.

## 8. Tests

Two test files, mirroring the two stubs.

### internal/project/service_test.go

Copy the shape of `internal/task/service_test.go`: a `stubRepo` whose methods `t.Fatal`
when unconfigured, and table-driven cases for the rules the service actually owns —
here, name normalisation and the blank-name rejection. A pass-through method does not
need its own case.

### internal/httpapi/projects_test.go

Two things will bite you, both because the `httpapi` package already has test files:

- **Do not add `TestMain`.** `tasks_test.go` already defines it for the package, and a
  second one will not compile.
- **Do not reuse the helper names** `testRouter`, `noService` or `sampleTask` — they are
  package-level and already taken. Name yours `testProjectRouter`, `noProjectService`,
  `sampleProject`.

```go
func testProjectRouter(svc project.Service) *gin.Engine {
	return NewRouter(Deps{
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Projects: svc,
		Version:  "test",
	})
}
```

Leaving `Tasks` nil here is fine: the task routes are registered but never hit by these
tests. The same is true in reverse for the existing `testRouter`, which is why adding a
resource does not require touching `tasks_test.go`.

Cover the happy path, each sentinel error, an input rejected before the service, and a
store failure mapped to 500 — the same four shapes as `TestTaskEndpoints`.

## 9. Docs and verification

Add the new routes to the endpoint tables in both `README.md` and `AGENTS.md`. If the
resource introduced configuration, `README.md`, `.env.example`,
`deploy/k8s/base/configmap.yaml` and the Terraform `app_env` maps need it too.

```bash
make check                              # fmt-check + vet + lint + test, no database needed
make up && make migrate-up && make smoke  # exercises the new SQL for real
```

The repository SQL has no automated test, so run the second line (or check by hand with
`make psql`) and say so explicitly in your report rather than implying `make check`
covered it.
