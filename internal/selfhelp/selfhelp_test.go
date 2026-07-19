package selfhelp

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLookup(t *testing.T) {
	reference := NewReference(Catalog{
		Flags: []FlagGroup{{Name: "General", Flags: []Flag{{
			Name: "model", Shorthand: "m", ValueType: "string", Description: "Choose a model.",
		}}}},
		Settings:  []Setting{{Path: "default-model", ValueType: "string", Description: "Default model."}},
		Providers: []Provider{{Name: "openai", Protocol: "openai"}},
		Protocols: []string{"openai"},
		Tools:     []Tool{{Name: "mods_help", Description: "Load help.", Kind: "builtin", ReadOnly: true}},
	})
	for _, topic := range Topics() {
		got, err := reference.Lookup(topic)
		require.NoError(t, err, topic)
		require.NotEmpty(t, got, topic)
	}
	_, err := reference.Lookup("missing")
	require.Error(t, err)
}

func TestReferenceClonesAndSortsFactualMetadata(t *testing.T) {
	catalog := Catalog{
		Flags: []FlagGroup{{Name: "Flags", Flags: []Flag{{Name: "model"}}}},
		Settings: []Setting{
			{Path: "z", Description: "last"},
			{Path: "a", Description: "first"},
		},
		Providers: []Provider{{Name: "z"}, {Name: "a"}},
		Protocols: []string{"z", "a"},
		Tools:     []Tool{{Name: "z"}, {Name: "a"}},
	}
	reference := NewReference(catalog)
	catalog.Flags[0].Flags[0].Name = "changed"
	catalog.Settings[0].Path = "changed"

	got := reference.Catalog()
	require.Equal(t, "model", got.Flags[0].Flags[0].Name)
	require.Equal(t, []string{"a", "z"}, []string{got.Settings[0].Path, got.Settings[1].Path})
	require.Equal(t, []string{"a", "z"}, []string{got.Providers[0].Name, got.Providers[1].Name})
	require.Equal(t, []string{"a", "z"}, got.Protocols)
	require.Equal(t, []string{"a", "z"}, []string{got.Tools[0].Name, got.Tools[1].Name})

	got.Flags[0].Flags[0].Name = "mutated copy"
	require.Equal(t, "model", reference.Catalog().Flags[0].Flags[0].Name)
}

func TestGeneratedInventoriesAreNotMaintainedInGuidance(t *testing.T) {
	require.NotContains(t, guidance, "### Registered options")
	require.NotContains(t, guidance, "### Persistent settings")
	require.NotContains(t, guidance, "### Built-in provider metadata")
	require.NotContains(t, guidance, "### Built-in tool catalog")
}

func TestDetectTopic(t *testing.T) {
	tests := []struct {
		prompt string
		topic  string
		ok     bool
	}{
		{"mods 怎么修改配置文件", TopicConfig, true},
		{"what does mods --minimal do", TopicCLI, true},
		{"how do portable installs work", TopicPortable, true},
		{"explain this repository", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.prompt, func(t *testing.T) {
			topic, ok := DetectTopic(tt.prompt)
			require.Equal(t, tt.ok, ok)
			require.Equal(t, tt.topic, topic)
		})
	}
}

func TestIsConfigMutation(t *testing.T) {
	for _, prompt := range []string{
		"帮我把 mods 默认模型改成 gpt-5",
		"change my mods config to use ollama",
		"直接修改 mods.yml",
	} {
		require.True(t, IsConfigMutation(prompt), prompt)
	}
	for _, prompt := range []string{
		"mods 怎么修改配置",
		"how to change the mods default model",
		"explain config.go",
	} {
		require.False(t, IsConfigMutation(prompt), prompt)
	}
}

func TestConfigInspectionAndHelpOnly(t *testing.T) {
	require.True(t, IsConfigInspection("检查我的 mods 配置"))
	require.True(t, IsConfigInspection("read my mods config"))
	require.False(t, IsConfigHelpOnly("检查我的 mods 配置"))

	require.True(t, IsConfigHelpOnly("mods 怎么修改配置"))
	require.True(t, IsConfigHelpOnly("how to change the mods default model"))
	require.False(t, IsConfigHelpOnly("帮我把 mods 默认模型改成 gpt-5"))
}
