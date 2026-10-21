//go:build windows

package tools

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

const elevationPipePrefix = `\\.\pipe\mods-uac-`
const elevationMaxFrame = 1 << 20

type elevationRequest struct {
	Program string
	Args    []string
	Dir     string
	Env     []string
	Timeout time.Duration
}

type elevationMessage struct {
	Stream   string
	Data     []byte
	ExitCode *int
	Duration time.Duration
	TimedOut bool
	Error    string
}

// Each frame is bounded before allocation. Commands, environment and output
// travel only over this pipe, never through shell arguments or temporary files.
func writeElevationFrame(w io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(data) > elevationMaxFrame {
		return fmt.Errorf("UAC message exceeds size limit")
	}
	if err := binary.Write(w, binary.LittleEndian, uint32(len(data))); err != nil {
		return err
	}
	_, err = io.Copy(w, bytes.NewReader(data))
	return err
}

func readElevationFrame(r io.Reader, value any) error {
	var size uint32
	if err := binary.Read(r, binary.LittleEndian, &size); err != nil {
		return err
	}
	if size == 0 || size > elevationMaxFrame {
		return fmt.Errorf("invalid UAC message size")
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(r, data); err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

func (r commandRunner) runElevated() (commandRunResult, error) {
	// An already elevated caller needs no second UAC prompt. It still uses the
	// ordinary command runner and its per-call process tree and cancellation.
	if windows.GetCurrentProcessToken().IsElevated() {
		r.Elevated = false
		return r.Run()
	}
	ctx, cancel := context.WithTimeout(r.Parent, r.Timeout)
	defer cancel()
	started := time.Now()
	cmd := r.BuildCommand(ctx)
	if cmd == nil {
		return commandRunResult{}, fmt.Errorf("command builder returned nil")
	}
	if cmd.Err != nil {
		return commandRunResult{}, cmd.Err
	}
	if r.Prepare != nil {
		cleanup, err := r.Prepare(ctx, cmd)
		if err != nil {
			return commandRunResult{}, err
		}
		defer cleanup()
	}
	result, err := runElevationBroker(ctx, cmd, r.Stdout, r.Stderr, r.Progress, launchElevation)
	if r.Parent.Err() != nil {
		return commandRunResult{}, r.Parent.Err()
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		result.TimedOut, result.ExitCode = true, nil
		result.Duration = time.Since(started)
		return result, nil
	}
	return result, err
}

type elevationProcess struct {
	pid    uint32
	handle windows.Handle
}

type elevationLauncher func(exe, address string, parentPID int) (elevationProcess, error)

func runElevationBroker(ctx context.Context, cmd *exec.Cmd, stdout, stderr io.Writer, progress func(time.Duration), launch elevationLauncher) (commandRunResult, error) {
	started := time.Now()
	if err := ctx.Err(); err != nil {
		return commandRunResult{}, err
	}
	exe, err := os.Executable()
	if err != nil {
		return commandRunResult{}, err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return commandRunResult{}, err
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return commandRunResult{}, err
	}
	address := elevationPipePrefix + hex.EncodeToString(nonce[:])
	listener, err := winio.ListenPipe(address, &winio.PipeConfig{
		// BA permits UAC credentials from a different administrator. Peer PIDs
		// are checked in BOTH directions before transmitting any command.
		SecurityDescriptor: "D:P(A;;GA;;;" + user.User.Sid.String() + ")(A;;GA;;;BA)",
	})
	if err != nil {
		return commandRunResult{}, fmt.Errorf("create UAC pipe: %w", err)
	}
	defer listener.Close()
	stop := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stop()

	// ShellExecuteEx can block inside the OS consent dialog. Canceling closes
	// the pipe immediately; a helper launched by late consent cannot run work.
	type launchResult struct {
		process elevationProcess
		err     error
	}
	launched := make(chan launchResult)
	go func() {
		process, err := launch(exe, address, os.Getpid())
		select {
		case launched <- launchResult{process, err}:
		case <-ctx.Done():
			if process.handle != 0 {
				_ = windows.CloseHandle(process.handle)
			}
		}
	}()
	var process elevationProcess
	select {
	case result := <-launched:
		if result.err != nil {
			if errors.Is(result.err, windows.ERROR_CANCELLED) {
				return commandRunResult{}, fmt.Errorf("Windows UAC elevation canceled by user: %w", result.err)
			}
			return commandRunResult{}, fmt.Errorf("Windows UAC elevation failed: %w", result.err)
		}
		process = result.process
	case <-ctx.Done():
		return commandRunResult{}, ctx.Err()
	}
	defer windows.CloseHandle(process.handle)

	type acceptedConn struct {
		conn net.Conn
		err  error
	}
	accepted := make(chan acceptedConn)
	acceptCtx, cancelAccept := context.WithCancel(ctx)
	defer cancelAccept()
	go func() {
		for {
			conn, err := listener.Accept()
			if err == nil {
				pid, peerErr := elevationPeerPID(conn, false)
				if peerErr != nil || pid != process.pid {
					_ = conn.Close()
					continue
				}
			}
			select {
			case accepted <- acceptedConn{conn, err}:
			case <-acceptCtx.Done():
				if conn != nil {
					_ = conn.Close()
				}
			}
			return
		}
	}()
	// Detect a helper that exits before connecting (e.g. an incompatible exe).
	poll := time.NewTicker(100 * time.Millisecond)
	defer poll.Stop()
	var conn net.Conn
awaitConnection:
	for {
		select {
		case accepted := <-accepted:
			if accepted.err != nil {
				return commandRunResult{}, accepted.err
			}
			conn = accepted.conn
			break awaitConnection
		case <-poll.C:
			status, err := windows.WaitForSingleObject(process.handle, 0)
			if err != nil || status == windows.WAIT_OBJECT_0 {
				return commandRunResult{}, fmt.Errorf("UAC helper exited before connecting")
			}
		case <-ctx.Done():
			return commandRunResult{}, ctx.Err()
		}
	}
	defer func() {
		_ = conn.Close()
		// Give the helper time to reap its job on cancellation before the next
		// tool starts. Never wait indefinitely on a misbehaving helper.
		_, _ = windows.WaitForSingleObject(process.handle, 3000)
	}()
	closeOnCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer closeOnCancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		return commandRunResult{}, fmt.Errorf("UAC execution requires a deadline")
	}
	_ = conn.SetDeadline(deadline)
	if err := ctx.Err(); err != nil {
		return commandRunResult{}, err
	}
	request := elevationRequest{Program: cmd.Path, Args: cmd.Args[1:], Dir: cmd.Dir, Env: cmd.Environ(), Timeout: time.Until(deadline)}
	if err := writeElevationFrame(conn, request); err != nil {
		return commandRunResult{}, err
	}
	if progress != nil {
		progress(0)
	}
	// Reading synchronously bounds queued output. Progress ticks also report
	// quiet commands and are joined before returning to avoid stale callbacks.
	progressCtx, cancelProgress := context.WithCancel(ctx)
	progressDone := make(chan struct{})
	defer func() { cancelProgress(); <-progressDone }()
	if progress != nil {
		go func() {
			defer close(progressDone)
			tick := time.NewTicker(defaultShellProgressInterval)
			defer tick.Stop()
			for {
				select {
				case <-tick.C:
					progress(time.Since(started))
				case <-progressCtx.Done():
					return
				}
			}
		}()
	} else {
		close(progressDone)
	}
	for {
		var message elevationMessage
		if err := readElevationFrame(conn, &message); err != nil {
			return commandRunResult{}, fmt.Errorf("UAC helper connection ended without a result: %w", err)
		}
		switch message.Stream {
		case "stdout", "stderr":
			writer := stdout
			if message.Stream == "stderr" {
				writer = stderr
			}
			if writer != nil {
				if _, err := writer.Write(message.Data); err != nil {
					return commandRunResult{}, err
				}
			}
		case "result":
			result := commandRunResult{ExitCode: message.ExitCode, Duration: message.Duration, TimedOut: message.TimedOut}
			if message.Error != "" {
				return result, errors.New(message.Error)
			}
			if !result.TimedOut && result.ExitCode == nil {
				return result, fmt.Errorf("UAC helper returned no exit status")
			}
			return result, nil
		default:
			return commandRunResult{}, fmt.Errorf("invalid UAC response")
		}
	}
}

func elevationPeerPID(conn net.Conn, server bool) (uint32, error) {
	file, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		return 0, fmt.Errorf("UAC pipe does not expose a handle")
	}
	var pid uint32
	var err error
	if server {
		err = windows.GetNamedPipeServerProcessId(windows.Handle(file.Fd()), &pid)
	} else {
		err = windows.GetNamedPipeClientProcessId(windows.Handle(file.Fd()), &pid)
	}
	return pid, err
}

func RunElevationHelper(args []string) error {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return fmt.Errorf("UAC helper requires an elevated process")
	}
	return runElevationHelper(args)
}

func runElevationHelper(args []string) error {
	if len(args) != 2 || !strings.HasPrefix(args[0], elevationPipePrefix) {
		return fmt.Errorf("invalid UAC helper arguments")
	}
	nonce, err := hex.DecodeString(strings.TrimPrefix(args[0], elevationPipePrefix))
	if err != nil || len(nonce) != 32 {
		return fmt.Errorf("invalid UAC pipe name")
	}
	pid, err := strconv.ParseUint(args[1], 10, 32)
	if err != nil || pid == 0 {
		return fmt.Errorf("invalid UAC parent PID")
	}
	// Retaining the handle prevents PID reuse for the duration of this helper.
	parent, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(parent)
	var name [32768]uint16
	size := uint32(len(name))
	if err := windows.QueryFullProcessImageName(parent, 0, &name[0], &size); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if !strings.EqualFold(filepath.Clean(windows.UTF16ToString(name[:size])), filepath.Clean(exe)) {
		return fmt.Errorf("UAC parent is not this mods executable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := winio.DialPipeContext(ctx, args[0])
	if err != nil {
		return err
	}
	defer conn.Close()
	peer, err := elevationPeerPID(conn, true)
	if err != nil || peer != uint32(pid) {
		return fmt.Errorf("UAC pipe server identity mismatch")
	}
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	var request elevationRequest
	if err := readElevationFrame(conn, &request); err != nil {
		return err
	}
	if request.Timeout <= 0 || !filepath.IsAbs(request.Program) || !filepath.IsAbs(request.Dir) {
		return fmt.Errorf("invalid UAC execution request")
	}
	_ = conn.SetDeadline(time.Time{})
	return executeElevationRequest(conn, request)
}

// The elevated helper owns process-tree termination; an unelevated parent
// cannot reliably kill an elevated child. Pipe EOF also covers parent crashes.
func executeElevationRequest(conn net.Conn, request elevationRequest) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// A stalled pipe reader must not leave an elevated writer alive forever.
	_ = conn.SetWriteDeadline(time.Now().Add(request.Timeout + 2*processKillGrace))
	go func() {
		var b [1]byte
		_, _ = conn.Read(b[:]) // no further requests are permitted
		cancel()
	}()
	var mu sync.Mutex
	stdout := elevationOutput{conn: conn, mu: &mu, stream: "stdout", cancel: cancel}
	stderr := elevationOutput{conn: conn, mu: &mu, stream: "stderr", cancel: cancel}
	result, err := (commandRunner{
		Parent: ctx, Timeout: request.Timeout,
		BuildCommand: func(ctx context.Context) *exec.Cmd {
			cmd := exec.CommandContext(ctx, request.Program, request.Args...)
			cmd.Dir, cmd.Env = request.Dir, request.Env
			return cmd
		},
		Stdout: stdout, Stderr: stderr,
	}).Run()
	message := elevationMessage{Stream: "result", ExitCode: result.ExitCode, Duration: result.Duration, TimedOut: result.TimedOut}
	if err != nil {
		message.Error = err.Error()
	} else if result.WaitError != nil && !result.TimedOut {
		var exitErr *exec.ExitError
		if !errors.As(result.WaitError, &exitErr) {
			message.Error = result.WaitError.Error()
		}
	}
	mu.Lock()
	defer mu.Unlock()
	return writeElevationFrame(conn, message)
}

type elevationOutput struct {
	conn   net.Conn
	mu     *sync.Mutex
	stream string
	cancel context.CancelFunc
}

func (w elevationOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	total := 0
	for len(p) > 0 {
		n := min(len(p), 8192)
		if err := writeElevationFrame(w.conn, elevationMessage{Stream: w.stream, Data: p[:n]}); err != nil {
			w.cancel()
			return total, err
		}
		total += n
		p = p[n:]
	}
	return total, nil
}

// SHELLEXECUTEINFOW layout follows shellapi.h on both 32- and 64-bit Windows.
type shellExecuteInfo struct {
	Size       uint32
	Mask       uint32
	Window     windows.Handle
	Verb       *uint16
	File       *uint16
	Parameters *uint16
	Directory  *uint16
	Show       int32
	Instance   windows.Handle
	IDList     uintptr
	Class      *uint16
	ClassKey   windows.Handle
	HotKey     uint32
	Icon       windows.Handle
	Process    windows.Handle
}

var shellExecuteEx = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")

func launchElevation(exe, address string, parentPID int) (elevationProcess, error) {
	// ShellExecute may use COM and requires a stable apartment/thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE); err != nil && !errors.Is(err, syscall.Errno(1)) { // S_FALSE also succeeds
		return elevationProcess{}, err
	}
	defer windows.CoUninitialize()
	verb, _ := windows.UTF16PtrFromString("runas")
	file, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return elevationProcess{}, err
	}
	parameters, err := windows.UTF16PtrFromString(windows.ComposeCommandLine([]string{ElevationHelperArg, address, strconv.Itoa(parentPID)}))
	if err != nil {
		return elevationProcess{}, err
	}
	const seeMaskNoCloseProcess = 0x40
	const seeMaskNoAsync = 0x100
	const seeMaskFlagNoUI = 0x400 // suppress shell error dialogs, not UAC consent
	info := shellExecuteInfo{Mask: seeMaskNoCloseProcess | seeMaskNoAsync | seeMaskFlagNoUI, Verb: verb, File: file, Parameters: parameters, Show: windows.SW_HIDE}
	info.Size = uint32(unsafe.Sizeof(info))
	ok, _, callErr := shellExecuteEx.Call(uintptr(unsafe.Pointer(&info)))
	runtime.KeepAlive(info)
	if ok == 0 {
		return elevationProcess{}, callErr
	}
	pid, err := windows.GetProcessId(info.Process)
	if err != nil {
		_ = windows.CloseHandle(info.Process)
		return elevationProcess{}, err
	}
	return elevationProcess{pid: pid, handle: info.Process}, nil
}
