package app

import (
	"context"
	"strings"
	"testing"

	cfgpkg "github.com/panjie/mods/internal/config"
	"github.com/panjie/mods/internal/providerinfo"
	"github.com/panjie/mods/internal/selfhelp"
	"github.com/panjie/mods/internal/tooling"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestBuildSelfHelpReferenceMatchesSubsystemCatalogs(t *testing.T) {
	flags := []selfhelp.FlagGroup{{
		Name: "Test",
		Flags: []selfhelp.Flag{{
			Name: "model", Description: "Choose model.",
		}},
	}}
	reference, err := buildSelfHelpReference(flags)
	require.NoError(t, err)
	catalog := reference.Catalog()
	require.Equal(t, flags, catalog.Flags)

	settingInfos := cfgpkg.SelfHelpSettings()
	require.Len(t, catalog.Settings, len(settingInfos))
	for i, setting := range catalog.Settings {
		require.Equal(t, settingInfos[i].Path, setting.Path)
		require.Equal(t, settingInfos[i].Description, setting.Description)
		require.Equal(t, settingInfos[i].Default, setting.Default)
	}

	providerInfos := providerinfo.Descriptors()
	require.Len(t, catalog.Providers, len(providerInfos))
	for i, provider := range catalog.Providers {
		require.Equal(t, providerInfos[i].Name, provider.Name)
		require.Equal(t, providerInfos[i].Protocol, provider.Protocol)
	}

	toolInfos, err := tooling.BuiltinSpecs()
	require.NoError(t, err)
	require.Len(t, catalog.Tools, len(toolInfos))
	for i, tool := range catalog.Tools {
		require.Equal(t, toolInfos[i].Name, tool.Name)
		require.Equal(t, toolInfos[i].Description, tool.Description)
		require.Equal(t, toolInfos[i].ReadOnly, tool.ReadOnly)
		require.Equal(t, toolInfos[i].Mutable, tool.Mutable)
	}
}

func TestModsHelpAndFallbackShareReferenceBody(t *testing.T) {
	reference, err := buildSelfHelpReference(nil)
	require.NoError(t, err)
	cfg := &Config{
		SettingsPath: "/home/test/.config/mods/mods.yml",
		PersistentConfig: PersistentConfig{
			BuiltinTools: cfgpkg.BuiltinToolsConfig{Filesystem: cfgpkg.FilesystemAuto},
		},
	}

	registry := toolregistry.NewRegistry()
	require.NoError(t, toolregistry.RegisterModsHelp(registry, toolregistry.ModsHelpConfig{
		SettingsPath:   cfg.SettingsPath,
		FilesystemMode: string(cfg.BuiltinTools.Filesystem),
		Reference:      reference,
	}))
	toolResult, err := registry.Call(
		context.Background(),
		toolregistry.ModsHelpToolName,
		[]byte(`{"topic":"config"}`),
	)
	require.NoError(t, err)
	fallback := formatSelfHelpFallback(reference, cfg, "how to change the mods config")

	require.Equal(t, resultBody(t, toolResult), resultBody(t, fallback))
	require.Contains(t, resultBody(t, toolResult), "### Persistent settings")
	require.NotContains(t, toolResult, "private-model")
}

func resultBody(t *testing.T, result string) string {
	t.Helper()
	_, body, ok := strings.Cut(result, "\n\n")
	require.True(t, ok)
	return body
}
