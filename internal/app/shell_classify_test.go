package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/pathutil"
	"github.com/stretchr/testify/require"
)

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

	t.Run("single-quoted absolute external path", func(t *testing.T) {
		p := filepath.Join(ext, "secret")
		got := extractExternalPaths("cat '"+p+"'", ws)
		require.Equal(t, []string{p}, got)
	})

	t.Run("single-quoted parent traversal", func(t *testing.T) {
		got := extractExternalPaths("cat '../sibling/secret'", ws)
		require.Equal(t, []string{filepath.Clean(filepath.Join(ws, "..", "sibling", "secret"))}, got)
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

func TestExtractExternalPathsWindowsFlavor(t *testing.T) {
	ws := filepath.Clean(t.TempDir())

	t.Run("compiler flags with colon are not paths", func(t *testing.T) {
		got := extractExternalPathsWithFlavor(
			`csc /out:C:\out\program.exe /reference:System.Drawing.dll /reference:System.Windows.Forms.dll source.cs`,
			ws, pathutil.FlavorPowerShell,
		)
		for _, p := range got {
			require.NotContains(t, p, "/out:", "compiler flag /out: should not be extracted")
			require.NotContains(t, p, "/reference:", "compiler flag /reference: should not be extracted")
		}
	})

	t.Run("compiler flags without colon are not paths", func(t *testing.T) {
		got := extractExternalPathsWithFlavor(
			`csc /out /target /reference /optimize src.cs`,
			ws, pathutil.FlavorPowerShell,
		)
		require.NotContains(t, got, "/out", "single-segment flag /out should not be extracted")
		require.NotContains(t, got, "/target", "single-segment flag /target should not be extracted")
		require.NotContains(t, got, "/reference", "single-segment flag /reference should not be extracted")
		require.NotContains(t, got, "/optimize", "single-segment flag /optimize should not be extracted")
	})

	t.Run("cmd flags are not paths", func(t *testing.T) {
		got := extractExternalPathsWithFlavor(`cmd /c dir`, ws, pathutil.FlavorPowerShell)
		require.NotContains(t, got, "/c", "cmd flag /c should not be extracted")
	})

	t.Run("compiler flags do not hide real paths", func(t *testing.T) {
		got := extractExternalPathsWithFlavor(
			`csc /out:C:\out\program.exe /reference:System.Drawing.dll C:\src\app.cs`,
			ws, pathutil.FlavorPowerShell,
		)
		require.Contains(t, got, `C:\src\app.cs`, "real Windows path should still be detected")
		for _, p := range got {
			require.NotContains(t, p, "/out:")
			require.NotContains(t, p, "/reference:")
		}
	})

	t.Run("double-quoted path with spaces preserves full Windows path", func(t *testing.T) {
		got := extractExternalPathsWithFlavor(
			`Get-Content "C:\Program Files\App\notes.txt"`,
			ws, pathutil.FlavorPowerShell,
		)
		require.Equal(t, []string{`C:\Program Files\App\notes.txt`}, got)
		require.NotContains(t, got, `C:\Program`)
	})

	t.Run("single-quoted path with spaces preserves full Windows path", func(t *testing.T) {
		got := extractExternalPathsWithFlavor(
			`Get-Content 'C:\Program Files\App\notes.txt'`,
			ws, pathutil.FlavorPowerShell,
		)
		require.Equal(t, []string{`C:\Program Files\App\notes.txt`}, got)
		require.NotContains(t, got, `C:\Program`)
	})

	t.Run("single-quoted path with escaped single quote preserves full Windows path", func(t *testing.T) {
		got := extractExternalPathsWithFlavor(
			`Get-Content 'C:\O''Reilly\App\notes.txt'`,
			ws, pathutil.FlavorPowerShell,
		)
		require.Equal(t, []string{`C:\O'Reilly\App\notes.txt`}, got)
		require.NotContains(t, got, `C:\O`)
		require.NotContains(t, got, `Reilly\App\notes.txt`)
	})

	t.Run("quoted dot-prefixed parent traversal resolves external", func(t *testing.T) {
		wantParent := filepath.Clean(filepath.Join(ws, ".."))
		want := filepath.Clean(filepath.Join(ws, "..", "outside.txt"))

		require.Equal(t, []string{wantParent}, extractExternalPathsWithFlavor(
			`Get-ChildItem '.\..'`,
			ws, pathutil.FlavorPowerShell,
		))
		require.Equal(t, []string{wantParent}, extractExternalPathsWithFlavor(
			`Get-ChildItem './..'`,
			ws, pathutil.FlavorPowerShell,
		))
		require.Equal(t, []string{want}, extractExternalPathsWithFlavor(
			`Get-Content '.\..\outside.txt'`,
			ws, pathutil.FlavorPowerShell,
		))
		require.Equal(t, []string{want}, extractExternalPathsWithFlavor(
			`Get-Content "./../outside.txt"`,
			ws, pathutil.FlavorPowerShell,
		))
		require.Equal(t, []string{want}, extractExternalPathsWithFlavor(
			`Get-Content '.\../outside.txt'`,
			ws, pathutil.FlavorPowerShell,
		))
		require.Equal(t, []string{want}, extractExternalPathsWithFlavor(
			`Get-Content './..\outside.txt'`,
			ws, pathutil.FlavorPowerShell,
		))
	})

	t.Run("Unix-style absolute paths are ignored", func(t *testing.T) {
		require.Empty(t,
			extractExternalPathsWithFlavor("cat /etc/passwd", ws, pathutil.FlavorPowerShell))
		require.Empty(t,
			extractExternalPathsWithFlavor("ls /usr/local/bin", ws, pathutil.FlavorPowerShell))
	})

	t.Run("quoted Unix-style absolute paths are ignored", func(t *testing.T) {
		require.Empty(t,
			extractExternalPathsWithFlavor(`Get-Content "/etc/passwd"`, ws, pathutil.FlavorPowerShell))
		require.Empty(t,
			extractExternalPathsWithFlavor(`Get-Content '/etc/passwd'`, ws, pathutil.FlavorPowerShell))
	})

	t.Run("Unix-style bare root is ignored", func(t *testing.T) {
		require.Empty(t,
			extractExternalPathsWithFlavor("find / -delete", ws, pathutil.FlavorPowerShell))
		require.Empty(t,
			extractExternalPathsWithFlavor("rm -rf /", ws, pathutil.FlavorPowerShell))
	})

	t.Run("compound: flags and real paths mixed", func(t *testing.T) {
		got := extractExternalPathsWithFlavor(
			`csc /out:$out /target:winexe /reference:System.dll src.cs && ls /etc/passwd`,
			ws, pathutil.FlavorPowerShell,
		)
		require.NotContains(t, got, "/etc/passwd", "Unix-style path should not be extracted in PowerShell mode")
		for _, p := range got {
			require.False(t, strings.HasPrefix(p, "/out"), "compiler flag should not appear: %s", p)
			require.False(t, strings.HasPrefix(p, "/target"), "compiler flag should not appear: %s", p)
			require.False(t, strings.HasPrefix(p, "/reference"), "compiler flag should not appear: %s", p)
		}
	})

	t.Run("division slash is not a path", func(t *testing.T) {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		require.NotEmpty(t, home)

		got := extractExternalPathsWithFlavor(
			`$dl = "$env:USERPROFILE\Downloads"; Get-ChildItem -Path $dl -File -Recurse -ErrorAction SilentlyContinue | Where-Object { $_.Length -gt 50MB } | ForEach-Object { $sizeInMB = [math]::Round($_.Length / 1MB, 2); "$($_.FullName)  ($sizeInMB MB)" }`,
			ws, pathutil.FlavorPowerShell,
		)
		require.Equal(t, []string{filepath.Join(home, "Downloads")}, got)
		require.NotContains(t, got, "/")
	})

	t.Run("UNC paths are detected", func(t *testing.T) {
		got := extractExternalPathsWithFlavor(
			`Get-Content \\server\share\notes.txt`,
			ws, pathutil.FlavorPowerShell,
		)
		require.Equal(t, []string{`\\server\share\notes.txt`}, got)
	})

	t.Run("quoted regex beginning with escaped separator is not a UNC path", func(t *testing.T) {
		got := extractExternalPathsWithFlavor(
			`Get-ChildItem -Recurse -Include *.go | Where-Object { $_.FullName -notmatch '\\(vendor|\.git|bin)\\' } | Get-Content | Measure-Object -Line | Select-Object -ExpandProperty Lines`,
			ws, pathutil.FlavorPowerShell,
		)
		require.Empty(t, got)
	})

	t.Run("quoted UNC path with spaces is detected", func(t *testing.T) {
		got := extractExternalPathsWithFlavor(
			`Get-Content '\\server\share name\notes.txt'`,
			ws, pathutil.FlavorPowerShell,
		)
		require.Equal(t, []string{`\\server\share name\notes.txt`}, got)
	})

	t.Run("semicolon-delimited paths are not concatenated", func(t *testing.T) {
		got := extractExternalPathsWithFlavor("cmd /c set PATH=C:\\bin;C:\\tools", ws, pathutil.FlavorPowerShell)
		for _, p := range got {
			require.False(t, strings.Contains(p, ";"), "semicolon-delimited paths should not be concatenated: %s", p)
		}
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

func TestFilterArgPathsPowerShellPreservesSpacedWindowsPath(t *testing.T) {
	ws := filepath.Clean(t.TempDir())

	got := filterArgPaths([]string{`C:\Program Files\App\notes.txt`}, ws, pathutil.FlavorPowerShell)
	require.Equal(t, []string{`C:\Program Files\App\notes.txt`}, got)
	require.NotContains(t, got, `C:\Program`)
}

func TestFilterArgPathsPowerShellIgnoresUnixStyleArgs(t *testing.T) {
	got := filterArgPaths([]string{"/out", "/reference", "/etc/passwd"}, "", pathutil.FlavorPowerShell)
	require.Empty(t, got)
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

func TestDefaultShellCommandAnalysisIsUnknown(t *testing.T) {
	got := approval.UnknownCommandAssessment()
	require.Equal(t, approval.EffectUnknown, got.Effect)
}

func TestShellStaticAnalysisSetsEffect(t *testing.T) {
	m := &Mods{Config: testConfigForWorkspace(t.TempDir())}

	read := m.assessCommand("shell_run", "git status")
	require.Equal(t, approval.EffectRead, read.Effect)

	writeCommand := "cat > out.txt <<'EOF'\nhello\nEOF"
	if runtime.GOOS == "windows" {
		writeCommand = `Set-Content out.txt hello`
	}
	write := m.assessCommand("shell_run", writeCommand)
	require.Equal(t, approval.EffectWrite, write.Effect)
}

func TestShellAccessMode(t *testing.T) {
	require.Equal(t, AccessRead, approval.CommandAssessment{Effect: approval.EffectRead}.AccessIntent().Class)
	require.Equal(t, AccessWrite, approval.CommandAssessment{Effect: approval.EffectWrite}.AccessIntent().Class)
	require.Equal(t, AccessWrite, approval.CommandAssessment{Effect: approval.EffectUnknown}.AccessIntent().Class)
}

func TestFinalizeShellAnalysisPreservesDynamicReadEffect(t *testing.T) {
	got := finalizeCommandAssessment(
		approval.CommandAssessment{
			Effect:         approval.EffectRead,
			Reason:         "inspects runtime path",
			DynamicTargets: []string{"$target"},
		},
		"/workspace",
		pathutil.FlavorPOSIX,
	)

	require.Equal(t, approval.EffectRead, got.Effect)
	require.Equal(t, AccessRead, got.AccessIntent().Class)
	require.Equal(t, []string{"$target"}, got.DynamicTargets)
}

func TestParseShellAnalysisResponseCanReturnUnknownEffect(t *testing.T) {
	analysis, ok := parseShellAssessmentResponse(`{"needs_review":true,"affected_dirs":[],"reason":"not sure","effect":"unknown"}`)
	require.True(t, ok)
	require.Equal(t, approval.EffectUnknown, analysis.Effect)
}

func TestParseShellAssessmentResponseAcceptsNewSchemaAndRejectsDynamicDirs(t *testing.T) {
	assessment, ok := parseShellAssessmentResponse(`{"effect":"read","affected_dirs":["/etc","$PROFILE.CurrentUserCurrentHost","<workspace>","unknown"],"reason":"inspection"}`)
	require.True(t, ok)
	require.Equal(t, approval.EffectRead, assessment.Effect)
	require.Equal(t, []string{"/etc"}, assessment.KnownDirs)
}

func TestMergeCommandAssessmentOnlyCompletesUnknownEffect(t *testing.T) {
	static := approval.CommandAssessment{
		Effect:         approval.EffectUnknown,
		DynamicTargets: []string{"$PROFILE"},
		Shape:          approval.CommandShape{TopLevelActions: 2, Pipelines: 2},
		Reviewability: approval.CommandReviewability{
			Level:         approval.ReviewabilityCompound,
			ShouldCorrect: true,
		},
	}
	completion := approval.CommandAssessment{
		Effect:         approval.EffectRead,
		KnownDirs:      []string{"/etc"},
		DynamicTargets: []string{"$ignored"},
		Shape:          approval.CommandShape{TopLevelActions: 99, Opaque: true},
		Reviewability:  approval.CommandReviewability{Level: approval.ReviewabilitySimple},
	}
	got := mergeCommandAssessment(static, completion)
	require.Equal(t, approval.EffectRead, got.Effect)
	require.Equal(t, []string{"/etc"}, got.KnownDirs)
	require.Equal(t, []string{"$PROFILE"}, got.DynamicTargets)
	require.Equal(t, static.Shape, got.Shape)
	require.Equal(t, static.Reviewability, got.Reviewability)

	known := static
	known.Effect = approval.EffectWrite
	require.Equal(t, known, mergeCommandAssessment(known, completion))

	uncertain := approval.CommandAssessment{Effect: approval.EffectUnknown, KnownDirs: []string{"/tmp"}, Reason: "classifier timeout"}
	require.Equal(t, static, mergeCommandAssessment(static, uncertain))
}

func TestParseLegacyShellEffectRejectsAmbiguousAnswers(t *testing.T) {
	effect, ok := parseLegacyShellEffect("YES")
	require.True(t, ok)
	require.Equal(t, approval.EffectWrite, effect)
	effect, ok = parseLegacyShellEffect("NO")
	require.True(t, ok)
	require.Equal(t, approval.EffectRead, effect)
	_, ok = parseLegacyShellEffect("YES or NO")
	require.False(t, ok)
}

func TestAnalyzeShellCommandDoesNotConcreteDynamicClassifierPaths(t *testing.T) {
	workspace := canonicalTestPath(t, t.TempDir())
	m := &Mods{
		Config: testConfigForWorkspace(workspace),
		shellAnalyzer: func(tool, command string) approval.CommandAssessment {
			require.Equal(t, "powershell_run", tool)
			return approval.CommandAssessment{
				KnownDirs: []string{`$PROFILE.CurrentUserCurrentHost`},
				Reason:    "writes PowerShell profile",
				Effect:    approval.EffectWrite,
			}
		},
	}

	got := m.assessCommand("powershell_run", `$prof = $PROFILE.CurrentUserCurrentHost; custom-writer -Path $prof`)
	require.Equal(t, approval.EffectWrite, got.Effect)
	require.Empty(t, got.KnownDirs)
	require.Contains(t, got.DynamicTargets, `$PROFILE.CurrentUserCurrentHost`)
	for _, dir := range got.KnownDirs {
		require.NotContains(t, dir, `$PROFILE`)
	}
}

func TestPartitionShellAnalysisPathsSeparatesRuntimeExpressions(t *testing.T) {
	concrete, unresolved := partitionShellAnalysisPaths(
		[]string{`C:\Users\Test\Documents`, `$PROFILE.CurrentUserCurrentHost`, `$target`},
		nil,
		pathutil.FlavorPowerShell,
	)
	require.Equal(t, []string{`C:\Users\Test\Documents`}, concrete)
	require.ElementsMatch(t, []string{`$PROFILE.CurrentUserCurrentHost`, `$target`}, unresolved)
}

func TestNormalizeLiteralProcessPathDoesNotExpandShellSyntax(t *testing.T) {
	workspace := canonicalTestPath(t, t.TempDir())
	require.Equal(t, filepath.Join(workspace, "$HOME", "out.txt"), normalizeLiteralProcessPath("$HOME/out.txt", workspace, pathutil.FlavorPOSIX))
	require.Equal(t, filepath.Join(workspace, "~", "out.txt"), normalizeLiteralProcessPath("~/out.txt", workspace, pathutil.FlavorPOSIX))
}

func TestAnalyzeShellCommandASTReadOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell_run uses PowerShell on Windows; POSIX AST coverage applies to non-Windows shell_run")
	}
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
				shellAnalyzer: func(tool, command string) approval.CommandAssessment {
					t.Fatalf("LLM classifier should not be called for %q", command)
					return approval.UnknownCommandAssessment()
				},
			}
			t.Cleanup(func() { mods.shellAnalyzer = nil })
			result := mods.assessCommand(c.tool, c.cmd)
			require.NotEmptyf(t, result.Reason, "cmd=%q should have a reason", c.cmd)
		})
	}
}

func TestAnalyzeShellCommandComplexPOSIXReadOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX AST coverage applies to non-Windows shell_run")
	}

	cases := []struct {
		name string
		cmd  string
	}{
		{
			name: "nested groups with conditional operators",
			cmd:  "(git status && git diff) || (git log --oneline -5 | head -2)",
		},
		{
			name: "read-only command substitution in pipeline",
			cmd:  `printf '%s\n' "$(git rev-parse --show-toplevel)" | head -1`,
		},
		{
			name: "heredoc contents are data not commands",
			cmd:  "cat <<'EOF' | grep rm\nrm -rf /tmp/not-executed\nEOF",
		},
		{
			name: "null-delimited multi-stage pipeline",
			cmd:  `find . -type f -name '*.txt' -print0 | xargs -0 wc -l | sort -n`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mods := &Mods{
				Config: testConfigForWorkspace(t.TempDir()),
				shellAnalyzer: func(tool, command string) approval.CommandAssessment {
					t.Fatalf("LLM classifier should not be called for %q", command)
					return approval.UnknownCommandAssessment()
				},
			}
			t.Cleanup(func() { mods.shellAnalyzer = nil })

			result := mods.assessCommand("shell_run", c.cmd)
			require.Equal(t, approval.EffectRead, result.Effect)
			require.NotEmpty(t, result.Reason)
		})
	}
}

func TestAnalyzeShellCommandComplexPOSIXWrites(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX AST coverage applies to non-Windows shell_run")
	}

	externalDir := canonicalTestPath(t, t.TempDir())
	cases := []struct {
		name string
		cmd  string
	}{
		{
			name: "writer nested in conditional subshell",
			cmd:  "(git status && touch " + filepath.Join(externalDir, "marker") + ") || rm -f " + filepath.Join(externalDir, "stale"),
		},
		{
			name: "writer hidden in command substitution",
			cmd:  `printf '%s\n' "$(touch ` + filepath.Join(externalDir, "nested") + `)"`,
		},
		{
			name: "read pipeline ending in tee",
			cmd:  "cat README.md | tee " + filepath.Join(externalDir, "report.txt") + " >/dev/null",
		},
		{
			name: "short-circuited writer remains conservative",
			cmd:  "false && rm -rf " + filepath.Join(externalDir, "cache"),
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mods := &Mods{
				Config: testConfigForWorkspace(t.TempDir()),
				shellAnalyzer: func(tool, command string) approval.CommandAssessment {
					t.Fatalf("LLM classifier should not be called for %q", command)
					return approval.UnknownCommandAssessment()
				},
			}
			t.Cleanup(func() { mods.shellAnalyzer = nil })

			result := mods.assessCommand("shell_run", c.cmd)
			require.Equal(t, approval.EffectWrite, result.Effect)
			require.Truef(t, hasPathUnder(result.KnownDirs, externalDir),
				"cmd=%q should affect %s; got %v", c.cmd, externalDir, result.KnownDirs)
			require.Contains(t, result.Reason, "static analysis")
		})
	}
}

func TestAnalyzeShellCommandOldestDownloadsPipelineIsExternalRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX pipeline coverage applies to non-Windows shell_run")
	}
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	workspace := canonicalTestPath(t, t.TempDir())
	mods := &Mods{
		Config: testConfigForWorkspace(workspace),
		shellAnalyzer: func(tool, command string) approval.CommandAssessment {
			t.Fatalf("LLM classifier should not be called for %q", command)
			return approval.UnknownCommandAssessment()
		},
	}
	t.Cleanup(func() { mods.shellAnalyzer = nil })

	cmd := `find "$HOME/Downloads" -type f -print0 | xargs -0 stat -f '%m %N' | sort -n | head -1`
	result := mods.assessCommand("shell_run", cmd)

	require.Equal(t, approval.EffectRead, result.Effect)
	require.Equal(t, []string{filepath.Join(home, "Downloads")}, result.KnownDirs)
}

func TestAnalyzeShellCommandASTExternalPath(t *testing.T) {
	// Read-only command with external path: AST classifier says read-only,
	// extractExternalPaths provides KnownDirs, no LLM call needed.
	mods := &Mods{
		shellAnalyzer: func(tool, command string) approval.CommandAssessment {
			t.Fatalf("LLM classifier should not be called for %q", command)
			return approval.UnknownCommandAssessment()
		},
	}
	t.Cleanup(func() { mods.shellAnalyzer = nil })
	result := mods.assessCommand("shell_run", "cat /etc/passwd")
	if runtime.GOOS == "windows" {
		require.NotContains(t, result.KnownDirs, "/etc/passwd")
		return
	}
	require.Contains(t, result.KnownDirs, "/etc/passwd")
}

func TestAnalyzeShellCommandASTSingleQuotedExternalPath(t *testing.T) {
	mods := &Mods{
		shellAnalyzer: func(tool, command string) approval.CommandAssessment {
			t.Fatalf("LLM classifier should not be called for %q", command)
			return approval.UnknownCommandAssessment()
		},
	}
	t.Cleanup(func() { mods.shellAnalyzer = nil })
	result := mods.assessCommand("shell_run", "cat '/etc/passwd'")
	if runtime.GOOS == "windows" {
		require.NotContains(t, result.KnownDirs, "/etc/passwd")
		return
	}
	require.Contains(t, result.KnownDirs, "/etc/passwd")
}

func TestAnalyzeShellCommandConfiguredReadOnlyCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX configured-command coverage applies to non-Windows shell_run")
	}
	workspace := canonicalTestPath(t, t.TempDir())
	externalDir := canonicalTestPath(t, t.TempDir())
	externalFile := filepath.Join(externalDir, "records.json")
	cfg := testConfigForWorkspace(workspace)
	cfg.BuiltinTools.ShellReadOnlyCommands = []string{"rg", "find"}
	mods := &Mods{
		Config: cfg,
		shellAnalyzer: func(tool, command string) approval.CommandAssessment {
			t.Fatalf("LLM classifier should not be called for configured command %q", command)
			return approval.UnknownCommandAssessment()
		},
	}
	t.Cleanup(func() { mods.shellAnalyzer = nil })

	result := mods.assessCommand("shell_run", "rg needle '"+externalFile+"'")
	require.Equal(t, approval.EffectRead, result.Effect)
	require.Contains(t, result.KnownDirs, externalFile)
	require.NotEmpty(t, result.Reason)

	result = mods.assessCommand("shell_run", "find . -delete")

	result = mods.assessCommand("shell_run", "rg needle README.md > matches.txt")
}

func TestAnalyzeShellCommandReadOnlyWorkspaceAffectedDirs(t *testing.T) {
	workspace := canonicalTestPath(t, t.TempDir())
	externalDir := canonicalTestPath(t, t.TempDir())
	externalFile := filepath.Join(externalDir, "passwd")

	t.Run("workspace command falls back to cwd", func(t *testing.T) {
		mods := &Mods{
			Config: testConfigForWorkspace(workspace),
			shellAnalyzer: func(tool, command string) approval.CommandAssessment {
				return approval.CommandAssessment{Effect: approval.EffectRead, Reason: "read-only"}
			},
		}
		t.Cleanup(func() { mods.shellAnalyzer = nil })

		result := mods.assessCommand("shell_run", "git status")
		require.Equal(t, []string{workspace}, result.KnownDirs)
	})

	t.Run("cd workspace command falls back to cwd", func(t *testing.T) {
		mods := &Mods{
			Config: testConfigForWorkspace(workspace),
			shellAnalyzer: func(tool, command string) approval.CommandAssessment {
				return approval.CommandAssessment{Effect: approval.EffectRead, Reason: "read-only"}
			},
		}
		t.Cleanup(func() { mods.shellAnalyzer = nil })

		cmd := "cd " + workspace + " && git tag --list 'v*' --sort=-v:refname | head -20"
		result := mods.assessCommand("shell_run", cmd)
		require.Equal(t, []string{workspace}, result.KnownDirs)
	})

	t.Run("external read does not add workspace", func(t *testing.T) {
		mods := &Mods{
			Config: testConfigForWorkspace(workspace),
			shellAnalyzer: func(tool, command string) approval.CommandAssessment {
				return approval.CommandAssessment{Effect: approval.EffectRead, Reason: "read-only"}
			},
		}
		t.Cleanup(func() { mods.shellAnalyzer = nil })

		result := mods.assessCommand("shell_run", "cat "+externalFile)
		require.NotEmpty(t, result.KnownDirs)
		require.True(t, hasPathUnder(result.KnownDirs, externalDir), "affected dirs should include external path under %s: %v", externalDir, result.KnownDirs)
		require.NotContains(t, result.KnownDirs, workspace)
	})
}

func hasPathUnder(paths []string, dir string) bool {
	for _, p := range paths {
		if p == dir || pathutil.Contains(dir, p) {
			return true
		}
	}
	return false
}

func canonicalTestPath(t *testing.T, path string) string {
	t.Helper()
	cleaned := filepath.Clean(path)
	if eval, err := filepath.EvalSymlinks(cleaned); err == nil {
		return filepath.Clean(eval)
	}
	return cleaned
}

func TestExtractExternalPathsIgnoresHeredocBody(t *testing.T) {
	cmd := "cat > /home/panjie/dev/myconfigs/vim/vimrc <<'EOF'\nset path=/\n/this/looks/like/a/path\nEOF"

	got := extractExternalPaths(cmd, "/workspace")
	require.Contains(t, got, "/home/panjie/dev/myconfigs/vim/vimrc")
	require.NotContains(t, got, "/")
	require.NotContains(t, got, "/this/looks/like/a/path")
}

func TestAnalyzeShellCommandMergesWritableDirsWhenAnalyzerOmitsDirs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test exercises POSIX shell heredoc analysis")
	}
	mods := &Mods{
		shellAnalyzer: func(tool, command string) approval.CommandAssessment {
			t.Fatalf("LLM classifier should not be called for %q", command)
			return approval.UnknownCommandAssessment()
		},
	}
	t.Cleanup(func() { mods.shellAnalyzer = nil })

	result := mods.assessCommand("shell_run", "cat > ~/dev/myconfigs/nvim/.gitignore <<'EOF'\nignored\nEOF")
	require.Contains(t, result.KnownDirs, "~/dev/myconfigs/nvim")
}

func TestAssessCommandPOSIXTargetDirectoryKeepsExternalWrite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell_run uses PowerShell on Windows")
	}
	workspace := canonicalTestPath(t, t.TempDir())
	external := canonicalTestPath(t, t.TempDir())
	m := &Mods{Config: testConfigForWorkspace(workspace)}

	assessment := m.assessCommand("shell_run", `cp -t "`+external+`" src.txt`)

	require.Equal(t, approval.EffectWrite, assessment.Effect)
	require.Contains(t, assessment.KnownDirs, external)
	require.Equal(t, DecisionAsk, ClassifyAccess(
		assessment.AccessIntent(),
		WorkspaceScope(workspace),
		nil,
		ApprovalReviewMode(ReviewAuto),
	))
}

func TestAnalyzeShellCommandStaticWriteSkipsLLM(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test exercises POSIX shell command analysis")
	}
	cases := []struct {
		name string
		cmd  string
		dir  string
	}{
		{"heredoc redirection", "cat > /tmp/out <<'EOF'\nhello\nEOF", "/tmp"},
		{"touch", "touch file", "."},
		{"rm", "rm /tmp/file", "/tmp"},
		{"cp", "cp a b", "."},
		{"mv", "mv a b", "."},
		{"env wrapped touch", "env touch file", "."},
		{"git output", "git diff --output=diff.txt", "."},
		{"xxd reverse output", "xxd -r input.hex output.bin", "."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mods := &Mods{
				shellAnalyzer: func(tool, command string) approval.CommandAssessment {
					t.Fatalf("LLM classifier should not be called for %q", command)
					return approval.UnknownCommandAssessment()
				},
			}
			t.Cleanup(func() { mods.shellAnalyzer = nil })

			result := mods.assessCommand("shell_run", c.cmd)
			require.Contains(t, result.KnownDirs, c.dir)
			require.Contains(t, result.Reason, "static analysis")
		})
	}
}

func TestAnalyzeShellCommandUnknownFallsThroughToLLM(t *testing.T) {
	called := false
	mods := &Mods{
		shellAnalyzer: func(tool, command string) approval.CommandAssessment {
			called = true
			return approval.CommandAssessment{Effect: approval.EffectWrite, Reason: "unknown writer"}
		},
	}
	t.Cleanup(func() { mods.shellAnalyzer = nil })

	result := mods.assessCommand("shell_run", "some unsupported writer")
	require.True(t, called, "unknown commands should still reach the LLM seam")
	require.Equal(t, approval.EffectWrite, result.Effect)
}

func TestAnalyzeShellCommandPowerShellReadOnly(t *testing.T) {
	// PowerShell AST classifier requires Windows + powershell.exe. On other
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
				shellAnalyzer: func(tool, command string) approval.CommandAssessment {
					t.Fatalf("LLM classifier should not be called for %q", command)
					return approval.UnknownCommandAssessment()
				},
			}
			t.Cleanup(func() { mods.shellAnalyzer = nil })
			result := mods.assessCommand("powershell_run", c.cmd)
			require.Equal(t, approval.EffectRead, result.Effect)
		})
	}
}

func TestAnalyzeShellCommandPowerShellWriteSkipsLLM(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell AST classifier requires Windows")
	}
	mods := &Mods{
		shellAnalyzer: func(tool, command string) approval.CommandAssessment {
			t.Fatalf("LLM classifier should not be called for %q", command)
			return approval.UnknownCommandAssessment()
		},
	}
	t.Cleanup(func() { mods.shellAnalyzer = nil })

	result := mods.assessCommand("powershell_run", "Set-Content file.txt 'hello'")
	require.Contains(t, result.KnownDirs, ".")
	require.Contains(t, result.Reason, "static analysis")
}

func TestAnalyzeShellCommandPowerShellExternalPathDirs(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell AST classifier requires Windows")
	}
	// Read-only PowerShell command with external path: AST classifier
	// says read-only, AST-extracted paths provide KnownDirs, no LLM call.
	mods := &Mods{
		shellAnalyzer: func(tool, command string) approval.CommandAssessment {
			t.Fatalf("LLM classifier should not be called for %q", command)
			return approval.UnknownCommandAssessment()
		},
	}
	t.Cleanup(func() { mods.shellAnalyzer = nil })
	result := mods.assessCommand("powershell_run", "Get-Content C:\\Users\\Public\\file.txt")
	require.NotEmpty(t, result.KnownDirs, "should have affected dirs from AST")

	spacedPath := `C:\Program Files\App\notes.txt`
	result = mods.assessCommand("powershell_run", `Get-Content "C:\Program Files\App\notes.txt"`)
	require.Contains(t, result.KnownDirs, spacedPath)
	require.NotContains(t, result.KnownDirs, `C:\Program`)
}

func TestAnalyzeShellCommandPowerShellDivisionDoesNotAffectRoot(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	require.NotEmpty(t, home)

	workspace := canonicalTestPath(t, t.TempDir())
	mods := &Mods{
		Config: testConfigForWorkspace(workspace),
		shellAnalyzer: func(tool, command string) approval.CommandAssessment {
			return approval.CommandAssessment{Effect: approval.EffectRead, Reason: "read-only"}
		},
	}
	t.Cleanup(func() { mods.shellAnalyzer = nil })

	cmd := `$dl = "$env:USERPROFILE\Downloads"; Get-ChildItem -Path $dl -File -Recurse -ErrorAction SilentlyContinue | Where-Object { $_.Length -gt 50MB } | ForEach-Object { $sizeInMB = [math]::Round($_.Length / 1MB, 2); "$($_.FullName)  ($sizeInMB MB)" }`
	result := mods.assessCommand("powershell_run", cmd)

	require.Equal(t, []string{filepath.Join(home, "Downloads")}, result.KnownDirs)
	require.NotContains(t, result.KnownDirs, "/")
}

func TestAnalyzeShellCommandPowerShellSetLocationGitLogWorkspaceDirs(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell AST classifier requires Windows")
	}
	workspace := canonicalTestPath(t, t.TempDir())
	mods := &Mods{
		Config: testConfigForWorkspace(workspace),
		shellAnalyzer: func(tool, command string) approval.CommandAssessment {
			t.Fatalf("LLM classifier should not be called for %q", command)
			return approval.UnknownCommandAssessment()
		},
	}
	t.Cleanup(func() { mods.shellAnalyzer = nil })

	cmd := "Set-Location " + workspace + "; git log --oneline -1 -- docs/superpowers/plans/2026-07-02-unified-directory-approval.md"
	result := mods.assessCommand("powershell_run", cmd)
	require.Equal(t, []string{workspace}, result.KnownDirs)
}

func TestExtractExternalPathsIgnoresBareSlash(t *testing.T) {
	ws := filepath.Clean(t.TempDir())
	// Single-quoted shell-program literals (awk/sed/perl scripts) contain
	// "/", "$", and "~" tokens that are syntax, not paths, and must not be
	// extracted. Previously this produced spurious "Risk: external read -
	// affects /" reviews for commands like `find . -exec awk '/re/{ ... }'`.
	for _, cmd := range []string{
		`awk '{print $1 / $2}' file`,                 // division inside quotes
		`awk '/^[[:space:]]*$/ {next}' file`,         // regex delimiter inside quotes
		`find . -exec awk '{n++} END{print n}' {} +`, // quoted awk program
	} {
		got := extractExternalPaths(cmd, ws)
		for _, p := range got {
			require.NotEqual(t, "/", p, "bare '/' inside quoted program must not be extracted for cmd=%q; got %v", cmd, got)
		}
	}

	// A bare UNQUOTED root argument is still detected (e.g. destructive
	// `find / -delete`), so stripping quoted programs does not weaken root
	// detection.
	require.Equal(t, []string{"/"}, extractExternalPaths("find / -delete", ws))

	// Positive control: a real unquoted absolute path is still extracted.
	ext := filepath.Clean(t.TempDir())
	secret := filepath.Join(ext, "secret")
	require.Equal(t, []string{secret}, extractExternalPaths("cat "+secret, ws))
}
