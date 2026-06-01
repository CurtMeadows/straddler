package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeTempConfig writes YAML content to a temp file and returns its path.
func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "straddler-*.yaml")
	require.NoError(t, err)
	_, err = f.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	return f.Name()
}

func TestLoad_Defaults(t *testing.T) {
	// A config with only DSN set should produce all expected defaults.
	path := writeTempConfig(t, `
database:
  dsn: "postgres://user:pass@localhost/test"
`)
	cfg, err := Load(path)
	require.NoError(t, err)

	assert.Equal(t, int32(10), cfg.Database.MaxConns)
	assert.Equal(t, int32(2), cfg.Database.MinConns)
	assert.Equal(t, 10*time.Second, cfg.Database.ConnectTimeout)

	assert.Equal(t, 2, cfg.Worker.Concurrency)
	assert.Equal(t, 5*time.Second, cfg.Worker.PollInterval)
	assert.Equal(t, 3, cfg.Worker.MaxAttempts)
	assert.Equal(t, 30*time.Second, cfg.Worker.BaseBackoff)
	assert.Equal(t, 30*time.Minute, cfg.Worker.StaleTimeout)

	assert.Equal(t, "info", cfg.Log.Level)
	assert.Equal(t, "json", cfg.Log.Format)
}

func TestLoad_YAMLOverridesDefaults(t *testing.T) {
	path := writeTempConfig(t, `
database:
  dsn: "postgres://user:pass@localhost/test"
  max_conns: 25

worker:
  concurrency: 8
  poll_interval: 10s
  base_backoff: 1m

log:
  level: debug
  format: text
`)
	cfg, err := Load(path)
	require.NoError(t, err)

	assert.Equal(t, int32(25), cfg.Database.MaxConns)
	assert.Equal(t, 8, cfg.Worker.Concurrency)
	assert.Equal(t, 10*time.Second, cfg.Worker.PollInterval)
	assert.Equal(t, time.Minute, cfg.Worker.BaseBackoff)
	assert.Equal(t, "debug", cfg.Log.Level)
	assert.Equal(t, "text", cfg.Log.Format)
}

func TestLoad_EnvVarOverridesYAML(t *testing.T) {
	path := writeTempConfig(t, `
database:
  dsn: "postgres://file-value@localhost/test"
worker:
  concurrency: 2
`)
	// Env vars should win over the YAML values.
	t.Setenv("STRADDLER_DATABASE_DSN", "postgres://env-value@localhost/test")
	t.Setenv("STRADDLER_WORKER_CONCURRENCY", "16")

	cfg, err := Load(path)
	require.NoError(t, err)

	assert.Equal(t, "postgres://env-value@localhost/test", cfg.Database.DSN,
		"env var should override YAML")
	assert.Equal(t, 16, cfg.Worker.Concurrency,
		"env var should override YAML")
}

func TestLoad_MissingFileIsOK(t *testing.T) {
	// A non-existent config file is not an error — env vars and defaults suffice.
	t.Setenv("STRADDLER_DATABASE_DSN", "postgres://user:pass@localhost/test")

	cfg, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "postgres://user:pass@localhost/test", cfg.Database.DSN)
}

func TestLoad_InvalidYAML(t *testing.T) {
	path := writeTempConfig(t, `this: is: not: valid: yaml: [`)
	_, err := Load(path)
	assert.Error(t, err, "invalid YAML should return an error")
}

func TestValidate_MissingDSN(t *testing.T) {
	path := writeTempConfig(t, `database: {}`)
	_, err := Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DSN")
}

func TestValidate_BadConcurrency(t *testing.T) {
	t.Setenv("STRADDLER_DATABASE_DSN", "postgres://x@localhost/test")
	t.Setenv("STRADDLER_WORKER_CONCURRENCY", "0")
	_, err := Load("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "concurrency")
}

func TestValidate_BadLogLevel(t *testing.T) {
	path := writeTempConfig(t, `
database:
  dsn: "postgres://user:pass@localhost/test"
log:
  level: verbose
`)
	_, err := Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "log.level")
}

func TestValidate_BadLogFormat(t *testing.T) {
	path := writeTempConfig(t, `
database:
  dsn: "postgres://user:pass@localhost/test"
log:
  format: yaml
`)
	_, err := Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "log.format")
}

func TestLoad_RegistryCredentials(t *testing.T) {
	path := writeTempConfig(t, `
database:
  dsn: "postgres://user:pass@localhost/test"
registry:
  source:
    username: srcuser
    password: srcpass
  dest:
    username: dstuser
    password: dstpass
`)
	cfg, err := Load(path)
	require.NoError(t, err)

	assert.Equal(t, "srcuser", cfg.Registry.Source.Username)
	assert.Equal(t, "srcpass", cfg.Registry.Source.Password)
	assert.Equal(t, "dstuser", cfg.Registry.Dest.Username)
	assert.Equal(t, "dstpass", cfg.Registry.Dest.Password)
}

func TestLoad_EnvVarRegistryCredentials(t *testing.T) {
	t.Setenv("STRADDLER_DATABASE_DSN", "postgres://user:pass@localhost/test")
	t.Setenv("STRADDLER_REGISTRY_DEST_USERNAME", "AWS")
	t.Setenv("STRADDLER_REGISTRY_DEST_PASSWORD", "mytoken")

	cfg, err := Load("")
	require.NoError(t, err)

	assert.Equal(t, "AWS", cfg.Registry.Dest.Username)
	assert.Equal(t, "mytoken", cfg.Registry.Dest.Password)
}
