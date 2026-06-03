package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/CurtMeadows/straddler/internal/db"
	"github.com/CurtMeadows/straddler/internal/registry"
	"github.com/CurtMeadows/straddler/internal/telemetry"
)

const (
	statusCheckTimeout = 5 * time.Second  // how long to wait for an idle queue check
	dbOpTimeout        = 10 * time.Second // budget for MarkComplete / MarkFailed DB calls
	heartbeatInterval  = 15 * time.Second // log cadence during a long image copy
	defaultMaxBackoff  = time.Hour        // cap on exponential retry delay
)

// WorkerCallbacks holds optional event hooks called during job processing.
// All functions must be safe for concurrent use from multiple worker goroutines.
// Nil functions are silently skipped.
type WorkerCallbacks struct {
	// OnComplete is called after a job is successfully marked complete in the DB.
	OnComplete func(sourceRef, destRef string, duration time.Duration)

	// OnFailed is called after a job permanently exhausts all retry attempts.
	// Not called for transient failures that will be retried.
	OnFailed func(sourceRef, destRef string, errMsg string)

	// OnHeartbeat is called every heartbeatInterval while a copy is in flight.
	OnHeartbeat func(sourceRef string, elapsed time.Duration)
}

// WorkerConfig holds parameters for a worker. All workers in a pool share one WorkerConfig.
// Named WorkerConfig (not Config) to avoid ambiguity with internal/config.Config at call sites.
type WorkerConfig struct {
	// WorkerID identifies this worker in the claimed_by column and log fields.
	// The pool appends a numeric suffix to the base ID for each goroutine.
	WorkerID     string
	PollInterval time.Duration
	MaxAttempts  int
	BaseBackoff  time.Duration
	MaxBackoff   time.Duration

	// ExitWhenDone makes workers return nil (instead of polling forever) when the
	// queue has no claimable jobs AND no jobs are in_progress. Used by the `run`
	// command to exit automatically without a Ctrl+C.
	ExitWhenDone bool

	// Callbacks are optional event hooks. Zero value (all nil) is valid.
	Callbacks WorkerCallbacks
}

// worker processes jobs from the queue one at a time.
type worker struct {
	cfg      WorkerConfig
	queue    *db.Queue
	registry registry.Client
}

// newWorker creates a worker.
func newWorker(cfg WorkerConfig, q *db.Queue, r registry.Client) *worker {
	if cfg.MaxBackoff == 0 {
		cfg.MaxBackoff = defaultMaxBackoff
	}
	return &worker{cfg: cfg, queue: q, registry: r}
}

// run polls for and processes jobs until ctx is cancelled (or queue is drained
// when ExitWhenDone is true).
//
// Shutdown behaviour:
//   - If ctx is cancelled between jobs (idle), the worker exits immediately.
//   - If ctx is cancelled while registry.Copy is running, the copy is allowed
//     to finish — Copy respects ctx internally and will abort the network call.
//     The job is then marked complete or failed before the loop exits.
//   - If the process is killed hard (SIGKILL) mid-copy, the job stays in_progress.
//     The reaper in pool.go will reset it to pending within staleTimeout.
func (w *worker) run(ctx context.Context) error {
	logger := telemetry.FromContext(ctx).With("worker_id", w.cfg.WorkerID)

	for {
		// Check for shutdown before each poll so we exit promptly after
		// the current job finishes, without waiting for the next poll tick.
		if ctx.Err() != nil {
			return nil
		}

		job, err := w.queue.ClaimNextJob(ctx, w.cfg.WorkerID)
		if err != nil {
			logger.Error("failed to claim job", "error", err)
			w.sleep(ctx)
			continue
		}

		if job == nil {
			if w.cfg.ExitWhenDone {
				// Check whether the queue is truly exhausted or whether jobs
				// are just waiting for their retry delay (next_retry_at > NOW()).
				checkCtx, cancel := context.WithTimeout(context.Background(), statusCheckTimeout)
				s, err := w.queue.StatusSummaryFor(checkCtx, "")
				cancel()
				if err == nil && s.Pending == 0 && s.InProgress == 0 {
					// Nothing left for any worker to do — exit cleanly.
					// The pool's watcher goroutine will cancel the reaper and
					// progress reporter once all workers return nil.
					logger.Info("queue drained, exiting")
					return nil
				}
				// Pending > 0 but all are waiting for retry delays — keep polling.
			}
			w.sleep(ctx)
			continue
		}

		w.process(ctx, logger, job)
	}
}

// process executes one copy job and updates the queue with the outcome.
// It does not return an error — failures are recorded in the DB and the
// worker loop continues regardless.
func (w *worker) process(ctx context.Context, logger *slog.Logger, job *db.Job) {
	log := logger.With(
		slog.Int64("job_id", job.ID),
		slog.String("source", job.SourceRef),
		slog.String("dest", job.DestRef),
		slog.Int("attempt", job.AttemptCount),
	)

	// Check whether the destination already has the correct content before
	// attempting a copy. If digests match, mark complete and return early —
	// no network transfer needed, no risk of a push permission failure.
	exists, err := w.registry.AlreadyExists(ctx, job.SourceRef, job.DestRef)
	if err != nil {
		log.Warn("could not check destination, proceeding with copy", "error", err)
	} else if exists {
		log.Info("destination already up-to-date, skipping copy")
		dbCtx, cancel := context.WithTimeout(context.Background(), dbOpTimeout)
		defer cancel()
		if dbErr := w.queue.MarkComplete(dbCtx, job.ID); dbErr != nil {
			log.Error("failed to mark skipped job as complete", "error", dbErr)
		}
		if w.cfg.Callbacks.OnComplete != nil {
			w.cfg.Callbacks.OnComplete(job.SourceRef, job.DestRef, 0)
		}
		return
	}

	log.Info("copying image")
	start := time.Now()

	// Heartbeat: log every 15 seconds while the copy is in flight so the
	// operator can see the worker is alive during large image transfers.
	heartbeatDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatDone:
				return
			case <-ticker.C:
				elapsed := time.Since(start).Round(time.Second)
				log.Info("still copying…", slog.Duration("elapsed", elapsed))
				if w.cfg.Callbacks.OnHeartbeat != nil {
					w.cfg.Callbacks.OnHeartbeat(job.SourceRef, elapsed)
				}
			}
		}
	}()

	err = w.registry.Copy(ctx, job.SourceRef, job.DestRef)
	close(heartbeatDone)

	// Use a fresh context for DB updates so they always complete even if the
	// parent ctx was cancelled (e.g. by SIGTERM mid-copy). Without this, both
	// MarkFailed and MarkComplete would fail with "context canceled" and the
	// job would be stranded in_progress until the reaper resets it.
	dbCtx, cancel := context.WithTimeout(context.Background(), dbOpTimeout)
	defer cancel()

	duration := time.Since(start)

	if err != nil {
		delay := Backoff(job.AttemptCount, w.cfg.BaseBackoff, w.cfg.MaxBackoff)
		log.Warn("copy failed",
			slog.String("error", err.Error()),
			slog.Duration("retry_after", delay),
		)
		if dbErr := w.queue.MarkFailed(dbCtx, job.ID, err.Error(), delay); dbErr != nil {
			log.Error("failed to mark job as failed", "error", dbErr)
		}
		// Fire OnFailed only on permanent exhaustion, not transient retries.
		if w.cfg.Callbacks.OnFailed != nil && job.AttemptCount >= job.MaxAttempts {
			w.cfg.Callbacks.OnFailed(job.SourceRef, job.DestRef, err.Error())
		}
		return
	}

	log.Info("copy complete", slog.Duration("duration", duration))

	if dbErr := w.queue.MarkComplete(dbCtx, job.ID); dbErr != nil {
		log.Error("failed to mark job as complete", "error", dbErr)
	}
	if w.cfg.Callbacks.OnComplete != nil {
		w.cfg.Callbacks.OnComplete(job.SourceRef, job.DestRef, duration)
	}
}

// sleep blocks for the poll interval or until ctx is cancelled.
func (w *worker) sleep(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-time.After(w.cfg.PollInterval):
	}
}
