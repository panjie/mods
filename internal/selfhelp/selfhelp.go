package selfhelp

import (
	_ "embed"
	"fmt"
	"strings"
)

//go:embed reference.md
var Reference string

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

func Topics() []string {
	return append([]string(nil), topics...)
}

func Lookup(topic string) (string, error) {
	topic = strings.ToLower(strings.TrimSpace(topic))
	if topic == TopicAll {
		return strings.TrimSpace(Reference), nil
	}
	heading := "## " + title(topic)
	if heading == "## " {
		return "", fmt.Errorf("unknown mods help topic %q", topic)
	}
	start := strings.Index(Reference, heading)
	if start < 0 {
		return "", fmt.Errorf("mods help topic %q is unavailable", topic)
	}
	end := strings.Index(Reference[start+len(heading):], "\n## ")
	if end < 0 {
		return strings.TrimSpace(Reference[start:]), nil
	}
	end += start + len(heading)
	return strings.TrimSpace(Reference[start:end]), nil
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
