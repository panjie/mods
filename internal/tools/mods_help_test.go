package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModsHelp(t *testing.T) {
	reg := NewRegistry()
	require.NoError(t, RegisterModsHelp(reg, ModsHelpConfig{
		SettingsPath:   "/home/test/.config/mods/mods.yml",
		Portable:       true,
		FilesystemMode: "false",
	}))

	tool, ok := reg.Tool(ModsHelpToolName)
	require.True(t, ok)
	require.True(t, tool.Capabilities.ReadOnly)

	got, err := reg.Call(context.Background(), ModsHelpToolName, []byte(`{"topic":"config"}`))
	require.NoError(t, err)
	require.Contains(t, got, "Active config path: /home/test/.config/mods/mods.yml")
	require.Contains(t, got, "Portable mode: active")
	require.Contains(t, got, "Filesystem tools: false")
	require.Contains(t, got, "## Config")
	require.NotContains(t, got, "## CLI")
}

func TestModsHelpAllAndInvalidTopic(t *testing.T) {
	reg := NewRegistry()
	require.NoError(t, RegisterModsHelp(reg, ModsHelpConfig{}))

	got, err := reg.Call(context.Background(), ModsHelpToolName, []byte(`{"topic":"all"}`))
	require.NoError(t, err)
	require.Contains(t, got, "## Overview")
	require.Contains(t, got, "## Troubleshooting")

	_, err = reg.Call(context.Background(), ModsHelpToolName, []byte(`{"topic":"missing"}`))
	require.Error(t, err)
}
