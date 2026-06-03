package db

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// startTestDB spins up a fresh Postgres container, applies migrations, and
// returns a connection pool. The container is automatically stopped when the
// test (or subtest) completes.
func startTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx,
		"postgres:17-alpine",
		tcpostgres.WithDatabase("straddler_test"),
		tcpostgres.WithUsername("straddler"),
		tcpostgres.WithPassword("straddler"),
		tcpostgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	// Apply migrations so the schema is ready.
	require.NoError(t, MigrateUp(dsn))

	pool, err := Open(ctx, dsn, 10, 2, 10*time.Second)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	return pool
}

// ── BulkEnqueue ───────────────────────────────────────────────────────────────

func TestBulkEnqueue_InsertsJobs(t *testing.T) {
	q := NewQueue(startTestDB(t))
	ctx := context.Background()

	n, err := q.BulkEnqueue(ctx, []EnqueueParams{
		{SourceRef: "docker.io/nginx:1.25", DestRef: "quay.io/org/nginx:1.25", MaxAttempts: 3},
		{SourceRef: "docker.io/nginx:1.26", DestRef: "quay.io/org/nginx:1.26", MaxAttempts: 3},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)
}

func TestBulkEnqueue_IdempotentForActivePairs(t *testing.T) {
	// Enqueueing the same (source, dest) pair twice while the first is still
	// active (pending) should silently skip the duplicate.
	q := NewQueue(startTestDB(t))
	ctx := context.Background()

	params := []EnqueueParams{
		{SourceRef: "docker.io/nginx:1.25", DestRef: "quay.io/org/nginx:1.25", MaxAttempts: 3},
	}

	n1, err := q.BulkEnqueue(ctx, params)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n1)

	n2, err := q.BulkEnqueue(ctx, params)
	require.NoError(t, err)
	assert.Equal(t, int64(0), n2, "duplicate active job should be silently skipped")
}

func TestBulkEnqueue_AllowsReEnqueueAfterComplete(t *testing.T) {
	// Completed jobs should be re-enqueueable (e.g. to re-sync after a source update).
	q := NewQueue(startTestDB(t))
	ctx := context.Background()

	params := []EnqueueParams{
		{SourceRef: "docker.io/nginx:1.25", DestRef: "quay.io/org/nginx:1.25", MaxAttempts: 3},
	}

	_, err := q.BulkEnqueue(ctx, params)
	require.NoError(t, err)

	job, err := q.ClaimNextJob(ctx, "test-worker")
	require.NoError(t, err)
	require.NotNil(t, job)
	require.NoError(t, q.MarkComplete(ctx, job.ID))

	// Should be allowed to re-enqueue now that the previous entry is completed.
	n, err := q.BulkEnqueue(ctx, params)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n, "should be able to re-enqueue a completed job")
}

func TestBulkEnqueue_Empty(t *testing.T) {
	q := NewQueue(startTestDB(t))
	n, err := q.BulkEnqueue(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)
}

// ── ClaimNextJob ──────────────────────────────────────────────────────────────

func TestClaimNextJob_ReturnsNilWhenEmpty(t *testing.T) {
	q := NewQueue(startTestDB(t))
	job, err := q.ClaimNextJob(context.Background(), "worker-0")
	require.NoError(t, err)
	assert.Nil(t, job, "empty queue should return nil job without error")
}

func TestClaimNextJob_ClaimsAndMarksInProgress(t *testing.T) {
	q := NewQueue(startTestDB(t))
	ctx := context.Background()

	_, err := q.BulkEnqueue(ctx, []EnqueueParams{
		{SourceRef: "docker.io/nginx:1.25", DestRef: "quay.io/org/nginx:1.25", MaxAttempts: 3},
	})
	require.NoError(t, err)

	job, err := q.ClaimNextJob(ctx, "worker-0")
	require.NoError(t, err)
	require.NotNil(t, job)

	assert.Equal(t, JobStatusInProgress, job.Status)
	assert.Equal(t, 1, job.AttemptCount, "attempt_count should be incremented on claim")
	assert.Equal(t, "worker-0", *job.ClaimedBy)
	assert.NotNil(t, job.ClaimedAt)
}

func TestClaimNextJob_RespectsNextRetryAt(t *testing.T) {
	// A job with next_retry_at in the future should not be claimable.
	q := NewQueue(startTestDB(t))
	ctx := context.Background()

	_, err := q.BulkEnqueue(ctx, []EnqueueParams{
		{SourceRef: "docker.io/nginx:1.25", DestRef: "quay.io/org/nginx:1.25", MaxAttempts: 3},
	})
	require.NoError(t, err)

	// Claim and immediately fail with a long retry delay.
	job, err := q.ClaimNextJob(ctx, "worker-0")
	require.NoError(t, err)
	require.NotNil(t, job)
	require.NoError(t, q.MarkFailed(ctx, job.ID, "timeout", 24*time.Hour))

	// Queue should appear empty until next_retry_at passes.
	next, err := q.ClaimNextJob(ctx, "worker-0")
	require.NoError(t, err)
	assert.Nil(t, next, "job with future next_retry_at should not be claimable")
}

func TestClaimNextJob_SkipLockedConcurrency(t *testing.T) {
	// Two concurrent workers should claim two different jobs, never the same one.
	q := NewQueue(startTestDB(t))
	ctx := context.Background()

	_, err := q.BulkEnqueue(ctx, []EnqueueParams{
		{SourceRef: "docker.io/nginx:1.25", DestRef: "quay.io/org/nginx:1.25", MaxAttempts: 3},
		{SourceRef: "docker.io/nginx:1.26", DestRef: "quay.io/org/nginx:1.26", MaxAttempts: 3},
	})
	require.NoError(t, err)

	var (
		mu   sync.Mutex
		seen = map[int64]string{}
		wg   sync.WaitGroup
	)

	for i := range 2 {
		wg.Add(1)
		workerID := fmt.Sprintf("worker-%d", i)
		go func() {
			defer wg.Done()
			job, err := q.ClaimNextJob(ctx, workerID)
			require.NoError(t, err)
			require.NotNil(t, job, "each worker should get a job")
			mu.Lock()
			seen[job.ID] = workerID
			mu.Unlock()
		}()
	}
	wg.Wait()

	assert.Len(t, seen, 2, "two workers should claim two distinct jobs")
}

// ── MarkComplete ──────────────────────────────────────────────────────────────

func TestMarkComplete(t *testing.T) {
	q := NewQueue(startTestDB(t))
	ctx := context.Background()

	_, err := q.BulkEnqueue(ctx, []EnqueueParams{
		{SourceRef: "docker.io/nginx:1.25", DestRef: "quay.io/org/nginx:1.25", MaxAttempts: 3},
	})
	require.NoError(t, err)

	job, err := q.ClaimNextJob(ctx, "worker-0")
	require.NoError(t, err)
	require.NotNil(t, job)

	require.NoError(t, q.MarkComplete(ctx, job.ID))

	s, err := q.StatusSummaryFor(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, int64(1), s.Completed)
	assert.Equal(t, int64(0), s.Pending)
	assert.Equal(t, int64(0), s.InProgress)
}

// ── MarkFailed ────────────────────────────────────────────────────────────────

func TestMarkFailed_SchedulesRetry(t *testing.T) {
	// A job that fails with remaining attempts should go back to pending.
	q := NewQueue(startTestDB(t))
	ctx := context.Background()

	_, err := q.BulkEnqueue(ctx, []EnqueueParams{
		{SourceRef: "docker.io/nginx:1.25", DestRef: "quay.io/org/nginx:1.25", MaxAttempts: 3},
	})
	require.NoError(t, err)

	job, err := q.ClaimNextJob(ctx, "worker-0")
	require.NoError(t, err)
	require.NotNil(t, job)

	// Fail with 1 attempt used — 2 remain.
	require.NoError(t, q.MarkFailed(ctx, job.ID, "connection refused", 30*time.Second))

	s, err := q.StatusSummaryFor(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, int64(1), s.Pending, "job should return to pending with retries remaining")
	assert.Equal(t, int64(0), s.Failed)
}

func TestMarkFailed_PermanentAfterMaxAttempts(t *testing.T) {
	// A job that exhausts all attempts should be permanently failed.
	q := NewQueue(startTestDB(t))
	ctx := context.Background()

	_, err := q.BulkEnqueue(ctx, []EnqueueParams{
		{SourceRef: "docker.io/nginx:1.25", DestRef: "quay.io/org/nginx:1.25", MaxAttempts: 1},
	})
	require.NoError(t, err)

	job, err := q.ClaimNextJob(ctx, "worker-0")
	require.NoError(t, err)
	require.NotNil(t, job)

	// MaxAttempts=1, AttemptCount is now 1 after claiming — should permanently fail.
	require.NoError(t, q.MarkFailed(ctx, job.ID, "auth error", 30*time.Second))

	s, err := q.StatusSummaryFor(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, int64(1), s.Failed, "job should be permanently failed after max attempts")
	assert.Equal(t, int64(0), s.Pending)
}

// ── ReapStale ─────────────────────────────────────────────────────────────────

func TestReapStale_ResetsStuckJobs(t *testing.T) {
	pool := startTestDB(t)
	q := NewQueue(pool)
	ctx := context.Background()

	_, err := q.BulkEnqueue(ctx, []EnqueueParams{
		{SourceRef: "docker.io/nginx:1.25", DestRef: "quay.io/org/nginx:1.25", MaxAttempts: 3},
	})
	require.NoError(t, err)

	job, err := q.ClaimNextJob(ctx, "worker-0")
	require.NoError(t, err)
	require.NotNil(t, job)

	// Artificially backdate claimed_at so the job looks stale.
	_, err = pool.Exec(ctx,
		"UPDATE sync_jobs SET claimed_at = NOW() - INTERVAL '2 hours' WHERE id = $1",
		job.ID,
	)
	require.NoError(t, err)

	n, err := q.ReapStale(ctx, 30*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n, "stale job should be reaped")

	s, err := q.StatusSummaryFor(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, int64(1), s.Pending, "reaped job should be back to pending")
	assert.Equal(t, int64(0), s.InProgress)
}

func TestReapStale_IgnoresRecentJobs(t *testing.T) {
	q := NewQueue(startTestDB(t))
	ctx := context.Background()

	_, err := q.BulkEnqueue(ctx, []EnqueueParams{
		{SourceRef: "docker.io/nginx:1.25", DestRef: "quay.io/org/nginx:1.25", MaxAttempts: 3},
	})
	require.NoError(t, err)

	_, err = q.ClaimNextJob(ctx, "worker-0")
	require.NoError(t, err)

	// The job was just claimed — should not be reaped with a 30m timeout.
	n, err := q.ReapStale(ctx, 30*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, int64(0), n, "recently claimed job should not be reaped")
}

// ── StatusSummaryFor ──────────────────────────────────────────────────────────

func TestStatusSummaryFor_CountsAllStatuses(t *testing.T) {
	q := NewQueue(startTestDB(t))
	ctx := context.Background()

	// Enqueue 3 jobs; claim 2; complete 1; leave 1 in_progress; 1 stays pending.
	for i := range 3 {
		_, err := q.BulkEnqueue(ctx, []EnqueueParams{
			{
				SourceRef:   fmt.Sprintf("docker.io/nginx:1.%d", i),
				DestRef:     fmt.Sprintf("quay.io/org/nginx:1.%d", i),
				MaxAttempts: 3,
			},
		})
		require.NoError(t, err)
	}

	job1, err := q.ClaimNextJob(ctx, "worker-0")
	require.NoError(t, err)
	require.NoError(t, q.MarkComplete(ctx, job1.ID))

	_, err = q.ClaimNextJob(ctx, "worker-0") // leaves in_progress
	require.NoError(t, err)

	s, err := q.StatusSummaryFor(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, int64(1), s.Completed)
	assert.Equal(t, int64(1), s.InProgress)
	assert.Equal(t, int64(1), s.Pending)
	assert.Equal(t, int64(0), s.Failed)
}

func TestStatusSummaryFor_FiltersBySourcePrefix(t *testing.T) {
	q := NewQueue(startTestDB(t))
	ctx := context.Background()

	_, err := q.BulkEnqueue(ctx, []EnqueueParams{
		{SourceRef: "docker.io/nginx:1.25", DestRef: "quay.io/org/nginx:1.25", MaxAttempts: 3},
		{SourceRef: "docker.io/alpine:3.18", DestRef: "quay.io/org/alpine:3.18", MaxAttempts: 3},
	})
	require.NoError(t, err)

	s, err := q.StatusSummaryFor(ctx, "docker.io/nginx")
	require.NoError(t, err)
	assert.Equal(t, int64(1), s.Pending, "filter should only count nginx job")
}
