package xmlfmt_test

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pytsekas/experiment-go/internal/consumption"
	"github.com/pytsekas/experiment-go/internal/consumption/xmlfmt"
)

// parseFile runs the ESMP parser over a testdata file and returns every
// reading it emitted, plus the batch sizes it used.
func parseFile(t *testing.T, name string, batchRows int) ([]consumption.Reading, []int, error) {
	t.Helper()

	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	reg := consumption.NewRegistry()
	xmlfmt.NewESMP(batchRows).Register(reg)

	d := xml.NewDecoder(f)
	p, root, err := reg.For(d)
	if err != nil {
		return nil, nil, err
	}

	var (
		all   []consumption.Reading
		sizes []int
	)
	err = p.Parse(context.Background(), d, root, func(batch []consumption.Reading) error {
		sizes = append(sizes, len(batch))
		all = append(all, batch...)

		return nil
	})

	return all, sizes, err
}

func TestESMPParseValid(t *testing.T) {
	got, _, err := parseFile(t, "esmp-valid.xml", 5000)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) != 6 {
		t.Fatalf("got %d readings, want 6", len(got))
	}

	first := got[0]
	want := consumption.Reading{
		MeteringPointID: "EE-METER-1",
		Start:           time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Resolution:      15 * time.Minute,
		Value:           10.5,
		Unit:            "KWH",
		Quality:         consumption.QualityMeasured,
		Direction:       consumption.DirectionConsumption,
	}
	if first != want {
		t.Fatalf("first reading:\n got %+v\nwant %+v", first, want)
	}

	// Position 2 carries quality A03 and sits one resolution after the start.
	if got[1].Quality != consumption.QualityEstimated {
		t.Errorf("quality: got %q, want estimated", got[1].Quality)
	}
	if want := time.Date(2026, 1, 1, 0, 15, 0, 0, time.UTC); !got[1].Start.Equal(want) {
		t.Errorf("start: got %s, want %s", got[1].Start, want)
	}

	// The second series is production at hourly resolution.
	if got[4].Direction != consumption.DirectionProduction {
		t.Errorf("direction: got %q, want production", got[4].Direction)
	}
	if got[5].Resolution != time.Hour {
		t.Errorf("resolution: got %s, want 1h", got[5].Resolution)
	}
}

func TestESMPParseBatches(t *testing.T) {
	_, sizes, err := parseFile(t, "esmp-valid.xml", 4)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// Six readings at four per batch, and a series boundary flushes: the
	// parser must never hand out more than batchRows at a time.
	for _, n := range sizes {
		if n > 4 {
			t.Fatalf("batch of %d exceeds batchRows=4 (sizes %v)", n, sizes)
		}
	}
}

// TestESMPParseLargeSingleSeriesBatches proves that a single TimeSeries with
// far more points than batchRows is still streamed in bounded batches — the
// regression this guards against decoded a whole TimeSeries (all its Points)
// into memory before ever looking at batchRows, so this document shape is
// exactly the one that used to defeat batching. The document is generated
// here rather than committed as a fixture, since its size is the point.
func TestESMPParseLargeSingleSeriesBatches(t *testing.T) {
	const batchRows = 100
	const points = 2*batchRows + 37 // deliberately not a multiple of batchRows

	var doc strings.Builder
	doc.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	doc.WriteString(`<GL_MarketDocument xmlns="` + xmlfmt.ESMPNamespace + `">`)
	doc.WriteString(`<mRID>doc-big</mRID><TimeSeries><mRID>ts-big</mRID><businessType>A04</businessType>`)
	doc.WriteString(`<quantity_Measure_Unit.name>KWH</quantity_Measure_Unit.name>`)
	doc.WriteString(`<MarketEvaluationPoint><mRID>EE-METER-BIG</mRID></MarketEvaluationPoint>`)
	doc.WriteString(`<Period><timeInterval><start>2026-01-01T00:00Z</start><end>2026-01-02T00:00Z</end></timeInterval>`)
	doc.WriteString(`<resolution>PT15M</resolution>`)
	for i := 1; i <= points; i++ {
		fmt.Fprintf(&doc, "<Point><position>%d</position><quantity>1</quantity></Point>", i)
	}
	doc.WriteString(`</Period></TimeSeries></GL_MarketDocument>`)

	reg := consumption.NewRegistry()
	xmlfmt.NewESMP(batchRows).Register(reg)

	d := xml.NewDecoder(strings.NewReader(doc.String()))
	p, root, err := reg.For(d)
	if err != nil {
		t.Fatalf("For: %v", err)
	}

	var (
		total int
		sizes []int
	)
	err = p.Parse(context.Background(), d, root, func(batch []consumption.Reading) error {
		sizes = append(sizes, len(batch))
		total += len(batch)

		return nil
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if total != points {
		t.Fatalf("got %d readings, want %d", total, points)
	}
	if len(sizes) < 2 {
		t.Fatalf("expected multiple batches for %d points at batchRows=%d, got sizes %v", points, batchRows, sizes)
	}
	for _, n := range sizes {
		if n > batchRows {
			t.Fatalf("batch of %d exceeds batchRows=%d (sizes %v)", n, batchRows, sizes)
		}
	}
}

func TestESMPParseMalformed(t *testing.T) {
	for _, name := range []string{"esmp-bad-resolution.xml", "esmp-truncated.xml"} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := parseFile(t, name, 5000); !errors.Is(err, consumption.ErrMalformed) {
				t.Fatalf("got %v, want ErrMalformed", err)
			}
		})
	}
}

// parseDoc runs the ESMP parser over an in-memory XML document, for tests
// that need a document shape not worth committing as a testdata fixture.
func parseDoc(t *testing.T, doc string, batchRows int) ([]consumption.Reading, error) {
	t.Helper()

	reg := consumption.NewRegistry()
	xmlfmt.NewESMP(batchRows).Register(reg)

	d := xml.NewDecoder(strings.NewReader(doc))
	p, root, err := reg.For(d)
	if err != nil {
		return nil, err
	}

	var all []consumption.Reading
	err = p.Parse(context.Background(), d, root, func(batch []consumption.Reading) error {
		all = append(all, batch...)

		return nil
	})

	return all, err
}

// TestESMPParseOutOfOrderIsMalformed covers the two ways a streamed decode
// can be led astray by an out-of-order document: a series-level scalar
// arriving after the Period that needed it, and a Period-level scalar
// arriving after the Point that needed it. The ESMP schema fixes this
// order, so a document that doesn't follow it is rejected rather than
// silently misparsed (e.g. an A01 production series read as consumption
// because businessType was never seen before Period ran).
func TestESMPParseOutOfOrderIsMalformed(t *testing.T) {
	tests := map[string]string{
		"businessType after Period": `<?xml version="1.0" encoding="UTF-8"?>
<GL_MarketDocument xmlns="` + xmlfmt.ESMPNamespace + `">
  <mRID>doc-1</mRID>
  <TimeSeries>
    <mRID>ts-1</mRID>
    <quantity_Measure_Unit.name>KWH</quantity_Measure_Unit.name>
    <MarketEvaluationPoint><mRID>EE-METER-1</mRID></MarketEvaluationPoint>
    <Period>
      <timeInterval><start>2026-01-01T00:00Z</start><end>2026-01-01T01:00Z</end></timeInterval>
      <resolution>PT15M</resolution>
      <Point><position>1</position><quantity>10.5</quantity></Point>
    </Period>
    <businessType>A01</businessType>
  </TimeSeries>
</GL_MarketDocument>`,
		"resolution after first Point": `<?xml version="1.0" encoding="UTF-8"?>
<GL_MarketDocument xmlns="` + xmlfmt.ESMPNamespace + `">
  <mRID>doc-1</mRID>
  <TimeSeries>
    <mRID>ts-1</mRID>
    <businessType>A04</businessType>
    <quantity_Measure_Unit.name>KWH</quantity_Measure_Unit.name>
    <MarketEvaluationPoint><mRID>EE-METER-1</mRID></MarketEvaluationPoint>
    <Period>
      <timeInterval><start>2026-01-01T00:00Z</start><end>2026-01-01T01:00Z</end></timeInterval>
      <Point><position>1</position><quantity>10.5</quantity></Point>
      <resolution>PT15M</resolution>
    </Period>
  </TimeSeries>
</GL_MarketDocument>`,
	}

	for name, doc := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := parseDoc(t, doc, 5000); !errors.Is(err, consumption.ErrMalformed) {
				t.Fatalf("got %v, want ErrMalformed", err)
			}
		})
	}
}

func TestESMPParseStopsOnEmitError(t *testing.T) {
	f, err := os.Open("testdata/esmp-valid.xml")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	reg := consumption.NewRegistry()
	xmlfmt.NewESMP(1).Register(reg)
	d := xml.NewDecoder(f)
	p, root, err := reg.For(d)
	if err != nil {
		t.Fatalf("For: %v", err)
	}

	sentinel := errors.New("sink is down")
	calls := 0
	err = p.Parse(context.Background(), d, root, func([]consumption.Reading) error {
		calls++

		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("got %v, want the emit error", err)
	}
	if calls != 1 {
		t.Fatalf("emit called %d times, want 1 — the parser must stop at the first failure", calls)
	}
}

// TestESMPParseCancelledContextIsTransient covers a SIGTERM arriving during
// Cloud Run scale-down: a cancelled context must produce a transient error so
// the handler answers 503 and Pub/Sub redelivers, rather than quarantining a
// document that was never actually malformed.
func TestESMPParseCancelledContextIsTransient(t *testing.T) {
	f, err := os.Open("testdata/esmp-valid.xml")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	reg := consumption.NewRegistry()
	xmlfmt.NewESMP(5000).Register(reg)
	d := xml.NewDecoder(f)
	p, root, err := reg.For(d)
	if err != nil {
		t.Fatalf("For: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = p.Parse(ctx, d, root, func([]consumption.Reading) error {
		t.Fatal("emit must not be called once the context is already cancelled")

		return nil
	})
	if !errors.Is(err, consumption.ErrTransient) {
		t.Fatalf("got %v, want ErrTransient", err)
	}
}

func BenchmarkESMPParse(b *testing.B) {
	payload, err := os.ReadFile("testdata/esmp-valid.xml")
	if err != nil {
		b.Fatalf("read: %v", err)
	}

	reg := consumption.NewRegistry()
	xmlfmt.NewESMP(5000).Register(reg)

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		d := xml.NewDecoder(bytes.NewReader(payload))
		p, root, err := reg.For(d)
		if err != nil {
			b.Fatal(err)
		}
		if err := p.Parse(context.Background(), d, root, func([]consumption.Reading) error { return nil }); err != nil {
			b.Fatal(err)
		}
	}
}

func FuzzESMPParse(f *testing.F) {
	seed, err := os.ReadFile("testdata/esmp-valid.xml")
	if err != nil {
		f.Fatalf("read: %v", err)
	}
	f.Add(seed)
	f.Add([]byte("<GL_MarketDocument/>"))

	reg := consumption.NewRegistry()
	xmlfmt.NewESMP(16).Register(reg)

	f.Fuzz(func(t *testing.T, payload []byte) {
		d := xml.NewDecoder(bytes.NewReader(payload))
		p, root, err := reg.For(d)
		if err != nil {
			return // not our format, or not XML: both are fine
		}
		// Must not panic and must terminate. Errors are expected.
		_ = p.Parse(context.Background(), d, root, func([]consumption.Reading) error { return nil })
	})
}
