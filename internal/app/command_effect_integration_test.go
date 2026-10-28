//go:build integration

package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/stream"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/panjie/mods/internal/websearch"
	"github.com/stretchr/testify/require"
)

// First-round smoke evidence only: real assembled prompts and schemas, no tool
// caller and no CallTools. Even a misdeclared write can never execute here.
func TestDeepSeekCommandEffectFirstRoundIntegration(t *testing.T) {
	skipIfNoKey(t, "DEEPSEEK_API_KEY", "command effect declarations")
	for _, mode := range []struct {
		name    string
		minimal bool
	}{{"normal", false}, {"minimal", true}} {
		for _, tc := range []struct {
			name, prompt string
			want         toolregistry.CommandEffect
		}{
			{"read", "Use the shell tool to count lines in the existing UTF-8 file sample.txt and print the count. Do not create any files.", toolregistry.CommandReadOnly},
			{"aggregate", "Use the shell tool to sum the integers from 1 through 100 using a loop and a variable, and print the sum. Keep all calculation in memory.", toolregistry.CommandReadOnly},
			{"write", "Use the shell tool to create result.txt containing exactly hello. The destination does not exist and the parent directory exists. Perform this operation now.", toolregistry.CommandMayWrite},
			{"build", "Use process_run to run go build -o app.bin . in this Go project. Go is installed. Issue the build invocation now; no preliminary checks are needed.", toolregistry.CommandMayWrite},
			{"remote", "Use process_run to run git push origin HEAD in this repository. The branch and origin are already configured and I want to push this commit now. No preliminary checks are needed.", toolregistry.CommandMayWrite},
			{"unknown-script", "Use process_run to execute python audit.py. Python is installed and audit.py exists. Its contents are unknown; I want you to invoke it directly without first opening it.", toolregistry.CommandMayWrite},
		} {
			t.Run(mode.name+"/"+tc.name, func(t *testing.T) {
				m := testIntegrationModsWithBaseURL(t, "deepseek", "deepseek-v4-flash", "https://api.deepseek.com/")
				m.Config.WorkingDir = t.TempDir()
				m.Config.NoSave, m.Config.Raw, m.Config.NoInstructions = true, true, true
				m.Config.Minimal, m.Config.Format = mode.minimal, ""
				m.Config.BuiltinTools.Filesystem, m.Config.BuiltinTools.Shell = FilesystemNever, true
				require.NoError(t, m.setupStreamContext(tc.prompt))
				api, model, err := m.resolveModel(m.Config)
				require.NoError(t, err)
				cfgs, err := m.buildProviderConfigs(model, api)
				require.NoError(t, err)
				_, err = m.resolveThinkWithOllama(&model, &cfgs.Anthropic, &cfgs.Google, &cfgs.Ollama, &cfgs.OpenAI)
				require.NoError(t, err)
				client, err := newStreamClient(modelProtocol(model), cfgs.Anthropic, cfgs.Google, cfgs.Ollama, cfgs.OpenAI)
				require.NoError(t, err)
				registry, err := m.buildToolRegistryForProvider(m.ctx, m.Config, websearch.Config{}, tc.prompt, client)
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, registry.Close()) })
				require.NoError(t, m.injectToolSelectionPrompt(registry))
				ctx, cancel := context.WithTimeout(m.ctx, time.Minute)
				defer cancel()
				maxTokens := int64(2048)
				st := client.Request(ctx, proto.Request{
					Messages: m.messages, API: model.API, Model: model.Name, Tools: registry.Specs(),
					Temperature: ptrOrNil(float64(0)), MaxTokens: &maxTokens,
				})
				defer func() { _ = st.Close() }()
				for st.Next() {
					_, err := st.Current()
					if err != nil && !errors.Is(err, stream.ErrNoContent) {
						require.NoError(t, err)
					}
				}
				require.NoError(t, st.Err())
				calls := 0
				for _, message := range st.Messages() {
					if message.Role != proto.RoleAssistant {
						continue
					}
					for _, call := range message.ToolCalls {
						if !registry.ShellExecution(call.Function.Name) {
							continue
						}
						calls++
						data := []byte(call.Function.Arguments)
						require.NoError(t, registry.ValidateRequiredArgs(call.Function.Name, data))
						effect, err := toolregistry.DeclaredCommandEffect(data)
						require.NoError(t, err)
						t.Logf("requested_model=%s minimal=%t tool=%s args=%s dispatched=false", model.Name, mode.minimal, call.Function.Name, data)
						require.Equal(t, tc.want, effect)
					}
				}
				require.Positive(t, calls, "expected a command tool call in the first response")
			})
		}
	}
}
