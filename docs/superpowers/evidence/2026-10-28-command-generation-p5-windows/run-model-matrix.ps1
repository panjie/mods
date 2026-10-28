[CmdletBinding()]
param(
    [ValidateSet('ps51','ps7')][string]$PowerShellHost = 'ps7',
    [Parameter(Mandatory=$true)][string]$BaselineDirectory,
    [ValidateSet('bc','a1')][string[]]$Groups = @('bc','a1')
)
$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '../../../..')).Path
if (-not $env:DEEPSEEK_API_KEY) { throw 'DEEPSEEK_API_KEY is not set' }
if ($PowerShellHost -eq 'ps51') {
    $env:PATH = (($env:PATH -split ';') | Where-Object {
        $_ -and -not (Test-Path -LiteralPath (Join-Path $_.Trim('"') 'pwsh.exe'))
    }) -join ';'
    $hostName = 'powershell.exe'
} else { $hostName = 'pwsh.exe' }
$hostPath = (Get-Command $hostName -ErrorAction Stop).Source
if ($PowerShellHost -eq 'ps51' -and (Get-Command pwsh.exe -ErrorAction SilentlyContinue)) {
    throw 'PS7 remains on PATH in the PS5.1 lane'
}
$laneRoot = if ($PowerShellHost -eq 'ps7') { $PSScriptRoot } else { Join-Path $PSScriptRoot 'ps51' }
New-Item -ItemType Directory -Force $laneRoot | Out-Null
@{requested_host=$PowerShellHost; resolved_host=$hostPath; version=(& $hostPath -NoProfile -Command '$PSVersionTable.PSVersion.ToString()')} |
    ConvertTo-Json | Set-Content -Encoding utf8 (Join-Path $laneRoot 'host.json')
$env:MODS_GENERATION_REPEATS = '5'
$env:MODS_BLACKBOX_MODEL = 'deepseek-v4-flash'
Remove-Item Env:MODS_GENERATION_CASES -ErrorAction SilentlyContinue
foreach ($group in $Groups) {
    $source = if ($group -eq 'bc') { $repoRoot } else { (Resolve-Path $BaselineDirectory).Path }
    $env:MODS_GENERATION_COMMIT = if ($group -eq 'bc') { (& git -C $repoRoot rev-parse HEAD) } else { (& git -C $repoRoot rev-parse d34fe20) }
    # The historical harness uses BASELINE to select frozen prompts, not Git.
    $env:MODS_GENERATION_BASELINE = if ($group -eq 'a1') { 'original' } else { $env:MODS_GENERATION_COMMIT }
    foreach ($mode in @('normal','minimal')) {
        $env:MODS_GENERATION_MINIMAL = if ($mode -eq 'minimal') { '1' } else { '0' }
        $env:MODS_GENERATION_REPORT_DIR = Join-Path $laneRoot "raw/$group/$mode"
        New-Item -ItemType Directory -Force $env:MODS_GENERATION_REPORT_DIR | Out-Null
        foreach ($phase in @('FirstRound','Completion')) {
            $testName = if ($group -eq 'bc') { "TestCommandGenerationP5${phase}Integration" } else { "TestReadOnlyGeneration${phase}Integration" }
            $log = Join-Path $env:MODS_GENERATION_REPORT_DIR "$phase.log"
            if (Test-Path -LiteralPath $log) { throw "Refusing to overwrite $log" }
            Push-Location $source
            try {
                $started = Get-Date
                & go test -tags integration ./internal/app -run "^$testName`$" -count=1 -v -timeout=30m *> $log
                $code = $LASTEXITCODE
            } finally { Pop-Location }
            @{host=$PowerShellHost; group=$group; mode=$mode; phase=$phase; exit_code=$code; started=$started.ToString('o'); seconds=((Get-Date)-$started).TotalSeconds} |
                ConvertTo-Json -Compress | Tee-Object -FilePath (Join-Path $laneRoot 'matrix-results.jsonl') -Append
            if ($code -ne 0) { throw "Test failed; inspect $log before resuming" }
            # The harness can record provider failures without failing go test.
            # Stop subsequent batches if the fixed model is unavailable.
            if (Select-String -LiteralPath $log -Pattern 'model_not_found|Model Not Exist|invalid_api_key|Insufficient Balance|Authentication Fails' -Quiet) {
                throw 'Fixed provider/model unavailable; no model substitution permitted'
            }
        }
    }
}
