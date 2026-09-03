package app

import (
	"context"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/panjie/mods/internal/approval"
	"github.com/stretchr/testify/require"
)

func TestExtractLiteralRemoteOrigins(t *testing.T) {
	got := extractLiteralRemoteOrigins(`curl -X POST 'HTTPS://user:secret@API.Example.com:443/v1?q=token'; scp file git@GitHub.com:org/repo.git`)
	require.ElementsMatch(t, []string{"https://api.example.com", "ssh://github.com"}, got)
	require.Empty(t, extractLiteralRemoteOrigins(`curl -X POST "$API_URL"`))
	require.Empty(t, extractLiteralRemoteOrigins(`copy C:\temp\file D:\out\file`))
	require.Empty(t, extractLiteralRemoteOrigins(`custom-writer key:value`))
	require.Equal(t, []string{"ssh://example.com"}, extractLiteralRemoteOrigins(`scp file example.com:path/to/file`))
}

func TestAssessCommandAddsDeterministicRemoteOrigin(t *testing.T) {
	workspace := t.TempDir()
	m := &Mods{
		ctx:    context.Background(),
		Config: testConfigForWorkspace(workspace),
		shellAnalyzer: func(_, _ string) approval.CommandAssessment {
			return approval.CommandAssessment{Effect: approval.EffectWrite, Reason: "HTTP mutation"}
		},
	}
	assessment := m.assessCommand("shell_run", `curl -X POST https://user:secret@API.Example.com:443/v1/items?token=x`)
	require.Equal(t, approval.EffectWrite, assessment.Effect)
	require.Equal(t, []string{"https://api.example.com"}, assessment.RemoteOrigins)
	require.NotContains(t, assessment.RemoteOrigins[0], "secret")
}

func TestRemoteCredentialsAreRedactedFromReviewText(t *testing.T) {
	command := `curl -X POST 'https://user:secret@API.Example.com:443/v1/items?token=hidden#part'`
	assessment := approval.CommandAssessment{Effect: approval.EffectWrite, RemoteOrigins: []string{"https://api.example.com"}}
	scope := WorkspaceScope(t.TempDir())
	summary := shellRiskSummary(command, assessment, scope)
	presentation := formatReviewPresentationWithIntent(
		"shell_run", []byte(`{"command":`+strconv.Quote(command)+`}`), assessment, scope,
		assessment.AccessIntent(),
	)

	require.Contains(t, summary, "https://api.example.com")
	require.NotContains(t, summary, "user")
	require.NotContains(t, summary, "secret")
	require.NotContains(t, summary, "token=hidden")
	require.Contains(t, presentation.rows[0].Value, "https://api.example.com")
	require.NotContains(t, presentation.rows[0].Value, "user")
	require.NotContains(t, presentation.rows[0].Value, "secret")
	require.NotContains(t, presentation.rows[0].Value, "hidden")
}

func TestResolveGitPushOrigin(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	require.NoError(t, exec.Command("git", "-C", repo, "init", "-q").Run())
	require.NoError(t, exec.Command("git", "-C", repo, "remote", "add", "origin", "git@GitHub.com:acme/project.git").Run())
	m := &Mods{ctx: context.Background(), Config: testConfigForWorkspace(repo), shellAnalyzer: func(_, _ string) approval.CommandAssessment {
		return approval.CommandAssessment{Effect: approval.EffectWrite, KnownDirs: []string{repo}, Reason: "pushes refs"}
	}}
	assessment := m.assessCommand("shell_run", "git push origin main")
	require.Equal(t, []string{"ssh://github.com"}, assessment.RemoteOrigins)
	require.Contains(t, assessment.KnownDirs, filepath.Clean(repo))

	parent := filepath.Dir(repo)
	process := &Mods{ctx: context.Background(), Config: testConfigForWorkspace(parent)}
	processAssessment := process.assessCommand("process_run", `{"program":"git","args":["-C",`+strconv.Quote(repo)+`,"push","origin","main"]}`)
	require.Equal(t, []string{"ssh://github.com"}, processAssessment.RemoteOrigins)
}

func TestResolvePowerShellGitPushOrigin(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	require.NoError(t, exec.Command("git", "-C", repo, "init", "-q").Run())
	require.NoError(t, exec.Command("git", "-C", repo, "remote", "add", "upstream", "ssh://git@example.com:2222/acme/project.git").Run())
	m := &Mods{ctx: context.Background(), Config: testConfigForWorkspace(repo), shellAnalyzer: func(_, _ string) approval.CommandAssessment {
		return approval.CommandAssessment{Effect: approval.EffectWrite, Reason: "pushes refs"}
	}}
	assessment := m.assessCommand("powershell_run", "git push upstream main")
	require.Equal(t, []string{"ssh://example.com:2222"}, assessment.RemoteOrigins)
}

func TestUnresolvedGitPushRemoteCannotBeSaved(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	require.NoError(t, exec.Command("git", "-C", repo, "init", "-q").Run())
	m := &Mods{ctx: context.Background(), Config: testConfigForWorkspace(repo), shellAnalyzer: func(_, _ string) approval.CommandAssessment {
		return approval.CommandAssessment{Effect: approval.EffectWrite, KnownDirs: []string{repo}, Reason: "pushes refs"}
	}}

	assessment := m.assessCommand("shell_run", "git push missing main")
	require.Equal(t, []string{"missing"}, assessment.UnresolvedRemoteTargets)
	intent := assessment.AccessIntent()
	require.Equal(t, DecisionAsk, ClassifyAccess(intent, WorkspaceScope(repo), nil, ApprovalReviewMode(ReviewAuto)))
	require.Empty(t, candidateRulesForIntent(intent, WorkspaceScope(repo), nil, ApprovalReviewMode(ReviewAuto)))
	presentation := formatReviewPresentationWithIntent("shell_run", []byte(`{"command":"git push missing main"}`), assessment, WorkspaceScope(repo), intent)
	require.Equal(t, "Modify an unknown remote resource", presentation.headline)
	require.Contains(t, presentation.rows, interactionRow{Label: "Remote", Value: "Unknown"})

	process := m.assessCommand("process_run", `{"program":"git","args":["push","missing","main"]}`)
	require.Equal(t, []string{"missing"}, process.UnresolvedRemoteTargets)
}

func TestExplicitGitPushURLProducesOrigin(t *testing.T) {
	workspace := t.TempDir()
	m := &Mods{ctx: context.Background(), Config: testConfigForWorkspace(workspace), shellAnalyzer: func(_, _ string) approval.CommandAssessment {
		return approval.CommandAssessment{Effect: approval.EffectWrite, Reason: "pushes refs"}
	}}
	assessment := m.assessCommand("shell_run", "git push https://user:secret@Git.Example.com:443/acme/repo.git main")
	require.Equal(t, []string{"https://git.example.com"}, assessment.RemoteOrigins)
	require.Empty(t, assessment.UnresolvedRemoteTargets)
}

func TestRemoteWriteCandidatesAndSavedRules(t *testing.T) {
	scope := WorkspaceScope(t.TempDir())
	intent := AccessIntent{
		Class:         AccessWrite,
		Dirs:          []string{scope.Value},
		RemoteOrigins: []string{"https://API.example.com:443/v1"},
	}
	rules := candidateRulesForIntent(intent, scope, nil, ApprovalReviewMode(ReviewAuto))
	require.Len(t, rules, 2)
	require.Equal(t, approval.DirAllow, rules[0].Type)
	require.Equal(t, approval.RemoteAllow, rules[1].Type)
	require.Equal(t, []string{"https://api.example.com"}, rules[1].Origins)
	require.True(t, RulesAllowIntent(rules, intent, scope, nil, ApprovalReviewMode(ReviewAuto)))
	require.False(t, RulesAllowIntent(rules[:1], intent, scope, nil, ApprovalReviewMode(ReviewAuto)))
	require.Empty(t, candidateRulesForIntent(intent, scope, nil, ApprovalReviewMode(ReviewAlways)))
}

func TestUnknownRemoteWriteHasNoAlwaysRule(t *testing.T) {
	scope := WorkspaceScope(t.TempDir())
	intent := AccessIntent{Class: AccessWrite, UnresolvedRemoteTargets: []string{"$API_URL"}}
	require.Equal(t, DecisionAsk, ClassifyAccess(intent, scope, nil, ApprovalReviewMode(ReviewAuto)))
	require.Empty(t, candidateRulesForIntent(intent, scope, nil, ApprovalReviewMode(ReviewAuto)))
}

func TestUnknownEffectWithKnownOriginHasNoAlwaysRule(t *testing.T) {
	scope := WorkspaceScope(t.TempDir())
	intent := approval.CommandAssessment{
		Effect: approval.EffectUnknown, RemoteOrigins: []string{"https://api.example.com"},
	}.AccessIntent()
	require.True(t, intent.UncertainEffect)
	require.Empty(t, candidateRulesForIntent(intent, scope, nil, ApprovalReviewMode(ReviewAuto)))
}

func TestTemporaryWriteAlwaysAllowsEvenInAlwaysMode(t *testing.T) {
	safe := filepath.Clean(t.TempDir())
	scope := WorkspaceScope(t.TempDir())
	intent := AccessIntent{Class: AccessWrite, Dirs: []string{safe}}
	require.Equal(t, DecisionAllow, ClassifyAccess(intent, scope, []string{safe}, ApprovalReviewMode(ReviewAlways)))
	reviewer := &toolReviewer{reviewMode: ReviewAlways, scope: scope}
	require.NoError(t, reviewer.requestApproval(reviewerDeps{ctx: context.Background(), accessIntent: intent, safeDirs: []string{safe}}, "fs_write_file", []byte(`{"path":"tmp.txt","content":"x"}`)))
}
