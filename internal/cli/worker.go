package cli

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/CurtMeadows/straddler/internal/db"
	"github.com/CurtMeadows/straddler/internal/registry"
	"github.com/CurtMeadows/straddler/internal/telemetry"
	"github.com/CurtMeadows/straddler/internal/worker"
	"github.com/spf13/cobra"
)

func newWorkerCmd(d *deps) *cobra.Command {
	var concurrency int

	cmd := &cobra.Command{
		Use:   "worker",
		Short: "Start the image copy worker pool",
		Long: `Start a pool of workers that process jobs from the queue.

Each worker claims one job at a time using PostgreSQL's SKIP LOCKED so
multiple workers run in parallel without stepping on each other. Images
are streamed directly between registries — nothing is written to disk.

Press Ctrl+C or send SIGTERM to drain in-flight jobs and exit cleanly.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cfg := d.cfg

			// CLI --concurrency flag overrides the config file value.
			if cmd.Flags().Changed("concurrency") {
				cfg.Worker.Concurrency = concurrency
			}

			// Graceful shutdown: cancel the context on SIGTERM or SIGINT.
			// Workers finish their current job before exiting.
			ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
			defer stop()

			// Store logger in context so workers can retrieve it.
			ctx = telemetry.WithLogger(ctx, d.logger)

			pool, err := db.Open(ctx,
				cfg.Database.DSN,
				cfg.Database.MaxConns,
				cfg.Database.MinConns,
				cfg.Database.ConnectTimeout,
			)
			if err != nil {
				return fmt.Errorf("connect to database: %w", err)
			}
			defer pool.Close()

			transport := registry.BuildTransport(cfg.Registry.InsecureSkipTLS)
			regClient := registry.NewRemoteClient(
				registry.BuildKeychain(cfg.Registry.Source),
				registry.BuildKeychain(cfg.Registry.Dest),
				transport,
			)

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
			}

			p := worker.NewPool(
				cfg.Worker.Concurrency,
				cfg.Worker.StaleTimeout,
				workerCfg,
				db.NewQueue(pool),
				regClient,
			)

			d.logger.Info("worker pool started — press Ctrl+C to stop")

			if err := p.Run(ctx); err != nil {
				return fmt.Errorf("worker pool: %w", err)
			}

			d.logger.Info("worker pool stopped")
			return nil
		},
	}

	cmd.Flags().IntVar(&concurrency, "concurrency", 0, "parallel workers (default: from config)")
	return cmd
}
