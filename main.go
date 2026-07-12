// Package main provides the mods CLI.
package main

import (
	"fmt"
	"os"

	"github.com/panjie/mods/internal/cli"
	"github.com/panjie/mods/internal/tools"
)

// Build vars.
var (
	//nolint: gochecknoglobals
	Version   = ""
	CommitSHA = ""
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == tools.SudoAskpassHelperArg {
		if err := tools.RunSudoAskpassHelper(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	os.Exit(cli.Run(Version, CommitSHA))
}
