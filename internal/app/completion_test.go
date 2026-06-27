package app

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEnsureKeyAPIKeyCmd(t *testing.T) {
	t.Run("empty command", func(t *testing.T) {
		mods := &Mods{ctx: context.Background(), Config: &Config{}}
		_, err := mods.ensureKey(API{APIKeyCmd: "   "}, "MODS_TEST_EMPTY_KEY", "")
		if err == nil {
			t.Fatal("expected empty api-key-cmd to fail")
		}
	})

	t.Run("failed command does not include command output", func(t *testing.T) {
		mods := &Mods{ctx: context.Background(), Config: &Config{}}
		_, err := mods.ensureKey(API{APIKeyCmd: failingAPIKeyCmd()}, "MODS_TEST_FAILING_KEY", "")
		if err == nil {
			t.Fatal("expected failing api-key-cmd to fail")
		}
		if strings.Contains(err.Error(), "secret-key-output") {
			t.Fatal("api-key-cmd error leaked command output")
		}
	})

	t.Run("timeout", func(t *testing.T) {
		oldTimeout := apiKeyCmdTimeout
		apiKeyCmdTimeout = 10 * time.Millisecond
		t.Cleanup(func() { apiKeyCmdTimeout = oldTimeout })

		mods := &Mods{ctx: context.Background(), Config: &Config{}}
		_, err := mods.ensureKey(API{APIKeyCmd: slowAPIKeyCmd()}, "MODS_TEST_TIMEOUT_KEY", "")
		if err == nil {
			t.Fatal("expected api-key-cmd timeout")
		}
		if !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("expected timeout error, got %v", err)
		}
	})
}

func failingAPIKeyCmd() string {
	if runtime.GOOS == "windows" {
		return `powershell.exe -NoProfile -Command "Write-Output secret-key-output; exit 1"`
	}
	return `sh -c 'echo secret-key-output; exit 1'`
}

func slowAPIKeyCmd() string {
	if runtime.GOOS == "windows" {
		return `powershell.exe -NoProfile -Command "Start-Sleep -Milliseconds 500"`
	}
	return `sh -c 'sleep 1'`
}

// TestRetryDoesNotBlock asserts retry() returns immediately rather than
// sleeping; the back-off wait is deferred to Update via retryMsg.wait so the
// Bubble Tea event loop stays responsive to Ctrl+C, animations, and
// keystrokes during retry windows that can exceed 3 seconds at higher retry
// counts. A regression introducing a synchronous time.Sleep here would freeze
// the UI for the entire back-off duration.
func TestRetryDoesNotBlock(t *testing.T) {
	m := testMods(t)
	m.Config.MaxRetries = 5
	m.retries = 0

	start := time.Now()
	msg := m.retry("prompt", modsError{ReasonText: "test"})
	elapsed := time.Since(start)

	rm, ok := msg.(retryMsg)
	require.True(t, ok, "retry must produce retryMsg, got %T", msg)
	require.Equal(t, "prompt", rm.content)
	require.Greater(t, rm.wait, time.Duration(0), "wait must be positive")
	require.Less(t, elapsed, 10*time.Millisecond,
		"retry must not block; elapsed=%v indicates a time.Sleep regressed", elapsed)
}

// TestRetryReturnsErrorWhenMaxRetriesReached preserves the existing semantics
// that the original modsError is propagated unchanged once the retry budget
// is exhausted, so the user sees the underlying provider failure rather than
// a synthetic "max retries" error.
func TestRetryReturnsErrorWhenMaxRetriesReached(t *testing.T) {
	m := testMods(t)
	m.Config.MaxRetries = 2
	m.retries = 2 // already at max

	msg := m.retry("prompt", modsError{ReasonText: "exhausted"})
	me, ok := msg.(modsError)
	require.True(t, ok, "expected modsError after max retries, got %T", msg)
	require.Equal(t, "exhausted", me.ReasonText)
}

// TestUpdateSchedulesRetryTick verifies Update() routes retryMsg through a
// tea.Tick that ultimately delivers completionInput, rather than firing the
// retry synchronously inside the current Update call.
func TestUpdateSchedulesRetryTick(t *testing.T) {
	m := testMods(t)
	// 1ms keeps the test fast while still exercising the tick path.
	_, cmd := m.Update(retryMsg{content: "x", wait: 1 * time.Millisecond})
	require.NotNil(t, cmd, "Update must return a tea.Cmd for retryMsg")

	msg := cmd()
	ci, ok := msg.(completionInput)
	require.True(t, ok, "tick must produce completionInput, got %T", msg)
	require.Equal(t, "x", ci.content)
}
