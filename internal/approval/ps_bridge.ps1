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

$controlFlowTypes = @(
    'IfStatementAst', 'SwitchStatementAst', 'ForStatementAst',
    'ForEachStatementAst', 'WhileStatementAst', 'DoWhileStatementAst',
    'DoUntilStatementAst', 'TryStatementAst'
)

$varConstants = @('true', 'false', 'null', '_', 'psitem', 'PSItem', 'args', 'error', 'matches', 'foreach', 'home', 'pwd', 'host', 'input')

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
            if ($node.InvocationOperator -eq [System.Management.Automation.Language.TokenKind]::Ampersand) {
                $ir.has_background = $true
            }
            continue
        }

        if ($node -is [System.Management.Automation.Language.PipelineAst]) {
            if ($node.PipelineElements.Count -gt 1) {
                Add-Unique $ir.operators "|"
            }
            continue
        }

        if ($node.GetType().Name -eq 'PipelineChainAst') {
            try {
                $op = $node.Operator.ToString()
                Add-Unique $ir.operators $op
            } catch {}
            continue
        }

        if ($node -is [System.Management.Automation.Language.FileRedirectionAst]) {
            Add-Unique $ir.redirects "FileRedirection"
            continue
        }

        if ($node -is [System.Management.Automation.Language.MergingRedirectionAst]) {
            Add-Unique $ir.redirects "MergingRedirection"
            continue
        }

        if ($node -is [System.Management.Automation.Language.SubExpressionAst]) {
            Add-Unique $ir.expansions "subshell"
            continue
        }

        if ($node -is [System.Management.Automation.Language.VariableExpressionAst]) {
            $varName = $node.VariablePath.UserPath.ToLower()
            if ($varConstants -notcontains $varName) {
                Add-Unique $ir.expansions "var"
            }
            continue
        }

        if ($node -is [System.Management.Automation.Language.ScriptBlockExpressionAst]) {
            $ir.has_script_block = $true
            continue
        }

        if ($node -is [System.Management.Automation.Language.AssignmentStatementAst]) {
            $ir.has_assignment = $true
            continue
        }

        $typeName = $node.GetType().Name
        if ($controlFlowTypes -contains $typeName) {
            $ir.has_control_flow = $true
            continue
        }
    }

    try {
        $endBlock = $ast.EndBlock
        if ($endBlock -and $endBlock.Statements -and $endBlock.Statements.Count -gt 1) {
            Add-Unique $ir.operators ";"
        }
    } catch {}

    foreach ($tok in $tokens) {
        $tv = $tok.Text.ToLower()
        switch ($tv) {
            '-encodedcommand' { Add-Unique $ir.risk_flags "invoke_expression" }
            '-enc'            { Add-Unique $ir.risk_flags "invoke_expression" }
            '-en'             { Add-Unique $ir.risk_flags "invoke_expression" }
            '--%'             { $ir.has_stop_parsing = $true }
            '&&'              { Add-Unique $ir.operators "&&" }
            '||'              { Add-Unique $ir.operators "||" }
            '&'               {
                # Background operator suffix (e.g. "Get-Process &").
                # The call-operator prefix ("& 'cmd'") is already handled via
                # CommandAst.InvocationOperator above; this catches the trailing
                # background form, which the parser does not surface on CommandAst.
                $ir.has_background = $true
            }
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
