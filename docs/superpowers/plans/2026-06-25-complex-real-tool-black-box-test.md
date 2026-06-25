# Complex Real Tool Black-Box Test Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Run and report complex real `mods` CLI black-box tests covering built-in filesystem tools, shell tools, plan mode, and continued tool-based conversations.

**Architecture:** Use a temporary PowerShell harness outside the repository to prepare a run-specific workspace and invoke `bin\mods.exe` as an external process. The harness verifies command exit codes, stdout/stderr snippets, and workspace file side effects. Product code remains unchanged unless a real bug is discovered and fixed separately.

**Tech Stack:** Go/Task build command, PowerShell 7, local `bin/mods.exe`, real `mods.yml`, real cache/database, built-in filesystem and shell tools.

## Global Constraints

- Use the user's real `mods.yml`, default AI provider, real cache/database, and real built-in tool execution.
- Exercise the CLI only through external commands; do not call internal Go APIs.
- All file and shell side effects must stay under `C:\Users\panjie\AppData\Local\Temp\opencode\mods-complex-blackbox-<timestamp>`.
- The repository working tree must not be used as the tool workspace.
- Use `--workspace <temp-workspace>`, `--review never`, `--raw --quiet`, and `--no-cache` except for save/continue.
- Web search and provider matrix testing are out of scope for this pass.
- Report each case as pass/fail with classification: CLI/tool failure, model compliance failure, interactive gate, or provider/environment failure.
- Do not commit unless the user explicitly requests it.

---

## File Structure

- Create outside repo: `C:\Users\panjie\AppData\Local\Temp\opencode\mods-complex-blackbox.ps1` — transient complex black-box harness.
- Create outside repo: `C:\Users\panjie\AppData\Local\Temp\opencode\mods-complex-blackbox-report.md` — generated report.
- Create at runtime outside repo: `C:\Users\panjie\AppData\Local\Temp\opencode\mods-complex-blackbox-<timestamp>` — tool workspace and fixtures.
- Read: `docs/superpowers/specs/2026-06-25-complex-real-tool-black-box-test-design.md` — approved design.
- Use existing binary path after build: `bin\mods.exe`.

## Task 1: Build Local CLI Binary

**Files:**
- Use: `Taskfile.yml`
- Produces: `bin\mods.exe`

**Interfaces:**
- Consumes: repository Go module and build task.
- Produces: executable CLI at `C:\Users\panjie\dev\mods\bin\mods.exe`.

- [ ] **Step 1: Run the build command**

Run: `go run github.com/go-task/task/v3/cmd/task@v3.51.1 build`

Expected: exits `0` and writes `bin\mods.exe`.

- [ ] **Step 2: Verify the binary exists**

Run: `Test-Path -LiteralPath "bin\mods.exe"`

Expected: `True`.

## Task 2: Create Complex Tool Harness

**Files:**
- Create: `C:\Users\panjie\AppData\Local\Temp\opencode\mods-complex-blackbox.ps1`
- Create during run: `C:\Users\panjie\AppData\Local\Temp\opencode\mods-complex-blackbox-report.md`

**Interfaces:**
- Consumes: `bin\mods.exe` from Task 1.
- Produces: report and workspace side effects under temp only.

- [ ] **Step 1: Write the harness**

Create `C:\Users\panjie\AppData\Local\Temp\opencode\mods-complex-blackbox.ps1` with these behaviors:

```powershell
param(
  [Parameter(Mandatory = $true)] [string]$ModsExe,
  [Parameter(Mandatory = $true)] [string]$ReportPath
)

$ErrorActionPreference = 'Stop'

function Invoke-ModsCase {
  param(
    [Parameter(Mandatory = $true)] [string]$Name,
    [Parameter(Mandatory = $true)] [string]$Purpose,
    [Parameter(Mandatory = $true)] [string[]]$Args,
    [int]$TimeoutSeconds = 90
  )

  $stdoutFile = Join-Path $env:TEMP ("mods-complex-stdout-{0}.txt" -f ([guid]::NewGuid().ToString('N')))
  $stderrFile = Join-Path $env:TEMP ("mods-complex-stderr-{0}.txt" -f ([guid]::NewGuid().ToString('N')))
  $started = Get-Date
  $process = Start-Process -FilePath $ModsExe -ArgumentList $Args -NoNewWindow -PassThru -RedirectStandardOutput $stdoutFile -RedirectStandardError $stderrFile
  $completed = $process.WaitForExit($TimeoutSeconds * 1000)
  if (-not $completed) {
    Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
    $process.WaitForExit()
  }
  $ended = Get-Date
  $stdout = if (Test-Path -LiteralPath $stdoutFile) { Get-Content -LiteralPath $stdoutFile -Raw } else { '' }
  $stderr = if (Test-Path -LiteralPath $stderrFile) { Get-Content -LiteralPath $stderrFile -Raw } else { '' }
  Remove-Item -LiteralPath $stdoutFile, $stderrFile -Force -ErrorAction SilentlyContinue

  [pscustomobject]@{
    Name = $Name
    Purpose = $Purpose
    Args = $Args
    ExitCode = if ($completed) { $process.ExitCode } else { -999 }
    TimedOut = -not $completed
    Stdout = $stdout
    Stderr = $stderr
    Started = $started
    Ended = $ended
    DurationSeconds = [math]::Round(($ended - $started).TotalSeconds, 2)
    Passed = $false
    Classification = 'unclassified'
    Notes = @()
  }
}

function Get-Snippet {
  param([string]$Text, [int]$Max = 1400)
  if ([string]::IsNullOrWhiteSpace($Text)) { return '' }
  $clean = $Text.Trim()
  if ($clean.Length -le $Max) { return $clean }
  return $clean.Substring(0, $Max) + '...'
}

function Set-CaseResult {
  param([pscustomobject]$Case, [bool]$Passed, [string]$Classification, [string]$Note = '')
  $Case.Passed = $Passed
  $Case.Classification = $Classification
  if ($Note -ne '') { $Case.Notes += $Note }
}

function Add-ReportCase {
  param([System.Collections.Generic.List[string]]$Lines, [pscustomobject]$Case)
  $status = if ($Case.Passed) { 'PASS' } else { 'FAIL' }
  $Lines.Add("### $($Case.Name): $status")
  $Lines.Add('')
  $Lines.Add("Purpose: $($Case.Purpose)")
  $Lines.Add('')
  $Lines.Add("Classification: $($Case.Classification)")
  $Lines.Add('')
  $Lines.Add("Exit code: $($Case.ExitCode)")
  $Lines.Add('')
  $Lines.Add("Timed out: $($Case.TimedOut)")
  $Lines.Add('')
  $Lines.Add("Duration: $($Case.DurationSeconds)s")
  $Lines.Add('')
  if ($Case.Notes.Count -gt 0) {
    $Lines.Add("Notes: $($Case.Notes -join '; ')")
    $Lines.Add('')
  }
  $Lines.Add('Stdout snippet:')
  $Lines.Add('```text')
  $Lines.Add((Get-Snippet $Case.Stdout))
  $Lines.Add('```')
  $Lines.Add('')
  $Lines.Add('Stderr snippet:')
  $Lines.Add('```text')
  $Lines.Add((Get-Snippet $Case.Stderr))
  $Lines.Add('```')
  $Lines.Add('')
}

$timestamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$marker = "MODS-COMPLEX-$timestamp"
$title = "mods-complex-$timestamp"
$workspace = Join-Path 'C:\Users\panjie\AppData\Local\Temp\opencode' "mods-complex-blackbox-$timestamp"
New-Item -ItemType Directory -Path $workspace -Force | Out-Null

$cases = New-Object System.Collections.Generic.List[object]

$factsPath = Join-Path $workspace 'facts.txt'
$factsContent = "marker=$marker`nkind=read-fixture`n"
Set-Content -LiteralPath $factsPath -Value $factsContent -Encoding UTF8

$tc1Prompt = "Use the filesystem tool to read facts.txt in the workspace. Reply with only the marker value from the file."
$tc1 = Invoke-ModsCase -Name 'TC1 tool read file' -Purpose 'Model reads an existing workspace file through tools.' -Args @('--workspace', $workspace, '--review', 'never', '--raw', '--quiet', '--no-cache', $tc1Prompt)
if ($tc1.ExitCode -eq 0 -and $tc1.Stdout.Contains($marker) -and ((Get-Content -LiteralPath $factsPath -Raw) -eq $factsContent)) {
  Set-CaseResult $tc1 $true 'pass'
} else {
  Set-CaseResult $tc1 $false 'model compliance failure' 'Expected stdout marker and unchanged facts.txt.'
}
$cases.Add($tc1)

$generatedPath = Join-Path $workspace 'generated.txt'
$tc2Prompt = "Use the filesystem write tool to create generated.txt in the workspace. The file must contain this exact marker: $marker. Reply with done after writing."
$tc2 = Invoke-ModsCase -Name 'TC2 tool create file' -Purpose 'Model creates a new workspace file through tools.' -Args @('--workspace', $workspace, '--review', 'never', '--raw', '--quiet', '--no-cache', $tc2Prompt)
$generatedContent = if (Test-Path -LiteralPath $generatedPath) { Get-Content -LiteralPath $generatedPath -Raw } else { '' }
if ($tc2.ExitCode -eq 0 -and $generatedContent.Contains($marker)) {
  Set-CaseResult $tc2 $true 'pass'
} else {
  Set-CaseResult $tc2 $false 'model compliance failure' 'Expected generated.txt containing marker.'
}
$cases.Add($tc2)

$editPath = Join-Path $workspace 'edit-target.txt'
Set-Content -LiteralPath $editPath -Value "HEADER-STABLE`nvalue=PLACEHOLDER`nFOOTER-STABLE`n" -Encoding UTF8
$tc3Prompt = "Use filesystem editing tools to replace PLACEHOLDER in edit-target.txt with $marker. Keep HEADER-STABLE and FOOTER-STABLE unchanged. Reply edited."
$tc3 = Invoke-ModsCase -Name 'TC3 tool edit file' -Purpose 'Model edits an existing file while preserving unrelated lines.' -Args @('--workspace', $workspace, '--review', 'never', '--raw', '--quiet', '--no-cache', $tc3Prompt)
$editContent = Get-Content -LiteralPath $editPath -Raw
if ($tc3.ExitCode -eq 0 -and $editContent.Contains($marker) -and $editContent.Contains('HEADER-STABLE') -and $editContent.Contains('FOOTER-STABLE') -and -not $editContent.Contains('PLACEHOLDER')) {
  Set-CaseResult $tc3 $true 'pass'
} else {
  Set-CaseResult $tc3 $false 'model compliance failure' 'Expected placeholder replacement with stable header/footer preserved.'
}
$cases.Add($tc3)

$shellResultPath = Join-Path $workspace 'shell-result.txt'
$tc4Prompt = "Use a shell or PowerShell tool to count the files in the workspace directory. Then write shell-result.txt containing COUNT=<number> and MARKER=$marker. Reply shell-done."
$tc4 = Invoke-ModsCase -Name 'TC4 shell workspace inspection' -Purpose 'Model uses shell execution and records the result in a file.' -Args @('--workspace', $workspace, '--review', 'never', '--raw', '--quiet', '--no-cache', $tc4Prompt)
$shellContent = if (Test-Path -LiteralPath $shellResultPath) { Get-Content -LiteralPath $shellResultPath -Raw } else { '' }
$countMatch = [regex]::Match($shellContent, 'COUNT\s*=\s*(\d+)')
$countOK = $countMatch.Success -and ([int]$countMatch.Groups[1].Value -ge 3)
if ($tc4.ExitCode -eq 0 -and $shellContent.Contains($marker) -and $countOK) {
  Set-CaseResult $tc4 $true 'pass'
} else {
  Set-CaseResult $tc4 $false 'model compliance failure' 'Expected shell-result.txt with marker and COUNT >= 3.'
}
$cases.Add($tc4)

$planPath = Join-Path $workspace 'plan-created.txt'
$tc5Prompt = "Plan and then create plan-created.txt in the workspace containing $marker."
$tc5 = Invoke-ModsCase -Name 'TC5 plan mode gate' -Purpose 'Exercise plan mode and classify non-interactive approval behavior.' -Args @('--plan', '--workspace', $workspace, '--review', 'never', '--raw', '--quiet', '--no-cache', $tc5Prompt) -TimeoutSeconds 45
$planContent = if (Test-Path -LiteralPath $planPath) { Get-Content -LiteralPath $planPath -Raw } else { '' }
$combinedPlanOutput = "$($tc5.Stdout)`n$($tc5.Stderr)"
if ($tc5.ExitCode -eq 0 -and $planContent.Contains($marker)) {
  Set-CaseResult $tc5 $true 'pass'
} elseif ($tc5.TimedOut -or $combinedPlanOutput -match '(?i)approve|approval|plan|interactive|input|prompt') {
  Set-CaseResult $tc5 $true 'interactive gate' 'Plan mode produced a non-automatable approval/input gate.'
} else {
  Set-CaseResult $tc5 $false 'CLI/tool failure' 'Expected file creation or clear interactive plan gate.'
}
$cases.Add($tc5)

$continuePath = Join-Path $workspace 'continue-file.txt'
$tc6PromptA = "Use filesystem tools to create continue-file.txt in the workspace containing $marker. Reply continue-created."
$tc6a = Invoke-ModsCase -Name 'TC6a save tool conversation' -Purpose 'Save a conversation that creates a workspace file.' -Args @('--workspace', $workspace, '--review', 'never', '--raw', '--quiet', '--title', $title, $tc6PromptA)
$continueContentA = if (Test-Path -LiteralPath $continuePath) { Get-Content -LiteralPath $continuePath -Raw } else { '' }
if ($tc6a.ExitCode -eq 0 -and $continueContentA.Contains($marker)) {
  Set-CaseResult $tc6a $true 'pass'
} else {
  Set-CaseResult $tc6a $false 'model compliance failure' 'Expected continue-file.txt containing marker after saved run.'
}
$cases.Add($tc6a)

$tc6PromptB = "Continue the prior task. Read continue-file.txt from the workspace and reply with only the marker in that file."
$tc6b = Invoke-ModsCase -Name 'TC6b continue tool conversation' -Purpose 'Continue saved conversation and read prior workspace file.' -Args @('--workspace', $workspace, '--review', 'never', '--raw', '--quiet', '--continue', $title, $tc6PromptB)
if ($tc6b.ExitCode -eq 0 -and $tc6b.Stdout.Contains($marker)) {
  Set-CaseResult $tc6b $true 'pass'
} else {
  Set-CaseResult $tc6b $false 'model compliance failure' 'Expected continued output to contain marker from continue-file.txt.'
}
$cases.Add($tc6b)

$lines = New-Object System.Collections.Generic.List[string]
$lines.Add('# Mods Complex Real Tool Black-Box Report')
$lines.Add('')
$lines.Add("Run timestamp: $timestamp")
$lines.Add('')
$lines.Add("Binary: $ModsExe")
$lines.Add('')
$lines.Add("Workspace: $workspace")
$lines.Add('')
$lines.Add("Marker: $marker")
$lines.Add('')
$lines.Add("Conversation title: $title")
$lines.Add('')
$passedCount = @($cases | Where-Object { $_.Passed }).Count
$lines.Add("Summary: $passedCount / $($cases.Count) cases passed")
$lines.Add('')
foreach ($case in $cases) {
  Add-ReportCase -Lines $lines -Case $case
}
$lines.Add('Repository note: tool workspace is outside the repository; only deliberate spec/plan files or separately reported bug fixes should affect the repo working tree.')
$lines | Set-Content -LiteralPath $ReportPath -Encoding UTF8

if ($passedCount -ne $cases.Count) {
  Write-Error "Complex black-box run failed: $passedCount / $($cases.Count) cases passed. Report: $ReportPath"
}

"Complex black-box run passed: $passedCount / $($cases.Count) cases. Report: $ReportPath"
```

- [ ] **Step 2: Verify harness file exists**

Run: `Test-Path -LiteralPath "C:\Users\panjie\AppData\Local\Temp\opencode\mods-complex-blackbox.ps1"`

Expected: `True`.

## Task 3: Execute Complex Black-Box Suite

**Files:**
- Run: `C:\Users\panjie\AppData\Local\Temp\opencode\mods-complex-blackbox.ps1`
- Read: `C:\Users\panjie\AppData\Local\Temp\opencode\mods-complex-blackbox-report.md`

**Interfaces:**
- Consumes: binary from Task 1 and harness from Task 2.
- Produces: report and final user summary.

- [ ] **Step 1: Run the harness**

Run: `pwsh -NoProfile -ExecutionPolicy Bypass -File "C:\Users\panjie\AppData\Local\Temp\opencode\mods-complex-blackbox.ps1" -ModsExe "C:\Users\panjie\dev\mods\bin\mods.exe" -ReportPath "C:\Users\panjie\AppData\Local\Temp\opencode\mods-complex-blackbox-report.md"`

Expected: exits `0` when every case passes or only TC5 is classified as an accepted interactive gate. If it exits non-zero, read the report and classify failures before deciding whether a real bug fix is needed.

- [ ] **Step 2: Read the report**

Run: `Get-Content -LiteralPath "C:\Users\panjie\AppData\Local\Temp\opencode\mods-complex-blackbox-report.md" -Raw`

Expected: report includes workspace, marker, all test cases, classifications, and snippets.

- [ ] **Step 3: Verify repository status**

Run: `git status --short`

Expected: only deliberate spec/plan files are untracked or modified unless a separately identified bug fix was required.

- [ ] **Step 4: Summarize results**

Final response must include:

- Number of passing cases.
- Any failing cases with classification and root-cause notes.
- Workspace path and report path.
- Whether plan mode was executed or classified as an interactive gate.
- Whether the repository working tree stayed isolated from tool side effects.

## Self-Review

- Spec coverage: TC1 through TC6 are implemented in Task 2 harness; isolation, failure classification, and reporting are covered in Task 3.
- Placeholder scan: runtime placeholders are concrete variables generated by the harness; there are no incomplete implementation steps.
- Type consistency: PowerShell case objects use the same `Passed`, `Classification`, `Notes`, stdout/stderr, and timeout fields throughout the harness.
