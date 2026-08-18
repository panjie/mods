package tools

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	if os.Getenv("MODS_SHELL_PROGRESS_HELPER") == "1" {
		fmt.Fprintln(os.Stdout, "first line")
		time.Sleep(30 * time.Millisecond)
		fmt.Fprintln(os.Stderr, "second line")
		time.Sleep(80 * time.Millisecond)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestProcessHelperProcess(t *testing.T) {
	separator := slices.Index(os.Args, "--")
	if separator < 0 || separator+1 >= len(os.Args) {
		return
	}
	action := os.Args[separator+1]
	values := os.Args[separator+2:]
	switch action {
	case "argv":
		_ = json.NewEncoder(os.Stdout).Encode(values)
		os.Exit(0)
	case "result":
		fmt.Fprint(os.Stdout, "standard output")
		fmt.Fprint(os.Stderr, "standard error")
		os.Exit(7)
	case "cwd":
		cwd, err := os.Getwd()
		if err != nil {
			os.Exit(2)
		}
		fmt.Fprint(os.Stdout, cwd)
		os.Exit(0)
	case "timeout":
		time.Sleep(5 * time.Second)
	case "delay":
		d, err := time.ParseDuration(values[0])
		if err != nil {
			os.Exit(2)
		}
		time.Sleep(d)
	case "tree":
		if len(values) != 1 {
			os.Exit(2)
		}
		child := exec.Command(os.Args[0], "-test.run=^TestProcessHelperProcess$", "--", "grandchild")
		if err := child.Start(); err != nil {
			os.Exit(3)
		}
		if err := os.WriteFile(values[0], []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
			os.Exit(4)
		}
		time.Sleep(5 * time.Second)
	case "grandchild":
		time.Sleep(5 * time.Second)
	case "large":
		fmt.Fprint(os.Stdout, strings.Repeat("A", 20<<10))
		fmt.Fprint(os.Stdout, strings.Repeat("B", 20<<10))
		fmt.Fprint(os.Stdout, strings.Repeat("C", 20<<10))
		os.Exit(0)
	}
}

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

func TestPowerShellGuidanceDescriptions(t *testing.T) {
	for _, desc := range []string{
		WindowsShellRunDescription,
		PowerShellRunDescription,
	} {
		if !strings.Contains(desc, "Get-ChildItem") {
			t.Fatalf("expected description to mention Get-ChildItem: %q", desc)
		}
		if !strings.Contains(desc, "Select-String") {
			t.Fatalf("expected description to mention Select-String: %q", desc)
		}
		if !strings.Contains(desc, "native PowerShell cmdlets") {
			t.Fatalf("expected description to prefer native PowerShell cmdlets: %q", desc)
		}
		if !strings.Contains(desc, "short, single-purpose commands") {
			t.Fatalf("expected description to prefer statically analyzable commands: %q", desc)
		}
		if !strings.Contains(desc, "do not use Out-File") {
			t.Fatalf("expected description to discourage Out-File: %q", desc)
		}
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

func TestValidateRequiredArgs(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	if err := RegisterFilesystem(registry, FilesystemConfig{Root: root}); err != nil {
		t.Fatalf("register: %v", err)
	}

	tests := []struct {
		name    string
		tool    string
		data    string
		wantErr string // substring expected; empty means expect nil
	}{
		{"write missing path", "fs_write_file", `{"content":"x"}`, "path is required"},
		{"write empty path", "fs_write_file", `{"path":"","content":"x"}`, "path is required"},
		{"write missing content", "fs_write_file", `{"path":"a.txt"}`, "content is required"},
		{"replace missing new text", "fs_replace", `{"path":"a.txt","old_text":"x"}`, "new_text is required"},
		{"replace empty new text allowed", "fs_replace", `{"path":"a.txt","old_text":"x","new_text":""}`, ""},
		{"read missing path", "fs_read_file", `{"offset":0}`, "path is required"},
		{"copy missing dest", "fs_copy", `{"source_path":"a"}`, "dest_path is required"},
		{"valid write", "fs_write_file", `{"path":"a.txt","content":"x"}`, ""},
		{"unknown tool passes", "fs_unknown", `{}`, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := registry.ValidateRequiredArgs(tc.tool, []byte(tc.data))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected nil error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestShellRunnerProgress(t *testing.T) {
	runner := ShellRunner{
		Root:             t.TempDir(),
		Tool:             "shell_run",
		Timeout:          2 * time.Second,
		ProgressInterval: 10 * time.Millisecond,
		BuildCommand: func(ctx context.Context, _ string) *exec.Cmd {
			cmd := exec.CommandContext(ctx, os.Args[0])
			cmd.Env = append(os.Environ(), "MODS_SHELL_PROGRESS_HELPER=1")
			return cmd
		},
	}
	var updates []ShellProgress
	runner.Progress = func(_ context.Context, progress ShellProgress) {
		updates = append(updates, progress)
	}

	out, err := runner.Run(context.Background(), "ignored command")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "first line") || !strings.Contains(out, "second line") {
		t.Fatalf("unexpected output: %q", out)
	}
	if len(updates) < 2 {
		t.Fatalf("expected multiple progress updates, got %d", len(updates))
	}
	if updates[0].Tool != "shell_run" || updates[0].Command != "ignored command" {
		t.Fatalf("unexpected first update: %#v", updates[0])
	}
	var sawLastOutput bool
	for _, update := range updates {
		if update.Elapsed < 0 {
			t.Fatalf("negative elapsed: %v", update.Elapsed)
		}
		if strings.Contains(update.LastOutput, "second line") {
			sawLastOutput = true
		}
	}
	if !sawLastOutput {
		t.Fatalf("expected progress to include recent output, got %#v", updates)
	}
}

func TestShellOutputPreservesContentAndTracksTail(t *testing.T) {
	out := newShellOutput()
	_, _ = out.Write([]byte("old line\n"))
	_, _ = out.Write([]byte(strings.Repeat("x", 100032)))
	_, _ = out.Write([]byte("\nnew line\n"))
	if got := out.LastLine(); got != "new line" {
		t.Fatalf("LastLine() = %q, want new line", got)
	}
	if text := out.String(); !strings.HasPrefix(text, "old line\n") || !strings.HasSuffix(text, "\nnew line\n") || !strings.Contains(text, "...[truncated ") {
		t.Fatalf("expected bounded head and tail output, got %q", text)
	}
}

func TestProcessRunLiteralArgvAndStructuredResults(t *testing.T) {
	root := t.TempDir()
	executable, err := filepath.Abs(os.Args[0])
	require.NoError(t, err)
	ctx := WithAuthorizedDirs(context.Background(), []string{filepath.Dir(executable)})
	cfg := ProcessConfig{Root: root, Timeout: 2 * time.Second}
	helperArgs := func(action string, values ...string) processRunArgs {
		args := []string{"-test.run=^TestProcessHelperProcess$", "--", action}
		args = append(args, values...)
		return processRunArgs{Program: executable, Args: args}
	}

	t.Run("literal argv", func(t *testing.T) {
		want := []string{"space value", "Unicode-路径", `$HOME`, `semi;colon`, `*.go`, `quote\"value`}
		out, err := runProcess(ctx, cfg, root, helperArgs("argv", want...))
		require.NoError(t, err)
		var result processRunResult
		require.NoError(t, json.Unmarshal([]byte(out), &result))
		require.NotNil(t, result.ExitCode)
		require.Zero(t, *result.ExitCode)
		var got []string
		require.NoError(t, json.Unmarshal([]byte(result.Stdout), &got))
		require.Equal(t, want, got)
	})

	t.Run("nonzero exit is a result", func(t *testing.T) {
		out, err := runProcess(ctx, cfg, root, helperArgs("result"))
		require.NoError(t, err)
		var result processRunResult
		require.NoError(t, json.Unmarshal([]byte(out), &result))
		require.Equal(t, 7, *result.ExitCode)
		require.Equal(t, "standard output", result.Stdout)
		require.Equal(t, "standard error", result.Stderr)
		require.False(t, result.TimedOut)
	})

	t.Run("cwd", func(t *testing.T) {
		subdir := filepath.Join(root, "sub dir")
		require.NoError(t, os.Mkdir(subdir, 0o700))
		args := helperArgs("cwd")
		args.Cwd = "sub dir"
		out, err := runProcess(ctx, cfg, root, args)
		require.NoError(t, err)
		var result processRunResult
		require.NoError(t, json.Unmarshal([]byte(out), &result))
		expected, err := filepath.EvalSymlinks(subdir)
		require.NoError(t, err)
		require.Equal(t, expected, result.Stdout)
	})

	t.Run("timeout is a result", func(t *testing.T) {
		ms := int64(25)
		args := helperArgs("timeout")
		args.TimeoutMS = &ms
		out, err := runProcess(ctx, cfg, root, args)
		require.NoError(t, err)
		var result processRunResult
		require.NoError(t, json.Unmarshal([]byte(out), &result))
		require.True(t, result.TimedOut)
		require.Nil(t, result.ExitCode)
	})

	t.Run("timeout terminates descendants", func(t *testing.T) {
		pidFile := filepath.Join(root, "grandchild.pid")
		ms := int64(250)
		args := helperArgs("tree", pidFile)
		args.TimeoutMS = &ms
		out, err := runProcess(ctx, cfg, root, args)
		require.NoError(t, err)
		var result processRunResult
		require.NoError(t, json.Unmarshal([]byte(out), &result))
		require.True(t, result.TimedOut)
		pidData, err := os.ReadFile(pidFile)
		require.NoError(t, err)
		pid, err := strconv.Atoi(string(pidData))
		require.NoError(t, err)
		require.Eventually(t, func() bool { return !testProcessExists(pid) }, 2*time.Second, 20*time.Millisecond)
	})

	t.Run("bounded output", func(t *testing.T) {
		out, err := runProcess(ctx, cfg, root, helperArgs("large"))
		require.NoError(t, err)
		var result processRunResult
		require.NoError(t, json.Unmarshal([]byte(out), &result))
		require.True(t, result.StdoutTruncated)
		require.Positive(t, result.StdoutOmitted)
		require.Contains(t, result.Stdout, "...[truncated ")
		require.True(t, strings.HasPrefix(result.Stdout, strings.Repeat("A", 1024)))
		require.True(t, strings.HasSuffix(result.Stdout, strings.Repeat("C", 1024)))
	})
}

func TestProcessRunTimeoutMSMayExceedDefault(t *testing.T) {
	root := t.TempDir()
	executable, err := filepath.Abs(os.Args[0])
	require.NoError(t, err)
	ctx := WithAuthorizedDirs(context.Background(), []string{filepath.Dir(executable)})
	cfg := ProcessConfig{Root: root, Timeout: 100 * time.Millisecond}
	delay := int64(2000)
	args := processRunArgs{
		Program:   executable,
		Args:      []string{"-test.run=^TestProcessHelperProcess$", "--", "delay", "500ms"},
		TimeoutMS: &delay,
	}
	out, err := runProcess(ctx, cfg, root, args)
	require.NoError(t, err)
	var result processRunResult
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	require.False(t, result.TimedOut)
	require.NotNil(t, result.ExitCode)
	require.Zero(t, *result.ExitCode)
}

func TestShellTimeoutMSOverride(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	require.NoError(t, RegisterShell(registry, ShellConfig{Root: root, Timeout: 2 * time.Second}))

	t.Run("shorter than default times out", func(t *testing.T) {
		_, err := registry.Call(context.Background(), "shell_run", []byte(`{"command":"sleep 1","timeout_ms":50}`))
		require.ErrorContains(t, err, "timed out after")
	})

	t.Run("longer than default is honored", func(t *testing.T) {
		registry := NewRegistry()
		require.NoError(t, RegisterShell(registry, ShellConfig{Root: root, Timeout: 100 * time.Millisecond}))
		_, err := registry.Call(context.Background(), "shell_run", []byte(`{"command":"sleep 0.5","timeout_ms":2000}`))
		require.NoError(t, err)
	})

	t.Run("non-positive rejected", func(t *testing.T) {
		_, err := registry.Call(context.Background(), "shell_run", []byte(`{"command":"echo hi","timeout_ms":0}`))
		require.ErrorContains(t, err, "timeout_ms must be positive")
	})
}

func TestProcessRunValidationAndRuntimeInfo(t *testing.T) {
	root := t.TempDir()
	cfg := ProcessConfig{Root: root, Timeout: 50 * time.Millisecond}
	executable, err := filepath.Abs(os.Args[0])
	require.NoError(t, err)
	ctx := WithAuthorizedDirs(context.Background(), []string{filepath.Dir(executable)})
	over := int64(51)
	_, err = runProcess(ctx, cfg, root, processRunArgs{
		Program:   executable,
		Args:      []string{"-test.run=^TestProcessHelperProcess$", "--", "result"},
		TimeoutMS: &over,
	})
	require.NoError(t, err)

	zero := int64(0)
	_, err = runProcess(ctx, cfg, root, processRunArgs{Program: executable, TimeoutMS: &zero})
	require.ErrorContains(t, err, "timeout_ms must be positive")

	registry := NewRegistry()
	require.NoError(t, RegisterRuntimeInfo(registry, root))
	out, err := registry.Call(context.Background(), "runtime_info", []byte(`{"commands":["go","go","definitely-not-a-mods-command"]}`))
	require.NoError(t, err)
	var info runtimeInfoResult
	require.NoError(t, json.Unmarshal([]byte(out), &info))
	require.Equal(t, runtime.GOOS, info.OS)
	require.Equal(t, root, info.Workspace)
	require.NotEmpty(t, info.Shell.Executable)
	require.True(t, info.Commands["go"].Found)
	require.False(t, info.Commands["definitely-not-a-mods-command"].Found)
	_, err = registry.Call(context.Background(), "runtime_info", []byte(`{"commands":["../go"]}`))
	require.ErrorContains(t, err, "invalid command name")
}

func TestPrepareProcessProgramBindsPATHResolution(t *testing.T) {
	executable, err := filepath.Abs(os.Args[0])
	require.NoError(t, err)
	t.Setenv("PATH", filepath.Dir(executable))

	data, err := json.Marshal(map[string]any{"program": filepath.Base(executable)})
	require.NoError(t, err)
	binding, err := PrepareProcessProgram(data)
	require.NoError(t, err)
	require.Equal(t, filepath.Base(executable), binding.Requested)
	require.Equal(t, executable, binding.Resolved)

	ctx := WithProcessProgramBinding(context.Background(), binding)
	resolved, err := resolveProcessProgram(ctx, t.TempDir(), t.TempDir(), binding.Requested, nil)
	require.NoError(t, err)
	require.Equal(t, executable, resolved)
}

func TestUnsupportedProcessProgramForGOOS(t *testing.T) {
	require.True(t, unsupportedProcessProgramForGOOS(`C:\\tools\\npm.cmd`, "windows"))
	require.True(t, unsupportedProcessProgramForGOOS(`C:\\tools\\build.BAT`, "windows"))
	require.False(t, unsupportedProcessProgramForGOOS(`C:\\tools\\native.exe`, "windows"))
	require.False(t, unsupportedProcessProgramForGOOS(`/tools/npm.cmd`, "linux"))
}

func TestPrepareProcessProgramRejectsWindowsBatchFiles(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows batch launchers are only special on Windows")
	}
	_, err := PrepareProcessProgram([]byte(`{"program":"C:\\\\tools\\\\npm.cmd"}`))
	require.ErrorContains(t, err, "cannot preserve literal argv")
}

func TestSearchSkipsSymlinks(t *testing.T) {
	// Regression: fs_search must not follow in-workspace symlinks. Before the
	// fix, os.Open followed the link and read its (possibly out-of-workspace)
	// target, bypassing the boundary check applied only to the search root.
	if runtime.GOOS == "windows" {
		t.Skip("symlinks not reliably supported on this Windows build")
	}
	secretDir := t.TempDir()
	root := t.TempDir()
	secret := filepath.Join(secretDir, "secret.txt")
	if err := os.WriteFile(secret, []byte("TOPSECRET-line\n"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	link := filepath.Join(root, "leak.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	registry := NewRegistry()
	if err := RegisterFilesystem(registry, FilesystemConfig{Root: root}); err != nil {
		t.Fatalf("register: %v", err)
	}

	got, err := registry.Call(context.Background(), "fs_search", []byte(`{"path":".","query":"TOPSECRET"}`))
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if strings.Contains(got, "TOPSECRET") {
		t.Fatalf("fs_search followed symlink and leaked out-of-workspace content: %q", got)
	}
}

func TestFilesystemReadByLines(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	if err := RegisterFilesystem(registry, FilesystemConfig{Root: root}); err != nil {
		t.Fatalf("register: %v", err)
	}
	// 5-line file with lines "L1".."L5".
	if _, err := registry.Call(context.Background(), "fs_write_file", []byte(`{"path":"f.txt","content":"L1\nL2\nL3\nL4\nL5\n"}`)); err != nil {
		t.Fatalf("write: %v", err)
	}

	tests := []struct {
		name    string
		data    string
		want    string
		wantErr string
	}{
		{"full from line 1", `{"path":"f.txt","start_line":1}`, "1: L1\n2: L2\n3: L3\n4: L4\n5: L5", ""},
		{"window 2-3", `{"path":"f.txt","start_line":2,"end_line":3}`, "2: L2\n3: L3", ""},
		{"single line", `{"path":"f.txt","start_line":4,"end_line":4}`, "4: L4", ""},
		{"end clamps at EOF", `{"path":"f.txt","start_line":4,"end_line":99}`, "4: L4\n5: L5", ""},
		{"start beyond EOF", `{"path":"f.txt","start_line":9}`, "", "beyond file length"},
		{"end < start", `{"path":"f.txt","start_line":3,"end_line":1}`, "", "end_line must be >= start_line"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := registry.Call(context.Background(), "fs_read_file", []byte(tc.data))
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil (output=%q)", tc.wantErr, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFilesystemReadByLinesTruncation(t *testing.T) {
	// When the file has more lines than the default window and the caller did
	// not set end_line, the output must report truncation and point at the
	// next line so the caller can page. (Setting end_line suppresses this.)
	root := t.TempDir()
	registry := NewRegistry()
	if err := RegisterFilesystem(registry, FilesystemConfig{Root: root}); err != nil {
		t.Fatalf("register: %v", err)
	}
	var content strings.Builder
	for i := 1; i <= defaultReadLines+10; i++ {
		content.WriteString("line ")
		content.WriteString(strconv.Itoa(i))
		content.WriteString("\n")
	}
	if err := os.WriteFile(filepath.Join(root, "big.txt"), []byte(content.String()), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Default window (no end_line) -> truncates at defaultReadLines.
	got, err := registry.Call(context.Background(), "fs_read_file", []byte(`{"path":"big.txt","start_line":1}`))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(got, "1: line 1") {
		t.Fatalf("missing first line in output")
	}
	if !strings.Contains(got, strconv.Itoa(defaultReadLines)+": line "+strconv.Itoa(defaultReadLines)) {
		t.Fatalf("missing line %d (window edge)", defaultReadLines)
	}
	if !strings.Contains(got, "[Output truncated") || !strings.Contains(got, "continue from line "+strconv.Itoa(defaultReadLines+1)) {
		t.Fatalf("expected truncation message pointing at line %d; tail=%q", defaultReadLines+1, tailForTest(got))
	}

	// Explicit end_line at EOF -> no truncation message.
	got, err = registry.Call(context.Background(), "fs_read_file", []byte(`{"path":"big.txt","start_line":1,"end_line":5}`))
	if err != nil {
		t.Fatalf("read window: %v", err)
	}
	if strings.Contains(got, "[Output truncated") {
		t.Fatalf("explicit end_line must not report truncation; got %q", tailForTest(got))
	}
}

// tailForTest returns the last ~120 bytes of s for compact assertion failures.
func tailForTest(s string) string {
	if len(s) <= 120 {
		return s
	}
	return s[len(s)-120:]
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

func TestFilesystemLargestFileKindIgnoresDirectoryEntries(t *testing.T) {
	root := t.TempDir()
	downloads := filepath.Join(root, "Downloads")
	courseDir := filepath.Join(downloads, "course")
	if err := os.MkdirAll(courseDir, 0o755); err != nil {
		t.Fatalf("mkdir course: %v", err)
	}
	if err := os.WriteFile(filepath.Join(courseDir, "material.mov"), []byte(strings.Repeat("x", 4096)), 0o644); err != nil {
		t.Fatalf("seed material: %v", err)
	}
	if err := os.WriteFile(filepath.Join(downloads, "loose.zip"), []byte(strings.Repeat("x", 1024)), 0o644); err != nil {
		t.Fatalf("seed loose file: %v", err)
	}

	registry := NewRegistry()
	if err := RegisterFilesystem(registry, FilesystemConfig{Root: root}); err != nil {
		t.Fatalf("register filesystem: %v", err)
	}

	files, err := registry.Call(context.Background(), "fs_largest", []byte(`{"path":"Downloads","kind":"file","max_results":1}`))
	if err != nil {
		t.Fatalf("largest files: %v", err)
	}
	if !strings.Contains(files, "file\tDownloads/course/material.mov") {
		t.Fatalf("expected largest file entry, got: %q", files)
	}
	if strings.Contains(files, "\tdir\tDownloads/course") {
		t.Fatalf("file search must not return directory entries: %q", files)
	}

	dirs, err := registry.Call(context.Background(), "fs_largest", []byte(`{"path":"Downloads","kind":"dir","max_results":1}`))
	if err != nil {
		t.Fatalf("largest dirs: %v", err)
	}
	if !strings.Contains(dirs, "dir\tDownloads/course") {
		t.Fatalf("expected largest directory entry, got: %q", dirs)
	}
}

func TestFilesystemDeleteFileAndDirBoundaries(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	if err := RegisterFilesystem(registry, FilesystemConfig{Root: root}); err != nil {
		t.Fatalf("register filesystem: %v", err)
	}

	dir := filepath.Join(root, "target-dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir target dir: %v", err)
	}
	if _, err := registry.Call(context.Background(), "fs_delete_file", []byte(`{"path":"target-dir"}`)); err == nil {
		t.Fatal("fs_delete_file must refuse directories")
	}

	file := filepath.Join(root, "target.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if _, err := registry.Call(context.Background(), "fs_delete_file", []byte(`{"path":"target.txt"}`)); err != nil {
		t.Fatalf("delete file: %v", err)
	}
	if _, err := os.Stat(file); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("file should be deleted, stat err=%v", err)
	}

	nested := filepath.Join(root, "non-empty", "child.txt")
	if err := os.MkdirAll(filepath.Dir(nested), 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	if err := os.WriteFile(nested, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed nested: %v", err)
	}
	if _, err := registry.Call(context.Background(), "fs_delete_dir", []byte(`{"path":"non-empty"}`)); err == nil {
		t.Fatal("non-recursive fs_delete_dir must refuse non-empty directories")
	}
	if _, err := os.Stat(nested); err != nil {
		t.Fatalf("non-recursive delete should leave contents in place: %v", err)
	}
	if _, err := registry.Call(context.Background(), "fs_delete_dir", []byte(`{"path":"non-empty","recursive":true}`)); err != nil {
		t.Fatalf("recursive delete dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "non-empty")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("directory should be deleted, stat err=%v", err)
	}
}

func TestFilesystemDeleteFileDeletesSymlinkItself(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "target.txt")
	if err := os.WriteFile(target, []byte("keep"), 0o644); err != nil {
		t.Fatalf("seed outside target: %v", err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink creation not supported: %v", err)
	}

	registry := NewRegistry()
	if err := RegisterFilesystem(registry, FilesystemConfig{Root: root}); err != nil {
		t.Fatalf("register filesystem: %v", err)
	}
	if _, err := registry.Call(context.Background(), "fs_delete_file", []byte(`{"path":"link.txt"}`)); err != nil {
		t.Fatalf("delete symlink: %v", err)
	}
	if _, err := os.Lstat(link); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("symlink should be deleted, lstat err=%v", err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "keep" {
		t.Fatalf("symlink target should remain, content=%q err=%v", got, err)
	}
}

func TestFilesystemCopyMoveMkdir(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	if err := RegisterFilesystem(registry, FilesystemConfig{Root: root}); err != nil {
		t.Fatalf("register filesystem: %v", err)
	}

	if _, err := registry.Call(context.Background(), "fs_mkdir", []byte(`{"path":"nested/out"}`)); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "source.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	if _, err := registry.Call(context.Background(), "fs_copy", []byte(`{"source_path":"source.txt","dest_path":"nested/out"}`)); err != nil {
		t.Fatalf("copy file into dir: %v", err)
	}
	copied := filepath.Join(root, "nested", "out", "source.txt")
	if got, err := os.ReadFile(copied); err != nil || string(got) != "hello" {
		t.Fatalf("copied file content=%q err=%v", got, err)
	}
	if _, err := registry.Call(context.Background(), "fs_move", []byte(`{"source_path":"nested/out/source.txt","dest_path":"renamed.txt"}`)); err != nil {
		t.Fatalf("move file: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(root, "renamed.txt")); err != nil || string(got) != "hello" {
		t.Fatalf("moved file content=%q err=%v", got, err)
	}
	if _, err := os.Stat(copied); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("source after move should be gone, stat err=%v", err)
	}

	if err := os.MkdirAll(filepath.Join(root, "tree", "sub"), 0o755); err != nil {
		t.Fatalf("mkdir tree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "tree", "sub", "data.txt"), []byte("tree"), 0o644); err != nil {
		t.Fatalf("seed tree: %v", err)
	}
	if _, err := registry.Call(context.Background(), "fs_copy", []byte(`{"source_path":"tree","dest_path":"tree-copy"}`)); err == nil {
		t.Fatal("directory copy must require recursive=true")
	}
	if _, err := registry.Call(context.Background(), "fs_copy", []byte(`{"source_path":"tree","dest_path":"tree/backup","recursive":true}`)); err == nil || !strings.Contains(err.Error(), "inside itself") {
		t.Fatalf("directory copy into itself should fail, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "tree", "backup")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("failed self-copy must not leave a partial destination, stat err=%v", err)
	}
	if _, err := registry.Call(context.Background(), "fs_copy", []byte(`{"source_path":"tree","dest_path":"tree-copy","recursive":true}`)); err != nil {
		t.Fatalf("copy dir: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(root, "tree-copy", "sub", "data.txt")); err != nil || string(got) != "tree" {
		t.Fatalf("copied dir content=%q err=%v", got, err)
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

func TestFilesystemApplyPatchAcceptsCodexFormat(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	require.NoError(t, RegisterFilesystem(registry, FilesystemConfig{Root: root}))
	require.NoError(t, os.WriteFile(filepath.Join(root, "update.txt"), []byte("alpha\nbeta\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "delete.txt"), []byte("remove\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "move.txt"), []byte("before\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "pure-move.txt"), []byte("same\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "no-newline.txt"), []byte("old"), 0o644))

	patch := `*** Begin Patch
*** Add File: nested/added.txt
+added
*** Update File: update.txt
@@
 alpha
-beta
+changed
*** Delete File: delete.txt
*** Update File: move.txt
*** Move to: nested/moved.txt
@@
-before
+after
*** Update File: pure-move.txt
*** Move to: nested/pure-moved.txt
*** Update File: no-newline.txt
@@
-old
+new
*** End Patch
`
	_, err := registry.Call(context.Background(), "fs_apply_patch", []byte(`{"patch":`+strconv.Quote(patch)+`}`))
	require.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(root, "nested", "added.txt"))
	require.NoError(t, err)
	require.Equal(t, "added\n", string(content))
	content, err = os.ReadFile(filepath.Join(root, "update.txt"))
	require.NoError(t, err)
	require.Equal(t, "alpha\nchanged\n", string(content))
	require.NoFileExists(t, filepath.Join(root, "delete.txt"))
	require.NoFileExists(t, filepath.Join(root, "move.txt"))
	content, err = os.ReadFile(filepath.Join(root, "nested", "moved.txt"))
	require.NoError(t, err)
	require.Equal(t, "after\n", string(content))
	require.NoFileExists(t, filepath.Join(root, "pure-move.txt"))
	content, err = os.ReadFile(filepath.Join(root, "nested", "pure-moved.txt"))
	require.NoError(t, err)
	require.Equal(t, "same\n", string(content))
	content, err = os.ReadFile(filepath.Join(root, "no-newline.txt"))
	require.NoError(t, err)
	require.Equal(t, "new", string(content))
}

func TestFilesystemApplyPatchCodexFormatPreservesCRLF(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	require.NoError(t, RegisterFilesystem(registry, FilesystemConfig{Root: root}))
	require.NoError(t, os.WriteFile(filepath.Join(root, "crlf.txt"), []byte("alpha\r\nbeta\r\n"), 0o644))

	patch := "*** Begin Patch\n*** Update File: crlf.txt\n@@\n alpha\n-beta\n+changed\n*** End Patch\n"
	_, err := registry.Call(context.Background(), "fs_apply_patch", []byte(`{"patch":`+strconv.Quote(patch)+`}`))
	require.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(root, "crlf.txt"))
	require.NoError(t, err)
	require.Equal(t, "alpha\r\nchanged\r\n", string(content))
}

func TestFilesystemApplyPatchCodexFormatIsAtomic(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	require.NoError(t, RegisterFilesystem(registry, FilesystemConfig{Root: root}))
	path := filepath.Join(root, "keep.txt")
	require.NoError(t, os.WriteFile(path, []byte("original\n"), 0o644))

	patch := `*** Begin Patch
*** Update File: keep.txt
@@
-original
+changed
*** Update File: missing.txt
@@
-missing
+changed
*** End Patch
`
	_, err := registry.Call(context.Background(), "fs_apply_patch", []byte(`{"patch":`+strconv.Quote(patch)+`}`))
	require.Error(t, err)
	content, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	require.Equal(t, "original\n", string(content))
}

func TestFilesystemApplyPatchCodexFormatRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	require.NoError(t, RegisterFilesystem(registry, FilesystemConfig{Root: root}))

	patch := "*** Begin Patch\n*** Add File: ../escape.txt\n+pwned\n*** End Patch\n"
	_, err := registry.Call(context.Background(), "fs_apply_patch", []byte(`{"patch":`+strconv.Quote(patch)+`}`))
	require.ErrorContains(t, err, "outside workspace")
	require.NoFileExists(t, filepath.Join(root, "..", "escape.txt"))
}

func TestFilesystemReplace(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	if err := RegisterFilesystem(registry, FilesystemConfig{Root: root}); err != nil {
		t.Fatalf("register filesystem: %v", err)
	}
	path := filepath.Join(root, "a.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o640); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	beforeInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat file before replace: %v", err)
	}

	args := map[string]string{"path": "a.txt", "old_text": "beta\n", "new_text": "delta\n"}
	data, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	if _, err := registry.Call(context.Background(), "fs_replace", data); err != nil {
		t.Fatalf("replace text: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(content) != "alpha\ndelta\ngamma\n" {
		t.Fatalf("unexpected content: %q", content)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if got, want := info.Mode().Perm(), beforeInfo.Mode().Perm(); got != want {
		t.Fatalf("mode=%v want unchanged %v", got, want)
	}
}

func TestFilesystemReplaceAllowsEmptyNewText(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	if err := RegisterFilesystem(registry, FilesystemConfig{Root: root}); err != nil {
		t.Fatalf("register filesystem: %v", err)
	}
	path := filepath.Join(root, "a.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	_, err := registry.Call(context.Background(), "fs_replace", []byte(`{"path":"a.txt","old_text":"beta\n","new_text":""}`))
	if err != nil {
		t.Fatalf("delete text with empty replacement: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(content) != "alpha\ngamma\n" {
		t.Fatalf("unexpected content: %q", content)
	}
}

func TestFilesystemReplaceRequiresUniqueOldText(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	if err := RegisterFilesystem(registry, FilesystemConfig{Root: root}); err != nil {
		t.Fatalf("register filesystem: %v", err)
	}
	path := filepath.Join(root, "a.txt")
	if err := os.WriteFile(path, []byte("same\nsame\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	_, err := registry.Call(context.Background(), "fs_replace", []byte(`{"path":"a.txt","old_text":"same\n","new_text":"once\n"}`))
	if err == nil {
		t.Fatal("expected duplicate old_text to fail")
	}
	if !strings.Contains(err.Error(), "matched 2 times") {
		t.Fatalf("expected duplicate match error, got: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(content) != "same\nsame\n" {
		t.Fatalf("file changed after failed replace: %q", content)
	}

	_, err = registry.Call(context.Background(), "fs_replace", []byte(`{"path":"a.txt","old_text":"missing","new_text":"x"}`))
	if err == nil {
		t.Fatal("expected missing old_text to fail")
	}
	if !strings.Contains(err.Error(), "was not found") {
		t.Fatalf("expected missing text error, got: %v", err)
	}
}

func TestFilesystemApplyPatchRecountsHunkHeaders(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	if err := RegisterFilesystem(registry, FilesystemConfig{Root: root}); err != nil {
		t.Fatalf("register filesystem: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	patch := "--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-old\n+new\n+more\n"
	_, err := registry.Call(context.Background(), "fs_apply_patch", []byte(`{"patch":`+strconv.Quote(patch)+`}`))
	if err != nil {
		t.Fatalf("apply patch with stale hunk count: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatalf("read patched file: %v", err)
	}
	if string(content) != "new\nmore\n" {
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

	t.Run("captures complete large output", func(t *testing.T) {
		limited := NewRegistry()
		if err := RegisterPowerShell(limited, ShellConfig{Root: root}); err != nil {
			t.Fatalf("register powershell: %v", err)
		}
		out, err := limited.Call(context.Background(), "powershell_run", []byte(`{"command":"[Console]::Out.Write(('x' * 5000))"}`))
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		if out != strings.Repeat("x", 5000) {
			t.Fatalf("expected complete output, got %d bytes", len(out))
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

func TestDecodeOutputPrefersUTF8(t *testing.T) {
	want := `[{"creator":"潘捷","receiveTime":"7-14","title":"差旅费报销申请"}]`
	if got := decodeOutput([]byte(want)); got != want {
		t.Fatalf("decodeOutput() = %q, want %q", got, want)
	}
}

func TestDecodeOutputRecognizesUnicodeBOMs(t *testing.T) {
	want := "Windows 路径 🚀"
	encodeUTF16 := func(littleEndian bool) []byte {
		units := utf16.Encode([]rune(want))
		out := []byte{0xfe, 0xff}
		var order binary.ByteOrder = binary.BigEndian
		if littleEndian {
			out = []byte{0xff, 0xfe}
			order = binary.LittleEndian
		}
		for _, unit := range units {
			var pair [2]byte
			order.PutUint16(pair[:], unit)
			out = append(out, pair[:]...)
		}
		return out
	}

	for name, data := range map[string][]byte{
		"utf8-bom":     append([]byte{0xef, 0xbb, 0xbf}, []byte(want)...),
		"utf16-le-bom": encodeUTF16(true),
		"utf16-be-bom": encodeUTF16(false),
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, want, decodeOutput(data))
		})
	}
}

func TestShellCommandUsesPowerShellOnWindows(t *testing.T) {
	cmd := shellCommand(context.Background(), "Write-Output ok")
	if runtime.GOOS == "windows" {
		base := filepath.Base(cmd.Path)
		if base != "pwsh.exe" && base != "powershell.exe" {
			t.Fatalf("expected shell_run to use pwsh.exe or powershell.exe on Windows, got %q", cmd.Path)
		}
		wantArgs := []string{cmd.Path, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", "Write-Output ok"}
		if strings.Join(cmd.Args, "\x00") != strings.Join(wantArgs, "\x00") {
			t.Fatalf("unexpected PowerShell args: %#v", cmd.Args)
		}
		return
	}
	if filepath.Base(cmd.Path) != "sh" {
		t.Fatalf("expected shell_run to use sh outside Windows, got %q", cmd.Path)
	}
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
	want, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("eval target: %v", err)
	}
	if got != want {
		t.Fatalf("resolved=%q want %q", got, want)
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
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("eval root: %v", err)
	}
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
	wantDir := filepath.Join(canonicalRoot, "sub")
	if len(intent.Dirs) != 1 || intent.Dirs[0] != wantDir {
		t.Fatalf("fs_read_file dirs=%v want [%s]", intent.Dirs, wantDir)
	}

	dirTarget := filepath.Join(canonicalRoot, "existing-dir")
	if err := os.MkdirAll(dirTarget, 0o755); err != nil {
		t.Fatalf("mkdir dir target: %v", err)
	}
	extList, ok := registry.IntentExtractor("fs_list_dir")
	if !ok {
		t.Fatal("fs_list_dir must have an intent extractor")
	}
	intentList := extList([]byte(`{"path":"existing-dir"}`))
	if intentList.Class != approval.AccessRead {
		t.Fatalf("fs_list_dir class=%q want read", intentList.Class)
	}
	if len(intentList.Dirs) != 1 || intentList.Dirs[0] != dirTarget {
		t.Fatalf("fs_list_dir dirs=%v want [%s]", intentList.Dirs, dirTarget)
	}

	extW, ok := registry.IntentExtractor("fs_write_file")
	if !ok {
		t.Fatal("fs_write_file must have an intent extractor")
	}
	intentW := extW([]byte(`{"path":"out.txt","content":"x"}`))
	if intentW.Class != approval.AccessWrite {
		t.Fatalf("fs_write_file class=%q want write", intentW.Class)
	}
	if len(intentW.Dirs) != 1 || intentW.Dirs[0] != canonicalRoot {
		t.Fatalf("fs_write_file dirs=%v want [%s]", intentW.Dirs, canonicalRoot)
	}
	intentLiteralGlob := extW([]byte(`{"path":"literal*/out.txt","content":"x"}`))
	wantLiteralGlobDir := filepath.Join(canonicalRoot, "literal*")
	if len(intentLiteralGlob.Dirs) != 1 || intentLiteralGlob.Dirs[0] != wantLiteralGlobDir {
		t.Fatalf("fs_write_file literal glob dirs=%v want [%s]", intentLiteralGlob.Dirs, wantLiteralGlobDir)
	}

	extReplace, ok := registry.IntentExtractor("fs_replace")
	if !ok {
		t.Fatal("fs_replace must have an intent extractor")
	}
	intentReplace := extReplace([]byte(`{"path":"out.txt","old_text":"a","new_text":"b"}`))
	if intentReplace.Class != approval.AccessWrite {
		t.Fatalf("fs_replace class=%q want write", intentReplace.Class)
	}
	if len(intentReplace.Dirs) != 1 || intentReplace.Dirs[0] != canonicalRoot {
		t.Fatalf("fs_replace dirs=%v want [%s]", intentReplace.Dirs, canonicalRoot)
	}

	extLargest, ok := registry.IntentExtractor("fs_largest")
	if !ok {
		t.Fatal("fs_largest must have an intent extractor")
	}
	intentLargest := extLargest([]byte(`{"path":"existing-dir","kind":"file"}`))
	if intentLargest.Class != approval.AccessRead {
		t.Fatalf("fs_largest class=%q want read", intentLargest.Class)
	}
	if len(intentLargest.Dirs) != 1 || intentLargest.Dirs[0] != dirTarget {
		t.Fatalf("fs_largest dirs=%v want [%s]", intentLargest.Dirs, dirTarget)
	}

	extDeleteDir, ok := registry.IntentExtractor("fs_delete_dir")
	if !ok {
		t.Fatal("fs_delete_dir must have an intent extractor")
	}
	intentDeleteDir := extDeleteDir([]byte(`{"path":"old-dir","recursive":true}`))
	wantDeleteDir := filepath.Join(canonicalRoot, "old-dir")
	if intentDeleteDir.Class != approval.AccessWrite {
		t.Fatalf("fs_delete_dir class=%q want write", intentDeleteDir.Class)
	}
	if len(intentDeleteDir.Dirs) != 1 || intentDeleteDir.Dirs[0] != wantDeleteDir {
		t.Fatalf("fs_delete_dir dirs=%v want [%s]", intentDeleteDir.Dirs, wantDeleteDir)
	}

	extCopy, ok := registry.IntentExtractor("fs_copy")
	if !ok {
		t.Fatal("fs_copy must have an intent extractor")
	}
	intentCopy := extCopy([]byte(`{"source_path":"out.txt","dest_path":"copies/out.txt"}`))
	wantCopyDest := filepath.Join(canonicalRoot, "copies")
	if len(intentCopy.ReadDirs) != 1 || intentCopy.ReadDirs[0] != canonicalRoot {
		t.Fatalf("fs_copy read dirs=%v want [%s]", intentCopy.ReadDirs, canonicalRoot)
	}
	if len(intentCopy.WriteDirs) != 1 || intentCopy.WriteDirs[0] != wantCopyDest {
		t.Fatalf("fs_copy write dirs=%v want [%s]", intentCopy.WriteDirs, wantCopyDest)
	}

	extP, ok := registry.IntentExtractor("fs_apply_patch")
	if !ok {
		t.Fatal("fs_apply_patch must have an intent extractor")
	}
	intentP := extP([]byte(`{"patch":"--- a/x.txt\n+++ b/x.txt\n@@ -0,0 +1 @@\n+hi\n"}`))
	if intentP.Class != approval.AccessWrite {
		t.Fatalf("fs_apply_patch class=%q want write", intentP.Class)
	}
	if len(intentP.Dirs) != 1 || intentP.Dirs[0] != canonicalRoot {
		t.Fatalf("fs_apply_patch dirs=%v want [%s]", intentP.Dirs, canonicalRoot)
	}

	if _, ok := registry.IntentExtractor("fs_read_file_nonexistent"); ok {
		t.Fatal("unknown tool must not have an extractor")
	}
}

func TestFilesystemHomePathExpansion(t *testing.T) {
	root := t.TempDir()
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("USERPROFILE", fakeHome)

	downloads := filepath.Join(fakeHome, "Downloads")
	if err := os.MkdirAll(downloads, 0o755); err != nil {
		t.Fatalf("mkdir downloads: %v", err)
	}
	downloadFile := filepath.Join(downloads, "Codex.dmg")
	if err := os.WriteFile(downloadFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed download: %v", err)
	}

	registry := NewRegistry()
	if err := RegisterFilesystem(registry, FilesystemConfig{Root: root}); err != nil {
		t.Fatalf("register: %v", err)
	}

	extStat, ok := registry.IntentExtractor("fs_stat")
	if !ok {
		t.Fatal("fs_stat must have an intent extractor")
	}
	intentStat := extStat([]byte(`{"path":"~/Downloads/Codex.dmg"}`))
	if intentStat.Class != approval.AccessRead {
		t.Fatalf("fs_stat class=%q want read", intentStat.Class)
	}
	if len(intentStat.Dirs) != 1 || intentStat.Dirs[0] != downloads {
		t.Fatalf("fs_stat dirs=%v want [%s]", intentStat.Dirs, downloads)
	}

	ctx := WithAuthorizedDirs(context.Background(), []string{downloads})
	resolved, err := resolveWorkspacePath(ctx, root, "~/Downloads/Codex.dmg", nil)
	if err != nil {
		t.Fatalf("approved home read must resolve: %v", err)
	}
	wantResolved, err := filepath.EvalSymlinks(downloadFile)
	if err != nil {
		t.Fatalf("eval download: %v", err)
	}
	if resolved != wantResolved {
		t.Fatalf("resolved=%q want %q", resolved, wantResolved)
	}

	extWrite, ok := registry.IntentExtractor("fs_write_file")
	if !ok {
		t.Fatal("fs_write_file must have an intent extractor")
	}
	outDir := filepath.Join(fakeHome, "mods-out")
	intentWrite := extWrite([]byte(`{"path":"~/mods-out/out.txt","content":"x"}`))
	if intentWrite.Class != approval.AccessWrite {
		t.Fatalf("fs_write_file class=%q want write", intentWrite.Class)
	}
	if len(intentWrite.Dirs) != 1 || intentWrite.Dirs[0] != outDir {
		t.Fatalf("fs_write_file dirs=%v want [%s]", intentWrite.Dirs, outDir)
	}
	ctx = WithAuthorizedDirs(context.Background(), []string{outDir})
	resolved, err = resolveWorkspacePath(ctx, root, "~/mods-out/out.txt", nil)
	if err != nil {
		t.Fatalf("approved home write must resolve: %v", err)
	}
	fakeHomeEval, err := filepath.EvalSymlinks(fakeHome)
	if err != nil {
		t.Fatalf("eval fake home: %v", err)
	}
	wantOut := filepath.Join(fakeHomeEval, "mods-out", "out.txt")
	if resolved != wantOut {
		t.Fatalf("resolved=%q want %q", resolved, wantOut)
	}
}

func TestFilesystemLiteralWorkspaceTilde(t *testing.T) {
	root := t.TempDir()
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("eval root: %v", err)
	}
	literalDir := filepath.Join(canonicalRoot, "~", "literal")
	if err := os.MkdirAll(literalDir, 0o755); err != nil {
		t.Fatalf("mkdir literal dir: %v", err)
	}
	literalFile := filepath.Join(literalDir, "file.txt")
	if err := os.WriteFile(literalFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed literal file: %v", err)
	}

	registry := NewRegistry()
	if err := RegisterFilesystem(registry, FilesystemConfig{Root: root}); err != nil {
		t.Fatalf("register: %v", err)
	}
	extStat, ok := registry.IntentExtractor("fs_stat")
	if !ok {
		t.Fatal("fs_stat must have an intent extractor")
	}
	intent := extStat([]byte(`{"path":"./~/literal/file.txt"}`))
	if intent.Class != approval.AccessRead {
		t.Fatalf("fs_stat class=%q want read", intent.Class)
	}
	if len(intent.Dirs) != 1 || intent.Dirs[0] != literalDir {
		t.Fatalf("fs_stat dirs=%v want [%s]", intent.Dirs, literalDir)
	}

	resolved, err := resolveWorkspacePath(context.Background(), root, "./~/literal/file.txt", nil)
	if err != nil {
		t.Fatalf("literal workspace tilde must resolve without external approval: %v", err)
	}
	wantResolved, err := filepath.EvalSymlinks(literalFile)
	if err != nil {
		t.Fatalf("eval literal file: %v", err)
	}
	if resolved != wantResolved {
		t.Fatalf("resolved=%q want %q", resolved, wantResolved)
	}
}

func TestResolveWorkspacePathRejectsUnsupportedOtherUserHome(t *testing.T) {
	root := t.TempDir()
	ctx := WithAuthorizedDirs(context.Background(), []string{"~root"})
	if _, err := resolveWorkspacePath(ctx, root, "~root/.ssh/config", nil); err == nil {
		t.Fatal("unsupported other-user home path must not resolve into the workspace")
	}
}

// TestFilesystemToolsAcceptWorkspaceSymlinkAlias pins the fix for workspaces
// reached through a symlink (e.g. ~/.emacs.d -> real dir): absolute paths
// spelled through the alias resolve inside the canonical workspace without
// approval, and writes land in the real directory.
func TestFilesystemToolsAcceptWorkspaceSymlinkAlias(t *testing.T) {
	realRoot := t.TempDir()
	aliasRoot := filepath.Join(t.TempDir(), "alias-root")
	if err := os.Symlink(realRoot, aliasRoot); err != nil {
		t.Skipf("symlink creation not supported (requires admin on Windows): %v", err)
	}
	if err := os.MkdirAll(filepath.Join(realRoot, "lisp"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(realRoot, "lisp", "init.el"), []byte("seed"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	registry := NewRegistry()
	if err := RegisterFilesystem(registry, FilesystemConfig{Root: realRoot}); err != nil {
		t.Fatalf("register: %v", err)
	}

	target := filepath.Join(aliasRoot, "lisp", "init.el")
	got, err := registry.Call(context.Background(), "fs_read_file", []byte(`{"path":`+strconv.Quote(target)+`}`))
	if err != nil {
		t.Fatalf("read via workspace symlink alias must succeed: %v", err)
	}
	if !strings.Contains(got, "seed") {
		t.Fatalf("read via alias returned %q", got)
	}

	writeTarget := filepath.Join(aliasRoot, "lisp", "new.el")
	if _, err := registry.Call(context.Background(), "fs_write_file", []byte(`{"path":`+strconv.Quote(writeTarget)+`,"content":"new"}`)); err != nil {
		t.Fatalf("write via workspace symlink alias must succeed: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(realRoot, "lisp", "new.el")); err != nil || string(data) != "new" {
		t.Fatalf("write via alias did not land in workspace: %v %q", err, data)
	}
}

// TestResolveWorkspacePathStillRejectsExternalViaSymlinkAlias checks the
// fail-closed companion case: a symlink alias of a directory outside the
// workspace must keep resolving outside and be rejected.
func TestResolveWorkspacePathStillRejectsExternalViaSymlinkAlias(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	if err := os.WriteFile(filepath.Join(external, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	aliasExternal := filepath.Join(t.TempDir(), "alias-external")
	if err := os.Symlink(external, aliasExternal); err != nil {
		t.Skipf("symlink creation not supported (requires admin on Windows): %v", err)
	}

	registry := NewRegistry()
	if err := RegisterFilesystem(registry, FilesystemConfig{Root: root}); err != nil {
		t.Fatalf("register: %v", err)
	}
	target := filepath.Join(aliasExternal, "secret.txt")
	if _, err := registry.Call(context.Background(), "fs_read_file", []byte(`{"path":`+strconv.Quote(target)+`}`)); err == nil {
		t.Fatal("read through symlink alias of external dir must fail without approval")
	}
}
