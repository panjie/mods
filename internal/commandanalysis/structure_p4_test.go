package commandanalysis

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCommandStructureP4FactsPreserveAuthorization(t *testing.T) {
	source := `a=1; a=2; b=3; for x in a; do eval 'mystery'; done`
	p := parseCommandLegacy(source, true)
	before := p.assess(ReadOnlyCommandPolicy{}, ArgvStaticContext{})
	snapshot := before.Clone()
	work := p.semantics.evaluations
	s := commandStructure(p)
	require.Equal(t, 2, s.LocalStateTargets)
	require.Equal(t, 1, s.DynamicEvaluations)
	require.False(t, s.FilePayload)
	require.Equal(t, s, commandStructure(p))
	require.Equal(t, snapshot, before)
	require.Equal(t, work, p.semantics.evaluations)
	copy := snapshot.Clone()
	copy.Structure.PayloadSources[0] = "changed"
	require.NotEqual(t, copy.Structure.PayloadSources, snapshot.Structure.PayloadSources)
	p = &parsedCommand{ir: &psBridgeIR{AssignmentTargets: []string{"$A", "$a", "$b", "$env:FOO"}, ScriptBlockAssignmentTargets: []string{"$B", "$c"}, Invocations: []psCommandInvocation{{Name: "Invoke-Expression", Args: []string{"'mystery'"}}}}}
	s = commandStructure(p)
	require.Equal(t, 3, s.LocalStateTargets)
	require.Equal(t, 1, s.DynamicEvaluations)
	for _, source := range []string{`for x in a; do python noextension; done`, `for x in a; do . existing; done`, `for x in a; do sh "$script"; done`} {
		require.True(t, commandStructure(parseCommandLegacy(source, true)).FilePayload, source)
	}
	require.Zero(t, commandStructure(parseCommandLegacy(`a=1 mystery`, true)).LocalStateTargets, "environment bindings are not shell local assignments")
}
