package commandanalysis

import (
	"context"
	"strings"
	"testing"

	"github.com/panjie/mods/internal/pathutil"
	"github.com/stretchr/testify/require"
)

const awkCodeCountPipeline = `git ls-files -z | xargs -0 wc -l | tail -n 1; echo '--- by ext ---'; git ls-files -z | xargs -0 wc -l | awk 'NF==2 {print $2}' | grep -E '\.' | sed 's/.*\.//' | sort | uniq -c | sort -rn | head -20`

func TestAWKReadOnlyPrograms(t *testing.T) {
	cases := [][]string{
		{`NF==2 {print $2}`},
		{`{print $1}`},
		{`NF > 1 || NR == 1 {print $2}`},
		{`{sum += $1} END {print sum}`},
		{`{sum += $1} END {if (NR) print sum/NR}`},
		{`{count[$2]++} END {for (key in count) printf "%s %d\n",key,count[key]}`},
		{`/\.go$/ {print $1}`},
		{`$1 ~ /^[a-z]+$/ {print toupper($1)}`},
		{`BEGIN {print "system(\"x\") > output | command"}`},
		{`/system\(.*\)>/ {print $0}`},
		{"-F", ":", "-v", "limit=10", `NF==2 && NR <= limit {print $2}`, "input"},
		{"-F:", "-vlimit=10", `{print substr($1,1,2)}`, "input"},
		{"--", `{gsub("x","y"); print}`, "FS=:"},
	}
	for _, program := range []string{"awk", "gawk", "mawk", "nawk"} {
		for _, args := range cases {
			name := program + strings.Join(args, " ")
			if len(name) > 200 {
				name = program + "/oversize"
			}
			t.Run(name, func(t *testing.T) {
				e := NewEngine(Settings{}, Dependencies{})
				r, err := e.Analyze(context.Background(), Input{Cwd: t.TempDir(), Dialect: pathutil.FlavorPOSIX, Process: &ProcessInput{Program: program, Args: args}})
				require.NoError(t, err)
				require.Equal(t, EffectRead, r.Effect)
				require.Equal(t, EvidenceStatic, r.Evidence)
				require.Equal(t, CoverageComplete, r.WriteCoverage)
			})
		}
	}
	for _, source := range []string{awkCodeCountPipeline, `awk -F: '{print $1}' /etc/passwd`, `awk '/\.go$/ {print $1}' input`, `for p in one two; do awk 'NF==2 {print $2}' "$p"; done`} {
		r, err := NewEngine(Settings{}, Dependencies{}).Analyze(context.Background(), Input{Cwd: t.TempDir(), Dialect: pathutil.FlavorPOSIX, Shell: &ShellInput{Source: source}})
		require.NoError(t, err)
		require.Equal(t, EffectRead, r.Effect, source)
		require.Empty(t, r.Diagnostics)
	}
}

func TestAWKUnsafePrograms(t *testing.T) {
	cases := [][]string{
		{`BEGIN {system("touch out")}`},
		{`BEGIN {print "x" > "out"}`},
		{`{printf "%s",$1 >> "out"}`},
		{`BEGIN {print "x" | "sh"}`},
		{`BEGIN {print "x" |& "sh"}`},
		{`BEGIN {"id" | getline x}`},
		{`BEGIN {getline x < "input"}`},
		{`@include "script.awk"`}, {`@load "extension"`},
		{`function f(){system("id")} BEGIN {f()}`},
		{`BEGIN {f="system"; @f("id")}`},
		{`BEGIN {PROCINFO["sorted_in"]="f"}`},
		{`BEGIN {ARGV[1]="other"}`},
		{`BEGIN {unknown("x")}`},
		{`BEGIN {x=1; print x / system("id") / 2}`},
		{"BEGIN {x=1; print x\n / system(\"id\") / 2}"},
		{`/ok/ {print "x" > "out"}`},
		{`BEGIN {print "unterminated}`},
		{"-f", "script.awk"}, {"-fscript.awk"}, {"--file=script.awk"},
		{"-e", `{print $1}`}, {"--profile", `{print $1}`},
		{`{print $1}`, "--profile=out"}, {"-W", "exec=script.awk"},
		{"-v", "PROCINFO=x", `{print $1}`}, {`{print $1}`, "ARGV=x"},
		{"-v"}, {"-F"}, {strings.Repeat("x", 65537)},
	}
	for _, program := range []string{"awk", "gawk", "mawk", "nawk"} {
		for _, args := range cases {
			name := program + strings.Join(args, " ")
			if len(name) > 200 {
				name = program + "/oversize"
			}
			t.Run(name, func(t *testing.T) {
				r, err := NewEngine(Settings{}, Dependencies{}).Analyze(context.Background(), Input{Cwd: t.TempDir(), Dialect: pathutil.FlavorPOSIX, Process: &ProcessInput{Program: program, Args: args}})
				require.NoError(t, err)
				require.NotEqual(t, EffectRead, r.Effect)
				require.Equal(t, CoverageIncomplete, r.WriteCoverage)
			})
		}
	}
	for _, source := range []string{`awk "$program" input`, `awk "$(cat script.awk)" input`, `awk '{print $1}' > out`, `awk '{print $1}'; mystery`} {
		r, err := NewEngine(Settings{}, Dependencies{}).Analyze(context.Background(), Input{Cwd: t.TempDir(), Dialect: pathutil.FlavorPOSIX, Shell: &ShellInput{Source: source}})
		require.NoError(t, err)
		require.NotEqual(t, EffectRead, r.Effect, source)
	}
}

func FuzzAWKReadOnlyProof(f *testing.F) {
	for _, program := range []string{`NF==2 {print $2}`, `{sum+=$1} END {print sum/NR}`, `/\.go$/ {print $1}`, `BEGIN {print "system(x)"}`} {
		f.Add(program)
	}
	f.Fuzz(func(t *testing.T, program string) {
		if len(program) > 65536 {
			t.Skip()
		}
		_ = awkProgramReadOnly(program)
		require.False(t, awkProgramReadOnly("BEGIN {system(\"forbidden\")}\n"+program))
		require.False(t, awkProgramReadOnly("BEGIN {print \"x\" > \"forbidden\"}\n"+program))
	})
}

func TestAWKInputOperandsExcludeProgramAndAssignments(t *testing.T) {
	source := `awk -F/ -v pattern=/ignored '/^[[:space:]]*$/ {next} {print $1}' /external/input value=/also-ignored`
	r, err := NewEngine(Settings{}, Dependencies{}).Analyze(context.Background(), Input{Cwd: t.TempDir(), Dialect: pathutil.FlavorPOSIX, Shell: &ShellInput{Source: source}})
	require.NoError(t, err)
	require.Equal(t, EffectRead, r.Effect)
	var inputs []string
	for _, target := range r.Targets {
		if target.Role == TargetRead {
			inputs = append(inputs, target.Raw)
		}
	}
	require.Equal(t, []string{"/external/input"}, inputs)
	require.NotContains(t, r.KnownDirs, "/")
}
