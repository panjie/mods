package app

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/panjie/mods/internal/pathutil"
	"github.com/stretchr/testify/require"
)

func TestIsSimpleReadOnly(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		// Allowlisted commands, no metacharacters.
		{"ls", true},
		{"ls -la", true},
		{"cat README.md", true},
		{"cat file1 file2", true},
		{"head -n 5 log.txt", true},
		{"tail -f out.log", true},
		{"wc -l *.go", true},
		{"file /bin/ls", true},
		{"stat report.pdf", true},
		{"pwd", true},
		{"echo hello world", true},
		{"date", true},
		{"whoami", true},
		{"hostname", true},
		{"uname -a", true},
		{"du -sh .", true},
		{"df -h", true},
		{"which go", true},
		{"env", true},
		{"printenv PATH", true},
		{"basename /path/to/file", true},
		{"dirname /path/to/file", true},
		{"realpath link", true},
		{"readlink link", true},
		{"grep pattern file", true},
		{"egrep 'a|b' file", false}, // | metacharacter, send to LLM
		{"fgrep literal file", true},

		// Not in allowlist.
		{"find . -name '*.go'", false},
		{"sed 's/a/b/' file", false},
		{"awk '{print $1}'", false},
		{"sort file", false},
		{"tr a-z A-Z", false},
		{"cut -d: -f1 /etc/passwd", false},
		{"diff a b", false},
		{"tar tf archive.tar", false},
		{"curl https://example.com", false},
		{"wget https://example.com", false},
		{"rm file", false},
		{"cp src dst", false},
		{"mv old new", false},
		{"git status", false},
		{"make", false},

		// Metacharacters disqualify even allowlisted commands.
		{"cat file | grep foo", false},
		{"echo hello > out.txt", false},
		{"ls -la > /dev/null", false},
		{"cat a; rm b", false},
		{"grep foo && echo found", false},
		{"ls -la `pwd`", false},
		{"echo $(date)", false},

		// Full path to allowlisted command still works.
		{"/bin/ls", true},
		{"/usr/bin/cat file", true},
	}
	for _, c := range cases {
		require.Equalf(t, c.want, isSimpleReadOnly(c.cmd), "cmd=%q", c.cmd)
	}
}

func TestExtractExternalPaths(t *testing.T) {
	ws := filepath.Clean(t.TempDir())
	ext := filepath.Clean(t.TempDir())

	t.Run("workspace-local command returns empty", func(t *testing.T) {
		require.Empty(t, extractExternalPaths("cat README.md", ws))
		require.Empty(t, extractExternalPaths("ls -la", ws))
		require.Empty(t, extractExternalPaths("cat "+filepath.Join(ws, "a.txt"), ws))
	})

	t.Run("absolute external path", func(t *testing.T) {
		p := filepath.Join(ext, "secret")
		got := extractExternalPaths("cat "+p, ws)
		require.Equal(t, []string{p}, got)
	})

	t.Run("multiple external paths, deduplicated", func(t *testing.T) {
		got := extractExternalPaths(
			"cat "+filepath.Join(ext, "a")+" "+filepath.Join(ext, "b")+" "+filepath.Join(ext, "a"),
			ws,
		)
		require.Len(t, got, 2)
		require.Contains(t, got, filepath.Join(ext, "a"))
		require.Contains(t, got, filepath.Join(ext, "b"))
	})

	t.Run("tilde home path", func(t *testing.T) {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		require.NotEmpty(t, home)

		got := extractExternalPaths("cat ~/Downloads/secret", ws)
		require.Equal(t, []string{filepath.Join(home, "Downloads", "secret")}, got)
	})

	t.Run("tilde downloads directory normalizes for read commands", func(t *testing.T) {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		require.NotEmpty(t, home)

		want := []string{filepath.Join(home, "Downloads")}
		require.Equal(t, want, extractExternalPaths("find ~/Downloads -type f -printf '%s %p\\n'", ws))
		require.Equal(t, want, extractExternalPaths("du -sh ~/Downloads", ws))
		require.Equal(t, want, extractExternalPaths("du -sk ~/Downloads/* 2>/dev/null | sort -rn | head -20", ws))
	})

	t.Run("absolute shell glob collapses to containing directory", func(t *testing.T) {
		got := extractExternalPaths("cat "+filepath.Join(ext, "*.log"), ws)
		require.Equal(t, []string{ext}, got)
	})

	t.Run("tilde other user path", func(t *testing.T) {
		got := extractExternalPaths("cat ~root/.ssh/authorized_keys", ws)
		require.Equal(t, []string{"~root/.ssh/authorized_keys"}, got)
	})

	t.Run("parent traversal path", func(t *testing.T) {
		got := extractExternalPaths("cat ../sibling/file", ws)
		require.Equal(t, []string{filepath.Clean(filepath.Join(ws, "..", "sibling", "file"))}, got)
	})

	t.Run("parent traversal shell glob collapses to containing directory", func(t *testing.T) {
		got := extractExternalPaths("cat ../sibling/*.txt", ws)
		require.Equal(t, []string{filepath.Clean(filepath.Join(ws, "..", "sibling"))}, got)
	})

	t.Run("bare dot-dot", func(t *testing.T) {
		got := extractExternalPaths("cat ../../file", ws)
		require.Equal(t, []string{filepath.Clean(filepath.Join(ws, "..", "..", "file"))}, got)
	})

	t.Run("find with external dir", func(t *testing.T) {
		got := extractExternalPaths("find /home/user/dev -type f -printf '%s %p\\n'", ws)
		require.Equal(t, []string{"/home/user/dev"}, got)
	})

	t.Run("bare root slash", func(t *testing.T) {
		got := extractExternalPaths("find / -delete", ws)
		require.Equal(t, []string{"/"}, got)
	})

	t.Run("mixed internal and external", func(t *testing.T) {
		p := filepath.Join(ext, "b")
		got := extractExternalPaths(
			"cat "+filepath.Join(ws, "a")+" "+p,
			ws,
		)
		require.Equal(t, []string{p}, got)
	})

	t.Run("no workspace treats all absolute paths as external", func(t *testing.T) {
		got := extractExternalPaths("cat /etc/passwd", "")
		require.Equal(t, []string{"/etc/passwd"}, got)
	})

	t.Run("workspace-local absolute path is not external", func(t *testing.T) {
		got := extractExternalPaths("cat "+filepath.Join(ws, "a.txt"), ws)
		require.Empty(t, got)
	})

	t.Run("PowerShell $HOME variable resolves to external", func(t *testing.T) {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		require.NotEmpty(t, home)
		sep := string(filepath.Separator)
		got := extractExternalPaths("Get-ChildItem $HOME"+sep+"Downloads -Recurse", ws)
		require.Equal(t, []string{filepath.Join(home, "Downloads")}, got)
	})

	t.Run("PowerShell $env:USERPROFILE variable resolves to external", func(t *testing.T) {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		require.NotEmpty(t, home)
		sep := string(filepath.Separator)
		got := extractExternalPaths("Get-ChildItem $env:USERPROFILE"+sep+"Downloads -Recurse", ws)
		require.Equal(t, []string{filepath.Join(home, "Downloads")}, got)
	})

	t.Run("PowerShell tilde backslash resolves to external", func(t *testing.T) {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		require.NotEmpty(t, home)
		got := extractExternalPaths(`Get-Content ~\Downloads\notes.txt`, ws)
		require.Equal(t, []string{filepath.Join(home, "Downloads", "notes.txt")}, got)
	})

	t.Run("cmd USERPROFILE variable resolves to external", func(t *testing.T) {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		require.NotEmpty(t, home)
		got := extractExternalPaths(`type %USERPROFILE%\Downloads\notes.txt`, ws)
		require.Equal(t, []string{filepath.Join(home, "Downloads", "notes.txt")}, got)
	})

	t.Run("PowerShell drive glob collapses to containing directory", func(t *testing.T) {
		got := extractExternalPathsWithFlavor(`Get-Content C:\Users\Test\Downloads\*`, ws, pathutil.FlavorPowerShell)
		require.Equal(t, []string{`C:\Users\Test\Downloads`}, got)
	})
}

func TestMentionsExternalPath(t *testing.T) {
	ws := filepath.Clean(t.TempDir())
	ext := filepath.Clean(t.TempDir())
	cases := []struct {
		cmd  string
		want bool
	}{
		{"cat README.md", false},
		{"cat " + filepath.Join(ws, "a.txt"), false},
		{"cat " + filepath.Join(ext, "secret"), true},
		{"ls -la", false},
		{"grep foo ~/Downloads/r", true},
		{"cat ../sibling/file", true},
		{"type C:\\Users\\Public\\x", true},
		{"echo hello world", false},
		{"cat " + filepath.Join(ws, "sub", "a") + " " + filepath.Join(ext, "b"), true},
		{"Get-ChildItem $HOME\\Downloads", true},
		{"ls $HOME/Downloads", true},
		{"Get-ChildItem $env:USERPROFILE\\Downloads", true},
		{`Get-Content ~\Downloads\x`, true},
		{`type %USERPROFILE%\Downloads\x`, true},
		{"echo $HOME is nice", false},
	}
	for _, c := range cases {
		require.Equalf(t, c.want, mentionsExternalPath(c.cmd, ws), "cmd=%q", c.cmd)
	}
}

func TestMentionsExternalPathEmptyRoot(t *testing.T) {
	// No workspace context: any absolute path is treated as potentially external.
	require.True(t, mentionsExternalPath("cat /etc/passwd", ""))
	require.False(t, mentionsExternalPath("cat README.md", ""))
}

func TestShellRunPathFlavor(t *testing.T) {
	if runtime.GOOS == "windows" {
		require.True(t, shellToolUsesPowerShell("shell_run"))
		require.Equal(t, pathutil.FlavorPowerShell, shellPathFlavor("shell_run"))
		return
	}
	require.False(t, shellToolUsesPowerShell("shell_run"))
	require.Equal(t, pathutil.FlavorPOSIX, shellPathFlavor("shell_run"))
}

func TestAnalyzeShellCommandASTReadOnly(t *testing.T) {
	// These commands would previously fall through to the LLM classifier
	// because of shell metacharacters (|, &&, $()) or missing from the
	// simple allowlist. The AST classifier should catch them locally.
	cases := []struct {
		name string
		tool string
		cmd  string
	}{
		{"pipe cat grep", "shell_run", "cat file | grep foo"},
		{"pipe ls head", "shell_run", "ls -la | head -5"},
		{"git status", "shell_run", "git status"},
		{"git log", "shell_run", "git log --oneline"},
		{"docker ps", "shell_run", "docker ps -a"},
		{"kubectl get", "shell_run", "kubectl get pods"},
		{"cmdsubst date", "shell_run", "echo $(date)"},
		{"and git", "shell_run", "git status && git diff"},
		{"subshell", "shell_run", "(git status)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mods := &Mods{
				shellAnalyzer: func(tool, command string) shellCommandAnalysis {
					t.Fatalf("LLM classifier should not be called for %q", command)
					return defaultShellCommandAnalysis()
				},
			}
			t.Cleanup(func() { mods.shellAnalyzer = nil })
			result := mods.analyzeShellCommand(c.tool, c.cmd)
			require.Falsef(t, result.NeedsReview, "cmd=%q should be read-only", c.cmd)
			require.NotEmptyf(t, result.Reason, "cmd=%q should have a reason", c.cmd)
		})
	}
}

func TestAnalyzeShellCommandASTExternalPath(t *testing.T) {
	// Read-only command with external path: AST classifier says read-only,
	// extractExternalPaths provides AffectedDirs, no LLM call needed.
	mods := &Mods{
		shellAnalyzer: func(tool, command string) shellCommandAnalysis {
			t.Fatalf("LLM classifier should not be called for %q", command)
			return defaultShellCommandAnalysis()
		},
	}
	t.Cleanup(func() { mods.shellAnalyzer = nil })
	result := mods.analyzeShellCommand("shell_run", "cat /etc/passwd")
	require.False(t, result.NeedsReview, "cat /etc/passwd should be read-only")
	require.Contains(t, result.AffectedDirs, "/etc/passwd")
}

func TestAnalyzeShellCommandPowerShellReadOnly(t *testing.T) {
	// PowerShell AST classifier requires Windows + pwsh.exe. On other
	// platforms IsReadOnlyPowerShell fail-closes, so read-only commands
	// reach the LLM seam and t.Fatalf would fire.
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell AST classifier requires Windows")
	}
	// These PowerShell commands should be caught by the AST classifier
	// and never reach the LLM (shellAnalyzer test seam).
	cases := []struct {
		name string
		cmd  string
	}{
		{"get-childitem", "Get-ChildItem"},
		{"get-content", "Get-Content file.txt"},
		{"test-path", "Test-Path x"},
		{"get-process", "Get-Process"},
		{"pipe sort", "Get-ChildItem | Sort-Object Name"},
		{"alias gci", "gci"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mods := &Mods{
				shellAnalyzer: func(tool, command string) shellCommandAnalysis {
					t.Fatalf("LLM classifier should not be called for %q", command)
					return defaultShellCommandAnalysis()
				},
			}
			t.Cleanup(func() { mods.shellAnalyzer = nil })
			result := mods.analyzeShellCommand("powershell_run", c.cmd)
			require.Falsef(t, result.NeedsReview, "cmd=%q should be read-only", c.cmd)
		})
	}
}

func TestAnalyzeShellCommandPowerShellWriteGoesToLLM(t *testing.T) {
	// PowerShell AST classifier requires Windows + pwsh.exe. On other
	// platforms the classifier fail-closes and all PowerShell commands
	// reach the LLM seam, so this test still passes but for a different
	// reason — skip to avoid testing the wrong code path.
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell AST classifier requires Windows")
	}
	// Write PowerShell commands should fall through to the LLM (test seam).
	called := false
	mods := &Mods{
		shellAnalyzer: func(tool, command string) shellCommandAnalysis {
			called = true
			return shellCommandAnalysis{NeedsReview: true, Reason: "write command"}
		},
	}
	t.Cleanup(func() { mods.shellAnalyzer = nil })
	result := mods.analyzeShellCommand("powershell_run", "Set-Content file.txt 'hello'")
	require.True(t, result.NeedsReview, "Set-Content should require review")
	require.True(t, called, "LLM classifier should be called for write commands")
}

func TestAnalyzeShellCommandPowerShellExternalPathDirs(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell AST classifier requires Windows")
	}
	// Read-only PowerShell command with external path: AST classifier
	// says read-only, AST-extracted paths provide AffectedDirs, no LLM call.
	mods := &Mods{
		shellAnalyzer: func(tool, command string) shellCommandAnalysis {
			t.Fatalf("LLM classifier should not be called for %q", command)
			return defaultShellCommandAnalysis()
		},
	}
	t.Cleanup(func() { mods.shellAnalyzer = nil })
	result := mods.analyzeShellCommand("powershell_run", "Get-Content C:\\Users\\Public\\file.txt")
	require.False(t, result.NeedsReview, "Get-Content should be read-only")
	require.NotEmpty(t, result.AffectedDirs, "should have affected dirs from AST")
}
