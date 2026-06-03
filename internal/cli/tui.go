package cli

import (
	"github.com/CurtMeadows/straddler/internal/tui"
	"github.com/spf13/cobra"
)

func newTUICmd(env *cmdEnv) *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Launch the interactive terminal dashboard",
		Long: `Open a full-screen interactive dashboard for managing sync jobs.

Navigate with Tab / Shift+Tab or number keys:
  [1] Dashboard  [2] Jobs  [3] Sync  [4] Workers  [5] Migrate

Press Q or Ctrl+C to exit.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return tui.Run(env.cfg)
		},
	}
}
