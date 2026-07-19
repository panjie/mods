package app

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/skills"
	"github.com/stretchr/testify/require"
)

func classifiedMessage(role, content string, class proto.ContextClass) proto.Message {
	message := proto.Message{Role: role, Content: content}
	message.SetContextClass(class)
	return message
}

func TestContextBudgetCountsToolSchemasAndReportsReserveSeparately(t *testing.T) {
	messages := []proto.Message{
		{Role: proto.RoleSystem, Content: "system"},
		classifiedMessage(proto.RoleUser, strings.Repeat("u", 256), proto.ContextClassCurrentUser),
	}
	withoutTools := newContextBudgeter(1<<20, 100, false, nil, nil)
	_, base, err := withoutTools.apply(messages)
	require.NoError(t, err)

	tools := []proto.ToolSpec{{
		Name:        "large_tool",
		Description: strings.Repeat("schema", 100),
		InputSchema: map[string]any{"type": "object"},
	}}
	withTools := newContextBudgeter(int64(base.InputBytes), 100, false, tools, nil)
	_, report, err := withTools.apply(messages)
	require.ErrorContains(t, err, "context exceeds")
	require.Greater(t, report.ToolBytes, 0)
	require.Equal(t, int64(400), report.ResponseReserve)

	// The response reserve is visible in the envelope but does not reduce the
	// input-only max-input-chars allowance.
	unlimitedInput := newContextBudgeter(int64(base.InputBytes), 1_000_000, false, nil, nil)
	_, report, err = unlimitedInput.apply(messages)
	require.NoError(t, err)
	require.Equal(t, base.InputBytes, report.InputBytes)
	require.Equal(t, int64(4_000_000), report.ResponseReserve)
}

func TestContextBudgetPrunesHistoryBeforeSkills(t *testing.T) {
	catalog := []skills.Skill{{Name: "demo", Description: strings.Repeat("skill", 50)}}
	skillPrompt := skills.CatalogPrompt(catalog)
	historyUser := classifiedMessage(proto.RoleUser, strings.Repeat("old-user", 100), proto.ContextClassHistory)
	historyAssistant := classifiedMessage(proto.RoleAssistant, strings.Repeat("old-answer", 100), proto.ContextClassHistory)
	messages := []proto.Message{
		{Role: proto.RoleSystem, Content: "fixed"},
		classifiedMessage(proto.RoleSystem, skillPrompt, proto.ContextClassSkillCatalog),
		historyUser,
		historyAssistant,
		classifiedMessage(proto.RoleUser, strings.Repeat("current", 50), proto.ContextClassCurrentUser),
	}
	withoutHistory := append([]proto.Message(nil), messages[:2]...)
	withoutHistory = append(withoutHistory, messages[4])
	limit, err := estimateInputBytes(withoutHistory, nil)
	require.NoError(t, err)

	budgeter := newContextBudgeter(int64(limit), 0, false, nil, catalog)
	got, report, err := budgeter.apply(messages)
	require.NoError(t, err)
	require.Equal(t, 1, report.RemovedHistoryGroups)
	require.Equal(t, 2, report.RemovedHistoryMsgs)
	require.Equal(t, skillPrompt, got[1].Content)
}

func TestContextBudgetShrinksSkillsBeforeToolRounds(t *testing.T) {
	catalog := []skills.Skill{
		{Name: "alpha", Description: strings.Repeat("a", 256)},
		{Name: "bravo", Description: strings.Repeat("b", 256)},
	}
	messages := []proto.Message{
		{Role: proto.RoleSystem, Content: "fixed"},
		classifiedMessage(proto.RoleSystem, skills.CatalogPrompt(catalog), proto.ContextClassSkillCatalog),
		classifiedMessage(proto.RoleUser, strings.Repeat("current", 50), proto.ContextClassCurrentUser),
		toolCallMessage("old-call", "old"),
		toolResultMessage("old-call", strings.Repeat("result", 30)),
		toolCallMessage("latest-call", "latest"),
		toolResultMessage("latest-call", strings.Repeat("latest", 30)),
	}
	noSkills := removeMessageIndexes(messages, []int{1})
	limit, err := estimateInputBytes(noSkills, nil)
	require.NoError(t, err)

	got, report, err := newContextBudgeter(int64(limit), 0, false, nil, catalog).apply(messages)
	require.NoError(t, err)
	require.Zero(t, report.RemovedToolRounds)
	require.Greater(t, report.OmittedSkills, 0)
	require.True(t, containsToolCall(got, "old-call"))
	require.True(t, containsToolCall(got, "latest-call"))
}

func TestContextBudgetDropsOlderToolRoundAsProtocolGroup(t *testing.T) {
	messages := []proto.Message{
		{Role: proto.RoleSystem, Content: "fixed"},
		classifiedMessage(proto.RoleUser, strings.Repeat("current", 50), proto.ContextClassCurrentUser),
		toolCallMessage("old-call", "old"),
		toolResultMessage("old-call", strings.Repeat("old-result", 100)),
		toolCallMessage("latest-call", "latest"),
		toolResultMessage("latest-call", "latest result"),
	}
	withoutOld := removeMessageIndexes(messages, []int{2, 3})
	limit, err := estimateInputBytes(withoutOld, nil)
	require.NoError(t, err)

	got, report, err := newContextBudgeter(int64(limit), 0, false, nil, nil).apply(messages)
	require.NoError(t, err)
	require.Equal(t, 1, report.RemovedToolRounds)
	require.False(t, containsToolCall(got, "old-call"))
	require.True(t, containsToolCall(got, "latest-call"))
	require.Equal(t, "latest result", got[len(got)-1].Content)
}

func TestContextBudgetTruncatesToolResultBeforeCurrentUser(t *testing.T) {
	current := strings.Repeat("current", 80)
	messages := []proto.Message{
		{Role: proto.RoleSystem, Content: "fixed"},
		classifiedMessage(proto.RoleUser, current, proto.ContextClassCurrentUser),
		toolCallMessage("latest-call", "latest"),
		toolResultMessage("latest-call", strings.Repeat("tool-output", 200)),
	}
	emptyResult := append([]proto.Message(nil), messages...)
	emptyResult[3].Content = ""
	limit, err := estimateInputBytes(emptyResult, nil)
	require.NoError(t, err)

	got, report, err := newContextBudgeter(int64(limit), 0, false, nil, nil).apply(messages)
	require.NoError(t, err)
	require.Equal(t, 1, report.TruncatedToolResults)
	require.Equal(t, current, got[1].Content)
	require.Empty(t, got[3].Content)
	require.Equal(t, "latest-call", got[2].ToolCalls[0].ID)
	require.Equal(t, "latest-call", got[3].ToolCalls[0].ID)
}

func TestContextBudgetTruncatesCurrentUserUTF8AndKeepsMinimum(t *testing.T) {
	current := strings.Repeat("你", 400)
	messages := []proto.Message{
		{Role: proto.RoleSystem, Content: "fixed"},
		classifiedMessage(proto.RoleUser, current, proto.ContextClassCurrentUser),
	}
	minimum := append([]proto.Message(nil), messages...)
	minimum[1].Content = strings.Repeat("你", 85) // 255 UTF-8 bytes.
	limit, err := estimateInputBytes(minimum, nil)
	require.NoError(t, err)

	got, report, err := newContextBudgeter(int64(limit), 0, false, nil, nil).apply(messages)
	require.NoError(t, err)
	require.True(t, utf8.ValidString(got[1].Content))
	require.GreaterOrEqual(t, len(got[1].Content), 253)
	require.LessOrEqual(t, len(got[1].Content), 256)
	require.Greater(t, report.TruncatedUserBytes, 0)
}

func TestContextBudgetFailsWhenFixedContentAndMinimumUserDoNotFit(t *testing.T) {
	messages := []proto.Message{
		{Role: proto.RoleSystem, Content: strings.Repeat("fixed", 100)},
		classifiedMessage(proto.RoleUser, strings.Repeat("u", 256), proto.ContextClassCurrentUser),
	}
	size, err := estimateInputBytes(messages, nil)
	require.NoError(t, err)
	_, _, err = newContextBudgeter(int64(size-1), 0, false, nil, nil).apply(messages)
	require.ErrorContains(t, err, "increase max-input-chars")
}

func TestContextBudgetNoLimitKeepsMessages(t *testing.T) {
	messages := []proto.Message{
		{Role: proto.RoleSystem, Content: strings.Repeat("fixed", 100)},
		classifiedMessage(proto.RoleUser, strings.Repeat("user", 100), proto.ContextClassCurrentUser),
	}
	got, report, err := newContextBudgeter(1, 0, true, nil, nil).apply(messages)
	require.NoError(t, err)
	require.Equal(t, messages, got)
	require.True(t, report.NoLimit)
	require.Greater(t, report.InputBytes, report.InputLimit)
}

func TestContextBudgetCountsImagesUsingJSONRepresentation(t *testing.T) {
	messages := []proto.Message{
		classifiedMessage(proto.RoleUser, "look", proto.ContextClassCurrentUser),
	}
	messages[0].Images = []proto.Image{{Data: []byte(strings.Repeat("x", 300)), MimeType: "image/png"}}
	_, report, err := newContextBudgeter(1<<20, 0, false, nil, nil).apply(messages)
	require.NoError(t, err)
	require.Greater(t, report.CurrentUserBytes, 300)
}

func TestContextBudgetCountsOpaqueThoughtSignatures(t *testing.T) {
	without := []proto.Message{toolCallMessage("call", "tool")}
	with := []proto.Message{toolCallMessage("call", "tool")}
	with[0].ToolCalls[0].Function.ThoughtSignature = strings.Repeat("sig", 100)
	withoutSize, err := estimateInputBytes(without, nil)
	require.NoError(t, err)
	withSize, err := estimateInputBytes(with, nil)
	require.NoError(t, err)
	require.Greater(t, withSize, withoutSize+250)
}

func TestContextBudgetEstimatesStructuredSystemWireShape(t *testing.T) {
	identity := proto.Message{Role: proto.RoleSystem, Content: "identity"}
	identity.SetSystemSection(proto.SystemSectionRuntimeIdentity)
	format := proto.Message{Role: proto.RoleSystem, Content: "format"}
	format.SetSystemSection(proto.SystemSectionOutputFormat)
	current := classifiedMessage(proto.RoleUser, "hello", proto.ContextClassCurrentUser)
	messages := []proto.Message{format, current, identity}

	rawMessages, err := json.Marshal(messages)
	require.NoError(t, err)
	rawTools, err := json.Marshal([]proto.ToolSpec(nil))
	require.NoError(t, err)
	estimated, err := estimateInputBytes(messages, nil)
	require.NoError(t, err)
	require.NotEqual(t, len(rawMessages)+len(rawTools), estimated)

	normalized := proto.NormalizeSystemMessages(messages)
	require.Len(t, normalized, 2)
	normalizedMessages, err := json.Marshal(normalized)
	require.NoError(t, err)
	require.Equal(t, len(normalizedMessages)+len(rawTools), estimated)
}

func toolCallMessage(id, name string) proto.Message {
	return proto.Message{
		Role: proto.RoleAssistant,
		ToolCalls: []proto.ToolCall{{
			ID: id,
			Function: proto.Function{
				Name:      name,
				Arguments: []byte(`{"value":"x"}`),
			},
		}},
	}
}

func toolResultMessage(id, content string) proto.Message {
	return proto.Message{
		Role:    proto.RoleTool,
		Content: content,
		ToolCalls: []proto.ToolCall{{
			ID:       id,
			Function: proto.Function{Name: "tool"},
		}},
	}
}

func containsToolCall(messages []proto.Message, id string) bool {
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			if call.ID == id {
				return true
			}
		}
	}
	return false
}
