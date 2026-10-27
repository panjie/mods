package commandanalysis

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/stretchr/testify/require"
)

// Real production bridge, parse-only. Negative sources are never executed.
func TestP2PowerShellReadAggregation(t *testing.T) {
	testP2PowerShellHosts(t, testP2PowerShellReadAggregation)
}

func testP2PowerShellHosts(t *testing.T, run func(*testing.T)) {
	t.Run("selected-host", run)
	t.Run("windows-powershell-5.1", func(t *testing.T) {
		if _, err := exec.LookPath("powershell.exe"); err != nil {
			t.Skip("Windows PowerShell unavailable")
		}
		// Exercise the real production host fallback, including environments
		// with several pwsh installations. This changes only the test process.
		var paths []string
		for _, path := range filepath.SplitList(os.Getenv("PATH")) {
			if _, err := os.Stat(filepath.Join(strings.Trim(path, "\""), "pwsh.exe")); err != nil {
				paths = append(paths, path)
			}
		}
		t.Setenv("PATH", strings.Join(paths, string(os.PathListSeparator)))
		require.Equal(t, "powershell.exe", strings.ToLower(filepath.Base(getWindowsShellPath())))
		run(t)
	})
}

func testP2PowerShellReadAggregation(t *testing.T) {
	t.Cleanup(CloseBridge)
	for _, source := range []string{
		"Import-Csv amounts.csv | Measure-Object -Property amount -Sum | Select-Object -ExpandProperty Sum",
		"Import-Csv -LiteralPath 'space name.csv' -Delimiter ';' -Encoding utf8 | Where-Object amount -gt 0 | Group-Object item | Select-Object Name,Count",
		"Get-ChildItem -Recurse -File -Filter *.go | ForEach-Object { Get-Content -LiteralPath $_.FullName } | Measure-Object | Select-Object -ExpandProperty Count",
		"Get-ChildItem -File | Where-Object { $_.Extension -eq '.go' } | ForEach-Object -Process { Get-Content -LiteralPath $_.FullName } | Measure-Object",
	} {
		a := AssessShellStaticWithPolicy(source, false, ReadOnlyCommandPolicy{})
		require.Equal(t, EffectRead, a.Effect, source)
		require.True(t, a.StaticRead, source)
		require.Equal(t, CoverageComplete, a.WriteCoverage, source)
		require.Zero(t, a.Structure.UnmodeledInvocations, source)
	}
	for _, source := range []string{
		"Import-Csv amounts.csv -Mystery x | Measure-Object",
		"Import-Csv amounts.csv -OutVariable script:x | Measure-Object",
		"Import-Csv $(Remove-Item victim) | Measure-Object",
		"Import-Csv amounts.csv | ForEach-Object { Set-Content out $_.amount } | Measure-Object",
		"Import-Csv amounts.csv | ForEach-Object { Invoke-Expression $_.amount }",
		"Import-Csv amounts.csv | Measure-Object > output",
		"Import-Csv amounts.csv | Measure-Object -Property { Remove-Item victim } -Sum",
		"Mystery-Source | ForEach-Object { Get-Content -LiteralPath $_.FullName } | Measure-Object",
		"Get-ChildItem -File | ForEach-Object { Get-Content -LiteralPath $_.FullName; Remove-Item $_.FullName } | Measure-Object",
		"Get-ChildItem -File | ForEach-Object { Get-Content -LiteralPath $_.FullName > out } | Measure-Object",
		"Get-ChildItem -File | ForEach-Object { Mystery $_.FullName } | Measure-Object",
		"Get-ChildItem -File | ForEach-Object -Mystery { Get-Content -LiteralPath $_.FullName } | Measure-Object",
		"Get-ChildItem -File | ForEach-Object -Parallel { Get-Content -LiteralPath $_.FullName } | Measure-Object",
		"Get-ChildItem -File | ForEach-Object { foreach ($x in $_) { Get-Content $x.FullName } } | Measure-Object",
		"Get-ChildItem -File | ForEach-Object { Start-Process pwsh } | Measure-Object",
		"Get-ChildItem -File | ForEach-Object { Invoke-WebRequest https://example.com } | Measure-Object",
		"Get-ChildItem -File | ForEach-Object { $_.Delete() } | Measure-Object",
		"Get-ChildItem -File | ForEach-Object { [IO.File]::ReadAllLines($_.FullName) } | Measure-Object",
		"Get-ChildItem -File | ForEach-Object { Get-Content $unbound } | Measure-Object",
	} {
		a := AssessShellStaticWithPolicy(source, false, ReadOnlyCommandPolicy{})
		require.NotEqual(t, EffectRead, a.Effect, source)
		require.False(t, a.StaticRead, source)
	}
}

func TestP2PowerShellStructure(t *testing.T) {
	t.Cleanup(CloseBridge)
	source := "Get-ChildItem '😀' | ForEach-Object { if ($true) { foreach ($f in $_) { $n = 1; mystery } } }; pwsh -Command 'Get-Date'"
	p := parseCommandLegacy(source, false)
	a := p.assess(ReadOnlyCommandPolicy{}, ArgvStaticContext{})
	require.Equal(t, "utf16", a.Structure.OffsetUnit)
	require.Equal(t, 2, a.Structure.ControlFlowDepth)
	require.Equal(t, 1, a.Structure.ScriptBlockDepth)
	require.Equal(t, 1, a.Structure.Assignments)
	require.Equal(t, 1, a.Structure.HostInvocations)
	require.Equal(t, 1, a.Structure.InlinePayloads)
	work := p.semantics.evaluations
	require.Equal(t, a.Structure, commandStructure(p))
	require.Equal(t, work, p.semantics.evaluations)
	units := utf16.Encode([]rune(source))
	found := false
	for _, finding := range a.Structure.Findings {
		if finding.Code == StructureUnknownInvocation && string(utf16.Decode(units[finding.Start:finding.End])) == "mystery" {
			found = true
		}
	}
	require.True(t, found, "UTF-16 positions must identify the real source node")
}
