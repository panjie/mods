[CmdletBinding()]
param(
    [ValidateSet('quick', 'stress')]
    [string]$Mode = 'quick',
    [ValidateSet('auto', 'ps51', 'ps7')]
    [string]$PowerShellHost = 'auto',
    [string]$OutputDirectory = 'artifacts/windows-reliability'
)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$outputRoot = Join-Path $repoRoot $OutputDirectory
New-Item -ItemType Directory -Force -Path $outputRoot | Out-Null

$originalPath = $env:PATH
$originalExpectedHost = $env:MODS_EXPECT_POWERSHELL_HOST
$originalIterations = $env:MODS_WINDOWS_STRESS_ITERATIONS
try {
    if ($PowerShellHost -eq 'ps51') {
        $pwsh = Get-Command pwsh.exe -ErrorAction SilentlyContinue
        if ($pwsh) {
            $pwshDirectory = Split-Path -Parent $pwsh.Source
            $env:PATH = (($env:PATH -split ';') | Where-Object {
                $_ -and -not [String]::Equals($_.TrimEnd('\'), $pwshDirectory.TrimEnd('\'), [StringComparison]::OrdinalIgnoreCase)
            }) -join ';'
        }
        $env:MODS_EXPECT_POWERSHELL_HOST = 'powershell.exe'
    } elseif ($PowerShellHost -eq 'ps7') {
        if (-not (Get-Command pwsh.exe -ErrorAction SilentlyContinue)) {
            throw 'PowerShell 7 (pwsh.exe) is required for the ps7 lane.'
        }
        $env:MODS_EXPECT_POWERSHELL_HOST = 'pwsh.exe'
    } else {
        Remove-Item Env:MODS_EXPECT_POWERSHELL_HOST -ErrorAction SilentlyContinue
    }
    $env:MODS_WINDOWS_STRESS_ITERATIONS = if ($Mode -eq 'stress') { '100' } else { '1' }

    $stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
    $lane = "$PowerShellHost-$Mode"
    $jsonLog = Join-Path $outputRoot "$lane-$stamp.jsonl"
    $packages = @('./internal/tools', './internal/approval', './internal/app')
    $arguments = @('test', '-tags', 'windowsreliability', '-run', 'WindowsReliability', '-json', '-count=1', '-timeout=15m') + $packages

    Push-Location $repoRoot
    try {
        & go @arguments 2>&1 | ForEach-Object { [string]$_ } | Set-Content -Encoding UTF8 -Path $jsonLog
        $exitCode = $LASTEXITCODE
    } finally {
        Pop-Location
    }

    $events = @()
    Get-Content $jsonLog | ForEach-Object {
        try { $events += ($_ | ConvertFrom-Json) } catch { }
    }
    $cases = @($events | Where-Object { $_.Test -and $_.Action -in @('pass', 'fail', 'skip') } | ForEach-Object {
        [PSCustomObject]@{
            package = $_.Package
            test = $_.Test
            status = if ($_.Action -eq 'skip') { 'blocked/not covered' } else { $_.Action }
            elapsed_seconds = $_.Elapsed
        }
    })
    $hostCommand = Get-Command $env:MODS_EXPECT_POWERSHELL_HOST -ErrorAction SilentlyContinue
    $report = [PSCustomObject]@{
        generated_at = (Get-Date).ToUniversalTime().ToString('o')
        production_ready = ($exitCode -eq 0 -and -not ($cases | Where-Object { $_.status -ne 'pass' }))
        exit_code = $exitCode
        mode = $Mode
        requested_host = $PowerShellHost
        expected_host = $env:MODS_EXPECT_POWERSHELL_HOST
        resolved_host = if ($hostCommand) { $hostCommand.Source } else { $null }
        os = [Environment]::OSVersion.VersionString
        architecture = $env:PROCESSOR_ARCHITECTURE
        ci = $env:CI
        terminal_session = $env:WT_SESSION
        stress_iterations = [int]$env:MODS_WINDOWS_STRESS_ITERATIONS
        test_unc_root_configured = -not [string]::IsNullOrWhiteSpace($env:TEST_UNC_ROOT)
        cases = $cases
        raw_log = Split-Path -Leaf $jsonLog
    }
    $jsonReport = Join-Path $outputRoot "$lane-$stamp-report.json"
    $markdownReport = Join-Path $outputRoot "$lane-$stamp-report.md"
    $report | ConvertTo-Json -Depth 6 | Set-Content -Encoding UTF8 -Path $jsonReport

    $lines = @(
        '# Windows reliability report',
        '',
        "- Generated: $($report.generated_at)",
        "- Environment: $($report.os) / $($report.architecture)",
        "- Lane: $lane",
        "- Production ready: $($report.production_ready)",
        "- UNC configured: $($report.test_unc_root_configured)",
        '',
        '| Package | Test | Status | Seconds |',
        '|---|---|---:|---:|'
    )
    foreach ($case in $cases) {
        $lines += "| $($case.package) | $($case.test) | $($case.status) | $($case.elapsed_seconds) |"
    }
    $lines | Set-Content -Encoding UTF8 -Path $markdownReport

    Write-Host "Windows reliability report: $markdownReport"
    if ($exitCode -ne 0) { exit $exitCode }
} finally {
    $env:PATH = $originalPath
    if ($null -eq $originalExpectedHost) { Remove-Item Env:MODS_EXPECT_POWERSHELL_HOST -ErrorAction SilentlyContinue } else { $env:MODS_EXPECT_POWERSHELL_HOST = $originalExpectedHost }
    if ($null -eq $originalIterations) { Remove-Item Env:MODS_WINDOWS_STRESS_ITERATIONS -ErrorAction SilentlyContinue } else { $env:MODS_WINDOWS_STRESS_ITERATIONS = $originalIterations }
}
