package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	requestIDHeader = "X-Request-ID"
	requestIDKey    = "request_id"
)

// requestID attaches a request ID to the context, log records and response.
func requestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(requestIDHeader)
		if id == "" {
			id = uuid.NewString()
		}
		c.Set(requestIDKey, id)
		c.Header(requestIDHeader, id)
		c.Next()
	}
}

// requestLogger logs one structured line per request.
func requestLogger(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		if raw := c.Request.URL.RawQuery; raw != "" {
			path += "?" + raw
		}

		c.Next()

		attrs := []any{
			slog.String("method", c.Request.Method),
			slog.String("path", path),
			slog.Int("status", c.Writer.Status()),
			slog.Duration("duration", time.Since(start)),
			slog.String("ip", c.ClientIP()),
			slog.String(requestIDKey, c.GetString(requestIDKey)),
		}

		switch {
		case len(c.Errors) > 0:
			log.Error("request failed", append(attrs, slog.String("error", c.Errors.String()))...)
		case c.Writer.Status() >= http.StatusInternalServerError:
			log.Error("request completed", attrs...)
		case c.Writer.Status() >= http.StatusBadRequest:
			log.Warn("request completed", attrs...)
		default:
			log.Info("request completed", attrs...)
		}
	}
}

// recovery turns a panic into a 500 response and an error log line.
func recovery(log *slog.Logger) gin.HandlerFunc {
	return gin.CustomRecoveryWithWriter(nil, func(c *gin.Context, err any) {
		log.Error("panic recovered",
			slog.Any("panic", err),
			slog.String("path", c.Request.URL.Path),
			slog.String(requestIDKey, c.GetString(requestIDKey)),
		)
		respondError(c, http.StatusInternalServerError, "internal server error")
	})
}
