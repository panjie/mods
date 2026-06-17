package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	localereader "github.com/mattn/go-localereader"

	"github.com/charmbracelet/mods/internal/proto"
	"github.com/charmbracelet/mods/internal/websearch"
)

const (
	defaultReadLimit     = 20000
	maxReadLimit         = 100000
	defaultSearchResults = 100
	maxSearchResults     = 500
	maxSearchFileBytes   = 1 << 20
	defaultShellTimeout  = 30 * time.Second
	defaultShellOutput   = 20000
)

// FilesystemConfig configures native filesystem tools.
type FilesystemConfig struct {
	Root string
}

// ShellConfig configures the native shell tool.
type ShellConfig struct {
	Root           string
	Timeout        time.Duration
	MaxOutputChars int
}

// RegisterFilesystem registers native filesystem tools.
func RegisterFilesystem(registry *Registry, cfg FilesystemConfig) error {
	root, err := filepath.Abs(cfg.Root)
	if err != nil {
		return err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}

	register := func(tool Tool) error {
		return registry.Register(tool)
	}

	if err := register(Tool{
		Kind: ToolKindBuiltin,
		Spec: proto.ToolSpec{
			Name:        "fs_read_file",
			Description: "Read a UTF-8 text file from the workspace. Use offset and limit to read large files in chunks.",
			InputSchema: objectSchema(map[string]any{
				"path":   stringProp("Path to the file, relative to the workspace root or absolute within it."),
				"offset": integerProp("Zero-based byte offset to start reading from."),
				"limit":  integerProp("Maximum bytes to return."),
			}, "path"),
		},
		Call: func(_ context.Context, data json.RawMessage) (string, error) {
			var args struct {
				Path   string `json:"path"`
				Offset int    `json:"offset"`
				Limit  int    `json:"limit"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			path, err := resolveWorkspacePath(root, args.Path)
			if err != nil {
				return "", err
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return "", err
			}
			if args.Offset < 0 {
				return "", fmt.Errorf("offset must be non-negative")
			}
			if args.Offset > len(content) {
				return "", fmt.Errorf("offset %d is beyond file size %d", args.Offset, len(content))
			}
			limit := args.Limit
			if limit <= 0 {
				limit = defaultReadLimit
			}
			if limit > maxReadLimit {
				limit = maxReadLimit
			}
			end := args.Offset + limit
			if end > len(content) {
				end = len(content)
			}
			out := string(content[args.Offset:end])
			if end < len(content) {
				out += fmt.Sprintf("\n\n[Output truncated. Read from offset %d to continue.]", end)
			}
			return out, nil
		},
	}); err != nil {
		return err
	}

	if err := register(Tool{
		Kind: ToolKindBuiltin,
		Spec: proto.ToolSpec{
			Name:        "fs_write_file",
			Description: "Write a UTF-8 text file inside the workspace, replacing existing content.",
			InputSchema: objectSchema(map[string]any{
				"path":    stringProp("Path to write, relative to the workspace root or absolute within it."),
				"content": stringProp("Complete file content to write."),
			}, "path", "content"),
		},
		Call: func(_ context.Context, data json.RawMessage) (string, error) {
			var args struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			path, err := resolveWorkspacePath(root, args.Path)
			if err != nil {
				return "", err
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return "", err
			}
			if err := os.WriteFile(path, []byte(args.Content), 0o644); err != nil {
				return "", err
			}
			return fmt.Sprintf("Wrote %d bytes to %s", len(args.Content), workspaceRel(root, path)), nil
		},
	}); err != nil {
		return err
	}

	if err := register(Tool{
		Kind: ToolKindBuiltin,
		Spec: proto.ToolSpec{
			Name:        "fs_list_dir",
			Description: "List files and directories in a workspace directory.",
			InputSchema: objectSchema(map[string]any{
				"path":        stringProp("Directory path, relative to the workspace root or absolute within it."),
				"max_entries": integerProp("Maximum entries to return."),
			}, "path"),
		},
		Call: func(_ context.Context, data json.RawMessage) (string, error) {
			var args struct {
				Path       string `json:"path"`
				MaxEntries int    `json:"max_entries"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			path, err := resolveWorkspacePath(root, args.Path)
			if err != nil {
				return "", err
			}
			entries, err := os.ReadDir(path)
			if err != nil {
				return "", err
			}
			limit := args.MaxEntries
			if limit <= 0 || limit > 1000 {
				limit = 1000
			}
			var sb strings.Builder
			for i, entry := range entries {
				if i >= limit {
					sb.WriteString(fmt.Sprintf("[Output truncated. %d entries omitted.]\n", len(entries)-i))
					break
				}
				kind := "file"
				if entry.IsDir() {
					kind = "dir"
				}
				sb.WriteString(fmt.Sprintf("%s\t%s\n", kind, entry.Name()))
			}
			return strings.TrimRight(sb.String(), "\n"), nil
		},
	}); err != nil {
		return err
	}

	if err := register(Tool{
		Kind: ToolKindBuiltin,
		Spec: proto.ToolSpec{
			Name:        "fs_stat",
			Description: "Get metadata for a workspace file or directory.",
			InputSchema: objectSchema(map[string]any{
				"path": stringProp("Path to inspect, relative to the workspace root or absolute within it."),
			}, "path"),
		},
		Call: func(_ context.Context, data json.RawMessage) (string, error) {
			var args struct {
				Path string `json:"path"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			path, err := resolveWorkspacePath(root, args.Path)
			if err != nil {
				return "", err
			}
			info, err := os.Stat(path)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("path: %s\nname: %s\nis_dir: %v\nsize: %d\nmode: %s\nmodified: %s",
				workspaceRel(root, path), info.Name(), info.IsDir(), info.Size(), info.Mode(), info.ModTime().Format(time.RFC3339)), nil
		},
	}); err != nil {
		return err
	}

	if err := register(Tool{
		Kind: ToolKindBuiltin,
		Spec: proto.ToolSpec{
			Name:        "fs_search",
			Description: "Search text files in the workspace for a literal query string.",
			InputSchema: objectSchema(map[string]any{
				"path":        stringProp("Directory or file path to search within."),
				"query":       stringProp("Literal text to search for."),
				"max_results": integerProp("Maximum matching lines to return."),
			}, "path", "query"),
		},
		Call: func(_ context.Context, data json.RawMessage) (string, error) {
			var args struct {
				Path       string `json:"path"`
				Query      string `json:"query"`
				MaxResults int    `json:"max_results"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			if args.Query == "" {
				return "", fmt.Errorf("query is required")
			}
			path, err := resolveWorkspacePath(root, args.Path)
			if err != nil {
				return "", err
			}
			limit := args.MaxResults
			if limit <= 0 {
				limit = defaultSearchResults
			}
			if limit > maxSearchResults {
				limit = maxSearchResults
			}
			return searchFiles(root, path, args.Query, limit)
		},
	}); err != nil {
		return err
	}

	return register(Tool{
		Kind: ToolKindBuiltin,
		Spec: proto.ToolSpec{
			Name:        "fs_apply_patch",
			Description: "Apply a unified diff patch to files inside the workspace.",
			InputSchema: objectSchema(map[string]any{
				"patch": stringProp("Unified diff patch text."),
			}, "patch"),
		},
		Call: func(ctx context.Context, data json.RawMessage) (string, error) {
			var args struct {
				Patch string `json:"patch"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			if strings.TrimSpace(args.Patch) == "" {
				return "", fmt.Errorf("patch is required")
			}
			if err := validatePatchPaths(root, args.Patch); err != nil {
				return "", err
			}
			cmd := exec.CommandContext(ctx, "git", "-c", "core.autocrlf=false", "apply", "--whitespace=nowarn")
			hideCommandWindow(cmd)
			cmd.Dir = root
			cmd.Stdin = strings.NewReader(args.Patch)
			out, err := cmd.CombinedOutput()
			if err != nil {
				return "", fmt.Errorf("git apply failed: %w\n%s", err, strings.TrimSpace(string(out)))
			}
			return "Patch applied", nil
		},
	})
}

// RegisterWebSearch registers the native web search tool.
func RegisterWebSearch(registry *Registry, cfg websearch.Config) error {
	return registry.Register(Tool{
		Kind: ToolKindBuiltin,
		Spec: proto.ToolSpec{
			Name:        "web_search",
			Description: "Search the web for current, up-to-date information. Returns formatted search results with titles, URLs, and snippets.",
			InputSchema: objectSchema(map[string]any{
				"query": stringProp("The search query to look up on the web."),
			}, "query"),
		},
		Call: func(ctx context.Context, data json.RawMessage) (string, error) {
			var args struct {
				Query string `json:"query"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			if args.Query == "" {
				return "", fmt.Errorf("websearch: empty search query")
			}
			return websearch.Search(ctx, cfg, args.Query)
		},
	})
}

// RegisterShell registers the native shell tool.
func RegisterShell(registry *Registry, cfg ShellConfig) error {
	root, err := filepath.Abs(cfg.Root)
	if err != nil {
		return err
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultShellTimeout
	}
	if cfg.MaxOutputChars <= 0 {
		cfg.MaxOutputChars = defaultShellOutput
	}
	desc := PosixShellRunDescription
	if runtime.GOOS == "windows" {
		desc = WindowsShellRunDescription
	}
	return registry.Register(Tool{
		Kind:          ToolKindShell,
		TimeoutPolicy: TimeoutPolicySelf,
		Spec: proto.ToolSpec{
			Name:        "shell_run",
			Description: desc,
			InputSchema: objectSchema(map[string]any{
				"command": stringProp("Shell command to run."),
			}, "command"),
		},
		Call: func(ctx context.Context, data json.RawMessage) (string, error) {
			var args struct {
				Command string `json:"command"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			return runShellCommand(ctx, cfg, root, args.Command, shellCommand)
		},
	})
}

// RegisterPowerShell registers the native PowerShell tool.
func RegisterPowerShell(registry *Registry, cfg ShellConfig) error {
	root, err := filepath.Abs(cfg.Root)
	if err != nil {
		return err
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultShellTimeout
	}
	if cfg.MaxOutputChars <= 0 {
		cfg.MaxOutputChars = defaultShellOutput
	}
	return registry.Register(Tool{
		Kind:          ToolKindShell,
		TimeoutPolicy: TimeoutPolicySelf,
		Spec: proto.ToolSpec{
			Name:        "powershell_run",
			Description: PowerShellRunDescription,
			InputSchema: objectSchema(map[string]any{
				"command": stringProp("PowerShell command to run directly."),
			}, "command"),
		},
		Call: func(ctx context.Context, data json.RawMessage) (string, error) {
			var args struct {
				Command string `json:"command"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			return runShellCommand(ctx, cfg, root, args.Command, powerShellCommand)
		},
	})
}

// RegisterThinking registers a lightweight sequential thinking note tool.
func RegisterThinking(registry *Registry) error {
	return registry.Register(Tool{
		Kind: ToolKindBuiltin,
		Spec: proto.ToolSpec{
			Name:        "thinking_note",
			Description: "Record one concise reasoning step, next step, and whether the task is complete.",
			InputSchema: objectSchema(map[string]any{
				"thought":   stringProp("Current concise reasoning step."),
				"next_step": stringProp("The next action to take."),
				"done": map[string]any{
					"type":        "boolean",
					"description": "Whether the reasoning process is complete.",
				},
			}, "thought"),
		},
		Call: func(_ context.Context, data json.RawMessage) (string, error) {
			var args struct {
				Thought  string `json:"thought"`
				NextStep string `json:"next_step"`
				Done     bool   `json:"done"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			return fmt.Sprintf("thought: %s\nnext_step: %s\ndone: %v", args.Thought, args.NextStep, args.Done), nil
		},
	})
}

func decodeArgs(data json.RawMessage, args any) error {
	if len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, args); err != nil {
		return fmt.Errorf("invalid tool arguments: %w: %s", err, string(data))
	}
	return nil
}

func objectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func stringProp(description string) map[string]any {
	return map[string]any{
		"type":        "string",
		"description": description,
	}
}

func integerProp(description string) map[string]any {
	return map[string]any{
		"type":        "integer",
		"description": description,
	}
}

func resolveWorkspacePath(root, input string) (string, error) {
	if input == "" {
		return "", fmt.Errorf("path is required")
	}
	path := input
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path = filepath.Clean(path)
	if err := ensureInsideRoot(root, path); err != nil {
		return "", err
	}

	existing := path
	var missing []string
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		} else if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", fmt.Errorf("could not find existing parent for %q", path)
		}
		missing = append([]string{filepath.Base(existing)}, missing...)
		existing = parent
	}

	existingEval, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", err
	}
	parts := append([]string{existingEval}, missing...)
	resolved := filepath.Join(parts...)
	if err := ensureInsideRoot(root, resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

func ensureInsideRoot(root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	if rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." && !filepath.IsAbs(rel)) {
		return nil
	}
	return fmt.Errorf("path %q is outside workspace root; use shell_run to access paths outside the workspace", path)
}

func workspaceRel(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}

func searchFiles(root, path, query string, limit int) (string, error) {
	var sb strings.Builder
	matches := 0
	err := filepath.WalkDir(path, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if matches >= limit {
			return fs.SkipAll
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor":
				return fs.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > maxSearchFileBytes {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.IndexByte(content, 0) >= 0 {
			return nil
		}
		lines := strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n")
		for i, line := range lines {
			if strings.Contains(line, query) {
				sb.WriteString(fmt.Sprintf("%s:%d:%s\n", workspaceRel(root, path), i+1, line))
				matches++
				if matches >= limit {
					return fs.SkipAll
				}
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if matches == 0 {
		return "No matches found", nil
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

type ShellExitError struct {
	Code int
}

func (e ShellExitError) Error() string {
	return fmt.Sprintf("command exited with status %d", e.Code)
}

type ShellRunner struct {
	Root           string
	Timeout        time.Duration
	MaxOutputChars int
	BuildCommand   func(context.Context, string) *exec.Cmd
}

func (r ShellRunner) Run(ctx context.Context, command string) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", fmt.Errorf("command is required")
	}
	if r.Timeout <= 0 {
		r.Timeout = defaultShellTimeout
	}
	if r.MaxOutputChars <= 0 {
		r.MaxOutputChars = defaultShellOutput
	}
	if r.BuildCommand == nil {
		return "", fmt.Errorf("shell runner command builder is required")
	}
	runCtx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	cmd := r.BuildCommand(runCtx, command)
	cmd.Dir = r.Root
	out := newCappedOutput(r.MaxOutputChars)
	cmd.Stdout = out
	cmd.Stderr = out
	err := cmd.Run()
	text := out.String()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			text = appendExitStatus(text, exitErr.ExitCode())
			return text, ShellExitError{Code: exitErr.ExitCode()}
		}
		return text, err
	}
	return text, nil
}

func runShellCommand(ctx context.Context, cfg ShellConfig, root, command string, buildCmd func(context.Context, string) *exec.Cmd) (string, error) {
	return ShellRunner{
		Root:           root,
		Timeout:        cfg.Timeout,
		MaxOutputChars: cfg.MaxOutputChars,
		BuildCommand:   buildCmd,
	}.Run(ctx, command)
}

type cappedOutput struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func newCappedOutput(limit int) *cappedOutput {
	if limit <= 0 {
		limit = defaultShellOutput
	}
	return &cappedOutput{limit: limit}
}

func (w *cappedOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	remaining := w.limit - w.buf.Len()
	if remaining > 0 {
		if len(p) > remaining {
			w.buf.Write(p[:remaining])
			w.truncated = true
		} else {
			w.buf.Write(p)
		}
	} else if len(p) > 0 {
		w.truncated = true
	}
	return len(p), nil
}

func (w *cappedOutput) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := w.buf.Bytes()
	if decoded, decErr := localereader.UTF8(out); decErr == nil {
		out = decoded
	}
	text := string(out)
	if w.truncated {
		return text + fmt.Sprintf("\n\n[Output truncated at %d chars.]", w.limit)
	}
	return text
}

func appendExitStatus(text string, code int) string {
	if text != "" {
		return text + fmt.Sprintf("\n[exit status %d]", code)
	}
	return fmt.Sprintf("[exit status %d]", code)
}

func validatePatchPaths(root, patch string) error {
	for _, line := range strings.Split(strings.ReplaceAll(patch, "\r\n", "\n"), "\n") {
		if !strings.HasPrefix(line, "+++ ") && !strings.HasPrefix(line, "--- ") {
			continue
		}
		path := strings.TrimSpace(line[4:])
		if path == "/dev/null" {
			continue
		}
		fields := strings.Fields(path)
		if len(fields) == 0 {
			return fmt.Errorf("patch contains an empty file path")
		}
		path = fields[0]
		if strings.HasPrefix(path, "a/") || strings.HasPrefix(path, "b/") {
			path = path[2:]
		}
		if filepath.IsAbs(path) || strings.HasPrefix(filepath.Clean(path), "..") {
			return fmt.Errorf("patch path %q is outside workspace root", path)
		}
		if _, err := resolveWorkspacePath(root, path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func shellCommand(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		cmd := exec.CommandContext(ctx, "cmd", "/D", "/C", command)
		hideCommandWindow(cmd)
		return cmd
	}
	return exec.CommandContext(ctx, "sh", "-c", command)
}

func powerShellCommand(ctx context.Context, command string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", command)
	hideCommandWindow(cmd)
	return cmd
}

func truncateOutput(out string, limit int) string {
	if len(out) <= limit {
		return out
	}
	return out[:limit] + fmt.Sprintf("\n\n[Output truncated at %d chars.]", limit)
}
