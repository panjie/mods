package commandanalysis

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCommandStructureDoesNotChangeFacts(t *testing.T) {
	for _, source := range []string{"cat input | wc -l", "for f in a b; do cat \"$f\"; done", "x=a; unknown \"$x\"; sh -c 'echo hello'", "echo x > output"} {
		p := parseCommandLegacy(source, true)
		a := p.assess(ReadOnlyCommandPolicy{}, ArgvStaticContext{})
		before := a.Clone()
		work := p.semantics.evaluations
		one := commandStructure(p)
		two := commandStructure(p)
		require.Equal(t, one, two)
		require.Equal(t, work, p.semantics.evaluations, "diagnostics cannot evaluate rules")
		require.Equal(t, before, a, "diagnostics cannot mutate facts")
		require.Equal(t, "byte", one.OffsetUnit)
		clone := a.Clone()
		if len(clone.Structure.Findings) > 0 {
			clone.Structure.Findings[0].Code = "changed"
			require.NotEqual(t, clone.Structure.Findings, a.Structure.Findings)
		}
		clone.Structure.Assignments++
		require.NotEqual(t, clone.Structure.Assignments, a.Structure.Assignments)
	}
}

func TestCommandStructureBoundsAndLegacy(t *testing.T) {
	require.Nil(t, commandStructure(nil))
	var old CommandAssessment
	require.NoError(t, json.Unmarshal([]byte(`{"Effect":"unknown"}`), &old))
	require.Nil(t, old.Clone().Structure)
	p := &parsedCommand{ir: &psBridgeIR{Commands: []string{"unknown"}}}
	s := commandStructure(p)
	require.Len(t, s.Findings, 1)
	require.False(t, s.Findings[0].Located)
	require.Zero(t, s.Findings[0].NodeID)
	a := AssessShellStaticWithPolicy(strings.Repeat("unknown; ", maxStructureFindings+10), true, ReadOnlyCommandPolicy{})
	require.Len(t, a.Structure.Findings, maxStructureFindings)
	require.True(t, a.Structure.Truncated)
	require.Equal(t, maxStructureFindings+10, a.Structure.UnmodeledInvocations)
}

func TestCommandStructureDepthAndLocations(t *testing.T) {
	a := AssessShellStaticWithPolicy("for x in a; do if true; then mystery; fi; done; sh -c 'echo hi'", true, ReadOnlyCommandPolicy{})
	require.Equal(t, 2, a.Structure.ControlFlowDepth)
	require.Equal(t, 1, a.Structure.HostInvocations)
	require.Equal(t, 1, a.Structure.InlinePayloads)
	for _, finding := range a.Structure.Findings {
		require.True(t, finding.Located)
		require.Greater(t, finding.End, finding.Start)
	}
}
