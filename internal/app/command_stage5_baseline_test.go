package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/panjie/mods/internal/approval"
	"github.com/stretchr/testify/require"
)

func loadStage5ExtendedCases(t testing.TB) []commandBaselineCase {
	raw, err := os.ReadFile("testdata/command_analysis/cases_stage5_extended.json")
	require.NoError(t, err)
	var cases []commandBaselineCase
	require.NoError(t, json.Unmarshal(raw, &cases))
	return cases
}
func TestStage5ExtendedBaseline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fixed sample set")
	}
	f := newCommandBaselineFixture(t)
	observed := map[string]commandBaselineFacts{}
	for _, c := range loadStage5ExtendedCases(t) {
		facts := f.facts(c, f.assess(t, c))
		observed[c.ID] = facts
		require.Equal(t, approval.Decision(c.Required), facts.Auto, c.ID)
		require.Equal(t, approval.DecisionAllow, facts.Never)
	}
	file := filepath.Join("testdata", "command_analysis", "baseline_stage5_extended_"+runtime.GOOS+".json")
	if *updateCommandBaseline {
		raw, err := json.MarshalIndent(observed, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(file, append(raw, '\n'), 0600))
		return
	}
	raw, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		t.Log("no measured platform snapshot; semantic assertions checked")
		return
	}
	require.NoError(t, err)
	var previous map[string]commandBaselineFacts
	require.NoError(t, json.Unmarshal(raw, &previous))
	require.Equal(t, previous, observed)
}
func BenchmarkStage5Extended(b *testing.B) {
	if runtime.GOOS == "windows" {
		b.Skip("POSIX sample set")
	}
	f := newCommandBaselineFixture(b)
	for _, c := range loadStage5ExtendedCases(b) {
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
