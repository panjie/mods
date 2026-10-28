// Package evals scores a finished mods session against the diagnosis rubric
// defined by the cases and limits in this package.
//
// The rubric is evaluated from the persisted message transcript, not from the
// model's prose alone. Each dimension is deliberately mechanical so a score is
// reproducible and can be compared before and after a prompt change. It does
// not run the model; use the prompt-policy tests in internal/prompts for the
// always-on prompt contract.
package evals

import (
	"fmt"
	"os"
	"strings"

	"github.com/panjie/mods/internal/proto"
)

// DefaultMaxToolCalls bounds a diagnosis that already has a concrete symptom.
// A vague report must first ask, so the budget starts on the next turn.
const DefaultMaxToolCalls = 20

const (
	CaseAmbiguousSymptom           = "ambiguous-symptom"
	CaseSpecificArtifact           = "specific-artifact"
	CaseExpectedProbe              = "expected-probe-value"
	CaseClearModification          = "clear-modification"
	CaseMissingModificationContext = "missing-modification-context"
	CaseEditRecovery               = "edit-recovery"
	CaseCheckRecovery              = "check-recovery"
	CaseVisualVerification         = "visual-verification"
	CaseCodeStatistics             = "code-statistics"
	CaseSimpleAggregation          = "simple-aggregation"
	CaseAmbiguousSymptomFirstTurn  = "ambiguous-symptom-first-turn"
	CaseInstalledSkill             = "installed-skill"
)

// Case is one rubric scenario. Empty optional fields disable their dimension,
// except MaxToolCalls which falls back to DefaultMaxToolCalls.
type Case struct {
	ID                     string `json:"id"`
	Description            string `json:"description"`
	RequiresClarification  bool   `json:"requires_clarification"`
	MaxToolCalls           int    `json:"max_tool_calls"`
	TargetPath             string `json:"target_path,omitempty"`
	ExpectedProbeToken     string `json:"expected_probe_token,omitempty"`
	RequiresEvidenceLabels bool   `json:"requires_evidence_labels"`
	ForbidsClarification   bool   `json:"forbids_clarification,omitempty"`
	ForbidsProbing         bool   `json:"forbids_probing,omitempty"`
}

// Budget returns the effective tool-call budget for the case.
func (c Case) Budget() int {
	if c.MaxToolCalls > 0 {
		return c.MaxToolCalls
	}
	return DefaultMaxToolCalls
}

// Dimension is one pass/fail rubric line with the evidence that decided it.
type Dimension struct {
	Name     string `json:"name"`
	Passed   bool   `json:"passed"`
	Evidence string `json:"evidence"`
}

// Report is the deterministic score of one transcript for one case.
type Report struct {
	CaseID     string           `json:"case_id"`
	UserTurns  int              `json:"user_turns"`
	ToolCalls  int              `json:"tool_calls"`
	FirstTool  string           `json:"first_tool,omitempty"`
	Dimensions []Dimension      `json:"dimensions"`
	Notes      []string         `json:"notes,omitempty"`
	Metrics    ExecutionMetrics `json:"metrics"`
}

// Passed reports whether every dimension passed.
func (r Report) Passed() bool {
	for _, dimension := range r.Dimensions {
		if !dimension.Passed {
			return false
		}
	}
	return true
}

// PassedCount and Total support report rendering.
func (r Report) PassedCount() int {
	passed := 0
	for _, dimension := range r.Dimensions {
		if dimension.Passed {
			passed++
		}
	}
	return passed
}

// Total returns the number of scored dimensions.
func (r Report) Total() int { return len(r.Dimensions) }

// Cases returns the built-in diagnosis scenarios. specific-artifact and
// expected-probe-value need the caller to supply TargetPath or
// ExpectedProbeToken; the score command enforces that.
func Cases() []Case {
	return []Case{
		{ID: CaseSimpleAggregation, Description: "Sum a fixture column with a simple command or pipeline.", ForbidsClarification: true, MaxToolCalls: 3},
		{ID: CaseAmbiguousSymptomFirstTurn, Description: "Clarify an ambiguous symptom without starting inspection in the first turn.", RequiresClarification: true, ForbidsProbing: true, MaxToolCalls: 1},
		{ID: CaseInstalledSkill, Description: "Load an installed fixture skill and independently verify its result.", ForbidsClarification: true, MaxToolCalls: 3},
		{ID: CaseCodeStatistics, Description: "Count tracked source files and reuse one collection for totals and breakdowns.", ForbidsClarification: true, MaxToolCalls: 4},
		{ID: CaseClearModification, Description: "An explicit small edit with enough context; choose routine details without asking.", ForbidsClarification: true},
		{ID: CaseMissingModificationContext, Description: "A modification with a missing target; clarify before inspection.", RequiresClarification: true},
		{ID: CaseEditRecovery, Description: "A stale edit context requires re-reading and correcting the edit."},
		{ID: CaseCheckRecovery, Description: "A failing relevant check requires further diagnosis and correction."},
		{ID: CaseVisualVerification, Description: "The user explicitly requests a screenshot; visual verification is appropriate."},
		{
			ID:                     CaseAmbiguousSymptom,
			Description:            "A vague report that a change does not show up; ask before probing.",
			RequiresClarification:  true,
			MaxToolCalls:           DefaultMaxToolCalls,
			RequiresEvidenceLabels: true,
		},
		{
			ID:                     CaseSpecificArtifact,
			Description:            "The report names the edited artifact; inspect the live artifact before trusting a copy.",
			MaxToolCalls:           DefaultMaxToolCalls,
			RequiresEvidenceLabels: true,
		},
		{
			ID:                     CaseExpectedProbe,
			Description:            "The diagnosis depends on a probe value; surface the expected value in the transcript.",
			MaxToolCalls:           DefaultMaxToolCalls,
			RequiresEvidenceLabels: true,
		},
	}
}

// CaseByID resolves a built-in case.
func CaseByID(id string) (Case, bool) {
	for _, c := range Cases() {
		if c.ID == id {
			return c, true
		}
	}
	return Case{}, false
}

var evidenceMarkers = [][]string{
	{"observed", "观察到", "已观察"},
	{"inferred", "推断", "推测"},
	{"unverified", "未验证", "未确认"},
}

var writeTools = map[string]bool{
	"fs_write_file":  true,
	"fs_apply_patch": true,
	"fs_replace":     true,
	"fs_copy":        true,
}

// Score evaluates one transcript against one case.
func Score(messages []proto.Message, c Case) Report {
	report := Report{CaseID: c.ID}
	report.Metrics = MeasureExecution(messages)
	var toolNames []string
	var firstProbe string
	probingStarted := false
	clarified := false
	targetSeen := false
	tokenSeen := false
	tempWrites := 0
	firstTargetCall := -1
	firstTempWrite := -1
	finalText := ""

	recordTarget := func(index int, value string) {
		if c.TargetPath == "" || targetSeen {
			return
		}
		if strings.Contains(value, c.TargetPath) {
			targetSeen = true
			firstTargetCall = index
		}
	}
	recordToken := func(value string) {
		if c.ExpectedProbeToken != "" && !tokenSeen && strings.Contains(value, c.ExpectedProbeToken) {
			tokenSeen = true
		}
	}

	for _, message := range messages {
		switch message.Role {
		case proto.RoleUser:
			if strings.TrimSpace(message.Content) != "" {
				report.UserTurns++
			}
		case proto.RoleTool:
			recordTarget(len(toolNames), message.Content)
			recordToken(message.Content)
		case proto.RoleAssistant:
			if strings.TrimSpace(message.Content) != "" {
				finalText = message.Content
			}
			if !probingStarted && len(message.ToolCalls) == 0 && asksQuestion(message.Content) {
				clarified = true
			}
		}
		// Tool results repeat the original call metadata; only assistant
		// messages represent new invocations.
		if message.Role != proto.RoleAssistant {
			continue
		}
		for _, call := range message.ToolCalls {
			name := call.Function.Name
			arguments := string(call.Function.Arguments)
			index := len(toolNames)
			toolNames = append(toolNames, name)
			recordTarget(index, arguments)
			recordToken(arguments)
			if name == "request_user_input" {
				if !probingStarted {
					clarified = true
				}
				continue
			}
			if firstProbe == "" {
				firstProbe = name
			}
			probingStarted = true
			if writeTools[name] && isTempPath(arguments) {
				tempWrites++
				if firstTempWrite == -1 {
					firstTempWrite = index
				}
			}
		}
	}
	recordToken(finalText)

	report.ToolCalls = len(toolNames)
	if len(toolNames) > 0 {
		report.FirstTool = toolNames[0]
	}

	if c.RequiresClarification {
		report.Dimensions = append(report.Dimensions, Dimension{
			Name:     "clarify-first",
			Passed:   clarified,
			Evidence: clarifyEvidence(clarified, firstProbe),
		})
	}
	if c.ForbidsClarification {
		report.Dimensions = append(report.Dimensions, Dimension{
			Name: "no-unneeded-input", Passed: report.Metrics.UserInputCalls == 0,
			Evidence: fmt.Sprintf("%d user-input call(s); manually review any text questions", report.Metrics.UserInputCalls),
		})
	}
	if c.ForbidsProbing {
		report.Dimensions = append(report.Dimensions, Dimension{
			Name: "no-premature-probe", Passed: firstProbe == "",
			Evidence: fmt.Sprintf("first inspection tool: %q", firstProbe),
		})
	}
	if strings.Contains(c.ID, "modification") || c.ID == CaseEditRecovery || c.ID == CaseCheckRecovery || c.ID == CaseVisualVerification {
		report.Notes = append(report.Notes, "Transcript metrics do not establish task correctness; verify the final artifact and manually review clarification, recovery, evidence and stopping decisions.")
	}
	if c.ID == CaseCodeStatistics {
		report.Notes = append(report.Notes, "The four-call budget is an observational target, not a runtime limit. Independently verify file scope, line-count semantics, totals and breakdowns; fewer calls do not compensate for incorrect counts. Compare runs with the same model and review mode.")
	}
	report.Dimensions = append(report.Dimensions, Dimension{
		Name:     "tool-budget",
		Passed:   report.ToolCalls <= c.Budget(),
		Evidence: fmt.Sprintf("%d tool call(s), limit %d", report.ToolCalls, c.Budget()),
	})
	if c.TargetPath != "" {
		report.Dimensions = append(report.Dimensions, Dimension{
			Name:     "live-target",
			Passed:   targetSeen,
			Evidence: targetEvidence(targetSeen, c.TargetPath),
		})
	}
	if c.ExpectedProbeToken != "" {
		report.Dimensions = append(report.Dimensions, Dimension{
			Name:     "expected-value",
			Passed:   tokenSeen,
			Evidence: probeEvidence(tokenSeen, c.ExpectedProbeToken),
		})
	}
	if c.RequiresEvidenceLabels {
		labels := evidenceLabelCount(finalText)
		report.Dimensions = append(report.Dimensions, Dimension{
			Name:     "evidence-labels",
			Passed:   labels >= 2,
			Evidence: fmt.Sprintf("%d of 3 evidence groups in the final answer", labels),
		})
	}

	if tempWrites > 0 {
		report.Notes = append(report.Notes, fmt.Sprintf("%d write tool call(s) targeted a temporary path", tempWrites))
	}
	if firstTargetCall >= 0 && firstTempWrite >= 0 && firstTempWrite < firstTargetCall {
		report.Notes = append(report.Notes, "a temporary write preceded the first live-target inspection")
	}
	if finalText != "" && isTempPath(strings.ReplaceAll(finalText, `\\`, `\`)) {
		report.Notes = append(report.Notes, "the final answer cites a temporary path")
	}
	if len(toolNames) == 0 {
		report.Notes = append(report.Notes, "no tool calls were recorded")
	}
	if finalText == "" {
		report.Notes = append(report.Notes, "no final assistant text was recorded")
	}
	return report
}

func clarifyEvidence(clarified bool, firstProbe string) string {
	if clarified {
		return "asked before any probe"
	}
	if firstProbe == "" {
		return "no clarifying question and no probe"
	}
	return "first probe was " + firstProbe + " before any question"
}

func targetEvidence(seen bool, target string) string {
	if seen {
		return "inspected " + target
	}
	return "never inspected " + target
}

func probeEvidence(seen bool, token string) string {
	if seen {
		return "expected value " + token + " appears in the transcript"
	}
	return "expected value " + token + " never appears"
}

func evidenceLabelCount(text string) int {
	lower := strings.ToLower(text)
	count := 0
	for _, markers := range evidenceMarkers {
		for _, marker := range markers {
			if strings.Contains(lower, marker) {
				count++
				break
			}
		}
	}
	return count
}

func asksQuestion(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	// A clarification can be followed by an explanatory sentence. Checking
	// only the final punctuation rejects ordinary multi-sentence questions.
	return strings.ContainsAny(text, "?？")
}

// isTempPath reports whether a tool argument or message mentions the system
// temporary area. Backslashes are normalized so JSON-escaped Windows paths
// match os.TempDir.
func isTempPath(value string) bool {
	value = strings.ReplaceAll(value, `\\`, `\`)
	if strings.Contains(value, "/tmp/") {
		return true
	}
	tempDir := os.TempDir()
	return tempDir != "" && strings.Contains(value, tempDir)
}
