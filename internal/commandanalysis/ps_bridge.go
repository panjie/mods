package commandanalysis

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/panjie/mods/internal/commandtrace"
)

//go:embed ps_bridge.ps1
var psBridgeScript []byte

// psBridgeIR is the intermediate representation emitted by the PowerShell
// bridge script after parsing a command. The Go-side classifier consumes
// this to determine read-only status.
type psBridgeIR struct {
	Nodes                        []psSyntaxNode        `json:"nodes"`
	Redirections                 []psRedirection       `json:"redirections"`
	Version                      string                `json:"version"`
	Commands                     []string              `json:"commands"`
	Operators                    []string              `json:"operators"`
	Redirects                    []string              `json:"redirects"`
	Expansions                   []string              `json:"expansions"`
	RiskFlags                    []string              `json:"risk_flags"`
	ParseErrors                  []string              `json:"parse_errors"`
	HasScriptBlock               bool                  `json:"has_script_block"`
	HasAssignment                bool                  `json:"has_assignment"`
	HasBackground                bool                  `json:"has_background"`
	HasStopParsing               bool                  `json:"has_stop_parsing"`
	HasControlFlow               bool                  `json:"has_control_flow"`
	CommandArgs                  map[string][]string   `json:"command_args"`
	Paths                        []string              `json:"paths"`
	ForEachMemberNames           []string              `json:"foreach_member_names"`
	Variables                    []string              `json:"variables"`
	AssignmentTargets            []string              `json:"assignment_targets"`
	ScriptBlockAssignmentTargets []string              `json:"script_block_assignment_targets"`
	LiteralAssignments           map[string]string     `json:"literal_assignments"`
	MethodInvocations            []string              `json:"method_invocations"`
	MemberExpressions            []string              `json:"member_expressions"`
	StaticMembers                []string              `json:"static_members"`
	Invocations                  []psCommandInvocation `json:"command_invocations"`
	TopLevelValueExpressions     []string              `json:"top_level_value_expressions"`
	TopLevelStatementCount       int                   `json:"top_level_statement_count"`
	PipelineCount                int                   `json:"pipeline_count"`
}

type psCommandInvocation struct {
	NodeID    int          `json:"node_id"`
	Arguments []psArgument `json:"arguments"`
	Name      string       `json:"name"`
	Args      []string     `json:"args"`
}

type bridgeProcess struct {
	hostKey      [32]byte
	shutdownOnce sync.Once
	mu           sync.Mutex
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	stdout       *bufio.Reader
	scriptPath   string
	dead         bool
}

func (bp *bridgeProcess) shutdown() {
	bp.shutdownOnce.Do(func() {
		defer func() {
			if bp.scriptPath != "" {
				_ = os.Remove(bp.scriptPath)
			}
		}()
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
	})
}

var (
	globalBridge   *bridgeProcess
	globalBridgeMu sync.Mutex
)

// getWindowsShellPath resolves the PowerShell binary that runs the
// classification bridge. It MUST match the shell that actually executes
// shell_run / powershell_run commands (internal/tools.windowsPowerShellExe
// prefers pwsh.exe and falls back to powershell.exe), so the bridge parses
// under the same PowerShell grammar that will run. PowerShell 7 (pwsh.exe)
// is preferred when on PATH: it ships Get-FileHash as a core cmdlet and
// avoids the PS5.1 -NoProfile module auto-load timing issues that break
// scoop's hash verification on some locales. When pwsh.exe is absent the
// bridge falls back to Windows PowerShell 5.1 (powershell.exe); if neither
// is available the bridge fails to start and IsReadOnlyPowerShell fails
// closed. If the executor is ever changed to use a different host, update
// this resolver to match.
func getWindowsShellPath() string {
	shell, _ := powerShellHost(os.Environ())
	return shell
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
	return getOrStartBridgeFor(context.Background(), os.Environ())
}
func getOrStartBridgeFor(ctx context.Context, env []string) (*bridgeProcess, error) {
	shell, err := powerShellHost(env)
	if err != nil {
		return nil, err
	}
	key := sha256.Sum256([]byte(shell + "\x00" + strings.Join(env, "\x00")))
	for !globalBridgeMu.TryLock() {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
	defer globalBridgeMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if globalBridge != nil && globalBridge.hostKey == key {
		return globalBridge, nil
	}
	if globalBridge != nil {
		old := globalBridge
		globalBridge = nil
		go old.shutdown()
	}
	bp, err := startBridgeProcessFor(shell, env)
	if err != nil {
		return nil, err
	}
	bp.hostKey = key
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
	env := os.Environ()
	shell, err := powerShellHost(env)
	if err != nil {
		return nil, err
	}
	return startBridgeProcessFor(shell, env)
}
func startBridgeProcessFor(shell string, env []string) (*bridgeProcess, error) {
	scriptFile, err := os.CreateTemp("", "mods-ps-bridge-*.ps1")
	if err != nil {
		return nil, fmt.Errorf("create bridge script: %w", err)
	}
	scriptPath := scriptFile.Name()
	if _, err := scriptFile.Write(psBridgeScript); err != nil {
		_ = scriptFile.Close()
		_ = os.Remove(scriptPath)
		return nil, fmt.Errorf("write bridge script: %w", err)
	}
	if err := scriptFile.Close(); err != nil {
		_ = os.Remove(scriptPath)
		return nil, fmt.Errorf("close bridge script: %w", err)
	}
	cmd := exec.Command(
		shell,
		"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-File", scriptPath,
	)
	cmd.Env = env
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		_ = os.Remove(scriptPath)
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdinPipe.Close()
		_ = os.Remove(scriptPath)
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		_ = stdinPipe.Close()
		_ = stdoutPipe.Close()
		_ = os.Remove(scriptPath)
		return nil, fmt.Errorf("start bridge: %w", err)
	}
	return &bridgeProcess{
		cmd:        cmd,
		stdin:      stdinPipe,
		stdout:     bufio.NewReader(stdoutPipe),
		scriptPath: scriptPath,
	}, nil
}

const bridgeCallTimeout = 15 * time.Second
const maxBridgeOutputBytes = 4 << 20

func (bp *bridgeProcess) roundTrip(command string) ([]byte, error) {
	return bp.roundTripContext(context.Background(), command)
}
func (bp *bridgeProcess) roundTripContext(ctx context.Context, command string) ([]byte, error) {
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
		line, rerr := readBridgeLine(bp.stdout)
		ch <- result{bytes.TrimSpace(line), rerr}
	}()

	select {
	case r := <-ch:
		return r.data, r.err
	case <-ctx.Done():
		_ = bp.stdin.Close()
		return nil, ctx.Err()
	case <-time.After(bridgeCallTimeout):
		_ = bp.stdin.Close()
		return nil, fmt.Errorf("bridge call timed out after %s", bridgeCallTimeout)
	}
}

// parseWithBridge sends a command to the persistent PowerShell bridge and
// returns the parsed IR. On any error (non-Windows, no powershell.exe, transport
// failure, JSON decode error) it returns a non-nil error so the caller
// can fail-closed. One automatic restart is attempted on transport failure.
func parseWithBridge(command string) (*psBridgeIR, error) {
	return parseWithBridgeContext(context.Background(), command)
}
func parseWithBridgeContext(ctx context.Context, command string) (*psBridgeIR, error) {
	return parseWithBridgeHost(ctx, command, os.Environ())
}
func parseWithBridgeHost(ctx context.Context, command string, env []string) (*psBridgeIR, error) {
	commandtrace.BridgeParse()
	if runtime.GOOS != "windows" {
		return nil, fmt.Errorf("powerShell bridge requires Windows")
	}
	if err := sanitizeCommand(command); err != nil {
		return nil, err
	}

	for attempt := 0; attempt < 2; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		bp, err := getOrStartBridgeFor(ctx, env)
		if err != nil {
			return nil, err
		}

		for !bp.mu.TryLock() {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Millisecond):
			}
		}
		if bp.dead {
			bp.mu.Unlock()
			invalidateBridge(bp)
			go bp.shutdown()
			continue
		}
		commandtrace.BridgeRoundTrip()
		raw, callErr := bp.roundTripContext(ctx, command)
		if callErr != nil {
			bp.dead = true
			bp.mu.Unlock()
			invalidateBridge(bp)
			go bp.shutdown()
			if attempt == 0 && ctx.Err() == nil {
				continue
			}
			return nil, callErr
		}
		bp.mu.Unlock()

		ir, decodeErr := unmarshalBridgeResponse(raw)
		if decodeErr != nil {
			bp.mu.Lock()
			bp.dead = true
			bp.mu.Unlock()
			invalidateBridge(bp)
			go bp.shutdown()
		}
		return ir, decodeErr
	}
	return nil, fmt.Errorf("bridge unavailable after restart")
}

func unmarshalBridgeResponse(raw []byte) (*psBridgeIR, error) {
	if len(raw) > maxBridgeOutputBytes {
		return nil, errAnalysisBudget
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("bridge emitted empty output")
	}
	var ir psBridgeIR
	if err := json.Unmarshal(raw, &ir); err != nil {
		return nil, fmt.Errorf("bridge JSON decode: %w", err)
	}
	if ir.Version != "2" {
		return nil, fmt.Errorf("bridge IR version mismatch: got %q", ir.Version)
	}
	return &ir, nil
}

func readBridgeLine(reader *bufio.Reader) ([]byte, error) {
	var data []byte
	for {
		part, err := reader.ReadSlice('\n')
		if len(data)+len(part) > maxBridgeOutputBytes {
			return nil, errAnalysisBudget
		}
		data = append(data, part...)
		if err == bufio.ErrBufferFull {
			continue
		}
		return data, err
	}
}

type psSyntaxNode struct {
	ID     int    `json:"id"`
	Parent int    `json:"parent"`
	Start  int    `json:"start"`
	End    int    `json:"end"`
	Kind   string `json:"kind"`
}
type psArgument struct {
	NodeID  int    `json:"node_id"`
	Text    string `json:"text"`
	Literal bool   `json:"literal"`
}
type psRedirection struct {
	NodeID  int    `json:"node_id"`
	Owner   int    `json:"owner"`
	Target  string `json:"target"`
	Literal bool   `json:"literal"`
	Append  bool   `json:"append"`
	Stream  int    `json:"stream"`
}
