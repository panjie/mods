package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/panjie/mods/internal/proto"
)

func TestRegistry(t *testing.T) {
	registry := NewRegistry()
	err := registry.Register(Tool{
		Spec: proto.ToolSpec{Name: "echo"},
		Call: func(_ context.Context, data json.RawMessage) (string, error) {
			return string(data), nil
		},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	tool, ok := registry.Tool("echo")
	if !ok {
		t.Fatal("expected registered tool metadata")
	}
	if tool.Kind != ToolKindBuiltin {
		t.Fatalf("unexpected default kind: %q", tool.Kind)
	}
	if registry.TimeoutPolicy("echo") != TimeoutPolicyCaller {
		t.Fatalf("unexpected default timeout policy: %q", registry.TimeoutPolicy("echo"))
	}

	if err := registry.Register(Tool{Spec: proto.ToolSpec{Name: "echo"}, Call: func(context.Context, json.RawMessage) (string, error) {
		return "", nil
	}}); err == nil {
		t.Fatal("expected duplicate tool registration to fail")
	}

	out, err := registry.Call(context.Background(), "echo", []byte(`{"ok":true}`))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if out != `{"ok":true}` {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestFilesystemToolsStayInsideRoot(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	if err := RegisterFilesystem(registry, FilesystemConfig{Root: root}); err != nil {
		t.Fatalf("register filesystem: %v", err)
	}

	_, err := registry.Call(context.Background(), "fs_read_file", []byte(`{"path":"../outside.txt"}`))
	if err == nil {
		t.Fatal("expected path outside root to fail")
	}
}

func TestFilesystemReadWriteSearch(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	if err := RegisterFilesystem(registry, FilesystemConfig{Root: root}); err != nil {
		t.Fatalf("register filesystem: %v", err)
	}

	_, err := registry.Call(context.Background(), "fs_write_file", []byte(`{"path":"notes/a.txt","content":"alpha\nbeta\n"}`))
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	content, err := registry.Call(context.Background(), "fs_read_file", []byte(`{"path":"notes/a.txt"}`))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if content != "alpha\nbeta\n" {
		t.Fatalf("unexpected content: %q", content)
	}

	result, err := registry.Call(context.Background(), "fs_search", []byte(`{"path":".","query":"beta"}`))
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !strings.Contains(result, "notes/a.txt:2:beta") {
		t.Fatalf("unexpected search result: %q", result)
	}

	if _, err := os.Stat(filepath.Join(root, "notes", "a.txt")); err != nil {
		t.Fatalf("written file missing: %v", err)
	}
}

func TestFilesystemApplyPatch(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	if err := RegisterFilesystem(registry, FilesystemConfig{Root: root}); err != nil {
		t.Fatalf("register filesystem: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	patch := `--- a/a.txt
+++ b/a.txt
@@ -1 +1 @@
-old
+new
`
	_, err := registry.Call(context.Background(), "fs_apply_patch", []byte(`{"patch":`+strconv.Quote(patch)+`}`))
	if err != nil {
		t.Fatalf("apply patch: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatalf("read patched file: %v", err)
	}
	if string(content) != "new\n" {
		t.Fatalf("unexpected patched content: %q", content)
	}
}

func TestPowerShellRun(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell tool is Windows-only")
	}

	root := t.TempDir()
	registry := NewRegistry()
	if err := RegisterPowerShell(registry, ShellConfig{Root: root}); err != nil {
		t.Fatalf("register powershell: %v", err)
	}
	tool, ok := registry.Tool("powershell_run")
	if !ok {
		t.Fatal("expected powershell tool metadata")
	}
	if tool.Kind != ToolKindShell {
		t.Fatalf("unexpected kind: %q", tool.Kind)
	}
	if registry.TimeoutPolicy("powershell_run") != TimeoutPolicySelf {
		t.Fatalf("unexpected timeout policy: %q", registry.TimeoutPolicy("powershell_run"))
	}

	t.Run("runs basic command", func(t *testing.T) {
		out, err := registry.Call(context.Background(), "powershell_run", []byte(`{"command":"Write-Output ok"}`))
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		if strings.TrimSpace(out) != "ok" {
			t.Fatalf("unexpected output: %q", out)
		}
	})

	t.Run("runs variables and pipelines without nested quoting", func(t *testing.T) {
		out, err := registry.Call(context.Background(), "powershell_run", []byte(`{"command":"1,2,3 | Where-Object { $_ -gt 1 } | Measure-Object | Select-Object -ExpandProperty Count"}`))
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		if strings.TrimSpace(out) != "2" {
			t.Fatalf("unexpected output: %q", out)
		}
	})

	t.Run("returns typed error and output for nonzero exit", func(t *testing.T) {
		out, err := registry.Call(context.Background(), "powershell_run", []byte(`{"command":"Write-Output before; exit 7"}`))
		if err == nil {
			t.Fatal("expected exit error")
		}
		var exitErr ShellExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("expected ShellExitError, got %T: %v", err, err)
		}
		if exitErr.Code != 7 {
			t.Fatalf("unexpected exit code: %d", exitErr.Code)
		}
		if !strings.Contains(out, "before") || !strings.Contains(out, "[exit status 7]") {
			t.Fatalf("unexpected output: %q", out)
		}
	})

	t.Run("captures large output with cap", func(t *testing.T) {
		limited := NewRegistry()
		if err := RegisterPowerShell(limited, ShellConfig{Root: root, MaxOutputChars: 64}); err != nil {
			t.Fatalf("register powershell: %v", err)
		}
		out, err := limited.Call(context.Background(), "powershell_run", []byte(`{"command":"[Console]::Out.Write(('x' * 5000))"}`))
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		if !strings.Contains(out, "[Output truncated at 64 chars.]") {
			t.Fatalf("expected truncation marker, got %q", out)
		}
		if len(out) > 140 {
			t.Fatalf("output was not capped enough: %d", len(out))
		}
	})

	t.Run("uses shell timeout", func(t *testing.T) {
		limited := NewRegistry()
		if err := RegisterPowerShell(limited, ShellConfig{Root: root, Timeout: 50 * time.Millisecond}); err != nil {
			t.Fatalf("register powershell: %v", err)
		}
		_, err := limited.Call(context.Background(), "powershell_run", []byte(`{"command":"Start-Sleep -Seconds 5"}`))
		if err == nil {
			t.Fatal("expected timeout error")
		}
	})
}
