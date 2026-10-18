package app

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/panjie/mods/internal/approval"
)

var (
	reviewURLStartRe = regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://`)
	remoteURIRe      = regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://[^\s'"<>]+`)
	scpTargetRe      = regexp.MustCompile(`(?:[^\s@:/]+@)?(?:\[[0-9a-fA-F:]+\]|[a-zA-Z0-9._-]+):[^\s'"<>]+`)
	gitPushRe        = regexp.MustCompile(`(?i)(?:^|[;&|]\s*)git(?:\s+-C\s+("[^"]+"|'[^']+'|[^\s]+))?\s+push(?:\s+([^;&|]+))?`)
	bareSCPContextRe = regexp.MustCompile(`(?i)(?:^|[;&|]\s*)(?:(?:[^\s;&|]+/)?(?:scp|sftp|rsync)\b|git(?:\s+-C\s+(?:"[^"]+"|'[^']+'|[^\s]+))?\s+(?:clone|fetch|pull|push|submodule\s+add|remote\s+add)\b)`)
)

func extractLiteralRemoteOrigins(command string) []string {
	var candidates []string
	for _, match := range remoteURIRe.FindAllString(command, -1) {
		candidates = append(candidates, trimRemoteToken(match))
	}
	for _, match := range scpTargetRe.FindAllString(command, -1) {
		match = trimRemoteToken(match)
		if strings.Contains(match, "://") || strings.ContainsAny(match, "$`") {
			continue
		}
		if !strings.Contains(match, "@") && !bareSCPContextRe.MatchString(command) {
			continue
		}
		candidates = append(candidates, match)
	}
	return approval.NormalizeRemoteOrigins(candidates)
}

// redactRemoteURLsForDisplay preserves executable text and URL paths. Unlike
// origin extraction, display redaction must not consume an adjacent shell
// operator, substitution, or command as part of a URL.
func redactRemoteURLsForDisplay(value string) string {
	var out strings.Builder
	last := 0
	quote, quoteStart, scanned := byte(0), -1, 0
	for _, start := range reviewURLStartRe.FindAllStringIndex(value, -1) {
		if start[0] < last {
			continue
		}
		// Track actual opening quotes, not just the preceding character:
		// ''https://host/?q=x&command has an executable '&'. Escaped quotes
		// and quotes inside another string do not open a literal URL either.
		for scanned < start[0] {
			c := value[scanned]
			if (c == '\\' || c == '`') && quote != '\'' {
				scanned += 2
				continue
			}
			if quote == 0 && (c == '\'' || c == '"') {
				quote, quoteStart = c, scanned
			} else if c == quote {
				quote = 0
			}
			scanned++
		}
		quotedURL := quote != 0 && quoteStart == start[0]-1
		end := start[1]
		for end < len(value) {
			c := value[end]
			if strings.ContainsRune(" \t\r\n\"'<>;|()`$\\", rune(c)) ||
				c == '&' && (!quotedURL || end+1 < len(value) && value[end+1] == '&') {
				break
			}
			end++
		}
		out.WriteString(value[last:start[0]])
		out.WriteString(redactReviewURL(value[start[0]:end]))
		last = end
	}
	out.WriteString(value[last:])
	return out.String()
}

// Redact literal URL components without re-serializing the URL: case, port,
// escaped path bytes, and trailing punctuation must stay as supplied.
func redactReviewURL(raw string) string {
	scheme := strings.Index(raw, "://")
	if scheme < 0 {
		return raw
	}
	authorityEnd := len(raw)
	if i := strings.IndexAny(raw[scheme+3:], "/?#"); i >= 0 {
		authorityEnd = scheme + 3 + i
	}
	if at := strings.LastIndex(raw[scheme+3:authorityEnd], "@"); at >= 0 {
		raw = raw[:scheme+3] + "[redacted]@" + raw[scheme+3+at+1:]
	}
	base, fragment, hasFragment := strings.Cut(raw, "#")
	path, query, hasQuery := strings.Cut(base, "?")
	if hasQuery {
		base = path + "?"
		if query != "" {
			base += "[redacted]"
		}
	}
	if hasFragment {
		base += "#"
		if fragment != "" {
			base += "[redacted]"
		}
	}
	return base
}

func trimRemoteToken(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), `.,;)]}`)
}

func (m *Mods) resolveGitPushOrigins(tool, command, baseDir string) (origins, unresolved []string) {
	if tool == "process_run" {
		return nil, nil
	}
	match := gitPushRe.FindStringSubmatch(command)
	if len(match) == 0 {
		return nil, nil
	}
	cwd := baseDir
	if strings.TrimSpace(match[1]) != "" {
		cwd = unquoteSimpleToken(match[1])
		if !filepath.IsAbs(cwd) {
			cwd = filepath.Join(baseDir, cwd)
		}
	}
	return m.resolveGitRemoteOrigins(strings.Fields(strings.TrimSpace(match[2])), cwd)
}

func (m *Mods) resolveGitPushArgvOrigins(args []string, cwd string) (origins, unresolved []string) {
	push := -1
	for i, arg := range args {
		if strings.EqualFold(arg, "push") {
			push = i
			break
		}
		if arg == "-C" && i+1 < len(args) {
			candidate := args[i+1]
			if !filepath.IsAbs(candidate) {
				candidate = filepath.Join(cwd, candidate)
			}
			cwd = candidate
		}
	}
	if push < 0 {
		return nil, nil
	}
	return m.resolveGitRemoteOrigins(args[push+1:], cwd)
}

func (m *Mods) resolveGitRemoteOrigins(rest []string, cwd string) (origins, unresolved []string) {
	remote := "origin"
	for i := 0; i < len(rest); i++ {
		arg := rest[i]
		if strings.HasPrefix(arg, "-") {
			if gitPushOptionTakesValue(arg) && !strings.Contains(arg, "=") {
				i++
			}
			continue
		}
		remote = unquoteSimpleToken(arg)
		break
	}
	if origin, ok := approval.NormalizeRemoteOrigin(remote); ok {
		return []string{origin}, nil
	}
	if cwd == "" || strings.ContainsAny(remote, "$`") {
		return nil, []string{remote}
	}
	ctx := context.Background()
	if m != nil && m.ctx != nil {
		ctx = m.ctx
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	git := "git"
	if runtime.GOOS == "windows" {
		git = "git.exe"
	}
	cmd := exec.CommandContext(ctx, git, "-C", cwd, "remote", "get-url", "--push", remote)
	output, err := cmd.Output()
	if err != nil {
		return nil, []string{remote}
	}
	origins = approval.NormalizeRemoteOrigins([]string{strings.TrimSpace(string(output))})
	if len(origins) == 0 {
		return nil, []string{remote}
	}
	return origins, nil
}

func gitPushOptionTakesValue(arg string) bool {
	switch arg {
	case "--repo", "--receive-pack", "--exec", "-o", "--push-option":
		return true
	default:
		return false
	}
}

func unquoteSimpleToken(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && (value[0] == '\'' && value[len(value)-1] == '\'' || value[0] == '"' && value[len(value)-1] == '"') {
		if value[0] == '"' {
			var decoded string
			if json.Unmarshal([]byte(value), &decoded) == nil {
				return decoded
			}
		}
		return value[1 : len(value)-1]
	}
	return value
}

func isGitProgram(program string) bool {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(program)))
	return base == "git" || base == "git.exe"
}
