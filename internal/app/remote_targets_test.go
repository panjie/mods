package app

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/commandanalysis"
	"github.com/stretchr/testify/require"
)

func TestAssessCommandAddsDeterministicRemoteOrigin(t *testing.T) {
	cwd := t.TempDir()
	m := &Mods{
		ctx:    context.Background(),
		Config: testConfigForWorkingDir(cwd),
	}
	assessment := m.assessCommand("shell_run", `curl -X POST https://user:secret@API.Example.com:443/v1/items?token=x`)
	require.Equal(t, commandanalysis.EffectUnknown, assessment.Effect)
	require.Equal(t, []string{"https://api.example.com"}, assessment.RemoteOrigins)
	require.NotContains(t, assessment.RemoteOrigins[0], "secret")
}

func TestRemoteCredentialsAreRedactedFromReviewText(t *testing.T) {
	command := `curl -X POST 'https://user:secret@API.Example.com:443/v1/items?token=hidden#part'`
	assessment := commandanalysis.CommandAssessment{Effect: commandanalysis.EffectWrite, RemoteOrigins: []string{"https://api.example.com"}}
	scope := WorkingDirScope(t.TempDir())
	summary := shellRiskSummary(command, assessment, scope)
	presentation := formatReviewPresentationWithIntent(
		"shell_run", []byte(`{"command":`+strconv.Quote(command)+`}`), assessment, scope,
		commandResultToIntent(assessment),
	)

	require.Contains(t, summary, "https://api.example.com")
	require.NotContains(t, summary, "user")
	require.NotContains(t, summary, "secret")
	require.NotContains(t, summary, "token=hidden")
	require.Contains(t, presentation.rows[0].Value, "https://[redacted]@API.Example.com:443/v1/items?[redacted]#[redacted]")
	require.NotContains(t, presentation.rows[0].Value, "user")
	require.NotContains(t, presentation.rows[0].Value, "secret")
	require.NotContains(t, presentation.rows[0].Value, "hidden")
}

func TestReviewURLRedactionPreservesCommandSyntax(t *testing.T) {
	for _, tc := range []struct{ command, want string }{
		{`curl https://example.com/x;touch${IFS}/tmp/marker`, `curl https://example.com/x;touch${IFS}/tmp/marker`},
		{`curl https://example.com/x|sh`, `curl https://example.com/x|sh`},
		{`curl https://example.com/x&&reboot`, `curl https://example.com/x&&reboot`},
		{`curl https://example.com/x?token=secret;reboot`, `curl https://example.com/x?[redacted];reboot`},
		{`curl 'https://user:secret@EXAMPLE.com:443/a%20b?token=hidden&key=private#secret' && echo done`, `curl 'https://[redacted]@EXAMPLE.com:443/a%20b?[redacted]#[redacted]' && echo done`},
		{`curl "https://example.com/x?token=secret$(touch /tmp/marker)"`, `curl "https://example.com/x?[redacted]$(touch /tmp/marker)"`},
		{`sh -c 'curl https://example.com/x?token=secret&&reboot'`, `sh -c 'curl https://example.com/x?[redacted]&&reboot'`},
		{`Invoke-WebRequest 'https://example.com/path?q=secret'; Remove-Item file`, `Invoke-WebRequest 'https://example.com/path?[redacted]'; Remove-Item file`},
		{`curl https://example.com/path?token=secret&echo done`, `curl https://example.com/path?[redacted]&echo done`},
		{`curl ''https://example.com/x?token=secret&reboot`, `curl ''https://example.com/x?[redacted]&reboot`},
		{`sh -c "curl 'https://example.com/x?token=secret&reboot'"`, `sh -c "curl 'https://example.com/x?[redacted]&reboot'"`},
	} {
		t.Run(tc.command, func(t *testing.T) {
			require.Equal(t, tc.want, redactRemoteURLsForDisplay(tc.command))
		})
	}
}

func TestResolveGitPushOrigin(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	require.NoError(t, exec.Command("git", "-C", repo, "init", "-q").Run())
	require.NoError(t, exec.Command("git", "-C", repo, "remote", "add", "origin", "git@GitHub.com:acme/project.git").Run())
	m := &Mods{ctx: context.Background(), Config: testConfigForWorkingDir(repo)}
	assessment := m.assessCommand("shell_run", "git push origin main")
	require.Equal(t, []string{"ssh://github.com"}, assessment.RemoteOrigins)
	require.Empty(t, assessment.KnownDirs, "classifier cwd is not a write fact")

	parent := filepath.Dir(repo)
	process := &Mods{ctx: context.Background(), Config: testConfigForWorkingDir(parent)}
	processAssessment := process.assessCommand("process_run", `{"program":"git","args":["-C",`+strconv.Quote(repo)+`,"push","origin","main"]}`)
	require.Equal(t, []string{"ssh://github.com"}, processAssessment.RemoteOrigins)
}

func TestResolvePowerShellGitPushOrigin(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("real PowerShell bridge requires Windows")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	require.NoError(t, exec.Command("git", "-C", repo, "init", "-q").Run())
	require.NoError(t, exec.Command("git", "-C", repo, "remote", "add", "upstream", "ssh://git@example.com:2222/acme/project.git").Run())
	m := &Mods{ctx: context.Background(), Config: testConfigForWorkingDir(repo)}
	assessment := m.assessCommand("powershell_run", "git push upstream main")
	require.Equal(t, []string{"ssh://example.com:2222"}, assessment.RemoteOrigins)
}

func TestUnresolvedGitPushRemoteCannotBeSaved(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	require.NoError(t, exec.Command("git", "-C", repo, "init", "-q").Run())
	m := &Mods{ctx: context.Background(), Config: testConfigForWorkingDir(repo)}

	assessment := m.assessCommand("shell_run", "git push missing main")
	require.Equal(t, []string{"missing"}, assessment.UnresolvedRemoteTargets)
	intent := commandResultToIntent(assessment)
	require.Equal(t, DecisionAsk, ClassifyAccess(intent, WorkingDirScope(repo), nil, ApprovalReviewMode(ReviewAuto)))
	require.Empty(t, candidateRulesForIntent(intent, WorkingDirScope(repo), nil, ApprovalReviewMode(ReviewAuto)))
	presentation := formatReviewPresentationWithIntent("shell_run", []byte(`{"command":"git push missing main"}`), assessment, WorkingDirScope(repo), intent)
	require.Equal(t, "Run with unknown effects", presentation.headline)
	require.Contains(t, presentation.rows, interactionRow{Label: "Remote (unresolved)", Value: "missing"})

	process := m.assessCommand("process_run", `{"program":"git","args":["push","missing","main"]}`)
	require.Equal(t, []string{"missing"}, process.UnresolvedRemoteTargets)
}

func TestExplicitGitPushURLProducesOrigin(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	cwd := t.TempDir()
	require.NoError(t, exec.Command("git", "-C", cwd, "init", "-q").Run())
	m := &Mods{ctx: context.Background(), Config: testConfigForWorkingDir(cwd)}
	assessment := m.assessCommand("shell_run", "git push https://user:secret@Git.Example.com:443/acme/repo.git main")
	require.Equal(t, []string{"https://git.example.com"}, assessment.RemoteOrigins)
	require.Empty(t, assessment.UnresolvedRemoteTargets)
}

func TestRemoteWriteCandidatesAndSavedRules(t *testing.T) {
	scope := WorkingDirScope(t.TempDir())
	intent := AccessIntent{WriteCoverage: testCompleteCoverage,
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
	scope := WorkingDirScope(t.TempDir())
	intent := AccessIntent{WriteCoverage: testCompleteCoverage, Class: AccessWrite, UnresolvedRemoteTargets: []string{"$API_URL"}}
	require.Equal(t, DecisionAsk, ClassifyAccess(intent, scope, nil, ApprovalReviewMode(ReviewAuto)))
	require.Empty(t, candidateRulesForIntent(intent, scope, nil, ApprovalReviewMode(ReviewAuto)))
}

func TestUnknownEffectWithKnownOriginHasNoAlwaysRule(t *testing.T) {
	scope := WorkingDirScope(t.TempDir())
	intent := commandResultToIntent(commandanalysis.CommandAssessment{
		Effect: commandanalysis.EffectUnknown, RemoteOrigins: []string{"https://api.example.com"},
	})
	require.True(t, intent.UncertainEffect)
	rules := candidateRulesForIntent(intent, scope, nil, ApprovalReviewMode(ReviewAuto))
	require.Empty(t, rules)
	require.False(t, RulesAllowIntent(rules, intent, scope, nil, ApprovalReviewMode(ReviewAuto)))
}

func TestTemporaryWriteAlwaysAllowsEvenInAlwaysMode(t *testing.T) {
	safe := filepath.Clean(t.TempDir())
	scope := WorkingDirScope(t.TempDir())
	intent := AccessIntent{WriteCoverage: testCompleteCoverage, Class: AccessWrite, Dirs: []string{safe}}
	require.Equal(t, DecisionAllow, ClassifyAccess(intent, scope, []string{safe}, ApprovalReviewMode(ReviewAlways)))
	reviewer := &toolReviewer{reviewMode: ReviewAlways, scope: scope}
	require.NoError(t, reviewer.requestApproval(reviewerDeps{ctx: context.Background(), accessIntent: intent, safeDirs: []string{safe}}, "fs_write_file", []byte(`{"path":"tmp.txt","content":"x"}`)))
}

func TestExplicitGitPushURLQueryFailureStaysUnresolved(t *testing.T) {
	cwd := filepath.Join(t.TempDir(), "missing")
	m := &Mods{ctx: context.Background()}
	_, unresolved, _ := m.resolveGitPushArgvOrigins([]string{"push", "https://git.example.com/acme/repo.git"}, cwd)
	require.NotEmpty(t, unresolved)
}
