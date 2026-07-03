# PowerShell AST 只读命令分类器实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 通过持久化 pwsh.exe 桥接进程调用 PowerShell 官方 Parser::ParseInput，在本地判定 PowerShell 命令是否只读，减少 LLM 往返。

**Architecture:** 新建 `internal/approval/ps_bridge.ps1`（嵌入式 PS 脚本）+ `ps_bridge.go`（Go 侧进程管理）+ `readonly_ps.go`（IR 分类 + cmdlet 白名单），集成到 `shell_classify.go` 的 `powershell_run` 分支。

**Tech Stack:** Go 1.25, PowerShell `System.Management.Automation.Language.Parser`, `//go:embed`, JSON line protocol

## Global Constraints

- fail-closed：任何不确定情况返回 `(false, "")`，降级到 LLM
- 桥接绝不执行输入命令，仅解析
- 非 Windows 或无 pwsh.exe → `(false, "")`
- `ps_bridge.ps1` 通过 `//go:embed` 嵌入
- 复用现有 `extractExternalPaths` 提供 `AffectedDirs`
- 不修改 `review.go`
- 验证：`go test ./internal/approval -count=1` 和 `go run github.com/go-task/task/v3/cmd/task@v3.51.1 check`

---

### Task 1: 桥接脚本 + Go 侧进程管理

**Files:**
- Create: `internal/approval/ps_bridge.ps1`
- Create: `internal/approval/ps_bridge.go`
- Test: `internal/approval/ps_bridge_test.go`

**Interfaces:**
- Consumes: nothing (self-contained)
- Produces: `psBridgeIR` struct, `getOrStartBridge() (*bridgeProcess, error)`, `CloseBridge()`, `parseWithBridge(command string) (*psBridgeIR, error)`

- [ ] **Step 1: Create `internal/approval/ps_bridge.ps1`**

This is the embedded PowerShell bridge script. It runs as a persistent process, reads newline-delimited JSON `{"cmd":"..."}` from stdin, parses with `Parser::ParseInput`, walks the AST, and emits one compact JSON IR line to stdout. It NEVER executes the input command.

```powershell
# ps_bridge.ps1 — AST-backed PowerShell command parser for mods.
# Persistent process: reads newline JSON {"cmd":"..."} from stdin,
# parses with Parser::ParseInput, walks AST, emits one JSON IR line to stdout.
# NEVER executes the input command. Exits on EOF.

param()
Set-StrictMode -Off
$ErrorActionPreference = 'Stop'

function New-IR {
    return @{
        version           = "1"
        commands          = [System.Collections.Generic.List[string]]::new()
        operators         = [System.Collections.Generic.List[string]]::new()
        redirects         = [System.Collections.Generic.List[string]]::new()
        expansions        = [System.Collections.Generic.List[string]]::new()
        risk_flags        = [System.Collections.Generic.List[string]]::new()
        parse_errors      = [System.Collections.Generic.List[string]]::new()
        has_script_block  = $false
        has_assignment    = $false
        has_background    = $false
        has_stop_parsing  = $false
        has_control_flow  = $false
        command_args      = @{}
    }
}

function Add-Unique {
    param([System.Collections.Generic.List[string]]$List, [string]$Value)
    if (-not $List.Contains($Value)) { $List.Add($Value) | Out-Null }
}

# Control flow AST type names
$controlFlowTypes = @(
    'IfStatementAst', 'SwitchStatementAst', 'ForStatementAst',
    'ForEachStatementAst', 'WhileStatementAst', 'DoWhileStatementAst',
    'DoUntilStatementAst', 'TryStatementAst'
)

# Safe variable constants — not flagged as dynamic expansions
$varConstants = @('true', 'false', 'null', '_', 'psitem', 'PSItem', 'args', 'error', 'matches', 'foreach', 'home', 'pwd', 'host', 'null', 'input')

function Invoke-Parse {
    param([string]$command)

    $ir = New-IR

    $tokens = $null
    try {
        $tokenArr = [System.Management.Automation.Language.Token[]]@()
        $errorArr = [System.Management.Automation.Language.ParseError[]]@()
        $ast = [System.Management.Automation.Language.Parser]::ParseInput(
            $command, [ref]$tokenArr, [ref]$errorArr
        )
        $tokens = $tokenArr
        $parseErrors = $errorArr
    } catch {
        Add-Unique $ir.risk_flags "syntax_error"
        $ir.parse_errors.Add($_.ToString()) | Out-Null
        return $ir
    }

    foreach ($e in $parseErrors) {
        $ir.parse_errors.Add($e.Message) | Out-Null
        Add-Unique $ir.risk_flags "syntax_error"
    }

    $astNodes = $ast.FindAll({ $true }, $true)
    foreach ($node in $astNodes) {
        # Commands
        if ($node -is [System.Management.Automation.Language.CommandAst]) {
            $elems = $node.CommandElements
            if ($elems -and $elems.Count -gt 0) {
                $cmdName = $elems[0].ToString().Trim().ToLower()
                if ($cmdName -ne '') {
                    Add-Unique $ir.commands $cmdName
                    if (-not $ir.command_args.ContainsKey($cmdName)) {
                        $ir.command_args[$cmdName] = [System.Collections.Generic.List[string]]::new()
                    }
                    for ($i = 1; $i -lt $elems.Count; $i++) {
                        $argText = $elems[$i].ToString().Trim()
                        if ($argText.StartsWith('-')) {
                            $colonIdx = $argText.IndexOf(':')
                            if ($colonIdx -gt 0) { $argText = $argText.Substring(0, $colonIdx) }
                            if (-not $ir.command_args[$cmdName].Contains($argText)) {
                                $ir.command_args[$cmdName].Add($argText) | Out-Null
                            }
                        }
                    }
                }
            }
            # Background invocation: & operator
            if ($node.InvocationOperator -eq [System.Management.Automation.Language.TokenKind]::Ampersand) {
                $ir.has_background = $true
            }
            continue
        }

        # Pipeline: | operator
        if ($node -is [System.Management.Automation.Language.PipelineAst]) {
            if ($node.PipelineElements.Count -gt 1) {
                Add-Unique $ir.operators "|"
            }
            continue
        }

        # && and || via PipelineChain (PS7+)
        if ($node.GetType().Name -eq 'PipelineChainAst') {
            try {
                $op = $node.Operator.ToString()
                Add-Unique $ir.operators $op
            } catch {}
            continue
        }

        # File redirection (> >>)
        if ($node -is [System.Management.Automation.Language.FileRedirectionAst]) {
            Add-Unique $ir.redirects "FileRedirection"
            continue
        }

        # Merging redirection (2>&1 etc.)
        if ($node -is [System.Management.Automation.Language.MergingRedirectionAst]) {
            Add-Unique $ir.redirects "MergingRedirection"
            continue
        }

        # $(...) sub-expression
        if ($node -is [System.Management.Automation.Language.SubExpressionAst]) {
            Add-Unique $ir.expansions "subshell"
            continue
        }

        # Variable: $var — flag dynamic expansions only
        if ($node -is [System.Management.Automation.Language.VariableExpressionAst]) {
            $varName = $node.VariablePath.UserPath.ToLower()
            if ($varConstants -notcontains $varName) {
                Add-Unique $ir.expansions "var"
            }
            continue
        }

        # Script block: { ... }
        if ($node -is [System.Management.Automation.Language.ScriptBlockExpressionAst]) {
            $ir.has_script_block = $true
            continue
        }

        # Assignment: $x = ...
        if ($node -is [System.Management.Automation.Language.AssignmentStatementAst]) {
            $ir.has_assignment = $true
            continue
        }

        # Control flow
        $typeName = $node.GetType().Name
        if ($controlFlowTypes -contains $typeName) {
            $ir.has_control_flow = $true
            continue
        }
    }

    # Detect ';' from top-level statement count
    try {
        $endBlock = $ast.EndBlock
        if ($endBlock -and $endBlock.Statements -and $endBlock.Statements.Count -gt 1) {
            Add-Unique $ir.operators ";"
        }
    } catch {}

    # Scan tokens for -EncodedCommand and &&/|| (PS5.1 fallback)
    foreach ($tok in $tokens) {
        $tv = $tok.Text.ToLower()
        switch ($tv) {
            '-encodedcommand' { Add-Unique $ir.risk_flags "invoke_expression" }
            '-enc'            { Add-Unique $ir.risk_flags "invoke_expression" }
            '-en'             { Add-Unique $ir.risk_flags "invoke_expression" }
            '--%'             { $ir.has_stop_parsing = $true }
            '&&'              { Add-Unique $ir.operators "&&" }
            '||'              { Add-Unique $ir.operators "||" }
        }
    }

    return $ir
}

function Write-IR {
    param($ir)
    $cmdArgsOut = @{}
    foreach ($k in $ir.command_args.Keys) {
        $cmdArgsOut[$k] = @($ir.command_args[$k])
    }
    $out = [ordered]@{
        version          = $ir.version
        commands         = @($ir.commands)
        operators        = @($ir.operators)
        redirects        = @($ir.redirects)
        expansions       = @($ir.expansions)
        risk_flags       = @($ir.risk_flags)
        parse_errors     = @($ir.parse_errors)
        has_script_block = $ir.has_script_block
        has_assignment   = $ir.has_assignment
        has_background   = $ir.has_background
        has_stop_parsing = $ir.has_stop_parsing
        has_control_flow = $ir.has_control_flow
        command_args     = $cmdArgsOut
    }
    [Console]::Out.WriteLine(($out | ConvertTo-Json -Compress -Depth 3))
    [Console]::Out.Flush()
}

# --- Main persistent loop ---
while ($true) {
    $line = $null
    try {
        $line = [Console]::In.ReadLine()
    } catch {
        break
    }
    if ($null -eq $line) { break }
    $line = $line.Trim()
    if ($line -eq '') { continue }

    $ir = $null
    try {
        $req = ConvertFrom-Json $line
        $ir = Invoke-Parse $req.cmd
    } catch {
        $ir = New-IR
        Add-Unique $ir.risk_flags "syntax_error"
        $ir.parse_errors.Add("request error: $_") | Out-Null
    }

    Write-IR $ir
}
```

- [ ] **Step 2: Create `internal/approval/ps_bridge.go`**

This file manages the persistent pwsh bridge process. It embeds the PS script, starts the process, handles the JSON line protocol, and provides fail-closed error handling.

```go
package approval

import (
	"bufio"
	"bytes"
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
	Version         string              `json:"version"`
	Commands        []string            `json:"commands"`
	Operators       []string            `json:"operators"`
	Redirects       []string            `json:"redirects"`
	Expansions      []string            `json:"expansions"`
	RiskFlags       []string            `json:"risk_flags"`
	ParseErrors     []string            `json:"parse_errors"`
	HasScriptBlock  bool                `json:"has_script_block"`
	HasAssignment   bool                `json:"has_assignment"`
	HasBackground   bool                `json:"has_background"`
	HasStopParsing  bool                `json:"has_stop_parsing"`
	HasControlFlow  bool                `json:"has_control_flow"`
	CommandArgs     map[string][]string `json:"command_args"`
}

// ---- bridge process management ----

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
```

- [ ] **Step 3: Create `internal/approval/ps_bridge_test.go`**

This test file has a `//go:build windows` tag because it requires a running pwsh.exe. It tests the bridge process directly.

```go
//go:build windows

package approval

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseWithBridgeSimpleCommand(t *testing.T) {
	t.Cleanup(func() { CloseBridge() })

	ir, err := parseWithBridge("Get-ChildItem")
	require.NoError(t, err)
	require.NotNil(t, ir)
	require.Contains(t, ir.Commands, "get-childitem")
	require.False(t, ir.HasScriptBlock)
	require.False(t, ir.HasAssignment)
	require.Empty(t, ir.Redirects)
}

func TestParseWithBridgePipeline(t *testing.T) {
	t.Cleanup(func() { CloseBridge() })

	ir, err := parseWithBridge("Get-ChildItem | Sort-Object Name")
	require.NoError(t, err)
	require.NotNil(t, ir)
	require.Contains(t, ir.Commands, "get-childitem")
	require.Contains(t, ir.Commands, "sort-object")
	require.Contains(t, ir.Operators, "|")
}

func TestParseWithBridgeScriptBlock(t *testing.T) {
	t.Cleanup(func() { CloseBridge() })

	ir, err := parseWithBridge("Where-Object { $_.Name -eq 'foo' }")
	require.NoError(t, err)
	require.NotNil(t, ir)
	require.True(t, ir.HasScriptBlock)
}

func TestParseWithBridgeRedirect(t *testing.T) {
	t.Cleanup(func() { CloseBridge() })

	ir, err := parseWithBridge("Get-Process > out.txt")
	require.NoError(t, err)
	require.NotNil(t, ir)
	require.Contains(t, ir.Redirects, "FileRedirection")
}

func TestParseWithBridgeSubexpression(t *testing.T) {
	t.Cleanup(func() { CloseBridge() })

	ir, err := parseWithBridge("Get-Content $(Get-ChildItem)")
	require.NoError(t, err)
	require.NotNil(t, ir)
	require.Contains(t, ir.Expansions, "subshell")
}

func TestParseWithBridgeVariableExclusion(t *testing.T) {
	t.Cleanup(func() { CloseBridge() })

	// $true, $_, $null should NOT be flagged as var expansions
	ir, err := parseWithBridge("Write-Output $true")
	require.NoError(t, err)
	require.NotNil(t, ir)
	require.NotContains(t, ir.Expansions, "var")
}

func TestParseWithBridgeVariableFlagged(t *testing.T) {
	t.Cleanup(func() { CloseBridge() })

	ir, err := parseWithBridge("Get-Content $env:SECRET")
	require.NoError(t, err)
	require.NotNil(t, ir)
	require.Contains(t, ir.Expansions, "var")
}

func TestParseWithBridgeAssignment(t *testing.T) {
	t.Cleanup(func() { CloseBridge() })

	ir, err := parseWithBridge("$x = Get-Date")
	require.NoError(t, err)
	require.NotNil(t, ir)
	require.True(t, ir.HasAssignment)
}

func TestParseWithBridgeControlFlow(t *testing.T) {
	t.Cleanup(func() { CloseBridge() })

	ir, err := parseWithBridge("if ($true) { Get-Date }")
	require.NoError(t, err)
	require.NotNil(t, ir)
	require.True(t, ir.HasControlFlow)
}

func TestParseWithBridgeSemicolon(t *testing.T) {
	t.Cleanup(func() { CloseBridge() })

	ir, err := parseWithBridge("Get-Date; Get-Process")
	require.NoError(t, err)
	require.NotNil(t, ir)
	require.Contains(t, ir.Operators, ";")
}

func TestParseWithBridgeCloseBridgeCleanup(t *testing.T) {
	// Start a bridge, close it, then start a new one
	_, err := parseWithBridge("Get-Date")
	require.NoError(t, err)
	CloseBridge()
	// After close, a new bridge should start
	_, err = parseWithBridge("Get-Date")
	require.NoError(t, err)
	CloseBridge()
}
```

- [ ] **Step 4: Run bridge tests on Windows**

Run: `go test ./internal/approval -run TestParseWithBridge -count=1 -tags windows`
Expected: PASS (all bridge tests pass)

If pwsh.exe is not found, the tests will fail with "pwsh not available" — ensure `pwsh.exe` or `powershell.exe` is in PATH.

- [ ] **Step 5: Run build check**

Run: `go run github.com/go-task/task/v3/cmd/task@v3.51.1 check`
Expected: PASS (`go build ./...` succeeds on all platforms — the `//go:embed` works cross-platform, the test file has a build tag)

- [ ] **Step 6: Commit**

```bash
git add internal/approval/ps_bridge.ps1 internal/approval/ps_bridge.go internal/approval/ps_bridge_test.go
git commit -m "feat(approval): add persistent pwsh AST bridge for PowerShell parsing"
```

---

### Task 2: 只读分类器 `readonly_ps.go`

**Files:**
- Create: `internal/approval/readonly_ps.go`
- Test: `internal/approval/readonly_ps_test.go`

**Interfaces:**
- Consumes: `parseWithBridge(command string) (*psBridgeIR, error)` from Task 1
- Produces: `IsReadOnlyPowerShell(command string) (bool, string)` — returns `(true, reason)` when read-only; `(false, "")` when not or inconclusive

- [ ] **Step 1: Create `internal/approval/readonly_ps.go`**

```go
package approval

import (
	"strings"
)

// IsReadOnlyPowerShell analyzes a PowerShell command using a persistent
// pwsh.exe bridge process that calls System.Management.Automation.Language.Parser.
// Returns (true, reason) when read-only; (false, "") when not or inconclusive
// (fail-closed — caller degrades to LLM classifier).
func IsReadOnlyPowerShell(command string) (bool, string) {
	command = strings.TrimSpace(command)
	if command == "" {
		return false, ""
	}

	ir, err := parseWithBridge(command)
	if err != nil {
		return false, ""
	}

	// Parse errors → fail-closed
	if len(ir.ParseErrors) > 0 {
		return false, ""
	}

	// Security flags — any hit means not read-only
	if ir.HasScriptBlock {
		return false, ""
	}
	if ir.HasAssignment {
		return false, ""
	}
	if ir.HasControlFlow {
		return false, ""
	}
	if ir.HasBackground {
		return false, ""
	}
	if ir.HasStopParsing {
		return false, ""
	}

	// File redirection → writes to filesystem
	for _, r := range ir.Redirects {
		if r == "FileRedirection" {
			return false, ""
		}
	}

	// Subexpression $(...) → can execute arbitrary code
	for _, e := range ir.Expansions {
		if e == "subshell" {
			return false, ""
		}
	}

	// Variable args → may leak secrets via error messages
	for _, e := range ir.Expansions {
		if e == "var" {
			return false, ""
		}
	}

	// Encoded command → hides intent
	for _, rf := range ir.RiskFlags {
		if rf == "invoke_expression" {
			return false, ""
		}
	}

	// Compound operators → all parts must be read-only, but we check
	// each command individually below. Semicolons and &&/|| are compound
	// but the bridge already splits them into separate commands, so we
	// just need all commands to be in the allowlist.
	// Note: we do NOT reject ; or &&/|| outright — if all commands are
	// read-only, the compound is read-only. This matches POSIX behavior.

	// No commands → fail-closed (empty or expression-only)
	if len(ir.Commands) == 0 {
		return false, ""
	}

	// All commands must be in the read-only allowlist
	for _, cmd := range ir.Commands {
		if !readOnlyPowerShellCmdlets[cmd] {
			return false, ""
		}
	}

	return true, "read-only PowerShell command (AST analysis)"
}

// readOnlyPowerShellCmdlets is the allowlist of PowerShell cmdlets and
// aliases that are always read-only. All names are lowercase.
var readOnlyPowerShellCmdlets = map[string]bool{
	// Filesystem reads
	"get-childitem": true, "gci": true, "ls": true, "dir": true,
	"get-content": true, "gc": true, "cat": true, "type": true,
	"get-item": true, "gi": true,
	"get-itemproperty": true,
	"get-itempropertyvalue": true,
	"test-path": true,
	"resolve-path": true,
	"get-filehash": true,
	"get-acl": true,
	"select-string": true,
	"get-location": true, "gl": true, "pwd": true,
	"get-psdrive": true,
	"get-psprovider": true,
	"convert-path": true,
	"join-path": true,
	"split-path": true,

	// Object inspection / transforms (pure)
	"get-member": true, "gm": true,
	"get-unique": true, "gu": true,
	"compare-object": true, "compare": true,
	"join-string": true,
	"get-random": true,
	"convertto-json": true,
	"convertfrom-json": true,
	"convertto-csv": true,
	"convertfrom-csv": true,
	"convertto-xml": true,
	"convertto-html": true,
	"format-hex": true,

	// Pipeline transformers
	"select-object": true, "select": true,
	"sort-object": true, "sort": true,
	"group-object": true, "group": true,
	"where-object": true, "?": true, "where": true,
	"measure-object": true, "measure": true,
	"format-table": true, "ft": true,
	"format-list": true, "fl": true,
	"format-wide": true, "fw": true,
	"format-custom": true, "fc": true,
	"out-string": true,
	"out-host": true,
	"out-null": true,

	// Output
	"write-output": true, "write": true, "echo": true,
	"write-host": true,

	// System info
	"get-process": true, "gps": true, "ps": true,
	"get-service": true, "gsv": true,
	"get-computerinfo": true,
	"get-host": true,
	"get-date": true, "date": true,
	"get-hotfix": true,
	"get-timezone": true,
	"get-uptime": true,
	"get-culture": true,
	"get-uiculture": true,
	"get-alias": true, "gal": true,
	"get-history": true, "h": true, "history": true,

	// Other
	"start-sleep": true, "sleep": true,
}
```

- [ ] **Step 2: Create `internal/approval/readonly_ps_test.go`**

This test file has a `//go:build windows` tag. It tests the full `IsReadOnlyPowerShell` classifier.

```go
//go:build windows

package approval

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsReadOnlyPowerShellReadOnly(t *testing.T) {
	t.Cleanup(func() { CloseBridge() })

	cases := []struct {
		name string
		cmd  string
	}{
		// Simple cmdlets
		{"get-childitem", "Get-ChildItem"},
		{"get-content", "Get-Content file.txt"},
		{"test-path", "Test-Path x"},
		{"get-process", "Get-Process"},
		{"get-date", "Get-Date"},
		{"get-location", "Get-Location"},

		// With parameters
		{"get-childitem params", "Get-ChildItem -Path C:\\Users -Recurse"},
		{"get-content params", "Get-Content -Path file.txt -TotalCount 10"},

		// Pipelines (all read-only)
		{"pipe sort", "Get-ChildItem | Sort-Object Name"},
		{"pipe select", "Get-Process | Select-Object Name,Id"},
		{"pipe measure", "Get-ChildItem | Measure-Object"},
		{"pipe where simplified", "Get-ChildItem | Where-Object Name -eq 'foo'"},

		// Aliases
		{"alias gci", "gci"},
		{"alias ls", "ls"},
		{"alias cat", "cat file.txt"},
		{"alias gps select", "gps | select Name"},

		// Multiple read-only commands with ;
		{"semicolon", "Get-Date; Get-Process"},

		// Out-Null (discard)
		{"out-null", "Get-Process | Out-Null"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := IsReadOnlyPowerShell(c.cmd)
			require.Truef(t, got, "cmd=%q should be read-only", c.cmd)
		})
	}
}

func TestIsReadOnlyPowerShellNotReadOnly(t *testing.T) {
	t.Cleanup(func() { CloseBridge() })

	cases := []struct {
		name string
		cmd  string
	}{
		// Script blocks
		{"script block where", "Where-Object { $_.Name -eq 'foo' }"},
		{"script block foreach", "ForEach-Object { $_ }"},

		// Subexpressions
		{"subexpression", "$(Get-Date)"},
		{"subexpression in arg", "Get-Content $(Get-ChildItem)"},

		// Variable args
		{"variable arg", "Get-Content $env:SECRET"},
		{"variable arg 2", "Get-Process $var"},

		// Redirections
		{"redirect out", "Get-Process > out.txt"},
		{"redirect append", "Get-Date >> log.txt"},

		// Assignment
		{"assignment", "$x = Get-Date"},

		// Control flow
		{"if", "if ($true) { Get-Date }"},
		{"foreach", "foreach ($f in $files) { Get-Content $f }"},

		// Background
		{"background", "Get-Process &"},

		// Write cmdlets
		{"set-content", "Set-Content file.txt 'hello'"},
		{"remove-item", "Remove-Item file.txt"},
		{"new-item", "New-Item -Path x"},

		// Excluded cmdlets (security traps)
		{"get-command", "Get-Command"},
		{"get-help", "Get-Help"},
		{"select-xml", "Select-Xml"},

		// Stop parsing
		{"stop parsing", "git --% --foo"},

		// Empty
		{"empty", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := IsReadOnlyPowerShell(c.cmd)
			require.Falsef(t, got, "cmd=%q should NOT be read-only", c.cmd)
		})
	}
}
```

- [ ] **Step 3: Run classifier tests on Windows**

Run: `go test ./internal/approval -run TestIsReadOnlyPowerShell -count=1 -tags windows`
Expected: PASS

- [ ] **Step 4: Run full approval package tests**

Run: `go test ./internal/approval -count=1`
Expected: PASS (no regressions in existing tests; Windows-only tests skipped on non-Windows)

- [ ] **Step 5: Commit**

```bash
git add internal/approval/readonly_ps.go internal/approval/readonly_ps_test.go
git commit -m "feat(approval): add PowerShell read-only classifier with cmdlet allowlist"
```

---

### Task 3: 集成到 `internal/app/shell_classify.go`

**Files:**
- Modify: `internal/app/shell_classify.go:46-59` (the tier 1 section)
- Test: `internal/app/shell_classify_test.go`

**Interfaces:**
- Consumes: `IsReadOnlyPowerShell(command string) (bool, string)` from Task 2
- Produces: modified `analyzeShellCommand` that calls `IsReadOnlyPowerShell` for `powershell_run`

- [ ] **Step 1: Add integration test cases to `internal/app/shell_classify_test.go`**

Append these test functions to the existing test file. These tests use the `shellAnalyzer` test seam to verify that read-only PowerShell commands don't reach the LLM.

```go
func TestAnalyzeShellCommandPowerShellReadOnly(t *testing.T) {
	// These PowerShell commands should be caught by the AST classifier
	// and never reach the LLM (shellAnalyzer test seam).
	cases := []struct {
		name string
		cmd  string
	}{
		{"get-childitem", "Get-ChildItem"},
		{"get-content", "Get-Content file.txt"},
		{"test-path", "Test-Path x"},
		{"get-process", "Get-Process"},
		{"pipe sort", "Get-ChildItem | Sort-Object Name"},
		{"alias gci", "gci"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mods := &Mods{
				shellAnalyzer: func(tool, command string) shellCommandAnalysis {
					t.Fatalf("LLM classifier should not be called for %q", command)
					return defaultShellCommandAnalysis()
				},
			}
			t.Cleanup(func() { mods.shellAnalyzer = nil })
			result := mods.analyzeShellCommand("powershell_run", c.cmd)
			require.Falsef(t, result.NeedsReview, "cmd=%q should be read-only", c.cmd)
		})
	}
}

func TestAnalyzeShellCommandPowerShellWriteGoesToLLM(t *testing.T) {
	// Write PowerShell commands should fall through to the LLM (test seam).
	called := false
	mods := &Mods{
		shellAnalyzer: func(tool, command string) shellCommandAnalysis {
			called = true
			return shellCommandAnalysis{NeedsReview: true, Reason: "write command"}
		},
	}
	t.Cleanup(func() { mods.shellAnalyzer = nil })
	result := mods.analyzeShellCommand("powershell_run", "Set-Content file.txt 'hello'")
	require.True(t, result.NeedsReview, "Set-Content should require review")
	require.True(t, called, "LLM classifier should be called for write commands")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/app -run TestAnalyzeShellCommandPowerShell -count=1`
Expected: FAIL — the PowerShell AST classifier is not yet integrated, so `shellAnalyzer` gets called and the test's `t.Fatalf` triggers.

- [ ] **Step 3: Modify `analyzeShellCommand` in `internal/app/shell_classify.go`**

Replace the tier 1 section (lines 46-59). The old code:

```go
	// Tier 1: AST-based read-only classifier (POSIX only, skip powershell_run).
	// Handles pipes, &&/||, subshells, command substitution, and subcommand
	// tables. Covers both workspace-local and external-path read-only commands;
	// the approval matrix decides whether external reads need review.
	if tool != "powershell_run" {
		if ro, reason := approval.IsReadOnlyPOSIX(command); ro {
			debug.Printf("analyzeShellCommand: cmd=%q -> AST: read-only", debug.Truncate(command, 80))
			return shellCommandAnalysis{
				NeedsReview:  false,
				AffectedDirs: externalPaths,
				Reason:       reason,
			}
		}
	}
```

Replace with:

```go
	// Tier 1: AST-based read-only classifier.
	// POSIX: handles pipes, &&/||, subshells, command substitution, subcommand tables.
	// PowerShell: uses a persistent pwsh.exe bridge to call Parser::ParseInput.
	// Both cover workspace-local and external-path read-only commands;
	// the approval matrix decides whether external reads need review.
	if tool == "powershell_run" {
		if ro, reason := approval.IsReadOnlyPowerShell(command); ro {
			debug.Printf("analyzeShellCommand: cmd=%q -> PS AST: read-only", debug.Truncate(command, 80))
			return shellCommandAnalysis{
				NeedsReview:  false,
				AffectedDirs: externalPaths,
				Reason:       reason,
			}
		}
	} else {
		if ro, reason := approval.IsReadOnlyPOSIX(command); ro {
			debug.Printf("analyzeShellCommand: cmd=%q -> AST: read-only", debug.Truncate(command, 80))
			return shellCommandAnalysis{
				NeedsReview:  false,
				AffectedDirs: externalPaths,
				Reason:       reason,
			}
		}
	}
```

- [ ] **Step 4: Run integration tests to verify they pass**

Run: `go test ./internal/app -run TestAnalyzeShellCommandPowerShell -count=1`
Expected: PASS

- [ ] **Step 5: Run full app package tests (non-interactive ones)**

Run: `go test ./internal/app -run "TestIsSimpleReadOnly|TestExtractExternalPaths|TestMentionsExternalPath|TestExpandHomeVars|TestAnalyzeShellCommandAST|TestAnalyzeShellCommandPowerShell|TestReviewPolicyNonTTY" -count=1`
Expected: PASS (no regressions)

- [ ] **Step 6: Run build check**

Run: `go run github.com/go-task/task/v3/cmd/task@v3.51.1 check`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/app/shell_classify.go internal/app/shell_classify_test.go
git commit -m "feat(app): integrate PowerShell AST classifier for powershell_run"
```
