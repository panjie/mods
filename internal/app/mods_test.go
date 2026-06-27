package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/self"
	"github.com/stretchr/testify/require"
)

type staticModel string

func (s staticModel) Init() tea.Cmd { return nil }

func (s staticModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return s, nil }

func (s staticModel) View() string { return string(s) }

type staticStream struct{}

func (staticStream) Next() bool { return false }

func (staticStream) Current() (proto.Chunk, error) { return proto.Chunk{}, nil }

func (staticStream) Close() error { return nil }

func (staticStream) Err() error { return nil }

func (staticStream) Messages() []proto.Message { return nil }

func (staticStream) CallTools() []proto.ToolCallStatus { return nil }

func TestFindCacheOpsDetails(t *testing.T) {
	newMods := func(t *testing.T) *Mods {
		db := testDB(t)
		return &Mods{
			db:     db,
			Config: &Config{},
		}
	}

	t.Run("all empty", func(t *testing.T) {
		msg := newMods(t).findCacheOpsDetails()()
		dets := msg.(cacheDetailsMsg)
		require.Empty(t, dets.ReadID)
		require.NotEmpty(t, dets.WriteID)
		require.Empty(t, dets.Title)
	})

	t.Run("show id", func(t *testing.T) {
		mods := newMods(t)
		id := newConversationID()
		require.NoError(t, mods.db.Save(id, "message", "openai", "gpt-4"))
		mods.Config.Show = id[:8]
		msg := mods.findCacheOpsDetails()()
		dets := msg.(cacheDetailsMsg)
		require.Equal(t, id, dets.ReadID)
	})

	t.Run("show title", func(t *testing.T) {
		mods := newMods(t)
		id := newConversationID()
		require.NoError(t, mods.db.Save(id, "message 1", "openai", "gpt-4"))
		mods.Config.Show = "message 1"
		msg := mods.findCacheOpsDetails()()
		dets := msg.(cacheDetailsMsg)
		require.Equal(t, id, dets.ReadID)
	})

	t.Run("continue id", func(t *testing.T) {
		mods := newMods(t)
		id := newConversationID()
		rules := []ApprovalRule{scopedRule(ApprovalRule{
			Type: approvalShellPrefix,
			Tool: "shell_run", Pattern: "git commit *",
		})}
		require.NoError(t, mods.db.SaveConversation(
			id,
			"message",
			"openai",
			"gpt-4",
			[]proto.Message{{Role: proto.RoleUser, Content: "message"}},
			rules,
		))
		mods.Config.Continue = id[:5]
		mods.Config.Prefix = "prompt"
		msg := mods.findCacheOpsDetails()()
		dets := msg.(cacheDetailsMsg)
		require.Equal(t, id, dets.ReadID)
		require.Equal(t, id, dets.WriteID)
		require.Equal(t, rules, dets.Rules)
	})

	t.Run("continue with no prompt", func(t *testing.T) {
		mods := newMods(t)
		id := newConversationID()
		require.NoError(t, mods.db.Save(id, "message 1", "openai", "gpt-4"))
		mods.Config.ContinueLast = true
		msg := mods.findCacheOpsDetails()()
		dets := msg.(cacheDetailsMsg)
		require.Equal(t, id, dets.ReadID)
		require.Equal(t, id, dets.WriteID)
		require.Empty(t, dets.Title)
	})

	t.Run("continue explicit missing target does not fall back to head", func(t *testing.T) {
		mods := newMods(t)
		id := newConversationID()
		require.NoError(t, mods.db.Save(id, "message 1", "openai", "gpt-4"))
		mods.Config.Continue = "missing-title"
		mods.Config.Prefix = "prompt"
		msg := mods.findCacheOpsDetails()()
		_, ok := msg.(modsError)
		require.True(t, ok, "expected missing explicit continue target to fail")
	})

	t.Run("continue title", func(t *testing.T) {
		mods := newMods(t)
		id := newConversationID()
		require.NoError(t, mods.db.Save(id, "message 1", "openai", "gpt-4"))
		mods.Config.Continue = "message 1"
		mods.Config.Prefix = "prompt"
		msg := mods.findCacheOpsDetails()()
		dets := msg.(cacheDetailsMsg)
		require.Equal(t, id, dets.ReadID)
		require.Equal(t, id, dets.WriteID)
	})

	t.Run("continue last", func(t *testing.T) {
		mods := newMods(t)
		id := newConversationID()
		require.NoError(t, mods.db.Save(id, "message 1", "openai", "gpt-4"))
		mods.Config.ContinueLast = true
		mods.Config.Prefix = "prompt"
		msg := mods.findCacheOpsDetails()()
		dets := msg.(cacheDetailsMsg)
		require.Equal(t, id, dets.ReadID)
		require.Equal(t, id, dets.WriteID)
		require.Empty(t, dets.Title)
	})

	t.Run("continue missing name fails", func(t *testing.T) {
		mods := newMods(t)
		id := newConversationID()
		require.NoError(t, mods.db.Save(id, "message 1", "openai", "gpt-4"))
		mods.Config.Continue = "message 2"
		mods.Config.Prefix = "prompt"
		msg := mods.findCacheOpsDetails()()
		_, ok := msg.(modsError)
		require.True(t, ok, "expected missing explicit continue target to fail")
	})

	t.Run("write", func(t *testing.T) {
		mods := newMods(t)
		mods.Config.Title = "some title"
		msg := mods.findCacheOpsDetails()()
		dets := msg.(cacheDetailsMsg)
		require.Empty(t, dets.ReadID)
		require.NotEmpty(t, dets.WriteID)
		require.NotEqual(t, "some title", dets.WriteID)
		require.Equal(t, "some title", dets.Title)
	})

	t.Run("continue id and write with title", func(t *testing.T) {
		mods := newMods(t)
		id := newConversationID()
		rules := []ApprovalRule{scopedRule(ApprovalRule{Type: approvalEditAll, Tool: "file_edit"})}
		require.NoError(t, mods.db.SaveConversation(
			id,
			"message 1",
			"openai",
			"gpt-4",
			[]proto.Message{{Role: proto.RoleUser, Content: "message"}},
			rules,
		))
		mods.Config.Title = "some title"
		mods.Config.Continue = id[:10]
		msg := mods.findCacheOpsDetails()()
		dets := msg.(cacheDetailsMsg)
		require.Equal(t, id, dets.ReadID)
		require.NotEmpty(t, dets.WriteID)
		require.NotEqual(t, id, dets.WriteID)
		require.NotEqual(t, "some title", dets.WriteID)
		require.Equal(t, "some title", dets.Title)
		require.Equal(t, rules, dets.Rules)
	})

	t.Run("no cache does not restore rules", func(t *testing.T) {
		mods := newMods(t)
		id := newConversationID()
		require.NoError(t, mods.db.SaveConversation(
			id,
			"message",
			"openai",
			"gpt-4",
			[]proto.Message{{Role: proto.RoleUser, Content: "message"}},
			[]ApprovalRule{scopedRule(ApprovalRule{Type: approvalEditAll, Tool: "file_edit"})},
		))
		mods.Config.Continue = id
		mods.Config.Prefix = "prompt"
		mods.Config.NoCache = true
		msg := mods.findCacheOpsDetails()()
		dets := msg.(cacheDetailsMsg)
		require.Empty(t, dets.Rules)
	})

	t.Run("continue title and write with title", func(t *testing.T) {
		mods := newMods(t)
		id := newConversationID()
		require.NoError(t, mods.db.Save(id, "message 1", "openai", "gpt-4"))
		mods.Config.Title = "some title"
		mods.Config.Continue = "message 1"
		msg := mods.findCacheOpsDetails()()
		dets := msg.(cacheDetailsMsg)
		require.Equal(t, id, dets.ReadID)
		require.NotEmpty(t, dets.WriteID)
		require.NotEqual(t, id, dets.WriteID)
		require.NotEqual(t, "some title", dets.WriteID)
		require.Equal(t, "some title", dets.Title)
	})

	t.Run("show invalid", func(t *testing.T) {
		mods := newMods(t)
		mods.Config.Show = "aaa"
		msg := mods.findCacheOpsDetails()()
		err := msg.(modsError)
		require.Equal(t, "Could not find the conversation.", err.ReasonText)
		require.EqualError(t, err, "no conversations found: aaa")
	})

	t.Run("uses config model and api not global config", func(t *testing.T) {
		mods := newMods(t)
		mods.Config.Model = "claude-3.7-sonnet"
		mods.Config.API = "anthropic"

		msg := mods.findCacheOpsDetails()()
		dets := msg.(cacheDetailsMsg)

		require.Equal(t, "claude-3.7-sonnet", dets.Model)
		require.Equal(t, "anthropic", dets.API)
		require.Empty(t, dets.ReadID)
		require.NotEmpty(t, dets.WriteID)
	})
}

func TestSetupStreamContextDoesNotTruncateWhenMaxCharsUnset(t *testing.T) {
	cfg := defaultConfig()
	cfg.MaxInputChars = 0
	cfg.FormatText = defaultConfig().FormatText
	mods := &Mods{
		ctx:    context.Background(),
		db:     testDB(t),
		Config: &cfg,
	}
	require.NoError(t, mods.setupStreamContext("hello world", Model{}))
	require.NotEmpty(t, mods.messages)
	require.Equal(t, "hello world", mods.messages[len(mods.messages)-1].Content)
}

func TestReadLimitedStdinTruncatesBeforeReadAll(t *testing.T) {
	m := &Mods{Config: &Config{}}
	m.Config.MaxInputChars = 5
	data, err := m.readLimitedStdin(bytes.NewBufferString("hello world"))
	require.NoError(t, err)
	require.Contains(t, string(data), "hello")
	require.Contains(t, string(data), "Input truncated")
}

func TestPruneHistoryForBudgetKeepsRecentMessages(t *testing.T) {
	messages := []proto.Message{
		{Role: proto.RoleSystem, Content: "sys"},
		{Role: proto.RoleUser, Content: strings.Repeat("old", 20)},
		{Role: proto.RoleAssistant, Content: strings.Repeat("older", 20)},
		{Role: proto.RoleUser, Content: "recent question"},
		{Role: proto.RoleAssistant, Content: "recent answer"},
	}
	got := pruneHistoryForBudget(messages, "new", 40, false)
	require.Equal(t, proto.RoleSystem, got[0].Role)
	require.Equal(t, "recent question", got[1].Content)
	require.Equal(t, "recent answer", got[2].Content)
	require.Len(t, got, 3)
}

func TestPruneHistoryForBudgetDropsLeadingToolResults(t *testing.T) {
	messages := []proto.Message{
		{Role: proto.RoleSystem, Content: "sys"},
		{Role: proto.RoleAssistant, Content: strings.Repeat("tool call", 20), ToolCalls: []proto.ToolCall{{ID: "call-1"}}},
		{Role: proto.RoleTool, Content: "small result", ToolCalls: []proto.ToolCall{{ID: "call-1"}}},
		{Role: proto.RoleAssistant, Content: "final answer"},
	}
	got := pruneHistoryForBudget(messages, "new", 40, false)
	require.Equal(t, []string{proto.RoleSystem, proto.RoleAssistant}, []string{got[0].Role, got[1].Role})
	require.Equal(t, "final answer", got[1].Content)
}

func TestRemoveWhitespace(t *testing.T) {
	t.Run("only whitespaces", func(t *testing.T) {
		require.Equal(t, "", removeWhitespace(" \n"))
	})

	t.Run("regular text", func(t *testing.T) {
		require.Equal(t, " regular\n ", removeWhitespace(" regular\n "))
	})
}

var cutPromptTests = map[string]struct {
	msg      string
	prompt   string
	expected string
}{
	"bad error": {
		msg:      "nope",
		prompt:   "the prompt",
		expected: "the prompt",
	},
	"crazy error": {
		msg:      tokenErrMsg(10, 93),
		prompt:   "the prompt",
		expected: "the prompt",
	},
	"cut prompt": {
		msg:      tokenErrMsg(10, 3),
		prompt:   "this is a long prompt I have no idea if its really 10 tokens",
		expected: "this is a long prompt ",
	},
	"missmatch of token estimation vs api result": {
		msg:      tokenErrMsg(30000, 100),
		prompt:   "tell me a joke",
		expected: "tell me a joke",
	},
}

func tokenErrMsg(l, ml int) string {
	return fmt.Sprintf(`This model's maximum context length is %d tokens. However, your messages resulted in %d tokens`, ml, l)
}

func TestCutPrompt(t *testing.T) {
	for name, tc := range cutPromptTests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.expected, cutPrompt(tc.msg, tc.prompt))
		})
	}
}

func TestIncreaseIndent(t *testing.T) {
	t.Run("single line", func(t *testing.T) {
		require.Equal(t, "\thello", increaseIndent("hello"))
	})
	t.Run("multiple lines", func(t *testing.T) {
		require.Equal(t, "\ta\n\tb\n\tc", increaseIndent("a\nb\nc"))
	})
	t.Run("empty", func(t *testing.T) {
		require.Equal(t, "\t", increaseIndent(""))
	})
}

func TestToolOperationLabel(t *testing.T) {
	t.Run("web search query", func(t *testing.T) {
		got := toolOperationLabel("web_search", []byte(`{"query":"GUI wrapper for command line tools"}`), 80)
		require.Equal(t, "Searching web: GUI wrapper for command line tools", got)
	})

	t.Run("shell command preview", func(t *testing.T) {
		got := toolOperationLabel("shell_run", []byte(`{"command":"go   test   ./...\necho done"}`), 80)
		require.Equal(t, "Running command: go test ./...", got)
	})

	t.Run("file read path", func(t *testing.T) {
		got := toolOperationLabel("fs_read_file", []byte(`{"path":"mods.go"}`), 80)
		require.Equal(t, "Reading file: mods.go", got)
	})

	t.Run("file write path", func(t *testing.T) {
		got := toolOperationLabel("fs_write_file", []byte(`{"path":"mods.go","content":"package main"}`), 80)
		require.Equal(t, "Writing file: mods.go", got)
	})

	t.Run("file search query and path", func(t *testing.T) {
		got := toolOperationLabel("fs_search", []byte(`{"path":"internal","query":"toolOperationLabel"}`), 80)
		require.Equal(t, "Searching files: toolOperationLabel in internal", got)
	})

	t.Run("thinking note", func(t *testing.T) {
		got := toolOperationLabel("thinking_note", []byte(`{"thought":"checking the next step","done":false}`), 80)
		require.Equal(t, "Thinking: checking the next step", got)
	})

	t.Run("unknown tool preferred fields", func(t *testing.T) {
		got := toolOperationLabel("github_search", []byte(`{"query":"mods status bar","repo":"panjie/mods","irrelevant":"x"}`), 120)
		require.Equal(t, "Running tool: github_search (query=mods status bar, repo=panjie/mods)", got)
	})

	t.Run("invalid json falls back", func(t *testing.T) {
		got := toolOperationLabel("mcp_tool", []byte(`{nope`), 80)
		require.Equal(t, "Running tool: mcp_tool", got)
	})

	t.Run("empty args falls back", func(t *testing.T) {
		got := toolOperationLabel("mcp_tool", []byte(`{}`), 80)
		require.Equal(t, "Running tool: mcp_tool", got)
	})

	t.Run("unicode and narrow width truncate safely", func(t *testing.T) {
		got := toolOperationLabel("web_search", []byte(`{"query":"搜索 一个 很长 很长 的 查询 内容"}`), 20)
		require.Equal(t, "Searching web: 搜索...", got)
	})
}

func TestSetupStreamContextMinimal(t *testing.T) {
	newTestMods := func(cfg Config) *Mods {
		if cfg.Roles == nil {
			cfg.Roles = map[string][]string{}
		}
		if cfg.FormatText == nil {
			cfg.FormatText = defaultConfig().FormatText
		}
		if cfg.FormatAs == "" {
			cfg.FormatAs = "markdown"
		}
		return &Mods{
			Config: &cfg,
			Styles: makeStyles(lipgloss.NewRenderer(nil)),
			ctx:    context.Background(),
		}
	}
	contents := func(messages []proto.Message) []string {
		out := make([]string, 0, len(messages))
		for _, msg := range messages {
			if msg.Role == proto.RoleSystem {
				out = append(out, msg.Content)
			}
		}
		return out
	}
	model := Model{MaxChars: 1000}

	t.Run("minimal disabled", func(t *testing.T) {
		m := newTestMods(Config{})
		require.NoError(t, m.setupStreamContext("hello", model))
		require.NotContains(t, contents(m.messages), minimalSystemPrompt)
	})

	t.Run("minimal adds system prompt", func(t *testing.T) {
		m := newTestMods(Config{PersistentConfig: PersistentConfig{Minimal: true}})
		require.NoError(t, m.setupStreamContext("hello", model))
		require.Contains(t, contents(m.messages), minimalSystemPrompt)
	})

	t.Run("minimal suppresses format prompt", func(t *testing.T) {
		m := newTestMods(Config{PersistentConfig: PersistentConfig{Minimal: true, Format: true}})
		require.NoError(t, m.setupStreamContext("hello", model))
		systemMessages := contents(m.messages)
		require.Contains(t, systemMessages, minimalSystemPrompt)
		require.NotContains(t, systemMessages, defaultMarkdownFormatText)
	})

	t.Run("minimal follows role prompt", func(t *testing.T) {
		m := newTestMods(Config{
			PersistentConfig: PersistentConfig{
				Minimal: true,
				Role:    "shell",
				Roles:   map[string][]string{"shell": {"role prompt"}},
			},
		})
		require.NoError(t, m.setupStreamContext("hello", model))
		systemMessages := contents(m.messages)
		roleIndex := slices.Index(systemMessages, "role prompt")
		minimalIndex := slices.Index(systemMessages, minimalSystemPrompt)
		require.NotEqual(t, -1, roleIndex)
		require.NotEqual(t, -1, minimalIndex)
		require.Less(t, roleIndex, minimalIndex)
	})
}

func TestSetupPlanContextPromptPolicy(t *testing.T) {
	cfg := Config{}
	cfg.Roles = map[string][]string{}
	cfg.FormatText = defaultConfig().FormatText
	cfg.FormatAs = "markdown"
	m := &Mods{
		Config: &cfg,
		Styles: makeStyles(lipgloss.NewRenderer(nil)),
		ctx:    context.Background(),
	}

	require.NoError(t, m.setupPlanContext("hello", Model{MaxChars: 1000}))

	systemMessages := make([]string, 0, len(m.messages))
	for _, msg := range m.messages {
		if msg.Role == proto.RoleSystem {
			systemMessages = append(systemMessages, msg.Content)
		}
	}
	require.Contains(t, systemMessages, self.PlanSystemPrompt)
	require.Contains(t, self.PlanSystemPrompt, "Use platform-appropriate read-only commands")
	for _, msg := range systemMessages {
		require.NotContains(t, msg, "Safe workspace:")
	}
}

func TestInjectApprovedPlanGuidance(t *testing.T) {
	m := &Mods{
		planContent: "1. Do the approved thing",
		messages: []proto.Message{
			{Role: proto.RoleSystem, Content: "system"},
			{Role: proto.RoleUser, Content: "execute"},
		},
	}

	m.injectApprovedPlan()

	require.Len(t, m.messages, 3)
	require.Equal(t, proto.RoleSystem, m.messages[1].Role)
	require.Contains(t, m.messages[1].Content, "The user has approved the following plan for execution:")
	require.Contains(t, m.messages[1].Content, "Follow this approved plan during execution.")
	require.Contains(t, m.messages[1].Content, "If new information requires changing it")
	require.Equal(t, proto.RoleUser, m.messages[2].Role)
	require.Empty(t, m.planContent)
}

func TestProbeWindowsPowerShellCapabilities(t *testing.T) {
	t.Run("reports versions and missing shells", func(t *testing.T) {
		probe := func(_ context.Context, name string) (string, error) {
			switch name {
			case "powershell":
				return "5.1.26100.8655", nil
			case "pwsh":
				return "", exec.ErrNotFound
			default:
				return "", errors.New("unexpected shell")
			}
		}

		require.Equal(t, "powershell=5.1.26100.8655, pwsh=not-found", probeWindowsPowerShellCapabilities(probe))
	})

	t.Run("reports unknown on probe errors", func(t *testing.T) {
		probe := func(_ context.Context, _ string) (string, error) {
			return "", errors.New("probe failed")
		}

		require.Equal(t, "powershell=unknown, pwsh=unknown", probeWindowsPowerShellCapabilities(probe))
	})
}

func TestSetupStreamContextWindowsPowerShellInfo(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only system info")
	}

	cfg := Config{}
	cfg.Roles = map[string][]string{}
	cfg.FormatText = defaultConfig().FormatText
	cfg.FormatAs = "markdown"
	m := &Mods{
		Config: &cfg,
		Styles: makeStyles(lipgloss.NewRenderer(nil)),
		ctx:    context.Background(),
	}
	require.NoError(t, m.setupStreamContext("hello", Model{MaxChars: 1000}))
	require.NotEmpty(t, m.messages)
	require.Contains(t, m.messages[0].Content, "powershell=")
	require.Contains(t, m.messages[0].Content, "pwsh=")
}

func TestOperationStatusView(t *testing.T) {
	newTestMods := func() *Mods {
		return &Mods{
			Config:              &Config{},
			Styles:              makeStyles(lipgloss.NewRenderer(nil)),
			anim:                staticModel("Generating"),
			state:               requestState,
			showOperationStatus: true,
			width:               80,
			reviewer:            &toolReviewer{},
		}
	}

	t.Run("shows active operation", func(t *testing.T) {
		m := newTestMods()
		_, _ = m.Update(toolOperationStatusMsg{content: "Running command: go test ./..."})
		require.Contains(t, m.View(), "Running command: go test ./...")
	})

	t.Run("clears active operation", func(t *testing.T) {
		m := newTestMods()
		_, _ = m.Update(toolOperationStatusMsg{content: "Running tool: fs_read_file"})
		_, _ = m.Update(toolOperationStatusMsg{done: true})
		require.NotContains(t, m.View(), "Running tool: fs_read_file")
	})

	t.Run("quiet hides active operation", func(t *testing.T) {
		m := newTestMods()
		m.Config.Quiet = true
		_, _ = m.Update(toolOperationStatusMsg{content: "Running command: go test ./..."})
		require.NotContains(t, m.View(), "Running command: go test ./...")
	})

	t.Run("hide tool status hides active operation", func(t *testing.T) {
		m := newTestMods()
		m.Config.HideToolStatus = true
		_, _ = m.Update(toolOperationStatusMsg{content: "Running command: go test ./..."})
		require.NotContains(t, m.View(), "Running command: go test ./...")
	})

	t.Run("reasoning alone does not show status", func(t *testing.T) {
		m := newTestMods()
		m.reasoningActive = true
		view := m.renderWithOperation("answer")
		require.Equal(t, "answer", view)
		require.NotContains(t, view, "[R]")
		require.NotContains(t, view, "Reasoning")
	})

	t.Run("active operation while reasoning renders without reasoning badge", func(t *testing.T) {
		m := newTestMods()
		m.reasoningActive = true
		m.setActiveOperation("Running command: go test ./...")
		view := m.renderWithOperation("answer")
		require.Contains(t, view, "Running command: go test ./...")
		require.NotContains(t, view, "[R]")
		require.NotContains(t, view, "Reasoning")
	})
}

func TestApprovedPlanTranscript(t *testing.T) {
	t.Run("uses full rendered output", func(t *testing.T) {
		m := &Mods{
			outputRenderer: outputRenderer{Output: "raw plan", glamOutput: "rendered plan\n\n"},
		}

		require.Equal(t, "rendered plan\n", m.approvedPlanTranscript())
	})

	t.Run("falls back to raw output", func(t *testing.T) {
		m := &Mods{outputRenderer: outputRenderer{Output: "raw plan\n\n"}}

		require.Equal(t, "raw plan\n", m.approvedPlanTranscript())
	})

	t.Run("empty plan stays empty", func(t *testing.T) {
		m := &Mods{outputRenderer: outputRenderer{glamOutput: "\n\n"}}

		require.Empty(t, m.approvedPlanTranscript())
	})
}

func TestPlanApprovalPreservesTranscriptBeforeExecution(t *testing.T) {
	oldIsOutputTTY := isOutputTTY
	isOutputTTY = func() bool { return true }
	defer func() { isOutputTTY = oldIsOutputTTY }()

	m := &Mods{
		Config:         &Config{Plan: true},
		Styles:         makeStyles(lipgloss.NewRenderer(nil)),
		state:          planState,
		outputRenderer: outputRenderer{Output: "raw plan", glamOutput: "rendered plan\n"},
		reviewer:       &toolReviewer{},
		width:          80,
	}

	model, cmd := m.Update(planApprovedMsg{plan: "approved plan"})

	require.Same(t, m, model)
	require.False(t, m.Config.Plan)
	require.Equal(t, planState, m.state)
	require.Equal(t, "rendered plan\n", m.glamOutput)
	require.NotNil(t, cmd)
	require.Contains(t, fmt.Sprintf("%T", cmd()), "sequenceMsg")
}

func TestPlanExecutionStartResetsOutput(t *testing.T) {
	m := &Mods{
		Config:                &Config{},
		Styles:                makeStyles(lipgloss.NewRenderer(nil)),
		state:                 planState,
		outputRenderer:        outputRenderer{Output: "approved plan", displayOutput: "approved plan display", glamOutput: "rendered approved plan", glamHeight: 3},
		responseOutputStarted: true,
		reviewer:              &toolReviewer{},
		contentMutex:          &sync.Mutex{},
	}

	_, cmd := m.Update(planExecutionStartMsg{})

	require.Equal(t, requestState, m.state)
	require.Empty(t, m.Output)
	require.Empty(t, m.displayOutput)
	require.Empty(t, m.glamOutput)
	require.Zero(t, m.glamHeight)
	require.False(t, m.responseOutputStarted)
	require.NotNil(t, cmd)
}

func TestGeneratingViewBeforeOutput(t *testing.T) {
	newTestMods := func() *Mods {
		return &Mods{
			Config:              &Config{},
			Styles:              makeStyles(lipgloss.NewRenderer(nil)),
			anim:                staticModel("Generating"),
			state:               requestState,
			showOperationStatus: true,
			width:               80,
			reviewer:            &toolReviewer{},
			contentMutex:        &sync.Mutex{},
		}
	}

	t.Run("request state shows generating", func(t *testing.T) {
		m := newTestMods()
		require.Contains(t, m.View(), "Generating")
	})

	t.Run("request state does not show approved plan", func(t *testing.T) {
		m := newTestMods()
		m.Output = "approved plan"
		m.glamOutput = "rendered approved plan"
		view := m.View()
		require.Contains(t, view, "Generating")
		require.NotContains(t, view, "approved plan")
		require.NotContains(t, view, "rendered approved plan")
	})

	t.Run("response state before output shows generating", func(t *testing.T) {
		m := newTestMods()
		m.state = responseState
		require.Contains(t, m.View(), "Generating")
	})

	t.Run("response state before output shows generating and operation", func(t *testing.T) {
		m := newTestMods()
		m.state = responseState
		m.setActiveOperation("Searching web: Go latest release")
		view := m.View()
		require.Contains(t, view, "Generating")
		require.Contains(t, view, "Searching web: Go latest release")
	})

	t.Run("response state before output renders fancy animation prefix", func(t *testing.T) {
		m := newTestMods()
		m.Config = &Config{PersistentConfig: PersistentConfig{StatusText: "Generating"}}
		m.anim = staticModel("***** Generating")
		m.state = responseState

		view := m.View()
		idx := strings.Index(view, "Generating")
		require.Greater(t, idx, 0)
		require.NotEmpty(t, strings.TrimSpace(view[:idx]))
	})

	t.Run("first output hides generating", func(t *testing.T) {
		m := newTestMods()
		m.Config.Raw = true
		_, _ = m.Update(streamEventMsg{
			kind:   streamEventChunk,
			chunk:  proto.Chunk{Content: "hello"},
			runner: newStreamRunner(staticStream{}, nil, func(err error) tea.Msg { return modsError{Err: err} }),
		})
		require.True(t, m.responseOutputStarted)
		require.Contains(t, m.Output, "hello")
		require.NotContains(t, m.renderWithOperation(m.Output), "Generating")
	})

	t.Run("quiet hides generating", func(t *testing.T) {
		m := newTestMods()
		m.Config.Quiet = true
		require.NotContains(t, m.View(), "Generating")
	})
}

func TestViewportNeeded(t *testing.T) {
	t.Run("viewport taller than window", func(t *testing.T) {
		m := &Mods{outputRenderer: outputRenderer{glamHeight: 100}, height: 50}
		require.True(t, m.viewportNeeded())
	})
	t.Run("viewport shorter than window", func(t *testing.T) {
		m := &Mods{outputRenderer: outputRenderer{glamHeight: 10}, height: 50}
		require.False(t, m.viewportNeeded())
	})
	t.Run("equal", func(t *testing.T) {
		m := &Mods{outputRenderer: outputRenderer{glamHeight: 50}, height: 50}
		require.False(t, m.viewportNeeded())
	})
}

func TestPtrOrNil(t *testing.T) {
	t.Run("negative returns nil", func(t *testing.T) {
		require.Nil(t, ptrOrNil[int64](-1))
		require.Nil(t, ptrOrNil[float64](-1.0))
	})
	t.Run("zero returns pointer", func(t *testing.T) {
		p := ptrOrNil[int64](0)
		require.NotNil(t, p)
		require.Equal(t, int64(0), *p)
	})
	t.Run("positive returns pointer", func(t *testing.T) {
		p := ptrOrNil[int64](42)
		require.NotNil(t, p)
		require.Equal(t, int64(42), *p)
	})
	t.Run("float positive returns pointer", func(t *testing.T) {
		p := ptrOrNil[float64](0.7)
		require.NotNil(t, p)
		require.Equal(t, float64(0.7), *p)
	})
}

func TestResolveModel(t *testing.T) {
	apis := APIs{
		{
			Name: "openai",
			Models: map[string]Model{
				"gpt-4":   {Aliases: []string{"4"}},
				"gpt-4o":  {Aliases: []string{"4o"}},
				"gpt-3.5": {},
			},
		},
		{
			Name: "anthropic",
			Models: map[string]Model{
				"claude-sonnet-4": {Aliases: []string{"sonnet-4"}},
			},
		},
	}

	m := testMods(t)

	t.Run("exact model match", func(t *testing.T) {
		cfg := &Config{PersistentConfig: PersistentConfig{API: "openai", Model: "gpt-4", APIs: apis}}
		api, mod, err := m.resolveModel(cfg)
		require.NoError(t, err)
		require.Equal(t, "openai", api.Name)
		require.Equal(t, "gpt-4", mod.Name)
		require.Equal(t, "openai", mod.API)
	})

	t.Run("alias match", func(t *testing.T) {
		cfg := &Config{PersistentConfig: PersistentConfig{API: "openai", Model: "4o", APIs: apis}}
		_, mod, err := m.resolveModel(cfg)
		require.NoError(t, err)
		require.Equal(t, "gpt-4o", mod.Name)
	})

	t.Run("model not in specified API", func(t *testing.T) {
		cfg := &Config{PersistentConfig: PersistentConfig{API: "openai", Model: "nonexistent", APIs: apis}}
		_, _, err := m.resolveModel(cfg)
		require.Error(t, err)
		merr := err.(modsError)
		require.Contains(t, merr.ReasonText, "does not contain the model")
	})

	t.Run("api not configured", func(t *testing.T) {
		cfg := &Config{PersistentConfig: PersistentConfig{API: "unknown-api", Model: "gpt-4", APIs: apis}}
		_, _, err := m.resolveModel(cfg)
		require.Error(t, err)
		merr := err.(modsError)
		require.Contains(t, merr.ReasonText, "not in the settings file")
	})

	t.Run("empty api searches all", func(t *testing.T) {
		cfg := &Config{PersistentConfig: PersistentConfig{API: "", Model: "claude-sonnet-4", APIs: apis}}
		api, mod, err := m.resolveModel(cfg)
		require.NoError(t, err)
		require.Equal(t, "anthropic", api.Name)
		require.Equal(t, "claude-sonnet-4", mod.Name)
	})
}

func TestBuildRequestSessionValidatesImagesBeforeAPIKey(t *testing.T) {
	m := testMods(t)
	m.ctx = context.Background()
	m.Config = &Config{PersistentConfig: PersistentConfig{
		API:    "openai",
		Model:  "gpt-4",
		Images: []string{filepath.Join(t.TempDir(), "missing.png")},
		APIs: APIs{{
			Name: "openai",
			Models: map[string]Model{
				"gpt-4": {},
			},
		}},
	}}

	_, err := m.buildRequestSession("describe image")
	require.Error(t, err)
	merr, ok := err.(modsError)
	require.True(t, ok)
	require.Equal(t, "Could not read image file", merr.ReasonText)
	require.NotContains(t, merr.Error(), "api key")
}

func TestBuildRequestSessionKeepsNonImageValidationAfterAPIKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	m := testMods(t)
	m.ctx = context.Background()
	m.Config = &Config{PersistentConfig: PersistentConfig{
		API:   "openai",
		Model: "gpt-4",
		Role:  "missing-role",
		Roles: map[string][]string{},
		APIs: APIs{{
			Name: "openai",
			Models: map[string]Model{
				"gpt-4": {},
			},
		}},
	}}

	_, err := m.buildRequestSession("hello")
	require.Error(t, err)
	merr, ok := err.(modsError)
	require.True(t, ok)
	require.Equal(t, "OpenAI authentication failed", merr.ReasonText)
}

func TestEnsureKey(t *testing.T) {
	m := testMods(t)

	t.Run("direct api key", func(t *testing.T) {
		api := API{APIKey: "sk-direct"}
		key, err := m.ensureKey(api, "DEFAULT_ENV", "https://example.com")
		require.NoError(t, err)
		require.Equal(t, "sk-direct", key)
	})

	t.Run("from env var", func(t *testing.T) {
		t.Setenv("TEST_API_KEY", "env-key")
		api := API{APIKeyEnv: "TEST_API_KEY"}
		key, err := m.ensureKey(api, "DEFAULT_ENV", "https://example.com")
		require.NoError(t, err)
		require.Equal(t, "env-key", key)
	})

	t.Run("from default env", func(t *testing.T) {
		t.Setenv("DEFAULT_ENV", "default-key")
		api := API{}
		key, err := m.ensureKey(api, "DEFAULT_ENV", "https://example.com")
		require.NoError(t, err)
		require.Equal(t, "default-key", key)
	})

	t.Run("api key env takes precedence over default env", func(t *testing.T) {
		t.Setenv("SPECIFIC_KEY", "specific")
		t.Setenv("DEFAULT_ENV", "default")
		api := API{APIKeyEnv: "SPECIFIC_KEY"}
		key, err := m.ensureKey(api, "DEFAULT_ENV", "https://example.com")
		require.NoError(t, err)
		require.Equal(t, "specific", key)
	})

	t.Run("no key found", func(t *testing.T) {
		api := API{}
		_, err := m.ensureKey(api, "MISSING_KEY", "https://example.com")
		require.Error(t, err)
		merr := err.(modsError)
		require.Contains(t, merr.ReasonText, "MISSING_KEY")
	})
}
