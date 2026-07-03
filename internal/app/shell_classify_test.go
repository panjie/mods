package app

import (
	"os"
	"path/filepath"
	"testing"

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
	})

	t.Run("tilde other user path", func(t *testing.T) {
		got := extractExternalPaths("cat ~root/.ssh/authorized_keys", ws)
		require.Equal(t, []string{"~root/.ssh/authorized_keys"}, got)
	})

	t.Run("parent traversal path", func(t *testing.T) {
		got := extractExternalPaths("cat ../sibling/file", ws)
		require.Equal(t, []string{"../sibling/file"}, got)
	})

	t.Run("bare dot-dot", func(t *testing.T) {
		got := extractExternalPaths("cat ../../file", ws)
		require.Equal(t, []string{"../../file"}, got)
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
		sep := string(filepath.Separator)
		got := extractExternalPaths("Get-ChildItem $HOME"+sep+"Downloads -Recurse", ws)
		require.NotEmpty(t, got, "$HOME path should be detected as external to workspace")
	})

	t.Run("PowerShell $env:USERPROFILE variable resolves to external", func(t *testing.T) {
		sep := string(filepath.Separator)
		got := extractExternalPaths("Get-ChildItem $env:USERPROFILE"+sep+"Downloads -Recurse", ws)
		require.NotEmpty(t, got, "$env:USERPROFILE path should be detected as external to workspace")
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

func TestExpandHomeVars(t *testing.T) {
	const home = "/home/test"
	cases := []struct {
		name string
		cmd  string
		want string
	}{
		{"$HOME backslash", "$HOME\\Downloads", "/home/test\\Downloads"},
		{"$HOME forward slash", "$HOME/Downloads", "/home/test/Downloads"},
		{"$env:USERPROFILE", "$env:USERPROFILE\\Downloads", "/home/test\\Downloads"},
		{"${HOME} braced", "${HOME}\\Downloads", "/home/test\\Downloads"},
		{"${env:USERPROFILE} braced", "${env:USERPROFILE}\\Downloads", "/home/test\\Downloads"},
		{"lowercase $home", "$home\\Downloads", "/home/test\\Downloads"},
		{"uppercase $ENV:USERPROFILE", "$ENV:USERPROFILE\\Downloads", "/home/test\\Downloads"},
		{"$HOMEPAGE not expanded", "$HOMEPAGE\\foo", "$HOMEPAGE\\foo"},
		{"no path separator after $HOME", "echo $HOME is nice", "echo $HOME is nice"},
		{"multiple vars", "$HOME\\a $env:USERPROFILE\\b", "/home/test\\a /home/test\\b"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, expandHomeVars(c.cmd, home))
		})
	}
	// Empty home dir: no expansion.
	require.Equal(t, "$HOME\\Downloads", expandHomeVars("$HOME\\Downloads", ""))
}
