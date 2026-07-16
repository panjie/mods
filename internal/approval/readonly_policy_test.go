package approval

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadOnlyCommandPolicyPlatformMatching(t *testing.T) {
	policy := ReadOnlyCommandPolicy{Commands: []string{"rg", "My-Report"}}

	require.True(t, policy.matchesPOSIX("/usr/local/bin/rg"))
	require.False(t, policy.matchesPOSIX("RG"))
	require.True(t, policy.matchesPowerShell(`C:\Tools\RG.EXE`))
	require.True(t, policy.matchesPowerShell("my-report.cmd"))
	require.False(t, policy.matchesPowerShell("other.exe"))
}
