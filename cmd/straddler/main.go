package main

import (
	"os"

	"github.com/CurtMeadows/straddler/internal/cli"
)

// Version variables are injected at build time by GoReleaser via -ldflags.
// When built with `go build` directly they remain at their "dev" defaults.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	cli.BuildVersion = version
	cli.BuildCommit = commit
	cli.BuildDate = date

	if err := cli.NewRootCommand().Execute(); err != nil {
		os.Exit(1)
	}
}
