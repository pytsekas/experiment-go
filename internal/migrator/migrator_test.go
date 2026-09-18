package migrator

import (
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/pytsekas/experiment-go/migrations"
)

func TestEmbeddedMigrationsAreValid(t *testing.T) {
	t.Parallel()

	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		t.Fatalf("embedded migrations are not a valid source: %v", err)
	}
	t.Cleanup(func() {
		if err := src.Close(); err != nil {
			t.Errorf("close source: %v", err)
		}
	})

	first, err := src.First()
	if err != nil {
		t.Fatalf("no migrations embedded: %v", err)
	}
	if first != 1 {
		t.Errorf("first migration version = %d, want 1", first)
	}

	// Every version must have both an up and a down file.
	version := first
	for {
		if _, _, err := src.ReadUp(version); err != nil {
			t.Errorf("version %d has no up migration: %v", version, err)
		}
		if _, _, err := src.ReadDown(version); err != nil {
			t.Errorf("version %d has no down migration: %v", version, err)
		}

		next, err := src.Next(version)
		if err != nil {
			return // no more migrations
		}
		version = next
	}
}

func TestToMigrateDSN(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		dsn     string
		want    string
		wantErr string
	}{
		{
			name: "postgres scheme is rewritten to pgx5",
			dsn:  "postgres://app:app@localhost:5432/experiment?sslmode=disable",
			want: "pgx5://app:app@localhost:5432/experiment?sslmode=disable",
		},
		{
			name: "postgresql scheme is rewritten too",
			dsn:  "postgresql://app@db:5432/experiment",
			want: "pgx5://app@db:5432/experiment",
		},
		{
			name: "already pgx5 is left alone",
			dsn:  "pgx5://app@db:5432/experiment",
			want: "pgx5://app@db:5432/experiment",
		},
		{
			name:    "keyword dsn is rejected with a clear message",
			dsn:     "host=localhost user=app",
			wantErr: "no scheme",
		},
		{
			name:    "other databases are rejected",
			dsn:     "mysql://app@localhost:3306/experiment",
			wantErr: "unsupported database scheme",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := toMigrateDSN(tc.dsn)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("toMigrateDSN(%q) error = nil, want %q", tc.dsn, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %v, want it to mention %q", err, tc.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("toMigrateDSN(%q) error = %v", tc.dsn, err)
			}
			if got != tc.want {
				t.Errorf("toMigrateDSN(%q) = %q, want %q", tc.dsn, got, tc.want)
			}
		})
	}
}
