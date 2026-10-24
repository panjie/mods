package app

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/commandtrace"
	"github.com/panjie/mods/internal/pathutil"
	"github.com/stretchr/testify/require"
)

var updateCommandBaseline = flag.Bool("update-command-baseline", false, "record command assessment facts; never changes safety expectations")

// The corpus separates observed compatibility facts from manually reviewed safety
// expectations. Updating a snapshot cannot turn a required review into an allow.
type commandBaselineCase struct {
	ID       string   `json:"id"`
	Mode     string   `json:"mode"`
	Command  string   `json:"command,omitempty"`
	Program  string   `json:"program,omitempty"`
	Args     []string `json:"args,omitempty"`
	Category string   `json:"category"`
	Source   string   `json:"source"`
	// Required decisions are semantic assertions, not generated observations.
	Required string   `json:"required,omitempty"`
	Grant    []string `json:"grant,omitempty"`
	Shadow   []string `json:"shadow,omitempty"`
	Origins  []string `json:"required_origins,omitempty"`
}

type commandBaselineFacts struct {
	Effect           approval.CommandEffect   `json:"effect"`
	Evidence         approval.Evidence        `json:"evidence"`
	Coverage         approval.TargetCoverage  `json:"coverage"`
	Gaps             []approval.AssessmentGap `json:"gaps"`
	Dirs             []string                 `json:"dirs"`
	WritePaths       []string                 `json:"write_paths"`
	Dynamic          []string                 `json:"dynamic"`
	Origins          []string                 `json:"origins"`
	UnresolvedRemote []string                 `json:"unresolved_remote"`
	Provider         []string                 `json:"provider"`
	Auto             approval.Decision        `json:"auto"`
	Always           approval.Decision        `json:"always"`
	Never            approval.Decision        `json:"never"`
}

type commandBaselineFixture struct {
	m                  *Mods
	ws, external, safe string
}

func loadCommandBaselineCases(t testing.TB) []commandBaselineCase {
	t.Helper()
	data, err := os.ReadFile("testdata/command_analysis/cases.json")
	require.NoError(t, err)
	var cases []commandBaselineCase
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	require.NoError(t, decoder.Decode(&cases))
	require.NotEmpty(t, cases)
	seen := map[string]bool{}
	for _, c := range cases {
		require.NotEmpty(t, c.ID)
		require.False(t, seen[c.ID], "duplicate case %s", c.ID)
		seen[c.ID] = true
		require.Contains(t, []string{"posix", "powershell", "argv"}, c.Mode)
		require.NotEmpty(t, c.Source)
		require.Contains(t, []string{"read", "authorized-write", "safety", "limitation"}, c.Category)
		require.Contains(t, []string{"", "allow", "ask"}, c.Required)
		if c.Category != "limitation" {
			require.NotEmpty(t, c.Required, "semantic cases need an independent decision oracle")
		}
		if c.Category == "safety" {
			require.Equal(t, "ask", c.Required)
		}
		if c.Mode == "argv" {
			require.NotEmpty(t, c.Program)
			require.Empty(t, c.Command)
		} else {
			require.NotEmpty(t, c.Command)
			require.Empty(t, c.Program)
			require.Empty(t, c.Args)
		}
	}
	return cases
}

func newCommandBaselineFixture(t testing.TB) commandBaselineFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	f := commandBaselineFixture{ws: filepath.Join(root, "workspace"), external: filepath.Join(root, "external"), safe: filepath.Join(root, "safe")}
	for _, dir := range []string{f.ws, f.external, f.safe} {
		require.NoError(t, os.MkdirAll(dir, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "input.txt"), []byte("fixture\n"), 0o600))
	}
	// Isolate discovery from the user's repositories, configuration and hooks.
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(strings.ToUpper(key), "GIT_") {
			t.Setenv(key, "") // registers restoration, including originally empty values
			require.NoError(t, os.Unsetenv(key))
		}
	}
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "absent-config"))
	git, err := exec.LookPath("git")
	require.NoError(t, err)
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "remote.origin.url", "https://first.example/repo"},
		{"config", "--add", "remote.origin.pushurl", "https://first.example/repo"},
		{"config", "--add", "remote.origin.pushurl", "https://second.example/repo"},
	} {
		out, err := exec.Command(git, append([]string{"-C", f.ws}, args...)...).CombinedOutput()
		require.NoError(t, err, "%s", out)
	}
	f.m = &Mods{ctx: context.Background(), Config: testConfigForWorkingDir(f.ws)}
	t.Setenv("PWD", f.ws)
	return f
}

func (f commandBaselineFixture) expand(s string) string {
	return strings.NewReplacer("<WS>", filepath.ToSlash(f.ws), "<EXT>", filepath.ToSlash(f.external), "<SAFE>", filepath.ToSlash(f.safe)).Replace(s)
}

func (f commandBaselineFixture) assess(t testing.TB, c commandBaselineCase) approval.CommandAssessment {
	t.Helper()
	if c.Mode == "argv" {
		args := make([]string, len(c.Args))
		for i, arg := range c.Args {
			args[i] = f.expand(arg)
		}
		data, err := json.Marshal(map[string]any{"program": f.expand(c.Program), "args": args, "cwd": f.ws})
		require.NoError(t, err)
		return f.m.assessProcessInvocation(string(data))
	}
	flavor := pathutil.FlavorPOSIX
	if c.Mode == "powershell" {
		flavor = pathutil.FlavorPowerShell
	}
	shadow := map[string]bool{}
	for _, key := range c.Shadow {
		shadow[key] = true
	}
	return f.m.assessShellCommand("shell_run", flavor, f.expand(c.Command), shadow, f.ws)
}

func (f commandBaselineFixture) facts(c commandBaselineCase, a approval.CommandAssessment) commandBaselineFacts {
	normalize := func(values []string) []string {
		out := make([]string, 0, len(values))
		for _, value := range values {
			value = strings.ReplaceAll(value, `\`, "/")
			value = strings.NewReplacer(filepath.ToSlash(f.ws), "<WS>", filepath.ToSlash(f.external), "<EXT>", filepath.ToSlash(f.safe), "<SAFE>").Replace(value)
			out = append(out, value)
		}
		slices.Sort(out)
		return slices.Compact(out)
	}
	intent := a.AccessIntent()
	scope := approval.WorkingDirScope(f.ws)
	var rules []approval.Rule
	for _, grant := range c.Grant {
		rules = append(rules, approval.RulesForDirs([]string{f.expand(grant)}, scope, approval.AccessWrite)...)
	}
	decision := func(mode approval.ReviewMode) approval.Decision {
		d := approval.ClassifyAccess(intent, scope, []string{f.safe}, mode)
		if d == approval.DecisionAsk && approval.RulesAllowIntent(rules, intent, scope, []string{f.safe}, mode) {
			return approval.DecisionAllow
		}
		return d
	}
	gaps := append([]approval.AssessmentGap{}, a.Gaps...)
	slices.Sort(gaps)
	return commandBaselineFacts{a.Effect, a.Evidence, a.WriteCoverage, gaps,
		normalize(a.KnownDirs), normalize(a.WritePaths), normalize(a.DynamicTargets), normalize(a.RemoteOrigins), normalize(a.UnresolvedRemoteTargets), normalize(a.ProviderWriteTargets),
		decision(approval.ReviewAuto), decision(approval.ReviewAlways), decision(approval.ReviewNever)}
}

func TestCommandAnalysisBaseline(t *testing.T) {
	cases := loadCommandBaselineCases(t)
	f := newCommandBaselineFixture(t)
	t.Cleanup(approval.CloseBridge)
	observed := map[string]commandBaselineFacts{}
	for _, c := range cases {
		t.Run(c.ID, func(t *testing.T) {
			if c.Mode == "powershell" && runtime.GOOS != "windows" {
				t.Skip("real PowerShell bridge baseline requires Windows")
			}
			facts := f.facts(c, f.assess(t, c))
			observed[c.ID] = facts
			if c.Required != "" {
				require.Equal(t, approval.Decision(c.Required), facts.Auto, "semantic safety oracle; snapshot updates cannot change this")
			}
			if c.Origins != nil {
				require.ElementsMatch(t, c.Origins, facts.Origins, "all configured push targets must survive analysis")
			}
			require.Equal(t, approval.DecisionAllow, facts.Never)
			if c.Category == "safety" {
				require.Equal(t, approval.DecisionAsk, facts.Always)
			}
		})
	}
	file := filepath.Join("testdata", "command_analysis", "baseline_"+runtime.GOOS+".json")
	if *updateCommandBaseline {
		if t.Failed() {
			t.Fatal("refusing to update baseline after a semantic assertion failed")
		}
		data, err := json.MarshalIndent(observed, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(file, append(data, '\n'), 0o600))
		return
	}
	data, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		t.Logf("No measured compatibility baseline for %s; semantic assertions ran, exact parity not verified", runtime.GOOS)
		return
	}
	require.NoError(t, err)
	var baseline map[string]commandBaselineFacts
	require.NoError(t, json.Unmarshal(data, &baseline))
	require.Equal(t, baseline, observed, "review fact and decision differences; do not blindly regenerate")
}

// Warm measurements exclude fixture creation and bridge startup. A separate
// cold-host benchmark below measures bridge startup explicitly, not Go caches.
func BenchmarkCommandAnalysisBaseline(b *testing.B) {
	cases := loadCommandBaselineCases(b)
	f := newCommandBaselineFixture(b)
	b.Cleanup(approval.CloseBridge)
	for _, c := range cases {
		if c.Mode == "powershell" && runtime.GOOS != "windows" {
			continue
		}
		b.Run(c.ID, func(b *testing.B) {
			f.assess(b, c)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				f.assess(b, c)
			}
		})
	}
}

func BenchmarkCommandAnalysisColdBridge(b *testing.B) {
	if runtime.GOOS != "windows" {
		b.Skip("Windows PowerShell bridge")
	}
	f := newCommandBaselineFixture(b)
	b.Cleanup(approval.CloseBridge)
	c := commandBaselineCase{Mode: "powershell", Command: "Get-Content '<WS>/input.txt'"}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		b.StopTimer()
		approval.CloseBridge()
		b.StartTimer()
		f.assess(b, c)
	}
}

// Global counters are opt-in and only compared in this serial test. They never
// contain command text and are compiled to no-ops in normal builds.
func TestCommandAnalysisWorkBaseline(t *testing.T) {
	if !commandtrace.Enabled {
		t.Skip("run with -tags commandtrace to measure parser and query work")
	}
	cases := loadCommandBaselineCases(t)
	f := newCommandBaselineFixture(t)
	t.Cleanup(approval.CloseBridge)
	type work struct {
		Initial      commandtrace.Counts `json:"initial"`
		Revalidation commandtrace.Counts `json:"revalidation"`
	}
	observed := map[string]work{}
	for _, c := range cases {
		if c.Mode == "powershell" && runtime.GOOS != "windows" {
			continue
		}
		before := commandtrace.Snapshot()
		a := f.assess(t, c)
		first := commandtrace.Snapshot().Since(before)
		var repeat commandtrace.Counts
		if a.AccessIntent().DominantClass() == approval.AccessWrite {
			before = commandtrace.Snapshot()
			f.assess(t, c)
			repeat = commandtrace.Snapshot().Since(before)
		}
		// Stage 2's contract is independent of saved measurements: one main
		// parse per shell analysis, including a fresh parse for revalidation.
		assertParseWork := func(counts commandtrace.Counts) {
			t.Helper()
			switch c.Mode {
			case "posix":
				require.EqualValues(t, 1, counts.POSIXParses, c.ID)
				require.Zero(t, counts.BridgeParses, c.ID)
			case "powershell":
				require.Zero(t, counts.POSIXParses, c.ID)
				require.EqualValues(t, 1, counts.BridgeParses, c.ID)
				require.EqualValues(t, 1, counts.BridgeRoundTrips, c.ID)
			default:
				require.Zero(t, counts.POSIXParses, c.ID)
				require.Zero(t, counts.BridgeParses, c.ID)
			}
		}
		assertParseWork(first)
		if a.AccessIntent().DominantClass() == approval.AccessWrite {
			assertParseWork(repeat)
		}
		observed[c.ID] = work{first, repeat}
	}
	file := filepath.Join("testdata", "command_analysis", "work_stage2_"+runtime.GOOS+".json")
	if *updateCommandBaseline {
		data, err := json.MarshalIndent(observed, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(file, append(data, '\n'), 0o600))
		return
	}
	data, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		t.Logf("No measured work baseline for %s", runtime.GOOS)
		return
	}
	require.NoError(t, err)
	var expected map[string]work
	require.NoError(t, json.Unmarshal(data, &expected))
	require.Equal(t, expected, observed, "analysis work changed; review before updating measurements")
}
