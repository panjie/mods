package ui

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var tokenErrRe = regexp.MustCompile(`This model's maximum context length is (\d+) tokens. However, your messages resulted in (\d+) tokens`)

func CutPrompt(msg, prompt string) string {
	found := tokenErrRe.FindStringSubmatch(msg)
	if len(found) != 3 { //nolint:mnd
		return prompt
	}

	maxt, _ := strconv.Atoi(found[1])
	current, _ := strconv.Atoi(found[2])

	if maxt > current {
		return prompt
	}

	// 1 token =~ 4 chars
	// cut 10 extra chars 'just in case'
	reduceBy := 10 + (current-maxt)*4 //nolint:mnd
	if len(prompt) > reduceBy {
		return prompt[:len(prompt)-reduceBy]
	}

	return prompt
}

func IncreaseIndent(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i := range lines {
		lines[i] = "\t" + lines[i]
	}
	return strings.Join(lines, "\n")
}

func ToolOperationLabel(name string, data []byte, width int) string {
	args := ToolOperationArgs(data)
	switch name {
	case "web_search":
		if query := OneLinePreview(ArgString(args, "query")); query != "" {
			return TruncateOperationStatus("Searching web: "+query, width)
		}
	case "shell_run":
		if command := OneLinePreview(ArgString(args, "command")); command != "" {
			return TruncateOperationStatus("Running command: "+command, width)
		}
	case "powershell_run":
		if command := OneLinePreview(ArgString(args, "command")); command != "" {
			return TruncateOperationStatus("Running PowerShell: "+command, width)
		}
	case "fs_read_file":
		if path := OneLinePreview(ArgString(args, "path")); path != "" {
			return TruncateOperationStatus("Reading file: "+path, width)
		}
	case "fs_write_file":
		if path := OneLinePreview(ArgString(args, "path")); path != "" {
			return TruncateOperationStatus("Writing file: "+path, width)
		}
	case "fs_list_dir":
		if path := OneLinePreview(ArgString(args, "path")); path != "" {
			return TruncateOperationStatus("Listing directory: "+path, width)
		}
	case "fs_stat":
		if path := OneLinePreview(ArgString(args, "path")); path != "" {
			return TruncateOperationStatus("Inspecting path: "+path, width)
		}
	case "fs_search":
		query := OneLinePreview(ArgString(args, "query"))
		path := OneLinePreview(ArgString(args, "path"))
		switch {
		case query != "" && path != "":
			return TruncateOperationStatus("Searching files: "+query+" in "+path, width)
		case query != "":
			return TruncateOperationStatus("Searching files: "+query, width)
		case path != "":
			return TruncateOperationStatus("Searching files in: "+path, width)
		}
	case "fs_apply_patch":
		return TruncateOperationStatus("Applying patch", width)
	case "thinking_note":
		if thought := OneLinePreview(ArgString(args, "thought")); thought != "" {
			return TruncateOperationStatus("Thinking: "+thought, width)
		}
		return TruncateOperationStatus("Thinking note", width)
	}
	if summary := ToolArgsSummary(args); summary != "" {
		return TruncateOperationStatus("Running tool: "+name+" ("+summary+")", width)
	}
	return TruncateOperationStatus("Running tool: "+name, width)
}

func ToolOperationArgs(data []byte) map[string]any {
	var args map[string]any
	if err := json.Unmarshal(data, &args); err != nil {
		return nil
	}
	return args
}

func ToolArgsSummary(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	preferred := []string{"query", "command", "path", "url", "repo", "repository", "file", "filename", "name"}
	parts := make([]string, 0, 3)
	seen := map[string]bool{}
	for _, key := range preferred {
		if appendToolArgSummaryPart(&parts, seen, args, key) && len(parts) >= 3 {
			return strings.Join(parts, ", ")
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, ", ")
	}
	for key := range args {
		if appendToolArgSummaryPart(&parts, seen, args, key) && len(parts) >= 3 {
			break
		}
	}
	return strings.Join(parts, ", ")
}

func appendToolArgSummaryPart(parts *[]string, seen map[string]bool, args map[string]any, key string) bool {
	if seen[key] {
		return false
	}
	value := OneLinePreview(ArgString(args, key))
	if value == "" {
		return false
	}
	seen[key] = true
	*parts = append(*parts, key+"="+value)
	return true
}

func ArgString(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	value, ok := args[key]
	if !ok {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case float64, bool:
		return fmt.Sprint(v)
	case []any:
		values := make([]string, 0, len(v))
		for _, item := range v {
			s := OneLinePreview(fmt.Sprint(item))
			if s != "" {
				values = append(values, s)
			}
		}
		return strings.Join(values, ",")
	default:
		return ""
	}
}

func OneLinePreview(s string) string {
	s = strings.ReplaceAll(s, "\r", "\n")
	s = FirstLine(strings.TrimSpace(s))
	return strings.Join(strings.Fields(s), " ")
}

func FirstLine(s string) string {
	first, _, _ := strings.Cut(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	return first
}

func TruncateOperationStatus(s string, width int) string {
	s = strings.TrimSpace(s)
	if width <= 0 || width > 120 {
		width = 120
	}
	runes := []rune(s)
	if len(runes) <= width {
		return s
	}
	if width <= 3 {
		return string(runes[:width])
	}
	return string(runes[:width-3]) + "..."
}

// if the input is whitespace only, make it empty.
func RemoveWhitespace(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return s
}
