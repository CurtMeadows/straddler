package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// BuildVersion holds values injected at link time by GoReleaser.
// Defaults to "dev" when built without -ldflags.
var (
	BuildVersion = "dev"
	BuildCommit  = "none"
	BuildDate    = "unknown"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Printf("straddler %s (commit %s, built %s)\n", BuildVersion, BuildCommit, BuildDate)
		},
	}
}
