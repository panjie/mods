package tools

import "github.com/panjie/mods/internal/prompts"

const ToolSelectionRules = prompts.ToolSelection

const PosixShellRunDescription = "Run a shell command via sh and return stdout+stderr. Output is returned directly; do not redirect to a file just to see results. Pipe commands together for filtering, counting, or text processing (e.g. find ... | wc -l)."

const WindowsShellRunDescription = "Run a cmd.exe command via cmd /D /C and return stdout+stderr. On Windows, prefer powershell_run for most tasks — it supports pipelines, variables, filtering, and is analyzed locally for read-only safety. Only use shell_run for cmd.exe builtins that have no PowerShell equivalent or when a legacy cmd syntax is required. Output is returned directly; do not redirect to a file just to see results."

const PowerShellRunDescription = "Run a PowerShell command directly via powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -Command and return stdout+stderr. Pass only the PowerShell pipeline or script block; do not prefix with powershell, powershell.exe, pwsh, or -Command. On Windows, prefer this tool over shell_run for filesystem inspection, text processing, filtering, counting, querying, and PowerShell variables such as $PSVersionTable or $_. Use PowerShell 5.1 compatible syntax (avoid ternary operators, null-coalescing operators, pipeline chain operators && and ||, and other PowerShell 7+ only features) so commands work across both Windows PowerShell 5.1 and PowerShell 7+. Output is returned directly; do not use Out-File, Set-Content, shell redirection, or temporary .ps1 files just to see results."
