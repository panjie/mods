package app

import (
	"fmt"
	"strings"
	"sync"

	"github.com/panjie/mods/internal/approval"
)

// The command preflight is advisory. It nudges the model toward command shapes
// that are easier to review, but it never decides whether a command may run:
// once the correction budget is spent the call goes to ordinary approval, where
// the user decides. Nothing is silently stopped.
//
// Pre-written script payloads are exempt from the nudges entirely. A skill's
// script is a file the reviewer can inspect, and asking the model to rewrite it
// is how those scripts break.
const commandCorrectionBudget = 2

type commandPreflightGate struct {
	mu      sync.Mutex
	enabled bool
	used    int
}

func newCommandPreflightGate(cfg *Config) *commandPreflightGate {
	enabled := cfg != nil && cfg.ReviewMode != ReviewNever
	return &commandPreflightGate{enabled: enabled}
}

// check returns a correction error while the advisory budget lasts, and nil
// afterwards so the call continues into approval. It never rejects a call.
func (g *commandPreflightGate) check(tool string, assessment approval.CommandAssessment) error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.enabled || (assessment.StaticRead && assessment.Effect == approval.EffectRead) {
		return nil
	}
	if assessment.Reviewability.ScriptFilePayload {
		return nil
	}
	if !assessment.RequiresSimplification() {
		return nil
	}
	if g.used >= commandCorrectionBudget {
		return nil
	}
	g.used++
	return commandSimplificationError{message: commandSimplificationMessage(assessment)}
}

type commandSimplificationError struct {
	message string
}

func (e commandSimplificationError) Error() string             { return e.message }
func (e commandSimplificationError) CorrectionSuggested() bool { return true }

func commandSimplificationMessage(assessment approval.CommandAssessment) string {
	reviewability := assessment.Reviewability
	var reasons []string
	for _, reason := range reviewability.Reasons {
		switch reason {
		case approval.ReviewabilitySingleProgramInShell:
			reasons = append(reasons, "runs one executable through a shell")
		case approval.ReviewabilityMultipleIndependent:
			reasons = append(reasons, fmt.Sprintf("combines %d top-level actions", assessment.Shape.TopLevelActions))
		case approval.ReviewabilityMixedReadWrite:
			reasons = append(reasons, "mixes inspection and mutation")
		case approval.ReviewabilityDynamicWriteTarget:
			reasons = append(reasons, "writes to a runtime-resolved path")
		case approval.ReviewabilityMultipleDynamicTargets:
			reasons = append(reasons, "uses multiple runtime-resolved paths")
		case approval.ReviewabilityDecorativeOutput:
			reasons = append(reasons, "adds presentation-only output")
		case approval.ReviewabilityNestedShellHost:
			reasons = append(reasons, "nests a shell host inside a shell tool")
		case approval.ReviewabilityCommandPassedAsScript:
			reasons = append(reasons, "passes a known executable name where the shell expects a script path")
		}
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "is harder to review than necessary")
	}
	message := "command needs simplification: " + strings.Join(reasons, "; ") + ". "
	if assessment.Shape.Opaque || reviewability.Level == approval.ReviewabilityOpaque {
		message += "Its payload cannot be analyzed, so approval is requested for exactly this command. "
	}
	if reviewability.RecommendedTool == "process_run" {
		return message + "Prefer process_run with literal argv instead of wrapping the executable in shell syntax."
	}
	if reviewability.RecommendedTool == "shell_run" || reviewability.RecommendedTool == "powershell_run" {
		return message + "Prefer " + reviewability.RecommendedTool + " and pass the shell source directly instead of nesting a shell host in process_run."
	}
	if containsReviewabilityReason(reviewability.Reasons, approval.ReviewabilityNestedShellHost) {
		return message + "Prefer passing the shell source directly instead of nesting sh -c, bash -c, eval, or exec inside it."
	}
	if containsReviewabilityReason(reviewability.Reasons, approval.ReviewabilityDynamicWriteTarget) {
		return message + "Resolving the target in a separate read-only call and then using the literal absolute path makes the target reviewable."
	}
	if len(assessment.DynamicTargets) > 0 {
		return message + "Resolving runtime paths in one short read-only call and then using the literal paths keeps the effect reviewable."
	}
	return message + "Splitting capability discovery, mutation, and verification into separate calls, and dropping decorative echo/printf separators, makes effects and targets reviewable; keep necessary pipelines intact. This is advice only: keep the operation's behavior identical, never rewrite an existing script file to satisfy review, and re-send the same command if it is already the right one."
}

func containsReviewabilityReason(reasons []approval.ReviewabilityReason, target approval.ReviewabilityReason) bool {
	for _, reason := range reasons {
		if reason == target {
			return true
		}
	}
	return false
}
