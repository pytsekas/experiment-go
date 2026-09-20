package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db?sslmode=disable")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.HTTP.Addr() != "0.0.0.0:8080" {
		t.Errorf("Addr() = %q, want 0.0.0.0:8080", cfg.HTTP.Addr())
	}
	if cfg.HTTP.ShutdownTimeout != 15*time.Second {
		t.Errorf("ShutdownTimeout = %v, want 15s", cfg.HTTP.ShutdownTimeout)
	}
	if cfg.IsProduction() {
		t.Error("IsProduction() = true, want false for the development default")
	}
}

func TestLoadComposesDSNFromPostgresVars(t *testing.T) {
	t.Setenv("POSTGRES_HOST", "db")
	t.Setenv("POSTGRES_USER", "app")
	t.Setenv("POSTGRES_PASSWORD", "secret")
	t.Setenv("POSTGRES_DB", "experiment")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want := "postgres://app:secret@db:5432/experiment?sslmode=disable"
	if cfg.DB.DSN != want {
		t.Errorf("DSN = %q, want %q", cfg.DB.DSN, want)
	}
}

func TestLoadValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{
			name:    "missing dsn",
			env:     map[string]string{},
			wantErr: "DATABASE_URL",
		},
		{
			name:    "port out of range",
			env:     map[string]string{"DATABASE_URL": "postgres://localhost/db", "HTTP_PORT": "99999"},
			wantErr: "HTTP_PORT",
		},
		{
			name: "min conns above max",
			env: map[string]string{
				"DATABASE_URL":  "postgres://localhost/db",
				"DB_MIN_CONNS":  "20",
				"DB_MAX_CONNS":  "5",
				"POSTGRES_HOST": "",
			},
			wantErr: "DB_MIN_CONNS",
		},
		{
			name:    "bad log format",
			env:     map[string]string{"DATABASE_URL": "postgres://localhost/db", "LOG_FORMAT": "yaml"},
			wantErr: "LOG_FORMAT",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Clear inherited values so each case starts from a known state.
			for _, k := range []string{"DATABASE_URL", "POSTGRES_HOST", "HTTP_PORT", "LOG_FORMAT", "DB_MIN_CONNS", "DB_MAX_CONNS"} {
				t.Setenv(k, "")
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			_, err := Load()
			if err == nil {
				t.Fatalf("Load() error = nil, want error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestLoadIngestDefaultsOff(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Ingest.Enabled {
		t.Error("ingest must be off unless enabled explicitly")
	}
	if cfg.Ingest.BatchRows != 5000 {
		t.Errorf("BatchRows: got %d, want 5000", cfg.Ingest.BatchRows)
	}
}

func TestLoadIngestRequiresItsSettings(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("ENABLE_INGEST_ENDPOINT", "true")

	if _, err := Load(); err == nil {
		t.Fatal("expected an error when the ingest endpoint is on but unconfigured")
	}
}

func TestLoadIngestMakesDatabaseOptional(t *testing.T) {
	// Clear both ways a DSN can be composed: the direct DATABASE_URL and the
	// POSTGRES_HOST path. This guard prevents inheriting the developer's own
	// database configuration from leaking into the test.
	t.Setenv("DATABASE_URL", "")
	t.Setenv("POSTGRES_HOST", "")

	// Invert the test: set a non-empty DATABASE_URL first, then prove the
	// guard clears it. If t.Setenv("DATABASE_URL", "") below did not work,
	// the DSN would be non-empty and the test would fail, proving the guard is
	// sound rather than assuming it.
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("DATABASE_URL", "")

	t.Setenv("ENABLE_INGEST_ENDPOINT", "true")
	t.Setenv("PUBSUB_AUDIENCE", "https://ingest.example")
	t.Setenv("PUBSUB_PUSH_SERVICE_ACCOUNT", "push@example.iam.gserviceaccount.com")
	t.Setenv("BQ_DATASET", "energy")
	t.Setenv("QUARANTINE_BUCKET", "experiment-go-quarantine")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DB.DSN != "" {
		t.Errorf("DSN: got %q, want empty", cfg.DB.DSN)
	}
	if cfg.Ingest.BQTable != "readings" {
		t.Errorf("BQTable: got %q, want readings", cfg.Ingest.BQTable)
	}
}

func TestLoadDatabaseStillRequiredWithoutIngest(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("POSTGRES_HOST", "")

	if _, err := Load(); err == nil {
		t.Fatal("expected the API service to still fail fast without a database")
	}
}
