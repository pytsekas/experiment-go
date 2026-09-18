package main

import (
	"fmt"
	"log/slog"
	"strconv"

	"github.com/pytsekas/experiment-go/internal/config"
	"github.com/pytsekas/experiment-go/internal/migrator"
)

// migrateUp applies pending migrations during startup (MIGRATE_ON_START=true).
func migrateUp(cfg config.Config, log *slog.Logger) error {
	mg, err := migrator.New(cfg.DB.DSN, log)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := mg.Close(); cerr != nil {
			log.Warn("closing migrator", slog.String("error", cerr.Error()))
		}
	}()

	return mg.Up()
}

// runMigrate implements the `api migrate ...` subcommands.
func runMigrate(args []string, cfg config.Config, log *slog.Logger) error {
	cmd := "up"
	if len(args) > 0 {
		cmd = args[0]
	}

	mg, err := migrator.New(cfg.DB.DSN, log)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := mg.Close(); cerr != nil {
			log.Warn("closing migrator", slog.String("error", cerr.Error()))
		}
	}()

	switch cmd {
	case "up":
		return mg.Up()

	case "down":
		steps := 1
		if len(args) > 1 {
			if args[1] == "all" {
				steps = 0
			} else {
				n, err := strconv.Atoi(args[1])
				if err != nil || n < 1 {
					return fmt.Errorf("migrate down: %q is not a positive number or \"all\"", args[1])
				}
				steps = n
			}
		}

		return mg.Down(steps)

	case "version":
		v, dirty, ok, err := mg.Version()
		if err != nil {
			return err
		}
		if !ok {
			fmt.Println("no migrations applied yet")

			return nil
		}
		state := "clean"
		if dirty {
			state = "dirty"
		}
		fmt.Printf("version %d (%s)\n", v, state)

		return nil

	case "force":
		if len(args) < 2 {
			return fmt.Errorf("migrate force: expected a version, e.g. api migrate force 1")
		}
		v, err := strconv.Atoi(args[1])
		if err != nil || v < 0 {
			return fmt.Errorf("migrate force: %q is not a valid version", args[1])
		}

		return mg.Force(v)

	default:
		return fmt.Errorf("unknown migrate command %q, expected: up | down [n|all] | version | force <v>", cmd)
	}
}
