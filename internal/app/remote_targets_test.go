package app

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/panjie/mods/internal/approval"
	"github.com/stretchr/testify/require"
)

func TestRemoteCredentialsAreRedactedFromReviewText(t *testing.T) {
	command := `curl -X POST 'https://user:secret@API.Example.com:443/v1/items?token=hidden#part'`
	scope := WorkingDirScope(t.TempDir())
	args := []byte(`{"command":` + strconv.Quote(command) + `,"effect":"may_write"}`)
	summary := formatReviewSummaryWithIntent("shell_run", args, scope, AccessIntent{})
	presentation := formatReviewPresentationWithIntent("shell_run", args, scope, AccessIntent{})
	require.NotContains(t, summary, "secret")
	require.NotContains(t, summary, "token=hidden")
	source := presentation.rows[1].Value
	require.Contains(t, source, "https://[redacted]@API.Example.com:443/v1/items?[redacted]#[redacted]")
	require.NotContains(t, source, "user")
	require.NotContains(t, source, "secret")
	require.NotContains(t, source, "hidden")
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

func TestTemporaryWriteAlwaysAllowsEvenInAlwaysMode(t *testing.T) {
	safe := filepath.Clean(t.TempDir())
	scope := WorkingDirScope(t.TempDir())
	intent := AccessIntent{WriteCoverage: testCompleteCoverage, Class: AccessWrite, Dirs: []string{safe}}
	require.Equal(t, DecisionAllow, ClassifyAccess(intent, scope, []string{safe}, ApprovalReviewMode(ReviewAlways)))
	reviewer := &toolReviewer{reviewMode: ReviewAlways, scope: scope}
	require.NoError(t, reviewer.requestApproval(reviewerDeps{ctx: context.Background(), accessIntent: intent, safeDirs: []string{safe}}, "fs_write_file", []byte(`{"path":"tmp.txt","content":"x"}`)))
}
