package app

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"runtime"
	"slices"
	"strings"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/commandanalysis"
	"github.com/panjie/mods/internal/pathutil"
	toolregistry "github.com/panjie/mods/internal/tools"
)

func shellPathFlavor(tool string) pathutil.Flavor {
	if shellToolUsesPowerShell(tool) {
		return pathutil.FlavorPowerShell
	}
	return pathutil.FlavorPOSIX
}
func shellToolUsesPowerShell(tool string) bool {
	return tool == "powershell_run" || ((tool == "shell_run" || tool == "process_run") && runtime.GOOS == "windows")
}

func (m *Mods) commandAnalyzer() (*commandanalysis.Engine, context.Context) {
	settings := commandanalysis.Settings{}
	ctx := context.Background()
	if m != nil {
		if m.ctx != nil {
			ctx = m.ctx
		}
		if m.Config != nil {
			settings.WorkingDir = m.Config.ResolveWorkingDir().Canonical
			settings.ReadOnlyCommands = m.Config.BuiltinTools.ShellReadOnlyCommands
		}
	}
	return commandanalysis.NewEngine(settings, commandanalysis.Dependencies{UntrustedProgramDirs: approval.SafeDirs()}), ctx
}

// commandResult adapts the phase-1 fact model without reconstructing any facts.
// The compatibility type remains for callers; authorization uses Targets only.
func commandResult(result commandanalysis.Result) approval.CommandAssessment {
	return approval.CommandAssessment(result.Clone())
}

// commandResultToIntent is the only production command authorization adapter.
// Legacy KnownDirs and literal candidates are display facts, never authority.
func commandResultToIntent(result commandanalysis.Result) approval.AccessIntent {
	intent := approval.AccessIntent{Class: approval.AccessWrite, WriteDirs: []string{}, WriteCoverage: approval.CoverageIncomplete, Reason: result.Reason}
	validEvidence := result.Evidence == commandanalysis.EvidenceStatic || result.Evidence == commandanalysis.EvidenceTrust
	read := result.Effect == commandanalysis.EffectRead && validEvidence && result.WriteCoverage == commandanalysis.CoverageComplete && len(result.Gaps) == 0
	validEffect := read || result.Effect == commandanalysis.EffectWrite && result.Evidence == commandanalysis.EvidenceStatic
	if read {
		intent.Class = approval.AccessRead
		intent.ReadDirs = []string{}
		intent.WriteDirs = nil
		intent.WriteCoverage = approval.CoverageComplete
		intent.DynamicProbe = result.DynamicProbe
	}
	intent.UncertainEffect = !validEffect
	intent.Gaps = append([]approval.AssessmentGap(nil), result.Gaps...)
	if validEffect && result.WriteCoverage == commandanalysis.CoverageComplete {
		intent.WriteCoverage = approval.CoverageComplete
	}
	gap := func() {
		intent.WriteCoverage = approval.CoverageIncomplete
		if !slices.Contains(intent.Gaps, approval.GapWriteTargets) {
			intent.Gaps = append(intent.Gaps, approval.GapWriteTargets)
		}
	}
	invalid := func() {
		read = false
		intent.Class = approval.AccessWrite
		if intent.WriteDirs == nil {
			intent.WriteDirs = []string{}
		}
		intent.UncertainEffect = true
		gap()
	}
	if result.WriteCoverage != commandanalysis.CoverageIncomplete && result.WriteCoverage != commandanalysis.CoverageComplete {
		invalid()
	}
	if !validEffect {
		gap()
	}
	for _, target := range result.Targets {
		if target.Role != commandanalysis.TargetRead && target.Role != commandanalysis.TargetWrite && target.Role != commandanalysis.TargetCandidate {
			invalid()
			continue
		}
		if target.Kind != commandanalysis.TargetFilesystem && target.Kind != commandanalysis.TargetOrigin && target.Kind != commandanalysis.TargetProvider {
			invalid()
			continue
		}
		if target.State != commandanalysis.TargetResolved && target.State != commandanalysis.TargetUnresolved {
			invalid()
			continue
		}
		if target.Role == commandanalysis.TargetCandidate {
			continue
		}
		write := target.Role == commandanalysis.TargetWrite
		if write {
			binding, _ := json.Marshal(struct {
				Kind                 commandanalysis.TargetKind
				Operation            commandanalysis.TargetOperation
				Raw, Lexical, Actual string
				State                commandanalysis.TargetState
			}{target.Kind, target.Operation, target.Raw, target.LexicalPath, target.Path, target.State})
			intent.TargetBindings = append(intent.TargetBindings, string(binding))
		}
		if write && read {
			read = false
			intent.Class = approval.AccessWrite
			intent.WriteDirs = []string{}
			intent.UncertainEffect = true
			gap()
		}
		if target.State == commandanalysis.TargetResolved && target.Path == "" {
			invalid()
			continue
		}
		if target.Kind == commandanalysis.TargetFilesystem && (target.Operation < commandanalysis.OperationRead || target.Operation > commandanalysis.OperationDestination) {
			invalid()
			continue
		}
		if target.State != commandanalysis.TargetResolved {
			if write {
				intent.UnresolvedPaths = append(intent.UnresolvedPaths, target.Raw)
				gap()
			}
			continue
		}
		switch target.Kind {
		case commandanalysis.TargetFilesystem:
			if target.Operation < commandanalysis.OperationRead || target.Operation > commandanalysis.OperationDestination || !pathutil.IsAbs(target.Path) || !pathutil.IsAbs(target.Dir) {
				invalid()
				continue
			}
			if write && (target.Operation == commandanalysis.OperationRead || target.Operation == commandanalysis.OperationDestination) {
				gap()
				continue
			}
			if write {
				expected := pathutil.ParentDir(target.Path)
				if target.Operation == commandanalysis.OperationDirectory {
					expected = target.Path
				}
				same := pathutil.Contains(expected, target.Dir) && pathutil.Contains(target.Dir, expected)
				if target.Operation == commandanalysis.OperationCreateEntry {
					same = pathutil.Contains(target.Dir, expected)
				}
				if !same {
					invalid()
					continue
				}
				intent.WriteDirs = append(intent.WriteDirs, target.Dir)
			} else {
				intent.ReadDirs = append(intent.ReadDirs, target.Dir)
			}
		case commandanalysis.TargetOrigin:
			origin, ok := commandanalysis.NormalizeRemoteOrigin(target.Path)
			if !ok {
				invalid()
				continue
			}
			if write {
				intent.WriteOrigins = append(intent.WriteOrigins, origin)
			} else {
				intent.ReadOrigins = append(intent.ReadOrigins, origin)
			}
		case commandanalysis.TargetProvider:
			if write {
				intent.ProviderWriteTargets = append(intent.ProviderWriteTargets, target.Path)
				gap()
			}
		}
	}
	if !read {
		intent.UnresolvedRemoteTargets = append([]string(nil), result.UnresolvedRemoteTargets...)
		if len(intent.UnresolvedRemoteTargets) > 0 {
			gap()
		}
		if len(intent.WriteDirs) == 0 && len(intent.WriteOrigins) == 0 {
			gap()
		}
	} else {
		intent.UnresolvedPaths = append([]string(nil), result.DynamicTargets...)
	}
	intent.ReadDirs = canonicalIntentValues(intent.ReadDirs)
	intent.WriteDirs = canonicalIntentValues(intent.WriteDirs)
	intent.ReadOrigins = canonicalIntentValues(intent.ReadOrigins)
	intent.WriteOrigins = canonicalIntentValues(intent.WriteOrigins)
	return intent
}
func canonicalIntentValues(values []string) []string {
	if values == nil {
		return nil
	}
	values = slices.Clone(values)
	slices.Sort(values)
	return slices.Compact(values)
}

// Review labels, diagnostic reasons, ordering and read-only candidates cannot
// invalidate a grant. Compare actual authorization groups and safety gates.
func commandAuthorizationEqual(a, b approval.AccessIntent) bool {
	semantic := func(i approval.AccessIntent) approval.AccessIntent {
		i.Reason = ""
		if i.DominantClass() == approval.AccessWrite {
			i.ReadDirs = nil
			i.ReadOrigins = nil
		}
		i.TargetBindings = canonicalIntentValues(i.TargetBindings)
		i.DynamicProbe = false
		i.Dirs = canonicalIntentValues(i.Dirs)
		i.RemoteOrigins = canonicalIntentValues(i.RemoteOrigins)
		i.ReadDirs = canonicalIntentValues(i.ReadDirs)
		i.WriteDirs = canonicalIntentValues(i.WriteDirs)
		i.ReadOrigins = canonicalIntentValues(i.ReadOrigins)
		i.WriteOrigins = canonicalIntentValues(i.WriteOrigins)
		i.UnresolvedPaths = canonicalIntentValues(i.UnresolvedPaths)
		i.UnresolvedRemoteTargets = canonicalIntentValues(i.UnresolvedRemoteTargets)
		i.ProviderWriteTargets = canonicalIntentValues(i.ProviderWriteTargets)
		// Gaps gate completeness, but their explanatory wording does not grant it.
		complete := i.HasCompleteWriteTargets()
		i.Gaps = nil
		if !complete {
			i.WriteCoverage = approval.CoverageIncomplete
		}
		return i
	}
	return reflect.DeepEqual(semantic(a), semantic(b))
}

func (m *Mods) assessCommandAtCwd(tool, command string, shadowedEnv map[string]bool, ws string) approval.CommandAssessment {
	result, _ := m.assessCommandAtCwdResult(tool, command, shadowedEnv, ws)
	return result
}
func (m *Mods) assessCommandAtCwdResult(tool, command string, shadowedEnv map[string]bool, ws string) (approval.CommandAssessment, error) {
	if tool == "process_run" {
		return m.assessProcessInvocationResult(command)
	}
	return m.assessShellCommandResult(tool, shellPathFlavor(tool), command, shadowedEnv, ws)
}

func (m *Mods) assessShellCommand(tool string, flavor pathutil.Flavor, command string, shadowedEnv map[string]bool, ws string) approval.CommandAssessment {
	result, _ := m.assessShellCommandResult(tool, flavor, command, shadowedEnv, ws)
	return result
}
func (m *Mods) assessShellCommandResult(tool string, flavor pathutil.Flavor, command string, shadowedEnv map[string]bool, ws string) (approval.CommandAssessment, error) {
	engine, ctx := m.commandAnalyzer()
	result, err := engine.Analyze(ctx, commandanalysis.Input{Cwd: ws, Dialect: flavor,
		Shell: &commandanalysis.ShellInput{Source: command, ShadowedEnvironment: shadowedEnv}})
	return commandResult(result), err
}

func (m *Mods) assessProcessInvocation(raw string) approval.CommandAssessment {
	result, _ := m.assessProcessInvocationResult(raw)
	return result
}
func (m *Mods) assessProcessInvocationResult(raw string) (approval.CommandAssessment, error) {
	var invocation struct {
		Program   string            `json:"program"`
		Args      []string          `json:"args"`
		Cwd       string            `json:"cwd"`
		SecretEnv map[string]string `json:"secret_env"`
	}
	if err := json.Unmarshal([]byte(raw), &invocation); err != nil {
		return approval.UnknownCommandAssessment(), err
	}
	if strings.TrimSpace(invocation.Program) == "" {
		return approval.UnknownCommandAssessment(), errors.New("empty process program")
	}
	baseDir := ""
	if m != nil && m.Config != nil {
		baseDir = m.Config.ResolveWorkingDir().Canonical
	}
	cwd := strings.TrimSpace(invocation.Cwd)
	flavor := shellPathFlavor("process_run")
	if cwd == "" {
		cwd = baseDir
	} else if !pathutil.IsAbs(cwd) {
		cwd = pathutil.NormalizeShellPath(cwd, pathutil.DefaultOptions(baseDir, flavor))
	}
	keys := make([]string, 0, len(invocation.SecretEnv))
	for key := range invocation.SecretEnv {
		keys = append(keys, key)
	}
	engine, ctx := m.commandAnalyzer()
	result, err := engine.Analyze(ctx, commandanalysis.Input{Cwd: cwd, Dialect: flavor,
		Process: &commandanalysis.ProcessInput{Program: invocation.Program, Args: invocation.Args, EnvironmentKeys: keys}})
	return commandResult(result), err
}

func (m *Mods) constrainResolvedProcessAssessment(result approval.CommandAssessment, binding toolregistry.ProcessProgramBinding) approval.CommandAssessment {
	if m == nil || m.Config == nil {
		return result
	}
	engine, _ := m.commandAnalyzer()
	return commandResult(engine.ConstrainResolvedProcess(commandanalysis.Result(result), binding.Resolved))
}
