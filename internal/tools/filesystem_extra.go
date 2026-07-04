package tools

import (
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

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/pathutil"
	"github.com/panjie/mods/internal/proto"
)

const (
	defaultLargestResults = 10
	maxLargestResults     = 100
)

type largestEntry struct {
	path string
	kind string
	size int64
}

func pathSelfIntent(root string, class approval.AccessClass) func(json.RawMessage) approval.AccessIntent {
	return func(data json.RawMessage) approval.AccessIntent {
		var args struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(data, &args)
		if args.Path == "" {
			return approval.AccessIntent{Class: class}
		}
		path := pathutil.NormalizePath(args.Path, pathutil.DefaultOptions(root, pathutil.FlavorPOSIX))
		return approval.AccessIntent{Class: class, Dirs: []string{path}}
	}
}

func filesystemCopyIntent(root string) func(json.RawMessage) approval.AccessIntent {
	return func(data json.RawMessage) approval.AccessIntent {
		var args struct {
			SourcePath string `json:"source_path"`
			DestPath   string `json:"dest_path"`
		}
		_ = json.Unmarshal(data, &args)
		dirs := make([]string, 0, 2)
		seen := map[string]struct{}{}
		addIntentDir(&dirs, seen, sourceIntentDir(root, args.SourcePath))
		addIntentDir(&dirs, seen, destIntentDir(root, args.SourcePath, args.DestPath))
		return approval.AccessIntent{Class: approval.AccessWrite, Dirs: dirs}
	}
}

func filesystemMoveIntent(root string) func(json.RawMessage) approval.AccessIntent {
	return func(data json.RawMessage) approval.AccessIntent {
		var args struct {
			SourcePath string `json:"source_path"`
			DestPath   string `json:"dest_path"`
		}
		_ = json.Unmarshal(data, &args)
		dirs := make([]string, 0, 2)
		seen := map[string]struct{}{}
		addIntentDir(&dirs, seen, sourceIntentDir(root, args.SourcePath))
		addIntentDir(&dirs, seen, destIntentDir(root, args.SourcePath, args.DestPath))
		return approval.AccessIntent{Class: approval.AccessWrite, Dirs: dirs}
	}
}

func addIntentDir(dirs *[]string, seen map[string]struct{}, dir string) {
	if dir == "" {
		return
	}
	if _, ok := seen[dir]; ok {
		return
	}
	seen[dir] = struct{}{}
	*dirs = append(*dirs, dir)
}

func sourceIntentDir(root, input string) string {
	if input == "" {
		return ""
	}
	path := pathutil.NormalizePath(input, pathutil.DefaultOptions(root, pathutil.FlavorPOSIX))
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return path
	}
	return pathutil.ParentDir(path)
}

func destIntentDir(root, source, dest string) string {
	if dest == "" {
		return ""
	}
	sourcePath := ""
	if source != "" {
		sourcePath = pathutil.NormalizePath(source, pathutil.DefaultOptions(root, pathutil.FlavorPOSIX))
	}
	destPath := pathutil.NormalizePath(dest, pathutil.DefaultOptions(root, pathutil.FlavorPOSIX))
	if info, err := os.Stat(destPath); err == nil && info.IsDir() {
		return destPath
	}
	if sourcePath != "" {
		if info, err := os.Stat(sourcePath); err == nil && info.IsDir() {
			return destPath
		}
	}
	return pathutil.ParentDir(destPath)
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

func filesystemDeleteFileTool(root string, safeDirs []string) Tool {
	return Tool{
		Kind:            ToolKindBuiltin,
		Capabilities:    ToolCapabilities{Mutable: true},
		IntentExtractor: pathParentIntent(root, false),
		Spec: proto.ToolSpec{
			Name:        "fs_delete_file",
			Description: "Delete a single file or symlink. Refuses directories; use fs_delete_dir for directories. Workspace-relative paths are resolved inside the workspace; absolute or home-directory paths outside it require approval.",
			InputSchema: objectSchema(map[string]any{
				"path": stringProp("File path to delete, relative to the workspace, absolute, or using the current user's home directory."),
			}, "path"),
		},
		Call: func(ctx context.Context, data json.RawMessage) (string, error) {
			var args struct {
				Path string `json:"path"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			path, err := resolveWorkspacePathNoFollowLeaf(ctx, root, args.Path, safeDirs)
			if err != nil {
				return "", err
			}
			info, err := os.Lstat(path)
			if err != nil {
				return "", err
			}
			if info.IsDir() {
				return "", fmt.Errorf("%s is a directory; use fs_delete_dir for directories", displayPath(root, path))
			}
			if err := os.Remove(path); err != nil {
				return "", err
			}
			return fmt.Sprintf("Deleted file %s", displayPath(root, path)), nil
		},
	}
}

func filesystemDeleteDirTool(root string, safeDirs []string) Tool {
	return Tool{
		Kind:            ToolKindBuiltin,
		Capabilities:    ToolCapabilities{Mutable: true},
		IntentExtractor: pathSelfIntent(root, approval.AccessWrite),
		Spec: proto.ToolSpec{
			Name:        "fs_delete_dir",
			Description: "Delete a directory. Non-empty directories require recursive=true. Refuses files; use fs_delete_file for files. Workspace-relative paths are resolved inside the workspace; absolute or home-directory paths outside it require approval.",
			InputSchema: objectSchema(map[string]any{
				"path":      stringProp("Directory path to delete, relative to the workspace, absolute, or using the current user's home directory."),
				"recursive": booleanProp("Set true to delete a non-empty directory and all contents."),
			}, "path"),
		},
		Call: func(ctx context.Context, data json.RawMessage) (string, error) {
			var args struct {
				Path      string `json:"path"`
				Recursive bool   `json:"recursive"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			path, err := resolveWorkspacePathNoFollowLeaf(ctx, root, args.Path, safeDirs)
			if err != nil {
				return "", err
			}
			info, err := os.Lstat(path)
			if err != nil {
				return "", err
			}
			if !info.IsDir() {
				return "", fmt.Errorf("%s is not a directory; use fs_delete_file for files", displayPath(root, path))
			}
			if args.Recursive {
				if err := os.RemoveAll(path); err != nil {
					return "", err
				}
				return fmt.Sprintf("Deleted directory recursively %s", displayPath(root, path)), nil
			}
			if err := os.Remove(path); err != nil {
				return "", err
			}
			return fmt.Sprintf("Deleted directory %s", displayPath(root, path)), nil
		},
	}
}

func filesystemMoveTool(root string, safeDirs []string) Tool {
	return Tool{
		Kind:            ToolKindBuiltin,
		Capabilities:    ToolCapabilities{Mutable: true},
		IntentExtractor: filesystemMoveIntent(root),
		Spec: proto.ToolSpec{
			Name:        "fs_move",
			Description: "Move or rename a file, symlink, or directory. If dest_path is an existing directory, the source is moved inside it. Existing destination files require overwrite=true; directory overwrites are refused.",
			InputSchema: objectSchema(map[string]any{
				"source_path": stringProp("Existing file, symlink, or directory to move."),
				"dest_path":   stringProp("Destination path, or an existing directory to move into."),
				"overwrite":   booleanProp("Set true to replace an existing destination file. Directory overwrites are refused."),
			}, "source_path", "dest_path"),
		},
		Call: func(ctx context.Context, data json.RawMessage) (string, error) {
			var args struct {
				SourcePath string `json:"source_path"`
				DestPath   string `json:"dest_path"`
				Overwrite  bool   `json:"overwrite"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			source, err := resolveWorkspacePathNoFollowLeaf(ctx, root, args.SourcePath, safeDirs)
			if err != nil {
				return "", err
			}
			dest, err := resolveWorkspacePathNoFollowLeaf(ctx, root, args.DestPath, safeDirs)
			if err != nil {
				return "", err
			}
			finalDest, err := movePath(source, dest, args.Overwrite)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Moved %s to %s", displayPath(root, source), displayPath(root, finalDest)), nil
		},
	}
}

func filesystemCopyTool(root string, safeDirs []string) Tool {
	return Tool{
		Kind:            ToolKindBuiltin,
		Capabilities:    ToolCapabilities{Mutable: true},
		IntentExtractor: filesystemCopyIntent(root),
		Spec: proto.ToolSpec{
			Name:        "fs_copy",
			Description: "Copy a file or directory. Directory copies require recursive=true and the destination must not already exist. If dest_path is an existing directory for a file copy, the file is copied inside it.",
			InputSchema: objectSchema(map[string]any{
				"source_path": stringProp("Existing file or directory to copy."),
				"dest_path":   stringProp("Destination path, or an existing directory for file copies."),
				"recursive":   booleanProp("Set true to copy a directory recursively."),
				"overwrite":   booleanProp("Set true to replace an existing destination file. Directory overwrites are refused."),
			}, "source_path", "dest_path"),
		},
		Call: func(ctx context.Context, data json.RawMessage) (string, error) {
			var args struct {
				SourcePath string `json:"source_path"`
				DestPath   string `json:"dest_path"`
				Recursive  bool   `json:"recursive"`
				Overwrite  bool   `json:"overwrite"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			source, err := resolveWorkspacePath(ctx, root, args.SourcePath, safeDirs)
			if err != nil {
				return "", err
			}
			dest, err := resolveWorkspacePathNoFollowLeaf(ctx, root, args.DestPath, safeDirs)
			if err != nil {
				return "", err
			}
			finalDest, err := copyPath(source, dest, args.Recursive, args.Overwrite)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Copied %s to %s", displayPath(root, source), displayPath(root, finalDest)), nil
		},
	}
}

func filesystemMkdirTool(root string, safeDirs []string) Tool {
	return Tool{
		Kind:            ToolKindBuiltin,
		Capabilities:    ToolCapabilities{Mutable: true},
		IntentExtractor: pathSelfIntent(root, approval.AccessWrite),
		Spec: proto.ToolSpec{
			Name:        "fs_mkdir",
			Description: "Create a directory. Parent directories are created by default; set parents=false to require the immediate parent to already exist.",
			InputSchema: objectSchema(map[string]any{
				"path":    stringProp("Directory path to create, relative to the workspace, absolute, or using the current user's home directory."),
				"parents": booleanProp("Create missing parent directories. Defaults to true."),
			}, "path"),
		},
		Call: func(ctx context.Context, data json.RawMessage) (string, error) {
			var args struct {
				Path    string `json:"path"`
				Parents *bool  `json:"parents"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			path, err := resolveWorkspacePathNoFollowLeaf(ctx, root, args.Path, safeDirs)
			if err != nil {
				return "", err
			}
			parents := true
			if args.Parents != nil {
				parents = *args.Parents
			}
			if parents {
				if err := os.MkdirAll(path, 0o755); err != nil {
					return "", err
				}
			} else if err := os.Mkdir(path, 0o755); err != nil {
				return "", err
			}
			return fmt.Sprintf("Created directory %s", displayPath(root, path)), nil
		},
	}
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

func copyPath(source, dest string, recursive, overwrite bool) (string, error) {
	info, err := os.Stat(source)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		if !recursive {
			return "", fmt.Errorf("%s is a directory; set recursive=true to copy directories", source)
		}
		if err := copyDir(source, dest); err != nil {
			return "", err
		}
		return dest, nil
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", source)
	}
	finalDest, err := copyFile(source, dest, overwrite)
	if err != nil {
		return "", err
	}
	return finalDest, nil
}

func copyFile(source, dest string, overwrite bool) (string, error) {
	sourceInfo, err := os.Stat(source)
	if err != nil {
		return "", err
	}
	if !sourceInfo.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", source)
	}
	if destInfo, err := os.Lstat(dest); err == nil {
		if destInfo.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("destination %s is a symlink; refusing to overwrite it", dest)
		}
		if destInfo.IsDir() {
			dest = filepath.Join(dest, filepath.Base(source))
			destInfo, err = os.Lstat(dest)
		}
		if err == nil {
			if destInfo.Mode()&os.ModeSymlink != 0 {
				return "", fmt.Errorf("destination %s is a symlink; refusing to overwrite it", dest)
			}
			if destInfo.IsDir() {
				return "", fmt.Errorf("destination %s is a directory", dest)
			}
			if !overwrite {
				return "", fmt.Errorf("destination %s already exists; set overwrite=true to replace it", dest)
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	in, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer in.Close() //nolint:errcheck

	flags := os.O_WRONLY | os.O_CREATE
	if overwrite {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_EXCL
	}
	out, err := os.OpenFile(dest, flags, sourceInfo.Mode().Perm())
	if err != nil {
		return "", err
	}
	defer out.Close() //nolint:errcheck
	if _, err := io.Copy(out, in); err != nil {
		return "", err
	}
	return dest, nil
}

func copyDir(source, dest string) error {
	if _, err := os.Lstat(dest); err == nil {
		return fmt.Errorf("destination directory %s already exists; choose a new destination path", dest)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return filepath.WalkDir(source, func(current string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to copy symlink %s inside directory copy", current)
		}
		rel, err := filepath.Rel(source, current)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		if d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			return os.MkdirAll(target, info.Mode().Perm())
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		_, err = copyFile(current, target, false)
		return err
	})
}

func movePath(source, dest string, overwrite bool) (string, error) {
	sourceInfo, err := os.Lstat(source)
	if err != nil {
		return "", err
	}
	if destInfo, err := os.Lstat(dest); err == nil {
		if destInfo.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("destination %s is a symlink; refusing to overwrite it", dest)
		}
		if destInfo.IsDir() {
			dest = filepath.Join(dest, filepath.Base(source))
			destInfo, err = os.Lstat(dest)
		}
		if err == nil {
			if destInfo.Mode()&os.ModeSymlink != 0 {
				return "", fmt.Errorf("destination %s is a symlink; refusing to overwrite it", dest)
			}
			if sourceInfo.IsDir() || destInfo.IsDir() {
				return "", fmt.Errorf("directory overwrites are refused")
			}
			if !overwrite {
				return "", fmt.Errorf("destination %s already exists; set overwrite=true to replace it", dest)
			}
			if err := os.Remove(dest); err != nil {
				return "", err
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if sourceInfo.IsDir() && contains(source, dest) {
		return "", fmt.Errorf("cannot move a directory inside itself")
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(source, dest); err != nil {
		return "", err
	}
	return dest, nil
}

func displayPath(root, path string) string {
	if contains(root, path) {
		return workspaceRel(root, path)
	}
	return filepath.ToSlash(path)
}
