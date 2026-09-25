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

// esmpPoint mirrors one Point. It is decoded and turned into a reading one
// at a time, so a Period's point count never bounds memory use.
type esmpPoint struct {
	Position int     `xml:"position"`
	Quantity float64 `xml:"quantity"`
	Quality  string  `xml:"quality"`
}

// Parse implements consumption.Parser.
func (p ESMP) Parse(ctx context.Context, d *xml.Decoder, root xml.StartElement, emit func([]consumption.Reading) error) error {
	return streamDocument(ctx, d, root, p.batchRows, emit, p.parseTimeSeries)
}

// parseTimeSeries streams one TimeSeries element's children. Its scalar
// fields (mRID, businessType, unit, MarketEvaluationPoint) are bounded and
// held in memory; Period is handed off to parsePeriod so that a series'
// Points never have to be materialised as a whole to be counted or batched.
//
// The ESMP schema fixes this element order (the scalars, then Period), which
// is what makes single-pass streaming possible at all. A document that
// violates it is not a real ESMP document, so once a Period has been
// processed, a later scalar is rejected rather than silently accepted and
// misapplied to nothing.
func (p ESMP) parseTimeSeries(d *xml.Decoder, batch *[]consumption.Reading, total *int, flush func() error) error {
	var (
		mrid, businessType, unit, meter string
		periodSeen                      bool
	)

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
			case "mRID", "businessType", "quantity_Measure_Unit.name", "MarketEvaluationPoint":
				if periodSeen {
					return fmt.Errorf("%w: %s after Period in TimeSeries", consumption.ErrMalformed, t.Name.Local)
				}

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
				}
			case "Period":
				periodSeen = true
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
//
// Like parseTimeSeries, this depends on the ESMP schema's fixed order
// (timeInterval, resolution, then Point*): once a Point has been processed,
// a later timeInterval or resolution is rejected rather than silently
// ignored, and the first Point requires meter/unit/start/resolution to
// already be known — a document missing or misordering any of them is
// malformed, not a document to guess at.
func (p ESMP) parsePeriod(d *xml.Decoder, meter, businessType, unit string, batch *[]consumption.Reading, total *int, flush func() error) error {
	var (
		startTime          time.Time
		resolution         time.Duration
		haveStart, haveRes bool
		pointSeen          bool
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
			case "timeInterval", "resolution":
				if pointSeen {
					return fmt.Errorf("%w: %s after Point in Period", consumption.ErrMalformed, t.Name.Local)
				}

				switch t.Name.Local {
				case "timeInterval":
					var interval timeIntervalStart
					if decErr := d.DecodeElement(&interval, &t); decErr != nil {
						return fmt.Errorf("%w: decoding timeInterval: %w", consumption.ErrMalformed, decErr)
					}

					startTime, err = time.Parse(time.RFC3339, normaliseInstant(interval.Start))
					if err != nil {
						return fmt.Errorf("%w: period start %q: %w", consumption.ErrMalformed, interval.Start, err)
					}
					haveStart = true
				case "resolution":
					var raw string
					if decErr := d.DecodeElement(&raw, &t); decErr != nil {
						return fmt.Errorf("%w: decoding resolution: %w", consumption.ErrMalformed, decErr)
					}

					resolution, err = parseResolution(raw)
					if err != nil {
						return err
					}
					haveRes = true
				}
			case "Point":
				if !pointSeen {
					if err := requirePointContext(meter, unit, haveStart, haveRes); err != nil {
						return err
					}
					pointSeen = true
				}

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
	start, err := intervalStart(startTime, point.Position, resolution)
	if err != nil {
		return err
	}

	*total++
	if *total > maxReadingsPerDocument {
		return fmt.Errorf("%w: more than %d readings in one document", consumption.ErrMalformed, maxReadingsPerDocument)
	}

	*batch = append(*batch, consumption.Reading{
		MeteringPointID: meter,
		Start:           start,
		Resolution:      resolution,
		Value:           point.Quantity,
		Unit:            unit,
		Quality:         quality(point.Quality),
		Direction:       direction(businessType),
		Measure:         consumption.MeasureGross,
	})

	if len(*batch) >= p.batchRows {
		if err := flush(); err != nil {
			return err
		}
	}

	return nil
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
