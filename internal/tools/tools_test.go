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

	"github.com/panjie/mods/internal/approval"
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

func TestRegistryCloseRunsClosersReverseOrder(t *testing.T) {
	registry := NewRegistry()
	var calls []string
	registry.AddCloser(func() error {
		calls = append(calls, "first")
		return nil
	})
	registry.AddCloser(func() error {
		calls = append(calls, "second")
		return nil
	})
	if err := registry.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if strings.Join(calls, ",") != "second,first" {
		t.Fatalf("unexpected close order: %v", calls)
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

func TestFilesystemApplyPatchRefusesSymlink(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	if err := RegisterFilesystem(registry, FilesystemConfig{Root: root}); err != nil {
		t.Fatalf("register filesystem: %v", err)
	}

	symlinkPatches := map[string]string{
		"new file": "diff --git a/escape b/escape\nnew file mode 120000\nindex 0000000..abc1234\n--- /dev/null\n+++ b/escape\n@@ -0,0 +1 @@\n+/etc\n",
		"old mode": "diff --git a/link b/link\nold mode 100644\nnew mode 120000\nindex 1111111..2222222\n--- a/link\n+++ b/link\n@@ -1 +1 @@\n-old\n+/etc\n",
	}
	for name, patch := range symlinkPatches {
		t.Run(name, func(t *testing.T) {
			_, err := registry.Call(context.Background(), "fs_apply_patch", []byte(`{"patch":`+strconv.Quote(patch)+`}`))
			if err == nil {
				t.Fatal("expected symlink patch to be refused")
			}
			if !strings.Contains(err.Error(), "symlink") {
				t.Fatalf("expected symlink error, got: %v", err)
			}
			// Ensure the symlink was actually NOT created.
			if _, statErr := os.Lstat(filepath.Join(root, "escape")); statErr == nil {
				t.Fatal("symlink escape was created despite rejection")
			}
			if _, statErr := os.Lstat(filepath.Join(root, "link")); statErr == nil {
				t.Fatal("symlink link was created despite rejection")
			}
		})
	}
}

func TestFilesystemApplyPatchRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	if err := RegisterFilesystem(registry, FilesystemConfig{Root: root}); err != nil {
		t.Fatalf("register filesystem: %v", err)
	}

	// A patch that tries to write outside the workspace via a literal `..` path.
	patch := "--- a/../outside.txt\n+++ b/../outside.txt\n@@ -0,0 +1 @@\n+pwned\n"
	_, err := registry.Call(context.Background(), "fs_apply_patch", []byte(`{"patch":`+strconv.Quote(patch)+`}`))
	if err == nil {
		t.Fatal("expected traversal patch to be rejected")
	}
	if _, statErr := os.Stat(filepath.Join(root, "..", "outside.txt")); statErr == nil {
		t.Fatal("file was created outside the workspace")
	}
}

// TestFilesystemApplyPatchRejectsQuotedTraversal pins the fix for the
// validatePatchPaths bypass where strings.Fields()[0] would split a
// C-quoted path at its leading double quote, leaving the actual traversal
// component unchecked.
func TestFilesystemApplyPatchRejectsQuotedTraversal(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	if err := RegisterFilesystem(registry, FilesystemConfig{Root: root}); err != nil {
		t.Fatalf("register filesystem: %v", err)
	}

	cases := map[string]string{
		"quoted absolute path":    "--- /dev/null\n+++ \"b/../../etc/leak\"\n@@ -0,0 +1 @@\n+pwned\n",
		"quoted traversal":        "--- /dev/null\n+++ \"b/sub/../../outside\"\n@@ -0,0 +1 @@\n+pwned\n",
		"quoted with octal slash": "--- /dev/null\n+++ \"b/\\057etc/leak\"\n@@ -0,0 +1 @@\n+pwned\n",
		"unbalanced quotes":       "--- /dev/null\n+++ \"b/oops\n@@ -0,0 +1 @@\n+pwned\n",
		"unknown escape":          "--- /dev/null\n+++ \"b/\\xff\"\n@@ -0,0 +1 @@\n+pwned\n",
		"unquoted with spaces":    "--- /dev/null\n+++ b/oops file\n@@ -0,0 +1 @@\n+pwned\n",
	}
	for name, patch := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := registry.Call(context.Background(), "fs_apply_patch", []byte(`{"patch":`+strconv.Quote(patch)+`}`))
			if err == nil {
				t.Fatal("expected quoted-path traversal patch to be rejected")
			}
		})
	}
}

// TestFilesystemApplyPatchAcceptsQuotedLegitPath confirms quoted paths
// without traversal (e.g. a filename containing a space, the canonical
// reason git C-quotes paths in the first place) are still accepted.
func TestFilesystemApplyPatchAcceptsQuotedLegitPath(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	if err := RegisterFilesystem(registry, FilesystemConfig{Root: root}); err != nil {
		t.Fatalf("register filesystem: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "with space.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	// Modify an existing file whose name contains a space; git emits such
	// paths C-quoted. The validator must accept the quoted path.
	patch := "--- \"a/with space.txt\"\n+++ \"b/with space.txt\"\n@@ -1 +1 @@\n-old\n+new\n"
	if _, err := registry.Call(context.Background(), "fs_apply_patch", []byte(`{"patch":`+strconv.Quote(patch)+`}`)); err != nil {
		t.Fatalf("legitimate quoted path with space must be accepted: %v", err)
	}
}

// TestFilesystemApplyPatchRejectsRenameTraversal pins the fix for the
// rename-header bypass: a rename-only diff carries no +++/--- lines, so
// the old validatePatchPaths skipped its target entirely. A rename target
// that escapes the workspace must now be rejected too.
func TestFilesystemApplyPatchRejectsRenameTraversal(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	if err := RegisterFilesystem(registry, FilesystemConfig{Root: root}); err != nil {
		t.Fatalf("register filesystem: %v", err)
	}

	patches := map[string]string{
		"rename to":   "diff --git a/a.txt b/../escape.txt\nsimilarity index 100%\nrename from a.txt\nrename to ../escape.txt\n",
		"copy to":     "diff --git a/a.txt b/../escape.txt\nsimilarity index 100%\ncopy from a.txt\ncopy to ../escape.txt\n",
		"rename from": "diff --git a/../oops b/x\nsimilarity index 100%\nrename from ../oops\nrename to x\n",
	}
	for name, patch := range patches {
		t.Run(name, func(t *testing.T) {
			_, err := registry.Call(context.Background(), "fs_apply_patch", []byte(`{"patch":`+strconv.Quote(patch)+`}`))
			if err == nil {
				t.Fatal("expected rename/copy traversal patch to be rejected")
			}
		})
	}
}

// TestFilesystemApplyPatchAcceptsTabbedTimestamp verifies the validator
// still tolerates the POSIX patch convention where a tab separates the
// path from a timestamp suffix.
func TestFilesystemApplyPatchAcceptsTabbedTimestamp(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	if err := RegisterFilesystem(registry, FilesystemConfig{Root: root}); err != nil {
		t.Fatalf("register filesystem: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	// `git apply` accepts a TAB followed by a timestamp on +++/--- lines.
	// Validation must strip that suffix, not reject the patch.
	patch := "--- a/a.txt\t2024-01-01 12:00:00 +0000\n+++ b/a.txt\t2024-01-02 12:00:00 +0000\n@@ -1 +1 @@\n-old\n+new\n"
	if _, err := registry.Call(context.Background(), "fs_apply_patch", []byte(`{"patch":`+strconv.Quote(patch)+`}`)); err != nil {
		t.Fatalf("apply patch with timestamps: %v", err)
	}
}

// TestResolveWorkspacePathRejectsSymlinkEscapeFromSafeDir pins the fix for
// the safe-dir bypass: a symlink inside the safe directory that points
// outside (e.g. /tmp/sub/link -> /etc) used to slip past
// resolveWorkspacePath because the safe-dir branch returned early without
// running EvalSymlinks. After the fix the resolved real path must still
// be inside the matched safe boundary, so escaping symlinks are rejected.
func TestResolveWorkspacePathRejectsSymlinkEscapeFromSafeDir(t *testing.T) {
	root := t.TempDir()
	safe := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatalf("seed secret: %v", err)
	}

	// Create the escaping symlink: safe/link -> outside.
	linkPath := filepath.Join(safe, "link")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Skipf("symlink creation not supported (requires admin on Windows): %v", err)
	}

	// Sanity: the lexical path is inside the safe dir.
	target := filepath.Join(safe, "link", "secret.txt")
	if _, ok := matchSafeDir(target, []string{safe}); !ok {
		t.Fatal("test sanity: matchSafeDir must report target inside safe dir")
	}

	if _, err := resolveWorkspacePath(context.Background(), root, target, []string{safe}); err == nil {
		t.Fatalf("resolveWorkspacePath must reject symlink escape via safe dir")
	}
}

// TestResolveWorkspacePathAcceptsSymlinkWithinSafeDir checks the
// complementary case: a symlink that stays inside the safe dir remains
// usable.
func TestResolveWorkspacePathAcceptsSymlinkWithinSafeDir(t *testing.T) {
	root := t.TempDir()
	safe := t.TempDir()

	subA := filepath.Join(safe, "a")
	subB := filepath.Join(safe, "b")
	if err := os.MkdirAll(subB, 0o755); err != nil {
		t.Fatalf("mkdir b: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subB, "data.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.Symlink(subB, subA); err != nil {
		t.Skipf("symlink creation not supported: %v", err)
	}

	target := filepath.Join(safe, "a", "data.txt")
	resolved, err := resolveWorkspacePath(context.Background(), root, target, []string{safe})
	if err != nil {
		t.Fatalf("symlink within safe dir must be accepted: %v", err)
	}
	// The resolved path must point at the real file location.
	wantSuffix := filepath.Join("b", "data.txt")
	if !strings.HasSuffix(resolved, wantSuffix) {
		t.Fatalf("resolved=%q does not end with %q", resolved, wantSuffix)
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

// TestResolveWorkspacePathAuthorizesApprovedExternal verifies the new
// behavior: a path outside the workspace is rejected unless the caller
// carries an approval-authorized directory (via ctx) that contains it.
func TestResolveWorkspacePathAuthorizesApprovedExternal(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "data.txt")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Unapproved external access is rejected.
	if _, err := resolveWorkspacePath(context.Background(), root, target, nil); err == nil {
		t.Fatal("expected unapproved external path to be rejected")
	}

	// Approved external access is allowed.
	ctx := WithAuthorizedDirs(context.Background(), []string{outside})
	got, err := resolveWorkspacePath(ctx, root, target, nil)
	if err != nil {
		t.Fatalf("approved external path must resolve: %v", err)
	}
	if got != target {
		t.Fatalf("resolved=%q want %q", got, target)
	}
}

// TestResolveWorkspacePathRejectsSymlinkEscapeFromAuthorized ensures a
// symlink inside an authorized directory that points outside is still
// rejected, mirroring the safe-dir protection.
func TestResolveWorkspacePathRejectsSymlinkEscapeFromAuthorized(t *testing.T) {
	root := t.TempDir()
	authorized := t.TempDir()
	secret := t.TempDir()
	if err := os.WriteFile(filepath.Join(secret, "pwn"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed secret: %v", err)
	}
	linkPath := filepath.Join(authorized, "escape")
	if err := os.Symlink(secret, linkPath); err != nil {
		t.Skipf("symlink creation not supported (requires admin on Windows): %v", err)
	}

	ctx := WithAuthorizedDirs(context.Background(), []string{authorized})
	if _, err := resolveWorkspacePath(ctx, root, filepath.Join(authorized, "escape", "pwn"), nil); err == nil {
		t.Fatal("resolveWorkspacePath must reject symlink escaping the authorized dir")
	}
}

func TestFilesystemIntentExtractor(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	if err := RegisterFilesystem(registry, FilesystemConfig{Root: root}); err != nil {
		t.Fatalf("register: %v", err)
	}

	ext, ok := registry.IntentExtractor("fs_read_file")
	if !ok {
		t.Fatal("fs_read_file must have an intent extractor")
	}
	intent := ext([]byte(`{"path":"sub/a.txt"}`))
	if intent.Class != approval.AccessRead {
		t.Fatalf("fs_read_file class=%q want read", intent.Class)
	}
	wantDir := filepath.Join(root, "sub")
	if len(intent.Dirs) != 1 || intent.Dirs[0] != wantDir {
		t.Fatalf("fs_read_file dirs=%v want [%s]", intent.Dirs, wantDir)
	}

	extW, ok := registry.IntentExtractor("fs_write_file")
	if !ok {
		t.Fatal("fs_write_file must have an intent extractor")
	}
	intentW := extW([]byte(`{"path":"out.txt","content":"x"}`))
	if intentW.Class != approval.AccessWrite {
		t.Fatalf("fs_write_file class=%q want write", intentW.Class)
	}
	if len(intentW.Dirs) != 1 || intentW.Dirs[0] != root {
		t.Fatalf("fs_write_file dirs=%v want [%s]", intentW.Dirs, root)
	}

	extP, ok := registry.IntentExtractor("fs_apply_patch")
	if !ok {
		t.Fatal("fs_apply_patch must have an intent extractor")
	}
	intentP := extP([]byte(`{"patch":"--- a/x.txt\n+++ b/x.txt\n@@ -0,0 +1 @@\n+hi\n"}`))
	if intentP.Class != approval.AccessWrite {
		t.Fatalf("fs_apply_patch class=%q want write", intentP.Class)
	}
	if len(intentP.Dirs) != 1 || intentP.Dirs[0] != root {
		t.Fatalf("fs_apply_patch dirs=%v want [%s]", intentP.Dirs, root)
	}

	if _, ok := registry.IntentExtractor("fs_read_file_nonexistent"); ok {
		t.Fatal("unknown tool must not have an extractor")
	}
}
