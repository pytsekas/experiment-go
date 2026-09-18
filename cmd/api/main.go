// Command api is the entrypoint of the experiment-go REST service.
//
// Usage:
//
//	api                      start the HTTP server
//	api migrate up           apply pending migrations
//	api migrate down [n|all] roll back n migrations (default 1)
//	api migrate version      print the current schema version
//	api migrate force <v>    clear a dirty state at version v
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/pytsekas/experiment-go/internal/config"
	"github.com/pytsekas/experiment-go/internal/database"
	"github.com/pytsekas/experiment-go/internal/httpapi"
	"github.com/pytsekas/experiment-go/internal/logger"
	"github.com/pytsekas/experiment-go/internal/task"
)

// version is injected at build time: -ldflags "-X main.version=$(git describe)".
var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		// The logger may not exist yet, so fail loudly on stderr too.
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := logger.New(os.Stdout, cfg.Logger.Level, cfg.Logger.Format).With(
		slog.String("service", "experiment-go"),
		slog.String("version", version),
		slog.String("env", cfg.Env),
	)
	slog.SetDefault(log)

	// ctx is cancelled on SIGINT/SIGTERM, which starts graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	switch {
	case len(args) == 0:
		return serve(ctx, cfg, log)
	case args[0] == "migrate":
		return runMigrate(args[1:], cfg, log)
	default:
		return fmt.Errorf("unknown command %q, try: api | api migrate up", args[0])
	}
}

func serve(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	if cfg.Migrate.OnStart {
		if err := migrateUp(cfg, log); err != nil {
			return err
		}
	}

	pool, err := database.NewPool(ctx, cfg.DB)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer pool.Close()

	log.Info("database connected", slog.Int("max_conns", cfg.DB.MaxConns))

	router := httpapi.NewRouter(httpapi.Deps{
		Logger:     log,
		Tasks:      task.NewService(task.NewPostgresRepository(pool), log),
		DB:         pool,
		Version:    version,
		Prodlike:   cfg.IsProduction(),
		EnableBurn: cfg.Features.BurnEndpoint,
	})

	srv := httpapi.NewServer(cfg.HTTP, router, log)
	if err := srv.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("http server: %w", err)
	}

	return nil
}
