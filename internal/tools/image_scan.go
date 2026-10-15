package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type imageScanFrame struct {
	path    string
	entries []os.DirEntry
	index   int
}
type imageScan struct {
	visited   map[string]os.FileInfo
	invalid   bool
	root      string
	recursive bool
	kinds     string
	stack     []imageScanFrame
	found     []string
	complete  bool
	issues    int
	cursor    string
}
type imageScanPage struct {
	Kinds      map[string]string `json:"kinds,omitempty"`
	Paths      []string          `json:"paths"`
	NextCursor string            `json:"next_cursor,omitempty"`
	Complete   bool              `json:"complete"`
	Issues     []string          `json:"issues,omitempty"`
}

func (s *imageSearchState) find(ctx context.Context, root string, safeDirs []string, data json.RawMessage) (string, error) {
	var args struct {
		Path      string   `json:"path"`
		Recursive bool     `json:"recursive"`
		Cursor    string   `json:"cursor"`
		Limit     int      `json:"limit"`
		Kinds     []string `json:"kinds"`
	}
	if err := decodeArgs(data, &args); err != nil {
		return "", err
	}
	kinds, err := scanKinds(args.Kinds)
	if err != nil {
		return "", err
	}
	path, err := resolveAuthorizedPath(ctx, root, args.Path, safeDirs)
	if err != nil {
		return "", err
	}
	if args.Limit <= 0 {
		args.Limit = 100
	}
	if args.Limit > 500 {
		args.Limit = 500
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var scan *imageScan
	if args.Cursor != "" {
		for _, candidate := range s.scans {
			if candidate.cursor == args.Cursor && !candidate.complete && !candidate.invalid {
				scan = candidate
				break
			}
		}
		if scan == nil {
			return "", fmt.Errorf("image scan cursor expired or already consumed; restart enumeration")
		}
		if scan.root != path || scan.recursive != args.Recursive || scan.kinds != kinds {
			return "", fmt.Errorf("cursor belongs to a different scan scope")
		}
	} else {
		entries, stamp, err := imageDirectorySnapshot(path)
		if err != nil {
			return "", err
		}
		for id, previous := range s.scans {
			if previous.root == path && previous.recursive == args.Recursive && previous.kinds == kinds {
				delete(s.scans, id)
			}
		}
		if len(s.scans) >= 8 {
			return "", fmt.Errorf("maximum 8 image scans per request; continue an existing cursor")
		}
		scan = &imageScan{kinds: kinds, visited: map[string]os.FileInfo{path: stamp}, root: path, recursive: args.Recursive, stack: []imageScanFrame{{path: path, entries: entries}}}
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return "", err
		}
		s.scans[hex.EncodeToString(nonce[:])] = scan
	}
	for dir, previous := range scan.visited {
		current, err := os.Stat(dir)
		if err != nil || !sameImageFileVersion(previous, current) {
			scan.invalid = true
			scan.issues++
			return "", fmt.Errorf("directory changed during image scan: %s; restart enumeration", dir)
		}
	}
	page := imageScanPage{Paths: []string{}, Kinds: map[string]string{}}
	// Bound work even when a page contains no image candidates.
	for steps := 0; len(scan.stack) > 0 && len(page.Paths) < args.Limit && steps < 10000; steps++ {
		if err := ctx.Err(); err != nil {
			scan.invalid = true
			scan.issues++
			return "", err
		}
		frame := &scan.stack[len(scan.stack)-1]
		if frame.index == len(frame.entries) {
			scan.stack = scan.stack[:len(scan.stack)-1]
			continue
		}
		entry := frame.entries[frame.index]
		frame.index++
		candidate := filepath.Join(frame.path, entry.Name())
		if entry.IsDir() {
			if !scan.recursive {
				continue
			}
			// Re-resolve each directory in case it changed to an escaping symlink.
			resolved, err := resolveAuthorizedPath(ctx, root, candidate, safeDirs)
			var entries []os.DirEntry
			if err == nil && scan.visited[resolved] != nil {
				page.Issues = append(page.Issues, fmt.Sprintf("%s: skipped repeated directory", candidate))
				scan.issues++
				continue
			}
			if err == nil {
				var stamp os.FileInfo
				entries, stamp, err = imageDirectorySnapshot(resolved)
				if err == nil {
					scan.visited[resolved] = stamp
				}
			}
			if err != nil {
				page.Issues = append(page.Issues, fmt.Sprintf("%s: %v", candidate, err))
				scan.issues++
				continue
			}
			scan.stack = append(scan.stack, imageScanFrame{path: resolved, entries: entries})
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			info, err := os.Stat(candidate)
			if err != nil || info.IsDir() {
				page.Issues = append(page.Issues, fmt.Sprintf("%s: skipped directory or broken symlink", candidate))
				scan.issues++
				continue
			}
		}
		kind := candidateKind(candidate)
		if kind == "" || !strings.Contains(scan.kinds, kind) {
			continue
		}
		rel, err := filepath.Rel(root, candidate)
		if err != nil {
			rel = candidate
		}
		page.Paths = append(page.Paths, rel)
		page.Kinds[rel] = kind
		resolved, err := resolveAuthorizedPath(ctx, root, candidate, safeDirs)
		if err != nil {
			resolved = candidate
		}
		scan.found = append(scan.found, resolved)
	}
	for len(scan.stack) > 0 && scan.stack[len(scan.stack)-1].index == len(scan.stack[len(scan.stack)-1].entries) {
		scan.stack = scan.stack[:len(scan.stack)-1]
	}
	scan.complete = len(scan.stack) == 0
	page.Complete = scan.complete
	if !scan.complete {
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return "", err
		}
		scan.cursor = hex.EncodeToString(nonce[:])
		page.NextCursor = scan.cursor
	}
	encoded, err := json.Marshal(page)
	return string(encoded), err
}

func imageDirectorySnapshot(path string) ([]os.DirEntry, os.FileInfo, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if !before.IsDir() {
		return nil, nil, fmt.Errorf("not a directory or directory changed to a symlink: %s", path)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, nil, err
	}
	after, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if !sameImageFileVersion(before, after) {
		return nil, nil, fmt.Errorf("directory changed during enumeration: %s", path)
	}
	return entries, after, nil
}

func sameImageFileVersion(a, b os.FileInfo) bool {
	return a != nil && b != nil && os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}
