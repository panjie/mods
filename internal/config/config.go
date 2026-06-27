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
	"github.com/caarlos0/duration"
	"github.com/caarlos0/env/v9"
	"github.com/charmbracelet/x/exp/strings"
	toolregistry "github.com/panjie/mods/internal/tools"
	"gopkg.in/yaml.v3"
)

//go:embed config_template.yml
var configTemplate string

const (
	defaultMarkdownFormatText = "Format the response as Markdown. Do not wrap the whole response in a code fence unless the user explicitly requests it."
	defaultJSONFormatText     = "Return valid JSON only. Do not include Markdown fences, prose, or explanations unless the user explicitly requests them."
	MinimalSystemPrompt       = "Unless the user explicitly requests otherwise, output only the final answer. Do not explain. Do not use Markdown. For lists, output one item per line. Preserve exact filenames, paths, commands, or IDs. Do not wrap output in quotes or code fences unless explicitly requested."
	ToolSelectionRules        = toolregistry.ToolSelectionRules
)

// ReasoningMode controls whether the LLM should use deep reasoning.
type ReasoningMode string

const (
	ReasoningOff  ReasoningMode = "off"
	ReasoningOn   ReasoningMode = "on"
	ReasoningAuto ReasoningMode = "auto"
)

// ReviewMode controls whether mods prompts for confirmation before executing tools.
type ReviewMode string

const (
	ReviewNever   ReviewMode = "never"
	ReviewMutable ReviewMode = "mutable"
	ReviewAlways  ReviewMode = "always"
)

var Help = map[string]string{
	"api":                    "OpenAI compatible REST API (openai, localai, anthropic, ...)",
	"apis":                   "Aliases and endpoints for OpenAI compatible REST API",
	"http-proxy":             "HTTP proxy to use for API requests",
	"model":                  "Default model (gpt-3.5-turbo, gpt-4, ggml-gpt4all-j...)",
	"ask-model":              "Ask which model to use via interactive prompt",
	"max-input-chars":        "Default character limit on input to model",
	"format":                 "Ask for the response to be formatted as markdown unless otherwise set",
	"format-as":              "Inline format prompt to use (empty = let mods decide based on format-text keys)",
	"format-text":            "Text to append when using the -f flag",
	"minimal":                "Output only the final result, optimized for pipelines",
	"role":                   "System role to use",
	"roles":                  "List of predefined system messages that can be used as roles",
	"list-roles":             "List the roles defined in your configuration file",
	"raw":                    "Render output as raw text when connected to a TTY",
	"quiet":                  "Quiet mode (hide the spinner while loading and stderr messages for success)",
	"hide-tool-status":       "Hide the bottom status line while tools are running",
	"hide-tool-results":      "Hide the completed shell-command result blocks in the output",
	"help":                   "Show Help and exit",
	"help-all":               "Show Help with advanced and configuration-first options",
	"version":                "Show version and exit",
	"max-retries":            "Maximum number of times to retry API calls",
	"no-limit":               "Turn off the client-side limit on the size of the input into the model",
	"word-wrap":              "Wrap formatted output at specific width (default is 80)",
	"max-tokens":             "Maximum number of tokens in response",
	"temp":                   "Temperature (randomness) of results, from 0.0 to 2.0, -1.0 to disable",
	"stop":                   "Up to 4 sequences where the API will stop generating further tokens",
	"topp":                   "TopP, an alternative to temperature that narrows response, from 0.0 to 1.0, -1.0 to disable",
	"topk":                   "TopK, only sample from the top K options for each subsequent token, -1 to disable",
	"status-text":            "Text to show while generating",
	"settings":               "Open settings in your $EDITOR",
	"config":                 "Interactive setup wizard for provider, model, API key, and tools",
	"dirs":                   "Print the directories in which mods store its data",
	"reset-settings":         "Backup your old settings file and reset everything to the defaults",
	"continue":               "Continue from the last response or a given save title",
	"continue-last":          "Continue from the last response",
	"no-cache":               "Disables caching of the prompt/response",
	"cache-path":             "Path to store conversation cache (defaults to XDG_DATA_HOME/mods)",
	"chat":                   "Start a continuous conversation; type /exit or /quit to quit",
	"title":                  "Saves the current conversation with the given title",
	"list":                   "Lists saved conversations",
	"delete":                 "Deletes one or more saved conversations with the given titles or IDs",
	"delete-older-than":      "Deletes all saved conversations older than the specified duration; valid values are " + strings.EnglishJoin(duration.ValidUnits(), true),
	"show":                   "Show a saved conversation with the given title or ID",
	"theme":                  "Theme to use in the forms; valid choices are charm, catppuccin, dracula, and base16",
	"show-last":              "Show the last saved conversation",
	"editor":                 "Edit the prompt in your $EDITOR; only taken into account if no other args and if STDIN is a TTY",
	"mcp-servers":            "MCP Servers configurations",
	"mcp-enable":             "Enable only specific MCP servers (whitelist, overrides disable list)",
	"mcp-disable":            "Disable specific MCP servers",
	"mcp-list":               "List all available MCP servers",
	"mcp-list-tools":         "List all available tools from enabled MCP servers",
	"mcp-timeout":            "Timeout for MCP server calls, defaults to 15 seconds",
	"builtin-tools":          "Native tool configuration for filesystem, shell, and sequential thinking tools",
	"web-search":             "Enable web search for up-to-date information (uses DuckDuckGo by default)",
	"web-search-provider":    "Web search provider: duckduckgo (default), tavily, or custom",
	"web-search-api-key":     "API key for the web search provider (required for tavily)",
	"web-search-api-key-env": "Environment variable name that holds the web search API key (defaults to TAVILY_API_KEY)",
	"image":                  "Attach one or more images to the prompt (supports png, jpg, gif, webp). Can be specified multiple times or as comma-separated paths",
	"stdin-image":            "Treat piped stdin input as raw image data instead of text",
	"clipboard-image":        "Attach the current image in the system clipboard to the prompt",
	"debug":                  "Enable debug mode to print execution steps, tool calls, and request details",
	"max-tool-rounds":        "Maximum total tool call rounds before stopping; 0 = default (30); failed rounds are capped at 3",
	"reasoning":              "Enables deep reasoning mode: off, on, or auto (judge task complexity with current model)",
	"review":                 "Review tool execution before running: never, mutable (default), or always",
	"shell-classify-prompt":  "Custom legacy prompt for classifying whether a shell command needs review; defaults to built-in JSON analysis when unset",
	"workspace":              "Set the workspace root for filesystem tools and shell, resolving relative paths from the current working directory",
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
	// ThinkingType is the value to set thinking.type to when reasoning is
	// enabled via -T / --reasoning. Defaults to "adaptive" (MiniMax) when
	// extra-params.thinking already exists; use "enabled" for GLM and
	// Anthropic-compatible APIs.
	ThinkingType string `yaml:"thinking-type,omitempty"`
	// ThinkFields overrides the list of delta fields consulted for
	// reasoning/thinking content extraction. Defaults to
	// [reasoning_content, reasoning, thinking, thinking_content].
	ThinkFields []string `yaml:"thought-fields,omitempty"`
	// ThinkTag overrides the inline reasoning tag name. Defaults to "think"
	// (rendered as <think>...</think>). Only consulted when reasoning is on.
	ThinkTag string `yaml:"think-tag,omitempty"`
	// ReasoningEffort is the target reasoning_effort value when reasoning
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
	Format              bool       `yaml:"format" env:"FORMAT"`
	FormatText          FormatText `yaml:"format-text"`
	FormatAs            string     `yaml:"format-as" env:"FORMAT_AS"`
	Minimal             bool       `yaml:"minimal" env:"MINIMAL"`
	Raw                 bool       `yaml:"raw" env:"RAW"`
	Quiet               bool       `yaml:"quiet" env:"QUIET"`
	HideToolStatus      bool       `yaml:"hide-tool-status" env:"HIDE_TOOL_STATUS"`
	HideToolResults     bool       `yaml:"hide-tool-results" env:"HIDE_TOOL_RESULTS"`
	MaxTokens           int64      `yaml:"max-tokens" env:"MAX_TOKENS"`
	MaxInputChars       int64      `yaml:"max-input-chars" env:"MAX_INPUT_CHARS"`
	Temperature         float64    `yaml:"temp" env:"TEMP"`
	Stop                []string   `yaml:"stop" env:"STOP"`
	TopP                float64    `yaml:"topp" env:"TOPP"`
	TopK                int64      `yaml:"topk" env:"TOPK"`
	NoLimit             bool       `yaml:"no-limit" env:"NO_LIMIT"`
	CachePath           string     `yaml:"cache-path" env:"CACHE_PATH"`
	NoCache             bool       `yaml:"no-cache" env:"NO_CACHE"`
	MaxRetries          int        `yaml:"max-retries" env:"MAX_RETRIES"`
	WordWrap            int        `yaml:"word-wrap" env:"WORD_WRAP"`
	StatusText          string     `yaml:"status-text" env:"STATUS_TEXT"`
	HTTPProxy           string     `yaml:"http-proxy" env:"HTTP_PROXY"`
	APIs                APIs       `yaml:"apis"`
	Role                string     `yaml:"role" env:"ROLE"`
	Roles               map[string][]string
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
	Reasoning           ReasoningMode              `yaml:"reasoning" env:"REASONING"`
	ReviewMode          ReviewMode                 `yaml:"review-mode" env:"REVIEW_MODE"`
	ShellClassifyPrompt string                     `yaml:"shell-classify-prompt"`
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
	AskModel        bool
	Chat            bool
	Plan            bool
	ShowHelp        bool
	HelpAll         bool
	ResetSettings   bool
	Version         bool
	Settings        bool
	ConfigSetup     bool
	Dirs            bool
	ContinueLast    bool
	Continue        string
	Title           string
	ShowLast        bool
	Show            string
	List            bool
	ListRoles       bool
	Delete          []string
	DeleteOlderThan time.Duration
	MCPList         bool
	MCPListTools    bool
	MCPEnable       []string
	MCPDisable      []string

	// Runtime state (computed internally, never persisted).
	Prefix                                             string
	SettingsPath                                       string
	SettingsExisted                                    bool
	User                                               string
	OpenEditor                                         bool
	CacheReadFromID, CacheWriteToID, CacheWriteToTitle string
}

// Workspace describes the configured workspace root in normalized forms.
type Workspace struct {
	Input     string
	Abs       string
	Canonical string
	Display   string
}

func (c Config) ResolveWorkspaceRoot() string {
	workspace := c.ResolveWorkspace()
	return workspace.Canonical
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
		return err
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
}

func Ensure() (Config, error) {
	c := Default()
	sp, err := settingsFilePath()
	if err != nil {
		return c, modsError{Err: err, ReasonText: "Could not find settings path."}
	}
	c.SettingsPath = sp

	dir := filepath.Dir(sp)
	if dirErr := os.MkdirAll(dir, 0o700); dirErr != nil { //nolint:mnd
		return c, modsError{Err: dirErr, ReasonText: "Could not create cache directory."}
	}
	_, statErr := os.Stat(sp)
	switch {
	case statErr == nil:
		c.SettingsExisted = true
	case errors.Is(statErr, os.ErrNotExist):
		c.SettingsExisted = false
	default:
		return c, modsError{Err: statErr, ReasonText: "Could not stat path."}
	}

	if dirErr := WriteDefaultFile(sp); dirErr != nil {
		return c, dirErr
	}
	content, err := os.ReadFile(sp)
	if err != nil {
		return c, modsError{Err: err, ReasonText: "Could not read settings file."}
	}
	// Parse env vars first so that the config file takes priority over
	// environment overrides (priority: CLI flags > config file > env > defaults).
	if err := env.ParseWithOptions(&c, env.Options{Prefix: "MODS_"}); err != nil {
		return c, modsError{Err: err, ReasonText: "Could not parse environment into settings file."}
	}

	if err := yaml.Unmarshal(content, &c); err != nil {
		return c, modsError{Err: err, ReasonText: "Could not parse settings file."}
	}

	if c.CachePath == "" {
		c.CachePath = filepath.Join(xdg.DataHome, "mods")
	}

	if err := os.MkdirAll(
		filepath.Join(c.CachePath, "conversations"),
		0o700,
	); err != nil { //nolint:mnd
		return c, modsError{Err: err, ReasonText: "Could not create cache directory."}
	}

	if c.WordWrap < 0 {
		c.WordWrap = 80
	}
	if c.WebSearchAPIKey == "" {
		envName := c.WebSearchAPIKeyEnv
		if envName == "" {
			envName = "TAVILY_API_KEY"
		}
		c.WebSearchAPIKey = os.Getenv(envName)
	}

	return c, nil
}

func settingsFilePath() (string, error) {
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

	f, err := os.Create(path)
	if err != nil {
		return modsError{Err: err, ReasonText: "Could not create configuration file."}
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return modsError{Err: err, ReasonText: "Could not set configuration file permissions."}
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
			FormatAs: "markdown",
			FormatText: FormatText{
				"markdown": defaultMarkdownFormatText,
				"json":     defaultJSONFormatText,
			},
			Reasoning:  ReasoningOff,
			ReviewMode: ReviewMutable,
			WordWrap:   80,
			StatusText: "Generating",
			MCPTimeout: 15 * time.Second,
			BuiltinTools: BuiltinToolsConfig{
				Filesystem:         FilesystemAuto,
				Shell:              false,
				SequentialThinking: false,
				ShellTimeout:       30 * time.Second,
				ShellMaxOutput:     20000,
			},
		},
	}
}
