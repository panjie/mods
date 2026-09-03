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

// redactRemoteURLsForDisplay replaces literal URLs with their normalized
// origins before they reach approval UI text. This removes URL credentials,
// paths, queries, and fragments while preserving the destination boundary the
// user is being asked to authorize.
func redactRemoteURLsForDisplay(value string) string {
	return remoteURIRe.ReplaceAllStringFunc(value, func(match string) string {
		origin, ok := approval.NormalizeRemoteOrigin(trimRemoteToken(match))
		if !ok {
			return "[REDACTED URL]"
		}
		return origin
	})
}

func trimRemoteToken(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), `.,;)]}`)
}

func (m *Mods) resolveGitPushOrigins(tool, command, workspace string) (origins, unresolved []string) {
	if tool == "process_run" {
		return nil, nil
	}
	match := gitPushRe.FindStringSubmatch(command)
	if len(match) == 0 {
		return nil, nil
	}
	cwd := workspace
	if strings.TrimSpace(match[1]) != "" {
		cwd = unquoteSimpleToken(match[1])
		if !filepath.IsAbs(cwd) {
			cwd = filepath.Join(workspace, cwd)
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
