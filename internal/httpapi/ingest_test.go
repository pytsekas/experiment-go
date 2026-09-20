package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pytsekas/experiment-go/internal/consumption"
)

type stubIngest struct {
	t        *testing.T
	ingestFn func(ctx context.Context, msg consumption.Message) (int, error)
}

var _ IngestService = stubIngest{}

func (s stubIngest) Ingest(ctx context.Context, msg consumption.Message) (int, error) {
	if s.ingestFn == nil {
		s.t.Fatal("unexpected Ingest call")
	}

	return s.ingestFn(ctx, msg)
}

type stubVerifier struct{ err error }

var _ TokenVerifier = stubVerifier{}

func (s stubVerifier) Verify(context.Context, string) error { return s.err }

type stubQuarantine struct {
	calls   int
	reason  string
	payload []byte
	err     error
}

func (s *stubQuarantine) Quarantine(_ context.Context, _ string, payload []byte, reason string) error {
	s.calls++
	s.reason = reason
	s.payload = payload

	return s.err
}

func envelope(t *testing.T, id, payload string) string {
	t.Helper()

	body, err := json.Marshal(map[string]any{
		"message": map[string]any{
			"data":      base64.StdEncoding.EncodeToString([]byte(payload)),
			"messageId": id,
		},
		"subscription": "projects/p/subscriptions/s",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	return string(body)
}

func ingestRouter(t *testing.T, svc IngestService, v TokenVerifier, q Quarantiner) http.Handler {
	t.Helper()

	return NewRouter(Deps{
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Tasks:        stubService{t: t},
		EnableIngest: true,
		Ingest:       svc,
		Verifier:     v,
		Quarantine:   q,
	})
}

func postEnvelope(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/internal/pubsub/consumption", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func TestIngestSuccessReturns204(t *testing.T) {
	svc := stubIngest{t: t, ingestFn: func(_ context.Context, msg consumption.Message) (int, error) {
		if msg.ID != "m-1" {
			t.Errorf("message id: got %q, want m-1", msg.ID)
		}
		if string(msg.Payload) != "<Doc/>" {
			t.Errorf("payload: got %q", msg.Payload)
		}

		return 3, nil
	}}

	rec := postEnvelope(t, ingestRouter(t, svc, stubVerifier{}, &stubQuarantine{}), envelope(t, "m-1", "<Doc/>"))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("got %d, want 204: %s", rec.Code, rec.Body)
	}
}

func TestIngestRejectsBadToken(t *testing.T) {
	q := &stubQuarantine{}
	rec := postEnvelope(t, ingestRouter(t, stubIngest{t: t}, stubVerifier{err: errors.New("bad token")}, q), envelope(t, "m", "<Doc/>"))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", rec.Code)
	}
	if q.calls != 0 {
		t.Error("an unauthenticated payload must not be quarantined")
	}
}

func TestIngestQuarantinesPermanentFailures(t *testing.T) {
	cases := map[string]error{
		"unknown format": consumption.ErrUnknownFormat,
		"malformed":      consumption.ErrMalformed,
		"invalid":        consumption.ErrInvalidReading,
	}

	for name, sentinel := range cases {
		t.Run(name, func(t *testing.T) {
			svc := stubIngest{t: t, ingestFn: func(context.Context, consumption.Message) (int, error) {
				return 0, fmt.Errorf("parsing: %w", sentinel)
			}}
			q := &stubQuarantine{}

			rec := postEnvelope(t, ingestRouter(t, svc, stubVerifier{}, q), envelope(t, "m", "<Doc/>"))
			if rec.Code != http.StatusOK {
				t.Fatalf("got %d, want 200 so Pub/Sub stops redelivering", rec.Code)
			}
			if q.calls != 1 {
				t.Fatalf("quarantine called %d times, want 1", q.calls)
			}
			if q.reason == "" {
				t.Error("the quarantine reason must say why the payload was rejected")
			}
		})
	}
}

// TestIngestAcksEvenWhenQuarantineWriteFails covers park's own error path: if
// the quarantine write itself fails, the response must still ack (200), not
// fall back to a retry — retrying a payload the quarantine writer already
// rejected would only repeat the failure forever.
func TestIngestAcksEvenWhenQuarantineWriteFails(t *testing.T) {
	svc := stubIngest{t: t, ingestFn: func(context.Context, consumption.Message) (int, error) {
		return 0, fmt.Errorf("parsing: %w", consumption.ErrMalformed)
	}}
	q := &stubQuarantine{err: errors.New("bucket unavailable")}

	rec := postEnvelope(t, ingestRouter(t, svc, stubVerifier{}, q), envelope(t, "m", "<Doc/>"))
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: a failed quarantine write must still ack the message", rec.Code)
	}
	if q.calls != 1 {
		t.Fatalf("quarantine called %d times, want 1", q.calls)
	}
}

func TestIngestRetriesTransientFailures(t *testing.T) {
	svc := stubIngest{t: t, ingestFn: func(context.Context, consumption.Message) (int, error) {
		return 0, fmt.Errorf("append: %w", consumption.ErrTransient)
	}}
	q := &stubQuarantine{}

	rec := postEnvelope(t, ingestRouter(t, svc, stubVerifier{}, q), envelope(t, "m", "<Doc/>"))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503", rec.Code)
	}
	if q.calls != 0 {
		t.Error("a retryable payload must not be quarantined")
	}
}

func TestIngestQuarantinesUnparseableEnvelope(t *testing.T) {
	q := &stubQuarantine{}
	body := "{not json"
	rec := postEnvelope(t, ingestRouter(t, stubIngest{t: t}, stubVerifier{}, q), body)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	if q.calls != 1 {
		t.Fatalf("quarantine called %d times, want 1", q.calls)
	}
	// The body was already read in full before the envelope failed to parse,
	// so it is the only evidence of what arrived: quarantining it with a nil
	// payload would throw away the one thing worth keeping.
	if string(q.payload) != body {
		t.Fatalf("quarantined payload: got %q, want the raw request body %q", q.payload, body)
	}
}

func TestIngestRejectsOversizeBody(t *testing.T) {
	q := &stubQuarantine{}
	big := strings.Repeat("a", maxIngestBody+1)

	rec := postEnvelope(t, ingestRouter(t, stubIngest{t: t}, stubVerifier{}, q), big)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("got %d, want 413", rec.Code)
	}
	if q.calls != 0 {
		t.Error("an unread body cannot be quarantined")
	}
}

// errReadCloser is an io.ReadCloser whose Read always fails with err. It
// simulates a transport-level failure while reading the request body — a
// dropped connection, say — as distinct from the body simply being too
// large: err is never an *http.MaxBytesError.
type errReadCloser struct{ err error }

func (e errReadCloser) Read([]byte) (int, error) { return 0, e.err }
func (e errReadCloser) Close() error             { return nil }

func TestIngestRetriesBodyReadFailure(t *testing.T) {
	q := &stubQuarantine{}
	// ingestFn is left nil: stubIngest.Ingest fails the test if the service
	// is reached, since a body that was never successfully read cannot be
	// turned into a consumption.Message.
	svc := stubIngest{t: t}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/internal/pubsub/consumption", nil)
	req.Body = errReadCloser{err: errors.New("connection reset by peer")}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()

	ingestRouter(t, svc, stubVerifier{}, q).ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503 — a body-read I/O failure is transient, not a poison payload", rec.Code)
	}
	if q.calls != 0 {
		t.Error("a body that failed to read must not be quarantined: there is nothing to park")
	}
}

func TestIngestRouteAbsentWhenDisabled(t *testing.T) {
	h := NewRouter(Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Tasks: stubService{t: t}})

	rec := postEnvelope(t, h, envelope(t, "m", "<Doc/>"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404 — the API service must not expose the ingest route", rec.Code)
	}
}
