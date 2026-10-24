package commandanalysis

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/panjie/mods/internal/pathutil"
	"github.com/stretchr/testify/require"
)

type parserFunc func(context.Context, string) ([]byte, error)

func (f parserFunc) Parse(ctx context.Context, s string) ([]byte, error) { return f(ctx, s) }

type gitFunc func(context.Context, GitQuery) (string, error)

func (f gitFunc) Query(ctx context.Context, q GitQuery) (string, error) { return f(ctx, q) }

type pathsFunc struct {
	resolve func(context.Context, string) (string, error)
	repo    func(context.Context, string) ([]string, error)
}

func (f pathsFunc) Resolve(ctx context.Context, p string) (string, error) {
	if f.resolve != nil {
		return f.resolve(ctx, p)
	}
	return p, nil
}
func (f pathsFunc) RepositoryDirs(ctx context.Context, p string) ([]string, error) {
	if f.repo != nil {
		return f.repo(ctx, p)
	}
	return []string{p}, nil
}

func shellInput(source string) Input {
	return Input{Cwd: "/workspace", Dialect: pathutil.FlavorPOSIX, Shell: &ShellInput{Source: source}}
}

func TestSharedPowerShellParseAndProtocol(t *testing.T) {
	source := "Get-Content notes.txt"
	ir := psBridgeIR{Version: "2", Commands: []string{"get-content"}, TopLevelStatementCount: 1, PipelineCount: 1,
		Nodes:       []psSyntaxNode{{ID: 1, Start: 0, End: len(source), Kind: "CommandAst"}},
		Invocations: []psCommandInvocation{{NodeID: 1, Name: "get-content", Args: []string{"notes.txt"}}}}
	raw, err := json.Marshal(ir)
	require.NoError(t, err)
	calls := 0
	e := NewEngine(Settings{}, Dependencies{PowerShell: parserFunc(func(ctx context.Context, s string) ([]byte, error) {
		calls++
		require.Equal(t, source, s)
		return raw, nil
	})})
	in := shellInput(source)
	in.Dialect = pathutil.FlavorPowerShell
	result, err := e.Analyze(context.Background(), in)
	require.NoError(t, err)
	require.Equal(t, EffectRead, result.Effect)
	require.Equal(t, 1, calls)
	// Revalidation parses again; nothing mutable is cached across Analyze.
	_, err = e.Analyze(context.Background(), in)
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	for _, bad := range []string{`{"version":"1"}`, `{"version":"2"}`, `invalid`} {
		e := NewEngine(Settings{}, Dependencies{PowerShell: parserFunc(func(context.Context, string) ([]byte, error) { return []byte(bad), nil })})
		result, err := e.Analyze(context.Background(), in)
		require.NoError(t, err)
		require.Equal(t, EffectUnknown, result.Effect)
		require.Equal(t, CoverageIncomplete, result.WriteCoverage)
		require.Contains(t, result.Gaps, GapParse)
	}
}

func TestStage2EnvironmentIsPerAnalysis(t *testing.T) {
	first := filepath.Join(t.TempDir(), "first")
	second := filepath.Join(t.TempDir(), "second")
	env := []string{"HOME=" + first, "USERPROFILE=" + first}
	reads := 0
	var paths []string
	e := NewEngine(Settings{}, Dependencies{Environment: func() []string { reads++; return env }, Paths: pathsFunc{resolve: func(ctx context.Context, p string) (string, error) {
		paths = append(paths, p)
		env[0] = "HOME=" + second
		env[1] = "USERPROFILE=" + second
		return p, nil
	}}})
	in := shellInput(`touch "$HOME/out" "$HOME/other"`)
	for i := 0; i < 2; i++ {
		result, err := e.Analyze(context.Background(), in)
		require.NoError(t, err)
		require.Equal(t, EffectWrite, result.Effect)
	}
	require.Equal(t, 2, reads)
	require.Equal(t, []string{filepath.Join(first, "out"), filepath.Join(first, "other"), filepath.Join(second, "out"), filepath.Join(second, "other")}, paths)
}

func TestStage2QueriesCancellationAndLimits(t *testing.T) {
	t.Run("cancelled parser", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		e := NewEngine(Settings{}, Dependencies{PowerShell: parserFunc(func(ctx context.Context, _ string) ([]byte, error) { <-ctx.Done(); return nil, ctx.Err() })})
		in := shellInput("Get-Content notes.txt")
		in.Dialect = pathutil.FlavorPowerShell
		result, err := e.Analyze(ctx, in)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Equal(t, EffectUnknown, result.Effect)
		require.Contains(t, result.Gaps, GapAnalysisCancelled)
	})
	t.Run("cancelled Git", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		e := NewEngine(Settings{}, Dependencies{Git: gitFunc(func(ctx context.Context, q GitQuery) (string, error) { <-ctx.Done(); return "", ctx.Err() })})
		result, err := e.Analyze(ctx, shellInput("git push origin"))
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Equal(t, CoverageIncomplete, result.WriteCoverage)
	})
	for _, tc := range []struct {
		name, source string
		value        string
	}{
		{"query count", strings.Repeat("git push origin;", maxAnalysisQueries+1), "https://example.com/repo.git"},
		{"query output", "git push origin", strings.Repeat("x", maxQueryOutput+1)},
		{"total output", strings.Repeat("git push origin;", 6), strings.Repeat("x", maxQueryOutput)},
		{"input bytes", strings.Repeat("x", maxAnalysisInputBytes+1), ""},
		{"node depth", strings.Repeat("( ", maxAnalysisDepth) + "cat file" + strings.Repeat(" )", maxAnalysisDepth), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			e := NewEngine(Settings{}, Dependencies{Git: gitFunc(func(ctx context.Context, q GitQuery) (string, error) { calls++; return tc.value, nil })})
			result, err := e.Analyze(context.Background(), shellInput(tc.source))
			require.NoError(t, err)
			require.Equal(t, EffectUnknown, result.Effect)
			require.Equal(t, CoverageIncomplete, result.WriteCoverage)
			require.Contains(t, result.Gaps, GapAnalysisBudget)
			require.LessOrEqual(t, calls, maxAnalysisQueries)
		})
	}
	t.Run("path failure", func(t *testing.T) {
		e := NewEngine(Settings{}, Dependencies{Paths: pathsFunc{resolve: func(context.Context, string) (string, error) { return "", errors.New("unavailable") }}})
		result, err := e.Analyze(context.Background(), shellInput("touch output"))
		require.NoError(t, err)
		require.Equal(t, EffectWrite, result.Effect)
		require.Equal(t, CoverageIncomplete, result.WriteCoverage)
		require.Contains(t, result.Gaps, GapWriteTargets)
	})
}

func TestStage2GitRefreshAndShadows(t *testing.T) {
	calls := 0
	e := NewEngine(Settings{}, Dependencies{Git: gitFunc(func(ctx context.Context, q GitQuery) (string, error) {
		if q.Kind == GitExecutable {
			return "", nil
		}
		require.Equal(t, GitPushURLs, q.Kind)
		require.Equal(t, "origin", q.Name)
		calls++
		if calls == 1 {
			return "https://first.example/repo.git\nssh://second.example/repo.git", nil
		}
		return "https://changed.example/repo.git", nil
	})})
	first, err := e.Analyze(context.Background(), shellInput("git push origin"))
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"https://first.example", "ssh://second.example"}, first.RemoteOrigins)
	second, err := e.Analyze(context.Background(), shellInput("git push origin"))
	require.NoError(t, err)
	require.Equal(t, []string{"https://changed.example"}, second.RemoteOrigins)
	in := shellInput("git push origin")
	in.Shell.ShadowedEnvironment = map[string]bool{"GIT_CONFIG_COUNT": true}
	result, err := e.Analyze(context.Background(), in)
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	require.NotEmpty(t, result.UnresolvedRemoteTargets)
}

func TestStage2BoundedBridgeIO(t *testing.T) {
	_, err := readBridgeLine(bufio.NewReader(strings.NewReader(strings.Repeat("x", maxBridgeOutputBytes+1))))
	require.ErrorIs(t, err, errAnalysisBudget)
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	inputReader, inputWriter := io.Pipe()
	defer inputReader.Close()
	defer inputWriter.Close()
	go io.Copy(io.Discard, inputReader)
	bp := &bridgeProcess{stdin: inputWriter, stdout: bufio.NewReader(reader)}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err = bp.roundTripContext(ctx, "cat file")
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestStage2IRMetadataCannotGrantRedirectionCoverage(t *testing.T) {
	source := "Write-Output hello > out.txt"
	ir := psBridgeIR{Version: "2", Commands: []string{"write-output"}, Redirects: []string{"FileRedirection"},
		Nodes:        []psSyntaxNode{{ID: 1, Kind: "CommandAst", End: len(source)}, {ID: 2, Parent: 1, Kind: "FileRedirectionAst", Start: 19, End: len(source)}},
		Invocations:  []psCommandInvocation{{NodeID: 1, Name: "write-output", Args: []string{"hello"}}},
		Redirections: []psRedirection{{NodeID: 2, Owner: 1, Target: "out.txt", Literal: true, Stream: 1}}}
	require.NoError(t, validateIRNodes(&ir, source))
	raw, err := json.Marshal(ir)
	require.NoError(t, err)
	e := NewEngine(Settings{}, Dependencies{PowerShell: parserFunc(func(context.Context, string) ([]byte, error) { return raw, nil })})
	in := shellInput(source)
	in.Dialect = pathutil.FlavorPowerShell
	result, err := e.Analyze(context.Background(), in)
	require.NoError(t, err)
	require.Equal(t, CoverageIncomplete, result.WriteCoverage)
	require.NotEqual(t, EffectRead, result.Effect)
	ir.Nodes[1].Parent = 99
	require.Error(t, validateIRNodes(&ir, source))
}

func TestStage2ShadowedHomeCannotUseInheritedAlias(t *testing.T) {
	home := t.TempDir()
	e := NewEngine(Settings{}, Dependencies{Environment: func() []string { return []string{"HOME=" + home, "USERPROFILE=" + home} }, Paths: pathsFunc{}})
	in := shellInput(`touch "$HOME/output"`)
	in.Shell.ShadowedEnvironment = map[string]bool{"HOME": true}
	result, err := e.Analyze(context.Background(), in)
	require.NoError(t, err)
	require.Equal(t, CoverageIncomplete, result.WriteCoverage)
	require.NotContains(t, result.WritePaths, filepath.Join(home, "output"))
}

func TestStage2AutomaticsRefreshWithoutGlobalCache(t *testing.T) {
	calls := 0
	e := NewEngine(Settings{}, Dependencies{Automatics: automaticFunc(func(context.Context, string, string, []string) (string, error) {
		calls++
		return filepath.Join(t.TempDir(), "profile"), nil
	})})
	first := e.run(context.Background()).resolveAutomatics([]string{"$PROFILE", "${PROFILE}"}, nil)
	second := e.run(context.Background()).resolveAutomatics([]string{"$PROFILE"}, nil)
	require.Equal(t, 2, calls)
	require.Equal(t, first["$PROFILE"], first["${PROFILE}"])
	require.NotEqual(t, first["$PROFILE"], second["$PROFILE"])
}
