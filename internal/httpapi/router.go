// Package httpapi contains the HTTP transport layer: router, middleware,
// handlers and the server lifecycle.
package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/pytsekas/experiment-go/internal/task"
)

// Deps are the dependencies the router needs.
type Deps struct {
	Logger   *slog.Logger
	Tasks    task.Service
	DB       Pinger // may be nil, readiness then only reports the process
	Version  string
	Prodlike bool // registers gin in release mode and trims debug output
	// EnableBurn exposes GET /api/v1/burn?ms=N, a deliberate CPU sink used for
	// autoscaling and load-test experiments. Keep it off in production.
	EnableBurn bool
}

// NewRouter builds the fully wired gin engine.
func NewRouter(d Deps) *gin.Engine {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Prodlike {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.HandleMethodNotAllowed = true
	r.Use(requestID(), requestLogger(d.Logger), recovery(d.Logger))

	health := healthHandler{db: d.DB, version: d.Version}
	r.GET("/healthz", health.live)
	r.GET("/readyz", health.ready)

	v1 := r.Group("/api/v1")
	{
		tasks := taskHandler{svc: d.Tasks, log: d.Logger}
		v1.GET("/tasks", tasks.list)
		v1.POST("/tasks", tasks.create)
		v1.GET("/tasks/:id", tasks.get)
		v1.PUT("/tasks/:id", tasks.update)
		v1.DELETE("/tasks/:id", tasks.remove)

		if d.EnableBurn {
			d.Logger.Warn("burn endpoint enabled", slog.String("path", "/api/v1/burn"))
			v1.GET("/burn", burnHandler{}.burn)
		}
	}

	r.NoRoute(func(c *gin.Context) {
		respondError(c, http.StatusNotFound, "route not found")
	})
	r.NoMethod(func(c *gin.Context) {
		respondError(c, http.StatusMethodNotAllowed, "method not allowed")
	})

	return r
}
