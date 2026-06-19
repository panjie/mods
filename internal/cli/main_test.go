package cli

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsCompletionCmd(t *testing.T) {
	for args, is := range map[string]bool{
		"":                                     false,
		"something":                            false,
		"something something":                  false,
		"completion for my bash script how to": false,
		"completion bash how to":               false,
		"completion":                           false,
		"completion -h":                        true,
		"completion --help":                    true,
		"completion help":                      true,
		"completion bash":                      true,
		"completion fish":                      true,
		"completion zsh":                       true,
		"completion powershell":                true,
		"completion bash -h":                   true,
		"completion fish -h":                   true,
		"completion zsh -h":                    true,
		"completion powershell -h":             true,
		"completion bash --help":               true,
		"completion fish --help":               true,
		"completion zsh --help":                true,
		"completion powershell --help":         true,
		"__complete":                           true,
		"__complete blah blah blah":            true,
	} {
		t.Run(args, func(t *testing.T) {
			vargs := append([]string{"mods"}, strings.Fields(args)...)
			if b := isCompletionCmd(vargs); b != is {
				t.Errorf("%v: expected %v, got %v", vargs, is, b)
			}
		})
	}
}

func TestConversationCompletions(t *testing.T) {
	saveDB := db
	defer func() { db = saveDB }()

	db = testDB(t)
	const id = "df31ae23ab8b75b5643c2f846c570997edc71333"
	require.NoError(t, db.Save(id, "message 1", "openai", "gpt-4o"))

	results := conversationCompletions("df31")
	require.Equal(t, []string{"df31ae2\tmessage 1"}, results)
}

func TestIsVersionOrHelpCmd(t *testing.T) {
	for args, is := range map[string]bool{
		"":                        false,
		"mods":                    false,
		"mods something":          false,
		"mods --version":          true,
		"mods -v":                 true,
		"mods --help":             true,
		"mods -h":                 true,
		"mods --model gpt-4":      false,
		"mods -v -m gpt-4":        true,
		"mods -m gpt-4 --version": true,
	} {
		t.Run(args, func(t *testing.T) {
			vargs := append([]string{"mods"}, strings.Fields(args)...)
			if b := isVersionOrHelpCmd(vargs); b != is {
				t.Errorf("%v: expected %v, got %v", vargs, is, b)
			}
		})
	}
}

func TestThemeFrom(t *testing.T) {
	t.Run("charm", func(t *testing.T) {
		require.NotNil(t, themeFrom("charm"))
	})
	t.Run("dracula", func(t *testing.T) {
		require.NotNil(t, themeFrom("dracula"))
	})
	t.Run("catppuccin", func(t *testing.T) {
		require.NotNil(t, themeFrom("catppuccin"))
	})
	t.Run("base16", func(t *testing.T) {
		require.NotNil(t, themeFrom("base16"))
	})
	t.Run("unknown defaults to charm", func(t *testing.T) {
		require.NotNil(t, themeFrom("nonexistent"))
	})
}

func TestMinimalFlagRegistered(t *testing.T) {
	if rootCmd.Flags().Lookup("minimal") == nil {
		initFlags()
	}
	require.NotNil(t, rootCmd.Flags().Lookup("minimal"))
}

func TestFancinessFlagRemoved(t *testing.T) {
	if rootCmd.Flags().Lookup("minimal") == nil {
		initFlags()
	}
	require.Nil(t, rootCmd.Flags().Lookup("fanciness"))
}

func TestRoleNames(t *testing.T) {
	saveConfig := config
	defer func() { config = saveConfig }()

	config = Config{
		PersistentConfig: PersistentConfig{
			Roles: map[string][]string{
				"default": {"You are helpful."},
				"shell":   {"You are a shell expert."},
				"go-dev":  {"You write Go code."},
			},
		},
	}

	t.Run("all roles", func(t *testing.T) {
		roles := roleNames("")
		require.Len(t, roles, 3)
		require.Contains(t, roles, "default")
		require.Contains(t, roles, "shell")
		require.Contains(t, roles, "go-dev")
	})

	t.Run("prefix filter", func(t *testing.T) {
		roles := roleNames("go")
		require.Len(t, roles, 1)
		require.Equal(t, "go-dev", roles[0])
	})

	t.Run("no match prefix", func(t *testing.T) {
		roles := roleNames("nonexistent")
		require.Empty(t, roles)
	})
}

func TestIsNoArgs(t *testing.T) {
	t.Run("empty config", func(t *testing.T) {
		cfg := Config{}
		require.True(t, isNoArgsCfg(cfg))
	})
	t.Run("with prefix", func(t *testing.T) {
		cfg := Config{Prefix: "hello"}
		require.False(t, isNoArgsCfg(cfg))
	})
	t.Run("with show", func(t *testing.T) {
		cfg := Config{Show: "abc123"}
		require.False(t, isNoArgsCfg(cfg))
	})
	t.Run("with delete", func(t *testing.T) {
		cfg := Config{Delete: []string{"abc"}}
		require.False(t, isNoArgsCfg(cfg))
	})
	t.Run("with help", func(t *testing.T) {
		cfg := Config{ShowHelp: true}
		require.False(t, isNoArgsCfg(cfg))
	})
}

func isNoArgsCfg(cfg Config) bool {
	return cfg.Prefix == "" &&
		cfg.Show == "" &&
		!cfg.ShowLast &&
		len(cfg.Delete) == 0 &&
		cfg.DeleteOlderThan == 0 &&
		!cfg.ShowHelp &&
		!cfg.List &&
		!cfg.ListRoles &&
		!cfg.MCPList &&
		!cfg.MCPListTools &&
		!cfg.Dirs &&
		!cfg.Settings &&
		!cfg.ResetSettings
}
