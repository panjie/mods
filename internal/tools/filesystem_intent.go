package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/pathutil"
)

// pathParentIntent builds an AccessIntent extractor for single-path tools.
// Writes touch the parent directory. Reads touch the target directory when
// the path exists as a directory, otherwise the containing directory.
func pathParentIntent(root string, readOnly bool) func(json.RawMessage) approval.AccessIntent {
	class := approval.AccessWrite
	if readOnly {
		class = approval.AccessRead
	}
	return func(data json.RawMessage) approval.AccessIntent {
		var args struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(data, &args)
		if args.Path == "" {
			return approval.AccessIntent{Class: class}
		}
		var dir string
		if readOnly {
			dir = intentDir(root, args.Path)
		} else {
			p := pathutil.NormalizePath(args.Path, pathutil.DefaultOptions(root, pathutil.FlavorPOSIX))
			dir = pathutil.ParentDir(p)
		}
		return approval.AccessIntent{Class: class, Dirs: []string{dir}}
	}
}

// patchIntent builds an AccessIntent extractor for fs_apply_patch. It
// parses the +++ headers (after stripping a/ b/ prefixes) and reports each
// touched file's parent directory. Patch paths are workspace-relative by
// construction (validatePatchPaths rejects absolute/.. paths), so they are
// joined onto root.
func patchIntent(root string) func(json.RawMessage) approval.AccessIntent {
	return func(data json.RawMessage) approval.AccessIntent {
		var args struct {
			Patch string `json:"patch"`
		}
		_ = json.Unmarshal(data, &args)
		var dirs []string
		seen := map[string]struct{}{}
		for _, line := range strings.Split(args.Patch, "\n") {
			if !strings.HasPrefix(line, "+++ ") {
				continue
			}
			p := strings.TrimSpace(line[4:])
			p = strings.Trim(p, `"`)
			if p == "/dev/null" {
				continue
			}
			if strings.HasPrefix(p, "a/") || strings.HasPrefix(p, "b/") {
				p = p[2:]
			}
			if !filepath.IsAbs(p) {
				p = filepath.Join(root, p)
			}
			d := filepath.Dir(filepath.Clean(p))
			if _, ok := seen[d]; !ok {
				seen[d] = struct{}{}
				dirs = append(dirs, d)
			}
		}
		return approval.AccessIntent{Class: approval.AccessWrite, Dirs: dirs}
	}
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
	return intentDir(root, input)
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

// intentDir returns the directory an operation on input affects: input
// itself when it names an existing directory, otherwise input's parent
// directory. Returns "" for empty input.
func intentDir(root, input string) string {
	if input == "" {
		return ""
	}
	p := pathutil.NormalizePath(input, pathutil.DefaultOptions(root, pathutil.FlavorPOSIX))
	if info, err := os.Stat(p); err == nil && info.IsDir() {
		return p
	}
	return pathutil.ParentDir(p)
}
