package xmlfmt_test

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"os"
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

func TestESMPParseMalformed(t *testing.T) {
	for _, name := range []string{"esmp-bad-resolution.xml", "esmp-truncated.xml"} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := parseFile(t, name, 5000); !errors.Is(err, consumption.ErrMalformed) {
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
