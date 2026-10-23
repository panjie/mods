package approval

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIncompleteCoverageNeverBorrowsPermission(t *testing.T) {
	root := t.TempDir()
	scope := WorkingDirScope(t.TempDir())
	target := filepath.Join(root, "scratch")
	rules := append(RulesForDirs([]string{target}, scope, AccessWrite), Rule{Type: RemoteAllow, Origins: []string{"https://example.com"}, Mode: AccessWrite})
	for _, intent := range []AccessIntent{
		{Class: AccessWrite, Dirs: []string{target}},
		{Class: AccessWrite, Dirs: []string{target}, RemoteOrigins: []string{"https://example.com"}},
		{WriteCoverage: CoverageComplete, Gaps: []AssessmentGap{GapUnknownInvocation}, Class: AccessWrite, Dirs: []string{target}},
	} {
		for _, mode := range []ReviewMode{ReviewAuto, ReviewAlways} {
			require.Equal(t, DecisionAsk, ClassifyAccess(intent, scope, []string{target}, mode))
			require.False(t, RulesAllowIntent(rules, intent, scope, []string{target}, mode))
		}
		require.Equal(t, DecisionAllow, ClassifyAccess(intent, scope, []string{target}, ReviewNever))
	}
	trusted := AccessIntent{WriteCoverage: CoverageComplete, Class: AccessWrite, Dirs: []string{target}}
	require.Equal(t, DecisionAllow, ClassifyAccess(trusted, scope, []string{target}, ReviewAuto))
	require.True(t, RulesAllowIntent(rules, trusted, scope, nil, ReviewAuto))
}

func TestReadEffectRequiresEvidence(t *testing.T) {
	a := CommandAssessment{Effect: EffectRead}
	require.Equal(t, AccessWrite, a.AccessIntent().DominantClass())
	a.Evidence = EvidenceStatic
	require.Equal(t, AccessRead, a.AccessIntent().DominantClass())
}
