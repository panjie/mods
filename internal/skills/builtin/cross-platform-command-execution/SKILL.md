---
name: cross-platform-command-execution
description: Execute, install, configure, diagnose, or translate terminal commands reliably across Linux, macOS, and Windows with small reviewable tool calls, direct argv processes, and native shells. Use for terminal work, package managers, shell configuration, runtime paths, command failures, and cross-platform command translation.
---

# Cross-platform command execution

Use this procedure for command execution and troubleshooting.

1. Keep every tool call to one independently reviewable purpose. Separate
   capability discovery, path discovery, mutation, and verification. Do not
   add headings, tables, or formatting commands merely to decorate inspection
   output.
2. Prefer `process_run` for one executable, including git, tests, builds,
   package managers, and installers. Pass every argument as a separate literal
   `args` item. Do not invoke `sh`, `cmd`, `powershell`, or `pwsh` with `-c`,
   `/C`, or `-Command`; use the matching shell tool for shell source.
3. Use `shell_run` only for required POSIX pipelines, redirection, variable
   expansion, globbing, or shell builtins. On Windows, use `powershell_run`
   only for PowerShell cmdlets, object pipelines, and runtime variables. Keep a
   necessary pipeline intact when it represents one purpose.
4. Call `runtime_info` only when the OS, selected shell, or availability of a
   specific executable is relevant and not already known. Do not implement
   capability discovery with several `Get-Command` or `command -v` statements.
5. Do not prepend `cd`, `Set-Location`, or `Push-Location`; use
   the literal `cwd` argument on process/shell tools. Commands already start in the configured workspace when
   `cwd` is omitted.
6. Treat paths as data. Keep paths containing spaces or Unicode in one argv
   element. Resolve runtime paths such as `$PROFILE` in a short read-only call,
   then use the returned literal absolute path for mutation. Never invent a
   concrete path from a variable name.
7. Prefer compact structured inspection output when later calls consume it.
   For example, inspect a PowerShell profile with:

   ```powershell
   [PSCustomObject]@{ Path = $PROFILE.CurrentUserCurrentHost; Exists = Test-Path -LiteralPath $PROFILE.CurrentUserCurrentHost } | ConvertTo-Json -Compress
   ```

   Then perform profile mutation and verification as separate calls. Do not
   combine installer discovery, profile enumeration, directory creation, file
   writing, and verification in one PowerShell command. Do not change execution
   policy or unrelated system settings unless the user requests it.
8. Prefer portable `sh` syntax on Linux and macOS. Remember that common tools
   such as `sed`, `stat`, `find`, and `xargs` may differ between GNU and BSD.
   On Windows, stay compatible with the reported PowerShell host and prefer
   native cmdlets over assuming POSIX utilities are installed.
9. Inspect the structured `process_run` result: `exit_code`, `stderr`,
   `timed_out`, and truncation fields. A nonzero exit code is an outcome, not a
   tool failure. When output is truncated, narrow the command or selector.
10. Use a concrete error to make one targeted repair. Check availability, cwd,
    syntax, permissions, and platform before any further retry. If mods offers
    simplification advice, treat it as advice: keep the operation's behavior
    identical, and re-send the same command when it is already correct.

Use `http_download` when available for explicit URL/destination lists, after
creating directories with `fs_mkdir`. Use a literal `cwd` instead of chaining
location changes.

Run a script that already exists as a file exactly as written, with one
interpreter or shell host and the literal path (`bash scripts/build.sh`,
`python tools/check.py`, `pwsh -File build.ps1`). Those calls never get
simplification advice, so never split, rewrite, or inline an existing script to
make it "easier to review" — that only breaks it. Prefer putting multi-step work
in a script file over inline `-c`/`-e` code or encoded payloads, which do get
advice and always reach approval as written.
