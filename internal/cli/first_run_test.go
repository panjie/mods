package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShouldAutoConfigRequiresMissingSettingsAndNoSessions(t *testing.T) {
	withFirstRunTest(t, Config{SettingsExisted: false}, func() {
		should, err := shouldAutoConfig([]string{"mods", "hello"})

		require.NoError(t, err)
		require.True(t, should)
	})
}

func TestShouldAutoConfigSkipsWhenSettingsAlreadyExisted(t *testing.T) {
	withFirstRunTest(t, Config{SettingsExisted: true}, func() {
		should, err := shouldAutoConfig([]string{"mods", "hello"})

		require.NoError(t, err)
		require.False(t, should)
	})
}

func TestShouldAutoConfigSkipsWhenSessionsExist(t *testing.T) {
	withFirstRunTest(t, Config{SettingsExisted: false}, func() {
		require.NoError(t, db.Save("df31ae23ab8b75b5643c2f846c570997edc71333", "message", "openai", "gpt-4"))

		should, err := shouldAutoConfig([]string{"mods", "hello"})

		require.NoError(t, err)
		require.False(t, should)
	})
}

func TestShouldAutoConfigSkipsNonRequestActions(t *testing.T) {
	tests := map[string]struct {
		args []string
		cfg  Config
	}{
		"help":           {args: []string{"mods", "--help"}},
		"version":        {args: []string{"mods", "--version"}},
		"completion":     {args: []string{"mods", "completion", "bash"}},
		"dirs":           {args: []string{"mods", "--dirs"}, cfg: Config{Dirs: true}},
		"settings":       {args: []string{"mods", "--settings"}, cfg: Config{Settings: true}},
		"config":         {args: []string{"mods", "--config"}, cfg: Config{ConfigSetup: true}},
		"list-sessions":  {args: []string{"mods", "--list-sessions"}, cfg: Config{List: true}},
		"list roles":     {args: []string{"mods", "--list-roles"}, cfg: Config{ListRoles: true}},
		"list prompts":   {args: []string{"mods", "--list-prompts"}, cfg: Config{ListPrompts: true}},
		"list skills":    {args: []string{"mods", "--list-skills"}, cfg: Config{ListSkills: true}},
		"mcp list":       {args: []string{"mods", "--list-mcps"}, cfg: Config{MCPList: true}},
		"mcp list tools": {args: []string{"mods", "--list-tools"}, cfg: Config{MCPListTools: true}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tc.cfg.SettingsExisted = false
			withFirstRunTest(t, tc.cfg, func() {
				should, err := shouldAutoConfig(tc.args)

				require.NoError(t, err)
				require.False(t, should)
			})
		})
	}
}

func TestShouldAutoConfigSkipsExplicitConfig(t *testing.T) {
	withFirstRunTest(t, Config{SettingsExisted: false, ConfigSetup: true}, func() {
		should, err := shouldAutoConfig([]string{"mods", "--config"})

		require.NoError(t, err)
		require.False(t, should)
	})
}

func TestRunAutoConfigRunsWizardAndAsksForRerun(t *testing.T) {
	saveConfig := config
	saveRunConfigWizard := runConfigWizard
	defer func() {
		config = saveConfig
		runConfigWizard = saveRunConfigWizard
	}()

	config = Config{}
	called := false
	runConfigWizard = func() error {
		called = true
		return nil
	}

	err := runAutoConfig()

	require.Error(t, err)
	require.True(t, called)
	require.Contains(t, err.Error(), "Configuration complete. Please rerun your command.")
}

func TestMaybeRunAutoConfigRunsBeforeInteractivePrompts(t *testing.T) {
	saveRunConfigWizard := runConfigWizard
	defer func() { runConfigWizard = saveRunConfigWizard }()
	runConfigWizard = func() error { return nil }

	withFirstRunTest(t, Config{SettingsExisted: false, OpenEditor: true, AskModel: true}, func() {
		autoConfig, err := maybeRunAutoConfig([]string{"mods"})

		require.True(t, autoConfig)
		require.Error(t, err)
		require.Contains(t, err.Error(), "Configuration complete. Please rerun your command.")
	})
}

func TestCleanupAutoCreatedConfigRemovesPassiveSkippedCommandConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mods.yml")
	require.NoError(t, os.WriteFile(path, []byte("default-api: openai\n"), 0o600))
	withFirstRunTest(t, Config{SettingsExisted: false, SettingsPath: path, Dirs: true}, func() {
		require.NoError(t, cleanupAutoCreatedConfig([]string{"mods", "--dirs"}))
		require.NoFileExists(t, path)
	})
}

func TestCleanupAutoCreatedConfigKeepsExplicitSettingsCommandConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mods.yml")
	require.NoError(t, os.WriteFile(path, []byte("default-api: openai\n"), 0o600))
	withFirstRunTest(t, Config{SettingsExisted: false, SettingsPath: path, Settings: true}, func() {
		require.NoError(t, cleanupAutoCreatedConfig([]string{"mods", "--settings"}))
		require.FileExists(t, path)
	})
}

func TestValidateChatModeRunsBeforeAutoConfig(t *testing.T) {
	withFirstRunTest(t, Config{SettingsExisted: false, Chat: true, NoSave: true}, func() {
		err := validateFirstRunPrerequisites([]string{"mods", "--chat", "--no-save"})

		require.Error(t, err)
		merr, ok := err.(modsError)
		require.True(t, ok)
		require.Equal(t, "Chat mode requires session saving.", merr.ReasonText)
	})
}

func withFirstRunTest(t *testing.T, cfg Config, fn func()) {
	t.Helper()

	saveConfig := config
	saveDB := db
	defer func() {
		config = saveConfig
		db = saveDB
	}()

	config = cfg
	db = testDB(t)
	fn()
}
