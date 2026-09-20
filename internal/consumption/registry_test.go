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

var _ consumption.Parser = (*stubParser)(nil)

func (s *stubParser) Parse(ctx context.Context, d *xml.Decoder, root xml.StartElement, emit func([]consumption.Reading) error) error {
	if s.parseFn == nil {
		s.t.Fatal("unexpected Parse call")
	}

	return s.parseFn(ctx, d, root, emit)
}

func TestRegistryForMatchesNamespaceAndLocalName(t *testing.T) {
	want := &stubParser{t: t}
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
	reg.Register("", "Doc", &stubParser{t: t})

	d := xml.NewDecoder(strings.NewReader("<?xml version=\"1.0\"?>\n<!-- a comment -->\n<Doc/>"))

	if _, _, err := reg.For(d); err != nil {
		t.Fatalf("For: %v", err)
	}
}
