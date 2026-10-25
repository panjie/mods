package commandanalysis

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/panjie/mods/internal/pathutil"
	"github.com/stretchr/testify/require"
)

func TestStage5ExtendedFileAndEnvBindings(t *testing.T) {
	for _, args := range [][]string{
		{"touch", "-am", "-r", "input", "output"}, {"touch", "--reference=input", "output"},
		{"touch", "-rinput", "output"}, {"touch", "-t", "202610251200", "output"},
		{"touch", "--date=2026-10-25", "output"}, {"tee", "-ai", "output"},
		{"env", "LANG=C", "touch", "-am", "-r", "input", "output"},
		{"env", "LANG=C", "env", "TZ=UTC", "rm", "output"},
	} {
		f := catalogTokens(args, nativeSpace, ReadOnlyCommandPolicy{})
		require.True(t, f.BoundedWrite, args)
		writes := []string{}
		for _, target := range f.Targets {
			if target.Role == TargetWrite {
				writes = append(writes, target.Path)
			}
		}
		require.Equal(t, []string{"output"}, writes, args)
		if strings.Contains(strings.Join(args, " "), "input") {
			require.Contains(t, f.Targets, InvocationTarget{Path: "input", Role: TargetRead, Operation: OperationRead})
		}
		if args[len(args)-2] == "rm" {
			require.Equal(t, OperationRemoveEntry, f.Targets[0].Operation)
		}
	}
	for _, args := range [][]string{
		{"touch", "-az", "output"}, {"touch", "-r"}, {"touch", "-"}, {"touch", "--no-dereference", "output"},
		{"env", "PATH=/tmp", "touch", "output"}, {"env", "-S", "touch output"},
		{"env", "LANG=C", "mystery", "output"}, {"env", "LANG=C", "cp", "input", "output"},
		{"env", "LANG=C", "rm", "-rf", "output"},
	} {
		require.False(t, catalogTokens(args, nativeSpace, ReadOnlyCommandPolicy{}).BoundedWrite, args)
	}
}

func TestStage5ExtendedGitGlobals(t *testing.T) {
	for _, args := range [][]string{{"--no-pager", "-C", "repo", "status"}, {"-C", "a", "-C", "b", "--literal-pathspecs", "ls-files"}, {"--no-optional-locks", "status"}} {
		require.True(t, catalogTokens(append([]string{"git"}, args...), nativeSpace, ReadOnlyCommandPolicy{}).BuiltinRead, args)
	}
	for _, args := range [][]string{{"-C"}, {"-c", "alias.x=!touch out", "status"}, {"--paginate", "log"}, {"--git-dir", "repo", "status"}, {"-C", "repo", "diff", "--output=out"}} {
		require.False(t, catalogTokens(append([]string{"git"}, args...), nativeSpace, ReadOnlyCommandPolicy{}).BuiltinRead, args)
	}
	root := makeStaticGitRepo(t)
	r := AssessArgvStaticWithContext("git", []string{"--no-pager", "-C", root, "commit", "-m", "message"}, true, ReadOnlyCommandPolicy{}, ArgvStaticContext{Cwd: t.TempDir()})
	require.Equal(t, EffectWrite, r.Effect)
	require.Contains(t, r.KnownDirs, root)
	require.Equal(t, CoverageIncomplete, r.WriteCoverage)
	calls := 0
	e := NewEngine(Settings{}, Dependencies{Git: gitFunc(func(_ context.Context, q GitQuery) (string, error) {
		require.Equal(t, filepath.Join(root, "a", "b"), q.Cwd)
		if q.Kind == GitExecutable {
			return "", nil
		}
		calls++
		return "https://example.com/repo", nil
	})})
	in := shellInput("git --no-pager -C a -C b push origin")
	in.Cwd = root
	result, err := e.Analyze(context.Background(), in)
	require.NoError(t, err)
	require.Positive(t, calls)
	require.Contains(t, result.RemoteOrigins, "https://example.com")
	require.Equal(t, CoverageIncomplete, result.WriteCoverage)
}

func TestStage5PowerShellScalarBindings(t *testing.T) {
	for _, args := range [][]string{
		{"Set-Content", "-LiteralPath", "'file[1]'", "-Value", "text", "-Encoding", "utf8", "-NoNewline"},
		{"Add-Content", "-Path", "'out file'", "-Value", "'hello, world'", "-Force"},
		{"Out-File", "-FilePath", "out", "-Encoding", "utf8", "-Append", "-Width", "80"},
		{"sc", "out", "text"},
	} {
		f := catalogTokens(args, powershellSpace, ReadOnlyCommandPolicy{})
		require.True(t, f.BoundedWrite, args)
		require.Len(t, f.Targets, 1)
	}
	for _, args := range [][]string{
		{"Set-Content", "-Path", "'*.txt'", "-Value", "x"},
		{"Set-Content", "-Path", "a,b", "-Value", "x"},
		{"Set-Content", "@params"}, {"Set-Content", "-Path", "out", "-Mystery", "second"},
		{"Set-Content", "-LiteralPath", "out", "-Stream", "stream", "-Value", "x"},
		{"Set-Content", "-LiteralPath", "out", "-Value"},
		{"Set-Content", "-Path", "out", "-Path", "second", "-Value", "x"},
		{"Out-File", "-FilePath", "out", "-LiteralPath", "second"},
		{"Set-Content", "-Path", `'HKCU:\Software\X'`, "-Value", "x"},
		{"Set-Content", "-Path", "$path", "-Value", "x"},
	} {
		require.False(t, catalogTokens(args, powershellSpace, ReadOnlyCommandPolicy{}).BoundedWrite, args)
	}
}

func TestStage5PowerShellRedirectionProof(t *testing.T) {
	ir := psBridgeIR{Redirects: []string{"FileRedirection"}, Nodes: []psSyntaxNode{{ID: 1, Kind: "CommandAst"}, {ID: 2, Parent: 1, Kind: "FileRedirectionAst"}}, Redirections: []psRedirection{{NodeID: 2, Owner: 1, Target: "'out file'", Literal: true, Stream: 1}}}
	targets, ok := powerShellRedirectionTargets(&ir)
	require.True(t, ok)
	require.Len(t, targets, 1)
	for _, mutate := range []func(*psBridgeIR){
		func(r *psBridgeIR) { r.Redirections[0].Literal = false }, func(r *psBridgeIR) { r.Redirections[0].Owner = 99 },
		func(r *psBridgeIR) { r.Redirections[0].Target = "'*.txt'" }, func(r *psBridgeIR) { r.Redirections[0].Stream = 99 },
		func(r *psBridgeIR) { r.Redirections = nil }, func(r *psBridgeIR) { r.Nodes[1].Kind = "StringConstantExpressionAst" },
		func(r *psBridgeIR) { r.HasAssignment = true }, func(r *psBridgeIR) { r.Redirections = append(r.Redirections, r.Redirections[0]) },
	} {
		raw, err := json.Marshal(ir)
		require.NoError(t, err)
		var changed psBridgeIR
		require.NoError(t, json.Unmarshal(raw, &changed))
		mutate(&changed)
		_, ok := powerShellRedirectionTargets(&changed)
		require.False(t, ok)
	}
}

// Optional real parser test. The production bridge script only parses stdin;
// source commands are never evaluated by analysis. Positive fixtures also run
// separately in isolated directories. Linux runs do not certify Windows paths.
func TestStage5RealPowerShellBindings(t *testing.T) {
	host := os.Getenv("MODS_TEST_PWSH")
	if host == "" {
		t.Skip("set MODS_TEST_PWSH to an installed PowerShell executable")
	}
	script := filepath.Join(t.TempDir(), "bridge.ps1")
	require.NoError(t, os.WriteFile(script, psBridgeScript, 0600))
	parser := parserFunc(func(ctx context.Context, source string) ([]byte, error) {
		request, _ := json.Marshal(map[string]string{"cmd": source})
		cmd := exec.CommandContext(ctx, host, "-NoProfile", "-NonInteractive", "-File", script)
		cmd.Stdin = strings.NewReader(string(request) + "\n")
		return cmd.Output()
	})
	for _, tc := range []struct {
		source   string
		complete bool
	}{
		{"Write-Output hello > 'out.txt'", true}, {"Write-Output hello 2>> 'err.txt'", true},
		{"Write-Output hello *> 'all.txt'", true},
		{"Set-Content -LiteralPath 'out.txt' -Value text -Encoding utf8", true},
		{"Set-Content -LiteralPath 'file[1]' -Value text", true},
		{"Out-File -LiteralPath 'out.txt' -Encoding utf8 -Append", true},
		{"Write-Output hello > 'out.txt' 2> 'err.txt'", true},
		{"Set-Content -Path '*.txt' -Value text", false},
		{"Set-Content -LiteralPath 'out.txt' -Value ('text')", false},
		{"Set-Content -Path 'HKCU:\\Software\\X' -Value text", false},
		{"Set-Content -Path 'a','b' -Value text", false}, {"Set-Content @params", false},
		{"Set-Content -Path 'out.txt' -Unknown other -Value text", false},
		{"Write-Output hello > $target", false}, {"mystery > 'out.txt'", false},
	} {
		t.Run(tc.source, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			root := t.TempDir()
			result, err := NewEngine(Settings{}, Dependencies{PowerShell: parser}).Analyze(ctx, Input{Cwd: root, Dialect: pathutil.FlavorPowerShell, Shell: &ShellInput{Source: tc.source}})
			require.NoError(t, err)
			require.NotContains(t, result.Gaps, GapParse)
			require.Equal(t, tc.complete, result.WriteCoverage == CoverageComplete, "%+v", result)
			if tc.complete {
				cmd := exec.CommandContext(ctx, host, "-NoProfile", "-NonInteractive", "-Command", tc.source)
				cmd.Dir = root
				output, err := cmd.CombinedOutput()
				require.NoError(t, err, string(output))
				writes := 0
				for _, target := range result.Targets {
					if target.Role == TargetWrite {
						writes++
						require.Equal(t, TargetResolved, target.State)
						_, err := os.ReadFile(target.Path)
						require.NoError(t, err)
					}
				}
				require.Positive(t, writes)
			}
		})
	}
}
