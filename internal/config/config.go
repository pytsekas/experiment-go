// Package config loads application configuration from environment variables.
//
// Configuration follows the 12-factor app style: everything comes from the
// environment, with sane defaults for local development.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the fully resolved application configuration.
type Config struct {
	Env      string
	HTTP     HTTP
	DB       DB
	Logger   Logger
	Migrate  Migrate
	Features Features
}

// Features toggles optional endpoints.
type Features struct {
	// BurnEndpoint exposes GET /api/v1/burn?ms=N, a deliberate CPU sink for
	// autoscaling and load-test experiments. Never enable it in production.
	BurnEndpoint bool
}

// Migrate holds the schema migration settings.
type Migrate struct {
	// OnStart applies pending migrations during startup. Handy in development
	// and for single-instance deploys; in production prefer running
	// `api migrate up` as a separate job and leaving this off.
	OnStart bool
}

// HTTP holds the HTTP server settings.
type HTTP struct {
	Host            string
	Port            int
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
}

// Addr returns the listen address for the HTTP server.
func (h HTTP) Addr() string {
	return fmt.Sprintf("%s:%d", h.Host, h.Port)
}

// DB holds the Postgres connection settings.
type DB struct {
	DSN             string
	MaxConns        int
	MinConns        int
	MaxConnLifetime time.Duration
	ConnectTimeout  time.Duration
}

// Logger holds the structured logging settings.
type Logger struct {
	Level  string // debug | info | warn | error
	Format string // json | text
}

// IsProduction reports whether the app runs with production semantics.
func (c Config) IsProduction() bool {
	return strings.EqualFold(c.Env, "production")
}

// Load reads the configuration from the environment and validates it.
func Load() (Config, error) {
	cfg := Config{
		Env: env("APP_ENV", "development"),
		HTTP: HTTP{
			Host:            env("HTTP_HOST", "0.0.0.0"),
			Port:            envInt("HTTP_PORT", 8080),
			ReadTimeout:     envDuration("HTTP_READ_TIMEOUT", 10*time.Second),
			WriteTimeout:    envDuration("HTTP_WRITE_TIMEOUT", 15*time.Second),
			IdleTimeout:     envDuration("HTTP_IDLE_TIMEOUT", 60*time.Second),
			ShutdownTimeout: envDuration("HTTP_SHUTDOWN_TIMEOUT", 15*time.Second),
		},
		DB: DB{
			DSN:             dsn(),
			MaxConns:        envInt("DB_MAX_CONNS", 10),
			MinConns:        envInt("DB_MIN_CONNS", 1),
			MaxConnLifetime: envDuration("DB_MAX_CONN_LIFETIME", time.Hour),
			ConnectTimeout:  envDuration("DB_CONNECT_TIMEOUT", 10*time.Second),
		},
		Logger: Logger{
			Level:  env("LOG_LEVEL", "info"),
			Format: env("LOG_FORMAT", "json"),
		},
		Migrate: Migrate{
			OnStart: envBool("MIGRATE_ON_START", false),
		},
		Features: Features{
			BurnEndpoint: envBool("ENABLE_BURN_ENDPOINT", false),
		},
	}

	return cfg, cfg.validate()
}

func (c Config) validate() error {
	if c.HTTP.Port < 1 || c.HTTP.Port > 65535 {
		return fmt.Errorf("config: HTTP_PORT %d out of range", c.HTTP.Port)
	}
	if c.DB.DSN == "" {
		return fmt.Errorf("config: DATABASE_URL (or POSTGRES_*) must be set")
	}
	if c.DB.MinConns > c.DB.MaxConns {
		return fmt.Errorf("config: DB_MIN_CONNS (%d) must not exceed DB_MAX_CONNS (%d)", c.DB.MinConns, c.DB.MaxConns)
	}
	switch strings.ToLower(c.Logger.Format) {
	case "json", "text":
	default:
		return fmt.Errorf("config: LOG_FORMAT %q must be json or text", c.Logger.Format)
	}

	return nil
}

// dsn prefers an explicit DATABASE_URL and otherwise composes one from the
// individual POSTGRES_* variables used by the docker-compose stack.
func dsn() string {
	if v := os.Getenv("DATABASE_URL"); v != "" {
		return v
	}

	host := env("POSTGRES_HOST", "")
	if host == "" {
		return ""
	}

	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(env("POSTGRES_USER", "postgres"), env("POSTGRES_PASSWORD", "postgres")),
		Host:   fmt.Sprintf("%s:%d", host, envInt("POSTGRES_PORT", 5432)),
		Path:   "/" + env("POSTGRES_DB", "postgres"),
	}
	u.RawQuery = url.Values{"sslmode": {env("POSTGRES_SSLMODE", "disable")}}.Encode()

	return u.String()
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}

	return fallback
}

func envInt(key string, fallback int) int {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}

	return n
}

func envBool(key string, fallback bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}

	return b
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}

	return d
}
