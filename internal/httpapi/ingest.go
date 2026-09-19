package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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

	// The body is read in full, rather than decoded straight off the wire,
	// because a body that is not valid JSON at all makes json.Decoder fail on
	// the first malformed byte before it has read enough to ever trip
	// MaxBytesReader. Reading fully first means an oversized body is always
	// caught as too large, regardless of what it contains.
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			h.log.ErrorContext(ctx, "push body too large", slog.Int64("limit_bytes", maxIngestBody))
			respondError(c, http.StatusRequestEntityTooLarge, "payload too large")

			return
		}

		h.log.ErrorContext(ctx, "reading push body failed, will be retried", slog.String("error", err.Error()))
		respondError(c, http.StatusServiceUnavailable, "temporarily unable to read request body")

		return
	}

	var envelope pushEnvelope
	if err = json.Unmarshal(body, &envelope); err != nil {
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
