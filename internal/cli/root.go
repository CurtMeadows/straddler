// Package cli assembles the straddler command tree.
package cli

import (
	"log/slog"

	"github.com/CurtMeadows/straddler/internal/config"
	"github.com/CurtMeadows/straddler/internal/telemetry"
	"github.com/spf13/cobra"
)

// deps holds shared resources initialised once in PersistentPreRunE and
// closed over by each subcommand's RunE. Keeping them here avoids global
// variables while still giving every command access to config and logging.
type deps struct {
	cfg    *config.Config
	logger *slog.Logger
}

// NewRootCommand builds and returns the root cobra command with all
// subcommands attached.
func NewRootCommand() *cobra.Command {
	var (
		cfgFile  string
		logLevel string
		logFmt   string
	)

	d := &deps{}

	root := &cobra.Command{
		Use:   "straddler",
		Short: "Sync OCI images between any two container registries",
		Long: `straddler copies Docker/OCI images from one registry to another.

It enumerates all tags for a source image, writes them to a PostgreSQL job
queue, and a worker pool streams each image directly between registries —
no Docker daemon required.

Supported registries: Docker Hub, ECR, GCR/GAR, GHCR, Quay, Harbor,
self-hosted, or any OCI-compliant registry.`,
		SilenceUsage: true,
		// PersistentPreRunE runs before every subcommand's RunE.
		// It loads config, then builds the logger from the merged values.
		// CLI flags (--log-level, --log-format) override whatever is in the file.
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return err
			}

			// CLI flags override config-file values for log settings.
			// Must use Root().PersistentFlags() because these flags are defined
			// on the root command — cmd.Flags() only sees flags on the subcommand.
			pf := cmd.Root().PersistentFlags()
			if pf.Changed("log-level") {
				cfg.Log.Level = logLevel
			}
			if pf.Changed("log-format") {
				cfg.Log.Format = logFmt
			}

			d.cfg = cfg
			d.logger = telemetry.New(cfg.Log.Level, cfg.Log.Format)
			slog.SetDefault(d.logger)
			return nil
		},
	}

	root.PersistentFlags().StringVar(&cfgFile, "config", "", "path to straddler.yaml (default: ./straddler.yaml)")
	root.PersistentFlags().StringVar(&logLevel, "log-level", "", "log verbosity: debug|info|warn|error")
	root.PersistentFlags().StringVar(&logFmt, "log-format", "", "log output format: json|text")

	root.AddCommand(
		newRunCmd(d),
		newSyncCmd(d),
		newWorkerCmd(d),
		newMigrateCmd(d),
		newStatusCmd(d),
		newTUICmd(d),
		newVersionCmd(),
	)

	return root
}
