package app

import (
	"fmt"
	"strings"
	"sync"

	"github.com/panjie/mods/internal/approval"
)

// commandPreflightGate provides at most one model-facing correction per
// request. It never approves, executes, rewrites, or splits a command.
type commandPreflightGate struct {
	mu      sync.Mutex
	enabled bool
	used    bool
}

func newCommandPreflightGate(cfg *Config) *commandPreflightGate {
	enabled := cfg != nil && !cfg.Minimal && cfg.ReviewMode != ReviewNever
	return &commandPreflightGate{enabled: enabled}
}

func (g *commandPreflightGate) check(tool string, assessment approval.CommandAssessment) error {
	if g == nil || !assessment.Reviewability.ShouldCorrect {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.enabled || g.used {
		return nil
	}
	g.used = true
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
	if reviewability.RecommendedTool == "process_run" {
		return message + "Retry with process_run and literal argv; do not wrap the executable in shell syntax."
	}
	if reviewability.RecommendedTool == "shell_run" || reviewability.RecommendedTool == "powershell_run" {
		return message + "Retry with " + reviewability.RecommendedTool + " and pass the shell source directly instead of nesting a shell host in process_run."
	}
	if containsReviewabilityReason(reviewability.Reasons, approval.ReviewabilityNestedShellHost) {
		return message + "Retry with the same tool and pass the shell source directly instead of nesting sh -c, bash -c, eval, or exec inside it."
	}
	if containsReviewabilityReason(reviewability.Reasons, approval.ReviewabilityDynamicWriteTarget) {
		return message + "Resolve the target in a separate read-only call, then mutate the returned literal absolute path."
	}
	if len(assessment.DynamicTargets) > 0 {
		return message + "Resolve runtime paths in one short read-only call, then use the returned literal absolute paths in later calls."
	}
	return message + "Retry as separate single-purpose calls for capability discovery, path inspection, mutation, and verification. Drop decorative echo/printf separators and keep necessary pipelines intact. Simple read-only commands run without review; long multi-action chains always require it."
}

func containsReviewabilityReason(reasons []approval.ReviewabilityReason, target approval.ReviewabilityReason) bool {
	for _, reason := range reasons {
		if reason == target {
			return true
		}
	}
	return false
}
