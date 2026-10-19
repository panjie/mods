//go:build integration

package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/panjie/mods/internal/evals"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/stream"
	"github.com/panjie/mods/internal/websearch"
	"github.com/stretchr/testify/require"
)

// Only the model's first decision is evaluated. Tool specifications are real,
// but no tool caller is supplied and CallTools is never invoked. An unwanted
// inspection is recorded as a failure without accessing the test host.
func TestDeepSeekClarificationFirstRoundIntegration(t *testing.T) {
	skipIfNoKey(t, "DEEPSEEK_API_KEY", "deepseek clarification decisions")
	for _, mode := range []struct {
		name    string
		minimal bool
	}{{"normal", false}, {"minimal", true}} {
		t.Run(mode.name, func(t *testing.T) {
			for _, scenario := range []struct {
				name, prompt string
				caseID       string
			}{
				{"ambiguous-symptom", "这个界面字体看起来不对，帮我看看怎么回事。", evals.CaseAmbiguousSymptomFirstTurn},
				{"missing-edit-target", "Reduce the font size in one of the two QML screens in this fixture. I have not specified which screen; ask me which one before inspecting or editing. Do not choose for me.", evals.CaseMissingModificationContext},
			} {
				t.Run(scenario.name, func(t *testing.T) {
					m := testIntegrationModsWithBaseURL(t, "deepseek", "deepseek-v4-flash", "https://api.deepseek.com/")
					m.Config.WorkingDir = t.TempDir()
					m.Config.NoSave, m.Config.Raw, m.Config.NoInstructions = true, true, true
					m.Config.Minimal, m.Config.Format = mode.minimal, ""
					m.Config.BuiltinTools.Filesystem, m.Config.BuiltinTools.Shell = FilesystemAlways, true
					require.NoError(t, m.setupStreamContext(scenario.prompt))
					api, model, err := m.resolveModel(m.Config)
					require.NoError(t, err)
					cfgs, err := m.buildProviderConfigs(model, api)
					require.NoError(t, err)
					_, err = m.resolveThinkWithOllama(&model, &cfgs.Anthropic, &cfgs.Google, &cfgs.Ollama, &cfgs.OpenAI)
					require.NoError(t, err)
					client, err := newStreamClient(modelProtocol(model), cfgs.Anthropic, cfgs.Google, cfgs.Ollama, cfgs.OpenAI)
					require.NoError(t, err)
					registry, err := m.buildToolRegistryForProvider(m.ctx, m.Config, websearch.Config{}, scenario.prompt, client)
					require.NoError(t, err)
					t.Cleanup(func() { require.NoError(t, registry.Close()) })
					require.NoError(t, m.injectToolSelectionPrompt(registry))
					ctx, cancel := context.WithTimeout(m.ctx, time.Minute)
					defer cancel()
					maxTokens := int64(1024)
					started := time.Now()
					st := client.Request(ctx, proto.Request{
						Messages: m.messages, API: model.API, Model: model.Name, Tools: registry.Specs(),
						Temperature: ptrOrNil(float64(0)), MaxTokens: &maxTokens, TrackUsage: true,
					})
					defer func() { _ = st.Close() }()
					var answer strings.Builder
					for st.Next() {
						chunk, err := st.Current()
						if err != nil && !errors.Is(err, stream.ErrNoContent) {
							require.NoError(t, err)
						}
						answer.WriteString(chunk.Content)
					}
					require.NoError(t, st.Err())
					c, ok := evals.CaseByID(scenario.caseID)
					require.True(t, ok)
					c.ForbidsProbing = true
					report := evals.Score(st.Messages(), c)
					encoded, err := json.Marshal(report)
					require.NoError(t, err)
					usage := st.Usage()
					t.Logf("provider=deepseek model=%s minimal=%t first_round_only=true temperature=0 elapsed=%s input_tokens=%d output_tokens=%d", model.Name, mode.minimal, time.Since(started), usage.InputTokens, usage.OutputTokens)
					t.Logf("execution report: %s", encoded)
					t.Logf("first-round answer: %s", answer.String())
					for _, dimension := range report.Dimensions {
						if dimension.Name != "tool-budget" {
							require.True(t, dimension.Passed, "%s: %s", dimension.Name, dimension.Evidence)
						}
					}
				})
			}
		})
	}
}
