package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type codexPatchOperation struct {
	kind   string
	path   string
	moveTo string
	body   []string
}

func normalizeApplyPatch(ctx context.Context, root, patch string) (string, error) {
	normalized := strings.ReplaceAll(patch, "\r\n", "\n")
	if !strings.HasPrefix(strings.TrimSpace(normalized), "*** Begin Patch") {
		return patch, nil
	}
	ops, err := parseCodexPatch(normalized)
	if err != nil {
		return "", err
	}
	if err := validateCodexPatchOperations(ctx, root, ops); err != nil {
		return "", err
	}

	var diff strings.Builder
	for _, op := range ops {
		path := filepath.Join(root, filepath.FromSlash(op.path))
		switch op.kind {
		case "add":
			content, contentErr := codexAddedContent(op.body)
			if contentErr != nil {
				return "", fmt.Errorf("add %s: %w", op.path, contentErr)
			}
			part, diffErr := fullUnifiedDiff(nil, []byte(content), "/dev/null", "b/"+op.path, "100644")
			if diffErr != nil {
				return "", diffErr
			}
			diff.WriteString(part)
		case "delete":
			old, readErr := os.ReadFile(path)
			if readErr != nil {
				return "", fmt.Errorf("delete %s: %w", op.path, readErr)
			}
			mode, modeErr := patchFileMode(path)
			if modeErr != nil {
				return "", modeErr
			}
			part, diffErr := fullUnifiedDiff(old, nil, "a/"+op.path, "/dev/null", mode)
			if diffErr != nil {
				return "", diffErr
			}
			diff.WriteString(part)
		case "update":
			old, readErr := os.ReadFile(path)
			if readErr != nil {
				return "", fmt.Errorf("update %s: %w", op.path, readErr)
			}
			updated := string(old)
			if len(op.body) > 0 {
				var updateErr error
				updated, updateErr = applyCodexUpdate(string(old), op.body)
				if updateErr != nil {
					return "", fmt.Errorf("update %s: %w", op.path, updateErr)
				}
			} else if op.moveTo == "" {
				return "", fmt.Errorf("update %s contains no hunks", op.path)
			}
			if op.moveTo == "" {
				part, diffErr := fullUnifiedDiff(old, []byte(updated), "a/"+op.path, "b/"+op.path, "")
				if diffErr != nil {
					return "", diffErr
				}
				diff.WriteString(part)
				continue
			}
			mode, modeErr := patchFileMode(path)
			if modeErr != nil {
				return "", modeErr
			}
			deletePart, diffErr := fullUnifiedDiff(old, nil, "a/"+op.path, "/dev/null", mode)
			if diffErr != nil {
				return "", diffErr
			}
			addPart, diffErr := fullUnifiedDiff(nil, []byte(updated), "/dev/null", "b/"+op.moveTo, mode)
			if diffErr != nil {
				return "", diffErr
			}
			diff.WriteString(deletePart)
			diff.WriteString(addPart)
		}
	}
	if diff.Len() == 0 {
		return "", fmt.Errorf("Codex patch does not change any files")
	}
	return diff.String(), nil
}

func parseCodexPatch(patch string) ([]codexPatchOperation, error) {
	lines := strings.Split(strings.TrimSuffix(patch, "\n"), "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != "*** Begin Patch" || strings.TrimSpace(lines[len(lines)-1]) != "*** End Patch" {
		return nil, fmt.Errorf("malformed Codex patch: expected *** Begin Patch and *** End Patch")
	}
	var ops []codexPatchOperation
	for i := 1; i < len(lines)-1; {
		line := lines[i]
		var op codexPatchOperation
		switch {
		case strings.HasPrefix(line, "*** Add File: "):
			op.kind, op.path = "add", strings.TrimSpace(strings.TrimPrefix(line, "*** Add File: "))
		case strings.HasPrefix(line, "*** Update File: "):
			op.kind, op.path = "update", strings.TrimSpace(strings.TrimPrefix(line, "*** Update File: "))
		case strings.HasPrefix(line, "*** Delete File: "):
			op.kind, op.path = "delete", strings.TrimSpace(strings.TrimPrefix(line, "*** Delete File: "))
		default:
			return nil, fmt.Errorf("malformed Codex patch line %d: %q", i+1, line)
		}
		if op.path == "" {
			return nil, fmt.Errorf("malformed Codex patch line %d: file path is empty", i+1)
		}
		i++
		for i < len(lines)-1 && !strings.HasPrefix(lines[i], "*** Add File: ") &&
			!strings.HasPrefix(lines[i], "*** Update File: ") &&
			!strings.HasPrefix(lines[i], "*** Delete File: ") {
			if strings.HasPrefix(lines[i], "*** Move to: ") {
				if op.kind != "update" || op.moveTo != "" {
					return nil, fmt.Errorf("malformed Codex patch line %d: unexpected move", i+1)
				}
				op.moveTo = strings.TrimSpace(strings.TrimPrefix(lines[i], "*** Move to: "))
				if op.moveTo == "" {
					return nil, fmt.Errorf("malformed Codex patch line %d: move target is empty", i+1)
				}
				i++
				continue
			}
			op.body = append(op.body, lines[i])
			i++
		}
		ops = append(ops, op)
	}
	if len(ops) == 0 {
		return nil, fmt.Errorf("Codex patch contains no file operations")
	}
	return ops, nil
}

func validateCodexPatchOperations(ctx context.Context, root string, ops []codexPatchOperation) error {
	seen := map[string]struct{}{}
	for _, op := range ops {
		paths := []struct {
			path   string
			source bool
		}{{path: op.path, source: op.kind != "add"}}
		if op.moveTo != "" {
			paths = append(paths, struct {
				path   string
				source bool
			}{path: op.moveTo})
		}
		for _, candidate := range paths {
			path := candidate.path
			clean := filepath.Clean(filepath.FromSlash(path))
			if filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
				return fmt.Errorf("patch path %q is outside workspace", path)
			}
			if _, ok := seen[clean]; ok {
				return fmt.Errorf("patch path %q is touched more than once", path)
			}
			seen[clean] = struct{}{}
			if candidate.source {
				if _, err := resolveWorkspacePath(ctx, root, clean, nil); err != nil {
					return err
				}
			} else if _, err := resolveWorkspacePathNoFollowLeaf(ctx, root, clean, nil); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

func codexAddedContent(lines []string) (string, error) {
	content := make([]string, 0, len(lines))
	for i, line := range lines {
		if !strings.HasPrefix(line, "+") {
			return "", fmt.Errorf("line %d must start with +", i+1)
		}
		content = append(content, strings.TrimPrefix(line, "+"))
	}
	if len(content) == 0 {
		return "", nil
	}
	return strings.Join(content, "\n") + "\n", nil
}

func applyCodexUpdate(original string, body []string) (string, error) {
	originalTrailingNewline := strings.HasSuffix(original, "\n")
	current := strings.Split(strings.TrimSuffix(strings.ReplaceAll(original, "\r\n", "\n"), "\n"), "\n")
	if original == "" {
		current = nil
	}
	var hunks [][]string
	for _, line := range body {
		if strings.HasPrefix(line, "@@") {
			hunks = append(hunks, nil)
			continue
		}
		if line == "*** End of File" {
			continue
		}
		if len(hunks) == 0 {
			hunks = append(hunks, nil)
		}
		hunks[len(hunks)-1] = append(hunks[len(hunks)-1], line)
	}
	if len(hunks) == 0 {
		return "", fmt.Errorf("update contains no hunks")
	}
	cursor := 0
	for hunkIndex, hunk := range hunks {
		var before, after []string
		for _, line := range hunk {
			if line == "" {
				before, after = append(before, ""), append(after, "")
				continue
			}
			switch line[0] {
			case ' ':
				before, after = append(before, line[1:]), append(after, line[1:])
			case '-':
				before = append(before, line[1:])
			case '+':
				after = append(after, line[1:])
			default:
				return "", fmt.Errorf("hunk %d contains invalid line %q", hunkIndex+1, line)
			}
		}
		if len(before) == 0 {
			return "", fmt.Errorf("hunk %d has no context or removed lines", hunkIndex+1)
		}
		match, matches := findLineSequence(current, before, cursor)
		if matches == 0 {
			return "", fmt.Errorf("hunk %d context was not found", hunkIndex+1)
		}
		if matches > 1 {
			return "", fmt.Errorf("hunk %d context is ambiguous (%d matches)", hunkIndex+1, matches)
		}
		next := make([]string, 0, len(current)-len(before)+len(after))
		next = append(next, current[:match]...)
		next = append(next, after...)
		next = append(next, current[match+len(before):]...)
		current = next
		cursor = match + len(after)
	}
	updated := strings.Join(current, "\n")
	if originalTrailingNewline && len(current) > 0 {
		updated += "\n"
	}
	return updated, nil
}

func findLineSequence(lines, sequence []string, start int) (index, matches int) {
	index = -1
	for i := start; i+len(sequence) <= len(lines); i++ {
		equal := true
		for j := range sequence {
			if lines[i+j] != sequence[j] {
				equal = false
				break
			}
		}
		if equal {
			if index < 0 {
				index = i
			}
			matches++
		}
	}
	return index, matches
}

func fullUnifiedDiff(old, updated []byte, from, to, mode string) (string, error) {
	if string(old) == string(updated) {
		return "", nil
	}
	fromPath := strings.TrimPrefix(from, "a/")
	toPath := strings.TrimPrefix(to, "b/")
	logicalPath := toPath
	if to == "/dev/null" {
		logicalPath = fromPath
	}
	var header strings.Builder
	header.WriteString("diff --git ")
	header.WriteString(quotePatchPath("a/" + logicalPath))
	header.WriteByte(' ')
	header.WriteString(quotePatchPath("b/" + logicalPath))
	header.WriteByte('\n')
	if from == "/dev/null" {
		header.WriteString("new file mode " + mode + "\n")
	} else if to == "/dev/null" {
		header.WriteString("deleted file mode " + mode + "\n")
	}
	header.WriteString("--- " + quotePatchPath(from) + "\n")
	header.WriteString("+++ " + quotePatchPath(to) + "\n")
	oldLines, oldTrailing := patchContentLines(string(old))
	newLines, newTrailing := patchContentLines(string(updated))
	header.WriteString(fmt.Sprintf("@@ -%s +%s @@\n", patchLineRange(len(oldLines)), patchLineRange(len(newLines))))
	writePatchContent(&header, '-', oldLines, oldTrailing)
	writePatchContent(&header, '+', newLines, newTrailing)
	return header.String(), nil
}

func patchFileMode(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("patch target %s is not a regular file", path)
	}
	if info.Mode().Perm()&0o111 != 0 {
		return "100755", nil
	}
	return "100644", nil
}

func quotePatchPath(path string) string {
	if path == "/dev/null" || !strings.ContainsAny(path, " \t\r\n\"") {
		return path
	}
	return strconv.Quote(path)
}

func patchContentLines(content string) ([]string, bool) {
	if content == "" {
		return nil, true
	}
	trailing := strings.HasSuffix(content, "\n")
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	return lines, trailing
}

func patchLineRange(count int) string {
	switch count {
	case 0:
		return "0,0"
	case 1:
		return "1"
	default:
		return fmt.Sprintf("1,%d", count)
	}
}

func writePatchContent(out *strings.Builder, prefix byte, lines []string, trailing bool) {
	for i, line := range lines {
		out.WriteByte(prefix)
		out.WriteString(line)
		out.WriteByte('\n')
		if i == len(lines)-1 && !trailing {
			out.WriteString("\\ No newline at end of file\n")
		}
	}
}
