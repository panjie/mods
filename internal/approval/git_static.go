package approval

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ArgvStaticContext carries deterministic process facts that are not encoded
// in argv. EnvironmentKeys must include keys added by the tool invocation;
// inherited Git repository-selection variables are inspected directly.
type ArgvStaticContext struct {
	Cwd             string
	EnvironmentKeys []string
}

var gitWorkspaceWriteSubcommands = map[string]bool{
	"add":         true,
	"checkout":    true,
	"cherry-pick": true,
	"clean":       true,
	"commit":      true,
	"merge":       true,
	"mv":          true,
	"rebase":      true,
	"reset":       true,
	"restore":     true,
	"revert":      true,
	"rm":          true,
	"stash":       true,
	"switch":      true,
}

var gitRepositoryEnvironmentNames = map[string]bool{
	"GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
	"GIT_CEILING_DIRECTORIES":          true,
	"GIT_COMMON_DIR":                   true,
	"GIT_DIR":                          true,
	"GIT_DISCOVERY_ACROSS_FILESYSTEM":  true,
	"GIT_INDEX_FILE":                   true,
	"GIT_OBJECT_DIRECTORY":             true,
	"GIT_WORK_TREE":                    true,
}

// assessGitArgvStatic recognizes a deliberately narrow set of Git operations
// whose built-in filesystem effects stay within the selected repository. The
// returned directories include both the worktree and Git administrative
// storage; linked worktrees therefore expose an external common Git directory
// instead of being mistaken for workspace-only writes.
func assessGitArgvStatic(tokens []string, posix bool, context ArgvStaticContext) (CommandAssessment, bool) {
	if len(tokens) < 2 || normalizedGitProgram(tokens[0], posix) != "git" {
		return CommandAssessment{}, false
	}
	// Supporting global options requires applying Git's option-order and
	// repository-selection semantics. Keep them on the normal fail-closed path
	// for now, especially -C, -c, --git-dir, and --work-tree.
	if strings.HasPrefix(tokens[1], "-") {
		return CommandAssessment{}, false
	}
	subcommand := strings.ToLower(strings.TrimSpace(tokens[1]))
	if !gitWorkspaceWriteSubcommands[subcommand] || gitInvocationHasUnsupportedIndirection(tokens[2:]) {
		return CommandAssessment{}, false
	}
	if gitRepositoryEnvironmentOverridden(context.EnvironmentKeys) {
		return CommandAssessment{}, false
	}
	dirs, err := discoverGitWriteDirs(context.Cwd)
	if err != nil {
		return CommandAssessment{}, false
	}
	return CommandAssessment{
		Effect:    EffectWrite,
		KnownDirs: dirs,
		Reason:    fmt.Sprintf("git %s mutates repository state (static argv analysis)", subcommand),
	}, true
}

func normalizedGitProgram(program string, posix bool) string {
	if !posix {
		return normalizePowerShellCommandName(program)
	}
	return strings.TrimSpace(program)
}

func gitInvocationHasUnsupportedIndirection(args []string) bool {
	for _, arg := range args {
		name, _, _ := strings.Cut(arg, "=")
		switch name {
		case "-h", "--help", "--pathspec-from-file", "--pathspec-file-nul":
			return true
		}
	}
	return false
}

func gitRepositoryEnvironmentOverridden(extraKeys []string) bool {
	for _, entry := range os.Environ() {
		name, value, ok := strings.Cut(entry, "=")
		if ok && value != "" && gitRepositoryEnvironmentKey(name) {
			return true
		}
	}
	for _, name := range extraKeys {
		if gitRepositoryEnvironmentKey(name) {
			return true
		}
	}
	return false
}

func gitRepositoryEnvironmentKey(name string) bool {
	name = strings.ToUpper(strings.TrimSpace(name))
	return gitRepositoryEnvironmentNames[name] || name == "GIT_CONFIG_COUNT" ||
		strings.HasPrefix(name, "GIT_CONFIG_KEY_") || strings.HasPrefix(name, "GIT_CONFIG_VALUE_")
}

func discoverGitWriteDirs(cwd string) ([]string, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return nil, fmt.Errorf("git cwd is empty")
	}
	current, err := filepath.Abs(cwd)
	if err != nil {
		return nil, err
	}
	current = filepath.Clean(current)
	if resolved, resolveErr := filepath.EvalSymlinks(current); resolveErr == nil {
		current = resolved
	}

	for {
		marker := filepath.Join(current, ".git")
		info, statErr := os.Stat(marker)
		switch {
		case statErr == nil && info.IsDir():
			return gitWriteDirs(current, marker)
		case statErr == nil && info.Mode().IsRegular():
			gitDir, parseErr := parseGitDirFile(marker)
			if parseErr != nil {
				return nil, parseErr
			}
			return gitWriteDirs(current, gitDir)
		case statErr != nil && !os.IsNotExist(statErr):
			return nil, statErr
		}

		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return nil, fmt.Errorf("git repository not found")
}

func parseGitDirFile(marker string) (string, error) {
	line, err := readGitMetadataLine(marker)
	if err != nil {
		return "", err
	}
	const prefix = "gitdir:"
	if len(line) < len(prefix) || !strings.EqualFold(line[:len(prefix)], prefix) {
		return "", fmt.Errorf("invalid gitdir file %q", marker)
	}
	gitDir := strings.TrimSpace(line[len(prefix):])
	if gitDir == "" {
		return "", fmt.Errorf("empty gitdir in %q", marker)
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(filepath.Dir(marker), gitDir)
	}
	return canonicalExistingDir(gitDir)
}

func gitWriteDirs(worktree, gitDir string) ([]string, error) {
	worktree, err := canonicalExistingDir(worktree)
	if err != nil {
		return nil, err
	}
	gitDir, err = canonicalExistingDir(gitDir)
	if err != nil {
		return nil, err
	}
	dirs := []string{worktree, gitDir}
	commonFile := filepath.Join(gitDir, "commondir")
	if commonDir, readErr := readGitMetadataLine(commonFile); readErr == nil {
		if commonDir == "" {
			return nil, fmt.Errorf("empty commondir in %q", commonFile)
		}
		if !filepath.IsAbs(commonDir) {
			commonDir = filepath.Join(gitDir, commonDir)
		}
		commonDir, err = canonicalExistingDir(commonDir)
		if err != nil {
			return nil, err
		}
		dirs = append(dirs, commonDir)
	} else if !os.IsNotExist(readErr) {
		return nil, readErr
	}
	return dedupeSorted(dirs), nil
}

func readGitMetadataLine(name string) (string, error) {
	file, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer file.Close() //nolint:errcheck
	const maxGitMetadataBytes = 4096
	data, err := io.ReadAll(io.LimitReader(file, maxGitMetadataBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxGitMetadataBytes {
		return "", fmt.Errorf("git metadata file %q is too large", name)
	}
	return strings.TrimSpace(strings.SplitN(string(data), "\n", 2)[0]), nil
}

func canonicalExistingDir(value string) (string, error) {
	value, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	value = filepath.Clean(value)
	info, err := os.Stat(value)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%q is not a directory", value)
	}
	resolved, err := filepath.EvalSymlinks(value)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}
