package cli

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/CurtMeadows/straddler/internal/db"
	"github.com/spf13/cobra"
)

func newStatusCmd(d *deps) *cobra.Command {
	var (
		sourcePrefix string
		format       string
	)

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show job queue statistics",
		Long: `Display aggregate counts for each job status.

Examples:
  straddler status
  straddler status --source docker.io/library/nginx
  straddler status --format json`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			pool, err := db.Open(ctx,
				d.cfg.Database.DSN,
				d.cfg.Database.MaxConns,
				d.cfg.Database.MinConns,
				d.cfg.Database.ConnectTimeout,
			)
			if err != nil {
				return fmt.Errorf("connect to database: %w", err)
			}
			defer pool.Close()

			summary, err := db.NewQueue(pool).StatusSummaryFor(ctx, sourcePrefix)
			if err != nil {
				return fmt.Errorf("query status: %w", err)
			}

			total := summary.Pending + summary.InProgress + summary.Completed + summary.Failed

			switch format {
			case "json":
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]int64{
					"pending":     summary.Pending,
					"in_progress": summary.InProgress,
					"completed":   summary.Completed,
					"failed":      summary.Failed,
					"total":       total,
				})
			default:
				w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
				fmt.Fprintln(w, "STATUS\tCOUNT")
				fmt.Fprintln(w, "------\t-----")
				fmt.Fprintf(w, "pending\t%d\n", summary.Pending)
				fmt.Fprintf(w, "in_progress\t%d\n", summary.InProgress)
				fmt.Fprintf(w, "completed\t%d\n", summary.Completed)
				fmt.Fprintf(w, "failed\t%d\n", summary.Failed)
				fmt.Fprintf(w, "total\t%d\n", total)
				return w.Flush()
			}
		},
	}

	cmd.Flags().StringVar(&sourcePrefix, "source", "", "filter by source repository prefix")
	cmd.Flags().StringVar(&format, "format", "table", "output format: table|json")
	return cmd
}
