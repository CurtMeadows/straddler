package db

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Queue wraps a pgxpool.Pool and exposes typed job queue operations.
// All methods are safe for concurrent use.
type Queue struct {
	pool *pgxpool.Pool
}

// NewQueue creates a Queue backed by the given pool.
func NewQueue(pool *pgxpool.Pool) *Queue {
	return &Queue{pool: pool}
}

// BulkEnqueue inserts a batch of jobs in a single transaction.
//
// Idempotency: the table has a partial unique index on (source_ref, dest_ref)
// where status NOT IN ('completed', 'failed'), so inserting a job that is
// already active (pending or in_progress) silently does nothing for that row.
// A previously completed or failed pair CAN be re-enqueued.
//
// Returns the number of rows actually inserted (duplicates not counted).
func (q *Queue) BulkEnqueue(ctx context.Context, jobs []EnqueueParams) (int64, error) {
	if len(jobs) == 0 {
		return 0, nil
	}

	tx, err := q.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback on any non-commit path

	var inserted int64
	for _, j := range jobs {
		tag, err := tx.Exec(ctx, `
			INSERT INTO sync_jobs (source_ref, dest_ref, max_attempts)
			VALUES ($1, $2, $3)
			ON CONFLICT (source_ref, dest_ref)
			    WHERE status NOT IN ('completed', 'failed')
			DO NOTHING`,
			j.SourceRef, j.DestRef, j.MaxAttempts,
		)
		if err != nil {
			return 0, fmt.Errorf("insert job (%s → %s): %w", j.SourceRef, j.DestRef, err)
		}
		inserted += tag.RowsAffected()
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit transaction: %w", err)
	}

	return inserted, nil
}

// BulkEnqueueTags builds copy jobs from a flat tag list and enqueues them in
// batches. It is the single canonical implementation of the batch-enqueue loop;
// callers (CLI and TUI) should use this rather than reimplementing chunking.
//
// Returns the number of newly inserted rows and the total number of tags.
func (q *Queue) BulkEnqueueTags(ctx context.Context, source, dest string, tags []string, maxAttempts, batchSize int) (inserted int64, err error) {
	if batchSize <= 0 {
		batchSize = 100
	}
	for start := 0; start < len(tags); start += batchSize {
		end := min(start+batchSize, len(tags))
		params := make([]EnqueueParams, end-start)
		for i, tag := range tags[start:end] {
			params[i] = EnqueueParams{
				SourceRef:   source + ":" + tag,
				DestRef:     dest + ":" + tag,
				MaxAttempts: maxAttempts,
			}
		}
		n, err := q.BulkEnqueue(ctx, params)
		if err != nil {
			return inserted, err
		}
		inserted += n
	}
	return inserted, nil
}

// ClaimNextJob atomically picks the next available job and marks it in_progress.
//
// The FOR UPDATE SKIP LOCKED subquery means:
//   - Multiple workers can call this concurrently without blocking each other.
//   - Each worker gets a different row; a row being processed by another worker
//     is invisible to this query.
//
// Returns (nil, nil) when the queue has no claimable jobs — not an error.
func (q *Queue) ClaimNextJob(ctx context.Context, workerID string) (*Job, error) {
	row := q.pool.QueryRow(ctx, `
		UPDATE sync_jobs
		SET
		    status        = 'in_progress',
		    claimed_at    = NOW(),
		    claimed_by    = $1,
		    attempt_count = attempt_count + 1,
		    updated_at    = NOW()
		WHERE id = (
		    SELECT id
		    FROM   sync_jobs
		    WHERE  status IN ('pending', 'failed')
		      AND  next_retry_at <= NOW()
		      AND  attempt_count < max_attempts
		    ORDER  BY next_retry_at ASC
		    FOR UPDATE SKIP LOCKED
		    LIMIT  1
		)
		RETURNING
		    id, source_ref, dest_ref, status,
		    attempt_count, max_attempts, last_error,
		    next_retry_at, claimed_at, claimed_by,
		    completed_at, created_at, updated_at`,
		workerID,
	)

	job, err := scanJob(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil // queue empty — not an error
		}
		return nil, fmt.Errorf("claim job: %w", err)
	}

	return job, nil
}

// MarkComplete marks a job as successfully completed.
func (q *Queue) MarkComplete(ctx context.Context, id int64) error {
	_, err := q.pool.Exec(ctx, `
		UPDATE sync_jobs
		SET
		    status       = 'completed',
		    completed_at = NOW(),
		    updated_at   = NOW()
		WHERE id = $1`,
		id,
	)
	if err != nil {
		return fmt.Errorf("mark job %d complete: %w", id, err)
	}
	return nil
}

// MarkFailed records the error and schedules a retry after retryAfter.
//
// If the job has exhausted max_attempts the status becomes 'failed' permanently.
// Otherwise it goes back to 'pending' with next_retry_at set in the future —
// ClaimNextJob will ignore it until that time passes.
func (q *Queue) MarkFailed(ctx context.Context, id int64, errMsg string, retryAfter time.Duration) error {
	_, err := q.pool.Exec(ctx, `
		UPDATE sync_jobs
		SET
		    status        = CASE
		                      WHEN attempt_count >= max_attempts THEN 'failed'::job_status
		                      ELSE 'pending'::job_status
		                    END,
		    last_error    = $2,
		    next_retry_at = NOW() + ($3 * INTERVAL '1 second'),
		    updated_at    = NOW()
		WHERE id = $1`,
		id, errMsg, int(retryAfter.Seconds()),
	)
	if err != nil {
		return fmt.Errorf("mark job %d failed: %w", id, err)
	}
	return nil
}

// ReapStale resets jobs that have been in_progress for longer than olderThan
// back to pending so they can be retried by another worker.
//
// This handles the case where a worker process is killed mid-copy and never
// calls MarkComplete or MarkFailed. Jobs that have already hit max_attempts
// are left as-is — they'll be picked up by a human or an admin command.
//
// Returns the number of rows reset.
func (q *Queue) ReapStale(ctx context.Context, olderThan time.Duration) (int64, error) {
	tag, err := q.pool.Exec(ctx, `
		UPDATE sync_jobs
		SET
		    status     = 'pending',
		    claimed_at = NULL,
		    claimed_by = NULL,
		    updated_at = NOW()
		WHERE status     = 'in_progress'
		  AND claimed_at < NOW() - ($1 * INTERVAL '1 second')
		  AND attempt_count < max_attempts`,
		int(olderThan.Seconds()),
	)
	if err != nil {
		return 0, fmt.Errorf("reap stale jobs: %w", err)
	}
	return tag.RowsAffected(), nil
}

// StatusSummaryFor returns aggregate job counts grouped by status.
// Pass sourcePrefix = "" to count all jobs; otherwise only jobs whose
// source_ref starts with the given prefix are counted.
func (q *Queue) StatusSummaryFor(ctx context.Context, sourcePrefix string) (*StatusSummary, error) {
	// Conditional aggregation — one table scan, four counters.
	const baseQuery = `
		SELECT
		    COALESCE(SUM(CASE WHEN status = 'pending'     THEN 1 END), 0),
		    COALESCE(SUM(CASE WHEN status = 'in_progress' THEN 1 END), 0),
		    COALESCE(SUM(CASE WHEN status = 'completed'   THEN 1 END), 0),
		    COALESCE(SUM(CASE WHEN status = 'failed'      THEN 1 END), 0)
		FROM sync_jobs`

	var (
		row pgx.Row
		s   StatusSummary
	)

	if sourcePrefix != "" {
		row = q.pool.QueryRow(ctx, baseQuery+" WHERE source_ref LIKE $1", sourcePrefix+"%")
	} else {
		row = q.pool.QueryRow(ctx, baseQuery)
	}

	if err := row.Scan(&s.Pending, &s.InProgress, &s.Completed, &s.Failed); err != nil {
		return nil, fmt.Errorf("query status summary: %w", err)
	}

	return &s, nil
}

// ListJobsParams controls filtering and pagination for ListJobs.
type ListJobsParams struct {
	Status   string // empty means all statuses
	Search   string // ILIKE filter on source_ref OR dest_ref
	Page     int    // 0-indexed
	PageSize int    // rows per page
}

// JobPage is the result of a ListJobs call.
type JobPage struct {
	Jobs  []Job
	Total int64
}

// ListJobs returns a paginated, optionally-filtered page of jobs.
func (q *Queue) ListJobs(ctx context.Context, p ListJobsParams) (*JobPage, error) {
	if p.PageSize <= 0 {
		p.PageSize = 20
	}

	var (
		whereClauses []string
		args         []any
		argIdx       = 1
	)

	if p.Status != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("status = $%d::job_status", argIdx))
		args = append(args, p.Status)
		argIdx++
	}
	if p.Search != "" {
		whereClauses = append(whereClauses, fmt.Sprintf(
			"(source_ref ILIKE $%d OR dest_ref ILIKE $%d)",
			argIdx, argIdx,
		))
		args = append(args, "%"+p.Search+"%")
		argIdx++
	}

	where := ""
	if len(whereClauses) > 0 {
		where = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	// Count query.
	countRow := q.pool.QueryRow(ctx, "SELECT COUNT(*) FROM sync_jobs "+where, args...)
	var total int64
	if err := countRow.Scan(&total); err != nil {
		return nil, fmt.Errorf("count jobs: %w", err)
	}

	// Data query.
	offset := p.Page * p.PageSize
	dataArgs := append(args, p.PageSize, offset)
	rows, err := q.pool.Query(ctx, `
		SELECT
		    id, source_ref, dest_ref, status,
		    attempt_count, max_attempts, last_error,
		    next_retry_at, claimed_at, claimed_by,
		    completed_at, created_at, updated_at
		FROM sync_jobs
		`+where+`
		ORDER BY updated_at DESC
		LIMIT $`+fmt.Sprintf("%d", argIdx)+` OFFSET $`+fmt.Sprintf("%d", argIdx+1),
		dataArgs...,
	)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scan job: %w", err)
		}
		jobs = append(jobs, *j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate jobs: %w", err)
	}

	return &JobPage{Jobs: jobs, Total: total}, nil
}

// ListInProgress returns all currently claimed jobs ordered by claimed_at.
func (q *Queue) ListInProgress(ctx context.Context) ([]Job, error) {
	rows, err := q.pool.Query(ctx, `
		SELECT
		    id, source_ref, dest_ref, status,
		    attempt_count, max_attempts, last_error,
		    next_retry_at, claimed_at, claimed_by,
		    completed_at, created_at, updated_at
		FROM sync_jobs
		WHERE status = 'in_progress'
		ORDER BY claimed_at ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list in-progress jobs: %w", err)
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scan job: %w", err)
		}
		jobs = append(jobs, *j)
	}
	return jobs, rows.Err()
}

// GetJob returns a single job by ID.
func (q *Queue) GetJob(ctx context.Context, id int64) (*Job, error) {
	row := q.pool.QueryRow(ctx, `
		SELECT
		    id, source_ref, dest_ref, status,
		    attempt_count, max_attempts, last_error,
		    next_retry_at, claimed_at, claimed_by,
		    completed_at, created_at, updated_at
		FROM sync_jobs
		WHERE id = $1`,
		id,
	)
	j, err := scanJob(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get job %d: %w", id, err)
	}
	return j, nil
}


// scanJob scans a Job from any pgx row type (pgx.Row or pgx.Rows).
//
// ⚠️  Column order must match the SELECT list in every query that feeds this
// function. pgx maps by position, not name — a mismatch causes silent data
// corruption. Expected order:
//
//	id, source_ref, dest_ref, status,
//	attempt_count, max_attempts, last_error,
//	next_retry_at, claimed_at, claimed_by,
//	completed_at, created_at, updated_at
func scanJob(row interface{ Scan(dest ...any) error }) (*Job, error) {
	var j Job
	err := row.Scan(
		&j.ID, &j.SourceRef, &j.DestRef, &j.Status,
		&j.AttemptCount, &j.MaxAttempts, &j.LastError,
		&j.NextRetryAt, &j.ClaimedAt, &j.ClaimedBy,
		&j.CompletedAt, &j.CreatedAt, &j.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &j, nil
}
