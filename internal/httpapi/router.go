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
	Logger *slog.Logger
	// Tasks may be nil: the ingest role runs this binary without a database,
	// and the /api/v1/tasks group is then left unregistered rather than
	// handed a nil service.
	Tasks    task.Service
	DB       Pinger // may be nil, readiness then only reports the process
	Version  string
	Prodlike bool // registers gin in release mode and trims debug output
	// EnableBurn exposes GET /api/v1/burn?ms=N, a deliberate CPU sink used for
	// autoscaling and load-test experiments. Keep it off in production.
	EnableBurn bool
	// EnableIngest registers POST /internal/pubsub/consumption. It is on for
	// the ingest service only; the API service must not expose it.
	EnableIngest bool
	Ingest       IngestService
	Verifier     TokenVerifier
	Quarantine   Quarantiner
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
		// Tasks is nil for the ingest role, which runs without a database.
		// Leaving the group unregistered there means a request falls through
		// to NoRoute instead of reaching a handler holding a nil service.
		if d.Tasks != nil {
			tasks := taskHandler{svc: d.Tasks, log: d.Logger}
			v1.GET("/tasks", tasks.list)
			v1.POST("/tasks", tasks.create)
			v1.GET("/tasks/:id", tasks.get)
			v1.PUT("/tasks/:id", tasks.update)
			v1.DELETE("/tasks/:id", tasks.remove)
		}

		// Burn does not depend on Tasks, so it is wired independently and
		// still works on an ingest deployment that opts into it.
		if d.EnableBurn {
			d.Logger.Warn("burn endpoint enabled", slog.String("path", "/api/v1/burn"))
			v1.GET("/burn", burnHandler{}.burn)
		}
	}

	if d.EnableIngest {
		ingest := ingestHandler{svc: d.Ingest, verifier: d.Verifier, quarantine: d.Quarantine, log: d.Logger}
		r.POST("/internal/pubsub/consumption", ingest.consume)
	}

	r.NoRoute(func(c *gin.Context) {
		respondError(c, http.StatusNotFound, "route not found")
	})
	r.NoMethod(func(c *gin.Context) {
		respondError(c, http.StatusMethodNotAllowed, "method not allowed")
	})

	return r
}
