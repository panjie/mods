package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/commandanalysis"
	"github.com/panjie/mods/internal/evals"
	"github.com/panjie/mods/internal/prompts"
	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

var updateGenerationBaseline = flag.Bool("update-generation-baseline", false, "record command generation controls after independent decision assertions")

type generationPromptBaseline struct {
	Commit     string `json:"source_commit"`
	General    string `json:"tool_selection_general"`
	Script     string `json:"script_policy"`
	PowerShell string `json:"powershell_intent"`
	Minimal    string `json:"minimal"`
}

func readGenerationPromptBaseline(t testing.TB) generationPromptBaseline {
	t.Helper()
	revision := os.Getenv("MODS_GENERATION_BASELINE")
	require.Contains(t, []string{"", "original", "p1"}, revision)
	path := "testdata/command_generation/prompts_before.json"
	if revision == "p1" {
		path = "testdata/command_generation/prompts_p1.json"
	}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var old generationPromptBaseline
	require.NoError(t, json.Unmarshal(data, &old))
	require.NotEmpty(t, old.General)
	require.NotEmpty(t, old.Script)
	// P0's immutable snapshot predates the platform-guidance change. P1 kept
	// that fragment identical, so it also supplies the original reference.
	if old.PowerShell == "" || old.Minimal == "" {
		data, err := os.ReadFile("testdata/command_generation/prompts_p1.json")
		require.NoError(t, err)
		var p1 generationPromptBaseline
		require.NoError(t, json.Unmarshal(data, &p1))
		old.PowerShell = p1.PowerShell
		old.Minimal = p1.Minimal
	}
	require.NotEmpty(t, old.PowerShell)
	require.NotEmpty(t, old.Minimal)
	return old
}

// Change only the frozen prompt fragments. Schemas, policy and the analyzer
// are shared by both arms. Return copies to avoid contaminating the next arm.
func generationPromptArm(t testing.TB, messages []proto.Message, specs []proto.ToolSpec, before bool) ([]proto.Message, []proto.ToolSpec) {
	t.Helper()
	messages = cloneCompletionMessages(messages)
	specs = append([]proto.ToolSpec(nil), specs...)
	if before {
		old := readGenerationPromptBaseline(t)
		for i := range messages {
			if messages[i].Role == proto.RoleSystem {
				messages[i].Content = strings.ReplaceAll(messages[i].Content, prompts.ToolSelectionGeneral, old.General)
				messages[i].Content = strings.ReplaceAll(messages[i].Content, prompts.Minimal, old.Minimal)
			}
		}
		for i := range specs {
			specs[i].Description = strings.ReplaceAll(specs[i].Description, prompts.ScriptPolicy, old.Script)
			specs[i].Description = strings.ReplaceAll(specs[i].Description, prompts.PowerShellIntentGuidance, old.PowerShell)
		}
	}
	return messages, specs
}

func TestCommandGenerationBaseline(t *testing.T) {
	cases, err := evals.CommandGenerationCases()
	require.NoError(t, err)
	f := newCommandBaselineFixture(t)
	t.Cleanup(commandanalysis.CloseBridge)
	observed := map[string]struct {
		Facts         commandBaselineFacts                 `json:"facts"`
		Shape         commandanalysis.CommandShape         `json:"shape"`
		Reviewability commandanalysis.CommandReviewability `json:"reviewability"`
	}{}
	for _, c := range cases {
		for _, dialect := range []string{"posix", "powershell"} {
			if dialect == "powershell" && runtime.GOOS != "windows" {
				continue // Windows evidence cannot be replaced by cross-compilation.
			}
			t.Run(c.ID+"/"+dialect, func(t *testing.T) {
				command := c.POSIX
				if dialect == "powershell" {
					command = c.PowerShell
				}
				bc := commandBaselineCase{ID: c.ID, Mode: dialect, Command: command}
				a := f.assess(t, bc)
				facts := f.facts(bc, a)
				want := c.Decision
				if dialect == "powershell" && c.PowerShellDecision != "" {
					want = c.PowerShellDecision
				}
				require.Equal(t, approval.Decision(want), facts.Auto, "independent policy assertion")
				observed[c.ID+"/"+dialect] = struct {
					Facts         commandBaselineFacts                 `json:"facts"`
					Shape         commandanalysis.CommandShape         `json:"shape"`
					Reviewability commandanalysis.CommandReviewability `json:"reviewability"`
				}{facts, a.Shape, a.Reviewability}
			})
		}
	}
	if runtime.GOOS == "windows" {
		bc := commandBaselineCase{ID: "original", Mode: "powershell", Command: evals.OriginalCodeStatisticsCommand()}
		a := f.assess(t, bc)
		facts := f.facts(bc, a)
		require.Equal(t, approval.DecisionAsk, facts.Auto)
		require.Equal(t, commandanalysis.EffectUnknown, a.Effect)
		require.NotEmpty(t, a.DynamicTargets)
		observed["original/powershell"] = struct {
			Facts         commandBaselineFacts                 `json:"facts"`
			Shape         commandanalysis.CommandShape         `json:"shape"`
			Reviewability commandanalysis.CommandReviewability `json:"reviewability"`
		}{facts, a.Shape, a.Reviewability}
	}
	path := filepath.Join("testdata", "command_generation", "baseline_"+runtime.GOOS+".json")
	data, err := json.MarshalIndent(observed, "", "  ")
	require.NoError(t, err)
	if *updateGenerationBaseline {
		require.False(t, t.Failed(), "never record observations over failed assertions")
		require.NoError(t, os.WriteFile(path, append(data, '\n'), 0600))
	} else {
		want, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			t.Log("no recorded platform snapshot; independent assertions ran")
			return
		}
		require.NoError(t, err)
		require.JSONEq(t, string(want), string(data))
	}
}

func TestGenerationPromptArmsPreserveSchemaAndInputs(t *testing.T) {
	for _, revision := range []string{"original", "p1"} {
		t.Run(revision, func(t *testing.T) {
			t.Setenv("MODS_GENERATION_BASELINE", revision)
			testGenerationPromptArmsPreserveSchemaAndInputs(t)
		})
	}
}

func testGenerationPromptArmsPreserveSchemaAndInputs(t *testing.T) {
	messages := []proto.Message{{Role: proto.RoleSystem, Content: prompts.ToolSelectionGeneral}, {Role: proto.RoleUser, Content: prompts.ToolSelectionGeneral}, {Role: proto.RoleSystem, Content: prompts.Minimal}}
	specs := []proto.ToolSpec{{Name: "test", Description: prompts.ScriptPolicy + "\n" + prompts.PowerShellIntentGuidance, InputSchema: map[string]any{"type": "object"}}}
	oldMessages, oldSpecs := generationPromptArm(t, messages, specs, true)
	old := readGenerationPromptBaseline(t)
	require.Equal(t, old.General, oldMessages[0].Content)
	require.Equal(t, messages[1], oldMessages[1])
	require.Equal(t, old.Minimal, oldMessages[2].Content)
	require.Equal(t, old.Script+"\n"+old.PowerShell, oldSpecs[0].Description)
	require.Equal(t, specs[0].InputSchema, oldSpecs[0].InputSchema)
	require.Equal(t, prompts.ToolSelectionGeneral, messages[0].Content)
	require.Equal(t, prompts.ScriptPolicy+"\n"+prompts.PowerShellIntentGuidance, specs[0].Description)
}

// Called by paired evaluations, not a permanent assertion against future P2.
func generationPolicyFingerprint(t testing.TB) string {
	t.Helper()
	data, err := os.ReadFile("testdata/command_generation/environment_before.json")
	require.NoError(t, err)
	var manifest struct {
		Hashes map[string]string `json:"source_hashes"`
	}
	require.NoError(t, json.Unmarshal(data, &manifest))
	require.NotEmpty(t, manifest.Hashes)
	for path, want := range manifest.Hashes {
		content, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(path)))
		require.NoError(t, err)
		digest := sha256.Sum256(content)
		require.Equal(t, want, hex.EncodeToString(digest[:]), "P0/P1 policy changed: %s", path)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
