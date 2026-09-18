package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// Pinger is anything that can be health-checked, e.g. a *pgxpool.Pool.
type Pinger interface {
	Ping(ctx context.Context) error
}

const readinessTimeout = 2 * time.Second

// healthHandler serves liveness and readiness probes.
type healthHandler struct {
	db      Pinger
	version string
}

// live reports that the process is up. It must not touch dependencies:
// a failing database should not get the container killed.
func (h healthHandler) live(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "version": h.version})
}

// ready reports whether the service can serve traffic right now.
func (h healthHandler) ready(c *gin.Context) {
	if h.db == nil {
		c.JSON(http.StatusOK, gin.H{"status": "ready"})

		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), readinessTimeout)
	defer cancel()

	if err := h.db.Ping(ctx); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status":     "unavailable",
			"dependency": "postgres",
		})

		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ready"})
}
