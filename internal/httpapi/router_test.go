package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pytsekas/experiment-go/internal/consumption"
)

// TestTaskRoutesAbsentWithoutTasksService covers the ingest role, which runs
// this binary without a database and therefore leaves Deps.Tasks nil. The
// /api/v1/tasks group must not be registered in that configuration, since a
// request reaching a handler holding a nil service would panic instead of
// failing cleanly.
func TestTaskRoutesAbsentWithoutTasksService(t *testing.T) {
	t.Parallel()

	r := NewRouter(Deps{
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Version: "test",
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/tasks", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when Tasks is nil (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestIngestRoleConfiguration exercises the router shape a deployment without
// a database would use: no Tasks, ingest enabled. The ingest route must keep
// working even though Tasks is nil.
func TestIngestRoleConfiguration(t *testing.T) {
	t.Parallel()

	svc := stubIngest{t: t, ingestFn: func(_ context.Context, _ consumption.Message) (int, error) {
		return 1, nil
	}}

	r := NewRouter(Deps{
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Version:      "test",
		EnableIngest: true,
		Ingest:       svc,
		Verifier:     stubVerifier{},
		Quarantine:   &stubQuarantine{},
	})

	rec := postEnvelope(t, r, envelope(t, "m-role", "<Doc/>"))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: the ingest route must work without a Tasks service (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestBurnWorksWithoutTasksService documents the decision that the burn
// endpoint is independent of Tasks: an ingest deployment that opts into the
// CPU-burn diagnostic gets it even though it has no task service at all.
func TestBurnWorksWithoutTasksService(t *testing.T) {
	t.Parallel()

	r := NewRouter(Deps{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Version:    "test",
		EnableBurn: true,
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/burn?ms=0", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: burn must not depend on Tasks (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestHealthProbesWorkWithoutTasksService confirms the health/readiness
// endpoints, which do not depend on Tasks, are unaffected by it being nil.
func TestHealthProbesWorkWithoutTasksService(t *testing.T) {
	t.Parallel()

	r := NewRouter(Deps{
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Version: "test",
	})

	for _, target := range []string{"/healthz", "/readyz"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil))

		if rec.Code != http.StatusOK {
			t.Errorf("%s status = %d, want 200 (body: %s)", target, rec.Code, rec.Body.String())
		}
	}
}
