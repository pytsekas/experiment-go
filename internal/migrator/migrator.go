// Package migrator runs the embedded SQL migrations using golang-migrate as a
// library, so migrations ship inside the service binary itself.
package migrator

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	// Registers the "pgx5" database driver used below.
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/pytsekas/experiment-go/migrations"
)

// Migrator wraps golang-migrate with the embedded migration source.
type Migrator struct {
	m   *migrate.Migrate
	log *slog.Logger
}

// New opens a migrator against dsn. Close it when done.
func New(dsn string, log *slog.Logger) (*Migrator, error) {
	if log == nil {
		log = slog.Default()
	}

	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("open embedded migrations: %w", err)
	}

	migrateDSN, err := toMigrateDSN(dsn)
	if err != nil {
		return nil, err
	}

	m, err := migrate.NewWithSourceInstance("iofs", src, migrateDSN)
	if err != nil {
		return nil, fmt.Errorf("init migrator: %w", err)
	}

	return &Migrator{m: m, log: log}, nil
}

// Close releases the migrator's database connection.
func (mg *Migrator) Close() error {
	srcErr, dbErr := mg.m.Close()

	return errors.Join(srcErr, dbErr)
}

// Up applies every pending migration. It is safe to call on every boot:
// golang-migrate takes a Postgres advisory lock, so concurrent instances
// queue up instead of fighting, and "no change" is not an error.
func (mg *Migrator) Up() error {
	before, _, _ := mg.m.Version()

	switch err := mg.m.Up(); {
	case errors.Is(err, migrate.ErrNoChange):
		mg.log.Info("database schema up to date", slog.Uint64("schema_version", uint64(before)))

		return nil
	case err != nil:
		return fmt.Errorf("apply migrations: %w", err)
	}

	after, dirty, err := mg.m.Version()
	if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	mg.log.Info("migrations applied",
		slog.Uint64("from", uint64(before)),
		slog.Uint64("to", uint64(after)),
		slog.Bool("dirty", dirty),
	)

	return nil
}

// Down rolls back n migrations, or all of them when n is 0.
func (mg *Migrator) Down(n int) error {
	if n <= 0 {
		if err := mg.m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			return fmt.Errorf("roll back all migrations: %w", err)
		}
		mg.log.Info("all migrations rolled back")

		return nil
	}

	if err := mg.m.Steps(-n); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("roll back %d migration(s): %w", n, err)
	}
	mg.log.Info("migrations rolled back", slog.Int("steps", n))

	return nil
}

// Version reports the current schema version and whether it is dirty.
// A version of 0 with ok=false means no migration has been applied yet.
func (mg *Migrator) Version() (version uint, dirty, ok bool, err error) {
	v, d, err := mg.m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, false, nil
	}
	if err != nil {
		return 0, false, false, fmt.Errorf("read schema version: %w", err)
	}

	return v, d, true, nil
}

// Force pins the recorded version and clears the dirty flag. Use it only after
// repairing a failed migration by hand.
func (mg *Migrator) Force(version int) error {
	if err := mg.m.Force(version); err != nil {
		return fmt.Errorf("force version %d: %w", version, err)
	}
	mg.log.Warn("schema version forced", slog.Int("schema_version", version))

	return nil
}

// toMigrateDSN rewrites a postgres:// DSN to the pgx5:// scheme that
// golang-migrate's pgx v5 driver registers itself under.
func toMigrateDSN(dsn string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parse database dsn: %w", err)
	}

	switch strings.ToLower(u.Scheme) {
	case "postgres", "postgresql", "pgx", "pgx5":
		u.Scheme = "pgx5"
	case "":
		return "", errors.New("database dsn has no scheme, expected postgres://")
	default:
		return "", fmt.Errorf("unsupported database scheme %q, expected postgres://", u.Scheme)
	}

	return u.String(), nil
}
