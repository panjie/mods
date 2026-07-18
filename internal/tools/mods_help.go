package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/selfhelp"
)

const ModsHelpToolName = "mods_help"

type ModsHelpConfig struct {
	SettingsPath   string
	Portable       bool
	FilesystemMode string
}

func RegisterModsHelp(registry *Registry, cfg ModsHelpConfig) error {
	return registry.Register(Tool{
		Kind:         ToolKindBuiltin,
		Capabilities: ToolCapabilities{ReadOnly: true},
		Spec: proto.ToolSpec{
			Name:        ModsHelpToolName,
			Description: "Load version-matched help for mods itself. Call this before answering questions about mods usage, CLI flags, configuration, providers, tools, skills, portable mode, or troubleshooting. The config topic reports the active config path and safe file-edit workflow.",
			InputSchema: objectSchema(map[string]any{
				"topic": map[string]any{
					"type":        "string",
					"enum":        selfhelp.Topics(),
					"description": "Help topic to load. Use all only when several topics are required.",
				},
			}, "topic"),
		},
		Call: func(_ context.Context, data json.RawMessage) (string, error) {
			var args struct {
				Topic string `json:"topic"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			content, err := selfhelp.Lookup(args.Topic)
			if err != nil {
				return "", err
			}
			return formatModsHelp(content, cfg), nil
		},
	})
}

func formatModsHelp(content string, cfg ModsHelpConfig) string {
	path := cfg.SettingsPath
	if strings.TrimSpace(path) == "" {
		path = "(unavailable)"
	}
	portable := "inactive"
	if cfg.Portable {
		portable = "active"
	}
	mode := cfg.FilesystemMode
	if mode == "" {
		mode = "auto"
	}
	return fmt.Sprintf(
		"Active config path: %s\nPortable mode: %s\nFilesystem tools: %s\n"+
			"Config file changes take effect on the next mods invocation.\n\n%s",
		path, portable, mode, content,
	)
}
