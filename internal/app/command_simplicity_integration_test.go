//go:build integration

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/stream"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

type recordedToolCall struct {
	name      string
	data      []byte
	corrected bool
}

func TestDeepSeekCommandSimplicityIntegration(t *testing.T) {
	skipIfNoKey(t, "DEEPSEEK_API_KEY", "deepseek command simplicity")
	m := testIntegrationModsWithBaseURL(t, "deepseek", "deepseek-v4-flash", "https://api.deepseek.com/")
	m.Config.Think = false

	registry := commandSimplicityEvalRegistry(t)
	prompt := "On a Windows machine, install Starship with winget and configure the CurrentUserCurrentHost PowerShell profile. Do not ask questions. Use the provided fake tools and treat their outputs as real."
	messages := []proto.Message{
		structuredSystemMessage(modsIdentityPrompt, proto.SystemSectionRuntimeIdentity),
		structuredSystemMessage(renderToolSelectionPrompt(registry, "windows"), proto.SystemSectionExecutionTools),
		{Role: proto.RoleUser, Content: prompt},
	}

	api, model, err := m.resolveModel(m.Config)
	require.NoError(t, err)
	cfgs, err := m.buildProviderConfigs(model, api)
	require.NoError(t, err)
	client, err := newStreamClient(modelProtocol(model), cfgs.Anthropic, cfgs.Google, cfgs.Ollama, cfgs.OpenAI)
	require.NoError(t, err)

	var mu sync.Mutex
	var calls []recordedToolCall
	preflight := newCommandPreflightGate(m.Config)
	fakeState := &commandSimplicityFakeState{}
	caller := func(call proto.ToolCallRequest) (string, error) {
		name, data := call.Name, call.Arguments
		analysis := approval.CommandAssessment{}
		lower := strings.ToLower(string(data))
		if name == "process_run" {
			analysis = m.assessCommand(name, string(data))
		}
		if name == "powershell_run" && strings.Contains(lower, "$profile") && (strings.Contains(lower, ";") || strings.Contains(lower, "if (")) {
			analysis = complexReviewabilityAnalysis()
			analysis.Effect = approval.EffectRead
			analysis.DynamicTargets = []string{"$PROFILE"}
		}
		if correctionErr := preflight.check(name, analysis); correctionErr != nil {
			mu.Lock()
			calls = append(calls, recordedToolCall{name: name, data: append([]byte(nil), data...), corrected: true})
			mu.Unlock()
			return "", correctionErr
		}
		mu.Lock()
		calls = append(calls, recordedToolCall{name: name, data: append([]byte(nil), data...)})
		mu.Unlock()
		return fakeState.result(name, data), nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := client.Request(ctx, proto.Request{
		Messages:   messages,
		API:        model.API,
		Model:      model.Name,
		Tools:      registry.Specs(),
		ToolCaller: caller,
	})
	defer func() { _ = st.Close() }()

	completed := false
	for round := 0; round < 10; round++ {
		for st.Next() {
			_, currentErr := st.Current()
			if currentErr != nil && !errors.Is(currentErr, stream.ErrNoContent) {
				require.NoError(t, currentErr)
			}
		}
		require.NoError(t, st.Err())
		results := st.CallTools()
		if len(results) == 0 {
			completed = true
			break
		}
		for _, result := range results {
			var correction correctionSuggester
			if result.Err != nil && errors.As(result.Err, &correction) && correction.CorrectionSuggested() {
				continue
			}
			require.NoError(t, result.Err)
		}
	}
	mu.Lock()
	recorded := append([]recordedToolCall(nil), calls...)
	mu.Unlock()
	require.NotEmpty(t, recorded)
	for _, call := range recorded {
		t.Logf("tool=%s corrected=%v args=%s", call.name, call.corrected, call.data)
	}
	require.True(t, completed, "model did not complete within the bounded tool rounds")
	assertSimpleStarshipToolSequence(t, recorded)
}

func commandSimplicityEvalRegistry(t *testing.T) *toolregistry.Registry {
	t.Helper()
	registry := toolregistry.NewRegistry()
	register := func(name, description string, capabilities toolregistry.ToolCapabilities, properties map[string]any, required ...string) {
		t.Helper()
		schema := map[string]any{
			"type":       "object",
			"properties": properties,
		}
		if len(required) > 0 {
			schema["required"] = required
		}
		require.NoError(t, registry.Register(toolregistry.Tool{
			Kind:         toolregistry.ToolKindBuiltin,
			Capabilities: capabilities,
			Spec: proto.ToolSpec{
				Name:        name,
				Description: description,
				InputSchema: schema,
			},
			Call: func(context.Context, json.RawMessage) (string, error) { return "", nil },
		}))
	}
	register("runtime_info", toolregistry.RuntimeInfoDescription, toolregistry.ToolCapabilities{ReadOnly: true}, map[string]any{
		"commands": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
	})
	register("process_run", toolregistry.ProcessRunDescription, toolregistry.ToolCapabilities{Mutable: true, ShellExecution: true}, map[string]any{
		"program": map[string]any{"type": "string"},
		"args":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
	}, "program")
	register("powershell_run", toolregistry.PowerShellRunDescription, toolregistry.ToolCapabilities{Mutable: true, ShellExecution: true}, map[string]any{
		"command": map[string]any{"type": "string"},
	}, "command")
	register("fs_write_file", "Write UTF-8 content to one literal path.", toolregistry.ToolCapabilities{Mutable: true}, map[string]any{
		"path":    map[string]any{"type": "string"},
		"content": map[string]any{"type": "string"},
	}, "path", "content")
	register("fs_replace", "Replace one exact text occurrence in one literal file path.", toolregistry.ToolCapabilities{Mutable: true}, map[string]any{
		"path":     map[string]any{"type": "string"},
		"old_text": map[string]any{"type": "string"},
		"new_text": map[string]any{"type": "string"},
	}, "path", "old_text", "new_text")
	register("fs_mkdir", "Create one literal directory path and its parents.", toolregistry.ToolCapabilities{Mutable: true}, map[string]any{
		"path": map[string]any{"type": "string"},
	}, "path")
	return registry
}

type commandSimplicityFakeState struct {
	mu            sync.Mutex
	starshipFound bool
	profileExists bool
	profile       string
}

func (s *commandSimplicityFakeState) result(name string, data []byte) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch name {
	case "runtime_info":
		starshipPath := ""
		if s.starshipFound {
			starshipPath = `C:\\Users\\tester\\AppData\\Local\\Microsoft\\WinGet\\Links\\starship.exe`
		}
		return `{"os":"windows","architecture":"amd64","workspace":"C:\\repo","shell":{"executable":"pwsh.exe","dialect":"powershell","version":"7.5.2"},"commands":[{"name":"winget","found":true,"path":"C:\\Windows\\winget.exe"},{"name":"starship","found":` + fmt.Sprintf("%t", s.starshipFound) + `,"path":"` + starshipPath + `"}]}`
	case "process_run":
		var args struct {
			Program string   `json:"program"`
			Args    []string `json:"args"`
		}
		_ = json.Unmarshal(data, &args)
		stdout := "ok"
		if strings.Contains(strings.ToLower(args.Program), "winget") && slices.Contains(args.Args, "install") {
			s.starshipFound = true
			stdout = "Starship installed successfully"
		} else if strings.Contains(strings.ToLower(args.Program), "starship") {
			stdout = "starship 1.26.0"
		}
		return `{"exit_code":0,"stdout":` + strconv.Quote(stdout) + `,"stderr":"","duration_ms":25,"timed_out":false,"stdout_truncated":false,"stdout_omitted_bytes":0,"stderr_truncated":false,"stderr_omitted_bytes":0}`
	case "powershell_run":
		var args struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(data, &args)
		lower := strings.ToLower(args.Command)
		if strings.Contains(lower, "add-content") || strings.Contains(lower, "set-content") || strings.Contains(lower, "new-item") {
			s.profileExists = true
			if strings.Contains(lower, "starship init") {
				s.profile = "Invoke-Expression (&starship init powershell)\n"
			}
			return "updated PowerShell profile"
		}
		if strings.Contains(lower, "test-path") {
			if s.profileExists {
				return "True"
			}
			return "False"
		}
		if strings.Contains(lower, "get-content") {
			return s.profile
		}
		if strings.Contains(lower, "get-item") && strings.Contains(lower, "length") {
			return strconv.Itoa(len(s.profile))
		}
		if strings.Contains(lower, "pscustomobject") || strings.Contains(lower, "convertto-json") {
			return `{"Path":"C:\\Users\\tester\\Documents\\PowerShell\\Microsoft.PowerShell_profile.ps1","Exists":false}`
		}
		if strings.Contains(lower, "$profile") {
			return `C:\Users\tester\Documents\PowerShell\Microsoft.PowerShell_profile.ps1`
		}
		return "ok"
	case "fs_write_file":
		var args struct {
			Content string `json:"content"`
		}
		_ = json.Unmarshal(data, &args)
		s.profileExists = true
		s.profile = args.Content
		return "wrote PowerShell profile"
	case "fs_replace":
		var args struct {
			OldText string `json:"old_text"`
			NewText string `json:"new_text"`
		}
		_ = json.Unmarshal(data, &args)
		s.profile = strings.Replace(s.profile, args.OldText, args.NewText, 1)
		return "updated PowerShell profile"
	case "fs_mkdir":
		return "created profile directory"
	default:
		return "ok"
	}
}

func assertSimpleStarshipToolSequence(t *testing.T, calls []recordedToolCall) {
	t.Helper()
	runtimeIndex, installIndex := -1, -1
	profileRead, profileWrite := false, false
	for i, call := range calls {
		if call.corrected {
			continue
		}
		lower := strings.ToLower(string(call.data))
		if call.name == "runtime_info" && strings.Contains(lower, "winget") {
			runtimeIndex = i
		}
		if strings.Contains(lower, "winget") && call.name != "runtime_info" {
			require.Equal(t, "process_run", call.name, "winget must not be wrapped in PowerShell")
			if strings.Contains(lower, "install") {
				installIndex = i
			}
		}
		if call.name == "powershell_run" {
			require.NotContains(t, lower, "===")
			require.NotContains(t, lower, "format-list")
			require.NotContains(t, lower, "set-executionpolicy", "must not add an unrelated execution-policy mutation")
			writer := strings.Contains(lower, "set-content") || strings.Contains(lower, "add-content") || strings.Contains(lower, "out-file") || strings.Contains(lower, "new-item")
			if strings.Contains(lower, "$profile") && !writer {
				profileRead = true
			}
			if writer {
				require.Fail(t, "PowerShell profile mutation should use an available filesystem tool", call.data)
			}
		}
		if call.name == "process_run" {
			var args struct {
				Program string   `json:"program"`
				Args    []string `json:"args"`
			}
			require.NoError(t, json.Unmarshal(call.data, &args))
			wrapped := approval.AnalyzeProcessReviewability(args.Program, args.Args, false)
			require.False(t, wrapped.ShouldCorrect, "process_run must not wrap shell source")
		}
		if call.name == "fs_write_file" || call.name == "fs_replace" {
			profileWrite = true
			require.Contains(t, lower, `c:\\users\\tester\\documents\\powershell`)
			require.NotContains(t, lower, "$profile")
		}
	}
	require.NotEqual(t, -1, installIndex, "expected winget install through process_run")
	if runtimeIndex >= 0 {
		require.Less(t, runtimeIndex, installIndex)
	}
	require.True(t, profileRead, "expected a separate read-only profile-path inspection")
	require.True(t, profileWrite, "expected a separate profile mutation")
}
