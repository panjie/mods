//go:build windows

package tools

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// A real separate helper and named pipe, without requiring UAC on CI. Only this
// test entry point omits the production helper's elevated-token requirement.
func TestElevationHelperProcess(t *testing.T) {
	if len(os.Args) < 5 || os.Args[2] != "--" {
		return
	}
	if err := runElevationHelper(os.Args[3:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func testElevationLauncher(t *testing.T) elevationLauncher {
	t.Helper()
	return func(exe, address string, pid int) (elevationProcess, error) {
		cmd := exec.Command(exe, "-test.run=^TestElevationHelperProcess$", "--", address, strconv.Itoa(pid))
		var output bytes.Buffer
		cmd.Stderr = &output
		if err := cmd.Start(); err != nil {
			return elevationProcess{}, err
		}
		handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(cmd.Process.Pid))
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		t.Cleanup(func() {
			select {
			case err := <-done:
				if err != nil {
					t.Logf("helper exited: %v: %s", err, output.String())
				}
			case <-time.After(20 * time.Second):
				_ = cmd.Process.Kill()
				t.Error("UAC helper did not exit")
			}
		})
		return elevationProcess{pid: uint32(cmd.Process.Pid), handle: handle}, err
	}
}

func TestElevationBrokerOutputAndExitCode(t *testing.T) {
	exe, err := os.Executable()
	require.NoError(t, err)
	for _, action := range []string{"result", "argv", "cwd", "large"} {
		t.Run(action, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			args := []string{"-test.run=^TestProcessHelperProcess$", "--", action}
			values := []string{"", "a b", `C:\中文 空格\`, `a\"b`, "$(not-expanded)", "; exit 99"}
			if action == "argv" {
				args = append(args, values...)
			}
			cmd := exec.CommandContext(ctx, exe, args...)
			cmd.Dir = t.TempDir()
			stdout, stderr := newBoundedOutput(commandOutputLimit), newBoundedOutput(commandOutputLimit)
			result, err := runElevationBroker(ctx, cmd, stdout, stderr, nil, testElevationLauncher(t))
			require.NoError(t, err)
			require.NotNil(t, result.ExitCode)
			require.False(t, result.TimedOut)
			switch action {
			case "result":
				require.Equal(t, 7, *result.ExitCode)
				require.Equal(t, "standard output", stdout.Snapshot().Text)
				require.Equal(t, "standard error", stderr.Snapshot().Text)
			case "argv":
				var actual []string
				require.NoError(t, json.Unmarshal([]byte(stdout.Snapshot().Text), &actual))
				require.Equal(t, values, actual)
			case "cwd":
				require.Equal(t, cmd.Dir, stdout.Snapshot().Text)
			case "large":
				require.True(t, stdout.Snapshot().Truncated)
				require.EqualValues(t, (60<<10)-commandOutputLimit, stdout.Snapshot().OmittedBytes)
			}
		})
	}
}

func TestElevationPowerShellEnvironment(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := powerShellCommand(ctx, `[Console]::Write($env:MODS_UAC_TEST); [Console]::Error.Write('错误输出'); exit 23`)
	cmd.Dir = t.TempDir()
	cmd.Env = commandEnv(cmd.Environ(), map[string]string{"MODS_UAC_TEST": "中文 with spaces"})
	stdout, stderr := newBoundedOutput(commandOutputLimit), newBoundedOutput(commandOutputLimit)
	result, err := runElevationBroker(ctx, cmd, stdout, stderr, nil, testElevationLauncher(t))
	require.NoError(t, err)
	require.Equal(t, 23, *result.ExitCode)
	require.Equal(t, "中文 with spaces", stdout.Snapshot().Text)
	require.Equal(t, "错误输出", stderr.Snapshot().Text)
}

func TestElevationCancellationKillsTree(t *testing.T) {
	exe, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	root := t.TempDir()
	pidFile := filepath.Join(root, "child.pid")
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestProcessHelperProcess$", "--", "tree", pidFile)
	cmd.Dir = root
	done := make(chan error, 1)
	launch := testElevationLauncher(t)
	go func() {
		_, err := runElevationBroker(ctx, cmd, nil, nil, nil, launch)
		done <- err
	}()
	var pid int
	require.Eventually(t, func() bool {
		data, _ := os.ReadFile(pidFile)
		pid, _ = strconv.Atoi(string(data))
		return pid > 0
	}, 10*time.Second, 20*time.Millisecond)
	child, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	require.NoError(t, err)
	defer windows.CloseHandle(child)
	cancel()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("broker did not cancel")
	}
	status, err := windows.WaitForSingleObject(child, 3000)
	require.NoError(t, err)
	require.EqualValues(t, windows.WAIT_OBJECT_0, status, "helper must terminate descendants on pipe EOF")
}

func TestElevationRequestTimeout(t *testing.T) {
	parent, helper := net.Pipe()
	defer parent.Close()
	exe, err := os.Executable()
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() {
		defer helper.Close()
		done <- executeElevationRequest(helper, elevationRequest{
			Program: exe, Args: []string{"-test.run=^TestProcessHelperProcess$", "--", "timeout"},
			Dir: t.TempDir(), Env: os.Environ(), Timeout: 300 * time.Millisecond,
		})
	}()
	_ = parent.SetDeadline(time.Now().Add(5 * time.Second))
	var result elevationMessage
	require.NoError(t, readElevationFrame(parent, &result))
	require.Equal(t, "result", result.Stream)
	require.True(t, result.TimedOut)
	require.Nil(t, result.ExitCode)
	require.NoError(t, <-done)
}

func TestElevationDeniedAndLateConsent(t *testing.T) {
	cmd := exec.Command("unused")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := runElevationBroker(ctx, cmd, nil, nil, nil, func(string, string, int) (elevationProcess, error) {
		return elevationProcess{}, windows.ERROR_CANCELLED
	})
	require.ErrorContains(t, err, "canceled by user")

	ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	release := make(chan struct{})
	pipeName := make(chan string, 1)
	launchDone := make(chan struct{})
	_, err = runElevationBroker(ctx, cmd, nil, nil, nil, func(_ string, address string, _ int) (elevationProcess, error) {
		defer close(launchDone)
		pipeName <- address
		<-release
		return elevationProcess{}, windows.ERROR_CANCELLED
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	close(release)
	<-launchDone
	dialCtx, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer stop()
	conn, err := winio.DialPipeContext(dialCtx, <-pipeName)
	if conn != nil {
		_ = conn.Close()
	}
	require.Error(t, err, "late UAC consent must find no command pipe")
}

func TestElevationFrameBoundsAndHelperValidation(t *testing.T) {
	for _, size := range []uint32{0, elevationMaxFrame + 1, ^uint32(0)} {
		var b bytes.Buffer
		require.NoError(t, binary.Write(&b, binary.LittleEndian, size))
		var message elevationMessage
		require.ErrorContains(t, readElevationFrame(&b, &message), "message size")
	}
	var b bytes.Buffer
	require.Error(t, writeElevationFrame(&b, strings.Repeat("x", elevationMaxFrame+1)))
	for _, args := range [][]string{nil, {"remote", "1"}, {elevationPipePrefix + "bad", "1"}, {elevationPipePrefix + strings.Repeat("a", 64), "0"}} {
		require.Error(t, runElevationHelper(args))
	}
}

func TestElevationRejectsUnexpectedClient(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cmd := exec.Command("must-never-execute")
	launch := testElevationLauncher(t)
	_, err := runElevationBroker(ctx, cmd, nil, nil, nil, func(exe, address string, pid int) (elevationProcess, error) {
		process, err := launch(exe, address, pid)
		// Simulate another process connecting to this call's pipe. Its request
		// must be rejected before any command or environment is transmitted.
		process.pid = uint32(os.Getpid())
		return process, err
	})
	require.Error(t, err)
}

func TestElevationCommandProcess(t *testing.T) {
	if len(os.Args) < 3 || os.Args[2] != "--" {
		return
	}
	if !windows.GetCurrentProcessToken().IsElevated() {
		os.Exit(11)
	}
	fmt.Fprint(os.Stdout, "standard output 中文")
	fmt.Fprint(os.Stderr, "standard error")
	os.Exit(7)
}

// Opt in explicitly on a Windows desktop. This exercises the actual native
// ShellExecuteEx/UAC path and production helper entry point, using only output.
func TestUACIntegration(t *testing.T) {
	if os.Getenv("MODS_TEST_UAC") != "1" {
		t.Skip("set MODS_TEST_UAC=1 to approve a real UAC prompt")
	}
	exe, err := os.Executable()
	require.NoError(t, err)
	root := t.TempDir()
	ctx := WithAuthorizedDirs(context.Background(), []string{filepath.Dir(exe)})
	text, err := runProcess(ctx, ProcessConfig{Timeout: 2 * time.Minute}, root, processRunArgs{
		Program: exe, Args: []string{"-test.run=^TestElevationCommandProcess$", "--"}, Elevated: true,
	})
	require.NoError(t, err)
	var result processRunResult
	require.NoError(t, json.Unmarshal([]byte(text), &result))
	require.Equal(t, 7, *result.ExitCode)
	require.Equal(t, "standard output 中文", result.Stdout)
	require.Equal(t, "standard error", result.Stderr)
}
