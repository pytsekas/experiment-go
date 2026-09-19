# Case A — a new endpoint on an existing resource

A complete worked change, adding `PATCH /api/v1/tasks/:id/done` to the existing `task`
resource. Substitute your own operation; the ordering and the touch points are the point.

The order below matters: each step compiles on top of the one before, so you never sit on
a broken tree wondering which of six edits is at fault.

## 1. Domain: params and sentinel errors

`internal/task/task.go` — only if the operation needs new input or can fail in a new way.
This example needs neither a `Params` struct (one bool travels fine as an argument) nor a
new sentinel (`ErrNotFound` already covers the failure), so the only edit is the
`Repository` interface in step 2.

Add a sentinel when the handler must map a failure to something other than 500:

```go
// ErrAlreadyDone is returned when a task is already in the requested state.
var ErrAlreadyDone = errors.New("task is already done")
```

## 2. Repository: interface, then SQL

`internal/task/task.go`:

```go
type Repository interface {
	List(ctx context.Context, p ListParams) ([]Task, error)
	Get(ctx context.Context, id int64) (Task, error)
	Create(ctx context.Context, p CreateParams) (Task, error)
	Update(ctx context.Context, id int64, p UpdateParams) (Task, error)
	SetDone(ctx context.Context, id int64, done bool) (Task, error) // new
	Delete(ctx context.Context, id int64) error
}
```

`internal/task/postgres.go` — reuse the package's `columns` const, map `pgx.ErrNoRows`
to the sentinel, wrap anything else with context:

```go
// SetDone flips the done flag of a task and returns the stored row.
func (r *PostgresRepository) SetDone(ctx context.Context, id int64, done bool) (Task, error) {
	var t Task
	err := r.pool.QueryRow(ctx,
		`UPDATE tasks SET done = $1, updated_at = now() WHERE id = $2 RETURNING `+columns,
		done, id).
		Scan(&t.ID, &t.Title, &t.Done, &t.CreatedAt, &t.UpdatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Task{}, ErrNotFound
	case err != nil:
		return Task{}, fmt.Errorf("set task %d done: %w", id, err)
	}

	return t, nil
}
```

## 3. Service: interface, then rules

`internal/task/service.go` — add to the interface and implement. If the operation had a
domain rule it would live here, the way `normaliseTitle` does for create and update. A
pass-through is fine when there is genuinely no rule; resist inventing one.

```go
// SetDone marks a task done or not done.
func (s *service) SetDone(ctx context.Context, id int64, done bool) (Task, error) {
	return s.repo.SetDone(ctx, id, done)
}
```

## 4. Update the stubs — the compiler will insist

Both stubs assert interface satisfaction at compile time, so step 3 has just broken them.
This is the step people forget, and the error message points at the `var _` line rather
than at the stub method you need to add.

`internal/task/service_test.go` (`stubRepo`) and `internal/httpapi/tasks_test.go`
(`stubService`) both get the same treatment:

```go
type stubService struct {
	t         *testing.T
	// ... existing fields
	setDoneFn func(ctx context.Context, id int64, done bool) (task.Task, error)
}

func (s stubService) SetDone(ctx context.Context, id int64, done bool) (task.Task, error) {
	if s.setDoneFn == nil {
		s.t.Fatal("unexpected service SetDone call")
	}

	return s.setDoneFn(ctx, id, done)
}
```

The `t.Fatal` on an unconfigured call is load-bearing. It is what makes a table row like
`"rejects non numeric id"` meaningful: if the handler ever stopped validating and passed
the request through, the stub would fail the test instead of quietly returning a zero
value that happens to match.

## 5. Handler

`internal/httpapi/tasks.go`. Note the binding tag on a bool:

```go
type setTaskDoneRequest struct {
	Done bool `json:"done"`
}
```

Do **not** write `binding:"required"` on a bool. Gin's `required` rejects the zero value,
so `{"done": false}` would be a 400 — almost never what you want. If you genuinely need to
tell "absent" from "false", use `*bool` and check for nil yourself.

```go
// setDone handles PATCH /tasks/:id/done.
func (h taskHandler) setDone(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}

	var req setTaskDoneRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body: "+err.Error())

		return
	}

	t, err := h.svc.SetDone(c.Request.Context(), id, req.Done)
	if err != nil {
		h.fail(c, err, "set task done")

		return
	}

	c.JSON(http.StatusOK, gin.H{"data": t})
}
```

`parseID` and `respondError` are already package-level in `internal/httpapi` — reuse them,
redefining either is a compile error.

If you added a sentinel in step 1, extend `h.fail`'s switch so it maps to a real status
instead of falling through to the logged 500:

```go
case errors.Is(err, task.ErrAlreadyDone):
	respondError(c, http.StatusConflict, task.ErrAlreadyDone.Error())

	return
```

## 6. Route

`internal/httpapi/router.go`, inside the `v1` group with its siblings:

```go
v1.PATCH("/tasks/:id/done", tasks.setDone)
```

The wildcard must stay `:id`. Gin panics at startup if two routes use different wildcard
names in the same path position, so `/tasks/:taskID/done` would take the whole service
down on boot rather than failing a test:

```
panic: ':taskID' in new path '/tasks/:taskID/done' conflicts with existing wildcard ':id'
in existing prefix '/tasks/:id'
```

## 7. Tests

Add rows to the existing table in `TestTaskEndpoints`. Four kinds of row earn their place:

```go
{
	name:   "set done returns the stored task",
	method: http.MethodPatch,
	target: "/api/v1/tasks/1/done",
	body:   `{"done":true}`,
	svc: func(t *testing.T) stubService {
		return stubService{t: t, setDoneFn: func(_ context.Context, id int64, done bool) (task.Task, error) {
			tk := sampleTask()
			tk.Done = done

			return tk, nil
		}}
	},
	wantStatus: http.StatusOK,
	wantBody:   `"done":true`,
},
{
	name:       "set done rejects non numeric id",
	method:     http.MethodPatch,
	target:     "/api/v1/tasks/abc/done",
	body:       `{"done":true}`,
	svc:        noService,
	wantStatus: http.StatusBadRequest,
	wantBody:   "positive integer",
},
{
	name:   "set done maps missing task to 404",
	method: http.MethodPatch,
	target: "/api/v1/tasks/42/done",
	body:   `{"done":true}`,
	svc: func(t *testing.T) stubService {
		return stubService{t: t, setDoneFn: func(_ context.Context, _ int64, _ bool) (task.Task, error) {
			return task.Task{}, task.ErrNotFound
		}}
	},
	wantStatus: http.StatusNotFound,
	wantBody:   "task not found",
},
{
	name:   "set done maps store failure to 500",
	method: http.MethodPatch,
	target: "/api/v1/tasks/1/done",
	body:   `{"done":true}`,
	svc: func(t *testing.T) stubService {
		return stubService{t: t, setDoneFn: func(_ context.Context, _ int64, _ bool) (task.Task, error) {
			return task.Task{}, errors.New("connection refused")
		}}
	},
	wantStatus: http.StatusInternalServerError,
	wantBody:   "internal server error",
},
```

Use `noService` for any row that must be rejected before the service is reached.

If the service method gained a domain rule, it needs its own test in
`internal/task/service_test.go` against `stubRepo` — the handler test cannot see it.

## 8. Docs

`README.md` and `AGENTS.md` both carry a table of every endpoint. Add the row to both;
`AGENTS.md` is what the next agent reads to orient itself, so a missing row there costs
more than it looks.

## 9. Verify

```bash
make check
```

Report what it printed. If you touched SQL, that path has no automated test — run
`make up && make smoke`, or check by hand with `make psql`, and say which you did.
