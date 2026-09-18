package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func burnRouter(t *testing.T, enabled bool) *gin.Engine {
	t.Helper()

	return NewRouter(Deps{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Tasks:      stubService{t: t},
		Version:    "test",
		EnableBurn: enabled,
	})
}

func TestBurnEndpointDisabledByDefault(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	burnRouter(t, false).ServeHTTP(rec,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/burn?ms=1", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when the burn endpoint is off", rec.Code)
	}
}

func TestBurnEndpoint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		target     string
		wantStatus int
	}{
		{name: "default duration", target: "/api/v1/burn", wantStatus: http.StatusOK},
		{name: "explicit duration", target: "/api/v1/burn?ms=5", wantStatus: http.StatusOK},
		{name: "zero is allowed", target: "/api/v1/burn?ms=0", wantStatus: http.StatusOK},
		{name: "clamped to the maximum", target: "/api/v1/burn?ms=999999", wantStatus: http.StatusOK},
		{name: "negative is rejected", target: "/api/v1/burn?ms=-1", wantStatus: http.StatusBadRequest},
		{name: "garbage is rejected", target: "/api/v1/burn?ms=abc", wantStatus: http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			burnRouter(t, true).ServeHTTP(rec,
				httptest.NewRequestWithContext(t.Context(), http.MethodGet, tc.target, nil))

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus != http.StatusOK {
				return
			}

			var got struct {
				ActualMS   int64 `json:"actual_ms"`
				Iterations int64 `json:"iterations"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if got.Iterations == 0 {
				t.Error("iterations = 0, expected the spin loop to do some work")
			}
			// maxBurnDuration is 2s; allow generous slack for slow CI machines.
			if got.ActualMS > 5000 {
				t.Errorf("actual_ms = %d, expected the duration to be clamped", got.ActualMS)
			}
		})
	}
}
