package main

import (
	"context"
	"fmt"
	"slices"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

type staticModel string

func (s staticModel) Init() tea.Cmd { return nil }

func (s staticModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return s, nil }

func (s staticModel) View() string { return string(s) }

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
		require.NoError(t, mods.db.Save(id, "message", "openai", "gpt-4"))
		mods.Config.Continue = id[:5]
		mods.Config.Prefix = "prompt"
		msg := mods.findCacheOpsDetails()()
		dets := msg.(cacheDetailsMsg)
		require.Equal(t, id, dets.ReadID)
		require.Equal(t, id, dets.WriteID)
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

	t.Run("continue last with name", func(t *testing.T) {
		mods := newMods(t)
		id := newConversationID()
		require.NoError(t, mods.db.Save(id, "message 1", "openai", "gpt-4"))
		mods.Config.Continue = "message 2"
		mods.Config.Prefix = "prompt"
		msg := mods.findCacheOpsDetails()()
		dets := msg.(cacheDetailsMsg)
		require.Equal(t, id, dets.ReadID)
		require.Equal(t, "message 2", dets.Title)
		require.NotEmpty(t, dets.WriteID)
		require.Equal(t, id, dets.WriteID)
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
		require.NoError(t, mods.db.Save(id, "message 1", "openai", "gpt-4"))
		mods.Config.Title = "some title"
		mods.Config.Continue = id[:10]
		msg := mods.findCacheOpsDetails()()
		dets := msg.(cacheDetailsMsg)
		require.Equal(t, id, dets.ReadID)
		require.NotEmpty(t, dets.WriteID)
		require.NotEqual(t, id, dets.WriteID)
		require.NotEqual(t, "some title", dets.WriteID)
		require.Equal(t, "some title", dets.Title)
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
		require.Equal(t, "Could not find the conversation.", err.reason)
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
		m := newTestMods(Config{Minimal: true})
		require.NoError(t, m.setupStreamContext("hello", model))
		require.Contains(t, contents(m.messages), minimalSystemPrompt)
	})

	t.Run("minimal suppresses format prompt", func(t *testing.T) {
		m := newTestMods(Config{Minimal: true, Format: true})
		require.NoError(t, m.setupStreamContext("hello", model))
		systemMessages := contents(m.messages)
		require.Contains(t, systemMessages, minimalSystemPrompt)
		require.NotContains(t, systemMessages, defaultMarkdownFormatText)
	})

	t.Run("minimal follows role prompt", func(t *testing.T) {
		m := newTestMods(Config{
			Minimal: true,
			Role:    "shell",
			Roles:   map[string][]string{"shell": {"role prompt"}},
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
}

func TestViewportNeeded(t *testing.T) {
	t.Run("viewport taller than window", func(t *testing.T) {
		m := &Mods{glamHeight: 100, height: 50}
		require.True(t, m.viewportNeeded())
	})
	t.Run("viewport shorter than window", func(t *testing.T) {
		m := &Mods{glamHeight: 10, height: 50}
		require.False(t, m.viewportNeeded())
	})
	t.Run("equal", func(t *testing.T) {
		m := &Mods{glamHeight: 50, height: 50}
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
		cfg := &Config{API: "openai", Model: "gpt-4", APIs: apis}
		api, mod, err := m.resolveModel(cfg)
		require.NoError(t, err)
		require.Equal(t, "openai", api.Name)
		require.Equal(t, "gpt-4", mod.Name)
		require.Equal(t, "openai", mod.API)
	})

	t.Run("alias match", func(t *testing.T) {
		cfg := &Config{API: "openai", Model: "4o", APIs: apis}
		_, mod, err := m.resolveModel(cfg)
		require.NoError(t, err)
		require.Equal(t, "gpt-4o", mod.Name)
	})

	t.Run("model not in specified API", func(t *testing.T) {
		cfg := &Config{API: "openai", Model: "nonexistent", APIs: apis}
		_, _, err := m.resolveModel(cfg)
		require.Error(t, err)
		merr := err.(modsError)
		require.Contains(t, merr.reason, "does not contain the model")
	})

	t.Run("api not configured", func(t *testing.T) {
		cfg := &Config{API: "unknown-api", Model: "gpt-4", APIs: apis}
		_, _, err := m.resolveModel(cfg)
		require.Error(t, err)
		merr := err.(modsError)
		require.Contains(t, merr.reason, "not in the settings file")
	})

	t.Run("empty api searches all", func(t *testing.T) {
		cfg := &Config{API: "", Model: "claude-sonnet-4", APIs: apis}
		api, mod, err := m.resolveModel(cfg)
		require.NoError(t, err)
		require.Equal(t, "anthropic", api.Name)
		require.Equal(t, "claude-sonnet-4", mod.Name)
	})
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
		require.Contains(t, merr.reason, "MISSING_KEY")
	})
}
