CREATE TYPE job_status AS ENUM ('pending', 'in_progress', 'completed', 'failed');

CREATE TABLE sync_jobs (
    id             BIGSERIAL PRIMARY KEY,
    source_ref     TEXT NOT NULL,
    dest_ref       TEXT NOT NULL,
    status         job_status    NOT NULL DEFAULT 'pending',
    attempt_count  INT           NOT NULL DEFAULT 0,
    max_attempts   INT           NOT NULL DEFAULT 3,
    last_error     TEXT,
    next_retry_at  TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    claimed_at     TIMESTAMPTZ,
    claimed_by     TEXT,
    completed_at   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

-- Partial index over claimable rows only — keeps SKIP LOCKED scans fast.
CREATE INDEX idx_sync_jobs_claimable
    ON sync_jobs (next_retry_at)
    WHERE status IN ('pending', 'failed');

-- Prevent duplicate active jobs for the same (source, dest) pair.
-- Completed / permanently-failed jobs are excluded so the same pair can be re-synced later.
CREATE UNIQUE INDEX idx_sync_jobs_dedup
    ON sync_jobs (source_ref, dest_ref)
    WHERE status NOT IN ('completed', 'failed');
