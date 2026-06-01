// Package db provides PostgreSQL access for the straddler job queue.
package db

import "time"

// JobStatus is the lifecycle state of a sync job.
type JobStatus string

const (
	JobStatusPending    JobStatus = "pending"
	JobStatusInProgress JobStatus = "in_progress"
	JobStatusCompleted  JobStatus = "completed"
	JobStatusFailed     JobStatus = "failed"
)

// Job mirrors a row in the sync_jobs table.
type Job struct {
	ID           int64
	SourceRef    string
	DestRef      string
	Status       JobStatus
	AttemptCount int
	MaxAttempts  int
	LastError    *string    // nil when no error has been recorded
	NextRetryAt  time.Time
	ClaimedAt    *time.Time // nil until a worker claims the job
	ClaimedBy    *string    // worker hostname, for observability
	CompletedAt  *time.Time // nil until the job succeeds
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// EnqueueParams is the input shape for BulkEnqueue.
type EnqueueParams struct {
	SourceRef   string
	DestRef     string
	MaxAttempts int
}

// StatusSummary holds aggregate counts per status, used by the status command.
type StatusSummary struct {
	Pending    int64
	InProgress int64
	Completed  int64
	Failed     int64
}
