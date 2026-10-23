package commandanalysis

import (
	"context"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/panjie/mods/internal/pathutil"
	"github.com/stretchr/testify/require"
)

func TestEngineContract(t *testing.T) {
	e := NewEngine(Settings{}, Dependencies{})
	for _, input := range []Input{
		{},
		{Shell: &ShellInput{}, Process: &ProcessInput{Program: "cat"}},
		{Dialect: pathutil.FlavorPOSIX, Process: &ProcessInput{}},
	} {
		result, err := e.Analyze(context.Background(), input)
		require.Error(t, err)
		require.Equal(t, EffectUnknown, result.Effect)
		require.Equal(t, CoverageIncomplete, result.WriteCoverage)
	}
	in := Input{Dialect: pathutil.FlavorPOSIX, Process: &ProcessInput{Program: "uniq", Args: []string{"input"}}}
	result, err := e.Analyze(context.Background(), in)
	require.NoError(t, err)
	require.Equal(t, EffectRead, result.Effect)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err = e.Analyze(ctx, in)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, EffectUnknown, result.Effect)
}

func TestEngineSnapshotsTrustAndInputs(t *testing.T) {
	commands := []string{"custom-reader"}
	dirs := []string{t.TempDir()}
	e := NewEngine(Settings{ReadOnlyCommands: commands}, Dependencies{UntrustedProgramDirs: dirs})
	commands[0] = "different-command"
	in := Input{Dialect: pathutil.FlavorPOSIX, Process: &ProcessInput{Program: "custom-reader", Args: []string{"original"}, EnvironmentKeys: []string{"SECRET"}}}
	snapshot, err := in.snapshot()
	require.NoError(t, err)
	in.Process.Args[0] = "changed"
	in.Process.EnvironmentKeys[0] = "CHANGED"
	require.Equal(t, []string{"original"}, snapshot.Process.Args)
	require.Equal(t, []string{"SECRET"}, snapshot.Process.EnvironmentKeys)
	result, err := e.Analyze(context.Background(), snapshot)
	require.NoError(t, err)
	require.Equal(t, EvidenceTrust, result.Evidence)
	resolved := filepath.Join(dirs[0], "custom-reader")
	dirs[0] = t.TempDir()
	constrained := e.ConstrainResolvedProcess(result, resolved)
	require.Equal(t, EffectUnknown, constrained.Effect)
	require.Contains(t, constrained.Gaps, GapExecutionContext)
	require.Equal(t, EffectRead, result.Effect, "binding must not mutate an approved snapshot")

	shell := Input{Dialect: pathutil.FlavorPOSIX, Shell: &ShellInput{InvocationDialect: pathutil.FlavorPOSIX, Source: "cat file", ShadowedEnvironment: map[string]bool{"SECRET": true}}}
	copy, err := shell.snapshot()
	require.NoError(t, err)
	delete(shell.Shell.ShadowedEnvironment, "SECRET")
	require.True(t, copy.Shell.ShadowedEnvironment["SECRET"])
}

func TestResultCloneOwnsMutableFacts(t *testing.T) {
	before := Result{KnownDirs: []string{"source"}, Gaps: []AssessmentGap{GapWriteTargets},
		LiteralAssignments: map[string]string{"p": "source"},
		Reviewability:      CommandReviewability{Reasons: []ReviewabilityReason{ReviewabilityDynamicWriteTarget}}}
	after := before.Clone()
	after.KnownDirs[0] = "changed"
	after.Gaps[0] = GapControlFlow
	after.LiteralAssignments["p"] = "changed"
	after.Reviewability.Reasons[0] = ReviewabilityOpaqueExecution
	require.Equal(t, "source", before.KnownDirs[0])
	require.Equal(t, GapWriteTargets, before.Gaps[0])
	require.Equal(t, "source", before.LiteralAssignments["p"])
	require.Equal(t, ReviewabilityDynamicWriteTarget, before.Reviewability.Reasons[0])
}

func TestAnalysisPackageDoesNotImportPolicyOrApplication(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		require.NoError(t, err)
		for _, spec := range parsed.Imports {
			name, err := strconv.Unquote(spec.Path.Value)
			require.NoError(t, err)
			if strings.HasPrefix(name, "github.com/panjie/mods/internal/") {
				require.Contains(t, []string{"github.com/panjie/mods/internal/pathutil", "github.com/panjie/mods/internal/commandtrace"}, name, "%s must not pull authorization, application, config, tools or providers into analysis", file)
			}
		}
	}
}
