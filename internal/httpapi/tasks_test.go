package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/pytsekas/experiment-go/internal/task"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

// stubService is a task.Service whose behaviour each test sets per method.
// Calling a method the test did not configure fails the test, so a request
// that should be rejected before reaching the service cannot pass by accident.
type stubService struct {
	t        *testing.T
	listFn   func(ctx context.Context, p task.ListParams) ([]task.Task, error)
	getFn    func(ctx context.Context, id int64) (task.Task, error)
	createFn func(ctx context.Context, p task.CreateParams) (task.Task, error)
	updateFn func(ctx context.Context, id int64, p task.UpdateParams) (task.Task, error)
	deleteFn func(ctx context.Context, id int64) error
}

var _ task.Service = stubService{}

func (s stubService) List(ctx context.Context, p task.ListParams) ([]task.Task, error) {
	if s.listFn == nil {
		s.t.Fatal("unexpected service List call")
	}

	return s.listFn(ctx, p)
}

func (s stubService) Get(ctx context.Context, id int64) (task.Task, error) {
	if s.getFn == nil {
		s.t.Fatal("unexpected service Get call")
	}

	return s.getFn(ctx, id)
}

func (s stubService) Create(ctx context.Context, p task.CreateParams) (task.Task, error) {
	if s.createFn == nil {
		s.t.Fatal("unexpected service Create call")
	}

	return s.createFn(ctx, p)
}

func (s stubService) Update(ctx context.Context, id int64, p task.UpdateParams) (task.Task, error) {
	if s.updateFn == nil {
		s.t.Fatal("unexpected service Update call")
	}

	return s.updateFn(ctx, id, p)
}

func (s stubService) Delete(ctx context.Context, id int64) error {
	if s.deleteFn == nil {
		s.t.Fatal("unexpected service Delete call")
	}

	return s.deleteFn(ctx, id)
}

// noService is a table entry for requests that must be rejected before the
// service is reached.
func noService(t *testing.T) stubService { return stubService{t: t} }

func testRouter(svc task.Service) *gin.Engine {
	return NewRouter(Deps{
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Tasks:   svc,
		Version: "test",
	})
}

func sampleTask() task.Task {
	ts := time.Date(2026, time.August, 17, 9, 0, 0, 0, time.UTC)

	return task.Task{ID: 1, Title: "write tests", Done: false, CreatedAt: ts, UpdatedAt: ts}
}

func TestTaskEndpoints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		method     string
		target     string
		body       string
		svc        func(t *testing.T) stubService
		wantStatus int
		wantBody   string // substring that must appear in the response
	}{
		{
			name:   "list returns tasks",
			method: http.MethodGet,
			target: "/api/v1/tasks",
			svc: func(t *testing.T) stubService {
				return stubService{t: t, listFn: func(_ context.Context, p task.ListParams) ([]task.Task, error) {
					return []task.Task{sampleTask()}, nil
				}}
			},
			wantStatus: http.StatusOK,
			wantBody:   `"title":"write tests"`,
		},
		{
			name:       "list rejects invalid limit",
			method:     http.MethodGet,
			target:     "/api/v1/tasks?limit=500",
			svc:        noService,
			wantStatus: http.StatusBadRequest,
			wantBody:   "invalid query parameters",
		},
		{
			name:   "get returns a task",
			method: http.MethodGet,
			target: "/api/v1/tasks/1",
			svc: func(t *testing.T) stubService {
				return stubService{t: t, getFn: func(_ context.Context, id int64) (task.Task, error) {
					if id != 1 {
						t.Fatalf("unexpected id %d", id)
					}

					return sampleTask(), nil
				}}
			},
			wantStatus: http.StatusOK,
			wantBody:   `"id":1`,
		},
		{
			name:   "get maps missing task to 404",
			method: http.MethodGet,
			target: "/api/v1/tasks/42",
			svc: func(t *testing.T) stubService {
				return stubService{t: t, getFn: func(_ context.Context, _ int64) (task.Task, error) {
					return task.Task{}, task.ErrNotFound
				}}
			},
			wantStatus: http.StatusNotFound,
			wantBody:   "task not found",
		},
		{
			name:       "get rejects non numeric id",
			method:     http.MethodGet,
			target:     "/api/v1/tasks/abc",
			svc:        noService,
			wantStatus: http.StatusBadRequest,
			wantBody:   "positive integer",
		},
		{
			name:   "create returns 201",
			method: http.MethodPost,
			target: "/api/v1/tasks",
			body:   `{"title":"write tests"}`,
			svc: func(t *testing.T) stubService {
				return stubService{t: t, createFn: func(_ context.Context, p task.CreateParams) (task.Task, error) {
					if p.Title != "write tests" {
						t.Fatalf("unexpected title %q", p.Title)
					}

					return sampleTask(), nil
				}}
			},
			wantStatus: http.StatusCreated,
			wantBody:   `"title":"write tests"`,
		},
		{
			name:       "create rejects empty title",
			method:     http.MethodPost,
			target:     "/api/v1/tasks",
			body:       `{"title":""}`,
			svc:        noService,
			wantStatus: http.StatusBadRequest,
			wantBody:   "invalid request body",
		},
		{
			name:   "create maps blank title to 400",
			method: http.MethodPost,
			target: "/api/v1/tasks",
			body:   `{"title":"   "}`,
			svc: func(t *testing.T) stubService {
				return stubService{t: t, createFn: func(_ context.Context, _ task.CreateParams) (task.Task, error) {
					return task.Task{}, task.ErrInvalidTitle
				}}
			},
			wantStatus: http.StatusBadRequest,
			wantBody:   `{"error":"task title must not be blank"}`,
		},
		{
			name:   "create maps store failure to 500",
			method: http.MethodPost,
			target: "/api/v1/tasks",
			body:   `{"title":"boom"}`,
			svc: func(t *testing.T) stubService {
				return stubService{t: t, createFn: func(_ context.Context, _ task.CreateParams) (task.Task, error) {
					return task.Task{}, errors.New("connection refused")
				}}
			},
			wantStatus: http.StatusInternalServerError,
			wantBody:   "internal server error",
		},
		{
			name:   "update returns the stored task",
			method: http.MethodPut,
			target: "/api/v1/tasks/1",
			body:   `{"title":"done writing","done":true}`,
			svc: func(t *testing.T) stubService {
				return stubService{t: t, updateFn: func(_ context.Context, id int64, p task.UpdateParams) (task.Task, error) {
					tk := sampleTask()
					tk.Title, tk.Done = p.Title, p.Done

					return tk, nil
				}}
			},
			wantStatus: http.StatusOK,
			wantBody:   `"done":true`,
		},
		{
			name:   "update maps blank title to 400",
			method: http.MethodPut,
			target: "/api/v1/tasks/1",
			body:   `{"title":"   ","done":false}`,
			svc: func(t *testing.T) stubService {
				return stubService{t: t, updateFn: func(_ context.Context, _ int64, _ task.UpdateParams) (task.Task, error) {
					return task.Task{}, task.ErrInvalidTitle
				}}
			},
			wantStatus: http.StatusBadRequest,
			wantBody:   `{"error":"task title must not be blank"}`,
		},
		{
			name:   "delete returns 204",
			method: http.MethodDelete,
			target: "/api/v1/tasks/1",
			svc: func(t *testing.T) stubService {
				return stubService{t: t, deleteFn: func(_ context.Context, _ int64) error {
					return nil
				}}
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name:   "delete maps missing task to 404",
			method: http.MethodDelete,
			target: "/api/v1/tasks/9",
			svc: func(t *testing.T) stubService {
				return stubService{t: t, deleteFn: func(_ context.Context, _ int64) error {
					return task.ErrNotFound
				}}
			},
			wantStatus: http.StatusNotFound,
			wantBody:   "task not found",
		},
		{
			name:       "unknown route returns 404 json",
			method:     http.MethodGet,
			target:     "/api/v1/nope",
			svc:        noService,
			wantStatus: http.StatusNotFound,
			wantBody:   "route not found",
		},
		{
			name:       "wrong method returns 405",
			method:     http.MethodPatch,
			target:     "/api/v1/tasks",
			svc:        noService,
			wantStatus: http.StatusMethodNotAllowed,
			wantBody:   "method not allowed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var body io.Reader
			if tc.body != "" {
				body = strings.NewReader(tc.body)
			}
			req := httptest.NewRequestWithContext(t.Context(), tc.method, tc.target, body)
			req.Header.Set("Content-Type", "application/json")

			rec := httptest.NewRecorder()
			testRouter(tc.svc(t)).ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantBody != "" && !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Errorf("body %q does not contain %q", rec.Body.String(), tc.wantBody)
			}
			if rec.Header().Get(requestIDHeader) == "" {
				t.Error("expected a request id header on every response")
			}
		})
	}
}

func TestCreateTaskResponseShape(t *testing.T) {
	t.Parallel()

	svc := stubService{t: t, createFn: func(_ context.Context, _ task.CreateParams) (task.Task, error) {
		return sampleTask(), nil
	}}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/tasks",
		strings.NewReader(`{"title":"write tests"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	testRouter(svc).ServeHTTP(rec, req)

	var got struct {
		Data task.Task `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Data != sampleTask() {
		t.Errorf("data = %+v, want %+v", got.Data, sampleTask())
	}
	if loc := rec.Header().Get("Location"); loc != "/api/v1/tasks/1" {
		t.Errorf("Location = %q, want /api/v1/tasks/1", loc)
	}
}
