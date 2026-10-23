package approval

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCommandAssessmentAccessIntent(t *testing.T) {
	readWithDir := CommandAssessment{Evidence: EvidenceStatic, Effect: EffectRead, KnownDirs: []string{"/etc"}, RemoteOrigins: []string{"https://api.example.com"}, UnresolvedRemoteTargets: []string{"$REMOTE"}, DynamicTargets: []string{"$PROFILE"}}
	require.Equal(t, AccessRead, readWithDir.AccessIntent().Class)
	require.Equal(t, []string{"/etc"}, readWithDir.AccessIntent().Dirs)
	require.Equal(t, []string{"https://api.example.com"}, readWithDir.AccessIntent().RemoteOrigins)
	require.Equal(t, []string{"$REMOTE"}, readWithDir.AccessIntent().UnresolvedRemoteTargets)
	require.Equal(t, []string{"$PROFILE"}, readWithDir.AccessIntent().UnresolvedPaths)

	dynamicRead := CommandAssessment{Evidence: EvidenceStatic, Effect: EffectRead, DynamicTargets: []string{"$PROFILE"}, DynamicProbe: true}
	require.Equal(t, AccessRead, dynamicRead.AccessIntent().Class)
	require.Empty(t, dynamicRead.AccessIntent().Dirs)
	require.Equal(t, []string{"$PROFILE"}, dynamicRead.AccessIntent().UnresolvedPaths)
	require.True(t, dynamicRead.AccessIntent().DynamicProbe)
	require.Equal(t, DecisionAllow, ClassifyAccess(dynamicRead.AccessIntent(), Scope{Value: "/cwd"}, nil, ReviewAuto))

	dynamicWrite := CommandAssessment{Effect: EffectWrite, DynamicTargets: []string{"$PROFILE"}}
	require.Equal(t, AccessWrite, dynamicWrite.AccessIntent().Class)
	require.Equal(t, []string{"$PROFILE"}, dynamicWrite.AccessIntent().UnresolvedPaths)
	providerWrite := CommandAssessment{Effect: EffectWrite, ProviderWriteTargets: []string{`HKCU:\Software\Classes\Neovide`}}
	require.Equal(t, []string{`HKCU:\Software\Classes\Neovide`}, providerWrite.AccessIntent().ProviderWriteTargets)
	require.Equal(t, AccessWrite, CommandAssessment{Effect: EffectUnknown}.AccessIntent().Class)
}
