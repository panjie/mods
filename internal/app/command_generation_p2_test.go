package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/commandanalysis"
	"github.com/stretchr/testify/require"
)

var updateP2Replay = flag.Bool("update-command-p2-replay", false, "record P2 replay after independent assertions")

type p2ReplayObservation struct {
	ID         string                        `json:"id"`
	SourceHash string                        `json:"source_sha256"`
	Command    string                        `json:"command"`
	Effect     commandanalysis.CommandEffect `json:"effect"`
	Coverage   approval.TargetCoverage       `json:"coverage"`
	Decision   approval.Decision             `json:"decision"`
}

// Replays exact saved A1 commands through production analysis and the production
// access adapter. No model request, CallTools, or generated-command execution.
// Native calls and process calls are outside this shell-only comparison.
func TestCommandGenerationP2Replay(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("recorded Windows PowerShell samples")
	}
	f := newCommandBaselineFixture(t)
	t.Cleanup(commandanalysis.CloseBridge)
	root := filepath.Join("..", "..", "docs", "superpowers", "evidence", "2026-10-26-command-generation-refinement")
	var observed []p2ReplayObservation
	for _, mode := range []string{"normal", "minimal"} {
		files, err := filepath.Glob(filepath.Join(root, "final-"+mode, "*.json"))
		require.NoError(t, err)
		require.Len(t, files, 2)
		for _, file := range files {
			raw, err := os.ReadFile(file)
			require.NoError(t, err)
			digest := sha256.Sum256(raw)
			var runs []struct {
				Case   string `json:"case"`
				Arm    string `json:"arm"`
				Repeat int    `json:"repeat"`
				Calls  []struct {
					Name string `json:"name"`
					Args struct {
						Command string `json:"command"`
					} `json:"args"`
				} `json:"calls"`
			}
			require.NoError(t, json.Unmarshal(raw, &runs))
			for _, run := range runs {
				if run.Arm != "A1" {
					continue
				}
				for i, call := range run.Calls {
					if call.Name != "powershell_run" && call.Name != "shell_run" {
						continue
					}
					c := commandBaselineCase{Mode: "powershell", Command: call.Args.Command}
					a := f.assess(t, c)
					facts := f.facts(c, a)
					observed = append(observed, p2ReplayObservation{fmt.Sprintf("%s/%s/%s/%d/%d", mode, filepath.Base(file), run.Case, run.Repeat, i), hex.EncodeToString(digest[:]), c.Command, a.Effect, a.WriteCoverage, facts.Auto})
				}
			}
		}
	}
	require.NotEmpty(t, observed)
	base := "testdata/command_generation/p2_replay_before_windows.json"
	raw, err := os.ReadFile(base)
	require.NoError(t, err)
	var before []p2ReplayObservation
	require.NoError(t, json.Unmarshal(raw, &before))
	require.Len(t, observed, len(before))
	changes := 0
	for i, old := range before {
		got := observed[i]
		require.Equal(t, old.ID, got.ID)
		require.Equal(t, old.SourceHash, got.SourceHash)
		require.Equal(t, old.Command, got.Command)
		if got.Decision != old.Decision {
			changes++
			// Only this independently audited, literal CSV read can gain permission.
			require.Equal(t, "Import-Csv amounts.csv | Measure-Object -Property amount -Sum | Select-Object -ExpandProperty Sum", got.Command)
			require.Equal(t, approval.DecisionAsk, old.Decision)
			require.Equal(t, approval.DecisionAllow, got.Decision)
			require.Equal(t, commandanalysis.EffectRead, got.Effect)
		} else {
			require.Equal(t, old, got)
		}
	}
	t.Logf("replayed %d shell calls; %d approval decisions changed", len(observed), changes)
	data, err := json.MarshalIndent(observed, "", "  ")
	require.NoError(t, err)
	path := strings.Replace(base, "before", "after", 1)
	if *updateP2Replay {
		require.False(t, t.Failed())
		require.NoError(t, os.WriteFile(path, append(data, '\n'), 0600))
	} else {
		want, err := os.ReadFile(path)
		require.NoError(t, err)
		require.JSONEq(t, string(want), string(data))
	}
}

func TestCommandGenerationP2AccessBoundaries(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("real PowerShell bridge")
	}
	f := newCommandBaselineFixture(t)
	t.Cleanup(commandanalysis.CloseBridge)
	for _, command := range []string{
		"Import-Csv '<EXT>/input.csv' | Measure-Object -Property amount -Sum",
		"Get-ChildItem '<EXT>' -File | ForEach-Object { Get-Content -LiteralPath $_.FullName } | Measure-Object",
	} {
		c := commandBaselineCase{Mode: "powershell", Command: command}
		facts := f.facts(c, f.assess(t, c))
		require.Equal(t, approval.DecisionAllow, facts.Auto, command)
		require.Equal(t, approval.DecisionAllow, facts.Always, command)
		require.Empty(t, facts.WriteDirs, command)
	}
	for _, command := range []string{
		"Import-Csv '<SAFE>/input.csv' -Mystery x | Measure-Object",
		"Import-Csv '<WS>/input.csv' | Mystery > '<SAFE>/out'",
		"Import-Csv '<WS>/input.csv' | ForEach-Object { Set-Content '<SAFE>/out' $_.amount }",
		"Get-ChildItem '<WS>' -File | ForEach-Object { Get-Content -LiteralPath $_.FullName; Remove-Item $_.FullName }",
		"Get-ChildItem '<WS>' -File | ForEach-Object -Mystery { Get-Content -LiteralPath $_.FullName }",
		"Get-ChildItem '<WS>' -File | ForEach-Object -Parallel { Get-Content -LiteralPath $_.FullName }",
		"Get-ChildItem '<WS>' -File | ForEach-Object { Invoke-Expression $_.Name }",
	} {
		// Neither a saved write rule covering every directory nor a temporary
		// location may authorize incompletely understood compound operations.
		c := commandBaselineCase{Mode: "powershell", Command: command, Grant: []string{"<WS>", "<SAFE>", "<EXT>"}}
		facts := f.facts(c, f.assess(t, c))
		require.Equal(t, approval.CoverageIncomplete, facts.Coverage, command)
		require.Equal(t, approval.DecisionAsk, facts.Auto, command)
		require.Equal(t, approval.DecisionAsk, facts.Always, command)
		require.Equal(t, approval.DecisionAllow, facts.Never, command)
	}
}
