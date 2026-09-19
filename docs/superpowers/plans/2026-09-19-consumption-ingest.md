# Streaming XML Consumption Ingest Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Consume energy-consumption XML from a Pub/Sub push subscription, parse it as a stream, and append canonical interval readings to BigQuery.

**Architecture:** A new `internal/consumption` package owns the domain: a canonical `Reading`, a `Parser` contract with a root-element registry, and a `Sink` contract. A push handler in `internal/httpapi` decodes the Pub/Sub envelope, verifies the OIDC token, and hands the payload to the service, which streams readings into a BigQuery sink. Permanent failures are parked in a GCS quarantine bucket and acked; transient ones return 503 so Pub/Sub retries. The same container image runs as a second Cloud Run service with the ingest route enabled.

**Tech Stack:** Go 1.25, Gin, `encoding/xml`, BigQuery Storage Write API (`managedwriter` + `adapt` + `dynamicpb`), Cloud Storage, `google.golang.org/api/idtoken`, Terraform, Cloud Run, Pub/Sub.

**Spec:** `docs/superpowers/specs/2026-09-19-gcp-consumption-ingest-design.md`

## Global Constraints

- Module path is `github.com/pytsekas/experiment-go`; Go directive `go 1.25.0`.
- Every task ends green under `make check` (fmt-check + vet + golangci-lint + test).
- Lint config is `.golangci.yml`: `errcheck, govet (with shadow), ineffassign, staticcheck, unused, bodyclose, errorlint, gocritic, gosec, misspell, nilerr, noctx, revive (exported, error-strings, context-as-argument, indent-error-flow), unconvert, unparam`. Exported identifiers need doc comments. Wrap errors with `%w`, never `%v`/`%s`.
- `goimports` local prefix `github.com/pytsekas/experiment-go` — stdlib, third-party, then project imports, each group separated by a blank line.
- Test style already in the repo: domain packages use external `package <name>_test`; `internal/httpapi` tests live in `package httpapi`. Stubs are structs of `...Fn` fields that call `t.Fatal` when a method the test did not configure is invoked, with a `var _ Iface = stub{}` assertion.
- Layering, from `.claude/skills/rest-endpoint/SKILL.md`: handler owns binding, status codes and the response envelope; service owns rules; sink/repository owns the external system. Never mix.
- Batch size default `INGEST_BATCH_ROWS=5000`; body limit 16 MB; ack deadline 60 s; DLQ after 5 delivery attempts; dataset region `europe-north1`.
- Never edit `deploy/terraform/gcp/terraform.tfstate*`.
- Commit messages follow the repo's conventional-commit style and end with the two attribution lines used by the previous two commits.

## Deviation from the spec (approved as part of this plan)

Spec §5 declares `Parse(ctx, r io.Reader, emit)` while §6 says the registry reads the root element and hands the *same decoder* to the parser. Those cannot both be true — an `io.Reader` handed to a parser after the registry consumed the root element would be missing that element. The contract below takes the decoder and the already-read root element:

```go
Parse(ctx context.Context, d *xml.Decoder, root xml.StartElement, emit func([]Reading) error) error
```

§15's claim-check argument is unaffected: the decoder is built from an `io.Reader`, so a `storage.Reader` still substitutes for a `bytes.Reader` one level up, in the service. Task 14 corrects the spec text.

## File Structure

| File | Responsibility |
| --- | --- |
| `internal/consumption/consumption.go` | `Reading`, `Message`, `Parser`, `Sink`, sentinel errors, validation |
| `internal/consumption/registry.go` | `Registry`: root element + namespace → `Parser` |
| `internal/consumption/service.go` | Orchestration: dispatch, parse, stamp, validate, write |
| `internal/consumption/xmlfmt/esmp.go` | ENTSO-E ESMP streaming parser (reference format) |
| `internal/consumption/xmlfmt/testdata/` | Golden XML: one valid, three malformed |
| `internal/consumption/bqsink/sink.go` | BigQuery Storage Write API sink + error classification |
| `internal/consumption/quarantine/gcs.go` | Writes failed payloads to a GCS bucket |
| `internal/httpapi/ingest.go` | Push envelope, token verification, status mapping |
| `internal/httpapi/oidc.go` | `idtoken`-backed `TokenVerifier` |
| `internal/config/config.go` | `Ingest` settings; database becomes optional |
| `cmd/api/main.go` | Wiring: optional pool, ingest dependencies |
| `deploy/terraform/gcp/modules/messaging/` | Subscription, DLQ, IAM |
| `deploy/terraform/gcp/modules/warehouse/` | Dataset, table, view, IAM |
| `deploy/terraform/gcp/main.tf` | Second Cloud Run service, quarantine bucket, toggle |
| `make/gcp.mk` | `ingest-deploy`, `ingest-smoke`, `bq-readings` |

---

### Task 1: Canonical reading and contracts

**Files:**
- Create: `internal/consumption/consumption.go`
- Test: `internal/consumption/consumption_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `Reading` struct; `Message` struct; `Parser` and `Sink` interfaces; `ErrUnknownFormat`, `ErrMalformed`, `ErrInvalidReading`, `ErrTransient`; `func (Reading) Validate() error`.

- [ ] **Step 1: Write the failing test**

Create `internal/consumption/consumption_test.go`:

```go
package consumption_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/pytsekas/experiment-go/internal/consumption"
)

func validReading() consumption.Reading {
	return consumption.Reading{
		MeteringPointID: "EE-123",
		Start:           time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Resolution:      time.Hour,
		Value:           12.5,
		Unit:            "kWh",
		Quality:         consumption.QualityMeasured,
		Direction:       consumption.DirectionConsumption,
	}
}

func TestReadingValidate(t *testing.T) {
	tests := map[string]struct {
		mutate  func(*consumption.Reading)
		wantErr bool
	}{
		"valid":            {mutate: func(*consumption.Reading) {}},
		"blank meter":      {mutate: func(r *consumption.Reading) { r.MeteringPointID = "  " }, wantErr: true},
		"zero start":       {mutate: func(r *consumption.Reading) { r.Start = time.Time{} }, wantErr: true},
		"zero resolution":  {mutate: func(r *consumption.Reading) { r.Resolution = 0 }, wantErr: true},
		"negative resolution": {mutate: func(r *consumption.Reading) { r.Resolution = -time.Hour }, wantErr: true},
		"NaN value":        {mutate: func(r *consumption.Reading) { r.Value = math.NaN() }, wantErr: true},
		"Inf value":        {mutate: func(r *consumption.Reading) { r.Value = math.Inf(1) }, wantErr: true},
		"blank unit":       {mutate: func(r *consumption.Reading) { r.Unit = "" }, wantErr: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := validReading()
			tc.mutate(&r)

			err := r.Validate()
			if tc.wantErr && !errors.Is(err, consumption.ErrInvalidReading) {
				t.Fatalf("got %v, want ErrInvalidReading", err)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("got %v, want nil", err)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/consumption/...`
Expected: FAIL — `no required module provides package .../internal/consumption`.

- [ ] **Step 3: Write the implementation**

Create `internal/consumption/consumption.go`:

```go
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
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/consumption/... && make fmt-check vet lint`
Expected: PASS, no lint findings.

- [ ] **Step 5: Commit**

```bash
git add internal/consumption/consumption.go internal/consumption/consumption_test.go
git commit -m "feat(consumption): canonical reading and ingest contracts"
```

---

### Task 2: Format registry

**Files:**
- Create: `internal/consumption/registry.go`
- Test: `internal/consumption/registry_test.go`

**Interfaces:**
- Consumes: `Parser`, `ErrUnknownFormat`, `ErrMalformed` from Task 1.
- Produces: `func NewRegistry() *Registry`; `func (*Registry) Register(space, local string, p Parser)`; `func (*Registry) For(d *xml.Decoder) (Parser, xml.StartElement, error)`.

- [ ] **Step 1: Write the failing test**

Create `internal/consumption/registry_test.go`:

```go
package consumption_test

import (
	"context"
	"encoding/xml"
	"errors"
	"strings"
	"testing"

	"github.com/pytsekas/experiment-go/internal/consumption"
)

// stubParser is a consumption.Parser that records the root element it was
// handed and emits whatever the test configured.
type stubParser struct {
	t       *testing.T
	parseFn func(ctx context.Context, d *xml.Decoder, root xml.StartElement, emit func([]consumption.Reading) error) error
}

var _ consumption.Parser = stubParser{}

func (s stubParser) Parse(ctx context.Context, d *xml.Decoder, root xml.StartElement, emit func([]consumption.Reading) error) error {
	if s.parseFn == nil {
		s.t.Fatal("unexpected Parse call")
	}

	return s.parseFn(ctx, d, root, emit)
}

func TestRegistryForMatchesNamespaceAndLocalName(t *testing.T) {
	want := stubParser{t: t}
	reg := consumption.NewRegistry()
	reg.Register("urn:test", "Doc", want)

	d := xml.NewDecoder(strings.NewReader(`<?xml version="1.0"?><Doc xmlns="urn:test"><a/></Doc>`))

	got, root, err := reg.For(d)
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if got != consumption.Parser(want) {
		t.Fatalf("got %#v, want the registered parser", got)
	}
	if root.Name.Local != "Doc" || root.Name.Space != "urn:test" {
		t.Fatalf("got root %v, want urn:test Doc", root.Name)
	}
}

func TestRegistryForUnknownRoot(t *testing.T) {
	reg := consumption.NewRegistry()
	d := xml.NewDecoder(strings.NewReader(`<Other/>`))

	if _, _, err := reg.For(d); !errors.Is(err, consumption.ErrUnknownFormat) {
		t.Fatalf("got %v, want ErrUnknownFormat", err)
	}
}

func TestRegistryForMalformedDocument(t *testing.T) {
	reg := consumption.NewRegistry()
	d := xml.NewDecoder(strings.NewReader(`<Doc`))

	if _, _, err := reg.For(d); !errors.Is(err, consumption.ErrMalformed) {
		t.Fatalf("got %v, want ErrMalformed", err)
	}
}

func TestRegistryForSkipsProlog(t *testing.T) {
	reg := consumption.NewRegistry()
	reg.Register("", "Doc", stubParser{t: t})

	d := xml.NewDecoder(strings.NewReader("<?xml version=\"1.0\"?>\n<!-- a comment -->\n<Doc/>"))

	if _, _, err := reg.For(d); err != nil {
		t.Fatalf("For: %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/consumption/...`
Expected: FAIL — `undefined: consumption.NewRegistry`.

- [ ] **Step 3: Write the implementation**

Create `internal/consumption/registry.go`:

```go
package consumption

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
)

// Registry maps a document's root element to the parser that understands it.
// It is a value the Service holds rather than package state, so tests can
// build their own with a stub parser in it.
type Registry struct {
	parsers map[xml.Name]Parser
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{parsers: make(map[xml.Name]Parser)} //nolint:exhaustruct // only field
}

// Register binds a parser to a root element. Registering the same element
// twice replaces the earlier parser, which keeps wiring order irrelevant.
func (r *Registry) Register(space, local string, p Parser) {
	r.parsers[xml.Name{Space: space, Local: local}] = p
}

// For advances d to the first start element and returns the parser registered
// for it, along with the element itself so the parser can read its attributes.
// Only the root element is consumed, so no bytes are decoded twice.
func (r *Registry) For(d *xml.Decoder) (Parser, xml.StartElement, error) {
	for {
		tok, err := d.Token()
		if err != nil {
			// errorlint forbids comparing errors with ==, so this is errors.Is
			// even for io.EOF.
			if errors.Is(err, io.EOF) {
				return nil, xml.StartElement{}, fmt.Errorf("%w: document has no root element", ErrMalformed)
			}

			return nil, xml.StartElement{}, fmt.Errorf("%w: reading root element: %w", ErrMalformed, err)
		}

		start, ok := tok.(xml.StartElement)
		if !ok {
			continue // prolog, comments, whitespace
		}

		p, ok := r.parsers[start.Name]
		if !ok {
			return nil, start, fmt.Errorf("%w: %s %s", ErrUnknownFormat, start.Name.Space, start.Name.Local)
		}

		return p, start, nil
	}
}
```

Note: drop the `//nolint` comment if `exhaustruct` is not enabled — it is not in `.golangci.yml`, so remove it.

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/consumption/... -run TestRegistry -v && make lint`
Expected: four PASS lines, no lint findings.

- [ ] **Step 5: Commit**

```bash
git add internal/consumption/registry.go internal/consumption/registry_test.go
git commit -m "feat(consumption): root-element parser registry"
```

---

### Task 3: ENTSO-E ESMP reference parser

**Files:**
- Create: `internal/consumption/xmlfmt/esmp.go`
- Create: `internal/consumption/xmlfmt/testdata/esmp-valid.xml`
- Create: `internal/consumption/xmlfmt/testdata/esmp-bad-resolution.xml`
- Create: `internal/consumption/xmlfmt/testdata/esmp-truncated.xml`
- Test: `internal/consumption/xmlfmt/esmp_test.go`

**Interfaces:**
- Consumes: `consumption.Reading`, `consumption.Parser`, `consumption.ErrMalformed`, quality/direction constants.
- Produces: `const ESMPNamespace`; `func NewESMP(batchRows int) ESMP`; `func (ESMP) Parse(...)`; `func (ESMP) Register(r *consumption.Registry)`.

The format: `<GL_MarketDocument>` → `<TimeSeries>` (carrying `mRID`, an optional `MarketEvaluationPoint/mRID`, a unit and a business type) → `<Period>` (carrying `timeInterval/start` and `resolution`) → repeated `<Point>` (`position`, `quantity`, optional `quality`). A point's interval start is `periodStart + (position-1) * resolution`.

- [ ] **Step 1: Create the golden files**

`internal/consumption/xmlfmt/testdata/esmp-valid.xml`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<GL_MarketDocument xmlns="urn:iec62325.351:tc57wg16:451-6:generationloaddocument:3:0">
  <mRID>doc-1</mRID>
  <TimeSeries>
    <mRID>ts-1</mRID>
    <businessType>A04</businessType>
    <quantity_Measure_Unit.name>KWH</quantity_Measure_Unit.name>
    <MarketEvaluationPoint>
      <mRID>EE-METER-1</mRID>
    </MarketEvaluationPoint>
    <Period>
      <timeInterval>
        <start>2026-01-01T00:00Z</start>
        <end>2026-01-01T01:00Z</end>
      </timeInterval>
      <resolution>PT15M</resolution>
      <Point><position>1</position><quantity>10.5</quantity></Point>
      <Point><position>2</position><quantity>11.0</quantity><quality>A03</quality></Point>
      <Point><position>3</position><quantity>12.25</quantity></Point>
      <Point><position>4</position><quantity>9.75</quantity></Point>
    </Period>
  </TimeSeries>
  <TimeSeries>
    <mRID>ts-2</mRID>
    <businessType>A01</businessType>
    <quantity_Measure_Unit.name>KWH</quantity_Measure_Unit.name>
    <MarketEvaluationPoint>
      <mRID>EE-METER-2</mRID>
    </MarketEvaluationPoint>
    <Period>
      <timeInterval>
        <start>2026-01-01T00:00Z</start>
        <end>2026-01-01T02:00Z</end>
      </timeInterval>
      <resolution>PT60M</resolution>
      <Point><position>1</position><quantity>100</quantity></Point>
      <Point><position>2</position><quantity>200</quantity></Point>
    </Period>
  </TimeSeries>
</GL_MarketDocument>
```

`esmp-bad-resolution.xml` is the same document truncated to one TimeSeries with `<resolution>PT7M</resolution>`. `esmp-truncated.xml` is the same document cut off mid-`<Point>` (delete the closing tags after the second point).

- [ ] **Step 2: Write the failing test**

Create `internal/consumption/xmlfmt/esmp_test.go`:

```go
package xmlfmt_test

import (
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
```

Add `"bytes"` to the import block (the benchmark and fuzz target use it).

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/consumption/xmlfmt/...`
Expected: FAIL — `undefined: xmlfmt.NewESMP`.

- [ ] **Step 4: Write the implementation**

Create `internal/consumption/xmlfmt/esmp.go`:

```go
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
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/consumption/... -v`
Expected: all PASS, including the four ESMP tests.

- [ ] **Step 6: Run the benchmark and a short fuzz session**

Run: `go test ./internal/consumption/xmlfmt/ -bench=. -benchmem -run=^$`
Expected: a `BenchmarkESMPParse` line with ns/op, B/op and MB/s.

Run: `go test ./internal/consumption/xmlfmt/ -run=Fuzz -fuzz=FuzzESMPParse -fuzztime=60s`
Expected: `elapsed: 60s ... no failures`. Commit any file that lands in `testdata/fuzz/`.

- [ ] **Step 7: Commit**

```bash
git add internal/consumption/xmlfmt
git commit -m "feat(consumption): streaming ENTSO-E ESMP parser"
```

---

### Task 4: Ingest service

**Files:**
- Create: `internal/consumption/service.go`
- Test: `internal/consumption/service_test.go`

**Interfaces:**
- Consumes: `Registry`, `Parser`, `Sink`, `Message`, `Reading`, sentinels.
- Produces: `func NewService(reg *Registry, sink Sink, log *slog.Logger) *Service`; `func (*Service) Ingest(ctx context.Context, msg Message) (int, error)`.

- [ ] **Step 1: Write the failing test**

Create `internal/consumption/service_test.go`:

```go
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
func emitting(t *testing.T, values ...float64) stubParser {
	t.Helper()

	return stubParser{
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
	svc := newService(t, stubParser{t: t}, stubSink{t: t})

	if _, err := svc.Ingest(context.Background(), consumption.Message{ID: "m", Payload: []byte(`<Other/>`)}); !errors.Is(err, consumption.ErrUnknownFormat) {
		t.Fatalf("got %v, want ErrUnknownFormat", err)
	}
}

func TestServiceIngestRejectsInvalidReading(t *testing.T) {
	bad := stubParser{t: t, parseFn: func(_ context.Context, _ *xml.Decoder, _ xml.StartElement, emit func([]consumption.Reading) error) error {
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
	svc := newService(t, stubParser{t: t}, stubSink{t: t})

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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/consumption/... -run TestService`
Expected: FAIL — `undefined: consumption.NewService`.

- [ ] **Step 3: Write the implementation**

Create `internal/consumption/service.go`:

```go
package consumption

import (
	"bytes"
	"context"
	"encoding/xml"
	"log/slog"
	"time"
)

// Service turns a message into stored readings. It is the only type that
// knows about all three contracts, which keeps the parser ignorant of storage
// and the sink ignorant of XML.
type Service struct {
	registry *Registry
	sink     Sink
	log      *slog.Logger

	// Now supplies the ingestion timestamp. Tests replace it; production
	// leaves it alone.
	Now func() time.Time
}

// NewService returns a service parsing with reg and storing through sink.
// A nil logger falls back to slog.Default.
func NewService(reg *Registry, sink Sink, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}

	return &Service{registry: reg, sink: sink, log: log, Now: time.Now}
}

// Ingest parses msg and writes every reading it contains, returning how many
// were stored. Errors wrap one of the package sentinels so the transport can
// decide whether the failure is worth retrying.
func (s *Service) Ingest(ctx context.Context, msg Message) (int, error) {
	d := xml.NewDecoder(bytes.NewReader(msg.Payload))

	parser, root, err := s.registry.For(d)
	if err != nil {
		return 0, err
	}

	ingestedAt := s.Now().UTC()
	stored := 0

	err = parser.Parse(ctx, d, root, func(batch []Reading) error {
		for i := range batch {
			batch[i].SourceMessageID = msg.ID
			batch[i].IngestedAt = ingestedAt

			if err := batch[i].Validate(); err != nil {
				return err
			}
		}

		if err := s.sink.Write(ctx, batch); err != nil {
			return err
		}
		stored += len(batch)

		return nil
	})
	if err != nil {
		return stored, err
	}

	s.log.DebugContext(ctx, "message ingested",
		slog.String("message_id", msg.ID),
		slog.Int("readings", stored))

	return stored, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/consumption/... && make lint`
Expected: PASS, no findings.

- [ ] **Step 5: Commit**

```bash
git add internal/consumption/service.go internal/consumption/service_test.go
git commit -m "feat(consumption): ingest service joining parser and sink"
```

---

### Task 5: Configuration

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces: `config.Ingest` struct on `config.Config` as field `Ingest`; database DSN optional when `Ingest.Enabled`.

- [ ] **Step 1: Write the failing test**

Append to `internal/config/config_test.go` (match the file's existing helper style; it already sets variables with `t.Setenv`):

```go
func TestLoadIngestDefaultsOff(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Ingest.Enabled {
		t.Error("ingest must be off unless enabled explicitly")
	}
	if cfg.Ingest.BatchRows != 5000 {
		t.Errorf("BatchRows: got %d, want 5000", cfg.Ingest.BatchRows)
	}
}

func TestLoadIngestRequiresItsSettings(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("ENABLE_INGEST_ENDPOINT", "true")

	if _, err := config.Load(); err == nil {
		t.Fatal("expected an error when the ingest endpoint is on but unconfigured")
	}
}

func TestLoadIngestMakesDatabaseOptional(t *testing.T) {
	t.Setenv("ENABLE_INGEST_ENDPOINT", "true")
	t.Setenv("PUBSUB_AUDIENCE", "https://ingest.example")
	t.Setenv("PUBSUB_PUSH_SERVICE_ACCOUNT", "push@example.iam.gserviceaccount.com")
	t.Setenv("BQ_DATASET", "energy")
	t.Setenv("QUARANTINE_BUCKET", "experiment-go-quarantine")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DB.DSN != "" {
		t.Errorf("DSN: got %q, want empty", cfg.DB.DSN)
	}
	if cfg.Ingest.BQTable != "readings" {
		t.Errorf("BQTable: got %q, want readings", cfg.Ingest.BQTable)
	}
}

func TestLoadDatabaseStillRequiredWithoutIngest(t *testing.T) {
	if _, err := config.Load(); err == nil {
		t.Fatal("expected the API service to still fail fast without a database")
	}
}
```

If `TestLoadDatabaseStillRequiredWithoutIngest` inherits a `DATABASE_URL` from the developer's shell, guard it with `t.Setenv("DATABASE_URL", "")` and `t.Setenv("POSTGRES_HOST", "")` first.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/config/...`
Expected: FAIL — `cfg.Ingest undefined`.

- [ ] **Step 3: Write the implementation**

In `internal/config/config.go`, add the field to `Config`:

```go
// Config is the fully resolved application configuration.
type Config struct {
	Env      string
	HTTP     HTTP
	DB       DB
	Logger   Logger
	Migrate  Migrate
	Features Features
	Ingest   Ingest
}
```

Add the type:

```go
// Ingest holds the Pub/Sub push ingest settings. Everything but Enabled is
// required once Enabled is true, and validate enforces that.
type Ingest struct {
	// Enabled registers POST /internal/pubsub/consumption. It is on for the
	// ingest service and off for the API service, which is how one image
	// serves both roles.
	Enabled bool
	// Audience is the expected `aud` claim: the ingest service's own URL.
	Audience string
	// PushServiceAccount is the expected token email claim.
	PushServiceAccount string
	BQProject          string // empty means the runtime project
	BQDataset          string
	BQTable            string
	QuarantineBucket   string
	BatchRows          int
}
```

Populate it in `Load`, after `Features`:

```go
		Ingest: Ingest{
			Enabled:            envBool("ENABLE_INGEST_ENDPOINT", false),
			Audience:           env("PUBSUB_AUDIENCE", ""),
			PushServiceAccount: env("PUBSUB_PUSH_SERVICE_ACCOUNT", ""),
			BQProject:          env("BQ_PROJECT", ""),
			BQDataset:          env("BQ_DATASET", ""),
			BQTable:            env("BQ_TABLE", "readings"),
			QuarantineBucket:   env("QUARANTINE_BUCKET", ""),
			BatchRows:          envInt("INGEST_BATCH_ROWS", 5000),
		},
```

Replace the DSN check in `validate` and add the ingest checks:

```go
	// The ingest service has no reason to reach Postgres, so a database is
	// required only when this process serves the API.
	if !c.Ingest.Enabled && c.DB.DSN == "" {
		return fmt.Errorf("config: DATABASE_URL (or POSTGRES_*) must be set")
	}
	if err := c.Ingest.validate(); err != nil {
		return err
	}
```

And the method:

```go
func (i Ingest) validate() error {
	if !i.Enabled {
		return nil
	}

	for _, missing := range []struct {
		key   string
		value string
	}{
		{"PUBSUB_AUDIENCE", i.Audience},
		{"PUBSUB_PUSH_SERVICE_ACCOUNT", i.PushServiceAccount},
		{"BQ_DATASET", i.BQDataset},
		{"BQ_TABLE", i.BQTable},
		{"QUARANTINE_BUCKET", i.QuarantineBucket},
	} {
		if missing.value == "" {
			return fmt.Errorf("config: %s must be set when ENABLE_INGEST_ENDPOINT is true", missing.key)
		}
	}

	if i.BatchRows < 1 {
		return fmt.Errorf("config: INGEST_BATCH_ROWS %d must be positive", i.BatchRows)
	}

	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/config/... -v && make lint`
Expected: PASS for the four new tests and every pre-existing one.

- [ ] **Step 5: Commit**

```bash
git add internal/config
git commit -m "feat(config): ingest settings, database optional for the ingest role"
```

---

### Task 6: BigQuery sink

**Files:**
- Create: `internal/consumption/bqsink/sink.go`
- Create: `internal/consumption/bqsink/schema.go`
- Test: `internal/consumption/bqsink/sink_test.go`
- Test: `internal/consumption/bqsink/integration_test.go`
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Consumes: `consumption.Reading`, `consumption.Sink`, `consumption.ErrTransient`.
- Produces: `func New(ctx context.Context, cfg Config) (*Sink, error)`; `func (*Sink) Write(ctx context.Context, rows []consumption.Reading) error`; `func (*Sink) Close() error`; `bqsink.Config{Project, Dataset, Table string}`; `func TableSchema() bigquery.Schema`.

- [ ] **Step 1: Confirm the API surface before writing code**

The Storage Write API is easy to get subtly wrong. Before implementing, read the current docs for the pinned version rather than trusting memory:

```bash
go get cloud.google.com/go/bigquery@latest
go doc cloud.google.com/go/bigquery/storage/managedwriter
go doc cloud.google.com/go/bigquery/storage/managedwriter/adapt
```

Confirm these four names exist and note their exact signatures: `managedwriter.NewClient`, `(*Client).NewManagedStream`, `adapt.BQSchemaToStorageTableSchema`, `adapt.StorageSchemaToProto2Descriptor`. If any differ from the code below, follow the docs — they are the source of truth, this plan is not.

- [ ] **Step 2: Write the failing test**

Create `internal/consumption/bqsink/sink_test.go`. It covers what can be tested without GCP: the schema, the row conversion and the error classification.

```go
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
	transient := []codes.Code{codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted, codes.Internal}
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
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/consumption/bqsink/...`
Expected: FAIL — the package does not exist.

- [ ] **Step 4: Add the dependencies**

```bash
go get cloud.google.com/go/bigquery
go get google.golang.org/protobuf
go mod tidy
```

- [ ] **Step 5: Write the schema**

Create `internal/consumption/bqsink/schema.go`:

```go
package bqsink

import (
	"time"

	"cloud.google.com/go/bigquery"

	"github.com/pytsekas/experiment-go/internal/consumption"
)

// TableSchema is the readings table, in the field order Terraform creates it
// with. The two definitions must agree; the integration test is what proves
// they still do.
func TableSchema() bigquery.Schema {
	return bigquery.Schema{
		{Name: "metering_point_id", Type: bigquery.StringFieldType, Required: true},
		{Name: "interval_start", Type: bigquery.TimestampFieldType, Required: true},
		{Name: "resolution_sec", Type: bigquery.IntegerFieldType, Required: true},
		{Name: "value", Type: bigquery.FloatFieldType, Required: true},
		{Name: "unit", Type: bigquery.StringFieldType, Required: true},
		{Name: "quality", Type: bigquery.StringFieldType, Required: true},
		{Name: "direction", Type: bigquery.StringFieldType, Required: true},
		{Name: "source_message_id", Type: bigquery.StringFieldType, Required: true},
		{Name: "ingested_at", Type: bigquery.TimestampFieldType, Required: true},
	}
}

// readingToValues maps a reading onto the schema's field names. BigQuery
// timestamps are microseconds since the epoch on the wire.
func readingToValues(r consumption.Reading) map[string]any {
	return map[string]any{
		"metering_point_id": r.MeteringPointID,
		"interval_start":    r.Start.UTC().UnixMicro(),
		"resolution_sec":    int64(r.Resolution / time.Second),
		"value":             r.Value,
		"unit":              r.Unit,
		"quality":           r.Quality,
		"direction":         r.Direction,
		"source_message_id": r.SourceMessageID,
		"ingested_at":       r.IngestedAt.UTC().UnixMicro(),
	}
}
```

- [ ] **Step 6: Write the sink**

Create `internal/consumption/bqsink/sink.go`:

```go
// Package bqsink stores readings in BigQuery through the Storage Write API.
package bqsink

import (
	"context"
	"fmt"

	"cloud.google.com/go/bigquery/storage/managedwriter"
	"cloud.google.com/go/bigquery/storage/managedwriter/adapt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/pytsekas/experiment-go/internal/consumption"
)

// Config identifies the destination table.
type Config struct {
	Project string
	Dataset string
	Table   string
}

// Sink appends readings to one BigQuery table using the default stream, which
// is at-least-once: duplicates are resolved by the readings_current view
// rather than by the write path.
type Sink struct {
	client     *managedwriter.Client
	stream     *managedwriter.ManagedStream
	descriptor protoreflect.MessageDescriptor
}

var _ consumption.Sink = (*Sink)(nil)

// New opens a managed stream against cfg's table. The caller closes the sink.
func New(ctx context.Context, cfg Config) (*Sink, error) {
	client, err := managedwriter.NewClient(ctx, cfg.Project)
	if err != nil {
		return nil, fmt.Errorf("bqsink: new client: %w", err)
	}

	storageSchema, err := adapt.BQSchemaToStorageTableSchema(TableSchema())
	if err != nil {
		client.Close()

		return nil, fmt.Errorf("bqsink: converting schema: %w", err)
	}

	messageDescriptor, err := adapt.StorageSchemaToProto2Descriptor(storageSchema, "readings")
	if err != nil {
		client.Close()

		return nil, fmt.Errorf("bqsink: building descriptor: %w", err)
	}

	md, ok := messageDescriptor.(protoreflect.MessageDescriptor)
	if !ok {
		client.Close()

		return nil, fmt.Errorf("bqsink: descriptor is %T, want a message descriptor", messageDescriptor)
	}

	descriptorProto, err := adapt.NormalizeDescriptor(md)
	if err != nil {
		client.Close()

		return nil, fmt.Errorf("bqsink: normalising descriptor: %w", err)
	}

	stream, err := client.NewManagedStream(ctx,
		managedwriter.WithDestinationTable(
			managedwriter.TableParentFromParts(cfg.Project, cfg.Dataset, cfg.Table)),
		managedwriter.WithType(managedwriter.DefaultStream),
		managedwriter.WithSchemaDescriptor(descriptorProto),
	)
	if err != nil {
		client.Close()

		return nil, fmt.Errorf("bqsink: opening stream: %w", err)
	}

	return &Sink{client: client, stream: stream, descriptor: md}, nil
}

// Write appends one batch and waits for BigQuery to accept it, so the caller
// only acknowledges a message whose rows are durable.
func (s *Sink) Write(ctx context.Context, readings []consumption.Reading) error {
	if len(readings) == 0 {
		return nil
	}

	rows := make([][]byte, 0, len(readings))
	for _, r := range readings {
		message := dynamicpb.NewMessage(s.descriptor)
		for name, value := range readingToValues(r) {
			field := s.descriptor.Fields().ByTextName(name)
			if field == nil {
				return fmt.Errorf("bqsink: no field %q in the table descriptor", name)
			}
			message.Set(field, protoreflect.ValueOf(value))
		}

		encoded, err := proto.Marshal(message)
		if err != nil {
			return fmt.Errorf("bqsink: encoding row: %w", err)
		}
		rows = append(rows, encoded)
	}

	result, err := s.stream.AppendRows(ctx, rows)
	if err != nil {
		return classify(err)
	}
	if _, err := result.GetResult(ctx); err != nil {
		return classify(err)
	}

	return nil
}

// Close releases the stream and the client.
func (s *Sink) Close() error {
	if err := s.stream.Close(); err != nil {
		return fmt.Errorf("bqsink: closing stream: %w", err)
	}
	if err := s.client.Close(); err != nil {
		return fmt.Errorf("bqsink: closing client: %w", err)
	}

	return nil
}

// classify marks the failures worth retrying. Anything else is permanent: a
// schema mismatch or a missing permission will fail identically forever.
func classify(err error) error {
	if err == nil {
		return nil
	}

	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted, codes.Internal:
		return fmt.Errorf("%w: bigquery append: %w", consumption.ErrTransient, err)
	default:
		return fmt.Errorf("bigquery append: %w", err)
	}
}
```

One note for the implementer: `protoreflect.ValueOf` panics on a type the field does not accept, so if `Set` panics, print `field.Kind()` and convert explicitly — `int64` for INTEGER and TIMESTAMP, `float64` for FLOAT, `string` for STRING.

- [ ] **Step 7: Write the integration test**

Create `internal/consumption/bqsink/integration_test.go`:

```go
//go:build integration

package bqsink_test

import (
	"context"
	"os"
	"testing"
	"time"

	"cloud.google.com/go/bigquery"
	"google.golang.org/api/iterator"

	"github.com/pytsekas/experiment-go/internal/consumption"
	"github.com/pytsekas/experiment-go/internal/consumption/bqsink"
)

// TestWriteRoundTrip appends a row to a real development table and reads it
// back. Run it with: go test -tags integration ./internal/consumption/bqsink/
// and BQ_PROJECT / BQ_DATASET set to a dataset you may write to.
func TestWriteRoundTrip(t *testing.T) {
	project, dataset := os.Getenv("BQ_PROJECT"), os.Getenv("BQ_DATASET")
	if project == "" || dataset == "" {
		t.Skip("BQ_PROJECT and BQ_DATASET must be set")
	}

	ctx := context.Background()
	sink, err := bqsink.New(ctx, bqsink.Config{Project: project, Dataset: dataset, Table: "readings"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sink.Close()

	messageID := "it-" + time.Now().UTC().Format("20060102150405.000")
	row := consumption.Reading{
		MeteringPointID: "IT-METER",
		Start:           time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Resolution:      time.Hour,
		Value:           42,
		Unit:            "kWh",
		Quality:         consumption.QualityMeasured,
		Direction:       consumption.DirectionConsumption,
		SourceMessageID: messageID,
		IngestedAt:      time.Now().UTC(),
	}

	if err := sink.Write(ctx, []consumption.Reading{row}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	client, err := bigquery.NewClient(ctx, project)
	if err != nil {
		t.Fatalf("bigquery client: %v", err)
	}
	defer client.Close()

	q := client.Query("SELECT COUNT(*) AS n FROM `" + dataset + ".readings` WHERE source_message_id = @id")
	q.Parameters = []bigquery.QueryParameter{{Name: "id", Value: messageID}}

	it, err := q.Read(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	var result struct{ N int64 }
	if err := it.Next(&result); err != nil && err != iterator.Done {
		t.Fatalf("next: %v", err)
	}
	if result.N != 1 {
		t.Fatalf("got %d rows, want 1", result.N)
	}
}
```

- [ ] **Step 8: Run the unit tests**

Run: `go test ./internal/consumption/bqsink/ -v && make lint`
Expected: the five unit tests PASS; the integration test is excluded by its build tag.

- [ ] **Step 9: Commit**

```bash
git add internal/consumption/bqsink go.mod go.sum
git commit -m "feat(consumption): BigQuery Storage Write API sink"
```

---

### Task 7: Quarantine writer

**Files:**
- Create: `internal/consumption/quarantine/gcs.go`
- Test: `internal/consumption/quarantine/gcs_test.go`

**Interfaces:**
- Produces: `func NewGCS(ctx context.Context, bucket string) (*GCS, error)`; `func (*GCS) Quarantine(ctx context.Context, messageID string, payload []byte, reason string) error`; `func (*GCS) Close() error`; `func ObjectName(now time.Time, messageID string) string`.

- [ ] **Step 1: Write the failing test**

Create `internal/consumption/quarantine/gcs_test.go`:

```go
package quarantine

import (
	"strings"
	"testing"
	"time"
)

func TestObjectNamePartitionsByDate(t *testing.T) {
	got := ObjectName(time.Date(2026, 9, 19, 14, 30, 0, 0, time.UTC), "msg-1")
	if want := "2026/09/19/msg-1.xml"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestObjectNameSanitisesTheMessageID(t *testing.T) {
	// A message id is attacker-adjacent data; it must never escape the prefix.
	got := ObjectName(time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC), "../../etc/passwd")
	if strings.Contains(got, "..") || strings.Count(got, "/") != 3 {
		t.Fatalf("got %q, which escapes the date prefix", got)
	}
}

func TestObjectNameHandlesAnEmptyID(t *testing.T) {
	got := ObjectName(time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC), "")
	if !strings.HasSuffix(got, "/unknown.xml") {
		t.Fatalf("got %q, want an unknown.xml fallback", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/consumption/quarantine/...`
Expected: FAIL — the package does not exist.

- [ ] **Step 3: Write the implementation**

```bash
go get cloud.google.com/go/storage
```

Create `internal/consumption/quarantine/gcs.go`:

```go
// Package quarantine parks payloads that failed permanently, so a poison
// message can be inspected after it has been acknowledged.
package quarantine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/storage"
)

// GCS writes quarantined payloads to a Cloud Storage bucket.
type GCS struct {
	client *storage.Client
	bucket string
}

// NewGCS returns a writer for bucket. The caller closes it.
func NewGCS(ctx context.Context, bucket string) (*GCS, error) {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("quarantine: new client: %w", err)
	}

	return &GCS{client: client, bucket: bucket}, nil
}

// ObjectName is the date-partitioned path a payload is stored under. The
// message id is reduced to its last path segment so a hostile id cannot
// write outside the prefix.
func ObjectName(now time.Time, messageID string) string {
	safe := messageID
	if i := strings.LastIndex(safe, "/"); i >= 0 {
		safe = safe[i+1:]
	}
	safe = strings.ReplaceAll(safe, "..", "")
	if strings.TrimSpace(safe) == "" {
		safe = "unknown"
	}

	return fmt.Sprintf("%s/%s.xml", now.UTC().Format("2006/01/02"), safe)
}

// Quarantine stores payload and records why it was rejected in the object's
// metadata, where it is visible without downloading the file.
func (g *GCS) Quarantine(ctx context.Context, messageID string, payload []byte, reason string) error {
	object := g.client.Bucket(g.bucket).Object(ObjectName(time.Now(), messageID))

	w := object.NewWriter(ctx)
	w.ContentType = "application/xml"
	w.Metadata = map[string]string{
		"message_id": messageID,
		"reason":     reason,
	}

	if _, err := w.Write(payload); err != nil {
		_ = w.Close()

		return fmt.Errorf("quarantine: writing %s: %w", object.ObjectName(), err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("quarantine: closing %s: %w", object.ObjectName(), err)
	}

	return nil
}

// Close releases the underlying client.
func (g *GCS) Close() error {
	if err := g.client.Close(); err != nil {
		return fmt.Errorf("quarantine: closing client: %w", err)
	}

	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/consumption/quarantine/... -v && make lint`
Expected: three PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/consumption/quarantine go.mod go.sum
git commit -m "feat(consumption): GCS quarantine for permanently failed payloads"
```

---

### Task 8: Push handler

**Files:**
- Create: `internal/httpapi/ingest.go`
- Test: `internal/httpapi/ingest_test.go`
- Modify: `internal/httpapi/router.go`

**Interfaces:**
- Consumes: `consumption.Message`, all four sentinels.
- Produces: `httpapi.IngestService`, `httpapi.TokenVerifier`, `httpapi.Quarantiner` interfaces; `Deps.Ingest`, `Deps.Verifier`, `Deps.Quarantine`, `Deps.EnableIngest` fields.

- [ ] **Step 1: Write the failing test**

Create `internal/httpapi/ingest_test.go`:

```go
package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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
	calls  int
	reason string
	err    error
}

func (s *stubQuarantine) Quarantine(_ context.Context, _ string, _ []byte, reason string) error {
	s.calls++
	s.reason = reason

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

	req := httptest.NewRequest(http.MethodPost, "/internal/pubsub/consumption", strings.NewReader(body))
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
	rec := postEnvelope(t, ingestRouter(t, stubIngest{t: t}, stubVerifier{}, q), "{not json")

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	if q.calls != 1 {
		t.Fatalf("quarantine called %d times, want 1", q.calls)
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

func TestIngestRouteAbsentWhenDisabled(t *testing.T) {
	h := NewRouter(Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Tasks: stubService{t: t}})

	rec := postEnvelope(t, h, envelope(t, "m", "<Doc/>"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404 — the API service must not expose the ingest route", rec.Code)
	}
}
```

Add `"io"` to the imports.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/httpapi/ -run TestIngest`
Expected: FAIL — `undefined: IngestService`.

- [ ] **Step 3: Write the handler**

Create `internal/httpapi/ingest.go`:

```go
package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/pytsekas/experiment-go/internal/consumption"
)

// maxIngestBody caps the push request body. A Pub/Sub message is at most
// 10 MB, and base64 inflates it by about a third, so 16 MB leaves headroom
// without letting an unbounded body into memory.
const maxIngestBody = 16 << 20

// IngestService stores the readings in one message.
type IngestService interface {
	Ingest(ctx context.Context, msg consumption.Message) (int, error)
}

// TokenVerifier checks the OIDC token Pub/Sub signs each push with.
type TokenVerifier interface {
	Verify(ctx context.Context, bearerToken string) error
}

// Quarantiner parks a payload that failed permanently.
type Quarantiner interface {
	Quarantine(ctx context.Context, messageID string, payload []byte, reason string) error
}

// pushEnvelope is the body Pub/Sub sends to a push endpoint. Data is []byte
// so encoding/json decodes its base64 for us.
type pushEnvelope struct {
	Message struct {
		Data        []byte            `json:"data"`
		MessageID   string            `json:"messageId"`
		Attributes  map[string]string `json:"attributes"`
		PublishTime time.Time         `json:"publishTime"`
	} `json:"message"`
	Subscription string `json:"subscription"`
}

// ingestHandler serves the Pub/Sub push subscription.
type ingestHandler struct {
	svc        IngestService
	verifier   TokenVerifier
	quarantine Quarantiner
	log        *slog.Logger
}

// consume handles POST /internal/pubsub/consumption.
//
// The status codes are a contract with Pub/Sub, not with a human caller:
// 2xx acknowledges and stops redelivery, anything else schedules a retry.
// A permanently broken payload therefore answers 200 after being quarantined,
// because retrying it can only fail again.
func (h ingestHandler) consume(c *gin.Context) {
	started := time.Now()
	ctx := c.Request.Context()

	if err := h.verifier.Verify(ctx, bearerToken(c)); err != nil {
		h.log.WarnContext(ctx, "rejected unauthenticated push", slog.String("error", err.Error()))
		respondError(c, http.StatusUnauthorized, "invalid authentication token")

		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxIngestBody)

	var envelope pushEnvelope
	if err := c.ShouldBindJSON(&envelope); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			h.log.ErrorContext(ctx, "push body too large", slog.Int64("limit_bytes", maxIngestBody))
			respondError(c, http.StatusRequestEntityTooLarge, "payload too large")

			return
		}

		// The envelope itself is broken, so there is no message id to file it
		// under and nothing a retry could fix.
		h.park(ctx, "", nil, "unparseable push envelope: "+err.Error())
		c.Status(http.StatusOK)

		return
	}

	msg := consumption.Message{
		ID:         envelope.Message.MessageID,
		Payload:    envelope.Message.Data,
		Attributes: envelope.Message.Attributes,
	}

	readings, err := h.svc.Ingest(ctx, msg)
	switch {
	case err == nil:
		h.log.InfoContext(ctx, "message ingested",
			slog.String("message_id", msg.ID),
			slog.Int("bytes", len(msg.Payload)),
			slog.Int("readings", readings),
			slog.Int64("duration_ms", time.Since(started).Milliseconds()),
			slog.String("outcome", "stored"))
		c.Status(http.StatusNoContent)

	case errors.Is(err, consumption.ErrTransient):
		h.log.ErrorContext(ctx, "ingest failed, will be retried",
			slog.String("message_id", msg.ID),
			slog.String("error", err.Error()),
			slog.String("outcome", "retry"))
		respondError(c, http.StatusServiceUnavailable, "temporarily unable to store readings")

	default:
		h.park(ctx, msg.ID, msg.Payload, err.Error())
		c.Status(http.StatusOK)
	}
}

// park quarantines a payload and logs the outcome. A failure to quarantine is
// logged but not returned: redelivering a payload that cannot be parsed would
// only repeat the failure.
func (h ingestHandler) park(ctx context.Context, messageID string, payload []byte, reason string) {
	attrs := []any{
		slog.String("message_id", messageID),
		slog.String("reason", reason),
		slog.String("outcome", "quarantined"),
	}

	if err := h.quarantine.Quarantine(ctx, messageID, payload, reason); err != nil {
		h.log.ErrorContext(ctx, "quarantine failed", append(attrs, slog.String("error", err.Error()))...)

		return
	}

	h.log.ErrorContext(ctx, "payload quarantined", attrs...)
}

// bearerToken extracts the credential from the Authorization header.
func bearerToken(c *gin.Context) string {
	header := c.GetHeader("Authorization")
	if after, ok := strings.CutPrefix(header, "Bearer "); ok {
		return after
	}

	return ""
}
```

- [ ] **Step 4: Register the route**

In `internal/httpapi/router.go`, extend `Deps`:

```go
	// EnableIngest registers POST /internal/pubsub/consumption. It is on for
	// the ingest service only; the API service must not expose it.
	EnableIngest bool
	Ingest       IngestService
	Verifier     TokenVerifier
	Quarantine   Quarantiner
```

And register it inside `NewRouter`, after the `v1` group and before `r.NoRoute`:

```go
	if d.EnableIngest {
		ingest := ingestHandler{svc: d.Ingest, verifier: d.Verifier, quarantine: d.Quarantine, log: d.Logger}
		r.POST("/internal/pubsub/consumption", ingest.consume)
	}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/httpapi/ -v && make lint`
Expected: every new `TestIngest*` PASS, and the existing task and health tests still PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/httpapi/ingest.go internal/httpapi/ingest_test.go internal/httpapi/router.go
git commit -m "feat(httpapi): Pub/Sub push handler for consumption documents"
```

---

### Task 9: OIDC verifier and wiring

**Files:**
- Create: `internal/httpapi/oidc.go`
- Modify: `cmd/api/main.go`
- Test: `internal/httpapi/oidc_test.go`

**Interfaces:**
- Produces: `func NewOIDCVerifier(audience, serviceAccount string) TokenVerifier`.

- [ ] **Step 1: Write the failing test**

Create `internal/httpapi/oidc_test.go`:

```go
package httpapi

import (
	"context"
	"testing"
)

func TestOIDCVerifierRejectsAnEmptyToken(t *testing.T) {
	v := NewOIDCVerifier("https://ingest.example", "push@example.iam.gserviceaccount.com")

	if err := v.Verify(context.Background(), ""); err == nil {
		t.Fatal("expected an error for a missing token")
	}
}

func TestOIDCVerifierRejectsGarbage(t *testing.T) {
	v := NewOIDCVerifier("https://ingest.example", "push@example.iam.gserviceaccount.com")

	if err := v.Verify(context.Background(), "not-a-jwt"); err == nil {
		t.Fatal("expected an error for a malformed token")
	}
}
```

A test that a *valid* token passes would need Google's signing keys, so it belongs to the deployed smoke test in Task 13, not here.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/httpapi/ -run TestOIDC`
Expected: FAIL — `undefined: NewOIDCVerifier`.

- [ ] **Step 3: Write the implementation**

```bash
go get google.golang.org/api/idtoken
```

Create `internal/httpapi/oidc.go`:

```go
package httpapi

import (
	"context"
	"fmt"

	"google.golang.org/api/idtoken"
)

// oidcVerifier validates the token Pub/Sub attaches to a push request.
//
// Cloud Run already rejects callers without an invoker binding; this is the
// second layer, so an accidental allUsers binding cannot turn the endpoint
// into an open sink. It checks the signature, the audience and the identity.
type oidcVerifier struct {
	audience       string
	serviceAccount string
}

var _ TokenVerifier = oidcVerifier{}

// NewOIDCVerifier returns a verifier accepting tokens issued for audience and
// signed for serviceAccount.
func NewOIDCVerifier(audience, serviceAccount string) TokenVerifier {
	return oidcVerifier{audience: audience, serviceAccount: serviceAccount}
}

// Verify implements TokenVerifier.
func (v oidcVerifier) Verify(ctx context.Context, bearerToken string) error {
	if bearerToken == "" {
		return fmt.Errorf("oidc: no bearer token")
	}

	payload, err := idtoken.Validate(ctx, bearerToken, v.audience)
	if err != nil {
		return fmt.Errorf("oidc: validating token: %w", err)
	}

	email, _ := payload.Claims["email"].(string)
	if email != v.serviceAccount {
		return fmt.Errorf("oidc: token belongs to %q, want %q", email, v.serviceAccount)
	}

	if verified, ok := payload.Claims["email_verified"].(bool); ok && !verified {
		return fmt.Errorf("oidc: token email is not verified")
	}

	return nil
}
```

- [ ] **Step 4: Wire it into main.go**

In `cmd/api/main.go`, replace the body of `serve` so the pool is optional and the ingest dependencies are built when enabled:

```go
func serve(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	if cfg.Migrate.OnStart {
		if err := migrateUp(cfg, log); err != nil {
			return err
		}
	}

	deps := httpapi.Deps{
		Logger:     log,
		Version:    version,
		Prodlike:   cfg.IsProduction(),
		EnableBurn: cfg.Features.BurnEndpoint,
	}

	// The ingest role runs without a database. Assigning a nil *pgxpool.Pool
	// to the Pinger interface would produce a non-nil interface holding a nil
	// pointer, so the field is only set when a pool actually exists.
	if cfg.DB.DSN != "" {
		pool, err := database.NewPool(ctx, cfg.DB)
		if err != nil {
			return fmt.Errorf("database: %w", err)
		}
		defer pool.Close()

		log.Info("database connected", slog.Int("max_conns", cfg.DB.MaxConns))
		deps.DB = pool
		deps.Tasks = task.NewService(task.NewPostgresRepository(pool), log)
	}

	if cfg.Ingest.Enabled {
		closeIngest, err := wireIngest(ctx, cfg, log, &deps)
		if err != nil {
			return err
		}
		defer closeIngest()
	}

	srv := httpapi.NewServer(cfg.HTTP, httpapi.NewRouter(deps), log)
	if err := srv.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("http server: %w", err)
	}

	return nil
}

// wireIngest builds the ingest dependencies and returns a function releasing
// the clients it opened.
func wireIngest(ctx context.Context, cfg config.Config, log *slog.Logger, deps *httpapi.Deps) (func(), error) {
	sink, err := bqsink.New(ctx, bqsink.Config{
		Project: cfg.Ingest.BQProject,
		Dataset: cfg.Ingest.BQDataset,
		Table:   cfg.Ingest.BQTable,
	})
	if err != nil {
		return nil, fmt.Errorf("ingest: %w", err)
	}

	parked, err := quarantine.NewGCS(ctx, cfg.Ingest.QuarantineBucket)
	if err != nil {
		_ = sink.Close()

		return nil, fmt.Errorf("ingest: %w", err)
	}

	registry := consumption.NewRegistry()
	xmlfmt.NewESMP(cfg.Ingest.BatchRows).Register(registry)

	deps.EnableIngest = true
	deps.Ingest = consumption.NewService(registry, sink, log)
	deps.Verifier = httpapi.NewOIDCVerifier(cfg.Ingest.Audience, cfg.Ingest.PushServiceAccount)
	deps.Quarantine = parked

	log.Info("ingest enabled",
		slog.String("dataset", cfg.Ingest.BQDataset),
		slog.String("table", cfg.Ingest.BQTable),
		slog.Int("batch_rows", cfg.Ingest.BatchRows))

	return func() {
		if err := sink.Close(); err != nil {
			log.Error("closing sink", slog.String("error", err.Error()))
		}
		if err := parked.Close(); err != nil {
			log.Error("closing quarantine", slog.String("error", err.Error()))
		}
	}, nil
}
```

Add the new imports to `cmd/api/main.go`: `internal/consumption`, `internal/consumption/bqsink`, `internal/consumption/quarantine`, `internal/consumption/xmlfmt`.

- [ ] **Step 5: Verify the API service still starts and the ingest role needs no database**

Run: `go build ./... && go test ./... && make lint`
Expected: everything PASS.

Run: `DATABASE_URL= POSTGRES_HOST= ./bin/api` after `make build`
Expected: exits non-zero with `config: DATABASE_URL (or POSTGRES_*) must be set` — the API role still fails fast.

- [ ] **Step 6: Commit**

```bash
git add internal/httpapi/oidc.go internal/httpapi/oidc_test.go cmd/api/main.go go.mod go.sum
git commit -m "feat(api): wire the ingest role behind its configuration toggle"
```

---

### Task 10: Terraform — warehouse module

**Files:**
- Create: `deploy/terraform/gcp/modules/warehouse/main.tf`
- Create: `deploy/terraform/gcp/modules/warehouse/variables.tf`
- Create: `deploy/terraform/gcp/modules/warehouse/outputs.tf`

**Interfaces:**
- Produces: variables `project_id, region, name, dataset_id, table_id, writer_service_account`; outputs `dataset_id`, `table_id`, `view_id`.

- [ ] **Step 1: Write the variables**

`deploy/terraform/gcp/modules/warehouse/variables.tf`:

```hcl
variable "project_id" {
  description = "GCP project that owns the dataset"
  type        = string
}

variable "region" {
  description = "Dataset location; must match where the service runs"
  type        = string
  default     = "europe-north1"
}

variable "name" {
  description = "Application name, used as a label"
  type        = string
}

variable "dataset_id" {
  description = "BigQuery dataset holding the readings table"
  type        = string
  default     = "energy"
}

variable "table_id" {
  description = "Table receiving the parsed readings"
  type        = string
  default     = "readings"
}

variable "writer_service_account" {
  description = "Service account email allowed to append rows"
  type        = string
}
```

- [ ] **Step 2: Write the module**

`deploy/terraform/gcp/modules/warehouse/main.tf`:

```hcl
resource "google_bigquery_dataset" "readings" {
  dataset_id  = var.dataset_id
  location    = var.region
  description = "Energy consumption interval readings ingested from Pub/Sub"

  labels = {
    app = var.name
  }
}

# Rows are append-only: corrections and Pub/Sub redeliveries both arrive as new
# rows and are resolved by the view below, never by updating in place.
resource "google_bigquery_table" "readings" {
  dataset_id          = google_bigquery_dataset.readings.dataset_id
  table_id            = var.table_id
  deletion_protection = true

  time_partitioning {
    type  = "DAY"
    field = "interval_start"
  }

  clustering = ["metering_point_id", "direction"]

  schema = jsonencode([
    { name = "metering_point_id", type = "STRING", mode = "REQUIRED" },
    { name = "interval_start", type = "TIMESTAMP", mode = "REQUIRED" },
    { name = "resolution_sec", type = "INTEGER", mode = "REQUIRED" },
    { name = "value", type = "FLOAT", mode = "REQUIRED" },
    { name = "unit", type = "STRING", mode = "REQUIRED" },
    { name = "quality", type = "STRING", mode = "REQUIRED" },
    { name = "direction", type = "STRING", mode = "REQUIRED" },
    { name = "source_message_id", type = "STRING", mode = "REQUIRED" },
    { name = "ingested_at", type = "TIMESTAMP", mode = "REQUIRED" },
  ])
}

# The read surface. A later correction wins because it was ingested later.
resource "google_bigquery_table" "readings_current" {
  dataset_id          = google_bigquery_dataset.readings.dataset_id
  table_id            = "${var.table_id}_current"
  deletion_protection = false

  view {
    use_legacy_sql = false
    query          = <<-SQL
      SELECT * EXCEPT(rn) FROM (
        SELECT *, ROW_NUMBER() OVER (
          PARTITION BY metering_point_id, interval_start, direction
          ORDER BY ingested_at DESC) AS rn
        FROM `${var.project_id}.${google_bigquery_dataset.readings.dataset_id}.${google_bigquery_table.readings.table_id}`
      ) WHERE rn = 1
    SQL
  }
}

resource "google_bigquery_dataset_iam_member" "writer" {
  dataset_id = google_bigquery_dataset.readings.dataset_id
  role       = "roles/bigquery.dataEditor"
  member     = "serviceAccount:${var.writer_service_account}"
}
```

`outputs.tf`:

```hcl
output "dataset_id" {
  description = "Dataset holding the readings"
  value       = google_bigquery_dataset.readings.dataset_id
}

output "table_id" {
  description = "Append-only readings table"
  value       = google_bigquery_table.readings.table_id
}

output "view_id" {
  description = "Deduplicating view to query"
  value       = google_bigquery_table.readings_current.table_id
}
```

- [ ] **Step 3: Validate**

Run: `cd deploy/terraform/gcp && terraform init -backend=false && terraform validate`
Expected: `Success! The configuration is valid.`

- [ ] **Step 4: Commit**

```bash
git add deploy/terraform/gcp/modules/warehouse
git commit -m "feat(terraform): BigQuery warehouse module for readings"
```

---

### Task 11: Terraform — messaging module

**Files:**
- Create: `deploy/terraform/gcp/modules/messaging/main.tf`
- Create: `deploy/terraform/gcp/modules/messaging/variables.tf`
- Create: `deploy/terraform/gcp/modules/messaging/outputs.tf`

**Interfaces:**
- Produces: variables `project_id, name, topic_name, create_topic, push_endpoint, push_service_account, ack_deadline_seconds, max_delivery_attempts`; outputs `topic`, `subscription`, `dead_letter_topic`.

- [ ] **Step 1: Write the variables**

`deploy/terraform/gcp/modules/messaging/variables.tf`:

```hcl
variable "project_id" {
  description = "GCP project that owns the subscription"
  type        = string
}

variable "name" {
  description = "Application name; prefixes the resources"
  type        = string
}

variable "topic_name" {
  description = "Upstream topic carrying the XML documents"
  type        = string
}

variable "create_topic" {
  description = "Create the topic here (development) instead of referencing one another system owns"
  type        = bool
  default     = false
}

variable "push_endpoint" {
  description = "Full URL of the ingest endpoint, including the path"
  type        = string
}

variable "push_service_account" {
  description = "Service account Pub/Sub signs the OIDC token with"
  type        = string
}

variable "ack_deadline_seconds" {
  description = "How long the handler has to parse and store one message"
  type        = number
  default     = 60
}

variable "max_delivery_attempts" {
  description = "Deliveries before a message goes to the dead-letter topic"
  type        = number
  default     = 5
}
```

- [ ] **Step 2: Write the module**

`deploy/terraform/gcp/modules/messaging/main.tf`:

```hcl
data "google_project" "this" {
  project_id = var.project_id
}

# The Pub/Sub service agent moves messages to the dead-letter topic and acks
# them on the subscription, so it needs rights on both.
locals {
  pubsub_agent = "serviceAccount:service-${data.google_project.this.number}@gcp-sa-pubsub.iam.gserviceaccount.com"
}

resource "google_pubsub_topic" "source" {
  count = var.create_topic ? 1 : 0
  name  = var.topic_name
}

data "google_pubsub_topic" "source" {
  count = var.create_topic ? 0 : 1
  name  = var.topic_name
}

resource "google_pubsub_topic" "dead_letter" {
  name = "${var.name}-ingest-dlq"
}

resource "google_pubsub_subscription" "ingest" {
  name  = "${var.name}-ingest"
  topic = var.create_topic ? google_pubsub_topic.source[0].id : data.google_pubsub_topic.source[0].id

  # Long enough for a multi-megabyte parse plus a BigQuery append.
  ack_deadline_seconds = var.ack_deadline_seconds

  push_config {
    push_endpoint = var.push_endpoint

    oidc_token {
      service_account_email = var.push_service_account
      audience              = var.push_endpoint
    }
  }

  retry_policy {
    minimum_backoff = "10s"
    maximum_backoff = "600s"
  }

  dead_letter_policy {
    dead_letter_topic     = google_pubsub_topic.dead_letter.id
    max_delivery_attempts = var.max_delivery_attempts
  }

  depends_on = [
    google_pubsub_topic_iam_member.dlq_publisher,
  ]
}

resource "google_pubsub_topic_iam_member" "dlq_publisher" {
  topic  = google_pubsub_topic.dead_letter.name
  role   = "roles/pubsub.publisher"
  member = local.pubsub_agent
}

resource "google_pubsub_subscription_iam_member" "agent_subscriber" {
  subscription = google_pubsub_subscription.ingest.name
  role         = "roles/pubsub.subscriber"
  member       = local.pubsub_agent
}

# Nothing consumes the dead-letter topic automatically; this subscription is
# what keeps the messages retrievable while someone investigates.
resource "google_pubsub_subscription" "dead_letter" {
  name                       = "${var.name}-ingest-dlq"
  topic                      = google_pubsub_topic.dead_letter.id
  message_retention_duration = "604800s" # 7 days
}
```

`outputs.tf`:

```hcl
output "topic" {
  description = "Topic the subscription reads from"
  value       = var.create_topic ? google_pubsub_topic.source[0].name : data.google_pubsub_topic.source[0].name
}

output "subscription" {
  description = "Push subscription feeding the ingest service"
  value       = google_pubsub_subscription.ingest.name
}

output "dead_letter_topic" {
  description = "Topic holding messages that failed every delivery"
  value       = google_pubsub_topic.dead_letter.name
}
```

- [ ] **Step 3: Validate**

Run: `cd deploy/terraform/gcp && terraform init -backend=false && terraform validate`
Expected: `Success! The configuration is valid.`

- [ ] **Step 4: Commit**

```bash
git add deploy/terraform/gcp/modules/messaging
git commit -m "feat(terraform): Pub/Sub push subscription with DLQ"
```

---

### Task 12: Terraform — root wiring

**Files:**
- Modify: `deploy/terraform/gcp/main.tf`
- Modify: `deploy/terraform/gcp/variables.tf`
- Modify: `deploy/terraform/gcp/outputs.tf`
- Modify: `deploy/terraform/gcp/locals.tf`

**Interfaces:**
- Consumes: the two new modules and the existing `serverless` module.
- Produces: `create_ingest` toggle; outputs `ingest_url`, `ingest_subscription`, `readings_view`.

- [ ] **Step 1: Add the variables**

Append to `deploy/terraform/gcp/variables.tf`:

```hcl
variable "create_ingest" {
  description = "Create the ingest service, its subscription and the BigQuery dataset"
  type        = bool
  default     = false
}

variable "ingest_topic" {
  description = "Topic carrying the consumption documents"
  type        = string
  default     = "energy-consumption"
}

variable "ingest_create_topic" {
  description = "Create the topic instead of referencing one another system owns"
  type        = bool
  default     = true
}

variable "ingest_cpu" {
  description = "CPU for the ingest service; parsing is CPU-bound"
  type        = string
  default     = "2"
}

variable "ingest_memory" {
  description = "Memory for the ingest service"
  type        = string
  default     = "1Gi"
}
```

- [ ] **Step 2: Add the resources**

Append to `deploy/terraform/gcp/main.tf`:

```hcl
# ----------------------------------------------------------------- ingest
# Everything below is behind create_ingest, so the existing deployments plan
# clean while the toggle is off.

resource "google_service_account" "ingest" {
  count        = var.create_ingest ? 1 : 0
  account_id   = "${var.name}-ingest"
  display_name = "Consumption ingest service"
}

resource "google_storage_bucket" "quarantine" {
  count                       = var.create_ingest ? 1 : 0
  name                        = "${var.project_id}-${var.name}-quarantine"
  location                    = var.region
  force_destroy               = false
  uniform_bucket_level_access = true

  # A persistent upstream defect must not accumulate cost forever.
  lifecycle_rule {
    condition {
      age = 90
    }
    action {
      type = "Delete"
    }
  }
}

resource "google_storage_bucket_iam_member" "ingest_writer" {
  count  = var.create_ingest ? 1 : 0
  bucket = google_storage_bucket.quarantine[0].name
  role   = "roles/storage.objectCreator"
  member = "serviceAccount:${google_service_account.ingest[0].email}"
}

module "warehouse" {
  count  = var.create_ingest ? 1 : 0
  source = "./modules/warehouse"

  project_id             = var.project_id
  region                 = var.region
  name                   = var.name
  writer_service_account = google_service_account.ingest[0].email
}

# The ingest role is the same image with a different configuration.
module "ingest_service" {
  count  = var.create_ingest && var.image != "" ? 1 : 0
  source = "./modules/serverless"

  project_id        = var.project_id
  region            = var.region
  name              = "${var.name}-ingest"
  image             = var.image
  container_port    = var.container_port
  health_check_path = var.health_check_path
  env               = local.ingest_env
  secret_env        = {}
  min_instances     = 0
  max_instances     = var.serverless_max_instances
  cpu               = var.ingest_cpu
  memory            = var.ingest_memory

  depends_on = [module.registry, module.warehouse]
}

# Pub/Sub authenticates as this account; only it may invoke the service.
resource "google_service_account" "pubsub_push" {
  count        = var.create_ingest ? 1 : 0
  account_id   = "${var.name}-push"
  display_name = "Pub/Sub push identity for the ingest service"
}

resource "google_cloud_run_v2_service_iam_member" "push_invoker" {
  count    = var.create_ingest && var.image != "" ? 1 : 0
  project  = var.project_id
  location = var.region
  name     = "${var.name}-ingest"
  role     = "roles/run.invoker"
  member   = "serviceAccount:${google_service_account.pubsub_push[0].email}"

  depends_on = [module.ingest_service]
}

module "messaging" {
  count  = var.create_ingest && var.image != "" ? 1 : 0
  source = "./modules/messaging"

  project_id           = var.project_id
  name                 = var.name
  topic_name           = var.ingest_topic
  create_topic         = var.ingest_create_topic
  push_endpoint        = "${module.ingest_service[0].url}/internal/pubsub/consumption"
  push_service_account = google_service_account.pubsub_push[0].email

  depends_on = [google_cloud_run_v2_service_iam_member.push_invoker]
}
```

In `locals.tf`, add the ingest configuration. The audience must equal the push endpoint the subscription uses:

```hcl
locals {
  ingest_env = var.create_ingest && var.image != "" ? {
    APP_ENV                     = "production"
    LOG_FORMAT                  = "json"
    ENABLE_INGEST_ENDPOINT      = "true"
    PUBSUB_AUDIENCE             = "${module.ingest_service[0].url}/internal/pubsub/consumption"
    PUBSUB_PUSH_SERVICE_ACCOUNT = google_service_account.pubsub_push[0].email
    BQ_PROJECT                  = var.project_id
    BQ_DATASET                  = module.warehouse[0].dataset_id
    BQ_TABLE                    = module.warehouse[0].table_id
    QUARANTINE_BUCKET           = google_storage_bucket.quarantine[0].name
  } : {}
}
```

If `module.ingest_service[0].url` inside `local.ingest_env` creates a cycle — the service needs the env, the env needs the URL — break it by setting `PUBSUB_AUDIENCE` from the deterministic Cloud Run URL instead, or by a second `google_cloud_run_v2_service` revision applied after the first. Run `terraform plan` and let the error tell you; if there is a cycle, use `"https://${var.name}-ingest-${data.google_project.this.number}.${var.region}.run.app/internal/pubsub/consumption"`.

Append to `outputs.tf`:

```hcl
output "ingest_url" {
  description = "Ingest service URL"
  value       = var.create_ingest && var.image != "" ? module.ingest_service[0].url : ""
}

output "ingest_subscription" {
  description = "Push subscription name"
  value       = var.create_ingest && var.image != "" ? module.messaging[0].subscription : ""
}

output "readings_view" {
  description = "Deduplicating view to query"
  value       = var.create_ingest ? "${var.project_id}.${module.warehouse[0].dataset_id}.${module.warehouse[0].view_id}" : ""
}
```

- [ ] **Step 3: Prove the existing deployment is untouched**

Run: `cd deploy/terraform/gcp && terraform init && terraform plan -var-file=terraform.tfvars`
Expected: the same plan as before this task — the ingest resources are all counted out while `create_ingest` is false. If the plan shows changes to existing resources, stop and fix the module wiring before continuing.

- [ ] **Step 4: Commit**

```bash
git add deploy/terraform/gcp/main.tf deploy/terraform/gcp/variables.tf deploy/terraform/gcp/outputs.tf deploy/terraform/gcp/locals.tf
git commit -m "feat(terraform): wire the ingest service behind create_ingest"
```

---

### Task 13: Make targets and smoke test

**Files:**
- Modify: `make/gcp.mk`

**Interfaces:**
- Produces: `ingest-deploy`, `ingest-publish`, `ingest-smoke`, `bq-readings`.

- [ ] **Step 1: Add the targets**

Append to `make/gcp.mk`, matching the help-comment style the file already uses:

```makefile
## ---------------------------------------------------------------- ingest

.PHONY: ingest-deploy
ingest-deploy: ## Terraform: create the ingest service, subscription and dataset
	cd $(TF_GCP) && terraform apply -auto-approve \
		-var="project_id=$(GCP_PROJECT)" \
		-var="region=$(GCP_REGION)" \
		-var="image=$(GCP_IMAGE)" \
		-var="create_ingest=true"

.PHONY: ingest-publish
ingest-publish: ## Publish a sample document: make ingest-publish file=path/to.xml
	@test -n "$(file)" || { echo "usage: make ingest-publish file=<path>"; exit 1; }
	gcloud pubsub topics publish $(INGEST_TOPIC) \
		--project=$(GCP_PROJECT) \
		--message="$$(cat $(file))"

.PHONY: ingest-smoke
ingest-smoke: ## Publish a golden file and wait for its readings to land
	@set -euo pipefail; \
	id="smoke-$$(date +%s)"; \
	sed "s|<mRID>doc-1</mRID>|<mRID>$$id</mRID>|" \
		internal/consumption/xmlfmt/testdata/esmp-valid.xml > /tmp/$$id.xml; \
	$(MAKE) --no-print-directory ingest-publish file=/tmp/$$id.xml; \
	echo "published $$id, waiting for rows..."; \
	for i in $$(seq 1 30); do \
		n=$$(bq --project_id=$(GCP_PROJECT) query --nouse_legacy_sql --format=csv \
			"SELECT COUNT(*) FROM \`$(BQ_DATASET).readings\` WHERE metering_point_id IN ('EE-METER-1','EE-METER-2')" \
			| tail -1); \
		if [ "$$n" -ge 6 ]; then echo "$$n rows present"; exit 0; fi; \
		sleep 2; \
	done; \
	echo "no rows after 60s — check: gcloud run services logs read $(APP_NAME)-ingest"; \
	exit 1

.PHONY: bq-readings
bq-readings: ## Query the deduplicated readings view
	bq --project_id=$(GCP_PROJECT) query --nouse_legacy_sql \
		"SELECT metering_point_id, interval_start, value, unit, quality, direction \
		 FROM \`$(BQ_DATASET).readings_current\` ORDER BY interval_start DESC LIMIT 20"
```

Add the two variables near the other GCP defaults at the top of `make/gcp.mk`:

```makefile
INGEST_TOPIC ?= energy-consumption
BQ_DATASET   ?= energy
```

- [ ] **Step 2: Verify the help output**

Run: `make | grep ingest`
Expected: four lines, one per new target, with their descriptions.

- [ ] **Step 3: Commit**

```bash
git add make/gcp.mk
git commit -m "feat(make): ingest deploy, publish and smoke targets"
```

---

### Task 14: Documentation

**Files:**
- Modify: `README.md`
- Modify: `deploy/README.md`
- Modify: `docs/superpowers/specs/2026-09-19-gcp-consumption-ingest-design.md`

- [ ] **Step 1: Correct the spec's parser signature**

In §5 of the spec, replace the `Parser` interface with the decoder-based contract this plan implements, and add one sentence noting that the registry hands the parser the decoder and the already-read root element. This keeps the spec and the code telling the same story.

- [ ] **Step 2: Update the README**

In the endpoints table, add the ingest route with a note that it only exists on the ingest service:

```markdown
| POST   | `/internal/pubsub/consumption` | Pub/Sub push: parse a consumption document into BigQuery — only when `ENABLE_INGEST_ENDPOINT=true` |
```

In the layout block, add:

```
internal/consumption/      ingest domain: readings, parser registry, service, BigQuery sink, quarantine
```

Add an "Ingest" section after "Migrations" covering: the flow in three sentences, the configuration table rows from Task 5, the fact that one image serves both roles, and the `make ingest-*` targets. Add the new settings to the configuration table.

- [ ] **Step 3: Update deploy/README.md**

Add a phase after the Cloud Run one: creating the dataset and subscription with `make ingest-deploy`, verifying with `make ingest-smoke`, reading quarantined payloads out of the bucket, and the cost note that BigQuery storage is negligible at this volume while query cost is the thing to watch.

- [ ] **Step 4: Verify every documented command exists**

Run: `make help | grep -E 'ingest-(deploy|publish|smoke)|bq-readings'`
Expected: all four present. Every command named in the README must appear here or in `make help`.

- [ ] **Step 5: Commit**

```bash
git add README.md deploy/README.md docs/superpowers/specs/2026-09-19-gcp-consumption-ingest-design.md
git commit -m "docs: document the consumption ingest path"
```

---

### Task 15: Full verification

- [ ] **Step 1: Run everything CI runs**

Run: `make check`
Expected: fmt-check, vet, lint and tests all pass.

- [ ] **Step 2: Run the race detector**

Run: `make test-race`
Expected: PASS, no data races.

- [ ] **Step 3: Confirm coverage of the new packages**

Run: `go test -cover ./internal/consumption/...`
Expected: every package reports coverage; `consumption` and `xmlfmt` above 80%.

- [ ] **Step 4: Check for the failure modes this plan is trying to avoid**

Run: `grep -rnE 'TODO|FIXME|t\.Skip\(|panic\("' internal/consumption internal/httpapi`
Expected: no matches other than the documented `t.Skip` in the integration test, which is guarded by a build tag and by missing configuration.

- [ ] **Step 5: Verify the acceptance criteria from spec §1**

| Criterion | Evidence |
| --- | --- |
| 1 — readings queryable within seconds | `make ingest-smoke` exits 0 |
| 2 — bad payload never blocks, stays retrievable | publish a truncated file; `gsutil ls gs://<bucket>/$(date +%Y/%m/%d)/` lists it; the subscription backlog stays at zero |
| 3 — redelivery does not duplicate | publish the same file twice; `make bq-readings` shows one row per interval |
| 4 — bounded memory, benchmark present | `go test ./internal/consumption/xmlfmt/ -bench=. -benchmem` reports allocations independent of document size |
| 5 — tasks API unaffected | `terraform plan` with `create_ingest=false` is empty; `go test ./...` passes |

- [ ] **Step 6: Final commit**

```bash
git add -A
git commit -m "chore: verify consumption ingest end to end"
```
