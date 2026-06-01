// Package config loads straddler configuration from three sources in priority order:
//
//  1. A YAML file (default: ./straddler.yaml, override with --config)
//  2. Environment variables with the STRADDLER_ prefix
//  3. CLI flags (applied by the cobra commands on top of the returned struct)
//
// YAML is loaded first so it forms the base. env.Parse then overwrites any
// field whose corresponding env var is actually set — unset env vars are left
// at their YAML (or default) value.
package config

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/caarlos0/env/v11"
	"gopkg.in/yaml.v3"
)

// Config is the root configuration object.
//
// The envPrefix tags compose with the env tags on nested structs to form the
// full environment variable name. For example:
//
//	Config.envPrefix       "STRADDLER_REGISTRY_"
//	  └─ RegistryConfig.envPrefix  "SOURCE_"
//	       └─ RegistryCredentials env:"USERNAME"
//	            = STRADDLER_REGISTRY_SOURCE_USERNAME
//
// Full variable reference:
//
//	STRADDLER_DATABASE_DSN
//	STRADDLER_DATABASE_MAX_CONNS
//	STRADDLER_REGISTRY_SOURCE_USERNAME / _PASSWORD
//	STRADDLER_REGISTRY_DEST_USERNAME   / _PASSWORD
//	STRADDLER_REGISTRY_INSECURE_SKIP_TLS
//	STRADDLER_WORKER_CONCURRENCY / POLL_INTERVAL / MAX_ATTEMPTS / BASE_BACKOFF / STALE_TIMEOUT
//	STRADDLER_LOG_LEVEL / FORMAT
type Config struct {
	Database DatabaseConfig `yaml:"database" envPrefix:"STRADDLER_DATABASE_"`
	Registry RegistryConfig `yaml:"registry" envPrefix:"STRADDLER_REGISTRY_"`
	Worker   WorkerConfig   `yaml:"worker"   envPrefix:"STRADDLER_WORKER_"`
	Log      LogConfig      `yaml:"log"      envPrefix:"STRADDLER_LOG_"`
}

// DatabaseConfig holds PostgreSQL connection settings.
type DatabaseConfig struct {
	DSN            string        `yaml:"dsn"             env:"DSN"`
	MaxConns       int32         `yaml:"max_conns"       env:"MAX_CONNS"       envDefault:"10"`
	MinConns       int32         `yaml:"min_conns"       env:"MIN_CONNS"       envDefault:"2"`
	ConnectTimeout time.Duration `yaml:"connect_timeout" env:"CONNECT_TIMEOUT" envDefault:"10s"`
}

// RegistryConfig holds credentials for the source and destination registries.
// Both sides can be any OCI-compliant registry.
// Leave Username and Password empty to fall back to ~/.docker/config.json
// and any installed credential helpers (ecr-login, gcr, acr-env, etc.).
type RegistryConfig struct {
	Source          RegistryCredentials `yaml:"source" envPrefix:"SOURCE_"`
	Dest            RegistryCredentials `yaml:"dest"   envPrefix:"DEST_"`
	InsecureSkipTLS bool                `yaml:"insecure_skip_tls" env:"INSECURE_SKIP_TLS" envDefault:"false"`
}

// RegistryCredentials holds the username/password for a single registry endpoint.
//
// Common patterns:
//   - Docker Hub / GHCR / Quay:  Username + Password (or personal access token)
//   - Amazon ECR:                Username = "AWS", Password = `aws ecr get-login-password`
//   - Google GCR / GAR:          Username = "_json_key", Password = service-account JSON
//   - Self-hosted (no auth):     leave both empty
type RegistryCredentials struct {
	Username string `yaml:"username" env:"USERNAME"`
	Password string `yaml:"password" env:"PASSWORD"`
}

// WorkerConfig controls the worker pool behaviour.
type WorkerConfig struct {
	Concurrency  int           `yaml:"concurrency"   env:"CONCURRENCY"   envDefault:"2"`
	PollInterval time.Duration `yaml:"poll_interval" env:"POLL_INTERVAL" envDefault:"5s"`
	MaxAttempts  int           `yaml:"max_attempts"  env:"MAX_ATTEMPTS"  envDefault:"3"`
	BaseBackoff  time.Duration `yaml:"base_backoff"  env:"BASE_BACKOFF"  envDefault:"30s"`
	StaleTimeout time.Duration `yaml:"stale_timeout" env:"STALE_TIMEOUT" envDefault:"30m"`
}

// LogConfig controls log verbosity and output format.
type LogConfig struct {
	Level  string `yaml:"level"  env:"LEVEL"  envDefault:"info"`  // debug|info|warn|error
	Format string `yaml:"format" env:"FORMAT" envDefault:"json"` // json|text
}

// Load reads configuration from the given file path (may be empty) and then
// overlays any STRADDLER_* environment variables that are set.
func Load(cfgFile string) (*Config, error) {
	cfg := defaultConfig()

	// 1. Read YAML file if one was provided or found at the default path.
	path := cfgFile
	if path == "" {
		path = "straddler.yaml"
	}

	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read config file %q: %w", path, err)
	}
	if err == nil {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse config file %q: %w", path, err)
		}
	}

	// 2. Overlay env vars — only fields whose env var is actually set are changed.
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("parse environment variables: %w", err)
	}

	// 3. Validate the merged result.
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// defaultConfig returns a Config pre-filled with built-in defaults.
//
// Why are defaults set in two places?
//   - envDefault struct tags: used by caarlos0/env when an env var IS present but
//     has no value, and also as documentation showing the default alongside the field.
//   - This function: sets the same defaults Go-side so that a run with no config file
//     AND no env vars still produces a sensible struct. Without this, unset env vars
//     would leave zero values (0 concurrency, 0 timeout, etc.) which fail validation.
func defaultConfig() *Config {
	return &Config{
		Database: DatabaseConfig{
			MaxConns:       10,
			MinConns:       2,
			ConnectTimeout: 10 * time.Second,
		},
		Worker: WorkerConfig{
			Concurrency:  2,
			PollInterval: 5 * time.Second,
			MaxAttempts:  3,
			BaseBackoff:  30 * time.Second,
			StaleTimeout: 30 * time.Minute,
		},
		Log: LogConfig{
			Level:  "info",
			Format: "json",
		},
	}
}

func (c *Config) validate() error {
	if c.Database.DSN == "" {
		return fmt.Errorf("database DSN is required — set database.dsn in straddler.yaml or STRADDLER_DATABASE_DSN")
	}
	if c.Worker.Concurrency < 1 {
		return fmt.Errorf("worker.concurrency must be >= 1, got %d", c.Worker.Concurrency)
	}
	if c.Worker.MaxAttempts < 1 {
		return fmt.Errorf("worker.max_attempts must be >= 1, got %d", c.Worker.MaxAttempts)
	}
	if c.Worker.PollInterval <= 0 {
		return fmt.Errorf("worker.poll_interval must be positive, got %s", c.Worker.PollInterval)
	}
	switch c.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("log.level must be debug|info|warn|error, got %q", c.Log.Level)
	}
	switch c.Log.Format {
	case "json", "text":
	default:
		return fmt.Errorf("log.format must be json|text, got %q", c.Log.Format)
	}
	return nil
}
