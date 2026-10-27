package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/commandanalysis"
	"github.com/stretchr/testify/require"
)

func TestP2ProjectionAccess(t *testing.T) {
	f := newCommandBaselineFixture(t)
	t.Cleanup(commandanalysis.CloseBridge)
	observed := map[string]commandBaselineFacts{}
	for _, tc := range []struct {
		id, command string
		allow       bool
	}{
		{"count", "(Get-Content '<EXT>/input.txt' | Measure-Object).Count", true},
		{"sum", "(Import-Csv '<EXT>/amounts.csv' | Measure-Object -Property amount -Sum).Sum", true},
		{"map", "(Get-ChildItem '<EXT>' -File | ForEach-Object { Get-Content -LiteralPath $_.FullName } | Measure-Object).Count", true},
		{"filter", "(Get-ChildItem '<WS>' -File | Where-Object Extension -eq '.go' | Select-Object FullName | Measure-Object).Count", true},
		{"group", "(Get-ChildItem '<WS>' -File | Group-Object Extension | Measure-Object).Count", true},
		{"unknown-option", "(Get-Content '<WS>/input.txt' | Measure-Object -Mystery).Count", false},
		{"unknown-source", "(Mystery '<SAFE>/input' | Measure-Object).Count", false},
		{"write-child", "(Get-Content '<WS>/input.txt' | ForEach-Object { Set-Content '<SAFE>/out' $_ } | Measure-Object).Count", false},
		{"redirect-child", "(Get-Content '<WS>/input.txt' | Measure-Object > '<EXT>/out').Count", false},
		{"unbound-property", "(Get-Content '<WS>/input.txt' | Measure-Object).$env:FIELD", false},
		{"unknown-sibling", "(Get-Content '<WS>/input.txt' | Measure-Object).Count; Mystery '<SAFE>/out'", false},
	} {
		c := commandBaselineCase{ID: tc.id, Mode: "powershell", Command: tc.command, Grant: []string{"<WS>", "<EXT>", "<SAFE>"}}
		if tc.id == "redirect-child" {
			// A fully modeled literal write may use an ordinary saved write
			// rule. Here it must ask because its external target is ungranted.
			c.Grant = nil
		}
		facts := f.facts(c, f.assess(t, c))
		// Existing directory-read targets can refer to the fixture parent.
		// Normalize that one known identity, preserving all other path facts.
		for i, dir := range facts.ReadDirs {
			if dir == filepath.ToSlash(filepath.Dir(f.ws)) {
				facts.ReadDirs[i] = "<ROOT>"
			}
		}
		want := approval.DecisionAsk
		if tc.allow {
			want = approval.DecisionAllow
			require.Equal(t, commandanalysis.EffectRead, facts.Effect)
			require.Empty(t, facts.WriteDirs)
		}
		require.Equal(t, want, facts.Auto, tc.id)
		require.Equal(t, want, facts.Always, tc.id)
		require.Equal(t, approval.DecisionAllow, facts.Never)
		observed[tc.id] = facts
	}
	path := "testdata/command_generation/projection_windows.json"
	data, err := json.MarshalIndent(observed, "", "  ")
	require.NoError(t, err)
	if *updateGenerationBaseline {
		require.False(t, t.Failed())
		require.NoError(t, os.WriteFile(path, append(data, '\n'), 0600))
	} else {
		want, err := os.ReadFile(path)
		require.NoError(t, err)
		require.JSONEq(t, string(want), string(data))
	}
}
