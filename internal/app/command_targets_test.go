package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/panjie/mods/internal/approval"
	ca "github.com/panjie/mods/internal/commandanalysis"
	"github.com/panjie/mods/internal/pathutil"
	"github.com/panjie/mods/internal/proto"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

func roleTarget(role ca.TargetRole, dir, leaf string) ca.Target {
	op := ca.OperationFollowLeaf
	if role == ca.TargetRead {
		op = ca.OperationRead
	}
	path := filepath.Join(dir, leaf)
	return ca.Target{Kind: ca.TargetFilesystem, Role: role, Operation: op, Raw: path, LexicalPath: path, Path: path, Dir: dir, State: ca.TargetResolved}
}
func TestCommandRoleAdapterMixedTargets(t *testing.T) {
	root := t.TempDir()
	input, output := filepath.Join(root, "input"), filepath.Join(root, "output")
	result := ca.Result{Effect: ca.EffectWrite, Evidence: ca.EvidenceStatic, WriteCoverage: ca.CoverageComplete, KnownDirs: []string{input, output}, Targets: []ca.Target{roleTarget(ca.TargetRead, input, "source"), roleTarget(ca.TargetWrite, output, "dest"), roleTarget(ca.TargetCandidate, root, "noise")}}
	intent := commandResultToIntent(result)
	require.Equal(t, []string{input}, intent.ReadDirs)
	require.Equal(t, []string{output}, intent.WriteDirs)
	rules := approval.RulesForDirs([]string{output}, WorkingDirScope(root), AccessWrite)
	require.True(t, RulesAllowIntent(rules, intent, WorkingDirScope(root), nil, ApprovalReviewMode(ReviewAuto)), "complete copy needs only destination write authority")
	result.Targets[0].Role = ca.TargetWrite
	result.Targets[0].Operation = ca.OperationRenameEntry
	intent = commandResultToIntent(result)
	require.False(t, RulesAllowIntent(rules, intent, WorkingDirScope(root), nil, ApprovalReviewMode(ReviewAuto)), "move requires the source entry too")
	rules = append(rules, approval.RulesForDirs([]string{input}, WorkingDirScope(root), AccessWrite)...)
	require.True(t, RulesAllowIntent(rules, intent, WorkingDirScope(root), nil, ApprovalReviewMode(ReviewAuto)))
	// Candidate/raw legacy paths can never become a reusable write rule.
	result.Targets = []ca.Target{roleTarget(ca.TargetCandidate, output, "dest")}
	intent = commandResultToIntent(result)
	require.Empty(t, intent.WriteDirs)
	require.False(t, intent.HasCompleteWriteTargets())
	require.Empty(t, candidateRulesForIntent(intent, WorkingDirScope(root), nil, ApprovalReviewMode(ReviewAuto)))
}

func TestCommandRoleAdapterRejectsInvalidFacts(t *testing.T) {
	root := t.TempDir()
	valid := ca.Result{Effect: ca.EffectWrite, Evidence: ca.EvidenceStatic, WriteCoverage: ca.CoverageComplete, Targets: []ca.Target{roleTarget(ca.TargetWrite, root, "dest")}}
	mutations := map[string]func(*ca.Result){
		"zero result":              func(r *ca.Result) { *r = ca.Result{} },
		"missing evidence":         func(r *ca.Result) { r.Evidence = ca.EvidenceUnknown },
		"trust cannot prove write": func(r *ca.Result) { r.Evidence = ca.EvidenceTrust },
		"unrelated directory":      func(r *ca.Result) { r.Targets[0].Dir = filepath.Join(root, "other") },
		"invalid evidence":         func(r *ca.Result) { r.Evidence = "invalid" },
		"unknown effect":           func(r *ca.Result) { r.Effect = ca.EffectUnknown },
		"invalid effect":           func(r *ca.Result) { r.Effect = "invalid" },
		"invalid coverage":         func(r *ca.Result) { r.WriteCoverage = 99 },
		"gap":                      func(r *ca.Result) { r.Gaps = []ca.AssessmentGap{ca.GapWriteTargets} },
		"zero role":                func(r *ca.Result) { r.Targets[0].Role = 0 },
		"invalid role":             func(r *ca.Result) { r.Targets[0].Role = 99 },
		"zero kind":                func(r *ca.Result) { r.Targets[0].Kind = 0 },
		"invalid kind":             func(r *ca.Result) { r.Targets[0].Kind = 99 },
		"zero operation":           func(r *ca.Result) { r.Targets[0].Operation = 0 },
		"invalid operation":        func(r *ca.Result) { r.Targets[0].Operation = 99 },
		"unknown destination":      func(r *ca.Result) { r.Targets[0].Operation = ca.OperationDestination },
		"unresolved":               func(r *ca.Result) { r.Targets[0].State = ca.TargetUnresolved },
		"invalid state":            func(r *ca.Result) { r.Targets[0].State = 99 },
		"relative":                 func(r *ca.Result) { r.Targets[0].Dir = "relative" },
		"missing path":             func(r *ca.Result) { r.Targets[0].Path = "" },
		"provider":                 func(r *ca.Result) { r.Targets[0].Kind = ca.TargetProvider; r.Targets[0].Path = `HKCU:\Settings` },
		"invalid origin":           func(r *ca.Result) { r.Targets[0].Kind = ca.TargetOrigin; r.Targets[0].Path = "unresolved-origin" },
		"unknown remote":           func(r *ca.Result) { r.UnresolvedRemoteTargets = []string{"remote"} },
		"legacy only":              func(r *ca.Result) { r.Targets = nil; r.KnownDirs = []string{root} },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			r := valid.Clone()
			mutate(&r)
			i := commandResultToIntent(r)
			require.False(t, i.HasCompleteWriteTargets())
			require.Equal(t, DecisionAsk, ClassifyAccess(i, WorkingDirScope(root), []string{root}, ApprovalReviewMode(ReviewAuto)))
			require.Equal(t, DecisionAllow, ClassifyAccess(i, WorkingDirScope(root), nil, ApprovalReviewMode(ReviewNever)))
		})
	}
	for _, evidence := range []ca.Evidence{ca.EvidenceStatic, ca.EvidenceTrust} {
		read := ca.Result{Effect: ca.EffectRead, Evidence: evidence, WriteCoverage: ca.CoverageComplete, DynamicTargets: []string{"$read"}}
		require.Equal(t, AccessRead, commandResultToIntent(read).DominantClass())
		read.Targets = []ca.Target{roleTarget(ca.TargetRead, root, "source")}
		read.Targets[0].Kind = 99
		require.Equal(t, AccessWrite, commandResultToIntent(read).DominantClass(), "invalid facts cannot inherit read permission")
	}
	remote := valid.Clone()
	remote.Targets[0] = ca.Target{Kind: ca.TargetOrigin, Role: ca.TargetWrite, Path: "https://example.com/repo", State: ca.TargetResolved}
	require.Equal(t, []string{"https://example.com"}, commandResultToIntent(remote).WriteOrigins)
}

func TestCommandAuthorizationComparisonIgnoresPresentation(t *testing.T) {
	root := t.TempDir()
	r := ca.Result{Effect: ca.EffectWrite, Evidence: ca.EvidenceStatic, WriteCoverage: ca.CoverageComplete, Targets: []ca.Target{roleTarget(ca.TargetWrite, root, "first")}}
	a := commandResultToIntent(r)
	r.Reason = "different diagnostic"
	r.Structure = &ca.CommandStructure{ControlFlowDepth: 3, UnmodeledInvocations: 1, Findings: []ca.StructureFinding{{Code: ca.StructureUnknownInvocation}}}
	r.KnownDirs = []string{"noise"}
	r.Targets = append(r.Targets, roleTarget(ca.TargetCandidate, root, "candidate"))
	require.True(t, commandAuthorizationEqual(a, commandResultToIntent(r)))
	r.Targets = append(r.Targets, roleTarget(ca.TargetRead, filepath.Join(root, "read-source"), "input"))
	require.True(t, commandAuthorizationEqual(a, commandResultToIntent(r)), "reads remain independently allowed")
	r.Targets[0].Path = filepath.Join(root, "second")
	require.False(t, commandAuthorizationEqual(a, commandResultToIntent(r)), "same directory but different leaf requires fresh review")
}

func TestCommandTargetsDriveActualReviewAndDispatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell cases; synthetic intent tests are portable")
	}
	root := t.TempDir()
	source, dest := filepath.Join(root, "input"), filepath.Join(root, "output")
	require.NoError(t, os.MkdirAll(source, 0700))
	require.NoError(t, os.MkdirAll(dest, 0700))
	cfg := defaultConfig()
	cfg.WorkingDir = root
	cfg.ReviewMode = ReviewAuto
	cfg.InteractiveTTYAvailable = false
	m := &Mods{Config: &cfg, ctx: context.Background(), reviewer: newToolReviewer(&cfg)}
	m.reviewer.rules.Add(RulesForDirs([]string{dest}, WorkingDirScope(root), AccessWrite)...)
	called := 0
	registry := toolregistry.NewRegistry()
	require.NoError(t, registry.Register(toolregistry.Tool{Spec: proto.ToolSpec{Name: "shell_run"}, Capabilities: toolregistry.ToolCapabilities{ShellExecution: true}, Call: func(context.Context, json.RawMessage) (string, error) { called++; return "ok", nil }}))
	for _, tc := range []struct {
		command string
		allow   bool
	}{
		{"uniq " + strconv.Quote(filepath.Join(source, "in")) + " " + strconv.Quote(filepath.Join(dest, "out")), true},
		{"cat \"$MISSING_INPUT/source\" > " + strconv.Quote(filepath.Join(dest, "out")), true},
		{"mystery " + strconv.Quote(filepath.Join(dest, "out")), false},
		{"mystery > " + strconv.Quote(filepath.Join(dest, "out")), false},
		{"cp " + strconv.Quote(filepath.Join(source, "in")) + " " + strconv.Quote(filepath.Join(dest, "out")), false},
		{"mv " + strconv.Quote(filepath.Join(source, "in")) + " " + strconv.Quote(filepath.Join(dest, "out")), false},
	} {
		before := called
		data, _ := json.Marshal(map[string]string{"command": tc.command})
		_, err := m.toolCaller(registry, &cfg)(proto.ToolCallRequest{Name: "shell_run", Arguments: data})
		if tc.allow {
			require.NoError(t, err, tc.command)
			require.Equal(t, before+1, called)
		} else {
			require.ErrorIs(t, err, errReviewUnavailable, tc.command)
			require.Equal(t, before, called)
		}
	}
}

func TestCommandLeafChangeReapprovesAndRepeatedChangeStops(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a file symlink")
	}
	for _, repeat := range []bool{false, true} {
		t.Run(strconv.FormatBool(repeat), func(t *testing.T) {
			root := t.TempDir()
			first, second := filepath.Join(root, "first"), filepath.Join(root, "second")
			require.NoError(t, os.WriteFile(first, []byte("a"), 0600))
			require.NoError(t, os.WriteFile(second, []byte("b"), 0600))
			link := filepath.Join(root, "link")
			require.NoError(t, os.Symlink(first, link))
			cfg := defaultConfig()
			cfg.WorkingDir = root
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			r := &toolReviewer{reviewMode: ReviewAuto, scope: WorkingDirScope(root), reviewAvailabilityKnown: true, interactiveReviewAvailable: true, reviewChan: make(chan toolReviewItem, 1)}
			m := &Mods{Config: &cfg, ctx: ctx, reviewer: r}
			called := false
			registry := toolregistry.NewRegistry()
			require.NoError(t, registry.Register(toolregistry.Tool{Spec: proto.ToolSpec{Name: "shell_run"}, Capabilities: toolregistry.ToolCapabilities{ShellExecution: true}, Call: func(context.Context, json.RawMessage) (string, error) { called = true; return "ok", nil }}))
			data, _ := json.Marshal(map[string]string{"command": "touch " + strconv.Quote(link)})
			done := make(chan error, 1)
			go func() {
				_, err := m.toolCaller(registry, &cfg)(proto.ToolCallRequest{Name: "shell_run", Arguments: data})
				done <- err
			}()
			item := receiveReviewItem(t, r.reviewChan)
			require.NoError(t, os.Remove(link))
			require.NoError(t, os.Symlink(second, link))
			item.resp <- reviewResponse{approved: true}
			item = receiveReviewItem(t, r.reviewChan)
			require.False(t, called)
			if repeat {
				require.NoError(t, os.Remove(link))
				require.NoError(t, os.Symlink(first, link))
			}
			item.resp <- reviewResponse{approved: true}
			err := <-done
			if repeat {
				require.ErrorContains(t, err, "changed repeatedly")
				require.False(t, called)
			} else {
				require.NoError(t, err)
				require.True(t, called)
			}
		})
	}
}

func TestTargetResolutionCannotBorrowLegacyDirs(t *testing.T) {
	root := t.TempDir()
	engine := ca.NewEngine(ca.Settings{}, ca.Dependencies{})
	result, err := engine.Analyze(context.Background(), ca.Input{Cwd: root, Dialect: pathutil.FlavorPOSIX, Process: &ca.ProcessInput{Program: "mystery", Args: []string{t.TempDir()}}})
	require.NoError(t, err)
	require.NotEmpty(t, result.KnownDirs)
	intent := commandResultToIntent(result)
	require.Empty(t, intent.WriteDirs)
	require.False(t, intent.HasCompleteWriteTargets())
}

func TestMixedUnknownWriteGroupSurvivesNormalization(t *testing.T) {
	root := t.TempDir()
	i := AccessIntent{Class: AccessWrite, ReadDirs: []string{root}, WriteDirs: []string{}, WriteCoverage: approval.CoverageIncomplete}
	for range 2 {
		i = normalizeAccessIntentDirs(i, root, "shell_run", true)
	}
	require.Equal(t, AccessWrite, i.DominantClass())
	require.NotNil(t, i.WriteDirs)
	require.Equal(t, DecisionAsk, ClassifyAccess(i, WorkingDirScope(root), []string{root}, ApprovalReviewMode(ReviewAuto)))
}
func TestDynamicReadIsNotPresentedAsUnknownWrite(t *testing.T) {
	root := t.TempDir()
	e := ca.NewEngine(ca.Settings{}, ca.Dependencies{})
	result, err := e.Analyze(context.Background(), ca.Input{Cwd: root, Dialect: pathutil.FlavorPOSIX, Shell: &ca.ShellInput{Source: `cat "$UNKNOWN_READ/source" > output`}})
	require.NoError(t, err)
	assessment := commandResult(result)
	intent := commandResultToIntent(result)
	require.True(t, intent.HasCompleteWriteTargets())
	require.Empty(t, intent.UnresolvedPaths)
	presentation := formatReviewPresentationWithIntent("shell_run", []byte(`{"command":"cat \"$UNKNOWN_READ/source\" > output"}`), assessment, WorkingDirScope(root), intent)
	require.Equal(t, "Modify local files", presentation.headline)
	require.Contains(t, presentation.rows, interactionRow{Label: "Read source (unresolved)", Value: "${UNKNOWN_READ}/source"})
}
