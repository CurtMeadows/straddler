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
		RunE: func(cmd *cobra.Command, args []string) error {
			direction := args[0]

			d.logger.Info("running migrations", "direction", direction)

			if err := db.Migrate(d.cfg.Database.DSN, direction, steps); err != nil {
				return fmt.Errorf("migrate %s: %w", direction, err)
			}

			d.logger.Info("migrations complete", "direction", direction)
			return nil
		},
	}

	cmd.Flags().IntVarP(&steps, "steps", "n", 0, "number of migrations to roll back (down only; 0 = all)")
	return cmd
}
