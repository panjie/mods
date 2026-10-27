package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/panjie/mods/internal/commandanalysis"
	"github.com/panjie/mods/internal/config"
	"github.com/panjie/mods/internal/evals"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/stream"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

const organizationNestedPOSIX = "for x in a; do for y in b; do mystery; done; done"

func organizationCommand() string {
	if runtime.GOOS == "windows" {
		return "foreach ($x in 1) { foreach ($y in 2) { mystery } }"
	}
	return organizationNestedPOSIX
}

func TestCommandOrganizationReasons(t *testing.T) {
	for _, tc := range []struct{ source, reason string }{
		{organizationNestedPOSIX, "nested_control_flow"},
		{"touch out; " + organizationNestedPOSIX, "nested_control_flow"},
		{`a=0; b=0; for x in a; do mystery "$a" "$b"; done`, "stateful_aggregation"},
		{`eval 'mystery'`, "dynamic_evaluation"},
		{`sh -c 'mystery'; bash -c 'mystery'`, ""},
		{`sh -c "$(bash -c 'mystery')"`, "nested_inline_hosts"},
		{`a=0; a=1; for x in a; do mystery "$a"; done`, ""},
		{`sh -c 'mystery'`, ""},
		{`mystery`, ""}, {`make all`, ""}, {`printf '%s' 'a;{}'; printf b`, ""},
		{`/custom/eval 'mystery'`, ""},
		{`touch out`, ""}, {`if then`, ""},
		{`for x in a; do for y in b; do python existing.py; done; done`, ""},
		{`for x in a; do for y in b; do sh "$existing"; done; done`, ""},
		{`for x in a; do for y in b; do python existing.py -c arg; done; done`, ""},
		{`for x in a; do for y in b; do env python existing; done; done`, ""},
	} {
		t.Run(tc.source, func(t *testing.T) {
			a := commandanalysis.AssessShellStaticWithPolicy(tc.source, true, commandanalysis.ReadOnlyCommandPolicy{})
			if tc.reason == "" {
				require.Empty(t, commandOrganizationReasons(&a))
			} else {
				require.Contains(t, commandOrganizationReasons(&a), tc.reason)
			}
		})
	}
	a := commandanalysis.AssessShellStaticWithPolicy(organizationNestedPOSIX, true, commandanalysis.ReadOnlyCommandPolicy{})
	for _, gap := range []commandanalysis.AssessmentGap{commandanalysis.GapParse, commandanalysis.GapAnalysisCancelled, commandanalysis.GapAnalysisBudget} {
		copy := a.Clone()
		copy.Gaps = append(copy.Gaps, gap)
		require.Empty(t, commandOrganizationReasons(&copy))
	}
	for _, effect := range []commandanalysis.CommandEffect{commandanalysis.EffectRead, commandanalysis.EffectWrite} {
		copy := a.Clone()
		copy.Effect = effect
		copy.Gaps = []commandanalysis.AssessmentGap{commandanalysis.GapWriteTargets}
		require.Empty(t, commandOrganizationReasons(&copy))
	}
	a.Evidence = commandanalysis.EvidenceTrust
	require.Empty(t, commandOrganizationReasons(&a))
}

func TestCommandOrganizationConcurrentBudgetAndSource(t *testing.T) {
	a := commandanalysis.AssessShellStaticWithPolicy(organizationNestedPOSIX, true, commandanalysis.ReadOnlyCommandPolicy{})
	s := newCommandOrganizationState(context.Background(), "Summarize project statistics")
	defer s.close()
	var feedback atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if len(s.consumeAutomatic(context.Background(), fmt.Sprint(i), organizationNestedPOSIX, &a)) > 0 {
				feedback.Add(1)
			}
		}(i)
	}
	wg.Wait()
	require.EqualValues(t, 1, feedback.Load())
	for _, source := range []string{"", "Execute this: " + organizationNestedPOSIX, "Run exactly this code: ```sh\nmystery\n```"} {
		state := newCommandOrganizationState(context.Background(), source)
		require.Empty(t, state.consumeAutomatic(context.Background(), "a", organizationNestedPOSIX, &a))
		state.close()
	}
	state := newCommandOrganizationState(context.Background(), "Summarize project statistics")
	state.markManual("manual", organizationNestedPOSIX)
	require.Empty(t, state.consumeAutomatic(context.Background(), "new-id", organizationNestedPOSIX, &a))
	require.NotEmpty(t, state.consumeAutomatic(context.Background(), "other", organizationNestedPOSIX+"; mystery", &a))
	state.close()
	require.Empty(t, state.consumeAutomatic(context.Background(), "late", organizationNestedPOSIX, &a))
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	state = newCommandOrganizationState(context.Background(), "Summarize")
	defer state.close()
	require.Empty(t, state.consumeAutomatic(canceled, "cancelled", organizationNestedPOSIX, &a))
	require.False(t, state.used)
	inline := commandanalysis.AssessShellStaticWithPolicy(`eval 'mystery arg'`, true, commandanalysis.ReadOnlyCommandPolicy{})
	provided := newCommandOrganizationState(context.Background(), "Run mystery arg unchanged")
	defer provided.close()
	require.Empty(t, provided.consumeAutomatic(context.Background(), "inline", `eval 'mystery arg'`, &inline))
	state.bindSourceContext([]proto.Message{{Role: proto.RoleUser, Content: organizationNestedPOSIX}})
	require.Empty(t, state.consumeAutomatic(context.Background(), "history", organizationNestedPOSIX, &a))
	answerState := newCommandOrganizationState(context.Background(), "Summarize project")
	defer answerState.close()
	answer, _ := json.Marshal(toolregistry.UserInputResponse{Answer: organizationNestedPOSIX})
	answerState.protectUserResponse(string(answer))
	require.Empty(t, answerState.consumeAutomatic(context.Background(), "user-answer", organizationNestedPOSIX, &a))
	require.False(t, answerState.used)
}

func organizationRegistry(t *testing.T, name string, kind toolregistry.ToolKind, calls *atomic.Int32) *toolregistry.Registry {
	t.Helper()
	registry := toolregistry.NewRegistry()
	require.NoError(t, registry.Register(toolregistry.Tool{
		Spec: proto.ToolSpec{Name: name}, Kind: kind, Capabilities: toolregistry.ToolCapabilities{Mutable: true, ShellExecution: true},
		Call: func(context.Context, json.RawMessage) (string, error) { calls.Add(1); return "executed", nil },
	}))
	return registry
}

func TestCommandOrganizationDispatchModesAndThreeRounds(t *testing.T) {
	for _, setting := range []config.CommandSimplification{config.CommandSimplificationOff, config.CommandSimplificationOnce} {
		for _, mode := range []ReviewMode{ReviewAuto, ReviewAlways, ReviewNever} {
			for _, minimal := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/minimal=%v", setting, mode, minimal), func(t *testing.T) {
					cfg := defaultConfig()
					cfg.WorkingDir = t.TempDir()
					cfg.CommandSimplification = setting
					cfg.ReviewMode = mode
					cfg.Minimal = minimal
					cfg.InteractiveTTYAvailable = false
					m := &Mods{Config: &cfg, ctx: context.Background(), reviewer: newToolReviewer(&cfg)}
					m.replaceCommandOrganization(newCommandOrganizationState(m.ctx, "Summarize the project"))
					defer m.closeCommandOrganization()
					var calls atomic.Int32
					for round := 0; round < 3; round++ {
						registry := organizationRegistry(t, "shell_run", toolregistry.ToolKindShell, &calls)
						data, _ := json.Marshal(map[string]string{"command": organizationCommand()})
						call := proto.ToolCallRequest{ID: fmt.Sprint(round), Name: "shell_run", Arguments: data}
						msg, status := stream.CallTool(call, nil, m.toolResultCaller(registry, &cfg))
						require.Equal(t, call.ID, msg.ToolCalls[0].ID)
						if setting == config.CommandSimplificationOnce && mode != ReviewNever && round == 0 {
							var control *commandReorganizationError
							require.ErrorAs(t, status.Err, &control)
							require.True(t, control.Automatic)
							require.True(t, msg.ToolCalls[0].IsError)
							require.Contains(t, msg.Content, `"automatic_remaining":0`)
							require.Contains(t, msg.Content, "nested_control_flow")
						} else if mode == ReviewNever {
							require.NoError(t, status.Err)
						} else {
							require.ErrorIs(t, status.Err, errReviewUnavailable)
						}
					}
					if mode == ReviewNever {
						require.EqualValues(t, 3, calls.Load())
					} else {
						require.Zero(t, calls.Load())
					}
				})
			}
		}
	}
}

func TestCommandOrganizationRetryCheckpointAndOldCallback(t *testing.T) {
	cfg := defaultConfig()
	cfg.WorkingDir = t.TempDir()
	cfg.NoInstructions = true
	cfg.CommandSimplification = config.CommandSimplificationOnce
	cfg.Prefix = "Summarize project"
	m := &Mods{Config: &cfg, ctx: context.Background(), reviewer: newToolReviewer(&cfg)}
	_ = m.startCompletionCmd("")
	first := m.currentCommandOrganization()
	require.Contains(t, first.userContent, cfg.Prefix)
	var calls atomic.Int32
	registry := organizationRegistry(t, "shell_run", toolregistry.ToolKindShell, &calls)
	callback := m.toolResultCaller(registry, &cfg)
	data, _ := json.Marshal(map[string]string{"command": organizationCommand()})
	call := proto.ToolCallRequest{ID: "first", Name: "shell_run", Arguments: data}
	_, err := callback(call)
	var control *commandReorganizationError
	require.ErrorAs(t, err, &control)
	m.saveCompletionCheckpoint([]proto.Message{{Role: proto.RoleUser, Content: "Summarize project"}})
	m.retries = 1
	_ = m.startCompletionCmd("retry")
	require.Same(t, first, m.currentCommandOrganization())
	require.NoError(t, m.setupCompletionContext("retry"))
	_, err = m.toolResultCaller(registry, &cfg)(call)
	require.ErrorIs(t, err, errReviewUnavailable)
	m.retries = 0
	_ = m.startCompletionCmd("A new project summary")
	defer m.closeCommandOrganization()
	require.NotSame(t, first, m.currentCommandOrganization())
	_, err = callback(call)
	require.ErrorIs(t, err, context.Canceled)
	// A queued completion from the retired runner must not close the new state.
	current := m.currentCommandOrganization()
	m.Update(streamEventMsg{kind: streamEventDone, runner: &streamRunner{organization: first}})
	require.Same(t, current, m.currentCommandOrganization())
	_, err = m.toolResultCaller(registry, &cfg)(call)
	require.ErrorAs(t, err, &control)
	require.Zero(t, calls.Load())
}

func TestCommandOrganizationMCPAndUserSourceKeepReview(t *testing.T) {
	for _, userProvided := range []bool{false, true} {
		cfg := defaultConfig()
		cfg.WorkingDir = t.TempDir()
		cfg.CommandSimplification = config.CommandSimplificationOnce
		m := &Mods{Config: &cfg, ctx: context.Background(), reviewer: newToolReviewer(&cfg)}
		source, kind := "Summarize project", toolregistry.ToolKindMCP
		if userProvided {
			source = organizationCommand()
			kind = toolregistry.ToolKindShell
		}
		m.replaceCommandOrganization(newCommandOrganizationState(m.ctx, source))
		var calls atomic.Int32
		registry := organizationRegistry(t, "shell_run", kind, &calls)
		data, _ := json.Marshal(map[string]string{"command": organizationCommand()})
		_, err := m.toolResultCaller(registry, &cfg)(proto.ToolCallRequest{ID: "original", Name: "shell_run", Arguments: data})
		require.ErrorIs(t, err, errReviewUnavailable)
		require.Zero(t, calls.Load())
		require.False(t, m.currentCommandOrganization().used)
		m.closeCommandOrganization()
	}
}

func TestCommandOrganizationManualAfterAutomaticStillIndependent(t *testing.T) {
	cfg := defaultConfig()
	cfg.WorkingDir = t.TempDir()
	cfg.CommandSimplification = config.CommandSimplificationOnce
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r := &toolReviewer{reviewMode: ReviewAuto, scope: WorkingDirScope(cfg.WorkingDir), reviewAvailabilityKnown: true, interactiveReviewAvailable: true, reviewChan: make(chan toolReviewItem, 1)}
	m := &Mods{Config: &cfg, ctx: ctx, reviewer: r}
	m.replaceCommandOrganization(newCommandOrganizationState(ctx, "Summarize project"))
	defer m.closeCommandOrganization()
	var calls atomic.Int32
	registry := organizationRegistry(t, "shell_run", toolregistry.ToolKindShell, &calls)
	data, _ := json.Marshal(map[string]string{"command": organizationCommand()})
	caller := m.toolResultCaller(registry, &cfg)
	call := proto.ToolCallRequest{ID: "auto", Name: "shell_run", Arguments: data}
	_, err := caller(call)
	var control *commandReorganizationError
	require.ErrorAs(t, err, &control)
	require.True(t, control.Automatic)
	automatic, typed := evals.ReorganizationKind(err)
	require.True(t, automatic)
	require.True(t, typed)
	for _, action := range []reviewOptionAction{reviewOptionSimplify, reviewOptionDeny} {
		finished := make(chan error, 1)
		go func() { _, err := caller(call); finished <- err }()
		item := receiveReviewItem(t, r.reviewChan)
		if action == reviewOptionSimplify {
			item.resp <- reviewResponse{action: reviewActionSimplify}
		} else {
			item.resp <- reviewResponse{action: reviewActionDeny}
		}
		err = <-finished
		if action == reviewOptionSimplify {
			require.ErrorAs(t, err, &control)
			require.False(t, control.Automatic)
		} else {
			require.False(t, errors.As(err, &control))
			require.ErrorContains(t, err, "execution denied")
		}
		require.True(t, m.currentCommandOrganization().used)
	}
	require.Zero(t, calls.Load())
}

func TestCommandOrganizationConcurrentDispatchAndCancellation(t *testing.T) {
	cfg := defaultConfig()
	cfg.WorkingDir = t.TempDir()
	cfg.CommandSimplification = config.CommandSimplificationOnce
	state := newCommandOrganizationState(context.Background(), "Summarize project")
	defer state.close()
	var calls atomic.Int32
	registry := organizationRegistry(t, "shell_run", toolregistry.ToolKindShell, &calls)
	data, _ := json.Marshal(map[string]string{"command": organizationCommand()})
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		m := &Mods{Config: &cfg, ctx: context.Background(), reviewer: newToolReviewer(&cfg)}
		caller := m.toolResultCallerForOrganization(registry, &cfg, state)
		go func(id string) {
			_, err := caller(proto.ToolCallRequest{ID: id, Name: "shell_run", Arguments: data})
			done <- err
		}(fmt.Sprint(i))
	}
	feedback := 0
	for i := 0; i < 2; i++ {
		err := <-done
		var control *commandReorganizationError
		if errors.As(err, &control) {
			feedback++
		} else {
			require.ErrorIs(t, err, errReviewUnavailable)
		}
	}
	require.Equal(t, 1, feedback)
	require.Zero(t, calls.Load())
	ctx, cancel := context.WithCancel(context.Background())
	canceled := newCommandOrganizationState(ctx, "Summarize project")
	defer canceled.close()
	cancel()
	m := &Mods{Config: &cfg, ctx: ctx, reviewer: newToolReviewer(&cfg)}
	_, err := m.toolResultCallerForOrganization(registry, &cfg, canceled)(proto.ToolCallRequest{Name: "shell_run", Arguments: data})
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, canceled.used)
	require.Zero(t, calls.Load())
}

func TestCommandOrganizationReadAndOpaqueProcessDoNotSpendBudget(t *testing.T) {
	cfg := defaultConfig()
	cfg.WorkingDir = t.TempDir()
	cfg.CommandSimplification = config.CommandSimplificationOnce
	m := &Mods{Config: &cfg, ctx: context.Background(), reviewer: newToolReviewer(&cfg)}
	m.replaceCommandOrganization(newCommandOrganizationState(m.ctx, "Summarize project"))
	defer m.closeCommandOrganization()
	var calls atomic.Int32
	read := "printf hello"
	host := "sh"
	argv := []string{"-c", "mystery"}
	if runtime.GOOS == "windows" {
		read = "Write-Output hello"
		host = "powershell.exe"
		argv = []string{"-Command", "mystery"}
	}
	registry := organizationRegistry(t, "shell_run", toolregistry.ToolKindShell, &calls)
	data, _ := json.Marshal(map[string]string{"command": read})
	_, err := m.toolResultCaller(registry, &cfg)(proto.ToolCallRequest{ID: "read", Name: "shell_run", Arguments: data})
	require.NoError(t, err)
	require.EqualValues(t, 1, calls.Load())
	require.False(t, m.currentCommandOrganization().used)
	if _, err := exec.LookPath(host); err != nil {
		t.Skipf("process fixture needs %s: %v", host, err)
	}
	registry = organizationRegistry(t, "process_run", toolregistry.ToolKindShell, &calls)
	data, _ = json.Marshal(map[string]any{"program": host, "args": argv})
	_, err = m.toolResultCaller(registry, &cfg)(proto.ToolCallRequest{ID: "process", Name: "process_run", Arguments: data})
	require.ErrorIs(t, err, errReviewUnavailable)
	require.EqualValues(t, 1, calls.Load())
	require.False(t, m.currentCommandOrganization().used)
}
