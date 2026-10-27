package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/panjie/mods/internal/commandanalysis"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/stream"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestP3PayloadLiteralPathsAndIdentity(t *testing.T) {
	root := t.TempDir()
	t.Setenv("P3_SCRIPT", "wrong.py")
	for _, name := range []string{"$P3_SCRIPT.py", "'quoted'.py", "中文😀.py", "~literal.py"} {
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte("print('literal')\n"), 0o600))
		payload := preparePayloadFixture(t, filepath.Join(root, "python"), []string{name}, root)
		require.True(t, payload.Complete, payload.Unavailable)
		require.Equal(t, filepath.Join(root, name), payload.Path)
		require.Equal(t, "print('literal')\n", payload.Source)
		// Identical bytes at a replacement inode must still require re-review.
		require.NoError(t, os.Rename(payload.Path, payload.Path+".old"))
		require.NoError(t, os.WriteFile(payload.Path, []byte(payload.Source), 0o600))
		changed, err := payload.revalidate(context.Background())
		require.NoError(t, err)
		require.True(t, changed)
	}
}

func TestP3PayloadCancellationAndReaderBound(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := readCommandPayloadFile(ctx, "unused")
	require.ErrorIs(t, err, context.Canceled)
	// Simulate OS operations that have not returned after their callers cancel.
	for i := 0; i < cap(commandPayloadReaders); i++ {
		commandPayloadReaders <- struct{}{}
	}
	defer func() {
		for i := 0; i < cap(commandPayloadReaders); i++ {
			<-commandPayloadReaders
		}
	}()
	payload := preparePayloadFixture(t, "python", []string{"unused.py"}, t.TempDir())
	require.False(t, payload.Complete)
	require.Contains(t, payload.Unavailable, "busy")
}

func TestP3PayloadPowerShellBinding(t *testing.T) {
	for _, args := range [][]string{
		{"-File", "script.ps1", "argument"},
		{"-NoProfile", "-NonInteractive", "-File", "script.ps1", "-Command"},
	} {
		path, ok := literalProcessScript("pwsh.exe", args)
		require.True(t, ok)
		require.Equal(t, "script.ps1", path)
	}
	for _, args := range [][]string{{"-File", "-"}, {"-Command", "script.ps1"}, {"-f", "script.ps1"}, {"-Unknown", "-File", "script.ps1"}} {
		_, ok := literalProcessScript("pwsh.exe", args)
		require.False(t, ok)
	}
}

func TestP3SourceLocationsAndControls(t *testing.T) {
	for _, tc := range []struct {
		unit       string
		start, end int
		ok         bool
	}{
		{"byte", 8, 9, true}, {"utf16", 4, 5, true},
		{"byte", 1, 9, false}, {"utf16", 2, 5, false},
		{"utf16", 0, 2, false}, {"byte", 0, 1, false},
		{"unknown", 0, 1, false}, {"byte", 9, 8, false},
	} {
		line, ok := commandFindingLine("中😀\nx", tc.unit, commandanalysis.StructureFinding{Located: true, Start: tc.start, End: tc.end})
		require.Equal(t, tc.ok, ok)
		if ok {
			require.Equal(t, 2, line)
		}
	}
	source := numberedReviewSource("safe\rhidden\n\x1b[2J\u202eevil")
	literal := reviewLiteral(source)
	require.Contains(t, literal, `safe\u000dhidden`)
	require.Contains(t, literal, `\u001b[2J\u202eevil`)
	require.NotContains(t, literal, "\x1b")
}

func TestP3PayloadRedactionBeforeNumbering(t *testing.T) {
	payload := &commandPayload{Complete: true, Source: "first\nsecret\nlast\nhttps://user:password@example.com/?token=value"}
	rows := payload.reviewRows(func(s string) string { return strings.ReplaceAll(s, "first\nsecret\nlast", "[REDACTED]") })
	for _, row := range rows {
		require.NotContains(t, row.Value, "secret")
		require.NotContains(t, row.Value, "password")
		require.NotContains(t, row.Value, "token=value")
	}
}

func TestP3CancelDuringActualDispatchReview(t *testing.T) {
	cfg := defaultConfig()
	cfg.WorkingDir = t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r := &toolReviewer{reviewMode: ReviewAuto, scope: WorkingDirScope(cfg.WorkingDir), reviewAvailabilityKnown: true, interactiveReviewAvailable: true, reviewChan: make(chan toolReviewItem, 1)}
	m := &Mods{Config: &cfg, ctx: ctx, ctxCancel: cancel, reviewer: r, userInput: newUserInputManager(&cfg)}
	registry := toolregistry.NewRegistry()
	var executed atomic.Int32
	require.NoError(t, registry.Register(toolregistry.Tool{
		Spec: proto.ToolSpec{Name: "shell_run"}, Capabilities: toolregistry.ToolCapabilities{Mutable: true, ShellExecution: true},
		Call: func(context.Context, json.RawMessage) (string, error) { executed.Add(1); return "unexpected", nil },
	}))
	done := make(chan error, 1)
	go func() {
		_, err := m.toolCaller(registry, &cfg)(proto.ToolCallRequest{Name: "shell_run", Arguments: []byte(`{"command":"p3-unknown-program"}`)})
		done <- err
	}()
	r.handleStartMsg(toolReviewStartMsg{item: receiveReviewItem(t, r.reviewChan)})
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	require.NotNil(t, cmd)
	m.Update(cmd())
	require.ErrorIs(t, <-done, context.Canceled)
	require.Zero(t, executed.Load())
}

func TestP3SimplificationPreservesSiblingResults(t *testing.T) {
	// Exercise the provider-neutral contract: successful siblings (including
	// media) are returned once, with their original IDs and group positions.
	counts := map[string]int{}
	caller := func(call proto.ToolCallRequest) (proto.ToolResult, error) {
		counts[call.ID]++
		if call.ID == "simplify" {
			return proto.ToolResult{}, &commandReorganizationError{ReasonCodes: []string{"user_requested"}}
		}
		return proto.ToolResult{Text: "done", Media: []proto.MediaAttachment{{Kind: "image", Source: call.ID, Data: []byte{1, 2, 3}}}}, nil
	}
	var results []proto.Message
	for i, id := range []string{"before", "simplify", "after"} {
		msg, status := stream.CallTool(proto.ToolCallRequest{ID: id, Index: i, Total: 3, Name: "fixture", Arguments: []byte(`{}`)}, nil, caller)
		require.Equal(t, id, msg.ToolCalls[0].ID)
		require.Equal(t, i, status.Index)
		require.Equal(t, 3, status.Total)
		require.Equal(t, id == "simplify", msg.ToolCalls[0].IsError)
		results = append(results, msg)
	}
	require.Equal(t, map[string]int{"before": 1, "simplify": 1, "after": 1}, counts)
	require.Empty(t, results[1].Media)
	require.Equal(t, "before", results[0].Media[0].Source)
	require.Equal(t, "after", results[2].Media[0].Source)
}
