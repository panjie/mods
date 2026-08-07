package approval

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAnalyzeShellStaticPOSIX(t *testing.T) {
	t.Run("read", func(t *testing.T) {
		got := AnalyzeShellStatic("git status && git diff", true)
		require.Equal(t, ShellStaticRead, got.Class)
		require.Empty(t, got.AffectedDirs)
		require.NotEmpty(t, got.Reason)
	})

	t.Run("write", func(t *testing.T) {
		got := AnalyzeShellStatic("cat > /tmp/out <<'EOF'\nhello\nEOF", true)
		require.Equal(t, ShellStaticWrite, got.Class)
		require.Contains(t, got.AffectedDirs, "/tmp")
		require.Contains(t, got.Reason, "static analysis")
	})

	t.Run("recursive remove targets directory", func(t *testing.T) {
		got := AnalyzeShellStatic("rm -rf ~/.ssh", true)
		require.Equal(t, ShellStaticWrite, got.Class)
		require.Equal(t, []string{"~/.ssh"}, got.AffectedDirs)
	})

	t.Run("unknown", func(t *testing.T) {
		got := AnalyzeShellStatic("some unsupported writer", true)
		require.Equal(t, ShellStaticUnknown, got.Class)
		require.Empty(t, got.AffectedDirs)
		require.Empty(t, got.Reason)
	})

	t.Run("env wrapped writer", func(t *testing.T) {
		got := AnalyzeShellStatic("env touch owned.txt", true)
		require.Equal(t, ShellStaticWrite, got.Class)
		require.Equal(t, []string{"."}, got.AffectedDirs)
	})

	t.Run("git output flag", func(t *testing.T) {
		got := AnalyzeShellStatic("git diff --output=owned.txt", true)
		require.Equal(t, ShellStaticWrite, got.Class)
		require.Equal(t, []string{"."}, got.AffectedDirs)
	})

	t.Run("git external diff helper", func(t *testing.T) {
		got := AnalyzeShellStatic("git diff --ext-diff", true)
		require.Equal(t, ShellStaticWrite, got.Class)
	})

	t.Run("xxd reverse output", func(t *testing.T) {
		got := AnalyzeShellStatic("xxd -r input.hex output.bin", true)
		require.Equal(t, ShellStaticWrite, got.Class)
		require.Equal(t, []string{"."}, got.AffectedDirs)
	})

	t.Run("runtime-expanded path requires review", func(t *testing.T) {
		got := AnalyzeShellStatic("cat ${FILE}", true)
		require.Equal(t, ShellStaticUnknown, got.Class)
		require.Contains(t, got.UnresolvedPaths, "$FILE")
	})

	t.Run("oldest downloads pipeline is read-only", func(t *testing.T) {
		got := AnalyzeShellStatic(
			`find "$HOME/Downloads" -type f -print0 | xargs -0 stat -f '%m %N' | sort -n | head -1`,
			true,
		)
		require.Equal(t, ShellStaticRead, got.Class)
		require.Empty(t, got.AffectedDirs)
	})

	t.Run("home-expanded write target remains deterministic", func(t *testing.T) {
		got := AnalyzeShellStatic(`rm "$HOME/Downloads/old.txt"`, true)
		require.Equal(t, ShellStaticWrite, got.Class)
		require.NotEmpty(t, got.AffectedDirs)
	})

	t.Run("find delete is a write", func(t *testing.T) {
		got := AnalyzeShellStatic(`find "$HOME/Downloads" -type f -delete`, true)
		require.Equal(t, ShellStaticWrite, got.Class)
	})

	t.Run("sort output is a write", func(t *testing.T) {
		got := AnalyzeShellStatic("sort -o /tmp/output input", true)
		require.Equal(t, ShellStaticWrite, got.Class)
		require.Equal(t, []string{"/tmp"}, got.AffectedDirs)
	})

	t.Run("xargs writer is not read-only", func(t *testing.T) {
		got := AnalyzeShellStatic(`find . -print0 | xargs -0 touch`, true)
		require.Equal(t, ShellStaticWrite, got.Class)
	})

	t.Run("dynamic target stays unresolved", func(t *testing.T) {
		got := AnalyzeShellStatic(`rm "$TARGET"`, true)
		require.Equal(t, ShellStaticWrite, got.Class)
		require.Contains(t, got.UnresolvedPaths, "$TARGET")
		require.Empty(t, got.AffectedDirs)
	})
}

func TestAnalyzeShellStaticPowerShell(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell AST classifier requires Windows")
	}
	t.Run("single-quoted write path with spaces", func(t *testing.T) {
		got := AnalyzeShellStatic(`Set-Content 'C:\Program Files\App\notes.txt' 'hello'`, false)
		require.Equal(t, ShellStaticWrite, got.Class)
		require.Contains(t, got.AffectedDirs, `C:\Program Files\App`)
		require.NotContains(t, got.AffectedDirs, `'C:`)
		require.NotContains(t, got.AffectedDirs, `Files\App`)
	})

	t.Run("single-quoted write path with escaped single quote", func(t *testing.T) {
		got := AnalyzeShellStatic(`Set-Content 'C:\O''Reilly\App\notes.txt' 'hello'`, false)
		require.Equal(t, ShellStaticWrite, got.Class)
		require.Contains(t, got.AffectedDirs, `C:\O'Reilly\App`)
		require.NotContains(t, got.AffectedDirs, `C:\`)
		require.NotContains(t, got.AffectedDirs, `C:\O`)
		require.NotContains(t, got.AffectedDirs, `Reilly\App`)
	})
}

func TestUnresolvedShellPathExpression(t *testing.T) {
	for _, value := range []string{`$PROFILE.CurrentUserCurrentHost`, `$prof`, `$(Join-Path $HOME x)`, `@args`} {
		require.True(t, IsUnresolvedShellPathExpression(value, false), value)
	}
	for _, value := range []string{`$HOME\Downloads\x`, `$env:USERPROFILE\Downloads\x`, `C:\Users\Test\x`, `relative\x`} {
		require.False(t, IsUnresolvedShellPathExpression(value, false), value)
	}
}

func TestPowerShellWritableTargetAnalysisSeparatesPathFromContent(t *testing.T) {
	dynamic := analyzeWritableTargetsFromTokens([]string{"Set-Content", "-Path", "$prof", "-Value", "$content"}, false)
	require.True(t, dynamic.Known)
	require.Empty(t, dynamic.Dirs)
	require.Equal(t, []string{"$prof"}, dynamic.Unresolved)

	concrete := analyzeWritableTargetsFromTokens([]string{"Set-Content", "-Path", `C:\Users\Test\profile.ps1`, "-Value", "$content"}, false)
	require.True(t, concrete.Known)
	require.Equal(t, []string{`C:\Users\Test`}, concrete.Dirs)
	require.Empty(t, concrete.Unresolved, "content variables are not path targets")
}

func TestAnalyzeArgvStatic(t *testing.T) {
	policy := ReadOnlyCommandPolicy{}
	for _, tc := range []struct {
		name    string
		program string
		args    []string
		class   ShellStaticClass
	}{
		{name: "read only", program: "git", args: []string{"status"}, class: ShellStaticRead},
		{name: "literal shell syntax", program: "ls", args: []string{"; rm -rf out"}, class: ShellStaticRead},
		{name: "known write", program: "rm", args: []string{"out.txt"}, class: ShellStaticWrite},
		{name: "literal variable-looking write path", program: "rm", args: []string{"$HOME"}, class: ShellStaticWrite},
		{name: "executable path not trusted", program: "./git", args: []string{"status"}, class: ShellStaticUnknown},
		{name: "unknown command", program: "custom-tool", class: ShellStaticUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := AnalyzeArgvStaticWithPolicy(tc.program, tc.args, true, policy)
			require.Equal(t, tc.class, got.Class)
		})
	}
	require.Equal(t, ShellStaticRead, AnalyzeArgvStaticWithPolicy("Git.EXE", []string{"status"}, false, policy).Class)
	require.Equal(t, []string{"."}, AnalyzeArgvStaticWithPolicy("rm", []string{"$HOME"}, true, policy).AffectedDirs)
}
