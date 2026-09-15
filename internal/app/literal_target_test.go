package app

import (
	"testing"

	"github.com/panjie/mods/internal/pathutil"
	"github.com/stretchr/testify/require"
)

func TestPropagateLiteralTargets(t *testing.T) {
	ws := `C:\ws`
	literals := map[string]string{"p": `C:\Users\x\init.el`}

	known, dynamic, providers := propagateLiteralTargets(
		[]string{ws},
		[]string{`$p`, `$target`},
		literals,
		ws,
	)
	require.Equal(t, []string{ws, `C:\Users\x\init.el`}, known)
	require.Equal(t, []string{`$target`}, dynamic)
	require.Empty(t, providers)

	known, dynamic, providers = propagateLiteralTargets(nil, []string{`$target`}, literals, ws)
	require.Nil(t, known)
	require.Equal(t, []string{`$target`}, dynamic)
	require.Empty(t, providers)

	known, dynamic, providers = propagateLiteralTargets([]string{ws}, []string{`$p`}, nil, ws)
	require.Equal(t, []string{ws}, known)
	require.Equal(t, []string{`$p`}, dynamic)
	require.Empty(t, providers)

	known, dynamic, providers = propagateLiteralTargets(nil, []string{`$registry`}, map[string]string{
		"registry": `HKCU:\Software\Classes\Neovide`,
	}, ws)
	require.Empty(t, known, "a provider path must not be normalized beneath the working directory")
	require.Empty(t, dynamic)
	require.Equal(t, []string{`HKCU:\Software\Classes\Neovide`}, providers)

	known, dynamic, providers = propagateLiteralTargets(nil, []string{`$filesystem`}, map[string]string{
		"filesystem": `FileSystem::C:\Users\x\init.el`,
	}, ws)
	require.Equal(t, []string{`C:\Users\x\init.el`}, known)
	require.Empty(t, dynamic)
	require.Empty(t, providers)
}

func TestResolveLiteralTarget(t *testing.T) {
	opts := pathutil.DefaultOptions(`C:\ws`, pathutil.FlavorPowerShell)
	literals := map[string]string{"p": `C:\Users\x\init.el`, "dir": `C:\Users\x`}

	value, provider, ok := resolveLiteralTarget(`$p`, literals, opts)
	require.True(t, ok)
	require.False(t, provider)
	require.Equal(t, `C:\Users\x\init.el`, value)

	value, provider, ok = resolveLiteralTarget(`${p}`, literals, opts)
	require.True(t, ok)
	require.False(t, provider)
	require.Equal(t, `C:\Users\x\init.el`, value)

	value, provider, ok = resolveLiteralTarget(`$dir\sub.txt`, literals, opts)
	require.True(t, ok)
	require.False(t, provider)
	require.Equal(t, `C:\Users\x\sub.txt`, value)

	_, _, ok = resolveLiteralTarget(`$pref`, literals, opts)
	require.False(t, ok, "a variable-name prefix collision must not resolve")

	_, _, ok = resolveLiteralTarget(`$p.something`, literals, opts)
	require.False(t, ok, "a member access must not resolve")

	_, _, ok = resolveLiteralTarget(`$target`, literals, opts)
	require.False(t, ok, "an unknown variable must not resolve")
}
