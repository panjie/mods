package app

import (
	"testing"

	"github.com/panjie/mods/internal/pathutil"
	"github.com/stretchr/testify/require"
)

func TestPropagateLiteralTargets(t *testing.T) {
	ws := `C:\ws`
	literals := map[string]string{"p": `C:\Users\x\init.el`}

	known, dynamic := propagateLiteralTargets(
		[]string{ws},
		[]string{`$p`, `$target`},
		literals,
		ws,
	)
	require.Equal(t, []string{ws, `C:\Users\x\init.el`}, known)
	require.Equal(t, []string{`$target`}, dynamic)

	known, dynamic = propagateLiteralTargets(nil, []string{`$target`}, literals, ws)
	require.Nil(t, known)
	require.Equal(t, []string{`$target`}, dynamic)

	known, dynamic = propagateLiteralTargets([]string{ws}, []string{`$p`}, nil, ws)
	require.Equal(t, []string{ws}, known)
	require.Equal(t, []string{`$p`}, dynamic)
}

func TestResolveLiteralTarget(t *testing.T) {
	opts := pathutil.DefaultOptions(`C:\ws`, pathutil.FlavorPowerShell)
	literals := map[string]string{"p": `C:\Users\x\init.el`, "dir": `C:\Users\x`}

	value, ok := resolveLiteralTarget(`$p`, literals, opts)
	require.True(t, ok)
	require.Equal(t, `C:\Users\x\init.el`, value)

	value, ok = resolveLiteralTarget(`${p}`, literals, opts)
	require.True(t, ok)
	require.Equal(t, `C:\Users\x\init.el`, value)

	value, ok = resolveLiteralTarget(`$dir\sub.txt`, literals, opts)
	require.True(t, ok)
	require.Equal(t, `C:\Users\x\sub.txt`, value)

	_, ok = resolveLiteralTarget(`$pref`, literals, opts)
	require.False(t, ok, "a variable-name prefix collision must not resolve")

	_, ok = resolveLiteralTarget(`$p.something`, literals, opts)
	require.False(t, ok, "a member access must not resolve")

	_, ok = resolveLiteralTarget(`$target`, literals, opts)
	require.False(t, ok, "an unknown variable must not resolve")
}
