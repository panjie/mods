//go:build windows && windowsreliability

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsReliabilityEnvironment(t *testing.T) {
	info := SelectedShellInfo()
	inJob, err := reliabilityCurrentProcessInJob()
	if err != nil {
		t.Fatalf("query current Job Object: %v", err)
	}
	t.Logf("environment os=%s arch=%s shell=%q shell_version=%v windows_build=%d in_job=%t CI=%q WT_SESSION=%q PATH=%q PATHEXT=%q",
		runtime.GOOS, runtime.GOARCH, info.Executable, info.Version, windows.RtlGetVersion().BuildNumber,
		inJob, os.Getenv("CI"), os.Getenv("WT_SESSION"), os.Getenv("PATH"), os.Getenv("PATHEXT"))
	for _, name := range []string{"powershell.exe", "pwsh.exe", "node.exe", "npm.cmd", "winget.exe", "scoop.cmd"} {
		path, err := exec.LookPath(name)
		t.Logf("command name=%q found=%t path=%q", name, err == nil, path)
	}
}

func reliabilityCurrentProcessInJob() (bool, error) {
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("IsProcessInJob")
	var result uint32
	r1, _, callErr := proc.Call(uintptr(windows.CurrentProcess()), 0, uintptr(unsafe.Pointer(&result)))
	if r1 == 0 {
		return false, callErr
	}
	return result != 0, nil
}

func TestWindowsReliabilityContextCancellationKillsDescendants(t *testing.T) {
	root := t.TempDir()
	executable, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(root, "grandchild.pid")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	_, err = (commandRunner{
		Parent:  ctx,
		Timeout: 5 * time.Second,
		BuildCommand: func(runCtx context.Context) *exec.Cmd {
			return exec.CommandContext(runCtx, executable, "-test.run=^TestProcessHelperProcess$", "--", "tree", pidFile)
		},
		Stdout: newBoundedOutput(commandOutputLimit),
		Stderr: newBoundedOutput(commandOutputLimit),
	}).Run()
	if err != context.Canceled {
		t.Fatalf("cancellation error = %v, want context.Canceled", err)
	}
	pid := readReliabilityPID(t, pidFile)
	waitForReliabilityProcessExit(t, pid)
}

func TestWindowsReliabilityNPMNodeTreeTimeout(t *testing.T) {
	npm, npmErr := exec.LookPath("npm.cmd")
	node, nodeErr := exec.LookPath("node.exe")
	if npmErr != nil || nodeErr != nil {
		t.Skipf("blocked/not covered: npm/node unavailable (npm=%v node=%v)", npmErr, nodeErr)
	}
	root := t.TempDir()
	pidFile := filepath.Join(root, "node-child.pid")
	packageJSON := `{"scripts":{"tree":"node parent.js"}}`
	parentJS := `const {spawn}=require('child_process'); const fs=require('fs'); const os=require('os'); const path=require('path'); const c=spawn(process.execPath,[path.join(path.dirname(process.argv[2]),'child.js')],{stdio:'ignore',cwd:os.tmpdir()}); fs.writeFileSync(process.argv[2],String(c.pid)); process.chdir(os.tmpdir()); setTimeout(()=>{},120000);`
	childJS := `process.chdir(require('os').tmpdir()); setTimeout(()=>{},120000);`
	for name, body := range map[string]string{"package.json": packageJSON, "parent.js": parentJS, "child.js": childJS} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	command := fmt.Sprintf("& %s run tree -- %s", quotePowerShellReliability(npm), quotePowerShellReliability(pidFile))
	_, err := (ShellRunner{Root: root, Tool: "powershell_run", Timeout: DefaultShellTimeout, BuildCommand: powerShellCommand}).Run(context.Background(), command)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("run error = %v, want timeout", err)
	}
	pid := readReliabilityPID(t, pidFile)
	nested, err := reliabilityCurrentProcessInJob()
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for testProcessExists(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	survived := testProcessExists(pid)
	killReliabilityTreeByCommandLine(filepath.Base(filepath.Dir(filepath.Dir(pidFile))))
	if survived {
		if nested {
			t.Skipf("blocked/not covered: descendant %d survived cleanup; inside an outer Job Object environment the runner's Job Object cannot capture PowerShell-launched npm/node processes (in_job=%t)", pid, nested)
		}
		t.Fatalf("descendant process %d survived cleanup", pid)
	}
	t.Logf("verified npm=%q node=%q descendant_pid=%d", npm, node, pid)
}

func TestWindowsReliabilityExitTimeoutRace(t *testing.T) {
	iterations := reliabilityIterations(t)
	executable, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < iterations; i++ {
		timeout := time.Duration(1+i%7) * time.Millisecond
		result, runErr := (commandRunner{
			Parent: context.Background(), Timeout: timeout,
			BuildCommand: func(runCtx context.Context) *exec.Cmd {
				return exec.CommandContext(runCtx, executable, "-test.run=^TestProcessHelperProcess$", "--", "argv", strconv.Itoa(i))
			},
			Stdout: newBoundedOutput(commandOutputLimit), Stderr: newBoundedOutput(commandOutputLimit),
		}).Run()
		if runErr != nil {
			t.Fatalf("iteration=%d timeout=%s seed=%d: %v", i, timeout, iterations, runErr)
		}
		if !result.TimedOut && result.WaitError != nil {
			t.Fatalf("iteration=%d timeout=%s: unexpected wait error: %v", i, timeout, result.WaitError)
		}
	}
	t.Logf("completed %d exit/timeout race iterations", iterations)
}

func TestWindowsReliabilityPathsAndJunctionBoundary(t *testing.T) {
	root := filepath.Join(t.TempDir(), "space 项目")
	deep := root
	for len(deep) < 280 {
		deep = filepath.Join(deep, "long-segment")
	}
	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Fatalf("create long Unicode path: %v", err)
	}
	resolved, err := resolveWorkspacePath(context.Background(), root, filepath.Join(deep, "mixed\\child.txt"), nil)
	if err != nil || !contains(root, resolved) {
		t.Fatalf("long/mixed path resolved=%q err=%v", resolved, err)
	}

	outside := t.TempDir()
	junction := filepath.Join(root, "junction")
	cmd := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", junction, outside)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("blocked/not covered: junction creation unavailable: %v (%s)", err, output)
	}
	if _, err := resolveWorkspacePath(context.Background(), root, filepath.Join(junction, "escape.txt"), nil); err == nil {
		t.Fatal("junction escape was accepted")
	}
}

func TestWindowsReliabilityUNCPath(t *testing.T) {
	root := t.TempDir()
	uncRoot := strings.TrimSpace(os.Getenv("TEST_UNC_ROOT"))
	if uncRoot == "" {
		t.Skip("blocked/not covered: TEST_UNC_ROOT is required for real UNC validation")
	}
	ctx := WithAuthorizedDirs(context.Background(), []string{uncRoot})
	if _, err := resolveWorkspacePath(ctx, root, uncRoot, nil); err != nil {
		t.Fatalf("approved UNC root rejected: %v", err)
	}
}

func TestWindowsReliabilityEncoding(t *testing.T) {
	want := "stdout-项目"
	runner := ShellRunner{
		Root: t.TempDir(), Tool: "powershell_run", Timeout: 5 * time.Second,
		BuildCommand: powerShellCommand,
	}
	command := `[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false); ` +
		`[Console]::Out.Write('stdout-项目'); [Console]::Error.Write(' stderr-错误')`
	out, err := runner.Run(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, want) || !strings.Contains(out, "stderr-错误") || strings.ContainsRune(out, '\uFFFD') {
		t.Fatalf("mixed Unicode output was corrupted: %q", out)
	}
}

func TestWindowsReliabilityPackageManagerShims(t *testing.T) {
	missing := make([]string, 0, 2)
	for _, name := range []string{"winget.exe", "scoop.cmd"} {
		if _, err := exec.LookPath(name); err != nil {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Skipf("blocked/not covered: package-manager commands unavailable: %s", strings.Join(missing, ", "))
	}
	for _, name := range []string{"scoop.cmd", "npm.cmd"} {
		if path, err := exec.LookPath(name); err == nil && !unsupportedProcessProgramForGOOS(path, runtime.GOOS) {
			t.Fatalf("batch shim %q was accepted by process_run", path)
		}
	}
}

func reliabilityIterations(t *testing.T) int {
	t.Helper()
	value := strings.TrimSpace(os.Getenv("MODS_WINDOWS_STRESS_ITERATIONS"))
	if value == "" {
		return 100
	}
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		t.Fatalf("invalid MODS_WINDOWS_STRESS_ITERATIONS=%q", value)
	}
	return n
}

func readReliabilityPID(t *testing.T, path string) int {
	t.Helper()
	var data []byte
	var err error
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		data, err = os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			return pid
		}
	}
	t.Fatalf("read PID file %q: %v", path, err)
	return 0
}

// killReliabilityTreeByCommandLine force-kills any escaped process tree whose
// command line references the given token. Inside an outer Job Object
// environment the runner's Job Object cannot capture PowerShell-launched
// native trees, so their survivors must be cleaned up here to release the
// fixture directory before TempDir removal. The token must not contain
// backslashes: they break the wmic LIKE pattern ("Invalid query").
func killReliabilityTreeByCommandLine(needle string) {
	out, err := exec.Command("wmic", "process", "where", fmt.Sprintf("CommandLine like '%%%s%%'", needle), "get", "ProcessId").Output()
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 1 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		_ = exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(pid)).Run()
	}
}

func waitForReliabilityProcessExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for testProcessExists(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if testProcessExists(pid) {
		t.Fatalf("descendant process %d survived cleanup", pid)
	}
}

func quotePowerShellReliability(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func TestWindowsReliabilityProcessBindingSurvivesPATHMutation(t *testing.T) {
	executable, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	first := t.TempDir()
	second := t.TempDir()
	name := "mods-binding-test.exe"
	for _, dir := range []string{first, second} {
		data, readErr := os.ReadFile(executable)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if writeErr := os.WriteFile(filepath.Join(dir, name), data, 0o700); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	t.Setenv("PATH", first)
	binding, err := PrepareProcessProgram(mustReliabilityJSON(t, map[string]any{"program": name}))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", second)
	resolved, err := resolveProcessProgram(WithProcessProgramBinding(context.Background(), binding), t.TempDir(), t.TempDir(), name, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(resolved, filepath.Join(first, name)) {
		t.Fatalf("approved executable changed after PATH mutation: got %q", resolved)
	}
}

func mustReliabilityJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
