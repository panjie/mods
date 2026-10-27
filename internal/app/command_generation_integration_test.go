//go:build integration

package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/commandanalysis"
	"github.com/panjie/mods/internal/evals"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/stream"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/panjie/mods/internal/websearch"
	"github.com/stretchr/testify/require"
)

type generationCallObservation struct {
	Name       string            `json:"name"`
	Args       json.RawMessage   `json:"args"`
	Effect     string            `json:"effect"`
	StaticRead bool              `json:"static_read"`
	ReadBasis  string            `json:"read_basis,omitempty"`
	Decision   approval.Decision `json:"predicted_decision"`
	Opaque     bool              `json:"opaque"`
	Error      string            `json:"error,omitempty"`
}

type generationRun struct {
	Case                 string                      `json:"case"`
	Arm                  string                      `json:"arm"`
	BaselineRevision     string                      `json:"baseline_revision"`
	Repeat               int                         `json:"repeat"`
	Model                string                      `json:"model"`
	OS                   string                      `json:"os"`
	Minimal              bool                        `json:"minimal"`
	PolicyFingerprint    string                      `json:"policy_fingerprint"`
	PromptFingerprint    string                      `json:"prompt_fingerprint"`
	Temperature          float64                     `json:"temperature"`
	MaxTokens            int64                       `json:"max_tokens"`
	Think                bool                        `json:"think"`
	FirstRoundOnly       bool                        `json:"first_round_only"`
	ElapsedMS            int64                       `json:"elapsed_ms"`
	InputTokens          int64                       `json:"input_tokens"`
	OutputTokens         int64                       `json:"output_tokens"`
	Calls                []generationCallObservation `json:"calls"`
	Answer               string                      `json:"answer"`
	Error                string                      `json:"error,omitempty"`
	Outcome              evals.GenerationOutcome     `json:"outcome"`
	ZeroReviewCompletion *bool                       `json:"zero_review_completion"`
}

func generationObserve(m *Mods, reg *toolregistry.Registry, call proto.ToolCallRequest) generationCallObservation {
	data := reg.UnwrapArguments(call.Name, call.Arguments)
	o := generationCallObservation{Name: call.Name, Args: append(json.RawMessage(nil), data...)}
	if err := reg.ValidateRequiredArgs(call.Name, data); err != nil {
		o.Error = err.Error()
		return o
	}
	var assessed *commandanalysis.CommandAssessment
	if reg.ShellExecution(call.Name) {
		input := ExtractShellCommand(data)
		if call.Name == "process_run" {
			input = string(data)
		}
		a, err := m.assessCommandAtCwdResult(call.Name, input, extractSecretEnvNames(data), m.Config.WorkingDir)
		if err != nil {
			o.Error = err.Error()
			return o
		}
		assessed = &a
		o.StaticRead = a.StaticRead
		if a.StaticRead && a.Effect == commandanalysis.EffectRead {
			o.ReadBasis = "static_command"
		}
		o.Opaque = a.Shape.Opaque
		o.Effect = string(a.Effect)
	}
	intent := buildAccessIntent(call.Name, data, reg, assessed)
	intent = normalizeAccessIntentDirs(intent, m.Config.WorkingDir, call.Name, reg.ShellExecution(call.Name))
	if assessed == nil {
		o.Effect = string(intent.DominantClass())
		if intent.DominantClass() == approval.AccessRead {
			o.ReadBasis = "native_read_contract"
		}
	}
	o.Decision = approval.ClassifyAccess(intent, approval.WorkingDirScope(m.Config.WorkingDir), m.safeDirs(), approval.ReviewAuto)
	return o
}

// First-round comparisons never invoke CallTools and cannot execute generated
// commands. Full queries use the unchanged production caller in noninteractive
// auto mode: unknown effects fail approval rather than being executed.
func TestReadOnlyGenerationFirstRoundIntegration(t *testing.T) { runGenerationComparison(t, true) }
func TestReadOnlyGenerationCompletionIntegration(t *testing.T) { runGenerationComparison(t, false) }

func runGenerationComparison(t *testing.T, firstOnly bool) {
	skipIfNoKey(t, "DEEPSEEK_API_KEY", "read-only generation A/A1")
	fingerprint := generationPolicyFingerprint(t)
	cases, err := evals.CommandGenerationCases()
	require.NoError(t, err)
	repeats := 1
	if value := os.Getenv("MODS_GENERATION_REPEATS"); value != "" {
		repeats, err = strconv.Atoi(value)
		require.NoError(t, err)
		require.True(t, repeats >= 1 && repeats <= 10)
	}
	minimal := os.Getenv("MODS_GENERATION_MINIMAL") == "1"
	model := os.Getenv("MODS_BLACKBOX_MODEL")
	if model == "" {
		model = "deepseek-v4-flash"
	}
	var reports []generationRun
	t.Cleanup(func() {
		commandanalysis.CloseBridge()
		if dir := os.Getenv("MODS_GENERATION_REPORT_DIR"); dir != "" {
			require.NoError(t, os.MkdirAll(dir, 0700))
			data, err := json.MarshalIndent(reports, "", "  ")
			require.NoError(t, err)
			name := fmt.Sprintf("generation-%s-minimal-%t.json", map[bool]string{true: "first-round", false: "completion"}[firstOnly], minimal)
			require.NoError(t, os.WriteFile(filepath.Join(dir, name), append(data, '\n'), 0600))
		}
	})
	for _, c := range cases {
		// The ambiguous original is also completed, but its correctness remains
		// unavailable until manual scope/line-semantics review of the report.
		if !firstOnly && len(c.Expected) == 0 && c.ID != "ambiguous-code" {
			continue
		}
		if selected := os.Getenv("MODS_GENERATION_CASES"); selected != "" && !strings.Contains(","+selected+",", ","+c.ID+",") {
			continue
		}
		for repeat := 0; repeat < repeats; repeat++ {
			arms := []string{"A", "A1"}
			if repeat%2 == 1 {
				arms = []string{"A1", "A"}
			}
			// Same paths and contents for each pair, with fixtures restored
			// between arms. No project instructions, installed skills or trust.
			root := t.TempDir()
			for _, arm := range arms {
				t.Run(fmt.Sprintf("%s/%d/%s", c.ID, repeat+1, arm), func(t *testing.T) {
					// root is owned by this t.TempDir, never a user-provided path.
					require.NoError(t, os.RemoveAll(root))
					require.NoError(t, os.MkdirAll(root, 0700))
					for name, content := range evals.CommandGenerationFiles() {
						path := filepath.Join(root, filepath.FromSlash(name))
						require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
						require.NoError(t, os.WriteFile(path, []byte(content), 0600))
					}
					m := testIntegrationModsWithBaseURL(t, "deepseek", model, "https://api.deepseek.com/")
					cfg := defaultConfig()
					cfg.APIs = m.Config.APIs
					cfg.API = "deepseek"
					cfg.Model = model
					cfg.WorkingDir = root
					cfg.NoSave = true
					cfg.NoInstructions = true
					cfg.Raw = true
					cfg.Minimal = minimal
					cfg.Format = ""
					cfg.Role = ""
					cfg.Think = false
					cfg.ReviewMode = ReviewAuto
					cfg.InteractiveTTYAvailable = false
					cfg.BuiltinTools.Filesystem = FilesystemAlways
					cfg.BuiltinTools.Shell = true
					cfg.BuiltinTools.ShellReadOnlyCommands = nil
					cfg.SkillsDirs = []string{filepath.Join(root, "absent-skills")}
					cfg.MCPServers = nil
					cfg.WebSearch = false
					m.Config = &cfg
					m.reviewer = newToolReviewer(&cfg)
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
					defer cancel()
					m.ctx = ctx
					require.NoError(t, m.setupStreamContext(c.Prompt))
					api, mod, err := m.resolveModel(&cfg)
					require.NoError(t, err)
					configs, err := m.buildProviderConfigs(mod, api)
					require.NoError(t, err)
					_, err = m.resolveThinkWithOllama(&mod, &configs.Anthropic, &configs.Google, &configs.Ollama, &configs.OpenAI)
					require.NoError(t, err)
					client, err := newStreamClient(modelProtocol(mod), configs.Anthropic, configs.Google, configs.Ollama, configs.OpenAI)
					require.NoError(t, err)
					reg, err := m.buildToolRegistryForProvider(ctx, &cfg, websearch.Config{}, c.Prompt, client)
					require.NoError(t, err)
					defer reg.Close()
					require.NoError(t, m.injectToolSelectionPrompt(reg))
					messages, specs := generationPromptArm(t, m.messages, reg.Specs(), arm == "A")
					r := generationRun{Case: c.ID, Arm: arm, Repeat: repeat + 1, Model: mod.Name, OS: runtime.GOOS, Minimal: minimal, PolicyFingerprint: fingerprint, FirstRoundOnly: firstOnly, Outcome: evals.GenerationOutcome{Query: c.Query}}
					r.BaselineRevision = os.Getenv("MODS_GENERATION_BASELINE")
					if r.BaselineRevision == "" {
						r.BaselineRevision = "original"
					}
					promptBytes, err := json.Marshal(struct {
						Messages []proto.Message
						Tools    []proto.ToolSpec
					}{messages, specs})
					require.NoError(t, err)
					r.PromptFingerprint = fmt.Sprintf("%x", sha256.Sum256(promptBytes))
					r.MaxTokens = 4096
					started := time.Now()
					defer func() {
						r.ElapsedMS = time.Since(started).Milliseconds()
						r.ZeroReviewCompletion = r.Outcome.ZeroReviewCompletion()
						reports = append(reports, r)
						data, _ := json.Marshal(r)
						t.Logf("generation report: %s", data)
					}()
					maxTokens := int64(4096)
					req := proto.Request{Messages: messages, API: mod.API, Model: mod.Name, Tools: specs, Temperature: ptrOrNil(float64(0)), MaxTokens: &maxTokens, TrackUsage: true}
					if !firstOnly {
						zero := 0
						r.Outcome.ActualReviewPrompts = &zero // raw mode cannot display a review
						productionCaller := m.toolResultCaller(reg, &cfg)
						req.ResultCaller = func(call proto.ToolCallRequest) (proto.ToolResult, error) {
							o := generationObserve(m, reg, call)
							r.Calls = append(r.Calls, o)
							r.Outcome.ToolCalls++
							if o.Effect == "write" {
								r.Outcome.UnexpectedWrites++
							}
							result, err := productionCaller(call)
							r.Outcome.ObserveReorganization(err)
							if errors.Is(err, errReviewUnavailable) {
								r.Outcome.ApprovalUnavailable++
							}
							return result, err
						}
					}
					st := client.Request(ctx, req)
					defer st.Close()
					for round := 0; round < 6; round++ {
						var answer strings.Builder
						for st.Next() {
							chunk, err := st.Current()
							if err != nil && !errors.Is(err, stream.ErrNoContent) {
								r.Error = err.Error()
								return
							}
							answer.WriteString(chunk.Content)
						}
						r.Answer = answer.String()
						usage := st.Usage()
						r.InputTokens = int64(usage.InputTokens)
						r.OutputTokens = int64(usage.OutputTokens)
						if err := st.Err(); err != nil {
							r.Error = err.Error()
							return
						}
						if firstOnly {
							for _, msg := range st.Messages() {
								if msg.Role == proto.RoleAssistant {
									for _, call := range msg.ToolCalls {
										r.Calls = append(r.Calls, generationObserve(m, reg, proto.ToolCallRequest{Name: call.Function.Name, Arguments: call.Function.Arguments}))
									}
								}
							}
							r.Outcome.ToolCalls = len(r.Calls)
							if len(r.Calls) > 0 {
								r.Outcome.ToolRounds = 1
							}
							return
						}
						results := st.CallTools()
						if len(results) == 0 {
							r.Outcome.Completed = true
							break
						}
						r.Outcome.ToolRounds++
					}
					if !r.Outcome.Completed {
						r.Error = "tool round limit reached"
					}
					correct := r.Outcome.Completed && evals.CheckGenerationAnswer(strings.TrimSpace(r.Answer), c.Expected) == nil
					for name, content := range evals.CommandGenerationFiles() {
						data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
						if err != nil || string(data) != content {
							correct = false
						}
					}
					fileCount := 0
					err = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
						if err != nil {
							return err
						}
						if !entry.IsDir() {
							fileCount++
						}
						return nil
					})
					if err != nil || fileCount != len(evals.CommandGenerationFiles()) {
						correct = false
					}
					if len(c.Expected) > 0 {
						r.Outcome.Correct = &correct
					}
					// Quality is observed, not a flaky CI gate. Transport/setup errors
					// and correctness failures remain explicit in every saved report.
				})
			}
		}
	}
	require.NotEmpty(t, reports, "case filter matched no scenarios")
}
