# Windows per-command UAC execution

The existing `process_run`, `powershell_run` and Windows `shell_run` tools expose
an optional `elevated` boolean on Windows. Their normal approval entry point,
effect classification, target rules, cwd checks and executable binding remain
unchanged. The review includes the requested elevation. UAC is a separate OS
authorization step after mods review, including when a saved rule or ReviewNever
skips that review. No elevation rule is persisted.

`commandRunner` selects the UAC backend only for this invocation. An already
elevated parent uses the ordinary runner. Otherwise, `ShellExecuteExW` with the
`runas` verb starts the same executable at the private `__mods_uac_exec` entry
point, before CLI, settings or database initialization. No system sudo,
scheduled task, service, password collection or temporary script is required.

Each call owns a random local named pipe. Its protected DACL allows the original
user and Administrators (to support UAC credentials for another administrator).
The broker checks the client PID against the retained ShellExecute process
handle. The helper checks the pipe server PID against the retained parent
process handle and checks the parent's executable path against its own. The
production helper additionally requires an elevated token. Neither side sends
the command before verifying its peer. The pipe library rejects remote clients
and uses anonymous impersonation level on clients. This is not a sandbox
against a compromised user, administrator or replacement mods executable.

The single request carries the resolved executable, literal argv, cwd, prepared
environment and remaining timeout. Commands and secrets are not stored in
temporary files or put in the launch arguments. The original environment is
preserved, including `secret_env` and Git authentication setup; external
credential helpers tied to the original user's account may be unavailable when
using a different administrator account. The original identity and UAC token
remain OS-managed.

Length-prefixed JSON frames are limited to 1 MiB before allocation. Stdout and
stderr stream separately in 8 KiB chunks into the existing bounded collectors.
The final frame carries exit code, duration, timeout or execution error. Process
tools keep their structured JSON result; shell tools keep combined output and
their existing nonzero `ShellExitError` behavior. A broken pipe without a final
result is an error, never a successful command.

Timeout starts after mods approval and includes UAC consent. The elevated
helper owns the ordinary command runner and its Job Object: timeout or pipe EOF
(including parent crash) cancels the command tree. Closing the call's pipe also
prevents late consent from receiving a command. Windows may leave the native
consent dialog visible after cancellation; mods does not manipulate that secure
desktop UI. No command is automatically retried. Pipe writes are deadline-bound,
and all resources and progress callbacks have per-call lifetimes.

Validation includes real separate helper processes without UAC for CI, literal
argv and cwd preservation, UTF-8 and separate output, nonzero exits, output
limits, tree cancellation, helper timeout, rejected peers, frame bounds and late
consent. App tests exercise approval-before-dispatch and unchanged target rules.
`TestUACIntegration` is an opt-in desktop test of native consent and the actual
elevated token; see `docs/development.md`.
