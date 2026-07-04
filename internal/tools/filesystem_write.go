package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/platform"
	"github.com/panjie/mods/internal/proto"
)

func filesystemWriteFileTool(root string, safeDirs []string) Tool {
	return Tool{
		Kind:            ToolKindBuiltin,
		Capabilities:    ToolCapabilities{Mutable: true},
		IntentExtractor: pathParentIntent(root, false),
		Spec: proto.ToolSpec{
			Name:        "fs_write_file",
			Description: "Write a UTF-8 text file, replacing existing content. Workspace-relative paths are resolved inside the workspace; absolute or home-directory paths outside it require approval.",
			InputSchema: objectSchema(map[string]any{
				"path":    stringProp("Path to write, relative to the workspace, absolute, or using the current user's home directory."),
				"content": stringProp("Complete file content to write."),
			}, "path", "content"),
		},
		Call: func(ctx context.Context, data json.RawMessage) (string, error) {
			var args struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			path, err := resolveWorkspacePath(ctx, root, args.Path, safeDirs)
			if err != nil {
				return "", err
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return "", err
			}
			if err := os.WriteFile(path, []byte(args.Content), 0o644); err != nil {
				return "", err
			}
			return fmt.Sprintf("Wrote %d bytes to %s", len(args.Content), displayPath(root, path)), nil
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

func filesystemApplyPatchTool(root string, safeDirs []string) Tool {
	return Tool{
		Kind:            ToolKindBuiltin,
		Capabilities:    ToolCapabilities{Mutable: true},
		IntentExtractor: patchIntent(root),
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
			if err := validatePatchPaths(ctx, root, args.Patch); err != nil {
				return "", err
			}
			cmd := exec.CommandContext(ctx, "git", "-c", "core.autocrlf=false", "apply", "--whitespace=nowarn")
			platform.HideCommandWindow(cmd)
			cmd.Dir = root
			cmd.Stdin = strings.NewReader(args.Patch)
			out, err := cmd.CombinedOutput()
			if err != nil {
				return "", fmt.Errorf("git apply failed: %w\n%s", err, strings.TrimSpace(string(out)))
			}
			return "Patch applied", nil
		},
	}
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
