package cli

import (
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/CurtMeadows/straddler/internal/db"
	"github.com/CurtMeadows/straddler/internal/registry"
	"github.com/CurtMeadows/straddler/internal/telemetry"
	"github.com/CurtMeadows/straddler/internal/worker"
	"github.com/spf13/cobra"
)

func newRunCmd(d *deps) *cobra.Command {
	var (
		source       string
		dest         string
		explicitTags string
		tagFilter    string
		batchSize    int
		concurrency  int
	)

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Fetch tags, copy all images, and exit when done",
		Long: `Combines sync and worker into a single one-shot command.

Fetches all tags from the source repository, enqueues them as copy jobs,
starts workers, streams progress to the terminal, and exits automatically
when every job has completed or permanently failed.

Returns exit code 0 when all jobs succeed, 1 when any permanently fail.
Safe to re-run — already-completed jobs are skipped.

Examples:
  straddler run \
    --source docker.io/solanafoundation/anchor \
    --dest   quay.io/ottersec/anchor

  straddler run \
    --source docker.io/library/nginx \
    --dest   123456.dkr.ecr.us-east-1.amazonaws.com/nginx \
    --tag-filter "^1\\." \
    --concurrency 4`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cfg := d.cfg

			if cmd.Flags().Changed("concurrency") {
				cfg.Worker.Concurrency = concurrency
			}

			// ── Compile optional tag filter ──────────────────────────────────
			var filter *regexp.Regexp
			if tagFilter != "" {
				var err error
				filter, err = regexp.Compile(tagFilter)
				if err != nil {
					return fmt.Errorf("invalid --tag-filter %q: %w", tagFilter, err)
				}
			}

			// ── Graceful shutdown on Ctrl+C / SIGTERM ────────────────────────
			ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
			defer stop()

			ctx = telemetry.WithLogger(ctx, d.logger)

			// ── Fetch tags ───────────────────────────────────────────────────
			runPrintln(ts(), fmt.Sprintf("fetching tags from %s...", source))

			transport := registry.BuildTransport(cfg.Registry.InsecureSkipTLS)
			regClient := registry.NewRemoteClient(
				registry.BuildKeychain(cfg.Registry.Source),
				registry.BuildKeychain(cfg.Registry.Dest),
				transport,
			)

			var tags []string
			if explicitTags != "" {
				for _, t := range strings.Split(explicitTags, ",") {
					if t = strings.TrimSpace(t); t != "" {
						tags = append(tags, t)
					}
				}
			} else {
				var err error
				tags, err = regClient.ListTags(ctx, source)
				if err != nil {
					return fmt.Errorf("list tags for %q: %w", source, err)
				}
			}

			if filter != nil {
				// Reslice to zero length but keep the backing array so we
				// filter in-place without a new allocation.
				filtered := tags[:0]
				for _, t := range tags {
					if filter.MatchString(t) {
						filtered = append(filtered, t)
					}
				}
				tags = filtered
			}

			if len(tags) == 0 {
				runPrintln(ts(), "no tags found")
				return nil
			}

			// ── Connect to DB ────────────────────────────────────────────────
			dbPool, err := db.Open(ctx,
				cfg.Database.DSN,
				cfg.Database.MaxConns,
				cfg.Database.MinConns,
				cfg.Database.ConnectTimeout,
			)
			if err != nil {
				return fmt.Errorf("connect to database: %w", err)
			}
			defer dbPool.Close()

			queue := db.NewQueue(dbPool)

			// ── Enqueue ──────────────────────────────────────────────────────
			var totalInserted int64
			for start := 0; start < len(tags); start += batchSize {
				end := min(start+batchSize, len(tags))
				params := make([]db.EnqueueParams, end-start)
				for i, tag := range tags[start:end] {
					params[i] = db.EnqueueParams{
						SourceRef:   source + ":" + tag,
						DestRef:     dest + ":" + tag,
						MaxAttempts: cfg.Worker.MaxAttempts,
					}
				}
				n, err := queue.BulkEnqueue(ctx, params)
				if err != nil {
					return fmt.Errorf("enqueue batch: %w", err)
				}
				totalInserted += n
			}

			skipped := int64(len(tags)) - totalInserted
			runPrintln(ts(), fmt.Sprintf(
				"%d tags found, %d enqueued (%d already existed)",
				len(tags), totalInserted, skipped,
			))
			runPrintln(ts(), fmt.Sprintf("starting %d workers", cfg.Worker.Concurrency))

			// ── Wire up event callbacks ──────────────────────────────────────
			var completed, failed atomic.Int64
			runStart := time.Now()

			hostname, err := os.Hostname()
			if err != nil {
				d.logger.Warn("could not determine hostname, using 'unknown'", "error", err)
				hostname = "unknown"
			}

			workerCfg := worker.WorkerConfig{
				WorkerID:     hostname,
				PollInterval: cfg.Worker.PollInterval,
				MaxAttempts:  cfg.Worker.MaxAttempts,
				BaseBackoff:  cfg.Worker.BaseBackoff,
				ExitWhenDone: true,

				OnComplete: func(sourceRef, _ string, duration time.Duration) {
					completed.Add(1)
					label := tagFrom(sourceRef)
					if duration == 0 {
						runPrintln(ts(), fmt.Sprintf("✓  %-45s already up-to-date", label))
					} else {
						runPrintln(ts(), fmt.Sprintf("✓  %-45s %s", label, duration.Round(time.Second)))
					}
				},

				OnFailed: func(sourceRef, _ string, errMsg string) {
					failed.Add(1)
					runPrintln(ts(), fmt.Sprintf(
						"✗  %-45s failed after %d attempts: %s",
						tagFrom(sourceRef), cfg.Worker.MaxAttempts, errMsg,
					))
				},

				OnHeartbeat: func(sourceRef string, elapsed time.Duration) {
					runPrintln(ts(), fmt.Sprintf(
						"   still copying %s (%s)…",
						tagFrom(sourceRef), elapsed,
					))
				},
			}

			// ── Run until done ───────────────────────────────────────────────
			p := worker.NewPool(
				cfg.Worker.Concurrency,
				cfg.Worker.StaleTimeout,
				workerCfg,
				queue,
				regClient,
			)

			if err := p.Run(ctx); err != nil {
				return fmt.Errorf("worker pool: %w", err)
			}

			// ── Summary and exit code ────────────────────────────────────────
			total := completed.Load() + failed.Load()
			elapsed := time.Since(runStart).Round(time.Second)

			if failed.Load() > 0 {
				runPrintln(ts(), fmt.Sprintf(
					"%d completed, %d permanently failed (check 'straddler status' for details)",
					completed.Load(), failed.Load(),
				))
				os.Exit(1)
			}

			runPrintln(ts(), fmt.Sprintf("all %d jobs complete (%s total)", total, elapsed))
			return nil
		},
	}

	cmd.Flags().StringVar(&source, "source", "", "source repository (required), e.g. docker.io/library/nginx")
	cmd.Flags().StringVar(&dest, "dest", "", "destination repository (required), e.g. quay.io/myorg/nginx")
	cmd.Flags().StringVar(&explicitTags, "tags", "", "comma-separated tags; skips registry enumeration")
	cmd.Flags().StringVar(&tagFilter, "tag-filter", "", `regex to filter enumerated tags, e.g. "^1\\."`)
	cmd.Flags().IntVar(&batchSize, "batch-size", 100, "jobs per INSERT transaction")
	cmd.Flags().IntVar(&concurrency, "concurrency", 0, "parallel workers (default: from config)")

	_ = cmd.MarkFlagRequired("source")
	_ = cmd.MarkFlagRequired("dest")

	return cmd
}

// ts returns the current time formatted as HH:MM:SS for run output lines.
func ts() string {
	return time.Now().Format("15:04:05")
}

// runPrintln writes a single timestamped line to stdout.
func runPrintln(timestamp, msg string) {
	fmt.Printf("[%s] %s\n", timestamp, msg)
}

// tagFrom extracts the tag portion from a "repo:tag" reference.
// Falls back to the full string if no colon is present.
func tagFrom(ref string) string {
	if i := strings.LastIndex(ref, ":"); i >= 0 {
		return ref[i+1:]
	}
	return ref
}

// workerLogger is the minimal interface needed by newRunCmd for hostname warnings.
// Satisfied by *slog.Logger.
type workerLogger interface {
	Warn(msg string, args ...any)
}

// compile-time check that *slog.Logger satisfies workerLogger.
var _ workerLogger = (*slog.Logger)(nil)
