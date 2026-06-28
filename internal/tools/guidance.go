package tools

import "github.com/panjie/mods/internal/prompts"

const ToolSelectionRules = prompts.ToolSelection

const PosixShellRunDescription = "Run a shell command via sh and return stdout+stderr. Output is returned directly; do not redirect to a file just to see results. Pipe commands together for filtering, counting, or text processing (e.g. find ... | wc -l)."

const WindowsShellRunDescription = "Run a cmd.exe command via cmd /D /C and return stdout+stderr. Use this for cmd.exe builtins such as dir, type, and echo. For PowerShell pipelines, variables, filtering, counting, or querying, use powershell_run instead of nesting powershell -Command here. Output is returned directly; do not redirect to a file just to see results."

const PowerShellRunDescription = "Run a PowerShell command directly via powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -Command and return stdout+stderr. Pass only the PowerShell pipeline or script block; do not prefix with powershell, powershell.exe, pwsh, or -Command. Use this on Windows for filtering, counting, querying, and PowerShell variables such as $PSVersionTable or $_ without cmd /C quoting. Output is returned directly; do not use Out-File, Set-Content, shell redirection, or temporary .ps1 files just to see results."
