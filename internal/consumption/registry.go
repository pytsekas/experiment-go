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
	return &Registry{parsers: make(map[xml.Name]Parser)}
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
