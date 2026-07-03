package pathutil

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExpandTokenPOSIX(t *testing.T) {
	opts := Options{
		Workspace: "/workspace/project",
		Home:      "/home/test",
		Flavor:    FlavorPOSIX,
	}

	require.Equal(t, "/home/test/Downloads", NormalizePath("~/Downloads", opts))
	require.Equal(t, "/home/test/Downloads", NormalizePath("$HOME/Downloads", opts))
	require.Equal(t, "/home/test/Downloads", NormalizePath("${HOME}/Downloads", opts))
	require.Equal(t, "/workspace/sibling/file", NormalizePath("../sibling/file", opts))
	require.Equal(t, "/workspace/project/~/literal", NormalizePath("./~/literal", opts))
}

func TestExpandTokenPowerShell(t *testing.T) {
	opts := Options{
		Workspace: `C:\work\project`,
		Home:      `C:\Users\Test`,
		Flavor:    FlavorPowerShell,
	}

	require.Equal(t, `C:\Users\Test\Downloads`, NormalizePath(`~\Downloads`, opts))
	require.Equal(t, `C:\Users\Test\Downloads`, NormalizePath(`$HOME\Downloads`, opts))
	require.Equal(t, `C:\Users\Test\Downloads`, NormalizePath(`${HOME}\Downloads`, opts))
	require.Equal(t, `C:\Users\Test\Downloads`, NormalizePath(`$env:USERPROFILE\Downloads`, opts))
	require.Equal(t, `C:\Users\Test\Downloads`, NormalizePath(`${env:USERPROFILE}\Downloads`, opts))
	require.Equal(t, `C:\Users\Test\Downloads`, NormalizePath(`$ENV:USERPROFILE\Downloads`, opts))
	require.Equal(t, `C:\work\project\~\literal`, NormalizePath(`.\~\literal`, opts))
}

func TestExpandTokenCMD(t *testing.T) {
	opts := Options{
		Workspace: `C:\work\project`,
		Env: map[string]string{
			"USERPROFILE": `C:\Users\Test`,
			"HOMEDRIVE":   `C:`,
			"HOMEPATH":    `\Users\Test`,
		},
		Flavor: FlavorCMD,
	}

	require.Equal(t, `C:\Users\Test\Downloads`, NormalizePath(`%USERPROFILE%\Downloads`, opts))
	require.Equal(t, `C:\Users\Test\Downloads`, NormalizePath(`%HOMEDRIVE%%HOMEPATH%\Downloads`, opts))
}

func TestContains(t *testing.T) {
	require.True(t, Contains("/tmp/cache", "/tmp/cache/file"))
	require.True(t, Contains("/tmp/cache", "/tmp/cache"))
	require.False(t, Contains("/tmp/cache", "/tmp/cache2/file"))

	require.True(t, Contains(`C:\Users`, `c:\users\test\file.txt`))
	require.False(t, Contains(`C:\Users`, `C:\Users2\file.txt`))
	require.True(t, Contains(`C:\`, `C:\old.txt`))

	require.True(t, Contains(`\\server\share`, `\\SERVER\share\dir\file.txt`))
	require.False(t, Contains(`\\server\share`, `\\server\share2\file.txt`))
}

func TestLocation(t *testing.T) {
	workspace := "/workspace/project"
	safe := "/tmp"

	require.Equal(t, LocationWorkspace, Location("/workspace/project/a.txt", workspace, []string{safe}))
	require.Equal(t, LocationWorkspace, Location("relative/path", workspace, []string{safe}))
	require.Equal(t, LocationSafe, Location("/tmp/cache/x", workspace, []string{safe}))
	require.Equal(t, LocationExternal, Location("/etc/passwd", workspace, []string{safe}))
	require.Equal(t, LocationExternal, Location("~root/.ssh/authorized_keys", workspace, []string{safe}))
	require.Equal(t, LocationUnknown, Location("", workspace, []string{safe}))
}

func TestNormalizeDirs(t *testing.T) {
	opts := Options{
		Workspace: "/workspace/project",
		Home:      "/home/test",
		Flavor:    FlavorPOSIX,
	}

	got := NormalizeDirs([]string{
		"~/Downloads/file.txt",
		"/home/test/Downloads",
		"../sibling/file",
	}, opts)
	require.ElementsMatch(t, []string{
		"/home/test/Downloads",
		"/workspace/sibling/file",
	}, got)
}

func TestNormalizeShellPathGlob(t *testing.T) {
	posix := Options{
		Workspace: "/workspace/project",
		Home:      "/home/test",
		Flavor:    FlavorPOSIX,
	}

	require.Equal(t, "/home/test/Downloads", NormalizeShellPath("~/Downloads/*", posix))
	require.Equal(t, "/home/test/Downloads", NormalizeShellPath("$HOME/Downloads/*", posix))
	require.Equal(t, "/home/test/Downloads", NormalizeShellPath("${HOME}/Downloads/*", posix))
	require.Equal(t, "/tmp", NormalizeShellPath("/tmp/*.log", posix))
	require.Equal(t, "/workspace/sibling", NormalizeShellPath("../sibling/*.txt", posix))
	require.Equal(t, "/home/test/Downloads", NormalizeShellPath("~/Downloads/**/*.zip", posix))
	require.Equal(t, "/", NormalizeShellPath("/*", posix))
	require.Equal(t, "/workspace/project/src", NormalizeShellPath("src/*.go", posix))
	require.Equal(t, "/tmp/*.log", NormalizePath("/tmp/*.log", posix))
}

func TestNormalizeShellPathGlobPowerShell(t *testing.T) {
	opts := Options{
		Workspace: `C:\work\project`,
		Home:      `C:\Users\Test`,
		Flavor:    FlavorPowerShell,
	}

	require.Equal(t, `C:\Users\Test\Downloads`, NormalizeShellPath(`~\Downloads\*`, opts))
	require.Equal(t, `C:\Users\Test\Downloads`, NormalizeShellPath(`$HOME\Downloads\*`, opts))
	require.Equal(t, `C:\Users\Test\Downloads`, NormalizeShellPath(`$env:USERPROFILE\Downloads\*`, opts))
	require.Equal(t, `C:\Users\Test\Downloads`, NormalizeShellPath(`${env:USERPROFILE}\Downloads\*`, opts))
	require.Equal(t, `C:\Users\Test\Downloads`, NormalizeShellPath(`C:\Users\Test\Downloads\*`, opts))
	require.Equal(t, `C:\Users\Test\Downloads`, NormalizeShellPath(`C:\Users\Test\Downloads\**\*.zip`, opts))
}

func TestNormalizeShellPathGlobCMD(t *testing.T) {
	opts := Options{
		Workspace: `C:\work\project`,
		Env: map[string]string{
			"USERPROFILE": `C:\Users\Test`,
			"HOMEDRIVE":   `C:`,
			"HOMEPATH":    `\Users\Test`,
		},
		Flavor: FlavorCMD,
	}

	require.Equal(t, `C:\Users\Test\Downloads`, NormalizeShellPath(`%USERPROFILE%\Downloads\*`, opts))
	require.Equal(t, `C:\Users\Test\Downloads`, NormalizeShellPath(`%HOMEDRIVE%%HOMEPATH%\Downloads\*`, opts))
}

func TestParentDir(t *testing.T) {
	require.Equal(t, "/home/test", ParentDir("/home/test/file.txt"))
	require.Equal(t, `C:\Users\Test`, ParentDir(`C:\Users\Test\file.txt`))
	require.Equal(t, `C:\`, ParentDir(`C:\file.txt`))
	require.Equal(t, `~root/.ssh`, ParentDir(`~root/.ssh/authorized_keys`))
}
