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
// message id is sanitized via allowlist: keep [A-Za-z0-9._-], replace other
// bytes with _. A nanosecond timestamp ensures distinct ids cannot collide.
func ObjectName(now time.Time, messageID string) string {
	safe := sanitizeID(messageID)

	return fmt.Sprintf("%s/%s-%s.xml", now.UTC().Format("2006/01/02"), now.Format("150405.000000000"), safe)
}

// sanitizeID reduces a messageID by allowlist: keep [A-Za-z0-9._-], replace
// other bytes with _, strip leading/trailing underscores and dots, cap at 128 chars.
func sanitizeID(id string) string {
	var buf strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			buf.WriteRune(r)
		} else {
			buf.WriteRune('_')
		}
	}

	safe := buf.String()

	// Strip leading and trailing underscores and dots
	safe = strings.Trim(safe, "_.")

	// Cap at 128 characters
	if len(safe) > 128 {
		safe = safe[:128]
	}

	// Fall back if nothing survives
	if safe == "" {
		safe = "unknown"
	}

	return safe
}

// sanitizeMetadata removes control characters from metadata values to prevent
// embedded newlines, carriage returns, and other non-printable characters.
func sanitizeMetadata(s string) string {
	var buf strings.Builder
	for _, r := range s {
		// Keep printable ASCII (0x20-0x7E) and tab; replace everything else with _
		if (r >= 0x20 && r <= 0x7E) || r == '\t' {
			buf.WriteRune(r)
		} else {
			buf.WriteRune('_')
		}
	}
	return buf.String()
}

// Quarantine stores payload and records why it was rejected in the object's
// metadata, where it is visible without downloading the file.
func (g *GCS) Quarantine(ctx context.Context, messageID string, payload []byte, reason string) error {
	object := g.client.Bucket(g.bucket).Object(ObjectName(time.Now(), messageID))

	w := object.NewWriter(ctx)
	w.ContentType = "application/xml"
	w.Metadata = map[string]string{
		"message_id": sanitizeMetadata(messageID),
		"reason":     sanitizeMetadata(reason),
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
