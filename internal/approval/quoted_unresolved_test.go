package approval

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPowerShellArgQuoting(t *testing.T) {
	single, double := powerShellArgQuoting(`'abc'`)
	require.True(t, single)
	require.False(t, double)

	single, double = powerShellArgQuoting(`"abc"`)
	require.False(t, single)
	require.True(t, double)

	single, double = powerShellArgQuoting(`abc`)
	require.False(t, single)
	require.False(t, double)

	single, double = powerShellArgQuoting(`''`)
	require.True(t, single)

	single, double = powerShellArgQuoting(`"`)
	require.False(t, single)
	require.False(t, double)
}

func TestShellPathExpressionUnresolvedQuoted(t *testing.T) {
	tests := []struct {
		name   string
		value  string
		single bool
		double bool
		want   bool
	}{
		{name: "double-quoted elisp is data", value: `(json-insert (emacs-startup-usage))`, double: true, want: false},
		{name: "single-quoted elisp is data", value: `(json-insert (emacs-startup-usage))`, single: true, want: false},
		{name: "single-quoted variable is data", value: `$PROFILE`, single: true, want: false},
		{name: "double-quoted variable interpolates", value: `$PROFILE`, double: true, want: true},
		{name: "double-quoted subexpression interpolates", value: `$($sw.ElapsedMilliseconds) ms`, double: true, want: true},
		{name: "double-quoted env path interpolates", value: `$env:TEMP\log`, double: true, want: true},
		{name: "single-quoted cmd var stays runtime", value: `%TEMP%\notes.txt`, single: true, want: true},
		{name: "unquoted subexpression stays dynamic", value: `(Get-Location).Path`, want: true},
		{name: "unquoted join-path stays dynamic", value: `(Join-Path $dir x)`, want: true},
		{name: "double-quoted home prefix resolves concrete", value: `$HOME\Downloads\x`, double: true, want: false},
		{name: "double-quoted literal paren without dollar", value: `(foo bar)`, double: true, want: false},
		{name: "single-quoted literal paren without dollar", value: `(foo bar)`, single: true, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, shellPathExpressionUnresolvedQuoted(tt.value, tt.single, tt.double))
		})
	}
}
