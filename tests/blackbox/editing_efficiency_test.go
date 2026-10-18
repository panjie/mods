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
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/session"
)

// Uses the existing isolated real-provider harness. A mechanical transcript
// score is separate from this final-artifact check; neither is a live UI test.
func TestRealAIBlackBoxSmallEdit(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("fixture checker requires Node.js")
	}
	for _, scenario := range []string{evals.CaseClearModification, evals.CaseEditRecovery, evals.CaseCheckRecovery, evals.CaseVisualVerification} {
		t.Run(scenario, func(t *testing.T) {
			h := newRealAIHarnessWithTools(t, true, true, "never")
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
			logEditingScore(t, h, scenario)
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
}

func TestRealAIBlackBoxMissingEditTarget(t *testing.T) {
	h := newRealAIHarness(t, true)
	if !h.profile.filesystemTools {
		t.Skip("provider does not support tools")
	}
	const original = "fontSize: 20\n"
	for _, name := range []string{"Popup.qml", "Settings.qml"} {
		if err := os.WriteFile(filepath.Join(h.cwd, name), []byte(original), 0600); err != nil {
			t.Fatal(err)
		}
	}
	h.run(t, "", "Reduce the font size in one of the two QML screens in this fixture. I have not specified which screen; ask me which one before inspecting or editing. Do not choose for me.")
	logEditingScore(t, h, evals.CaseMissingModificationContext)
	for _, name := range []string{"Popup.qml", "Settings.qml"} {
		data, err := os.ReadFile(filepath.Join(h.cwd, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != original {
			t.Fatalf("changed %s before target was supplied", name)
		}
	}
}

func logEditingScore(t *testing.T, h *realAIHarness, caseID string) {
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
	c, ok := evals.CaseByID(caseID)
	if !ok {
		t.Fatal("unknown case", caseID)
	}
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
}
