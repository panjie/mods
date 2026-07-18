package selfhelp

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLookup(t *testing.T) {
	for _, topic := range Topics() {
		got, err := Lookup(topic)
		require.NoError(t, err, topic)
		require.NotEmpty(t, got, topic)
	}
	_, err := Lookup("missing")
	require.Error(t, err)
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
