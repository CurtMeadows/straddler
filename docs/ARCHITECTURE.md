# Straddler — Architecture & Design Reference

## Overview

Straddler is a Go CLI tool that syncs Docker/OCI images between any two container registries.
It uses PostgreSQL as a durable job queue; workers pick up jobs with `SELECT FOR UPDATE SKIP LOCKED`
for safe concurrent processing without a separate broker.

No Docker daemon is required — the `go-containerregistry` library streams image layers directly
between registries using the OCI Distribution Spec HTTP API.

## Supported Registries

Straddler works with **any OCI-compliant container registry**. Examples:

| Registry | Reference Format | Auth Method |
|---|---|---|
| Docker Hub | `docker.io/library/nginx` | username + password / token |
| GitHub Container Registry | `ghcr.io/org/image` | username + PAT |
| Google Artifact Registry | `us-docker.pkg.dev/project/repo/image` | `_json_key` + service account JSON |
| Amazon ECR | `123456.dkr.ecr.us-east-1.amazonaws.com/image` | `AWS` + `aws ecr get-login-password` token |
| Quay.io | `quay.io/org/image` | username + password |
| Harbor | `harbor.example.com/project/image` | username + password |
| Self-hosted (HTTP) | `localhost:5000/image` | optional basic auth |

Auth is resolved in order:
1. Explicit `username` + `password` in `straddler.yaml` (or `STRADDLER_REGISTRY_SOURCE_*` env vars)
2. `~/.docker/config.json` credential helpers (picks up `docker login`, `ecr-credential-helper`, `gcr`, etc.)

This means if you've already run `docker login` or configured a credential helper, no extra config is needed.

## Design Goals

- **No daemon dependency** — pure Go, no `docker` binary on PATH required
- **Any OCI registry** — source and destination can be any pair of registries
- **Durable queue** — jobs survive worker crashes; retried with exponential backoff
- **Safe concurrency** — `SKIP LOCKED` prevents multiple workers claiming the same job
- **Multi-arch aware** — transparently handles `ImageIndex` (manifest lists) and single-platform images
- **Idempotent enqueue** — re-running `sync` for the same source/dest is always safe (`ON CONFLICT DO NOTHING`)
- **Graceful shutdown** — SIGTERM drains in-flight copies; no partial uploads

## Module & Dependency Map

```
github.com/CurtMeadows/straddler

├── github.com/spf13/cobra v1.10.2           CLI framework
├── github.com/caarlos0/env/v11              Config from environment variables
├── gopkg.in/yaml.v3                         Config from YAML file
├── github.com/jackc/pgx/v5 v5.9.2          PostgreSQL driver + pool
├── github.com/golang-migrate/migrate/v4     DB schema migrations (embed.FS)
├── github.com/google/go-containerregistry   OCI registry client (any registry, no daemon)
├── golang.org/x/sync                        errgroup for worker pool
├── github.com/charmbracelet/bubbletea       TUI framework
├── github.com/charmbracelet/lipgloss        TUI styling
└── github.com/charmbracelet/bubbles         TUI components (table, textinput, spinner)
```

Config resolution: CLI flags → `STRADDLER_*` env vars → `straddler.yaml` → built-in defaults.
No Viper — config uses `caarlos0/env` for environment variables and `gopkg.in/yaml.v3` for the
YAML file, keeping the dependency tree minimal.

## Directory Structure

```
straddler/
├── cmd/straddler/main.go       # Entry point — wires build-time version vars
├── docs/
│   └── ARCHITECTURE.md         # This file
├── migrations/
│   ├── 000001_create_jobs.up.sql
│   └── 000001_create_jobs.down.sql
└── internal/
    ├── config/config.go         # Config struct; Load() merges YAML + env + defaults
    ├── telemetry/logger.go      # slog setup (JSON or text)
    ├── db/
    │   ├── db.go                # pgxpool construction
    │   ├── migrate.go           # golang-migrate runner (embedded migrations)
    │   ├── models.go            # Job struct, JobStatus enum
    │   └── queue.go             # BulkEnqueue, ClaimNextJob, MarkComplete, MarkFailed, ReapStale,
    │                            # ListJobs, ListInProgress, GetJob
    ├── registry/
    │   ├── registry.go          # Client interface
    │   ├── auth.go              # BuildKeychain(creds) → authn.Keychain
    │   ├── client.go            # go-containerregistry implementation (any OCI registry)
    │   └── transport.go         # BuildTransport(insecure) → http.RoundTripper
    ├── worker/
    │   ├── backoff.go           # Exponential backoff with jitter
    │   ├── worker.go            # Single worker loop
    │   └── pool.go              # errgroup pool + stale-job reaper
    ├── tui/
    │   ├── app.go               # Root Bubbletea model, tab bar, view routing
    │   ├── run.go               # tui.Run(cfg) entry point
    │   ├── msgs/msgs.go         # All tea.Msg types for async I/O
    │   ├── styles/styles.go     # Shared Lipgloss styles and status icons
    │   ├── components/          # Banner, Confirm dialog, StatusBar
    │   └── views/               # Dashboard, Jobs, Sync wizard, Workers, Migrate
    └── cli/
        ├── root.go              # Cobra root, global flags, PersistentPreRunE
        ├── run.go               # `straddler run` (sync + worker combined)
        ├── sync.go              # `straddler sync`
        ├── worker.go            # `straddler worker`
        ├── migrate.go           # `straddler migrate [up|down]`
        ├── status.go            # `straddler status`
        ├── tui.go               # `straddler tui`
        └── version.go           # `straddler version`
```

## Database Schema

```sql
CREATE TYPE job_status AS ENUM ('pending', 'in_progress', 'completed', 'failed');

CREATE TABLE sync_jobs (
    id             BIGSERIAL PRIMARY KEY,
    source_ref     TEXT NOT NULL,       -- fully-qualified OCI ref: docker.io/library/nginx:1.25
    dest_ref       TEXT NOT NULL,       -- fully-qualified OCI ref: ghcr.io/myorg/nginx:1.25
    status         job_status NOT NULL DEFAULT 'pending',
    attempt_count  INT NOT NULL DEFAULT 0,
    max_attempts   INT NOT NULL DEFAULT 3,
    last_error     TEXT,
    next_retry_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    claimed_at     TIMESTAMPTZ,
    claimed_by     TEXT,               -- worker hostname for observability
    completed_at   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Efficient SKIP LOCKED claim index (only claimable rows)
CREATE INDEX idx_sync_jobs_claimable
    ON sync_jobs (next_retry_at)
    WHERE status IN ('pending', 'failed');

-- Deduplication guard: one active job per (source, dest) pair
CREATE UNIQUE INDEX idx_sync_jobs_dedup
    ON sync_jobs (source_ref, dest_ref)
    WHERE status NOT IN ('completed', 'failed');
```

## Queue Mechanics

### Claiming a Job (`SKIP LOCKED`)

```sql
UPDATE sync_jobs
SET
    status        = 'in_progress',
    claimed_at    = NOW(),
    claimed_by    = $1,
    attempt_count = attempt_count + 1,
    updated_at    = NOW()
WHERE id = (
    SELECT id FROM sync_jobs
    WHERE status IN ('pending', 'failed')
      AND next_retry_at <= NOW()
      AND attempt_count < max_attempts
    ORDER BY next_retry_at ASC
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
RETURNING *;
```

`FOR UPDATE SKIP LOCKED` means concurrent workers each grab a *different* row atomically
with no blocking — the canonical PostgreSQL job queue pattern.

### Marking Failed (with Backoff)

```sql
UPDATE sync_jobs
SET
    status        = CASE
                      WHEN attempt_count >= max_attempts THEN 'failed'
                      ELSE 'pending'
                    END,
    last_error    = $2,
    next_retry_at = NOW() + ($3 * INTERVAL '1 second'),
    updated_at    = NOW()
WHERE id = $1;
```

The backoff duration (`$3`) is computed in Go: `base * 2^(attempt-1)` capped at 1h with ±10% jitter.

### Stale Job Reaper

A background goroutine in the worker pool runs every `stale_timeout / 2` and resets
jobs stuck `in_progress` beyond `stale_timeout` back to `pending`:

```sql
UPDATE sync_jobs
SET status = 'pending', claimed_at = NULL, claimed_by = NULL, updated_at = NOW()
WHERE status = 'in_progress'
  AND claimed_at < NOW() - ($1 * INTERVAL '1 second')
  AND attempt_count < max_attempts;
```

## Registry Client

### Interface

```go
type RegistryClient interface {
    ListTags(ctx context.Context, repo string) ([]string, error)
    Copy(ctx context.Context, src, dst string) error
}
```

### Multi-Arch Copy Logic

`Copy()` uses `remote.Get()` to inspect the media type before copying:

```
remote.Get(srcRef) → descriptor
  if descriptor.MediaType is ImageIndex (manifest list):
    idx = remote.Index(srcRef)
    remote.WriteIndex(dstRef, idx)   // copies all platform variants
  else:
    img = remote.Image(srcRef)
    remote.Write(dstRef, img)
```

Both paths are **lazy/streaming** — blobs are only downloaded as `remote.Write` /
`remote.WriteIndex` requests them. No full image materialization occurs locally.

### Authentication

`BuildKeychain(creds RegistryCredentials) authn.Keychain` constructs an auth chain:

1. If `username` + `password` are set in config → `authn.FromConfig(authn.AuthConfig{...})`
   wrapped in a `fixedKeychain` that matches any registry (for the configured side).
2. Otherwise → `authn.DefaultKeychain` which reads `~/.docker/config.json` and any
   registered credential helpers (`docker-credential-ecr-login`, `docker-credential-gcr`, etc.)

Source and destination use **separate** keychain instances, allowing different credentials
on each side (e.g. pull from Docker Hub with a read token, push to ECR with AWS credentials).

## CLI Commands

```
straddler run    --source <repo>   --dest <repo>
                 [--tags tag1,tag2]          # explicit tag list; skips registry enumeration
                 [--tag-filter "^1\\."]      # regex to filter enumerated tags
                 [--dry-run]                 # print tags without writing to DB
                 [--batch-size 100]          # rows per INSERT transaction
                 [--concurrency N]           # parallel workers (default: from config)

straddler sync   --source <repo>   --dest <repo>
                 [--tags tag1,tag2]
                 [--tag-filter "^1\\."]
                 [--dry-run]
                 [--batch-size 100]

straddler worker [--concurrency 4]

straddler migrate [up|down]  [--steps N]

straddler status [--source <prefix>]  [--format table|json]

straddler tui    # full-screen interactive dashboard

straddler version
```

Global flags (inherited by all subcommands):
- `--config FILE` — path to straddler.yaml
- `--log-level debug|info|warn|error`
- `--log-format json|text`

Config resolution order: CLI flags → `STRADDLER_*` env vars → `straddler.yaml` → built-in defaults

## Configuration File (`straddler.yaml`)

```yaml
database:
  dsn: "postgres://user:pass@localhost:5432/straddler?sslmode=disable"
  max_conns: 10
  min_conns: 2

registry:
  # Source registry credentials (any OCI registry)
  # Omit to use ~/.docker/config.json / credential helpers
  source:
    username: ""
    password: ""
  # Destination registry credentials (any OCI registry)
  dest:
    username: ""
    password: ""
  insecure_skip_tls: false   # set true for self-hosted HTTP registries

worker:
  concurrency: 4
  poll_interval: 5s
  max_attempts: 3
  base_backoff: 30s
  stale_timeout: 30m

log:
  level: info
  format: json
```

### Common Auth Recipes

**Amazon ECR:**
```bash
# Generate a token (valid 12h), then pass as STRADDLER_REGISTRY_DEST_PASSWORD
export STRADDLER_REGISTRY_DEST_USERNAME=AWS
export STRADDLER_REGISTRY_DEST_PASSWORD=$(aws ecr get-login-password --region us-east-1)
```
Or install `amazon-ecr-credential-helper` and add to `~/.docker/config.json` — no config needed.

**Google Artifact Registry / GCR:**
```bash
# With a service account key file:
export STRADDLER_REGISTRY_SOURCE_USERNAME=_json_key
export STRADDLER_REGISTRY_SOURCE_PASSWORD="$(cat sa-key.json)"
```
Or run `gcloud auth configure-docker` once — no config needed.

**GitHub Container Registry:**
```bash
export STRADDLER_REGISTRY_DEST_USERNAME=myuser
export STRADDLER_REGISTRY_DEST_PASSWORD=ghp_myPersonalAccessToken
```

## Correctness Properties

| Property | Mechanism |
|---|---|
| No duplicate job claims | `SELECT FOR UPDATE SKIP LOCKED` |
| Idempotent enqueue | `ON CONFLICT DO NOTHING` on dedup partial unique index |
| Crash recovery | Stale reaper resets `in_progress` → `pending` after configurable timeout |
| At-least-once delivery | Job retried on crash; registry blob push is idempotent (already-present digest is a no-op) |
| Graceful shutdown | `signal.NotifyContext` + `errgroup` drain; SIGTERM waits for current copy to finish |
| Context cancellation | All `remote.*` and `pgx` calls accept `context.Context` |

## Sequence Diagram

```
straddler sync --source docker.io/library/nginx --dest ghcr.io/myorg/nginx:

  User → CLI: straddler sync --source docker.io/library/nginx --dest ghcr.io/myorg/nginx
  CLI → Registry(src): ListTags("docker.io/library/nginx")
  Registry(src) → CLI: ["1.25", "1.26", "latest", ...]
  CLI → DB: BulkEnqueue(N jobs)  [ON CONFLICT DO NOTHING]
  CLI → User: "Enqueued 42 new jobs (0 already existed)"

straddler worker --concurrency 4:

  Pool spawns 4 worker goroutines + 1 reaper goroutine
  Worker (×4):
    DB: ClaimNextJob(workerID)           [SKIP LOCKED — each worker gets a different row]
    Registry(src): remote.Get(srcRef)    → lazy Image or ImageIndex descriptor
    Registry(dst): remote.Write(dstRef)  [streaming; blobs never materialized locally]
    DB: MarkComplete(job.ID)
    log: {level:info, msg:"copied", source:"docker.io/library/nginx:1.25", dest:"ghcr.io/myorg/nginx:1.25"}
  Reaper (every stale_timeout/2):
    DB: ReapStale(stale_timeout)         → resets hung in_progress jobs back to pending
```

> **Note:** Keep this document updated when making architectural changes.
> It is the authoritative reference for contributors and operators.
