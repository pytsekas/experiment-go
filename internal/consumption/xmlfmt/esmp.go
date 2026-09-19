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

// esmpMarketEvaluationPoint mirrors the small MarketEvaluationPoint child of
// a TimeSeries. It is bounded, so decoding it whole is fine.
type esmpMarketEvaluationPoint struct {
	MRID string `xml:"mRID"`
}

// esmpTimeInterval mirrors a Period's timeInterval. Only Start is used.
type esmpTimeInterval struct {
	Start string `xml:"start"`
}

// esmpPoint mirrors one Point. It is decoded and turned into a reading one
// at a time, so a Period's point count never bounds memory use.
type esmpPoint struct {
	Position int     `xml:"position"`
	Quantity float64 `xml:"quantity"`
	Quality  string  `xml:"quality"`
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

		if err := p.parseTimeSeries(d, &batch, &total, flush); err != nil {
			return err
		}
	}
}

// parseTimeSeries streams one TimeSeries element's children. Its scalar
// fields (mRID, businessType, unit, MarketEvaluationPoint) are bounded and
// held in memory; Period is handed off to parsePeriod so that a series'
// Points never have to be materialised as a whole to be counted or batched.
func (p ESMP) parseTimeSeries(d *xml.Decoder, batch *[]consumption.Reading, total *int, flush func() error) error {
	var mrid, businessType, unit, meter string

	for {
		tok, err := d.Token()
		if err != nil {
			return fmt.Errorf("%w: decoding TimeSeries: %w", consumption.ErrMalformed, err)
		}

		switch t := tok.(type) {
		case xml.EndElement:
			if t.Name.Local == "TimeSeries" {
				return nil
			}
		case xml.StartElement:
			switch t.Name.Local {
			case "mRID":
				if err := d.DecodeElement(&mrid, &t); err != nil {
					return fmt.Errorf("%w: decoding TimeSeries mRID: %w", consumption.ErrMalformed, err)
				}
			case "businessType":
				if err := d.DecodeElement(&businessType, &t); err != nil {
					return fmt.Errorf("%w: decoding businessType: %w", consumption.ErrMalformed, err)
				}
			case "quantity_Measure_Unit.name":
				if err := d.DecodeElement(&unit, &t); err != nil {
					return fmt.Errorf("%w: decoding unit: %w", consumption.ErrMalformed, err)
				}
			case "MarketEvaluationPoint":
				var mep esmpMarketEvaluationPoint
				if err := d.DecodeElement(&mep, &t); err != nil {
					return fmt.Errorf("%w: decoding MarketEvaluationPoint: %w", consumption.ErrMalformed, err)
				}
				meter = mep.MRID
			case "Period":
				meterID := meter
				if meterID == "" {
					meterID = mrid
				}

				if err := p.parsePeriod(d, meterID, businessType, unit, batch, total, flush); err != nil {
					return err
				}
			default:
				if err := d.Skip(); err != nil {
					return fmt.Errorf("%w: skipping TimeSeries child %s: %w", consumption.ErrMalformed, t.Name.Local, err)
				}
			}
		}
	}
}

// parsePeriod streams one Period element's children: the small timeInterval
// and resolution scalars, then each Point decoded and turned into a reading
// one at a time, so memory stays proportional to a batch rather than to how
// many points the period holds.
func (p ESMP) parsePeriod(d *xml.Decoder, meter, businessType, unit string, batch *[]consumption.Reading, total *int, flush func() error) error {
	var (
		startTime  time.Time
		resolution time.Duration
	)

	for {
		tok, err := d.Token()
		if err != nil {
			return fmt.Errorf("%w: decoding Period: %w", consumption.ErrMalformed, err)
		}

		switch t := tok.(type) {
		case xml.EndElement:
			if t.Name.Local == "Period" {
				return nil
			}
		case xml.StartElement:
			switch t.Name.Local {
			case "timeInterval":
				var interval esmpTimeInterval
				if decErr := d.DecodeElement(&interval, &t); decErr != nil {
					return fmt.Errorf("%w: decoding timeInterval: %w", consumption.ErrMalformed, decErr)
				}

				startTime, err = time.Parse(time.RFC3339, normaliseInstant(interval.Start))
				if err != nil {
					return fmt.Errorf("%w: period start %q: %w", consumption.ErrMalformed, interval.Start, err)
				}
			case "resolution":
				var raw string
				if decErr := d.DecodeElement(&raw, &t); decErr != nil {
					return fmt.Errorf("%w: decoding resolution: %w", consumption.ErrMalformed, decErr)
				}

				resolution, err = parseResolution(raw)
				if err != nil {
					return err
				}
			case "Point":
				var point esmpPoint
				if err := d.DecodeElement(&point, &t); err != nil {
					return fmt.Errorf("%w: decoding Point: %w", consumption.ErrMalformed, err)
				}

				if err := p.appendPoint(point, meter, businessType, unit, startTime, resolution, batch, total, flush); err != nil {
					return err
				}
			default:
				if err := d.Skip(); err != nil {
					return fmt.Errorf("%w: skipping Period child %s: %w", consumption.ErrMalformed, t.Name.Local, err)
				}
			}
		}
	}
}

// appendPoint turns one decoded Point into a reading, flushing the batch
// once it reaches batchRows.
func (p ESMP) appendPoint(point esmpPoint, meter, businessType, unit string, startTime time.Time, resolution time.Duration, batch *[]consumption.Reading, total *int, flush func() error) error {
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
		Unit:            unit,
		Quality:         quality(point.Quality),
		Direction:       direction(businessType),
	})

	if len(*batch) >= p.batchRows {
		if err := flush(); err != nil {
			return err
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
