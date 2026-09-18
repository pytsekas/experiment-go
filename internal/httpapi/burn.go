package httpapi

import (
	"net/http"
	"runtime"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// maxBurnDuration caps how long a single burn request may spin, so a stray
// load test cannot pin a pod forever.
const maxBurnDuration = 2 * time.Second

// burnHandler spends CPU on purpose. It exists so autoscaling and load-test
// experiments have a knob that actually moves the CPU graph — the CRUD
// endpoints are dominated by database wait, not compute.
//
// It is only registered when ENABLE_BURN_ENDPOINT=true.
type burnHandler struct{}

// burn handles GET /api/v1/burn?ms=50.
func (burnHandler) burn(c *gin.Context) {
	ms, err := strconv.Atoi(c.DefaultQuery("ms", "50"))
	if err != nil || ms < 0 {
		respondError(c, http.StatusBadRequest, "ms must be a non-negative integer")

		return
	}

	d := time.Duration(ms) * time.Millisecond
	if d > maxBurnDuration {
		d = maxBurnDuration
	}

	start := time.Now()
	iterations := spin(d)

	c.JSON(http.StatusOK, gin.H{
		"requested_ms": ms,
		"actual_ms":    time.Since(start).Milliseconds(),
		"iterations":   iterations,
		"cpus":         runtime.NumCPU(),
	})
}

// spin busy-loops for d and returns how many iterations it managed. The
// accumulator is returned so the compiler cannot optimise the loop away.
func spin(d time.Duration) int64 {
	deadline := time.Now().Add(d)

	var iterations int64
	for {
		// Check the clock every so often instead of every iteration.
		for i := 0; i < 1000; i++ {
			iterations++
		}
		if time.Now().After(deadline) {
			return iterations
		}
	}
}
