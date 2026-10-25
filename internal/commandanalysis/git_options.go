package commandanalysis

import (
	"path/filepath"
	"strings"
)

// Bind only context selectors and output controls whose effects are modeled.
// Config injection, external pagers and alternate worktrees remain unknown.
func bindGitGlobals(args []string, cwd string) ([]string, string, bool) {
	for len(args) > 0 {
		switch args[0] {
		case "--no-pager", "--literal-pathspecs", "--no-optional-locks":
			args = args[1:]
		case "-C":
			if len(args) < 2 || strings.ContainsAny(args[1], "$`*?[]") {
				return nil, cwd, false
			}
			if args[1] != "" {
				if filepath.IsAbs(args[1]) {
					cwd = filepath.Clean(args[1])
				} else {
					cwd = filepath.Join(cwd, args[1])
				}
			}
			args = args[2:]
		default:
			return args, cwd, !strings.HasPrefix(args[0], "-")
		}
	}
	return nil, cwd, false
}
