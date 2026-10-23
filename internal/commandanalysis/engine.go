// Package commandanalysis analyzes command effects and targets without granting
// permissions or executing the command being assessed.
package commandanalysis

import (
	"context"
	"slices"

	"github.com/panjie/mods/internal/pathutil"
)

type Engine struct {
	settings     Settings
	dependencies Dependencies
}

func NewEngine(settings Settings, dependencies Dependencies) *Engine {
	settings.ReadOnlyCommands = slices.Clone(settings.ReadOnlyCommands)
	dependencies.UntrustedProgramDirs = slices.Clone(dependencies.UntrustedProgramDirs)
	return &Engine{settings, dependencies}
}

type analysisRun struct {
	ctx               context.Context
	workingDir        string
	programDirs       []string
	policy            ReadOnlyCommandPolicy
	invocationDialect pathutil.Flavor
}

func (e *Engine) run(ctx context.Context) *analysisRun {
	if ctx == nil {
		ctx = context.Background()
	}
	return &analysisRun{ctx: ctx, workingDir: e.settings.WorkingDir,
		programDirs: slices.Clone(e.dependencies.UntrustedProgramDirs),
		policy:      ReadOnlyCommandPolicy{Commands: slices.Clone(e.settings.ReadOnlyCommands)}}
}

func (e *Engine) Analyze(ctx context.Context, input Input) (Result, error) {
	in, err := input.snapshot()
	if err != nil {
		return UnknownCommandAssessment(), err
	}
	run := e.run(ctx)
	if err := run.ctx.Err(); err != nil {
		return UnknownCommandAssessment(), err
	}
	if in.Shell != nil {
		run.invocationDialect = in.Shell.InvocationDialect
		return run.analyzeShell(in.Dialect, in.Shell.Source, in.Shell.ShadowedEnvironment, in.Cwd).Clone(), nil
	}
	return run.analyzeProcess(*in.Process, in.Cwd, in.Dialect).Clone(), nil
}

// ConstrainResolvedProcess preserves the existing pinned-program trust check.
func (e *Engine) ConstrainResolvedProcess(result Result, resolved string) Result {
	result = result.Clone()
	if resolved == "" {
		return result
	}
	location := pathutil.Location(resolved, e.settings.WorkingDir, e.dependencies.UntrustedProgramDirs)
	if location == pathutil.LocationWorkingDir || location == pathutil.LocationSafe {
		result.Effect = EffectUnknown
		result.Evidence = EvidenceUnknown
		result.AddGap(GapExecutionContext)
		result.Reason = "executable resolves from a cwd or temporary directory"
	}
	return result
}

// ResolveGitPushOrigins and ResolveGitPushArgvOrigins keep the existing focused
// discovery seam while phase 2 replaces it with shared parsed invocations.
func (e *Engine) ResolveGitPushOrigins(ctx context.Context, posix bool, command, cwd string) (origins, unresolved, local []string) {
	return e.run(ctx).resolveGitPushOrigins(posix, command, cwd)
}
func (e *Engine) ResolveGitPushArgvOrigins(ctx context.Context, args []string, cwd string) (origins, unresolved, local []string) {
	return e.run(ctx).resolveGitPushArgvOrigins(slices.Clone(args), cwd)
}
