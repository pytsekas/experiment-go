package consumption_test

import (
	"context"
	"encoding/xml"
	"errors"
	"testing"
	"time"

	"github.com/pytsekas/experiment-go/internal/consumption"
)

// stubSink is a consumption.Sink that records what it was asked to write.
type stubSink struct {
	t       *testing.T
	writeFn func(ctx context.Context, rows []consumption.Reading) error
}

var _ consumption.Sink = stubSink{}

func (s stubSink) Write(ctx context.Context, rows []consumption.Reading) error {
	if s.writeFn == nil {
		s.t.Fatal("unexpected Write call")
	}

	return s.writeFn(ctx, rows)
}

// emitting returns a parser that emits one batch built from values.
func emitting(t *testing.T, values ...float64) *stubParser {
	t.Helper()

	return &stubParser{
		t: t,
		parseFn: func(_ context.Context, _ *xml.Decoder, _ xml.StartElement, emit func([]consumption.Reading) error) error {
			batch := make([]consumption.Reading, 0, len(values))
			for _, v := range values {
				r := validReading()
				r.Value = v
				batch = append(batch, r)
			}

			return emit(batch)
		},
	}
}

func newService(t *testing.T, p consumption.Parser, sink consumption.Sink) *consumption.Service {
	t.Helper()

	reg := consumption.NewRegistry()
	reg.Register("", "Doc", p)

	return consumption.NewService(reg, sink, nil)
}

func TestServiceIngestStampsAndWrites(t *testing.T) {
	var written []consumption.Reading
	sink := stubSink{t: t, writeFn: func(_ context.Context, rows []consumption.Reading) error {
		written = append(written, rows...)

		return nil
	}}

	svc := newService(t, emitting(t, 1, 2), sink)

	n, err := svc.Ingest(context.Background(), consumption.Message{ID: "msg-7", Payload: []byte(`<Doc/>`)})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if n != 2 {
		t.Fatalf("got %d readings, want 2", n)
	}
	for _, r := range written {
		if r.SourceMessageID != "msg-7" {
			t.Errorf("SourceMessageID: got %q, want msg-7", r.SourceMessageID)
		}
		if r.IngestedAt.IsZero() {
			t.Error("IngestedAt was not stamped")
		}
	}
}

func TestServiceIngestUnknownFormat(t *testing.T) {
	svc := newService(t, &stubParser{t: t}, stubSink{t: t})

	if _, err := svc.Ingest(context.Background(), consumption.Message{ID: "m", Payload: []byte(`<Other/>`)}); !errors.Is(err, consumption.ErrUnknownFormat) {
		t.Fatalf("got %v, want ErrUnknownFormat", err)
	}
}

func TestServiceIngestRejectsInvalidReading(t *testing.T) {
	bad := &stubParser{t: t, parseFn: func(_ context.Context, _ *xml.Decoder, _ xml.StartElement, emit func([]consumption.Reading) error) error {
		r := validReading()
		r.MeteringPointID = ""

		return emit([]consumption.Reading{r})
	}}

	// The sink must never see an invalid batch, so leaving writeFn nil is the assertion.
	svc := newService(t, bad, stubSink{t: t})

	if _, err := svc.Ingest(context.Background(), consumption.Message{ID: "m", Payload: []byte(`<Doc/>`)}); !errors.Is(err, consumption.ErrInvalidReading) {
		t.Fatalf("got %v, want ErrInvalidReading", err)
	}
}

func TestServiceIngestPropagatesSinkError(t *testing.T) {
	sink := stubSink{t: t, writeFn: func(context.Context, []consumption.Reading) error {
		return consumption.ErrTransient
	}}

	svc := newService(t, emitting(t, 1), sink)

	if _, err := svc.Ingest(context.Background(), consumption.Message{ID: "m", Payload: []byte(`<Doc/>`)}); !errors.Is(err, consumption.ErrTransient) {
		t.Fatalf("got %v, want ErrTransient", err)
	}
}

func TestServiceIngestEmptyPayload(t *testing.T) {
	svc := newService(t, &stubParser{t: t}, stubSink{t: t})

	if _, err := svc.Ingest(context.Background(), consumption.Message{ID: "m"}); !errors.Is(err, consumption.ErrMalformed) {
		t.Fatalf("got %v, want ErrMalformed", err)
	}
}

func TestServiceIngestClock(t *testing.T) {
	fixed := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	var got time.Time
	sink := stubSink{t: t, writeFn: func(_ context.Context, rows []consumption.Reading) error {
		got = rows[0].IngestedAt

		return nil
	}}

	svc := newService(t, emitting(t, 1), sink)
	svc.Now = func() time.Time { return fixed }

	if _, err := svc.Ingest(context.Background(), consumption.Message{ID: "m", Payload: []byte(`<Doc/>`)}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if !got.Equal(fixed) {
		t.Fatalf("got %s, want %s", got, fixed)
	}
}
