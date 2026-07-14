# PowerShell Static Analysis and Unknown UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Improve PowerShell static read-only classification for narrowly proven-safe script-block pipelines and stop presenting unknown shell effects as file modifications.

**Architecture:** Keep the existing PowerShell parser bridge as the source of AST facts, but enrich its IR with assignment, variable, method, and static-member context. The Go classifier will allow only bounded safe script-block shapes and reject path-hiding or state-mutating expressions. Add a shell effect enum beside the existing `NeedsReview` boolean so approval remains fail-closed while the review UI can display `unknown` separately from definite writes.

**Tech Stack:** Go 1.25, PowerShell `System.Management.Automation.Language.Parser`, existing `internal/approval` static shell classifier, existing app review presentation/summary code.

## Global Constraints

- Do not execute user PowerShell input in the bridge; only parse it.
- Preserve fail-closed behavior: parse errors, bridge errors, unknown commands, unsafe variables, path-hiding assigned variables, redirections, background execution, stop parsing, invoke-expression risks, unsafe method calls, static member access, and control flow must require review.
- Keep approval conservative: unknown effects still map to write-like approval (`AccessWrite`) so they require review unless already allowed by policy.
- Static PowerShell script blocks may be considered read-only only when every invocation is already in the read-only allowlist, assignments are local temporary scalars, variables are allowlisted pipeline variables or locally assigned temporaries, and method calls are restricted to known pure string helpers (`Trim`, `Split`, `ToString`) on expression values.
- Local assigned variables must not be used as command path arguments. If a local variable appears in a command argument position, the command remains unknown unless the bridge can prove and report the concrete path for `AffectedDirs`; this plan does not add value tracking, so reject that shape.
- `$HOME`, `$PWD`, `$env:*`, `$global:*`, `$script:*`, and constructed path expressions such as `Join-Path $HOME ...` are not safe unless the raw text extractor already sees the concrete external path; keep these unknown for this work.
- Keep existing shell analyzer tests source-compatible where possible; zero-value `shellCommandAnalysis` used in tests must not panic.
- Windows-specific parser bridge tests stay behind `//go:build windows`.
- Verification commands: `go test ./internal/approval -count=1`, `go test ./internal/app -run 'TestFormatReviewSummary|TestShellRisk|TestBuildAccessIntent|TestToolReviewer|TestAnalyzeShellCommandPowerShellLineCountPipelineIsReadOnly' -count=1`, `go run github.com/go-task/task/v3/cmd/task@v3.51.1 check`, and `go run github.com/go-task/task/v3/cmd/task@v3.51.1 test`.

---

## File Structure

**Modify:**
- `internal/approval/ps_bridge.ps1` — include variable names, assignment targets, command argument text, method invocation names, and static-member risk facts in the bridge IR; only mark command-containing subexpressions as `subshell`.
- `internal/approval/ps_bridge.go` — add Go fields matching the new bridge IR.
- `internal/approval/readonly_ps.go` — replace blanket script-block rejection with a bounded safe-script-block classifier and explicit unsafe-expression rejection.
- `internal/approval/ps_bridge_test.go` — assert new IR facts for safe script-block commands and unsafe method/static/variable/assignment cases.
- `internal/approval/readonly_ps_test.go` — add acceptance tests for safe script-block pipelines and rejection tests for persistent/path-hiding mutations.
- `internal/app/shell_classify.go` — add shell effect state and set it in static/LLM/default classification paths.
- `internal/app/review.go` — preserve shell effect when `requestApproval` receives a prebuilt `AccessIntent` from `buildAccessIntent`; do this by reusing `deps.analyzeShell` for presentation metadata, not by changing `approval.AccessIntent`.
- `internal/app/review_summary.go` — use shell effect state to display unknown effects separately from mutations.
- `internal/app/review_presentation.go` — use the same risk label/headline behavior for unknown effects.
- `internal/app/shell_classify_test.go`, `internal/app/review_summary_test.go`, `internal/app/approval_rules_test.go`, and/or `internal/app/build_access_intent_test.go` — cover unknown UI behavior and the sample command analysis.

---

### Task 1: Enrich PowerShell Bridge IR With Safety Facts

**Files:**
- Modify: `internal/approval/ps_bridge.ps1`
- Modify: `internal/approval/ps_bridge.go`
- Test: `internal/approval/ps_bridge_test.go`

**Interfaces:**
- Produces new `psBridgeIR` fields:
  - `Variables []string` from JSON `variables`
  - `AssignmentTargets []string` from JSON `assignment_targets`
  - `MethodInvocations []string` from JSON `method_invocations`
  - `StaticMembers []string` from JSON `static_members`
- Preserves existing fields: `Commands`, `Invocations`, `Paths`, `Expansions`, `HasScriptBlock`, `HasAssignment`.

- [ ] **Step 1: Write failing bridge IR tests**

Add these tests to `internal/approval/ps_bridge_test.go`:

```go
func TestParseWithBridgeSafeScriptBlockFacts(t *testing.T) {
	t.Cleanup(func() { CloseBridge() })

	cmd := `Get-ChildItem -Recurse -Filter *.go | ForEach-Object { $lines = (Get-Content $_.FullName | Measure-Object -Line).Lines; "$($_.FullName): $lines lines" } | Sort-Object { [int]($_.Split(':')[1].Trim().Split(' ')[0]) } -Descending`
	ir, err := parseWithBridge(cmd)
	require.NoError(t, err)
	require.NotNil(t, ir)
	require.True(t, ir.HasScriptBlock)
	require.True(t, ir.HasAssignment)
	require.Contains(t, ir.AssignmentTargets, "$lines")
	require.Contains(t, ir.Variables, "_")
	require.Contains(t, ir.Variables, "lines")
	require.Contains(t, ir.Commands, "foreach-object")
	require.Contains(t, ir.Commands, "sort-object")
	require.Contains(t, ir.MethodInvocations, "Split")
	require.Contains(t, ir.MethodInvocations, "Trim")
	require.NotContains(t, ir.Expansions, "subshell", "member-only $() in expandable strings must not be treated like command substitution")
}

func TestParseWithBridgeUnsafeVariableFacts(t *testing.T) {
	t.Cleanup(func() { CloseBridge() })

	ir, err := parseWithBridge(`Get-Content $env:SECRET`)
	require.NoError(t, err)
	require.Contains(t, ir.Variables, "env:SECRET")
	require.Contains(t, ir.Expansions, "var")
}

func TestParseWithBridgeCommandSubexpressionStillFlagged(t *testing.T) {
	t.Cleanup(func() { CloseBridge() })

	ir, err := parseWithBridge(`Get-Content $(Get-ChildItem)`)
	require.NoError(t, err)
	require.Contains(t, ir.Expansions, "subshell")
}

func TestParseWithBridgeUnsafeExpressionFacts(t *testing.T) {
	t.Cleanup(func() { CloseBridge() })

	ir, err := parseWithBridge(`Get-ChildItem | ForEach-Object { $_.Delete() }`)
	require.NoError(t, err)
	require.Contains(t, ir.MethodInvocations, "Delete")

	ir, err = parseWithBridge(`[IO.File]::Delete('owned.txt')`)
	require.NoError(t, err)
	require.NotEmpty(t, ir.StaticMembers)

	ir, err = parseWithBridge(`[Environment]::UserName`)
	require.NoError(t, err)
	require.NotEmpty(t, ir.StaticMembers)
}
```

- [ ] **Step 2: Run tests and confirm failure**

Run: `go test ./internal/approval -run 'TestParseWithBridge(SafeScriptBlockFacts|UnsafeVariableFacts|CommandSubexpressionStillFlagged|UnsafeExpressionFacts)' -count=1`

Expected: compile failure because `Variables`, `AssignmentTargets`, `MethodInvocations`, and `StaticMembers` are not present, or assertion failure because the bridge does not emit them yet.

- [ ] **Step 3: Update bridge script IR shape**

In `internal/approval/ps_bridge.ps1`, update `New-IR` to include:

```powershell
        variables          = [System.Collections.Generic.List[string]]::new()
        assignment_targets = [System.Collections.Generic.List[string]]::new()
        method_invocations = [System.Collections.Generic.List[string]]::new()
        static_members     = [System.Collections.Generic.List[string]]::new()
```

In the `VariableExpressionAst` branch, add every variable path to `variables` before the existing risk logic:

```powershell
        if ($node -is [System.Management.Automation.Language.VariableExpressionAst]) {
            $varNameRaw = $node.VariablePath.UserPath
            $varName = $varNameRaw.ToLower()
            if ($varNameRaw) { Add-Unique $ir.variables $varNameRaw }
            if ($varConstants -notcontains $varName) {
                Add-Unique $ir.expansions "var"
            }
            continue
        }
```

In the `AssignmentStatementAst` branch, record the assignment target:

```powershell
        if ($node -is [System.Management.Automation.Language.AssignmentStatementAst]) {
            $ir.has_assignment = $true
            try {
                $target = $node.Left.ToString().Trim()
                if ($target) { Add-Unique $ir.assignment_targets $target }
            } catch {}
            continue
        }
```

Add an `InvokeMemberExpressionAst` branch before the generic control-flow branch. It records method/member names and flags static member access by checking for `::` in the expression text:

```powershell
        if ($node -is [System.Management.Automation.Language.InvokeMemberExpressionAst]) {
            try {
                $memberName = $node.Member.ToString().Trim().Trim('"').Trim("'")
                if ($memberName) { Add-Unique $ir.method_invocations $memberName }
            } catch {}
            try {
                $exprText = $node.ToString()
                if ($exprText -like '*::*') { Add-Unique $ir.static_members $exprText }
            } catch {}
            continue
        }
```

Add a `MemberExpressionAst` branch immediately after it so static property/field access is also flagged:

```powershell
        if ($node -is [System.Management.Automation.Language.MemberExpressionAst]) {
            try {
                $exprText = $node.ToString()
                if ($exprText -like '*::*') { Add-Unique $ir.static_members $exprText }
            } catch {}
            continue
        }
```

Change the `SubExpressionAst` branch so pure member/expression interpolation does not add `subshell`; only subexpressions that contain a command do:

```powershell
        if ($node -is [System.Management.Automation.Language.SubExpressionAst]) {
            $hasCommand = $false
            try {
                $matches = $node.SubExpression.FindAll({ param($n) $n -is [System.Management.Automation.Language.CommandAst] }, $true)
                $hasCommand = ($matches -and $matches.Count -gt 0)
            } catch {
                $hasCommand = $true
            }
            if ($hasCommand) { Add-Unique $ir.expansions "subshell" }
            continue
        }
```

In `Write-IR`, add the arrays:

```powershell
        variables          = @($ir.variables)
        assignment_targets = @($ir.assignment_targets)
        method_invocations = @($ir.method_invocations)
        static_members     = @($ir.static_members)
```

- [ ] **Step 4: Update Go IR struct**

In `internal/approval/ps_bridge.go`, extend `psBridgeIR`:

```go
type psBridgeIR struct {
	Version           string                `json:"version"`
	Commands          []string              `json:"commands"`
	Operators         []string              `json:"operators"`
	Redirects         []string              `json:"redirects"`
	Expansions        []string              `json:"expansions"`
	RiskFlags         []string              `json:"risk_flags"`
	ParseErrors       []string              `json:"parse_errors"`
	HasScriptBlock    bool                  `json:"has_script_block"`
	HasAssignment     bool                  `json:"has_assignment"`
	HasBackground     bool                  `json:"has_background"`
	HasStopParsing    bool                  `json:"has_stop_parsing"`
	HasControlFlow    bool                  `json:"has_control_flow"`
	CommandArgs       map[string][]string   `json:"command_args"`
	Paths             []string              `json:"paths"`
	Variables         []string              `json:"variables"`
	AssignmentTargets []string              `json:"assignment_targets"`
	MethodInvocations []string              `json:"method_invocations"`
	StaticMembers     []string              `json:"static_members"`
	Invocations       []psCommandInvocation `json:"command_invocations"`
}
```

- [ ] **Step 5: Run bridge tests**

Run: `go test ./internal/approval -run 'TestParseWithBridge(SafeScriptBlockFacts|UnsafeVariableFacts|CommandSubexpressionStillFlagged|UnsafeExpressionFacts)' -count=1`

Expected: PASS on Windows. On non-Windows, these tests are skipped by build tags.

---

### Task 2: Allow Only Bounded Safe PowerShell Script-Block Pipelines

**Files:**
- Modify: `internal/approval/readonly_ps.go`
- Test: `internal/approval/readonly_ps_test.go`

**Interfaces:**
- Consumes `psBridgeIR.Variables`, `AssignmentTargets`, `MethodInvocations`, and `StaticMembers` from Task 1.
- Produces helper functions:
  - `safePowerShellAssignments(ir *psBridgeIR) bool`
  - `safePowerShellVariables(ir *psBridgeIR) bool`
  - `safePowerShellMethods(ir *psBridgeIR) bool`
  - `powerShellCommandArgsContainUnsafeVariable(ir *psBridgeIR) bool`
  - `normalizePowerShellVariableName(name string) string`

- [ ] **Step 1: Write failing read-only tests**

Update `TestIsReadOnlyPowerShellReadOnly` in `internal/approval/readonly_ps_test.go` by adding these cases:

```go
{"foreach local line count", `Get-ChildItem -Recurse -Filter *.go | Select-Object FullName | ForEach-Object { $lines = (Get-Content $_.FullName | Measure-Object -Line).Lines; "$($_.FullName): $lines lines" } | Sort-Object { [int]($_.Split(':')[1].Trim().Split(' ')[0]) } -Descending`},
{"foreach read content", `Get-ChildItem -Filter *.go | ForEach-Object { Get-Content $_.FullName | Measure-Object -Line }`},
{"where script block predicate", `Get-ChildItem | Where-Object { $_.Name -like '*.go' }`},
{"sort script block expression", `Get-ChildItem | Sort-Object { $_.Name.Trim().Split('.')[0] }`},
```

Update `TestIsReadOnlyPowerShellNotReadOnly` by replacing the old blanket script-block rejections with unsafe script-block cases:

```go
{"script block writer", `Get-ChildItem | ForEach-Object { Remove-Item $_.FullName }`},
{"script block env assignment", `Get-ChildItem | ForEach-Object { $env:MODS_TEST = $_.FullName }`},
{"script block unassigned variable path", `Get-ChildItem | ForEach-Object { Get-Content $path }`},
{"script block assigned variable path", `Get-ChildItem | ForEach-Object { $p = 'C:\Users\Public\secret.txt'; Get-Content $p }`},
{"script block braced assigned variable path", `Get-ChildItem | ForEach-Object { $p = 'C:\Users\Public\secret.txt'; Get-Content ${p} }`},
{"script block unsafe instance method", `Get-ChildItem | ForEach-Object { $_.Delete() }`},
{"static method mutation", `[IO.File]::Delete('owned.txt')`},
{"static property access", `[Environment]::UserName`},
{"join path home", `Get-ChildItem | ForEach-Object { Get-Content (Join-Path $HOME '.ssh') }`},
{"command subexpression path", `Get-Content $(Get-ChildItem)`},
```

Keep the existing redirection, background, stop parsing, write cmdlet, and external-helper cases.

- [ ] **Step 2: Run tests and confirm failure**

Run: `go test ./internal/approval -run 'TestIsReadOnlyPowerShell(ReadOnly|NotReadOnly)' -count=1`

Expected: the new safe script-block cases fail because `HasScriptBlock`, `HasAssignment`, or variable expansions still reject them.

- [ ] **Step 3: Add bounded safety helpers**

In `internal/approval/readonly_ps.go`, add imports:

```go
import (
	"regexp"
	"strings"
)
```

Add helpers near `trimPowerShellLiteral`:

```go
var simplePowerShellLocalVar = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var safePipelinePowerShellVariables = map[string]bool{
	"_": true, "psitem": true, "true": true, "false": true, "null": true,
	"args": true, "input": true,
}

var purePowerShellMethodNames = map[string]bool{
	"trim": true,
	"split": true,
	"tostring": true,
}

func normalizePowerShellVariableName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "$")
	return strings.ToLower(name)
}

func assignedPowerShellLocals(ir *psBridgeIR) map[string]bool {
	assigned := map[string]bool{}
	for _, target := range ir.AssignmentTargets {
		name := normalizePowerShellVariableName(target)
		if simplePowerShellLocalVar.MatchString(name) {
			assigned[name] = true
		}
	}
	return assigned
}

func safePowerShellAssignments(ir *psBridgeIR) bool {
	if !ir.HasAssignment {
		return true
	}
	if !ir.HasScriptBlock || len(ir.AssignmentTargets) == 0 {
		return false
	}
	for _, target := range ir.AssignmentTargets {
		name := normalizePowerShellVariableName(target)
		if strings.Contains(name, ":") || !simplePowerShellLocalVar.MatchString(name) {
			return false
		}
	}
	return true
}

func safePowerShellVariables(ir *psBridgeIR) bool {
	assigned := assignedPowerShellLocals(ir)
	for _, variable := range ir.Variables {
		name := normalizePowerShellVariableName(variable)
		if safePipelinePowerShellVariables[name] || assigned[name] {
			continue
		}
		return false
	}
	return true
}

func safePowerShellMethods(ir *psBridgeIR) bool {
	if len(ir.StaticMembers) > 0 {
		return false
	}
	for _, method := range ir.MethodInvocations {
		if !purePowerShellMethodNames[strings.ToLower(strings.TrimSpace(method))] {
			return false
		}
	}
	return true
}

func hasPowerShellExpansion(ir *psBridgeIR, expansion string) bool {
	for _, e := range ir.Expansions {
		if e == expansion {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Reject local variables used as command path arguments**

Add a helper that scans invocation args and rejects assigned locals when they appear in command arguments. Exclude the script-block argument text for transformer cmdlets because those blocks are checked separately by command, variable, assignment, and method rules.

```go
func powerShellCommandArgsContainUnsafeVariable(ir *psBridgeIR) bool {
	assigned := assignedPowerShellLocals(ir)
	if len(assigned) == 0 {
		return false
	}
	for _, inv := range ir.Invocations {
		name := normalizePowerShellCommandName(inv.Name)
		for _, arg := range inv.Args {
			trimmed := strings.TrimSpace(arg)
			if (name == "foreach-object" || name == "where-object" || name == "sort-object") && strings.HasPrefix(trimmed, "{") {
				continue
			}
			for local := range assigned {
				lower := strings.ToLower(trimmed)
				if strings.Contains(lower, "$"+local) || strings.Contains(lower, "${"+local+"}") {
					return true
				}
			}
		}
	}
	return false
}
```

This intentionally rejects `$p` in `Get-Content $p` because this plan does not add value tracking for `AffectedDirs`.

- [ ] **Step 5: Replace blanket rejection in `IsReadOnlyPowerShell`**

In `IsReadOnlyPowerShell`, remove these blanket blocks:

```go
if ir.HasScriptBlock {
	return false, "", nil
}
if ir.HasAssignment {
	return false, "", nil
}
```

Replace the subexpression and variable expansion loops with:

```go
if hasPowerShellExpansion(ir, "subshell") {
	return false, "", nil
}
if !safePowerShellAssignments(ir) {
	return false, "", nil
}
if !safePowerShellVariables(ir) {
	return false, "", nil
}
if !safePowerShellMethods(ir) {
	return false, "", nil
}
if powerShellCommandArgsContainUnsafeVariable(ir) {
	return false, "", nil
}
```

Leave these fail-closed checks unchanged: parse errors, control flow, background, stop parsing, file redirection, invoke-expression risk flags, empty command list, and per-invocation allowlist checks.

- [ ] **Step 6: Run read-only tests**

Run: `go test ./internal/approval -run 'TestIsReadOnlyPowerShell(ReadOnly|NotReadOnly)' -count=1`

Expected: PASS.

---

### Task 3: Add Shell Effect State for Unknown vs Write

**Files:**
- Modify: `internal/app/shell_classify.go`
- Test: `internal/app/shell_classify_test.go`

**Interfaces:**
- Produces `shellEffect` constants:
  - `shellEffectUnknown`
  - `shellEffectRead`
  - `shellEffectWrite`
- Extends `shellCommandAnalysis` with `Effect shellEffect`.
- Preserves `NeedsReview bool` for approval compatibility.

- [ ] **Step 1: Add shell effect tests**

Add these tests to `internal/app/shell_classify_test.go`:

```go
func TestDefaultShellCommandAnalysisIsUnknown(t *testing.T) {
	got := defaultShellCommandAnalysis()
	require.True(t, got.NeedsReview)
	require.Equal(t, shellEffectUnknown, got.Effect)
}

func TestShellStaticAnalysisSetsEffect(t *testing.T) {
	m := &Mods{Config: testConfigForWorkspace(t.TempDir())}

	read := m.analyzeShellCommand("shell_run", "git status")
	require.False(t, read.NeedsReview)
	require.Equal(t, shellEffectRead, read.Effect)

	write := m.analyzeShellCommand("shell_run", "cat > out.txt <<'EOF'\nhello\nEOF")
	require.True(t, write.NeedsReview)
	require.Equal(t, shellEffectWrite, write.Effect)
}

func TestParseShellAnalysisResponseCanReturnUnknownEffect(t *testing.T) {
	analysis, ok := parseShellAnalysisResponse(`{"needs_review":true,"affected_dirs":[],"reason":"not sure","effect":"unknown"}`)
	require.True(t, ok)
	require.True(t, analysis.NeedsReview)
	require.Equal(t, shellEffectUnknown, analysis.Effect)
}
```

- [ ] **Step 2: Run tests and confirm failure**

Run: `go test ./internal/app -run 'Test(DefaultShellCommandAnalysisIsUnknown|ShellStaticAnalysisSetsEffect|ParseShellAnalysisResponseCanReturnUnknownEffect)' -count=1`

Expected: compile failure because `shellEffect` and `Effect` do not exist, or parse failure because `effect` is ignored.

- [ ] **Step 3: Add shell effect enum and field**

In `internal/app/shell_classify.go`, near `shellCommandAnalysis`, add:

```go
type shellEffect string

const (
	shellEffectUnknown shellEffect = "unknown"
	shellEffectRead    shellEffect = "read"
	shellEffectWrite   shellEffect = "write"
)
```

Update the struct and default:

```go
type shellCommandAnalysis struct {
	NeedsReview  bool
	AffectedDirs []string
	Reason       string
	Effect       shellEffect
}

func defaultShellCommandAnalysis() shellCommandAnalysis {
	return shellCommandAnalysis{NeedsReview: true, Effect: shellEffectUnknown, Reason: "unknown shell effects"}
}
```

- [ ] **Step 4: Parse and normalize effect**

Update `parseShellAnalysisResponse` to accept optional JSON field `effect`. Validate values strictly: `"read"`, `"write"`, `"unknown"`, or empty. Preserve compatibility with old classifier responses that omit the field.

Add a helper:

```go
func normalizeShellEffect(a shellCommandAnalysis) shellCommandAnalysis {
	if a.Effect != "" {
		return a
	}
	if a.NeedsReview {
		a.Effect = shellEffectWrite
	} else {
		a.Effect = shellEffectRead
	}
	return a
}
```

Call it in `finalizeShellAnalysis` before merging dirs:

```go
result = normalizeShellEffect(result)
```

Important: keep `defaultShellCommandAnalysis()` returning `Effect: shellEffectUnknown`; because it already has a non-empty effect, `normalizeShellEffect` must not convert it to write.

- [ ] **Step 5: Set effect in static paths**

In `analyzeShellCommand`, set effect in static read and write results:

```go
NeedsReview:  false,
AffectedDirs: affected,
Reason:       static.Reason,
Effect:       shellEffectRead,
```

```go
NeedsReview:  true,
AffectedDirs: static.AffectedDirs,
Reason:       static.Reason,
Effect:       shellEffectWrite,
```

- [ ] **Step 6: Run shell classifier tests**

Run: `go test ./internal/app -run 'Test(DefaultShellCommandAnalysisIsUnknown|ShellStaticAnalysisSetsEffect|ParseShellAnalysisResponseCanReturnUnknownEffect|ShellAnalysisParsing)' -count=1`

Expected: PASS.

---

### Task 4: Preserve Unknown Effect Through Real Review Flow

**Files:**
- Modify: `internal/app/review.go`
- Test: `internal/app/build_access_intent_test.go`
- Test: `internal/app/approval_rules_test.go`

**Interfaces:**
- Consumes `shellCommandAnalysis.Effect` from Task 3.
- Produces this behavior: when `requestApproval` receives a prebuilt shell `AccessIntent`, it still calls `deps.analyzeShell` to populate `shellCommandAnalysis` for summary/presentation metadata. The prebuilt `AccessIntent` continues to control the approval decision.
- Approval decisions must remain based on `AccessClass` and directories, not effect presentation metadata.

- [ ] **Step 1: Write failing end-to-end review test**

Add this test to `internal/app/approval_rules_test.go`:

```go
func TestShellUnknownEffectPresentationSurvivesPrebuiltAccessIntent(t *testing.T) {
	workspaceScope := testShellWorkspaceScope(t)
	registry := testReviewRegistry(t)
	mods := &Mods{
		ctx:                 context.Background(),
		Config:              testConfigForWorkspace(workspaceScope.Value),
		currentToolRegistry: registry,
		shellAnalyzer: func(string, string) shellCommandAnalysis {
			return shellCommandAnalysis{
				NeedsReview:  true,
				AffectedDirs: []string{workspaceScope.Value},
				Reason:       "classifier could not prove read-only",
				Effect:       shellEffectUnknown,
			}
		},
	}
	reviewer := &toolReviewer{
		reviewMode: ReviewAuto,
		scope:      workspaceScope,
		reviewChan: make(chan toolReviewItem, 1),
	}
	data := []byte(`{"command":"opaque-command"}`)
	intent := buildAccessIntent("shell_run", data, registry, mods.analyzeShellCommand)

	errCh := make(chan error, 1)
	go func() {
		errCh <- reviewer.requestApproval(reviewerDeps{
			ctx:              context.Background(),
			isShellExecution: registry.ShellExecution,
			analyzeShell:     mods.analyzeShellCommand,
			accessIntent:     intent,
		}, "shell_run", data)
	}()

	item := receiveReviewItem(t, reviewer.reviewChan)
	require.Contains(t, item.summary, "unknown")
	require.NotContains(t, item.summary, "workspace mutation")
	require.Equal(t, "Run a command with unknown effects", item.presentation.headline)
	item.resp <- reviewResponse{approved: true}
	require.NoError(t, <-errCh)
}
```

- [ ] **Step 2: Run test and confirm failure**

Run: `go test ./internal/app -run 'TestShellUnknownEffectPresentationSurvivesPrebuiltAccessIntent' -count=1`

Expected: failure because `requestApproval` reconstructs `shellCommandAnalysis` from `AccessIntent` and loses `Effect`.

- [ ] **Step 3: Preserve analysis metadata in `requestApproval`**

Implement the preferred path to avoid changing `approval.AccessIntent`: when `shellExecution` and `cmd != ""` and `deps.analyzeShell != nil`, call `deps.analyzeShell(name, cmd)` for presentation metadata even when `intent.HasAccess()` is true. Then normalize its affected dirs. Use the prebuilt `intent` for approval decisions, but keep the richer `analysis` for summary/presentation.

Modify the shell block in `requestApproval` to this shape:

```go
if shellExecution {
	cmd := extractShellCommand(data)
	if cmd != "" && deps.analyzeShell != nil {
		analysis = deps.analyzeShell(name, cmd)
		analysis.AffectedDirs = normalizeShellAffectedDirsForTool(analysis.AffectedDirs, r.scope.Value, name)
	}
	if intent.HasAccess() {
		class := intent.DominantClass()
		if analysis.Effect == "" {
			analysis = shellCommandAnalysis{
				NeedsReview:  class == AccessWrite,
				AffectedDirs: normalizeShellAffectedDirsForTool(intent.AllDirs(), r.scope.Value, name),
				Reason:       intent.Reason,
			}
			analysis = normalizeShellEffect(analysis)
		}
		intent = AccessIntent{Class: class, Dirs: normalizeShellAffectedDirsForTool(intent.AllDirs(), r.scope.Value, name), Reason: intent.Reason}
	} else if analysis.Effect != "" || cmd != "" && deps.analyzeShell != nil {
		intent = AccessIntent{Class: shellAccessMode(analysis), Dirs: analysis.AffectedDirs, Reason: analysis.Reason}
	} else {
		analysis = defaultShellCommandAnalysis()
		intent = AccessIntent{Class: shellAccessMode(analysis), Reason: analysis.Reason}
	}
}
```

After writing this, simplify the control flow to match existing style while preserving the behavior: prebuilt intent controls access, analyzer result controls presentation.

- [ ] **Step 4: Run end-to-end review test**

Run: `go test ./internal/app -run 'TestShellUnknownEffectPresentationSurvivesPrebuiltAccessIntent' -count=1`

Expected: PASS.

---

### Task 5: Present Unknown Effects Separately from Mutations

**Files:**
- Modify: `internal/app/review_summary.go`
- Modify: `internal/app/review_presentation.go`
- Test: `internal/app/review_summary_test.go`

**Interfaces:**
- Consumes `shellCommandAnalysis.Effect` from Task 3 and Task 4.
- Produces risk label behavior:
  - unknown effect => `unknown`
  - read effect + external dirs => `external read`
  - write effect + workspace dirs => `workspace mutation`
  - write effect + external dirs => `external mutation`

- [ ] **Step 1: Write failing summary tests**

Update `TestFormatReviewSummary` in `internal/app/review_summary_test.go` by adding:

```go
unknownSummary := formatReviewSummary("shell_run", []byte(`{"command":"opaque-command"}`), shellCommandAnalysis{NeedsReview: true, Effect: shellEffectUnknown, AffectedDirs: []string{"/workspace"}}, scope)
require.Contains(t, unknownSummary, "unknown")
require.NotContains(t, unknownSummary, "workspace mutation")
```

- [ ] **Step 2: Run tests and confirm failure**

Run: `go test ./internal/app -run 'TestFormatReviewSummary' -count=1`

Expected: failure because `shellRiskLevel` treats `NeedsReview=true` with workspace dirs as `workspace mutation`.

- [ ] **Step 3: Update `shellRiskLevel`**

In `internal/app/review_summary.go`, change `shellRiskLevel` to:

```go
func shellRiskLevel(analysis shellCommandAnalysis, scope Scope) string {
	if analysis.Effect == shellEffectUnknown {
		return "unknown"
	}
	if !analysis.NeedsReview || analysis.Effect == shellEffectRead {
		for _, dir := range analysis.AffectedDirs {
			if !pathWithinScope(dir, scope) {
				return "external read"
			}
		}
		return "read-only"
	}
	if len(analysis.AffectedDirs) == 0 {
		return "unknown"
	}
	for _, dir := range analysis.AffectedDirs {
		if !pathWithinScope(dir, scope) {
			return "external mutation"
		}
	}
	return "workspace mutation"
}
```

No change is required in `shellRiskHeadline` because the default branch already returns `Run a command with unknown effects`.

- [ ] **Step 4: Run summary tests**

Run: `go test ./internal/app -run 'TestFormatReviewSummary|TestShellUnknownEffectPresentationSurvivesPrebuiltAccessIntent' -count=1`

Expected: PASS.

---

### Task 6: End-to-End Sample Command Regression

**Files:**
- Test: `internal/approval/readonly_ps_test.go`
- Test: `internal/app/shell_classify_windows_test.go`

**Interfaces:**
- Consumes Task 1 bridge fields, Task 2 read-only classification, Task 3 shell effect state, and Task 4 review path preservation.
- Produces regression coverage for the command that triggered this work.

- [ ] **Step 1: Add sample classifier regression test**

Create `internal/app/shell_classify_windows_test.go` if it does not exist:

```go
//go:build windows

package app

import (
	"testing"

	"github.com/panjie/mods/internal/approval"
	"github.com/stretchr/testify/require"
)

func TestAnalyzeShellCommandPowerShellLineCountPipelineIsReadOnly(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	workspace := t.TempDir()
	m := &Mods{Config: testConfigForWorkspace(workspace)}
	cmd := `Get-ChildItem -Recurse -Filter *.go | Select-Object FullName | ForEach-Object { $lines = (Get-Content $_.FullName | Measure-Object -Line).Lines; "$($_.FullName): $lines lines" } | Sort-Object { [int]($_.Split(':')[1].Trim().Split(' ')[0]) } -Descending`

	got := m.analyzeShellCommand("shell_run", cmd)
	require.False(t, got.NeedsReview)
	require.Equal(t, shellEffectRead, got.Effect)
	require.NotEmpty(t, got.Reason)
}
```

- [ ] **Step 2: Run sample test and confirm pass**

Run: `go test ./internal/app -run 'TestAnalyzeShellCommandPowerShellLineCountPipelineIsReadOnly' -count=1`

Expected: PASS on Windows.

- [ ] **Step 3: Run focused approval tests**

Run: `go test ./internal/approval -run 'Test(ParseWithBridge|IsReadOnlyPowerShell)' -count=1`

Expected: PASS.

- [ ] **Step 4: Run focused app tests**

Run: `go test ./internal/app -run 'Test(DefaultShellCommandAnalysisIsUnknown|ShellStaticAnalysisSetsEffect|ParseShellAnalysisResponseCanReturnUnknownEffect|FormatReviewSummary|ShellUnknownEffectPresentationSurvivesPrebuiltAccessIntent|AnalyzeShellCommandPowerShellLineCountPipelineIsReadOnly)' -count=1`

Expected: PASS.

---

### Task 7: Full Verification

**Files:**
- No source files; verification only.

**Interfaces:**
- Consumes all prior tasks.
- Produces confidence that the implementation did not break build/test baselines.

- [ ] **Step 1: Run approval package tests**

Run: `go test ./internal/approval -count=1`

Expected: PASS.

- [ ] **Step 2: Run app package focused tests**

Run: `go test ./internal/app -run 'TestFormatReviewSummary|TestAnalyzeShellCommandPowerShellLineCountPipelineIsReadOnly|TestDefaultShellCommandAnalysisIsUnknown|TestShellStaticAnalysisSetsEffect|TestParseShellAnalysisResponseCanReturnUnknownEffect|TestShellUnknownEffectPresentationSurvivesPrebuiltAccessIntent' -count=1`

Expected: PASS.

- [ ] **Step 3: Run project check**

Run: `go run github.com/go-task/task/v3/cmd/task@v3.51.1 check`

Expected: PASS (`go build ./...`).

- [ ] **Step 4: Run full test suite**

Run: `go run github.com/go-task/task/v3/cmd/task@v3.51.1 test`

Expected: PASS (`go test ./...`).

---

## Self-Review

- Spec coverage: covers both requested areas: bounded broader PowerShell static read-only analysis and unknown-vs-write UI labeling.
- Security review coverage: incorporates preflight blockers by rejecting unsafe method/static calls, assigned-variable path hiding, `$HOME`/constructed paths, and preserving unknown through the actual review path.
- Placeholder scan: no placeholder tokens or undefined future work remains.
- Type consistency: `shellEffect`, `Effect`, `Variables`, `AssignmentTargets`, `MethodInvocations`, and `StaticMembers` names are consistent across tasks.
- Scope check: implementation remains focused on parser bridge facts, Go read-only classification, shell analysis state, and review labels; no unrelated approval architecture changes are included.
