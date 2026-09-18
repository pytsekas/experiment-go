package task_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strconv"
	"testing"

	"github.com/pytsekas/experiment-go/internal/task"
)

// stubRepo is a task.Repository whose behaviour each test sets per method.
// Calling a method the test did not configure fails the test, so a test that
// expects the repository to stay untouched catches an unexpected call.
type stubRepo struct {
	t        *testing.T
	listFn   func(ctx context.Context, p task.ListParams) ([]task.Task, error)
	getFn    func(ctx context.Context, id int64) (task.Task, error)
	createFn func(ctx context.Context, p task.CreateParams) (task.Task, error)
	updateFn func(ctx context.Context, id int64, p task.UpdateParams) (task.Task, error)
	deleteFn func(ctx context.Context, id int64) error
}

var _ task.Repository = stubRepo{}

func (s stubRepo) List(ctx context.Context, p task.ListParams) ([]task.Task, error) {
	if s.listFn == nil {
		s.t.Fatal("unexpected repository List call")
	}

	return s.listFn(ctx, p)
}

func (s stubRepo) Get(ctx context.Context, id int64) (task.Task, error) {
	if s.getFn == nil {
		s.t.Fatal("unexpected repository Get call")
	}

	return s.getFn(ctx, id)
}

func (s stubRepo) Create(ctx context.Context, p task.CreateParams) (task.Task, error) {
	if s.createFn == nil {
		s.t.Fatal("unexpected repository Create call")
	}

	return s.createFn(ctx, p)
}

func (s stubRepo) Update(ctx context.Context, id int64, p task.UpdateParams) (task.Task, error) {
	if s.updateFn == nil {
		s.t.Fatal("unexpected repository Update call")
	}

	return s.updateFn(ctx, id, p)
}

func (s stubRepo) Delete(ctx context.Context, id int64) error {
	if s.deleteFn == nil {
		s.t.Fatal("unexpected repository Delete call")
	}

	return s.deleteFn(ctx, id)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestServiceCreateTrimsTitle(t *testing.T) {
	t.Parallel()

	var stored task.CreateParams
	repo := stubRepo{t: t, createFn: func(_ context.Context, p task.CreateParams) (task.Task, error) {
		stored = p

		return task.Task{ID: 7, Title: p.Title}, nil
	}}

	got, err := task.NewService(repo, discardLogger()).Create(t.Context(), task.CreateParams{Title: "  write tests\t"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if stored.Title != "write tests" {
		t.Errorf("repository received title %q, want %q", stored.Title, "write tests")
	}
	if got.ID != 7 || got.Title != "write tests" {
		t.Errorf("Create returned %+v, want ID 7 and title %q", got, "write tests")
	}
}

func TestServiceCreateRejectsBlankTitle(t *testing.T) {
	t.Parallel()

	for _, title := range []string{"", "   ", "\t\n"} {
		t.Run("title="+strconv.Quote(title), func(t *testing.T) {
			t.Parallel()

			// No createFn: reaching the repository fails the test.
			repo := stubRepo{t: t}

			_, err := task.NewService(repo, discardLogger()).Create(t.Context(), task.CreateParams{Title: title})
			if !errors.Is(err, task.ErrInvalidTitle) {
				t.Fatalf("Create error = %v, want task.ErrInvalidTitle", err)
			}
		})
	}
}

// TestServiceForwardsToRepository checks that every operation reaches the
// repository with the expected arguments and hands back the repository's result
// and error unchanged. Title normalisation on Create and Update is covered by
// the dedicated tests above; the rows here use already-normalised titles.
func TestServiceForwardsToRepository(t *testing.T) {
	t.Parallel()

	sample := task.Task{ID: 3, Title: "ship it", Done: true}
	repoErr := errors.New("connection refused")

	tests := []struct {
		name    string
		repo    func(t *testing.T) stubRepo
		call    func(ctx context.Context, svc task.Service) (any, error)
		want    any
		wantErr error
	}{
		{
			name: "List forwards paging params",
			repo: func(t *testing.T) stubRepo {
				return stubRepo{t: t, listFn: func(_ context.Context, p task.ListParams) ([]task.Task, error) {
					if p != (task.ListParams{Limit: 5, Offset: 10}) {
						t.Errorf("repository received %+v, want Limit 5 Offset 10", p)
					}

					return []task.Task{sample}, nil
				}}
			},
			call: func(ctx context.Context, svc task.Service) (any, error) {
				return svc.List(ctx, task.ListParams{Limit: 5, Offset: 10})
			},
			want: []task.Task{sample},
		},
		{
			name: "List returns repository error",
			repo: func(t *testing.T) stubRepo {
				return stubRepo{t: t, listFn: func(context.Context, task.ListParams) ([]task.Task, error) {
					return nil, repoErr
				}}
			},
			call: func(ctx context.Context, svc task.Service) (any, error) {
				return svc.List(ctx, task.ListParams{})
			},
			want:    []task.Task(nil),
			wantErr: repoErr,
		},
		{
			name: "Get forwards id",
			repo: func(t *testing.T) stubRepo {
				return stubRepo{t: t, getFn: func(_ context.Context, id int64) (task.Task, error) {
					if id != 3 {
						t.Errorf("repository received id %d, want 3", id)
					}

					return sample, nil
				}}
			},
			call: func(ctx context.Context, svc task.Service) (any, error) {
				return svc.Get(ctx, 3)
			},
			want: sample,
		},
		{
			name: "Get returns not found unchanged",
			repo: func(t *testing.T) stubRepo {
				return stubRepo{t: t, getFn: func(context.Context, int64) (task.Task, error) {
					return task.Task{}, task.ErrNotFound
				}}
			},
			call: func(ctx context.Context, svc task.Service) (any, error) {
				return svc.Get(ctx, 99)
			},
			want:    task.Task{},
			wantErr: task.ErrNotFound,
		},
		{
			name: "Create returns repository error",
			repo: func(t *testing.T) stubRepo {
				return stubRepo{t: t, createFn: func(context.Context, task.CreateParams) (task.Task, error) {
					return task.Task{}, repoErr
				}}
			},
			call: func(ctx context.Context, svc task.Service) (any, error) {
				return svc.Create(ctx, task.CreateParams{Title: "boom"})
			},
			want:    task.Task{},
			wantErr: repoErr,
		},
		{
			name: "Update forwards id and params",
			repo: func(t *testing.T) stubRepo {
				return stubRepo{t: t, updateFn: func(_ context.Context, id int64, p task.UpdateParams) (task.Task, error) {
					if id != 3 || p != (task.UpdateParams{Title: "ship it", Done: true}) {
						t.Errorf("repository received id %d params %+v, want id 3 title %q done true", id, p, "ship it")
					}

					return sample, nil
				}}
			},
			call: func(ctx context.Context, svc task.Service) (any, error) {
				return svc.Update(ctx, 3, task.UpdateParams{Title: "ship it", Done: true})
			},
			want: sample,
		},
		{
			name: "Update returns not found unchanged",
			repo: func(t *testing.T) stubRepo {
				return stubRepo{t: t, updateFn: func(context.Context, int64, task.UpdateParams) (task.Task, error) {
					return task.Task{}, task.ErrNotFound
				}}
			},
			call: func(ctx context.Context, svc task.Service) (any, error) {
				return svc.Update(ctx, 99, task.UpdateParams{Title: "x"})
			},
			want:    task.Task{},
			wantErr: task.ErrNotFound,
		},
		// Delete has no result, so for the two Delete rows only the error
		// assertion carries weight; want stays nil.
		{
			name: "Delete forwards id",
			repo: func(t *testing.T) stubRepo {
				return stubRepo{t: t, deleteFn: func(_ context.Context, id int64) error {
					if id != 3 {
						t.Errorf("repository received id %d, want 3", id)
					}

					return nil
				}}
			},
			call: func(ctx context.Context, svc task.Service) (any, error) {
				return nil, svc.Delete(ctx, 3)
			},
			want: nil,
		},
		{
			name: "Delete returns not found unchanged",
			repo: func(t *testing.T) stubRepo {
				return stubRepo{t: t, deleteFn: func(context.Context, int64) error {
					return task.ErrNotFound
				}}
			},
			call: func(ctx context.Context, svc task.Service) (any, error) {
				return nil, svc.Delete(ctx, 99)
			},
			want:    nil,
			wantErr: task.ErrNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc := task.NewService(tc.repo(t), discardLogger())
			got, err := tc.call(t.Context(), svc)

			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("result = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestServiceCreateLogsBusinessEvent(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	repo := stubRepo{t: t, createFn: func(_ context.Context, p task.CreateParams) (task.Task, error) {
		return task.Task{ID: 7, Title: p.Title}, nil
	}}

	if _, err := task.NewService(repo, log).Create(t.Context(), task.CreateParams{Title: "write tests"}); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	var record struct {
		Level  string `json:"level"`
		TaskID int64  `json:"task_id"`
	}
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("expected exactly one JSON log record, got %q: %v", buf.String(), err)
	}
	if record.Level != "INFO" {
		t.Errorf("log level = %q, want INFO", record.Level)
	}
	if record.TaskID != 7 {
		t.Errorf("log task_id = %d, want 7", record.TaskID)
	}
}

func TestServiceCreateWithNilLoggerDoesNotPanic(t *testing.T) {
	t.Parallel()

	repo := stubRepo{t: t, createFn: func(_ context.Context, p task.CreateParams) (task.Task, error) {
		return task.Task{ID: 1, Title: p.Title}, nil
	}}

	if _, err := task.NewService(repo, nil).Create(t.Context(), task.CreateParams{Title: "no logger"}); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
}

func TestServiceUpdateTrimsTitle(t *testing.T) {
	t.Parallel()

	var stored task.UpdateParams
	repo := stubRepo{t: t, updateFn: func(_ context.Context, _ int64, p task.UpdateParams) (task.Task, error) {
		stored = p

		return task.Task{ID: 3, Title: p.Title, Done: p.Done}, nil
	}}

	got, err := task.NewService(repo, discardLogger()).Update(t.Context(), 3, task.UpdateParams{Title: "  ship it ", Done: true})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if stored != (task.UpdateParams{Title: "ship it", Done: true}) {
		t.Errorf("repository received %+v, want title %q done true", stored, "ship it")
	}
	if got.Title != "ship it" {
		t.Errorf("Update returned title %q, want %q", got.Title, "ship it")
	}
}

func TestServiceUpdateRejectsBlankTitle(t *testing.T) {
	t.Parallel()

	for _, title := range []string{"", "   ", "\t\n"} {
		t.Run("title="+strconv.Quote(title), func(t *testing.T) {
			t.Parallel()

			// No updateFn: reaching the repository fails the test.
			repo := stubRepo{t: t}

			_, err := task.NewService(repo, discardLogger()).Update(t.Context(), 3, task.UpdateParams{Title: title})
			if !errors.Is(err, task.ErrInvalidTitle) {
				t.Fatalf("Update error = %v, want task.ErrInvalidTitle", err)
			}
		})
	}
}

func TestServiceCreateDoesNotLogWhenRepositoryFails(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	repo := stubRepo{t: t, createFn: func(context.Context, task.CreateParams) (task.Task, error) {
		return task.Task{}, errors.New("connection refused")
	}}

	if _, err := task.NewService(repo, log).Create(t.Context(), task.CreateParams{Title: "write tests"}); err == nil {
		t.Fatal("Create returned nil error, want the repository error")
	}
	if buf.Len() != 0 {
		t.Errorf("expected no log output after a failed insert, got %q", buf.String())
	}
}
