package app

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
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
