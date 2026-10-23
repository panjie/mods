package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/pathutil"
)

var ErrStaleAccess = errors.New("approved filesystem target changed; prepare and review the call again")
var ErrMissingWriteGrant = errors.New("filesystem write requires a grant for this exact call")

type preparedPath struct {
	input, resolved string
	noFollow        bool
	write           bool
}

// PreparedAccess binds a native call to operation-specific canonical targets.
// It is read-only after preparation; only the approval caller issues its grant.
type PreparedAccess struct {
	Intent     approval.AccessIntent
	name, root string
	args       []byte
	paths      []preparedPath
	safeDirs   []string
}

type executionGrantKey struct{}

// WithExecutionGrant must be called only after the ordinary approval gate.
// A copy binds the grant to this payload and these targets, not to a cwd.
func WithExecutionGrant(ctx context.Context, access *PreparedAccess) context.Context {
	if access == nil {
		return ctx
	}
	copy := *access
	copy.args = append([]byte(nil), access.args...)
	copy.paths = append([]preparedPath(nil), access.paths...)
	return context.WithValue(ctx, executionGrantKey{}, &copy)
}

func (r *Registry) PrepareAccess(ctx context.Context, name string, data []byte) (*PreparedAccess, error) {
	tool, ok := r.Tool(name)
	if !ok {
		return nil, r.UnknownToolError(name)
	}
	if tool.PrepareAccess == nil {
		return nil, nil
	}
	return tool.PrepareAccess(ctx, data)
}

func compactArgs(data []byte) []byte {
	var b bytes.Buffer
	if json.Compact(&b, data) != nil {
		return nil
	}
	return b.Bytes()
}

func (p *PreparedAccess) validate(ctx context.Context, name string, data []byte) error {
	if p == nil || p.name != name || !bytes.Equal(p.args, compactArgs(data)) {
		return ErrMissingWriteGrant
	}
	for _, target := range p.paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		current, err := resolvePreparedPath(target.input, target.noFollow)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrStaleAccess, err)
		}
		if !samePath(current, target.resolved) {
			return ErrStaleAccess
		}
	}
	return nil
}

// Revalidate runs before dispatch, allowing the caller to obtain a fresh review
// without retrying a tool that may already have produced side effects.
func (p *PreparedAccess) Revalidate(ctx context.Context) error {
	if p == nil {
		return nil
	}
	return p.validate(ctx, p.name, p.args)
}

// BindResolvedArguments permits a secret reference to change content, but never
// a path or operation target. Neither payload is logged or shown here.
func (p *PreparedAccess) BindResolvedArguments(ctx context.Context, data []byte) (*PreparedAccess, error) {
	if p == nil || bytes.Equal(p.args, compactArgs(data)) {
		return p, nil
	}
	fresh, err := prepareFilesystemAccess(ctx, p.root, p.safeDirs, p.name, data)
	if err != nil {
		return nil, err
	}
	if !slices.Equal(p.paths, fresh.paths) {
		return nil, ErrStaleAccess
	}
	return fresh, nil
}

func samePath(a, b string) bool { return pathutil.Contains(a, b) && pathutil.Contains(b, a) }

func resolvePreparedPath(path string, noFollow bool) (string, error) {
	if !noFollow {
		return pathutil.ResolveThroughExistingParent(path)
	}
	parent, err := pathutil.ResolveThroughExistingParent(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(path)), nil
}

// A prepared target is rechecked at each path access as well as before any
// side effects. This does not claim to eliminate filesystem syscall races.
func grantedPath(ctx context.Context, root, input string, noFollow bool) (string, bool, error) {
	p, _ := ctx.Value(executionGrantKey{}).(*PreparedAccess)
	if p == nil {
		return "", false, nil
	}
	path := pathutil.NormalizePath(input, pathutil.DefaultOptions(root, pathutil.FlavorPOSIX))
	for _, target := range p.paths {
		if !samePath(path, target.input) {
			continue
		}
		current, err := resolvePreparedPath(path, noFollow)
		if err != nil {
			return "", true, err
		}
		if !samePath(current, target.resolved) {
			return "", true, ErrStaleAccess
		}
		return current, true, nil
	}
	// A write grant never falls back to implicit cwd/safe-directory authority.
	return "", true, ErrMissingWriteGrant
}

func withPreparedFilesystem(tool Tool, root string, safeDirs []string) Tool {
	name, call := tool.Spec.Name, tool.Call
	tool.PrepareAccess = func(ctx context.Context, data json.RawMessage) (*PreparedAccess, error) {
		return prepareFilesystemAccess(ctx, root, safeDirs, name, data)
	}
	tool.Call = func(ctx context.Context, data json.RawMessage) (string, error) {
		p, _ := ctx.Value(executionGrantKey{}).(*PreparedAccess)
		if err := p.validate(ctx, name, data); err != nil {
			return "", err
		}
		return call(ctx, data)
	}
	return tool
}

func prepareFilesystemAccess(ctx context.Context, root string, safeDirs []string, name string, data json.RawMessage) (*PreparedAccess, error) {
	var args struct {
		Path   string `json:"path"`
		Source string `json:"source_path"`
		Dest   string `json:"dest_path"`
		Patch  string `json:"patch"`
	}
	if err := json.Unmarshal(data, &args); err != nil {
		return nil, err
	}
	p := &PreparedAccess{name: name, root: root, args: compactArgs(data), safeDirs: append([]string(nil), safeDirs...), Intent: approval.AccessIntent{WriteCoverage: approval.CoverageComplete, WriteDirs: []string{}}}
	add := func(input string, noFollow, write, directory bool) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if input == "" {
			return fmt.Errorf("path is required")
		}
		path := pathutil.NormalizePath(input, pathutil.DefaultOptions(root, pathutil.FlavorPOSIX))
		if pathutil.IsUnresolvedHomePath(path) {
			return fmt.Errorf("unsupported user-home path")
		}
		resolved, err := resolvePreparedPath(path, noFollow)
		if err != nil {
			return err
		}
		p.paths = append(p.paths, preparedPath{input: path, resolved: resolved, noFollow: noFollow, write: write})
		dir := filepath.Dir(resolved)
		if directory {
			dir = resolved
		}
		if write {
			p.Intent.WriteDirs = append(p.Intent.WriteDirs, dir)
			// MkdirAll may create ancestors above the requested subtree. Include
			// every missing ancestor rather than borrowing a grant for its child.
			for parent := filepath.Dir(dir); parent != dir; parent = filepath.Dir(parent) {
				if _, err := os.Lstat(parent); !os.IsNotExist(err) {
					break
				}
				p.Intent.WriteDirs = append(p.Intent.WriteDirs, parent)
				if filepath.Dir(parent) == parent {
					break
				}
			}
		} else {
			p.Intent.ReadDirs = append(p.Intent.ReadDirs, dir)
		}
		return nil
	}
	var err error
	switch name {
	case "fs_write_file", "fs_replace":
		err = add(args.Path, false, true, false)
	case "fs_delete_file":
		err = add(args.Path, true, true, false)
	case "fs_delete_dir", "fs_mkdir":
		err = add(args.Path, true, true, true)
	case "fs_move", "fs_copy":
		err = add(args.Source, name == "fs_move", name == "fs_move", false)
		if err != nil {
			break
		}
		dest := pathutil.NormalizePath(args.Dest, pathutil.DefaultOptions(root, pathutil.FlavorPOSIX))
		// The tools refuse a destination leaf symlink. Preserve that check;
		// an existing real directory also selects a basename inside it.
		if info, statErr := os.Lstat(dest); statErr == nil && info.IsDir() {
			if err = add(args.Dest, true, true, true); err != nil {
				break
			}
			err = add(filepath.Join(dest, filepath.Base(args.Source)), true, true, false)
		} else {
			err = add(args.Dest, true, true, false)
		}
	case "http_download":
		var downloads downloadArgs
		downloads, err = parseDownloads(root, data)
		if err != nil {
			break
		}
		for _, file := range downloads.Files {
			if err = add(file.Path, true, true, false); err != nil {
				break
			}
			p.Intent.ReadOrigins = append(p.Intent.ReadOrigins, file.URL)
		}
	case "fs_apply_patch":
		if strings.HasPrefix(strings.TrimSpace(args.Patch), "*** Begin Patch") {
			ops, parseErr := parseCodexPatch(args.Patch)
			if parseErr != nil {
				return nil, parseErr
			}
			for _, op := range ops {
				if err = add(op.path, false, true, false); err != nil {
					return nil, err
				}
				if op.moveTo != "" {
					if err = add(op.moveTo, false, true, false); err != nil {
						return nil, err
					}
				}
			}
		} else {
			// Reuse Git's header decoding, including C quoting and timestamps.
			// A mode-only/binary section without path headers remains non-reusable.
			section, hasTarget := false, false
			for _, line := range strings.Split(strings.ReplaceAll(args.Patch, "\r\n", "\n"), "\n") {
				if strings.HasPrefix(line, "diff --git ") {
					if section && !hasTarget {
						p.Intent.Gaps = append(p.Intent.Gaps, approval.GapWriteTargets)
					}
					section, hasTarget = true, false
				}
				for _, prefix := range patchPathLinePrefixes {
					if !strings.HasPrefix(line, prefix.prefix) {
						continue
					}
					value, parseErr := extractPatchPath(strings.TrimPrefix(line, prefix.prefix))
					if parseErr != nil {
						return nil, parseErr
					}
					if value == "/dev/null" {
						continue
					}
					if prefix.skipAB && (strings.HasPrefix(value, "a/") || strings.HasPrefix(value, "b/")) {
						value = value[2:]
					}
					if err = add(value, false, true, false); err != nil {
						return nil, err
					}
					hasTarget = true
				}
			}
			if section && !hasTarget {
				p.Intent.Gaps = append(p.Intent.Gaps, approval.GapWriteTargets)
			}
			if len(p.Intent.Gaps) > 0 || len(p.paths) == 0 {
				p.Intent.WriteCoverage = approval.CoverageIncomplete
				if err = add(root, false, true, true); err != nil {
					return nil, err
				}
			}
		}

	default:
		return nil, fmt.Errorf("unsupported filesystem preparation: %s", name)
	}
	if err != nil {
		return nil, err
	}
	if len(p.Intent.WriteDirs) == 0 {
		return nil, fmt.Errorf("no writable targets in %s", name)
	}
	return p, nil
}
