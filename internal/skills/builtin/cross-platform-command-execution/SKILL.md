---
name: cross-platform-command-execution
description: Execute, diagnose, or translate terminal commands reliably across Linux, macOS, and Windows using direct argv processes or the native shell.
---

# Cross-platform command execution

Use this procedure for command execution and command troubleshooting.

1. Prefer `process_run` for one executable. Pass every argument as a separate
   literal `args` item. This avoids shell quoting and expansion differences.
2. Use `shell_run` only for POSIX pipelines, redirection, variable expansion,
   globbing, or shell builtins. On Windows, use `powershell_run` for PowerShell
   pipelines and cmdlets.
3. Do not prepend `cd`; use `process_run.cwd`. Commands already start in the
   configured workspace when `cwd` is omitted.
4. Call `runtime_info` only when the OS, selected shell, or availability of a
   specific executable is relevant and not already known.
5. Treat paths as data. Keep paths containing spaces or Unicode in one argv
   element. In shell source, use the quoting rules of the selected dialect.
6. Prefer portable `sh` syntax on Linux and macOS. Remember that common tools
   such as `sed`, `stat`, `find`, and `xargs` may differ between GNU and BSD.
7. On Windows, stay compatible with the reported PowerShell host. Prefer native
   cmdlets over assuming POSIX utilities are installed.
8. Use the structured `process_run` result: inspect `exit_code`, `stderr`,
   `timed_out`, and truncation fields. A nonzero exit code is an outcome, not a
   tool failure.
9. When output is truncated, rerun with a narrower subcommand, filter, test
   selector, or verbosity setting instead of requesting unbounded output.
10. When a command fails, use the concrete error to make one targeted repair.
    Check command availability, cwd, syntax, permissions, and platform before
    retrying. Do not repeat an unchanged failing command.
