package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/caarlos0/env/v9"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestCommandSimplificationConfiguration(t *testing.T) {
	require.Equal(t, CommandSimplificationOff, Default().CommandSimplification)
	for _, mode := range []string{"off", "once"} {
		t.Run(mode, func(t *testing.T) {
			cfg := Default()
			require.NoError(t, yaml.Unmarshal([]byte("command-simplification: "+mode+"\n"), &cfg))
			require.Equal(t, CommandSimplification(mode), cfg.CommandSimplification)
			require.NoError(t, validateCommandSimplificationStrict(cfg.CommandSimplification))
			path := filepath.Join(t.TempDir(), "mods.yml")
			require.NoError(t, os.WriteFile(path, []byte("review-mode: auto\n"), 0600))
			require.NoError(t, MergeSettingsYAML(path, []byte("command-simplification: "+mode+"\n")))
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Contains(t, string(data), "command-simplification: "+mode)
		})
	}
	cfg := Default()
	cfg.CommandSimplification = "always"
	validateCommandSimplification(&cfg)
	require.Equal(t, CommandSimplificationOff, cfg.CommandSimplification)
	require.Error(t, validateCommandSimplificationStrict("always"))
	path := filepath.Join(t.TempDir(), "mods.yml")
	original := []byte("review-mode: auto\n")
	require.NoError(t, os.WriteFile(path, original, 0600))
	require.Error(t, MergeSettingsYAML(path, []byte("command-simplification: always\n")))
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, original, after)
	t.Setenv("MODS_COMMAND_SIMPLIFICATION", "once")
	cfg = Default()
	require.NoError(t, env.ParseWithOptions(&cfg, env.Options{Prefix: "MODS_"}))
	require.Equal(t, CommandSimplificationOnce, cfg.CommandSimplification)
	require.NoError(t, yaml.Unmarshal([]byte("command-simplification: off\n"), &cfg))
	require.Equal(t, CommandSimplificationOff, cfg.CommandSimplification)
	for _, setting := range SelfHelpSettings() {
		if setting.Path == "command-simplification" {
			require.Equal(t, "off", setting.Default)
			require.Contains(t, setting.Description, "permissions")
			return
		}
	}
	t.Fatal("missing self-help setting")
}
