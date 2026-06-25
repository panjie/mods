# Real Config Black-Box Test Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Run and report the approved black-box test suite against the local `mods` CLI using the user's real configuration and real AI provider.

**Architecture:** Use a temporary PowerShell harness outside the repository to invoke the built CLI as an external process. The harness captures exit codes, stdout, stderr, validates observable behavior, and writes a Markdown report. Product code remains unchanged.

**Tech Stack:** Go/Task build command, PowerShell 7, local `bin/mods.exe`, real `mods.yml`, real cache/database.

## Global Constraints

- Use the current user's real `mods.yml`, real cache/database, and configured default AI provider.
- Exercise behavior visible to a user rather than internal Go APIs.
- Build the local binary first and run it from the command line.
- Cover configuration discovery, single completion, JSON formatting, conversation persistence, and conversation continuation.
- Exclude filesystem and shell tool execution from this first pass.
- Record command purpose, exit code, stdout/stderr snippets, and observable side effects.
- Do not commit unless the user explicitly requests it.

---

## File Structure

- Create outside repo: `C:\Users\panjie\AppData\Local\Temp\opencode\mods-real-blackbox.ps1` — transient test harness that runs the black-box cases and writes the report.
- Create outside repo: `C:\Users\panjie\AppData\Local\Temp\opencode\mods-real-blackbox-report.md` — generated test report with pass/fail details.
- Read: `docs/superpowers/specs/2026-06-25-real-config-black-box-test-design.md` — approved test design.
- Use existing binary path after build: `bin\mods.exe`.

## Task 1: Build Local CLI Binary

**Files:**
- Use: `Taskfile.yml`
- Produces: `bin\mods.exe`

**Interfaces:**
- Consumes: Go module and build task from repository root.
- Produces: executable CLI at `C:\Users\panjie\dev\mods\bin\mods.exe`.

- [ ] **Step 1: Run the build command**

Run: `go run github.com/go-task/task/v3/cmd/task@v3.51.1 build`

Expected: command exits `0` and writes `bin\mods.exe`.

- [ ] **Step 2: Verify the binary exists**

Run: `Test-Path -LiteralPath "bin\mods.exe"`

Expected: `True`.

## Task 2: Create The Black-Box Harness

**Files:**
- Create: `C:\Users\panjie\AppData\Local\Temp\opencode\mods-real-blackbox.ps1`
- Create: `C:\Users\panjie\AppData\Local\Temp\opencode\mods-real-blackbox-report.md`

**Interfaces:**
- Consumes: `bin\mods.exe` from Task 1.
- Produces: Markdown report and process-level pass/fail summary.

- [ ] **Step 1: Write the harness**

Create `C:\Users\panjie\AppData\Local\Temp\opencode\mods-real-blackbox.ps1` with these behaviors:

```powershell
param(
  [Parameter(Mandatory = $true)]
  [string]$ModsExe,

  [Parameter(Mandatory = $true)]
  [string]$ReportPath
)

$ErrorActionPreference = 'Stop'

function Invoke-ModsCase {
  param(
    [Parameter(Mandatory = $true)] [string]$Name,
    [Parameter(Mandatory = $true)] [string]$Purpose,
    [Parameter(Mandatory = $true)] [string[]]$Args
  )

  $stdoutFile = Join-Path $env:TEMP ("mods-bb-stdout-{0}.txt" -f ([guid]::NewGuid().ToString('N')))
  $stderrFile = Join-Path $env:TEMP ("mods-bb-stderr-{0}.txt" -f ([guid]::NewGuid().ToString('N')))
  $started = Get-Date
  $process = Start-Process -FilePath $ModsExe -ArgumentList $Args -NoNewWindow -Wait -PassThru -RedirectStandardOutput $stdoutFile -RedirectStandardError $stderrFile
  $ended = Get-Date
  $stdout = if (Test-Path -LiteralPath $stdoutFile) { Get-Content -LiteralPath $stdoutFile -Raw } else { '' }
  $stderr = if (Test-Path -LiteralPath $stderrFile) { Get-Content -LiteralPath $stderrFile -Raw } else { '' }
  Remove-Item -LiteralPath $stdoutFile, $stderrFile -Force -ErrorAction SilentlyContinue

  [pscustomobject]@{
    Name = $Name
    Purpose = $Purpose
    Args = $Args
    ExitCode = $process.ExitCode
    Stdout = $stdout
    Stderr = $stderr
    Started = $started
    Ended = $ended
    DurationSeconds = [math]::Round(($ended - $started).TotalSeconds, 2)
    Passed = $false
    Notes = @()
  }
}

function Get-Snippet {
  param([string]$Text, [int]$Max = 1000)
  if ([string]::IsNullOrWhiteSpace($Text)) { return '' }
  $clean = $Text.Trim()
  if ($clean.Length -le $Max) { return $clean }
  return $clean.Substring(0, $Max) + '...'
}

function Get-JsonCandidate {
  param([string]$Text)
  $trimmed = $Text.Trim()
  if ($trimmed -match '(?s)^```(?:json)?\s*(.*?)\s*```$') {
    return $Matches[1].Trim()
  }
  return $trimmed
}

function Add-ReportCase {
  param([System.Collections.Generic.List[string]]$Lines, [pscustomobject]$Case)
  $status = if ($Case.Passed) { 'PASS' } else { 'FAIL' }
  $Lines.Add("### $($Case.Name): $status")
  $Lines.Add("")
  $Lines.Add("Purpose: $($Case.Purpose)")
  $Lines.Add("")
  $Lines.Add("Exit code: $($Case.ExitCode)")
  $Lines.Add("")
  $Lines.Add("Duration: $($Case.DurationSeconds)s")
  $Lines.Add("")
  if ($Case.Notes.Count -gt 0) {
    $Lines.Add("Notes: $($Case.Notes -join '; ')")
    $Lines.Add("")
  }
  $Lines.Add("Stdout snippet:")
  $Lines.Add('```text')
  $Lines.Add((Get-Snippet $Case.Stdout))
  $Lines.Add('```')
  $Lines.Add("")
  $Lines.Add("Stderr snippet:")
  $Lines.Add('```text')
  $Lines.Add((Get-Snippet $Case.Stderr))
  $Lines.Add('```')
  $Lines.Add("")
}

$timestamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$marker = "MODS-BLACKBOX-$timestamp"
$title = "mods-blackbox-$timestamp"
$cases = New-Object System.Collections.Generic.List[object]

$tc1 = Invoke-ModsCase -Name 'TC1 dirs' -Purpose 'Discover real configuration and cache directories.' -Args @('--dirs')
$tc1.Passed = $tc1.ExitCode -eq 0 -and $tc1.Stdout -match 'Configuration:' -and $tc1.Stdout -match 'Cache:'
if (-not $tc1.Passed) { $tc1.Notes += 'Expected Configuration and Cache lines.' }
$cases.Add($tc1)

$tc2Prompt = "Reply with exactly this token and no extra text: $marker-PING"
$tc2 = Invoke-ModsCase -Name 'TC2 default completion' -Purpose 'Run a real completion with default provider/model.' -Args @('--raw', '--quiet', '--no-cache', $tc2Prompt)
$tc2.Passed = $tc2.ExitCode -eq 0 -and -not [string]::IsNullOrWhiteSpace($tc2.Stdout) -and $tc2.Stdout.Contains("$marker-PING")
if (-not $tc2.Passed) { $tc2.Notes += 'Expected non-empty output containing requested token.' }
$cases.Add($tc2)

$tc3Prompt = 'Return only valid JSON: {"blackbox_status":"ok","answer":42}'
$tc3 = Invoke-ModsCase -Name 'TC3 JSON formatting' -Purpose 'Validate formatted JSON output can be parsed.' -Args @('--format', '--format-as', 'json', '--raw', '--quiet', '--no-cache', $tc3Prompt)
try {
  $json = Get-JsonCandidate $tc3.Stdout | ConvertFrom-Json -ErrorAction Stop
  $tc3.Passed = $tc3.ExitCode -eq 0 -and $json.blackbox_status -eq 'ok' -and [int]$json.answer -eq 42
} catch {
  $tc3.Passed = $false
  $tc3.Notes += "JSON parse failed: $($_.Exception.Message)"
}
if (-not $tc3.Passed -and $tc3.Notes.Count -eq 0) { $tc3.Notes += 'Expected blackbox_status=ok and answer=42.' }
$cases.Add($tc3)

$tc4Prompt = "Remember this marker for the next turn: $marker. Reply with saved."
$tc4 = Invoke-ModsCase -Name 'TC4 save conversation' -Purpose 'Save a real conversation under a unique title.' -Args @('--raw', '--quiet', '--title', $title, $tc4Prompt)
$tc4.Passed = $tc4.ExitCode -eq 0 -and -not [string]::IsNullOrWhiteSpace($tc4.Stdout)
if (-not $tc4.Passed) { $tc4.Notes += 'Expected saved completion to return non-empty output.' }
$cases.Add($tc4)

$tc4List = Invoke-ModsCase -Name 'TC4 list conversation' -Purpose 'Verify saved title appears in conversation list.' -Args @('--list')
$tc4List.Passed = $tc4List.ExitCode -eq 0 -and $tc4List.Stdout.Contains($title)
if (-not $tc4List.Passed) { $tc4List.Notes += "Expected --list output to contain $title." }
$cases.Add($tc4List)

$tc4Show = Invoke-ModsCase -Name 'TC4 show conversation' -Purpose 'Verify saved conversation can be shown by title.' -Args @('--show', $title)
$tc4Show.Passed = $tc4Show.ExitCode -eq 0 -and ($tc4Show.Stdout.Contains($marker) -or $tc4Show.Stdout.Contains('saved'))
if (-not $tc4Show.Passed) { $tc4Show.Notes += 'Expected --show output to contain marker or assistant response.' }
$cases.Add($tc4Show)

$tc5Prompt = 'What marker did I ask you to remember? Reply with only the marker.'
$tc5 = Invoke-ModsCase -Name 'TC5 continue conversation' -Purpose 'Continue saved conversation and verify prior context is loaded.' -Args @('--raw', '--quiet', '--continue', $title, $tc5Prompt)
$tc5.Passed = $tc5.ExitCode -eq 0 -and -not [string]::IsNullOrWhiteSpace($tc5.Stdout) -and $tc5.Stdout.Contains($marker)
if (-not $tc5.Passed) { $tc5.Notes += 'Expected continued output to contain the remembered marker.' }
$cases.Add($tc5)

$lines = New-Object System.Collections.Generic.List[string]
$lines.Add('# Mods Real Config Black-Box Report')
$lines.Add('')
$lines.Add("Run timestamp: $timestamp")
$lines.Add('')
$lines.Add("Binary: $ModsExe")
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
$lines | Set-Content -LiteralPath $ReportPath -Encoding UTF8

if ($passedCount -ne $cases.Count) {
  Write-Error "Black-box run failed: $passedCount / $($cases.Count) cases passed. Report: $ReportPath"
}

"Black-box run passed: $passedCount / $($cases.Count) cases. Report: $ReportPath"
```

- [ ] **Step 2: Verify the harness file exists**

Run: `Test-Path -LiteralPath "C:\Users\panjie\AppData\Local\Temp\opencode\mods-real-blackbox.ps1"`

Expected: `True`.

## Task 3: Execute And Report

**Files:**
- Run: `C:\Users\panjie\AppData\Local\Temp\opencode\mods-real-blackbox.ps1`
- Read: `C:\Users\panjie\AppData\Local\Temp\opencode\mods-real-blackbox-report.md`

**Interfaces:**
- Consumes: binary from Task 1 and harness from Task 2.
- Produces: final pass/fail report for the user.

- [ ] **Step 1: Run the harness**

Run: `pwsh -NoProfile -ExecutionPolicy Bypass -File "C:\Users\panjie\AppData\Local\Temp\opencode\mods-real-blackbox.ps1" -ModsExe "C:\Users\panjie\dev\mods\bin\mods.exe" -ReportPath "C:\Users\panjie\AppData\Local\Temp\opencode\mods-real-blackbox-report.md"`

Expected: exits `0` with `Black-box run passed` if every case passes. If it exits non-zero, read the report and classify failures.

- [ ] **Step 2: Read the report**

Run: `Get-Content -LiteralPath "C:\Users\panjie\AppData\Local\Temp\opencode\mods-real-blackbox-report.md" -Raw`

Expected: report includes all test cases and snippets.

- [ ] **Step 3: Summarize results**

Final response must include:

- The number of passing cases.
- Any failing cases with command-level reason.
- The real config/cache paths discovered by TC1.
- The report path.
- Residual risks such as provider nondeterminism, rate limits, or JSON fence tolerance.

## Self-Review

- Spec coverage: TC1 through TC5 map directly to Task 2 harness cases and Task 3 report summary.
- Placeholder scan: no placeholder work remains; commands, paths, and validations are explicit.
- Type consistency: harness uses PowerShell objects consistently with `Passed`, `Notes`, stdout/stderr, and report fields.
