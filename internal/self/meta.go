package self

import (
	"fmt"
	"strings"
)

// DefaultEvolveThreshold is the rating at or below which automatic improvement
// triggers when --evolve-threshold is unset.
const DefaultEvolveThreshold = 3

// ShouldTriggerAutoImprove reports whether a rating is low enough to trigger
// automatic improvement for the configured threshold. A zero threshold falls
// back to DefaultEvolveThreshold.
func ShouldTriggerAutoImprove(rating, threshold int) bool {
	if threshold == 0 {
		threshold = DefaultEvolveThreshold
	}
	return rating <= threshold
}

// EvolutionPromptInput carries the primitive inputs needed to assemble the
// automatic-improvement prompt. It deliberately avoids higher-level runtime
// types so the self layer stays decoupled from the kernel.
type EvolutionPromptInput struct {
	Workspace       string
	ConversationID  string
	Rating          int
	Feedback        string
	OriginalRequest string
	ModelOutput     string
}

// AutomaticEvolutionPrompt renders the prompt that drives a self-evolution
// improvement pass. Its stated boundaries mirror the boundary the kernel
// actually enforces (the internal/self directory), so the model is told exactly
// how far it may go. The kernel remains the source of truth: even if this text
// is weakened by self-edits, file writes outside internal/self are still refused.
func AutomaticEvolutionPrompt(in EvolutionPromptInput) string {
	var b strings.Builder
	b.WriteString("Improve mods based on the completed session evaluation.\n\n")
	b.WriteString("Boundaries:\n")
	b.WriteString("- Work only inside the self layer (internal/self) of the current mods workspace.\n")
	b.WriteString("- Do not edit files outside internal/self, including the kernel, providers, DB schema, user home config, system config, or global settings.\n")
	b.WriteString("- Do not create or use an evolution proposal, approval workflow, or plan approval gate.\n")
	b.WriteString("- Make the smallest code, test, or documentation change that directly addresses the feedback, using only files inside internal/self.\n")
	b.WriteString("- Run relevant tests; if the affected area is unclear, run task check and task test.\n")
	b.WriteString("- Stop and explain if the request cannot be implemented safely inside the self layer.\n\n")
	fmt.Fprintf(&b, "Workspace: %s\n", in.Workspace)
	fmt.Fprintf(&b, "Conversation ID: %s\n", in.ConversationID)
	fmt.Fprintf(&b, "Rating: %d\n", in.Rating)
	writeAutoSection(&b, "User Feedback", in.Feedback)
	writeAutoSection(&b, "Original Request", in.OriginalRequest)
	writeAutoSection(&b, "Model Output", in.ModelOutput)
	return strings.TrimSpace(b.String())
}

func writeAutoSection(b *strings.Builder, title, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "(empty)"
	}
	fmt.Fprintf(b, "\n%s:\n%s\n", title, value)
}
