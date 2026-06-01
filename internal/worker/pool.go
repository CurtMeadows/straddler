package worker

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/CurtMeadows/straddler/internal/db"
	"github.com/CurtMeadows/straddler/internal/registry"
	"github.com/CurtMeadows/straddler/internal/telemetry"
	"golang.org/x/sync/errgroup"
)

// Pool manages a fixed number of concurrent workers and a background reaper.
type Pool struct {
	concurrency  int
	cfg          WorkerConfig
	queue        *db.Queue
	registry     registry.Client
	staleTimeout time.Duration
}

// NewPool creates a Pool.
// concurrency is the number of parallel copy workers.
// staleTimeout is how long a job can stay in_progress before the reaper
// resets it back to pending (handles crashed workers).
func NewPool(concurrency int, staleTimeout time.Duration, cfg WorkerConfig, q *db.Queue, r registry.Client) *Pool {
	return &Pool{
		concurrency:  concurrency,
		cfg:          cfg,
		queue:        q,
		registry:     r,
		staleTimeout: staleTimeout,
	}
}

// Run starts all workers and the reaper, blocking until ctx is cancelled or
// (when cfg.ExitWhenDone is true) the queue is fully drained.
// All goroutines complete before Run returns.
func (p *Pool) Run(ctx context.Context) error {
	logger := telemetry.FromContext(ctx)
	logger.Info("starting worker pool",
		slog.Int("concurrency", p.concurrency),
		slog.Duration("poll_interval", p.cfg.PollInterval),
		slog.Duration("stale_timeout", p.staleTimeout),
	)

	// supportCtx is cancelled when all worker goroutines have exited.
	// This ensures the reaper and progress reporter stop cleanly when workers
	// drain the queue (ExitWhenDone=true) rather than blocking forever waiting
	// for the parent ctx to be cancelled.
	supportCtx, supportCancel := context.WithCancel(ctx)
	defer supportCancel()

	eg, egCtx := errgroup.WithContext(supportCtx)

	// WaitGroup tracks only the worker goroutines (not support goroutines) so
	// we can fire supportCancel() the moment all workers have returned.
	var workerWg sync.WaitGroup

	// Spawn one goroutine per worker slot.
	for i := range p.concurrency {
		workerCfg := p.cfg
		workerCfg.WorkerID = fmt.Sprintf("%s-%d", p.cfg.WorkerID, i)
		workerWg.Add(1)

		eg.Go(func() error {
			defer workerWg.Done()
			return newWorker(workerCfg, p.queue, p.registry).run(egCtx)
		})
	}

	// Watcher: cancel support goroutines as soon as all workers have exited.
	// This is what lets the reaper and progress reporter stop cleanly when
	// ExitWhenDone causes workers to return nil.
	eg.Go(func() error {
		workerWg.Wait()
		supportCancel()
		return nil
	})

	// Reaper: resets stuck in_progress jobs back to pending.
	eg.Go(func() error {
		return p.reaper(egCtx, logger)
	})

	// Progress reporter: logs queue counts every 30 seconds.
	eg.Go(func() error {
		return p.progressReporter(egCtx, logger)
	})

	return eg.Wait()
}

// progressReporter logs a queue summary every 30 seconds so the operator can
// see how many jobs have completed out of the total without querying the DB.
func (p *Pool) progressReporter(ctx context.Context, logger *slog.Logger) error {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			s, err := p.queue.StatusSummaryFor(ctx, "")
			if err != nil {
				logger.Error("progress reporter error", "error", err)
				continue
			}
			total := s.Pending + s.InProgress + s.Completed + s.Failed
			logger.Info("queue progress",
				slog.Int64("completed", s.Completed),
				slog.Int64("in_progress", s.InProgress),
				slog.Int64("pending", s.Pending),
				slog.Int64("failed", s.Failed),
				slog.Int64("total", total),
			)
		}
	}
}

// reaper periodically resets jobs that have been stuck in_progress for longer
// than p.staleTimeout. It runs every staleTimeout/2 so stale jobs are caught
// within at most 1.5× the timeout window.
func (p *Pool) reaper(ctx context.Context, logger *slog.Logger) error {
	// Guard against a very small or zero staleTimeout.
	interval := p.staleTimeout / 2
	if interval < time.Second {
		interval = time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			n, err := p.queue.ReapStale(ctx, p.staleTimeout)
			if err != nil {
				logger.Error("reaper error", "error", err)
				continue
			}
			if n > 0 {
				logger.Info("reaped stale jobs", slog.Int64("count", n))
			}
		}
	}
}
