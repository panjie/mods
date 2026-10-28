# Tool Details

Mods ships its built-in tools inside the binary. Filesystem tools auto-activate
when a request needs them; shell tools are enabled by default. Run
`mods --list-tools` for the catalogue available in your build, including any MCP
tools.

Filesystem paths resolve from the process current working directory. Shell and
process tools start there by default and accept a per-call `cwd` argument. The
status line shows what Mods is doing between tool calls ("read ...",
"shell ...", "search web ..."). `--hide-tool-status` hides it and
the compact one-line record each completed call leaves in normal output.

## Command Declarations

`shell_run`, `powershell_run`, and `process_run` require `effect: read_only` or
`effect: may_write`. The declaration covers the whole call, including subprocesses,
pipelines and scripts. `may_write` includes uncertain effects and mixed reads/writes.
Missing, null, invalid or incorrectly typed values are argument errors.
Each call also requires a nonblank `purpose` explaining the operation and its
meaningful effects in the user's language. Purpose is display text and does not
affect authorization.

In `auto` and `always`, `read_only` executes immediately; `may_write` asks for a
one-call approval. Saved directory/remote rules and temporary paths do not exempt
commands. `never` disables review but does not bypass argument validation or UAC.
In `auto` and `always`, `may_write` calls without an interactive review channel
return approval unavailable.

Mods trusts the model's declaration and does not independently analyze effects.
The model can misclassify a mutation as a read. There is no command analyzer,
PowerShell parser bridge, complexity gate, automatic reorganization or script
content preview/digest. Existing scripts execute by interpreter and path.

Tool descriptions in normal and minimal mode instruct models to prefer reads
when they can correctly finish the task, generate clear reviewable commands, and
label the final call honestly. Variables, loops, .NET calls and scripts are allowed;
necessary mutations should use `may_write`, not be omitted to avoid approval.

The review displays the purpose, normalized working directory, and complete
command: quoted executable/arguments for `process_run`, or shell source with line
numbers for multi-line commands. Y approves once, N denies, and Ctrl+C/Esc
cancels the task. F selects **Full control (this session)**: approve this command
and disable subsequent tool review, including native and MCP writes, for the
current invocation and all subsequent `--chat` turns. This is an in-memory
switch to `never`, not a saved permission rule or configuration change. Restarting
Mods (including `--continue`) restores the configured review mode. Argument
validation, filesystem execution checks, and OS credential/UAC prompts still apply.
Left/Right moves the highlighted choice; Enter confirms it.
Up/Down browses long commands without disabling approval. Source is escaped and
known secrets are redacted for display. Approval uses the saved call arguments;
there is no effect reanalysis or script-content reapproval.

Native file tools and MCP tools retain their own capabilities and target policy.
An `effect` argument cannot relabel a native file write as a read. Downloads retain
structured destination review and their existing page-reading requirement.

## http_download

`http_download` accepts up to 32 URL/path pairs (16 MiB per file), stops on the
first failure, and reports completed/failed/skipped entries. It requires
existing parent directories and preserves existing files unless `overwrite` is
explicit. Downloads use the web-search private-network opt-in and never execute
content. Publishing uses a same-directory rename for overwrite or a hard link
for no-overwrite; filesystems without the required operation fail without
falling back to a partial destination write. Batch completion is not
transactional.

## Git Authentication

When Git needs an HTTP(S) username or password/token during a tool call, Mods
can prompt in the terminal and continue the same command. Existing credential
helpers, `GIT_ASKPASS`, `core.askPass`, and `SSH_ASKPASS` retain priority. Mods
installs its bridge only as the last askpass fallback; it does not change Git
configuration or add an authentication tool.

Passwords are masked and passed directly to Git through a local IPC connection,
without going through the model. Mods does not save credentials, though your
configured Git credential helper may do so. Prefer HTTPS and your existing
credential manager for persistent authentication. Windows requires Git for
Windows, including its bundled shell.

Input counts toward the command timeout (60 seconds by default). Escape cancels
authentication; timeout or task cancellation also closes the prompt. Mods does
not automatically rerun the command. Raw/minimal mode and non-interactive
sessions cannot open these prompts: configure credentials beforehand. SSH private-key unlocking, host-key confirmation, and interactive
`gh auth login` are not handled by this bridge.

## Debug Output

Use `-D` or `--debug` to write a human-readable execution trace to stderr. The
trace groups startup settings, each user turn, model rounds, tool calls,
approval decisions, results, timing, token usage, and the final turn summary.
Provider call IDs and `call N/total` labels connect every tool request to its
result, including validation failures, denied reviews, cancellations, and
non-zero command exits.

The request's `prompt bytes` line measures the rendered system text and complete
provider-neutral tool definitions, with tool descriptions and schema JSON shown
as breakdowns. Schema property descriptions count too. The total is system text
plus tool-definition JSON; do not add the breakdowns again. These byte counts
exclude conversation content and media and are not token estimates or exact
provider wire sizes. The separate request-size line includes message history.

Tool arguments are pretty-printed as JSON up to 4 KiB. Tool results are shown up
to 16 KiB. Larger payloads retain both the beginning and end and report the
number of omitted bytes. Empty results are shown explicitly as `<empty>`. Known
protected credentials are redacted before formatting or truncation, and
resolved credential values are never sent to the debug renderer. Terminal
output uses color; redirected debug output contains the same labels without ANSI
styling.

Use `--show-token-usage` (or `-s`) to print the input, output, and total token
count for an interaction to stderr without mixing it into the model response on
stdout.

## Installed skill directories

Use `mods --skills-dirs DIR` to select installed skill directories. Repeat the
flag to supply multiple directories; later directories override earlier skills
with the same name. Use `mods --skills-dirs` without a directory to print the
effective directories.

The default is `~/.agents/skills`. Portable mode also includes a `skills`
directory next to the executable. Use `mods --list-skills` to list the installed
skills from the effective directories.
