package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type stubPinger struct{ err error }

func (s stubPinger) Ping(context.Context) error { return s.err }

func TestHealthEndpoints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		target     string
		db         Pinger
		wantStatus int
		wantBody   string
	}{
		{
			name:       "liveness ignores a broken database",
			target:     "/healthz",
			db:         stubPinger{err: errors.New("down")},
			wantStatus: http.StatusOK,
			wantBody:   `"status":"ok"`,
		},
		{
			name:       "readiness ok when database answers",
			target:     "/readyz",
			db:         stubPinger{},
			wantStatus: http.StatusOK,
			wantBody:   `"status":"ready"`,
		},
		{
			name:       "readiness fails when database is down",
			target:     "/readyz",
			db:         stubPinger{err: errors.New("down")},
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   `"dependency":"postgres"`,
		},
		{
			name:       "readiness ok without a configured database",
			target:     "/readyz",
			db:         nil,
			wantStatus: http.StatusOK,
			wantBody:   `"status":"ready"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := NewRouter(Deps{Tasks: stubService{t: t}, DB: tc.db, Version: "test"})
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, tc.target, nil))

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Errorf("body %q does not contain %q", rec.Body.String(), tc.wantBody)
			}
		})
	}
}
