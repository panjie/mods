package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func TestNormalizeSettingsArgs(t *testing.T) {
	yamlInput := "apis:\n  custom:\n    api-type: google\n"
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "bare flag",
			args: []string{"--settings"},
			want: []string{"--settings"},
		},
		{
			name: "space separated YAML",
			args: []string{"--settings", yamlInput},
			want: []string{"--settings=" + yamlInput},
		},
		{
			name: "equals YAML",
			args: []string{"--settings=" + yamlInput},
			want: []string{"--settings=" + yamlInput},
		},
		{
			name: "bare flag before another option",
			args: []string{"--settings", "--reset-settings"},
			want: []string{"--settings", "--reset-settings"},
		},
		{
			name: "sequence YAML still becomes an import",
			args: []string{"--settings", "- invalid-root"},
			want: []string{"--settings=- invalid-root"},
		},
		{
			name: "explicit empty YAML",
			args: []string{"--settings", ""},
			want: []string{"--settings="},
		},
		{
			name: "end of flags protects prompt arguments",
			args: []string{"--", "--settings", "prompt"},
			want: []string{"--", "--settings", "prompt"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, normalizeSettingsArgs(tc.args))
		})
	}
}

func TestSettingsFlagDistinguishesEditorAndImport(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantImport bool
		wantYAML   string
	}{
		{name: "editor", args: []string{"--settings"}},
		{name: "space separated import", args: []string{"--settings", "default-api: openai"}, wantImport: true, wantYAML: "default-api: openai"},
		{name: "equals import", args: []string{"--settings=default-api: openai"}, wantImport: true, wantYAML: "default-api: openai"},
		{name: "empty import", args: []string{"--settings="}, wantImport: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var cfg Config
			flags := pflag.NewFlagSet("settings-test", pflag.ContinueOnError)
			regSettingsFlag(flags, &cfg)

			require.NoError(t, flags.Parse(normalizeSettingsArgs(tc.args)))
			require.Empty(t, flags.Args())
			require.True(t, cfg.Settings)
			require.Equal(t, tc.wantImport, cfg.SettingsImport)
			require.Equal(t, tc.wantYAML, cfg.SettingsYAML)
		})
	}
}

func TestHasSettingsArg(t *testing.T) {
	require.True(t, hasSettingsArg([]string{"mods", "--settings"}))
	require.True(t, hasSettingsArg([]string{"mods", "--settings=default-api: openai"}))
	require.False(t, hasSettingsArg([]string{"mods", "--config"}))
	require.False(t, hasSettingsArg([]string{"mods", "--", "--settings"}))
	require.False(t, hasSettingsArg([]string{"mods", "prompt mentioning --settings"}))
}

func TestSettingsImportActionUpdatesConfigAndBypassesEditor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mods.yml")
	require.NoError(t, os.WriteFile(path, []byte("default-api: openai\n"), 0o600))

	withTestConfig(t, Config{
		Settings:        true,
		SettingsImport:  true,
		SettingsYAML:    "default-api: ollama\n",
		SettingsPath:    path,
		SettingsExisted: true,
	}, func() {
		stderr := captureStderr(t, func() {
			handled, err := dispatchPreTurnAction(context.Background(), nil)
			require.True(t, handled)
			require.NoError(t, err)
		})
		require.Contains(t, stderr, "Wrote config file to:")
		require.Contains(t, stderr, path)
	})

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(data), "default-api: ollama")
}

func TestSettingsImportActionReturnsContextualError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mods.yml")
	original := []byte("default-api: openai\n")
	require.NoError(t, os.WriteFile(path, original, 0o600))

	withTestConfig(t, Config{
		Settings:        true,
		SettingsImport:  true,
		SettingsYAML:    "apis: [",
		SettingsPath:    path,
		SettingsExisted: true,
	}, func() {
		handled, err := dispatchPreTurnAction(context.Background(), nil)
		require.True(t, handled)
		require.Error(t, err)
		require.Contains(t, err.Error(), "Could not import settings")
	})

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, original, data)
}
