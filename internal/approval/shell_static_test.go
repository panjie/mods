package approval

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAssessShellStaticPOSIX(t *testing.T) {
	t.Run("read", func(t *testing.T) {
		got := AssessShellStaticWithContext("git status && git diff", true, ReadOnlyCommandPolicy{}, "")
		require.Equal(t, EffectRead, got.Effect)
		require.True(t, got.StaticRead)
		require.NotEmpty(t, got.Reason)
	})

	t.Run("write", func(t *testing.T) {
		got := AssessShellStaticWithContext("cat > /tmp/out <<'EOF'\nhello\nEOF", true, ReadOnlyCommandPolicy{}, "")
		require.Equal(t, EffectWrite, got.Effect)
		require.Contains(t, got.KnownDirs, "/tmp")
		require.Contains(t, got.Reason, "static analysis")
	})

	t.Run("recursive remove targets directory", func(t *testing.T) {
		got := AssessShellStaticWithContext("rm -rf ~/.ssh", true, ReadOnlyCommandPolicy{}, "")
		require.Equal(t, EffectWrite, got.Effect)
		require.Equal(t, []string{"~/.ssh"}, got.KnownDirs)
	})

	t.Run("unknown", func(t *testing.T) {
		got := AssessShellStaticWithContext("some unsupported writer", true, ReadOnlyCommandPolicy{}, "")
		require.Equal(t, EffectUnknown, got.Effect)
		require.Empty(t, got.KnownDirs)
		require.Equal(t, "effects could not be proven", got.Reason)
	})

	t.Run("env wrapped writer", func(t *testing.T) {
		got := AssessShellStaticWithContext("env touch owned.txt", true, ReadOnlyCommandPolicy{}, "")
		require.Equal(t, EffectWrite, got.Effect)
		require.Equal(t, []string{"."}, got.KnownDirs)
	})

	t.Run("git output flag", func(t *testing.T) {
		got := AssessShellStaticWithContext("git diff --output=owned.txt", true, ReadOnlyCommandPolicy{}, "")
		require.Equal(t, EffectWrite, got.Effect)
		require.Equal(t, []string{"."}, got.KnownDirs)
	})

	t.Run("git external diff helper", func(t *testing.T) {
		got := AssessShellStaticWithContext("git diff --ext-diff", true, ReadOnlyCommandPolicy{}, "")
		require.Equal(t, EffectWrite, got.Effect)
	})

	t.Run("xxd reverse output", func(t *testing.T) {
		got := AssessShellStaticWithContext("xxd -r input.hex output.bin", true, ReadOnlyCommandPolicy{}, "")
		require.Equal(t, EffectWrite, got.Effect)
		require.Equal(t, []string{"."}, got.KnownDirs)
	})

	t.Run("runtime-expanded read stays read-only", func(t *testing.T) {
		got := AssessShellStaticWithContext("cat ${FILE}", true, ReadOnlyCommandPolicy{}, "")
		require.Equal(t, EffectRead, got.Effect)
		require.Contains(t, got.DynamicTargets, "$FILE")
	})

	t.Run("oldest downloads pipeline is read-only", func(t *testing.T) {
		got := AssessShellStaticWithContext(`find "$HOME/Downloads" -type f -print0 | xargs -0 stat -f '%m %N' | sort -n | head -1`, true, ReadOnlyCommandPolicy{}, "")
		require.Equal(t, EffectRead, got.Effect)
		require.True(t, got.StaticRead)
	})

	t.Run("home-expanded write target remains deterministic", func(t *testing.T) {
		got := AssessShellStaticWithContext(`rm "$HOME/Downloads/old.txt"`, true, ReadOnlyCommandPolicy{}, "")
		require.Equal(t, EffectWrite, got.Effect)
		require.NotEmpty(t, got.KnownDirs)
	})

	t.Run("find delete is a write", func(t *testing.T) {
		got := AssessShellStaticWithContext(`find "$HOME/Downloads" -type f -delete`, true, ReadOnlyCommandPolicy{}, "")
		require.Equal(t, EffectWrite, got.Effect)
	})

	t.Run("sort output is a write", func(t *testing.T) {
		got := AssessShellStaticWithContext("sort -o /tmp/output input", true, ReadOnlyCommandPolicy{}, "")
		require.Equal(t, EffectWrite, got.Effect)
		require.Equal(t, []string{"/tmp"}, got.KnownDirs)
	})

	t.Run("xargs writer is not read-only", func(t *testing.T) {
		got := AssessShellStaticWithContext(`find . -print0 | xargs -0 touch`, true, ReadOnlyCommandPolicy{}, "")
		require.Equal(t, EffectWrite, got.Effect)
	})

	t.Run("dynamic target stays unresolved", func(t *testing.T) {
		got := AssessShellStaticWithContext(`rm "$TARGET"`, true, ReadOnlyCommandPolicy{}, "")
		require.Equal(t, EffectWrite, got.Effect)
		require.Contains(t, got.DynamicTargets, "$TARGET")
		require.Empty(t, got.KnownDirs)
	})
}

func TestAssessShellStaticPowerShell(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell AST classifier requires Windows")
	}
	t.Run("single-quoted write path with spaces", func(t *testing.T) {
		got := AssessShellStaticWithContext(`Set-Content 'C:\Program Files\App\notes.txt' 'hello'`, false, ReadOnlyCommandPolicy{}, "")
		require.Equal(t, EffectWrite, got.Effect)
		require.Contains(t, got.KnownDirs, `C:\Program Files\App`)
		require.NotContains(t, got.KnownDirs, `'C:`)
		require.NotContains(t, got.KnownDirs, `Files\App`)
	})

	t.Run("single-quoted write path with escaped single quote", func(t *testing.T) {
		got := AssessShellStaticWithContext(`Set-Content 'C:\O''Reilly\App\notes.txt' 'hello'`, false, ReadOnlyCommandPolicy{}, "")
		require.Equal(t, EffectWrite, got.Effect)
		require.Contains(t, got.KnownDirs, `C:\O'Reilly\App`)
		require.NotContains(t, got.KnownDirs, `C:\`)
		require.NotContains(t, got.KnownDirs, `C:\O`)
		require.NotContains(t, got.KnownDirs, `Reilly\App`)
	})
}

func TestUnresolvedShellPathExpression(t *testing.T) {
	for _, value := range []string{`$PROFILE.CurrentUserCurrentHost`, `$prof`, `$(Join-Path $HOME x)`, `@args`, `%TEMP%\notes.txt`, `(Get-Location).Path`} {
		require.True(t, IsUnresolvedShellPathExpression(value, false), value)
	}
	for _, value := range []string{`$HOME\Downloads\x`, `$env:USERPROFILE\Downloads\x`, `C:\Users\Test\x`, `relative\x`, `%USERPROFILE%\Downloads\x`, `%s\`, `(progn (message \`, `%s\" (emacs-init-time)) (kill-emacs))"`} {
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

func TestPowerShellWritableTargetAnalysisSeparatesProviderTargets(t *testing.T) {
	tests := []struct {
		name   string
		target string
	}{
		{name: "registry drive", target: `HKCU:\Software\Classes\Neovide`},
		{name: "registry drive-relative", target: `HKCU:Software\Classes\Neovide`},
		{name: "registry provider", target: `Registry::HKEY_CURRENT_USER\Software\Classes\Neovide`},
		{name: "module-qualified registry provider", target: `Microsoft.PowerShell.Core\Registry::HKEY_CURRENT_USER\Software\Classes\Neovide`},
		{name: "certificate drive", target: `Cert:\CurrentUser\My`},
		{name: "unknown named drive", target: `Work:\project`},
		{name: "unknown drive-relative", target: `Work:project`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := analyzeWritableTargetsFromTokens([]string{"Set-ItemProperty", "-Path", tc.target, "-Name", "x", "-Value", "y"}, false)
			require.True(t, got.Known)
			require.Empty(t, got.Dirs, "a PowerShell provider target must not become a filesystem directory")
			require.Empty(t, got.Unresolved)
			require.Equal(t, []string{tc.target}, got.ProviderTargets)
		})
	}

	for _, target := range []string{`C:\Users\Test\out.txt`, `C:\Users\Test\out.txt:stream`, `\\server\share\out.txt`} {
		got := analyzeWritableTargetsFromTokens([]string{"Set-Content", "-Path", target, "-Value", "x"}, false)
		require.NotEmpty(t, got.Dirs, target)
		require.Empty(t, got.ProviderTargets, target)
	}

	filesystemProvider := analyzeWritableTargetsFromTokens([]string{"Set-Content", "-Path", `FileSystem::C:\Users\Test\out.txt`, "-Value", "x"}, false)
	require.Equal(t, []string{`C:\Users\Test`}, filesystemProvider.Dirs)
	require.Empty(t, filesystemProvider.ProviderTargets)
}

func TestPowerShellProviderPathRecognizerFailsClosedForAmbiguousDrives(t *testing.T) {
	for _, target := range []string{
		`HKCU:\Software\Classes\Neovide`,
		`HKCU:Software\Classes\Neovide`,
		"HK`CU:\\Software\\Classes\\Neovide",
		`Registry::HKEY_CURRENT_USER\Software\Classes\Neovide`,
		`Microsoft.PowerShell.Core\Registry::HKEY_CURRENT_USER\Software`,
		`Work:\project`,
		`Work:project`,
		`Work://project`,
	} {
		require.True(t, IsPowerShellProviderPath(target), target)
	}
	for _, target := range []string{`C:\Users\Test\out.txt`, `C:\Users\Test\out.txt:stream`, `\\server\share\out.txt`} {
		require.False(t, IsPowerShellProviderPath(target), target)
	}
	path, ok := PowerShellFilesystemProviderPath(`FileSystem::C:\Users\Test\out.txt`)
	require.True(t, ok)
	require.Equal(t, `C:\Users\Test\out.txt`, path)
}

func TestPowerShellWritableTargetAnalysisRecognizesPathAndCommandAliases(t *testing.T) {
	for _, parameter := range []string{"-PSPath", "-PS", "-LP", "-LiteralP"} {
		got := analyzeWritableTargetsFromTokens([]string{"Set-ItemProperty", parameter, `HKCU:\Software\Classes\Neovide`, "-Name", "x", "-Value", "y"}, false)
		require.Equal(t, []string{`HKCU:\Software\Classes\Neovide`}, got.ProviderTargets, parameter)
		require.Empty(t, got.Dirs, parameter)
	}

	for alias, canonical := range map[string]string{
		"ni":  "New-Item",
		"md":  "New-Item",
		"si":  "Set-Item",
		"sp":  "Set-ItemProperty",
		"sc":  "Set-Content",
		"ri":  "Remove-Item",
		"rp":  "Remove-ItemProperty",
		"rni": "Rename-Item",
		"cli": "Clear-Item",
		"clp": "Clear-ItemProperty",
		"ac":  "Add-Content",
		"clc": "Clear-Content",
		"cpi": "Copy-Item",
		"cpp": "Copy-ItemProperty",
		"mi":  "Move-Item",
		"mp":  "Move-ItemProperty",
	} {
		t.Run(alias+" aliases "+canonical, func(t *testing.T) {
			got := analyzeWritableTargetsFromTokens([]string{alias, "-Path", `HKCU:\Software\Classes\Neovide`}, false)
			require.True(t, got.Known)
			require.Equal(t, []string{`HKCU:\Software\Classes\Neovide`}, got.ProviderTargets)
		})
	}

	for _, command := range []string{"Copy-ItemProperty", "Move-ItemProperty"} {
		got := analyzeWritableTargetsFromTokens([]string{command, "-Path", `HKCU:\Software\Source`, "-Name", "x", `-Dest:HKCU:\Software\Destination`}, false)
		require.Equal(t, []string{`HKCU:\Software\Destination`}, got.ProviderTargets, command)
	}

	acl := analyzeWritableTargetsFromTokens([]string{"Set-Acl", "-Path", `HKCU:\Software\Classes\Neovide`, "-AclObject", "$acl"}, false)
	require.True(t, acl.Known)
	require.Equal(t, []string{`HKCU:\Software\Classes\Neovide`}, acl.ProviderTargets)
}

func TestAssessPowerShellIRPathContextMutationMakesWriteTargetNonReusable(t *testing.T) {
	tests := []struct {
		name        string
		invocations []psCommandInvocation
	}{
		{
			name: "drive cmdlets",
			invocations: []psCommandInvocation{
				{Name: "remove-psdrive", Args: []string{"C"}},
				{Name: "new-psdrive", Args: []string{"-Name", "C", "-PSProvider", "Registry", "-Root", `HKCU:\Software`}},
				{Name: "set-content", Args: []string{"-Path", `C:\Users\marker.txt`, "-Value", "x"}},
			},
		},
		{
			name: "drive aliases",
			invocations: []psCommandInvocation{
				{Name: "rdr", Args: []string{"C"}},
				{Name: "ndr", Args: []string{"C", "Registry", `HKCU:\Software`}},
				{Name: "set-content", Args: []string{"-Path", `C:\Users\marker.txt`, "-Value", "x"}},
			},
		},
		{
			name: "provider location",
			invocations: []psCommandInvocation{
				{Name: "set-location", Args: []string{"Env:"}},
				{Name: "set-item", Args: []string{"-Path", `.\MODS_REVIEW_PROBE`, "-Value", "x"}},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assessment := assessPowerShellIR("", &psBridgeIR{
				Invocations:            tc.invocations,
				TopLevelStatementCount: len(tc.invocations),
				PipelineCount:          len(tc.invocations),
			}, ReadOnlyCommandPolicy{}, `C:\Users`)

			require.Equal(t, EffectWrite, assessment.Effect)
			require.Empty(t, assessment.KnownDirs)
			require.Equal(t, []string{"PowerShell path context"}, assessment.DynamicTargets)
		})
	}
}

func TestPowerShellWritableTargetsIgnoreCommonParameterValues(t *testing.T) {
	space := analyzeWritableTargetsFromTokens([]string{"Remove-Item", `C:\ws\a.wav`, "-ErrorAction", "SilentlyContinue"}, false)
	require.True(t, space.Known)
	require.Equal(t, []string{`C:\ws`}, space.Dirs, "an action-preference value is not a path operand")

	leading := analyzeWritableTargetsFromTokens([]string{"Set-Content", "-ErrorAction", "SilentlyContinue", `C:\ws\a.log`}, false)
	require.Equal(t, []string{`C:\ws`}, leading.Dirs, "a common parameter before the path must not hide the real target")

	colon := analyzeWritableTargetsFromTokens([]string{"Remove-Item", `C:\ws\a.wav`, "-ErrorAction:SilentlyContinue"}, false)
	require.Equal(t, []string{`C:\ws`}, colon.Dirs, "inline -ErrorAction:value carries its value inline")

	switchParam := analyzeWritableTargetsFromTokens([]string{"Remove-Item", "-Verbose", `C:\ws\a.wav`}, false)
	require.Equal(t, []string{`C:\ws`}, switchParam.Dirs, "switch parameters consume no value")

	posix := analyzeWritableTargetsFromTokens([]string{"rm", "-f", "a.txt"}, true)
	require.Equal(t, []string{"."}, posix.Dirs, "POSIX operand semantics stay unchanged")
}

func TestAssessArgvStatic(t *testing.T) {
	policy := ReadOnlyCommandPolicy{}
	for _, tc := range []struct {
		name    string
		program string
		args    []string
		class   CommandEffect
	}{
		{name: "read only", program: "git", args: []string{"status"}, class: EffectRead},
		{name: "literal shell syntax", program: "ls", args: []string{"; rm -rf out"}, class: EffectRead},
		{name: "known write", program: "rm", args: []string{"out.txt"}, class: EffectWrite},
		{name: "literal variable-looking write path", program: "rm", args: []string{"$HOME"}, class: EffectWrite},
		{name: "executable path not trusted", program: "./git", args: []string{"status"}, class: EffectUnknown},
		{name: "unknown command", program: "custom-tool", class: EffectUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := AssessArgvStaticWithContext(tc.program, tc.args, true, policy, ArgvStaticContext{})
			require.Equal(t, tc.class, got.Effect)
		})
	}
	require.Equal(t, EffectRead, AssessArgvStaticWithContext("Git.EXE", []string{"status"}, false, policy, ArgvStaticContext{}).Effect)
	require.Equal(t, []string{"."}, AssessArgvStaticWithContext("rm", []string{"$HOME"}, true, policy, ArgvStaticContext{}).KnownDirs)
}

func TestAssessShellStaticTargetDirectoryOptions(t *testing.T) {
	for _, command := range []string{
		`cp -t /outside src.txt`,
		`cp --target-directory=/outside src.txt`,
		`mv -t/outside src.txt`,
	} {
		got := AssessShellStaticWithContext(command, true, ReadOnlyCommandPolicy{}, "")
		require.Equal(t, EffectWrite, got.Effect, command)
		require.Equal(t, []string{"/outside"}, got.KnownDirs, command)
	}
}

func TestPowerShellAutomaticConstantArgumentsAreNotDynamicTargets(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "boolean constant", args: []string{"System.Text.UTF8Encoding", "($false)"}},
		{name: "bare constant", args: []string{"$true"}},
		{name: "null constant", args: []string{"($null)"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, unresolved, _, _ := analyzePowerShellWritablePathsIR(&psBridgeIR{
				Invocations: []psCommandInvocation{{Name: "new-object", Args: tc.args}},
			}, ReadOnlyCommandPolicy{}, "")
			require.Empty(t, unresolved, "automatic constants never reach the filesystem")
		})
	}

	_, unresolved, _, _ := analyzePowerShellWritablePathsIR(&psBridgeIR{
		Invocations: []psCommandInvocation{{Name: "new-object", Args: []string{"$p"}}},
	}, ReadOnlyCommandPolicy{}, "")
	require.Equal(t, []string{"$p"}, unresolved, "a genuine variable argument still surfaces as a runtime target")
}
