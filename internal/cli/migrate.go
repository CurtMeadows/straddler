package cli

import (
	"fmt"

	"github.com/CurtMeadows/straddler/internal/db"
	"github.com/spf13/cobra"
)

func newMigrateCmd(d *deps) *cobra.Command {
	var steps int

	cmd := &cobra.Command{
		Use:       "migrate [up|down]",
		Short:     "Run database schema migrations",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"up", "down"},
		Long: `Apply or roll back straddler's PostgreSQL schema.

  straddler migrate up          apply all pending migrations
  straddler migrate down        roll back the last migration
  straddler migrate down -n 3   roll back the last 3 migrations`,
		RunE: func(_ *cobra.Command, args []string) error {
			dsn := d.cfg.Database.DSN
			switch args[0] {
			case "up":
				d.logger.Info("running migrate up")
				if err := db.MigrateUp(dsn); err != nil {
					return fmt.Errorf("migrate up: %w", err)
				}
				d.logger.Info("migrate up complete")
			case "down":
				d.logger.Info("running migrate down", "steps", steps)
				if err := db.MigrateDown(dsn, steps); err != nil {
					return fmt.Errorf("migrate down: %w", err)
				}
				d.logger.Info("migrate down complete")
			}
			return nil
		},
	}

	cmd.Flags().IntVarP(&steps, "steps", "n", 0, "number of migrations to roll back (down only; 0 = all)")
	return cmd
}
