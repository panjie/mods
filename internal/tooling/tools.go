package tooling

import (
	"context"
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync"

	"github.com/panjie/mods/internal/approval"
	cfgpkg "github.com/panjie/mods/internal/config"
	"github.com/panjie/mods/internal/mcpclient"
	"github.com/panjie/mods/internal/selfhelp"
	"github.com/panjie/mods/internal/skills"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/panjie/mods/internal/websearch"
)

var filesystemPathPattern = regexp.MustCompile(`(?i)(^|\s)(\.?/[\w.-]+|[\w.-]+/[\w./-]+|[\w.-]+\.(go|ts|tsx|js|jsx|py|rs|java|c|cc|cpp|h|hpp|md|txt|json|yaml|yml|toml|mod|sum|sh|sql))($|\s|[,.，。:：;；])`)

// builtinEnv carries everything a built-in tool registration may depend on.
type builtinEnv struct {
	registry *toolregistry.Registry
	cfg      *cfgpkg.Config
	wscfg    websearch.Config
	prompt   string
	root     string
	safeDirs []string
	skills   []skills.Skill
	handlers toolregistry.InteractionHandlers
}

// builtinTool is one entry of the built-in tool table: the single place where a
// built-in's registration and its runtime enablement are declared.
//
// Both the runtime registry (BuildRegistry) and the --list-tools catalogue
// (BuiltinSpecs) walk this table. Previously each kept its own hand-copied
// registration chain, so a new built-in could be registered at runtime and be
// missing from the catalogue (or vice versa) with nothing able to notice.
type builtinTool struct {
	// group names one registration for error messages and tests; a single
	// registration can add several tools (the filesystem group registers fs_*).
	group    string
	register func(builtinEnv) error
	// enabled reports whether the tool belongs in this runtime configuration. It
	// is ignored when the catalogue forces every entry. nil means always.
	enabled func(builtinEnv) bool
}

func builtinTools() []builtinTool {
	return []builtinTool{
		{
			group: "mods_help",
			register: func(env builtinEnv) error {
				return toolregistry.RegisterModsHelp(env.registry, toolregistry.ModsHelpConfig{
					SettingsPath:   env.cfg.SettingsPath,
					Portable:       env.cfg.PortableDir != "",
					FilesystemMode: string(env.cfg.BuiltinTools.Filesystem),
					Reference:      env.handlers.SelfHelp,
				})
			},
		},
		{
			group: "todo_write",
			register: func(env builtinEnv) error {
				return toolregistry.RegisterTodoWrite(env.registry)
			},
		},
		{
			group:   "filesystem",
			enabled: func(env builtinEnv) bool { return ShouldEnableFilesystemTools(env.cfg, env.prompt) },
			register: func(env builtinEnv) error {
				return toolregistry.RegisterFilesystem(env.registry, toolregistry.FilesystemConfig{
					Root:     env.root,
					SafeDirs: env.safeDirs,
				})
			},
		},
		{
			group: "http_download",
			// A shell-only runtime still needs downloads: the shell group does
			// not provide them.
			enabled: func(env builtinEnv) bool {
				return ShouldEnableFilesystemTools(env.cfg, env.prompt) || env.cfg.BuiltinTools.Shell
			},
			register: func(env builtinEnv) error {
				return toolregistry.RegisterDownload(env.registry, toolregistry.FilesystemConfig{
					Root:     env.root,
					SafeDirs: env.safeDirs,
				})
			},
		},
		{
			group:   "web_search",
			enabled: func(env builtinEnv) bool { return env.cfg.WebSearch },
			register: func(env builtinEnv) error {
				return toolregistry.RegisterWebSearch(env.registry, env.wscfg)
			},
		},
		{
			group:   "process_run",
			enabled: func(env builtinEnv) bool { return env.cfg.BuiltinTools.Shell },
			register: func(env builtinEnv) error {
				return toolregistry.RegisterProcess(env.registry, toolregistry.ProcessConfig{
					Root:       env.root,
					SafeDirs:   env.safeDirs,
					Timeout:    env.cfg.BuiltinTools.ShellTimeout,
					SudoPrompt: env.handlers.SudoPrompt,
					Progress:   env.handlers.ShellProgress,
				})
			},
		},
		{
			group:   "runtime_info",
			enabled: func(env builtinEnv) bool { return env.cfg.BuiltinTools.Shell },
			register: func(env builtinEnv) error {
				return toolregistry.RegisterRuntimeInfo(env.registry, env.root)
			},
		},
		{
			group:   "shell",
			enabled: func(env builtinEnv) bool { return env.cfg.BuiltinTools.Shell },
			register: func(env builtinEnv) error {
				// Windows exposes only powershell_run: both names share the same
				// PowerShell host, so registering shell_run there would offer the
				// model a second label for the identical executor.
				if runtime.GOOS == "windows" {
					return toolregistry.RegisterPowerShell(env.registry, toolregistry.ShellConfig{
						Root:     env.root,
						Timeout:  env.cfg.BuiltinTools.ShellTimeout,
						Progress: env.handlers.ShellProgress,
					})
				}
				return toolregistry.RegisterShell(env.registry, toolregistry.ShellConfig{
					Root:       env.root,
					Timeout:    env.cfg.BuiltinTools.ShellTimeout,
					SudoPrompt: env.handlers.SudoPrompt,
					Progress:   env.handlers.ShellProgress,
				})
			},
		},
		{
			group:   "skills",
			enabled: func(env builtinEnv) bool { return len(env.skills) > 0 },
			register: func(env builtinEnv) error {
				return toolregistry.RegisterSkill(env.registry, env.skills)
			},
		},
		{
			group:   "request_user_input",
			enabled: func(env builtinEnv) bool { return env.handlers.UserInput != nil },
			register: func(env builtinEnv) error {
				return toolregistry.RegisterUserInput(env.registry, env.handlers.UserInput)
			},
		},
	}
}

// registerBuiltins walks the built-in table. catalogue forces every entry, so
// --list-tools enumerates built-ins regardless of the current configuration, and
// treats a failing registration as non-fatal because listing must not break on
// one tool.
func registerBuiltins(env builtinEnv, catalogue bool) error {
	for _, tool := range builtinTools() {
		if !catalogue && tool.enabled != nil && !tool.enabled(env) {
			continue
		}
		if err := tool.register(env); err != nil {
			if catalogue {
				continue
			}
			return err
		}
	}
	return nil
}

func BuildRegistry(ctx context.Context, cfg *cfgpkg.Config, wscfg websearch.Config, prompt string, skillCatalog []skills.Skill, interaction ...toolregistry.InteractionHandlers) (*toolregistry.Registry, error) {
	registry := toolregistry.NewRegistry()
	complete := false
	defer func() {
		if !complete {
			_ = registry.Close()
		}
	}()
	var handlers toolregistry.InteractionHandlers
	if len(interaction) > 0 {
		handlers = interaction[0]
	}

	workspace := cfg.ResolveWorkspace()
	if err := registerBuiltins(builtinEnv{
		registry: registry,
		cfg:      cfg,
		wscfg:    wscfg,
		prompt:   prompt,
		root:     workspace.Canonical,
		safeDirs: approval.SafeDirs(),
		skills:   skillCatalog,
		handlers: handlers,
	}, false); err != nil {
		return nil, err
	}

	// MCP registration may start subprocesses or open network connections.
	// Keep it last so no subsequent fallible registration can strand those
	// resources; RegisterTools closes its session if an MCP name collides with
	// one of the built-ins registered above.
	if err := mcpclient.RegisterTools(ctx, cfg, registry); err != nil {
		return nil, err
	}

	complete = true
	return registry, nil
}

func ShouldEnableFilesystemTools(cfg *cfgpkg.Config, prompt string) bool {
	switch cfg.BuiltinTools.Filesystem {
	case cfgpkg.FilesystemAlways:
		return true
	case cfgpkg.FilesystemNever:
		return false
	case "", cfgpkg.FilesystemAuto:
		if selfhelp.IsConfigHelpOnly(prompt) {
			return false
		}
		return PromptLooksFileRelated(prompt) ||
			selfhelp.IsConfigMutation(prompt) ||
			selfhelp.IsConfigInspection(prompt)
	default:
		return false
	}
}

func PromptLooksFileRelated(prompt string) bool {
	p := strings.ToLower(prompt)
	keywords := []string{
		"file", "files", "directory", "folder", "repo", "repository",
		"codebase", "source", "write", "edit", "modify", "patch",
		"grep", "rg",
		"文件", "目录", "代码", "仓库", "项目",
		"修改", "编辑", "修复",
	}
	for _, keyword := range keywords {
		if strings.Contains(p, keyword) {
			return true
		}
	}
	return filesystemPathPattern.MatchString(prompt)
}

// BuiltinToolInfo describes one built-in tool for listing/discovery.
type BuiltinToolInfo struct {
	Name        string
	Description string
	Kind        toolregistry.ToolKind
	ReadOnly    bool
	Mutable     bool
	Shell       bool
	Interactive bool
}

var (
	builtinSpecsOnce  sync.Once
	builtinSpecsCache []BuiltinToolInfo
	builtinSpecsErr   error
)

// BuiltinSpecs enumerates every built-in tool mods can provide, independent of
// runtime enablement (which depends on prompt and config). It powers
// --list-tools' built-in catalogue. Tools are registered into a throwaway
// registry (their Call closures are never invoked) purely to harvest specs and
// capabilities, so this works offline and without API keys.
func BuiltinSpecs() ([]BuiltinToolInfo, error) {
	builtinSpecsOnce.Do(func() {
		builtinSpecsCache, builtinSpecsErr = buildBuiltinSpecs()
	})
	return append([]BuiltinToolInfo(nil), builtinSpecsCache...), builtinSpecsErr
}

func buildBuiltinSpecs() ([]BuiltinToolInfo, error) {
	root, err := os.MkdirTemp("", "mods-list-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(root)
	registry := toolregistry.NewRegistry()
	// Walk the same table the runtime registry uses, with the catalogue flag so
	// every entry registers regardless of configuration. Stub dependencies are
	// enough because the Call closures are never invoked; a zero Config keeps the
	// harvested specs independent of the machine's settings.
	var cfg cfgpkg.Config
	_ = registerBuiltins(builtinEnv{registry: registry, cfg: &cfg, root: root}, true)

	infos := make([]BuiltinToolInfo, 0, registry.Len())
	for _, spec := range registry.Specs() {
		tool, ok := registry.Tool(spec.Name)
		if !ok {
			continue
		}
		infos = append(infos, BuiltinToolInfo{
			Name:        spec.Name,
			Description: spec.Description,
			Kind:        tool.Kind,
			ReadOnly:    tool.Capabilities.ReadOnly,
			Mutable:     tool.Capabilities.Mutable,
			Shell:       tool.Capabilities.ShellExecution,
			Interactive: tool.Capabilities.Interactive,
		})
	}
	return infos, nil
}
