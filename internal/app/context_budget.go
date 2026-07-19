package app

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/skills"
	"github.com/panjie/mods/internal/textutil"
)

const minCurrentUserBytes = 256

// contextBudgetReport contains byte counts only. It is safe to print in debug
// output because it never includes message bodies, tool arguments, or secrets.
type contextBudgetReport struct {
	InputLimit       int
	SystemBytes      int
	ToolBytes        int
	AgentsBytes      int
	SkillsBytes      int
	HistoryBytes     int
	CurrentUserBytes int
	ActiveToolBytes  int
	InputBytes       int
	ResponseReserve  int64

	RemovedHistoryGroups int
	RemovedHistoryMsgs   int
	RemovedToolRounds    int
	TruncatedToolResults int
	OmittedSkills        int
	TruncatedUserBytes   int
	NoLimit              bool
}

type contextBudgeter struct {
	limit           int
	responseReserve int64
	noLimit         bool
	tools           []proto.ToolSpec
	skillCatalog    []skills.Skill
}

func newContextBudgeter(
	maxInputChars int64,
	maxTokens int64,
	noLimit bool,
	tools []proto.ToolSpec,
	skillCatalog []skills.Skill,
) *contextBudgeter {
	limit := 0
	if maxInputChars > 0 {
		if maxInputChars > int64(math.MaxInt) {
			limit = math.MaxInt
		} else {
			limit = int(maxInputChars)
		}
	}
	reserve := int64(0)
	if maxTokens > 0 {
		if maxTokens > math.MaxInt64/4 {
			reserve = math.MaxInt64
		} else {
			reserve = maxTokens * 4
		}
	}
	return &contextBudgeter{
		limit:           limit,
		responseReserve: reserve,
		noLimit:         noLimit,
		tools:           append([]proto.ToolSpec(nil), tools...),
		skillCatalog:    append([]skills.Skill(nil), skillCatalog...),
	}
}

func (b *contextBudgeter) apply(messages []proto.Message) ([]proto.Message, contextBudgetReport, error) {
	out := append([]proto.Message(nil), messages...)
	report, err := b.report(out)
	if err != nil {
		return nil, report, err
	}
	if b.noLimit || b.limit <= 0 || report.InputBytes <= b.limit {
		return out, report, nil
	}

	// First remove complete historical turns, oldest first. History is
	// explicitly classified when restored or carried out of Plan mode, so
	// assistant/tool protocol pairs are never split.
	for report.InputBytes > b.limit {
		groups := classifiedHistoryGroups(out)
		if len(groups) == 0 {
			break
		}
		report.RemovedHistoryGroups++
		report.RemovedHistoryMsgs += len(groups[0])
		out = removeMessageIndexes(out, groups[0])
		report, err = b.refresh(out, report)
		if err != nil {
			return nil, report, err
		}
	}

	// Then reduce or fully omit the skills catalog. The full in-memory
	// catalog remains available to search_skills and load_skill.
	if report.InputBytes > b.limit {
		if idx := messageIndexByClass(out, proto.ContextClassSkillCatalog); idx >= 0 {
			out, report, err = b.fitSkillCatalog(out, idx, report)
			if err != nil {
				return nil, report, err
			}
		}
	}

	// Older live tool rounds can be discarded as whole protocol groups while
	// the latest assistant call/result structure is retained.
	for report.InputBytes > b.limit {
		rounds := activeToolRounds(out)
		if len(rounds) <= 1 {
			break
		}
		report.RemovedToolRounds++
		out = removeMessageIndexes(out, rounds[0])
		report, err = b.refresh(out, report)
		if err != nil {
			return nil, report, err
		}
	}

	// Preserve the latest tool protocol metadata but allow result content to
	// shrink. Multiple results in the same parallel call are reduced oldest
	// first until the envelope fits.
	if report.InputBytes > b.limit {
		rounds := activeToolRounds(out)
		if len(rounds) > 0 {
			for _, idx := range rounds[len(rounds)-1] {
				if report.InputBytes <= b.limit || out[idx].Role != proto.RoleTool || out[idx].Content == "" {
					continue
				}
				before := len(out[idx].Content)
				out[idx].Content = b.largestContentThatFits(out, idx, 0)
				if len(out[idx].Content) < before {
					report.TruncatedToolResults++
				}
				report, err = b.refresh(out, report)
				if err != nil {
					return nil, report, err
				}
			}
		}
	}

	// Current images and message structure are fixed. Text is the final
	// reducible input and must retain a useful UTF-8-safe minimum.
	if report.InputBytes > b.limit {
		current := currentUserIndex(out)
		if current < 0 {
			return nil, report, b.tooLargeError()
		}
		minBytes := min(minCurrentUserBytes, len(out[current].Content))
		before := len(out[current].Content)
		out[current].Content = b.largestContentThatFits(out, current, minBytes)
		report.TruncatedUserBytes += before - len(out[current].Content)
		report, err = b.refresh(out, report)
		if err != nil {
			return nil, report, err
		}
	}

	if report.InputBytes > b.limit {
		return nil, report, b.tooLargeError()
	}
	return out, report, nil
}

func (b *contextBudgeter) fitSkillCatalog(
	messages []proto.Message,
	idx int,
	report contextBudgetReport,
) ([]proto.Message, contextBudgetReport, error) {
	original := messages[idx].Content
	best := ""
	bestOmitted := len(b.skillCatalog)
	lo, hi := 0, min(len(original), skills.MaxCatalogBytes)
	for lo <= hi {
		mid := lo + (hi-lo)/2
		rendered := skills.CatalogPromptBudget(b.skillCatalog, mid)
		candidate := append([]proto.Message(nil), messages...)
		if rendered.Prompt == "" {
			candidate = removeMessageIndexes(candidate, []int{idx})
		} else {
			candidate[idx].Content = rendered.Prompt
		}
		size, err := estimateInputBytes(candidate, b.tools)
		if err != nil {
			return nil, report, err
		}
		if size <= b.limit {
			best = rendered.Prompt
			bestOmitted = rendered.Omitted
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	if best == "" {
		messages = removeMessageIndexes(messages, []int{idx})
	} else {
		messages[idx].Content = best
	}
	report.OmittedSkills = max(report.OmittedSkills, bestOmitted)
	refreshed, err := b.refresh(messages, report)
	return messages, refreshed, err
}

func (b *contextBudgeter) largestContentThatFits(messages []proto.Message, idx, minBytes int) string {
	original := messages[idx].Content
	minimum := textutil.TruncateUTF8Bytes(original, minBytes)
	candidate := append([]proto.Message(nil), messages...)
	candidate[idx].Content = minimum
	size, err := estimateInputBytes(candidate, b.tools)
	if err != nil || size > b.limit {
		return minimum
	}
	best := minimum
	lo, hi := len(minimum), len(original)
	for lo <= hi {
		mid := lo + (hi-lo)/2
		content := textutil.TruncateUTF8Bytes(original, mid)
		candidate[idx].Content = content
		size, marshalErr := estimateInputBytes(candidate, b.tools)
		if marshalErr == nil && size <= b.limit {
			best = content
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return best
}

func (b *contextBudgeter) refresh(messages []proto.Message, previous contextBudgetReport) (contextBudgetReport, error) {
	next, err := b.report(messages)
	if err != nil {
		return previous, err
	}
	next.RemovedHistoryGroups = previous.RemovedHistoryGroups
	next.RemovedHistoryMsgs = previous.RemovedHistoryMsgs
	next.RemovedToolRounds = previous.RemovedToolRounds
	next.TruncatedToolResults = previous.TruncatedToolResults
	next.OmittedSkills = previous.OmittedSkills
	next.TruncatedUserBytes = previous.TruncatedUserBytes
	return next, nil
}

func (b *contextBudgeter) report(messages []proto.Message) (contextBudgetReport, error) {
	toolJSON, err := json.Marshal(b.tools)
	if err != nil {
		return contextBudgetReport{}, fmt.Errorf("estimate tool schemas: %w", err)
	}
	inputBytes, err := estimateInputBytes(messages, b.tools)
	if err != nil {
		return contextBudgetReport{}, err
	}
	report := contextBudgetReport{
		InputLimit:      b.limit,
		ToolBytes:       len(toolJSON),
		InputBytes:      inputBytes,
		ResponseReserve: b.responseReserve,
		NoLimit:         b.noLimit,
	}
	sawSkillCatalog := false
	for _, message := range messages {
		body, marshalErr := json.Marshal(message)
		if marshalErr != nil {
			return contextBudgetReport{}, fmt.Errorf("estimate message: %w", marshalErr)
		}
		size := len(body) + thoughtSignatureBytes(message)
		switch message.ContextClass() {
		case proto.ContextClassProjectInstructions:
			report.AgentsBytes += size
		case proto.ContextClassSkillCatalog:
			report.SkillsBytes += size
			sawSkillCatalog = true
			report.OmittedSkills = skills.CatalogPromptBudget(b.skillCatalog, len(message.Content)).Omitted
		case proto.ContextClassHistory:
			report.HistoryBytes += size
		case proto.ContextClassCurrentUser:
			report.CurrentUserBytes += size
		default:
			if message.Role == proto.RoleSystem {
				report.SystemBytes += size
			} else {
				report.ActiveToolBytes += size
			}
		}
	}
	if !sawSkillCatalog && len(b.skillCatalog) > 0 {
		report.OmittedSkills = len(b.skillCatalog)
	}
	return report, nil
}

func (b *contextBudgeter) tooLargeError() error {
	return fmt.Errorf(
		"context exceeds max-input-chars=%d after safe trimming; increase max-input-chars or disable tools/project instructions",
		b.limit,
	)
}

func estimateInputBytes(messages []proto.Message, tools []proto.ToolSpec) (int, error) {
	normalized := proto.NormalizeSystemMessages(messages)
	messageJSON, err := json.Marshal(normalized)
	if err != nil {
		return 0, fmt.Errorf("estimate messages: %w", err)
	}
	toolJSON, err := json.Marshal(tools)
	if err != nil {
		return 0, fmt.Errorf("estimate tool schemas: %w", err)
	}
	signatureBytes := 0
	for _, message := range normalized {
		signatureBytes += thoughtSignatureBytes(message)
	}
	return len(messageJSON) + len(toolJSON) + signatureBytes, nil
}

func thoughtSignatureBytes(message proto.Message) int {
	size := 0
	for _, call := range message.ToolCalls {
		if call.Function.ThoughtSignature != "" {
			// The neutral Function intentionally excludes the opaque signature
			// from JSON used by non-Google adapters. Count its bytes plus a
			// conservative field-name/string overhead for the Google wire form.
			size += len(call.Function.ThoughtSignature) + len(`,"thoughtSignature":""`)
		}
	}
	return size
}

func classifiedHistoryGroups(messages []proto.Message) [][]int {
	var groups [][]int
	lastHistoryIndex := -2
	for idx, message := range messages {
		if message.ContextClass() != proto.ContextClassHistory {
			continue
		}
		if len(groups) == 0 || message.Role == proto.RoleUser || idx != lastHistoryIndex+1 {
			groups = append(groups, nil)
		}
		groups[len(groups)-1] = append(groups[len(groups)-1], idx)
		lastHistoryIndex = idx
	}
	return groups
}

func activeToolRounds(messages []proto.Message) [][]int {
	var rounds [][]int
	for idx, message := range messages {
		if message.ContextClass() != proto.ContextClassUnspecified || message.Role == proto.RoleSystem {
			continue
		}
		if message.Role == proto.RoleAssistant && len(message.ToolCalls) > 0 {
			rounds = append(rounds, []int{idx})
			continue
		}
		if message.Role == proto.RoleTool && len(rounds) > 0 {
			rounds[len(rounds)-1] = append(rounds[len(rounds)-1], idx)
		}
	}
	return rounds
}

func currentUserIndex(messages []proto.Message) int {
	for idx := len(messages) - 1; idx >= 0; idx-- {
		if messages[idx].ContextClass() == proto.ContextClassCurrentUser {
			return idx
		}
	}
	return -1
}

func messageIndexByClass(messages []proto.Message, class proto.ContextClass) int {
	for idx := range messages {
		if messages[idx].ContextClass() == class {
			return idx
		}
	}
	return -1
}

func removeMessageIndexes(messages []proto.Message, indexes []int) []proto.Message {
	if len(indexes) == 0 {
		return messages
	}
	remove := make(map[int]struct{}, len(indexes))
	for _, idx := range indexes {
		remove[idx] = struct{}{}
	}
	out := make([]proto.Message, 0, len(messages)-len(indexes))
	for idx, message := range messages {
		if _, ok := remove[idx]; !ok {
			out = append(out, message)
		}
	}
	return out
}

func debugContextBudget(report contextBudgetReport) {
	debug.Printf(
		"Context budget: input=%d/%d bytes, response-reserve=%d bytes, envelope=%d bytes, no-limit=%v",
		report.InputBytes,
		report.InputLimit,
		report.ResponseReserve,
		int64(report.InputBytes)+report.ResponseReserve,
		report.NoLimit,
	)
	debug.Printf(
		"Context budget parts: system=%d, tools=%d, AGENTS=%d, skills=%d, history=%d, current-user=%d, active-tool-round=%d bytes",
		report.SystemBytes,
		report.ToolBytes,
		report.AgentsBytes,
		report.SkillsBytes,
		report.HistoryBytes,
		report.CurrentUserBytes,
		report.ActiveToolBytes,
	)
	debug.Printf(
		"Context budget trimming: history-groups=%d, history-messages=%d, skill-entries=%d, tool-rounds=%d, tool-results=%d, user-bytes=%d",
		report.RemovedHistoryGroups,
		report.RemovedHistoryMsgs,
		report.OmittedSkills,
		report.RemovedToolRounds,
		report.TruncatedToolResults,
		report.TruncatedUserBytes,
	)
}
