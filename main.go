// Package main provides the mods CLI.
package main

import (
	"os"

	"github.com/charmbracelet/mods/internal/cli"
)

// Build vars.
var (
	//nolint: gochecknoglobals
	Version   = ""
	CommitSHA = ""
)

func main() {
	os.Exit(cli.Run(Version, CommitSHA))
}
