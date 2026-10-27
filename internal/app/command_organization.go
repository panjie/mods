package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"slices"
	"strings"
	"sync"

	"github.com/panjie/mods/internal/commandanalysis"
	"github.com/panjie/mods/internal/config"
	"github.com/panjie/mods/internal/proto"
	toolregistry "github.com/panjie/mods/internal/tools"
)

// commandOrganizationState belongs to one user request. It is deliberately
// independent of checkpoints and provider/tool registry lifetimes.
type commandOrganizationState struct {
	ctx            context.Context
	cancel         context.CancelFunc
	mu             sync.Mutex
	used           bool
	closed         bool
	userContent    string
	feedbackIDs    map[string]struct{}
	feedbackInputs map[[32]byte]struct{}
	sourceTexts    []string
	sourcesSet     bool
}

func (s *commandOrganizationState) bindSourceContext(messages []proto.Message) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.sourcesSet {
		return
	}
	for _, message := range messages {
		if message.Role == proto.RoleUser {
			s.sourceTexts = append(s.sourceTexts, message.Content)
		}
	}
	s.sourcesSet = true
}

func (s *commandOrganizationState) protectUserResponse(text string) {
	if s == nil {
		return
	}
	var response toolregistry.UserInputResponse
	err := json.Unmarshal([]byte(text), &response)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if err != nil {
		s.userContent = ""
		return
	} // Provenance cannot be recovered.
	answers := append([]string{response.Answer}, response.Answers...)
	for _, field := range response.Form {
		answers = append(answers, field.Answer)
	}
	for _, answer := range answers {
		if answer == "" {
			continue
		}
		if len(s.sourceTexts) >= 128 {
			s.userContent = ""
			return
		}
		s.sourceTexts = append(s.sourceTexts, answer)
	}
}

func newCommandOrganizationState(ctx context.Context, userContent string) *commandOrganizationState {
	ctx, cancel := context.WithCancel(ctx)
	return &commandOrganizationState{ctx: ctx, cancel: cancel, userContent: userContent, feedbackIDs: make(map[string]struct{}), feedbackInputs: make(map[[32]byte]struct{})}
}

func (m *Mods) currentCommandOrganization() *commandOrganizationState {
	m.commandOrganizationMu.Lock()
	defer m.commandOrganizationMu.Unlock()
	return m.commandOrganization
}

func (m *Mods) replaceCommandOrganization(state *commandOrganizationState) {
	m.commandOrganizationMu.Lock()
	previous := m.commandOrganization
	m.commandOrganization = state
	m.commandOrganizationMu.Unlock()
	if previous != nil && previous != state {
		previous.close()
	}
}

func (m *Mods) closeCommandOrganization() {
	m.commandOrganizationMu.Lock()
	state := m.commandOrganization
	m.commandOrganization = nil
	m.commandOrganizationMu.Unlock()
	state.close()
}

func (s *commandOrganizationState) close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.cancel()
	s.closed = true
	s.userContent = ""
	s.feedbackIDs = nil
	s.feedbackInputs = nil
	s.sourceTexts = nil
	s.mu.Unlock()
}

func (s *commandOrganizationState) markManual(callID, input string) {
	if s == nil || callID == "" && input == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		// Manual requests do not refund or consume the automatic opportunity.
		// If bookkeeping is exhausted, conservatively disable automatic feedback.
		if len(s.feedbackIDs) >= 128 || len(s.feedbackInputs) >= 128 {
			s.userContent = ""
			return
		}
		s.feedbackIDs[callID] = struct{}{}
		if input != "" {
			s.feedbackInputs[sha256.Sum256([]byte(input))] = struct{}{}
		}
	}
}

// consumeAutomatic atomically spends the single per-request opportunity.
func (s *commandOrganizationState) consumeAutomatic(ctx context.Context, callID string, input string, assessment *commandanalysis.CommandAssessment, sourceFragments ...string) []string {
	if s == nil || ctx.Err() != nil || input == "" {
		return nil
	}
	reasons := commandOrganizationReasons(assessment)
	if len(reasons) == 0 {
		return nil
	}
	sourceFragments = append(slices.Clone(sourceFragments), assessment.Structure.PayloadSources...)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.used || ctx.Err() != nil || s.ctx.Err() != nil {
		return nil
	}
	if _, alreadyFeedback := s.feedbackIDs[callID]; alreadyFeedback {
		return nil
	}
	if _, alreadyFeedback := s.feedbackInputs[sha256.Sum256([]byte(input))]; alreadyFeedback {
		return nil
	}
	// A command copied verbatim from the user's request is treated as user
	// supplied. This conservative exact match avoids parsing natural language.
	if strings.TrimSpace(s.userContent) == "" || strings.Contains(s.userContent, "```") || strings.Contains(s.userContent, "~~~") || containsCommandSource(s.userContent, input) {
		return nil
	}
	for _, fragment := range sourceFragments {
		if fragment != "" && strings.Contains(s.userContent, fragment) {
			return nil
		}
	}
	for _, text := range s.sourceTexts {
		if containsCommandSource(text, input) {
			return nil
		}
		for _, fragment := range sourceFragments {
			if fragment != "" && strings.Contains(text, fragment) {
				return nil
			}
		}
	}
	s.used = true
	s.feedbackIDs[callID] = struct{}{}
	return reasons
}

// Whitespace normalization only excludes candidates; it is never used to
// equate commands for execution or authorization.
func containsCommandSource(text, source string) bool {
	return strings.Contains(text, source) || strings.Contains(strings.Join(strings.Fields(text), " "), strings.Join(strings.Fields(source), " "))
}

// Pure organization policy; these facts never authorize an operation.
func commandOrganizationReasons(a *commandanalysis.CommandAssessment) []string {
	if a == nil || a.Structure == nil || a.Evidence == commandanalysis.EvidenceTrust || a.StaticRead || a.Effect == commandanalysis.EffectRead || a.Reviewability.ScriptFilePayload || a.Structure.FilePayload {
		return nil
	}
	// A known write with unmodeled effects is eligible, but missing target
	// coverage alone is not an organization problem.
	if a.Effect != commandanalysis.EffectUnknown && !slices.Contains(a.Gaps, commandanalysis.GapUnknownInvocation) && !slices.Contains(a.Gaps, commandanalysis.GapControlFlow) {
		return nil
	}
	for _, gap := range []commandanalysis.AssessmentGap{commandanalysis.GapParse, commandanalysis.GapAnalysisBudget, commandanalysis.GapAnalysisCancelled} {
		if slices.Contains(a.Gaps, gap) {
			return nil
		}
	}
	s := a.Structure
	var reasons []string
	unmodeledDepth := s.UnmodeledControlFlowDepth
	if slices.Contains(a.Gaps, commandanalysis.GapControlFlow) {
		unmodeledDepth = s.ControlFlowDepth
	}
	if unmodeledDepth >= 2 {
		reasons = append(reasons, "nested_control_flow")
	}
	if unmodeledDepth > 0 && s.LocalStateTargets >= 2 {
		reasons = append(reasons, "stateful_aggregation")
	}
	if s.DynamicEvaluations > 0 {
		reasons = append(reasons, "dynamic_evaluation")
	}
	if s.InlineHostDepth >= 2 {
		reasons = append(reasons, "nested_inline_hosts")
	}
	return reasons
}

func commandOrganizationEnabled(setting config.CommandSimplification, mode config.ReviewMode) bool {
	return setting == config.CommandSimplificationOnce && (mode == config.ReviewAuto || mode == config.ReviewAlways)
}
