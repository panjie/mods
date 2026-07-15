package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	stdstrings "strings"
	"text/template"
	"time"

	_ "embed"

	"github.com/adrg/xdg"
	"github.com/caarlos0/env/v9"
	"github.com/panjie/mods/internal/prompts"
	"github.com/panjie/mods/internal/tools"
	"gopkg.in/yaml.v3"
)

// DefaultWebSearchAPIKeyEnv is the canonical environment-variable name
// consulted when WebSearchAPIKeyEnv is unset. It is referenced from
// Ensure / applyDefaults and the configuration wizard so the literal
// cannot drift between call sites.
const DefaultWebSearchAPIKeyEnv = "TAVILY_API_KEY"

//go:embed config_template.yml
var configTemplate string

const (
	defaultMarkdownFormatText = prompts.MarkdownFormat
	defaultJSONFormatText     = prompts.JSONFormat
	MinimalSystemPrompt       = prompts.Minimal
	ToolSelectionRules        = prompts.ToolSelection
)

// ReviewMode controls whether mods prompts for confirmation before executing tools.
type ReviewMode string

const (
	ReviewNever  ReviewMode = "never"
	ReviewAuto   ReviewMode = "auto"
	ReviewAlways ReviewMode = "always"
)

var Help = map[string]string{
	"api":               "OpenAI compatible REST API (openai, localai, anthropic, ...)",
	"apis":              "Aliases and endpoints for OpenAI compatible REST API",
	"api-type":          "Wire protocol for a custom provider, overriding name-based routing: openai (default), anthropic, ollama, google, azure, or azure-ad. Use 'anthropic' for any endpoint that speaks the Anthropic Messages API",
	"http-proxy":        "HTTP proxy to use for API requests",
	"model":             "Default model (gpt-3.5-turbo, gpt-4, ggml-gpt4all-j...)",
	"ask-model":         "Ask which model to use via interactive prompt",
	"max-input-chars":   "Default character limit on input to model",
	"format":            "Ask for the response to be formatted (markdown, json, or a custom format-text key); bare -f defaults to markdown",
	"format-text":       "Text to append when using the -f flag",
	"minimal":           "Output only the final result, optimized for pipelines",
	"role":              "System role to use",
	"roles":             "List of predefined system messages that can be used as roles",
	"list-roles":        "List the roles defined in your configuration file",
	"list-prompts":      "List built-in prompts and prompt templates",
	"list-skills":       "List installed skills from the configured skills directories",
	"prompts":           "Override built-in runtime prompts; empty values use the built-in defaults",
	"raw":               "Render output as raw text when connected to a TTY",
	"hide-tool-status":  "Hide the tool-operation label while tools run (the spinner stays visible)",
	"show-tool-results": "Show completed shell-command result blocks in the output",
	"show-token-usage":  "Show input, output, and total token usage after each interaction",
	"help":              "Show Help and exit",
	"version":           "Show version and exit",
	"max-retries":       "Maximum number of times to retry API calls",
	"no-limit":          "Turn off the client-side limit on the size of the input into the model",
	"no-instructions":   "Disable auto-loading AGENTS.md from the workspace root as project context",
	"word-wrap":         "Wrap formatted output at specific width (default is 80)",
	"max-tokens":        "Maximum number of tokens in response",
	"settings":          "Open settings in your $EDITOR",
	"config":            "Interactive setup wizard for provider, model, API key, and tools",
	"dirs":              "Print the directories in which mods store its data",
	"reset-settings":    "Backup your old settings file and reset everything to the defaults",
	"continue":          "Continue from the last response or a given save title",
	"continue-last":     "Continue from the last response",
	"no-save":           "Disable saving and resuming sessions",
	"chat":              "Start a continuous session; type /exit or /quit to quit",
	"list-sessions":     "Interactively browse, view, and delete saved sessions",
	"theme":             "Theme to use in interactive forms and panels; valid choices are charm, catppuccin, dracula, and base16",
	"editor":            "Edit the prompt in your $EDITOR; only taken into account if no other args and if STDIN is a TTY",
	"mcp-servers":       "MCP Servers configurations",

	"list-mcps":              "List all available MCP servers",
	"list-tools":             "List all available tools (built-in and MCP), with built-in tools annotated",
	"mcp-timeout":            "Timeout for MCP server calls, defaults to 15 seconds",
	"builtin-tools":          "Native tool configuration for filesystem, shell, and sequential thinking tools",
	"web-search":             "Enable or disable the web_search tool",
	"web-search-provider":    "Web search provider: duckduckgo (default), tavily, or custom",
	"web-search-api-key":     "API key for the web search provider (required for tavily)",
	"web-search-api-key-env": "Environment variable name that holds the web search API key (defaults to " + DefaultWebSearchAPIKeyEnv + ")",
	"image":                  "Attach one or more images to the prompt (supports png, jpg, gif, webp). Can be specified multiple times or as comma-separated paths",
	"stdin-image":            "Treat piped stdin input as raw image data instead of text",
	"clipboard-image":        "Attach the current image in the system clipboard to the prompt",
	"debug":                  "Enable debug mode to print execution steps, tool calls, and request details",
	"max-tool-rounds":        "Maximum total tool call rounds before stopping; 0 = default (30); failed rounds are capped at 3",
	"think":                  "Enable extended thinking for models that opt in with thinking-type",
	"review-mode":            "Set tool review mode: auto (default), always, or never",
	"shell-classify-prompt":  "Legacy custom prompt for classifying whether a shell command needs review; prefer prompts.shell-classifier",
	"skills-dirs":            "Directories containing installed skills. Can be set multiple times; later directories override earlier same-name skills. Defaults to ~/.agents/skills, plus a skills directory next to the executable in portable mode.",
	"workspace":              "Set the workspace for filesystem tools and shell, resolving relative paths from the current working directory",
	"plan":                   "Plan mode: generates a detailed plan for user approval before executing any changes",
}

// Model represents the LLM model used in the API call.
type Model struct {
	Name           string
	API            string
	MaxChars       int64          `yaml:"max-input-chars"`
	Aliases        []string       `yaml:"aliases"`
	Fallback       string         `yaml:"fallback"`
	ThinkingBudget int            `yaml:"thinking-budget,omitempty"`
	ExtraParams    map[string]any `yaml:"extra-params,omitempty"`
	// ThinkingType opts this model into -t / --think. When empty, mods keeps
	// thinking disabled even if the provider defaults it on. Values are mapped
	// by the app thinking policy (e.g. "enabled", or "adaptive" for MiniMax).
	ThinkingType string `yaml:"thinking-type,omitempty"`
	// ThinkFields overrides the list of delta fields consulted for
	// reasoning/thinking content extraction. Defaults to
	// [reasoning_content, reasoning, thinking, thinking_content].
	ThinkFields []string `yaml:"thought-fields,omitempty"`
	// ThinkTag overrides the inline reasoning tag name. Defaults to "think"
	// (rendered as <think>...</think>). Only consulted when thinking is on.
	ThinkTag string `yaml:"think-tag,omitempty"`
	// ReasoningEffort is the target reasoning_effort value when thinking
	// is enabled. Defaults to "medium".
	ReasoningEffort string `yaml:"reasoning-effort,omitempty"`
}

// API represents an API endpoint and its models.
type API struct {
	Name      string
	APIKey    string           `yaml:"api-key"`
	APIKeyEnv string           `yaml:"api-key-env"`
	APIKeyCmd string           `yaml:"api-key-cmd"`
	Version   string           `yaml:"version"`
	BaseURL   string           `yaml:"base-url"`
	Models    map[string]Model `yaml:"models"`
	User      string           `yaml:"user"`
	// APIType declares the wire protocol a custom provider speaks, overriding
	// name-based routing. When empty, mods routes by the provider name and
	// defaults unknown names to the OpenAI-compatible adapter. Set this to
	// "anthropic" for any endpoint that implements the Anthropic Messages API,
	// or one of "openai", "ollama", "google", "azure", "azure-ad".
	APIType string `yaml:"api-type,omitempty"`
}

// APIs is a type alias to allow custom YAML decoding.
type APIs []API

// UnmarshalYAML implements sorted API YAML decoding.
func (apis *APIs) UnmarshalYAML(node *yaml.Node) error {
	for i := 0; i < len(node.Content); i += 2 {
		var api API
		if err := node.Content[i+1].Decode(&api); err != nil {
			return fmt.Errorf("error decoding YAML file: %s", err)
		}
		api.Name = node.Content[i].Value
		*apis = append(*apis, api)
	}
	return nil
}

// FormatText is a map[format]formatting_text.
type FormatText map[string]string

// UnmarshalYAML conforms with yaml.Unmarshaler.
func (ft *FormatText) UnmarshalYAML(unmarshal func(any) error) error {
	var text string
	if err := unmarshal(&text); err != nil {
		var formats map[string]string
		if err := unmarshal(&formats); err != nil {
			return err
		}
		*ft = (FormatText)(formats)
		return nil
	}

	*ft = map[string]string{
		"markdown": text,
	}
	return nil
}

// PersistentConfig holds configuration that is persisted to the YAML settings
// file and loaded from environment variables. It is embedded in Config so all
// fields are promoted and accessible directly on Config.
type PersistentConfig struct {
	API                 string     `yaml:"default-api" env:"API"`
	Model               string     `yaml:"default-model" env:"MODEL"`
	Format              string     `yaml:"format" env:"FORMAT"`
	FormatText          FormatText `yaml:"format-text"`
	Minimal             bool       `yaml:"minimal" env:"MINIMAL"`
	Raw                 bool       `yaml:"raw" env:"RAW"`
	HideToolStatus      bool       `yaml:"hide-tool-status" env:"HIDE_TOOL_STATUS"`
	ShowToolResults     bool       `yaml:"show-tool-results" env:"SHOW_TOOL_RESULTS"`
	ShowTokenUsage      bool       `yaml:"show-token-usage" env:"SHOW_TOKEN_USAGE"`
	MaxTokens           int64      `yaml:"max-tokens" env:"MAX_TOKENS"`
	MaxInputChars       int64      `yaml:"max-input-chars" env:"MAX_INPUT_CHARS"`
	NoLimit             bool       `yaml:"no-limit" env:"NO_LIMIT"`
	NoInstructions      bool       `yaml:"no-instructions" env:"NO_INSTRUCTIONS"`
	MaxRetries          int        `yaml:"max-retries" env:"MAX_RETRIES"`
	WordWrap            int        `yaml:"word-wrap" env:"WORD_WRAP"`
	HTTPProxy           string     `yaml:"http-proxy" env:"HTTP_PROXY"`
	APIs                APIs       `yaml:"apis"`
	Role                string     `yaml:"role" env:"ROLE"`
	Roles               map[string][]string
	Prompts             PromptConfig `yaml:"prompts"`
	Theme               string
	MCPServers          map[string]MCPServerConfig `yaml:"mcp-servers"`
	MCPTimeout          time.Duration              `yaml:"mcp-timeout" env:"MCP_TIMEOUT"`
	BuiltinTools        BuiltinToolsConfig         `yaml:"builtin-tools"`
	WebSearch           bool                       `yaml:"web-search" env:"WEB_SEARCH"`
	WebSearchProvider   string                     `yaml:"web-search-provider" env:"WEB_SEARCH_PROVIDER"`
	WebSearchAPIKey     string                     `yaml:"web-search-api-key" env:"WEB_SEARCH_API_KEY"`
	WebSearchAPIKeyEnv  string                     `yaml:"web-search-api-key-env"`
	Images              []string                   `yaml:"images" env:"IMAGES"`
	StdinImage          bool                       `yaml:"stdin-image" env:"STDIN_IMAGE"`
	ClipboardImage      bool                       `yaml:"clipboard-image" env:"CLIPBOARD_IMAGE"`
	Think               bool                       `yaml:"think" env:"THINK"`
	ReviewMode          ReviewMode                 `yaml:"review-mode" env:"REVIEW_MODE"`
	ShellClassifyPrompt string                     `yaml:"shell-classify-prompt"`
	SkillsDirs          []string                   `yaml:"skills-dirs"`
	MaxToolRounds       int                        `yaml:"max-tool-rounds" env:"MAX_TOOL_ROUNDS"`

	// Deprecated: retained for YAML backward compatibility; no longer read at runtime.
	System string `yaml:"system"`
	// Deprecated: retained for YAML backward compatibility; prefer SetDebugEnabled().
	Debug bool `yaml:"debug" env:"DEBUG"`
}

// Config holds the full application configuration. PersistentConfig is embedded
// so that all persisted fields are promoted and accessible directly on Config.
// The remaining fields are CLI-only flags or computed runtime state.
type Config struct {
	PersistentConfig `yaml:",inline"`

	// CLI-flag-only fields (one-shot operations, never persisted).
	AskModel      bool
	Chat          bool
	Plan          bool
	ShowHelp      bool
	ResetSettings bool
	Version       bool
	Settings      bool
	ConfigSetup   bool
	Dirs          bool
	ContinueLast  bool
	Continue      string
	List          bool
	ListRoles     bool
	ListPrompts   bool
	ListSkills    bool
	MCPList       bool
	MCPListTools  bool

	NoSave bool

	// Runtime state (computed internally, never persisted).
	Prefix                                                   string
	SettingsPath                                             string
	SettingsExisted                                          bool
	User                                                     string
	OpenEditor                                               bool
	InteractiveTTYAvailable                                  bool
	SessionDir                                               string
	PortableDir                                              string
	SessionReadFromID, SessionWriteToID, SessionWriteToTitle string
}

// PromptConfig holds user overrides for built-in runtime prompts.
type PromptConfig struct {
	Identity        string `yaml:"identity"`
	ToolSelection   string `yaml:"tool-selection"`
	Plan            string `yaml:"plan"`
	ShellClassifier string `yaml:"shell-classifier"`
}

func (p PromptConfig) Value(key string) string {
	switch key {
	case prompts.KeyIdentity:
		return p.Identity
	case prompts.KeyToolSelection:
		return p.ToolSelection
	case prompts.KeyPlan:
		return p.Plan
	case prompts.KeyShellClassifier:
		return p.ShellClassifier
	default:
		return ""
	}
}

// Workspace describes the configured workspace in normalized forms.
type Workspace struct {
	Input     string
	Abs       string
	Canonical string
	Display   string
}

func (c Config) ResolveWorkspace() Workspace {
	input := c.BuiltinTools.Workspace
	abs := ""
	if c.BuiltinTools.Workspace != "" {
		if resolved, err := filepath.Abs(c.BuiltinTools.Workspace); err == nil {
			abs = resolved
		}
	} else if cwd, err := os.Getwd(); err == nil {
		input = cwd
		abs = cwd
	}
	if abs == "" {
		abs = input
	}
	canonical := filepath.Clean(abs)
	if eval, err := filepath.EvalSymlinks(canonical); err == nil {
		canonical = eval
	}
	return Workspace{
		Input:     input,
		Abs:       filepath.Clean(abs),
		Canonical: canonical,
		Display:   filepath.Clean(abs),
	}
}

// BuiltinToolsConfig controls native tools implemented by mods.
type BuiltinToolsConfig struct {
	Filesystem         FilesystemMode `yaml:"filesystem"`
	Shell              bool           `yaml:"shell"`
	SequentialThinking bool           `yaml:"sequential-thinking"`
	ShellTimeout       time.Duration  `yaml:"shell-timeout"`
	ShellMaxOutput     int            `yaml:"shell-max-output"`
	Workspace          string         `yaml:"workspace"`
}

// FilesystemMode controls when native filesystem tools are exposed.
type FilesystemMode string

const (
	FilesystemAuto   FilesystemMode = "auto"
	FilesystemAlways FilesystemMode = "true"
	FilesystemNever  FilesystemMode = "false"
)

// UnmarshalYAML accepts both the new string modes and old boolean values.
// Invalid values cause a warning and fall back to "auto".
func (m *FilesystemMode) UnmarshalYAML(node *yaml.Node) error {
	if node.Tag == "!!bool" {
		var enabled bool
		if err := node.Decode(&enabled); err != nil {
			return err
		}
		if enabled {
			*m = FilesystemAlways
		} else {
			*m = FilesystemNever
		}
		return nil
	}

	var value string
	if err := node.Decode(&value); err != nil {
		return err
	}
	mode, err := parseFilesystemMode(value)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: invalid builtin-tools.filesystem %q, defaulting to \"true\"\n", value)
		*m = FilesystemAlways
		return nil
	}
	*m = mode
	return nil
}

func parseFilesystemMode(value string) (FilesystemMode, error) {
	value = stdstrings.ToLower(stdstrings.TrimSpace(value))
	switch value {
	case "", "auto":
		return FilesystemAuto, nil
	case "true", "always", "on":
		return FilesystemAlways, nil
	case "false", "never", "off":
		return FilesystemNever, nil
	default:
		return "", fmt.Errorf("invalid builtin-tools.filesystem mode %q, expected auto, true, or false", value)
	}
}

// MCPServerConfig holds configuration for an MCP server.
type MCPServerConfig struct {
	Type    string   `yaml:"type"`
	Command string   `yaml:"command"`
	Env     []string `yaml:"env"`
	Args    []string `yaml:"args"`
	URL     string   `yaml:"url"`
	// PassEnvAll restores the legacy behaviour of forwarding the entire
	// parent process environment (including OPENAI_API_KEY, AWS_*,
	// GITHUB_TOKEN, ...) to the MCP subprocess. The default is to filter
	// out variables that commonly carry secrets so a third-party MCP
	// package cannot exfiltrate them. Users who deliberately want to
	// share their full environment with a trusted server can set this
	// to true; secrets only the server needs are otherwise listed
	// explicitly via the Env field.
	PassEnvAll bool `yaml:"pass-env-all"`
}

func Ensure() (Config, error) {
	// fallback is the Config returned on every error path. Callers such as
	// the --settings / --config wizards continue to use the returned value
	// after a partial failure, so it must satisfy the invariants those
	// callers rely on: SettingsPath is filled in as soon as it is known,
	// SessionDir always has the fixed session storage path, and every other
	// field carries its Default() value rather than the zero value.
	fallback := Default()
	c := Default()
	applySessionDirDefault(&c)
	applySessionDirDefault(&fallback)
	applySkillsDirsDefault(&c)
	applySkillsDirsDefault(&fallback)

	sp, err := settingsFilePath()
	if err != nil {
		return fallback, modsError{Err: err, ReasonText: "Could not find settings path."}
	}
	c.SettingsPath = sp
	fallback.SettingsPath = sp
	if portableActive() {
		exeDir := executableDir()
		c.PortableDir = exeDir
		fallback.PortableDir = exeDir
	}

	dir := filepath.Dir(sp)
	if dirErr := os.MkdirAll(dir, 0o700); dirErr != nil { //nolint:mnd
		return fallback, modsError{Err: dirErr, ReasonText: "Could not create config directory."}
	}
	_, statErr := os.Stat(sp)
	switch {
	case statErr == nil:
		c.SettingsExisted = true
	case errors.Is(statErr, os.ErrNotExist):
		c.SettingsExisted = false
	default:
		return fallback, modsError{Err: statErr, ReasonText: "Could not stat path."}
	}

	if dirErr := WriteDefaultFile(sp); dirErr != nil {
		return fallback, dirErr
	}
	content, err := os.ReadFile(sp)
	if err != nil {
		return fallback, modsError{Err: err, ReasonText: "Could not read settings file."}
	}
	// Parse env vars first so that the config file takes priority over
	// environment overrides (priority: CLI flags > config file > env > defaults).
	if err := env.ParseWithOptions(&c, env.Options{Prefix: "MODS_"}); err != nil {
		return fallback, modsError{Err: err, ReasonText: "Could not parse environment into settings file."}
	}
	if err := parseSkillsDirsEnv(&c); err != nil {
		return fallback, modsError{Err: err, ReasonText: "Could not parse environment into settings file."}
	}

	if err := yaml.Unmarshal(content, &c); err != nil {
		return fallback, modsError{Err: err, ReasonText: "Could not parse settings file."}
	}
	validateReviewMode(&c)

	applySessionDirDefault(&c)
	applySkillsDirsDefault(&c)

	if err := os.MkdirAll(
		c.SessionDir,
		0o700,
	); err != nil { //nolint:mnd
		return fallback, modsError{Err: err, ReasonText: "Could not create session directory."}
	}

	c.applyDefaults()

	return c, nil
}

// ApplyDefaults normalizes fields whose zero value (set explicitly via env,
// YAML, or partial CLI input) should fall back to a canonical default. The
// canonical values live in Default() for the no-config case and in the
// tools package for shell-related constants; this method exists so Ensure
// does not have to re-derive each default at a different call site (which
// previously led to drift between Default(), Ensure(), initFlags and the
// tools package).
//
// ApplyDefaults is idempotent: running it twice produces the same Config.
// Callers that construct a Config without going through Ensure (for
// example, tests) should invoke ApplyDefaults to obtain the same
// normalization the production path applies.
func (c *Config) ApplyDefaults() {
	c.applyDefaults()
}

func (c *Config) applyDefaults() {
	if c.WordWrap <= 0 {
		c.WordWrap = 80
	}
	if c.MCPTimeout <= 0 {
		c.MCPTimeout = 15 * time.Second
	}
	if c.FormatText == nil {
		c.FormatText = FormatText{
			"markdown": defaultMarkdownFormatText,
			"json":     defaultJSONFormatText,
		}
	}
	if c.Format != "" && c.FormatText[c.Format] == "" {
		c.FormatText[c.Format] = defaultFormatTextFor(c.Format)
	}
	if c.WebSearchAPIKeyEnv == "" {
		c.WebSearchAPIKeyEnv = DefaultWebSearchAPIKeyEnv
	}
	if c.WebSearchAPIKey == "" {
		c.WebSearchAPIKey = os.Getenv(c.WebSearchAPIKeyEnv)
	}
}

// defaultFormatTextFor returns the canonical FormatText body for the
// given format key. Unknown keys fall back to the markdown body so
// downstream format-text lookups never silently produce empty output.
func defaultFormatTextFor(format string) string {
	switch format {
	case "json":
		return defaultJSONFormatText
	default:
		return defaultMarkdownFormatText
	}
}

func validateReviewMode(c *Config) {
	switch c.ReviewMode {
	case "", ReviewAuto, ReviewAlways, ReviewNever:
		return
	default:
		fmt.Fprintf(os.Stderr, "Warning: invalid review mode %q, defaulting to \"auto\"\n", c.ReviewMode)
		c.ReviewMode = ReviewAuto
	}
}

// applySessionDirDefault ensures c.SessionDir always points at the fixed
// session storage directory. The default lives outside Default() because
// the XDG lookup depends on environment variables that are normally only
// resolved at Ensure() time, and we want any partial Ensure() failure to
// still leave callers with a Config they can use for --settings / --config
// flows.
func applySessionDirDefault(c *Config) {
	if c.SessionDir == "" {
		c.SessionDir = defaultSessionDir()
	}
}

// defaultSessionDir resolves the session storage directory. In portable
// mode (mods.yml next to the executable) it lives next to the binary so
// the whole folder is self-contained; otherwise it follows the XDG data
// home. Portable mode intentionally ignores XDG_DATA_HOME.
func defaultSessionDir() string {
	if portableActive() {
		return filepath.Join(executableDir(), "sessions")
	}
	return filepath.Join(xdg.DataHome, "mods", "sessions")
}

// defaultSkillsDirs resolves the default skills directories. Portable mode
// keeps the shared user-level directory and also loads skills next to the
// executable so the portable folder can carry overrides with it.
func defaultSkillsDirs() []string {
	userDir := filepath.Join(xdg.Home, ".agents", "skills")
	if portableActive() {
		return []string{userDir, filepath.Join(executableDir(), "skills")}
	}
	return []string{userDir}
}

// NormalizeSkillsDir expands a leading home-directory marker in a skills
// path. It intentionally leaves relative paths and ~user syntax unchanged.
func NormalizeSkillsDir(path string) string {
	if path == "~" {
		return xdg.Home
	}
	if stdstrings.HasPrefix(path, "~/") ||
		(runtime.GOOS == "windows" && stdstrings.HasPrefix(path, `~\`)) {
		return filepath.Join(xdg.Home, path[2:])
	}
	return path
}

func parseSkillsDirsEnv(c *Config) error {
	value := os.Getenv("MODS_SKILLS_DIRS")
	if value == "" {
		return nil
	}
	c.SkillsDirs = filepath.SplitList(value)
	return nil
}

// applySkillsDirsDefault ensures c.SkillsDirs is a normalized ordered list
// with the default skills directories as its empty/default value.
func applySkillsDirsDefault(c *Config) {
	if len(c.ResolveSkillsDirs()) == 0 {
		c.SkillsDirs = defaultSkillsDirs()
		return
	}
	c.SkillsDirs = c.ResolveSkillsDirs()
}

func (c Config) ResolveSkillsDirs() []string {
	raw := make([]string, 0, len(c.SkillsDirs))
	raw = append(raw, c.SkillsDirs...)

	last := make(map[string]int, len(raw))
	normalized := make([]string, 0, len(raw))
	for _, dir := range raw {
		dir = stdstrings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		dir = NormalizeSkillsDir(dir)
		normalized = append(normalized, dir)
		last[dir] = len(normalized) - 1
	}

	result := normalized[:0]
	for i, dir := range normalized {
		if last[dir] == i {
			result = append(result, dir)
		}
	}
	if len(result) == 0 {
		return defaultSkillsDirs()
	}
	return result
}

func settingsFilePath() (string, error) {
	// Portable mode: a mods.yml next to the executable takes precedence
	// over XDG/home resolution and ignores XDG_CONFIG_HOME.
	if p, ok := portableConfigPath(); ok {
		return p, nil
	}
	relPath := filepath.Join("mods", "mods.yml")
	if configHome := os.Getenv("XDG_CONFIG_HOME"); configHome != "" {
		return filepath.Join(configHome, relPath), nil
	}
	if runtime.GOOS != "darwin" {
		return xdg.ConfigFile(relPath)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", relPath), nil
}

func WriteDefaultFile(path string) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return createConfigFile(path)
	} else if err != nil {
		return modsError{Err: err, ReasonText: "Could not stat path."}
	}
	return nil
}

func createConfigFile(path string) error {
	tmpl := template.Must(template.New("config").Parse(configTemplate))

	// Create with restrictive mode in the OpenFile call rather than a
	// post-Create Chmod: the latter leaves a sub-millisecond window during
	// which a 0o666 (& ~umask) file containing the API-key template is
	// readable by other local users. O_EXCL ensures we never silently
	// overwrite an existing file the caller did not consent to clobber.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return modsError{Err: err, ReasonText: "Could not create configuration file."}
	}
	defer func() { _ = f.Close() }()

	m := struct {
		Config Config
		Help   map[string]string
	}{
		Config: Default(),
		Help:   Help,
	}
	if err := tmpl.Execute(f, m); err != nil {
		return modsError{Err: err, ReasonText: "Could not render template."}
	}
	return nil
}

func Default() Config {
	return Config{
		PersistentConfig: PersistentConfig{
			FormatText: FormatText{
				"markdown": defaultMarkdownFormatText,
				"json":     defaultJSONFormatText,
			},
			ReviewMode:         ReviewAuto,
			WordWrap:           80,
			MCPTimeout:         15 * time.Second,
			WebSearchAPIKeyEnv: DefaultWebSearchAPIKeyEnv,
			BuiltinTools: BuiltinToolsConfig{
				Filesystem:         FilesystemAlways,
				Shell:              false,
				SequentialThinking: false,
				// Reference the canonical tools-package defaults so the
				// YAML template and the runtime fallback cannot drift.
				ShellTimeout:   tools.DefaultShellTimeout,
				ShellMaxOutput: tools.DefaultShellMaxOutput,
			},
		},
	}
}
