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

			if valErr := batch[i].Validate(); valErr != nil {
				return valErr
			}
		}

		if writeErr := s.sink.Write(ctx, batch); writeErr != nil {
			return writeErr
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
