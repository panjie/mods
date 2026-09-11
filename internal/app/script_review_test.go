package app

import (
	"context"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/pathutil"
	"github.com/stretchr/testify/require"
)

// scriptReviewMods builds a model with a real temporary workspace and a
// deterministic classifier stub, so script verification has a real file to bind
// and never reaches the network.
func scriptReviewMods(t *testing.T) (*Mods, string) {
	t.Helper()
	ws := t.TempDir()
	return &Mods{
		ctx:    context.Background(),
		Config: testConfigForWorkspace(ws),
		shellAnalyzer: func(string, string) approval.CommandAssessment {
			return approval.UnknownCommandAssessment()
		},
	}, ws
}

func writeScript(t *testing.T, dir, name, content string) string {
	t.Helper()
	target := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
	require.NoError(t, os.WriteFile(target, []byte(content), 0o600))
	return target
}

func processRunPayload(t *testing.T, program string, args ...string) []byte {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"program": program, "args": args})
	require.NoError(t, err)
	return payload
}

func rowValue(presentation reviewPresentation, label string) (string, bool) {
	for _, row := range presentation.rows {
		if row.Label == label {
			return row.Value, true
		}
	}
	return "", false
}

func TestScriptExecutionVerificationBindsReviewedBytes(t *testing.T) {
	m, ws := scriptReviewMods(t)
	operand := filepath.Join("tools", "check.py")
	content := "import sys\nprint(sys.argv)\n"
	target := writeScript(t, ws, operand, content)

	assessment := m.assessCommand("process_run", string(processRunPayload(t, "python3", operand)))
	facts := assessment.Reviewability.ScriptExecution
	require.NotNil(t, facts)
	require.True(t, facts.Verified())
	require.Equal(t, "python3", facts.Interpreter)
	require.Equal(t, target, facts.ResolvedPath)
	require.Equal(t, int64(len(content)), facts.SizeBytes)
	require.Equal(t, scriptDigest([]byte(content)), facts.ContentSHA256)
	require.True(t, assessment.RequiresSimplification(), "the call is still structurally opaque")
	require.False(t, assessment.StaticRead)
	require.NotEqual(t, approval.EffectRead, assessment.Effect, "an interpreter script is never a proven read")
	require.Empty(t, assessment.KnownDirs, "a guessed target must not bound the review")
	require.NoError(t, verifyReviewedScript(&assessment))
}

func TestScriptExecutionVerificationRefusesUnreviewableTargets(t *testing.T) {
	m, ws := scriptReviewMods(t)
	external := writeScript(t, t.TempDir(), "check.py", "print('x')\n")
	writeScript(t, ws, "big.py", strings.Repeat("# padding\n", 20000))
	writeScript(t, ws, "bin.py", "print(1)\x00\n")
	writeScript(t, ws, "latin.py", string([]byte{0xff, 0xfe, 0x0a}))
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "adir.py"), 0o755))

	for _, tc := range []struct {
		name    string
		operand string
	}{
		{name: "external file", operand: external},
		{name: "missing file", operand: filepath.Join(ws, "missing.py")},
		{name: "oversized file", operand: filepath.Join(ws, "big.py")},
		{name: "binary file", operand: filepath.Join(ws, "bin.py")},
		{name: "invalid utf-8", operand: filepath.Join(ws, "latin.py")},
		{name: "directory", operand: filepath.Join(ws, "adir.py")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assessment := m.assessCommand("process_run", string(processRunPayload(t, "python3", tc.operand)))
			facts := assessment.Reviewability.ScriptExecution
			require.NotNil(t, facts, "the narrow shape is still detected")
			require.False(t, facts.Verified(), "unreviewable targets stay opaque")
			require.True(t, assessment.RequiresSimplification())
			require.Error(t, newCommandPreflightGate(m.Config).check("process_run", assessment))
		})
	}
}

func TestScriptExecutionVerificationRefusesSymlinkEscape(t *testing.T) {
	m, ws := scriptReviewMods(t)
	outside := writeScript(t, t.TempDir(), "check.py", "print('outside')\n")
	link := filepath.Join(ws, "link.py")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	assessment := m.assessCommand("process_run", string(processRunPayload(t, "python3", link)))
	facts := assessment.Reviewability.ScriptExecution
	require.NotNil(t, facts)
	require.False(t, facts.Verified(), "a symlinked leaf must not carry the reviewed path outside the boundary")
}

func TestScriptExecutionPOSIXShellVerification(t *testing.T) {
	m, ws := scriptReviewMods(t)
	relative := path.Join("tools", "check.py")
	target := writeScript(t, ws, filepath.FromSlash(relative), "print('ok')\n")

	verified := m.assessShellCommand("shell_run", pathutil.FlavorPOSIX, "python3 "+relative, nil, ws)
	facts := verified.Reviewability.ScriptExecution
	require.NotNil(t, facts)
	require.True(t, facts.Verified())
	require.Equal(t, filepath.Clean(target), filepath.Clean(facts.ResolvedPath))

	outside := writeScript(t, t.TempDir(), "check.py", "print('x')\n")
	external := m.assessShellCommand("shell_run", pathutil.FlavorPOSIX, "python3 "+filepath.ToSlash(outside), nil, ws)
	require.NotNil(t, external.Reviewability.ScriptExecution)
	require.False(t, external.Reviewability.ScriptExecution.Verified())
}

func TestScriptExecutionClassifierReadCannotAutoApprove(t *testing.T) {
	ws := t.TempDir()
	m := &Mods{
		ctx:    context.Background(),
		Config: testConfigForWorkspace(ws),
		shellAnalyzer: func(string, string) approval.CommandAssessment {
			return approval.CommandAssessment{Effect: approval.EffectRead, Reason: "classifier said read"}
		},
	}
	relative := path.Join("tools", "check.py")
	writeScript(t, ws, filepath.FromSlash(relative), "print('ok')\n")

	assessment := m.assessShellCommand("shell_run", pathutil.FlavorPOSIX, "python3 "+relative, nil, ws)
	require.NotEqual(t, approval.EffectRead, assessment.Effect, "a classifier read must not authorize an interpreter payload")
	require.False(t, assessment.StaticRead)
	require.True(t, assessment.Reviewability.ScriptExecution.Verified())
	require.Equal(t, DecisionAsk,
		ClassifyAccess(assessment.AccessIntent(), WorkspaceScope(ws), m.safeDirs(), ApprovalReviewMode(ReviewAuto)))
}

func TestVerifiedScriptExecutionReleasesGateAndOthersDoNot(t *testing.T) {
	cfg := testConfigForWorkspace(t.TempDir())
	verified := approval.CommandAssessment{
		Effect: approval.EffectUnknown,
		Shape:  approval.CommandShape{TopLevelActions: 1, Opaque: true},
		Reviewability: approval.CommandReviewability{
			Level:           approval.ReviewabilityOpaque,
			Reasons:         []approval.ReviewabilityReason{approval.ReviewabilityScriptExecution},
			ScriptExecution: &approval.ScriptExecutionFacts{Interpreter: "python3", Operand: "check.py", ResolvedPath: "/ws/check.py", ContentSHA256: "abcd"},
		},
	}
	require.True(t, verifiedScriptExecution(verified))
	gate := newCommandPreflightGate(cfg)
	require.NoError(t, gate.check("process_run", verified), "a verified script call reaches ordinary review")

	unverified := verified
	unverified.Reviewability.ScriptExecution = &approval.ScriptExecutionFacts{Interpreter: "python3", Operand: "check.py"}
	require.False(t, verifiedScriptExecution(unverified))
	require.Error(t, gate.check("process_run", unverified), "an unverifiable script call still consumes the budget")
	require.Error(t, gate.check("process_run", unverified))
	require.ErrorIs(t, gate.check("process_run", unverified), errCommandRejected)

	// The verified exemption must never cover a compound or runtime-resolved
	// command even when script facts are present.
	compound := verified
	compound.Shape.TopLevelActions = 3
	require.False(t, verifiedScriptExecution(compound))
	dynamic := verified
	dynamic.DynamicTargets = []string{"$TARGET"}
	require.False(t, verifiedScriptExecution(dynamic))
	remote := verified
	remote.UnresolvedRemoteTargets = []string{"origin"}
	require.False(t, verifiedScriptExecution(remote))

	never := testConfigForWorkspace(t.TempDir())
	never.ReviewMode = ReviewNever
	require.NoError(t, newCommandPreflightGate(never).check("process_run", unverified))
	require.True(t, pagedReviewTool("process_run"), "script review must be paginated so approval waits for every page")
	require.True(t, pagedReviewTool("shell_run"))
}

func TestScriptReviewPresentationShowsCompleteSource(t *testing.T) {
	m, ws := scriptReviewMods(t)
	operand := filepath.Join("tools", "check.py")
	content := "import sys\nprint(sys.argv)\n"
	target := writeScript(t, ws, operand, content)
	payload := processRunPayload(t, "python3", operand)
	assessment := m.assessCommand("process_run", string(payload))
	facts := assessment.Reviewability.ScriptExecution
	require.True(t, facts.Verified())
	intent := buildAccessIntent("process_run", payload, nil, &assessment)
	scope := WorkspaceScope(ws)

	presentation := formatReviewPresentationWithIntent("process_run", payload, assessment, scope, intent)
	require.Equal(t, target, mustRow(t, presentation, "Script"))
	require.Equal(t, content, mustRow(t, presentation, "Script source (complete)"))
	require.Equal(t, shortScriptDigest(scriptDigest([]byte(content))), mustRow(t, presentation, "Script SHA-256"))
	require.Empty(t, candidateRulesForIntent(intent, scope, m.safeDirs(), ApprovalReviewMode(ReviewAuto)),
		"running a script must not offer a saveable path rule")

	// A change between assessment and presentation rebinds the digest to the
	// bytes actually displayed, and that is what execution then verifies.
	updated := content + "print('more')\n"
	require.NoError(t, os.WriteFile(target, []byte(updated), 0o600))
	refreshed := formatReviewPresentationWithIntent("process_run", payload, assessment, scope, intent)
	require.Equal(t, updated, mustRow(t, refreshed, "Script source (complete)"))
	require.Equal(t, scriptDigest([]byte(updated)), facts.ContentSHA256)
	require.NoError(t, verifyReviewedScript(&assessment))

	// An unreadable script clears the binding, so a stale approval cannot run.
	require.NoError(t, os.Remove(target))
	missing := formatReviewPresentationWithIntent("process_run", payload, assessment, scope, intent)
	require.Contains(t, mustRow(t, missing, "Script source"), "unavailable")
	require.False(t, facts.Verified())
	require.ErrorIs(t, verifyReviewedScript(&assessment), errScriptReviewUnavailable)
}

func TestReviewedScriptChangeRefusesExecution(t *testing.T) {
	m, ws := scriptReviewMods(t)
	operand := filepath.Join("tools", "check.py")
	target := writeScript(t, ws, operand, "print('one')\n")
	assessment := m.assessCommand("process_run", string(processRunPayload(t, "python3", operand)))
	require.NoError(t, verifyReviewedScript(&assessment))

	require.NoError(t, os.WriteFile(target, []byte("print('two')\n"), 0o600))
	require.ErrorIs(t, verifyReviewedScript(&assessment), errScriptReviewChanged)

	require.NoError(t, os.Remove(target))
	require.ErrorIs(t, verifyReviewedScript(&assessment), errScriptReviewUnavailable)
}

func TestScriptExecutionNeedsReviewDespiteWorkspaceSavedRule(t *testing.T) {
	oldInputTTY := IsInputTTY
	IsInputTTY = func() bool { return true }
	t.Cleanup(func() { IsInputTTY = oldInputTTY })

	m, ws := scriptReviewMods(t)
	operand := filepath.Join("tools", "check.py")
	writeScript(t, ws, operand, "print('ok')\n")
	payload := processRunPayload(t, "python3", operand)
	assessment := m.assessCommand("process_run", string(payload))
	require.True(t, assessment.Reviewability.ScriptExecution.Verified())
	intent := buildAccessIntent("process_run", payload, nil, &assessment)

	// A saved write rule covering the whole workspace must not release a script
	// run: the rule speaks for a path, not for the bytes that will execute.
	reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: WorkspaceScope(ws), raw: true}
	reviewer.rules.Add(RulesForDirs([]string{ws}, WorkspaceScope(ws), AccessWrite)...)
	err := reviewer.requestApproval(reviewerDeps{
		ctx:            context.Background(),
		shellExecution: true,
		assessment:     &assessment,
		accessIntent:   intent,
		safeDirs:       m.safeDirs(),
	}, "process_run", payload)
	require.ErrorIs(t, err, errReviewUnavailable)
}

func mustRow(t *testing.T, presentation reviewPresentation, label string) string {
	t.Helper()
	value, ok := rowValue(presentation, label)
	require.Truef(t, ok, "review presentation is missing the %q row", label)
	return value
}
