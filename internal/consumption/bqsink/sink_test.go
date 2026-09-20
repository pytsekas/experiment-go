package bqsink

import (
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/pytsekas/experiment-go/internal/consumption"
)

func TestTableSchemaFieldsMatchTheDesign(t *testing.T) {
	want := []string{
		"metering_point_id", "interval_start", "resolution_sec", "value",
		"unit", "quality", "direction", "source_message_id", "ingested_at",
	}

	schema := TableSchema()
	if len(schema) != len(want) {
		t.Fatalf("got %d fields, want %d", len(schema), len(want))
	}
	for i, name := range want {
		if schema[i].Name != name {
			t.Errorf("field %d: got %q, want %q", i, schema[i].Name, name)
		}
		if !schema[i].Required {
			t.Errorf("field %q must be REQUIRED", name)
		}
	}
}

func TestClassifyTransient(t *testing.T) {
	// Canceled belongs here alongside the others: it fires when the request
	// context is cancelled mid-append (an ordinary Cloud Run SIGTERM during
	// scale-down), not because the payload is bad, so it must be retried
	// rather than quarantined.
	transient := []codes.Code{codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted, codes.Internal, codes.Canceled}
	for _, code := range transient {
		err := classify(status.Error(code, "boom"))
		if !errors.Is(err, consumption.ErrTransient) {
			t.Errorf("%s: got %v, want ErrTransient", code, err)
		}
	}
}

func TestClassifyPermanent(t *testing.T) {
	permanent := []codes.Code{codes.InvalidArgument, codes.NotFound, codes.PermissionDenied}
	for _, code := range permanent {
		err := classify(status.Error(code, "boom"))
		if errors.Is(err, consumption.ErrTransient) {
			t.Errorf("%s: got ErrTransient, want a permanent error", code)
		}
		if err == nil {
			t.Errorf("%s: got nil, want an error", code)
		}
	}
}

func TestClassifyNil(t *testing.T) {
	if err := classify(nil); err != nil {
		t.Fatalf("got %v, want nil", err)
	}
}

func TestReadingToValuesOrdersFieldsLikeTheSchema(t *testing.T) {
	r := consumption.Reading{
		MeteringPointID: "EE-1",
		Start:           time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Resolution:      15 * time.Minute,
		Value:           1.5,
		Unit:            "kWh",
		Quality:         consumption.QualityMeasured,
		Direction:       consumption.DirectionConsumption,
		SourceMessageID: "m-1",
		IngestedAt:      time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC),
	}

	got := readingToValues(r)
	if got["resolution_sec"] != int64(900) {
		t.Errorf("resolution_sec: got %v, want 900", got["resolution_sec"])
	}
	if got["metering_point_id"] != "EE-1" {
		t.Errorf("metering_point_id: got %v", got["metering_point_id"])
	}
	if len(got) != len(TableSchema()) {
		t.Errorf("got %d values for %d schema fields", len(got), len(TableSchema()))
	}
}

func TestJoinCloseErrorsBothNil(t *testing.T) {
	if err := joinCloseErrors(nil, nil); err != nil {
		t.Fatalf("got %v, want nil", err)
	}
}

func TestJoinCloseErrorsStreamOnly(t *testing.T) {
	streamErr := errors.New("stream boom")

	err := joinCloseErrors(streamErr, nil)
	if err == nil {
		t.Fatal("got nil, want an error")
	}
	if !errors.Is(err, streamErr) {
		t.Errorf("got %v, want it to match the stream error", err)
	}
}

func TestJoinCloseErrorsClientOnly(t *testing.T) {
	clientErr := errors.New("client boom")

	err := joinCloseErrors(nil, clientErr)
	if err == nil {
		t.Fatal("got nil, want an error")
	}
	if !errors.Is(err, clientErr) {
		t.Errorf("got %v, want it to match the client error", err)
	}
}

func TestJoinCloseErrorsBoth(t *testing.T) {
	streamErr := errors.New("stream boom")
	clientErr := errors.New("client boom")

	err := joinCloseErrors(streamErr, clientErr)
	if err == nil {
		t.Fatal("got nil, want an error")
	}
	if !errors.Is(err, streamErr) {
		t.Errorf("got %v, want it to match the stream error", err)
	}
	if !errors.Is(err, clientErr) {
		t.Errorf("got %v, want it to match the client error", err)
	}
}
