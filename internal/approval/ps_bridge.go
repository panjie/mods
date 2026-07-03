package approval

import (
	"bufio"
	"bytes"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"sync"
	"time"
	"unicode/utf16"
)

//go:embed ps_bridge.ps1
var psBridgeScript []byte

// psBridgeIR is the intermediate representation emitted by the PowerShell
// bridge script after parsing a command. The Go-side classifier consumes
// this to determine read-only status.
type psBridgeIR struct {
	Version        string              `json:"version"`
	Commands       []string            `json:"commands"`
	Operators      []string            `json:"operators"`
	Redirects      []string            `json:"redirects"`
	Expansions     []string            `json:"expansions"`
	RiskFlags      []string            `json:"risk_flags"`
	ParseErrors    []string            `json:"parse_errors"`
	HasScriptBlock bool                `json:"has_script_block"`
	HasAssignment  bool                `json:"has_assignment"`
	HasBackground  bool                `json:"has_background"`
	HasStopParsing bool                `json:"has_stop_parsing"`
	HasControlFlow bool                `json:"has_control_flow"`
	CommandArgs    map[string][]string `json:"command_args"`
}

type bridgeProcess struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	dead   bool
}

func (bp *bridgeProcess) shutdown() {
	_ = bp.stdin.Close()
	done := make(chan struct{})
	go func() {
		_ = bp.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = bp.cmd.Process.Kill()
		<-done
	}
}

var (
	globalBridge   *bridgeProcess
	globalBridgeMu sync.Mutex

	winPSPath     string
	winPSPathOnce sync.Once

	winEncodedBridge     string
	winEncodedBridgeOnce sync.Once
)

func getWindowsShellPath() string {
	winPSPathOnce.Do(func() {
		if p, err := exec.LookPath("pwsh.exe"); err == nil {
			winPSPath = p
			return
		}
		if p, err := exec.LookPath("powershell.exe"); err == nil {
			winPSPath = p
			return
		}
		winPSPath = ""
	})
	return winPSPath
}

func encodePSScript(script []byte) string {
	u16 := utf16.Encode([]rune(string(script)))
	b := make([]byte, len(u16)*2)
	for i, r := range u16 {
		b[i*2] = byte(r)
		b[i*2+1] = byte(r >> 8)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func getBridgeEncoded() string {
	winEncodedBridgeOnce.Do(func() {
		winEncodedBridge = encodePSScript(psBridgeScript)
	})
	return winEncodedBridge
}

const maxCommandBytes = 65536

func sanitizeCommand(command string) error {
	if len(command) > maxCommandBytes {
		return fmt.Errorf("command exceeds %d byte limit", maxCommandBytes)
	}
	for _, r := range command {
		if r == '\x00' {
			return fmt.Errorf("command contains null byte")
		}
	}
	return nil
}

func getOrStartBridge() (*bridgeProcess, error) {
	globalBridgeMu.Lock()
	defer globalBridgeMu.Unlock()
	if globalBridge != nil {
		return globalBridge, nil
	}
	bp, err := startBridgeProcess()
	if err != nil {
		return nil, err
	}
	globalBridge = bp
	return bp, nil
}

func invalidateBridge(bp *bridgeProcess) {
	globalBridgeMu.Lock()
	if globalBridge == bp {
		globalBridge = nil
	}
	globalBridgeMu.Unlock()
}

// CloseBridge shuts down the global bridge process if one is running.
// Safe to call multiple times. Useful for testing and explicit cleanup.
func CloseBridge() {
	globalBridgeMu.Lock()
	bp := globalBridge
	globalBridge = nil
	globalBridgeMu.Unlock()
	if bp != nil {
		bp.shutdown()
	}
}

func startBridgeProcess() (*bridgeProcess, error) {
	shell := getWindowsShellPath()
	if shell == "" {
		return nil, fmt.Errorf("pwsh not available")
	}
	cmd := exec.Command(
		shell,
		"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-EncodedCommand", getBridgeEncoded(),
	)
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdinPipe.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		_ = stdinPipe.Close()
		_ = stdoutPipe.Close()
		return nil, fmt.Errorf("start bridge: %w", err)
	}
	return &bridgeProcess{
		cmd:    cmd,
		stdin:  stdinPipe,
		stdout: bufio.NewReader(stdoutPipe),
	}, nil
}

const bridgeCallTimeout = 15 * time.Second

func (bp *bridgeProcess) roundTrip(command string) ([]byte, error) {
	req, err := json.Marshal(struct {
		Cmd string `json:"cmd"`
	}{Cmd: command})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	req = append(req, '\n')

	type result struct {
		data []byte
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		if _, werr := bp.stdin.Write(req); werr != nil {
			ch <- result{nil, fmt.Errorf("write: %w", werr)}
			return
		}
		line, rerr := bp.stdout.ReadBytes('\n')
		ch <- result{bytes.TrimSpace(line), rerr}
	}()

	select {
	case r := <-ch:
		return r.data, r.err
	case <-time.After(bridgeCallTimeout):
		_ = bp.stdin.Close()
		return nil, fmt.Errorf("bridge call timed out after %s", bridgeCallTimeout)
	}
}

// parseWithBridge sends a command to the persistent PowerShell bridge and
// returns the parsed IR. On any error (non-Windows, no pwsh, transport
// failure, JSON decode error) it returns a non-nil error so the caller
// can fail-closed. One automatic restart is attempted on transport failure.
func parseWithBridge(command string) (*psBridgeIR, error) {
	if runtime.GOOS != "windows" {
		return nil, fmt.Errorf("powerShell bridge requires Windows")
	}
	if err := sanitizeCommand(command); err != nil {
		return nil, err
	}

	for attempt := 0; attempt < 2; attempt++ {
		bp, err := getOrStartBridge()
		if err != nil {
			return nil, err
		}

		bp.mu.Lock()
		if bp.dead {
			bp.mu.Unlock()
			invalidateBridge(bp)
			go bp.shutdown()
			continue
		}
		raw, callErr := bp.roundTrip(command)
		if callErr != nil {
			bp.dead = true
			bp.mu.Unlock()
			invalidateBridge(bp)
			go bp.shutdown()
			if attempt == 0 {
				continue
			}
			return nil, callErr
		}
		bp.mu.Unlock()

		return unmarshalBridgeResponse(raw)
	}
	return nil, fmt.Errorf("bridge unavailable after restart")
}

func unmarshalBridgeResponse(raw []byte) (*psBridgeIR, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("bridge emitted empty output")
	}
	var ir psBridgeIR
	if err := json.Unmarshal(raw, &ir); err != nil {
		return nil, fmt.Errorf("bridge JSON decode: %w", err)
	}
	if ir.Version != "1" {
		return nil, fmt.Errorf("bridge IR version mismatch: got %q", ir.Version)
	}
	return &ir, nil
}
