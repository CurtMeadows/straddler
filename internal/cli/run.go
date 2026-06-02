package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/CurtMeadows/straddler/internal/config"
	"github.com/CurtMeadows/straddler/internal/db"
	"github.com/CurtMeadows/straddler/internal/registry"
	"github.com/CurtMeadows/straddler/internal/telemetry"
	"github.com/CurtMeadows/straddler/internal/worker"
	"github.com/spf13/cobra"
)

func newRunCmd(env *cmdEnv) *cobra.Command {
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
			cfg := env.cfg

			if cmd.Flags().Changed("concurrency") {
				cfg.Worker.Concurrency = concurrency
			}

			// Parse filter early to fail fast on an invalid regex before hitting the registry.
			filter, err := compileTagFilter(tagFilter)
			if err != nil {
				return err
			}

			// Cancel on SIGTERM/SIGINT; workers finish their current job before exiting.
			ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
			defer stop()

			ctx = telemetry.WithLogger(ctx, env.logger)

			runPrintln(ts(), fmt.Sprintf("fetching tags from %s...", source))

			regClient := buildRegistryClient(cfg)
			tags, err := collectTags(ctx, regClient, source, explicitTags, filter)
			if err != nil {
				return err
			}
			if len(tags) == 0 {
				runPrintln(ts(), "no tags found")
				return nil
			}

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
			inserted, err := queue.BulkEnqueueTags(ctx, source, dest, tags, cfg.Worker.MaxAttempts, batchSize)
			if err != nil {
				return fmt.Errorf("enqueue tags: %w", err)
			}

			skipped := int64(len(tags)) - inserted
			runPrintln(ts(), fmt.Sprintf(
				"%d tags found, %d enqueued (%d already existed)",
				len(tags), inserted, skipped,
			))
			runPrintln(ts(), fmt.Sprintf("starting %d workers", cfg.Worker.Concurrency))

			var completed, failed atomic.Int64
			runStart := time.Now()

			hostname, err := os.Hostname()
			if err != nil {
				env.logger.Warn("could not determine hostname, using 'unknown'", "error", err)
				hostname = "unknown"
			}

			p := worker.NewPool(
				cfg.Worker.Concurrency,
				cfg.Worker.StaleTimeout,
				buildWorkerConfig(hostname, cfg, &completed, &failed),
				queue,
				regClient,
			)
			if err := p.Run(ctx); err != nil {
				return fmt.Errorf("worker pool: %w", err)
			}

			return reportResults(completed.Load(), failed.Load(), time.Since(runStart).Round(time.Second))
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

// compileTagFilter parses an optional regex filter string.
// Returns nil when no filter is provided.
func compileTagFilter(pattern string) (*regexp.Regexp, error) {
	if pattern == "" {
		return nil, nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid --tag-filter %q: %w", pattern, err)
	}
	return re, nil
}

// buildRegistryClient constructs a registry client from the current config.
func buildRegistryClient(cfg *config.Config) registry.Client {
	return registry.NewRemoteClient(
		registry.BuildKeychain(cfg.Registry.Source),
		registry.BuildKeychain(cfg.Registry.Dest),
		registry.BuildTransport(cfg.Registry.InsecureSkipTLS),
	)
}

// collectTags fetches or parses the tag list, then applies the optional filter.
func collectTags(ctx context.Context, reg registry.Client, source, explicit string, filter *regexp.Regexp) ([]string, error) {
	var tags []string
	if explicit != "" {
		for _, t := range strings.Split(explicit, ",") {
			if t = strings.TrimSpace(t); t != "" {
				tags = append(tags, t)
			}
		}
	} else {
		var err error
		tags, err = reg.ListTags(ctx, source)
		if err != nil {
			return nil, fmt.Errorf("list tags for %q: %w", source, err)
		}
	}

	if filter == nil {
		return tags, nil
	}
	// Reslice to zero length but keep the backing array to filter in-place.
	filtered := tags[:0]
	for _, t := range tags {
		if filter.MatchString(t) {
			filtered = append(filtered, t)
		}
	}
	return filtered, nil
}

// buildWorkerConfig wires up terminal progress callbacks for the run command.
func buildWorkerConfig(workerID string, cfg *config.Config, completed, failed *atomic.Int64) worker.WorkerConfig {
	return worker.WorkerConfig{
		WorkerID:     workerID,
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
			runPrintln(ts(), fmt.Sprintf("   still copying %s (%s)…", tagFrom(sourceRef), elapsed))
		},
	}
}

// reportResults prints the final summary and returns a non-nil error when any
// jobs permanently failed, causing the CLI to exit with a non-zero status code.
func reportResults(completed, failed int64, elapsed time.Duration) error {
	if failed > 0 {
		runPrintln(ts(), fmt.Sprintf(
			"%d completed, %d permanently failed (check 'straddler status' for details)",
			completed, failed,
		))
		return fmt.Errorf("%d job(s) permanently failed", failed)
	}
	runPrintln(ts(), fmt.Sprintf("all %d jobs complete (%s total)", completed+failed, elapsed))
	return nil
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
