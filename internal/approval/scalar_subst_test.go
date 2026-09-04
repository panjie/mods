package approval

import (
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/syntax"
)

func firstCmdSubst(t *testing.T, expr string) *syntax.CmdSubst {
	t.Helper()
	file, err := syntax.NewParser(syntax.Variant(syntax.LangPOSIX)).Parse(strings.NewReader(expr), "")
	require.NoError(t, err)
	var found *syntax.CmdSubst
	syntax.Walk(file, func(node syntax.Node) bool {
		if found != nil {
			return false
		}
		if subst, ok := node.(*syntax.CmdSubst); ok {
			found = subst
			return false
		}
		return true
	})
	require.NotNil(t, found, "no command substitution in %q", expr)
	return found
}

func TestCmdSubstScalarConfined(t *testing.T) {
	cases := []struct {
		name string
		expr string
		want bool
	}{
		{"date epoch", "$(date +%s)", true},
		{"date iso format", "$(date +%Y-%m-%dT%H:%M:%S)", true},
		{"date utc", "$(date -u +%s)", true},
		{"date absolute program", "$(/usr/bin/date +%s)", true},
		{"uuidgen", "$(uuidgen)", true},
		{"whoami", "$(whoami)", true},
		{"hostname bare", "$(hostname)", true},
		{"id bare", "$(id)", true},
		{"date format with slash", "$(date +%Y/%m/%d)", false},
		{"date format file", "$(date -f fmts.txt +%s)", false},
		{"date dynamic format", "$(date +$FMT)", false},
		{"date pipeline", "$(date +%s | head -1)", false},
		{"id with flag", "$(id -u)", false},
		{"hostname with flag", "$(hostname -f)", false},
		{"uuidgen with flag", "$(uuidgen -r)", false},
		{"two statements", "$(date +%s; date +%s)", false},
		{"redirected", "$(date +%s >/tmp/out)", false},
		{"negated", "$( ! date +%s)", false},
		{"env prefix", "$(PATH=/tmp date +%s)", false},
		{"nested substitution", "$(date +%s$(echo x))", false},
		{"arbitrary program", "$(cat /etc/passwd)", false},
		{"program with args", "$(echo hello)", false},
		{"backquoted date", "`date +%s`", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, cmdSubstScalarConfined(firstCmdSubst(t, tc.expr)))
		})
	}
}

func TestPosixFileAllowsScalarConfinement(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    bool
	}{
		{"plain command", "cp a b", true},
		{"scalar substitution", "cp a b.$(date +%s)", true},
		{"prefix assignment", "PATH=/tmp:$PATH cp a b", false},
		{"standalone assignment", "FMT=%s; cp a b", false},
		{"function definition", "date() { echo /x; }; cp a b", false},
		{"export", "export PATH=/tmp; cp a b", false},
		{"readonly", "readonly FMT=%s; cp a b", false},
		{"unset", "unset FMT; cp a b", false},
		{"eval", "eval date; cp a b", false},
		{"source", "source env.sh; cp a b", false},
		{"alias", "alias date='echo /x'; cp a b", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file, err := syntax.NewParser(syntax.Variant(syntax.LangPOSIX)).Parse(strings.NewReader(tc.command), "")
			require.NoError(t, err)
			require.Equal(t, tc.want, posixFileAllowsScalarConfinement(file))
		})
	}
}

func TestAssessShellStaticScalarSubstitutionMaterializesWriteScope(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX AST coverage applies to non-Windows shell_run")
	}
	t.Run("timestamped cp backup", func(t *testing.T) {
		got := AnalyzeShellStatic(`cp ~/cfg/a.lua ~/cfg/a.lua.$(date +%s)`, true)
		require.Equal(t, ShellStaticWrite, got.Class)
		require.Equal(t, []string{"~/cfg"}, got.AffectedDirs)
		require.Empty(t, got.UnresolvedPaths)
	})

	t.Run("double-quoted confined substitution", func(t *testing.T) {
		got := AnalyzeShellStatic(`rm "f-$(uuidgen).tmp"`, true)
		require.Equal(t, ShellStaticWrite, got.Class)
		require.Equal(t, []string{"."}, got.AffectedDirs)
		require.Empty(t, got.UnresolvedPaths)
	})

	t.Run("confined redirection target", func(t *testing.T) {
		got := AnalyzeShellStatic(`echo hi > ~/log/entry-$(date +%s).log`, true)
		require.Equal(t, ShellStaticWrite, got.Class)
		require.Equal(t, []string{"~/log"}, got.AffectedDirs)
		require.Empty(t, got.UnresolvedPaths)
	})

	t.Run("relative destination", func(t *testing.T) {
		got := AnalyzeShellStatic("cp a.conf a.conf.$(date +%s)", true)
		require.Equal(t, ShellStaticWrite, got.Class)
		require.Equal(t, []string{"."}, got.AffectedDirs)
		require.Empty(t, got.UnresolvedPaths)
	})

	t.Run("non-whitelisted substitution stays dynamic", func(t *testing.T) {
		got := AnalyzeShellStatic(`cp a $(cat /tmp/list)`, true)
		require.Equal(t, ShellStaticWrite, got.Class)
		require.Equal(t, []string{"command substitution"}, got.UnresolvedPaths)
		require.Empty(t, got.AffectedDirs)
	})

	t.Run("slash-bearing date format stays dynamic", func(t *testing.T) {
		got := AnalyzeShellStatic(`cp a a.$(date +%Y/%m/%d)`, true)
		require.Equal(t, ShellStaticWrite, got.Class)
		require.Equal(t, []string{"command substitution"}, got.UnresolvedPaths)
		require.Empty(t, got.AffectedDirs)
	})

	t.Run("variable operand keeps command dynamic", func(t *testing.T) {
		got := AnalyzeShellStatic(`cp $src b.$(date +%s)`, true)
		require.Equal(t, ShellStaticWrite, got.Class)
		require.Equal(t, []string{"$src"}, got.UnresolvedPaths)
		require.Empty(t, got.AffectedDirs)
	})

	t.Run("state binding disables confinement command-wide", func(t *testing.T) {
		got := AnalyzeShellStatic(`export PATH=/tmp:$PATH; cp a b.$(date +%s)`, true)
		require.Equal(t, ShellStaticWrite, got.Class)
		require.Equal(t, []string{"$PATH", "command substitution"}, got.UnresolvedPaths)
		require.Empty(t, got.AffectedDirs)
	})
}

func TestExtractWritableDirsScalarSubstitution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX AST coverage applies to non-Windows shell_run")
	}
	require.Equal(t, []string{"~/cfg"}, ExtractWritableDirs(`cp ~/cfg/a.lua ~/cfg/a.lua.$(date +%s)`, true))
	require.Empty(t, ExtractWritableDirs(`cp ~/cfg/a.lua ~/cfg/a.lua.$(cat /tmp/list)`, true))
}
