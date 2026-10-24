package app

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
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
// The compatibility conversion remains in approval until role-aware targets land.
func commandResult(result commandanalysis.Result) approval.CommandAssessment {
	return approval.CommandAssessment(result.Clone())
}

// commandResultToIntent is the shared initial-review/revalidation boundary.
// The legacy policy mapping is retained until role-aware targets are enabled.
func commandResultToIntent(result commandanalysis.Result) approval.AccessIntent {
	return commandResult(result).AccessIntent()
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
