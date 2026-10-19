//go:build integration

package blackbox_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/panjie/mods/internal/evals"
	"github.com/panjie/mods/internal/prompts"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/session"
)

// Uses the existing isolated real-provider harness. A mechanical transcript
// score is separate from this final-artifact check; neither is a live UI test.
func TestRealAIBlackBoxSmallEdit(t *testing.T) {
	forEachPromptMode(t, func(t *testing.T, minimal bool) {
		if _, err := exec.LookPath("node"); err != nil {
			t.Skip("fixture checker requires Node.js")
		}
		for _, scenario := range []string{evals.CaseClearModification, evals.CaseEditRecovery, evals.CaseCheckRecovery, evals.CaseVisualVerification} {
			t.Run(scenario, func(t *testing.T) {
				h := newRealAIHarnessWithPromptMode(t, true, true, "never", minimal)
				if !h.profile.filesystemTools {
					t.Skip("provider does not support tools")
				}
				for _, name := range []string{"ResultContent.qml", "Markdown.js", "check.mjs"} {
					data, err := os.ReadFile(filepath.Join("testdata", "editing", name))
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(h.cwd, name), data, 0600); err != nil {
						t.Fatal(err)
					}
				}
				prompt := "In this synthetic plugin fixture, reduce ResultContent.qml body from theme title to body. Shrink Markdown.js H1-H3 and preserve non-increasing heading sizes H1 through H6. Choose reasonable ratios yourself. Preserve check.mjs unchanged and run node check.mjs to verify. Only edit these fixture files; no real desktop configuration."
				if scenario == "edit-recovery" {
					prompt += " An old note says baseSize uses Style.font.large; the current file is authoritative if that note is stale."
				}
				if scenario == "check-recovery" {
					// Simulate a partial fix containing the H3/H4 regression in the log.
					data := "function headingSize(base, level) { return level === 1 ? Math.round(base * 1.25) : level === 2 ? Math.round(base * 1.1) : level === 3 ? base : base + 1; }\n"
					if err := os.WriteFile(filepath.Join(h.cwd, "Markdown.js"), []byte(data), 0600); err != nil {
						t.Fatal(err)
					}
					prompt += " A previous partial fix failed validation. Run the check first to inspect the failure, then complete the fix."
				}
				if scenario == evals.CaseVisualVerification {
					prompt += " Also capture a screenshot if a compatible renderer is available. Otherwise state that no screenshot was captured. Do not install dependencies or present generated placeholder graphics as a screenshot."
				}
				stdout, _ := h.run(t, "", prompt)
				logExecutionScore(t, h, scenario)
				// Check with the trusted original, independent of any model edits.
				checker, err := os.ReadFile(filepath.Join("testdata", "editing", "check.mjs"))
				if err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command("node", "--input-type=module", "-e", string(checker))
				cmd.Dir = h.cwd
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("artifact check failed: %v\n%s\nmodel: %s", err, out, stdout)
				}
				t.Logf("provider=%s scenario=%s response=%s", h.profile.name, scenario, stdout)
			})
		}
	})
}

func (h *realAIHarness) messages(t *testing.T) []proto.Message {
	t.Helper()
	var dataHome string
	for _, entry := range h.env {
		if value, ok := strings.CutPrefix(entry, "XDG_DATA_HOME="); ok {
			dataHome = value
		}
	}
	if dataHome == "" {
		t.Fatal("isolated data home missing")
	}
	// The child has exited; this is its private fixture DB, never the user's DB.
	db, err := session.Open(filepath.Join(dataHome, "mods", "sessions", "mods.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	head, err := db.FindHEAD()
	if err != nil {
		t.Fatal(err)
	}
	var messages []proto.Message
	if err := db.ReadMessages(head.ID, &messages); err != nil {
		t.Fatal(err)
	}

	var system []string
	for _, message := range messages {
		if message.Role == proto.RoleSystem {
			system = append(system, message.Content)
		}
	}
	joined := strings.Join(system, "\n")
	for _, instruction := range []string{prompts.Identity, prompts.Workflow} {
		if strings.Contains(joined, instruction) == h.minimal {
			t.Fatalf("saved system instructions disagree with minimal=%t", h.minimal)
		}
	}
	return messages
}

func logExecutionScore(t *testing.T, h *realAIHarness, caseID string) evals.Report {
	t.Helper()
	c, ok := evals.CaseByID(caseID)
	if !ok {
		t.Fatal("unknown case", caseID)
	}
	messages := h.messages(t)
	report := evals.Score(messages, c)
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("execution report: %s", encoded)
	for _, dimension := range report.Dimensions {
		// Budgets remain observational: do not reward fewer calls over a
		// correct artifact. Clarification expectations are scenario-specific.
		if dimension.Name != "tool-budget" && !dimension.Passed {
			t.Errorf("%s: %s", dimension.Name, dimension.Evidence)
		}
	}
	for _, message := range messages {
		if message.Role == proto.RoleAssistant {
			for _, call := range message.ToolCalls {
				t.Logf("fixture call %s: %.2000s", call.Function.Name, call.Function.Arguments)
			}
		} else if message.Role == proto.RoleTool && t.Failed() {
			t.Logf("fixture result: %.2000s", message.Content)
		}
	}
	return report
}
