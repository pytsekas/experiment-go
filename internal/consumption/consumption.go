// Package consumption contains the energy-consumption ingest domain: the
// canonical reading, the contracts for parsing documents and storing readings,
// and the service that joins them.
package consumption

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// Reading quality values, normalised away from whatever the source called them.
const (
	QualityMeasured  = "measured"
	QualityEstimated = "estimated"
	QualityMissing   = "missing"
)

// Reading direction values.
const (
	DirectionConsumption = "consumption"
	DirectionProduction  = "production"
)

// ErrUnknownFormat means no parser is registered for a document's root element.
var ErrUnknownFormat = errors.New("unknown document format")

// ErrMalformed means a document is not well-formed or is missing required data.
var ErrMalformed = errors.New("malformed document")

// ErrInvalidReading means a reading violates a domain rule.
var ErrInvalidReading = errors.New("invalid reading")

// ErrTransient marks a failure that may succeed if the same work is retried.
// Sinks wrap their retryable errors with it and the transport maps it to 503.
var ErrTransient = errors.New("transient failure")

// Reading is one metered interval, normalised away from the source format.
type Reading struct {
	MeteringPointID string
	Start           time.Time
	Resolution      time.Duration
	Value           float64
	Unit            string
	Quality         string
	Direction       string
	SourceMessageID string
	IngestedAt      time.Time
}

// Validate reports whether the reading satisfies the domain rules. It returns
// an error wrapping ErrInvalidReading so the transport can map it without
// knowing which rule failed.
func (r Reading) Validate() error {
	switch {
	case strings.TrimSpace(r.MeteringPointID) == "":
		return fmt.Errorf("%w: blank metering point", ErrInvalidReading)
	case r.Start.IsZero():
		return fmt.Errorf("%w: zero interval start", ErrInvalidReading)
	case r.Resolution <= 0:
		return fmt.Errorf("%w: resolution %s must be positive", ErrInvalidReading, r.Resolution)
	case math.IsNaN(r.Value) || math.IsInf(r.Value, 0):
		return fmt.Errorf("%w: value is not finite", ErrInvalidReading)
	case r.Unit == "":
		return fmt.Errorf("%w: blank unit", ErrInvalidReading)
	}

	return nil
}

// Message is one delivery from the messaging layer, independent of Pub/Sub.
type Message struct {
	ID         string
	Payload    []byte
	Attributes map[string]string
}

// Parser turns one document into batches of readings.
type Parser interface {
	// Parse consumes the document whose root element has already been read
	// from d. It calls emit once per batch; implementations must not retain
	// the slice after emit returns, and must stop and return emit's error.
	Parse(ctx context.Context, d *xml.Decoder, root xml.StartElement, emit func([]Reading) error) error
}

// Sink stores a batch of readings. Implementations wrap retryable failures
// with ErrTransient and return everything else unwrapped.
type Sink interface {
	Write(ctx context.Context, rows []Reading) error
}
