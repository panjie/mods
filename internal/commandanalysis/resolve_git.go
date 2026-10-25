package commandanalysis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/panjie/mods/internal/pathutil"
	"mvdan.cc/sh/v3/syntax"
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
	flavor := pathutil.FlavorPOSIX
	if !posix {
		flavor = pathutil.FlavorPowerShell
	}
	return m.resolveParsedGitPushOrigins(m.parse(command, flavor), baseDir)
}
func (m *analysisRun) resolveParsedGitPushOrigins(p *parsedCommand, baseDir string) (origins, unresolved, local []string) {
	invocations, ok := p.invocations()
	if !ok {
		return nil, nil, nil
	}
	// Only a straight-line AST without assignments, redirections or nested
	// execution can use the audited context-preserving read evidence.
	stableSequence := p.file != nil
	if stableSequence {
		syntax.Walk(p.file, func(n syntax.Node) bool {
			switch v := n.(type) {
			case *syntax.Stmt:
				if v.Background || v.Coprocess || len(v.Redirs) > 0 {
					stableSequence = false
				}
				switch v.Cmd.(type) {
				case *syntax.CallExpr:
				default:
					stableSequence = false
				}
			case *syntax.CallExpr:
				if len(v.Assigns) > 0 {
					stableSequence = false
				}
			}
			return true
		})
	}
	contextChanged := false
	for _, inv := range invocations {
		if !isGitProgram(inv.Program) {
			if stableSequence && inv.Literal && p.semantics != nil {
				f := p.semantics.byID[inv.NodeID]
				if f != nil && f.BuiltinRead && f.ContextStableRead {
					continue
				}
			}
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
			read := false
			if p.semantics != nil && p.semantics.byID[inv.NodeID] != nil {
				read = p.semantics.byID[inv.NodeID].BuiltinRead
			} else {
				read = catalogTokens(append([]string{"git"}, inv.Args...), nativeSpace, ReadOnlyCommandPolicy{}).BuiltinRead
			}
			if !read {
				contextChanged = true
			}
		}
	}
	return NormalizeRemoteOrigins(origins), unresolved, local
}

func (m *analysisRun) resolveGitPushArgvOrigins(args []string, cwd string) (origins, unresolved, local []string) {
	rest, dir, ok := bindGitGlobals(args, cwd)
	if !ok {
		if gitArgsArePush(args) {
			return nil, []string{"Git global options"}, nil
		}
		return nil, nil, nil
	}
	if len(rest) > 0 && rest[0] == "push" {
		return m.resolveGitRemoteOrigins(rest[1:], dir)
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
	for _, key := range m.environmentKeys {
		if gitRepositoryEnvironmentKey(key) || strings.EqualFold(key, "PATH") {
			return nil, []string{"Git environment"}, nil
		}
	}
	ctx := context.Background()
	if m != nil && m.ctx != nil {
		ctx = m.ctx
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	query := func(kind GitQueryKind, name string) (string, error) { return m.gitQuery(ctx, kind, cwd, name) }
	if _, err := query(GitExecutable, ""); err != nil {
		return nil, []string{"Git executable"}, nil
	}

	if remote == "" {
		branch, _ := query(GitBranch, "")
		if branch != "" {
			remote, _ = query(GitBranchPushRemote, branch)
		}
		if remote == "" {
			remote, _ = query(GitPushDefault, "")
		}
		if remote == "" && branch != "" {
			remote, _ = query(GitBranchRemote, branch)
		}
		if remote == "" {
			remotes, queryErr := query(GitRemotes, "")
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
		config, err := query(GitURLRewrites, "")
		var exitErr *exec.ExitError
		if err != nil && !(errors.As(err, &exitErr) && exitErr.ExitCode() == 1) {
			return []string{origin}, []string{"Git URL rewrite configuration"}, nil
		}
		rewritten := rewriteGitPushURL(remote, config)
		if normalized, valid := NormalizeRemoteOrigin(rewritten); valid && !gitHelperDestination(rewritten) {
			return []string{normalized}, nil, nil
		}
		if target, valid := m.gitLocalTarget(rewritten, cwd); valid {
			return nil, nil, []string{target}
		}
		return nil, []string{"Git rewritten destination"}, nil
	}
	if strings.HasPrefix(remote, "-") || strings.ContainsAny(remote, "\r\n") {
		return nil, []string{"Git remote"}, nil
	}
	if strings.ContainsAny(remote, `/\`) || remote == "." {
		if target, ok := m.gitLocalTarget(remote, cwd); ok {
			return nil, nil, []string{target}
		}
	}
	output, err := query(GitPushURLs, remote)
	if err != nil {
		return nil, []string{remote}, nil
	}
	for _, target := range strings.Split(output, "\n") {
		if origin, ok := NormalizeRemoteOrigin(strings.TrimSpace(target)); ok && !gitHelperDestination(target) {
			origins = append(origins, origin)
		} else if target, ok := m.gitLocalTarget(strings.TrimSpace(target), cwd); ok {
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
		return 0, fmt.Errorf("%w: Git query output", errAnalysisBudget)
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
	return NewEngine(Settings{}, Dependencies{}).run(context.Background()).gitLocalTarget(raw, cwd)
}
func (m *analysisRun) gitLocalTarget(raw, cwd string) (string, bool) {
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
	resolved, err := m.resolvePath(raw)
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
