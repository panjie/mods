package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Patch path validation. Runs before `git apply` to ensure every path
// referenced by the patch (including rename/copy targets and C-style
// quoted paths) stays inside the workspace root. This is the security
// boundary that prevents a malicious patch from writing to /etc/passwd
// via a symlink created earlier in the same diff.

// patchPathLinePrefixes maps each path-bearing header that can appear in a
// unified or git diff to the slice index where its path component begins.
// rename/copy headers are included so a rename-only diff (which carries no
// +++/--- lines) cannot smuggle a path outside the workspace via its target
// header.
var patchPathLinePrefixes = []struct {
	prefix string
	skipAB bool // strip a/ or b/ prefix (only on +++/--- lines)
}{
	{"+++ ", true},
	{"--- ", true},
	{"rename from ", false},
	{"rename to ", false},
	{"copy from ", false},
	{"copy to ", false},
}

func validatePatchPaths(root, patch string) error {
	for _, line := range strings.Split(strings.ReplaceAll(patch, "\r\n", "\n"), "\n") {
		// Refuse symlink creation. A single patch can first create a symlink
		// inside the workspace (e.g. `escape -> /etc`) and then write through
		// it in a later diff. Because validation runs before `git apply`,
		// such a patch would pass the path checks below and escape the
		// workspace root at apply time. There is no safe way to allow
		// mode 120000 via fs_apply_patch.
		if strings.HasSuffix(line, "mode 120000") &&
			(strings.HasPrefix(line, "new file mode ") ||
				strings.HasPrefix(line, "old mode ") ||
				strings.HasPrefix(line, "new mode ")) {
			return fmt.Errorf("patch refused: symlink creation is not allowed")
		}
		var raw string
		var skipAB bool
		matched := false
		for _, p := range patchPathLinePrefixes {
			if strings.HasPrefix(line, p.prefix) {
				raw = line[len(p.prefix):]
				skipAB = p.skipAB
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		path, err := extractPatchPath(raw)
		if err != nil {
			return err
		}
		if path == "" || path == "/dev/null" {
			continue
		}
		if skipAB && (strings.HasPrefix(path, "a/") || strings.HasPrefix(path, "b/")) {
			path = path[2:]
		}
		if filepath.IsAbs(path) || strings.HasPrefix(filepath.Clean(path), "..") {
			return fmt.Errorf("patch path %q is outside workspace root", path)
		}
		if _, err := resolveWorkspacePath(root, path, nil); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// extractPatchPath pulls the file path out of a patch header trailer. It
// honours two patch conventions that the old strings.Fields()[0] heuristic
// missed:
//
//   - POSIX patch terminates the path at a tab; the remainder is a timestamp.
//   - Git wraps paths containing spaces or special characters in
//     C-style double quotes. Without unquoting, a path like
//     "b/escape \"file" would have its first whitespace-delimited token
//     accepted ("b/escape) which neither matches the real target nor flags
//     as outside-workspace, letting a follow-on apply step write to the
//     real (escaped) destination.
//
// An unquoted path is required to contain no whitespace; trailing whitespace
// and CR are stripped. A path beginning with " must end with " and is
// unquoted using git's C-style escape rules.
func extractPatchPath(raw string) (string, error) {
	raw = strings.TrimRight(raw, " \t\r")
	if raw == "" {
		return "", fmt.Errorf("patch contains an empty file path")
	}
	if idx := strings.IndexByte(raw, '\t'); idx >= 0 {
		raw = raw[:idx]
	}
	if strings.HasPrefix(raw, `"`) {
		if !strings.HasSuffix(raw, `"`) || len(raw) < 2 {
			return "", fmt.Errorf("patch path has unbalanced quotes: %q", raw)
		}
		return unquoteCStylePath(raw)
	}
	if strings.ContainsAny(raw, " \r\n") {
		return "", fmt.Errorf("patch path contains unexpected whitespace; quote the path: %q", raw)
	}
	return raw, nil
}

// unquoteCStylePath decodes git's C-style quoted path (double-quoted with
// the standard backslash escapes plus three-digit octal sequences). It
// returns an error rather than silently dropping unknown escapes so a
// malformed patch fails closed.
func unquoteCStylePath(s string) (string, error) {
	if !strings.HasPrefix(s, `"`) || !strings.HasSuffix(s, `"`) || len(s) < 2 {
		return "", fmt.Errorf("malformed quoted path: %q", s)
	}
	body := s[1 : len(s)-1]
	var out strings.Builder
	out.Grow(len(body))
	i := 0
	for i < len(body) {
		c := body[i]
		if c != '\\' {
			out.WriteByte(c)
			i++
			continue
		}
		if i+1 >= len(body) {
			return "", fmt.Errorf("dangling escape in quoted path: %q", s)
		}
		next := body[i+1]
		switch next {
		case 'a':
			out.WriteByte('\a')
			i += 2
		case 'b':
			out.WriteByte('\b')
			i += 2
		case 'f':
			out.WriteByte('\f')
			i += 2
		case 'n':
			out.WriteByte('\n')
			i += 2
		case 'r':
			out.WriteByte('\r')
			i += 2
		case 't':
			out.WriteByte('\t')
			i += 2
		case 'v':
			out.WriteByte('\v')
			i += 2
		case '\\':
			out.WriteByte('\\')
			i += 2
		case '"':
			out.WriteByte('"')
			i += 2
		case '\'':
			out.WriteByte('\'')
			i += 2
		case '?':
			out.WriteByte('?')
			i += 2
		case '0', '1', '2', '3':
			if i+3 >= len(body) {
				return "", fmt.Errorf("invalid octal escape in quoted path: %q", s)
			}
			d2, d3 := body[i+2], body[i+3]
			if d2 < '0' || d2 > '7' || d3 < '0' || d3 > '7' {
				return "", fmt.Errorf("invalid octal escape in quoted path: %q", s)
			}
			v := (int(next-'0') << 6) | (int(d2-'0') << 3) | int(d3-'0')
			out.WriteByte(byte(v))
			i += 4
		default:
			return "", fmt.Errorf("unknown escape \\%c in quoted path: %q", next, s)
		}
	}
	return out.String(), nil
}
