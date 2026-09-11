package app

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/pathutil"
	"github.com/stretchr/testify/require"
)

// This file is the conformance suite for the shell assessment pipeline
// (assessShellCommand): tool + command -> effect, known directories, dynamic
// targets. It exists so the pipeline can be split into files and its two path
// fact sources (approval's AST/IR analysis and the literal-extraction fallback)
// can be unified without changing behaviour silently.
//
// Expectations are written as exact facts rather than "contains" checks: any
// change to the merged result has to be made here on purpose. Directory
// comparison normalizes separators and sorts, because pathutil emits the host
// separator and known dirs are an unordered set.

// conformanceMods returns a model whose classifier leg is a deterministic stub.
// The stub is only consulted when the deterministic analysis cannot prove an
// effect, which is what makes the classifier-merge cases meaningful.
func conformanceMods(ext string) *Mods {
	return &Mods{
		Config: &Config{},
		shellAnalyzer: func(string, string) approval.CommandAssessment {
			return approval.CommandAssessment{
				Effect:    approval.EffectWrite,
				KnownDirs: []string{filepath.Join(ext, "from-classifier")},
				Reason:    "classifier said write",
			}
		},
	}
}

type conformanceDirs struct {
	ws   string
	ext  string
	safe string
	home string
}

func newConformanceDirs(t *testing.T) conformanceDirs {
	t.Helper()
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	return conformanceDirs{
		ws:   filepath.Clean(t.TempDir()),
		ext:  filepath.Clean(t.TempDir()),
		safe: filepath.Clean(t.TempDir()),
		home: filepath.Clean(home),
	}
}

// expand replaces the table's directory placeholders.
func (d conformanceDirs) expand(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ReplaceAll(value, "<WS>", d.ws)
		value = strings.ReplaceAll(value, "<EXT>", d.ext)
		value = strings.ReplaceAll(value, "<SAFE>", d.safe)
		value = strings.ReplaceAll(value, "<HOME>", d.home)
		out = append(out, value)
	}
	return out
}

// slashSet normalizes a fact list for comparison: separators are unified and
// order is dropped. Known dirs are a set, and pathutil emits the host
// separator.
func slashSet(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, filepath.ToSlash(filepath.Clean(v)))
	}
	sort.Strings(out)
	return out
}

type conformanceCase struct {
	name     string
	tool     string
	command  string
	want     approval.CommandEffect
	wantDirs []string
	wantDyn  []string
	note     string
}

// posixConformanceCases covers the POSIX dialect. It runs on every platform:
// the POSIX analyzer is platform independent and the pipeline takes its
// flavour as a parameter, so Windows no longer skips POSIX coverage.
func posixConformanceCases() []conformanceCase {
	return []conformanceCase{
		{
			name: "workspace read falls back to workspace scope", tool: "shell_run",
			command: "ls -la", want: approval.EffectRead,
			wantDirs: []string{"<WS>"},
		},
		{
			name: "workspace file read falls back to workspace scope", tool: "shell_run",
			command: "cat README.md", want: approval.EffectRead,
			wantDirs: []string{"<WS>"},
		},
		{
			name: "absolute external read keeps the file fact", tool: "shell_run",
			command: "cat /etc/passwd", want: approval.EffectRead,
			wantDirs: []string{"/etc/passwd"},
		},
		{
			name: "external read keeps the file fact", tool: "shell_run",
			command: "cat <EXT>/secret.txt", want: approval.EffectRead,
			wantDirs: []string{"<EXT>/secret.txt"},
		},
		{
			name: "relative write keeps the literal dot", tool: "shell_run",
			command: "touch out.txt", want: approval.EffectWrite,
			wantDirs: []string{"."},
		},
		{
			name: "redirect write records target and parent", tool: "shell_run",
			command: "echo hi > <EXT>/out.txt", want: approval.EffectWrite,
			wantDirs: []string{"<EXT>", "<EXT>/out.txt"},
		},
		{
			name: "single external operand is recorded twice via both sources", tool: "shell_run",
			command: "rm -rf <SAFE>/x", want: approval.EffectWrite,
			wantDirs: []string{"<SAFE>/x", "<SAFE>/x"},
			note:     "duplicate fact in two separator styles: AST raw token plus normalized fallback",
		},
		{
			name: "pipeline reads both operands", tool: "shell_run",
			command: "cat <EXT>/a <EXT>/b | grep x", want: approval.EffectRead,
			wantDirs: []string{"<EXT>/a", "<EXT>/b"},
		},
		{
			name: "find delete is a write of the searched root", tool: "shell_run",
			command: "find <EXT> -name '*.log' -delete", want: approval.EffectWrite,
			wantDirs: []string{"<EXT>"},
		},
		{
			name: "single quoted path with a space is recovered", tool: "shell_run",
			command: "cat '<EXT>/quoted path'", want: approval.EffectRead,
			wantDirs: []string{"<EXT>/quoted path"},
		},
		{
			name: "heredoc body is not a path fact", tool: "shell_run",
			command: "cat <<'EOF'\n<EXT>/inside\nEOF", want: approval.EffectRead,
			wantDirs: []string{"<WS>"},
			note:     "the heredoc body must not leak an external fact",
		},
		{
			name: "git status reads the execution context", tool: "shell_run",
			command: "git status", want: approval.EffectRead,
			wantDirs: []string{"<WS>"},
		},
		{
			name: "du of an external directory", tool: "shell_run",
			command: "du -sh <EXT>", want: approval.EffectRead,
			wantDirs: []string{"<EXT>"},
		},
		{
			name: "sort -o is a write with both operands", tool: "shell_run",
			command: "sort <EXT>/in.txt -o <EXT>/out.txt", want: approval.EffectWrite,
			wantDirs: []string{"<EXT>", "<EXT>/in.txt", "<EXT>/out.txt"},
		},
		{
			name: "home expansion is an external read fact", tool: "shell_run",
			command: "cat <HOME>/Downloads/secret.txt", want: approval.EffectRead,
			wantDirs: []string{"<HOME>/Downloads/secret.txt"},
		},
		{
			name: "environment expansion is an external read fact", tool: "shell_run",
			command: "echo $HOME/x", want: approval.EffectRead,
			wantDirs: []string{"<HOME>/x"},
		},
		{
			name: "unknown command merges classifier dirs with literal facts", tool: "shell_run",
			command: "unknowncmd --flag <EXT>/f", want: approval.EffectWrite,
			wantDirs: []string{"<EXT>/f", "<EXT>/from-classifier"},
			note:     "two sources merged: normalized literal plus raw classifier dir",
		},
		{
			name: "unknown command with a bare tilde keeps both sources", tool: "shell_run",
			command: "unknowncmd ~/x", want: approval.EffectWrite,
			wantDirs: []string{"<HOME>/x", "<EXT>/from-classifier"},
		},
		{
			name: "UNC-style external read is not collapsed into the workspace", tool: "shell_run",
			command: `cat \\server\share\f`, want: approval.EffectRead,
			wantDirs: []string{`\\server\share\f`},
			note:     "regression: this fact carries no explicit-path syntax and used to be replaced by <WS>",
		},
		{
			name: "other-user home read keeps the unresolved home fact", tool: "shell_run",
			command: "cat ~root", want: approval.EffectRead,
			wantDirs: []string{"~root"},
			note:     "~root carries no explicit-path syntax either; the literal fallback supplies it",
		},
	}
}

func TestShellAssessmentConformancePOSIX(t *testing.T) {
	dirs := newConformanceDirs(t)
	for _, tc := range posixConformanceCases() {
		t.Run(tc.name, func(t *testing.T) {
			command := dirs.expand(tc.command)[0]
			got := conformanceMods(dirs.ext).assessShellCommand(tc.tool, pathutil.FlavorPOSIX, command, nil, dirs.ws)

			require.Equal(t, tc.want, got.Effect, "effect for %q", command)
			require.Equal(t, slashSet(dirs.expand(tc.wantDirs...)), slashSet(got.KnownDirs),
				"known dirs for %q (%s)", command, tc.note)
			require.Equal(t, slashSet(dirs.expand(tc.wantDyn...)), slashSet(got.DynamicTargets),
				"dynamic targets for %q", command)
		})
	}
}

// TestShellAssessmentKeepsAuthoritativePathFacts is the invariant that makes
// approval the authoritative source of shell path facts: a directory that
// approval's own analysis reports as external must still be covered by the
// merged result. The literal-extraction fallback may only add, never replace.
//
// Workspace and safe-directory targets are exempt: neither needs a review
// scope, so the read branch may replace them with the workspace fallback.
func TestShellAssessmentKeepsAuthoritativePathFacts(t *testing.T) {
	dirs := newConformanceDirs(t)
	m := conformanceMods(dirs.ext)

	for _, tc := range posixConformanceCases() {
		t.Run(tc.name, func(t *testing.T) {
			command := dirs.expand(tc.command)[0]
			authoritative := approval.AssessShellStaticWithContext(command, true, approval.ReadOnlyCommandPolicy{}, dirs.ws)
			merged := m.assessShellCommand(tc.tool, pathutil.FlavorPOSIX, command, nil, dirs.ws)

			for _, dir := range authoritative.KnownDirs {
				if pathutil.Location(dir, dirs.ws, m.safeDirs()) != pathutil.LocationExternal {
					continue
				}
				require.Truef(t, conformanceCoversDir(merged.KnownDirs, dir),
					"authoritative external dir %q from approval is missing from merged facts %v for %q",
					dir, merged.KnownDirs, command)
			}
		})
	}
}

// TestShellAssessmentScriptExecutionConformance pins the one interpreter shape
// that keeps a script payload reviewable, deliberately separate from the fact
// tables above: the merged effect stays unknown, the script path is never
// reported as a directory fact, and only a readable text file inside the
// workspace earns a verified binding.
func TestShellAssessmentScriptExecutionConformance(t *testing.T) {
	dirs := newConformanceDirs(t)
	scriptDir := filepath.Join(dirs.ws, "tools")
	require.NoError(t, os.MkdirAll(scriptDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(scriptDir, "check.py"), []byte("print('ok')\n"), 0o600))
	m := &Mods{
		Config: testConfigForWorkspace(dirs.ws),
		shellAnalyzer: func(string, string) approval.CommandAssessment {
			return approval.UnknownCommandAssessment()
		},
	}

	got := m.assessShellCommand("shell_run", pathutil.FlavorPOSIX, "python3 tools/check.py", nil, dirs.ws)
	require.Equal(t, approval.EffectUnknown, got.Effect)
	require.Empty(t, slashSet(got.KnownDirs))
	require.Empty(t, slashSet(got.DynamicTargets))
	require.True(t, got.RequiresSimplification(), "the structural constraint still holds")
	facts := got.Reviewability.ScriptExecution
	require.NotNil(t, facts)
	require.True(t, facts.Verified())
	require.Equal(t, filepath.Clean(filepath.Join(scriptDir, "check.py")), filepath.Clean(facts.ResolvedPath))

	for _, tc := range []struct{ command, note string }{
		{command: "python3 -c 'print(1)'", note: "inline source"},
		{command: "python3 tools/check.py extra", note: "extra argument"},
	} {
		t.Run(tc.note, func(t *testing.T) {
			out := m.assessShellCommand("shell_run", pathutil.FlavorPOSIX, tc.command, nil, dirs.ws)
			require.Nil(t, out.Reviewability.ScriptExecution)
		})
	}

	t.Run("external script stays unverified", func(t *testing.T) {
		command := "python3 " + filepath.ToSlash(filepath.Join(dirs.ext, "check.py"))
		out := m.assessShellCommand("shell_run", pathutil.FlavorPOSIX, command, nil, dirs.ws)
		require.NotNil(t, out.Reviewability.ScriptExecution)
		require.False(t, out.Reviewability.ScriptExecution.Verified())
	})
}

// conformanceCoversDir reports whether values contains the same directory as
// want, using the same notion of directory identity the review layer uses
// (pathutil.NormalizeDirs dedupes by its platform comparison key).
func conformanceCoversDir(values []string, want string) bool {
	for _, v := range values {
		if len(pathutil.NormalizeDirs([]string{v, want}, pathutil.DefaultOptions("", pathutil.FlavorPOSIX))) <= 1 {
			return true
		}
	}
	return false
}
