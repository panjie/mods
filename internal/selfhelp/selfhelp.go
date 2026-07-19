package selfhelp

import (
	_ "embed"
	"fmt"
	"slices"
	"strings"
)

//go:embed reference.md
var guidance string

const (
	TopicOverview        = "overview"
	TopicCLI             = "cli"
	TopicConfig          = "config"
	TopicProviders       = "providers"
	TopicTools           = "tools"
	TopicSkills          = "skills"
	TopicPortable        = "portable"
	TopicTroubleshooting = "troubleshooting"
	TopicAll             = "all"
)

var topics = []string{
	TopicOverview,
	TopicCLI,
	TopicConfig,
	TopicProviders,
	TopicTools,
	TopicSkills,
	TopicPortable,
	TopicTroubleshooting,
	TopicAll,
}

// FlagGroup describes the visible CLI flags in one help category. The CLI
// builds these values from its live pflag registry after flag registration.
type FlagGroup struct {
	Name  string
	Flags []Flag
}

// Flag describes one provider-visible CLI option without its runtime default.
// Runtime defaults can contain values loaded from the user's config.
type Flag struct {
	Name        string
	Shorthand   string
	ValueType   string
	Description string
	Advanced    bool
}

// Setting describes one persisted YAML path using metadata from the config
// package. Default is populated only for safe scalar defaults.
type Setting struct {
	Path        string
	ValueType   string
	Description string
	Default     string
}

// Provider describes built-in provider metadata shared with runtime routing.
type Provider struct {
	Name           string
	Protocol       string
	Description    string
	DefaultBaseURL string
	APIKeyEnv      string
}

// Tool describes a built-in tool using its actual ToolSpec and capabilities.
type Tool struct {
	Name        string
	Description string
	Kind        string
	ReadOnly    bool
	Mutable     bool
	Shell       bool
	Interactive bool
}

// Catalog contains factual self-help metadata collected from the subsystems
// that own it. NewReference clones the catalog so a Reference is immutable.
type Catalog struct {
	Flags     []FlagGroup
	Settings  []Setting
	Providers []Provider
	Protocols []string
	Tools     []Tool
}

// Reference is a version-matched, immutable view of mods self-help.
type Reference struct {
	catalog Catalog
}

// NewReference constructs an immutable self-help reference.
func NewReference(catalog Catalog) Reference {
	return Reference{catalog: cloneCatalog(catalog)}
}

// Catalog returns a defensive copy of the factual metadata in this reference.
func (r Reference) Catalog() Catalog {
	return cloneCatalog(r.catalog)
}

func cloneCatalog(catalog Catalog) Catalog {
	cloned := Catalog{
		Flags:     make([]FlagGroup, len(catalog.Flags)),
		Settings:  append([]Setting(nil), catalog.Settings...),
		Providers: append([]Provider(nil), catalog.Providers...),
		Protocols: append([]string(nil), catalog.Protocols...),
		Tools:     append([]Tool(nil), catalog.Tools...),
	}
	for i, group := range catalog.Flags {
		cloned.Flags[i] = FlagGroup{
			Name:  group.Name,
			Flags: append([]Flag(nil), group.Flags...),
		}
	}
	slices.SortFunc(cloned.Settings, func(a, b Setting) int {
		return strings.Compare(a.Path, b.Path)
	})
	slices.SortFunc(cloned.Providers, func(a, b Provider) int {
		return strings.Compare(a.Name, b.Name)
	})
	slices.Sort(cloned.Protocols)
	slices.SortFunc(cloned.Tools, func(a, b Tool) int {
		return strings.Compare(a.Name, b.Name)
	})
	return cloned
}

func Topics() []string {
	return append([]string(nil), topics...)
}

// Lookup renders one topic or the complete reference.
func (r Reference) Lookup(topic string) (string, error) {
	topic = strings.ToLower(strings.TrimSpace(topic))
	if topic == TopicAll {
		sections := make([]string, 0, len(topics)-1)
		for _, sectionTopic := range topics {
			if sectionTopic == TopicAll {
				continue
			}
			section, err := r.Lookup(sectionTopic)
			if err != nil {
				return "", err
			}
			sections = append(sections, section)
		}
		return strings.Join(sections, "\n\n"), nil
	}
	heading := "## " + title(topic)
	if heading == "## " {
		return "", fmt.Errorf("unknown mods help topic %q", topic)
	}
	start := strings.Index(guidance, heading)
	if start < 0 {
		return "", fmt.Errorf("mods help topic %q is unavailable", topic)
	}
	end := strings.Index(guidance[start+len(heading):], "\n## ")
	if end < 0 {
		end = len(guidance)
	} else {
		end += start + len(heading)
	}
	base := strings.TrimSpace(guidance[start:end])
	generated := r.renderCatalog(topic)
	if generated == "" {
		return base, nil
	}
	return base + "\n\n" + generated, nil
}

func (r Reference) renderCatalog(topic string) string {
	switch topic {
	case TopicCLI:
		return renderFlags(r.catalog.Flags)
	case TopicConfig:
		return renderSettings(r.catalog.Settings)
	case TopicProviders:
		return renderProviders(r.catalog.Protocols, r.catalog.Providers)
	case TopicTools:
		return renderTools(r.catalog.Tools)
	default:
		return ""
	}
}

func renderFlags(groups []FlagGroup) string {
	if len(groups) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("### Registered options\n\n")
	for groupIndex, group := range groups {
		if groupIndex > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("#### " + group.Name + "\n\n")
		for _, flag := range group.Flags {
			sb.WriteString("- `")
			if flag.Shorthand != "" {
				sb.WriteString("-" + flag.Shorthand + "`, `")
			}
			sb.WriteString("--" + flag.Name)
			if flag.ValueType != "" {
				sb.WriteString(" <" + flag.ValueType + ">")
			}
			sb.WriteString("` — " + flag.Description)
			if flag.Advanced {
				sb.WriteString(" [advanced]")
			}
			sb.WriteString("\n")
		}
	}
	return strings.TrimSpace(sb.String())
}

func renderSettings(settings []Setting) string {
	if len(settings) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("### Persistent settings\n\n")
	for _, setting := range settings {
		sb.WriteString("- `" + setting.Path + "`")
		if setting.ValueType != "" {
			sb.WriteString(" (" + setting.ValueType + ")")
		}
		sb.WriteString(" — " + setting.Description)
		if setting.Default != "" {
			sb.WriteString(" Default: `" + setting.Default + "`.")
		}
		sb.WriteString("\n")
	}
	return strings.TrimSpace(sb.String())
}

func renderProviders(protocols []string, providers []Provider) string {
	if len(protocols) == 0 && len(providers) == 0 {
		return ""
	}
	var sb strings.Builder
	if len(protocols) > 0 {
		sb.WriteString("### Supported API protocols\n\n")
		for _, protocol := range protocols {
			sb.WriteString("- `" + protocol + "`\n")
		}
	}
	if len(providers) > 0 {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("### Built-in provider metadata\n\n")
		for _, provider := range providers {
			sb.WriteString("- `" + provider.Name + "` — protocol `" + provider.Protocol + "`")
			if provider.Description != "" {
				sb.WriteString("; " + provider.Description)
			}
			if provider.DefaultBaseURL != "" {
				sb.WriteString("; default endpoint `" + provider.DefaultBaseURL + "`")
			}
			if provider.APIKeyEnv != "" {
				sb.WriteString("; credential environment variable `" + provider.APIKeyEnv + "`")
			}
			sb.WriteString("\n")
		}
	}
	return strings.TrimSpace(sb.String())
}

func renderTools(tools []Tool) string {
	if len(tools) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("### Built-in tool catalog\n\n")
	for _, tool := range tools {
		capabilities := make([]string, 0, 4)
		if tool.ReadOnly {
			capabilities = append(capabilities, "read-only")
		}
		if tool.Mutable {
			capabilities = append(capabilities, "mutable")
		}
		if tool.Shell {
			capabilities = append(capabilities, "shell")
		}
		if tool.Interactive {
			capabilities = append(capabilities, "interactive")
		}
		sb.WriteString("- `" + tool.Name + "`")
		if tool.Kind != "" || len(capabilities) > 0 {
			sb.WriteString(" (")
			if tool.Kind != "" {
				sb.WriteString(tool.Kind)
				if len(capabilities) > 0 {
					sb.WriteString("; ")
				}
			}
			sb.WriteString(strings.Join(capabilities, ", "))
			sb.WriteString(")")
		}
		sb.WriteString(" — " + tool.Description + "\n")
	}
	return strings.TrimSpace(sb.String())
}

func title(topic string) string {
	switch topic {
	case TopicOverview:
		return "Overview"
	case TopicCLI:
		return "CLI"
	case TopicConfig:
		return "Config"
	case TopicProviders:
		return "Providers"
	case TopicTools:
		return "Tools"
	case TopicSkills:
		return "Skills"
	case TopicPortable:
		return "Portable"
	case TopicTroubleshooting:
		return "Troubleshooting"
	default:
		return ""
	}
}

func DetectTopic(prompt string) (string, bool) {
	p := strings.ToLower(prompt)
	switch {
	case containsAny(p, "mods.yml", "mods config", "default model", "default-model", "default-api", "配置", "默认模型",
		"配置文件", "config file", "configuration", "setting"):
		return TopicConfig, true
	case containsAny(p, "provider", "api-type", "api key", "模型提供商", "供应商"):
		return TopicProviders, true
	case containsAny(p, "portable", "便携", "绿色版"):
		return TopicPortable, true
	case containsAny(p, "skill", "技能"):
		return TopicSkills, true
	case containsAny(p, "mcp", "filesystem", "shell tool", "web search", "工具"):
		return TopicTools, true
	case containsAny(p, "troubleshoot", "not working", "报错", "故障", "不能用"):
		return TopicTroubleshooting, true
	case strings.Contains(p, "mods --") || strings.Contains(p, " flag") ||
		strings.Contains(p, "参数") || strings.Contains(p, "命令行"):
		return TopicCLI, true
	case strings.Contains(p, "mods"):
		return TopicOverview, true
	default:
		return "", false
	}
}

func IsConfigMutation(prompt string) bool {
	p := strings.ToLower(prompt)
	if _, ok := DetectTopic(p); !ok || !containsAny(p,
		"mods", "mods.yml", "config", "setting", "default-model", "default-api",
		"配置", "设置", "默认模型", "模型提供商") {
		return false
	}
	direct := containsAny(p,
		"change my", "edit my", "update my", "set my", "apply this", "do it",
		"change the config", "edit the config", "update the config", "set the default",
		"帮我", "替我", "直接改", "改成", "设置为", "设为", "写入配置",
		"更新配置", "修改我的")
	if direct {
		return true
	}
	questionOnly := containsAny(p, "how do", "how to", "如何", "怎么", "怎样")
	if questionOnly {
		return false
	}
	return containsAny(p, "modify", "edit", "update", "change", "set ", "add ", "remove ",
		"修改", "编辑", "更新", "添加", "删除")
}

func IsConfigInspection(prompt string) bool {
	p := strings.ToLower(prompt)
	topic, ok := DetectTopic(p)
	if !ok || topic != TopicConfig {
		return false
	}
	return containsAny(p,
		"read my", "check my", "inspect my", "show my", "open my",
		"read the config", "check the config", "inspect the config",
		"查看我的", "检查我的", "读取我的", "打开我的",
		"查看配置", "检查配置", "读取配置")
}

func IsConfigHelpOnly(prompt string) bool {
	p := strings.ToLower(prompt)
	topic, ok := DetectTopic(p)
	if !ok || topic != TopicConfig || IsConfigMutation(p) || IsConfigInspection(p) {
		return false
	}
	return containsAny(p, "how do", "how to", "what is", "如何", "怎么", "怎样", "是什么")
}

func containsAny(value string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(value, term) {
			return true
		}
	}
	return false
}
