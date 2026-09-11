package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/prompts"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/stream"
	"github.com/panjie/mods/internal/textutil"
	toolregistry "github.com/panjie/mods/internal/tools"
)

const writeTargetTimeout = 30 * time.Second

// prepareWriteTargets runs once per user turn, including when the main request
// is retried. The resulting rules use the ordinary session persistence path.
func (m *Mods) prepareWriteTargets(client stream.Client, registry *toolregistry.Registry, request proto.Request) error {
	if m.writeTargetsDone || m.reviewer == nil || m.reviewer.reviewMode != ReviewAuto || registry.Len() == 0 {
		return nil
	}
	m.writeTargetsDone = true
	ctx, cancel := context.WithTimeout(m.ctx, writeTargetTimeout)
	defer cancel()
	m.sendToolOperationStatus("Inferring write targets")
	request.Messages = writeTargetMessages(m.messages, m.reviewer.scope.Value)
	request.Tools = writeTargetTools(registry)
	request.ToolCaller = m.writeTargetToolCaller(ctx, registry)
	request.ResponseFormat = nil
	rules, usage, err := inferWriteTargets(ctx, client, request, m.reviewer.scope)
	// Publish usage with the completed turn on the Update goroutine. Keep it
	// across retries without exposing worker mutations to UI/debug readers.
	m.writeTargetsUsage.Add(usage)
	if m.ctx.Err() != nil {
		return m.ctx.Err()
	}
	if err != nil {
		debug.Printf("Write target inference skipped: %v", err)
		m.sendToolOperationStatus("")
		return nil
	}
	m.reviewer.rules.Add(rules...)
	if len(rules) > 0 {
		debug.Printf("Inferred session write permissions: %s", RulesLabel(rules))
		m.sendToolOperationStatus("Allowed writes: " + RulesLabel(rules))
	} else {
		debug.Printf("Write target inference: no write targets identified")
		m.sendToolOperationStatus("")
	}
	return nil
}

func writeTargetMessages(messages []proto.Message, workspace string) []proto.Message {
	type entry struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	home, _ := os.UserHomeDir()
	envelope := struct {
		Workspace string  `json:"workspace"`
		Home      string  `json:"home"`
		OS        string  `json:"os"`
		History   []entry `json:"history"`
		Current   string  `json:"current_request"`
	}{Workspace: workspace, Home: home, OS: runtime.GOOS}
	for i, message := range messages {
		if message.Role == proto.RoleSystem {
			continue
		}
		if i == len(messages)-1 && message.Role == proto.RoleUser {
			envelope.Current = message.Content
		} else {
			envelope.History = append(envelope.History, entry{message.Role, proto.TranscriptContent(message)})
		}
	}
	data, _ := json.Marshal(envelope)
	return []proto.Message{
		{Role: proto.RoleSystem, Content: prompts.WriteTargets},
		{Role: proto.RoleUser, Content: string(data)},
	}
}

// Each provider stream is consumed exactly once. After the optional first
// batch, a fresh request without tools collects the final decision. Never call
// Next again on a completed stream: adapters would start another tool round.
func inferWriteTargets(ctx context.Context, client stream.Client, request proto.Request, scope Scope) ([]Rule, proto.TokenUsage, error) {
	var usage proto.TokenUsage
	for round := 0; round < 2; round++ {
		if err := ctx.Err(); err != nil {
			return nil, usage, err
		}
		started := time.Now()
		debug.Printf("Write target inference: round=%d tools=%d", round+1, len(request.Tools))
		st := client.Request(ctx, request)
		var output strings.Builder
		var readErr error
		for st.Next() {
			chunk, err := st.Current()
			if err != nil && !errors.Is(err, stream.ErrNoContent) {
				readErr = err
				break
			}
			output.WriteString(chunk.Content)
		}
		_ = st.Close()
		usage.Add(st.Usage())
		if err := errors.Join(readErr, st.Err(), ctx.Err()); err != nil {
			return nil, usage, err
		}
		messages := st.Messages()
		calls := len(messages) > 0 && messages[len(messages)-1].Role == proto.RoleAssistant && len(messages[len(messages)-1].ToolCalls) > 0
		if !calls {
			rules, err := parseWriteTargets(output.String(), scope)
			debug.Printf("Write target inference: round=%d duration=%s rules=%s parse_error=%v", round+1, time.Since(started), RulesLabel(rules), err)
			return rules, usage, err
		}
		if round != 0 || len(request.Tools) == 0 {
			return nil, usage, fmt.Errorf("write target inference exceeded its one tool batch")
		}
		for _, result := range st.CallTools() {
			debug.Printf("Write target discovery: tool=%s duration=%s error=%v", result.Name, result.Duration, result.Err)
		}
		request.Messages = append([]proto.Message(nil), st.Messages()...)
		request.Tools = nil
		request.ToolCaller = nil
	}
	return nil, usage, fmt.Errorf("write target inference did not produce a decision")
}

func parseWriteTargets(raw string, scope Scope) ([]Rule, error) {
	var result struct {
		Dirs []string `json:"write_dirs"`
		URLs []string `json:"write_urls"`
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, err
	}
	var dirs []string
	for _, dir := range result.Dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" || strings.ContainsAny(dir, "\x00\r\n*?") || strings.Contains(dir, "://") || approval.IsUnresolvedShellPathExpression(dir, runtime.GOOS != "windows") {
			continue
		}
		dirs = append(dirs, dir)
	}
	rules := RulesForDirs(dirs, scope, AccessWrite)
	return append(rules, RulesForRemoteOrigins(result.URLs)...), nil
}

func writeTargetToolAllowed(registry *toolregistry.Registry, name string) bool {
	if registry.Interactive(name) || name == "todo_write" {
		return false
	}
	return registry.ReadOnly(name) && !registry.Mutable(name) || name == "process_run" || name == "shell_run" || name == "powershell_run"
}

func writeTargetTools(registry *toolregistry.Registry) []proto.ToolSpec {
	var specs []proto.ToolSpec
	for _, spec := range registry.Specs() {
		if writeTargetToolAllowed(registry, spec.Name) {
			specs = append(specs, spec)
		}
	}
	return specs
}

// Discovery never enters interactive review or the LLM shell classifier.
// Reuse the normal assessment/path pipeline with an unknown-only fallback.
func (m *Mods) writeTargetToolCaller(ctx context.Context, registry *toolregistry.Registry) proto.ToolCaller {
	return func(call proto.ToolCallRequest) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		name := call.Name
		if !writeTargetToolAllowed(registry, name) {
			return "", fmt.Errorf("write target discovery only permits non-interactive reads")
		}
		data := registry.UnwrapArguments(name, call.Arguments)
		if err := registry.ValidateRequiredArgs(name, data); err != nil {
			return "", err
		}
		callCtx, cancel := context.WithTimeout(ctx, m.Config.MCPTimeout)
		defer cancel()
		cwd := m.Config.ResolveWorkspace().Canonical
		var assessment *approval.CommandAssessment
		if registry.ShellExecution(name) {
			var args struct {
				Cwd       string            `json:"cwd"`
				SecretEnv map[string]string `json:"secret_env"`
			}
			if err := json.Unmarshal(data, &args); err != nil {
				return "", err
			}
			if len(args.SecretEnv) != 0 {
				return "", fmt.Errorf("discovery does not resolve secret environment overrides")
			}
			var err error
			cwd, err = toolregistry.NormalizeExecutionCwd(cwd, args.Cwd)
			if err != nil {
				return "", err
			}
			var normalized map[string]json.RawMessage
			if err := json.Unmarshal(data, &normalized); err != nil {
				return "", err
			}
			if normalized == nil {
				return "", fmt.Errorf("discovery command arguments must be an object")
			}
			normalized["cwd"], _ = json.Marshal(cwd)
			data, _ = json.Marshal(normalized)
			checker := &Mods{Config: m.Config, ctx: callCtx, shellAnalyzer: func(string, string) approval.CommandAssessment {
				return approval.UnknownCommandAssessment()
			}}
			command := ExtractShellCommand(data)
			var binding toolregistry.ProcessProgramBinding
			if name == "process_run" {
				command = string(data)
				binding, err = toolregistry.PrepareProcessProgram(data)
				if err != nil {
					return "", err
				}
				callCtx = toolregistry.WithProcessProgramBinding(callCtx, binding)
			}
			value := checker.assessCommandAtCwd(name, command, nil, cwd)
			if name == "process_run" {
				value = checker.constrainResolvedProcessAssessment(value, binding)
			}
			if value.Effect != approval.EffectRead {
				return "", fmt.Errorf("discovery command is not proven read-only")
			}
			assessment = &value
		}
		intent := buildAccessIntent(name, data, registry, assessment)
		if intent.DominantClass() != AccessRead {
			return "", fmt.Errorf("discovery tool is not read-only")
		}
		intent = normalizeAccessIntentDirs(intent, cwd, name, registry.ShellExecution(name))
		dirs := append(ExternalDirs(intent, m.reviewer.scope, m.safeDirs()), cwd)
		callCtx = toolregistry.WithAuthorizedDirs(callCtx, dirs)
		output, err := registry.Call(callCtx, name, data)
		if m.secrets != nil {
			output = m.secrets.Redact(output)
			if err != nil {
				err = errors.New(m.secrets.Redact(err.Error()))
			}
		}
		return textutil.TruncateUTF8Bytes(output, 24*1024), err
	}
}
