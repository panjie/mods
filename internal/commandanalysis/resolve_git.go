package commandanalysis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/panjie/mods/internal/commandtrace"
	"github.com/panjie/mods/internal/pathutil"
)

var (
	remoteURIRe      = regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://[^\s'"<>]+`)
	scpTargetRe      = regexp.MustCompile(`(?:[^\s@:/]+@)?(?:\[[0-9a-fA-F:]+\]|[a-zA-Z0-9._-]+):[^\s'"<>]+`)
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
	return NormalizeRemoteOrigins(candidates)
}

func trimRemoteToken(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), `.,;)]}`)
}

func (m *analysisRun) resolveGitPushOrigins(posix bool, command, baseDir string) (origins, unresolved, local []string) {
	invocations, ok := ShellInvocations(command, posix)
	if !ok {
		return nil, nil, nil
	}
	contextChanged := false
	for _, inv := range invocations {
		if !isGitProgram(inv.Program) {
			// A preceding arbitrary command may change Git configuration or cwd.
			contextChanged = true
			continue
		}
		if gitArgsArePush(inv.Args) && (!inv.Literal || contextChanged || strings.ContainsAny(inv.Program, `/\`)) {
			unresolved = append(unresolved, "Git execution context")
			continue
		}
		found, unknown, dirs := m.resolveGitPushArgvOrigins(inv.Args, baseDir)
		origins = append(origins, found...)
		local = append(local, dirs...)
		unresolved = append(unresolved, unknown...)
		if !gitArgsArePush(inv.Args) {
			a := AssessArgvStaticWithPolicy("git", inv.Args, true, ReadOnlyCommandPolicy{})
			if a.Effect != EffectRead {
				contextChanged = true
			}
		}
	}
	return NormalizeRemoteOrigins(origins), unresolved, local
}

func (m *analysisRun) resolveGitPushArgvOrigins(args []string, cwd string) (origins, unresolved, local []string) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-C":
			i++
			if i >= len(args) {
				return nil, []string{"Git working directory"}, nil
			}
			candidate := args[i]
			if !filepath.IsAbs(candidate) {
				candidate = filepath.Join(cwd, candidate)
			}
			cwd = candidate
		case "push":
			return m.resolveGitRemoteOrigins(args[i+1:], cwd)
		default:
			if strings.HasPrefix(args[i], "-") && gitArgsArePush(args[i:]) {
				return nil, []string{"Git global options"}, nil
			}
			return nil, nil, nil
		}
	}
	return nil, nil, nil
}

func gitArgsArePush(args []string) bool {
	for _, arg := range args {
		if arg == "push" {
			return true
		}
	}
	return false
}

func (m *analysisRun) resolveGitRemoteOrigins(rest []string, cwd string) (origins, unresolved, local []string) {
	remote := ""
	for i := 0; i < len(rest); i++ {
		arg := rest[i]
		if arg == "--repo" {
			i++
			if i >= len(rest) {
				return nil, []string{"Git --repo"}, nil
			}
			remote = rest[i]
			break
		}
		if value, ok := strings.CutPrefix(arg, "--repo="); ok {
			remote = value
			break
		}
		if strings.HasPrefix(arg, "-") {
			if arg == "--" {
				if i+1 < len(rest) {
					remote = rest[i+1]
				}
				break
			}
			if gitPushOptionTakesValue(arg) && !strings.Contains(arg, "=") {
				i++
				if i >= len(rest) {
					return nil, []string{"Git push options"}, nil
				}
			}
			continue
		}
		remote = unquoteSimpleToken(arg)
		break
	}
	if cwd == "" || strings.ContainsAny(remote, "$`") {
		return nil, []string{"Git remote"}, nil
	}
	ctx := context.Background()
	if m != nil && m.ctx != nil {
		ctx = m.ctx
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	git, err := exec.LookPath("git")
	if err != nil {
		return nil, []string{"Git executable"}, nil
	}
	if location := pathutil.Location(git, cwd, m.programDirs); location == pathutil.LocationWorkingDir || location == pathutil.LocationSafe {
		return nil, []string{"untrusted Git executable"}, nil
	}
	query := func(args ...string) (string, error) {
		commandtrace.GitQuery()
		cmd := exec.CommandContext(ctx, git, append([]string{"-C", cwd}, args...)...)
		cmd.WaitDelay = 100 * time.Millisecond
		out := &gitQueryOutput{limit: 64 << 10}
		cmd.Stdout = out
		cmd.Stderr = io.Discard
		err := cmd.Run()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(out.String()), nil
	}
	if remote == "" {
		branch, _ := query("symbolic-ref", "--quiet", "--short", "HEAD")
		if branch != "" {
			remote, _ = query("config", "--get", "branch."+branch+".pushRemote")
		}
		if remote == "" {
			remote, _ = query("config", "--get", "remote.pushDefault")
		}
		if remote == "" && branch != "" {
			remote, _ = query("config", "--get", "branch."+branch+".remote")
		}
		if remote == "" {
			remotes, queryErr := query("remote")
			if queryErr != nil {
				return nil, []string{"default Git remote"}, nil
			}
			for _, name := range strings.Split(remotes, "\n") {
				if strings.TrimSpace(name) == "origin" {
					remote = "origin"
				}
			}
			if remote == "" {
				return nil, []string{"default Git remote"}, nil
			}
		}
	}
	// Separate helper destinations, network URLs, and local directory targets.
	// Unknown helpers remain unresolved for one-time review.
	if gitHelperDestination(remote) {
		return nil, []string{"Git remote helper"}, nil
	}
	if origin, ok := NormalizeRemoteOrigin(remote); ok {
		// Query configuration only: remote get-url does not accept a remote
		// defined solely through -c on all supported Git versions.
		config, err := query("config", "--null", "--get-regexp", `^url\..*\.(insteadof|pushinsteadof)$`)
		var exitErr *exec.ExitError
		if err != nil && !(errors.As(err, &exitErr) && exitErr.ExitCode() == 1) {
			return []string{origin}, []string{"Git URL rewrite configuration"}, nil
		}
		rewritten := rewriteGitPushURL(remote, config)
		if normalized, valid := NormalizeRemoteOrigin(rewritten); valid && !gitHelperDestination(rewritten) {
			return []string{normalized}, nil, nil
		}
		if target, valid := gitLocalTarget(rewritten, cwd); valid {
			return nil, nil, []string{target}
		}
		return nil, []string{"Git rewritten destination"}, nil
	}
	if strings.HasPrefix(remote, "-") || strings.ContainsAny(remote, "\r\n") {
		return nil, []string{"Git remote"}, nil
	}
	if strings.ContainsAny(remote, `/\`) || remote == "." {
		if target, ok := gitLocalTarget(remote, cwd); ok {
			return nil, nil, []string{target}
		}
	}
	output, err := query("remote", "get-url", "--push", "--all", remote)
	if err != nil {
		return nil, []string{remote}, nil
	}
	for _, target := range strings.Split(output, "\n") {
		if origin, ok := NormalizeRemoteOrigin(strings.TrimSpace(target)); ok && !gitHelperDestination(target) {
			origins = append(origins, origin)
		} else if target, ok := gitLocalTarget(strings.TrimSpace(target), cwd); ok {
			local = append(local, target)
		} else {
			unresolved = append(unresolved, "Git destination")
		}
	}
	if len(origins) == 0 && len(unresolved) == 0 && len(local) == 0 {
		unresolved = []string{remote}
	}
	return NormalizeRemoteOrigins(origins), unresolved, local
}

func gitHelperDestination(raw string) bool {
	prefix, _, ok := strings.Cut(raw, "::")
	return ok && prefix != "" && !strings.ContainsAny(prefix, `/\@[]:`)
}

type gitQueryOutput struct {
	bytes.Buffer
	limit int
}

func (b *gitQueryOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, fmt.Errorf("Git query output exceeded limit")
	}
	return b.Buffer.Write(p)
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

// Local remotes are directory targets, not network origins or helper names.
func gitLocalTarget(raw, cwd string) (string, bool) {
	if strings.HasPrefix(raw, "file://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", false
		}
		raw = u.Path
		if u.Host != "" && u.Host != "localhost" {
			raw = "//" + u.Host + raw
		}
		if len(raw) > 2 && raw[0] == '/' && raw[2] == ':' {
			raw = raw[1:]
		}
	}
	if raw == "" || strings.HasPrefix(raw, "-") || gitHelperDestination(raw) || strings.Contains(raw, "://") || strings.ContainsAny(raw, "\r\n$`") {
		return "", false
	}
	if strings.Contains(raw, ":") && !filepath.IsAbs(raw) {
		return "", false
	}
	if !filepath.IsAbs(raw) {
		raw = filepath.Join(cwd, raw)
	}
	resolved, err := pathutil.ResolveThroughExistingParent(raw)
	return resolved, err == nil
}

// Git chooses the longest pushInsteadOf prefix, falling back to insteadOf
// only when no push rewrite matches. Rewriting is not recursive.
func rewriteGitPushURL(raw, config string) string {
	for _, suffix := range []string{".pushinsteadof", ".insteadof"} {
		longest, replacement := "", ""
		matched := false
		for _, entry := range strings.Split(config, "\x00") {
			key, prefix, ok := strings.Cut(entry, "\n")
			if !ok || !strings.HasPrefix(key, "url.") || !strings.HasSuffix(key, suffix) {
				continue
			}
			if strings.HasPrefix(raw, prefix) && (!matched || len(prefix) > len(longest)) {
				longest = prefix
				matched = true
				replacement = strings.TrimSuffix(strings.TrimPrefix(key, "url."), suffix)
			}
		}
		if matched {
			return replacement + strings.TrimPrefix(raw, longest)
		}
	}
	return raw
}
