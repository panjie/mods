package commandanalysis

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/panjie/mods/internal/commandtrace"
	"github.com/panjie/mods/internal/pathutil"
)

const (
	analysisTimeout    = 15 * time.Second
	maxAnalysisQueries = 64
	maxQueryOutput     = 64 << 10
	maxAnalysisOutput  = 256 << 10
)

func (r *analysisRun) beginQuery() error {
	if err := r.ctx.Err(); err != nil {
		return err
	}
	r.queries++
	if r.queries > maxAnalysisQueries {
		r.failure = errAnalysisBudget
		return r.failure
	}
	return nil
}
func (r *analysisRun) accountOutput(n int) error {
	r.outputBytes += n
	if n > maxQueryOutput || r.outputBytes > maxAnalysisOutput {
		r.failure = errAnalysisBudget
		return r.failure
	}
	return r.ctx.Err()
}
func (r *analysisRun) options(cwd string, flavor pathutil.Flavor) pathutil.Options {
	env := make(map[string]string, len(r.environment))
	for _, entry := range r.environment {
		k, v, ok := strings.Cut(entry, "=")
		if ok {
			env[k] = v
		}
	}
	// Match pathutil's inherited case handling while keeping call shadows absent.
	for k := range env {
		if r.shadowed(k, flavor) {
			delete(env, k)
		}
	}
	// Do not let pathutil's home aliases substitute a different inherited
	// variable for a HOME value hidden by the invocation's secret environment.
	if r.shadowed("HOME", flavor) || r.shadowed("USERPROFILE", flavor) {
		for key := range env {
			switch strings.ToUpper(key) {
			case "HOME", "USERPROFILE", "HOMEDRIVE", "HOMEPATH":
				delete(env, key)
			}
		}
	}
	home := env["HOME"]
	if runtime.GOOS == "windows" {
		home = env["USERPROFILE"]
		if home == "" {
			home = env["HOME"]
		}
	}
	if flavor == pathutil.FlavorPOSIX {
		env["PWD"] = cwd
	}
	return pathutil.Options{WorkingDir: cwd, Flavor: flavor, Env: env, Home: home}
}
func (r *analysisRun) shadowed(name string, flavor pathutil.Flavor) bool {
	for _, key := range r.environmentKeys {
		if key == name || (flavor == pathutil.FlavorPowerShell && strings.EqualFold(key, name)) {
			return true
		}
	}
	return false
}
func (r *analysisRun) resolvePath(path string) (string, error) {
	if err := r.beginQuery(); err != nil {
		return "", err
	}
	value, err := r.dependencies.Paths.Resolve(r.ctx, path)
	if errors.Is(err, errAnalysisBudget) {
		r.failure = err
	}
	if bound := r.accountOutput(len(value)); bound != nil {
		return "", bound
	}
	return value, err
}
func (r *analysisRun) repositoryDirs(cwd string) ([]string, error) {
	if err := r.beginQuery(); err != nil {
		return nil, err
	}
	dirs, err := r.dependencies.Paths.RepositoryDirs(r.ctx, cwd)
	n := 0
	for _, v := range dirs {
		n += len(v)
	}
	if bound := r.accountOutput(n); bound != nil {
		return nil, bound
	}
	return slices.Clone(dirs), err
}
func (r *analysisRun) gitQuery(ctx context.Context, kind GitQueryKind, cwd, name string) (string, error) {
	if err := r.beginQuery(); err != nil {
		return "", err
	}
	value, err := r.dependencies.Git.Query(ctx, GitQuery{Kind: kind, Cwd: cwd, Name: name, Environment: slices.Clone(r.environment), UntrustedProgramDirs: slices.Clone(r.programDirs)})
	if errors.Is(err, errAnalysisBudget) {
		r.failure = err
	}
	if bound := r.accountOutput(len(value)); bound != nil {
		return "", bound
	}
	return value, err
}

type hostPaths struct{}

func (hostPaths) Resolve(ctx context.Context, p string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return pathutil.ResolveThroughExistingParent(p)
}
func (hostPaths) RepositoryDirs(ctx context.Context, cwd string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return discoverGitWriteDirs(cwd)
}

type hostGit struct{}

func (hostGit) Query(ctx context.Context, q GitQuery) (string, error) {
	var args []string
	switch q.Kind {
	case GitExecutable:
		// Validate the helper identity even when a literal local remote needs no subprocess.
	case GitBranch:
		args = []string{"symbolic-ref", "--quiet", "--short", "HEAD"}
	case GitBranchPushRemote:
		args = []string{"config", "--get", "branch." + q.Name + ".pushRemote"}
	case GitPushDefault:
		args = []string{"config", "--get", "remote.pushDefault"}
	case GitBranchRemote:
		args = []string{"config", "--get", "branch." + q.Name + ".remote"}
	case GitRemotes:
		args = []string{"remote"}
	case GitURLRewrites:
		args = []string{"config", "--null", "--get-regexp", `^url\..*\.(insteadof|pushinsteadof)$`}
	case GitPushURLs:
		if q.Name == "" || strings.HasPrefix(q.Name, "-") || strings.ContainsAny(q.Name, "\r\n") {
			return "", errors.New("invalid remote name")
		}
		args = []string{"remote", "get-url", "--push", "--all", q.Name}
	default:
		return "", errors.New("unsupported Git query")
	}
	git, err := helperPath("git", q.Environment)
	if err != nil {
		return "", err
	}
	if location := pathutil.Location(git, q.Cwd, q.UntrustedProgramDirs); location == pathutil.LocationWorkingDir || location == pathutil.LocationSafe {
		return "", errors.New("untrusted Git executable")
	}
	if q.Kind == GitExecutable {
		return git, nil
	}
	commandtrace.GitQuery()
	cmd := exec.CommandContext(ctx, git, append([]string{"-C", q.Cwd}, args...)...)
	cmd.Env = q.Environment
	cmd.WaitDelay = 100 * time.Millisecond
	out := &gitQueryOutput{limit: maxQueryOutput}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}

func (r *analysisRun) finish(result Result) (Result, error) {
	if err := r.ctx.Err(); err != nil {
		result.Effect = EffectUnknown
		result.Evidence = EvidenceUnknown
		result.AddGap(GapAnalysisCancelled)
		return result, err
	}
	if r.failure != nil {
		result.Effect = EffectUnknown
		result.Evidence = EvidenceUnknown
		result.AddGap(GapAnalysisBudget)
		result.Reason = fmt.Sprint(r.failure)
	}
	return result.Clone(), nil
}

func (r *analysisRun) resolveAutomatics(targets []string, assigned map[string]bool) map[string]string {
	result := map[string]string{}
	values := map[string]string{}
	resolver := r.dependencies.Automatics
	if resolver == nil {
		resolver = hostAutomatics{}
	}
	for _, target := range targets {
		ref := strings.TrimSpace(target)
		match := probeEligibleRef.FindStringSubmatch(ref)
		if match == nil || assigned[strings.ToLower(match[1])] {
			continue
		}
		key := strings.ToLower(match[1]) + "." + strings.ToLower(match[2])
		if v, ok := values[key]; ok {
			result[ref] = v
			continue
		}
		if r.beginQuery() != nil {
			break
		}
		value, err := resolver.Resolve(r.ctx, strings.ToLower(match[1]), match[2], slices.Clone(r.environment))
		if errors.Is(err, errAnalysisBudget) {
			r.failure = err
		}
		if bound := r.accountOutput(len(value)); bound != nil {
			break
		}
		if err == nil && validProbeValue(value) {
			values[key] = value
			result[ref] = value
		}
	}
	return result
}

type hostAutomatics struct{}

func (hostAutomatics) Resolve(ctx context.Context, name, property string, env []string) (string, error) {
	expression := "$" + name
	if property != "" {
		expression += "." + property
	}
	if !probeEligibleRef.MatchString(expression) {
		return "", errors.New("unsupported automatic variable")
	}
	shell, err := powerShellHost(env)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", "Write-Output "+expression)
	cmd.Env = env
	cmd.WaitDelay = 100 * time.Millisecond
	out := &gitQueryOutput{limit: maxQueryOutput}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}

// Resolve against the captured PATH, never the live process environment.
func helperPath(name string, env []string) (string, error) {
	path := ""
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(key, "PATH") {
			path = value
		}
	}
	names := []string{name}
	if runtime.GOOS == "windows" {
		names = []string{name + ".exe", name + ".com"}
	}
	for _, dir := range filepath.SplitList(path) {
		if !filepath.IsAbs(dir) {
			continue
		}
		for _, candidate := range names {
			full := filepath.Join(dir, candidate)
			info, err := os.Stat(full)
			if err == nil && !info.IsDir() && (runtime.GOOS == "windows" || info.Mode()&0111 != 0) {
				return full, nil
			}
		}
	}
	return "", fmt.Errorf("helper %s unavailable in captured PATH", name)
}

func powerShellHost(env []string) (string, error) {
	if runtime.GOOS != "windows" {
		return "", errors.New("PowerShell host requires Windows")
	}
	if shell, err := helperPath("pwsh", env); err == nil {
		return shell, nil
	}
	return helperPath("powershell", env)
}

func (hostPaths) CreationParent(ctx context.Context, p string) (string, error) {
	p = filepath.Dir(p)
	for depth := 0; depth < maxAnalysisDepth; depth++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if _, err := os.Lstat(p); err == nil {
			return pathutil.ResolveThroughExistingParent(p)
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(p)
		if parent == p {
			return "", errors.New("no existing creation parent")
		}
		p = parent
	}
	return "", errAnalysisBudget
}
func (r *analysisRun) creationParent(path string) (string, error) {
	if err := r.beginQuery(); err != nil {
		return "", err
	}
	resolver, ok := r.dependencies.Paths.(CreationParentResolver)
	if !ok {
		return "", errors.New("creation parent evidence unavailable")
	}
	value, err := resolver.CreationParent(r.ctx, path)
	if errors.Is(err, errAnalysisBudget) {
		r.failure = err
	}
	if bound := r.accountOutput(len(value)); bound != nil {
		return "", bound
	}
	return value, err
}
