package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"unicode"

	"github.com/adrg/xdg"
	"github.com/caarlos0/env/v9"
	"github.com/panjie/mods/internal/prompts"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestConfig(t *testing.T) {
	t.Run("old format text", func(t *testing.T) {
		var cfg Config
		require.NoError(t, yaml.Unmarshal([]byte("format-text: as markdown"), &cfg))
		require.Equal(t, FormatText(map[string]string{
			"markdown": "as markdown",
		}), cfg.FormatText)
	})
	t.Run("new format text", func(t *testing.T) {
		var cfg Config
		require.NoError(t, yaml.Unmarshal([]byte("format-text:\n  markdown: as markdown\n  json: as json"), &cfg))
		require.Equal(t, FormatText(map[string]string{
			"markdown": "as markdown",
			"json":     "as json",
		}), cfg.FormatText)
	})
}

func TestSelfHelpSettingsCoverEveryPersistentConfigKey(t *testing.T) {
	documented := make(map[string]SettingInfo)
	for _, setting := range SelfHelpSettings() {
		require.NotEmpty(t, setting.Description, setting.Path)
		require.NotContains(t, documented, setting.Path, "duplicate setting path")
		documented[setting.Path] = setting
	}
	configType := reflect.TypeFor[PersistentConfig]()
	for i := range configType.NumField() {
		field := configType.Field(i)
		if field.Name == "System" {
			continue // Deprecated compatibility-only key.
		}
		key := strings.Split(field.Tag.Get("yaml"), ",")[0]
		if key == "" {
			key = strings.ToLower(field.Name)
		}
		require.Contains(t, documented, key,
			"self-help settings must include persistent config key %q", key)
	}
}

func TestSelfHelpSettingsIncludeNestedSchemasAndSafeDefaults(t *testing.T) {
	documented := make(map[string]SettingInfo)
	for _, setting := range SelfHelpSettings() {
		documented[setting.Path] = setting
	}
	for _, path := range []string{
		"prompts.identity",
		"prompts.tool-selection",
		"builtin-tools.filesystem",
		"builtin-tools.shell-timeout",
		"mcp-servers.<server>.pass-env-all",
		"apis.<provider>.api-type",
		"apis.<provider>.api-key-env",
		"apis.<provider>.models.<model>.reasoning-effort",
		"apis.<provider>.models.<model>.extra-params",
	} {
		require.Contains(t, documented, path)
		require.NotEmpty(t, documented[path].Description, path)
	}

	require.Equal(t, "80", documented["word-wrap"].Default)
	require.Equal(t, "auto", documented["review-mode"].Default)
	require.Equal(t, "true", documented["web-search"].Default)
	require.Equal(t, DefaultWebSearchAPIKeyEnv, documented["web-search-api-key-env"].Default)
	require.Equal(t, "30s", documented["builtin-tools.shell-timeout"].Default)
	for _, path := range []string{
		"web-search-api-key",
		"prompts.identity",
		"prompts.shell-classifier",
		"apis.<provider>.api-key",
		"apis.<provider>.api-key-cmd",
		"apis.<provider>.models.<model>.extra-params",
	} {
		require.Empty(t, documented[path].Default, path)
	}
}

func TestPromptConfig(t *testing.T) {
	var cfg Config
	require.NoError(t, yaml.Unmarshal([]byte(`prompts:
  identity: custom identity
  tool-selection: custom tools
  plan: custom plan
  shell-classifier: custom shell
`), &cfg))

	require.Equal(t, "custom identity", cfg.Prompts.Identity)
	require.Equal(t, "custom tools", cfg.Prompts.ToolSelection)
	require.Equal(t, "custom plan", cfg.Prompts.Plan)
	require.Equal(t, "custom shell", cfg.Prompts.ShellClassifier)
	require.Equal(t, "custom identity", cfg.Prompts.Value(prompts.KeyIdentity))
	require.Equal(t, "custom shell", cfg.Prompts.Value(prompts.KeyShellClassifier))
}

func TestValidateReviewMode(t *testing.T) {
	cfg := &Config{}
	cfg.ReviewMode = ""
	validateReviewMode(cfg)
	require.Equal(t, ReviewMode(""), cfg.ReviewMode) // empty is valid, left alone

	cfg = &Config{}
	cfg.ReviewMode = ReviewAuto
	validateReviewMode(cfg)
	require.Equal(t, ReviewAuto, cfg.ReviewMode)

	cfg = &Config{}
	cfg.ReviewMode = ReviewAlways
	validateReviewMode(cfg)
	require.Equal(t, ReviewAlways, cfg.ReviewMode)

	cfg = &Config{}
	cfg.ReviewMode = ReviewNever
	validateReviewMode(cfg)
	require.Equal(t, ReviewNever, cfg.ReviewMode)

	cfg = &Config{}
	cfg.ReviewMode = "mutable"
	validateReviewMode(cfg)
	require.Equal(t, ReviewAuto, cfg.ReviewMode) // invalid resets to default
}

func TestDefaultPromptText(t *testing.T) {
	cfg := Default()

	require.Contains(t, MinimalSystemPrompt, "Unless the user explicitly requests otherwise")
	require.Contains(t, MinimalSystemPrompt, "output only the final answer")

	require.Equal(t,
		"Format the response as Markdown. Do not wrap the whole response in a code fence unless the user explicitly requests it.",
		cfg.FormatText["markdown"],
	)
	require.Equal(t,
		"Return valid JSON only. Do not include Markdown fences, prose, or explanations unless the user explicitly requests them.",
		cfg.FormatText["json"],
	)
}

func TestToolSelectionRulesCoverCoreChoices(t *testing.T) {
	require.Contains(t, ToolSelectionRules, "Prefer fs_* tools")
	require.Contains(t, ToolSelectionRules, "Use shell tools for repository-wide")
	require.Contains(t, ToolSelectionRules, "review step")
	require.Contains(t, ToolSelectionRules, "PowerShell 5.1")
	require.Contains(t, ToolSelectionRules, "Do not retry blindly")
}

func TestWorkspaceHelpUsesWorkspaceTerminology(t *testing.T) {
	require.Contains(t, Help["workspace"], "Set the workspace")
}

func TestDefaultToolSettings(t *testing.T) {
	cfg := Default()

	require.Equal(t, FilesystemAuto, cfg.BuiltinTools.Filesystem)
	require.True(t, cfg.BuiltinTools.Shell)
	require.True(t, cfg.WebSearch)
}

func TestRemovedSequentialThinkingConfigIsIgnored(t *testing.T) {
	cfg := Default()
	require.NoError(t, yaml.Unmarshal([]byte("builtin-tools:\n  sequential-thinking: false\n"), &cfg))
	require.True(t, cfg.BuiltinTools.Shell)
}

func TestMinimalConfig(t *testing.T) {
	t.Run("yaml", func(t *testing.T) {
		var cfg Config
		require.NoError(t, yaml.Unmarshal([]byte("minimal: true"), &cfg))
		require.True(t, cfg.Minimal)
	})

	t.Run("env", func(t *testing.T) {
		t.Setenv("MODS_MINIMAL", "true")
		var cfg Config
		require.NoError(t, env.ParseWithOptions(&cfg, env.Options{Prefix: "MODS_"}))
		require.True(t, cfg.Minimal)
	})
}

func TestSessionSaveConfig(t *testing.T) {
	t.Run("default is false", func(t *testing.T) {
		require.False(t, Default().NoSave)
	})

	t.Run("no-save is not settable via env or yaml", func(t *testing.T) {
		t.Setenv("MODS_NO_SAVE", "true")
		var cfg Config
		require.NoError(t, env.ParseWithOptions(&cfg, env.Options{Prefix: "MODS_"}))
		require.False(t, cfg.NoSave)

		require.NoError(t, yaml.Unmarshal([]byte("no-save: true"), &cfg))
		require.False(t, cfg.NoSave)
	})
}

func TestHideToolStatusConfig(t *testing.T) {
	t.Run("yaml", func(t *testing.T) {
		var cfg Config
		require.NoError(t, yaml.Unmarshal([]byte("hide-tool-status: true"), &cfg))
		require.True(t, cfg.HideToolStatus)
	})

	t.Run("env", func(t *testing.T) {
		t.Setenv("MODS_HIDE_TOOL_STATUS", "true")
		var cfg Config
		require.NoError(t, env.ParseWithOptions(&cfg, env.Options{Prefix: "MODS_"}))
		require.True(t, cfg.HideToolStatus)
	})
}

func TestConfigTemplateIncludesHideToolStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mods.yml")
	require.NoError(t, createConfigFile(path))

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.True(t, strings.Contains(string(content), "hide-tool-status: false"))
}

func TestConfigTemplateIncludesPrompts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mods.yml")
	require.NoError(t, createConfigFile(path))

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(content), "prompts:")
	require.Contains(t, string(content), `identity: ""`)
	require.Contains(t, string(content), `shell-classifier: ""`)
}

func TestShowTokenUsageConfig(t *testing.T) {
	require.False(t, Default().ShowTokenUsage)

	var yamlCfg Config
	require.NoError(t, yaml.Unmarshal([]byte("show-token-usage: true"), &yamlCfg))
	require.True(t, yamlCfg.ShowTokenUsage)

	t.Setenv("MODS_SHOW_TOKEN_USAGE", "true")
	var envCfg Config
	require.NoError(t, env.ParseWithOptions(&envCfg, env.Options{Prefix: "MODS_"}))
	require.True(t, envCfg.ShowTokenUsage)
}

func TestConfigTemplateIncludesShowTokenUsage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mods.yml")
	require.NoError(t, createConfigFile(path))
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(content), "show-token-usage: false")
}

func TestConfigTemplateIncludesDefaultToolSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mods.yml")
	require.NoError(t, createConfigFile(path))

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	text := string(content)

	require.Contains(t, text, "filesystem: auto")
	require.Contains(t, text, "shell: true")
	require.NotContains(t, text, "sequential-thinking")
	require.Contains(t, text, "web-search: true")
}

func TestCreateConfigFileUsesLFLineEndings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mods.yml")
	require.NoError(t, createConfigFile(path))

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotContains(t, string(content), "\r")
}

func TestNormalizeLineEndings(t *testing.T) {
	require.Equal(t, "a\nb\nc\n", normalizeLineEndings("a\r\nb\rc\n"))
}

func TestConfigTemplateUsesEnglishNeutralSections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mods.yml")
	require.NoError(t, createConfigFile(path))

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	text := string(content)

	for _, r := range text {
		require.Falsef(t, unicode.Is(unicode.Han, r), "config template must not contain Chinese characters: %q", r)
	}

	for _, section := range []string{
		"Chinese AI Providers",
		"Major Cloud Providers",
		"Enterprise / Alternative",
	} {
		require.NotContains(t, text, section)
	}
}

func TestConfigTemplateOmitsDefaultProviderModelLists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mods.yml")
	require.NoError(t, createConfigFile(path))

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var cfg Config
	require.NoError(t, yaml.Unmarshal(data, &cfg))
	require.Empty(t, cfg.Model)

	builtinProviders := map[string]struct{}{
		"openai":     {},
		"google":     {},
		"anthropic":  {},
		"azure":      {},
		"deepseek":   {},
		"glm":        {},
		"minimax":    {},
		"qwen":       {},
		"kimi":       {},
		"openrouter": {},
		"ollama":     {},
	}
	for _, api := range cfg.APIs {
		if _, ok := builtinProviders[api.Name]; ok {
			require.Emptyf(t, api.Models, "default provider %s should not ship model lists", api.Name)
		}
	}
}

func TestAPITypeYAML(t *testing.T) {
	t.Run("decodes api-type on a custom provider", func(t *testing.T) {
		var cfg Config
		require.NoError(t, yaml.Unmarshal([]byte(`apis:
  acme-claude:
    api-type: anthropic
    base-url: https://acme.example.com/v1
    api-key-env: ACME_API_KEY
    models:
      claude-sonnet-4:
        aliases: ["acme-sonnet"]
`), &cfg))
		require.Len(t, cfg.APIs, 1)
		require.Equal(t, "acme-claude", cfg.APIs[0].Name)
		require.Equal(t, "anthropic", cfg.APIs[0].APIType)
		require.Equal(t, "https://acme.example.com/v1", cfg.APIs[0].BaseURL)
	})

	t.Run("api-type defaults to empty", func(t *testing.T) {
		var cfg Config
		require.NoError(t, yaml.Unmarshal([]byte(`apis:
  custom:
    base-url: https://example.com/v1
    models:
      m: {}
`), &cfg))
		require.Empty(t, cfg.APIs[0].APIType)
	})
}

func TestConfigTemplateDocumentsAPIType(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mods.yml")
	require.NoError(t, createConfigFile(path))

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(content), "api-type: anthropic")
}

func TestFilesystemModeYAML(t *testing.T) {
	t.Run("string auto", func(t *testing.T) {
		var cfg Config
		require.NoError(t, yaml.Unmarshal([]byte("builtin-tools:\n  filesystem: auto"), &cfg))
		require.Equal(t, FilesystemAuto, cfg.BuiltinTools.Filesystem)
	})

	t.Run("legacy bool true", func(t *testing.T) {
		var cfg Config
		require.NoError(t, yaml.Unmarshal([]byte("builtin-tools:\n  filesystem: true"), &cfg))
		require.Equal(t, FilesystemAlways, cfg.BuiltinTools.Filesystem)
	})

	t.Run("legacy bool false", func(t *testing.T) {
		var cfg Config
		require.NoError(t, yaml.Unmarshal([]byte("builtin-tools:\n  filesystem: false"), &cfg))
		require.Equal(t, FilesystemNever, cfg.BuiltinTools.Filesystem)
	})
}

func TestShellReadOnlyCommandsConfig(t *testing.T) {
	var cfg Config
	require.NoError(t, yaml.Unmarshal([]byte(`builtin-tools:
  shell-read-only-commands:
    - " rg "
    - jq
    - rg
`), &cfg))
	require.NoError(t, validateShellReadOnlyCommands(&cfg))
	require.Equal(t, []string{"rg", "jq"}, cfg.BuiltinTools.ShellReadOnlyCommands)
	require.Empty(t, Default().BuiltinTools.ShellReadOnlyCommands)
}

func TestValidateShellReadOnlyCommandsRejectsNonNames(t *testing.T) {
	for _, command := range []string{"", "git status", "/usr/bin/rg", `bin\rg`, "C:rg", "rg*", "rg|cat"} {
		t.Run(command, func(t *testing.T) {
			cfg := Config{}
			cfg.BuiltinTools.ShellReadOnlyCommands = []string{command}
			err := validateShellReadOnlyCommands(&cfg)
			require.Error(t, err)
			require.Contains(t, err.Error(), "shell-read-only-commands[0]")
		})
	}
}

func TestEnsureRejectsInvalidShellReadOnlyCommand(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	path := filepath.Join(configHome, "mods", "mods.yml")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte("builtin-tools:\n  shell-read-only-commands: ['git status']\n"), 0o600))

	_, err := Ensure()
	require.Error(t, err)
	require.Contains(t, err.Error(), "shell-read-only-commands[0]")
}

func TestConfigTemplateIncludesShellReadOnlyCommands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mods.yml")
	require.NoError(t, createConfigFile(path))
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(content), "shell-read-only-commands: []")
	require.Contains(t, string(content), "all arguments, subcommands, and internal side effects")
}

func TestSettingsFilePathDarwin(t *testing.T) {
	defer swapExecutableDir("")()
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-specific default config path")
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	path, err := settingsFilePath()
	require.NoError(t, err)
	require.Equal(t, filepath.Join(home, ".config", "mods", "mods.yml"), path)

	xdgConfigHome := filepath.Join(t.TempDir(), "xdg-config")
	t.Setenv("XDG_CONFIG_HOME", xdgConfigHome)

	path, err = settingsFilePath()
	require.NoError(t, err)
	require.Equal(t, filepath.Join(xdgConfigHome, "mods", "mods.yml"), path)
}

func TestEnsureReportsSettingsExistedFalseWhenCreatingDefault(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	cfg, err := Ensure()

	require.NoError(t, err)
	require.False(t, cfg.SettingsExisted)
	require.FileExists(t, cfg.SettingsPath)
}

func TestEnsureReportsSettingsExistedTrueWhenFileAlreadyExists(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	first, err := Ensure()
	require.NoError(t, err)
	require.False(t, first.SettingsExisted)

	second, err := Ensure()

	require.NoError(t, err)
	require.True(t, second.SettingsExisted)
	require.Equal(t, first.SettingsPath, second.SettingsPath)
}

func TestEnsureResetsInvalidReviewMode(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	path := filepath.Join(configHome, "mods", "mods.yml")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte("review-mode: mutable\n"), 0o600))

	cfg, err := Ensure()

	require.NoError(t, err)
	require.Equal(t, ReviewAuto, cfg.ReviewMode)
}

// TestEnsureReturnsCompleteConfigOnError pins the invariant that every
// error path from Ensure() returns a Config the caller can still use:
// SettingsPath is filled in as soon as it is known, SessionDir always has
// the default value, and the rest matches Default(). Callers of
// --settings / --config keep the returned value after a partial failure,
// so a half-initialised Config would surface as zero-valued fields
// downstream.
func TestEnsureReturnsCompleteConfigOnError(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	// Write a syntactically invalid YAML so the yaml.Unmarshal branch in
	// Ensure() takes its error return, exercising one of the middle
	// failure paths.
	path := filepath.Join(configHome, "mods", "mods.yml")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte("not: [a: valid: yaml"), 0o600))

	cfg, err := Ensure()
	require.Error(t, err, "Ensure must surface the parse error")

	// Even on failure, callers must observe a usable Config.
	require.NotEmpty(t, cfg.SettingsPath,
		"SettingsPath must be set so --settings / --config can locate the file")
	require.NotEmpty(t, cfg.SessionDir,
		"SessionDir must have its default so the session layer never sees an empty path")
	require.Equal(t, Default().ReviewMode, cfg.ReviewMode,
		"non-failure fields must keep their Default() value")
	require.Equal(t, Default().WordWrap, cfg.WordWrap)
	require.Equal(t, Default().Format, cfg.Format)
	require.Equal(t, Default().MCPTimeout, cfg.MCPTimeout)
}
func TestCreateConfigFileRefusesToOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mods.yml")
	require.NoError(t, createConfigFile(path))

	err := createConfigFile(path)
	require.Error(t, err, "createConfigFile must not silently overwrite an existing file")
}

// TestCreateConfigFilePermissionsOnUnix asserts the file is created with
// 0o600 in a single syscall, eliminating the post-create chmod race
// window that previously exposed the API-key template to other users.
// Permission bits on Windows do not follow POSIX conventions, so the test
// is unix-only.
func TestCreateConfigFilePermissionsOnUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits do not follow POSIX conventions on Windows")
	}
	path := filepath.Join(t.TempDir(), "mods.yml")
	require.NoError(t, createConfigFile(path))

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestResolveSkillsDirsDefault(t *testing.T) {
	cfg := Config{}
	applySkillsDirsDefault(&cfg)
	require.Equal(t, []string{filepath.Join(xdg.Home, ".agents", "skills")}, cfg.ResolveSkillsDirs())
}

func TestResolveSkillsDirsDefaultWithoutApplyingDefaults(t *testing.T) {
	cfg := Config{}
	require.Equal(t, []string{filepath.Join(xdg.Home, ".agents", "skills")}, cfg.ResolveSkillsDirs())
}

func TestResolveSkillsDirsYAMLOverride(t *testing.T) {
	var cfg Config
	require.NoError(t, yaml.Unmarshal([]byte("skills-dirs:\n  - ~/team-skills\n  - ./project-skills\n"), &cfg))
	applySkillsDirsDefault(&cfg)
	require.Equal(t, []string{
		filepath.Join(xdg.Home, "team-skills"),
		"./project-skills",
	}, cfg.ResolveSkillsDirs())
}

func TestResolveSkillsDirsEnvPathList(t *testing.T) {
	t.Setenv("MODS_SKILLS_DIRS", filepath.Join("env", "one")+string(os.PathListSeparator)+filepath.Join("env", "two"))
	cfg := Default()
	require.NoError(t, parseSkillsDirsEnv(&cfg))
	applySkillsDirsDefault(&cfg)
	require.Equal(t, []string{filepath.Join("env", "one"), filepath.Join("env", "two")}, cfg.ResolveSkillsDirs())
}

func TestResolveSkillsDirsIgnoresEmptyAndKeepsLastDuplicate(t *testing.T) {
	cfg := Config{PersistentConfig: PersistentConfig{
		SkillsDirs: []string{"/skills/global", "", "/skills/project", "/skills/global"},
	}}
	applySkillsDirsDefault(&cfg)
	require.Equal(t, []string{"/skills/project", "/skills/global"}, cfg.ResolveSkillsDirs())
}

func TestSkillsDirLegacyKeyIsIgnored(t *testing.T) {
	var cfg Config
	require.NoError(t, yaml.Unmarshal([]byte("skills-dir: /legacy\n"), &cfg))
	applySkillsDirsDefault(&cfg)
	require.Equal(t, []string{filepath.Join(xdg.Home, ".agents", "skills")}, cfg.ResolveSkillsDirs())
}

func TestNormalizeSkillsDirExpandsHome(t *testing.T) {
	require.Equal(t, xdg.Home, NormalizeSkillsDir("~"))
	require.Equal(t, filepath.Join(xdg.Home, ".agents", "skills"), NormalizeSkillsDir("~/.agents/skills"))
}

func TestNormalizeSkillsDirLeavesRelativePathUnchanged(t *testing.T) {
	require.Equal(t, filepath.Join("relative", "skills"), NormalizeSkillsDir(filepath.Join("relative", "skills")))
}

func TestRemovedSkillSourcesYAMLIsIgnored(t *testing.T) {
	var cfg Config
	require.NoError(t, yaml.Unmarshal([]byte("skill-sources:\n  - url: https://example.com/skills.git\n"), &cfg))
	applySkillsDirsDefault(&cfg)
	require.Equal(t, []string{filepath.Join(xdg.Home, ".agents", "skills")}, cfg.ResolveSkillsDirs())
}
