package app

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMaterializeProbeTargets(t *testing.T) {
	known, dynamic := materializeProbeTargets(
		[]string{`C:\ws`},
		[]string{`$PROFILE`, `$target`},
		map[string]string{`$PROFILE`: `C:\Users\Test\Documents\PowerShell\Microsoft.PowerShell_profile.ps1`},
	)
	require.Equal(t, []string{`C:\ws`, `C:\Users\Test\Documents\PowerShell\Microsoft.PowerShell_profile.ps1`}, known)
	require.Equal(t, []string{`$target`}, dynamic)

	// No resolutions is a no-op that preserves both slices.
	known, dynamic = materializeProbeTargets([]string{`C:\ws`}, []string{`$target`}, nil)
	require.Equal(t, []string{`C:\ws`}, known)
	require.Equal(t, []string{`$target`}, dynamic)

	// A resolution already present in known dirs is not duplicated.
	known, dynamic = materializeProbeTargets(
		[]string{`C:\Users\Test`},
		[]string{`$HOME`},
		map[string]string{`$HOME`: `C:\Users\Test`},
	)
	require.Equal(t, []string{`C:\Users\Test`}, known)
	require.Empty(t, dynamic)
}
