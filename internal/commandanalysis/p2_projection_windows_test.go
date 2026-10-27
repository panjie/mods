package commandanalysis

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/panjie/mods/internal/pathutil"
	"github.com/stretchr/testify/require"
)

func TestP2AggregatePropertyProjection(t *testing.T) {
	testP2PowerShellHosts(t, testP2AggregatePropertyProjection)
}

func testP2AggregatePropertyProjection(t *testing.T) {
	t.Cleanup(CloseBridge)
	for _, source := range []string{
		"(Get-Content file.txt | Measure-Object).Count",
		"(Import-Csv amounts.csv | Measure-Object -Property amount -Sum).Sum",
		"(Get-ChildItem -File | Where-Object Extension -eq '.go' | Measure-Object).Count",
		"(Get-ChildItem -File | Group-Object Extension | Measure-Object).Count",
		"(Get-ChildItem -Recurse -File -Filter *.go | ForEach-Object { Get-Content -LiteralPath $_.FullName } | Measure-Object).Count",
		"(Get-Content '😀.txt' | Measure-Object -Line).Lines",
	} {
		a := AssessShellStaticWithPolicy(source, false, ReadOnlyCommandPolicy{})
		require.Equal(t, EffectRead, a.Effect, source)
		require.True(t, a.StaticRead, source)
		require.Equal(t, CoverageComplete, a.WriteCoverage, source)
	}
	for _, source := range []string{
		"(Get-Content file.txt | Measure-Object -Mystery).Count",
		"(Get-Content file.txt | Measure-Object -OutVariable script:x).Count",
		"(Get-Content file.txt | Measure-Object -Property { Remove-Item x } -Sum).Sum",
		"(Get-Content file.txt | Measure-Object).$env:FIELD",
		"(Get-Content file.txt | Measure-Object).Count.ToString()",
		"(Get-Content file.txt | Measure-Object).Count = 1",
		"(Get-Content file.txt | Measure-Object).Count > out",
		"(Get-Content file.txt | Measure-Object > out).Count",
		"(Mystery | Measure-Object).Count",
		"(Get-Content file.txt | ForEach-Object { Set-Content out $_ } | Measure-Object).Count",
		"(Get-Content file.txt | ForEach-Object { Invoke-Expression $_ } | Measure-Object).Count",
		"(Get-Content file.txt | ForEach-Object { foreach ($x in $_) { Get-Content $x } } | Measure-Object).Count",
		"(Get-Content file.txt | Measure-Object).PSObject",
		"(Get-Content file.txt | Measure-Object).Sum",
		"(Get-Content file.txt | Measure-Object -Line).Count",
		"(Get-Content file.txt | Measure-Object -Line -Sum).Lines",
		"(Get-Content file.txt | Measure-Object.exe).Count",
		"(Get-Content file.txt).Count",
		"(Get-Content file.txt | Measure-Object; Get-Date).Count",
		"(Get-Content file.txt | Measure-Object).Count; Mystery",
	} {
		a := AssessShellStaticWithPolicy(source, false, ReadOnlyCommandPolicy{})
		require.NotEqual(t, EffectRead, a.Effect, source)
		require.False(t, a.StaticRead, source)
	}
}

func TestP2BridgeUnicodeRoundTrip(t *testing.T) {
	testP2PowerShellHosts(t, func(t *testing.T) {
		t.Cleanup(CloseBridge)
		source := "(Get-Content '中文😀.txt' | Measure-Object).Count"
		ir, err := parseWithBridge(source)
		require.NoError(t, err)
		require.Equal(t, []string{source}, ir.TopLevelValueExpressions)
		require.Contains(t, ir.Paths, "中文😀.txt")
		require.Equal(t, []string{"'中文😀.txt'"}, ir.Invocations[0].Args)
	})
}

func TestP2ProjectionSharesParseAndFacts(t *testing.T) {
	t.Cleanup(CloseBridge)
	source := "(Get-Content 'input.txt' | Measure-Object).Count"
	p := parseCommandLegacy(source, false)
	require.NoError(t, p.err)
	a := p.assess(ReadOnlyCommandPolicy{}, ArgvStaticContext{})
	require.Equal(t, EffectRead, a.Effect)
	work := p.semantics.evaluations
	require.True(t, safeTopLevelPowerShellValues(source, p.ir, p.semantics))
	require.Equal(t, work, p.semantics.evaluations, "property proof only consumes cached invocation facts")
	raw, err := json.Marshal(p.ir)
	require.NoError(t, err)
	calls := 0
	e := NewEngine(Settings{}, Dependencies{PowerShell: parserFunc(func(context.Context, string) ([]byte, error) { calls++; return raw, nil })})
	in := Input{Cwd: t.TempDir(), Dialect: pathutil.FlavorPowerShell, Shell: &ShellInput{Source: source}}
	for i := 1; i <= 2; i++ {
		result, err := e.Analyze(context.Background(), in)
		require.NoError(t, err)
		require.Equal(t, EffectRead, result.Effect)
		require.Equal(t, i, calls, "one main parse, including fresh revalidation")
	}
	for _, mutate := range []func(*psBridgeIR){
		func(ir *psBridgeIR) { ir.Nodes = nil },
		func(ir *psBridgeIR) { ir.Version = "1" },
		func(ir *psBridgeIR) {
			for i := range ir.Nodes {
				if ir.Nodes[i].Kind == "MemberExpressionAst" {
					ir.Nodes[i].Kind = "InvokeMemberExpressionAst"
				}
			}
		},
		func(ir *psBridgeIR) {
			for i := range ir.Nodes {
				if ir.Nodes[i].Kind == "ParenExpressionAst" {
					ir.Nodes[i].Parent = 0
				}
			}
		},
		func(ir *psBridgeIR) {
			for i := range ir.Nodes {
				if ir.Nodes[i].Kind == "MemberExpressionAst" {
					ir.Nodes[i].End = len(source) + 100
				}
			}
		},
	} {
		var changed psBridgeIR
		require.NoError(t, json.Unmarshal(raw, &changed))
		mutate(&changed)
		require.False(t, safeTopLevelPowerShellValues(source, &changed, p.semantics))
	}
	require.False(t, safeTopLevelPowerShellValues(source, p.ir, newSemanticSession(nil, ReadOnlyCommandPolicy{Commands: []string{"measure-object"}})), "trust cannot replace a built-in output proof")
}
