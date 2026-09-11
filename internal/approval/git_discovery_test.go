package approval

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGitDiscoveryReadOnly(t *testing.T) {
	for _, tc := range []struct {
		command string
		read    bool
	}{
		{"git remote", true},
		{"git remote -v", true},
		{"git remote get-url --push --all origin", true},
		{"git branch --show-current", true},
		{"git branch -vv", true},
		{"git branch --all --verbose", true},
		{"git branch new-branch", false},
		{"git branch -vv new-branch", false},
		{"git config --list --show-origin", true},
		{"git config --local --get remote.origin.pushurl", true},
		{"git config --get-regexp ^remote\\.", true},
		{"git config --get-all remote.origin.url", true},
		{"git remote set-url origin https://example.com/repo", false},
		{"git remote update", false},
		{"git remote prune origin", false},
		{"git remote add origin https://example.com/repo", false},
		{"git config remote.origin.url https://example.com/repo", false},
		{"git config --get --unset remote.origin.url", false},
		{"git config --list --edit", false},
		{"git config --get --output=out remote.origin.url", false},
		{"git branch --show-current --delete other", false},
	} {
		t.Run(tc.command, func(t *testing.T) {
			args := strings.Fields(tc.command)
			for _, posix := range []bool{true, false} {
				result := AssessArgvStaticWithPolicy(args[0], args[1:], posix, ReadOnlyCommandPolicy{})
				require.Equal(t, tc.read, result.Effect == EffectRead)
			}
			result := AssessShellStaticWithPolicy(tc.command, true, ReadOnlyCommandPolicy{})
			require.Equal(t, tc.read, result.Effect == EffectRead)
		})
	}
}
