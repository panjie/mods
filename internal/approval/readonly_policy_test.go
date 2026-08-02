package approval

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadOnlyCommandPolicyPlatformMatching(t *testing.T) {
	policy := ReadOnlyCommandPolicy{Commands: []string{"rg", "My-Report"}}

	require.False(t, policy.matchesPOSIX("/usr/local/bin/rg"))
	require.True(t, policy.matchesPOSIX("rg"))
	require.False(t, policy.matchesPOSIX("RG"))
	require.False(t, policy.matchesPowerShell(`C:\Tools\RG.EXE`))
	require.True(t, policy.matchesPowerShell("RG.EXE"))
	require.True(t, policy.matchesPowerShell("my-report.cmd"))
	require.False(t, policy.matchesPowerShell("other.exe"))
}
