package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/skills"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

type staticModel string

func (s staticModel) Init() tea.Cmd { return nil }

func (s staticModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return s, nil }

func (s staticModel) View() tea.View { return tea.NewView(string(s)) }

type staticStream struct{ usage proto.TokenUsage }

func (staticStream) Next() bool { return false }

func (staticStream) Current() (proto.Chunk, error) { return proto.Chunk{}, nil }

func (staticStream) Close() error { return nil }

func (staticStream) Err() error { return nil }

func (staticStream) Messages() []proto.Message { return nil }

func (s staticStream) Usage() proto.TokenUsage { return s.usage }

func (staticStream) CallTools() []proto.ToolCallStatus { return nil }

func TestInitQueriesBackgroundColorOnlyWithInteractiveTerminal(t *testing.T) {
	nonInteractive := &Mods{Config: &Config{}}
	require.IsType(t, sessionDetailsMsg{}, nonInteractive.Init()())

	raw := &Mods{Config: &Config{
		PersistentConfig:        PersistentConfig{Raw: true},
		InteractiveTTYAvailable: true,
	}}
	require.IsType(t, sessionDetailsMsg{}, raw.Init()())

	interactive := &Mods{Config: &Config{InteractiveTTYAvailable: true}}
	batch, ok := interactive.Init()().(tea.BatchMsg)
	require.True(t, ok)
	require.Len(t, batch, 2)
}

func TestFindSessionDetails(t *testing.T) {
	newMods := func(t *testing.T) *Mods {
		db := testDB(t)
		return &Mods{
			db:     db,
			Config: &Config{},
		}
	}

	t.Run("all empty", func(t *testing.T) {
		msg := newMods(t).findSessionDetails()()
		dets := msg.(sessionDetailsMsg)
		require.Empty(t, dets.ReadID)
		require.NotEmpty(t, dets.WriteID)
		require.Empty(t, dets.Title)
	})

	t.Run("continue id", func(t *testing.T) {
		mods := newMods(t)
		id := newSessionID()
		rules := []ApprovalRule{scopedRule(ApprovalRule{
			Type: approvalShellPrefix,
			Tool: "shell_run", Pattern: "git commit *",
		})}
		require.NoError(t, mods.db.SaveSession(
			id,
			"message",
			"openai",
			"gpt-4",
			[]proto.Message{{Role: proto.RoleUser, Content: "message"}},
			rules,
		))
		mods.Config.Continue = id[:5]
		mods.Config.Prefix = "prompt"
		msg := mods.findSessionDetails()()
		dets := msg.(sessionDetailsMsg)
		require.Equal(t, id, dets.ReadID)
		require.Equal(t, id, dets.WriteID)
		require.Equal(t, rules, dets.Rules)
	})

	t.Run("continue id preserves existing title", func(t *testing.T) {
		mods := newMods(t)
		id := newSessionID()
		require.NoError(t, mods.db.Save(id, "message", "openai", "gpt-4"))
		mods.Config.Continue = id
		mods.Config.Prefix = "prompt"

		msg := mods.findSessionDetails()()
		dets := msg.(sessionDetailsMsg)

		require.Equal(t, id, dets.ReadID)
		require.Equal(t, id, dets.WriteID)
		require.Equal(t, "message", dets.Title)
	})

	t.Run("continue with no prompt", func(t *testing.T) {
		mods := newMods(t)
		id := newSessionID()
		require.NoError(t, mods.db.Save(id, "message 1", "openai", "gpt-4"))
		mods.Config.ContinueLast = true
		msg := mods.findSessionDetails()()
		dets := msg.(sessionDetailsMsg)
		require.Equal(t, id, dets.ReadID)
		require.Equal(t, id, dets.WriteID)
		require.Empty(t, dets.Title)
	})

	t.Run("continue explicit missing target does not fall back to head", func(t *testing.T) {
		mods := newMods(t)
		id := newSessionID()
		require.NoError(t, mods.db.Save(id, "message 1", "openai", "gpt-4"))
		mods.Config.Continue = "missing-title"
		mods.Config.Prefix = "prompt"
		msg := mods.findSessionDetails()()
		_, ok := msg.(modsError)
		require.True(t, ok, "expected missing explicit continue target to fail")
	})

	t.Run("continue title", func(t *testing.T) {
		mods := newMods(t)
		id := newSessionID()
		require.NoError(t, mods.db.Save(id, "message 1", "openai", "gpt-4"))
		mods.Config.Continue = "message 1"
		mods.Config.Prefix = "prompt"
		msg := mods.findSessionDetails()()
		dets := msg.(sessionDetailsMsg)
		require.Equal(t, id, dets.ReadID)
		require.Equal(t, id, dets.WriteID)
	})

	t.Run("continue last", func(t *testing.T) {
		mods := newMods(t)
		id := newSessionID()
		require.NoError(t, mods.db.Save(id, "message 1", "openai", "gpt-4"))
		mods.Config.ContinueLast = true
		mods.Config.Prefix = "prompt"
		msg := mods.findSessionDetails()()
		dets := msg.(sessionDetailsMsg)
		require.Equal(t, id, dets.ReadID)
		require.Equal(t, id, dets.WriteID)
		require.Empty(t, dets.Title)
	})

	t.Run("continue missing name fails", func(t *testing.T) {
		mods := newMods(t)
		id := newSessionID()
		require.NoError(t, mods.db.Save(id, "message 1", "openai", "gpt-4"))
		mods.Config.Continue = "message 2"
		mods.Config.Prefix = "prompt"
		msg := mods.findSessionDetails()()
		_, ok := msg.(modsError)
		require.True(t, ok, "expected missing explicit continue target to fail")
	})

	t.Run("write", func(t *testing.T) {
		mods := newMods(t)
		msg := mods.findSessionDetails()()
		dets := msg.(sessionDetailsMsg)
		require.Empty(t, dets.ReadID)
		require.NotEmpty(t, dets.WriteID)
		require.Empty(t, dets.Title)
	})

	t.Run("no session save does not restore rules", func(t *testing.T) {
		mods := newMods(t)
		id := newSessionID()
		require.NoError(t, mods.db.SaveSession(
			id,
			"message",
			"openai",
			"gpt-4",
			[]proto.Message{{Role: proto.RoleUser, Content: "message"}},
			[]ApprovalRule{scopedRule(ApprovalRule{Type: approvalEditAll, Tool: "file_edit"})},
		))
		mods.Config.Continue = id
		mods.Config.Prefix = "prompt"
		mods.Config.NoSave = true
		msg := mods.findSessionDetails()()
		dets := msg.(sessionDetailsMsg)
		require.Empty(t, dets.Rules)
	})

	t.Run("continue id and write with title", func(t *testing.T) {
		mods := newMods(t)
		mods.Config.Model = "claude-3.7-sonnet"
		mods.Config.API = "anthropic"

		msg := mods.findSessionDetails()()
		dets := msg.(sessionDetailsMsg)

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
		require.Equal(t, "Shell: go test ./...", got)
	})

	t.Run("shell command skips leading comment", func(t *testing.T) {
		got := toolOperationLabel("shell_run", []byte(`{"command":"# check workspace config\nls .opencode*"}`), 80)
		require.Equal(t, "Shell: ls .opencode*", got)
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

	t.Run("largest files path", func(t *testing.T) {
		got := toolOperationLabel("fs_largest", []byte(`{"path":"Downloads","kind":"file"}`), 80)
		require.Equal(t, "Finding largest file in: Downloads", got)
	})

	t.Run("copy path", func(t *testing.T) {
		got := toolOperationLabel("fs_copy", []byte(`{"source_path":"a.txt","dest_path":"b.txt"}`), 80)
		require.Equal(t, "Copying: a.txt to b.txt", got)
	})

	t.Run("move path", func(t *testing.T) {
		got := toolOperationLabel("fs_move", []byte(`{"source_path":"a.txt","dest_path":"b.txt"}`), 80)
		require.Equal(t, "Moving: a.txt to b.txt", got)
	})

	t.Run("thinking note", func(t *testing.T) {
		got := toolOperationLabel("thinking_note", []byte(`{"thought":"checking the next step","done":false}`), 80)
		require.Equal(t, "Thinking: checking the next step", got)
	})

	t.Run("skill load body", func(t *testing.T) {
		got := toolOperationLabel("load_skill", []byte(`{"name":"mcp-builder"}`), 80)
		require.Equal(t, "Loading skill: mcp-builder", got)
	})

	t.Run("skill load aux file", func(t *testing.T) {
		got := toolOperationLabel("load_skill", []byte(`{"name":"mcp-builder","file":"reference/foo.md"}`), 80)
		require.Equal(t, "Loading skill: mcp-builder (reference/foo.md)", got)
	})

	t.Run("skill load no name", func(t *testing.T) {
		got := toolOperationLabel("load_skill", []byte(`{}`), 80)
		require.Equal(t, "Loading skill", got)
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

func systemContents(messages []proto.Message) []string {
	out := make([]string, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == proto.RoleSystem {
			out = append(out, msg.Content)
		}
	}
	return out
}

func TestSetupStreamContextMinimal(t *testing.T) {
	newTestMods := func(cfg Config) *Mods {
		cfg.ApplyDefaults()
		if cfg.Roles == nil {
			cfg.Roles = map[string][]string{}
		}
		return &Mods{
			Config: &cfg,
			Styles: makeStyles(true),
			ctx:    context.Background(),
		}
	}
	model := Model{MaxChars: 1000}

	t.Run("minimal disabled", func(t *testing.T) {
		m := newTestMods(Config{})
		require.NoError(t, m.setupStreamContext("hello", model))
		require.NotContains(t, systemContents(m.messages), minimalSystemPrompt)
	})

	t.Run("minimal adds system prompt", func(t *testing.T) {
		m := newTestMods(Config{PersistentConfig: PersistentConfig{Minimal: true}})
		require.NoError(t, m.setupStreamContext("hello", model))
		require.Contains(t, systemContents(m.messages), minimalSystemPrompt)
	})

	t.Run("minimal suppresses format prompt", func(t *testing.T) {
		m := newTestMods(Config{PersistentConfig: PersistentConfig{Minimal: true, Format: "markdown"}})
		require.NoError(t, m.setupStreamContext("hello", model))
		systemMessages := systemContents(m.messages)
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
		systemMessages := systemContents(m.messages)
		roleIndex := slices.Index(systemMessages, "role prompt")
		minimalIndex := slices.Index(systemMessages, minimalSystemPrompt)
		require.NotEqual(t, -1, roleIndex)
		require.NotEqual(t, -1, minimalIndex)
		require.Less(t, roleIndex, minimalIndex)
	})
}

func TestSetupStreamContextFormatFallback(t *testing.T) {
	newTestMods := func(cfg Config) *Mods {
		if cfg.Roles == nil {
			cfg.Roles = map[string][]string{}
		}
		if cfg.FormatText == nil {
			cfg.FormatText = defaultConfig().FormatText
		}
		return &Mods{
			Config: &cfg,
			Styles: makeStyles(true),
			ctx:    context.Background(),
		}
	}
	model := Model{MaxChars: 1000}

	t.Run("defined format injects its format-text", func(t *testing.T) {
		cfg := Config{PersistentConfig: PersistentConfig{Format: "csv"}}
		cfg.FormatText = FormatText{"csv": "Return CSV only."}
		m := newTestMods(cfg)
		require.NoError(t, m.setupStreamContext("hello", model))
		require.Contains(t, systemContents(m.messages), "Return CSV only.")
	})

	t.Run("undefined format falls back to markdown text", func(t *testing.T) {
		m := newTestMods(Config{PersistentConfig: PersistentConfig{Format: "xml"}})
		require.NoError(t, m.setupStreamContext("hello", model))
		require.Contains(t, systemContents(m.messages), defaultMarkdownFormatText)
	})

	t.Run("json format injects json text from format-text", func(t *testing.T) {
		m := newTestMods(Config{PersistentConfig: PersistentConfig{Format: "json"}})
		require.NoError(t, m.setupStreamContext("hello", model))
		require.Contains(t, systemContents(m.messages), defaultJSONFormatText)
	})

	t.Run("json format falls back when format-text has no json entry", func(t *testing.T) {
		cfg := Config{PersistentConfig: PersistentConfig{Format: "json"}}
		cfg.FormatText = FormatText{}
		m := newTestMods(cfg)
		require.NoError(t, m.setupStreamContext("hello", model))
		require.Contains(t, systemContents(m.messages), defaultJSONFormatText)
	})

	t.Run("empty format injects nothing", func(t *testing.T) {
		m := newTestMods(Config{})
		require.NoError(t, m.setupStreamContext("hello", model))
		require.NotContains(t, systemContents(m.messages), defaultMarkdownFormatText)
	})
}

func TestSetupStreamContextIdentityPrompt(t *testing.T) {
	newTestMods := func(cfg Config) *Mods {
		if cfg.Roles == nil {
			cfg.Roles = map[string][]string{}
		}
		if cfg.FormatText == nil {
			cfg.FormatText = defaultConfig().FormatText
		}
		return &Mods{
			Config: &cfg,
			Styles: makeStyles(true),
			ctx:    context.Background(),
		}
	}
	model := Model{MaxChars: 1000}

	t.Run("normal mode injects identity", func(t *testing.T) {
		m := newTestMods(Config{})
		require.NoError(t, m.setupStreamContext("hello", model))
		require.Contains(t, systemContents(m.messages), modsIdentityPrompt)
	})

	t.Run("system info uses workspace field", func(t *testing.T) {
		m := newTestMods(Config{})
		require.NoError(t, m.setupStreamContext("hello", model))
		require.NotEmpty(t, m.messages)
		require.Contains(t, m.messages[0].Content, "workspace=")
		require.NotContains(t, m.messages[0].Content, "workspace_root=")
	})

	t.Run("minimal mode skips identity", func(t *testing.T) {
		m := newTestMods(Config{PersistentConfig: PersistentConfig{Minimal: true}})
		require.NoError(t, m.setupStreamContext("hello", model))
		require.NotContains(t, systemContents(m.messages), modsIdentityPrompt)
	})

	t.Run("plan mode injects identity and plan prompt", func(t *testing.T) {
		m := newTestMods(Config{})
		require.NoError(t, m.setupPlanContext("hello", model))
		contents := systemContents(m.messages)
		require.Contains(t, contents, modsIdentityPrompt)
		require.Contains(t, contents, planSystemPrompt)
	})

	t.Run("identity prompt override", func(t *testing.T) {
		m := newTestMods(Config{PersistentConfig: PersistentConfig{
			Prompts: PromptConfig{Identity: "custom identity"},
		}})
		require.NoError(t, m.setupStreamContext("hello", model))
		contents := systemContents(m.messages)
		require.Contains(t, contents, "custom identity")
		require.NotContains(t, contents, modsIdentityPrompt)
	})

	t.Run("tool selection prompt override", func(t *testing.T) {
		m := newTestMods(Config{PersistentConfig: PersistentConfig{
			Prompts: PromptConfig{ToolSelection: "custom tool rules"},
		}})
		require.NoError(t, m.setupStreamContext("hello", model))
		contents := systemContents(m.messages)
		require.Contains(t, contents, "custom tool rules")
		require.NotContains(t, contents, ToolSelectionRules)
	})

	t.Run("prompt override loads file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "identity.txt")
		require.NoError(t, os.WriteFile(path, []byte("identity from file"), 0o644))
		m := newTestMods(Config{PersistentConfig: PersistentConfig{
			Prompts: PromptConfig{Identity: "file://" + path},
		}})
		require.NoError(t, m.setupStreamContext("hello", model))
		require.Contains(t, systemContents(m.messages), "identity from file")
	})
}

func TestSetupStreamContextInjectsSkillCatalog(t *testing.T) {
	newTestMods := func(cfg Config) *Mods {
		if cfg.Roles == nil {
			cfg.Roles = map[string][]string{}
		}
		if cfg.FormatText == nil {
			cfg.FormatText = defaultConfig().FormatText
		}
		return &Mods{
			Config: &cfg,
			Styles: makeStyles(true),
			ctx:    context.Background(),
		}
	}
	model := Model{MaxChars: 1000}

	t.Run("non-empty catalog injects Available skills section", func(t *testing.T) {
		root := t.TempDir()
		skillDir := filepath.Join(root, "demo", "SKILL.md")
		require.NoError(t, os.MkdirAll(filepath.Dir(skillDir), 0o700))
		require.NoError(t, os.WriteFile(skillDir, []byte("---\nname: demo\ndescription: Demo skill.\n---\n\nbody.\n"), 0o600))
		catalog, err := skills.Scan(root)
		require.NoError(t, err)
		require.Len(t, catalog, 1)

		m := newTestMods(Config{})
		m.skillCatalog = catalog
		require.NoError(t, m.setupStreamContext("hello", model))
		joined := strings.Join(systemContents(m.messages), "\n")
		require.Contains(t, joined, "## Available skills")
		require.Contains(t, joined, "demo: Demo skill.")
	})

	t.Run("empty catalog skips injection", func(t *testing.T) {
		m := newTestMods(Config{})
		require.NoError(t, m.setupStreamContext("hello", model))
		joined := strings.Join(systemContents(m.messages), "\n")
		require.NotContains(t, joined, "Available skills")
	})

	t.Run("minimal mode skips injection even with non-empty catalog", func(t *testing.T) {
		root := t.TempDir()
		skillDir := filepath.Join(root, "demo", "SKILL.md")
		require.NoError(t, os.MkdirAll(filepath.Dir(skillDir), 0o700))
		require.NoError(t, os.WriteFile(skillDir, []byte("---\nname: demo\ndescription: Demo.\n---\n\nbody.\n"), 0o600))
		catalog, err := skills.Scan(root)
		require.NoError(t, err)
		require.Len(t, catalog, 1)

		m := newTestMods(Config{PersistentConfig: PersistentConfig{Minimal: true}})
		m.skillCatalog = catalog
		require.NoError(t, m.setupStreamContext("hello", model))
		joined := strings.Join(systemContents(m.messages), "\n")
		require.NotContains(t, joined, "Available skills")
	})
}

func TestSetupPlanContextPromptPolicy(t *testing.T) {
	cfg := Config{}
	cfg.Roles = map[string][]string{}
	cfg.FormatText = defaultConfig().FormatText
	cfg.Format = "markdown"
	m := &Mods{
		Config: &cfg,
		Styles: makeStyles(true),
		ctx:    context.Background(),
	}

	require.NoError(t, m.setupPlanContext("hello", Model{MaxChars: 1000}))

	systemMessages := systemContents(m.messages)
	require.Contains(t, systemMessages, planSystemPrompt)
	require.Contains(t, planSystemPrompt, "Use platform-appropriate read-only commands")
	for _, msg := range systemMessages {
		require.NotContains(t, msg, "Safe temporary workspace:")
	}
}

func TestSetupPlanContextPromptOverride(t *testing.T) {
	cfg := Config{PersistentConfig: PersistentConfig{
		Prompts: PromptConfig{Plan: "custom plan prompt"},
	}}
	cfg.Roles = map[string][]string{}
	cfg.FormatText = defaultConfig().FormatText
	cfg.Format = "markdown"
	m := &Mods{
		Config: &cfg,
		Styles: makeStyles(true),
		ctx:    context.Background(),
	}

	require.NoError(t, m.setupPlanContext("hello", Model{MaxChars: 1000}))

	systemMessages := systemContents(m.messages)
	require.Contains(t, systemMessages, "custom plan prompt")
	require.NotContains(t, systemMessages, planSystemPrompt)
}

func TestShellClassifierPromptResolution(t *testing.T) {
	m := &Mods{Config: &Config{PersistentConfig: PersistentConfig{
		ShellClassifyPrompt: "legacy yes no",
		Prompts:             PromptConfig{ShellClassifier: "custom json"},
	}}}

	system, structured, err := m.resolveShellClassifierPrompt()

	require.NoError(t, err)
	require.Equal(t, "custom json", system)
	require.True(t, structured)

	m.Config.Prompts.ShellClassifier = ""
	system, structured, err = m.resolveShellClassifierPrompt()
	require.NoError(t, err)
	require.Equal(t, "legacy yes no", system)
	require.False(t, structured)
}

func TestShellClassifyCacheKeyIncludesPromptAndMode(t *testing.T) {
	keyA := shellClassifyCacheKey("shell_run", "rm out", "json", "prompt a")
	keyB := shellClassifyCacheKey("shell_run", "rm out", "json", "prompt b")
	keyC := shellClassifyCacheKey("shell_run", "rm out", "yesno", "prompt a")

	require.NotEqual(t, keyA, keyB)
	require.NotEqual(t, keyA, keyC)
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
	require.Equal(t, proto.RoleUser, m.messages[1].Role)
	require.Equal(t, proto.RoleSystem, m.messages[2].Role)
	require.Contains(t, m.messages[2].Content, "The user has approved the following plan for execution:")
	require.Contains(t, m.messages[2].Content, "Follow this approved plan during execution.")
	require.Contains(t, m.messages[2].Content, "If new information requires changing it")
	require.Empty(t, m.planContent)
}

func TestPlanHistoryCarriedIntoExecution(t *testing.T) {
	// Plan-phase conversation: system block (including the plan-mode prompt
	// that forbids execution) + user request + investigation + proposed plan.
	m := &Mods{
		Config:      &Config{},
		planContent: "1. Do the approved thing",
		messages: []proto.Message{
			{Role: proto.RoleSystem, Content: "CRITICAL - PLANNING PHASE ONLY, do not execute"},
			{Role: proto.RoleUser, Content: "refactor the foo function"},
			{Role: proto.RoleAssistant, Content: "Investigation: foo.go defines bar() at line 12."},
			{Role: proto.RoleTool, Content: "<file contents of foo.go>"},
			{Role: proto.RoleAssistant, Content: "# Plan\n1. Do the approved thing"},
		},
	}

	// Approval transitions to execution: snapshot before the rebuild resets.
	m.capturePlanHistory()
	// System messages (incl. the plan-mode prompt) must be excluded.
	require.Len(t, m.planHistory, 4) // user + assistant + tool + plan
	for _, msg := range m.planHistory {
		require.NotContains(t, msg.Content, "PLANNING PHASE ONLY")
	}

	// Execution rebuilds the system block fresh and re-appends the request.
	m.messages = []proto.Message{
		{Role: proto.RoleSystem, Content: "sysInfo"},
		{Role: proto.RoleSystem, Content: "identity"},
		{Role: proto.RoleUser, Content: "refactor the foo function"},
	}
	m.injectPlanHistory()
	m.injectApprovedPlan()

	// The investigation must be carried; the plan-mode prompt must not leak.
	found := false
	for _, msg := range m.messages {
		require.NotContains(t, msg.Content, "PLANNING PHASE ONLY",
			"execution must not carry the plan-mode prompt")
		if msg.Content == "Investigation: foo.go defines bar() at line 12." {
			found = true
		}
	}
	require.True(t, found, "execution must carry the plan-phase investigation")
	// The approved-plan instruction lands last (after the proposed plan).
	last := m.messages[len(m.messages)-1]
	require.Equal(t, proto.RoleSystem, last.Role)
	require.Contains(t, last.Content, "approved")
	require.Empty(t, m.planHistory)
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
	cfg.Format = "markdown"
	m := &Mods{
		Config: &cfg,
		Styles: makeStyles(true),
		ctx:    context.Background(),
	}
	require.NoError(t, m.setupStreamContext("hello", Model{MaxChars: 1000}))
	require.NotEmpty(t, m.messages)
	require.Contains(t, m.messages[0].Content, "powershell=")
	require.Contains(t, m.messages[0].Content, "pwsh=")
}

func newAnimatingMods() *Mods {
	return &Mods{
		Config:              &Config{},
		Styles:              makeStyles(true),
		anim:                staticModel("animating"),
		state:               requestState,
		showOperationStatus: true,
		width:               80,
		reviewer:            &toolReviewer{},
		contentMutex:        &sync.Mutex{},
	}
}

func TestOperationStatusView(t *testing.T) {
	t.Run("shows active operation", func(t *testing.T) {
		m := newAnimatingMods()
		_, _ = m.Update(toolOperationStatusMsg{content: "Shell: go test ./..."})
		require.Contains(t, m.View().Content, "Shell: go test ./...")
	})

	t.Run("clears active operation", func(t *testing.T) {
		m := newAnimatingMods()
		_, _ = m.Update(toolOperationStatusMsg{content: "Running tool: fs_read_file"})
		_, _ = m.Update(toolOperationStatusMsg{done: true})
		require.NotContains(t, m.View().Content, "Running tool: fs_read_file")
	})

	t.Run("hide tool status hides active operation", func(t *testing.T) {
		m := newAnimatingMods()
		m.Config.HideToolStatus = true
		_, _ = m.Update(toolOperationStatusMsg{content: "Shell: go test ./..."})
		require.NotContains(t, m.View().Content, "Shell: go test ./...")
	})

	t.Run("reasoning alone does not show status", func(t *testing.T) {
		m := newAnimatingMods()
		m.thinkActive = true
		view := m.renderWithOperation("answer")
		require.Equal(t, "answer", view)
		require.NotContains(t, view, "[R]")
		require.NotContains(t, view, "Reasoning")
	})

	t.Run("active operation while reasoning renders without reasoning badge", func(t *testing.T) {
		m := newAnimatingMods()
		m.thinkActive = true
		m.setActiveOperation("Shell: go test ./...")
		view := m.renderWithOperation("answer")
		require.Contains(t, view, "Shell: go test ./...")
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
		Styles:         makeStyles(true),
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
		Styles:                makeStyles(true),
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
	oldTTY := IsOutputTTY
	IsOutputTTY = func() bool { return true }
	t.Cleanup(func() { IsOutputTTY = oldTTY })

	t.Run("request state shows spinner", func(t *testing.T) {
		m := newAnimatingMods()
		require.Contains(t, m.View().Content, "animating")
	})

	t.Run("request state does not show approved plan", func(t *testing.T) {
		m := newAnimatingMods()
		m.Output = "approved plan"
		m.glamOutput = "rendered approved plan"
		view := m.View().Content
		require.Contains(t, view, "animating")
		require.NotContains(t, view, "approved plan")
		require.NotContains(t, view, "rendered approved plan")
	})

	t.Run("response state before output shows spinner", func(t *testing.T) {
		m := newAnimatingMods()
		m.state = responseState
		require.Contains(t, m.View().Content, "animating")
	})

	t.Run("response state before output shows operation and spinner", func(t *testing.T) {
		m := newAnimatingMods()
		m.state = responseState
		m.setActiveOperation("Searching web: Go latest release")
		view := m.View().Content
		require.Contains(t, view, "Searching web: Go latest release")
		require.Contains(t, view, "animating",
			"the spinner stays on alongside the tool/search operation label")
	})

	t.Run("response state before output renders the animation", func(t *testing.T) {
		m := newAnimatingMods()
		m.state = responseState

		view := m.View()
		require.NotEmpty(t, strings.TrimSpace(view.Content))
	})

	t.Run("first output hides spinner", func(t *testing.T) {
		m := newAnimatingMods()
		m.Config.Raw = true
		_, _ = m.Update(streamEventMsg{
			kind:   streamEventChunk,
			chunk:  proto.Chunk{Content: "hello"},
			runner: newStreamRunner(staticStream{}, nil, nil, func(err error) tea.Msg { return modsError{Err: err} }),
		})
		require.True(t, m.responseOutputStarted)
		require.Contains(t, m.Output, "hello")
		require.NotContains(t, m.renderWithOperation(m.Output), "animating")
	})
}

func TestSpinnerPhaseDerivation(t *testing.T) {
	m := &Mods{
		Config:   &Config{},
		reviewer: &toolReviewer{},
	}

	// No output, no operation, not thinking -> connecting.
	require.Equal(t, PhaseConnecting, m.spinnerPhase())

	// First token streamed -> streaming.
	m.responseOutputStarted = true
	require.Equal(t, PhaseStreaming, m.spinnerPhase())

	// Reasoning (-t) also counts as streaming before any answer token.
	m.responseOutputStarted = false
	m.thinkActive = true
	require.Equal(t, PhaseStreaming, m.spinnerPhase())
	m.thinkActive = false

	// An active tool/search operation wins over streaming.
	m.responseOutputStarted = true
	m.setActiveOperation("Running: go test ./...")
	require.Equal(t, PhaseTool, m.spinnerPhase())
}

// TestShouldUpdateAnimationTicksDuringStreaming locks in the always-on change:
// the spinner used to stop ticking the moment responseOutputStarted flipped
// true. It must now keep ticking through every running phase and only pause in
// terminal states.
func TestShouldUpdateAnimationTicksDuringStreaming(t *testing.T) {
	oldTTY := IsOutputTTY
	IsOutputTTY = func() bool { return true }
	t.Cleanup(func() { IsOutputTTY = oldTTY })

	m := &Mods{
		Config:   &Config{},
		state:    responseState,
		anim:     staticModel("animating"),
		reviewer: &toolReviewer{},
	}

	require.True(t, m.shouldUpdateAnimation(), "pre-output ticks")
	m.responseOutputStarted = true
	require.True(t, m.shouldUpdateAnimation(), "streaming still ticks (regression)")
	m.state = doneState
	require.False(t, m.shouldUpdateAnimation(), "terminal state stops ticking")
}

// TestPlanStreamingStaysInResponseState locks in the fix for the plan-mode
// flicker: while the model streams text (with or without interleaved tool
// calls), the state must stay in responseState so the streaming output stays
// visible. Switching to planState mid-stream hid the accumulated output behind
// the spinner, then startToolCalls switched back to responseState
// (revealing it), producing a visible flicker on every text→tool→text round.
func TestPlanStreamingStaysInResponseState(t *testing.T) {
	oldIsOutputTTY := isOutputTTY
	oldExportedIsOutputTTY := IsOutputTTY
	isOutputTTY = func() bool { return true }
	IsOutputTTY = func() bool { return true }
	defer func() {
		isOutputTTY = oldIsOutputTTY
		IsOutputTTY = oldExportedIsOutputTTY
	}()

	gr, err := glamour.NewTermRenderer(glamour.WithStandardStyle("dark"))
	require.NoError(t, err)

	m := &Mods{
		Config:              &Config{Plan: true},
		Styles:              makeStyles(true),
		anim:                staticModel("animating"),
		state:               planState,
		showOperationStatus: true,
		width:               80,
		reviewer:            &toolReviewer{},
		contentMutex:        &sync.Mutex{},
		glam:                gr,
		glamViewport:        viewport.New(viewport.WithWidth(80), viewport.WithHeight(20)),
	}
	runner := newStreamRunner(staticStream{}, nil, nil, func(err error) tea.Msg { return modsError{Err: err} })

	_, _ = m.Update(streamEventMsg{
		kind:   streamEventChunk,
		chunk:  proto.Chunk{Content: "Investigating the codebase..."},
		runner: runner,
	})

	require.Equal(t, responseState, m.state,
		"plan-mode streaming must stay in responseState so the output stays visible")
	require.True(t, m.responseOutputStarted)
	require.Contains(t, m.Output, "Investigating the codebase...")
	// The streamed text must be visible in the View, not hidden. With the
	// always-on spinner the animation renders on the line below the output;
	// verify the output text still appears (Glamour wraps words in ANSI spans,
	// so check a fragment that stays within one span) and that the spinner
	// renders beneath it rather than replacing the output.
	view := m.View().Content
	require.Contains(t, view, "Investigating")
	require.Contains(t, view, "animating")
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
		{
			// A custom-named provider that declares the Anthropic wire
			// protocol via api-type.
			Name:    "acme-claude",
			APIType: "anthropic",
			Models: map[string]Model{
				"acme-sonnet-4": {Aliases: []string{"acme"}},
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

	t.Run("api-type overrides name-based routing", func(t *testing.T) {
		cfg := &Config{PersistentConfig: PersistentConfig{API: "acme-claude", Model: "acme", APIs: apis}}
		api, mod, err := m.resolveModel(cfg)
		require.NoError(t, err)
		require.Equal(t, "acme-claude", api.Name, "provider keeps its own name")
		require.Equal(t, "acme-sonnet-4", mod.Name)
		require.Equal(t, "anthropic", mod.API, "api-type should route through the Anthropic adapter")
	})

	t.Run("api-type absent falls back to name", func(t *testing.T) {
		cfg := &Config{PersistentConfig: PersistentConfig{API: "acme-claude", Model: "acme", APIs: APIs{
			{Name: "acme-claude", Models: map[string]Model{"acme-sonnet-4": {Aliases: []string{"acme"}}}},
		}}}
		_, mod, err := m.resolveModel(cfg)
		require.NoError(t, err)
		require.Equal(t, "acme-claude", mod.API, "without api-type the provider name is used (OpenAI-compatible default)")
	})

	t.Run("api-type openai is a no-op", func(t *testing.T) {
		cfg := &Config{PersistentConfig: PersistentConfig{API: "acme-claude", Model: "acme", APIs: APIs{
			{Name: "acme-claude", APIType: "openai", Models: map[string]Model{"acme-sonnet-4": {Aliases: []string{"acme"}}}},
		}}}
		_, mod, err := m.resolveModel(cfg)
		require.NoError(t, err)
		require.Equal(t, "acme-claude", mod.API, "api-type openai keeps the provider name (true no-op)")
	})

	t.Run("api-type unknown falls back to name", func(t *testing.T) {
		cfg := &Config{PersistentConfig: PersistentConfig{API: "acme-claude", Model: "acme", APIs: APIs{
			{Name: "acme-claude", APIType: "anthrpic", Models: map[string]Model{"acme-sonnet-4": {Aliases: []string{"acme"}}}},
		}}}
		_, mod, err := m.resolveModel(cfg)
		require.NoError(t, err)
		require.Equal(t, "acme-claude", mod.API, "an unrecognized api-type falls back to the provider name instead of keeping the typo")
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

	t.Run("missing key names configured env over default", func(t *testing.T) {
		t.Setenv("OPENCODE_API_KEY", "")
		t.Setenv("ANTHROPIC_API_KEY", "")
		api := API{APIKeyEnv: "OPENCODE_API_KEY"}
		_, err := m.ensureKey(api, "ANTHROPIC_API_KEY", "https://console.anthropic.com/settings/keys")
		require.Error(t, err)
		merr := err.(modsError)
		require.Contains(t, merr.ReasonText, "OPENCODE_API_KEY")
		require.NotContains(t, merr.ReasonText, "ANTHROPIC_API_KEY")
	})
}

func TestAppendShellResult(t *testing.T) {
	newMods := func() *Mods {
		return &Mods{Config: &Config{}, contentMutex: &sync.Mutex{}}
	}

	t.Run("success appends block", func(t *testing.T) {
		m := newMods()
		m.Config.ShowToolResults = true
		m.appendShellResult("shell_run", []byte(`{"command":"ls -la"}`), nil)
		require.Contains(t, m.Output, "> \u2713 ran `ls -la`")
		require.Contains(t, m.Output, "exit 0")
	})

	t.Run("failure appends exit code", func(t *testing.T) {
		m := newMods()
		m.Config.ShowToolResults = true
		m.appendShellResult("shell_run", []byte(`{"command":"npm test"}`), toolregistry.ShellExitError{Code: 1})
		require.Contains(t, m.Output, "> \u2717 ran `npm test`")
		require.Contains(t, m.Output, "exit 1")
	})

	t.Run("default suppresses tool results", func(t *testing.T) {
		m := newMods()
		m.appendShellResult("shell_run", []byte(`{"command":"ls"}`), nil)
		require.Empty(t, m.Output)
	})

	t.Run("non shell tool is skipped", func(t *testing.T) {
		m := newMods()
		m.appendShellResult("fs_read_file", []byte(`{"path":"/a"}`), nil)
		require.Empty(t, m.Output)
	})

	t.Run("block separates from prior content", func(t *testing.T) {
		m := newMods()
		m.Config.ShowToolResults = true
		m.appendToOutput("thinking...")
		m.appendShellResult("shell_run", []byte(`{"command":"ls"}`), nil)
		require.Contains(t, m.Output, "thinking...\n\n> \u2713 ran `ls`")
	})
}

func TestToolCallFailed(t *testing.T) {
	t.Run("nil is not a failure", func(t *testing.T) {
		require.False(t, toolCallFailed(nil))
	})
	t.Run("plain error is a failure", func(t *testing.T) {
		require.True(t, toolCallFailed(errors.New("boom")))
	})
	t.Run("non-zero shell exit is not a failure", func(t *testing.T) {
		require.False(t, toolCallFailed(toolregistry.ShellExitError{Code: 1}))
	})
	t.Run("wrapped non-zero shell exit is not a failure", func(t *testing.T) {
		require.False(t, toolCallFailed(fmt.Errorf("wrapped: %w", toolregistry.ShellExitError{Code: 2})))
	})
}

func TestPlanCompleteRejectsNonPlanOutput(t *testing.T) {
	m := &Mods{
		Config:         &Config{Plan: true},
		Styles:         makeStyles(true),
		reviewer:       &toolReviewer{},
		width:          80,
		operationMutex: sync.Mutex{},
	}
	model, cmd := m.Update(planCompleteMsg{plan: "好的，我先调查一下你当前的 opencode 配置和相关环境。"})
	m = model.(*Mods)
	require.NotNil(t, cmd, "non-plan output must not silently enter plan review")
	msg := cmd()
	mErr, ok := msg.(modsError)
	require.True(t, ok, "expected a modsError when no plan was produced, got %T", msg)
	require.Contains(t, mErr.ReasonText, "without producing a plan")
	require.NotEqual(t, planState, m.state, "must not enter plan review without a real plan")
}

func TestPlanCompleteAcceptsStructuredPlan(t *testing.T) {
	m := &Mods{
		Config:         &Config{Plan: true},
		Styles:         makeStyles(true),
		reviewer:       &toolReviewer{},
		width:          80,
		operationMutex: sync.Mutex{},
	}
	plan := "## Plan\n- **Approach**: do it\n- **Steps**: 1. thing\n- **Files**: internal/app/mods.go\n- **Risks**: low"
	model, cmd := m.Update(planCompleteMsg{plan: plan})
	m = model.(*Mods)
	require.Nil(t, cmd)
	require.Equal(t, planState, m.state)
	require.Equal(t, plan, m.planContent)
}

// retryResetMods builds a minimal Mods for testing the retries=0 reset on
// plan/exec lineage transitions. It avoids the full New() constructor so the
// test stays focused on Update state machine semantics.
func retryResetMods(t *testing.T) *Mods {
	t.Helper()
	return &Mods{
		Config:         &Config{},
		Styles:         makeStyles(true),
		reviewer:       &toolReviewer{},
		contentMutex:   &sync.Mutex{},
		operationMutex: sync.Mutex{},
		ctx:            context.Background(),
	}
}

// TestPlanExecutionStartResetsRetries asserts that transitioning from a
// planning phase into execution clears the API retry counter, so plan-phase
// retries do not steal back-off budget from execution.
func TestPlanExecutionStartResetsRetries(t *testing.T) {
	m := retryResetMods(t)
	m.retries = 4
	m.Input = "do thing"
	_, _ = m.Update(planExecutionStartMsg{})
	require.Equal(t, 0, m.retries, "planExecutionStartMsg must reset m.retries")
}

// TestPlanDeniedResetsRetries asserts that abandoning a plan and starting a
// fresh planning attempt clears the API retry counter.
func TestPlanDeniedResetsRetries(t *testing.T) {
	m := retryResetMods(t)
	m.Config.Prefix = "ignored"
	m.retries = 3
	m.planRetries = 0
	_, _ = m.Update(planDeniedMsg{content: "ignored"})
	require.Equal(t, 0, m.retries, "planDeniedMsg must reset m.retries before the next plan")
}

// TestPlanModifyResetsRetries asserts that user-driven plan revision clears
// the API retry counter.
func TestPlanModifyResetsRetries(t *testing.T) {
	m := retryResetMods(t)
	m.retries = 2
	_, _ = m.Update(planModifyMsg{feedback: "redo", plan: "old plan"})
	require.Equal(t, 0, m.retries, "planModifyMsg must reset m.retries before the revised plan")
}

func TestStreamDoneAccumulatesUsageAcrossLineagesOnce(t *testing.T) {
	m := testMods(t)
	first := newStreamRunner(staticStream{usage: proto.TokenUsage{
		InputTokens: 10, OutputTokens: 4, TotalTokens: 14,
	}}, nil, nil, func(error) tea.Msg { return nil })
	second := newStreamRunner(staticStream{usage: proto.TokenUsage{
		InputTokens: 20, OutputTokens: 6, TotalTokens: 26,
	}}, nil, nil, func(error) tea.Msg { return nil })

	m.Config.Plan = true
	_, _ = m.Update(first.doneMsg())
	_, _ = m.Update(first.doneMsg()) // duplicate delivery must not double count
	m.Config.Plan = false
	_, _ = m.Update(second.doneMsg())

	require.Equal(t, proto.TokenUsage{
		InputTokens: 30, OutputTokens: 10, TotalTokens: 40,
	}, m.TokenUsage())
}
