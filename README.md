# straddler

Copy Docker/OCI images between **any two container registries** without a Docker daemon.

[![CI](https://github.com/CurtMeadows/straddler/actions/workflows/ci.yml/badge.svg)](https://github.com/CurtMeadows/straddler/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/CurtMeadows/straddler)](https://goreportcard.com/report/github.com/CurtMeadows/straddler)

Straddler syncs images between Docker Hub, ECR, GCR/GAR, GHCR, Quay, Harbor, or any
OCI-compliant registry. It uses a PostgreSQL job queue so large migrations survive worker
crashes and can run across multiple machines in parallel.

---

## Quick start

```bash
# 1. Start a Postgres instance
docker run -d --name straddler-pg \
  -e POSTGRES_USER=straddler \
  -e POSTGRES_PASSWORD=straddler \
  -e POSTGRES_DB=straddler \
  -p 5432:5432 \
  postgres:17-alpine

# 2. Apply the schema
export STRADDLER_DATABASE_DSN="postgres://straddler:straddler@localhost:5432/straddler?sslmode=disable"
straddler migrate up

# 3. Run the migration — syncs tags and copies images in one command
straddler run \
  --source docker.io/library/nginx \
  --dest   myreg.example.com/nginx
```

That's it. `run` enumerates tags, enqueues them, starts workers, streams images directly
between registries, and exits when every job completes or permanently fails.

---

## Installation

### Option 1 — Install script (recommended, no Go or sudo required)

Detects your OS and architecture, installs to `~/.local/bin`, and adds it to your PATH automatically:

```bash
curl -sSL https://raw.githubusercontent.com/CurtMeadows/straddler/main/install.sh | sh
```

Restart your shell (or run `export PATH="$PATH:$HOME/.local/bin"`), then:

```bash
straddler version
```

To install to a different directory:
```bash
INSTALL_DIR=~/bin curl -sSL https://raw.githubusercontent.com/CurtMeadows/straddler/main/install.sh | sh
```

### Option 2 — `go install` (requires Go 1.23+)

```bash
go install github.com/CurtMeadows/straddler/cmd/straddler@latest
```

The binary is placed in `$GOPATH/bin` (usually `~/go/bin`). Make sure that directory is on
your PATH — add this to your `~/.zshrc` or `~/.bashrc` if it isn't already:

```bash
export PATH="$PATH:$(go env GOPATH)/bin"
```

Then reload your shell (`source ~/.zshrc`) and run `straddler version` to confirm.

### Option 3 — Build from source

```bash
git clone https://github.com/CurtMeadows/straddler.git
cd straddler
make install          # builds and copies to /usr/local/bin (may need sudo)

# Or install to a custom directory:
make install INSTALL_DIR=~/.local/bin
```

### Option 4 — Docker (no install needed)

```bash
docker run --rm ghcr.io/CurtMeadows/straddler:latest --help
```

---

## How it works

```
straddler run (or sync + worker separately)
        │
        ├─ ListTags(source repo) ────────────── Source Registry
        │
        ├─ BulkEnqueue(tags) ────────────────── PostgreSQL (job queue)
        │
        └─ Worker pool (N goroutines)
               │
               ├─ ClaimNextJob (SKIP LOCKED) ── PostgreSQL
               ├─ remote.Get(src) ───────────── Source Registry
               └─ remote.Write(dst) ─────────── Dest Registry
                     (streaming — no local disk)
```

Images are **never written to disk**. Blobs are streamed on demand from source to
destination using the OCI Distribution Spec HTTP API.

Multi-architecture manifest lists (`ImageIndex`) are handled transparently — all platform
variants are copied as a single atomic operation.

---

## Commands

### `straddler run` — the one-shot command

Fetches tags, enqueues jobs, runs workers, and exits when done. Use this for most migrations.

```bash
straddler run \
  --source docker.io/library/nginx \
  --dest   ghcr.io/myorg/nginx

# Filter to only 1.x tags
straddler run \
  --source docker.io/library/nginx \
  --dest   ghcr.io/myorg/nginx \
  --tag-filter "^1\."

# Explicit tag list (skips registry enumeration)
straddler run \
  --source docker.io/library/nginx \
  --dest   ghcr.io/myorg/nginx \
  --tags   1.25,1.26,latest

# Preview without copying
straddler run \
  --source docker.io/library/nginx \
  --dest   ghcr.io/myorg/nginx \
  --dry-run
```

| Flag | Default | Description |
|------|---------|-------------|
| `--source` | required | Source repository |
| `--dest` | required | Destination repository |
| `--tags` | | Comma-separated explicit tags; skips enumeration |
| `--tag-filter` | | Regex to filter enumerated tags |
| `--dry-run` | false | Print what would be copied without doing it |
| `--concurrency` | 2 | Parallel copy workers |
| `--batch-size` | 100 | Jobs per INSERT transaction |

### `straddler sync` + `straddler worker` — separate steps

Use these when you want to:
- Run workers on **multiple machines** in parallel
- Keep workers **running permanently** and periodically re-sync
- Enqueue jobs on one host and process them on another

```bash
# On the control node: enumerate and enqueue
straddler sync \
  --source docker.io/library/nginx \
  --dest   myreg.example.com/nginx

# On each worker node (or the same machine, multiple times):
straddler worker --concurrency 8
```

`sync` flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--source` | required | Source repository |
| `--dest` | required | Destination repository |
| `--tags` | | Explicit tag list |
| `--tag-filter` | | Regex filter |
| `--dry-run` | false | Preview without writing to DB |
| `--batch-size` | 100 | Jobs per INSERT transaction |

`worker` flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--concurrency` | 2 | Parallel workers |

### `straddler tui` — interactive dashboard

A full-screen terminal UI for monitoring and managing jobs.

```bash
straddler tui
```

Navigate with **Tab** or number keys:
- `1` Dashboard — live queue stats, recent activity
- `2` Jobs — filterable, searchable job table with detail view
- `3` Sync — wizard to enqueue a migration without leaving the TUI
- `4` Workers — start/stop the worker pool, watch in-progress jobs
- `5` Migrate — apply or roll back schema migrations

### `straddler status` — queue statistics

```bash
straddler status
# STATUS        COUNT
# pending       42
# in_progress   3
# completed     1500
# failed        5

straddler status --format json
straddler status --source docker.io/library/nginx  # filter by source prefix
```

### `straddler migrate` — schema management

```bash
straddler migrate up         # apply all pending migrations
straddler migrate down       # roll back the last migration
straddler migrate down -n 2  # roll back 2 migrations
```

### `straddler version`

```bash
straddler version
# straddler v1.2.0 (commit abc1234, built 2025-06-01)
```

---

## Configuration

Config is resolved in priority order: **CLI flags → `STRADDLER_*` env vars → `straddler.yaml` → built-in defaults**.

Copy `straddler.yaml.example` to `straddler.yaml` and edit, or set env vars directly.

```yaml
database:
  dsn: "postgres://user:pass@host:5432/straddler?sslmode=disable"
  max_conns: 10
  min_conns: 2
  connect_timeout: 10s

registry:
  # Credentials for source registry.
  # Omit to use ~/.docker/config.json and installed credential helpers.
  source:
    username: ""
    password: ""
  # Credentials for destination registry.
  dest:
    username: ""
    password: ""
  # Set true for self-hosted registries with self-signed certificates.
  insecure_skip_tls: false

worker:
  concurrency: 2       # parallel copy workers
  poll_interval: 5s    # sleep between polls when queue is empty
  max_attempts: 3      # mark permanently failed after this many tries
  base_backoff: 30s    # first retry delay; doubles each attempt, capped at 1h
  stale_timeout: 30m   # reclaim in_progress jobs stuck longer than this

log:
  level: info    # debug|info|warn|error
  format: json   # json|text
```

### Registry authentication

Straddler inherits credentials from `~/.docker/config.json` and any installed credential
helpers automatically. Explicit config is only needed if you want to override or supply
credentials without running `docker login`.

**Amazon ECR:**
```bash
export STRADDLER_REGISTRY_DEST_USERNAME=AWS
export STRADDLER_REGISTRY_DEST_PASSWORD=$(aws ecr get-login-password --region us-east-1)
# Or install amazon-ecr-credential-helper — no config needed.
```

**Google Artifact Registry / GCR:**
```bash
export STRADDLER_REGISTRY_SOURCE_USERNAME=_json_key
export STRADDLER_REGISTRY_SOURCE_PASSWORD="$(cat sa-key.json)"
# Or run `gcloud auth configure-docker` — no config needed.
```

**GitHub Container Registry:**
```bash
export STRADDLER_REGISTRY_DEST_USERNAME=myuser
export STRADDLER_REGISTRY_DEST_PASSWORD=ghp_myPersonalAccessToken
```

**Self-hosted / insecure:**
```yaml
registry:
  insecure_skip_tls: true
```

---

## Running workers across multiple machines

For very large migrations, spread the copy work across multiple hosts:

```bash
# Control node: enqueue all jobs
straddler sync --source docker.io/myorg/app --dest myreg.example.com/app

# Worker nodes A, B, C — run concurrently against the same Postgres queue
straddler worker --concurrency 8
```

Each worker uses `SELECT FOR UPDATE SKIP LOCKED` so machines never claim the same job.
The stale-job reaper resets any job that was claimed by a crashed worker back to `pending`
so it gets retried automatically.

---

## Development

```bash
git clone https://github.com/CurtMeadows/straddler.git
cd straddler

# Build the binary (output: bin/straddler)
make build

# Install it so `straddler` works from anywhere
make install          # copies to /usr/local/bin, may need sudo
# or without sudo:
make install INSTALL_DIR=~/.local/bin

# Start local Postgres
make db-up

# Apply schema
make migrate-up

# Run tests (requires Docker for testcontainers)
make test
```

If using `go install` during development, ensure `$(go env GOPATH)/bin` is in your PATH:
```bash
# Add to ~/.zshrc or ~/.bashrc
export PATH="$PATH:$(go env GOPATH)/bin"
```

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the full design reference.

---

## License

[MIT](LICENSE)
