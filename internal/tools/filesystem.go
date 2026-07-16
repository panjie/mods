package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/panjie/mods/internal/debug"
	"github.com/panjie/mods/internal/proto"
)

const (
	defaultReadLimit      = 20000
	maxReadLimit          = 100000
	defaultReadLines      = 2000
	maxReadLines          = 10000
	defaultSearchResults  = 100
	maxSearchResults      = 500
	maxSearchFileBytes    = 1 << 20
	defaultLargestResults = 10
	maxLargestResults     = 100
)

// FilesystemConfig configures native filesystem tools.
type FilesystemConfig struct {
	Root     string
	SafeDirs []string
}

type largestEntry struct {
	path string
	kind string
	size int64
}

// RegisterFilesystem registers native filesystem tools.
func RegisterFilesystem(registry *Registry, cfg FilesystemConfig) error {
	root, err := filepath.Abs(cfg.Root)
	if err != nil {
		return err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			debug.Printf("RegisterFilesystem: workspace %q does not exist, skipping filesystem tools", root)
			return nil
		}
		return err
	}
	safeDirs := cfg.SafeDirs
	tools := []Tool{
		filesystemReadFileTool(root, safeDirs),
		filesystemWriteFileTool(root, safeDirs),
		filesystemReplaceTool(root, safeDirs),
		filesystemListDirTool(root, safeDirs),
		filesystemStatTool(root, safeDirs),
		filesystemSearchTool(root, safeDirs),
		filesystemLargestTool(root, safeDirs),
		filesystemDeleteFileTool(root, safeDirs),
		filesystemDeleteDirTool(root, safeDirs),
		filesystemMoveTool(root, safeDirs),
		filesystemCopyTool(root, safeDirs),
		filesystemMkdirTool(root, safeDirs),
		filesystemApplyPatchTool(root, safeDirs),
	}
	for _, t := range tools {
		if err := registry.Register(t); err != nil {
			return err
		}
	}
	return nil
}

func filesystemReadFileTool(root string, safeDirs []string) Tool {
	return Tool{
		Kind:            ToolKindBuiltin,
		Capabilities:    ToolCapabilities{ReadOnly: true},
		IntentExtractor: pathParentIntent(root, true),
		Spec: proto.ToolSpec{
			Name:        "fs_read_file",
			Description: "Read a UTF-8 text file. Workspace-relative paths are resolved inside the workspace; absolute or home-directory paths outside it require approval. Read by line number with start_line/end_line (1-based, inclusive; output is line-numbered); page large files by bytes with offset/limit.",
			InputSchema: objectSchema(map[string]any{
				"path":       stringProp("Path to the file, relative to the workspace, absolute, or using the current user's home directory."),
				"offset":     integerProp("Zero-based byte offset to start reading from (byte mode)."),
				"limit":      integerProp("Maximum bytes to return (byte mode)."),
				"start_line": integerProp("1-based first line to return (line mode). When set, reads by line number instead of byte offset."),
				"end_line":   integerProp("1-based last line to return, inclusive (line mode). Defaults to start_line + 2000."),
			}, "path"),
		},
		Call: func(ctx context.Context, data json.RawMessage) (string, error) {
			var args struct {
				Path      string `json:"path"`
				Offset    int    `json:"offset"`
				Limit     int    `json:"limit"`
				StartLine int    `json:"start_line"`
				EndLine   int    `json:"end_line"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			path, err := resolveWorkspacePath(ctx, root, args.Path, safeDirs)
			if err != nil {
				return "", err
			}
			info, err := os.Stat(path)
			if err != nil {
				return "", err
			}
			if args.StartLine > 0 {
				return readByLines(path, args.StartLine, args.EndLine)
			}
			if args.Offset < 0 {
				return "", fmt.Errorf("offset must be non-negative")
			}
			if int64(args.Offset) > info.Size() {
				return "", fmt.Errorf("offset %d is beyond file size %d", args.Offset, info.Size())
			}
			limit := args.Limit
			if limit <= 0 {
				limit = defaultReadLimit
			}
			if limit > maxReadLimit {
				limit = maxReadLimit
			}
			file, err := os.Open(path)
			if err != nil {
				return "", err
			}
			defer file.Close() //nolint:errcheck
			if _, err := file.Seek(int64(args.Offset), io.SeekStart); err != nil {
				return "", err
			}
			content, err := io.ReadAll(io.LimitReader(file, int64(limit)))
			if err != nil {
				return "", err
			}
			end := args.Offset + len(content)
			out := string(content)
			if int64(end) < info.Size() {
				out += fmt.Sprintf("\n\n[Output truncated. Read from offset %d to continue.]", end)
			}
			return out, nil
		},
	}
}

func filesystemListDirTool(root string, safeDirs []string) Tool {
	return Tool{
		Kind:            ToolKindBuiltin,
		Capabilities:    ToolCapabilities{ReadOnly: true},
		IntentExtractor: pathParentIntent(root, true),
		Spec: proto.ToolSpec{
			Name:        "fs_list_dir",
			Description: "List files and directories. Workspace-relative paths are resolved inside the workspace; absolute or home-directory paths outside it require approval.",
			InputSchema: objectSchema(map[string]any{
				"path":        stringProp("Directory path, relative to the workspace, absolute, or using the current user's home directory."),
				"max_entries": integerProp("Maximum entries to return."),
			}, "path"),
		},
		Call: func(ctx context.Context, data json.RawMessage) (string, error) {
			var args struct {
				Path       string `json:"path"`
				MaxEntries int    `json:"max_entries"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			path, err := resolveWorkspacePath(ctx, root, args.Path, safeDirs)
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
	}
}

func filesystemStatTool(root string, safeDirs []string) Tool {
	return Tool{
		Kind:            ToolKindBuiltin,
		Capabilities:    ToolCapabilities{ReadOnly: true},
		IntentExtractor: pathParentIntent(root, true),
		Spec: proto.ToolSpec{
			Name:        "fs_stat",
			Description: "Get metadata for a file or directory. Workspace-relative paths are resolved inside the workspace; absolute or home-directory paths outside it require approval.",
			InputSchema: objectSchema(map[string]any{
				"path": stringProp("Path to inspect, relative to the workspace, absolute, or using the current user's home directory."),
			}, "path"),
		},
		Call: func(ctx context.Context, data json.RawMessage) (string, error) {
			var args struct {
				Path string `json:"path"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			path, err := resolveWorkspacePath(ctx, root, args.Path, safeDirs)
			if err != nil {
				return "", err
			}
			info, err := os.Stat(path)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("path: %s\nname: %s\nis_dir: %v\nsize: %d\nmode: %s\nmodified: %s",
				displayPath(root, path), info.Name(), info.IsDir(), info.Size(), info.Mode(), info.ModTime().Format(time.RFC3339)), nil
		},
	}
}

func filesystemSearchTool(root string, safeDirs []string) Tool {
	return Tool{
		Kind:            ToolKindBuiltin,
		Capabilities:    ToolCapabilities{ReadOnly: true},
		IntentExtractor: pathParentIntent(root, true),
		Spec: proto.ToolSpec{
			Name:        "fs_search",
			Description: "Search text files for a literal query string. Workspace-relative paths are resolved inside the workspace; absolute or home-directory paths outside it require approval.",
			InputSchema: objectSchema(map[string]any{
				"path":        stringProp("Directory or file path to search within, relative to the workspace, absolute, or using the current user's home directory."),
				"query":       stringProp("Literal text to search for."),
				"max_results": integerProp("Maximum matching lines to return."),
			}, "path", "query"),
		},
		Call: func(ctx context.Context, data json.RawMessage) (string, error) {
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
			path, err := resolveWorkspacePath(ctx, root, args.Path, safeDirs)
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
			return searchFiles(ctx, root, path, args.Query, limit)
		},
	}
}

func filesystemLargestTool(root string, safeDirs []string) Tool {
	return Tool{
		Kind:            ToolKindBuiltin,
		Capabilities:    ToolCapabilities{ReadOnly: true},
		IntentExtractor: pathParentIntent(root, true),
		Spec: proto.ToolSpec{
			Name:        "fs_largest",
			Description: "Find the largest files or directories under a path. Use kind=file for requests that specifically ask for files. Workspace-relative paths are resolved inside the workspace; absolute or home-directory paths outside it require approval.",
			InputSchema: objectSchema(map[string]any{
				"path":        stringProp("Directory or file path to inspect, relative to the workspace, absolute, or using the current user's home directory."),
				"kind":        stringProp("What to rank: file, dir, or both. Defaults to file."),
				"max_results": integerProp("Maximum entries to return, up to 100. Defaults to 10."),
				"max_depth":   integerProp("Maximum directory depth to descend from path. Omit or use a negative value for unlimited depth."),
			}, "path"),
		},
		Call: func(ctx context.Context, data json.RawMessage) (string, error) {
			var args struct {
				Path       string `json:"path"`
				Kind       string `json:"kind"`
				MaxResults int    `json:"max_results"`
				MaxDepth   *int   `json:"max_depth"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			path, err := resolveWorkspacePath(ctx, root, args.Path, safeDirs)
			if err != nil {
				return "", err
			}
			limit := args.MaxResults
			if limit <= 0 {
				limit = defaultLargestResults
			}
			if limit > maxLargestResults {
				limit = maxLargestResults
			}
			maxDepth := -1
			if args.MaxDepth != nil {
				maxDepth = *args.MaxDepth
			}
			return largestPaths(ctx, root, path, args.Kind, limit, maxDepth)
		},
	}
}

// readByLines returns the 1-based, inclusive line range [startLine, endLine]
// of the file at path, each line prefixed with its number (like fs_search).
// When endLine <= 0 it defaults to startLine + defaultReadLines - 1. Output is
// capped at maxReadLines lines. A startLine past the end of the file is an
// error; lines beyond EOF are simply omitted.
func readByLines(path string, startLine, endLine int) (string, error) {
	if startLine < 1 {
		return "", fmt.Errorf("start_line must be >= 1")
	}
	if endLine > 0 && endLine < startLine {
		return "", fmt.Errorf("end_line must be >= start_line")
	}
	last := endLine
	if last <= 0 {
		last = startLine + defaultReadLines - 1
	}
	if last-startLine+1 > maxReadLines {
		last = startLine + maxReadLines - 1
	}
	// honoredUserEnd is true when the caller set end_line and it was honored
	// exactly (not reduced by the maxReadLines cap). Reaching last then is
	// intentional, not a truncation worth flagging.
	honoredUserEnd := endLine > 0 && endLine == last
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close() //nolint:errcheck
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), maxSearchFileBytes)
	var sb strings.Builder
	lineNo, returned := 0, 0
	stoppedByLimit := false
	for scanner.Scan() {
		lineNo++
		if lineNo < startLine {
			continue
		}
		if lineNo > last {
			stoppedByLimit = true
			break
		}
		fmt.Fprintf(&sb, "%d: %s\n", lineNo, scanner.Text())
		returned++
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if returned == 0 {
		if lineNo >= 1 && startLine > lineNo {
			return "", fmt.Errorf("start_line %d is beyond file length %d", startLine, lineNo)
		}
		return "", nil
	}
	out := strings.TrimRight(sb.String(), "\n")
	if stoppedByLimit && !honoredUserEnd {
		out += fmt.Sprintf("\n\n[Output truncated. File has more lines; raise end_line to continue from line %d.]", last+1)
	}
	return out, nil
}

func searchFiles(ctx context.Context, root, path, query string, limit int) (string, error) {
	var sb strings.Builder
	matches := 0
	err := filepath.WalkDir(path, func(path string, d fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
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
		// Skip symlinks and other non-regular files: os.Open below follows
		// symlinks, which would let an in-workspace link read its target
		// outside the workspace and bypass the boundary check applied to the
		// search root. Mirrors the guard in largestPaths.
		if !info.Mode().IsRegular() {
			return nil
		}
		if info.Size() > maxSearchFileBytes {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close() //nolint:errcheck
		reader := bufio.NewReader(file)
		lineNo := 0
		for {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			line, readErr := reader.ReadString('\n')
			if len(line) > 0 {
				lineNo++
				if strings.Contains(line, "\x00") {
					return nil
				}
				line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
				if strings.Contains(line, query) {
					sb.WriteString(fmt.Sprintf("%s:%d:%s\n", displayPath(root, path), lineNo, line))
					matches++
					if matches >= limit {
						return fs.SkipAll
					}
				}
			}
			if errors.Is(readErr, io.EOF) {
				break
			}
			if readErr != nil {
				return readErr
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

func largestPaths(ctx context.Context, root, path, kind string, limit, maxDepth int) (string, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		kind = "file"
	}
	wantFiles := kind == "file" || kind == "both"
	wantDirs := kind == "dir" || kind == "both"
	if !wantFiles && !wantDirs {
		return "", fmt.Errorf("kind must be file, dir, or both")
	}

	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		if wantFiles && info.Mode().IsRegular() {
			return formatLargestEntries(root, []largestEntry{{path: path, kind: "file", size: info.Size()}}, limit, 0), nil
		}
		return "No matching entries found", nil
	}

	var entries []largestEntry
	dirSizes := map[string]int64{}
	seenDirs := map[string]struct{}{}
	var skipped []string
	skippedCount := 0

	err = filepath.WalkDir(path, func(current string, d fs.DirEntry, walkErr error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if walkErr != nil {
			skippedCount++
			if len(skipped) < 3 {
				skipped = append(skipped, fmt.Sprintf("%s: %v", displayPath(root, current), walkErr))
			}
			return nil
		}
		if d.IsDir() {
			if current != path && maxDepth >= 0 && relativeDepth(path, current) > maxDepth {
				return fs.SkipDir
			}
			if wantDirs && current != path {
				seenDirs[current] = struct{}{}
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			skippedCount++
			if len(skipped) < 3 {
				skipped = append(skipped, fmt.Sprintf("%s: %v", displayPath(root, current), err))
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if wantFiles {
			entries = append(entries, largestEntry{path: current, kind: "file", size: info.Size()})
		}
		for dir := filepath.Dir(current); contains(path, dir); dir = filepath.Dir(dir) {
			dirSizes[dir] += info.Size()
			if dir == path {
				break
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if wantDirs {
		for dir := range seenDirs {
			entries = append(entries, largestEntry{path: dir, kind: "dir", size: dirSizes[dir]})
		}
	}
	return formatLargestEntries(root, entries, limit, skippedCount, skipped...), nil
}

func formatLargestEntries(root string, entries []largestEntry, limit, skippedCount int, skipped ...string) string {
	if len(entries) == 0 {
		out := "No matching entries found"
		if skippedCount > 0 {
			out += fmt.Sprintf("\n[Skipped %d paths due to errors.]", skippedCount)
		}
		return out
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].size != entries[j].size {
			return entries[i].size > entries[j].size
		}
		if entries[i].kind != entries[j].kind {
			return entries[i].kind < entries[j].kind
		}
		return entries[i].path < entries[j].path
	})
	if limit > len(entries) {
		limit = len(entries)
	}
	var sb strings.Builder
	sb.WriteString("size_bytes\tkind\tpath\n")
	for i := 0; i < limit; i++ {
		entry := entries[i]
		sb.WriteString(fmt.Sprintf("%d\t%s\t%s\n", entry.size, entry.kind, displayPath(root, entry.path)))
	}
	if len(entries) > limit {
		sb.WriteString(fmt.Sprintf("[Output truncated. %d entries omitted.]\n", len(entries)-limit))
	}
	if skippedCount > 0 {
		sb.WriteString(fmt.Sprintf("[Skipped %d paths due to errors.]\n", skippedCount))
		for _, item := range skipped {
			sb.WriteString("[Skipped] " + item + "\n")
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

func relativeDepth(base, path string) int {
	rel, err := filepath.Rel(base, path)
	if err != nil || rel == "." {
		return 0
	}
	return len(strings.Split(filepath.Clean(rel), string(os.PathSeparator)))
}
