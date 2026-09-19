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
