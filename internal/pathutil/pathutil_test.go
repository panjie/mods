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

func TestIsStableEnvName(t *testing.T) {
	require.True(t, IsStableEnvName("SystemRoot"))
	require.True(t, IsStableEnvName(" programfiles(x86) "))
	require.False(t, IsStableEnvName("USERPROFILE"))
	require.False(t, IsStableEnvName("AWS_SHARED_CREDENTIALS_FILE"))
}

func TestIsPublicEnvName(t *testing.T) {
	require.True(t, IsPublicEnvName("Path", FlavorPowerShell))
	require.True(t, IsPublicEnvName(" path ", FlavorPowerShell))
	require.True(t, IsPublicEnvName("PROCESSOR_ARCHITECTURE", FlavorPowerShell))
	require.True(t, IsPublicEnvName("PATHEXT", FlavorPowerShell))
	require.False(t, IsPublicEnvName("USERPROFILE", FlavorPowerShell))
	require.False(t, IsPublicEnvName("GITHUB_TOKEN", FlavorPowerShell))
	require.False(t, IsPublicEnvName("STARSHIP_CONFIG", FlavorPowerShell))

	require.True(t, IsPublicEnvName("PATH", FlavorPOSIX))
	require.False(t, IsPublicEnvName("path", FlavorPOSIX), "POSIX variables are case-sensitive")
	require.False(t, IsPublicEnvName("LANG", FlavorPOSIX))
}

func TestExpandEnvPath(t *testing.T) {
	opts := Options{
		Workspace: `C:\work\project`,
		Env: map[string]string{
			"SYSTEMROOT":        `C:\Windows`,
			"PROGRAMFILES(X86)": `C:\Program Files (x86)`,
			"TEMP":              `C:\Users\Test\AppData\Local\Temp`,
			"USERPROFILE":       `C:\Users\Test`,
			"DATA":              `D:\Data`,
		},
		Flavor: FlavorPowerShell,
	}

	expanded, ok := ExpandEnvPath(`$env:SystemRoot\WinSxS`, opts)
	require.True(t, ok)
	require.Equal(t, `C:\Windows\WinSxS`, expanded)

	expanded, ok = ExpandEnvPath(`$ENV:SYSTEMROOT/WinSxS`, opts)
	require.True(t, ok)
	require.Equal(t, `C:\Windows\WinSxS`, expanded)

	expanded, ok = ExpandEnvPath(`${env:ProgramFiles(x86)}\Vendor`, opts)
	require.True(t, ok)
	require.Equal(t, `C:\Program Files (x86)\Vendor`, expanded)

	expanded, ok = ExpandEnvPath(`$env:TEMP\build`, opts)
	require.True(t, ok)
	require.Equal(t, `C:\Users\Test\AppData\Local\Temp\build`, expanded)

	expanded, ok = ExpandEnvPath(`$env:TEMP\..\..\Documents`, opts)
	require.True(t, ok)
	require.Equal(t, `C:\Users\Test\AppData\Documents`, expanded)

	expanded, ok = ExpandEnvPath(`$env:USERPROFILE\.emacs.d\init.el`, opts)
	require.True(t, ok, "any variable with a dir-like value expands, not just the reserved set")
	require.Equal(t, `C:\Users\Test\.emacs.d\init.el`, expanded)

	expanded, ok = ExpandEnvPath(`$env:DATA/logs`, opts)
	require.True(t, ok)
	require.Equal(t, `D:\Data\logs`, expanded)

	_, ok = ExpandEnvPath(`$env:SystemRoot`, opts)
	require.False(t, ok, "bare value reference must stay dynamic")
	_, ok = ExpandEnvPath(`${env:SystemRoot}`, opts)
	require.False(t, ok, "braced bare reference must stay dynamic")
	_, ok = ExpandEnvPath(`$env:AWS_SHARED_CREDENTIALS_FILE\creds`, opts)
	require.False(t, ok, "unset variable must stay dynamic")
	_, ok = ExpandEnvPath(`$env:ProgramData\`, opts)
	require.False(t, ok, "unset variable must stay dynamic")
	_, ok = ExpandEnvPath(`$env:SystemRoot\WinSxS`, Options{Flavor: FlavorPOSIX, Env: opts.Env})
	require.False(t, ok, "POSIX flavor must not expand PowerShell env references")
}

func TestExpandEnvPathPOSIX(t *testing.T) {
	opts := Options{
		Workspace: "/workspace/project",
		Env: map[string]string{
			"DATA": "/srv/data",
			"PATH": "/usr/bin:/bin",
		},
		Flavor: FlavorPOSIX,
	}

	expanded, ok := ExpandEnvPath(`$DATA/cache/app.yml`, opts)
	require.True(t, ok)
	require.Equal(t, `/srv/data/cache/app.yml`, expanded)

	expanded, ok = ExpandEnvPath(`${DATA}/cache/app.yml`, opts)
	require.True(t, ok)
	require.Equal(t, `/srv/data/cache/app.yml`, expanded)

	_, ok = ExpandEnvPath(`$DATA`, opts)
	require.False(t, ok, "bare value reference must stay dynamic")
	_, ok = ExpandEnvPath(`$DATAX/cache`, opts)
	require.False(t, ok, "unset variable must stay dynamic")
	_, ok = ExpandEnvPath(`$PATH/bin`, opts)
	require.False(t, ok, "a path-list value must never expand")
	_, ok = ExpandEnvPath(`$DATA\cache`, opts)
	require.False(t, ok, "POSIX must not treat a backslash as a path separator")
	_, ok = ExpandEnvPath(`$DATA/cache`, Options{Flavor: FlavorPowerShell, Env: opts.Env})
	require.False(t, ok, "PowerShell flavor must not expand POSIX references")
}

func TestEnvDirValue(t *testing.T) {
	opts := Options{Flavor: FlavorPowerShell, Env: map[string]string{
		"GOOD":   `C:\Users\Test`,
		"REL":    `Users\Test`,
		"LIST":   `C:\a;C:\b`,
		"MULTI":  "C:\\a\nC:\\b",
		"SLASHY": "/srv/data",
	}}

	value, ok := EnvDirValue("GOOD", opts)
	require.True(t, ok)
	require.Equal(t, `C:\Users\Test`, value)

	_, ok = EnvDirValue("REL", opts)
	require.False(t, ok, "relative values never expand")
	_, ok = EnvDirValue("LIST", opts)
	require.False(t, ok, "path-list values never expand")
	_, ok = EnvDirValue("MULTI", opts)
	require.False(t, ok, "multi-line values never expand")
	_, ok = EnvDirValue("UNSET", opts)
	require.False(t, ok)

	posix := Options{Flavor: FlavorPOSIX, Env: map[string]string{
		"GOOD": "/srv/data", "LISTY": "/a:/b",
	}}
	_, ok = EnvDirValue("GOOD", posix)
	require.True(t, ok)
	_, ok = EnvDirValue("LISTY", posix)
	require.False(t, ok, "POSIX path-list values never expand")
}

func TestEnvRefParts(t *testing.T) {
	name, pathShaped, ok := EnvRefParts(`$env:TEMP\notes.txt`, FlavorPowerShell)
	require.True(t, ok)
	require.Equal(t, "TEMP", name)
	require.True(t, pathShaped)

	name, pathShaped, ok = EnvRefParts(`$env:TEMP`, FlavorPowerShell)
	require.True(t, ok)
	require.Equal(t, "TEMP", name)
	require.False(t, pathShaped)

	name, pathShaped, ok = EnvRefParts(`${env:TEMP}\notes.txt`, FlavorPowerShell)
	require.True(t, ok)
	require.Equal(t, "TEMP", name)
	require.True(t, pathShaped)

	_, _, ok = EnvRefParts(`$PROFILE.CurrentUserCurrentHost`, FlavorPowerShell)
	require.False(t, ok, "engine-automatic variables are not env references")

	_, _, ok = EnvRefParts(`$env:USERPROFILE`, FlavorPOSIX)
	require.True(t, ok, "bash reads this as the variable named env followed by a literal tail")
	name, _, ok = EnvRefParts(`$env:USERPROFILE`, FlavorPOSIX)
	require.True(t, ok)
	require.Equal(t, "env", name)

	name, pathShaped, ok = EnvRefParts(`$DATA/cache`, FlavorPOSIX)
	require.True(t, ok)
	require.Equal(t, "DATA", name)
	require.True(t, pathShaped)

	name, pathShaped, ok = EnvRefParts(`$DATA`, FlavorPOSIX)
	require.True(t, ok)
	require.Equal(t, "DATA", name)
	require.False(t, pathShaped)

	name, _, ok = EnvRefParts(`${DATA}`, FlavorPOSIX)
	require.True(t, ok)
	require.Equal(t, "DATA", name)
}

func TestExpandEnvRefs(t *testing.T) {
	ps := Options{Flavor: FlavorPowerShell, Env: map[string]string{"UP": `C:\Users\Test`}}

	uses, ok := ExpandEnvRefs(`Get-Content "$env:UP\.emacs.d\init.el" -TotalCount 300`, "UP", ps)
	require.True(t, ok)
	require.Equal(t, 0, uses.Bare)
	require.Equal(t, []string{`C:\Users\Test\.emacs.d\init.el`}, uses.Paths)

	uses, ok = ExpandEnvRefs(`Get-ChildItem $env:UP`, "UP", ps)
	require.True(t, ok)
	require.Equal(t, 1, uses.Bare)
	require.Empty(t, uses.Paths)

	uses, ok = ExpandEnvRefs(`Get-ChildItem $env:UPX\dir`, "UP", ps)
	require.True(t, ok)
	require.Equal(t, 0, uses.Bare, "a longer variable name is not this variable")
	require.Empty(t, uses.Paths)

	_, ok = ExpandEnvRefs(`Get-Content $env:UP\x"suf"`, "UP", ps)
	require.False(t, ok, "a quote concatenating more path text is compound")

	uses, ok = ExpandEnvRefs(`Get-Content "$env:UP\x" -Tail 5`, "UP", ps)
	require.True(t, ok, "a closing quote before whitespace terminates safely")
	require.Equal(t, []string{`C:\Users\Test\x`}, uses.Paths)

	_, ok = ExpandEnvRefs(`Get-Content $env:UP\x$env:UP\y`, "UP", ps)
	require.False(t, ok, "adjacent expansions are compound")

	uses, ok = ExpandEnvRefs(`Write-Output '$env:UP\x'`, "UP", ps)
	require.True(t, ok)
	require.Equal(t, 0, uses.Bare, "single-quoted spans never interpolate")
	require.Empty(t, uses.Paths)

	uses, ok = ExpandEnvRefs("Write-Output `$env:UP\\x", "UP", ps)
	require.True(t, ok)
	require.Equal(t, 0, uses.Bare, "backtick-escaped references do not interpolate")
	require.Empty(t, uses.Paths)

	_, ok = ExpandEnvRefs(`Get-Content $env:UNSET\x`, "UNSET", ps)
	require.False(t, ok, "unset variables have no dir-like value")

	_, ok = ExpandEnvRefs(`${env:UP}suffix`, "UP", ps)
	require.False(t, ok, "braced concatenation without a separator is compound")

	posix := Options{Flavor: FlavorPOSIX, Env: map[string]string{"DATA": "/srv/data"}}
	uses, ok = ExpandEnvRefs(`cat "$DATA/config.yml"`, "DATA", posix)
	require.True(t, ok)
	require.Equal(t, 0, uses.Bare)
	require.Equal(t, []string{"/srv/data/config.yml"}, uses.Paths)

	uses, ok = ExpandEnvRefs(`ls $DATA`, "DATA", posix)
	require.True(t, ok)
	require.Equal(t, 1, uses.Bare)

	uses, ok = ExpandEnvRefs(`ls $DATABASE`, "DATA", posix)
	require.True(t, ok)
	require.Equal(t, 0, uses.Bare, "a longer variable name is not this variable")
	require.Empty(t, uses.Paths)

	_, ok = ExpandEnvRefs(`cat $DATA/x\suf`, "DATA", posix)
	require.False(t, ok, "a POSIX backslash continues the word in ways expansion cannot bound")

	_, ok = ExpandEnvRefs(`cat \$DATA/x`, "DATA", posix)
	require.True(t, ok, "an escaped reference is not an interpolation")
	require.Empty(t, uses.Paths)
	require.Equal(t, 0, uses.Bare)
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
