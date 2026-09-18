package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/pytsekas/experiment-go/internal/config"
)

// Server owns the HTTP listener and its shutdown behaviour.
type Server struct {
	http     *http.Server
	log      *slog.Logger
	shutdown config.HTTP
}

// NewServer wraps a handler in a configured http.Server.
func NewServer(cfg config.HTTP, handler http.Handler, log *slog.Logger) *Server {
	return &Server{
		http: &http.Server{
			Addr:              cfg.Addr(),
			Handler:           handler,
			ReadTimeout:       cfg.ReadTimeout,
			ReadHeaderTimeout: cfg.ReadTimeout,
			WriteTimeout:      cfg.WriteTimeout,
			IdleTimeout:       cfg.IdleTimeout,
		},
		log:      log,
		shutdown: cfg,
	}
}

// Run serves until ctx is cancelled, then drains in-flight requests within the
// configured shutdown timeout. It returns the first error that mattered.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)

	go func() {
		s.log.Info("http server listening", slog.String("addr", s.http.Addr))
		if err := s.http.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("listen: %w", err)

			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		s.log.Info("shutdown signal received, draining connections",
			slog.Duration("timeout", s.shutdown.ShutdownTimeout))

		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdown.ShutdownTimeout)
		defer cancel()

		if err := s.http.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("graceful shutdown: %w", err)
		}

		s.log.Info("http server stopped cleanly")

		return nil
	}
}
