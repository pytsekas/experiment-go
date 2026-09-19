// Package xmlfmt holds the concrete document parsers. Each file implements
// consumption.Parser for one format and registers itself against the root
// element it understands, so adding a format touches nothing else.
package xmlfmt

import (
	"context"
	"encoding/xml"
	"fmt"
	"time"

	"github.com/pytsekas/experiment-go/internal/consumption"
)

// ESMPNamespace is the ENTSO-E generation-and-load document namespace.
const ESMPNamespace = "urn:iec62325.351:tc57wg16:451-6:generationloaddocument:3:0"

// defaultBatchRows is used when NewESMP is given a non-positive size.
const defaultBatchRows = 5000

// maxReadingsPerDocument stops a crafted document from driving unbounded work.
const maxReadingsPerDocument = 5_000_000

// ESMP parses ENTSO-E ESMP market documents as a stream: it decodes one
// TimeSeries at a time and never materialises the whole document.
//
// It is the reference implementation of consumption.Parser. Its structure —
// a repeating Point list under a Period with an ISO-8601 resolution — is
// representative of the other candidate metering formats.
type ESMP struct {
	batchRows int
}

var _ consumption.Parser = ESMP{}

// NewESMP returns a parser emitting batches of at most batchRows readings.
func NewESMP(batchRows int) ESMP {
	if batchRows <= 0 {
		batchRows = defaultBatchRows
	}

	return ESMP{batchRows: batchRows}
}

// Register binds the parser to the ESMP root element.
func (p ESMP) Register(r *consumption.Registry) {
	r.Register(ESMPNamespace, "GL_MarketDocument", p)
}

// esmpSeries mirrors the subset of a TimeSeries this service needs. Decoding
// one series at a time keeps memory proportional to a series, not a document.
type esmpSeries struct {
	MRID         string `xml:"mRID"`
	BusinessType string `xml:"businessType"`
	Unit         string `xml:"quantity_Measure_Unit.name"`
	Point        struct {
		MRID string `xml:"mRID"`
	} `xml:"MarketEvaluationPoint"`
	Periods []struct {
		TimeInterval struct {
			Start string `xml:"start"`
		} `xml:"timeInterval"`
		Resolution string `xml:"resolution"`
		Points     []struct {
			Position int     `xml:"position"`
			Quantity float64 `xml:"quantity"`
			Quality  string  `xml:"quality"`
		} `xml:"Point"`
	} `xml:"Period"`
}

// Parse implements consumption.Parser.
func (p ESMP) Parse(ctx context.Context, d *xml.Decoder, _ xml.StartElement, emit func([]consumption.Reading) error) error {
	batch := make([]consumption.Reading, 0, p.batchRows)
	total := 0

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := emit(batch); err != nil {
			return err
		}
		batch = batch[:0]

		return nil
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		tok, err := d.Token()
		if err != nil {
			// io.EOF without a closing root element is a truncated document.
			return fmt.Errorf("%w: reading document: %w", consumption.ErrMalformed, err)
		}

		start, ok := tok.(xml.StartElement)
		switch {
		case !ok:
			if end, isEnd := tok.(xml.EndElement); isEnd && end.Name.Local == "GL_MarketDocument" {
				return flush()
			}

			continue
		case start.Name.Local != "TimeSeries":
			continue
		}

		var series esmpSeries
		if err := d.DecodeElement(&series, &start); err != nil {
			return fmt.Errorf("%w: decoding TimeSeries: %w", consumption.ErrMalformed, err)
		}

		if err := p.appendSeries(series, &batch, &total, flush); err != nil {
			return err
		}
	}
}

// appendSeries turns one decoded series into readings, flushing whenever the
// batch is full.
func (p ESMP) appendSeries(s esmpSeries, batch *[]consumption.Reading, total *int, flush func() error) error {
	meter := s.Point.MRID
	if meter == "" {
		meter = s.MRID
	}

	for _, period := range s.Periods {
		startTime, err := time.Parse(time.RFC3339, normaliseInstant(period.TimeInterval.Start))
		if err != nil {
			return fmt.Errorf("%w: period start %q: %w", consumption.ErrMalformed, period.TimeInterval.Start, err)
		}

		resolution, err := parseResolution(period.Resolution)
		if err != nil {
			return err
		}

		for _, point := range period.Points {
			if point.Position < 1 {
				return fmt.Errorf("%w: point position %d must be positive", consumption.ErrMalformed, point.Position)
			}

			*total++
			if *total > maxReadingsPerDocument {
				return fmt.Errorf("%w: more than %d readings in one document", consumption.ErrMalformed, maxReadingsPerDocument)
			}

			*batch = append(*batch, consumption.Reading{
				MeteringPointID: meter,
				Start:           startTime.Add(time.Duration(point.Position-1) * resolution),
				Resolution:      resolution,
				Value:           point.Quantity,
				Unit:            s.Unit,
				Quality:         quality(point.Quality),
				Direction:       direction(s.BusinessType),
			})

			if len(*batch) >= p.batchRows {
				if err := flush(); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// normaliseInstant turns the minute-precision instants ESMP uses
// ("2026-01-01T00:00Z") into something time.RFC3339 accepts.
func normaliseInstant(s string) string {
	if len(s) == len("2006-01-02T15:04Z") && s[len(s)-1] == 'Z' {
		return s[:len(s)-1] + ":00Z"
	}

	return s
}

// parseResolution maps the resolutions ESMP allows. A closed set is both
// faster and stricter than a general ISO-8601 duration parser: anything else
// is a document this service must not guess at.
func parseResolution(s string) (time.Duration, error) {
	switch s {
	case "PT15M":
		return 15 * time.Minute, nil
	case "PT30M":
		return 30 * time.Minute, nil
	case "PT60M", "PT1H":
		return time.Hour, nil
	case "P1D":
		return 24 * time.Hour, nil
	case "P7D":
		return 7 * 24 * time.Hour, nil
	}

	return 0, fmt.Errorf("%w: unsupported resolution %q", consumption.ErrMalformed, s)
}

// quality maps ESMP quality codes onto the canonical vocabulary.
func quality(code string) string {
	switch code {
	case "A02":
		return consumption.QualityMissing
	case "A03":
		return consumption.QualityEstimated
	default: // A01 adjusted, A04 as provided, or absent
		return consumption.QualityMeasured
	}
}

// direction maps ESMP business types onto the canonical vocabulary. A01 is
// production; everything else in a consumption feed is consumption.
func direction(businessType string) string {
	if businessType == "A01" {
		return consumption.DirectionProduction
	}

	return consumption.DirectionConsumption
}
